/*
 * Base64 encoder
 *
 * Standard base64 encoding (RFC 4648, with '=' padding).  Wraps
 * []byte input in JSON string quotes.
 *
 * SIMD core: Muła–Lemire algorithm (12 input bytes → 16 output bytes
 * per iteration via pshufb), with scalar tail for < 12 bytes.
 *
 * Reference: Muła & Lemire, "Faster Base64 Encoding and Decoding Using
 *            AVX2 Instructions", ACM TOMPECS, 2018.
 */

#ifndef VJ_ENCVM_BASE64_H
#define VJ_ENCVM_BASE64_H

#include "types.h"
#include "util.h"

static const char VJ_B64_CHARS[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Compute base64 output length: ceil(len / 3) * 4 */
static inline int64_t vj_base64_encoded_len(int64_t len) {
  return ((len + 2) / 3) * 4;
}

/* Scalar base64 — used for tail bytes (< 12 remaining) */
static inline uint8_t *vj_base64_encode_scalar(uint8_t *buf, const uint8_t *data, int64_t len) {
  int64_t i = 0;
  int64_t full_groups = len - (len % 3);
  for (; i < full_groups; i += 3) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8) | ((uint32_t)data[i + 2]);
    buf[0] = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = VJ_B64_CHARS[(triple >> 6) & 0x3F];
    buf[3] = VJ_B64_CHARS[triple & 0x3F];
    buf += 4;
  }

  int64_t remainder = len - i;
  if (remainder == 2) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8);
    buf[0] = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = VJ_B64_CHARS[(triple >> 6) & 0x3F];
    buf[3] = '=';
    buf += 4;
  } else if (remainder == 1) {
    uint32_t triple = (uint32_t)data[i] << 16;
    buf[0] = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1] = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2] = '=';
    buf[3] = '=';
    buf += 4;
  }
  return buf;
}

/* SIMD base64 (Muła–Lemire) — 12 input bytes → 16 output bytes */
#if defined(__SSE2__) || defined(__aarch64__)

static inline __m128i vj_base64_encode_simd_12(__m128i input) {

  /* Reshuffle 3×4 → 16 six-bit values via pshufb.
   * Each 3-byte group is replicated into a 4-byte slot:
   *   [a0,a1,a2] → [a1,a0,a2,a1] */
  const __m128i shuf = _mm_setr_epi8(1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10);
  __m128i shuffled = _mm_shuffle_epi8(input, shuf);

  /* Extract 6-bit indices via AND + multiply-shift. */
  const __m128i mask0 = _mm_set1_epi32(0x0fc0fc00);
  __m128i t0 = _mm_and_si128(shuffled, mask0);
  __m128i t1 = _mm_mulhi_epu16(t0, _mm_set1_epi32(0x04000040));

  const __m128i mask2 = _mm_set1_epi32(0x003f03f0);
  __m128i t2 = _mm_and_si128(shuffled, mask2);
  __m128i t3 = _mm_mullo_epi16(t2, _mm_set1_epi32(0x01000010));

  __m128i indices = _mm_or_si128(t1, t3);

  /* Map 6-bit indices → ASCII via saturating subtract + pshufb LUT. */
  __m128i result = _mm_subs_epu8(indices, _mm_set1_epi8(51));
  __m128i lt26 = _mm_cmpgt_epi8(_mm_set1_epi8(26), indices);
  result = _mm_or_si128(result, _mm_and_si128(lt26, _mm_set1_epi8(13)));

  const __m128i lut = _mm_setr_epi8(71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0);
  __m128i offsets = _mm_shuffle_epi8(lut, result);
  return _mm_add_epi8(indices, offsets);
}

#endif /* __SSE2__ || __aarch64__ */

/* Encode a byte slice as a base64-encoded JSON string (with quotes).
 * Returns advanced buffer pointer on success, NULL on buffer full. */
uint8_t *vj_encode_base64(uint8_t *buf, const uint8_t *bend, const uint8_t *data, int64_t len);

#endif /* VJ_ENCVM_BASE64_H */
