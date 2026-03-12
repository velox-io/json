//go:build !(darwin && arm64)

package encoder

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
const Available = false

// Encode is a no-op stub for platforms without a native encoder .syso.
// The root package checks Available before calling; this is defensive.
func Encode(_ unsafe.Pointer) {
	panic("vjson: native encoder not available on this platform")
}

// EncodeArray is a no-op stub for platforms without a native encoder .syso.
func EncodeArray(_ unsafe.Pointer) {
	panic("vjson: native encoder not available on this platform")
}
