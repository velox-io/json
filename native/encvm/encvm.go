// Package encvm provides the Go ↔ C bridge for the native JSON encoder VM.
//
// The package owns the compiled .syso objects and Plan9 assembly trampolines
// that translate Go calling convention to C ABI.
package encvm

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
var Available bool

var (
	vmExec        func(ctx unsafe.Pointer)
	vmExecFast    func(ctx unsafe.Pointer)
	vmExecCompact func(ctx unsafe.Pointer)
)

// VMExec calls the full-mode native encoder.
func VMExec(ctx unsafe.Pointer) { vmExec(ctx) }

// VMExecFast calls the fast-mode native encoder.
func VMExecFast(ctx unsafe.Pointer) { vmExecFast(ctx) }

// VMExecCompact calls the compact-mode native encoder.
func VMExecCompact(ctx unsafe.Pointer) { vmExecCompact(ctx) }
