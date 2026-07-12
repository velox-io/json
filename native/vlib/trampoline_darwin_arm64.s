#include "textflag.h"

// Trampolines from Go ABI0 to ARM64 C ABI for the ndec_lookup C API.
// Non-void APIs need a real frame: BL to invoke, then move X0 into the
// caller's return slot at NAME+off(FP).

// func vjLookupSizeFor(cfg *Config) uintptr
// C: size_t ndec_lookup_size_for(const ndec_lookup_config *cfg)
// Args: cfg=X0. Ret: X0.
TEXT ·vjLookupSizeFor(SB), NOSPLIT, $16-16
	MOVD cfg+0(FP), R0
	BL   ndec_lookup_size_for(SB)
	MOVD R0, ret+8(FP)
	RET

// func vjLookupScratchSize() uintptr
// C: size_t ndec_lookup_scratch_size(void)
// Ret: X0.
TEXT ·vjLookupScratchSize(SB), NOSPLIT, $16-8
	BL   ndec_lookup_scratch_size(SB)
	MOVD R0, ret+0(FP)
	RET

// func vjLookupInit(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32
// C: int ndec_lookup_init(ndec_lookup *storage, size_t storage_size,
//                         const ndec_lookup_config *cfg)
// Args: X0=storage, X1=storageSize, X2=cfg. Ret: X0 (int -> low 32 bits).
TEXT ·vjLookupInit(SB), NOSPLIT, $16-28
	MOVD storage+0(FP), R0
	MOVD storageSize+8(FP), R1
	MOVD cfg+16(FP), R2
	BL   ndec_lookup_init(SB)
	MOVW R0, ret+24(FP)
	RET

// func vjLookupGetTier(storage unsafe.Pointer) uint32
// C: ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l)
// Args: X0=storage. Ret: X0 (int -> low 32 bits).
TEXT ·vjLookupGetTier(SB), NOSPLIT, $16-12
	MOVD storage+0(FP), R0
	BL   ndec_lookup_get_tier(SB)
	MOVW R0, ret+8(FP)
	RET

// func vjLookupFootprint(storage unsafe.Pointer) uintptr
// C: size_t ndec_lookup_footprint(const ndec_lookup *l)
// Args: X0=storage. Ret: X0.
TEXT ·vjLookupFootprint(SB), NOSPLIT, $16-16
	MOVD storage+0(FP), R0
	BL   ndec_lookup_footprint(SB)
	MOVD R0, ret+8(FP)
	RET
