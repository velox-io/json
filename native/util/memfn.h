/*
 * SIMD-accelerated memory copy helpers.
 *
 * Static inline copy functions for 0-128 bytes, plus the cross-ISA
 * 16-byte load/store wrappers they build on. On aarch64 the wrappers
 * map to vld1q_u8 / vst1q_u8; on x86 they fold to the matching SSE2
 * intrinsic. Together with vj_v16u8 they let the hot paths below
 * compile unchanged on both ISAs.
 */

#ifndef VJ_UTIL_MEMFN_H
#define VJ_UTIL_MEMFN_H

#include <stdint.h>

#ifdef __aarch64__
#include <arm_neon.h>
#else
#include <immintrin.h>
#endif

#ifdef __aarch64__
static inline uint8x16_t vj_load16(const void *p) {
  return vld1q_u8((const uint8_t *)p);
}
static inline void vj_store16(void *p, uint8x16_t v) {
  vst1q_u8((uint8_t *)p, v);
}
typedef uint8x16_t vj_v16u8;
#else
static inline __m128i vj_load16(const void *p) {
  return _mm_loadu_si128((const __m128i *)p);
}
static inline void vj_store16(void *p, __m128i v) {
  _mm_storeu_si128((__m128i *)p, v);
}
typedef __m128i vj_v16u8;
#endif

/* Inline word-sized copies for 0-15 bytes. Avoids function-call
 * overhead of memcpy for these common small sizes. Uses
 * __builtin_memcpy with compile-time-constant sizes so the compiler
 * emits optimal load/store pairs (never a _memcpy call). */
static inline void copy_small(uint8_t *dst, const uint8_t *src, int n) {
  if (n >= 8) {
    __builtin_memcpy(dst, src, 8);
    dst += 8;
    src += 8;
    n -= 8;
  }
  if (n >= 4) {
    __builtin_memcpy(dst, src, 4);
    dst += 4;
    src += 4;
    n -= 4;
  }
  if (n >= 2) {
    __builtin_memcpy(dst, src, 2);
    dst += 2;
    src += 2;
    n -= 2;
  }
  if (n) {
    *dst = *src;
  }
}

#if defined(__SSE2__) || defined(__aarch64__)

/* vj_copy_key: optimized for WRITE_KEY (the #1 hot call site).
 * Typical key lengths: 4-32 bytes (JSON `"field_name":`).
 * Uses overlapping SIMD loads to avoid branching on exact size.
 * Always inlined: no function call overhead. */
static inline void vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
  if (n <= 8) {
    copy_small(dst, (const uint8_t *)src, (int)n);
    return;
  }
  if (n <= 16) {
    /* Overlapping 8-byte copies: first 8 + last 8 */
    __builtin_memcpy(dst, src, 8);
    __builtin_memcpy(dst + n - 8, src + n - 8, 8);
    return;
  }
  if (n <= 32) {
    /* Overlapping 16-byte SIMD copies */
    vj_v16u8 v0 = vj_load16(src);
    vj_v16u8 v1 = vj_load16(src + n - 16);
    vj_store16(dst, v0);
    vj_store16(dst + n - 16, v1);
    return;
  }
  /* n > 32: rare for keys, loop with 16-byte SIMD + overlapping tail */
  uint16_t i = 0;
  for (; i + 16 <= n; i += 16) {
    vj_store16(dst + i, vj_load16(src + i));
  }
  if (i < n) {
    vj_store16(dst + n - 16, vj_load16(src + n - 16));
  }
}

/* vj_copy_var: general-purpose inline copy for variable-size data.
 * Used for OP_RAW_MESSAGE, OP_NUMBER, integer digit output, etc.
 * Handles up to 128 bytes inline; falls through to _memcpy for larger. */
static inline void vj_copy_var(uint8_t *dst, const void *src, uint64_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 8) {
    copy_small(dst, s, (int)n);
    return;
  }
  if (n <= 16) {
    __builtin_memcpy(dst, s, 8);
    __builtin_memcpy(dst + n - 8, s + n - 8, 8);
    return;
  }
  if (n <= 32) {
    vj_v16u8 v0 = vj_load16(s);
    vj_v16u8 v1 = vj_load16(s + n - 16);
    vj_store16(dst, v0);
    vj_store16(dst + n - 16, v1);
    return;
  }
  if (n <= 64) {
    /* 2x overlapping 16-byte: first 32 + last 32 */
    vj_v16u8 a0 = vj_load16(s);
    vj_v16u8 a1 = vj_load16(s + 16);
    vj_v16u8 b0 = vj_load16(s + n - 32);
    vj_v16u8 b1 = vj_load16(s + n - 16);
    vj_store16(dst, a0);
    vj_store16(dst + 16, a1);
    vj_store16(dst + n - 32, b0);
    vj_store16(dst + n - 16, b1);
    return;
  }
  if (n <= 128) {
    /* 4x overlapping 16-byte: first 64 + last 64 */
    vj_v16u8 a0 = vj_load16(s);
    vj_v16u8 a1 = vj_load16(s + 16);
    vj_v16u8 a2 = vj_load16(s + 32);
    vj_v16u8 a3 = vj_load16(s + 48);
    vj_v16u8 b0 = vj_load16(s + n - 64);
    vj_v16u8 b1 = vj_load16(s + n - 48);
    vj_v16u8 b2 = vj_load16(s + n - 32);
    vj_v16u8 b3 = vj_load16(s + n - 16);
    vj_store16(dst, a0);
    vj_store16(dst + 16, a1);
    vj_store16(dst + 32, a2);
    vj_store16(dst + 48, a3);
    vj_store16(dst + n - 64, b0);
    vj_store16(dst + n - 48, b1);
    vj_store16(dst + n - 32, b2);
    vj_store16(dst + n - 16, b3);
    return;
  }
  /* > 128 bytes: fall through to _memcpy (call overhead negligible) */
  __builtin_memcpy(dst, src, n);
}

#else /* No SIMD: scalar fallback */

static inline void vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 15) {
    copy_small(dst, s, (int)n);
    return;
  }
  /* Word loop + tail for larger keys (rare without SIMD) */
  while (n >= 8) {
    __builtin_memcpy(dst, s, 8);
    dst += 8;
    s += 8;
    n -= 8;
  }
  copy_small(dst, s, (int)n);
}

static inline void vj_copy_var(uint8_t *dst, const void *src, uint64_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 15) {
    copy_small(dst, s, (int)n);
    return;
  }
  /* Fall through to _memcpy for larger copies */
  __builtin_memcpy(dst, src, n);
}

#endif /* SIMD check */

#endif /* VJ_UTIL_MEMFN_H */
