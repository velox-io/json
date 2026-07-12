/*
 * Number encoding public API.
 *
 * NOINLINE entry points for narrow integer types (int8/int16/int32
 * and unsigned counterparts) used by the VM dispatch loop.
 * INLINE write_uint64/write_int64 are defined in itoa.h. */

#ifndef VJ_ENCVM_NUMBER_H
#define VJ_ENCVM_NUMBER_H

#include "itoa.h" // IWYU pragma: keep

/* NOINLINE wrappers — used by the VM for narrow integer opcodes
 * (int8/int16/int32/uint8/uint16/uint32) where the extra function
 * call is acceptable to avoid bloating the dispatch loop. */
NOINLINE int write_uint64_call(uint8_t *buf, uint64_t v);
NOINLINE int write_int64_call(uint8_t *buf, int64_t v);

#endif /* VJ_ENCVM_NUMBER_H */
