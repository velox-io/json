/*
 * BEGIN_MAP fast path.
 *
 * '{' on a struct's map field or a map's map value only needs to carve a
 * sub-interval out of d.kvBuf for the new MAP frame. Since this involves no
 * GC allocation, the sub-interval bump is performed inline in C; the
 * NDEC_YA_BEGIN_MAP yield is reserved for the buffer-exhausted fallback
 * where Go must grow the kvBuf and rebase outstanding bases.
 *
 * Protocol with the driver:
 *   At entry, the driver pre-fills d.kvBuf based on builtType.mapKVBufBudget
 *   and syncs (base, len, cap) into ud->kv_buf_*. ndec_bind_begin_map_fast
 *   bumps a sub-interval from [kv_buf_len, kv_buf_cap). Returning non-zero
 *   forces the caller to fall back to NDEC_YA_BEGIN_MAP so Go can grow.
 *
 * Invariants on entry (parser has already STACK_PUSH'd):
 *   frames[sp]   = child MAP frame slot (blank)
 *   frames[sp-1] = parent STRUCT / MAP frame
 *
 * Failure return is reserved for cases the caller must surface to Go:
 *   1. ud->kv_buf_cap has insufficient headroom (needs grow + rebase).
 *   2. Parent MAP's elem_type / kvSlotSize validation fails (cleaner to
 *      let Go raise TYPE_MISMATCH than to yield from C).
 */

#ifndef NDEC_BIND_MAP_H
#define NDEC_BIND_MAP_H

#include <stdint.h>

#include "ndec/core/types.h"
#include "go_abi.h"

/* mapKVBufCount: KV buffer slot count for a single MAP frame.
 * Must match mapKVBufCount in ndec/driver.go. A mismatch would cause
 * the BEGIN_MAP yield path (Go) and fast path (C) to compute different
 * sub-interval sizes, leading to out-of-bounds bumps in subsequent hooks.
 * Go-side abi_assert.go has a runtime check as safety net. */
#define NDEC_MAP_KV_BUF_COUNT 32u

/* Writes binding fields into a child MAP frame. base is the sub-interval
 * start within kvBuf (computed by bump in the BEGIN_MAP fast path).
 * parent_field_idx is the field index on the parent STRUCT frame that
 * triggered this MAP push (used for nested path rendering in error context);
 * pass -1 when the parent is not a STRUCT. Equivalent to Go's initMapFrame. */
static inline void ndec_bind_init_map_child(NdecFrame *child,
                                            const NdecBindTypeInfo *map_ti,
                                            uint8_t *base,
                                            int32_t parent_field_idx) {
  child->bind_type             = map_ti;
  child->bind_dst              = (uint8_t *)0;     /* lazy alloc sentinel */
  child->as.map_.kv_count      = 0;
  child->as.map_.kv_buf_cap    = NDEC_MAP_KV_BUF_COUNT;
  child->bind_container_kind   = NDEC_BK_MAP;
  child->parent_field_idx      = parent_field_idx;
  child->bind_slice_hdr        = base;
}

/* Resolves "which field on the parent STRUCT frame is the map" and returns
 * its map typeinfo. parent->pending_field_idx must point to a kind=MAP field.
 * Returns 0 on failure (caller must fall back). */
static inline const NdecBindTypeInfo *ndec_bind_resolve_map_from_struct(
    const NdecFrame *parent) {
  int32_t idx = parent->as.struct_.pending_field_idx;
  if (idx < 0) return 0;
  const NdecBindTypeInfo *parent_ti = parent->bind_type;
  if ((uint32_t)idx >= parent_ti->field_count) return 0;
  const NdecBindFieldInfo *fi = &parent_ti->fields[idx];
  if (fi->kind != NDEC_BK_MAP) return 0;
  return fi->type;
}

/* Resolves the inner map typeinfo from a parent MAP frame.
 * parent->bind_type must be a MAP wrapper with elem_kind=MAP.
 * Returns 0 on failure. */
static inline const NdecBindTypeInfo *ndec_bind_resolve_map_from_map(
    const NdecFrame *parent) {
  const NdecBindTypeInfo *parent_ti = parent->bind_type;
  if (parent_ti->elem_kind != NDEC_BK_MAP) return 0;
  return parent_ti->elem_type;
}

/* Bumps a sub-interval of need bytes from ud->kv_buf. Returns 0 on capacity
 * exhaustion (caller falls back to yield, letting Go grow + rebase outstanding bases). */
static inline uint8_t *ndec_bind_kvbuf_bump(NdecBindUserData *ud, uint32_t need) {
  if (ud->kv_buf_len + need > ud->kv_buf_cap) {
    return 0;
  }
  uint8_t *base = ud->kv_buf_base + ud->kv_buf_len;
  ud->kv_buf_len += need;
  return base;
}

/* BEGIN_MAP fast path. Returns 0 on success; non-zero means caller must
 * yield NDEC_YA_BEGIN_MAP.
 *
 * Caller passes the already-pushed child frame pointer (parser owns
 * push). On success this fills the child binding from a kvBuf bump;
 * on capacity exhaustion the driver fills the binding via the
 * BEGIN_MAP yield handler. */
static inline int ndec_bind_begin_map_fast(NdecFrame *parent,
                                           NdecFrame *child,
                                           NdecBindUserData *ud) {
  const NdecBindTypeInfo *map_ti;
  int32_t parent_idx = -1;
  switch (parent->bind_container_kind) {
    case NDEC_BK_STRUCT:
      parent_idx = parent->as.struct_.pending_field_idx;
      map_ti = ndec_bind_resolve_map_from_struct(parent);
      break;
    case NDEC_BK_MAP:
      map_ti = ndec_bind_resolve_map_from_map(parent);
      break;
    default:
      return 1;
  }
  if (map_ti == 0 || map_ti->elem_type == 0) return 1;

  uint32_t slot_size = 16u + ((map_ti->elem_size + 7u) & ~7u);
  uint32_t need = slot_size * NDEC_MAP_KV_BUF_COUNT;
  uint8_t *base = ndec_bind_kvbuf_bump(ud, need);
  if (base == 0) return 1;

  ndec_bind_init_map_child(child, map_ti, base, parent_idx);
  return 0;
}

INLINE uint8_t *ndec_bind_map_value_ptr(NdecFrame *map_frame) {
  const NdecBindTypeInfo *map_ti = map_frame->bind_type;
  uint32_t slot_size = 16 + ((map_ti->elem_size + 7u) & ~7u);
  uint8_t *base = (uint8_t *)map_frame->bind_slice_hdr;
  return base + (size_t)map_frame->as.map_.kv_count * slot_size + 16;
}

/* MAP frame advance: increments kv_count. Yield FLUSH_MAP when buffer is full. */
#define NDEC_BIND_MAP_ADVANCE(ud_, frame_)                                    \
  do {                                                                        \
    (frame_)->as.map_.kv_count++;                                             \
    if ((frame_)->as.map_.kv_count >= (frame_)->as.map_.kv_buf_cap) {         \
      (ud_)->pending_action = (uint32_t)NDEC_YA_FLUSH_MAP;                    \
      (ud_)->yield_flags    = NDEC_YF_NONE;                                   \
      return NDEC_YIELD;                                                      \
    }                                                                         \
    return NDEC_PROCEED;                                                      \
  } while (0)

#endif /* NDEC_BIND_MAP_H */
