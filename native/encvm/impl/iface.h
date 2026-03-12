/*
 * iface.h -- Velox JSON C Engine: Go Interface Value Encoder
 *
 * Out-of-line encoder for interface{} (eface) values.
 * Marked noinline to keep the VM's hot dispatch loop compact
 * and avoid register spill from the cold interface logic
 * (binary search, primitive tag switch, Blueprint dispatch).
 *
 * Depends on: types.h, number.h, strfn.h,
 *             ryu.h, pointer.h.
 */

#ifndef VJ_ENCVM_IFACE_H
#define VJ_ENCVM_IFACE_H

/* ================================================================
 *  Interface encode action codes
 *
 *  Returned to the VM caller so it can take the appropriate action
 *  (advance buf, yield to Go, push frame, or report error).
 * ================================================================ */

enum VjIfaceAction {
  VJ_IFACE_DONE       = 0,  /* primitive encoded into buf */
  VJ_IFACE_YIELD      = 1,  /* fallback to Go (not compilable or unsupported tag) */
  VJ_IFACE_CACHE_MISS = 2,  /* type not in cache, yield for Go compilation */
  VJ_IFACE_SWITCH_OPS = 3,  /* cached Blueprint found, caller pushes frame */
  VJ_IFACE_BUF_FULL   = 4,  /* buffer space insufficient */
  VJ_IFACE_NAN_INF    = 5,  /* float NaN/Inf encountered */
};

/* Result struct — returned by value (fits in 3-4 registers on arm64/x86_64). */
typedef struct {
  uint8_t         *buf;        /* advanced buffer pointer (valid when action=DONE) */
  const VjOpStep  *cached_ops; /* Blueprint ops (valid when action=SWITCH_OPS) */
  const void      *type_ptr;   /* eface.type_ptr (valid when action=CACHE_MISS) */
  const uint8_t   *data_ptr;   /* eface.data_ptr (valid when action=SWITCH_OPS) */
  int32_t          action;     /* VjIfaceAction */
} VjIfaceResult;

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

static __attribute__((noinline)) VjIfaceResult
vj_encode_interface_value(
    uint8_t *buf, const uint8_t *bend,
    const uint8_t *iface_ptr,
    const VjIfaceCacheEntry *cache, int32_t cache_count,
    uint32_t flags)
{
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
   * Tag is stored as (opcode + 1) so that tag=0 is unambiguous for
   * "no primitive tag" (needed because OP_BOOL == 0). */
  if (tag == 0 && cached_ops == NULL) {
    return (VjIfaceResult){buf, NULL, NULL, NULL, VJ_IFACE_YIELD};
  }

  /* ---- Primitive type (tag != 0) → encode inline ---- */
  if (tag != 0) {
    /* Subtract 1 to recover the actual opcode (tag was stored as opcode+1). */
    VjPtrEncResult r = vj_encode_ptr_value(buf, bend, data_ptr, tag - 1, flags);
    if (__builtin_expect(r.buf == NULL, 0)) {
      if (r.error == VJ_ERR_NAN_INF)
        return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_NAN_INF};
      return (VjIfaceResult){NULL, NULL, NULL, NULL, VJ_IFACE_BUF_FULL};
    }
    return (VjIfaceResult){r.buf, NULL, NULL, NULL, VJ_IFACE_DONE};
  }

  /* ---- Cached Blueprint (ops != NULL) → caller pushes frame ---- */
  return (VjIfaceResult){buf, cached_ops, type_ptr, data_ptr, VJ_IFACE_SWITCH_OPS};
}

#endif /* VJ_ENCVM_IFACE_H */
