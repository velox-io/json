/*
 * ROOT container writeval operations.
 *
 * The top-level JSON value lands at the root frame (frames[0].bind_type /
 * bind_dst). Unlike STRUCT fields, SLICE elements, or MAP values, the root
 * frame has no parent + index pair; writes act directly on the
 * caller-supplied *T.
 *
 * Dispatch by root->bind_type->kind:
 *   - scalar (BOOL / INT* / UINT* / FLOAT* / STRING): write directly.
 *   - PTR: yield BEGIN_PTR so the driver allocates the pointee and writes
 *          through to it.
 *   - other (STRUCT / SLICE / MAP / FIXED_ARRAY): TYPE_MISMATCH (a JSON
 *     scalar against a container root is a stdlib error, e.g. `42` ->
 *     *struct{}). null is the documented exception: stdlib treats null
 *     against a container root as setting a nil header.
 */

#ifndef NDEC_CONTAINER_ROOT_H
#define NDEC_CONTAINER_ROOT_H

#include "ndec/core/types.h"
#include "go_abi.h"
#include "ndec_bind_yield.h"
#include "ndec_bind_target.h"
#include "ndec_bind_writer.h"

/* Build a NdecBindTarget from the root frame. Used by bool / number /
 * string write paths. PTR root returns BTS_PTR so the caller can
 * yield BEGIN_PTR. */
INLINE NdecBindTargetStatus ndec_bind_target_for_root(NdecFrame *root,
                                                       NdecBindTarget *out) {
  out->dst       = root->bind_dst;
  out->kind      = root->bind_type->kind;
  out->type      = root->bind_type;
  out->tag_flags = 0;
  if (out->kind == NDEC_BK_PTR) return NDEC_BTS_PTR;
  return NDEC_BTS_OK;
}

INLINE int32_t root_writeval_null(NdecFrame *root, NdecBindUserData *ud) {
  /* stdlib: null for root target, ordered by kind:
   *   PTR    : *root.bind_dst = nil
   *   SLICE  : write empty slice header (nil, 0, 0)
   *   MAP    : *root.bind_dst = nil
   *   STRUCT / FIXED_ARRAY / any scalar : no-op (keep caller prefill)
   * scalar no-op is stdlib semantics: var x int = 5; Unmarshal("null", &x) -> x still 5.
   */
  uint8_t kind = root->bind_type->kind;
  if (kind == NDEC_BK_PTR || kind == NDEC_BK_MAP) {
    *(void **)root->bind_dst = 0;
    return NDEC_PROCEED;
  }
  if (kind == NDEC_BK_SLICE) {
    NdecGoSliceHeader *hdr = (NdecGoSliceHeader *)root->bind_dst;
    hdr->data = 0;
    hdr->len  = 0;
    hdr->cap  = 0;
    return NDEC_PROCEED;
  }
  /* scalar / struct / fixed_array: no-op, keep pre-filled value. */
  return NDEC_PROCEED;
}

INLINE int32_t root_writeval_bool(NdecFrame *root, NdecBindUserData *ud,
                                   int v) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_root(root, &tgt);
  if (NDEC_BIND_UNLIKELY(st == NDEC_BTS_PTR)) {
    static const uint8_t s_true[]  = {'t','r','u','e'};
    static const uint8_t s_false[] = {'f','a','l','s','e'};
    if (v) NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, s_true,  4, NDEC_YF_NONE);
    else   NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, s_false, 5, NDEC_YF_NONE);
  }
  if (NDEC_BIND_UNLIKELY(ndec_bind_write_bool(&tgt, v) != 0)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }
  /* root frame needs no advance: follow-up goes to ndec_root_done,
   * never returns to this frame. */
  return NDEC_PROCEED;
}

INLINE int32_t root_writeval_number(NdecFrame *root, NdecBindUserData *ud,
                                     const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_root(root, &tgt);
  if (NDEC_BIND_UNLIKELY(st == NDEC_BTS_PTR)) {
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, ptr, len, NDEC_YF_NONE);
  }
  ndec_bwn_status wst = ndec_bind_write_number(tgt.dst, tgt.kind, ptr, len, ud);
  if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }
  return NDEC_PROCEED;
}

INLINE int32_t root_writeval_string(NdecFrame *root, NdecBindUserData *ud,
                                     const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_root(root, &tgt);
  if (NDEC_BIND_UNLIKELY(st == NDEC_BTS_PTR)) {
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, ptr, len, NDEC_YF_NONE);
  }
  if (NDEC_BIND_UNLIKELY(tgt.kind != NDEC_BK_STRING)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }
  ndec_bind_write_string(&tgt, ptr, len);
  return NDEC_PROCEED;
}

#endif /* NDEC_CONTAINER_ROOT_H */
