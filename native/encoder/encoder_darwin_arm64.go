//go:build darwin && arm64

package encoder

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
const Available = true

// Encode calls the C engine entry point vj_encode_struct.
//
// ctx must point to a CVjEncodingCtx struct with all fields initialized.
// The caller is responsible for pinning any Go memory referenced by
// the context (buffer, struct base, key pointers) before calling Encode.
//
// On return, the caller inspects the ErrorCode field at offset 36 to
// determine the result.
func Encode(ctx unsafe.Pointer) {
	vjEncodeStruct(ctx)
}

// vjEncodeStruct is the assembly trampoline to C vj_encode_struct.
// Defined in trampoline_darwin_arm64.s.
//
//go:noescape
//go:nosplit
func vjEncodeStruct(ctx unsafe.Pointer)
