// ARM64 Go assembly trampoline for the ndec parser entry on darwin/arm64.
//
// Bridges Go ABI -> ARM64 C ABI. The C function ndec_parse_default_neon
// takes a single NdecCtx* argument; C ABI puts the first arg in X0, Go
// ABI passes it on the stack at offset 0.
//
// NOSPLIT $0-8: no Go stack frame allocated. The C function manages its
// own frame on the goroutine stack, subject to the 800-byte nosplit
// budget (1600 with -race). NdecCtx itself is heap-allocated by the
// driver, so the C frame stays well under budget.
//
// B (tail call): ndec_parse_default_neon returns directly to our Go
// caller, skipping a redundant return through the trampoline.

#include "textflag.h"

// func vjNdecParseDefaultNeon(ctx unsafe.Pointer)
// C: void ndec_parse_default_neon(NdecCtx *ctx)
TEXT ·vjNdecParseDefaultNeon(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_parse_default_neon(SB)
