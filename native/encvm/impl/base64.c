#include "base64.h"

NOINLINE uint8_t *vj_encode_base64(uint8_t *buf, const uint8_t *bend, const uint8_t *data, int64_t len) {

  int64_t b64_len = vj_base64_encoded_len(len);
  int64_t total   = 2 + b64_len;

  if (__builtin_expect(buf + total > bend, 0)) {
    return (uint8_t *)0;
  }

  *buf++ = '"';

#if defined(__SSE2__) || defined(__aarch64__)
  /* SIMD main loop: 12 input bytes → 16 output bytes */
  int64_t i = 0;
  for (; i + 12 <= len; i += 12) {
    __m128i input   = _mm_loadu_si128((const __m128i *)(data + i));
    __m128i encoded = vj_base64_encode_simd_12(input);
    _mm_storeu_si128((__m128i *)buf, encoded);
    buf += 16;
  }
  /* Scalar tail for remaining < 12 bytes */
  buf = vj_base64_encode_scalar(buf, data + i, len - i);
#else
  /* Pure scalar fallback */
  buf = vj_base64_encode_scalar(buf, data, len);
#endif

  *buf++ = '"';
  return buf;
}
