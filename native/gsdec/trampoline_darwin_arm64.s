// ARM64 Go assembly trampoline for gsdec C functions (macOS).
//
// Bridges Go ABI to ARM64 C ABI.
// Uses B (tail call). NOSPLIT $0-8 means no local stack frame.

#include "textflag.h"

// func vjGdecExec(ctx unsafe.Pointer)
// C: void vj_gdec_exec(DecExecCtx *ctx)
// C ABI: ctx=X0
TEXT ·vjGdecExec(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_gdec_exec(SB)

// func vjGdecResume(ctx unsafe.Pointer)
// C: void vj_gdec_resume(DecExecCtx *ctx)
// C ABI: ctx=X0
TEXT ·vjGdecResume(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_gdec_resume(SB)
