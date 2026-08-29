//go:build !vj_nolookup

#include "textflag.h"

// Trampolines from Go ABI0 to Win64 C ABI. Args in RCX, RDX, R8; return in
// RAX. Each entry reserves 32B shadow space and restores RSP through the BP
// frame, matching the ndec windows bridge.

// func vjLookupSizeFor(cfg *Config) uintptr
// C: size_t ndec_lookup_size_for(const ndec_lookup_config *cfg)
TEXT ·vjLookupSizeFor(SB), NOSPLIT, $16-16
	MOVQ cfg+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_lookup_size_for(SB)
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+8(FP)
	RET

// func vjLookupScratchSize() uintptr
// C: size_t ndec_lookup_scratch_size(void)
TEXT ·vjLookupScratchSize(SB), NOSPLIT, $16-8
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_lookup_scratch_size(SB)
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+0(FP)
	RET

// func vjLookupInit(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32
// C: int ndec_lookup_init(ndec_lookup *storage, size_t storage_size,
//                         const ndec_lookup_config *cfg)
TEXT ·vjLookupInit(SB), NOSPLIT, $16-28
	MOVQ storage+0(FP), CX
	MOVQ storageSize+8(FP), DX
	MOVQ cfg+16(FP), R8
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_lookup_init(SB)
	MOVQ BP, SP
	POPQ BP
	MOVL AX, ret+24(FP)
	RET

// func vjLookupGetTier(storage unsafe.Pointer) uint32
// C: ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l)
TEXT ·vjLookupGetTier(SB), NOSPLIT, $16-12
	MOVQ storage+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_lookup_get_tier(SB)
	MOVQ BP, SP
	POPQ BP
	MOVL AX, ret+8(FP)
	RET

// func vjLookupFootprint(storage unsafe.Pointer) uintptr
// C: size_t ndec_lookup_footprint(const ndec_lookup *l)
TEXT ·vjLookupFootprint(SB), NOSPLIT, $16-16
	MOVQ storage+0(FP), CX
	PUSHQ BP
	MOVQ SP, BP
	SUBQ $32, SP
	ANDQ $~15, SP
	CALL ndec_lookup_footprint(SB)
	MOVQ BP, SP
	POPQ BP
	MOVQ AX, ret+8(FP)
	RET
