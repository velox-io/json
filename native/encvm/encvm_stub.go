//go:build vj_noencvm || !(amd64 || arm64)

package encvm

import "unsafe"

// Stub entries for builds without the native encoder VM: either the
// vj_noencvm tag is set or the platform has no arch-canonical blob. The
// trampoline declarations in encvm.go get panicking bodies here; Available
// stays false and callers take the pure-Go encoder before ever reaching
// these.
func stackReserve() {}

func vjVMExecFull(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }

func vjVMExecFast(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }

func vjVMExecCompact(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }
