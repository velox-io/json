#ifndef NDEC_MACHINE_H
#define NDEC_MACHINE_H

#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>

#include "macros.h"
#include "ndec/bind_bridge.h"
#include "ndec/core/tape.h"
#include "ndec/string.h"
#include "vlib/lookup.h"

#define BIND_MAX_DEPTH      255
#define BIND_AUX_STACK_SIZE 16 /* Slot zero is the sentinel; 15 nested phase 2 structs remain. */

/* A frame preserves the parent container across a child descent.
 *
 * MAP stores the published parent slot in dst and derives cur_count from the
 * live region. SLICE and STREAM persist cur_count in the slice header, leaving
 * u.sc available for the next write pointer. STRUCT has no count and caches its
 * lookup pointer in u.sc. ARRAY has no header, so u.raw_count holds its index.
 * The union is interpreted strictly by kind; map draining reads u.map_region
 * only for MAP frames. */
typedef struct BindFrame {
  uint8_t *dst;          /* off 0  parent container base (or parent_slot for MAP) */
  uint8_t kind;          /* off 8  BindKind */
  uint8_t flags;         /* off 9  cur_type.flags (BIND_FLAG_*); preserved across push/pop
                          * so array_value/continue see the container's real mode bits
                          * after a nested container closes. */
  uint16_t type_idx;     /* off 10 index into ctx.types / ctx.type_meta */
  union {                /* off 12 kind-tagged 4B view of cur_type.u.raw */
    uint32_t child_size; /*   element size restored for ARRAY, SLICE, and STREAM */
    uint32_t raw;        /*   opaque cur_type.u.raw for other kinds */
  } cs;
  const void *child_type;            /* off 16 cur_type.child (BindType* or BindField*) */
  union {                            /* off 24 8B kind-tagged */
    BindMapRegionHeader *map_region; /*   MAP region header in map_buf */
    void *sc;                        /*   SLICE write pointer or STRUCT ndec_lookup* */
    uint64_t raw64;                  /*   8B raw view for zero initialization and spill */
    uint32_t raw_count;              /*   ARRAY element index */
    dom_open_ctn ctn;                /*   vd tape walk state stored above depth; drain scans only
                                      *   live bind frames through frames[depth]. */
  } u;
} BindFrame;
_Static_assert(sizeof(BindFrame) == 32, "BindFrame size drift");
_Static_assert(offsetof(BindFrame, dst) == 0, "BindFrame.dst");
_Static_assert(offsetof(BindFrame, kind) == 8, "BindFrame.kind");
_Static_assert(offsetof(BindFrame, flags) == 9, "BindFrame.flags");
_Static_assert(offsetof(BindFrame, type_idx) == 10, "BindFrame.type_idx");
_Static_assert(offsetof(BindFrame, cs) == 12, "BindFrame.cs");
_Static_assert(offsetof(BindFrame, child_type) == 16, "BindFrame.child");
_Static_assert(offsetof(BindFrame, u) == 24, "BindFrame.u");

/* Incremental tape state on the shared bump arena.
 *
 * start identifies the root. end tracks this tape's next logical position
 * because unrelated writers may append between entries. A mismatch with
 * tape_used is bridged by widening the reserved seam before the gap.
 *
 * A phase 2 struct builds one physical tape. When an inline variant and
 * reserve-unknown both consume it, the latter uses a second logical view
 * encoded by the other seam distance. That view needs an independent count
 * but no independent build cursor. */
typedef struct TapeBuild {
  uint32_t start; /* Absolute root index, or BIND_AUX_NO_TAPE before opening. */
  uint32_t end;   /* Position after the last write, including its reserved seam. */
  uint32_t count; /* Phase 1 entry count before per-view classification. */
} TapeBuild;      /* 12B */

/* The auxiliary stack is indexed by struct nesting, not parse depth.
 *
 * Phase 1 writes fields whose ownership is not yet decidable to one merged
 * tape. At struct close, phase 2 combines that self-describing tape with the
 * immutable host field table to recover the target, variant, and case. Values
 * resolved at the field site leave no delayed state.
 *
 * Phase 2 closes nested structs synchronously in LIFO order, so a stack is
 * sufficient. A case descent pushes a real child while the host frame remains
 * available to the continuing walk. Bind frames preserve hot container state;
 * rebind frames preserve the replaced input cursor. */
typedef struct BindAuxFrame {
  int32_t owner_depth; /* off 0  Parse depth owning this slot; slot 0 is the sentinel. */
  int32_t parent_aux;  /* off 4  Enclosing struct slot for LIFO restoration. */
  TapeBuild a;         /* off 8  Merged tape written by phase 1 and classified by phase 2. */
  /* View B has no writer, but its entry count diverges during classification.
   * It starts at a.count, decreases when entries leave B, and patches B's root
   * after the walk. */
  uint32_t b_count; /* off 20 */
  uint32_t walk;    /* off 24 Reserved seam before the next phase 2 entry. */
  /* The active value range survives case descent and BLOCK_FULL. It cannot use
   * the outer cursor, whose structural-index interpretation resumes after the
   * synchronous phase 2 walk, or idx_end, which the descent replaces. */
  uint32_t val_at;  /* off 28 */
  uint32_t val_end; /* off 32 */
  /* Presence must distinguish a missing inline discriminator from an explicit
   * empty string. The bound Go string cannot distinguish them, and phase 2
   * removes the discriminator entry before final case selection. */
  uint32_t disc_seen; /* off 36 */
  uint32_t _pad[2];   /* off 40 Preserves the 48-byte ABI layout. */
} BindAuxFrame;       /* 48B */
_Static_assert(sizeof(BindAuxFrame) == 48, "BindAuxFrame size drift");

#define BIND_AUX_NO_TAPE 0xFFFFFFFFu /* No merged tape has been opened for this struct. */

#define BIND_REBIND_STACK_SIZE 4 /* Maximum nested phase 2 descent depth. */

typedef struct BindAuxRebind {
  const uint32_t *saved_idx_p;   /* off 0  Outer structural or tape cursor. */
  const uint32_t *saved_idx_end; /* off 8 */
  uint64_t *saved_value_tape;    /* off 16 Outer tape base restored after descent. */
  uint32_t return_phase;         /* off 24 Phase restored after the case closes. */
  int32_t saved_base_depth;      /* off 28 Outer tape-bind close depth. */
  /* A case descent replaces the input cursor, tape base, and view mode as one
   * unit. Case content always uses view A of the host's merged tape, but an
   * outer UnmarshalValue walk may use view B and must regain it on return.
   * The complete mode, including CountAtClose, is saved so an escaping inline
   * root keeps its count-location contract across the descent. Phase 2
   * classification explicitly reads view A and does not consume this field. */
  uint32_t saved_view_mode; /* off 32 */
  uint32_t _pad;            /* off 36 Preserves 8-byte alignment and the 40-byte ABI size. */
} BindAuxRebind;            /* 40B */
_Static_assert(sizeof(BindAuxRebind) == 40, "BindAuxRebind size drift");
_Static_assert(offsetof(BindAuxRebind, saved_idx_p) == 0, "BindAuxRebind.saved_idx_p");
_Static_assert(offsetof(BindAuxRebind, saved_value_tape) == 16, "BindAuxRebind.saved_value_tape");
_Static_assert(offsetof(BindAuxRebind, return_phase) == 24, "BindAuxRebind.return_phase");

/* Binding state embedded after NdecBindBridge. Register-resident hot locals
 * spill here across every yield. cur_aux is interpreted by container kind. */
typedef struct NdecBindCore {
  uint32_t phase;            /* off 0  Fresh JSON parses start at BIND_PHASE_ROOT. */
  int32_t depth;             /* off 4  */
  BindType cur_type;         /* off 8  current container's BindType*/
  uint8_t *cur_dst;          /* off 24 */
  uint32_t cur_count;        /* off 32 */
  uint32_t first_error_kind; /* off 36  First mismatch, with zero meaning none; promoted
                              * to Yield at document end. Placed here to keep str_used aligned. */
  size_t str_used;           /* off 40  Next-free string-arena byte offset. */
  atof_ctx *atof;            /* off 48  Driver-owned floating-point scratch. */

  union {
    struct {
      uint8_t *field; /* off 56 Field needed by OBJECT_FIELD_VALUE resume; cur_dst
                       * already preserves the struct base. */
    } field_value;
    struct {
      uint8_t *slot; /* off 56 Parent slot for eface publication. The any metadata
                      * is the TypeTree singleton and need not be stashed. */
    } any_yield;
    struct {
      uint8_t *slot;        /* off 56 Receiver for deferred Unmarshal. It must remain
                             * GC-scannable while the closure may publish pointers. */
      const BindType *type; /* off 64 Resolved site-specific type needed after
                             * FLUSH_UNMARSHAL, outside the standard spill set. */
    } deferred_yield;
    struct {
      uint8_t *slot;      /* off 56 Destination for a Value alias yield. */
      uint32_t view_mode; /* off 64 Active view mode including flag bits. */
      uint32_t _pad;      /* off 68 */
    } tape_value_yield;
  } stash; /* off 56  Site-specific state that must survive a yield. */

  /* Kind-tagged pointer to the active container's side state. */
  void *cur_aux; /* off 72 */
  /* frames[0] is the root sentinel. depth is zero outside the root and equals
   * container nesting inside it, making every unconditional push and pop slot
   * access valid without a lower-bound branch. */
  BindFrame frames[BIND_MAX_DEPTH + 1]; /* off 80  8 KiB (32B * 256, [0]=sentinel) */
} NdecBindCore;
_Static_assert(offsetof(NdecBindCore, str_used) == 40, "NdecBindCore.str_used offset");
_Static_assert(offsetof(NdecBindCore, atof) == 48, "NdecBindCore.atof offset");
_Static_assert(offsetof(NdecBindCore, stash) == 56, "NdecBindCore.stash offset");
_Static_assert(offsetof(NdecBindCore, cur_aux) == 72, "NdecBindCore.cur_aux offset");
_Static_assert(offsetof(NdecBindCore, frames) == 80, "NdecBindCore.frames offset");

typedef struct NdecBindMachine {
  NdecBindBridge b;      /* off 0    driver-engine bridge (ctx 80 + alloc 120 + yield 24 = 224B) */
  NdecBindCore c;        /* off 224  binding state machine internals (scalars 80 + frames 8KiB = 8272B) */
  const uint32_t *idx_p; /* off 8496 structural index cursor (overlaid by tape cursor during tape-bind) */
  const uint32_t *idx_end;
  int32_t aux_depth; /* Current struct auxiliary slot; zero is the sentinel. Cold
                      * poly paths update it in memory, so it consumes no hot register
                      * and needs no yield spill. */

  /* Indexed by struct nesting rather than parse depth. A struct entering phase 2
   * lazily claims a slot, then restores parent_aux at close. */
  BindAuxFrame auxFrames[BIND_AUX_STACK_SIZE]; /* 16 * 48 = 768B */

  /* Preserves the outer input state while a phase 2 case descent replaces it. */
  BindAuxRebind rebind_stack[BIND_REBIND_STACK_SIZE]; /* 4 * 40 = 160B */
  uint8_t rebind_top;                                 /* Zero means empty. */

  /* Depth at which the current tape walk's root closes. Each phase 2 case
   * descent resets it so t_document_end can recognize that close. */
  int32_t tape_bind_base_depth;

  /* Logical view mode read by the current tape walk. It is machine state because
   * both yields and case descents outlive local registers. Input cursor, tape
   * base, and mode change and restore together. Ordinary tapes use zero, which
   * selects view A. The low bits name the seam view; the remaining bits carry
   * mode flags such as CountAtClose, so seam consumers mask with
   * TAPE_VIEW_SHIFT_MASK. */
  uint32_t tape_view_mode;

  /* Identifies the outer walk as tape input rather than JSON input. Case descents
   * always use tape labels and do not change it. Phase 2 uses this flag to return
   * non-root closes to the compatible TAP or IDX continuation family. */
  uint8_t in_tape_bind;
} NdecBindMachine;
_Static_assert(offsetof(NdecBindMachine, b) == 0, "bridge must be at offset 0");
_Static_assert(offsetof(NdecBindMachine, idx_p) == 8496, "idx_p offset must match Go BindMachineCursorOffset");

/* Save the parent at frames[depth], then advance depth. On success the caller
 * installs the child's hot state.
 *
 * MAP preserves the live region because draining may relocate it. SLICE and
 * STREAM store their count in the stable parent header and use u.sc for the
 * next write pointer. STRUCT caches its lookup pointer and has count zero.
 * ARRAY stores its count in the frame. A root push writes frames[0] as a
 * sentinel restore target, keeping both push and pop free of root branches. */
INLINE int bind_push(BindFrame *frames, int32_t *depthp, uint8_t *cur_dst, BindType cur_type, uint32_t cur_count,
                     void *cur_aux) {
  int32_t depth = *depthp;
  BindFrame *f  = &frames[depth];
  if (BIND_IS_SLICE_LIKE(cur_type.kind)) {
    /* The stable parent header carries the count across the child descent. */
    *(intptr_t *)(cur_dst + 8) = (intptr_t)cur_count;
  }
  f->dst           = cur_dst;
  f->kind          = cur_type.kind;
  f->flags         = cur_type.flags;
  f->type_idx      = cur_type.type_idx;
  f->cs.child_size = cur_type.u.raw;
  f->child_type    = (const void *)cur_type.child;
  if (cur_type.kind == BIND_KIND_MAP) {
    f->u.map_region = (BindMapRegionHeader *)cur_aux;
  } else if (BIND_IS_SLICE_LIKE(cur_type.kind) || cur_type.kind == BIND_KIND_STRUCT) {
    /* These kinds preserve pointer-valued side state in the shared union slot. */
    f->u.sc = cur_aux;
  } else {
    f->u.raw64 = (uint64_t)cur_count; /* Zero extension clears the unused union bytes. */
  }
  int32_t nd = depth + 1;
  if (UNLIKELY(nd > BIND_MAX_DEPTH)) return -1;
  *depthp = nd;
  return 0;
}

/* Callers with a statically known parent kind use these variants to remove
 * union dispatch after inlining. Dynamic parent sites use bind_push. */
INLINE int bind_push_struct(BindFrame *frames, int32_t *depthp, uint8_t *cur_dst, BindType cur_type,
                            uint32_t cur_count, void *cur_aux) {
  int32_t depth    = *depthp;
  BindFrame *f     = &frames[depth];
  f->dst           = cur_dst;
  f->kind          = cur_type.kind;
  f->flags         = cur_type.flags;
  f->type_idx      = cur_type.type_idx;
  f->cs.child_size = cur_type.u.raw;
  f->child_type    = (const void *)cur_type.child;
  f->u.sc          = cur_aux;
  (void)cur_count;
  int32_t nd = depth + 1;
  if (UNLIKELY(nd > BIND_MAX_DEPTH)) return -1;
  *depthp = nd;
  return 0;
}

INLINE int bind_push_map(BindFrame *frames, int32_t *depthp, uint8_t *cur_dst, BindType cur_type,
                         uint32_t cur_count, void *cur_aux) {
  int32_t depth    = *depthp;
  BindFrame *f     = &frames[depth];
  f->dst           = cur_dst;
  f->kind          = cur_type.kind;
  f->flags         = cur_type.flags;
  f->type_idx      = cur_type.type_idx;
  f->cs.child_size = cur_type.u.raw;
  f->child_type    = (const void *)cur_type.child;
  f->u.map_region  = (BindMapRegionHeader *)cur_aux;
  (void)cur_count;
  int32_t nd = depth + 1;
  if (UNLIKELY(nd > BIND_MAX_DEPTH)) return -1;
  *depthp = nd;
  return 0;
}

INLINE int bind_push_array_or_slice(BindFrame *frames, int32_t *depthp, uint8_t *cur_dst, BindType cur_type,
                                    uint32_t cur_count, void *cur_aux) {
  int32_t depth = *depthp;
  BindFrame *f  = &frames[depth];
  if (BIND_IS_SLICE_LIKE(cur_type.kind)) {
    /* The stable parent header carries the count across the child descent. */
    *(intptr_t *)(cur_dst + 8) = (intptr_t)cur_count;
    f->u.sc                    = cur_aux;
  } else {
    f->u.raw64 = (uint64_t)cur_count;
  }
  f->dst           = cur_dst;
  f->kind          = cur_type.kind;
  f->flags         = cur_type.flags;
  f->type_idx      = cur_type.type_idx;
  f->cs.child_size = cur_type.u.raw;
  f->child_type    = (const void *)cur_type.child;
  int32_t nd       = depth + 1;
  if (UNLIKELY(nd > BIND_MAX_DEPTH)) return -1;
  *depthp = nd;
  return 0;
}

/* Restore frames[depth - 1] unconditionally. At root close this is the sentinel
 * written by the root push, and callers detect completion from depth zero.
 * cur_dst must be restored before slice-like counts are read from its header. */
INLINE void bind_pop(BindFrame *frames, int32_t *depthp, uint8_t **cur_dst, BindType *cur_type,
                     uint32_t *cur_count, void **cur_aux) {
  int32_t nd         = *depthp - 1;
  *depthp            = nd;
  BindFrame *f       = &frames[nd];
  *cur_dst           = f->dst;
  cur_type->kind     = f->kind;
  cur_type->flags    = f->flags;
  cur_type->type_idx = f->type_idx;
  cur_type->u.raw    = f->cs.child_size;
  cur_type->child    = (uintptr_t)f->child_type;
  if (f->kind == BIND_KIND_MAP) {
    *cur_aux = f->u.map_region;
    /* Only the root sentinel lacks a live map region. */
    *cur_count = f->u.map_region ? f->u.map_region->entry_count : 0;
  } else if (BIND_IS_SLICE_LIKE(f->kind)) {
    *cur_aux = f->u.sc;
    /* The stored length is bounded by the uint32_t machine count. */
    intptr_t _len;
    __builtin_memcpy(&_len, *cur_dst + 8, sizeof(_len));
    *cur_count = (uint32_t)_len;
  } else if (f->kind == BIND_KIND_STRUCT) {
    *cur_aux   = f->u.sc;
    *cur_count = 0;
  } else {
    /* ARRAY reconstructs the next write pointer from its saved next index. */
    *cur_count = f->u.raw_count;
    *cur_aux   = *cur_dst + (uintptr_t)(*cur_count) * (uintptr_t)f->cs.child_size;
  }
}

/* Callers with a statically known restored kind use these variants to remove
 * union dispatch after inlining. Dynamic restore sites use bind_pop. */
INLINE void bind_pop_struct(BindFrame *frames, int32_t *depthp, uint8_t **cur_dst, BindType *cur_type,
                            uint32_t *cur_count, void **cur_aux) {
  int32_t nd         = *depthp - 1;
  *depthp            = nd;
  BindFrame *f       = &frames[nd];
  *cur_dst           = f->dst;
  cur_type->kind     = f->kind;
  cur_type->flags    = f->flags;
  cur_type->type_idx = f->type_idx;
  cur_type->u.raw    = f->cs.child_size;
  cur_type->child    = (uintptr_t)f->child_type;
  *cur_aux           = f->u.sc;
  *cur_count         = 0;
}

INLINE void bind_pop_map(BindFrame *frames, int32_t *depthp, uint8_t **cur_dst, BindType *cur_type,
                         uint32_t *cur_count, void **cur_aux) {
  int32_t nd         = *depthp - 1;
  *depthp            = nd;
  BindFrame *f       = &frames[nd];
  *cur_dst           = f->dst;
  cur_type->kind     = f->kind;
  cur_type->flags    = f->flags;
  cur_type->type_idx = f->type_idx;
  cur_type->u.raw    = f->cs.child_size;
  cur_type->child    = (uintptr_t)f->child_type;
  *cur_aux           = f->u.map_region;
  /* Only the root sentinel lacks a live map region. */
  *cur_count = f->u.map_region ? f->u.map_region->entry_count : 0;
}

INLINE void bind_pop_array_or_slice(BindFrame *frames, int32_t *depthp, uint8_t **cur_dst, BindType *cur_type,
                                    uint32_t *cur_count, void **cur_aux) {
  int32_t nd         = *depthp - 1;
  *depthp            = nd;
  BindFrame *f       = &frames[nd];
  *cur_dst           = f->dst;
  cur_type->kind     = f->kind;
  cur_type->flags    = f->flags;
  cur_type->type_idx = f->type_idx;
  cur_type->u.raw    = f->cs.child_size;
  cur_type->child    = (uintptr_t)f->child_type;
  if (BIND_IS_SLICE_LIKE(f->kind)) {
    *cur_aux = f->u.sc;
    /* The stored length is bounded by the uint32_t machine count. */
    intptr_t _len;
    __builtin_memcpy(&_len, *cur_dst + 8, sizeof(_len));
    *cur_count = (uint32_t)_len;
  } else {
    /* ARRAY reconstructs the next write pointer from its saved next index. */
    *cur_count = f->u.raw_count;
    *cur_aux   = *cur_dst + (uintptr_t)(*cur_count) * (uintptr_t)f->cs.child_size;
  }
}

/* The lookup reads key storage directly and matches on the caller-supplied
 * length, so bodies of every string tag qualify. The
 * arena's pooled tail covers its SIMD loads.
 * NOINLINE keeps the tier bodies' SIMD spills out of the parse function's
 * nosplit-constrained frame. */
NOINLINE static int bind_lookup_key(const ndec_lookup *lk, const uint8_t *kdata, uint32_t klen) {
  ndec_lookup_key lkey = {(const char *)kdata, (size_t)klen};
  return ndec_lookup_find(lk, lkey);
}

/* A merged tape reserves one seam before every entry, with adjacent entries
 * sharing the boundary seam.
 *
 *   [TagObjBeg][seam][k1][v1..][seam][k2][v2..][seam][TagObjEnd]
 *
 * If another arena writer creates a gap, both seam distances widen across that
 * physical gap. During phase 2, widening only one distance removes an entry
 * from that logical view without moving words. Unconditional reservation lets
 * unrelated writers remain unaware of active merged tapes.
 *
 * With two consumers, reserve-unknown uses view B over the same words rather
 * than a second tape. Compact vd tapes are contiguous and use no seams. All
 * container pair indices remain relative to tb->start, which is also the
 * published Value and descent base. */

/* Patch the shared object begin with its member count and close index relative
 * to the base. */
INLINE void tape_build_patch_open(NdecBindMachine *m, uint32_t base, uint32_t open, uint32_t close,
                                  uint32_t count) {
  if (count > 0xFFFFFFu) count = 0xFFFFFFu; /* The tape format stores a 24-bit count. */
  m->b.alloc.tape_arena[open] = TAPE_START_OBJECT | (uint64_t)(close - base) | ((uint64_t)count << 32);
}

/* Patch the close word's high24, otherwise unused, with the inline projection's
 * member count; a dual shared root carries both counts in its begin/close pair. */
INLINE void tape_build_patch_close_count(NdecBindMachine *m, uint32_t close, uint32_t count) {
  if (count > 0xFFFFFFu) count = 0xFFFFFFu; /* The tape format stores a 24-bit count. */
  uint64_t w                   = m->b.alloc.tape_arena[close];
  m->b.alloc.tape_arena[close] = TAPE_END_OBJECT | (w & 0xFFFFFFFFu) | ((uint64_t)count << 32);
}

/* Lazily open a merged tape. Every merged tape, dual or single view, shares one
 * physical shape:
 *
 *   rel 0  shared object begin, also the base of every paired container index
 *   rel 1  shared leading seam, the sole logical split point between views
 *   rel 2  first entry or the shared close
 *   rel N  shared object end
 *
 * A dual tape's two logical roots both address the shared begin at relative
 * index zero. */
INLINE void tape_build_open(NdecBindMachine *m, TapeBuild *tb) {
  if (tb->start != BIND_AUX_NO_TAPE) return;
  tb->start                                     = (uint32_t)m->b.alloc.tape_used;
  tb->count                                     = 0;
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = TAPE_START_OBJECT;
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = TAPE_SEAM_RESERVED;
  tb->end                                       = (uint32_t)m->b.alloc.tape_used;
}

/* The classification cursor starts at the shared leading seam before the first
 * entry. Both views enter the entry body through it. */
#define PHASE2_FIRST_SEAM(start_) ((start_) + 1u)

/* Bridge an intervening arena gap in both views. Whenever end differs from
 * tape_used, end - 1 is the unconsumed seam guaranteed by open or entry_end.
 * View ownership diverges only in tape_build_drop. */
INLINE void tape_build_seam(NdecBindMachine *m, TapeBuild *tb) {
  uint32_t used = (uint32_t)m->b.alloc.tape_used;
  if (tb->end == used) return;
  uint32_t seam               = tb->end - 1;
  uint32_t dist               = used - seam;
  m->b.alloc.tape_arena[seam] = tape_seam_set(dist, dist);
  tb->end                     = used;
}

INLINE void tape_build_entry_end(NdecBindMachine *m, TapeBuild *tb) {
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = TAPE_SEAM_RESERVED;
  tb->end                                       = (uint32_t)m->b.alloc.tape_used;
  tb->count++;
}

/* Close the physical span and patch only A's root. B's count is unknown until
 * phase 2 classification and is published separately. The return value is the
 * exclusive end of the span beginning at tb->start. */
INLINE uint32_t tape_build_close(NdecBindMachine *m, TapeBuild *tb) {
  tape_build_seam(m, tb);
  uint32_t close               = (uint32_t)m->b.alloc.tape_used;
  m->b.alloc.tape_arena[close] = TAPE_END_OBJECT;
  m->b.alloc.tape_used         = close + 1;
  tb->end                      = close + 1;
  tape_build_patch_open(m, tb->start, tb->start, close, tb->count);
  return close + 1;
}

/* Remove one entry from one logical view by widening that view's preceding
 * seam to the reserved seam after the entry. Words and the other view remain
 * unchanged. Consecutive removals form a seam chain followed by the reader.
 * The caller updates the independently owned view count. */
INLINE void tape_build_drop(NdecBindMachine *m, uint32_t seam, uint32_t to, uint32_t shift) {
  uint64_t w                  = m->b.alloc.tape_arena[seam];
  uint32_t d                  = to - seam;
  uint32_t da                 = (shift == TAPE_VIEW_A) ? d : tape_seam_get(w, TAPE_VIEW_A);
  uint32_t db                 = (shift == TAPE_VIEW_B) ? d : tape_seam_get(w, TAPE_VIEW_B);
  m->b.alloc.tape_arena[seam] = tape_seam_set(da, db);
}

/* A second view is needed only when an inline variant and reserve-unknown are
 * both consumers. This static host property decides two-view classification,
 * dual count publication, and descriptor mode. Open and publish share this
 * predicate so they cannot disagree on the layout. */
INLINE int struct_needs_dual_view(const NdecBindMachine *m, const BindType *t) {
  const BindTypeMeta *sm = &m->b.ctx.type_meta[t->type_idx];
  return sm->u.strct.reserve_unknown_field_off != 0xFFFFFFFF && sm->u.strct.inline_variant_idx != 0xFFFFu;
}

/* After the MAY_PHASE2 screen, inline-variant and reserve-unknown hosts still
 * require phase 2 with no taped entries. This performs empty inline finalization
 * under selected-case semantics and installs an empty reserve-unknown Value. A
 * missing discriminator may leave the inline target nil. Other hosts need the
 * pass only when this depth owns delayed tape state. */
INLINE int struct_needs_phase2(const NdecBindMachine *m, const BindType *t, int owns_aux) {
  if (owns_aux) return 1;
  const BindTypeMeta *sm = &m->b.ctx.type_meta[t->type_idx];
  return sm->u.strct.reserve_unknown_field_off != 0xFFFFFFFF || sm->u.strct.inline_variant_idx != 0xFFFFu;
}

/* Materialize an empty merged tape when phase 2 is required without delayed
 * entries, keeping one walk shape for both empty and nonempty inputs. */
INLINE void tape_build_close_or_empty(NdecBindMachine *m, TapeBuild *tb) {
  tape_build_open(m, tb);
  tape_build_close(m, tb);
}

/* Scan view A for the inline discriminator and bind it before case selection.
 * Phase 1 keeps this entry on tape so a value.Value case can observe it, and its
 * input order is unrestricted.
 *
 * Presence is distinct from successful case selection because an explicit empty
 * string is present but names no case. Idempotence uses the per-parse seen flag,
 * never the caller-owned destination field, which may contain stale data. When
 * the key is absent the field remains untouched under normal Unmarshal semantics;
 * callers gate selection on seen so stale data cannot choose a case. */
INLINE int tape_build_bind_disc(NdecBindMachine *m, const BindType *host_type, uint16_t iv_idx, uint8_t *host,
                                uint32_t start, const uint8_t *src, uint8_t **str_pp, uint32_t *seen) {
  if (*seen) return 1;
  uint32_t disc_off       = m->b.ctx.variants[iv_idx].disc_off;
  uint64_t *arena         = m->b.alloc.tape_arena;
  const uint64_t *limit   = &arena[start + (uint32_t)(arena[start] & 0xFFFFFFFFu)];
  const ndec_lookup *lk   = (const ndec_lookup *)(uintptr_t)m->b.ctx.type_meta[host_type->type_idx].u.strct.lookup;
  const BindField *fields = (const BindField *)host_type->child;
  /* Host and case content is classified through view A. */
  TapeView tv       = tape_view(&arena[start], limit, TAPE_VIEW_A);
  const uint64_t *p = tape_seam_skip(&arena[start + 1], limit, TAPE_VIEW_A);
  while (p < limit) {
    uint32_t klen;
    const uint8_t *kd = tape_bind_string_ptr(*p, m->b.alloc.str_arena, src, &klen);
    int fi            = bind_lookup_key(lk, kd, klen);
    if (fi >= 0 && (fields[fi].flags & BIND_FF_VDISC) && BIND_FIELD_VARIANT_IDX(&fields[fi]) == iv_idx) {
      const BindField *disc_field = &fields[fi];
      uint64_t word               = p[1];
      uint8_t tag                 = (uint8_t)(word >> 56);
      if (!TAPE_IS_STRING_TAG(tag)) return -1;
      if (disc_field->flags & BIND_FF_QUOTED) {
        uint32_t qlen;
        const uint8_t *qd = tape_bind_string_ptr(word, m->b.alloc.str_arena, src, &qlen);
        if (bind_write_quoted_string(str_pp, qd, qlen, host + disc_off) < 0) return -1;
      } else {
        tape_bind_copy_string_header(word, str_pp, host + disc_off, m->b.alloc.str_arena, src);
      }
      *seen = 1;
      return 1;
    }
    p = tape_skip_value(p + 1, tv);
  }
  return 0;
}

/* Append an arena entry while rebasing container indices from the source tape
 * base to this merged tape's base. */
INLINE void tape_build_copy_entry(NdecBindMachine *m, TapeBuild *tb, uint64_t key_word, const uint64_t *val,
                                  uint32_t val_words, uint32_t val_src_off) {
  tape_build_seam(m, tb);
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = key_word;
  uint32_t dst_off                              = (uint32_t)m->b.alloc.tape_used - tb->start;
  tape_copy_subtree_rebase(val, &m->b.alloc.tape_arena[m->b.alloc.tape_used], val_words, val_src_off - dst_off);
  m->b.alloc.tape_used += val_words;
  tape_build_entry_end(m, tb);
}

/* Emit a compact empty object. A reserved seam between its root and end would
 * be read as content, so the merged-tape layout cannot represent this value.
 * The close index is one relative to the returned root. */
INLINE uint32_t tape_build_emit_empty_object(NdecBindMachine *m) {
  uint32_t start                                = (uint32_t)m->b.alloc.tape_used;
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = TAPE_START_OBJECT | (uint64_t)1; /* count 0, close at +1 */
  m->b.alloc.tape_arena[m->b.alloc.tape_used++] = TAPE_END_OBJECT;
  return start;
}

/* Always overwrite every Value coordinate because the caller-owned slot may
 * contain an unrelated prior Value. In particular, a stale mode makes a view B
 * Value interpret its seams incorrectly or move a root's count location.
 * Incremental vd construction writes the known coordinates first and patches
 * end at close; finished spans write all coordinates immediately. */
INLINE void value_begin_install(NdecBindMachine *m, uint8_t *target, uint32_t base, uint32_t tidx, uint32_t mode) {
  *(void **)(target + VALUE_DOC_OFF)    = m->b.alloc.value_doc;
  *(int32_t *)(target + VALUE_BASE_OFF) = (int32_t)base;
  *(int32_t *)(target + VALUE_TIDX_OFF) = (int32_t)tidx;
  *(int32_t *)(target + VALUE_MODE_OFF) = (int32_t)mode;
}

/* Install [start, end) using the shared value_doc. tidx addresses a root tag
 * directly; both dual logical roots use zero because they share the begin
 * word. Both views share the same base-relative end. */
INLINE void value_install_tape(NdecBindMachine *m, uint8_t *target, uint32_t start, uint32_t end, uint32_t tidx,
                               uint32_t mode) {
  value_begin_install(m, target, start, tidx, mode);
  *(int32_t *)(target + VALUE_END_OFF) = (int32_t)(end - start);
}

/* Poly binding selects a case, acquires its storage, and publishes the eface.
 * A field binds immediately when its discriminator or JSON kind is sufficient.
 * An unbound discriminator or cold case defers the value to the merged tape and
 * re-derives the decision at struct close, so no case state crosses fields.
 * JSON and tape paths share the variant and kindof selectors and table layout. */

/* A negative case_idx means selection failed. */
typedef struct PolyCase {
  int32_t case_idx;
  uint16_t case_type_idx;
  int32_t slot_class;
  const void *rtype; /* Runtime type word for the target eface. */
} PolyCase;

/* Kindof tables use bool, number, string, array, and object as indices zero
 * through four. A negative result is not a recognizable JSON value start. */
INLINE int poly_kind_of_tape_tag(uint8_t tag) {
  switch (tag) {
  case 't':
  case 'f':
    return 0;
  case 'l':
  case 'u':
  case 'd':
  case 'D': /* number kept as source text; still a JSON number */
    return 1;
  case '"':
  case 'R':
  case 'S': /* arena-backed escape-free string; still a JSON string */
    return 2;
  case '[':
    return 3;
  case '{':
    return 4;
  }
  return -1;
}

INLINE int poly_kind_of_json_char(uint8_t ch) {
  switch (ch) {
  case 't':
  case 'f':
    return 0;
  case '-':
  case '0':
  case '1':
  case '2':
  case '3':
  case '4':
  case '5':
  case '6':
  case '7':
  case '8':
  case '9':
    return 1;
  case '"':
    return 2;
  case '[':
    return 3;
  case '{':
    return 4;
  }
  return -1;
}

INLINE PolyCase poly_case_by_kindof(const NdecBindMachine *m, uint16_t poly_idx, int json_kind) {
  PolyCase pc               = {-1, 0, 0, NULL};
  const BindKindofTable *ot = &m->b.ctx.kindofs[poly_idx];
  if (json_kind < 0) return pc;
  int32_t ci = ot->case_idx_by_kind[json_kind];
  if (ci < 0) return pc;
  pc.case_idx      = ci;
  pc.case_type_idx = ot->case_type_idx[ci];
  pc.slot_class    = ot->case_slot_class[ci];
  pc.rtype         = ot->case_rtype[ci];
  return pc;
}

/* Select from the host's bound Go string discriminator. A null pointer returns
 * no case with disc_bound false; it may mean a later input field, so it must not
 * choose the default early. A present value that is empty, too long for the
 * lookup tier, or unmatched may choose the declared default. NOINLINE keeps
 * this cold selector out of inlined field dispatch. */
NOINLINE static PolyCase poly_case_by_disc(const NdecBindMachine *m, uint16_t poly_idx, const uint8_t *host,
                                           const uint8_t *str_p, int *disc_bound) {
  PolyCase pc                = {-1, 0, 0, NULL};
  const BindVariantTable *vt = &m->b.ctx.variants[poly_idx];
  const uint8_t *disc_ptr    = *(const uint8_t *const *)(host + vt->disc_off);
  uint64_t disc_len          = *(const uint64_t *)(host + vt->disc_off + 8);
  uintptr_t dp               = (uintptr_t)disc_ptr;
  uintptr_t begin            = (uintptr_t)m->b.alloc.str_arena + m->b.alloc.str_gen_start;
  uintptr_t end              = (uintptr_t)str_p;
  *disc_bound                = dp >= begin && dp < end;
  /* Only storage published by this root parse may drive case selection. */
  if (!*disc_bound) return pc;
  int ci = -1;
  if (disc_len > 0 && disc_len <= 63 && vt->lookup != 0) {
    ndec_lookup_key key = {(const char *)disc_ptr, (size_t)disc_len};
    ci                  = ndec_lookup_find((const ndec_lookup *)vt->lookup, key);
  }
  if (ci < 0) {
    /* Only a present but unmatched value may select the default. */
    if (vt->default_case_idx == 0xFFFFu) return pc;
    ci = (int)vt->default_case_idx;
  }
  pc.case_idx      = ci;
  pc.case_type_idx = vt->case_type_idx[ci];
  pc.slot_class    = vt->case_slot_class[ci];
  pc.rtype         = vt->case_rtype[ci];
  return pc;
}

/* Decide whether a poly field binds now, defers, or has no case. A variant may
 * defer until its discriminator is bound. Kindof already knows its final input
 * kind, so an unregistered kind fails without building tape. Cold cases always
 * defer because only tape bind can construct them. Phase 2 re-derives the pure
 * selection instead of carrying case state across fields. */
enum {
  POLY_SITE_BIND = 0,
  POLY_SITE_DEFER,
  POLY_SITE_NO_CASE,
};

INLINE int poly_case_site(const NdecBindMachine *m, const BindField *f, const uint8_t *host, const uint8_t *str_p,
                          int json_kind) {
  uint16_t poly_idx = BIND_FIELD_VARIANT_IDX(f);
  PolyCase pc;
  if (f->flags & BIND_FF_KINDOF) {
    /* Defer an invalid value start to the tape path, the single syntax-error site. */
    if (json_kind < 0) return POLY_SITE_DEFER;
    pc = poly_case_by_kindof(m, poly_idx, json_kind);
    if (pc.case_idx < 0) return POLY_SITE_NO_CASE;
  } else {
    int disc_bound;
    pc = poly_case_by_disc(m, poly_idx, host, str_p, &disc_bound);
    if (pc.case_idx < 0) return POLY_SITE_DEFER;
  }
  return (m->b.ctx.types[pc.case_type_idx].flags & BIND_FLAG_COLD) ? POLY_SITE_DEFER : POLY_SITE_BIND;
}

/* Pointer and map cases occupy the eface data word directly. Other kinds use a
 * pointer to copied storage. Confusing the forms corrupts type assertions. */
INLINE int poly_eface_is_direct(uint8_t case_kind) {
  return case_kind == BIND_KIND_PTR || case_kind == BIND_KIND_MAP;
}

INLINE void poly_eface_nil(uint8_t *target) {
  *(const void **)target = NULL;
  *(void **)(target + 8) = NULL;
}

/* Prepare the selected case's eface and return its decode target. PTR and MAP
 * decode directly into the data word; value kinds decode into SlotClass storage
 * referenced by that word. Data becomes nil or a valid pointer before the type
 * is published, so GC never observes a typed eface with garbage data. The caller
 * must check poly_case_slot_full and yield BLOCK_FULL before a value allocation. */
INLINE uint8_t *poly_bind_target(NdecBindMachine *m, uint8_t *target, const PolyCase *pc) {
  uint8_t case_kind = m->b.ctx.types[pc->case_type_idx].kind;
  if (poly_eface_is_direct(case_kind)) {
    *(void **)(target + 8) = NULL; /* descent writes the pointer here */
    *(const void **)target = pc->rtype;
    return target + 8;
  }
  BindSlotClass *sc = &m->b.alloc.slot_classes[pc->slot_class];
  uint8_t *slot     = sc->block + sc->offset;
  sc->offset += sc->elem_size;
  *(void **)(target + 8) = slot;
  *(const void **)target = pc->rtype;
  return slot;
}

INLINE int poly_case_slot_full(const NdecBindMachine *m, const PolyCase *pc) {
  if (poly_eface_is_direct(m->b.ctx.types[pc->case_type_idx].kind)) return 0;
  const BindSlotClass *sc = &m->b.alloc.slot_classes[pc->slot_class];
  return sc->offset >= sc->limit;
}

/* Report whether the selected case claims this key. Struct cases consult their
 * field table; non-struct cases consume the whole object and claim every key.
 * The caller combines this result with sink ownership to classify the entry. */
INLINE int poly_case_declares(const NdecBindMachine *m, const PolyCase *pc, const uint8_t *kdata, uint32_t klen) {
  const BindType *ct = &m->b.ctx.types[pc->case_type_idx];
  if (ct->kind != BIND_KIND_STRUCT) return 1;
  const ndec_lookup *lk = (const ndec_lookup *)(uintptr_t)m->b.ctx.type_meta[ct->type_idx].u.strct.lookup;
  return bind_lookup_key(lk, kdata, klen) >= 0;
}

/* When the host has no sink, a selected struct case's sink owns the leftovers.
 * They must stay in view A for that case's phase 2 instead of being removed by
 * the host. Non-struct cases have no leftover-field distinction. */
INLINE int poly_case_has_sink(const NdecBindMachine *m, const PolyCase *pc) {
  const BindType *ct = &m->b.ctx.types[pc->case_type_idx];
  if (ct->kind != BIND_KIND_STRUCT) return 0;
  return m->b.ctx.type_meta[ct->type_idx].u.strct.reserve_unknown_field_off != 0xFFFFFFFF;
}

#endif /* NDEC_MACHINE_H */
