// typeInfo keeps the C ABI view in base at offset 0 and stores Go-only
// kind-specific state in body, so both sides can share the same pointer.

package ndec

import (
	"reflect"
	"unsafe"
)

type typeInfo struct {
	base         bindTypeInfo   // offset 0: C-side ABI view (64 B)
	body         unsafe.Pointer // offset 64: Go-side *structBody | *sliceBody | *mapBody | *ptrBody
	rtypePtrData unsafe.Pointer // offset 72: Go rtype (all kinds, including scalars)
}

type builtType = typeInfo

type structBody struct {
	fields     []bindFieldInfo
	fieldNames []string
	// fieldOriginalPaths stores each field's original Go path, same length
	// as fields. Plain fields are single-segment ("X"); embedded promoted
	// fields are multi-segment (["Inner", "Y"]). Only used by the error
	// path (renderFieldPath for UnmarshalTypeError.Field); hot paths do
	// not read it. Stored as [][]string rather than joined strings because
	// errCtx assembles the dotted form at render time (e.g., when a SLICE
	// frame interposes [N], the renderer needs the segments to insert dots;
	// pre-joining "Inner.Y" would make splitting the field difficult).
	fieldOriginalPaths [][]string
	lookup             *builtLookup
	rt                 reflect.Type
	rtypePtr           unsafe.Pointer
	children           []*typeInfo
	noscanSliceBudget  int
	mapKVBufBudget     int
}

type sliceBody struct {
	rt                reflect.Type
	rtypePtr          unsafe.Pointer
	elem              *typeInfo
	elemHasPtr        bool
	emptySliceData    unsafe.Pointer
	noscanSliceBudget int
}

type mapBody struct {
	rt             reflect.Type
	rtypePtr       unsafe.Pointer
	elem           *typeInfo // elem = value typeinfo
	elemHasPtr     bool
	emptySliceData unsafe.Pointer
	kvSlotSize     uint32
	mapValueRType  unsafe.Pointer
	mapValueHasPtr bool
	mapKVBufBudget int
	// keyKind: bkString uses MapAssignFastStr fast path; bkInt/bkInt8...
	// bkUint64 use strconv.ParseInt + MapAssign slow path. Flush reads
	// the KV slot's first 16 bytes (string header) and converts by key type.
	keyKind bindKind
	// keySize: byte count for writing int keys to the stack buffer
	// (1/2/4/8). Only meaningful when keyKind is an integer type.
	keySize uint8
}

type ptrBody struct {
	rt       reflect.Type
	rtypePtr unsafe.Pointer
	elem     *typeInfo
}

func (ti *typeInfo) kind() bindKind { return bindKind(ti.base.Kind) }

func (ti *typeInfo) structBody() *structBody {
	return (*structBody)(ti.body)
}

func (ti *typeInfo) sliceBody() *sliceBody {
	return (*sliceBody)(ti.body)
}

func (ti *typeInfo) mapBody() *mapBody {
	return (*mapBody)(ti.body)
}

func (ti *typeInfo) ptrBody() *ptrBody {
	return (*ptrBody)(ti.body)
}

func (ti *typeInfo) structFields() []bindFieldInfo { return ti.structBody().fields }
func (ti *typeInfo) structFieldNames() []string    { return ti.structBody().fieldNames }
func (ti *typeInfo) structFieldOriginalPaths() [][]string {
	return ti.structBody().fieldOriginalPaths
}
func (ti *typeInfo) rt() reflect.Type {
	switch ti.kind() {
	case bkStruct:
		return ti.structBody().rt
	case bkSlice:
		return ti.sliceBody().rt
	case bkMap:
		return ti.mapBody().rt
	case bkPtr:
		return ti.ptrBody().rt
	default:
		return nil
	}
}

func (ti *typeInfo) rtypePtr() unsafe.Pointer {
	return ti.rtypePtrData
}

func (ti *typeInfo) elemTypeInfo() *typeInfo {
	switch ti.kind() {
	case bkSlice:
		return ti.sliceBody().elem
	case bkMap:
		return ti.mapBody().elem
	case bkPtr:
		return ti.ptrBody().elem
	default:
		return nil
	}
}

func (ti *typeInfo) elemHasPtr() bool {
	switch ti.kind() {
	case bkSlice:
		return ti.sliceBody().elemHasPtr
	case bkMap:
		return ti.mapBody().elemHasPtr
	default:
		return false
	}
}

func (ti *typeInfo) emptySliceData() unsafe.Pointer {
	return ti.sliceBody().emptySliceData
}

func (ti *typeInfo) kvSlotSize() uint32            { return ti.mapBody().kvSlotSize }
func (ti *typeInfo) mapValueRType() unsafe.Pointer { return ti.mapBody().mapValueRType }
func (ti *typeInfo) mapValueHasPtr() bool          { return ti.mapBody().mapValueHasPtr }
func (ti *typeInfo) mapKeyKind() bindKind          { return ti.mapBody().keyKind }
func (ti *typeInfo) noscanSliceBudget() int {
	switch ti.kind() {
	case bkStruct:
		return ti.structBody().noscanSliceBudget
	case bkSlice:
		return ti.sliceBody().noscanSliceBudget
	default:
		return 0
	}
}

func (ti *typeInfo) mapKVBufBudget() int {
	switch ti.kind() {
	case bkStruct:
		return ti.structBody().mapKVBufBudget
	case bkMap:
		return ti.mapBody().mapKVBufBudget
	default:
		return 0
	}
}
