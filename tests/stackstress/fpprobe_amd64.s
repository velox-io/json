//go:build vjstackstress

#include "textflag.h"

// func ucomisdFlags(a, b *[2]float64) (flagsF, flagsG uint64)
TEXT ·ucomisdFlags(SB), NOSPLIT, $0-32
	MOVQ	a+0(FP), AX
	MOVQ	b+8(FP), BX
	MOVSD	0(AX), X0
	MOVSD	0(BX), X1
	UCOMISD	X1, X0
	PUSHFQ
	POPQ	CX
	MOVQ	CX, flagsF+16(FP)
	MOVSD	8(AX), X0
	MOVSD	8(BX), X1
	UCOMISD	X1, X0
	PUSHFQ
	POPQ	CX
	MOVQ	CX, flagsG+24(FP)
	RET

// func stmxcsr() uint32
TEXT ·stmxcsr(SB), NOSPLIT, $0-4
	STMXCSR	ret+0(FP)
	RET
