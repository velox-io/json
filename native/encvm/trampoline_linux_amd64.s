// AMD64 Go assembly trampolines for native encoder C functions (Linux).
//
// Bridges Go ABI to x86-64 System V C ABI.
// System V ABI: first arg in RDI, second in RSI, etc.
// On Linux (ELF), C symbols have no underscore prefix — the Go linker
// handles this automatically.
//
// Uses JMP (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame.
//
// Each ISA has two mode variants: default and fast.

#include "textflag.h"

// ---- Default mode ----

// func vjVMExecDefaultSSE42(ctx unsafe.Pointer)
// C: void vj_vm_exec_default_sse42(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecDefaultSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_default_sse42(SB)

// func vjVMExecDefaultAVX2(ctx unsafe.Pointer)
// C: void vj_vm_exec_default_avx2(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecDefaultAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_default_avx2(SB)

// func vjVMExecDefaultAVX512(ctx unsafe.Pointer)
// C: void vj_vm_exec_default_avx512(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecDefaultAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_default_avx512(SB)

// ---- Fast mode ----

// func vjVMExecFastSSE42(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_sse42(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecFastSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_sse42(SB)

// func vjVMExecFastAVX2(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_avx2(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecFastAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_avx2(SB)

// func vjVMExecFastAVX512(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast_avx512(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecFastAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_avx512(SB)
