/*
 * Integer-to-ASCII conversion using range-based dispatch
 *
 * Pure algorithm: SSE2 parallel digit extraction for large numbers,
 * digit-pair table lookups for medium/small numbers, and scalar
 * fallback for non-SIMD builds.
 *
 * Small (0..9999):       direct digit-pair table lookups
 * Medium (10^4..10^8):   divide-by-10000 + table lookups
 * Large  (10^8..10^16):  SSE2 parallel 8-digit conversion + PSHUFB
 * XLarge (>= 10^16):     scalar prefix + SSE2 for remaining 16 digits
 */

#ifndef VJ_ENCVM_ITOA_H
#define VJ_ENCVM_ITOA_H

#include "util/memfn.h" // IWYU pragma: keep
#include "tables.h"

/* SSE2 constants for itoa8 */

#if defined(__aarch64__)

/* uint16 mulhi: take the high 16 bits of each 16-bit×16-bit product. */
INLINE uint16x8_t vj_mulhi_u16_neon(uint16x8_t a, uint16x8_t b) {
  uint32x4_t lo = vmull_u16(vget_low_u16(a), vget_low_u16(b));
  uint32x4_t hi = vmull_high_u16(a, b);
  return vcombine_u16(vshrn_n_u32(lo, 16), vshrn_n_u32(hi, 16));
}

/* Convert an 8-digit number (0..99999999) to 8 ASCII digit values
 * spread across the 8 lanes of a uint16x8.  Splits into hi/lo
 * (top 4, bottom 4 digits), extracts each digit via fixed-point
 * reciprocal multiplies. */
INLINE uint16x8_t vj_itoa8_neon(uint32_t v) {
  /* hi = v / 10000, lo = v % 10000 via fixed-point reciprocal. */
  uint32_t hi = (uint32_t)(((uint64_t)v * 0xd1b71759ULL) >> 45);
  uint32_t lo = v - hi * 10000;

  /* {hi*4, hi*4, hi*4, hi*4, lo*4, lo*4, lo*4, lo*4} */
  uint16x4_t hi_x4 = vdup_n_u16((uint16_t)(hi << 2));
  uint16x4_t lo_x4 = vdup_n_u16((uint16_t)(lo << 2));
  uint16x8_t v08   = vcombine_u16(hi_x4, lo_x4);

  /* Per-lane reciprocal multiplies extract each decimal digit. */
  const uint16x8_t divPowers   = {0x20c5, 0x147b, 0x3334, 0x8000, 0x20c5, 0x147b, 0x3334, 0x8000};
  const uint16x8_t shiftPowers = {0x0080, 0x0800, 0x2000, 0x8000, 0x0080, 0x0800, 0x2000, 0x8000};

  uint16x8_t v09 = vj_mulhi_u16_neon(v08, divPowers);
  uint16x8_t v10 = vj_mulhi_u16_neon(v09, shiftPowers);
  uint16x8_t v11 = vmulq_u16(v10, vdupq_n_u16(10));

  /* Shift each 16-bit lane up one position within its 64-bit half
   * (lane[0] and lane[4] become 0). */
  uint16x8_t v12 = vreinterpretq_u16_u64(vshlq_n_u64(vreinterpretq_u64_u16(v11), 16));
  uint16x8_t v13 = vsubq_u16(v10, v12);
  return v13;
}

/* Large: 10^8 <= val < 10^16.  Produces up to 16 digits via two itoa8 calls.
 * Leading zeros are stripped using TBL. */
INLINE int vj_u64toa_large_neon(uint8_t *out, uint64_t val) {
  uint32_t a = (uint32_t)(val / 100000000);
  uint32_t b = (uint32_t)(val % 100000000);

  uint16x8_t d0 = vj_itoa8_neon(a);
  uint16x8_t d1 = vj_itoa8_neon(b);

  /* Pack 16-bit digits to 8-bit and add '0'.  Digits are 0..9, so
   * vqmovn_u16 saturates only on logic bugs (never in normal use). */
  uint8x16_t packed = vcombine_u8(vqmovn_u16(d0), vqmovn_u16(d1));
  uint8x16_t ascii  = vaddq_u8(packed, vdupq_n_u8('0'));

  /* Count leading zeros via nibble-mask scan.  ~nm | top-nibble forces
   * at least one non-zero digit so the all-zero input returns nd=15. */
  uint8x16_t eq0 = vceqq_u8(ascii, vdupq_n_u8('0'));
  uint64_t nm    = vget_lane_u64(vreinterpret_u64_u8(vshrn_n_u16(vreinterpretq_u16_u8(eq0), 4)), 0);
  uint64_t inv   = ~nm | 0xF000000000000000ULL;
  uint32_t nd    = __builtin_ctzll(inv) >> 2;

  /* Shift digits left to remove leading zeros.  vqtbl1q_u8 returns 0
   * for indices >= 16 (VEC_SHIFT_SHUFFLES uses 0xff for skip slots). */
  uint8x16_t shuf   = vld1q_u8(&VEC_SHIFT_SHUFFLES[nd * 16]);
  uint8x16_t result = vqtbl1q_u8(ascii, shuf);

  vst1q_u8(out, result);
  return 16 - (int)nd;
}

/* Extra-large: val >= 10^16.  Scalar prefix (1-4 digits) + SIMD for last 16. */
INLINE int vj_u64toa_xlarge_neon(uint8_t *out, uint64_t val) {
  int n       = 0;
  uint64_t lo = val % 10000000000000000ULL;
  uint32_t hi = (uint32_t)(val / 10000000000000000ULL);

  /* hi is 1..1844 (since max uint64 = 18446744073709551615, hi <= 1844) */
  if (hi < 10) {
    out[n++] = (char)hi + '0';
  } else if (hi < 100) {
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[hi * 2], 2);
    n += 2;
  } else if (hi < 1000) {
    out[n++] = (char)(hi / 100) + '0';
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi % 100) * 2], 2);
    n += 2;
  } else {
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi / 100) * 2], 2);
    n += 2;
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi % 100) * 2], 2);
    n += 2;
  }

  /* Remaining 16 digits — always exactly 16 (zero-padded) */
  uint16x8_t d0     = vj_itoa8_neon((uint32_t)(lo / 100000000));
  uint16x8_t d1     = vj_itoa8_neon((uint32_t)(lo % 100000000));
  uint8x16_t packed = vcombine_u8(vqmovn_u16(d0), vqmovn_u16(d1));
  uint8x16_t ascii  = vaddq_u8(packed, vdupq_n_u8('0'));
  vst1q_u8(&out[n], ascii);
  return n + 16;
}

#elif defined(__SSE2__)

ALIGNED_DECL(16)
static const char Vec16xA0[16] ALIGNED(16) = {'0', '0', '0', '0', '0', '0', '0', '0',
                                              '0', '0', '0', '0', '0', '0', '0', '0'};
ALIGNED_DECL(16)
static const uint16_t Vec8x10[8] ALIGNED(16) = {10, 10, 10, 10, 10, 10, 10, 10};
ALIGNED_DECL(16)
static const uint32_t Vec4x10k[4] ALIGNED(16) = {10000, 10000, 10000, 10000};
ALIGNED_DECL(16)
static const uint32_t Vec4xDiv10k[4] ALIGNED(16) = {0xd1b71759, 0xd1b71759, 0xd1b71759, 0xd1b71759};
ALIGNED_DECL(16)
static const uint16_t VecDivPowers[8] ALIGNED(16) = {0x20c5, 0x147b, 0x3334, 0x8000,
                                                     0x20c5, 0x147b, 0x3334, 0x8000};
ALIGNED_DECL(16)
static const uint16_t VecShiftPowers[8] ALIGNED(16) = {0x0080, 0x0800, 0x2000, 0x8000,
                                                       0x0080, 0x0800, 0x2000, 0x8000};

/* Convert an 8-digit number (0..99999999) to 8 ASCII digit values in an XMM
 * register. Uses SSE2 multiplication-based digit extraction (no division). */
INLINE __m128i vj_itoa8_sse2(uint32_t v) {
  __m128i v00 = _mm_cvtsi32_si128(v);
  __m128i v01 = _mm_mul_epu32(v00, *((const __m128i *)Vec4xDiv10k));
  __m128i v02 = _mm_srli_epi64(v01, 45);
  __m128i v03 = _mm_mul_epu32(v02, *((const __m128i *)Vec4x10k));
  __m128i v04 = _mm_sub_epi32(v00, v03);
  __m128i v05 = _mm_unpacklo_epi16(v02, v04);
  __m128i v06 = _mm_slli_epi64(v05, 2);
  __m128i v07 = _mm_unpacklo_epi16(v06, v06);
  __m128i v08 = _mm_unpacklo_epi32(v07, v07);
  __m128i v09 = _mm_mulhi_epu16(v08, *((const __m128i *)VecDivPowers));
  __m128i v10 = _mm_mulhi_epu16(v09, *((const __m128i *)VecShiftPowers));
  __m128i v11 = _mm_mullo_epi16(v10, *((const __m128i *)Vec8x10));
  __m128i v12 = _mm_slli_epi64(v11, 16);
  __m128i v13 = _mm_sub_epi16(v10, v12);
  return v13;
}

/* Large: 10^8 <= val < 10^16.  Produces up to 16 digits via two itoa8 calls.
 * Leading zeros are stripped using PSHUFB (SSSE3, available under -msse4.2). */
INLINE int vj_u64toa_large_sse2(uint8_t *out, uint64_t val) {
  uint32_t a = (uint32_t)(val / 100000000);
  uint32_t b = (uint32_t)(val % 100000000);

  __m128i d0 = vj_itoa8_sse2(a);
  __m128i d1 = vj_itoa8_sse2(b);

  /* Pack 16-bit digits to 8-bit and add '0' */
  __m128i packed = _mm_packus_epi16(d0, d1);
  __m128i ascii  = _mm_add_epi8(packed, *((const __m128i *)Vec16xA0));

  /* Count leading zeros */
  __m128i eq0 = _mm_cmpeq_epi8(ascii, *((const __m128i *)Vec16xA0));
  uint32_t bm = _mm_movemask_epi8(eq0);
  uint32_t nd = __builtin_ctz(~bm | 0x8000);

  /* Shift digits left to remove leading zeros */
  __m128i shuf   = _mm_loadu_si128((const __m128i *)&VEC_SHIFT_SHUFFLES[nd * 16]);
  __m128i result = _mm_shuffle_epi8(ascii, shuf);

  _mm_storeu_si128((__m128i *)out, result);
  return 16 - nd;
}

/* Extra-large: val >= 10^16.  Scalar prefix (1-4 digits) + SSE2 for last 16. */
INLINE int vj_u64toa_xlarge_sse2(uint8_t *out, uint64_t val) {
  int n       = 0;
  uint64_t lo = val % 10000000000000000ULL;
  uint32_t hi = (uint32_t)(val / 10000000000000000ULL);

  /* hi is 1..1844 (since max uint64 = 18446744073709551615, hi <= 1844) */
  if (hi < 10) {
    out[n++] = (char)hi + '0';
  } else if (hi < 100) {
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[hi * 2], 2);
    n += 2;
  } else if (hi < 1000) {
    out[n++] = (char)(hi / 100) + '0';
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi % 100) * 2], 2);
    n += 2;
  } else {
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi / 100) * 2], 2);
    n += 2;
    __builtin_memcpy(&out[n], &DIGIT_PAIRS[(hi % 100) * 2], 2);
    n += 2;
  }

  /* Remaining 16 digits — always exactly 16 (zero-padded) */
  __m128i d0     = vj_itoa8_sse2((uint32_t)(lo / 100000000));
  __m128i d1     = vj_itoa8_sse2((uint32_t)(lo % 100000000));
  __m128i packed = _mm_packus_epi16(d0, d1);
  __m128i ascii  = _mm_add_epi8(packed, *((const __m128i *)Vec16xA0));
  _mm_storeu_si128((__m128i *)&out[n], ascii);
  return n + 16;
}

#endif /* itoa SIMD */

/* Scalar paths (used for all ISAs, and as fallback) */

/* Small: 0..9999 — forward-write with conditional digit skipping. */
INLINE int vj_u32toa_small(uint8_t *out, uint32_t val) {
  int n       = 0;
  uint32_t d1 = (val / 100) * 2;
  uint32_t d2 = (val % 100) * 2;

  if (val >= 1000)
    out[n++] = DIGIT_PAIRS[d1];
  if (val >= 100)
    out[n++] = DIGIT_PAIRS[d1 + 1];
  if (val >= 10)
    out[n++] = DIGIT_PAIRS[d2];
  out[n++] = DIGIT_PAIRS[d2 + 1];
  return n;
}

/* Medium: 10000..99999999 — divide by 10000, then digit-pair lookups. */
INLINE int vj_u32toa_medium(uint8_t *out, uint32_t val) {
  int n       = 0;
  uint32_t hi = val / 10000;
  uint32_t lo = val % 10000;
  uint32_t d1 = (hi / 100) * 2;
  uint32_t d2 = (hi % 100) * 2;
  uint32_t d3 = (lo / 100) * 2;
  uint32_t d4 = (lo % 100) * 2;

  if (val >= 10000000)
    out[n++] = DIGIT_PAIRS[d1];
  if (val >= 1000000)
    out[n++] = DIGIT_PAIRS[d1 + 1];
  if (val >= 100000)
    out[n++] = DIGIT_PAIRS[d2];
  out[n++] = DIGIT_PAIRS[d2 + 1];
  out[n++] = DIGIT_PAIRS[d3];
  out[n++] = DIGIT_PAIRS[d3 + 1];
  out[n++] = DIGIT_PAIRS[d4];
  out[n++] = DIGIT_PAIRS[d4 + 1];
  return n;
}

/* ---- Top-level INLINE dispatch: write_uint64 / write_int64 ----
 *
 * Force-inlined at every call site to eliminate function-call overhead
 * for the hot integer-encoding path.
 * buf must have >= 20 bytes available. */

INLINE int write_uint64(uint8_t *buf, uint64_t v) {
  if (v < 10000) {
    return vj_u32toa_small(buf, (uint32_t)v);
  }
  if (v < 100000000) {
    return vj_u32toa_medium(buf, (uint32_t)v);
  }
#if defined(__aarch64__)
  if (v < 10000000000000000ULL) {
    return vj_u64toa_large_neon(buf, v);
  }
  return vj_u64toa_xlarge_neon(buf, v);
#elif defined(__SSE2__)
  if (v < 10000000000000000ULL) {
    return vj_u64toa_large_sse2(buf, v);
  }
  return vj_u64toa_xlarge_sse2(buf, v);
#else
  /* Scalar fallback for non-SIMD builds: right-to-left + copy. */
  {
    uint8_t tmp[20];
    int pos = 20;
    while (v >= 100) {
      uint64_t q = v / 100;
      uint32_t r = (uint32_t)(v - q * 100);
      v          = q;
      pos -= 2;
      __builtin_memcpy(&tmp[pos], &DIGIT_PAIRS[r * 2], 2);
    }
    if (v >= 10) {
      pos -= 2;
      __builtin_memcpy(&tmp[pos], &DIGIT_PAIRS[v * 2], 2);
    } else {
      pos--;
      tmp[pos] = '0' + (uint8_t)v;
    }
    int len = 20 - pos;
    vj_copy_var(buf, &tmp[pos], len);
    return len;
  }
#endif
}

INLINE int write_int64(uint8_t *buf, int64_t v) {
  if (v >= 0) {
    return write_uint64(buf, (uint64_t)v);
  }
  buf[0] = '-';
  /* INT64_MIN = -9223372036854775808, negate carefully. */
  uint64_t uv = (uint64_t)(-(v + 1)) + 1;
  return 1 + write_uint64(buf + 1, uv);
}

#endif /* VJ_ENCVM_ITOA_H */
