#ifndef NDEC_ATOI_H
#define NDEC_ATOI_H

#include <stddef.h>
#include <stdint.h>
#include "macros.h"

typedef enum {
  NDEC_NUM_OK       = 0,
  NDEC_NUM_EMPTY    = 1,
  NDEC_NUM_FLOAT    = 2,
  NDEC_NUM_OVERFLOW = 3,
} ndec_num_status;

INLINE int ndec_atoi_swar_is_8digits(uint64_t val) {
  return ((val & 0xF0F0F0F0F0F0F0F0ULL) | (((val + 0x0606060606060606ULL) & 0xF0F0F0F0F0F0F0F0ULL) >> 4)) ==
         0x3333333333333333ULL;
}

INLINE uint32_t ndec_atoi_swar_parse_8digits(const uint8_t *p) {
  uint64_t val;
  __builtin_memcpy(&val, p, 8);
  val = (val & 0x0F0F0F0F0F0F0F0FULL) * 2561 >> 8;
  val = (val & 0x00FF00FF00FF00FFULL) * 6553601 >> 16;
  return (uint32_t)((val & 0x0000FFFF0000FFFFULL) * 42949672960001ULL >> 32);
}

INLINE ndec_num_status ndec_parse_int64(const uint8_t *s, uint32_t n, int64_t *out) {
  int neg            = (*s == '-');
  const uint8_t *p   = s + neg;
  const uint8_t *end = s + n;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  /* SWAR batch path runs in 8-digit chunks. For short inputs
   * (n - neg < 8) the (end - p) >= 8 test is statically false, the
   * loop is folded out by the branch predictor, and parsing falls
   * straight to the per-byte loop. int64 has at most 19 digits, so
   * SWAR runs 0 to 2 times. */
  while ((size_t)(end - p) >= 8) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!ndec_atoi_swar_is_8digits(val)) break;
    i = i * 100000000ULL + ndec_atoi_swar_parse_8digits(p);
    p += 8;
  }
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9) break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0) return NDEC_NUM_EMPTY;
  if (*start_digits == '0' && dc > 1) return NDEC_NUM_EMPTY;
  if (p != end) return NDEC_NUM_FLOAT;
  if (dc > 19) return NDEC_NUM_OVERFLOW;
  if (i > (uint64_t)INT64_MAX + (uint64_t)neg) return NDEC_NUM_OVERFLOW;

  *out = neg ? (int64_t)(~i + 1) : (int64_t)i;
  return NDEC_NUM_OK;
}

INLINE ndec_num_status ndec_parse_uint64(const uint8_t *s, uint32_t n, uint64_t *out) {
  const uint8_t *p   = s;
  const uint8_t *end = s + n;
  if (*p == '-') return NDEC_NUM_EMPTY;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  while ((size_t)(end - p) >= 8) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!ndec_atoi_swar_is_8digits(val)) break;
    i = i * 100000000ULL + ndec_atoi_swar_parse_8digits(p);
    p += 8;
  }
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9) break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0) return NDEC_NUM_EMPTY;
  if (*start_digits == '0' && dc > 1) return NDEC_NUM_EMPTY;
  if (p != end) return NDEC_NUM_FLOAT;
  if (dc > 20) return NDEC_NUM_OVERFLOW;
  if (dc == 20) {
    if (*start_digits != '1' || i <= (uint64_t)INT64_MAX) return NDEC_NUM_OVERFLOW;
  }

  *out = i;
  return NDEC_NUM_OK;
}

/* --- Padded variants: token boundary discovered from digit content --- *
 *
 * Callers must guarantee the buffer has readable non-digit padding past the
 * token (the ndec source contract provides 64 bytes of 0x20 trailing pad).
 * The digit loop halts at the first non-digit byte, which is guaranteed to
 * exist within the padded region. This eliminates the caller-side prescan
 * that would otherwise walk the token twice (once to measure, once to parse).
 *
 * *end_out is set to the first non-digit byte on both success and FLOAT
 * (caller uses it to detect '.'/'e' or to run a structural check on OK).
 * Not written on EMPTY/OVERFLOW: those are hard errors and the caller does
 * not resume from them. */

INLINE ndec_num_status ndec_parse_int64_padded(const uint8_t *s, int64_t *out, const uint8_t **end_out) {
  int neg          = (*s == '-');
  const uint8_t *p = s + neg;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  /* SWAR at most twice: int64 has <=19 digits; two 8-digit rounds cover 16.
   * The gate is content-driven (ndec_atoi_swar_is_8digits) so the padding's
   * 0x20 bytes fail the digit check and stop the SWAR loop safely. */
  for (int r = 0; r < 2; r++) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!ndec_atoi_swar_is_8digits(val)) break;
    i = i * 100000000ULL + ndec_atoi_swar_parse_8digits(p);
    p += 8;
  }
  while (1) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9) break;
    i = 10 * i + digit;
    p++;
  }

  *end_out  = p;
  size_t dc = (size_t)(p - start_digits);
  if (dc == 0) return NDEC_NUM_EMPTY;
  /* RFC 8259: a leading '0' must stand alone ("0", "0.5", "0e1" -- not "01"). */
  if (*start_digits == '0' && dc > 1) return NDEC_NUM_EMPTY;
  /* '.' or 'e'/'E' means the token is a float, defer to caller. */
  if (*p == '.' || (*p | 0x20) == 'e') return NDEC_NUM_FLOAT;
  if (dc > 19) return NDEC_NUM_OVERFLOW;
  if (i > (uint64_t)INT64_MAX + (uint64_t)neg) return NDEC_NUM_OVERFLOW;

  *out = neg ? (int64_t)(~i + 1) : (int64_t)i;
  return NDEC_NUM_OK;
}

INLINE ndec_num_status ndec_parse_uint64_padded(const uint8_t *s, uint64_t *out, const uint8_t **end_out) {
  const uint8_t *p = s;
  if (*p == '-') return NDEC_NUM_EMPTY;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  /* uint64 has <=20 digits; two SWAR rounds cover 16 and the tail loop
   * consumes the remaining 4 (or fewer). */
  for (int r = 0; r < 2; r++) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!ndec_atoi_swar_is_8digits(val)) break;
    i = i * 100000000ULL + ndec_atoi_swar_parse_8digits(p);
    p += 8;
  }
  while (1) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9) break;
    i = 10 * i + digit;
    p++;
  }

  *end_out  = p;
  size_t dc = (size_t)(p - start_digits);
  if (dc == 0) return NDEC_NUM_EMPTY;
  if (*start_digits == '0' && dc > 1) return NDEC_NUM_EMPTY;
  if (*p == '.' || (*p | 0x20) == 'e') return NDEC_NUM_FLOAT;
  if (dc > 20) return NDEC_NUM_OVERFLOW;
  if (dc == 20) {
    if (*start_digits != '1' || i <= (uint64_t)INT64_MAX) return NDEC_NUM_OVERFLOW;
  }

  *out = i;
  return NDEC_NUM_OK;
}

#endif /* NDEC_ATOI_H */
