#include "eface.h"
#include "number.h"
#include "strfn.h"
#include "uscale.h"

#include "vj_compat.h"

/* Result from encoding a single primitive value. */
typedef struct {
  uint8_t *buf;  /* NULL on error */
  int exit_code; /* 0 = ok, otherwise VJ_EXIT_* */
} EncValueResult;

/* Encode a primitive value given its data pointer and type tag.
 * Called when ifaceCache lookup finds a primitive (tag != 0).
 * Fixed-size types assume caller pre-checked buffer space;
 * variable-length types (string, raw_message, number) check inline. */
static NOINLINE EncValueResult encode_primitive_value(uint8_t *buf, const uint8_t *bend, const void *ptr,
                                                      uint16_t etype, uint32_t flags) {
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
    if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0))
      return (EncValueResult){NULL, VJ_EXIT_NAN_INF};
    buf += us_write_float32(buf, fval, (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO : US_FMT_FIXED);
    break;
  }
  case OP_FLOAT64: {
    double dval;
    __builtin_memcpy(&dval, ptr, 8);
    if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0))
      return (EncValueResult){NULL, VJ_EXIT_NAN_INF};
    buf += us_write_float64(buf, dval, (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO : US_FMT_FIXED);
    break;
  }
  case OP_STRING: {
    const GoString *s = (const GoString *)ptr;
    int64_t str_need = 2 + (s->len * 6);
    if (__builtin_expect(buf + str_need > bend, 0))
      return (EncValueResult){NULL, VJ_EXIT_BUF_FULL};
    *buf++ = '"';
    if (s->len > 0) {
#ifdef VJ_FAST_STRING_ESCAPE
#if defined(__AVX2__)
      buf += escape_string_content_fast_sse(buf, s->ptr, s->len);
#else
      buf += escape_string_content_fast(buf, s->ptr, s->len);
#endif
#else
#if defined(__AVX2__)
      buf += escape_string_content_sse(buf, s->ptr, s->len, flags);
#else
      buf += escape_string_content(buf, s->ptr, s->len, flags);
#endif
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
      if (__builtin_expect(buf + raw->len > bend, 0))
        return (EncValueResult){NULL, VJ_EXIT_BUF_FULL};
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
      if (__builtin_expect(buf + s->len > bend, 0))
        return (EncValueResult){NULL, VJ_EXIT_BUF_FULL};
      vj_copy_var(buf, s->ptr, s->len);
      buf += s->len;
    }
    break;
  }
  default:
    return (EncValueResult){NULL, VJ_EXIT_GO_FALLBACK};
  }
  return (EncValueResult){buf, 0};
}

/* Encode a non-nil interface{} value.
 *
 * Reads eface {type_ptr, data_ptr} from iface_ptr, then binary-searches
 * the sorted ifaceCache to decide the encoding strategy:
 *
 *   cache miss        → CACHE_MISS  (yield to Go for compilation)
 *   tag=0, ops=NULL   → YIELD       (not compilable, e.g. map)
 *   tag!=0            → DONE        (primitive, encoded inline)
 *   ops!=NULL          → SWITCH_OPS  (cached Blueprint, caller pushes frame)
 *
 * Caller must: nil-check type_ptr, write key/comma, pre-check buffer. */
VjIfaceResult vj_encode_interface_value(uint8_t *buf, const uint8_t *bend, const uint8_t *iface_ptr,
                                        const VjIfaceCacheEntry *cache, int32_t cache_count, uint32_t flags) {

  const void *type_ptr = *(const void **)iface_ptr;
  const uint8_t *data_ptr = *(const uint8_t **)(iface_ptr + 8);

  /* Binary search the sorted cache by type pointer. */
  uint8_t tag = 0;
  const uint8_t *cached_ops = NULL;
  uint8_t cache_flags = 0;
  int found = 0;

  {
    int32_t lo = 0, hi = cache_count - 1;
    while (lo <= hi) {
      int32_t mid = (lo + hi) >> 1;
      const void *mid_ptr = cache[mid].type_ptr;
      if (mid_ptr == type_ptr) {
        tag = cache[mid].tag;
        cached_ops = cache[mid].ops;
        cache_flags = cache[mid].flags;
        found = 1;
        break;
      }
      if ((uintptr_t)mid_ptr < (uintptr_t)type_ptr)
        lo = mid + 1;
      else
        hi = mid - 1;
    }
  }

  if (__builtin_expect(!found, 0))
    return (VjIfaceResult){buf, NULL, type_ptr, NULL, VJ_IFACE_CACHE_MISS, 0};

  /* Not compilable by C (e.g. map) → yield to Go fallback. */
  if (tag == 0 && cached_ops == NULL)
    return (VjIfaceResult){buf, NULL, NULL, NULL, VJ_IFACE_YIELD, 0};

  /* Primitive → encode inline via tag dispatch. */
  if (tag != 0) {
    EncValueResult r = encode_primitive_value(buf, bend, data_ptr, tag, flags);
    if (__builtin_expect(r.buf == NULL, 0)) {
      if (r.exit_code == VJ_EXIT_NAN_INF)
        return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_NAN_INF, 0};
      if (r.exit_code == VJ_EXIT_GO_FALLBACK)
        return (VjIfaceResult){buf, NULL, NULL, NULL, VJ_IFACE_YIELD, 0};
      return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_BUF_FULL, 0};
    }
    return (VjIfaceResult){r.buf, NULL, NULL, NULL, VJ_IFACE_DONE, 0};
  }

  /* Cached Blueprint → caller pushes VM frame.
   * For INDIRECT types (maps), data_ptr = &eface.data so MAP_STR_ITER can
   * dereference the map pointer correctly (base+0 → map pointer). */
  const uint8_t *bp_base = (cache_flags & VJ_IFACE_FLAG_INDIRECT) ? (const uint8_t *)(iface_ptr + 8) : data_ptr;
  return (VjIfaceResult){buf, cached_ops, type_ptr, bp_base, VJ_IFACE_SWITCH_OPS, cache_flags};
}
