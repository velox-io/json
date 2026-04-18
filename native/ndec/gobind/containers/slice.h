/*
 * SLICE container writeval operations.
 *
 * Invoked by ARR_SCALAR_* hooks when the current frame is SLICE or
 * FIXED_ARRAY. SLICE-cap-exhausted yields GROW_SLICE; FIXED_ARRAY overflow
 * silently drops the element to match stdlib semantics.
 */

#ifndef NDEC_CONTAINER_SLICE_H
#define NDEC_CONTAINER_SLICE_H

#include "ndec/core/types.h"
#include "go_abi.h"
#include "ndec_bind_yield.h"
#include "ndec_bind_target.h"
#include "ndec_bind_writer.h"

/* Check if the slice elem is a pointer and yield to Go for alloc+write.
 * Returns 1 if the yield was taken (caller should NOT proceed). */
#define NDEC_SLICE_YIELD_PTR_IF_NEEDED(top_, ud_, raw_ptr_, raw_len_, flags_)  \
  do {                                                                          \
    const NdecBindTypeInfo *_si = (top_)->bind_type;                            \
    if (_si->elem_kind == NDEC_BK_PTR) {                                        \
      NDEC_BIND_YIELD_RAW((ud_), NDEC_YA_GROW_SLICE, (raw_ptr_), (raw_len_),   \
                          (flags_) | NDEC_YF_GROW_ALLOC_PTR);                   \
    }                                                                           \
  } while (0)

#define NDEC_SLICE_GROW_OR_PTR(ud_, raw_ptr_, raw_len_, flags_)                  \
  do {                                                                            \
    const NdecBindTypeInfo *_si = top->bind_type;                                 \
    NDEC_BIND_YIELD_RAW((ud_), NDEC_YA_GROW_SLICE, (raw_ptr_), (raw_len_),       \
                        (_si->elem_kind == NDEC_BK_PTR) ?                        \
                        ((flags_) | NDEC_YF_GROW_ALLOC_PTR) : (flags_));          \
  } while (0)

/* NEED_GROW on a FIXED_ARRAY frame is an overflow signal: under stdlib
 * semantics, elements beyond N are silently ignored. The frame's
 * array_index/array_cap are not mutated (no advance, no write). Child scalar
 * hooks can use this branch uniformly. */
#define NDEC_FIXEDARR_OVERFLOW_PROCEED(top_)                                    \
  do {                                                                          \
    if ((top_)->bind_container_kind == NDEC_BK_FIXED_ARRAY) {                   \
      return NDEC_PROCEED;                                                      \
    }                                                                           \
  } while (0)

INLINE int32_t slice_writeval_null(NdecFrame *top, NdecBindUserData *ud) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_slice_elem(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_NEED_GROW) {
      NDEC_FIXEDARR_OVERFLOW_PROCEED(top);
      static const uint8_t s_null[] = {'n','u','l','l'};
      NDEC_SLICE_GROW_OR_PTR(ud, s_null, 4, NDEC_YF_GROW_NULL);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NULL);
  }

  /* PTR elem: yield GROW_SLICE with ALLOC_PTR flag */
  if (NDEC_BIND_UNLIKELY(tgt.kind == NDEC_BK_PTR)) {
    static const uint8_t s_null[] = {'n','u','l','l'};
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_GROW_SLICE, s_null, 4,
                        NDEC_YF_GROW_NULL | NDEC_YF_GROW_ALLOC_PTR);
  }

  if (NDEC_BIND_UNLIKELY(ndec_bind_write_null(&tgt) != 0)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NULL);
  }
  ndec_bind_advance_slice_elem(top);
  return NDEC_PROCEED;
}

INLINE int32_t slice_writeval_bool(NdecFrame *top, NdecBindUserData *ud, int v) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_slice_elem(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_NEED_GROW) {
      NDEC_FIXEDARR_OVERFLOW_PROCEED(top);
      static const uint8_t s_true[]  = {'t','r','u','e'};
      static const uint8_t s_false[] = {'f','a','l','s','e'};
      if (v) {
        NDEC_SLICE_GROW_OR_PTR(ud, s_true, 4, NDEC_YF_NONE);
      } else {
        NDEC_SLICE_GROW_OR_PTR(ud, s_false, 5, NDEC_YF_NONE);
      }
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }

  if (NDEC_BIND_UNLIKELY(tgt.kind == NDEC_BK_PTR)) {
    static const uint8_t s_true[]  = {'t','r','u','e'};
    static const uint8_t s_false[] = {'f','a','l','s','e'};
    if (v) {
      NDEC_BIND_YIELD_RAW(ud, NDEC_YA_GROW_SLICE, s_true, 4, NDEC_YF_GROW_ALLOC_PTR);
    } else {
      NDEC_BIND_YIELD_RAW(ud, NDEC_YA_GROW_SLICE, s_false, 5, NDEC_YF_GROW_ALLOC_PTR);
    }
  }

  if (NDEC_BIND_UNLIKELY(ndec_bind_write_bool(&tgt, v) != 0)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }
  ndec_bind_advance_slice_elem(top);
  return NDEC_PROCEED;
}

INLINE int32_t slice_writeval_number(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_slice_elem(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_NEED_GROW) {
      NDEC_FIXEDARR_OVERFLOW_PROCEED(top);
      NDEC_SLICE_GROW_OR_PTR(ud, ptr, len, NDEC_YF_NONE);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }

  if (NDEC_BIND_UNLIKELY(tgt.kind == NDEC_BK_PTR)) {
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_GROW_SLICE, ptr, len, NDEC_YF_GROW_ALLOC_PTR);
  }

  ndec_bwn_status wst = ndec_bind_write_number(tgt.dst, tgt.kind, ptr, len, ud);
  if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }
  ndec_bind_advance_slice_elem(top);
  return NDEC_PROCEED;
}

INLINE int32_t slice_writeval_string(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_slice_elem(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_NEED_GROW) {
      NDEC_FIXEDARR_OVERFLOW_PROCEED(top);
      NDEC_SLICE_GROW_OR_PTR(ud, ptr, len, NDEC_YF_NONE);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }

  /* quoted string option */
  if (tgt.tag_flags & NDEC_BFF_QUOTED) {
    switch (tgt.kind) {
      case NDEC_BK_STRING:
        ndec_bind_write_string(&tgt, ptr, len);
        break;
      case NDEC_BK_BOOL:
        if (len == 4 && ptr[0] == 't' && ptr[1] == 'r' && ptr[2] == 'u' && ptr[3] == 'e') {
          *(uint8_t*)tgt.dst = 1;
        } else if (len == 5 && ptr[0] == 'f' && ptr[1] == 'a' && ptr[2] == 'l' && ptr[3] == 's' && ptr[4] == 'e') {
          *(uint8_t*)tgt.dst = 0;
        } else {
          NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
        }
        break;
      default: {
        ndec_bwn_status wst = ndec_bind_write_number(tgt.dst, tgt.kind, ptr, len, ud);
        if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
          NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
        }
        break;
      }
    }
    ndec_bind_advance_slice_elem(top);
    return NDEC_PROCEED;
  }

  /* PTR elem for string value (*string in slice) */
  if (NDEC_BIND_UNLIKELY(tgt.kind == NDEC_BK_PTR)) {
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_GROW_SLICE, ptr, len, NDEC_YF_GROW_ALLOC_PTR);
  }

  if (NDEC_BIND_UNLIKELY(tgt.kind != NDEC_BK_STRING)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }
  ndec_bind_write_string(&tgt, ptr, len);
  ndec_bind_advance_slice_elem(top);
  return NDEC_PROCEED;
}

#endif /* NDEC_CONTAINER_SLICE_H */
