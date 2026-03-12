// ARM64 Go assembly trampoline for native encoder C function (Linux).
//
// Bridges Go ABI to ARM64 C ABI. On Linux (ELF), C symbols have no
// underscore prefix — the Go linker handles this automatically.
//
// Uses B (tail call) like the darwin trampoline. The C function
// returns directly to our Go caller. NOSPLIT $0-8 means no local
// stack frame — the C function allocates its own frame on the
// goroutine stack.

#include "textflag.h"

// func vjVMExecNeon(ctx unsafe.Pointer)
// C: void vj_vm_exec_neon(VjExecCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjVMExecNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_vm_exec_neon(SB)
