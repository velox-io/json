//go:build !vj_nondec && !vj_ndeclink

#include "textflag.h"

// Trampolines from Go ABI0 on windows/amd64 to the x86-64 System V C ABI.
//
// The blob is compiled once, for linux/amd64, mapped at runtime, and shared
// by every amd64 OS, so windows reaches it through a Win64 to System V
// bridge: the context pointer moves into RDI instead of RCX. The entries
// return void, so there is no return-value traffic to bridge. Go's amd64 ABI
// is uniform across operating systems, the compiler treats RDI, RSI and every
// XMM as caller save on windows too, so the bridge preserves argument
// placement and RSP alignment, and nothing else.
//
// The prologue drops RSP to a 16-byte boundary through the BP frame. CALL
// then pushes the return address, which leaves the callee with the RSP+8
// alignment that both conventions require.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and calls through it.
//
// stackReserve commits Go stack headroom before the deeper bind entries: the
// canonical linux chain plus the bridge frame (~48B of shadow space and
// alignment slack). Windows also grows the goroutine stack guard
// (stackSystem), so the budget carries margin over the non-windows $512.

TEXT ·stackReserve(SB), $512-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cDomParseCounted(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cDomBuild(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cBindParse(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cBindParseStream(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cWindowScan(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cFmtParse(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cValid(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET
