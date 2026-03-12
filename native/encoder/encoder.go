// Package encoder provides the Go ↔ C bridge for the native JSON encoder.
//
// The package owns the compiled .syso object (vj_encode_struct) and the
// Plan9 assembly trampolines that translate Go calling convention to C ABI.
//
// The root vjson package sets up a CVjEncodingCtx and calls Encode()
// with an unsafe.Pointer to it. This package never interprets the context
// struct — layout correctness is enforced by compile-time assertions in
// both C (native/impl/encoder.h) and Go (vj_native_encoder.go).
package encoder
