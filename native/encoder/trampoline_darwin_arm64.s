// ARM64 Go assembly trampoline for native encoder C function.
//
// Bridges Go ABI to ARM64 C ABI. On macOS, Mach-O symbols have _
// prefix but the Go linker handles this automatically — reference
// without prefix here.
//
// Uses B (tail call) like the jsonmarker trampolines. The C function
// returns directly to our Go caller. NOSPLIT $0-8 means no local
// stack frame — the C function allocates its own frame on the
// goroutine stack.

#include "textflag.h"

// func vjEncodeStruct(ctx unsafe.Pointer)
// C: void vj_encode_struct(VjEncodingCtx* ctx)
// C ABI: ctx=X0
TEXT ·vjEncodeStruct(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    vj_encode_struct(SB)
