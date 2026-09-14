//go:build windows && !vj_noencvm && !vj_encvmlink

#include "textflag.h"

// Trampolines from Go ABI0 on windows/amd64 to the x86-64 System V C ABI.
//
// The blob is compiled once, for linux/amd64, mapped at runtime, and shared
// by every amd64 OS, so windows reaches it through a Win64 to System V
// bridge: the context pointer moves into RDI instead of RCX. The entries
// return void, so there is no return-value traffic to bridge. Go's amd64 ABI
// is uniform across operating systems, the compiler treats RDI, RSI and every
// XMM as caller save on windows too, so the bridge preserves argument
// placement and RSP alignment, and nothing else.
//
// The prologue drops RSP to a 16-byte boundary through the BP frame. CALL
// then pushes the return address, which leaves the callee with the RSP+8
// alignment that both conventions require.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and calls through it.
//
// stackReserve commits Go stack headroom for the exec chains: the canonical
// linux chain plus the bridge frame (~48B of shadow space and alignment
// slack). Windows also grows the goroutine stack guard (stackSystem), so the
// budget carries margin over the non-windows $384.

TEXT ·stackReserve(SB), $512-0
    RET

// func vjVMExecFull(ctx unsafe.Pointer)
// C: void vj_vm_exec_full(VjExecCtx *ctx)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecFull(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecFast(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast(VjExecCtx *ctx)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecFast(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET

// func vjVMExecCompact(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact(VjExecCtx *ctx)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	MOVQ ·cVMExecCompact(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	RET
