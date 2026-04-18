#ifndef NDEC_BIND_NUMBER_H
#define NDEC_BIND_NUMBER_H

#include <stdint.h>

#include "ndec/number.h"
#include "go_abi.h"

typedef enum {
  NDEC_BWN_OK         = 0, /* write succeeded */
  NDEC_BWN_OVERFLOW   = 1, /* integer overflow, float-literal into int, or malformed input */
  NDEC_BWN_BAD_TARGET = 2, /* target kind is not a numeric type */
} ndec_bwn_status;

/* Narrowing range check from int64 to target integer kind. Returns 0 on overflow. */
INLINE int ndec_bind_store_int_narrow(uint8_t *dst, uint8_t kind, int64_t v) {
  switch (kind) {
  case NDEC_BK_INT8:
    if (v < -128 || v > 127)
      return 0;
    *(int8_t *)dst = (int8_t)v;
    return 1;
  case NDEC_BK_INT16:
    if (v < -32768 || v > 32767)
      return 0;
    *(int16_t *)dst = (int16_t)v;
    return 1;
  case NDEC_BK_INT32:
    if (v < -2147483648LL || v > 2147483647LL)
      return 0;
    *(int32_t *)dst = (int32_t)v;
    return 1;
  case NDEC_BK_INT64:
  case NDEC_BK_INT:
    *(int64_t *)dst = v;
    return 1;
  default:
    return 0;
  }
}

INLINE int ndec_bind_store_uint_narrow(uint8_t *dst, uint8_t kind, uint64_t v) {
  switch (kind) {
  case NDEC_BK_UINT8:
    if (v > 255)
      return 0;
    *(uint8_t *)dst = (uint8_t)v;
    return 1;
  case NDEC_BK_UINT16:
    if (v > 65535)
      return 0;
    *(uint16_t *)dst = (uint16_t)v;
    return 1;
  case NDEC_BK_UINT32:
    if (v > 4294967295ULL)
      return 0;
    *(uint32_t *)dst = (uint32_t)v;
    return 1;
  case NDEC_BK_UINT64:
  case NDEC_BK_UINT:
    *(uint64_t *)dst = v;
    return 1;
  default:
    return 0;
  }
}

INLINE ndec_bwn_status ndec_bind_write_number(uint8_t *dst, uint8_t kind, const uint8_t *raw, uint32_t len,
                                              NdecBindUserData *ud) {
  switch (kind) {
  case NDEC_BK_INT8:
  case NDEC_BK_INT16:
  case NDEC_BK_INT32:
  case NDEC_BK_INT64:
  case NDEC_BK_INT: {
    int64_t v;
    if (ndec_parse_int64(raw, len, &v) != NDEC_NUM_OK)
      return NDEC_BWN_OVERFLOW;
    return ndec_bind_store_int_narrow(dst, kind, v) ? NDEC_BWN_OK : NDEC_BWN_OVERFLOW;
  }

  case NDEC_BK_UINT8:
  case NDEC_BK_UINT16:
  case NDEC_BK_UINT32:
  case NDEC_BK_UINT64:
  case NDEC_BK_UINT: {
    uint64_t v;
    if (ndec_parse_uint64(raw, len, &v) != NDEC_NUM_OK)
      return NDEC_BWN_OVERFLOW;
    return ndec_bind_store_uint_narrow(dst, kind, v) ? NDEC_BWN_OK : NDEC_BWN_OVERFLOW;
  }

  case NDEC_BK_FLOAT32: {
    double d;
    /* Padded path: when at least NDEC_ATOF_PADDED_TAIL bytes remain after the
     * token, use atof's _json_padded_ctx which skips all bound checks. The
     * ud->buf_end and ud->atof_ctx pointers are stable across hooks, set once
     * at driver entry. Edge condition (raw.ptr + len + TAIL == buf_end): padded
     * is safe; when fewer bytes remain (e.g. root scalar at buffer end), fall
     * back to _json_ctx. */
    const uint8_t *raw_end = raw + len;
    if ((size_t)((const uint8_t *)ud->buf_end - raw_end) >= NDEC_ATOF_PADDED_TAIL) {
      if (ndec_parse_double_padded(raw, len, &d, (atof_ctx *)ud->atof_ctx) != 0)
        return NDEC_BWN_OVERFLOW;
    } else {
      if (ndec_parse_double(raw, len, &d, (atof_ctx *)ud->atof_ctx) != 0)
        return NDEC_BWN_OVERFLOW;
    }
    *(float *)dst = (float)d;
    return NDEC_BWN_OK;
  }
  case NDEC_BK_FLOAT64: {
    double d;
    const uint8_t *raw_end = raw + len;
    if ((size_t)((const uint8_t *)ud->buf_end - raw_end) >= NDEC_ATOF_PADDED_TAIL) {
      if (ndec_parse_double_padded(raw, len, &d, (atof_ctx *)ud->atof_ctx) != 0)
        return NDEC_BWN_OVERFLOW;
    } else {
      if (ndec_parse_double(raw, len, &d, (atof_ctx *)ud->atof_ctx) != 0)
        return NDEC_BWN_OVERFLOW;
    }
    *(double *)dst = d;
    return NDEC_BWN_OK;
  }

  default:
    return NDEC_BWN_BAD_TARGET;
  }
}

#endif /* NDEC_BIND_NUMBER_H */
