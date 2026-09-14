//go:build arm64 && vj_ndeclink && !vj_nondec

#include "textflag.h"

// Linked-syso trampolines, used only by instrumentation PGO collection (see
// ndec_init_linked.go). The instrumented artifact is linked by Go's linker,
// so these branch straight to the exported symbols instead of through
// function pointers into a mapped blob.
//
// stackReserve commits Go stack headroom for the deeper bind entries; the
// instrumented C frames only grow past the production ones.

TEXT ·stackReserve(SB), $256-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_dom_parse_counted(SB)

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_dom_build(SB)

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_bind_parse(SB)

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_bind_parse_stream(SB)

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_window_scan(SB)

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_fmt_parse(SB)

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_valid(SB)
