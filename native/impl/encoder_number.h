/*
 * Fast integer-to-ASCII conversion using range-based dispatch with
 * SSE2 parallel digit extraction for large numbers.
 *
 * - Small (< 10000): direct digit-pair table lookups
 * - Medium (< 10^8): divide-by-10000 + table lookups
 * - Large (< 10^16): SSE2 parallel 8-digit conversion + PSHUFB
 * - XLarge (>= 10^16): scalar prefix + SSE2 for remaining 16 digits
 */

#ifndef VJ_ENCODER_NUMBER_H
#define VJ_ENCODER_NUMBER_H

// clang-format off

/* ---- Digit-pair lookup table (200 bytes, shared format with digit_pairs) ---- */

static const char digit_pairs[201] = "00010203040506070809"
                                     "10111213141516171819"
                                     "20212223242526272829"
                                     "30313233343536373839"
                                     "40414243444546474849"
                                     "50515253545556575859"
                                     "60616263646566676869"
                                     "70717273747576777879"
                                     "80818283848586878889"
                                     "90919293949596979899";

/* ---- SSE2 constants for itoa8 ---- */

#if defined(__SSE2__) || defined(__aarch64__)

static const char     vj_Vec16xA0[16]      __attribute__((aligned(16))) = {
  '0','0','0','0','0','0','0','0','0','0','0','0','0','0','0','0'
};
static const uint16_t vj_Vec8x10[8]        __attribute__((aligned(16))) = {
  10, 10, 10, 10, 10, 10, 10, 10
};
static const uint32_t vj_Vec4x10k[4]       __attribute__((aligned(16))) = {
  10000, 10000, 10000, 10000
};
static const uint32_t vj_Vec4xDiv10k[4]    __attribute__((aligned(16))) = {
  0xd1b71759, 0xd1b71759, 0xd1b71759, 0xd1b71759
};
static const uint16_t vj_VecDivPowers[8]   __attribute__((aligned(16))) = {
  0x20c5, 0x147b, 0x3334, 0x8000, 0x20c5, 0x147b, 0x3334, 0x8000
};
static const uint16_t vj_VecShiftPowers[8] __attribute__((aligned(16))) = {
  0x0080, 0x0800, 0x2000, 0x8000, 0x0080, 0x0800, 0x2000, 0x8000
};

/* 9 pre-computed shuffle masks for leading-zero removal (0..8 leading zeros).
 * Each mask is 16 bytes; we store them contiguously and index by nd*16. */
static const uint8_t vj_VecShiftShuffles[144] __attribute__((aligned(16))) = {
  /* nd=0: no shift */
  0x00,0x01,0x02,0x03,0x04,0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,
  /* nd=1 */
  0x01,0x02,0x03,0x04,0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,
  /* nd=2 */
  0x02,0x03,0x04,0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,
  /* nd=3 */
  0x03,0x04,0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,
  /* nd=4 */
  0x04,0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,0xff,
  /* nd=5 */
  0x05,0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,0xff,0xff,
  /* nd=6 */
  0x06,0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,0xff,0xff,0xff,
  /* nd=7 */
  0x07,0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,0xff,0xff,0xff,0xff,
  /* nd=8 */
  0x08,0x09,0x0a,0x0b,0x0c,0x0d,0x0e,0x0f,0xff,0xff,0xff,0xff,0xff,0xff,0xff,0xff,
};

/* Convert an 8-digit number (0..99999999) to 8 ASCII digit values in an XMM register.
 * Uses SSE2 multiplication-based digit extraction (no division). */
static __attribute__((always_inline)) inline __m128i
vj_itoa8_sse2(uint32_t v) {
  __m128i v00 = _mm_cvtsi32_si128(v);
  __m128i v01 = _mm_mul_epu32(v00, *((const __m128i *)vj_Vec4xDiv10k));
  __m128i v02 = _mm_srli_epi64(v01, 45);
  __m128i v03 = _mm_mul_epu32(v02, *((const __m128i *)vj_Vec4x10k));
  __m128i v04 = _mm_sub_epi32(v00, v03);
  __m128i v05 = _mm_unpacklo_epi16(v02, v04);
  __m128i v06 = _mm_slli_epi64(v05, 2);
  __m128i v07 = _mm_unpacklo_epi16(v06, v06);
  __m128i v08 = _mm_unpacklo_epi32(v07, v07);
  __m128i v09 = _mm_mulhi_epu16(v08, *((const __m128i *)vj_VecDivPowers));
  __m128i v10 = _mm_mulhi_epu16(v09, *((const __m128i *)vj_VecShiftPowers));
  __m128i v11 = _mm_mullo_epi16(v10, *((const __m128i *)vj_Vec8x10));
  __m128i v12 = _mm_slli_epi64(v11, 16);
  __m128i v13 = _mm_sub_epi16(v10, v12);
  return v13;
}

/* Large: 10^8 <= val < 10^16.  Produces up to 16 digits via two itoa8 calls.
 * Leading zeros are stripped using PSHUFB (SSSE3, available under -msse4.2). */
static __attribute__((always_inline)) inline int
vj_u64toa_large_sse2(uint8_t *out, uint64_t val) {
  uint32_t a = (uint32_t)(val / 100000000);
  uint32_t b = (uint32_t)(val % 100000000);

  __m128i d0 = vj_itoa8_sse2(a);
  __m128i d1 = vj_itoa8_sse2(b);

  /* Pack 16-bit digits to 8-bit and add '0' */
  __m128i packed = _mm_packus_epi16(d0, d1);
  __m128i ascii  = _mm_add_epi8(packed, *((const __m128i *)vj_Vec16xA0));

  /* Count leading zeros */
  __m128i eq0 = _mm_cmpeq_epi8(ascii, *((const __m128i *)vj_Vec16xA0));
  uint32_t bm = _mm_movemask_epi8(eq0);
  uint32_t nd = __builtin_ctz(~bm | 0x8000);

  /* Shift digits left to remove leading zeros */
  __m128i shuf = _mm_loadu_si128((const __m128i *)&vj_VecShiftShuffles[nd * 16]);
  __m128i result = _mm_shuffle_epi8(ascii, shuf);

  _mm_storeu_si128((__m128i *)out, result);
  return 16 - nd;
}

/* Extra-large: val >= 10^16.  Scalar prefix (1-4 digits) + SSE2 for last 16. */
static __attribute__((always_inline)) inline int
vj_u64toa_xlarge_sse2(uint8_t *out, uint64_t val) {
  int n = 0;
  uint64_t lo = val % 10000000000000000ULL;
  uint32_t hi = (uint32_t)(val / 10000000000000000ULL);

  /* hi is 1..1844 (since max uint64 = 18446744073709551615, hi <= 1844) */
  if (hi < 10) {
    out[n++] = (char)hi + '0';
  } else if (hi < 100) {
    __builtin_memcpy(&out[n], &digit_pairs[hi * 2], 2);
    n += 2;
  } else if (hi < 1000) {
    out[n++] = (char)(hi / 100) + '0';
    __builtin_memcpy(&out[n], &digit_pairs[(hi % 100) * 2], 2);
    n += 2;
  } else {
    __builtin_memcpy(&out[n], &digit_pairs[(hi / 100) * 2], 2);
    n += 2;
    __builtin_memcpy(&out[n], &digit_pairs[(hi % 100) * 2], 2);
    n += 2;
  }

  /* Remaining 16 digits — always exactly 16 (zero-padded) */
  __m128i d0 = vj_itoa8_sse2((uint32_t)(lo / 100000000));
  __m128i d1 = vj_itoa8_sse2((uint32_t)(lo % 100000000));
  __m128i packed = _mm_packus_epi16(d0, d1);
  __m128i ascii  = _mm_add_epi8(packed, *((const __m128i *)vj_Vec16xA0));
  _mm_storeu_si128((__m128i *)&out[n], ascii);
  return n + 16;
}

#endif /* __SSE2__ || __aarch64__ */

/* ---- Scalar paths (used for all ISAs, and as fallback) ---- */

/* Small: 0..9999 — forward-write with conditional digit skipping. */
static __attribute__((always_inline)) inline int
vj_u32toa_small(uint8_t *out, uint32_t val) {
  int n = 0;
  uint32_t d1 = (val / 100) * 2;
  uint32_t d2 = (val % 100) * 2;

  if (val >= 1000) out[n++] = digit_pairs[d1];
  if (val >= 100)  out[n++] = digit_pairs[d1 + 1];
  if (val >= 10)   out[n++] = digit_pairs[d2];
  out[n++] = digit_pairs[d2 + 1];
  return n;
}

/* Medium: 10000..99999999 — divide by 10000, then digit-pair lookups. */
static __attribute__((always_inline)) inline int
vj_u32toa_medium(uint8_t *out, uint32_t val) {
  int n = 0;
  uint32_t hi = val / 10000;
  uint32_t lo = val % 10000;
  uint32_t d1 = (hi / 100) * 2;
  uint32_t d2 = (hi % 100) * 2;
  uint32_t d3 = (lo / 100) * 2;
  uint32_t d4 = (lo % 100) * 2;

  if (val >= 10000000) out[n++] = digit_pairs[d1];
  if (val >= 1000000)  out[n++] = digit_pairs[d1 + 1];
  if (val >= 100000)   out[n++] = digit_pairs[d2];
  out[n++] = digit_pairs[d2 + 1];
  out[n++] = digit_pairs[d3];
  out[n++] = digit_pairs[d3 + 1];
  out[n++] = digit_pairs[d4];
  out[n++] = digit_pairs[d4 + 1];
  return n;
}

/* ---- Top-level entry: write_uint64 / write_int64 ----
 *
 * Forward-write with range-based dispatch.
 * buf must have >= 20 bytes available. */

static inline int write_uint64(uint8_t *buf, uint64_t v) {
  if (__builtin_expect(v < 10000, 1)) {
    return vj_u32toa_small(buf, (uint32_t)v);
  }
  if (__builtin_expect(v < 100000000, 1)) {
    return vj_u32toa_medium(buf, (uint32_t)v);
  }
#if defined(__SSE2__) || defined(__aarch64__)
  if (__builtin_expect(v < 10000000000000000ULL, 1)) {
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
      v = q;
      pos -= 2;
      __builtin_memcpy(&tmp[pos], &digit_pairs[r * 2], 2);
    }
    if (v >= 10) {
      pos -= 2;
      __builtin_memcpy(&tmp[pos], &digit_pairs[v * 2], 2);
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

static inline int write_int64(uint8_t *buf, int64_t v) {
  if (v >= 0) {
    return write_uint64(buf, (uint64_t)v);
  }
  buf[0] = '-';
  /* INT64_MIN = -9223372036854775808, negate carefully. */
  uint64_t uv = (uint64_t)(-(v + 1)) + 1;
  return 1 + write_uint64(buf + 1, uv);
}

#endif /* VJ_ENCODER_NUMBER_H */
