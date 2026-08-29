//go:build !vj_noencvm

// ARM64 Go assembly trampolines for native encoder C functions (macOS).
//
// Bridges Go ABI to ARM64 C ABI. On macOS, Mach-O symbols have _
// prefix but the Go linker handles this automatically, so we reference
// symbols without prefix here.
//
// Uses B (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame; the C function
// allocates its own frame on the goroutine stack.

#include "textflag.h"

// ---- Full mode ----

// func vjVMExecFull(ctx unsafe.Pointer)
// C: void vj_vm_exec_full_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_full_neon(SB)

// ---- Fast mode ----

// func vjVMExecFast(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_fast_neon(SB)

// ---- Compact mode ----

// func vjVMExecCompact(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_compact_neon(SB)
