// AMD64 Go assembly trampolines for native encoder C functions (Linux).
//
// Bridges Go ABI to x86-64 System V C ABI.
// System V ABI: first arg in RDI, second in RSI, etc.
// On Linux (ELF), C symbols have no underscore prefix — the Go linker
// handles this automatically.
//
// Each ISA has three mode variants: full, compact, and fast.

#include "textflag.h"

// ---- Full mode ----

// func vjVMExecFullSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecFullSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_full_sse42(SB)

// func vjVMExecFullAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecFullAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_full_avx2(SB)

// func vjVMExecFullAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecFullAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_full_avx512(SB)

// ---- Fast mode ----

// func vjVMExecFastSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecFastSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_sse42(SB)

// func vjVMExecFastAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecFastAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_avx2(SB)

// func vjVMExecFastAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecFastAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_avx512(SB)

// ---- Compact mode ----

// func vjVMExecCompactSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_compact_sse42(SB)

// func vjVMExecCompactAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_compact_avx2(SB)

// func vjVMExecCompactAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_compact_avx512(SB)
