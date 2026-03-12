//go:build darwin && arm64

package encoder

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
const Available = true

// Encode calls the C engine entry point vj_encode_struct.
//
// ctx must point to a CVjEncodingCtx struct with all fields initialized.
// The struct may live on the goroutine stack — the NOSPLIT trampoline
// guarantees no GC safe-points during the C call.
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

// EncodeArray calls the C engine entry point vj_encode_array.
//
// ctx must point to a CVjArrayCtx struct with all fields initialized.
// The C function loops over array elements calling vj_encode_struct
// per element. On BUF_FULL, the current element index is saved for
// resume.
func EncodeArray(ctx unsafe.Pointer) {
	vjEncodeArray(ctx)
}

// vjEncodeArray is the assembly trampoline to C vj_encode_array.
// Defined in trampoline_darwin_arm64.s.
//
//go:noescape
//go:nosplit
func vjEncodeArray(ctx unsafe.Pointer)
