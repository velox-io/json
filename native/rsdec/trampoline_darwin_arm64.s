// ARM64 Go assembly trampoline for native decoder Rust functions (macOS).
//
// Bridges Go ABI to ARM64 C ABI (Rust extern "C" uses the same ABI).
// Uses B (tail call). The Rust function returns directly to our Go
// caller. NOSPLIT $0-8 means no local stack frame.

#include "textflag.h"

// func vjDecExec(ctx unsafe.Pointer)
// Rust: extern "C" fn vj_dec_exec(ctx: *mut DecExecCtx)
// C ABI: ctx=X0
TEXT ·vjDecExec(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_dec_exec(SB)

// func vjDecResume(ctx unsafe.Pointer)
// Rust: extern "C" fn vj_dec_resume(ctx: *mut DecExecCtx)
// C ABI: ctx=X0
TEXT ·vjDecResume(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_dec_resume(SB)

