// ARM64 Go assembly trampolines for native encoder C functions (Linux).
//
// Bridges Go ABI to ARM64 C ABI. On Linux (ELF), C symbols have no
// underscore prefix — the Go linker handles this automatically.
//
// Uses B (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame — the C function
// allocates its own frame on the goroutine stack.
//
// Each ISA has three mode variants: full, compact, and fast.

#include "textflag.h"

// ---- Full mode ----

// func vjVMExecFullNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_full_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecFullNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_full_neon(SB)

// ---- Fast mode ----

// func vjVMExecFastNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecFastNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_fast_neon(SB)

// ---- Compact mode ----

// func vjVMExecCompactNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecCompactNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_compact_neon(SB)
