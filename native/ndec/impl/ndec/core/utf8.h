/* The streaming UTF-8 validator classifies adjacent byte pairs with three
 * lookup tables and accumulates malformed sequence bits. prev_input_block and
 * prev_incomplete carry cross-block context, including unfinished multibyte
 * sequences.
 *
 * Callers feed every 64-byte block in order, pad the final partial block with
 * ASCII bytes, call utf8_check_eof once, and then read utf8_errored. */
#ifndef NDEC_UTF8_H
#define NDEC_UTF8_H

#include <stddef.h>
#include <stdint.h>
#include "macros.h"

#if defined(__aarch64__)
#include <arm_neon.h>
#elif defined(__x86_64__)
#include <immintrin.h>
#endif

/* Hosts that only need JSON syntax processing (e.g. jsonfmt) define
 * NDEC_NO_UTF8_CHECK before including the parser: the checker collapses
 * to a no-op and the scanner drops all UTF-8 work. */
#ifdef NDEC_NO_UTF8_CHECK

typedef struct {
  char _unused;
} Utf8Checker;

INLINE void utf8_checker_init(Utf8Checker *c) {
  (void)c;
}
INLINE void utf8_check_block64(Utf8Checker *c, const uint8_t *buf) {
  (void)c;
  (void)buf;
}
INLINE void utf8_check_eof(Utf8Checker *c) {
  (void)c;
}
INLINE int utf8_errored(const Utf8Checker *c) {
  (void)c;
  return 0;
}

#else

/* lookup4 error-bit assignments. Names and values match checking special cases. */
#define UTF8_TOO_SHORT      (1u << 0)
#define UTF8_TOO_LONG       (1u << 1)
#define UTF8_OVERLONG_3     (1u << 2)
#define UTF8_TOO_LARGE      (1u << 3)
#define UTF8_SURROGATE      (1u << 4)
#define UTF8_OVERLONG_2     (1u << 5)
#define UTF8_TOO_LARGE_1000 (1u << 6)
#define UTF8_OVERLONG_4     (1u << 6) /* shares bit 6 with TOO_LARGE_1000 */
#define UTF8_TWO_CONTS      (1u << 7)

#define UTF8_CARRY (UTF8_TOO_SHORT | UTF8_TOO_LONG | UTF8_TWO_CONTS)

#if defined(__aarch64__)

typedef struct Utf8Checker {
  /* Accumulated error vector: any non-zero byte means at least one
   * of the input bytes was part of a malformed UTF-8 sequence. */
  uint8x16_t error;
  /* Last 16-byte lane of the previous block. Used to materialize
   * prev1/prev2/prev3 across block boundaries. */
  uint8x16_t prev_input_block;
  /* Marks the 1..3 final-byte tail that, if not followed by enough
   * continuations in the next block, would be an error. */
  uint8x16_t prev_incomplete;
} Utf8Checker;

INLINE void utf8_checker_init(Utf8Checker *c) {
  c->error            = vdupq_n_u8(0);
  c->prev_input_block = vdupq_n_u8(0);
  c->prev_incomplete  = vdupq_n_u8(0);
}

/* The three lookup tables leave error bits for malformed adjacent byte pairs. */

INLINE uint8x16_t utf8_check_special_cases(uint8x16_t input, uint8x16_t prev1) {
  // clang-format off
  static const uint8_t byte_1_high_data[16] = {
    /* 0_______ */ UTF8_TOO_LONG, UTF8_TOO_LONG,
                   UTF8_TOO_LONG, UTF8_TOO_LONG,
                   UTF8_TOO_LONG, UTF8_TOO_LONG,
                   UTF8_TOO_LONG, UTF8_TOO_LONG,
    /* 10______ */ UTF8_TWO_CONTS, UTF8_TWO_CONTS,
                   UTF8_TWO_CONTS, UTF8_TWO_CONTS,
    /* 1100____ */ UTF8_TOO_SHORT | UTF8_OVERLONG_2,
    /* 1101____ */ UTF8_TOO_SHORT,
    /* 1110____ */ UTF8_TOO_SHORT | UTF8_OVERLONG_3 | UTF8_SURROGATE,
    /* 1111____ */ UTF8_TOO_SHORT | UTF8_TOO_LARGE
                 | UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4
  };
  static const uint8_t byte_1_low_data[16] = {
    /* ____0000 */ UTF8_CARRY | UTF8_OVERLONG_3 | UTF8_OVERLONG_2 | UTF8_OVERLONG_4,
    /* ____0001 */ UTF8_CARRY | UTF8_OVERLONG_2,
    /* ____001_ */ UTF8_CARRY, UTF8_CARRY,
    /* ____0100 */ UTF8_CARRY | UTF8_TOO_LARGE,
    /* ____0101 */ UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
    /* ____011_ */ UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
    /* ____1___ */ UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
    /* ____1101 */ UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000
                 | UTF8_SURROGATE,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
                   UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000
  };
  static const uint8_t byte_2_high_data[16] = {
    /* ________ 0_______ ASCII byte 2 */
    UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
    UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
    /* ________ 1000____ */
    UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS
      | UTF8_OVERLONG_3 | UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4,
    /* ________ 1001____ */
    UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS
      | UTF8_OVERLONG_3 | UTF8_TOO_LARGE,
    /* ________ 101_____ */
    UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS
      | UTF8_SURROGATE | UTF8_TOO_LARGE,
    UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS
      | UTF8_SURROGATE | UTF8_TOO_LARGE,
    /* ________ 11______ */
    UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
  };
  // clang-format on

  uint8x16_t lut_b1_hi = vld1q_u8(byte_1_high_data);
  uint8x16_t lut_b1_lo = vld1q_u8(byte_1_low_data);
  uint8x16_t lut_b2_hi = vld1q_u8(byte_2_high_data);

  uint8x16_t b1_hi = vqtbl1q_u8(lut_b1_hi, vshrq_n_u8(prev1, 4));
  uint8x16_t b1_lo = vqtbl1q_u8(lut_b1_lo, vandq_u8(prev1, vdupq_n_u8(0x0F)));
  uint8x16_t b2_hi = vqtbl1q_u8(lut_b2_hi, vshrq_n_u8(input, 4));
  return vandq_u8(vandq_u8(b1_hi, b1_lo), b2_hi);
}

/* Bytes following a 3-byte or 4-byte lead must be continuations. */
INLINE uint8x16_t utf8_must_be_2_3_continuation(uint8x16_t prev2, uint8x16_t prev3) {
  uint8x16_t is_third  = vqsubq_u8(prev2, vdupq_n_u8(0xe0u - 0x80u));
  uint8x16_t is_fourth = vqsubq_u8(prev3, vdupq_n_u8(0xf0u - 0x80u));
  return vorrq_u8(is_third, is_fourth);
}

INLINE uint8x16_t utf8_check_pair(uint8x16_t input, uint8x16_t prev_block) {
  uint8x16_t prev1  = vextq_u8(prev_block, input, 16 - 1);
  uint8x16_t prev2  = vextq_u8(prev_block, input, 16 - 2);
  uint8x16_t prev3  = vextq_u8(prev_block, input, 16 - 3);
  uint8x16_t sc     = utf8_check_special_cases(input, prev1);
  uint8x16_t must23 = utf8_must_be_2_3_continuation(prev2, prev3);
  uint8x16_t must80 = vandq_u8(must23, vdupq_n_u8(0x80));
  return veorq_u8(must80, sc);
}

/* detects 4-/3-/2-byte leads at the tail end of a 16-byte lane.
 * Compare last 3 bytes against { ..., 0xf0-1, 0xe0-1, 0xc0-1 } (saturated):
 * bytes >= those thresholds are unfinished multibyte leads. */
INLINE uint8x16_t utf8_is_incomplete(uint8x16_t input) {
  // clang-format off
  static const uint8_t max_arr[16] = {
    255,255,255,255,255,255,255,255,
    255,255,255,255,255, (uint8_t)(0xf0u-1u), (uint8_t)(0xe0u-1u), (uint8_t)(0xc0u-1u)
  };
  // clang-format on
  uint8x16_t mx = vld1q_u8(max_arr);
  return vqsubq_u8(input, mx);
}

/*  ASCII fast path: if all 64 bytes have their high bit clear, only the carried
 *  prev_incomplete contributes to error and we skip the 4 LUT passes. */
INLINE void utf8_check_block64(Utf8Checker *c, const uint8_t *buf) {
  uint8x16_t in0 = vld1q_u8(buf + 0);
  uint8x16_t in1 = vld1q_u8(buf + 16);
  uint8x16_t in2 = vld1q_u8(buf + 32);
  uint8x16_t in3 = vld1q_u8(buf + 48);

  uint8x16_t any_high = vorrq_u8(vorrq_u8(in0, in1), vorrq_u8(in2, in3));
  /* maxv across 16 bytes -> top-bit-set fast probe */
  uint8_t mx = vmaxvq_u8(any_high);
  if (LIKELY(mx < 0x80)) {
    c->error            = vorrq_u8(c->error, c->prev_incomplete);
    c->prev_input_block = in3;
    /* prev_incomplete unchanged: an all-ASCII block can't *finish*
     * an incomplete sequence either, so the carry persists for eof. */
    return;
  }

  c->error            = vorrq_u8(c->error, utf8_check_pair(in0, c->prev_input_block));
  c->error            = vorrq_u8(c->error, utf8_check_pair(in1, in0));
  c->error            = vorrq_u8(c->error, utf8_check_pair(in2, in1));
  c->error            = vorrq_u8(c->error, utf8_check_pair(in3, in2));
  c->prev_incomplete  = utf8_is_incomplete(in3);
  c->prev_input_block = in3;
}

INLINE void utf8_check_eof(Utf8Checker *c) {
  c->error = vorrq_u8(c->error, c->prev_incomplete);
}

INLINE int utf8_errored(const Utf8Checker *c) {
  return vmaxvq_u8(c->error) != 0;
}

#elif defined(__x86_64__)

typedef struct Utf8Checker {
  __m256i error;
  __m256i prev_input_block;
  __m256i prev_incomplete;
} Utf8Checker;

INLINE void utf8_checker_init(Utf8Checker *c) {
  c->error            = _mm256_setzero_si256();
  c->prev_input_block = _mm256_setzero_si256();
  c->prev_incomplete  = _mm256_setzero_si256();
}

/* On AVX2 a 256-bit vpshufb operates on two 128-bit halves
 * independently, so we pack the same 16-byte LUT twice. */
INLINE __m256i utf8_check_special_cases(__m256i input, __m256i prev1) {
  // clang-format off
  const __m256i lut_b1_hi = _mm256_setr_epi8(
      UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG,
      UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TWO_CONTS, UTF8_TWO_CONTS,
      UTF8_TWO_CONTS, UTF8_TWO_CONTS, UTF8_TOO_SHORT | UTF8_OVERLONG_2, UTF8_TOO_SHORT,
      UTF8_TOO_SHORT | UTF8_OVERLONG_3 | UTF8_SURROGATE,
      UTF8_TOO_SHORT | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4,
      /* Replicate for the upper lane */
      UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG,
      UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TOO_LONG, UTF8_TWO_CONTS, UTF8_TWO_CONTS,
      UTF8_TWO_CONTS, UTF8_TWO_CONTS, UTF8_TOO_SHORT | UTF8_OVERLONG_2, UTF8_TOO_SHORT,
      UTF8_TOO_SHORT | UTF8_OVERLONG_3 | UTF8_SURROGATE,
      UTF8_TOO_SHORT | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4);
  const __m256i lut_b1_lo = _mm256_setr_epi8(
      UTF8_CARRY | UTF8_OVERLONG_3 | UTF8_OVERLONG_2 | UTF8_OVERLONG_4,
      UTF8_CARRY | UTF8_OVERLONG_2, UTF8_CARRY, UTF8_CARRY, UTF8_CARRY | UTF8_TOO_LARGE,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000 | UTF8_SURROGATE,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      /* Replicate */
      UTF8_CARRY | UTF8_OVERLONG_3 | UTF8_OVERLONG_2 | UTF8_OVERLONG_4,
      UTF8_CARRY | UTF8_OVERLONG_2, UTF8_CARRY, UTF8_CARRY, UTF8_CARRY | UTF8_TOO_LARGE,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000 | UTF8_SURROGATE,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000,
      UTF8_CARRY | UTF8_TOO_LARGE | UTF8_TOO_LARGE_1000);
  const __m256i lut_b2_hi = _mm256_setr_epi8(
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_OVERLONG_3 |
          UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_OVERLONG_3 | UTF8_TOO_LARGE,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_SURROGATE | UTF8_TOO_LARGE,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_SURROGATE | UTF8_TOO_LARGE,
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
      /* Replicate */
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_OVERLONG_3 |
          UTF8_TOO_LARGE_1000 | UTF8_OVERLONG_4,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_OVERLONG_3 | UTF8_TOO_LARGE,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_SURROGATE | UTF8_TOO_LARGE,
      UTF8_TOO_LONG | UTF8_OVERLONG_2 | UTF8_TWO_CONTS | UTF8_SURROGATE | UTF8_TOO_LARGE,
      UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT, UTF8_TOO_SHORT);

  // clang-format on

  __m256i mask_0F = _mm256_set1_epi8(0x0F);
  /* prev1 >> 4 (logical, byte-wise). srli_epi16 leaks high nibbles
   * across odd/even byte pairs unless masked. */
  __m256i prev1_hi = _mm256_and_si256(_mm256_srli_epi16(prev1, 4), mask_0F);
  __m256i prev1_lo = _mm256_and_si256(prev1, mask_0F);
  __m256i input_hi = _mm256_and_si256(_mm256_srli_epi16(input, 4), mask_0F);

  __m256i b1_hi = _mm256_shuffle_epi8(lut_b1_hi, prev1_hi);
  __m256i b1_lo = _mm256_shuffle_epi8(lut_b1_lo, prev1_lo);
  __m256i b2_hi = _mm256_shuffle_epi8(lut_b2_hi, input_hi);
  return _mm256_and_si256(_mm256_and_si256(b1_hi, b1_lo), b2_hi);
}

INLINE __m256i utf8_must_be_2_3_continuation(__m256i prev2, __m256i prev3) {
  __m256i is_third  = _mm256_subs_epu8(prev2, _mm256_set1_epi8((char)(0xe0u - 0x80u)));
  __m256i is_fourth = _mm256_subs_epu8(prev3, _mm256_set1_epi8((char)(0xf0u - 0x80u)));
  return _mm256_or_si256(is_third, is_fourth);
}

/* shift current vector right by N bytes within each 128-bit lane, then OR in the upper N bytes of
 * the previous block. AVX2 lane crossing is done via permute2x128. */
INLINE __m256i utf8_prev(int n, __m256i input, __m256i prev_block) {
  /* shifted = | prev_hi | input_lo |  i.e. the previous 16 bytes concatenated with the start of input. */
  __m256i shifted = _mm256_permute2x128_si256(prev_block, input, 0x21);
  /* Use a switch on N so palignr's immediate is constant. */
  switch (n) {
  case 1:
    return _mm256_alignr_epi8(input, shifted, 16 - 1);
  case 2:
    return _mm256_alignr_epi8(input, shifted, 16 - 2);
  case 3:
    return _mm256_alignr_epi8(input, shifted, 16 - 3);
  default:
    __builtin_unreachable();
  }
}

INLINE __m256i utf8_check_pair(__m256i input, __m256i prev_block) {
  __m256i prev1  = utf8_prev(1, input, prev_block);
  __m256i prev2  = utf8_prev(2, input, prev_block);
  __m256i prev3  = utf8_prev(3, input, prev_block);
  __m256i sc     = utf8_check_special_cases(input, prev1);
  __m256i must23 = utf8_must_be_2_3_continuation(prev2, prev3);
  __m256i must80 = _mm256_and_si256(must23, _mm256_set1_epi8((char)0x80));
  return _mm256_xor_si256(must80, sc);
}

INLINE __m256i utf8_is_incomplete(__m256i input) {
  // clang-format off
  static const uint8_t max_arr[32] __attribute__((aligned(32))) = {
    255,255,255,255,255,255,255,255,
    255,255,255,255,255,255,255,255,
    255,255,255,255,255,255,255,255,
    255,255,255,255,255, (uint8_t)(0xf0u-1u), (uint8_t)(0xe0u-1u), (uint8_t)(0xc0u-1u)
  };
  // clang-format on
  __m256i mx = _mm256_load_si256((const __m256i *)max_arr);
  return _mm256_subs_epu8(input, mx);
}

INLINE void utf8_check_block64(Utf8Checker *c, const uint8_t *buf) {
  __m256i in0 = _mm256_loadu_si256((const __m256i *)(buf + 0));
  __m256i in1 = _mm256_loadu_si256((const __m256i *)(buf + 32));

  /* ASCII fast path: a byte's MSB is set iff the value is >= 0x80.
   * movemask of (in0 | in1) collapses both halves into a 32-bit
   * mask; zero means all 64 bytes are pure ASCII. */
  if (LIKELY(_mm256_movemask_epi8(_mm256_or_si256(in0, in1)) == 0)) {
    c->error            = _mm256_or_si256(c->error, c->prev_incomplete);
    c->prev_input_block = in1;
    return;
  }

  c->error            = _mm256_or_si256(c->error, utf8_check_pair(in0, c->prev_input_block));
  c->error            = _mm256_or_si256(c->error, utf8_check_pair(in1, in0));
  c->prev_incomplete  = utf8_is_incomplete(in1);
  c->prev_input_block = in1;
}

INLINE void utf8_check_eof(Utf8Checker *c) {
  c->error = _mm256_or_si256(c->error, c->prev_incomplete);
}

INLINE int utf8_errored(const Utf8Checker *c) {
  return !_mm256_testz_si256(c->error, c->error);
}

#else
#error "utf8 validator: unsupported architecture (need aarch64 or x86_64)"
#endif

#endif /* !NDEC_NO_UTF8_CHECK */

#endif /* NDEC_UTF8_H */
