/*
 * Container-independent scalar value writer.
 *
 * Decodes a JSON token into the location described by NdecBindTarget. Called
 * by container-specific writeval functions (struct_writeval, slice_writeval,
 * map_writeval, ...) which own the container framing; this layer is
 * deliberately blind to which container the dst lives in.
 *
 * Returns 0 on success, non-zero on type mismatch.
 */

#ifndef NDEC_BIND_WRITER_H
#define NDEC_BIND_WRITER_H

#include "ndec_bind_number.h"
#include "ndec_bind_string.h"

INLINE int ndec_bind_write_null(NdecBindTarget *tgt) {
  switch (tgt->kind) {
    case NDEC_BK_BOOL:    *(uint8_t*)tgt->dst  = 0; return 0;
    case NDEC_BK_INT8:    *(int8_t*)tgt->dst   = 0; return 0;
    case NDEC_BK_INT16:   *(int16_t*)tgt->dst  = 0; return 0;
    case NDEC_BK_INT32:   *(int32_t*)tgt->dst  = 0; return 0;
    case NDEC_BK_INT64:
    case NDEC_BK_INT:     *(int64_t*)tgt->dst  = 0; return 0;
    case NDEC_BK_UINT8:   *(uint8_t*)tgt->dst  = 0; return 0;
    case NDEC_BK_UINT16:  *(uint16_t*)tgt->dst = 0; return 0;
    case NDEC_BK_UINT32:  *(uint32_t*)tgt->dst = 0; return 0;
    case NDEC_BK_UINT64:
    case NDEC_BK_UINT:    *(uint64_t*)tgt->dst = 0; return 0;
    case NDEC_BK_FLOAT32: *(float*)tgt->dst    = 0.0f; return 0;
    case NDEC_BK_FLOAT64: *(double*)tgt->dst   = 0.0; return 0;
    case NDEC_BK_STRING: {
      NdecGoStringHeader *h = (NdecGoStringHeader *)tgt->dst;
      h->data = 0;
      h->len  = 0;
      return 0;
    }
    case NDEC_BK_SLICE:
      *(void **)tgt->dst        = 0;
      *(intptr_t *)(tgt->dst + 8)  = 0;
      *(intptr_t *)(tgt->dst + 16) = 0;
      return 0;
    case NDEC_BK_MAP:
      *(void **)tgt->dst = 0;
      return 0;
    case NDEC_BK_STRUCT:
      return 0;
    /* [N]T: under stdlib semantics, JSON null does not mutate the array's
     * existing values (preserving the caller's pre-filled content).
     * Same no-op path as STRUCT. */
    case NDEC_BK_FIXED_ARRAY:
      return 0;
    default:
      return 1;
  }
}

INLINE int ndec_bind_write_bool(NdecBindTarget *tgt, int v) {
  if (NDEC_BIND_UNLIKELY(tgt->kind != NDEC_BK_BOOL)) return 1;
  *(uint8_t*)tgt->dst = (uint8_t)(v ? 1 : 0);
  return 0;
}

INLINE void ndec_bind_write_string(NdecBindTarget *tgt, const uint8_t *ptr, uint32_t len) {
  NdecGoStringHeader *h = (NdecGoStringHeader *)tgt->dst;
  h->data = ptr;
  h->len  = (intptr_t)len;
}

INLINE ndec_bwn_status ndec_bind_write_number_tgt(NdecBindTarget *tgt,
    const uint8_t *ptr, uint32_t len, NdecBindUserData *ud) {
  return ndec_bind_write_number(tgt->dst, tgt->kind, ptr, len, ud);
}

#endif /* NDEC_BIND_WRITER_H */
