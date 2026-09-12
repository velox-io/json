#include "textflag.h"

TEXT ·stackReserve(SB), $256-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_dom_parse_counted(SB)

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_dom_build(SB)

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_bind_parse(SB)

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B  ndec_fmt_parse(SB)

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_bind_parse_stream(SB)

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_window_scan(SB)

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
// C ABI: ctx=X0
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B    ndec_valid(SB)
