/* The macros in this header are implementation vocabulary for
 * ndec_bind_parse_inner. They capture its cursor, src, m, frames, depth,
 * cur_dst, cur_type, cur_count, cur_aux, and str_p locals as well as its
 * labels. Expanding them in any other scope is invalid. The inline helpers
 * have no such scope dependency.
 */
#ifndef NDEC_PRIMITIVE_H
#define NDEC_PRIMITIVE_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/bind_bridge.h"
#include "ndec/core/atof.h"
#include "ndec/core/atoi.h"
#include "ndec/core/number.h"
#include "ndec/core/tape.h"
#include "ndec/cursor.h"
#include "ndec/string.h"

/* Stage 1 validates only the opening byte. The full atom must match and be
 * followed by a JSON delimiter so prefixes such as `truee` are rejected.
 * The input has at least 64 bytes of 0x20 padding, which keeps the atom and
 * delimiter reads in bounds while making complete atoms at EOF valid and
 * truncated atoms invalid. memcpy provides alignment-safe word loads.
 */
INLINE int bind_validate_atom(const uint8_t *value, uint8_t tag) {
  if (tag == 'n' || tag == 't') {
    uint32_t sv, av;
    __builtin_memcpy(&sv, value, 4);
    __builtin_memcpy(&av, tag == 'n' ? "null" : "true", 4);
    if (sv != av || UNLIKELY(is_non_delim(value[4]))) return -1;
    return 0;
  }
  if (tag == 'f') {
    uint32_t sv, av;
    __builtin_memcpy(&sv, value + 1, 4);
    __builtin_memcpy(&av, "alse", 4);
    if (sv != av || UNLIKELY(is_non_delim(value[5]))) return -1;
    return 0;
  }
  return -1;
}

/* src has at least 64 bytes of 0x20 padding. The parser discovers the token
 * boundary and returns it through end_out so callers can validate the following
 * delimiter without a prescan.
 */
INLINE int bind_write_number(const uint8_t *src, uint8_t kind, uint8_t *dst, atof_ctx *atof,
                             const uint8_t **end_out) {
  switch (kind) {
  case BIND_KIND_FLOAT64: {
    double dv;
    if (UNLIKELY(ndec_parse_double_padded(src, &dv, atof, end_out))) return -1;
    if (UNLIKELY(!__builtin_isfinite(dv))) return -1;
    __builtin_memcpy(dst, &dv, sizeof(dv));
    return 0;
  }
  case BIND_KIND_FLOAT32: {
    float fv;
    if (UNLIKELY(ndec_parse_float32_padded(src, &fv, atof, end_out))) return -1;
    if (UNLIKELY(!__builtin_isfinite(fv))) return -1;
    __builtin_memcpy(dst, &fv, sizeof(fv));
    return 0;
  }
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64: {
    int64_t v;
    if (ndec_parse_int64_padded(src, &v, end_out) != NDEC_NUM_OK) return -1;
    switch (kind) {
    case BIND_KIND_INT8:
      if (v < -128 || v > 127) return -1;
      *(int8_t *)dst = (int8_t)v;
      return 0;
    case BIND_KIND_INT16:
      if (v < -32768 || v > 32767) return -1;
      *(int16_t *)dst = (int16_t)v;
      return 0;
    case BIND_KIND_INT32:
      if (v < -2147483648LL || v > 2147483647LL) return -1;
      *(int32_t *)dst = (int32_t)v;
      return 0;
    case BIND_KIND_INT64:
      *(int64_t *)dst = v;
      return 0;
    case BIND_KIND_INT:
      *(intptr_t *)dst = (intptr_t)v;
      return 0;
    }
    return -1;
  }
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64: {
    int64_t v;
    ndec_num_status s = ndec_parse_int64_padded(src, &v, end_out);
    if (s == NDEC_NUM_OK) {
      if (v < 0) return -1;
      uint64_t uv = (uint64_t)v;
      switch (kind) {
      case BIND_KIND_UINT8:
        if (uv > 0xFF) return -1;
        *(uint8_t *)dst = (uint8_t)uv;
        return 0;
      case BIND_KIND_UINT16:
        if (uv > 0xFFFF) return -1;
        *(uint16_t *)dst = (uint16_t)uv;
        return 0;
      case BIND_KIND_UINT32:
        if (uv > 0xFFFFFFFFu) return -1;
        *(uint32_t *)dst = (uint32_t)uv;
        return 0;
      case BIND_KIND_UINT64:
        *(uint64_t *)dst = uv;
        return 0;
      case BIND_KIND_UINT:
        *(uintptr_t *)dst = (uintptr_t)uv;
        return 0;
      }
    }
    if (s == NDEC_NUM_OVERFLOW) {
      uint64_t uv;
      if (ndec_parse_uint64_padded(src, &uv, end_out) != NDEC_NUM_OK) return -1;
      switch (kind) {
      case BIND_KIND_UINT8:
        if (uv > 0xFF) return -1;
        *(uint8_t *)dst = (uint8_t)uv;
        return 0;
      case BIND_KIND_UINT16:
        if (uv > 0xFFFF) return -1;
        *(uint16_t *)dst = (uint16_t)uv;
        return 0;
      case BIND_KIND_UINT32:
        if (uv > 0xFFFFFFFFu) return -1;
        *(uint32_t *)dst = (uint32_t)uv;
        return 0;
      case BIND_KIND_UINT64:
        *(uint64_t *)dst = uv;
        return 0;
      case BIND_KIND_UINT:
        *(uintptr_t *)dst = (uintptr_t)uv;
        return 0;
      }
    }
    return -1;
  }
  }
  return -1;
}

/* For a `,string` scalar, data is the already decoded outer string body.
 * Numeric parsing is bounded by len. A string target contains a second JSON
 * string literal, which is decoded into str_arena before its header is stored.
 */
INLINE int bind_write_quoted_scalar(uint8_t **str_pp, const uint8_t *data, uint32_t len, uint8_t kind,
                                    uint8_t *dst, atof_ctx *atof) {
  switch (kind) {
  case BIND_KIND_BOOL:
    if (len == 4 && data[0] == 't' && data[1] == 'r' && data[2] == 'u' && data[3] == 'e') {
      *(uint8_t *)dst = 1;
      return 0;
    }
    if (len == 5 && data[0] == 'f' && data[1] == 'a' && data[2] == 'l' && data[3] == 's' && data[4] == 'e') {
      *(uint8_t *)dst = 0;
      return 0;
    }
    return -1;
  case BIND_KIND_STRING:
    return bind_write_quoted_string(str_pp, data, len, dst);
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64: {
    int64_t v;
    if (ndec_parse_int64(data, len, &v) != NDEC_NUM_OK) return -1;
    switch (kind) {
    case BIND_KIND_INT8:
      if (v < -128 || v > 127) return -1;
      *(int8_t *)dst = (int8_t)v;
      return 0;
    case BIND_KIND_INT16:
      if (v < -32768 || v > 32767) return -1;
      *(int16_t *)dst = (int16_t)v;
      return 0;
    case BIND_KIND_INT32:
      if (v < -2147483648LL || v > 2147483647LL) return -1;
      *(int32_t *)dst = (int32_t)v;
      return 0;
    case BIND_KIND_INT64:
      *(int64_t *)dst = v;
      return 0;
    default:
      *(intptr_t *)dst = (intptr_t)v;
      return 0;
    }
  }
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64: {
    uint64_t uv;
    if (ndec_parse_uint64(data, len, &uv) != NDEC_NUM_OK) return -1;
    switch (kind) {
    case BIND_KIND_UINT8:
      if (uv > 0xFF) return -1;
      *(uint8_t *)dst = (uint8_t)uv;
      return 0;
    case BIND_KIND_UINT16:
      if (uv > 0xFFFF) return -1;
      *(uint16_t *)dst = (uint16_t)uv;
      return 0;
    case BIND_KIND_UINT32:
      if (uv > 0xFFFFFFFFu) return -1;
      *(uint32_t *)dst = (uint32_t)uv;
      return 0;
    case BIND_KIND_UINT64:
      *(uint64_t *)dst = uv;
      return 0;
    default:
      *(uintptr_t *)dst = (uintptr_t)uv;
      return 0;
    }
  }
  case BIND_KIND_FLOAT32: {
    float f;
    if (ndec_parse_float32(data, len, &f, atof) != 0) return -1;
    if (UNLIKELY(!__builtin_isfinite(f))) return -1;
    *(float *)dst = f;
    return 0;
  }
  case BIND_KIND_FLOAT64: {
    double d;
    if (ndec_parse_double(data, len, &d, atof) != 0) return -1;
    if (UNLIKELY(!__builtin_isfinite(d))) return -1;
    *(double *)dst = d;
    return 0;
  }
  default:
    return -1;
  }
}

/* Deferred values are staged as UnmarshalRecords. Go drains them before an
 * operation moves or publishes their targets, including map flush, stream batch
 * settlement, explicit unmarshal flush, and document completion.
 */
#define BIND_IS_DEFERRED_VALUE(k)                                                                                 \
  ((k) == BIND_KIND_UNMARSHALER || (k) == BIND_KIND_TEXT_UNMARSHALER || (k) == BIND_KIND_RAW_MESSAGE)

/* Values use the descriptor {doc, base, tidx, end, mode}. mode packs the seam
 * view with descriptor flags. Go keeps ValueDoc and its arenas reachable while
 * C emits tape content, and vd_close patches end after the final word is known.
 * BIND_FLAG_COLD gates pointer, Value, any, and interface dispatch.
 */

#define BIND_IS_VALUE(k) ((k) == BIND_KIND_VALUE)
#define BIND_IS_ANY(k)   ((k) == BIND_KIND_ANY || (k) == BIND_KIND_IFACE)

/* STREAM shares the slice-header representation and must follow slice null
 * semantics.
 */
#define BIND_NULL_ZERO(dst, m, ct)                                                                                \
  do {                                                                                                            \
    switch ((ct)->kind) {                                                                                         \
    case BIND_KIND_PTR:                                                                                           \
    case BIND_KIND_SLICE:                                                                                         \
    case BIND_KIND_STREAM:                                                                                        \
    case BIND_KIND_MAP:                                                                                           \
    case BIND_KIND_ANY:                                                                                           \
    case BIND_KIND_NUMBER:                                                                                        \
      __builtin_memset((dst), 0, (m)->b.ctx.type_meta[(ct)->type_idx].size);                                      \
      break;                                                                                                      \
    default:                                                                                                      \
      break;                                                                                                      \
    }                                                                                                             \
  } while (0)

#define BIND_WRITE_EMPTY_SLICE(dst, m, type_idx)                                                                  \
  do {                                                                                                            \
    const void *_ed           = (const void *)(m)->b.ctx.type_meta[(type_idx)].u.slice.empty_slice_data;          \
    *(const void **)(dst)     = _ed;                                                                              \
    *(intptr_t *)((dst) + 8)  = 0;                                                                                \
    *(intptr_t *)((dst) + 16) = 0;                                                                                \
  } while (0)

/* Each RecBatch row owns fixed-size backings for one power-of-two capacity.
 * Allocation transfers a bitmap slot to a slice. Growth returns only pointers
 * within the row's current allocation; pointers from retained refill arrays or
 * bypass allocations are ignored. Returned backings are zeroed before reuse so
 * the GC cannot follow stale pointers. BindSlotClass supplies both the matrix
 * and elem_size.
 */
INLINE uint32_t recbatch_row_idx(uint32_t cap) {
  return (uint32_t)__builtin_ctz(cap);
}

INLINE uint32_t recbatch_row_cap(uint32_t row_idx) {
  return 1u << row_idx;
}

INLINE uint32_t recbatch_row_slots(uint32_t row_idx) {
  return row_idx < 4 ? (64u >> row_idx) : 8u;
}

INLINE void *recbatch_alloc(BindSlotClass *sc, uint32_t row_idx) {
  RecBatchMatrix *mat = (RecBatchMatrix *)sc->block;
  RecBatchRow *r      = &mat->rows[row_idx];
  uint64_t bm         = r->bitmap;
  if (bm == 0) return NULL;
  uint32_t i = (uint32_t)__builtin_ctzll(bm);
  r->bitmap  = bm & ~(1ULL << i);
  r->free_count--;
  uint32_t cap = recbatch_row_cap(row_idx);
  return (char *)r->base + (uintptr_t)i * cap * sc->elem_size;
}

INLINE void recbatch_free(BindSlotClass *sc, void *ptr, uint32_t cap) {
  if (cap < 1 || cap > BIND_RECBATCH_MAX_CAP) return;
  uint32_t row_idx      = recbatch_row_idx(cap);
  RecBatchMatrix *mat   = (RecBatchMatrix *)sc->block;
  RecBatchRow *r        = &mat->rows[row_idx];
  uintptr_t slot_bytes  = (uintptr_t)cap * sc->elem_size;
  uintptr_t total_bytes = (uintptr_t)recbatch_row_slots(row_idx) * slot_bytes;
  uintptr_t offset      = (uintptr_t)ptr - (uintptr_t)r->base;
  if (r->base == NULL || offset >= total_bytes || offset % slot_bytes != 0) return;
  uint32_t i = (uint32_t)(offset / slot_bytes);
  r->bitmap |= (1ULL << i);
  r->free_count++;
  __builtin_memset(ptr, 0, (size_t)slot_bytes);
}

/* The Value walk shares the main cursor. Stash the parent slot and type so the
 * parent destination, type, and count can resume after the walk.
 */
#define BIND_DISPATCH_VALUE(slot_)                                                                                \
  do {                                                                                                            \
    m->c.stash.deferred_yield.slot = (slot_);                                                                     \
    m->c.stash.deferred_yield.type = (BindType *)&cur_type;                                                       \
    goto vd_dispatch_value;                                                                                       \
  } while (0)

#define BIND_DISPATCH_ANY(ct_, slot_)                                                                             \
  do {                                                                                                            \
    m->c.stash.any_yield.slot = (slot_);                                                                          \
    goto any_value;                                                                                               \
  } while (0)

#define TAPE_BIND_DISPATCH_ANY(slot_)                                                                             \
  do {                                                                                                            \
    m->c.stash.any_yield.slot = (slot_);                                                                          \
    goto t_any_value;                                                                                             \
  } while (0)

/* Callers must handle STRING, pointer resolution, cold dispatch, null, and
 * quoted values before this shared switch, then provide their own slice or array
 * case after it.
 */
#define BIND_VALUE_SWITCH_COMMON(ct, body, ch, cont_label, zero_size, ON_MISMATCH, push_fn)                       \
  case BIND_KIND_INT:                                                                                             \
  case BIND_KIND_INT8:                                                                                            \
  case BIND_KIND_INT16:                                                                                           \
  case BIND_KIND_INT32:                                                                                           \
  case BIND_KIND_INT64:                                                                                           \
  case BIND_KIND_UINT:                                                                                            \
  case BIND_KIND_UINT8:                                                                                           \
  case BIND_KIND_UINT16:                                                                                          \
  case BIND_KIND_UINT32:                                                                                          \
  case BIND_KIND_UINT64:                                                                                          \
  case BIND_KIND_FLOAT32:                                                                                         \
  case BIND_KIND_FLOAT64:                                                                                         \
    if (ch == '-' || (ch >= '0' && ch <= '9')) BIND_WRITE_NUMBER(ct, body, SRC_POS(), cont_label, ON_MISMATCH);   \
    ON_MISMATCH;                                                                                                  \
  case BIND_KIND_BOOL:                                                                                            \
    if (ch == 't') {                                                                                              \
      if (bind_validate_atom(SRC_PTR(), 't') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());                  \
      *(uint8_t *)body = 1;                                                                                       \
      SRC_ADVANCE();                                                                                              \
      goto cont_label;                                                                                            \
    }                                                                                                             \
    if (ch == 'f') {                                                                                              \
      if (bind_validate_atom(SRC_PTR(), 'f') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());                  \
      *(uint8_t *)body = 0;                                                                                       \
      SRC_ADVANCE();                                                                                              \
      goto cont_label;                                                                                            \
    }                                                                                                             \
    ON_MISMATCH;                                                                                                  \
  case BIND_KIND_STRUCT:                                                                                          \
    if (ch == '{') {                                                                                              \
      __builtin_memset(body, 0, zero_size);                                                                       \
      BIND_DESCEND_STRUCT(body, ct, cont_label, push_fn);                                                         \
    }                                                                                                             \
    ON_MISMATCH;                                                                                                  \
  case BIND_KIND_MAP:                                                                                             \
    if (ch == '{') {                                                                                              \
      BIND_MAP_OPEN(m, ct, body, push_fn);                                                                        \
    }                                                                                                             \
    ON_MISMATCH;                                                                                                  \
  case BIND_KIND_PTR:                                                                                             \
    ON_MISMATCH;                                                                                                  \
  default:                                                                                                        \
    ON_MISMATCH

/* STRING stays ahead of the kind switch so its common path uses a predicted
 * branch instead of the jump table clang emits for the remaining kinds. */
#define BIND_DISPATCH_STRING(ct, body, ch, cont_label, ON_MISMATCH)                                               \
  do {                                                                                                            \
    if (LIKELY((ct)->kind == BIND_KIND_STRING)) {                                                                 \
      if ((ch) == '"') {                                                                                          \
        if (bind_visit_str(&str_p, SRC_PTR(), (body)) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());         \
        SRC_ADVANCE();                                                                                            \
        goto cont_label;                                                                                          \
      }                                                                                                           \
      ON_MISMATCH;                                                                                                \
    }                                                                                                             \
    if ((ct)->kind == BIND_KIND_NUMBER) {                                                                         \
      if ((ch) == '"') {                                                                                          \
        if (bind_visit_str(&str_p, SRC_PTR(), (body)) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());         \
        SRC_ADVANCE();                                                                                            \
        goto cont_label;                                                                                          \
      }                                                                                                           \
      if ((ch) == '-' || ((ch) >= '0' && (ch) <= '9')) BIND_WRITE_NUMBER_AS_STR((body), SRC_POS(), cont_label);   \
      ON_MISMATCH;                                                                                                \
    }                                                                                                             \
  } while (0)

/* Every bind yield must spill these register-live locals to their NdecBindCore
 * homes. The resume entry reloads them from the same offsets.
 */
#define __BIND_SAVE_LOCALS(m)                                                                                     \
  do {                                                                                                            \
    (m)->cursor      = cursor;                                                                                    \
    (m)->c.depth     = depth;                                                                                     \
    (m)->c.cur_dst   = cur_dst;                                                                                   \
    (m)->c.cur_type  = cur_type;                                                                                  \
    (m)->c.cur_count = cur_count;                                                                                 \
    (m)->c.cur_aux   = cur_aux;                                                                                   \
    (m)->c.str_used  = (size_t)((uintptr_t)str_p - (uintptr_t)(m)->b.alloc.str_arena);                            \
  } while (0)

/* Aux slots are claimed only when a struct enters a cold merged-tape,
 * discriminator, variant, or kindof path. owner_depth makes reentry a single
 * comparison and leaves bookkeeping owned by the aux stack. Slot zero is a
 * sentinel, and every live field is initialized at claim time so failed parses
 * cannot leak state into a later parse.
 */
#define AUX_LAZY_ALLOC(m, ON_OVERFLOW)                                                                            \
  do {                                                                                                            \
    if (m->auxFrames[m->aux_depth].owner_depth != depth) {                                                        \
      if (++m->aux_depth >= BIND_AUX_STACK_SIZE) ON_OVERFLOW;                                                     \
      BindAuxFrame *_ax = &m->auxFrames[m->aux_depth];                                                            \
      _ax->owner_depth  = depth;                                                                                  \
      _ax->parent_aux   = m->aux_depth - 1;                                                                       \
      _ax->a.start      = BIND_AUX_NO_TAPE;                                                                       \
      _ax->a.end        = 0;                                                                                      \
      _ax->a.count      = 0;                                                                                      \
      _ax->b_count      = 0;                                                                                      \
      _ax->walk         = 0;                                                                                      \
      _ax->disc_seen    = 0;                                                                                      \
    }                                                                                                             \
  } while (0)

/* Clear owner_depth before the LIFO release so a later struct at the same parse
 * depth cannot inherit the slot.
 */
#define AUX_RELEASE(m)                                                                                            \
  do {                                                                                                            \
    BindAuxFrame *_ax = &m->auxFrames[m->aux_depth];                                                              \
    _ax->owner_depth  = -1;                                                                                       \
    m->aux_depth      = _ax->parent_aux;                                                                          \
  } while (0)

/* Phase 2 descends while the current auxiliary stack slot remains live. The
 * structural frame preserves hot locals, and rebind_stack preserves the outer
 * structural or tape cursor, tape base, view mode, and return phase.
 *
 * base_ is the merged-tape origin used by relative container indices. at_ is
 * the value's first word, end_ is one past its last word, and view_mode_ is
 * the complete mode installed for the descent. Control transfers directly to
 * t_document_start.
 */
#define PHASE2_DESCEND(m, slot_, case_type_, case_type_idx_, base_, at_, end_, view_mode_, return_phase_)         \
  do {                                                                                                            \
    if ((m)->rebind_top >= BIND_REBIND_STACK_SIZE) BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                   \
    BindAuxRebind *_rb    = &(m)->rebind_stack[(m)->rebind_top++];                                                \
    _rb->saved_cursor     = cursor;                                                                               \
    _rb->saved_cursor_end = (m)->cursor_end;                                                                      \
    _rb->saved_value_tape = (m)->b.alloc.value_tape;                                                              \
    _rb->return_phase     = (return_phase_);                                                                      \
    _rb->saved_base_depth = (m)->tape_bind_base_depth;                                                            \
    _rb->saved_view_mode  = (m)->tape_view_mode;                                                                  \
    /* The complete mode, flags included, is restored at t_document_end. */                                       \
    (m)->tape_view_mode = (view_mode_);                                                                           \
    if (bind_push_struct(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))                                  \
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                                                \
    cur_dst                   = (slot_);                                                                          \
    cur_type                  = *(case_type_);                                                                    \
    cur_count                 = 0;                                                                                \
    cur_aux                   = cur_type.kind == BIND_KIND_STRUCT                                                 \
                                    ? (void *)(uintptr_t)(m)->b.ctx.type_meta[(case_type_idx_)].u.strct.lookup    \
                                    : NULL;                                                                       \
    cursor.tape               = &(m)->b.alloc.tape_arena[(at_)];                                                  \
    (m)->cursor               = cursor;                                                                           \
    (m)->cursor_end.tape      = &(m)->b.alloc.tape_arena[(end_)];                                                 \
    (m)->b.alloc.value_tape   = &(m)->b.alloc.tape_arena[(base_)];                                                \
    (m)->tape_bind_base_depth = depth;                                                                            \
    goto t_document_start;                                                                                        \
  } while (0)

#define BIND_YIELD(m, action, a0, a1, resume_phase)                                                               \
  do {                                                                                                            \
    __BIND_SAVE_LOCALS(m);                                                                                        \
    (m)->c.phase                = (resume_phase);                                                                 \
    (m)->b.yield.pending_action = (action);                                                                       \
    (m)->b.yield.arg0           = (a0);                                                                           \
    (m)->b.yield.arg1           = (a1);                                                                           \
    (m)->b.yield.target         = cur_dst;                                                                        \
    return;                                                                                                       \
  } while (0)

#define BIND_ERROR_NO_POS UINT32_MAX

#define BIND_ERROR_PAYLOAD(m, kind, detail, source_pos, error_target)                                             \
  do {                                                                                                            \
    (m)->b.yield.arg0            = (kind);                                                                        \
    (m)->b.yield.arg1            = (uint32_t)(detail);                                                            \
    (m)->b.yield.first_error_pos = (uint32_t)(source_pos);                                                        \
    (m)->b.yield.target          = (error_target);                                                                \
  } while (0)

#define BIND_YIELD_ERR(m, kind, pos)                                                                              \
  do {                                                                                                            \
    vj_fprintf_stderr("YIELD_ERR %s:%d kind=%d pos=%u\n", __FILE__, __LINE__, (int)(kind), (uint32_t)(pos));      \
    __BIND_SAVE_LOCALS(m);                                                                                        \
    (m)->c.phase                = BIND_PHASE_DOCUMENT_END;                                                        \
    (m)->b.yield.pending_action = BIND_YIELD_ERROR;                                                               \
    BIND_ERROR_PAYLOAD((m), (kind), (pos), (pos), NULL);                                                          \
    return;                                                                                                       \
  } while (0)

#define BIND_YIELD_ERR_NO_POS(m, kind, detail)                                                                    \
  do {                                                                                                            \
    vj_fprintf_stderr("YIELD_ERR %s:%d kind=%d detail=%u\n", __FILE__, __LINE__, (int)(kind),                     \
                      (uint32_t)(detail));                                                                        \
    __BIND_SAVE_LOCALS(m);                                                                                        \
    (m)->c.phase                = BIND_PHASE_DOCUMENT_END;                                                        \
    (m)->b.yield.pending_action = BIND_YIELD_ERROR;                                                               \
    BIND_ERROR_PAYLOAD((m), (kind), (detail), BIND_ERROR_NO_POS, NULL);                                           \
    return;                                                                                                       \
  } while (0)

/* A mismatch at the 0x20 scan sentinel is truncation, not a type or syntax
 * error. This check stays on the cold error path.
 */
#define BIND_ERR_VALUE_OR_EOF(m, kind, pos)                                                                       \
  do {                                                                                                            \
    if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, (pos));                                              \
    BIND_YIELD_ERR(m, (kind), (pos));                                                                             \
  } while (0)

#define BIND_MAP_OPEN(m, type_ptr_, body_, push_fn)                                                               \
  do {                                                                                                            \
    if (push_fn(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))                                           \
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                                                \
    cur_dst  = (body_);                                                                                           \
    cur_type = *(type_ptr_);                                                                                      \
    goto map_open;                                                                                                \
  } while (0)

#define BIND_YIELD_FLUSH_MAP(m, count, closing, map_hdr, resume_phase)                                            \
  do {                                                                                                            \
    __BIND_SAVE_LOCALS(m);                                                                                        \
    (m)->c.phase                = (resume_phase);                                                                 \
    (m)->b.yield.pending_action = BIND_YIELD_FLUSH_MAP;                                                           \
    (m)->b.yield.arg0           = (count);                                                                        \
    (m)->b.yield.arg1           = (closing);                                                                      \
    (m)->b.yield.target         = (map_hdr);                                                                      \
    return;                                                                                                       \
  } while (0)

/* Non-null pointer chains publish each allocated pointee before advancing to
 * the next layer. With reuse enabled, existing pointees remain user-owned and
 * make restart safe. Map values disable reuse because their KV slot is scratch.
 * Null is handled by the caller without walking the chain.
 */
#define BIND_RESOLVE_PTR_CHAIN(m, body, ct, ch, phase, reuse)                                                     \
  do {                                                                                                            \
    if ((ch) != 'n') {                                                                                            \
      int _depth = 0;                                                                                             \
      while ((ct)->kind == BIND_KIND_PTR) {                                                                       \
        if (UNLIKELY(++_depth > 32)) BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                 \
        uint8_t *_pointee;                                                                                        \
        if ((reuse) && (_pointee = *(uint8_t **)(body)) != NULL) {                                                \
        } else {                                                                                                  \
          int32_t _ci        = (ct)->u.ptr.alloc_class;                                                           \
          BindSlotClass *_sc = &(m)->b.alloc.slot_classes[_ci];                                                   \
          if (_sc->offset >= _sc->limit) BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, _ci, 0, (phase));                   \
          _pointee = _sc->block + _sc->offset;                                                                    \
          _sc->offset += _sc->elem_size;                                                                          \
          *(uint8_t **)(body) = _pointee;                                                                         \
        }                                                                                                         \
        (body) = _pointee;                                                                                        \
        (ct)   = (const BindType *)(ct)->child;                                                                   \
      }                                                                                                           \
    }                                                                                                             \
  } while (0)

/* Promoted-field hops are walked even for null because null belongs to the
 * nested field, not its embedded host pointer. Existing pointees are always
 * reused. A BLOCK_FULL resume restarts at hop zero, and published hops make that
 * replay idempotent instead of allocating duplicate pointees.
 */
#define BIND_RESOLVE_FIELD_HOPS(m, dst, f, hops_base, phase)                                                      \
  do {                                                                                                            \
    const BindPtrHop *_hop = (const BindPtrHop *)(hops_base) + ((f)->flags >> 16);                                \
    for (;;) {                                                                                                    \
      uint8_t **_slot   = (uint8_t **)((dst) + _hop->slot_offset);                                                \
      uint8_t *_pointee = *_slot;                                                                                 \
      if (_pointee == NULL) {                                                                                     \
        int32_t _ci        = BIND_PTR_HOP_CLASS(_hop);                                                            \
        BindSlotClass *_sc = &(m)->b.alloc.slot_classes[_ci];                                                     \
        if (_sc->offset >= _sc->limit) {                                                                          \
          (m)->c.stash.field_value.field = (uint8_t *)(f);                                                        \
          BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, _ci, 0, (phase));                                                  \
        }                                                                                                         \
        _pointee = _sc->block + _sc->offset;                                                                      \
        _sc->offset += _sc->elem_size;                                                                            \
        *_slot = _pointee;                                                                                        \
      }                                                                                                           \
      (dst) = _pointee;                                                                                           \
      if (BIND_PTR_HOP_IS_LAST(_hop)) break;                                                                      \
      _hop++;                                                                                                     \
    }                                                                                                             \
  } while (0)

/* An empty object may still require inline-case finalization or an empty
 * reserve-unknown Value. Only a child without MAY_PHASE2 may bypass descent and
 * object_close.
 */
#define BIND_DESCEND_STRUCT(body, child_type_ptr, cont_label, push_fn)                                            \
  do {                                                                                                            \
    SRC_ADVANCE();                                                                                                \
    int _empty = SRC_ACCEPT('}');                                                                                 \
    if (_empty && !((child_type_ptr)->flags & BIND_FLAG_MAY_PHASE2)) goto cont_label;                             \
    if (push_fn(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))                                           \
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                                                \
    cur_dst  = (body);                                                                                            \
    cur_type = *(child_type_ptr);                                                                                 \
    if (_empty) goto object_close;                                                                                \
    goto object_begin;                                                                                            \
  } while (0)

#define BIND_EMPTY_ARRAY_CLOSE(m, ct, pop_target, pop_fn)                                                         \
  do {                                                                                                            \
    if (BIND_IS_SLICE_LIKE((ct)->kind)) BIND_WRITE_EMPTY_SLICE(cur_dst, (m), (ct)->type_idx);                     \
    pop_fn(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);                                            \
    goto pop_target;                                                                                              \
  } while (0)

/* cur_dst_ must be a slice header, cur_aux_ its write pointer, and cur_count_
 * its live length. Deferred Unmarshalers must drain before a grow moves the
 * backing. Values need no drain because their tape header is installed inline
 * and moves with the slice.
 */
#define BIND_SLICE_GROW_CHECK(m, cur_type_, cur_dst_, cur_aux_, cur_count_)                                       \
  do {                                                                                                            \
    uint64_t _gcap;                                                                                               \
    __builtin_memcpy(&_gcap, (cur_dst_) + 16, sizeof(intptr_t));                                                  \
    if (UNLIKELY((cur_count_) == _gcap)) {                                                                        \
      if (UNLIKELY((m)->b.alloc.deferred_drain_used > 0)) {                                                       \
        __BIND_SAVE_LOCALS(m);                                                                                    \
        (m)->c.phase                = BIND_PHASE_ARRAY_VALUE;                                                     \
        (m)->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;                                                 \
        return;                                                                                                   \
      }                                                                                                           \
      int32_t _gac        = (m)->b.ctx.type_meta[(cur_type_).type_idx].u.slice.alloc_class;                       \
      BindSlotClass *_gsc = &(m)->b.alloc.slot_classes[_gac];                                                     \
      if (UNLIKELY(_gsc->mode == BIND_SLOT_RECBATCH)) {                                                           \
        /* A preallocated empty slice can have non-null Data and zero capacity.                                   \
         * RecBatch grows inline, so floor the next capacity before ctz. */                                       \
        intptr_t _gnext_cap = _gcap ? _gcap * 2 : 1;                                                              \
        if (UNLIKELY(_gnext_cap > (intptr_t)BIND_RECBATCH_MAX_CAP)) {                                             \
          BIND_YIELD(m, BIND_YIELD_RECBATCH_BYPASS, (uint32_t)(cur_type_).type_idx, (uint32_t)_gnext_cap,         \
                     BIND_PHASE_ARRAY_VALUE);                                                                     \
        }                                                                                                         \
        uint32_t _grow_idx = recbatch_row_idx((uint32_t)_gnext_cap);                                              \
        void *_gbk         = recbatch_alloc(_gsc, _grow_idx);                                                     \
        if (UNLIKELY(_gbk == NULL)) {                                                                             \
          BIND_YIELD(m, BIND_YIELD_RECBATCH_REFILL, (uint32_t)(cur_type_).type_idx, _grow_idx,                    \
                     BIND_PHASE_ARRAY_VALUE);                                                                     \
        }                                                                                                         \
        const uint8_t *_gold_data;                                                                                \
        __builtin_memcpy(&_gold_data, (cur_dst_), sizeof(_gold_data));                                            \
        size_t _gbyte_len = (size_t)((uint8_t *)(cur_aux_) - _gold_data);                                         \
        if (_gbyte_len > 0) {                                                                                     \
          __builtin_memmove(_gbk, _gold_data, _gbyte_len);                                                        \
        }                                                                                                         \
        *(void **)((cur_dst_))         = _gbk;                                                                    \
        *(intptr_t *)((cur_dst_) + 16) = _gnext_cap;                                                              \
        (cur_aux_)                     = (uint8_t *)_gbk + _gbyte_len;                                            \
        recbatch_free(_gsc, (void *)_gold_data, (uint32_t)_gcap);                                                 \
      } else {                                                                                                    \
        BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)(cur_type_).type_idx, 0, BIND_PHASE_ARRAY_VALUE);          \
      }                                                                                                           \
    }                                                                                                             \
  } while (0)

/* Preserve only the first type mismatch, then skip the value. Syntax errors
 * still abort immediately.
 */
#define BIND_TYPE_MISMATCH_SKIP(m, pos)                                                                           \
  do {                                                                                                            \
    if (m->c.first_error_kind == 0) {                                                                             \
      m->c.first_error_kind      = BIND_ERR_TYPE_MISMATCH;                                                        \
      m->b.yield.first_error_pos = (pos);                                                                         \
    }                                                                                                             \
    goto safe_skip_value;                                                                                         \
  } while (0)

#define BIND_WRITE_NUMBER(ct, body, err_pos, cont_label, ON_MISMATCH)                                             \
  do {                                                                                                            \
    const uint8_t *_tok = SRC_PTR();                                                                              \
    const uint8_t *_end;                                                                                          \
    if (bind_write_number(_tok, (ct)->kind, (body), m->c.atof, &_end) < 0) ON_MISMATCH;                           \
    if (UNLIKELY(is_non_delim(*_end))) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (err_pos));                             \
    SRC_ADVANCE();                                                                                                \
    goto cont_label;                                                                                              \
  } while (0)

/* json.Number preserves the validated source token in str_arena. */
#define BIND_WRITE_NUMBER_AS_STR(body, err_pos, cont_label)                                                       \
  do {                                                                                                            \
    const uint8_t *_tok = SRC_PTR();                                                                              \
    const uint8_t *_end;                                                                                          \
    double _dv;                                                                                                   \
    if (UNLIKELY(ndec_parse_double_padded(_tok, &_dv, m->c.atof, &_end)))                                         \
      BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (err_pos));                                                       \
    if (UNLIKELY(is_non_delim(*_end))) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (err_pos));                             \
    (void)_dv;                                                                                                    \
    uint32_t _nlen  = (uint32_t)(_end - _tok);                                                                    \
    uint8_t *_ndata = str_p;                                                                                      \
    __builtin_memcpy(_ndata, _tok, _nlen);                                                                        \
    str_p += _nlen;                                                                                               \
    bind_write_str_header((body), _ndata, _nlen);                                                                 \
    SRC_ADVANCE();                                                                                                \
    goto cont_label;                                                                                              \
  } while (0)

/* Tape bind reuses NdecBindMachine to bind a prebuilt tape into typed storage.
 * Its cursor reads the tape member of the same pair the JSON walk reads through
 * idx. The outer cursor may be structural or tape state, and rebind_stack
 * restores it at t_document_end. The t_ labels keep this state machine distinct
 * from the JSON bind labels.
 */

/* Every tape-bind yield spills the shared hot locals. cursor denotes the active
 * JSON or tape cursor and resumes through the same m->cursor home.
 */
#define __TAPE_BIND_SAVE_LOCALS(m)                                                                                \
  do {                                                                                                            \
    (m)->cursor      = cursor;                                                                                    \
    (m)->c.depth     = depth;                                                                                     \
    (m)->c.cur_dst   = cur_dst;                                                                                   \
    (m)->c.cur_type  = cur_type;                                                                                  \
    (m)->c.cur_count = cur_count;                                                                                 \
    (m)->c.cur_aux   = cur_aux;                                                                                   \
    (m)->c.str_used  = (size_t)((uintptr_t)str_p - (uintptr_t)(m)->b.alloc.str_arena);                            \
  } while (0)

#define TAPE_BIND_YIELD(m, action, a0, a1, resume_phase)                                                          \
  do {                                                                                                            \
    __TAPE_BIND_SAVE_LOCALS(m);                                                                                   \
    (m)->c.phase                = (resume_phase);                                                                 \
    (m)->b.yield.pending_action = (action);                                                                       \
    (m)->b.yield.arg0           = (a0);                                                                           \
    (m)->b.yield.arg1           = (a1);                                                                           \
    (m)->b.yield.target         = cur_dst;                                                                        \
    return;                                                                                                       \
  } while (0)

#define TAPE_BIND_YIELD_ERR(m, kind, pos)                                                                         \
  do {                                                                                                            \
    vj_fprintf_stderr("TAPE_YIELD_ERR %s:%d kind=%d pos=%u\n", __FILE__, __LINE__, (int)(kind), (uint32_t)(pos)); \
    __TAPE_BIND_SAVE_LOCALS(m);                                                                                   \
    (m)->c.phase                = BIND_PHASE_DOCUMENT_END;                                                        \
    (m)->b.yield.pending_action = BIND_YIELD_ERROR;                                                               \
    BIND_ERROR_PAYLOAD((m), (kind), (pos), BIND_ERROR_NO_POS, NULL);                                              \
    return;                                                                                                       \
  } while (0)

#define TAPE_BIND_YIELD_ERR_NO_POS(m, kind, detail) TAPE_BIND_YIELD_ERR((m), (kind), (detail))

#define TAPE_BIND_YIELD_FLUSH_MAP(m, count, closing, map_hdr, resume_phase)                                       \
  do {                                                                                                            \
    __TAPE_BIND_SAVE_LOCALS(m);                                                                                   \
    (m)->c.phase                = (resume_phase);                                                                 \
    (m)->b.yield.pending_action = BIND_YIELD_FLUSH_MAP;                                                           \
    (m)->b.yield.arg0           = (count);                                                                        \
    (m)->b.yield.arg1           = (closing);                                                                      \
    (m)->b.yield.target         = (map_hdr);                                                                      \
    return;                                                                                                       \
  } while (0)

/* Tape pointer-chain yields must stash their site state before saving the active
 * tape cursor. ct_ptr is a mutable lvalue advanced through each pointer layer;
 * allocation and reuse follow the JSON pointer-chain contract.
 */
#define TAPE_BIND_RESOLVE_PTR_CHAIN(m, body_lvalue, ct_ptr, tag, phase, reuse, stash_stmt)                        \
  do {                                                                                                            \
    if ((tag) != (TAPE_NULL_VAL >> 56)) {                                                                         \
      int _depth = 0;                                                                                             \
      while ((ct_ptr)->kind == BIND_KIND_PTR) {                                                                   \
        if (UNLIKELY(++_depth > 32)) TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                            \
        uint8_t *_pointee;                                                                                        \
        if ((reuse) && (_pointee = *(uint8_t **)(body_lvalue)) != NULL) {                                         \
        } else {                                                                                                  \
          int32_t _ci        = (ct_ptr)->u.ptr.alloc_class;                                                       \
          BindSlotClass *_sc = &(m)->b.alloc.slot_classes[_ci];                                                   \
          if (_sc->offset >= _sc->limit) {                                                                        \
            stash_stmt;                                                                                           \
            TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, _ci, 0, (phase));                                           \
          }                                                                                                       \
          _pointee = _sc->block + _sc->offset;                                                                    \
          _sc->offset += _sc->elem_size;                                                                          \
          *(uint8_t **)(body_lvalue) = _pointee;                                                                  \
        }                                                                                                         \
        (body_lvalue) = _pointee;                                                                                 \
        (ct_ptr)      = (const BindType *)(ct_ptr)->child;                                                        \
      }                                                                                                           \
    }                                                                                                             \
  } while (0)

/* A Value field aliases its subtree from the source tape. The yield reports the
 * subtree extent without advancing TAP_CURSOR; the site-specific resume label
 * advances it exactly once after Go installs the alias.
 */
#define TAPE_BIND_VALUE_FIELD(m, body_lvalue, resume_phase)                                                       \
  do {                                                                                                            \
    const uint64_t *_sub_end                = tape_skip_value(TAP_CURSOR, TAP_VIEW());                            \
    uint32_t _sub_start                     = (uint32_t)(TAP_CURSOR - (const uint64_t *)(m)->b.alloc.value_tape); \
    uint32_t _sub_words                     = (uint32_t)(_sub_end - TAP_CURSOR);                                  \
    (m)->c.stash.tape_value_yield.slot      = (uint8_t *)(body_lvalue);                                           \
    (m)->c.stash.tape_value_yield.view_mode = (m)->tape_view_mode;                                                \
    TAPE_BIND_YIELD(m, BIND_YIELD_TAPE_BIND_VALUE, _sub_start, _sub_words, (resume_phase));                       \
  } while (0)

/* Empty tape objects still enter t_object_close_drain when the child has
 * close-time work. This matches the JSON descent contract.
 */
#define TAPE_BIND_DESCEND_STRUCT(body, child_type_ptr, cont_label, push_fn)                                       \
  do {                                                                                                            \
    TAP_ADVANCE();                                                                                                \
    int _empty = (TAP_TAG() == (TAPE_END_OBJECT >> 56));                                                          \
    if (_empty) {                                                                                                 \
      TAP_ADVANCE();                                                                                              \
      if (!((child_type_ptr)->flags & BIND_FLAG_MAY_PHASE2)) goto cont_label;                                     \
    }                                                                                                             \
    if (push_fn(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))                                           \
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                                           \
    cur_dst  = (body);                                                                                            \
    cur_type = *(child_type_ptr);                                                                                 \
    if (_empty) goto t_object_close_drain;                                                                        \
    cur_aux = (void *)(uintptr_t)m->b.ctx.type_meta[cur_type.type_idx].u.strct.lookup;                            \
    goto t_object_field;                                                                                          \
  } while (0)

#define TAPE_BIND_MAP_OPEN(m, type_ptr_, body_, push_fn)                                                          \
  do {                                                                                                            \
    if (push_fn(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))                                           \
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);                                                           \
    cur_dst  = (body_);                                                                                           \
    cur_type = *(type_ptr_);                                                                                      \
    goto t_map_open;                                                                                              \
  } while (0)

#define TAPE_BIND_TYPE_MISMATCH_SKIP(m, pos)                                                                      \
  do {                                                                                                            \
    if (m->c.first_error_kind == 0) {                                                                             \
      m->c.first_error_kind      = BIND_ERR_TYPE_MISMATCH;                                                        \
      m->b.yield.first_error_pos = (pos);                                                                         \
    }                                                                                                             \
    goto t_skip_value;                                                                                            \
  } while (0)

/* A root mismatch has no enclosing container, so it must end the document
 * instead of entering the nested skip continuation. Use tape_value_end because
 * consuming a following seam would move past cursor_end and report trailing data.
 */
#define TAPE_BIND_ROOT_TYPE_MISMATCH_SKIP(m, pos)                                                                 \
  do {                                                                                                            \
    if (m->c.first_error_kind == 0) {                                                                             \
      m->c.first_error_kind      = BIND_ERR_TYPE_MISMATCH;                                                        \
      m->b.yield.first_error_pos = (pos);                                                                         \
    }                                                                                                             \
    cursor.tape = tape_value_end(TAP_CURSOR, TAP_VIEW());                                                         \
    goto t_document_end;                                                                                          \
  } while (0)

/* Tape tags preserve the JSON number form independently of the target kind.
 * Integer payloads may promote to floats, but TAPE_DOUBLE must not bind to an
 * integer because the direct JSON path rejects fractional or exponent forms for
 * integer targets. TAPE_DOUBLE always carries the source span in str_arena so
 * the uncommon float32 target can parse once at binary32 precision. */
INLINE int tape_bind_write_number(uint8_t kind, uint8_t tag, uint64_t word, uint64_t v, uint8_t *dst,
                                  const uint8_t *str_arena, atof_ctx *atof) {
  int is_double = (tag == (TAPE_DOUBLE >> 56));
  int64_t iv;
  uint64_t uv;
  double dv;
  if (is_double) {
    __builtin_memcpy(&dv, &v, 8);
  } else {
    uv = v;
    iv = (int64_t)v;
    dv = (tag == (TAPE_UINT64 >> 56)) ? (double)uv : (double)iv;
  }
  if (is_double) {
    switch (kind) {
    case BIND_KIND_INT8:
    case BIND_KIND_INT16:
    case BIND_KIND_INT32:
    case BIND_KIND_INT64:
    case BIND_KIND_INT:
    case BIND_KIND_UINT8:
    case BIND_KIND_UINT16:
    case BIND_KIND_UINT32:
    case BIND_KIND_UINT64:
    case BIND_KIND_UINT:
      return -1;
    default:
      break;
    }
  }
  switch (kind) {
  case BIND_KIND_INT8: {
    if (iv < -128 || iv > 127) return -1;
    *(int8_t *)dst = (int8_t)iv;
    return 0;
  }
  case BIND_KIND_INT16: {
    if (iv < -32768 || iv > 32767) return -1;
    *(int16_t *)dst = (int16_t)iv;
    return 0;
  }
  case BIND_KIND_INT32: {
    if (iv < -2147483648LL || iv > 2147483647LL) return -1;
    *(int32_t *)dst = (int32_t)iv;
    return 0;
  }
  case BIND_KIND_INT64: {
    __builtin_memcpy(dst, &iv, 8);
    return 0;
  }
  case BIND_KIND_INT: {
    *(intptr_t *)dst = (intptr_t)iv;
    return 0;
  }
  case BIND_KIND_UINT8: {
    if (uv > 0xFF) return -1;
    *(uint8_t *)dst = (uint8_t)uv;
    return 0;
  }
  case BIND_KIND_UINT16: {
    if (uv > 0xFFFF) return -1;
    *(uint16_t *)dst = (uint16_t)uv;
    return 0;
  }
  case BIND_KIND_UINT32: {
    if (uv > 0xFFFFFFFFu) return -1;
    *(uint32_t *)dst = (uint32_t)uv;
    return 0;
  }
  case BIND_KIND_UINT64: {
    /* Full-width unsigned targets need an explicit signed-tag check. Narrow
     * targets reject sign extension by range, while TAPE_UINT64 values above
     * 2^63 remain valid even though their iv view is negative.
     */
    if (!is_double && tag == (TAPE_INT64 >> 56) && iv < 0) return -1;
    __builtin_memcpy(dst, &uv, 8);
    return 0;
  }
  case BIND_KIND_UINT: {
    if (!is_double && tag == (TAPE_INT64 >> 56) && iv < 0) return -1;
    *(uintptr_t *)dst = (uintptr_t)uv;
    return 0;
  }
  case BIND_KIND_FLOAT32: {
    float f;
    if (is_double) {
      /* A zero span is unproducible: every TAPE_DOUBLE emitter copies the
       * token into str_arena. Treat it as a corrupt tape. */
      uint32_t len = (uint32_t)((word >> 32) & 0xFFFFFFu);
      if (UNLIKELY(len == 0)) return -1;
      const uint8_t *text = str_arena + (uint32_t)word;
      if (UNLIKELY(ndec_parse_float32(text, len, &f, atof))) return -1;
    } else if (tag == (TAPE_UINT64 >> 56)) {
      f = (float)uv;
    } else {
      f = (float)iv;
    }
    if (UNLIKELY(!__builtin_isfinite(f))) return -1;
    __builtin_memcpy(dst, &f, sizeof(f));
    return 0;
  }
  case BIND_KIND_FLOAT64: {
    __builtin_memcpy(dst, &dv, 8);
    return 0;
  }
  }
  return -1;
}

/* TAPE_NUM_RAW preserves source text when no binary payload is faithful.
 * Reparse its validated str_arena bytes at the target precision. The emitter
 * uses a binary integer tag for every representable int64 or uint64, so a raw
 * number always mismatches integer targets.
 */
INLINE int tape_bind_write_num_raw(uint8_t kind, const uint8_t *text, uint32_t len, uint8_t *dst, atof_ctx *atof) {
  switch (kind) {
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64:
  case BIND_KIND_INT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64:
  case BIND_KIND_UINT:
    return -1;
  case BIND_KIND_FLOAT32: {
    float f;
    if (UNLIKELY(ndec_parse_float32(text, len, &f, atof))) return -1;
    if (UNLIKELY(!__builtin_isfinite(f))) return -1;
    __builtin_memcpy(dst, &f, sizeof(f));
    return 0;
  }
  case BIND_KIND_FLOAT64: {
    double d;
    if (UNLIKELY(ndec_parse_double(text, len, &d, atof))) return -1;
    if (UNLIKELY(!__builtin_isfinite(d))) return -1;
    __builtin_memcpy(dst, &d, sizeof(d));
    return 0;
  }
  }
  return -1;
}

/* All tape-bind sites must consume number words identically. Integer, unsigned,
 * and double tags carry a following value word; TAPE_NUM_RAW carries its source
 * span in the tag word itself. ON_MISMATCH supplies the site's container or root
 * policy.
 */
#define TAPE_BIND_NUMBER_ARM(m, kind_, dst_, cont_label, ON_MISMATCH)                                             \
  do {                                                                                                            \
    if ((tag) == (TAPE_INT64 >> 56) || (tag) == (TAPE_UINT64 >> 56) || (tag) == (TAPE_DOUBLE >> 56)) {            \
      uint64_t _word = *TAP_CURSOR;                                                                               \
      uint64_t _v    = TAP_READ_NUMBER();                                                                         \
      if (UNLIKELY(tape_bind_write_number((kind_), (tag), _word, _v, (dst_), (m)->b.alloc.str_arena,              \
                                          (m)->c.atof) < 0))                                                      \
        ON_MISMATCH;                                                                                              \
      goto cont_label;                                                                                            \
    }                                                                                                             \
    if ((tag) == (TAPE_NUM_RAW >> 56)) {                                                                          \
      /* Advance only after a successful raw-number write. ON_MISMATCH skips                                      \
       * from the current word and would otherwise skip the following value. */                                   \
      uint32_t _nlen;                                                                                             \
      const uint8_t *_ntext = tape_bind_string_ptr(*TAP_CURSOR, (m)->b.alloc.str_arena, src, &_nlen);             \
      if (UNLIKELY(tape_bind_write_num_raw((kind_), _ntext, _nlen, (dst_), (m)->c.atof) < 0)) ON_MISMATCH;        \
      TAP_ADVANCE();                                                                                              \
      goto cont_label;                                                                                            \
    }                                                                                                             \
    ON_MISMATCH;                                                                                                  \
  } while (0)

#endif
