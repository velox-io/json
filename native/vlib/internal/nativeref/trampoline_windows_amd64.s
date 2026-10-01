//go:build !vj_nolookup

#include "textflag.h"

// Trampolines from Go ABI0 on windows/amd64 to the x86-64 System V C ABI.
//
// The blob is compiled once, for linux/amd64, mapped at runtime, and shared
// by every amd64 OS, so windows reaches it through a Win64 to System V
// bridge: arguments move into RDI, RSI, RDX instead of RCX, RDX, R8, and
// RAX carries the return value in both conventions. Go's amd64 ABI is
// uniform across operating systems, the compiler treats RDI, RSI and every
// XMM as caller save on windows too, so the bridge preserves argument
// placement and RSP alignment, and nothing else.
//
// The prologue drops RSP to a 16-byte boundary through the BP frame. CALL
// then pushes the return address, which leaves the callee with the RSP+8
// alignment that both conventions require.
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and calls through it.

// func vjLookupSizeFor(cfg *Config) uintptr
// C: size_t ndec_lookup_size_for(const ndec_lookup_config *cfg)
TEXT ·vjLookupSizeFor(SB), NOSPLIT, $16-16
	MOVQ cfg+0(FP), DI
	MOVQ ·cLookupSizeFor(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+8(FP)
	RET

// func vjLookupScratchSize() uintptr
// C: size_t ndec_lookup_scratch_size(void)
TEXT ·vjLookupScratchSize(SB), NOSPLIT, $16-8
	MOVQ ·cLookupScratchSize(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+0(FP)
	RET

// func vjLookupInit(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32
// C: int ndec_lookup_init(ndec_lookup *storage, size_t storage_size,
//                         const ndec_lookup_config *cfg)
TEXT ·vjLookupInit(SB), NOSPLIT, $16-28
	MOVQ storage+0(FP), DI
	MOVQ storageSize+8(FP), SI
	MOVQ cfg+16(FP), DX
	MOVQ ·cLookupInit(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	MOVL AX, ret+24(FP)
	RET

// func vjLookupGetTier(storage unsafe.Pointer) uint32
// C: ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l)
TEXT ·vjLookupGetTier(SB), NOSPLIT, $16-12
	MOVQ storage+0(FP), DI
	MOVQ ·cLookupGetTier(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	MOVL AX, ret+8(FP)
	RET

// func vjLookupFootprint(storage unsafe.Pointer) uintptr
// C: size_t ndec_lookup_footprint(const ndec_lookup *l)
TEXT ·vjLookupFootprint(SB), NOSPLIT, $16-16
	MOVQ storage+0(FP), DI
	MOVQ ·cLookupFootprint(SB), AX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL AX
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+8(FP)
	RET
