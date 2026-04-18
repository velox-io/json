/*
 * Go binding reactor hooks and NDEC_R_* macro overrides.
 *
 * Must be included BEFORE parser.h so that the override macros are visible
 * when the parser body is expanded. Requires types.h definitions
 * (NdecBindTypeInfo, NdecBindFieldInfo) to be in scope.
 *
 * NDEC_FRAME_EXTRA_FIELDS is injected into NdecFrame so every parser stack
 * frame carries binding state inline. The Go-side ABI (go_abi.h, the Go
 * trampolines) depends on the exact field order and offsets defined here.
 */
#ifndef NDEC_BIND_HOOKS_H
#define NDEC_BIND_HOOKS_H

#include <stdint.h>
#include <stdatomic.h>

/* Injected into NdecFrame by parser.h.
 * The union `as` overlays struct_ / slice_ / map_ with the same layout;
 * bind_container_kind selects which interpretation is valid.
 * _bind_pad[3] ensures bind_container_kind ends on a 4-byte boundary. */
#define NDEC_FRAME_EXTRA_FIELDS                                                                                   \
  const struct NdecBindTypeInfo *bind_type;                                                                       \
  uint8_t *bind_dst;                                                                                              \
  union {                                                                                                         \
    struct {                                                                                                      \
      int32_t pending_field_idx;                                                                                  \
      uint32_t _pad;                                                                                              \
    } struct_;                                                                                                    \
    struct {                                                                                                      \
      uint32_t array_index;                                                                                       \
      uint32_t array_cap;                                                                                         \
    } slice_;                                                                                                     \
    struct {                                                                                                      \
      uint32_t kv_count;                                                                                          \
      uint32_t kv_buf_cap;                                                                                        \
    } map_;                                                                                                       \
  } as;                                                                                                           \
  uint8_t bind_container_kind;                                                                                    \
  uint8_t _bind_pad[3];                                                                                           \
  int32_t parent_field_idx;                                                                                       \
  void *bind_slice_hdr;

#include "ndec/core/types.h"
#include "go_abi.h"
#include "ndec_bind_yield.h"
#include "ndec_bind_lookup.h"
#include "ndec_bind_target.h"
#include "ndec_bind_string.h"
#include "ndec_bind_map.h"
#include "containers/struct.h"
#include "containers/slice.h"
#include "containers/map.h"
#include "containers/root.h"

#define NDEC_R_BEGIN_OBJECT(ud)      ndec_bind_begin_object((ud), frames, depth)
#define NDEC_R_END_OBJECT(ud)        ndec_bind_end_object((ud), frames, depth)
#define NDEC_R_OBJECT_FIELD(ud, key) ndec_bind_object_field((ud), frames, depth, (key))
#define NDEC_R_BEGIN_ARRAY(ud)       ndec_bind_begin_array((ud), frames, depth)
#define NDEC_R_END_ARRAY(ud)         ndec_bind_end_array((ud), frames, depth)

#define NDEC_R_OBJ_SCALAR_NULL(ud)        ndec_bind_obj_scalar_null((ud), frames, depth)
#define NDEC_R_OBJ_SCALAR_BOOL(ud, v)     ndec_bind_obj_scalar_bool((ud), frames, depth, (v))
#define NDEC_R_OBJ_SCALAR_NUMBER(ud, raw) ndec_bind_obj_scalar_number((ud), frames, depth, (raw))
#define NDEC_R_OBJ_SCALAR_STRING(ud, raw) ndec_bind_obj_scalar_string((ud), frames, depth, (raw))

#define NDEC_R_ARR_SCALAR_NULL(ud)        ndec_bind_arr_scalar_null((ud), frames, depth)
#define NDEC_R_ARR_SCALAR_BOOL(ud, v)     ndec_bind_arr_scalar_bool((ud), frames, depth, (v))
#define NDEC_R_ARR_SCALAR_NUMBER(ud, raw) ndec_bind_arr_scalar_number((ud), frames, depth, (raw))
#define NDEC_R_ARR_SCALAR_STRING(ud, raw) ndec_bind_arr_scalar_string((ud), frames, depth, (raw))

/* ROOT scalar: dedicated root-frame hook reading frames[0].bind_dst directly,
 * with no STRUCT field indirection. The generic NDEC_R_SCALAR_* macros stay
 * unoverridden; binding does not install reactor->scalar_* function pointers,
 * so they fall through to the default vtable (equivalent to NDEC_PROCEED). */
#define NDEC_R_ROOT_SCALAR_NULL(ud)        ndec_bind_root_scalar_null((ud), frames, depth)
#define NDEC_R_ROOT_SCALAR_BOOL(ud, v)     ndec_bind_root_scalar_bool((ud), frames, depth, (v))
#define NDEC_R_ROOT_SCALAR_NUMBER(ud, raw) ndec_bind_root_scalar_number((ud), frames, depth, (raw))
#define NDEC_R_ROOT_SCALAR_STRING(ud, raw) ndec_bind_root_scalar_string((ud), frames, depth, (raw))

/* Called immediately after the parser STACK_PUSH. The child slot at
 * frames[depth-1] has been allocated but its NDEC_FRAME_EXTRA_FIELDS still
 * hold residue from the previous occupant of that slot, so every binding
 * field must be explicitly written.
 *
 * parent_field_idx is intentionally left unwritten. errCtx only consults
 * parent_field_idx for SLICE/MAP children of a STRUCT parent; STRUCT
 * children never enter that path, so stale residue is harmless. */
INLINE void ndec_bind_push_struct_child(NdecFrame *child, const NdecBindTypeInfo *ti, uint8_t *dst) {
  child->bind_type                    = ti;
  child->bind_dst                     = dst;
  child->as.struct_.pending_field_idx = -1;
  child->bind_container_kind          = NDEC_BK_STRUCT;
}

INLINE int32_t ndec_bind_begin_object(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;

  /* Parser bootstrap: after seeing '{' on the root value, the parser
   * STACK_PUSHes depth 1->2 and calls begin_object. Entry state:
   *   depth==2 : child = frames[1], driver pre-filled root binding.
   *              parent = frames[0] is the sentinel (BindContainerKind=INVALID).
   *   depth>=3 : child = frames[depth-1] (fresh STACK_PUSH slot, extras are
   *              stale), parent = frames[depth-2] is a real container frame
   *              (STRUCT / SLICE / MAP).
   *
   * Hot path: nested STRUCT object (depth>=3, parent is STRUCT or SLICE). */

  NdecFrame *child = &frames[depth - 1];

  if (NDEC_BIND_LIKELY(depth >= 3)) {
    NdecFrame *parent = &frames[depth - 2];

    /* SLICE<struct> parent: elem is struct object. */
    if (parent->bind_container_kind == NDEC_BK_SLICE) {
      const NdecBindTypeInfo *slice_ti = parent->bind_type;
      const NdecBindTypeInfo *elem_ti  = slice_ti->elem_type;
      if (NDEC_BIND_UNLIKELY(slice_ti->elem_kind == NDEC_BK_STRUCT)) {
        if (NDEC_BIND_UNLIKELY(parent->as.slice_.array_index >= parent->as.slice_.array_cap)) {
          NDEC_BIND_YIELD(ud, NDEC_YA_GROW_SLICE_STRUCT);
        }
        ndec_bind_push_struct_child(child, elem_ti,
                                    parent->bind_dst + parent->as.slice_.array_index * slice_ti->elem_size);
        return NDEC_PROCEED;
      }
      if (slice_ti->elem_kind == NDEC_BK_MAP) {
        if (NDEC_BIND_UNLIKELY(parent->as.slice_.array_index >= parent->as.slice_.array_cap)) {
          NDEC_BIND_YIELD(ud, NDEC_YA_BEGIN_MAP);
        }
        NDEC_BIND_YIELD(ud, NDEC_YA_BEGIN_MAP);
      }
      if (slice_ti->elem_kind == NDEC_BK_PTR && elem_ti != 0 && elem_ti->elem_kind == NDEC_BK_STRUCT) {
        NDEC_BIND_YIELD(ud, NDEC_YA_GROW_SLICE_PTR_STRUCT);
      }
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
    }

    /* MAP<struct>/MAP<*struct>/MAP<map> parent. */
    if (parent->bind_container_kind == NDEC_BK_MAP) {
      const NdecBindTypeInfo *map_ti  = parent->bind_type;
      const NdecBindTypeInfo *elem_ti = map_ti->elem_type;
      if (map_ti->elem_kind == NDEC_BK_STRUCT) {
        uint8_t *value_ptr = ndec_bind_map_value_ptr(parent);
        __builtin_memset(value_ptr, 0, map_ti->elem_size);
        ndec_bind_push_struct_child(child, elem_ti, value_ptr);
        return NDEC_PROCEED;
      }
      if (map_ti->elem_kind == NDEC_BK_MAP) {
        if (ndec_bind_begin_map_fast(parent, child, ud) == 0) {
          return NDEC_PROCEED;
        }
        NDEC_BIND_YIELD(ud, NDEC_YA_BEGIN_MAP);
      }
      if (map_ti->elem_kind == NDEC_BK_PTR && elem_ti != 0 && elem_ti->kind == NDEC_BK_STRUCT) {
        NDEC_BIND_YIELD_MAP_VALUE_PTR_STRUCT(ud);
      }
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
    }

    /* STRUCT parent: nested struct field or PTR/MAP variant. */
    int32_t idx = parent->as.struct_.pending_field_idx;
    if (NDEC_BIND_UNLIKELY(idx < 0)) {
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
    }
    const NdecBindTypeInfo *parent_ti = parent->bind_type;
    const NdecBindFieldInfo *fi       = &parent_ti->fields[idx];

    /* PTR-to-struct: yield Go alloc + fill child slot */
    if (fi->kind == NDEC_BK_PTR && fi->type != 0 && fi->type->kind == NDEC_BK_STRUCT) {
      NDEC_BIND_YIELD_PTR_STRUCT(ud);
    }

    /* MAP field (or *map): yield BEGIN_MAP, Go lazy allocs + fills MAP frame. */
    if ((fi->kind == NDEC_BK_MAP) || (fi->kind == NDEC_BK_PTR && fi->type != 0 && fi->type->kind == NDEC_BK_MAP)) {
      if (ndec_bind_begin_map_fast(parent, child, ud) == 0) {
        return NDEC_PROCEED;
      }
      NDEC_BIND_YIELD(ud, NDEC_YA_BEGIN_MAP);
    }

    if (NDEC_BIND_UNLIKELY(fi->kind != NDEC_BK_STRUCT || fi->type == 0)) {
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
    }

    ndec_bind_push_struct_child(child, fi->type, parent->bind_dst + fi->offset);
    return NDEC_PROCEED;
  }

  /* depth == 2: root entry. child = frames[1] with driver pre-filled binding.
   * parent = frames[0] is the sentinel and is not read. Dispatch by child.kind:
   * STRUCT (hot path) / MAP / PTR / mismatch. */
  {
    uint8_t kind = child->bind_container_kind;
    if (NDEC_BIND_LIKELY(kind == NDEC_BK_STRUCT)) {
      return NDEC_PROCEED;
    }
    if (kind == NDEC_BK_MAP) {
      /* Root MAP: yield BEGIN_MAP, Go driver allocs map header + kvBuf sub-region
       * and rewrites frames[1] as MAP frame. */
      NDEC_BIND_YIELD(ud, NDEC_YA_BEGIN_MAP);
    }
    if (kind == NDEC_BK_PTR) {
      /* Root PTR: yield with pointee-kind flags so Go driver performs
       * ptr-chain alloc and rewrites frames[1] as leaf frame. */
      const NdecBindTypeInfo *pointee = child->bind_type->elem_type;
      if (NDEC_BIND_UNLIKELY(pointee == 0))
        NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
      if (pointee->kind == NDEC_BK_STRUCT)
        NDEC_BIND_YIELD_PTR_STRUCT(ud);
      if (pointee->kind == NDEC_BK_MAP)
        NDEC_BIND_YIELD_PTR_TO_MAP(ud);
      if (pointee->kind == NDEC_BK_SLICE)
        NDEC_BIND_YIELD_PTR_TO_SLICE(ud);
      if (pointee->kind == NDEC_BK_PTR)
        NDEC_BIND_YIELD_PTR_STRUCT(ud);
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
    }
    NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_OBJECT);
  }
}

INLINE int32_t ndec_bind_end_object(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  /* The parser has already done STACK_POP. The popped frame is at
   * frames[depth] (POP does not clear data). The root sentinel guarantees
   * depth >= 1 after POP: on a nested close, parent is a real container
   * frame; on root close, parent = frames[0] is the sentinel
   * (BindContainerKind=INVALID, falls through default to PROCEED, then the
   * parser dispatches to root_done).
   *
   * MAP frame close: if kv_count > 0, yield FLUSH_MAP (yield_flags =
   * yfMapClosing=1). The driver completes the final batch of mapassign,
   * frees the KV buffer sub-interval, and advances the parent frame.
   * When kv_count == 0 (empty map or already flushed), the C side finishes
   * up and advances the parent. */
  NdecFrame *popped = &frames[depth];
  if (popped->bind_container_kind == NDEC_BK_MAP) {
    if (popped->as.map_.kv_count > 0) {
      ud->pending_action = (uint32_t)NDEC_YA_FLUSH_MAP;
      ud->yield_flags    = 1; /* yfMapClosing: triggered by end_object */
      return NDEC_YIELD;
    }
    /* Empty / already-flushed map still needs the kvBuf sub-interval
     * released. The interval was carved by BEGIN_MAP into
     * popped->bind_slice_hdr; the driver releases it on the FLUSH_MAP
     * closing path. Funnel both kv_count>0 and kv_count==0 through the
     * same yield so the release logic stays in one place on the Go side. */
    ud->pending_action = (uint32_t)NDEC_YA_FLUSH_MAP;
    ud->yield_flags    = 1; /* yfMapClosing */
    return NDEC_YIELD;
  }

  /* Non-MAP frame (child STRUCT frame close within a STRUCT parent):
   * advance parent frame. */
  NdecFrame *parent = &frames[depth - 1];
  switch (parent->bind_container_kind) {
  case NDEC_BK_STRUCT:
    parent->as.struct_.pending_field_idx = -1;
    return NDEC_PROCEED;
  case NDEC_BK_SLICE:
    parent->as.slice_.array_index++;
    return NDEC_PROCEED;
  case NDEC_BK_MAP:
    /* MAP<struct>: after child STRUCT frame pops, advance parent MAP
     * frame's kv_count. Full buffer triggers FLUSH_MAP yield (continuing
     * flush, yield_flags=NONE); otherwise continue parsing the next KV pair. */
    parent->as.map_.kv_count++;
    if (parent->as.map_.kv_count >= parent->as.map_.kv_buf_cap) {
      ud->pending_action = (uint32_t)NDEC_YA_FLUSH_MAP;
      ud->yield_flags    = NDEC_YF_NONE;
      return NDEC_YIELD;
    }
    return NDEC_PROCEED;
  default:
    /* Root object close: parent is the sentinel, NDEC_BK_INVALID, no advance. */
    return NDEC_PROCEED;
  }
}

INLINE int32_t ndec_bind_object_field(void *ud_v, NdecFrame *frames, uint32_t depth, NdecStrInfo key) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;

  NdecFrame *f = &frames[depth - 1];

  /* MAP frame branch: skip lookup, write key string directly into the
   * current KV slot's string header. Slot layout (key and value share
   * ndec_bind_map_value_ptr's offset base):
   *   off  0   GoStringHeader (key, 16 bytes)
   *   off 16   value bytes (written by subsequent obj_scalar_* hooks)
   * kv_count is NOT advanced yet (wait until value is written, keeping
   * KV atomicity).
   *
   * has_escape: unescape writes into scratch's persistent region and
   * advances scratch_len. The driver's KeepAlive(d) keeps scratch alive,
   * same as for STRUCT string values. */
  if (f->bind_container_kind == NDEC_BK_MAP) {
    const uint8_t *eff_ptr = key.raw.ptr;
    uint32_t eff_len       = key.raw.len;
    if (NDEC_BIND_UNLIKELY(key.has_escape)) {
      uint32_t written = 0;
      const uint8_t *p = ndec_bind_unescape_into_scratch(ud, key.raw.ptr, key.raw.len, &written);
      if (p == 0) {
        NDEC_BIND_YIELD(ud, NDEC_YA_TYPE_MISMATCH);
      }
      eff_ptr = p;
      eff_len = written;
    }
    /* key slot = value slot - 16; the helper returns the value slot address */
    NdecGoStringHeader *kh = (NdecGoStringHeader *)(ndec_bind_map_value_ptr(f) - 16);
    kh->data               = eff_ptr;
    kh->len                = (intptr_t)eff_len;
    return NDEC_PROCEED;
  }

  /* STRUCT frame path: unescaped temporary data is written into scratch
   * without advancing scratch_len; it is discarded immediately after lookup. */
  const uint8_t *eff_ptr = key.raw.ptr;
  uint32_t eff_len       = key.raw.len;
  if (NDEC_BIND_UNLIKELY(key.has_escape)) {
    uint8_t *tmp    = ud->scratch_ptr + ud->scratch_len;
    int32_t written = ndec_unescape(key.raw.ptr, key.raw.len, tmp);
    if (written < 0) {
      NDEC_BIND_YIELD(ud, NDEC_YA_TYPE_MISMATCH);
    }
    eff_ptr = tmp;
    eff_len = (uint32_t)written;
  }

  const NdecBindTypeInfo *ti = f->bind_type;
  int idx                    = ndec_bind_lookup_find(ti->lookup, ti, eff_ptr, eff_len);
  if (NDEC_BIND_UNLIKELY(idx < 0)) {
    if (ud->opt_flags & NDEC_OPT_DISALLOW_UNKNOWN) {
      NDEC_BIND_YIELD_RAW(ud, NDEC_YA_UNKNOWN_FIELD, eff_ptr, eff_len, NDEC_YF_NONE);
    }
    return NDEC_SKIP;
  }
  f->as.struct_.pending_field_idx = idx;
  return NDEC_PROCEED;
}

INLINE int32_t ndec_bind_begin_array(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;

  /* Parser bootstrap: root sentinel frames[0] + root binding frames[1].
   * root_value sees '[' and STACK_PUSHes, then calls this hook. Entry state:
   *   depth==2 : root array. child = frames[1] (driver pre-filled root
   *              binding), parent = frames[0] is the sentinel
   *              (BindContainerKind=INVALID).
   *   depth>=3 : nested array. parent is a real container frame
   *              (STRUCT / SLICE / MAP).
   *
   * STRUCT parent with pending field being SLICE: lazy alloc. The parent
   * field's slice header is initialized to (empty_slice_data, 0, 0) as the
   * default empty value. The child SLICE frame is filled but with
   * BindDst=NULL / array_cap=0; no yield, no allocation here.
   *
   * Both paths out of the lazy state still produce a valid hdr->data/cap:
   *   - Empty array (immediate ']'): end_array sees array_index=0, the
   *     header is already in the empty state, so just advance the parent.
   *     0 allocs, 0 yields.
   *   - First element appears: the scalar/struct hook goes through
   *     target_for_slice_elem, sees array_index (0) >= array_cap (0), gets
   *     NEED_GROW, and yields. The driver allocates backing and overwrites
   *     hdr->data/cap. */
  if (depth >= 3) {
    NdecFrame *parent = &frames[depth - 2];
    if (parent->bind_container_kind == NDEC_BK_STRUCT && parent->as.struct_.pending_field_idx >= 0) {
      const NdecBindTypeInfo *parent_ti = parent->bind_type;
      int32_t parent_idx                = parent->as.struct_.pending_field_idx;
      const NdecBindFieldInfo *fi       = &parent_ti->fields[parent_idx];
      if (fi->kind == NDEC_BK_SLICE && fi->type != 0) {
        const NdecBindTypeInfo *slice_ti = fi->type;

        NdecGoSliceHeader *hdr = (NdecGoSliceHeader *)(parent->bind_dst + fi->offset);
        hdr->data              = slice_ti->empty_slice_data;
        hdr->len               = 0;
        hdr->cap               = 0;

        parent->as.struct_.pending_field_idx = -1;

        NdecFrame *child             = &frames[depth - 1];
        child->bind_type             = slice_ti;
        child->bind_dst              = (uint8_t *)0;
        child->as.slice_.array_index = 0;
        child->as.slice_.array_cap   = 0;
        child->bind_container_kind   = NDEC_BK_SLICE;
        child->parent_field_idx      = parent_idx;
        child->bind_slice_hdr        = hdr;
        return NDEC_PROCEED;
      }

      if (fi->kind == NDEC_BK_PTR && fi->type != 0 && fi->type->kind == NDEC_BK_SLICE) {
        NDEC_BIND_YIELD_PTR_TO_SLICE(ud);
      }

      if (fi->kind == NDEC_BK_FIXED_ARRAY && fi->type != 0) {
        const NdecBindTypeInfo *arr_ti = fi->type;
        uint8_t *arr_base              = parent->bind_dst + fi->offset;
        __builtin_memset(arr_base, 0, (size_t)arr_ti->fixed_count * arr_ti->elem_size);
        parent->as.struct_.pending_field_idx = -1;

        NdecFrame *child             = &frames[depth - 1];
        child->bind_type             = arr_ti;
        child->bind_dst              = arr_base;
        child->as.slice_.array_index = 0;
        child->as.slice_.array_cap   = arr_ti->fixed_count;
        child->bind_container_kind   = NDEC_BK_FIXED_ARRAY;
        child->parent_field_idx      = parent_idx;
        child->bind_slice_hdr        = (void *)0;
        return NDEC_PROCEED;
      }
    }

    /* SLICE parent (nested [][]T): elem_kind is SLICE, push inner child frame.
     * The inner slice header lives at the outer elem slot.
     * If outer backing is not yet allocated, yield GROW_SLICE_STRUCT. */
    if (parent->bind_container_kind == NDEC_BK_SLICE) {
      const NdecBindTypeInfo *outer_ti = parent->bind_type;
      if (NDEC_BIND_UNLIKELY(outer_ti->elem_kind != NDEC_BK_SLICE || outer_ti->elem_type == 0)) {
        NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
      }
      const NdecBindTypeInfo *inner_ti = outer_ti->elem_type;

      if (parent->as.slice_.array_index >= parent->as.slice_.array_cap) {
        /* outer backing not allocated, yield to grow then re-enter */
        NDEC_BIND_YIELD(ud, NDEC_YA_GROW_SLICE_STRUCT);
      }

      NdecGoSliceHeader *hdr =
          (NdecGoSliceHeader *)(parent->bind_dst + parent->as.slice_.array_index * outer_ti->elem_size);
      hdr->data = inner_ti->empty_slice_data;
      hdr->len  = 0;
      hdr->cap  = 0;

      NdecFrame *child             = &frames[depth - 1];
      child->bind_type             = inner_ti;
      child->bind_dst              = (uint8_t *)0;
      child->as.slice_.array_index = 0;
      child->as.slice_.array_cap   = 0;
      child->bind_container_kind   = NDEC_BK_SLICE;
      /* SLICE-in-SLICE: parent is a SLICE, not STRUCT. errCtx never reads
       * parent_field_idx from a child of a SLICE parent, so stale residue
       * is harmless. */
      child->bind_slice_hdr = hdr;
      return NDEC_PROCEED;
    }

    /* MAP parent (map value is container): push child frame for map value. */
    if (parent->bind_container_kind == NDEC_BK_MAP) {
      const NdecBindTypeInfo *map_ti = parent->bind_type;
      if (map_ti->elem_kind != NDEC_BK_SLICE || map_ti->elem_type == 0) {
        NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
      }
      const NdecBindTypeInfo *slice_ti = map_ti->elem_type;

      NdecGoSliceHeader *hdr = (NdecGoSliceHeader *)ndec_bind_map_value_ptr(parent);
      hdr->data              = slice_ti->empty_slice_data;
      hdr->len               = 0;
      hdr->cap               = 0;

      NdecFrame *child             = &frames[depth - 1];
      child->bind_type             = slice_ti;
      child->bind_dst              = (uint8_t *)0;
      child->as.slice_.array_index = 0;
      child->as.slice_.array_cap   = 0;
      child->bind_container_kind   = NDEC_BK_SLICE;
      /* SLICE-in-MAP: parent is MAP, not STRUCT. Same reasoning as above. */
      child->bind_slice_hdr = hdr;
      return NDEC_PROCEED;
    }
  }

  /* depth == 2: root array entry. child = frames[1] with driver pre-filled
   * binding (parent = frames[0] is the sentinel). Dispatch by child.kind:
   * SLICE (lazy alloc), FIXED_ARRAY (memset), PTR (yield alloc chain). */
  {
    NdecFrame *child = &frames[depth - 1];
    uint8_t kind     = child->bind_container_kind;

    if (kind == NDEC_BK_SLICE) {
      /* Root SLICE lazy alloc: write empty header to user dst slot,
       * set child.dst=NULL for grow yield to fill on first element. */
      const NdecBindTypeInfo *slice_ti = child->bind_type;
      NdecGoSliceHeader *hdr           = (NdecGoSliceHeader *)child->bind_dst;
      hdr->data                        = slice_ti->empty_slice_data;
      hdr->len                         = 0;
      hdr->cap                         = 0;

      child->bind_dst              = (uint8_t *)0;
      child->as.slice_.array_index = 0;
      child->as.slice_.array_cap   = 0;
      /* root frame: parent is the sentinel, errCtx does not read parent_field_idx */
      child->bind_slice_hdr = (void *)hdr;
      return NDEC_PROCEED;
    }

    if (kind == NDEC_BK_FIXED_ARRAY) {
      /* Root FIXED_ARRAY: memset all slots to zero, set child array bounds. */
      const NdecBindTypeInfo *arr_ti = child->bind_type;
      __builtin_memset(child->bind_dst, 0, (size_t)arr_ti->fixed_count * arr_ti->elem_size);
      child->as.slice_.array_index = 0;
      child->as.slice_.array_cap   = arr_ti->fixed_count;
      child->bind_slice_hdr        = (void *)0;
      return NDEC_PROCEED;
    }

    if (kind == NDEC_BK_PTR) {
      /* Root PTR-to-slice/array: yield, Go driver allocs ptr chain + rewrites
       * frames[1] as leaf SLICE/FIXED_ARRAY frame. */
      const NdecBindTypeInfo *pointee = child->bind_type->elem_type;
      if (NDEC_BIND_UNLIKELY(pointee == 0))
        NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
      if (pointee->kind == NDEC_BK_SLICE)
        NDEC_BIND_YIELD_PTR_TO_SLICE(ud);
      if (pointee->kind == NDEC_BK_FIXED_ARRAY)
        NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
      if (pointee->kind == NDEC_BK_PTR)
        NDEC_BIND_YIELD_PTR_TO_SLICE(ud);
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
    }
  }
  NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_ARRAY);
}

INLINE int32_t ndec_bind_end_array(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  /* The parser has already done STACK_POP; the child SLICE frame is at
   * frames[depth] (POP does not clear data). Close is done inline in C:
   * write final len into the parent field's slice header, then advance
   * the parent frame.
   *
   * Empty array fast path: cap=0 (lazy alloc never triggered grow). The
   * header is already (empty_slice_data, 0, 0) from begin_array and len
   * is already 0. Skip the write and just advance the parent.
   * Non-empty array: hdr->data/cap were written by the first grow yield.
   * Only len needs to be written here. */
  NdecFrame *popped = &frames[depth];
  /* FIXED_ARRAY frame close: no slice header to update, no cap_hint EMA feed
   * (fixed size has no semantics). Just advance the parent frame. */
  if (popped->bind_container_kind == NDEC_BK_FIXED_ARRAY) {
    /* Root sentinel guarantees depth >= 1 after STACK_POP. On root array
     * close, parent = frames[0] is the BK_INVALID sentinel; no branch matches.
     * Parser proceeds to root_done. */
    NdecFrame *parent = &frames[depth - 1];
    if (NDEC_BIND_LIKELY(parent->bind_container_kind == NDEC_BK_STRUCT)) {
      parent->as.struct_.pending_field_idx = -1;
    } else if (parent->bind_container_kind == NDEC_BK_MAP) {
      NDEC_BIND_MAP_ADVANCE(ud, parent);
    } else if (parent->bind_container_kind == NDEC_BK_SLICE ||
               parent->bind_container_kind == NDEC_BK_FIXED_ARRAY) {
      parent->as.slice_.array_index++;
    }
    /* INVALID sentinel: root array close, no advance; parser takes root_done. */
    return NDEC_PROCEED;
  }
  if (NDEC_BIND_LIKELY(popped->bind_container_kind == NDEC_BK_SLICE)) {
    NdecGoSliceHeader *hdr = (NdecGoSliceHeader *)popped->bind_slice_hdr;
    uint32_t observed_len  = popped->as.slice_.array_index;
    if (NDEC_BIND_LIKELY(hdr != 0 && popped->as.slice_.array_cap > 0)) {
      hdr->len = (intptr_t)observed_len;

      /* EMA-adaptive cap_hint update (alpha=2):
       *   hint = (old + observed_len) / 2
       * Relaxed atomic: a dirty read across goroutines at worst picks a
       * sub-optimal capacity, not affecting correctness. Only written for
       * non-empty arrays; empty arrays do not feed 0 into the EMA (would
       * pollute the hint for types that normally see non-empty arrays).
       * The const cast is safe: the typeinfo is held via unsafe.Pointer on
       * the Go side; the C side sees it as const-by-default but it is
       * actually writable. */
      NdecBindTypeInfo *slice_ti = (NdecBindTypeInfo *)(uintptr_t)popped->bind_type;
      int32_t old_hint           = atomic_load_explicit(&slice_ti->cap_hint, memory_order_relaxed);
      int32_t new_hint;
      if (old_hint == 0) {
        new_hint = (int32_t)observed_len;
      } else {
        new_hint = (old_hint + (int32_t)observed_len) / 2;
      }
      atomic_store_explicit(&slice_ti->cap_hint, new_hint, memory_order_relaxed);
    }
    /* Advance parent frame: STRUCT clears pending, SLICE increments
     * array_index++, MAP increments kv_count++ (with flush check).
     * On root array close, parent is the BK_INVALID sentinel; default
     * does not advance. */
    NdecFrame *parent = &frames[depth - 1];
    if (NDEC_BIND_LIKELY(parent->bind_container_kind == NDEC_BK_STRUCT)) {
      parent->as.struct_.pending_field_idx = -1;
    } else if (parent->bind_container_kind == NDEC_BK_MAP) {
      NDEC_BIND_MAP_ADVANCE(ud, parent);
    } else if (parent->bind_container_kind == NDEC_BK_SLICE) {
      parent->as.slice_.array_index++;
    }
    return NDEC_PROCEED;
  }
  /* Non-SLICE, non-FIXED_ARRAY frame: array close on a non-array frame is
   * a structural mismatch. Surface as TYPE_MISMATCH for the driver. */
  NDEC_BIND_YIELD(ud, NDEC_YA_TYPE_MISMATCH);
}

/* STRUCT scalar hooks, specialized per container kind. */
INLINE int32_t ndec_bind_obj_scalar_null(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  if (top->bind_container_kind == NDEC_BK_MAP) {
    return map_writeval_null(top, ud);
  }
  return struct_writeval_null(top, ud);
}

INLINE int32_t ndec_bind_obj_scalar_bool(void *ud_v, NdecFrame *frames, uint32_t depth, int v) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  if (top->bind_container_kind == NDEC_BK_MAP) {
    return map_writeval_bool(top, ud, v);
  }
  return struct_writeval_bool(top, ud, v);
}

INLINE int32_t ndec_bind_obj_scalar_number(void *ud_v, NdecFrame *frames, uint32_t depth, NdecRawStr raw) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  if (top->bind_container_kind == NDEC_BK_MAP) {
    return map_writeval_number(top, ud, raw.ptr, raw.len);
  }
  return struct_writeval_number(top, ud, raw.ptr, raw.len);
}

INLINE int32_t ndec_bind_obj_scalar_string(void *ud_v, NdecFrame *frames, uint32_t depth, NdecStrInfo str) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];

  const uint8_t *eff_ptr = str.raw.ptr;
  uint32_t eff_len       = str.raw.len;
  if (NDEC_BIND_UNLIKELY(str.has_escape)) {
    uint32_t written = 0;
    const uint8_t *p = ndec_bind_unescape_into_scratch(ud, str.raw.ptr, str.raw.len, &written);
    if (p == 0) {
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
    }
    eff_ptr = p;
    eff_len = written;
  }

  if (top->bind_container_kind == NDEC_BK_MAP) {
    return map_writeval_string(top, ud, eff_ptr, eff_len);
  }
  return struct_writeval_string(top, ud, eff_ptr, eff_len);
}

/* SLICE scalar hooks, specialized per element write target. */
INLINE int32_t ndec_bind_arr_scalar_null(void *ud_v, NdecFrame *frames, uint32_t depth) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  return slice_writeval_null(top, ud);
}

INLINE int32_t ndec_bind_arr_scalar_bool(void *ud_v, NdecFrame *frames, uint32_t depth, int v) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  return slice_writeval_bool(top, ud, v);
}

INLINE int32_t ndec_bind_arr_scalar_number(void *ud_v, NdecFrame *frames, uint32_t depth, NdecRawStr raw) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];
  return slice_writeval_number(top, ud, raw.ptr, raw.len);
}

INLINE int32_t ndec_bind_arr_scalar_string(void *ud_v, NdecFrame *frames, uint32_t depth, NdecStrInfo str) {
  NdecBindUserData *ud = (NdecBindUserData *)ud_v;
  NdecFrame *top       = &frames[depth - 1];

  const uint8_t *eff_ptr = str.raw.ptr;
  uint32_t eff_len       = str.raw.len;
  if (NDEC_BIND_UNLIKELY(str.has_escape)) {
    uint32_t written = 0;
    const uint8_t *p = ndec_bind_unescape_into_scratch(ud, str.raw.ptr, str.raw.len, &written);
    if (p == 0) {
      NDEC_BIND_YIELD_TYPE_MISMATCH(ud, NDEC_YF_TOKEN_STRING);
    }
    eff_ptr = p;
    eff_len = written;
  }
  return slice_writeval_string(top, ud, eff_ptr, eff_len);
}

/*
 * ROOT scalar hooks (static, not INLINE; root scalar is a cold path)
 */
static int32_t ndec_bind_root_scalar_null(void *ud_v, NdecFrame *frames, uint32_t depth) {
  (void)depth;
  return root_writeval_null(&frames[1], (NdecBindUserData *)ud_v);
}

static int32_t ndec_bind_root_scalar_bool(void *ud_v, NdecFrame *frames, uint32_t depth, int v) {
  (void)depth;
  return root_writeval_bool(&frames[1], (NdecBindUserData *)ud_v, v);
}

static int32_t ndec_bind_root_scalar_number(void *ud_v, NdecFrame *frames, uint32_t depth, NdecRawStr raw) {
  (void)depth;
  return root_writeval_number(&frames[1], (NdecBindUserData *)ud_v, raw.ptr, raw.len);
}

static int32_t ndec_bind_root_scalar_string(void *ud_v, NdecFrame *frames, uint32_t depth, NdecStrInfo str) {
  (void)depth;
  NdecBindUserData *ud   = (NdecBindUserData *)ud_v;
  const uint8_t *eff_ptr = str.raw.ptr;
  uint32_t eff_len       = str.raw.len;
  if (NDEC_BIND_UNLIKELY(str.has_escape)) {
    uint32_t written = 0;
    const uint8_t *p = ndec_bind_unescape_into_scratch(ud, str.raw.ptr, str.raw.len, &written);
    if (p == 0)
      NDEC_BIND_YIELD(ud, NDEC_YA_TYPE_MISMATCH);
    eff_ptr = p;
    eff_len = written;
  }
  return root_writeval_string(&frames[1], ud, eff_ptr, eff_len);
}

#endif /* NDEC_BIND_HOOKS_H */
