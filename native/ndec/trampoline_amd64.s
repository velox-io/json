//go:build !windows && !vj_nondec && !vj_ndeclink

#include "textflag.h"

// Trampolines from Go ABI0 to x86-64 System V C ABI for the ndec C entries.
// System V: the single context pointer travels in RDI; the entries return
// void. darwin/amd64 uses System V as well, so it shares this trampoline and
// the blob that linux/amd64 builds.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and tail-jumps through it. The
// $0 frame keeps the stack layout identical to a direct jump into the blob,
// so the nosplit chain stack-check budget is unchanged.
//
// stackReserve commits Go stack headroom for the deeper bind entries; the
// canonical blob runs the same frames on every OS, so one budget (developed
// against the linux chain) serves every consumer.

TEXT ·stackReserve(SB), $384-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cDomParseCounted(SB), AX
	JMP  AX

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cDomBuild(SB), AX
	JMP  AX

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cBindParse(SB), AX
	JMP  AX

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cBindParseStream(SB), AX
	JMP  AX

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cWindowScan(SB), AX
	JMP  AX

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cFmtParse(SB), AX
	JMP  AX

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cValid(SB), AX
	JMP  AX
