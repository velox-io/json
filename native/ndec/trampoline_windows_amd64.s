#include "textflag.h"

// Each trampoline passes the context in CX and reserves the 32-byte Win64
// shadow space before calling C.
// func vjNdecDOMParseCounted(ctx unsafe.Pointer)
// C: void ndec_dom_parse_counted(NdecDomContext *ctx)
TEXT ·vjNdecDOMParseCounted(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_dom_parse_counted(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecDOMBuild(ctx unsafe.Pointer)
// C: void ndec_dom_build(NdecDomContext *ctx)
TEXT ·vjNdecDOMBuild(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_dom_build(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecBindParse(ctx unsafe.Pointer)
// C: void ndec_bind_parse(NdecBindContext *ctx)
TEXT ·vjNdecBindParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_bind_parse(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecFmtParse(ctx unsafe.Pointer)
// C: void ndec_fmt_parse(NdecFmtContext *ctx)
TEXT ·vjNdecFmtParse(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_fmt_parse(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecBindParseStream(ctx unsafe.Pointer)
// C: void ndec_bind_parse_stream(NdecBindMachine *ctx)
TEXT ·vjNdecBindParseStream(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_bind_parse_stream(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecWindowScan(ctx unsafe.Pointer)
// C: void ndec_window_scan(NdecWindowScanCtx *ctx)
TEXT ·vjNdecWindowScan(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_window_scan(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjNdecValid(ctx unsafe.Pointer)
// C: void ndec_valid(NdecValidContext *ctx)
TEXT ·vjNdecValid(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_valid(SB)
	MOVQ BP, SP
	POPQ BP
	RET
