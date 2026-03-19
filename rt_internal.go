package vjson

import (
	"reflect"
	"unsafe"
	_ "unsafe" // required for go:linkname
)

// Low-level runtime allocation and copy functions, accessed via go:linkname
// to bypass reflect's validation overhead (type assertions, kind checks,
// mustBeAssignable, reflect.Value construction, etc.).
//
// All three targets are explicitly marked as stable linkname targets in the
// Go runtime with "Do not remove or change the type signature" comments
// (see go.dev/issue/67401).

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

// goIface matches the runtime iface layout: {itab, data}.
// Used to construct interface values with a cached itab, avoiding
// the mallocgc boxing that reflect.Value.Interface() triggers for
// value types larger than a pointer (e.g. time.Time = 24 bytes).
type goIface struct {
	tab  unsafe.Pointer
	data unsafe.Pointer
}

// extractItab extracts the cached *itab from an interface pointer.
// ifacePtr must point to a non-empty interface value (e.g. *json.Marshaler).
// The interface must have layout {itab, data} (iface, not eface).
func extractItab(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return (*goIface)(ifacePtr).tab
}

// rtypePtr extracts the *abi.Type pointer from a reflect.Type interface value.
//
// reflect.Type is an interface {itab, data} where data points to an *rtype,
// and rtype is struct{t abi.Type}. Since abi.Type is the first (and only)
// field, the data pointer IS the *abi.Type pointer.
func rtypePtr(t reflect.Type) unsafe.Pointer {
	// An interface value in memory is {itab *itab, data unsafe.Pointer}.
	// We extract the data word (index 1), which is the *rtype == *abi.Type.
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

// makeDirtyBytes allocates a []byte of the given length and capacity without
// zeroing the memory. The caller MUST overwrite every byte before reading it.
//
// This is safe for byte slices because bytes contain no pointers — the GC
// never scans their contents. The returned slice is GC-managed (allocated
// via mallocgc), so it does not require manual freeing.
func makeDirtyBytes(len, cap int) []byte {
	var b []byte
	p := mallocgc(uintptr(cap), nil, false)
	sh := (*SliceHeader)(unsafe.Pointer(&b))
	sh.Data = p
	sh.Len = len
	sh.Cap = cap
	return b
}

// Legacy shim-based iteration (kept for reference/fallback)

// GoMapIterator mirrors runtime.linknameIter. The runtime's mapiterinit
// heap-allocates a maps.Iter internally (new(maps.Iter)). Prefer the
// stack-based mapsIter above for hot paths.
type GoMapIterator struct {
	Key  unsafe.Pointer // current key ptr (nil = end of iteration)
	Elem unsafe.Pointer // current elem ptr
	Typ  unsafe.Pointer // *abi.MapType (opaque, set by runtime)
	It   unsafe.Pointer // *maps.Iter (opaque, allocated by runtime)
}

//go:linkname mapiterinit runtime.mapiterinit
func mapiterinit(t unsafe.Pointer, m unsafe.Pointer, it *GoMapIterator)

//go:linkname mapiternext runtime.mapiternext
func mapiternext(it *GoMapIterator)

// maplen returns the number of entries in the map.
// m is the map header pointer (same as mapiterinit's m parameter).
//
//go:linkname maplen reflect.maplen
func maplen(m unsafe.Pointer) int
