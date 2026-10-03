package gbind

import (
	"bytes"
	"unsafe"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
	"github.com/velox-io/json/vopt"
)

// Poly hosts: structs with an inline variant, a reserve-unknown Value, or a
// variant or kindof field. Phase 1 walks the object like any struct, but a
// key whose value depends on unresolved state becomes an entry: a host miss,
// a reserve-unknown or inline-variant carrier, the inline discriminator, and
// a variant whose discriminator is not yet bound. Phase 2 at close
// classifies the entries in the native order (deferred poly, case content,
// discriminator, reserve-unknown, case sink, disallow, drop) and binds them
// by seeking the cursor back to each value, so no intermediate tape exists.

// polyEntry is one deferred object member: its key and the raw key and
// value spans, which serve re-serialization and, at vpos, the cursor seek
// that binds the value.
type polyEntry struct {
	key        string
	kpos, kend int
	vpos, vend int
}

// polyHost is one poly struct instance being bound.
type polyHost struct {
	dst     unsafe.Pointer
	ti      uint32
	entries []polyEntry
	// bound lists the discriminator offsets this object wrote, the proof a
	// discriminator came from this parse rather than stale destination data.
	bound []uint32
}

func (h *polyHost) discBound(off uint32) bool {
	for _, o := range h.bound {
		if o == off {
			return true
		}
	}
	return false
}

// bindPolyStruct binds a poly host object after its '{'.
func (c *binder) bindPolyStruct(dst unsafe.Pointer, ti uint32) error {
	h := &polyHost{dst: dst, ti: ti}
	empty := c.accept('}')
	if err := c.push(); err != nil {
		return err
	}
	for !empty {
		kpos := c.p
		if c.peek() != '"' {
			return c.fail(ndec.BindErrSyntax, c.pos())
		}
		key, err := c.string(false)
		if err != nil {
			return err
		}
		if err = c.expectColon(); err != nil {
			return err
		}
		if err = c.hostMember(h, c.hostField(ti, key), key, kpos); err != nil {
			return err
		}
		switch c.peek() {
		case ',':
			c.next()
		case '}':
			c.next()
			empty = true
		default:
			return c.closeErr()
		}
	}
	err := c.polyClose(h)
	c.pop()
	return err
}

// hostMember binds or defers the member whose value is at the cursor.
func (c *binder) hostMember(h *polyHost, f *vbind.BindField, key string, kpos int) error {
	if f == nil {
		if collectsEntries(c.tt.TypeMeta[h.ti].StructMeta()) {
			return c.deferEntry(h, key, kpos)
		}
		if c.opt&ndec.BindOptDisallowUnknown != 0 {
			return c.failType(ndec.BindErrUnknownField, uint64(kpos), h.ti)
		}
		return c.skipValue()
	}
	if f.Flags&uint32(vbind.TagReserveUnknown|vbind.TagInlineVariant|vbind.TagInlineVDisc) != 0 {
		return c.deferEntry(h, key, kpos)
	}
	if f.Flags&uint32(vbind.TagVariant|vbind.TagKindof) != 0 {
		return c.polyField(h, f, key, kpos)
	}
	if vbind.FieldIsDiscriminator(f) && c.peek() == '"' {
		h.bound = append(h.bound, f.Offset)
	}
	return c.bindField(h.dst, h.ti, f)
}

// deferEntry records the member at the cursor and consumes its value. The
// stored key is read after the walk, and the transient decode of an escaped
// key views the scratch buffer the next transient decode overwrites, so an
// escaped key moves into the string arena before it is stored.
func (c *binder) deferEntry(h *polyHost, key string, kpos int) error {
	// The key decoded before its value, so it closes.
	kend, _ := gdec.StringEnd(c.src, kpos+1)
	if bytes.IndexByte(c.src[kpos+1:kend], '\\') >= 0 {
		b := c.alloc(len(key))
		copy(b, key)
		key = unsafe.String(&b[0], len(b))
	}
	e := polyEntry{key: key, kpos: kpos, kend: kend + 1, vpos: c.p}
	if err := c.validValue(); err != nil {
		return err
	}
	e.vend = e.vpos + len(gdec.TrimTrailingWS(c.src[e.vpos:c.p]))
	h.entries = append(h.entries, e)
	return nil
}

// polyField binds a variant or kindof member now when its case is known,
// and defers it otherwise.
func (c *binder) polyField(h *polyHost, f *vbind.BindField, key string, kpos int) error {
	target := unsafe.Add(h.dst, f.Offset)
	ch := c.peek()
	if ch == 'n' {
		if err := c.atom(); err != nil {
			return err
		}
		*(*[2]unsafe.Pointer)(target) = [2]unsafe.Pointer{}
		return nil
	}
	polyIdx := vbind.FieldPolyIdx(f)
	var pc polyCase
	if vbind.FieldHasKindof(f) {
		kind := jsonKindOf(ch)
		if kind < 0 {
			return c.fail(ndec.BindErrSyntax, c.pos())
		}
		if pc = c.caseByKindof(polyIdx, kind); !pc.ok {
			c.info = Error{Kind: ndec.BindErrKindofUnregistered, Detail: uint32(kind), Pos: c.pos(), TypeIdx: int(h.ti)}
			return errAbort
		}
	} else if pc, _ = c.caseByDisc(polyIdx, h); !pc.ok {
		return c.deferEntry(h, key, kpos)
	}
	if c.typ(pc.ti).Kind != vbind.KindAny && c.coldCase(pc) {
		return c.deferEntry(h, key, kpos)
	}
	err := c.bindCase(target, h, pc)
	if err != nil && c.tape {
		c.rewriteCaseMismatch(pc)
	}
	return err
}

// polyCase is a selected case. ok is false when selection failed.
type polyCase struct {
	ok    bool
	ti    uint32
	rtype unsafe.Pointer
	slot  int32 // slot class of the case's storage
}

func (c *binder) polyCaseAt(polyIdx uint16, ci int) polyCase {
	pt := &c.tt.Polys[polyIdx]
	return polyCase{ok: true, ti: uint32(pt.CaseTypeIdx(ci)), rtype: pt.CaseRType(ci), slot: pt.CaseSlotClass(ci)}
}

// jsonKindOf maps a value's first byte to the kindof index: bool, number,
// string, array, object.
func jsonKindOf(ch byte) int {
	switch {
	case ch == 't' || ch == 'f':
		return 0
	case ch == '-' || gdec.IsDigit(ch):
		return 1
	case ch == '"':
		return 2
	case ch == '[':
		return 3
	case ch == '{':
		return 4
	}
	return -1
}

func (c *binder) caseByKindof(polyIdx uint16, kind int) polyCase {
	if c.tt.Polys[polyIdx].CaseRType(kind) == nil {
		return polyCase{}
	}
	return c.polyCaseAt(polyIdx, kind)
}

// caseByDisc selects from the host's discriminator. Only a value this
// object bound may select; bound reports that one was present, which tells
// an unknown discriminator from a missing one.
func (c *binder) caseByDisc(polyIdx uint16, h *polyHost) (pc polyCase, bound bool) {
	pt := &c.tt.Polys[polyIdx]
	if !h.discBound(pt.DiscFieldOff) {
		return polyCase{}, false
	}
	s := *(*string)(unsafe.Add(h.dst, pt.DiscFieldOff))
	ci := -1
	if len(s) > 0 && len(s) <= 63 && pt.Lookup != nil {
		ci = vbind.LookupFind(pt.Lookup, s)
	}
	if ci < 0 {
		if pt.DefaultCaseIdx == 0xFFFF {
			return polyCase{}, true
		}
		ci = int(pt.DefaultCaseIdx)
	}
	return c.polyCaseAt(polyIdx, ci), true
}

// coldCase reports a case the native tape walker cannot construct.
func (c *binder) coldCase(pc polyCase) bool {
	ct := c.typ(pc.ti)
	return ct.IsCold() && ct.Kind != vbind.KindPointer && ct.Kind != vbind.KindValue
}

// bindCase publishes the case's interface word pair at target and binds the
// value at the cursor into it. Pointer and map cases occupy the data word
// directly; other kinds bind into fresh storage the data word points to.
func (c *binder) bindCase(target unsafe.Pointer, h *polyHost, pc polyCase) error {
	switch c.typ(pc.ti).Kind {
	case vbind.KindAny:
		var err error
		c.p, err = c.bindAny(c.txt, c.p, target, h.ti)
		return err
	case vbind.KindPointer, vbind.KindMap:
		var word unsafe.Pointer
		*(*[2]unsafe.Pointer)(target) = [2]unsafe.Pointer{pc.rtype, nil}
		var err error
		c.p, err = c.bindValue(c.txt, c.p, unsafe.Pointer(&word), pc.ti, h.ti, siteField, false)
		*(*[2]unsafe.Pointer)(target) = [2]unsafe.Pointer{pc.rtype, word}
		return err
	}
	slot := c.carve(pc.slot)
	*(*[2]unsafe.Pointer)(target) = [2]unsafe.Pointer{pc.rtype, slot}
	var err error
	c.p, err = c.bindValue(c.txt, c.p, slot, pc.ti, h.ti, siteField, false)
	return err
}

func (c *binder) phase2Err(kind uint32, detail uint32, h *polyHost) error {
	c.info = Error{Kind: kind, Detail: detail, Pos: NoPos, TypeIdx: int(h.ti), Target: h.dst}
	return errAbort
}

// polyClose is phase 2: bind the inline discriminator, classify entries,
// publish reserve-unknown, then bind the inline case.
func (c *binder) polyClose(h *polyHost) error {
	sm := c.tt.TypeMeta[h.ti].StructMeta()
	iv, ru := sm.InlineVariantIdx, sm.ReserveUnknownFieldOff

	var hc polyCase
	caseSink := false
	if iv != 0xFFFF {
		seen := false
		for i := range h.entries {
			e := &h.entries[i]
			f := c.hostField(h.ti, e.key)
			if f == nil || !vbind.FieldIsDiscriminator(f) || vbind.FieldPolyIdx(f) != iv {
				continue
			}
			if err := c.bindInlineDisc(h, f, e, iv); err != nil {
				return err
			}
			seen = true
			break
		}
		var bound bool
		hc, bound = c.caseByDisc(iv, h)
		if !hc.ok && seen {
			return c.discErr(iv, h, bound)
		}
		if hc.ok && c.typ(hc.ti).Kind == vbind.KindStruct {
			caseSink = c.tt.TypeMeta[hc.ti].StructMeta().ReserveUnknownFieldOff != 0xFFFFFFFF
		}
	}
	declares := func(key string) bool {
		if c.typ(hc.ti).Kind != vbind.KindStruct {
			return true
		}
		return vbind.LookupFind(c.pl.keys[hc.ti].blob, key) >= 0
	}

	var viewA, viewB []polyEntry
	for i := range h.entries {
		e := h.entries[i]
		f := c.hostField(h.ti, e.key)
		switch {
		case f != nil && f.Flags&uint32(vbind.TagVariant|vbind.TagKindof) != 0:
			if err := c.seek(e.vpos, func() error { return c.phase2Poly(h, f) }); err != nil {
				return err
			}
		case hc.ok && declares(e.key):
			viewA = append(viewA, e)
		case f != nil && vbind.FieldIsDiscriminator(f):
		case ru != 0xFFFFFFFF:
			if iv == 0xFFFF {
				viewA = append(viewA, e)
			} else {
				viewB = append(viewB, e)
			}
		case caseSink:
			viewA = append(viewA, e)
		case c.opt&ndec.BindOptDisallowUnknown != 0:
			return c.phase2Err(ndec.BindErrUnknownField, 0, h)
		}
	}

	if ru != 0xFFFFFFFF {
		sink := viewA
		if iv != 0xFFFF {
			sink = viewB
		}
		if err := c.storeObjectValue(unsafe.Add(h.dst, ru), sink); err != nil {
			return err
		}
	}
	if !hc.ok {
		return nil
	}
	target := c.inlineVariantTarget(h)
	if target == nil {
		return nil
	}
	if c.coldCase(hc) {
		return c.phase2Err(ndec.BindErrVariantColdCase, uint32(iv), h)
	}
	return c.bindInlineCase(target, hc, viewA)
}

// inlineVariantTarget is the address of the host's inline variant field.
func (c *binder) inlineVariantTarget(h *polyHost) unsafe.Pointer {
	bt := c.typ(h.ti)
	first := bt.StructFirstFieldIndex(&c.tt.Fields[0])
	for fi := first; fi < first+bt.Struct().FieldCount; fi++ {
		if vbind.FieldHasInlineVariant(&c.tt.Fields[fi]) {
			return unsafe.Add(h.dst, c.tt.Fields[fi].Offset)
		}
	}
	return nil
}

func (c *binder) discErr(polyIdx uint16, h *polyHost, bound bool) error {
	kind := uint32(ndec.BindErrVariantMissingDisc)
	if bound {
		kind = ndec.BindErrVariantUnknownDisc
	}
	return c.phase2Err(kind, uint32(polyIdx), h)
}

// bindInlineDisc binds the first inline discriminator entry, which must be
// a string.
func (c *binder) bindInlineDisc(h *polyHost, f *vbind.BindField, e *polyEntry, iv uint16) error {
	if c.byteAt(e.vpos) != '"' {
		return c.phase2Err(ndec.BindErrTypeMismatch, 0, h)
	}
	disc := unsafe.Add(h.dst, c.tt.Polys[iv].DiscFieldOff)
	err := c.seek(e.vpos, func() error {
		s, err := c.string(true)
		if err != nil {
			return err
		}
		if f.Flags&uint32(vbind.TagQuoted) != 0 {
			if !c.writeQuotedScalar(disc, vbind.KindString, s) {
				return c.phase2Err(ndec.BindErrTypeMismatch, 0, h)
			}
			return nil
		}
		*(*string)(disc) = s
		return nil
	})
	if err == nil {
		h.bound = append(h.bound, c.tt.Polys[iv].DiscFieldOff)
	}
	return err
}

// phase2Poly binds a deferred variant or kindof member at the cursor.
func (c *binder) phase2Poly(h *polyHost, f *vbind.BindField) error {
	polyIdx := vbind.FieldPolyIdx(f)
	var pc polyCase
	if vbind.FieldHasKindof(f) {
		kind := jsonKindOf(c.peek())
		if kind < 0 {
			return c.fail(ndec.BindErrSyntax, c.pos())
		}
		if pc = c.caseByKindof(polyIdx, kind); !pc.ok {
			return c.phase2Err(ndec.BindErrKindofUnregistered, uint32(kind), h)
		}
		if c.typ(pc.ti).Kind != vbind.KindAny && c.coldCase(pc) {
			return c.phase2Err(ndec.BindErrKindofColdCase, uint32(kind), h)
		}
	} else {
		var bound bool
		if pc, bound = c.caseByDisc(polyIdx, h); !pc.ok {
			return c.discErr(polyIdx, h, bound)
		}
		if c.typ(pc.ti).Kind != vbind.KindAny && c.coldCase(pc) {
			return c.phase2Err(ndec.BindErrVariantColdCase, uint32(polyIdx), h)
		}
	}
	if err := c.push(); err != nil {
		return err
	}
	err := c.bindCase(unsafe.Add(h.dst, f.Offset), h, pc)
	c.pop()
	if err != nil {
		c.rewriteCaseMismatch(pc)
	}
	return err
}

// rewriteCaseMismatch lifts a type-mismatch abort out of a case descent to
// the case type. Tape-backed descents, the deferred phase2 bind and every
// tape-mode walk, report the descent root as the native tape binder does,
// because the tape carries no source identity for the leaf. The JSON path's
// immediate dispatch keeps the leaf identity a plain field reports. Syntax,
// truncation, and depth failures pass through untouched, and the innermost
// descent's lift wins, matching the native yield point.
func (c *binder) rewriteCaseMismatch(pc polyCase) {
	if c.info.Kind == ndec.BindErrTypeMismatch && !c.caseLift {
		c.info.TypeIdx = int(pc.ti)
		c.caseLift = true
	}
}

// bindInlineCase binds the inline case from its view of the entries: a
// struct case takes them as members, a Value case as one object. Pointer
// and map cases take nothing.
func (c *binder) bindInlineCase(target unsafe.Pointer, pc polyCase, view []polyEntry) error {
	k := c.typ(pc.ti).Kind
	if k == vbind.KindPointer || k == vbind.KindMap {
		return nil
	}
	slot := c.carve(pc.slot)
	*(*[2]unsafe.Pointer)(target) = [2]unsafe.Pointer{pc.rtype, slot}
	if err := c.push(); err != nil {
		return err
	}
	defer c.pop()
	switch k {
	case vbind.KindValue:
		return c.storeObjectValue(slot, view)
	case vbind.KindStruct:
		return c.bindEntries(slot, pc.ti, view)
	}
	return nil
}

// bindEntries binds members already located by an outer walk into a struct,
// as the native tape walk binds a case from the merged tape.
func (c *binder) bindEntries(dst unsafe.Pointer, ti uint32, entries []polyEntry) error {
	if !c.pl.polyHost[ti] {
		for i := range entries {
			f := c.hostField(ti, entries[i].key)
			if f == nil {
				if c.opt&ndec.BindOptDisallowUnknown != 0 {
					c.info = Error{Kind: ndec.BindErrUnknownField, Pos: NoPos, TypeIdx: int(ti)}
					return errAbort
				}
				continue
			}
			if err := c.seek(entries[i].vpos, func() error { return c.bindField(dst, ti, f) }); err != nil {
				return err
			}
		}
		return nil
	}
	h := &polyHost{dst: dst, ti: ti}
	for i := range entries {
		e := &entries[i]
		f := c.hostField(ti, e.key)
		if err := c.seek(e.vpos, func() error { return c.hostMember(h, f, e.key, e.kpos) }); err != nil {
			return err
		}
	}
	return c.polyClose(h)
}

// storeObjectValue publishes a Value holding an object of the entries.
func (c *binder) storeObjectValue(dst unsafe.Pointer, entries []polyEntry) error {
	n := 2
	for i := range entries {
		n += entries[i].kend - entries[i].kpos + entries[i].vend - entries[i].vpos + 2
	}
	buf := make([]byte, 0, n)
	buf = append(buf, '{')
	for i := range entries {
		e := &entries[i]
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, c.src[e.kpos:e.kend]...)
		buf = append(buf, ':')
		buf = append(buf, c.src[e.vpos:e.vend]...)
	}
	buf = append(buf, '}')
	return c.storeDoc(dst, buf, NoPos)
}

// bindValueDoc binds the value at the cursor as a value.Value over its own
// document.
func (c *binder) bindValueDoc(dst unsafe.Pointer) error {
	if c.eof() {
		return c.fail(ndec.BindErrEOF, c.pos())
	}
	start := c.p
	if c.tape && c.peek() == 'n' {
		*(*value.Value)(dst) = value.Value{}
		return c.skipToken()
	}
	if err := c.validValue(); err != nil {
		return err
	}
	span := gdec.TrimTrailingWS(c.src[start:c.p])
	return c.storeDoc(dst, span, uint64(start))
}

// storeDoc parses text into a fresh document and installs its root Value,
// whose document publishes with the parse. A malformed document is a syntax
// error at pos.
func (c *binder) storeDoc(dst unsafe.Pointer, text []byte, pos uint64) error {
	var opts []dom.ParseOption
	if c.opt&ndec.BindOptStrictScan != 0 {
		opts = append(opts, vopt.AllowInvalidUTF8(false))
	}
	v, err := dom.Parse(text, opts...)
	if err != nil {
		c.info = Error{Kind: ndec.BindErrSyntax, Pos: pos, TypeIdx: int(c.rootType)}
		return errAbort
	}
	*(*value.Value)(dst) = v
	c.stageDoc(valueabi.Load(dst).Doc)
	return nil
}
