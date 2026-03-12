// ARM64 Go assembly trampolines for native encoder C functions (macOS).
//
// Bridges Go ABI to ARM64 C ABI. On macOS, Mach-O symbols have _
// prefix but the Go linker handles this automatically — reference
// without prefix here.
//
// Uses B (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame — the C function
// allocates its own frame on the goroutine stack.
//
// Each ISA has two mode variants: default and fast.

#include "textflag.h"

// ---- Default mode ----

// func vjVMExecDefaultNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_default_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecDefaultNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_default_neon(SB)

// ---- Fast mode ----

// func vjVMExecFastNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecFastNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_fast_neon(SB)
