package gbind

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// root mirrors document_start and root_scalar.
func (c *binder) root(dst unsafe.Pointer) error {
	ti := c.tt.Root
	c.rootType = ti
	if c.eof() {
		return c.fail(ndec.BindErrEOF, c.pos())
	}
	ch := c.peek()
	// The root chain has no layer limit and always reuses.
	for ch != 'n' && c.typ(ti).Kind == vbind.KindPointer {
		pointee := *(*unsafe.Pointer)(dst)
		if pointee == nil {
			pointee = c.newPointee(ti)
			*(*unsafe.Pointer)(dst) = pointee
		}
		dst, ti = pointee, c.child(ti)
	}
	c.rootType = ti
	k := c.typ(ti).Kind
	switch {
	case k == vbind.KindAny:
		var err error
		c.p, err = c.bindAny(c.txt, c.p, dst, ti)
		return err
	case k == vbind.KindValue:
		return c.bindValueDoc(dst)
	case isDeferredKind(k):
		return c.deferValue(dst, ti, ti)
	}
	pos := c.pos()
	switch ch {
	case 'n':
		if err := c.atom(); err != nil {
			return err
		}
		c.nullZero(dst, ti)
		return nil
	case '{':
		c.next()
		var err error
		switch k {
		case vbind.KindMap:
			c.p, err = c.bindMap(c.txt, c.p, dst, ti)
			return err
		case vbind.KindStruct:
			c.p, err = c.bindStruct(c.txt, c.p, dst, ti)
			return err
		}
		c.record(pos, ti)
		return c.rootSkip(int(pos))
	case '[':
		c.next()
		switch k {
		case vbind.KindSlice, vbind.KindArray:
			var err error
			c.p, err = c.bindArray(c.txt, c.p, dst, ti)
			return err
		case vbind.KindStream:
			return c.bindStream(dst, ti)
		}
		c.record(pos, ti)
		return c.rootSkip(int(pos))
	case '"':
		switch {
		case k == vbind.KindString || k == vbind.KindNumber:
			p0 := c.p
			var err error
			if c.p, err = c.storeString(c.txt, c.p, dst); err != nil || k == vbind.KindString || numberText(dst) {
				return err
			}
			*(*string)(dst) = ""
			c.p = p0
			c.record(pos, ti)
			return c.rootSkip(int(pos))
		case c.isByteSlice(ti):
			return c.deferValue(dst, ti, ti)
		}
		c.record(pos, ti)
		return c.rootSkip(int(pos))
	case 't', 'f':
		if k != vbind.KindBool {
			c.record(pos, ti)
			return c.rootSkip(int(pos))
		}
		if err := c.atom(); err != nil {
			return err
		}
		*(*bool)(dst) = ch == 't'
		return nil
	}
	if ch == '-' || gdec.IsDigit(ch) {
		if k == vbind.KindNumber {
			return c.storeNumberText(dst)
		}
		if !isNumericKind(k) {
			// A token outside the number grammar is a syntax error whatever
			// the destination, ahead of the mismatch, as root_scalar's
			// number write checks it for every kind.
			if _, ok := gdec.ValidNumber(c.src, c.p); !ok {
				return c.fail(ndec.BindErrSyntax, pos)
			}
			c.record(pos, ti)
			return c.rootSkip(int(pos))
		}
		var mis bool
		var err error
		c.p, mis, err = c.numberAt(c.txt, c.p, dst, k)
		if err != nil {
			return err
		}
		if mis {
			c.record(pos, ti)
			return c.rootSkip(int(pos))
		}
		return nil
	}
	return c.fail(ndec.BindErrSyntax, pos)
}

func isNumericKind(k vbind.Kind) bool { return k >= vbind.KindInt && k <= vbind.KindFloat64 }

func isDeferredKind(k vbind.Kind) bool {
	return k == vbind.KindUnmarshaler || k == vbind.KindTextUnmarshaler ||
		k == vbind.KindRawMessage || k == vbind.KindIface
}

func (c *binder) isByteSlice(ti uint32) bool {
	return c.typ(ti).Kind == vbind.KindSlice && c.typ(c.child(ti)).Kind == vbind.KindUint8
}

// siteKind selects the per-position policy of bindValue.
type siteKind uint8

const (
	// siteField records a mismatch and skips the value, zeroes reference
	// kinds on null, and honors `,string`.
	siteField siteKind = iota
	// siteElem aborts on a mismatch and leaves the slot untouched on null.
	// Map values bind as elements into a fresh zeroed slot.
	siteElem
)

// bindValue binds the value at token start p into dst of type ti and
// returns the token start past it. ctr is the enclosing container type an
// immediate error reports against.
//
// The common bindings, a value of the kind's own JSON type, dispatch
// first with the cursor in registers; bindSlow keeps the full order of
// checks for the rest over c.p.
func (c *binder) bindValue(s text, p int, dst unsafe.Pointer, ti, ctr uint32, site siteKind, quoted bool) (int, error) {
	ch := s.peek(p)
	if !quoted {
		switch k := c.typ(ti).Kind; k {
		case vbind.KindString:
			if ch == '"' {
				return c.storeString(s, p, dst)
			}
		case vbind.KindInt, vbind.KindInt8, vbind.KindInt16, vbind.KindInt32, vbind.KindInt64,
			vbind.KindUint, vbind.KindUint8, vbind.KindUint16, vbind.KindUint32, vbind.KindUint64,
			vbind.KindFloat32, vbind.KindFloat64:
			if ch == '-' || gdec.IsDigit(ch) {
				next, mis, err := c.numberAt(s, p, dst, k)
				if err == nil && mis {
					c.p = p
					err = c.typeMismatch(ti, site)
					next = c.p
				}
				return next, err
			}
		case vbind.KindBool:
			if ch == 't' || ch == 'f' {
				end, ok := s.atomEnd(p, ch)
				if !ok {
					return p, c.fail(ndec.BindErrSyntax, uint64(p))
				}
				*(*bool)(dst) = ch == 't'
				return s.skip(end), nil
			}
		case vbind.KindStruct:
			if ch == '{' {
				return c.bindStruct(s, s.skip(p+1), dst, ti)
			}
		case vbind.KindSlice, vbind.KindArray:
			if ch == '[' {
				return c.bindArray(s, s.skip(p+1), dst, ti)
			}
		case vbind.KindMap:
			if ch == '{' {
				return c.bindMap(s, s.skip(p+1), dst, ti)
			}
		case vbind.KindAny:
			return c.bindAny(s, p, dst, ctr)
		case vbind.KindPointer:
			if ch != 'n' {
				var err error
				if dst, ti, err = c.resolvePtrChain(dst, ti); err != nil {
					return p, err
				}
				return c.bindValue(s, p, dst, ti, ctr, site, false)
			}
		}
	}
	c.p = p
	err := c.bindSlow(dst, ti, ctr, site, quoted, ch)
	return c.p, err
}

// bindSlow is bindValue in full: pointers, null, hooks, `,string`, and
// mismatches.
func (c *binder) bindSlow(dst unsafe.Pointer, ti, ctr uint32, site siteKind, quoted bool, ch byte) error {
	if ch != 'n' && c.typ(ti).Kind == vbind.KindPointer {
		var err error
		if dst, ti, err = c.resolvePtrChain(dst, ti); err != nil {
			return err
		}
	}
	k := c.typ(ti).Kind
	switch {
	case k == vbind.KindAny:
		var err error
		c.p, err = c.bindAny(c.txt, c.p, dst, ctr)
		return err
	case k == vbind.KindValue:
		return c.bindValueDoc(dst)
	case isDeferredKind(k):
		return c.deferValue(dst, ti, ctr)
	}
	if ch == 'n' {
		if err := c.atom(); err != nil {
			return err
		}
		if site == siteField {
			c.nullZero(dst, ti)
		}
		return nil
	}
	if quoted {
		return c.bindQuoted(dst, ti, k, ctr)
	}
	s := c.txt
	var err error
	switch k {
	case vbind.KindString:
		if ch == '"' {
			c.p, err = c.storeString(s, c.p, dst)
			return err
		}
	case vbind.KindNumber:
		if ch == '"' {
			p0 := c.p
			if c.p, err = c.storeString(s, c.p, dst); err != nil || numberText(dst) {
				return err
			}
			// A json.Number holds number text only, as encoding/json
			// requires: any other string is a value it cannot hold.
			*(*string)(dst) = ""
			c.p = p0
			return c.typeMismatch(ti, site)
		}
		if ch == '-' || gdec.IsDigit(ch) {
			return c.storeNumberText(dst)
		}
	case vbind.KindInt, vbind.KindInt8, vbind.KindInt16, vbind.KindInt32, vbind.KindInt64,
		vbind.KindUint, vbind.KindUint8, vbind.KindUint16, vbind.KindUint32, vbind.KindUint64,
		vbind.KindFloat32, vbind.KindFloat64:
		if ch == '-' || gdec.IsDigit(ch) {
			var mis bool
			if c.p, mis, err = c.numberAt(s, c.p, dst, k); err != nil || !mis {
				return err
			}
		}
	case vbind.KindBool:
		if ch == 't' || ch == 'f' {
			if err = c.atom(); err != nil {
				return err
			}
			*(*bool)(dst) = ch == 't'
			return nil
		}
	case vbind.KindStruct:
		if ch == '{' {
			c.p, err = c.bindStruct(s, s.skip(c.p+1), dst, ti)
			return err
		}
	case vbind.KindMap:
		if ch == '{' {
			c.p, err = c.bindMap(s, s.skip(c.p+1), dst, ti)
			return err
		}
	case vbind.KindSlice:
		if ch == '"' && c.isByteSlice(ti) {
			return c.deferValue(dst, ti, ctr)
		}
		fallthrough
	case vbind.KindArray:
		if ch == '[' {
			c.p, err = c.bindArray(s, s.skip(c.p+1), dst, ti)
			return err
		}
	case vbind.KindStream:
		if ch == '[' {
			c.next()
			return c.bindStream(dst, ti)
		}
	}
	return c.typeMismatch(ti, site)
}

// numberText reports whether the json.Number at dst holds one JSON number.
func numberText(dst unsafe.Pointer) bool {
	s := *(*string)(dst)
	end, ok := gdec.ValidNumber(unsafe.Slice(unsafe.StringData(s), len(s)), 0)
	return ok && end == len(s)
}

// typeMismatch handles a value at the cursor its destination cannot hold: a
// field records it and skips the value, anything else aborts. Either way
// the error reports ti, the leaf destination that rejected the value.
func (c *binder) typeMismatch(ti uint32, site siteKind) error {
	if site == siteField {
		c.record(c.pos(), ti)
		return c.skipValue()
	}
	// A token the grammar rejects is malformed input, not a value the
	// destination rejected. The field site's skip surfaces the same check;
	// an element abort must classify before it reports. A container's
	// contents stay unread, as the abort leaves them.
	if ch := c.peek(); !c.eof() && ch != '{' && ch != '[' {
		if _, err := c.scalarAt(c.txt, c.p); err != nil {
			return err
		}
	}
	return c.failValueOrEOF(ndec.BindErrTypeMismatch, ti)
}

// bindQuoted binds a `,string` value at the cursor.
func (c *binder) bindQuoted(dst unsafe.Pointer, ti uint32, k vbind.Kind, ctr uint32) error {
	pos := c.pos()
	if ch := c.peek(); ch != '"' {
		// The abort classifies its token as an element abort does.
		if !c.eof() && ch != '{' && ch != '[' {
			if _, err := c.scalarAt(c.txt, c.p); err != nil {
				return err
			}
		}
		return c.failType(ndec.BindErrTypeMismatch, pos, ti)
	}
	s, err := c.string(false)
	if err != nil {
		return err
	}
	if !c.writeQuotedScalar(dst, k, s) {
		return c.failType(ndec.BindErrTypeMismatch, pos, ctr)
	}
	return nil
}

// atom validates and consumes a true, false, or null literal.
func (c *binder) atom() error {
	end, ok := c.txt.atomEnd(c.p, c.peek())
	if !ok {
		return c.fail(ndec.BindErrSyntax, c.pos())
	}
	c.p = c.txt.skip(end)
	return nil
}

// nullZero mirrors BIND_NULL_ZERO: null clears reference-like storage and
// Number, and leaves scalars and structs untouched.
func (c *binder) nullZero(dst unsafe.Pointer, ti uint32) {
	switch c.typ(ti).Kind {
	case vbind.KindPointer, vbind.KindSlice, vbind.KindStream, vbind.KindMap, vbind.KindAny, vbind.KindNumber:
		gort.MemclrHasPointers(dst, c.size(ti))
	}
}

// resolvePtrChain walks a field, element, or map value's pointer layers,
// keeping each existing pointee and allocating each nil one.
func (c *binder) resolvePtrChain(dst unsafe.Pointer, ti uint32) (unsafe.Pointer, uint32, error) {
	for layers := 0; c.typ(ti).Kind == vbind.KindPointer; {
		if layers++; layers > maxPtrChain {
			return nil, 0, c.failNoPos(ndec.BindErrDepth)
		}
		pointee := *(*unsafe.Pointer)(dst)
		if pointee == nil {
			pointee = c.newPointee(ti)
			*(*unsafe.Pointer)(dst) = pointee
		}
		dst = pointee
		ti = c.child(ti)
	}
	return dst, ti, nil
}

// bindStruct binds an object from token start p past its '{'. Fields merge
// into dst.
func (c *binder) bindStruct(s text, p int, dst unsafe.Pointer, ti uint32) (int, error) {
	if c.pl.polyHost[ti] {
		c.p = p
		err := c.bindPolyStruct(dst, ti)
		return c.p, err
	}
	if s.peek(p) == '}' {
		return s.skip(p + 1), nil
	}
	if err := c.push(); err != nil {
		return p, err
	}
	kt := c.pl.keys[ti]
	next := 0 // the field a member in declaration order binds next
	for {
		kpos := p
		if s.peek(p) != '"' {
			return p, c.fail(ndec.BindErrSyntax, uint64(kpos))
		}
		idx, end, ok := c.keyAt(kt, s, p, next)
		if ok {
			p = end + 1
		} else {
			c.p = p
			key, _, err := c.str(false)
			if err != nil {
				return p, err
			}
			idx, p = c.matchString(kt, unsafe.String(unsafe.SliceData(key), len(key)), next), c.p
		}
		if idx >= 0 {
			next = idx + 1
		} else if c.opt&ndec.BindOptDisallowUnknown != 0 {
			return p, c.failType(ndec.BindErrUnknownField, uint64(kpos), ti)
		}
		// SRC_EXPECT(':'), whose failure names no position.
		if p < s.n && s.at(p) == ':' {
			p = s.skip(p + 1)
		} else if p = s.skip(p); s.peek(p) == ':' {
			p = s.skip(p + 1)
		} else {
			return p, c.fail(ndec.BindErrSyntax, 0)
		}
		var err error
		switch {
		case idx < 0:
			p, err = c.skipAt(s, p)
		case kt.fields[idx].plain:
			fp := &kt.fields[idx]
			if fp.kind == vbind.KindString && s.peek(p) == '"' {
				p, err = c.storeString(s, p, unsafe.Add(dst, fp.off))
			} else {
				p, err = c.bindValue(s, p, unsafe.Add(dst, fp.off), fp.ti, ti, siteField, false)
			}
		default:
			c.p = p
			err = c.bindField(dst, ti, kt.fields[idx].f)
			p = c.p
		}
		if err != nil {
			return p, err
		}
		switch s.peek(p) {
		case ',':
			p = s.skip(p + 1)
		case '}':
			c.pop()
			return s.skip(p + 1), nil
		default:
			c.p = p
			return p, c.closeErr()
		}
	}
}

// bindField binds the value at the cursor into host field f, crossing its
// embedded pointers and reading `,string`.
func (c *binder) bindField(host unsafe.Pointer, hostTi uint32, f *vbind.BindField) error {
	base := host
	if vbind.FieldViaPtr(f) {
		base = c.resolveHops(host, hostTi, f)
	}
	ti := f.FieldTypeIndex(&c.tt.Types[0])
	var err error
	c.p, err = c.bindValue(c.txt, c.p, unsafe.Add(base, f.Offset), ti, hostTi, siteField, f.Flags&uint32(vbind.TagQuoted) != 0)
	return err
}

// resolveHops crosses a promoted field's embedded pointers, allocating each
// nil one, even for a null value: null belongs to the field, not its host.
func (c *binder) resolveHops(host unsafe.Pointer, hostTi uint32, f *vbind.BindField) unsafe.Pointer {
	hops := c.tt.TypeMeta[hostTi].StructMeta().PtrHops
	for i := uintptr(vbind.FieldHopStart(f)); ; i++ {
		h := (*vbind.BindPtrHop)(unsafe.Add(hops, i*unsafe.Sizeof(vbind.BindPtrHop{})))
		slot := (*unsafe.Pointer)(unsafe.Add(host, h.SlotOffset))
		if *slot == nil {
			*slot = c.carve(h.SlotClass())
		}
		host = *slot
		if h.IsLast() {
			return host
		}
	}
}

// bindArray binds an array from token start p past its '['. A slice reuses
// its backing and grows past its capacity; its final capacity equals its
// length. A fixed array binds in place and skips elements past its length.
func (c *binder) bindArray(s text, p int, dst unsafe.Pointer, ti uint32) (int, error) {
	if s.peek(p) == ']' {
		// Native pushes an empty array's frame too.
		c.descents++
		c.emptyArray(dst, ti)
		return s.skip(p + 1), nil
	}
	if err := c.push(); err != nil {
		return p, err
	}
	return c.arrayElems(s, p, dst, ti)
}

// emptyArray binds `[]`: a slice takes the shared empty backing, a fixed
// array keeps its contents.
func (c *binder) emptyArray(dst unsafe.Pointer, ti uint32) {
	if c.typ(ti).Kind == vbind.KindSlice {
		sm := c.tt.TypeMeta[ti].SliceMeta()
		*(*gort.SliceHeader)(dst) = gort.SliceHeader{Data: *(*unsafe.Pointer)(unsafe.Pointer(&sm.EmptySliceData))}
	}
}

// arrayElems binds the elements of a non-empty array whose frame is pushed,
// through its ']'.
func (c *binder) arrayElems(s text, p int, dst unsafe.Pointer, ti uint32) (int, error) {
	if c.typ(ti).Kind != vbind.KindSlice {
		return c.fixedElems(s, p, dst, ti)
	}
	elemTi := c.child(ti)
	esz := uintptr(c.typ(ti).Slice().ChildSize)
	sc := &c.a.Slots[c.tt.TypeMeta[ti].SliceMeta().AllocClass]
	hdr := (*gort.SliceHeader)(dst)
	if hdr.Data == nil {
		c.a.OpenSlice(sc, hdr)
	}
	hdr.Len = 0
	count := 0
	for {
		if count == hdr.Cap {
			c.growSlice(sc, hdr, esz)
		}
		count++
		d0 := c.descents
		var err error
		p, err = c.bindValue(s, p, unsafe.Add(hdr.Data, uintptr(count-1)*esz), elemTi, ti, siteElem, false)
		if err == nil {
			switch s.peek(p) {
			case ',':
				p = s.skip(p + 1)
				hdr.Len = count
				continue
			case ']':
				hdr.Len, hdr.Cap = count, count
				c.a.CloseSlice(sc, hdr.Data, count)
				c.pop()
				return s.skip(p + 1), nil
			default:
				c.p = p
				err = c.closeErr()
			}
		}
		// An element that entered a container counts in the length, as the
		// native push of its frame published it. The elements bound so far
		// stay charged; the rest of the tail returns to the class.
		if c.descents != d0 {
			hdr.Len = count
		}
		c.a.CloseSlice(sc, hdr.Data, count)
		return p, err
	}
}

// fixedElems binds a fixed array's elements in place, skipping those past
// its length.
func (c *binder) fixedElems(s text, p int, dst unsafe.Pointer, ti uint32) (int, error) {
	elemTi := c.child(ti)
	esz := uintptr(c.typ(ti).Array().ChildSize)
	arrLen := int(c.tt.TypeMeta[ti].ArrayMeta().ArrayLen)
	for count := 0; ; {
		var err error
		if count >= arrLen {
			p, err = c.skipAt(s, p)
		} else {
			count++
			p, err = c.bindValue(s, p, unsafe.Add(dst, uintptr(count-1)*esz), elemTi, ti, siteElem, false)
		}
		if err != nil {
			return p, err
		}
		switch s.peek(p) {
		case ',':
			p = s.skip(p + 1)
		case ']':
			c.pop()
			return s.skip(p + 1), nil
		default:
			c.p = p
			return p, c.closeErr()
		}
	}
}

// growSlice moves the full slice hdr to a larger backing of its class.
// Staged hook targets move with their elements, so hook timing stays
// independent of allocation.
func (c *binder) growSlice(sc *vbind.SlotClass, hdr *gort.SliceHeader, esz uintptr) {
	old, n := hdr.Data, hdr.Len
	c.a.GrowSlice(sc, hdr)
	if n > 0 {
		c.rebase(old, uintptr(n)*esz, hdr.Data)
	}
}

// bindMap binds an object from token start p past its '{' into a new map.
func (c *binder) bindMap(s text, p int, dst unsafe.Pointer, ti uint32) (int, error) {
	m := c.openMap(dst, ti)
	if s.peek(p) == '}' {
		// Native pushes an empty map's frame too.
		c.descents++
		return s.skip(p + 1), nil
	}
	if err := c.push(); err != nil {
		return p, err
	}
	return c.mapEntries(s, p, m, ti)
}

// openMap stores at dst the prewired empty map its slot class carves.
func (c *binder) openMap(dst unsafe.Pointer, ti uint32) unsafe.Pointer {
	m := *(*unsafe.Pointer)(c.carve(c.typ(ti).Map().AllocClass))
	*(*unsafe.Pointer)(dst) = m
	return m
}

// mapPlan is how a map type's entries stage. An entry binds into a region
// slot, {key string; value V}, and a full or closing region assigns its
// complete entries in order, the map presized for them, as the native
// drain does. The in-progress entry of a failed walk is never assigned.
type mapPlan struct {
	info   *vbind.MapDrainInfo
	vrt    unsafe.Pointer // value runtime type
	ert    unsafe.Pointer // region slot runtime type
	stride uintptr
	valOff uintptr
	list   int // index of the region free list
}

// newMapPlan plans the map type ti. A value holding deferred targets binds
// into a slot of its own class instead, so no hook target lies in a region.
func (pl *Plan) newMapPlan(ti uint32) *mapPlan {
	tt := pl.tt
	info := (*vbind.MapDrainInfo)(tt.TypeMeta[ti].MapMeta().DrainInfo)
	valTi := tt.Types[ti].ChildIndex(&tt.Types[0])
	mp := &mapPlan{info: info, vrt: pl.rtypes[valTi]}
	if info.ValIsDeferred {
		return mp
	}
	et := reflect.StructOf([]reflect.StructField{
		{Name: "K", Type: reflect.TypeFor[string]()},
		{Name: "V", Type: tt.ReflectTypes[valTi]},
	})
	mp.ert, mp.stride, mp.valOff = gort.TypePtr(et), et.Size(), et.Field(1).Offset
	mp.list = pl.nmaps
	pl.nmaps++
	return mp
}

// regionSlots is the entry count of a region, the native region's.
const regionSlots = vbind.RegionSlotsPerMap

// openRegion takes a zeroed region from the free list of mp. Regions of
// one map type nest under recursion, so each open map holds its own.
func (c *binder) openRegion(mp *mapPlan) unsafe.Pointer {
	free := c.regions[mp.list]
	if n := len(free); n > 0 {
		r := free[n-1]
		c.regions[mp.list] = free[:n-1]
		return r
	}
	return gort.UnsafeNewArray(mp.ert, regionSlots)
}

// closeRegion assigns the first n entries of r and returns r, its first
// used slots cleared, to the free list.
func (c *binder) closeRegion(mp *mapPlan, m, r unsafe.Pointer, n, used int) {
	c.drainRegion(mp, m, r, n)
	gort.MemclrHasPointers(r, uintptr(used)*mp.stride)
	c.regions[mp.list] = append(c.regions[mp.list], r)
}

// drainRegion assigns the first n entries of r in order. A key that fails
// conversion gets no entry.
func (c *binder) drainRegion(mp *mapPlan, m, r unsafe.Pointer, n int) {
	gort.MapPresize(mp.info.MapRType, n, m)
	for i := range n {
		e := unsafe.Add(r, uintptr(i)*mp.stride)
		if slot := c.mapSlot(mp.info, m, *(*string)(e)); slot != nil {
			gort.TypedMemmove(mp.vrt, slot, unsafe.Add(e, mp.valOff))
		}
	}
}

// mapEntries binds the members of a non-empty object whose frame is pushed
// into map m, through its '}'.
func (c *binder) mapEntries(s text, p int, m unsafe.Pointer, ti uint32) (int, error) {
	mp := c.pl.maps[ti]
	if mp.info.ValIsDeferred {
		return c.deferredEntries(s, p, m, ti, mp)
	}
	valTi := c.child(ti)
	r := c.openRegion(mp)
	n := 0
	for {
		if s.peek(p) != '"' {
			c.closeRegion(mp, m, r, n, n)
			return p, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		key, q, err := c.strAt(s, p)
		if err != nil {
			c.closeRegion(mp, m, r, n, n)
			return q, err
		}
		if p = q; s.peek(p) != ':' {
			c.closeRegion(mp, m, r, n, n)
			return p, c.fail(ndec.BindErrSyntax, 0)
		}
		p = s.skip(p + 1)
		e := unsafe.Add(r, uintptr(n)*mp.stride)
		*(*string)(e) = key
		if p, err = c.bindValue(s, p, unsafe.Add(e, mp.valOff), valTi, ti, siteElem, false); err != nil {
			c.closeRegion(mp, m, r, n, n+1)
			return p, err
		}
		n++
		switch s.peek(p) {
		case ',':
			p = s.skip(p + 1)
			if n == regionSlots {
				c.closeRegion(mp, m, r, n, n)
				r, n = c.openRegion(mp), 0
			}
		case '}':
			c.closeRegion(mp, m, r, n, n)
			c.pop()
			return s.skip(p + 1), nil
		default:
			c.closeRegion(mp, m, r, n, n)
			c.p = p
			return p, c.closeErr()
		}
	}
}

// deferredEntries is mapEntries for a value holding deferred targets: each
// binds into a slot of its own class and publishes after the hooks run.
func (c *binder) deferredEntries(s text, p int, m unsafe.Pointer, ti uint32, mp *mapPlan) (int, error) {
	valTi := c.child(ti)
	for {
		if s.peek(p) != '"' {
			return p, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		key, q, err := c.strAt(s, p)
		if err != nil {
			return q, err
		}
		if p = q; s.peek(p) != ':' {
			return p, c.fail(ndec.BindErrSyntax, 0)
		}
		p = s.skip(p + 1)
		val := c.carve(mp.info.ValSlotClass)
		c.pending = append(c.pending, mapAssign{info: mp.info, m: m, key: key, val: val, vrt: mp.vrt})
		if p, err = c.bindValue(s, p, val, valTi, ti, siteElem, false); err != nil {
			return p, err
		}
		switch s.peek(p) {
		case ',':
			p = s.skip(p + 1)
		case '}':
			c.pop()
			return s.skip(p + 1), nil
		default:
			c.p = p
			return p, c.closeErr()
		}
	}
}

// mapSlot assigns key, converting an integer key as the native drain does,
// and returns its value slot. A key that fails conversion gets no entry,
// and the first failure is recorded.
func (c *binder) mapSlot(info *vbind.MapDrainInfo, m unsafe.Pointer, key string) unsafe.Pointer {
	switch {
	case info.KeyKind != vbind.KindString:
		if err := info.EncodeIntKey(&c.intKey, key); err != nil {
			if c.keyErr == nil {
				c.keyErr = err
			}
			return nil
		}
		return gort.MapAssign(info.MapRType, m, unsafe.Pointer(&c.intKey[0]))
	case info.ValIndirect:
		c.strKey = key
		slot := gort.MapAssign(info.MapRType, m, unsafe.Pointer(&c.strKey))
		c.strKey = ""
		return slot
	}
	return gort.MapAssignFastStr(info.MapRType, m, key)
}

// publishMapEntry assigns a value bound in its own slot.
func (c *binder) publishMapEntry(info *vbind.MapDrainInfo, m unsafe.Pointer, key string, val, vrt unsafe.Pointer) {
	if slot := c.mapSlot(info, m, key); slot != nil {
		gort.TypedMemmove(vrt, slot, val)
	}
}

// hasPointers reports whether values of t hold pointers the GC scans.
func hasPointers(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.String, reflect.Slice, reflect.Map, reflect.Chan,
		reflect.Func, reflect.Interface:
		return true
	case reflect.Array:
		return t.Len() > 0 && hasPointers(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if hasPointers(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}
