#include "iface.h"
#include "number.h"
#include "strfn.h"
#include "uscale.h"

/* ================================================================
 *  vj_encode_interface_value — out-of-line interface encoder
 *
 *  Handles a non-nil interface value:
 *    1. Binary search the interface cache (single pass)
 *    2. Cache miss → return CACHE_MISS
 *    3. Found but not compilable (tag=0, ops=NULL) → return YIELD
 *    4. Primitive tag → encode via vj_encode_ptr_value, return DONE
 *    5. Cached Blueprint → return SWITCH_OPS
 *
 *  Parameters:
 *    buf, bend    — output buffer (caller already wrote key+comma)
 *    iface_ptr    — pointer to eface {type_ptr, data_ptr} in struct
 *    cache, count — sorted interface cache array
 *    flags        — VjEncFlags bitmask
 *
 *  The caller is responsible for:
 *    - Nil check (type_ptr == NULL → write "null")
 *    - Writing key+comma (VM_WRITE_KEY) before calling
 *    - Buffer check for fixed-size worst case before calling
 *    - Interpreting the returned action code
 * ================================================================ */

static __attribute__((noinline)) VjPtrEncResult
vj_encode_ptr_value(uint8_t *buf, const uint8_t *bend, const void *ptr,
                    uint16_t etype, uint32_t flags) {

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
      return (VjPtrEncResult){NULL, VJ_EXIT_NAN_INF};
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
      return (VjPtrEncResult){NULL, VJ_EXIT_NAN_INF};
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
      return (VjPtrEncResult){NULL, VJ_EXIT_BUF_FULL};
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
        return (VjPtrEncResult){NULL, VJ_EXIT_BUF_FULL};
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
        return (VjPtrEncResult){NULL, VJ_EXIT_BUF_FULL};
      }
      vj_copy_var(buf, s->ptr, s->len);
      buf += s->len;
    }
    break;
  }
  default:
    /* Should not happen — Go compiler only emits known types. */
    return (VjPtrEncResult){NULL, VJ_EXIT_BUF_FULL};
  }
  return (VjPtrEncResult){buf, 0};
}

VjIfaceResult vj_encode_interface_value(uint8_t *buf, const uint8_t *bend,
                                        const uint8_t *iface_ptr,
                                        const VjIfaceCacheEntry *cache,
                                        int32_t cache_count, uint32_t flags) {

  const void *type_ptr = *(const void **)iface_ptr;
  const uint8_t *data_ptr = *(const uint8_t **)(iface_ptr + 8);

  /* ---- Single binary search with found flag ---- */
  uint8_t tag = 0;
  const VjOpStep *cached_ops = NULL;
  int found = 0;

  {
    int32_t lo = 0, hi = cache_count - 1;
    while (lo <= hi) {
      int32_t mid = (lo + hi) >> 1;
      const void *mid_ptr = cache[mid].type_ptr;
      if (mid_ptr == type_ptr) {
        tag = cache[mid].tag;
        cached_ops = cache[mid].ops;
        found = 1;
        break;
      }
      if ((uintptr_t)mid_ptr < (uintptr_t)type_ptr)
        lo = mid + 1;
      else
        hi = mid - 1;
    }
  }

  /* Not in cache → yield to Go for compilation */
  if (__builtin_expect(!found, 0)) {
    return (VjIfaceResult){buf, NULL, type_ptr, NULL, VJ_IFACE_CACHE_MISS};
  }

  /* Found but not compilable by C (tag=0, ops=NULL) → yield fallback.
   * Since opcodes are 1-based, tag=0 unambiguously means "no primitive tag". */
  if (tag == 0 && cached_ops == NULL) {
    return (VjIfaceResult){buf, NULL, NULL, NULL, VJ_IFACE_YIELD};
  }

  /* ---- Primitive type (tag != 0) → encode inline ---- */
  if (tag != 0) {
    /* Tag is the opcode directly (opcodes are 1-based, so tag >= 1). */
    VjPtrEncResult r = vj_encode_ptr_value(buf, bend, data_ptr, tag, flags);
    if (__builtin_expect(r.buf == NULL, 0)) {
      if (r.exit_code == VJ_EXIT_NAN_INF)
        return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_NAN_INF};
      return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_BUF_FULL};
    }
    return (VjIfaceResult){r.buf, NULL, NULL, NULL, VJ_IFACE_DONE};
  }

  /* ---- Cached Blueprint (ops != NULL) → caller pushes frame ---- */
  return (VjIfaceResult){buf, cached_ops, type_ptr, data_ptr,
                         VJ_IFACE_SWITCH_OPS};
}
