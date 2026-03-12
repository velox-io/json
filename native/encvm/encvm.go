// Package encvm provides the Go ↔ C bridge for the native JSON encoder VM.
//
// The package owns the compiled .syso object (vj_vm_exec) and the
// Plan9 assembly trampolines that translate Go calling convention to C ABI.
//
// The root vjson package sets up a VjExecCtx and calls VMExec()
// with an unsafe.Pointer to it. This package never interprets the context
// struct — layout correctness is enforced by compile-time assertions in
// both C (native/impl/encoder_types.h) and Go (vj_native_encoder.go).
package encvm

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
// Set to true by platform-specific init() when at least one ISA is available.
var Available bool

// vmExec holds the default-mode ISA-specific entry point selected at init time.
var vmExec func(ctx unsafe.Pointer)

// vmExecFast holds the fast-mode ISA-specific entry point selected at init time.
// Fast mode unconditionally uses the fast string escape path (no HTML/UTF-8/
// line-terminator checks), eliminating runtime flag dispatch.
var vmExecFast func(ctx unsafe.Pointer)

// VMExec calls the default-mode native encoder entry point.
func VMExec(ctx unsafe.Pointer) { vmExec(ctx) }

// VMExecFast calls the fast-mode native encoder entry point.
func VMExecFast(ctx unsafe.Pointer) { vmExecFast(ctx) }
