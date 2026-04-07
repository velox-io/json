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

/* --- Scalar base64 — used for tail bytes (< 12 remaining) --- */

static inline uint8_t *base64_encode_scalar(uint8_t *buf, const uint8_t *data,
                                            int64_t len) {
  int64_t i = 0;
  int64_t full_groups = len - (len % 3);
  for (; i < full_groups; i += 3) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8) |
                      ((uint32_t)data[i + 2]);
    buf[0] = B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = B64_CHARS[(triple >> 6) & 0x3F];
    buf[3] = B64_CHARS[triple & 0x3F];
    buf += 4;
  }

  int64_t remainder = len - i;
  if (remainder == 2) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8);
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

/* --- SIMD base64 (Muła–Lemire) — 12 input bytes → 16 output bytes --- */

#if defined(__SSE2__) || defined(__aarch64__)

static inline __m128i base64_encode_simd_12(__m128i input) {

  /* Reshuffle 3×4 → 16 six-bit values via pshufb.
   * Each 3-byte group is replicated into a 4-byte slot:
   *   [a0,a1,a2] → [a1,a0,a2,a1]
   * so the two 16-bit words contain all bits for 4 base64 indices. */
  const __m128i shuf = _mm_setr_epi8(1, 0, 2, 1,   /* group A: [a₁ a₀ a₂ a₁] */
                                     4, 3, 5, 4,   /* group B */
                                     7, 6, 8, 7,   /* group C */
                                     10, 9, 11, 10 /* group D */
  );
  __m128i shuffled = _mm_shuffle_epi8(input, shuf);

  /* Extract 6-bit indices via AND + multiply-shift.
   * mulhi/mullo with carefully chosen constants shift each 16-bit word
   * so the 6-bit base64 index lands in the low bits of each byte. */
  const __m128i mask0 = _mm_set1_epi32(0x0fc0fc00);
  __m128i t0 = _mm_and_si128(shuffled, mask0);
  __m128i t1 = _mm_mulhi_epu16(t0, _mm_set1_epi32(0x04000040));

  const __m128i mask2 = _mm_set1_epi32(0x003f03f0);
  __m128i t2 = _mm_and_si128(shuffled, mask2);
  __m128i t3 = _mm_mullo_epi16(t2, _mm_set1_epi32(0x01000010));

  /* t1 has [idx0, 0, idx2, 0, ...], t3 has [0, idx1, 0, idx3, ...] */
  __m128i indices = _mm_or_si128(t1, t3);

  /* Map 6-bit indices → ASCII via saturating subtract + pshufb LUT.
   *   [0..25]  → 'A'..'Z'   [26..51] → 'a'..'z'
   *   [52..61] → '0'..'9'   62 → '+'   63 → '/' */
  __m128i result = _mm_subs_epu8(indices, _mm_set1_epi8(51));
  __m128i lt26 = _mm_cmpgt_epi8(_mm_set1_epi8(26), indices);
  result = _mm_or_si128(result, _mm_and_si128(lt26, _mm_set1_epi8(13)));

  /* LUT: range_id → ASCII offset to add to 6-bit index.
   *   0→lowercase(+71)  1..10→digit(-4)  11→'+'(-19)  12→'/'(-16) 13→upper(+65)
   */
  const __m128i lut = _mm_setr_epi8(71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4,
                                    -19, -16, 65, 0, 0);
  __m128i offsets = _mm_shuffle_epi8(lut, result);
  return _mm_add_epi8(indices, offsets);
}

#endif /* __SSE2__ || __aarch64__ */

/* --- Public entry point --- */

__attribute__((noinline)) uint8_t *vj_encode_base64(uint8_t *buf,
                                                    const uint8_t *bend,
                                                    const uint8_t *data,
                                                    int64_t len) {

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
