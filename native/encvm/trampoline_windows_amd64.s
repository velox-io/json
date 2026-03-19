// AMD64 Go assembly trampolines for native encoder C functions (Windows).
//
// Bridges Go ABI to Windows x64 C ABI.
// Windows x64 ABI: first arg in RCX; callee-saved XMM6-XMM15.
//
// Why not JMP tail-call (like the Linux trampoline)?
//
// Go's ABI wrapper leaves RSP 16-byte aligned (mod 16 == 0) at our entry.
// Windows x64 C functions expect RSP mod 16 == 8 at entry (return address
// already pushed), and their prologues rely on this to align XMM save areas
// with movaps. A JMP preserves mod 16 == 0, causing movaps to fault.
// On Linux this isn't a problem because System V ABI doesn't require
// callee-saved XMM registers, so the alignment mismatch is harmless.
// We must use CALL to push a return address and fix the alignment.
//
// Trampoline strategy:
//   PUSHQ BP / SUBQ $32 / ANDQ $~15 — shadow space + 16-byte alignment
//   CALL target
//   MOVQ BP, SP / POPQ BP / RET — restore via saved frame pointer
//
// Each ISA has three mode variants: full, compact, and fast.

#include "textflag.h"

// ---- Full mode ----

// func vjVMExecFullSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecFullSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_full_sse42(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecFullAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecFullAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_full_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecFullAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecFullAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_full_avx512(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// ---- Fast mode ----

// func vjVMExecFastSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecFastSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_fast_sse42(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecFastAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecFastAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_fast_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecFastAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecFastAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_fast_avx512(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// ---- Compact mode ----

// func vjVMExecCompactSSE42(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactSSE42(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_compact_sse42(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecCompactAVX2(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactAVX2(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_compact_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecCompactAVX512(ctx unsafe.Pointer)
TEXT ·vjVMExecCompactAVX512(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_compact_avx512(SB)
	MOVQ BP, SP
	POPQ BP
	RET
