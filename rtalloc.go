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
func unsafe_New(typ unsafe.Pointer) unsafe.Pointer

//go:linkname unsafe_NewArray reflect.unsafe_NewArray
//go:noescape
func unsafe_NewArray(typ unsafe.Pointer, n int) unsafe.Pointer

//go:linkname typedslicecopy runtime.typedslicecopy
//go:nosplit
func typedslicecopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int

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
