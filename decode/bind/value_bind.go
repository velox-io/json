package bind

import (
	"fmt"
	"io"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// UnmarshalValue binds a pre-built value.Value (tape) into the value pointed
// to by out. out must be a non-nil pointer, directly or via an interface{}.
//
// The Value's tape is walked by the native tape binder. TypeTree construction
// rejects unsupported target positions before native execution. Tape, Src, and
// the published StrArena extent are read-only. Ordinary strings may alias
// Doc.StrArena; conversions that synthesize strings append to an owned arena.
// This path consumes an existing tape, so WithStrictScan leaves binding unchanged.
func UnmarshalValue[T any](v value.Value, out T, opts ...UnmarshalOption) error {
	rt := reflect.TypeFor[T]()
	var ptr unsafe.Pointer
	var elemType reflect.Type

	if rt.Kind() == reflect.Pointer {
		ptr = *(*unsafe.Pointer)(unsafe.Pointer(&out))
		if ptr == nil {
			return &InvalidUnmarshalError{Type: rt}
		}
		elemType = rt.Elem()
	} else if rt.Kind() == reflect.Interface {
		rv := reflect.ValueOf(out)
		if !rv.IsValid() {
			return &InvalidUnmarshalError{Type: nil}
		}
		if rv.Kind() != reflect.Pointer {
			return &InvalidUnmarshalError{Type: rv.Type()}
		}
		if rv.IsNil() {
			return &InvalidUnmarshalError{Type: rv.Type()}
		}
		ptr = rv.UnsafePointer()
		elemType = rv.Elem().Type()
	} else {
		return &InvalidUnmarshalError{Type: rt}
	}
	desc := valueabi.Load(unsafe.Pointer(&v))
	if !desc.HasTape() {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	sh, err := shapeFor(elemType)
	if err != nil {
		return err
	}
	p := getParser(sh)
	defer putParser(sh, p)
	applyOpts(p, opts)
	return p.unmarshalValue(v, &desc, ptr)
}

// UnmarshalValue binds a pre-built value.Value into dst using this Parser's
// shape. dst must be a non-nil *T matching the type the Parser was created for.
func (p *Parser) UnmarshalValue(v value.Value, dst any, opts ...UnmarshalOption) error {
	rt := reflect.TypeOf(dst)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return &InvalidUnmarshalError{Type: rt}
	}
	dstPtr := (*gort.GoIface)(unsafe.Pointer(&dst)).Data
	if dstPtr == nil {
		return &InvalidUnmarshalError{Type: rt}
	}
	desc := valueabi.Load(unsafe.Pointer(&v))
	if !desc.HasTape() {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	applyOpts(p, opts)
	return p.unmarshalValue(v, &desc, dstPtr)
}

// tapeBindStringAppendBound computes an overflow-checked upper bound for bytes
// that string-producing conversions may append, including one terminator per
// string or object key occurrence.
func tapeBindStringAppendBound(v value.Value) (int, bool) {
	const maxInt = int(^uint(0) >> 1)
	total := 0
	valid := true
	add := func(n int) {
		if n < 0 || total > maxInt-n {
			valid = false
			return
		}
		total += n
	}
	var walk func(value.Value)
	walk = func(cur value.Value) {
		if !valid {
			return
		}
		switch cur.Type() {
		case value.KindString:
			s, ok := cur.Str()
			if !ok {
				valid = false
				return
			}
			add(len(s))
			add(1)
		case value.KindArray:
			cur.ForEachElem(func(_ int, elem value.Value) bool {
				walk(elem)
				return valid
			})
		case value.KindObject:
			cur.ForEachKey(func(key string, elem value.Value) bool {
				add(len(key))
				add(1)
				walk(elem)
				return valid
			})
		}
	}
	walk(v)
	return total, valid
}

// unmarshalValue seeds the native tape binder from a Value descriptor and
// consumes its existing tape through the shared BindParseRun and serveYield loop.
func (p *Parser) unmarshalValue(v value.Value, desc *valueabi.Descriptor, rootDst unsafe.Pointer) error {
	// TypeTree records the first unsupported target position with its field path.
	if pos := p.tt.TapeBindUnsupported; pos != nil {
		var rt reflect.Type
		if int(pos.TypeIdx) < len(p.tt.ReflectTypes) {
			rt = p.tt.ReflectTypes[pos.TypeIdx]
		}
		return &TapeBindUnsupportedError{Pos: pos, Type: rt}
	}

	// Typed binding requires arena-backed documents because zero-copy tapes may
	// publish caller-source string pointers into the output.
	doc := desc.Doc
	if doc.ZeroCopy {
		return ErrZeroCopyValue
	}

	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))

	// RootDst is the caller's output. Src preserves the input document's source
	// coordinate space for nested Value descriptors.
	m.Ctx = p.ctxTemplate
	m.Ctx.OptFlags |= p.optFlags
	m.Ctx.Src = (*byte)(unsafe.SliceData(doc.Src)) // Borrowed for the native walk.
	m.Ctx.SrcLen = uint64(len(doc.Src))
	m.Ctx.RootDst = rootDst // Borrowed for the native walk.
	// RootViewMode selects the logical seam view for shared merged-tape words.
	m.Ctx.RootViewMode = uint32(desc.Mode)

	allocABI := &m.Alloc

	// Native reads string and numeric text from the published StrArena extent.
	// Its length is the generation boundary and first writable offset.
	published := len(doc.StrArena)
	strArena := doc.StrArena
	if p.tt.TapeBindMayAppendStrings {
		// Copy the published extent because spare capacity remains producer-owned.
		appendBound, ok := tapeBindStringAppendBound(v)
		if !ok || appendBound > int(^uint(0)>>1)-published-64 {
			return fmt.Errorf("vjson: input too large: string arena bound overflows int")
		}
		owned := make([]byte, published, published+appendBound+64)
		copy(owned, strArena)
		strArena = owned
	}
	allocABI.StrArena = (*byte)(unsafe.SliceData(strArena))
	allocABI.StrArenaCap = uint64(cap(strArena))
	allocABI.StrGenStart = uint64(published)
	m.Core.StrUsed = uint64(published)

	// The native walk uses three coordinates. Base is the origin for encoded
	// container indices, root is this Value's first visible word, and extent is
	// one past this Value. Child Values share the base but select distinct roots
	// and extents. Extent also applies the active seam view when locating them.
	root, extent := desc.Extent()
	tape := doc.Tape[desc.Base:]
	if extent == cap(tape) {
		// Add one addressable word so checkptr permits the one-past-end pointer.
		padded := make([]uint64, len(tape), len(tape)+1)
		copy(padded, tape)
		tape = padded
	}
	tape = tape[:cap(tape)]
	tapeBase := unsafe.Pointer(unsafe.SliceData(tape))
	tapeRoot := unsafe.Pointer(unsafe.SliceData(tape[root:]))
	tapeEnd := unsafe.Pointer(unsafe.SliceData(tape[extent:]))
	allocABI.ValueTape = (*uint64)(tapeBase) // Borrowed for the native walk.

	// BindMachineCursorOffset locates the ABI cursor pair, interpreted as tape
	// pointers during the native walk.
	cursor := (*[2]unsafe.Pointer)(unsafe.Add(unsafe.Pointer(m), ndec.BindMachineCursorOffset))
	cursor[0] = tapeRoot // First word of the selected Value.
	cursor[1] = tapeEnd  // One past the selected Value.

	// Retain every backing referenced by raw machine pointers until the native
	// walk and its drains complete. tape may be the padded copy above.
	p.alloc.Retain(tapeBase)
	p.alloc.Retain(unsafe.Pointer(unsafe.SliceData(strArena)))
	p.alloc.Retain(unsafe.Pointer(unsafe.SliceData(doc.Src)))

	// Variant binding may build merged scratch tapes with unchecked TapeUsed
	// bumps, so this allocation must cover the complete write. For K tapes with E
	// entries copied from W input words, seams add E+K words. Each entry consumes
	// at least two input words and each tape contributes two container words, so
	// E <= (W-2K)/2 and the total is at most 3W/2. Split tapes add two words per
	// statically known site, or use the conservative 5W/2 bound when K is dynamic.
	alloc := p.alloc
	tapeNeed := (3*len(doc.Tape) + 1) / 2
	if p.tt.HasSplitTape {
		if k := p.tt.SplitTapeSites; k != vbind.SplitTapeSitesUnbounded {
			tapeNeed += 2 * k
		} else {
			tapeNeed = (5*len(doc.Tape) + 1) / 2
		}
	}
	if err := syncTapeArena(alloc, allocABI, tapeNeed); err != nil {
		return err
	}

	// HasValueField gates the new Doc that nested Values receive for the scratch
	// tape and clears the pooled ABI field for other target graphs.
	var valueDoc *valueabi.Doc
	if p.tt.HasValueField {
		valueDoc = syncDoc(allocABI)
	} else {
		allocABI.ValueDoc = nil
	}

	// Reset the deferred and map staging cursors. Slot-class bump cursors retain
	// their reusable arena positions across calls.
	syncDeferredDrain(p.alloc, allocABI)
	syncMapBuf(p.alloc, allocABI)

	// Route the machine at the cold-start seed phase.
	m.Core.Phase = ndec.BindPhaseTapeBindRoot

	defer func() {
		p.alloc.Release() // Publish native writes, then stage reusable backings.
		// Clear borrowed ABI pointers before the machine is reused. KeepAlive
		// preserves their Go owners through the final stores.
		m.Ctx.RootDst = nil
		m.Ctx.Src = nil
		allocABI.StrArena = nil
		allocABI.ValueTape = nil
		allocABI.ValueDoc = nil
		cursor[0] = nil
		cursor[1] = nil
		runtime.KeepAlive(rootDst)
		runtime.KeepAlive(doc.Tape)
		runtime.KeepAlive(doc.Src)
		runtime.KeepAlive(strArena)
	}()

	for {
		ndec.BindParseRun(unsafe.Pointer(m))
		done, err := p.serveYield(m, doc.Src)
		if err != nil {
			return err
		}
		if done {
			// Publish the tape produced for nested Values with the string and
			// source views used by this walk.
			if valueDoc != nil {
				valueDoc.StrArena = strArena[:m.Core.StrUsed]
				valueDoc.Src = doc.Src
				valueDoc.Tape = alloc.TapeArena[:m.Alloc.TapeUsed]
			}

			// Deferred callbacks complete before map slots publish their values.
			if m.Alloc.DeferredDrainUsed > 0 {
				if err := drainDeferredRecords(p, m, doc.Src); err != nil {
					return err
				}
			}
			if m.Alloc.MapBufUsed > 0 {
				if err := drainAllMapSlots(m); err != nil {
					return err
				}
			}
			return nil
		}
	}
}
