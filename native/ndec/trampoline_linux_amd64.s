#include "textflag.h"

TEXT ·stackReserve(SB), $384-0
    RET

// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_dom_parse_counted(SB)

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_dom_build(SB)

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_bind_parse(SB)

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_fmt_parse(SB)

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_bind_parse_stream(SB)

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_window_scan(SB)

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  ndec_valid(SB)
