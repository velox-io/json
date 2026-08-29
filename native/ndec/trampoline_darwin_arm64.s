//go:build !vj_nondec

#include "textflag.h"

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

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	B  ndec_fmt_parse(SB)
