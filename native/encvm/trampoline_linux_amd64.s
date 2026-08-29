//go:build !vj_noencvm

// AMD64 Go assembly trampolines for native encoder C functions (Linux).
//
// Bridges Go ABI to x86-64 System V C ABI.
// System V ABI: first arg in RDI, second in RSI, etc.
// On Linux (ELF), C symbols have no underscore prefix and the Go linker
// handles this automatically.

#include "textflag.h"

// ---- Full mode ----

// func vjVMExecFull(ctx unsafe.Pointer)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_full_avx2(SB)

// ---- Fast mode ----

// func vjVMExecFast(ctx unsafe.Pointer)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast_avx2(SB)

// ---- Compact mode ----

// func vjVMExecCompact(ctx unsafe.Pointer)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_compact_avx2(SB)
