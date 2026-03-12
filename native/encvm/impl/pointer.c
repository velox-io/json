#include "pointer.h"
#include "memfn.h"
#include "uscale.h"

VjPtrEncResult vj_encode_ptr_value(uint8_t *buf, const uint8_t *bend,
                                   const void *ptr, uint16_t etype,
                                   uint32_t flags) {

  /* Caller already did CHECK(key_len+1+330) or similar for fixed-size
   * types.  For variable-length types (string, raw_message, number)
   * we do additional bounds checks below. */

  switch (etype) {
  case OP_BOOL: {
    uint8_t val = *(const uint8_t *)ptr;
    if (val) {
      __builtin_memcpy(buf, "true", 4);
      buf += 4;
    } else {
      __builtin_memcpy(buf, "false", 5);
      buf += 5;
    }
    break;
  }
  case OP_INT:
  case OP_INT64:
    buf += write_int64(buf, *(const int64_t *)ptr);
    break;
  case OP_INT8:
    buf += write_int64(buf, (int64_t)*(const int8_t *)ptr);
    break;
  case OP_INT16:
    buf += write_int64(buf, (int64_t)*(const int16_t *)ptr);
    break;
  case OP_INT32:
    buf += write_int64(buf, (int64_t)*(const int32_t *)ptr);
    break;
  case OP_UINT:
  case OP_UINT64:
    buf += write_uint64(buf, *(const uint64_t *)ptr);
    break;
  case OP_UINT8:
    buf += write_uint64(buf, (uint64_t)*(const uint8_t *)ptr);
    break;
  case OP_UINT16:
    buf += write_uint64(buf, (uint64_t)*(const uint16_t *)ptr);
    break;
  case OP_UINT32:
    buf += write_uint64(buf, (uint64_t)*(const uint32_t *)ptr);
    break;
  case OP_FLOAT32: {
    float fval;
    __builtin_memcpy(&fval, ptr, 4);
    if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_NAN_INF};
    }
    buf += us_write_float32(buf, fval,
                            (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO
                                                              : US_FMT_FIXED);
    break;
  }
  case OP_FLOAT64: {
    double dval;
    __builtin_memcpy(&dval, ptr, 8);
    if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_NAN_INF};
    }
    buf += us_write_float64(buf, dval,
                            (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO
                                                              : US_FMT_FIXED);
    break;
  }
  case OP_STRING: {
    const GoString *s = (const GoString *)ptr;
    int64_t str_need = 2 + (s->len * 6);
    if (__builtin_expect(buf + str_need > bend, 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
    }
    *buf++ = '"';
    if (s->len > 0) {
#ifdef VJ_FAST_STRING_ESCAPE
      buf += escape_string_content_fast(buf, s->ptr, s->len);
#else
      buf += escape_string_content(buf, s->ptr, s->len, flags);
#endif
    }
    *buf++ = '"';
    break;
  }
  case OP_RAW_MESSAGE: {
    const GoSlice *raw = (const GoSlice *)ptr;
    if (raw->data == NULL || raw->len == 0) {
      __builtin_memcpy(buf, "null", 4);
      buf += 4;
    } else {
      if (__builtin_expect(buf + raw->len > bend, 0)) {
        return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
      }
      vj_copy_var(buf, raw->data, raw->len);
      buf += raw->len;
    }
    break;
  }
  case OP_NUMBER: {
    const GoString *s = (const GoString *)ptr;
    if (s->len == 0) {
      *buf++ = '0';
    } else {
      if (__builtin_expect(buf + s->len > bend, 0)) {
        return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
      }
      vj_copy_var(buf, s->ptr, s->len);
      buf += s->len;
    }
    break;
  }
  default:
    /* Should not happen — Go compiler only emits known types. */
    return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
  }
  return (VjPtrEncResult){buf, 0};
}
