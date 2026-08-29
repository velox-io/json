/*
 * interface{} encoding
 *
 * Split along the hot/cold line:
 *
 *  - vj_iface_cache_lookup: binary search of the sorted iface cache.
 *    INLINE in the VM (small, hot for any-heavy loads).
 *  - vj_iface_encode_primitive: the primitive encode switch.  NOINLINE:
 *    its cases fold the itoa/ftoa/escape chains, which is the bulk of the
 *    code; keeping it out shrinks the VM function.  Plain register args,
 *    no result struct: the caller has pre-checked every failure condition
 *    and written the key, so this cannot fail.
 *
 *  The OP_INTERFACE handler owns resolution, pre-checks (NaN/Inf,
 *  variable-length buffer), the key write, and the Blueprint frame push
 *  (SWITCH_OPS).  The key is written only after all failure conditions
 *  are ruled out, so no speculative key write and no undo state exists.
 */

#ifndef VJ_ENCVM_IFACE_H
#define VJ_ENCVM_IFACE_H

#include "types.h"

#include "ftoa.h"
#include "itoa.h"
#include "strfn.h"

/* Binary search the sorted cache by type pointer.
 * Returns the matching entry, NULL on miss. */
INLINE const VjIfaceCacheEntry *vj_iface_cache_lookup(const VjIfaceCacheEntry *cache, int32_t cache_count,
                                                      const void *type_ptr) {
  int32_t lo = 0, hi = cache_count - 1;
  while (lo <= hi) {
    int32_t mid         = (lo + hi) >> 1;
    const void *mid_ptr = cache[mid].type_ptr;
    if (mid_ptr == type_ptr) return &cache[mid];
    if ((uintptr_t)mid_ptr < (uintptr_t)type_ptr) lo = mid + 1;
    else
      hi = mid - 1;
  }
  return NULL;
}

/* Encode one pre-checked primitive value.
 * ptr is the eface data word (deref'd), tag the cache entry tag.
 * Cannot fail: the caller ruled out NaN/Inf and buffer exhaustion and
 * wrote the key.  Returns the advanced buffer pointer. */
NOINLINE static uint8_t *vj_iface_encode_primitive(uint8_t *buf, const uint8_t *ptr, uint16_t tag,
                                                   uint32_t flags) {
  switch (tag) {
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
    buf += vj_write_float32(buf, fval, (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? VJ_FTOA_EXP_AUTO : VJ_FTOA_FIXED);
    break;
  }
  case OP_FLOAT64: {
    double dval;
    __builtin_memcpy(&dval, ptr, 8);
    buf += vj_write_float64(buf, dval, (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? VJ_FTOA_EXP_AUTO : VJ_FTOA_FIXED);
    break;
  }
  case OP_STRING: {
    const GoString *s = (const GoString *)ptr;
    *buf++            = '"';
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
      vj_copy_var(buf, s->ptr, s->len);
      buf += s->len;
    }
    break;
  }
  default:
    __builtin_unreachable(); /* pre-check yielded unknown tags */
  }
  return buf;
}

#endif /* VJ_ENCVM_IFACE_H */
