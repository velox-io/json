// Package vcopy implements type-driven deep copy for Go values.
//
// It reuses the precompiled type descriptors from package typ (the same
// descriptors powering venc/vdec) to drive an unsafe, zero-reflect hot path
// for primitives, structs, slices, arrays, pointers, and maps. Empty
// interface (any) and non-empty interface fields resolve the dynamic rtype
// directly from the interface header and reuse the same type cache, and
// reflect.Value boxing never occurs anywhere on the copy path.
//
// The dispatch shape mirrors vdec.scanValue: a switch on UniType.Kind that
// recurses through the type graph. There is no tokenize step, so the cost
// per value is one branch plus one typed memmove (for scalars) or one
// allocation (for containers).
//
// Cyclic graphs are handled via a per-call visiting map; types provably
// acyclic at compile-time (determined by a one-time graph walk cached on
// the Copier) skip the visiting map entirely.
package vcopy

import (
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/typ"
)

// UnsupportedTypeError is re-exported from jerr for symmetry with venc/vdec.
type UnsupportedTypeError = jerr.UnsupportedTypeError

// A Copier drives deep-copy traversal. Obtain one via NewCopier or use the
// package-level DeepCopy helper.
//
// A Copier carries two pieces of state:
//   - acyclicCache: a long-lived, per-Copier cache of which *UniType nodes
//     are provably acyclic. Populated lazily on first encounter of each
//     pointer-bearing type. Skipping the visiting map for acyclic types is
//     the fast path; most business types qualify.
//   - visit: a per-DeepCopy-call map from source pointer to its already-
//     allocated destination. Used only for types that are NOT provably
//     acyclic, to break cycles.
//
// A Copier is not safe for concurrent use.
type Copier struct {
	acyclicCache map[*typ.UniType]acyclicState
	visit        map[unsafe.Pointer]unsafe.Pointer
}

// acyclicState is the result of a compile-time-style analysis of a type's
// reference graph.
type acyclicState uint8

const (
	acyclicUnknown acyclicState = iota
	acyclicTrue                 // proven: this type cannot participate in a cycle
	acyclicFalse                // may participate in a cycle; needs visiting map
)

// NewCopier returns a Copier. Options are reserved for future use.
func NewCopier(opts ...Option) *Copier {
	c := &Copier{}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a Copier. Reserved for future use; no options are
// currently defined.
type Option func(*Copier)

// beginCall resets per-call state. Called by the public entry points.
func (c *Copier) beginCall() {
	if c.visit != nil {
		for k := range c.visit {
			delete(c.visit, k)
		}
	}
}

// copyValue dispatches on UniType.Kind. It is the structural analog of
// vdec.scanValue: every kind either emits a typed memmove (scalars) or
// recurses through the type descriptor (containers).
func (c *Copier) copyValue(ut *typ.UniType, src, dst unsafe.Pointer) error {
	switch ut.Kind {
	case typ.KindBool,
		typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
		typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
		typ.KindFloat32, typ.KindFloat64:
		// Scalars: one typed memmove. Write barrier semantics handled by
		// the runtime via the rtype pointer.
		gort.TypedMemmove(ut.Ptr, dst, src)
		return nil

	case typ.KindString:
		// Deep copy semantics: force a fresh backing array. The source
		// string header is moved, then the bytes are cloned so that the
		// destination never aliases the source's storage.
		s := *(*string)(src)
		if len(s) == 0 {
			*(*string)(dst) = ""
			return nil
		}
		b := make([]byte, len(s))
		copy(b, s)
		*(*string)(dst) = unsafe.String(&b[0], len(b))
		return nil

	case typ.KindStruct:
		return c.copyStruct(ut, src, dst)

	case typ.KindSlice:
		return c.copySlice(ut, src, dst)

	case typ.KindArray:
		return c.copyArray(ut, src, dst)

	case typ.KindPointer:
		return c.copyPointer(ut, src, dst)

	case typ.KindMap:
		return c.copyMap(ut, src, dst)

	case typ.KindRawMessage:
		// json.RawMessage is a []byte; deep copy clones the bytes.
		srcSlice := *(*[]byte)(src)
		var dstSlice []byte
		if len(srcSlice) > 0 {
			dstSlice = make([]byte, len(srcSlice))
			copy(dstSlice, srcSlice)
		}
		*(*[]byte)(dst) = dstSlice
		return nil

	case typ.KindValue:
		// value.Value is a 24-byte descriptor with one document pointer.
		// The document is immutable after publication, so sharing it is safe.
		// TypedMemmove preserves the pointer's write-barrier semantics.
		gort.TypedMemmove(ut.Ptr, dst, src)
		return nil

	case typ.KindNumber:
		// json.Number is a string; same deep-copy semantics as string.
		s := *(*string)(src)
		if len(s) == 0 {
			*(*string)(dst) = ""
			return nil
		}
		b := make([]byte, len(s))
		copy(b, s)
		*(*string)(dst) = unsafe.String(&b[0], len(b))
		return nil

	case typ.KindAny, typ.KindIface:
		return c.copyInterface(ut, src, dst)

	default:
		return &UnsupportedTypeError{Type: ut.Type}
	}
}

func (c *Copier) copyStruct(ut *typ.UniType, src, dst unsafe.Pointer) error {
	si := ut.Ext.(*typ.StructTypeInfo)
	// Copying walks the same promoted offsets encode and decode do, so a shape
	// the typ layer refused would read and write unrelated memory here too.
	if len(si.Rejects) > 0 {
		return &jerr.UnsupportedShapeError{Type: ut.Type, Msg: si.Rejects[0]}
	}
	for i := range si.Fields {
		f := &si.Fields[i]
		fsrc, fdst := src, dst
		if len(f.PtrPath) > 0 {
			// Promoted across an embedded pointer, so the pointer word itself is
			// not in the field list and nothing else copies it. Walk both sides
			// together, allocating on dst so the copy stays deep. A nil source hop
			// leaves dst nil and skips the field: there is no value to copy, and
			// allocating one would invent it.
			var ok bool
			if fsrc, fdst, ok = resolveCopyHops(f.PtrPath, src, dst); !ok {
				continue
			}
		}
		fsrc = unsafe.Add(fsrc, f.Offset)
		fdst = unsafe.Add(fdst, f.Offset)
		if err := c.copyValue(f.FieldType, fsrc, fdst); err != nil {
			return err
		}
	}
	return nil
}

// resolveCopyHops walks a promoted field's embedded-pointer hops on both source
// and destination, allocating destination pointees so the copy stays deep. It
// reports false when a source hop is nil, leaving the destination pointer nil.
//
// An existing destination pointee is reused, so several fields promoted through
// one pointer share a single allocation instead of each replacing the last.
func resolveCopyHops(path []typ.PtrHop, src, dst unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, bool) {
	for i := range path {
		off := path[i].SlotOffset
		srcNext := *(*unsafe.Pointer)(unsafe.Add(src, off))
		if srcNext == nil {
			return nil, nil, false
		}
		dstSlot := (*unsafe.Pointer)(unsafe.Add(dst, off))
		if *dstSlot == nil {
			*dstSlot = gort.UnsafeNew(path[i].PointeeType.Ptr)
		}
		src, dst = srcNext, *dstSlot
	}
	return src, dst, true
}

func (c *Copier) copySlice(ut *typ.UniType, src, dst unsafe.Pointer) error {
	si := ut.Ext.(*typ.SliceTypeInfo)
	srcHdr := (*gort.SliceHeader)(src)
	if srcHdr.Data == nil || srcHdr.Len == 0 {
		// Preserve nil vs empty distinction: a nil source stays nil.
		if srcHdr.Data == nil {
			*(*unsafe.Pointer)(unsafe.Pointer(dst)) = nil
			*(*int)(unsafe.Add(unsafe.Pointer(dst), unsafe.Sizeof(uintptr(0)))) = 0
			*(*int)(unsafe.Add(unsafe.Pointer(dst), 2*unsafe.Sizeof(uintptr(0)))) = 0
			return nil
		}
	}

	elemUT := si.ElemType
	elemSize := elemUT.Size

	// Allocate destination backing array with the correct element type so
	// the GC can scan pointer-bearing elements.
	dstData := gort.UnsafeNewArray(elemUT.Ptr, srcHdr.Len)
	dstHdr := (*gort.SliceHeader)(dst)
	dstHdr.Data = dstData
	dstHdr.Len = srcHdr.Len
	dstHdr.Cap = srcHdr.Len

	// Element-wise deep copy. We cannot use gort.TypedSliceCopy here because
	// that is a shallow memmove; we must recurse for any element containing
	// pointers, strings, slices, maps, etc.
	//
	// Fast path: if the element type contains no GC pointers (pure scalar
	// like int/float/bool), a single typedmemmove over the whole run is
	// correct and much faster than recursing per element.
	if !si.ElemHasPtr {
		gort.TypedMemmove(elemUT.Ptr, dstData, srcHdr.Data)
		return nil
	}

	for i := 0; i < srcHdr.Len; i++ {
		esrc := unsafe.Add(srcHdr.Data, uintptr(i)*elemSize)
		edst := unsafe.Add(dstData, uintptr(i)*elemSize)
		if err := c.copyValue(elemUT, esrc, edst); err != nil {
			return err
		}
	}
	return nil
}

func (c *Copier) copyArray(ut *typ.UniType, src, dst unsafe.Pointer) error {
	ai := ut.Ext.(*typ.ArrayTypeInfo)
	elemUT := ai.ElemType
	elemSize := elemUT.Size

	// Arrays are inline storage; no allocation. Same scalar fast path as
	// slices.
	if !ai.ElemHasPtr {
		gort.TypedMemmove(ut.Ptr, dst, src)
		return nil
	}

	for i := 0; i < ai.ArrayLen; i++ {
		esrc := unsafe.Add(src, uintptr(i)*elemSize)
		edst := unsafe.Add(dst, uintptr(i)*elemSize)
		if err := c.copyValue(elemUT, esrc, edst); err != nil {
			return err
		}
	}
	return nil
}

func (c *Copier) copyPointer(ut *typ.UniType, src, dst unsafe.Pointer) error {
	pi := ut.Ext.(*typ.PointerTypeInfo)
	srcElem := *(*unsafe.Pointer)(src)
	if srcElem == nil {
		// nil pointer copies as nil.
		*(*unsafe.Pointer)(dst) = nil
		return nil
	}

	// Cycle handling. Only pointer dereferences can form cycles in Go's
	// reference graph (slices/arrays hold values inline; map values are
	// slots, not references). If the element type is provably acyclic we
	// skip the visiting map entirely, the common case.
	if !c.isAcyclic(pi.ElemType) {
		if existing, ok := c.visit[srcElem]; ok {
			*(*unsafe.Pointer)(dst) = existing
			return nil
		}
		dstElem := gort.UnsafeNew(pi.ElemType.Ptr)
		*(*unsafe.Pointer)(dst) = dstElem
		if c.visit == nil {
			c.visit = make(map[unsafe.Pointer]unsafe.Pointer)
		}
		c.visit[srcElem] = dstElem
		return c.copyValue(pi.ElemType, srcElem, dstElem)
	}

	dstElem := gort.UnsafeNew(pi.ElemType.Ptr)
	*(*unsafe.Pointer)(dst) = dstElem
	return c.copyValue(pi.ElemType, srcElem, dstElem)
}

func (c *Copier) copyMap(ut *typ.UniType, src, dst unsafe.Pointer) error {
	mi := ut.Ext.(*typ.MapTypeInfo)
	srcMap := *(*unsafe.Pointer)(src)
	if srcMap == nil {
		*(*unsafe.Pointer)(dst) = nil
		return nil
	}

	// Allocate an empty destination map of the same type. MapLen gives the
	// prealloc hint to avoid rehashing during copy.
	hint := gort.MapLen(srcMap)
	dstMap := gort.MakeMap(ut.Ptr, hint, nil)
	*(*unsafe.Pointer)(dst) = dstMap

	keyUT := mi.KeyType
	valUT := mi.ValType
	keySize := keyUT.Size
	valSize := valUT.Size

	// Iterate the source map via the runtime iterator. MapsIterKey/Elem
	// return pointers valid only until MapsIterNext is called, so we deep
	// copy each entry in-place before advancing.
	var it gort.MapsIter
	gort.MapsIterInit(ut.Ptr, srcMap, &it)
	for gort.MapsIterKey(&it) != nil {
		keyPtr := gort.MapsIterKey(&it)
		elemPtr := gort.MapsIterElem(&it)

		// Map keys are not deep-copied: keys are immutable by Go spec
		// (strings, numbers, bools, comparable structs/arrays of these).
		// MapAssign returns a slot to write the value into; we copy the
		// key into the slot's key side, then deep-copy the value.
		dstElemSlot := gort.MapAssign(ut.Ptr, dstMap, keyPtr)
		if err := c.copyValue(valUT, elemPtr, dstElemSlot); err != nil {
			return err
		}
		_ = keySize
		_ = valSize
		gort.MapsIterNext(&it)
	}
	return nil
}

// copyInterface handles KindAny (empty interface) and KindIface (non-empty
// interface). The dynamic type is read directly from the interface header
// via gort.EfaceRType / gort.IfaceConcreteRType, then the runtime rtype is
// rewrapped into a reflect.Type via gort.TypeFromRType so it can key into
// typ.UniTypeOf's cache. No reflect.Value boxing occurs on the hot path.
//
// Cycle safety: a pointer boxed in an interface participates in the same
// visiting map as a typed pointer field. The destination storage is
// registered in c.visit before recursion so a back edge resolves to the
// in-progress copy.
func (c *Copier) copyInterface(ut *typ.UniType, src, dst unsafe.Pointer) error {
	hdr := (*[2]unsafe.Pointer)(src)
	data := hdr[1]
	if data == nil {
		// Nil interface (both eface and iface have data == nil when nil).
		// Write a zeroed {nil, nil}, never {tab, nil}, which would be a
		// typed-nil pitfall.
		*(*[2]unsafe.Pointer)(dst) = [2]unsafe.Pointer{}
		return nil
	}

	// Resolve the concrete rtype. KindAny is eface (word 0 is rtype);
	// KindIface is iface (word 0 is *itab, rtype at offset 8).
	var rtype unsafe.Pointer
	if ut.Kind == typ.KindAny {
		rtype = gort.EfaceRType(src)
	} else {
		rtype = gort.IfaceConcreteRType(src)
	}
	concreteType := gort.TypeFromRType(rtype)
	concreteUT := typ.UniTypeOf(concreteType)

	// Determine the source pointer to the concrete value. For most kinds
	// the iface data word already points at the value. For pointer-shaped
	// kinds (Pointer/Map/Chan/Func) the data word IS the value (a pointer-
	// width descriptor), so the pointer-to-value is the address of the
	// data slot itself.
	var srcVal unsafe.Pointer
	switch concreteUT.Kind {
	case typ.KindPointer, typ.KindMap:
		srcVal = unsafe.Pointer(&hdr[1])
	default:
		// Chan/Func are not in typ's kind enum (they fall through to the
		// default error path), so we don't need to special-case them here.
		srcVal = data
	}

	// Allocate destination storage for the concrete value. UnsafeNew gives
	// us zeroed, GC-aware memory of the right type.
	dstVal := gort.UnsafeNew(rtype)

	// If the concrete type may cycle, register dstVal under srcVal before
	// recursing so a back edge resolves here. For pointer-typed dynamics,
	// the back edge points to dstVal (the storage for the *T), matching
	// how copyPointer registers the destination pointer.
	if !c.isAcyclic(concreteUT) && concreteUT.Kind == typ.KindPointer {
		if existing, ok := c.visit[srcVal]; ok {
			// Existing copy of this pointer-in-interface: reuse it.
			*(*[2]unsafe.Pointer)(dst) = [2]unsafe.Pointer{hdr[0], existing}
			return nil
		}
		if c.visit == nil {
			c.visit = make(map[unsafe.Pointer]unsafe.Pointer)
		}
		c.visit[srcVal] = dstVal
	}

	if err := c.copyValue(concreteUT, srcVal, dstVal); err != nil {
		return err
	}

	// Box the destination storage back into the interface, preserving the
	// original tab/type word. For Pointer/Map the data word IS the value,
	// so we read it out of dstVal (which now holds a fresh *T / map
	// header). For other kinds dstVal is the storage and data should be
	// dstVal itself.
	var boxedData unsafe.Pointer
	switch concreteUT.Kind {
	case typ.KindPointer, typ.KindMap:
		boxedData = *(*unsafe.Pointer)(dstVal)
	default:
		boxedData = dstVal
	}
	*(*[2]unsafe.Pointer)(dst) = [2]unsafe.Pointer{hdr[0], boxedData}
	return nil
}
