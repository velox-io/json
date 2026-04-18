/*
 * STRUCT container writeval operations.
 *
 * Invoked by OBJ_SCALAR_* hooks when top->bind_container_kind == NDEC_BK_STRUCT.
 * Each writeval resolves the pending field's target, writes the value, and
 * advances pending_field_idx. PTR fields yield BEGIN_PTR so the Go driver
 * allocates the pointee.
 */

#ifndef NDEC_CONTAINER_STRUCT_H
#define NDEC_CONTAINER_STRUCT_H

#include "ndec/core/types.h"
#include "go_abi.h"
#include "ndec_bind_yield.h"
#include "ndec_bind_target.h"
#include "ndec_bind_writer.h"

INLINE int32_t struct_writeval_null(NdecFrame *top, NdecBindUserData *ud) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_struct_field(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_PTR) {
      *(void **)tgt.dst = 0;
      ndec_bind_advance_struct_field(top);
      return NDEC_PROCEED;
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NULL);
  }

  if (NDEC_BIND_UNLIKELY(ndec_bind_write_null(&tgt) != 0)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NULL);
  }
  ndec_bind_advance_struct_field(top);
  return NDEC_PROCEED;
}

INLINE int32_t struct_writeval_bool(NdecFrame *top, NdecBindUserData *ud, int v) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_struct_field(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_PTR) {
      static const uint8_t s_true[]  = {'t','r','u','e'};
      static const uint8_t s_false[] = {'f','a','l','s','e'};
      if (v) {
        NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, s_true, 4, NDEC_YF_NONE);
      } else {
        NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, s_false, 5, NDEC_YF_NONE);
      }
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }

  if (NDEC_BIND_UNLIKELY(ndec_bind_write_bool(&tgt, v) != 0)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }
  ndec_bind_advance_struct_field(top);
  return NDEC_PROCEED;
}

INLINE int32_t struct_writeval_number(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_struct_field(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_PTR) {
      NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, ptr, len, NDEC_YF_NONE);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }

  ndec_bwn_status wst = ndec_bind_write_number(tgt.dst, tgt.kind, ptr, len, ud);
  if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }
  ndec_bind_advance_struct_field(top);
  return NDEC_PROCEED;
}

INLINE int32_t struct_writeval_string(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  NdecBindTarget tgt;
  NdecBindTargetStatus st = ndec_bind_target_for_struct_field(top, &tgt);
  if (NDEC_BIND_UNLIKELY(st != NDEC_BTS_OK)) {
    if (st == NDEC_BTS_PTR) {
      NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BEGIN_PTR, ptr, len, NDEC_YF_NONE);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }

  /* json tag "string" option: the value was quoted in JSON, now unescape
   * the inner string content according to the actual target kind. */
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
        /* number target: parse the inner string as a number */
        ndec_bwn_status wst = ndec_bind_write_number(tgt.dst, tgt.kind, ptr, len, ud);
        if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
          NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
        }
        break;
      }
    }
    ndec_bind_advance_struct_field(top);
    return NDEC_PROCEED;
  }

  /* []byte base64: JSON string value for a []byte / []uint8 field */
  if (tgt.kind == NDEC_BK_SLICE && tgt.type != 0 && tgt.type->elem_kind == NDEC_BK_UINT8) {
    NDEC_BIND_YIELD_RAW(ud, NDEC_YA_BASE64_SLICE, ptr, len, NDEC_YF_NONE);
  }

  if (NDEC_BIND_UNLIKELY(tgt.kind != NDEC_BK_STRING)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }
  ndec_bind_write_string(&tgt, ptr, len);
  ndec_bind_advance_struct_field(top);
  return NDEC_PROCEED;
}

#endif /* NDEC_CONTAINER_STRUCT_H */
