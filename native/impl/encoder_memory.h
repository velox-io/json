/*
 * encoder_memory.h — Velox JSON C Engine: Memory Primitives
 *
 * Custom memcpy/memset implementations (to avoid libc dependencies in the
 * .syso object) and SIMD-accelerated inline copy helpers used throughout
 * the encoder.
 *
 * Included first by encoder_types.h — no dependencies on other encoder_*.h
 * files.  Only depends on <stddef.h>, <stdint.h>, and SIMD headers.
 */

#ifndef VJ_ENCODER_MEMORY_H
#define VJ_ENCODER_MEMORY_H

// clang-format off

#include <stdint.h>

#ifdef __aarch64__
#include "sse2neon.h"
#else
#include <immintrin.h>
#endif

/* ================================================================
 *  memcpy / memset — custom implementations
 *
 *  The .syso object must not reference libc.  We provide hidden-
 *  visibility implementations that the compiler resolves via
 *  __asm__ symbol renaming.
 *
 *  The actual function bodies live in encoder_memfn.c (compiled once,
 *  without ISA flags) to avoid duplicate symbols when multiple ISA
 *  objects are linked into a single .syso.
 * ================================================================ */

/* Platform-specific symbol naming:
 * macOS Mach-O: C symbols have _ prefix (_memcpy, _memset)
 * Linux ELF:    C symbols have no prefix (memcpy, memset) */
#if defined(__APPLE__)
  #define VJ_MEMCPY_SYM "_memcpy"
  #define VJ_MEMSET_SYM "_memset"
#else
  #define VJ_MEMCPY_SYM "memcpy"
  #define VJ_MEMSET_SYM "memset"
#endif

/* Declarations — always visible so each ISA TU can link against
 * the single memcpy/memset compiled from encoder_memfn.c. */
__attribute__((visibility("hidden"))) void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src,
               size_t n) __asm__(VJ_MEMCPY_SYM);

__attribute__((visibility("hidden"))) void *
vj_memset_impl(void *dst, int c, size_t n) __asm__(VJ_MEMSET_SYM);


/* ================================================================
 *  Small copy helper
 *
 *  Inline word-sized copies for 0-15 bytes.  Avoids function-call
 *  overhead of memcpy for these common small sizes.  Uses
 *  __builtin_memcpy with compile-time-constant sizes so the compiler
 *  emits optimal load/store pairs (never a _memcpy call).
 * ================================================================ */

static __attribute__((always_inline)) inline void
copy_small(uint8_t *dst, const uint8_t *src, int n) {
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

/* vj_copy_key — optimized for WRITE_KEY (the #1 hot call site).
 * Typical key lengths: 4-32 bytes (JSON `"field_name":`).
 * Uses overlapping SIMD loads to avoid branching on exact size.
 * Always inlined — no function call overhead. */
static __attribute__((always_inline)) inline void
vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
  if (__builtin_expect(n <= 8, 1)) {
    copy_small(dst, (const uint8_t *)src, (int)n);
    return;
  }
  if (__builtin_expect(n <= 16, 1)) {
    /* Overlapping 8-byte copies: first 8 + last 8 */
    __builtin_memcpy(dst, src, 8);
    __builtin_memcpy(dst + n - 8, src + n - 8, 8);
    return;
  }
  if (__builtin_expect(n <= 32, 1)) {
    /* Overlapping 16-byte SIMD copies */
    __m128i v0 = _mm_loadu_si128((const __m128i *)src);
    __m128i v1 = _mm_loadu_si128((const __m128i *)(src + n - 16));
    _mm_storeu_si128((__m128i *)dst, v0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), v1);
    return;
  }
  /* n > 32: rare for keys — loop with 16-byte SIMD + overlapping tail */
  uint16_t i = 0;
  for (; i + 16 <= n; i += 16) {
    __m128i v = _mm_loadu_si128((const __m128i *)(src + i));
    _mm_storeu_si128((__m128i *)(dst + i), v);
  }
  if (i < n) {
    __m128i v = _mm_loadu_si128((const __m128i *)(src + n - 16));
    _mm_storeu_si128((__m128i *)(dst + n - 16), v);
  }
}

/* vj_copy_var — general-purpose inline copy for variable-size data.
 * Used for OP_RAW_MESSAGE, OP_NUMBER, integer digit output, etc.
 * Handles up to 128 bytes inline; falls through to _memcpy for larger. */
static __attribute__((always_inline)) inline void
vj_copy_var(uint8_t *dst, const void *src, size_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (__builtin_expect(n <= 8, 1)) {
    copy_small(dst, s, (int)n);
    return;
  }
  if (__builtin_expect(n <= 16, 1)) {
    __builtin_memcpy(dst, s, 8);
    __builtin_memcpy(dst + n - 8, s + n - 8, 8);
    return;
  }
  if (__builtin_expect(n <= 32, 1)) {
    __m128i v0 = _mm_loadu_si128((const __m128i *)s);
    __m128i v1 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, v0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), v1);
    return;
  }
  if (__builtin_expect(n <= 64, 1)) {
    /* 2x overlapping 16-byte: first 32 + last 32 */
    __m128i a0 = _mm_loadu_si128((const __m128i *)s);
    __m128i a1 = _mm_loadu_si128((const __m128i *)(s + 16));
    __m128i b0 = _mm_loadu_si128((const __m128i *)(s + n - 32));
    __m128i b1 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, a0);
    _mm_storeu_si128((__m128i *)(dst + 16), a1);
    _mm_storeu_si128((__m128i *)(dst + n - 32), b0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), b1);
    return;
  }
  if (__builtin_expect(n <= 128, 1)) {
    /* 4x overlapping 16-byte: first 64 + last 64 */
    __m128i a0 = _mm_loadu_si128((const __m128i *)s);
    __m128i a1 = _mm_loadu_si128((const __m128i *)(s + 16));
    __m128i a2 = _mm_loadu_si128((const __m128i *)(s + 32));
    __m128i a3 = _mm_loadu_si128((const __m128i *)(s + 48));
    __m128i b0 = _mm_loadu_si128((const __m128i *)(s + n - 64));
    __m128i b1 = _mm_loadu_si128((const __m128i *)(s + n - 48));
    __m128i b2 = _mm_loadu_si128((const __m128i *)(s + n - 32));
    __m128i b3 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, a0);
    _mm_storeu_si128((__m128i *)(dst + 16), a1);
    _mm_storeu_si128((__m128i *)(dst + 32), a2);
    _mm_storeu_si128((__m128i *)(dst + 48), a3);
    _mm_storeu_si128((__m128i *)(dst + n - 64), b0);
    _mm_storeu_si128((__m128i *)(dst + n - 48), b1);
    _mm_storeu_si128((__m128i *)(dst + n - 32), b2);
    _mm_storeu_si128((__m128i *)(dst + n - 16), b3);
    return;
  }
  /* > 128 bytes: fall through to _memcpy (call overhead negligible) */
  __builtin_memcpy(dst, src, n);
}

#else /* No SIMD — scalar fallback */

static __attribute__((always_inline)) inline void
vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
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

static __attribute__((always_inline)) inline void
vj_copy_var(uint8_t *dst, const void *src, size_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 15) {
    copy_small(dst, s, (int)n);
    return;
  }
  /* Fall through to _memcpy for larger copies */
  __builtin_memcpy(dst, src, n);
}

#endif /* SIMD check */

#endif /* VJ_ENCODER_MEMORY_H */
