/*
 * Current frame target location resolution and advance.
 *
 * The parser dispatches OBJECT field values and ARRAY element values from
 * separate labels. Binding rides on this split via the OBJ_/ARR_ macro
 * override pairs (see parser.h NDEC_R_OBJ_SCALAR_* / NDEC_R_ARR_SCALAR_*),
 * giving each scalar hook a single container kind:
 *   - object_field_value calls _struct_ hooks; resolution reads
 *     pending_field_idx -> fields[idx].
 *   - array_elem_value calls _slice_ hooks; resolution reads
 *     array_index -> base + index*size.
 * Runtime container_kind dispatch disappears, leaving the compiler with
 * straight-line single-container code. Two yield branches still survive:
 * STRUCT-PTR (PTR field) and SLICE NEED_GROW (cap exhausted).
 *
 * After writing a scalar, the corresponding advance helper clears
 * pending_field_idx (STRUCT) or increments array_index (SLICE).
 *
 * Frame layout: NDEC_FRAME_EXTRA_FIELDS injects binding state into NdecFrame
 * itself (see bind_hooks.h), so binding shares ctx->depth with the parser
 * rather than maintaining a parallel stack.
 */

#ifndef NDEC_BIND_TARGET_H
#define NDEC_BIND_TARGET_H

#include <stdint.h>

#include "go_abi.h"

#define NDEC_BIND_LIKELY(x)   __builtin_expect(!!(x), 1)
#define NDEC_BIND_UNLIKELY(x) __builtin_expect(!!(x), 0)

typedef struct NdecBindTarget {
  uint8_t *dst;
  uint8_t kind;
  uint8_t tag_flags;
  const NdecBindTypeInfo *type; /* child typeinfo (non-null only for struct/array fields) */
} NdecBindTarget;

typedef enum NdecBindTargetStatus {
  NDEC_BTS_OK        = 0, /* target ready, write directly */
  NDEC_BTS_PTR       = 1, /* *T field: caller should yield BEGIN_PTR (STRUCT only) */
  NDEC_BTS_NEED_GROW = 2, /* SLICE frame cap exhausted: caller should yield GROW_SLICE (SLICE only) */
  NDEC_BTS_BAD       = 3, /* frame unusable / no pending field: caller should yield TYPE_MISMATCH */
} NdecBindTargetStatus;

/* STRUCT frame target resolution. The parser's OBJECT field value dispatch
 * label only calls reactor hooks where the parent frame is a STRUCT, so
 * container_kind dispatch is unnecessary. */
INLINE NdecBindTargetStatus ndec_bind_target_for_struct_field(NdecFrame *top, NdecBindTarget *out) {
  /* Defensive: if called when parent kind is not STRUCT, return BAD.
   * The STRUCT hot path is unaffected since kind is a constant after inlining
   * (this function is only called from the OBJECT dispatch label inline). */
  if (NDEC_BIND_UNLIKELY(top->bind_container_kind != NDEC_BK_STRUCT)) {
    return NDEC_BTS_BAD;
  }
  int32_t idx = top->as.struct_.pending_field_idx;
  if (NDEC_BIND_UNLIKELY(idx < 0))
    return NDEC_BTS_BAD;
  const NdecBindTypeInfo *ti = top->bind_type;
  if (NDEC_BIND_UNLIKELY(idx >= (int32_t)ti->field_count))
    return NDEC_BTS_BAD;
  const NdecBindFieldInfo *fi = &ti->fields[idx];
  out->dst                    = top->bind_dst + fi->offset;
  out->kind                   = fi->kind;
  out->tag_flags              = fi->tag_flags;
  out->type                   = fi->type;
  if (NDEC_BIND_UNLIKELY(fi->kind == NDEC_BK_PTR))
    return NDEC_BTS_PTR;
  return NDEC_BTS_OK;
}

/* SLICE frame target resolution. The parser's ARRAY elem value dispatch
 * label only calls reactor hooks where the parent frame is a SLICE, so
 * container_kind dispatch is unnecessary.
 *
 * top->bind_type is the SLICE wrapper (holds elem_kind/elem_size/elem_type/cap_hint),
 * not elem_ti directly. Elem info is read from wrapper fields, saving a dereference.
 *
 * FIXED_ARRAY reuses this function: frame layout is identical to SLICE
 * (bind_dst = inline base, array_index = cursor, array_cap = N).
 * On overflow, returns NEED_GROW; the caller distinguishes via container_kind:
 * SLICE goes to grow yield, FIXED_ARRAY silently ignores. */
INLINE NdecBindTargetStatus ndec_bind_target_for_slice_elem(NdecFrame *top, NdecBindTarget *out) {
  if (NDEC_BIND_UNLIKELY(top->bind_container_kind != NDEC_BK_SLICE &&
                         top->bind_container_kind != NDEC_BK_FIXED_ARRAY)) {
    return NDEC_BTS_BAD;
  }
  if (NDEC_BIND_UNLIKELY(top->as.slice_.array_index >= top->as.slice_.array_cap)) {
    return NDEC_BTS_NEED_GROW;
  }
  const NdecBindTypeInfo *slice_ti = top->bind_type;
  out->dst                         = top->bind_dst + top->as.slice_.array_index * slice_ti->elem_size;
  out->kind                        = slice_ti->elem_kind;
  out->tag_flags                   = 0;
  out->type                        = slice_ti->elem_type;
  return NDEC_BTS_OK;
}

INLINE void ndec_bind_advance_struct_field(NdecFrame *top) {
  top->as.struct_.pending_field_idx = -1;
}

INLINE void ndec_bind_advance_slice_elem(NdecFrame *top) {
  top->as.slice_.array_index++;
}

#endif /* NDEC_BIND_TARGET_H */
