//go:build !vj_noencvm && !vj_encvmlink

#include "textflag.h"

// Trampolines from Go ABI0 to ARM64 C ABI for the encvm entries.
// AAPCS64 and Apple arm64 pass integer arguments identically, the single
// context pointer travels in X0, and the entries return void, so one blob
// built for linux/arm64 serves every arm64 OS with zero adaptation.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and tail-branches through it.
// R16 is the AAPCS64 IP0 scratch register, caller save in both ABIs. The $0
// frame keeps the stack layout identical to a direct branch into the blob,
// so the nosplit chain stack-check budget is unchanged.
//
// stackReserve commits Go stack headroom for the exec chains; the canonical
// blob runs the same frames on every OS, so one budget (developed against
// the linux chain) serves every consumer.

TEXT ·stackReserve(SB), $256-0
    RET

// func vjVMExecFull(ctx unsafe.Pointer)
// C: void vj_vm_exec_full(VjExecCtx *ctx)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cVMExecFull(SB), R16
	B    (R16)

// func vjVMExecFast(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast(VjExecCtx *ctx)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cVMExecFast(SB), R16
	B    (R16)

// func vjVMExecCompact(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact(VjExecCtx *ctx)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVD ctx+0(FP), R0
	MOVD ·cVMExecCompact(SB), R16
	B    (R16)
