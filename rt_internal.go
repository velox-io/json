package vjson

import (
	"reflect"
	"unsafe"
	_ "unsafe" // required for go:linkname
)

// Low-level runtime functions via go:linkname, bypassing reflect overhead.
// All targets are stable linkname targets (see go.dev/issue/67401).

//go:linkname unsafe_New reflect.unsafe_New
//go:noescape
func unsafe_New(typ unsafe.Pointer) unsafe.Pointer //nolint:revive

//go:linkname unsafe_NewArray reflect.unsafe_NewArray
//go:noescape
func unsafe_NewArray(typ unsafe.Pointer, n int) unsafe.Pointer //nolint:revive

//go:linkname typedslicecopy runtime.typedslicecopy
//go:nosplit
func typedslicecopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int

//go:linkname makemap runtime.makemap
func makemap(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign runtime.mapassign
func mapassign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign_faststr runtime.mapassign_faststr
func mapassign_faststr(t unsafe.Pointer, m unsafe.Pointer, key string) unsafe.Pointer //nolint:revive

//go:linkname typedmemmove runtime.typedmemmove
func typedmemmove(typ unsafe.Pointer, dst unsafe.Pointer, src unsafe.Pointer)

// goIface matches the runtime iface layout {itab, data}.
type goIface struct {
	tab  unsafe.Pointer
	data unsafe.Pointer
}

// extractItab extracts the cached *itab from a non-empty interface pointer.
func extractItab(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return (*goIface)(ifacePtr).tab
}

// rtypePtr extracts the *abi.Type from a reflect.Type interface value.
func rtypePtr(t reflect.Type) unsafe.Pointer {
	iface := *(*[2]unsafe.Pointer)(unsafe.Pointer(&t))
	return iface[1]
}

// SliceHeader matches the internal layout of a Go slice.
type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

//go:linkname mallocgc runtime.mallocgc
func mallocgc(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

// makeDirtyBytes allocates a []byte without zeroing. Caller MUST overwrite
// every byte before reading. Safe because bytes have no pointers for GC.
func makeDirtyBytes(len, cap int) []byte {
	var b []byte
	p := mallocgc(uintptr(cap), nil, false)
	sh := (*SliceHeader)(unsafe.Pointer(&b))
	sh.Data = p
	sh.Len = len
	sh.Cap = cap
	return b
}

//go:linkname maplen reflect.maplen
func maplen(m unsafe.Pointer) int
