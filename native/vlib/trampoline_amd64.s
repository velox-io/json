//go:build !windows && !vj_nolookup

#include "textflag.h"

// Trampolines from Go ABI0 to x86-64 System V C ABI for the ndec_lookup C API.
// System V: args in RDI, RSI, RDX; return value in RAX. darwin/amd64 uses
// System V as well, so it shares this trampoline and the blob that
// linux/amd64 builds. Non-void APIs build a real frame so CALL can preserve
// alignment; then move RAX into the caller's return slot at NAME+off(FP).
//
// The blob is mapped at runtime, not linked, so each trampoline loads its
// entry point from a c* function-pointer var and calls through it.

// func vjLookupSizeFor(cfg *Config) uintptr
// C: size_t ndec_lookup_size_for(const ndec_lookup_config *cfg)
TEXT ·vjLookupSizeFor(SB), NOSPLIT, $16-16
	MOVQ cfg+0(FP), DI
	MOVQ ·cLookupSizeFor(SB), AX
	CALL AX
	MOVQ AX, ret+8(FP)
	RET

// func vjLookupScratchSize() uintptr
// C: size_t ndec_lookup_scratch_size(void)
TEXT ·vjLookupScratchSize(SB), NOSPLIT, $16-8
	MOVQ ·cLookupScratchSize(SB), AX
	CALL AX
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
	CALL AX
	MOVL AX, ret+24(FP)
	RET

// func vjLookupGetTier(storage unsafe.Pointer) uint32
// C: ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l)
TEXT ·vjLookupGetTier(SB), NOSPLIT, $16-12
	MOVQ storage+0(FP), DI
	MOVQ ·cLookupGetTier(SB), AX
	CALL AX
	MOVL AX, ret+8(FP)
	RET

// func vjLookupFootprint(storage unsafe.Pointer) uintptr
// C: size_t ndec_lookup_footprint(const ndec_lookup *l)
TEXT ·vjLookupFootprint(SB), NOSPLIT, $16-16
	MOVQ storage+0(FP), DI
	MOVQ ·cLookupFootprint(SB), AX
	CALL AX
	MOVQ AX, ret+8(FP)
	RET
