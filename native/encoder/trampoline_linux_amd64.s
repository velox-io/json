// AMD64 Go assembly trampolines for native encoder C functions (Linux).
//
// Bridges Go ABI to x86-64 System V C ABI.
// System V ABI: first arg in RDI, second in RSI, etc.
// On Linux (ELF), C symbols have no underscore prefix — the Go linker
// handles this automatically.
//
// Uses JMP (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame.

#include "textflag.h"

// func vjVMExecSSE42(ctx unsafe.Pointer)
// C: void vj_vm_exec_sse42(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_sse42(SB)

// func vjVMExecAVX2(ctx unsafe.Pointer)
// C: void vj_vm_exec_avx2(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_avx2(SB)

// func vjVMExecAVX512(ctx unsafe.Pointer)
// C: void vj_vm_exec_avx512(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExecAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_avx512(SB)
