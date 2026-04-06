package gort

import "unsafe"

// NoescapePtr hides a pointer from escape analysis.
// Uses the same uintptr xor trick as runtime.noescape.
//
//go:nosplit
func NoescapePtr(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}
