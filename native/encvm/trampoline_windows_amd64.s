//go:build !vj_noencvm

// AMD64 Go assembly trampolines for native encoder C functions on Windows.
//
// These symbols use Go ABI0. The compiler-generated ABI adapter places ctx
// in its ABI0 stack slot, and the trampoline moves it to RCX for the Windows
// x64 C ABI. The native callee is responsible for preserving the Windows
// nonvolatile registers, including XMM6 through XMM15.
//
// A Windows x64 caller must reserve 32 bytes of register home space and align
// RSP to 16 bytes immediately before CALL, which gives the callee RSP mod 16
// equal to 8 after CALL pushes its return address. The plain tail JMP used by
// the System V trampoline cannot establish this caller frame. Saving the
// incoming stack pointer in BP lets the trampoline round RSP down for the
// native call and restore the exact Go stack pointer afterward.

#include "textflag.h"

TEXT ·stackReserve(SB), $1280-0
	RET

// ---- Full mode ----

// func vjVMExecFull(ctx unsafe.Pointer)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_full_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// ---- Fast mode ----

// func vjVMExecFast(ctx unsafe.Pointer)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_fast_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET

// ---- Compact mode ----

// func vjVMExecCompact(ctx unsafe.Pointer)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL vj_vm_exec_compact_avx2(SB)
	MOVQ BP, SP
	POPQ BP
	RET
