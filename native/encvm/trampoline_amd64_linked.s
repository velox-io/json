//go:build amd64 && !windows && vj_encvmlink && !vj_noencvm

#include "textflag.h"

// Linked-syso trampolines, used only by instrumentation PGO collection (see
// encvm_init_linked.go). The instrumented artifact is linked by Go's linker,
// so these jump straight to the exported symbols instead of through
// function pointers into a mapped blob.
//
// stackReserve commits Go stack headroom for the exec chains; the
// instrumented C frames only grow past the production ones.

TEXT ·stackReserve(SB), $384-0
    RET

// func vjVMExecFull(ctx unsafe.Pointer)
// C: void vj_vm_exec_full(VjExecCtx *ctx)
TEXT ·vjVMExecFull(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_full(SB)

// func vjVMExecFast(ctx unsafe.Pointer)
// C: void vj_vm_exec_fast(VjExecCtx *ctx)
TEXT ·vjVMExecFast(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_fast(SB)

// func vjVMExecCompact(ctx unsafe.Pointer)
// C: void vj_vm_exec_compact(VjExecCtx *ctx)
TEXT ·vjVMExecCompact(SB), NOSPLIT, $0-8
	MOVQ ctx+0(FP), DI
	JMP  vj_vm_exec_compact(SB)
