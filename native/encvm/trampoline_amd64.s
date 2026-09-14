//go:build !windows && !vj_noencvm && !vj_encvmlink

#include "textflag.h"

// Trampolines from Go ABI0 to x86-64 System V C ABI for the encvm entries.
// System V: the single context pointer travels in RDI; the entries return
// void. darwin/amd64 uses System V as well, so it shares this trampoline and
// the blob that linux/amd64 builds.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and tail-jumps through it. The
// $0 frame keeps the stack layout identical to a direct jump into the blob,
// so the nosplit chain stack-check budget is unchanged.
//
// stackReserve commits Go stack headroom for the exec chains; the canonical
// blob runs the same frames on every OS, so one budget (developed against
// the linux chain) serves every consumer.

TEXT ·stackReserve(SB), $384-0
    RET

// func vjVMExecFull(ctx unsafe.Pointer)
// C: void vj_vm_exec_full(VjExecCtx *ctx)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecFull(SB), AX
	JMP  AX

// func vjVMExecFast(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast(VjExecCtx *ctx)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecFast(SB), AX
	JMP  AX

// func vjVMExecCompact(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact(VjExecCtx *ctx)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecCompact(SB), AX
	JMP  AX
