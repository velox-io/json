/*
 * memfn.h — Velox JSON C Engine: Memory Primitives
 *
 * SIMD-accelerated inline copy helpers used throughout the encoder.
 * Depends on stdlib/memory.h for memcpy/memset declarations.
 */

#ifndef VJ_ENCVM_MEMFN_H
#define VJ_ENCVM_MEMFN_H

#include <stdint.h>

void copy_small(uint8_t *dst, const uint8_t *src, int n);
void vj_copy_key(uint8_t *dst, const char *src, uint16_t n);
void vj_copy_var(uint8_t *dst, const void *src, uint64_t n);

#endif /* VJ_ENCVM_MEMFN_H */
