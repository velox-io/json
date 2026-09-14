//go:build !vj_nondec && !vj_ndeclink

#include "textflag.h"

// Trampolines from Go ABI0 to ARM64 C ABI for the ndec C entries.
// AAPCS64 and Apple arm64 pass integer arguments identically, the single
// context pointer travels in X0, and the entries return void, so one blob
// built for linux/arm64 serves every arm64 OS with zero adaptation.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and tail-branches through it.
// R16 is the AAPCS64 IP0 scratch register, caller save in both ABIs. The $0
// frame keeps the stack layout identical to a direct branch into the blob,
// so the nosplit chain stack-check budget is unchanged.
//
// stackReserve commits Go stack headroom for the deeper bind entries; the
// canonical blob runs the same frames on every OS, so one budget (developed
// against the linux chain) serves every consumer.

TEXT ·stackReserve(SB), $256-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cDomParseCounted(SB), R16
	B    (R16)

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cDomBuild(SB), R16
	B    (R16)

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cBindParse(SB), R16
	B    (R16)

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cBindParseStream(SB), R16
	B    (R16)

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cWindowScan(SB), R16
	B    (R16)

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cFmtParse(SB), R16
	B    (R16)

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cValid(SB), R16
	B    (R16)
