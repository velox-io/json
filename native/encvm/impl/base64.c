/*
 * base64.c — Velox JSON C Engine: SIMD Base64 Encoder for []byte
 *
 * Standard base64 encoding (RFC 4648) with '=' padding, matching
 * Go's encoding/json behavior: []byte fields are serialized as
 * base64-encoded JSON strings.
 *
 * SIMD core: processes 12 input bytes → 16 output bytes per iteration
 * using the Muła–Lemire algorithm:
 *   1. Reshuffle 3×4 byte groups into 6-bit fields via pshufb + mulhi/mullo
 *   2. Map 6-bit indices to ASCII via pshufb LUT
 *
 * Falls through to scalar for the tail (< 12 bytes).
 *
 * Reference: Wojciech Muła, Daniel Lemire, "Faster Base64 Encoding and
 * Decoding Using AVX2 Instructions", ACM TOMPECS, 2018. */

#include "base64.h"
#include "util.h"

static const char B64_CHARS[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Compute base64 output length: ceil(len / 3) * 4 */
static inline int64_t base64_encoded_len(int64_t len) {
  return ((len + 2) / 3) * 4;
}

/* ================================================================
 *  Scalar base64 — used for tail bytes (< 12 remaining)
 * ================================================================ */

static inline uint8_t *base64_encode_scalar(uint8_t *buf,
                                             const uint8_t *data,
                                             int64_t len) {
  int64_t i = 0;
  int64_t full_groups = len - (len % 3);
  for (; i < full_groups; i += 3) {
    uint32_t triple = ((uint32_t)data[i] << 16) |
                      ((uint32_t)data[i + 1] << 8) |
                      ((uint32_t)data[i + 2]);
    buf[0] = B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = B64_CHARS[(triple >> 6) & 0x3F];
    buf[3] = B64_CHARS[triple & 0x3F];
    buf += 4;
  }

  int64_t remainder = len - i;
  if (remainder == 2) {
    uint32_t triple = ((uint32_t)data[i] << 16) |
                      ((uint32_t)data[i + 1] << 8);
    buf[0] = B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = B64_CHARS[(triple >> 6) & 0x3F];
    buf[3] = '=';
    buf += 4;
  } else if (remainder == 1) {
    uint32_t triple = (uint32_t)data[i] << 16;
    buf[0] = B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = '=';
    buf[3] = '=';
    buf += 4;
  }
  return buf;
}

/* ================================================================
 *  SIMD base64 — 12 bytes → 16 bytes per iteration
 *
 *  Algorithm (Muła–Lemire):
 *
 *  Step 1: Reshuffle.  Input bytes form 4 groups of 3:
 *    [A₀ A₁ A₂] [B₀ B₁ B₂] [C₀ C₁ C₂] [D₀ D₁ D₂]
 *    → 16 output 6-bit indices packed into bytes.
 *
 *    pshufb replicates each 3-byte group into 4-byte slots:
 *      [A₀ A₁ A₁ A₂ | B₀ B₁ B₁ B₂ | C₀ C₁ C₁ C₂ | D₀ D₁ D₁ D₂]
 *
 *    Then mulhi_epu16 / mullo_epi16 with constants [0x0040,0x0100]
 *    shift and mask the 6-bit fields into the low 6 bits of each byte.
 *
 *  Step 2: Lookup.  Map 6-bit indices [0..63] → ASCII characters.
 *    Uses a range-reduce + pshufb approach:
 *    - Saturating subtract to classify into ranges
 *    - pshufb to map range offsets → character base
 * ================================================================ */

#if defined(__SSE2__) || defined(__aarch64__)

static inline __m128i base64_encode_simd_12(__m128i input) {

  /* Step 1: Reshuffle 3×4 → 16 six-bit values */
  const __m128i shuf = _mm_setr_epi8(
    1, 0, 2, 1,    /* group A: [A₁ A₀ A₂ A₁] */
    4, 3, 5, 4,    /* group B: [B₁ B₀ B₂ B₁] */
    7, 6, 8, 7,    /* group C: [C₁ C₀ C₂ C₁] */
    10, 9, 11, 10  /* group D: [D₁ D₀ D₂ D₁] */
  );
  __m128i shuffled = _mm_shuffle_epi8(input, shuf);

  /* Multiply to shift 6-bit fields into position:
   *   word 0 (bytes 0-1): mulhi by 0x0040 → top 6 bits of byte 0 become index 0
   *   word 1 (bytes 2-3): mullo by 0x0100 → bits shift for index 1
   *   Alternating pattern across all 8 words. */
  const __m128i mul_const = _mm_set1_epi32(0x01000040);
  __m128i hi = _mm_mulhi_epu16(shuffled, mul_const);
  __m128i lo = _mm_mullo_epi16(shuffled, mul_const);

  /* Interleave hi and lo results:
   * merged = [hi₀ lo₀ hi₁ lo₁ ...] with 6-bit values in bits [8..13] of each word.
   * After blending and masking, each byte holds a 6-bit index. */
  const __m128i blend_mask = _mm_setr_epi8(
    -1, 0, -1, 0, -1, 0, -1, 0,
    -1, 0, -1, 0, -1, 0, -1, 0
  );
  __m128i merged = _mm_or_si128(
    _mm_and_si128(blend_mask, hi),
    _mm_andnot_si128(blend_mask, lo)
  );
  __m128i indices = _mm_and_si128(merged, _mm_set1_epi8(0x3F));

  /* Step 2: Map 6-bit indices → ASCII
   *
   * Range classification using saturating subtract:
   *   index in [0..25]  → 'A'..'Z'  (add 65)
   *   index in [26..51] → 'a'..'z'  (add 71)
   *   index in [52..61] → '0'..'9'  (subtract 4)
   *   index == 62       → '+'       (subtract 19)
   *   index == 63       → '/'       (subtract 16)
   *
   * Reduced to a pshufb lookup on range id. */
  __m128i result = _mm_subs_epu8(indices, _mm_set1_epi8(51));
  /* result[i] = 0 if index <= 51, else index - 51 (1..12) */

  /* For indices <= 25 (uppercase), we need a different offset.
   * Compare: index < 26 → 0xFF, else 0x00 */
  __m128i lt26 = _mm_cmpgt_epi8(_mm_set1_epi8(26), indices);

  /* If index <= 51 (result == 0), it's either upper (< 26) or lower (26..51).
   * Blend: for upper, set result to 13 (our lookup index for uppercase offset) */
  result = _mm_or_si128(result, _mm_and_si128(lt26, _mm_set1_epi8(13)));

  /* Lookup table: maps range_id → offset to add to index to get ASCII.
   *   range_id 0  → index 52..63 area, but specifically:
   *              handled by entries 1..12
   *   range_id 1  (index=52)  → '0' - 52 = -4
   *   range_id 2..10 (53..61) → same: -4
   *   range_id 11 (index=62)  → '+' - 62 = -19
   *   range_id 12 (index=63)  → '/' - 63 = -16
   *   range_id 13 → uppercase: 'A' - 0 = 65
   *   range_id 0  → lowercase: 'a' - 26 = 71 (but also covers 52..63 fallback)
   *
   * Wait — range_id 0 means index was in [26..51].  That's lowercase.
   * 'a' - 26 = 71.  Let's verify: index=26 + 71 = 97 = 'a' ✓
   *
   * For digits: index=52, result = 52-51 = 1.  lookup[1] = -4.  52+(-4)=48='0' ✓
   * For '+':    index=62, result = 62-51 = 11. lookup[11] = -19. 62+(-19)=43='+' ✓
   * For '/':    index=63, result = 63-51 = 12. lookup[12] = -16. 63+(-16)=47='/' ✓
   * For 'A':    index=0,  result = 0|13 = 13.  lookup[13] = 65. 0+65=65='A' ✓
   * For 'a':    index=26, result = 0.   lookup[0] = 71. 26+71=97='a' ✓ */
  const __m128i lut = _mm_setr_epi8(
    71,   /* 0:  lowercase [26..51] → 'a'-26 = 71 */
    -4,   /* 1:  digit '0' (index 52) */
    -4,   /* 2:  digit '1' */
    -4,   /* 3:  digit '2' */
    -4,   /* 4:  digit '3' */
    -4,   /* 5:  digit '4' */
    -4,   /* 6:  digit '5' */
    -4,   /* 7:  digit '6' */
    -4,   /* 8:  digit '7' */
    -4,   /* 9:  digit '8' */
    -4,   /* 10: digit '9' */
    -19,  /* 11: '+' (index 62) */
    -16,  /* 12: '/' (index 63) */
    65,   /* 13: uppercase [0..25] → 'A'-0 = 65 */
    0,    /* 14: unused */
    0     /* 15: unused */
  );
  __m128i offsets = _mm_shuffle_epi8(lut, result);
  return _mm_add_epi8(indices, offsets);
}

#endif /* __SSE2__ || __aarch64__ */


/* ================================================================
 *  Public entry point
 * ================================================================ */

__attribute__((noinline))
uint8_t *vj_encode_base64(uint8_t *buf, const uint8_t *bend,
                           const uint8_t *data, int64_t len) {

  int64_t b64_len = base64_encoded_len(len);
  int64_t total = 2 + b64_len;

  if (__builtin_expect(buf + total > bend, 0)) {
    return (uint8_t *)0;
  }

  *buf++ = '"';

#if defined(__SSE2__) || defined(__aarch64__)
  /* SIMD main loop: 12 input bytes → 16 output bytes */
  int64_t i = 0;
  for (; i + 12 <= len; i += 12) {
    __m128i input = _mm_loadu_si128((const __m128i *)(data + i));
    __m128i encoded = base64_encode_simd_12(input);
    _mm_storeu_si128((__m128i *)buf, encoded);
    buf += 16;
  }
  /* Scalar tail for remaining < 12 bytes */
  buf = base64_encode_scalar(buf, data + i, len - i);
#else
  /* Pure scalar fallback */
  buf = base64_encode_scalar(buf, data, len);
#endif

  *buf++ = '"';
  return buf;
}
