//go:build !(darwin && arm64) && !(linux && arm64) && !(linux && amd64)

package encoder

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
const Available = false

// VMExec is a no-op stub for platforms without a native encoder .syso.
func VMExec(_ unsafe.Pointer) {
	panic("vjson: native VM encoder not available on this platform")
}
