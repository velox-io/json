package gort

import (
	"reflect"
	"unsafe"
	_ "unsafe" // required for go:linkname
)

//go:linkname UnsafeNew reflect.unsafe_New
//go:noescape
func UnsafeNew(typ unsafe.Pointer) unsafe.Pointer //nolint:revive

//go:linkname UnsafeNewArray reflect.unsafe_NewArray
//go:noescape
func UnsafeNewArray(typ unsafe.Pointer, n int) unsafe.Pointer //nolint:revive

//go:linkname TypedSliceCopy runtime.typedslicecopy
//go:nosplit
func TypedSliceCopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int

//go:linkname MakeMap runtime.makemap
func MakeMap(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer

//go:linkname MapAssign runtime.mapassign
func MapAssign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

//go:linkname MapAssignFastStr runtime.mapassign_faststr
func MapAssignFastStr(t unsafe.Pointer, m unsafe.Pointer, key string) unsafe.Pointer //nolint:revive

//go:linkname MapLen reflect.maplen
//go:noescape
func MapLen(m unsafe.Pointer) int

//go:linkname TypedMemmove runtime.typedmemmove
func TypedMemmove(typ unsafe.Pointer, dst unsafe.Pointer, src unsafe.Pointer)

//go:linkname MallocGC runtime.mallocgc
func MallocGC(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

type GoIface struct {
	Tab  unsafe.Pointer
	Data unsafe.Pointer
}

func ExtractItab(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return (*GoIface)(ifacePtr).Tab
}

func TypePtr(t reflect.Type) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(&t), unsafe.Sizeof(uintptr(0))))
}

type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// MakeDirtyBytes allocates a []byte without zeroing. Caller MUST overwrite
// every byte before reading. Safe because bytes have no pointers for GC.
func MakeDirtyBytes(len, cap int) []byte {
	var b []byte
	p := MallocGC(uintptr(cap), nil, false)
	sh := (*SliceHeader)(unsafe.Pointer(&b))
	sh.Data = p
	sh.Len = len
	sh.Cap = cap
	return b
}
