/*
 * encoder_number.h — Velox JSON C Engine: Integer Formatting
 *
 * Fast integer-to-ASCII conversion using digit-pair lookup tables.
 * Depends on: encoder_types.h (vj_memcpy).
 */

#ifndef VJ_ENCODER_NUMBER_H
#define VJ_ENCODER_NUMBER_H

/* ================================================================
 *  Section 7 — Helper: Fast integer to ASCII
 *
 *  write_uint64 / write_int64 convert to decimal ASCII in buf.
 *  They return the number of bytes written. buf must have >= 20 bytes.
 * ================================================================ */

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

static inline int write_uint64(uint8_t *buf, uint64_t v) {
  /* Fast path for small numbers (very common). */
  if (v < 10) {
    buf[0] = '0' + (uint8_t)v;
    return 1;
  }
  if (v < 100) {
    vj_memcpy(buf, &digit_pairs[v * 2], 2);
    return 2;
  }

  /* Write digits from right to left into a temp buffer. */
  uint8_t tmp[20];
  int pos = 20;

  while (v >= 100) {
    uint64_t q = v / 100;
    uint32_t r = (uint32_t)(v - q * 100);
    v = q;
    pos -= 2;
    vj_memcpy(&tmp[pos], &digit_pairs[r * 2], 2);
  }

  if (v >= 10) {
    pos -= 2;
    vj_memcpy(&tmp[pos], &digit_pairs[v * 2], 2);
  } else {
    pos--;
    tmp[pos] = '0' + (uint8_t)v;
  }

  int len = 20 - pos;
  vj_copy_var(buf, &tmp[pos], len);
  return len;
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
