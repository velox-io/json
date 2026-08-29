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

#include "types.h"      // IWYU pragma: keep
#include "util/memfn.h" // IWYU pragma: keep

static const char VJ_B64_CHARS[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Compute base64 output length: ceil(len / 3) * 4 */
static inline int64_t vj_base64_encoded_len(int64_t len) {
  return ((len + 2) / 3) * 4;
}

/* Scalar base64, used for tail bytes (< 12 remaining)
   Common body: encode len bytes of `data` into `buf`. Not called directly. */
static inline uint8_t *vj_base64_encode_scalar_body(uint8_t *buf, const uint8_t *data, int64_t len) {
  int64_t i           = 0;
  int64_t full_groups = len - (len % 3);
  for (; i < full_groups; i += 3) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8) | ((uint32_t)data[i + 2]);
    buf[0]          = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1]          = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2]          = VJ_B64_CHARS[(triple >> 6) & 0x3F];
    buf[3]          = VJ_B64_CHARS[triple & 0x3F];
    buf += 4;
  }

  int64_t remainder = len - i;
  if (remainder == 2) {
    uint32_t triple = ((uint32_t)data[i] << 16) | ((uint32_t)data[i + 1] << 8);
    buf[0]          = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1]          = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2]          = VJ_B64_CHARS[(triple >> 6) & 0x3F];
    buf[3]          = '=';
    buf += 4;
  } else if (remainder == 1) {
    uint32_t triple = (uint32_t)data[i] << 16;
    buf[0]          = VJ_B64_CHARS[(triple >> 18) & 0x3F];
    buf[1]          = VJ_B64_CHARS[(triple >> 12) & 0x3F];
    buf[2]          = '=';
    buf[3]          = '=';
    buf += 4;
  }
  return buf;
}

/* SIMD-path tail: at most 11 bytes remain after the 12-byte SIMD main loop.
 * The unreachable() gate is a hard hint to the optimizer: any len >= 12 is a
 * caller bug, and clang can drop the auto-vectorized fast path it would
 * otherwise synthesize for large `len`. That fast path (unused at runtime,
 * but present in the object) is what inflated the frame past 400B on amd64:
 * ymm spill slots for an SSE/AVX2 branch that no caller ever reaches. */
INLINE uint8_t *vj_base64_encode_scalar_tail(uint8_t *buf, const uint8_t *data, int64_t len) {
  if (len < 0 || len >= 12) __builtin_unreachable();
  return vj_base64_encode_scalar_body(buf, data, len);
}

/* Whole-input scalar encoder used when no SIMD path is available. INLINE
 * to fold the body into the caller's frame (rare path, no extra cost). */
INLINE uint8_t *vj_base64_encode_scalar(uint8_t *buf, const uint8_t *data, int64_t len) {
  return vj_base64_encode_scalar_body(buf, data, len);
}

/* SIMD base64 (Muła–Lemire): 12 input bytes → 16 output bytes */
#if defined(__aarch64__)

/* Muła–Lemire base64 encoder: 12 input bytes → 16 output bytes
 * via TBL shuffle + saturating arithmetic. */
static inline uint8x16_t vj_base64_encode_simd_12(uint8x16_t input) {
  /* Replicate each 3-byte group into a 4-byte slot:
   *   [a0,a1,a2] → [a1,a0,a2,a1] */
  const uint8x16_t shuf_idx = {1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10};
  uint8x16_t shuffled       = vqtbl1q_u8(input, shuf_idx);

  /* Extract 6-bit indices.  Two parallel lanes per 32-bit group:
   *   path 0: AND with 0x0fc0fc00, mulhi by 0x04000040 → right shifts {10, 6}
   *   path 1: AND with 0x003f03f0, mullo by 0x01000010 → left  shifts {4, 8} */
  const uint8x16_t mask0_b = {0x00, 0xfc, 0xc0, 0x0f, 0x00, 0xfc, 0xc0, 0x0f,
                              0x00, 0xfc, 0xc0, 0x0f, 0x00, 0xfc, 0xc0, 0x0f};
  uint16x8_t t0            = vreinterpretq_u16_u8(vandq_u8(shuffled, mask0_b));
  const int16x8_t sh0      = {-10, -6, -10, -6, -10, -6, -10, -6};
  uint16x8_t t1            = vshlq_u16(t0, sh0);

  const uint8x16_t mask2_b = {0xf0, 0x03, 0x3f, 0x00, 0xf0, 0x03, 0x3f, 0x00,
                              0xf0, 0x03, 0x3f, 0x00, 0xf0, 0x03, 0x3f, 0x00};
  uint16x8_t t2            = vreinterpretq_u16_u8(vandq_u8(shuffled, mask2_b));
  const int16x8_t sh1      = {4, 8, 4, 8, 4, 8, 4, 8};
  uint16x8_t t3            = vshlq_u16(t2, sh1);

  uint8x16_t indices = vreinterpretq_u8_u16(vorrq_u16(t1, t3));

  /* Map 6-bit indices → ASCII via saturating subtract + LUT. */
  uint8x16_t result = vqsubq_u8(indices, vdupq_n_u8(51));
  uint8x16_t lt26   = vcltq_u8(indices, vdupq_n_u8(26));
  result            = vorrq_u8(result, vandq_u8(lt26, vdupq_n_u8(13)));

  const uint8x16_t lut = {
      71,          (uint8_t)-4, (uint8_t)-4, (uint8_t)-4,  (uint8_t)-4,  (uint8_t)-4, (uint8_t)-4, (uint8_t)-4,
      (uint8_t)-4, (uint8_t)-4, (uint8_t)-4, (uint8_t)-19, (uint8_t)-16, 65,          0,           0};
  uint8x16_t offsets = vqtbl1q_u8(lut, result);
  return vaddq_u8(indices, offsets);
}

#elif defined(__SSE2__)

static inline __m128i vj_base64_encode_simd_12(__m128i input) {

  /* Reshuffle 3×4 → 16 six-bit values via pshufb.
   * Each 3-byte group is replicated into a 4-byte slot:
   *   [a0,a1,a2] → [a1,a0,a2,a1] */
  const __m128i shuf = _mm_setr_epi8(1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10);
  __m128i shuffled   = _mm_shuffle_epi8(input, shuf);

  /* Extract 6-bit indices via AND + multiply-shift. */
  const __m128i mask0 = _mm_set1_epi32(0x0fc0fc00);
  __m128i t0          = _mm_and_si128(shuffled, mask0);
  __m128i t1          = _mm_mulhi_epu16(t0, _mm_set1_epi32(0x04000040));

  const __m128i mask2 = _mm_set1_epi32(0x003f03f0);
  __m128i t2          = _mm_and_si128(shuffled, mask2);
  __m128i t3          = _mm_mullo_epi16(t2, _mm_set1_epi32(0x01000010));

  __m128i indices = _mm_or_si128(t1, t3);

  /* Map 6-bit indices → ASCII via saturating subtract + pshufb LUT. */
  __m128i result = _mm_subs_epu8(indices, _mm_set1_epi8(51));
  __m128i lt26   = _mm_cmpgt_epi8(_mm_set1_epi8(26), indices);
  result         = _mm_or_si128(result, _mm_and_si128(lt26, _mm_set1_epi8(13)));

  const __m128i lut = _mm_setr_epi8(71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0);
  __m128i offsets   = _mm_shuffle_epi8(lut, result);
  return _mm_add_epi8(indices, offsets);
}

#endif /* base64 SIMD */

/* Encode a byte slice as a base64-encoded JSON string (with quotes).
 * Returns advanced buffer pointer on success, NULL on buffer full.
 * INLINE: as a standalone callee the SIMD body needs a 664B frame on
 * windows/amd64 (callee-saved xmm6-15), blowing the nosplit chain budget;
 * folded into the VM tail label block its spills share slots with the
 * other mutually exclusive handlers. */
INLINE uint8_t *vj_encode_base64(uint8_t *buf, const uint8_t *bend, const uint8_t *data, int64_t len) {

  int64_t b64_len = vj_base64_encoded_len(len);
  int64_t total   = 2 + b64_len;

  if (__builtin_expect(buf + total > bend, 0)) {
    return (uint8_t *)0;
  }

  *buf++ = '"';

#if defined(__aarch64__)
  /* SIMD main loop: 12 input bytes → 16 output bytes. */
  int64_t i = 0;
  for (; i + 12 <= len; i += 12) {
    uint8x16_t input   = vld1q_u8(data + i);
    uint8x16_t encoded = vj_base64_encode_simd_12(input);
    vst1q_u8(buf, encoded);
    buf += 16;
  }
  /* Scalar tail for remaining < 12 bytes */
  buf = vj_base64_encode_scalar_tail(buf, data + i, len - i);
#elif defined(__SSE2__)
  /* SIMD main loop: 12 input bytes → 16 output bytes */
  int64_t i = 0;
  for (; i + 12 <= len; i += 12) {
    __m128i input   = _mm_loadu_si128((const __m128i *)(data + i));
    __m128i encoded = vj_base64_encode_simd_12(input);
    _mm_storeu_si128((__m128i *)buf, encoded);
    buf += 16;
  }
  /* Scalar tail for remaining < 12 bytes */
  buf = vj_base64_encode_scalar_tail(buf, data + i, len - i);
#else
  /* Pure scalar fallback */
  buf = vj_base64_encode_scalar(buf, data, len);
#endif

  *buf++ = '"';
  return buf;
}

#endif /* VJ_ENCVM_BASE64_H */
