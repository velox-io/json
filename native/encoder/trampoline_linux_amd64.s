// AMD64 Go assembly trampoline for native encoder C function (Linux).
//
// Bridges Go ABI to x86-64 System V C ABI.
// System V ABI: first arg in RDI, second in RSI, etc.
// On Linux (ELF), C symbols have no underscore prefix — the Go linker
// handles this automatically.
//
// Uses JMP (tail call). The C function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame.

#include "textflag.h"

// func vjVMExec(ctx unsafe.Pointer)
// C: void vj_vm_exec(VjExecCtx* ctx)
// SysV ABI: ctx=RDI
TEXT ·vjVMExec(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec(SB)
