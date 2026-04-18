/*
 * MAP container writeval operations.
 *
 * Invoked by OBJ_SCALAR_* hooks when top->bind_container_kind == NDEC_BK_MAP.
 * Each writeval locates the current KV slot's value pointer, writes the
 * value, and advances kv_count. A full buffer triggers FLUSH_MAP so the
 * driver can drain via batch mapassign.
 */

#ifndef NDEC_CONTAINER_MAP_H
#define NDEC_CONTAINER_MAP_H

#include "ndec/core/types.h"
#include "go_abi.h"
#include "ndec_bind_yield.h"
#include "ndec_bind_writer.h"
#include "ndec_bind_map.h"

INLINE int32_t map_writeval_null(NdecFrame *top, NdecBindUserData *ud) {
  const NdecBindTypeInfo *map_ti = top->bind_type;
  uint8_t *value_ptr = ndec_bind_map_value_ptr(top);
  if (map_ti->elem_kind == NDEC_BK_STRUCT) {
    __builtin_memset(value_ptr, 0, map_ti->elem_size);
  } else if (map_ti->elem_kind == NDEC_BK_PTR) {
    *(void **)value_ptr = 0;
  } else {
    NdecBindTarget tgt;
    tgt.dst = value_ptr;
    tgt.kind = map_ti->elem_kind;
    tgt.tag_flags = 0;
    tgt.type = map_ti->elem_type;
    if (NDEC_BIND_UNLIKELY(ndec_bind_write_null(&tgt) != 0)) {
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NULL);
    }
  }
  NDEC_BIND_MAP_ADVANCE(ud, top);
}

INLINE int32_t map_writeval_bool(NdecFrame *top, NdecBindUserData *ud, int v) {
  const NdecBindTypeInfo *map_ti = top->bind_type;
  if (map_ti->elem_kind == NDEC_BK_PTR) {
    static const uint8_t s_true[]  = {'t','r','u','e'};
    static const uint8_t s_false[] = {'f','a','l','s','e'};
    if (v) {
      NDEC_BIND_YIELD_MAP_VALUE_PTR_RAW(ud, s_true, 4);
    } else {
      NDEC_BIND_YIELD_MAP_VALUE_PTR_RAW(ud, s_false, 5);
    }
  }
  if (NDEC_BIND_UNLIKELY(map_ti->elem_kind != NDEC_BK_BOOL)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_BOOL);
  }
  *ndec_bind_map_value_ptr(top) = (uint8_t)(v ? 1 : 0);
  NDEC_BIND_MAP_ADVANCE(ud, top);
}

INLINE int32_t map_writeval_number(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  const NdecBindTypeInfo *map_ti = top->bind_type;
  if (map_ti->elem_kind == NDEC_BK_PTR) {
    NDEC_BIND_YIELD_MAP_VALUE_PTR_RAW(ud, ptr, len);
  }
  ndec_bwn_status wst = ndec_bind_write_number(
      ndec_bind_map_value_ptr(top), map_ti->elem_kind, ptr, len, ud);
  if (NDEC_BIND_UNLIKELY(wst != NDEC_BWN_OK)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_NUMBER);
  }
  NDEC_BIND_MAP_ADVANCE(ud, top);
}

INLINE int32_t map_writeval_string(NdecFrame *top, NdecBindUserData *ud,
    const uint8_t *ptr, uint32_t len) {
  const NdecBindTypeInfo *map_ti = top->bind_type;

  /* json tag "string" option on map fields: the MAP frame doesn't carry
   * per-KV tag_flags, but the elem_kind tells us the value type. When the
   * string hook fires for a non-string map value, the JSON was "quoted". */
  if (map_ti->elem_kind == NDEC_BK_PTR) {
    NDEC_BIND_YIELD_MAP_VALUE_PTR_RAW(ud, ptr, len);
  }
  if (NDEC_BIND_UNLIKELY(map_ti->elem_kind != NDEC_BK_STRING)) {
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
  }
  NdecGoStringHeader *vh = (NdecGoStringHeader *)ndec_bind_map_value_ptr(top);
  vh->data = ptr;
  vh->len  = (intptr_t)len;
  NDEC_BIND_MAP_ADVANCE(ud, top);
}

#endif /* NDEC_CONTAINER_MAP_H */
