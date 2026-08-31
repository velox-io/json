/* This state machine binds either JSON structural indices or a prebuilt Value
 * tape into a typed Go destination. Both walks share container state and may
 * yield to Go for allocation or draining before resuming at a phase-specific
 * label. */

#ifndef NDEC_BIND_H
#define NDEC_BIND_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/bind_bridge.h"
#include "ndec/core/extract.h"
#include "ndec/core/str.h"
#include "ndec/core/number.h"
#include "vlib/lookup.h"

#include "machine.h"
#include "string.h"
#include "primitive.h"
#include "cursor.h"
#include "value.h"

#include "util/log.h" // IWYU pragma: keep

NOINLINE static void ndec_bind_parse_inner(NdecBindMachine *);

#ifndef NDEC_FN_DECL
#define NDEC_FN_DECL
#endif

NDEC_FN_DECL void ndec_bind_parse(void *_m_) {
  NdecBindMachine *m = (NdecBindMachine *)_m_;

  if (UNLIKELY(m->c.phase == BIND_PHASE_ROOT)) {
    /* Running the PHASE_ROOT scan in this wrapper separates scanner and
     * state-machine spill frames, reducing the maximum native stack depth. */
    uint32_t n_idx = 0;
    int size_tape  = (m->b.ctx.opt_flags & BIND_OPT_SIZE_TAPE) != 0;
    int scan_err;
    if (size_tape) {
      /* Count tape words only when the destination can emit tape content. */
      NdecPlanePop pop = {0, 0, 0};
      if (m->b.ctx.opt_flags & BIND_OPT_STRICT_SCAN) {
        scan_err =
            ndec_scan_structurals_strict_counted(m->b.ctx.src, m->b.ctx.src_len, (uint32_t *)m->b.alloc.structural,
                                                 &n_idx, m->b.alloc.structural_cap, &pop);
      } else {
        scan_err = ndec_scan_structurals_counted(m->b.ctx.src, m->b.ctx.src_len, (uint32_t *)m->b.alloc.structural,
                                                 &n_idx, m->b.alloc.structural_cap, &pop);
      }
      if (!scan_err) {
        uint32_t dual      = (m->b.ctx.opt_flags & BIND_OPT_TAPE_DUAL) != 0;
        uint32_t by_tokens = ndec_scan_tape_words(pop, dual);
        /* Keep the tighter valid bound so arena growth cannot yield twice. */
        uint32_t ceiling     = m->b.alloc.tape_need;
        m->b.alloc.tape_need = (ceiling && ceiling < by_tokens) ? ceiling : by_tokens;
      }
    } else {
      if (m->b.ctx.opt_flags & BIND_OPT_STRICT_SCAN) {
        scan_err = ndec_scan_structurals_strict(m->b.ctx.src, m->b.ctx.src_len, (uint32_t *)m->b.alloc.structural,
                                                &n_idx, m->b.alloc.structural_cap);
      } else {
        scan_err = ndec_scan_structurals(m->b.ctx.src, m->b.ctx.src_len, (uint32_t *)m->b.alloc.structural, &n_idx,
                                         m->b.alloc.structural_cap);
      }
    }
    if (scan_err) {
      m->b.yield.pending_action = BIND_YIELD_ERROR;
      BIND_ERROR_PAYLOAD(m, BIND_ERR_SYNTAX, 0, BIND_ERROR_NO_POS, NULL);
      m->c.phase = BIND_PHASE_DOCUMENT_END;
      return;
    }
    m->cursor_end.idx          = m->b.alloc.structural + n_idx;
    m->cursor.idx              = m->b.alloc.structural;
    m->c.depth                 = 0;
    m->c.cur_dst               = m->b.ctx.root_dst;
    m->c.cur_type              = m->b.ctx.types[m->b.ctx.root_type];
    m->c.cur_count             = 0;
    m->c.cur_aux               = NULL;
    m->c.first_error_kind      = 0;
    m->b.yield.first_error_pos = BIND_ERROR_NO_POS;
    /* Reset both auxiliary stacks. Live aux slots initialize lazily, so only
     * the sentinel must be restored after an earlier failed parse. */
    m->rebind_top               = 0;
    m->tape_bind_base_depth     = 0;
    m->aux_depth                = 0;
    m->auxFrames[0].owner_depth = -1;
    m->in_tape_bind             = 0;
    /* JSON binding reads source bytes. Only nested phase2 walks select a tape view. */
    m->tape_view_mode = TAPE_VIEW_A;

    /* Yield only after the root state is complete and before any tape write.
     * Go may replace the tape arena, and ROOT_SCANNED resumes without rescanning. */
    if (size_tape && m->b.alloc.tape_need > m->b.alloc.tape_arena_cap) {
      m->c.phase                = BIND_PHASE_ROOT_SCANNED;
      m->b.yield.pending_action = BIND_YIELD_TAPE_ARENA;
      m->b.yield.arg0           = 0;
      m->b.yield.arg1           = 0;
      m->b.yield.target         = NULL;
      return;
    }
  } else if (m->c.phase == BIND_PHASE_TAPE_BIND_ROOT) {
    /* A tape root borrows a Value tape and appends interned strings after the
     * Value's existing arena content. Its auxiliary stacks start fresh. */
    m->c.depth                  = 0;
    m->c.cur_dst                = m->b.ctx.root_dst;
    m->c.cur_type               = m->b.ctx.types[m->b.ctx.root_type];
    m->c.cur_count              = 0;
    m->c.cur_aux                = NULL;
    m->c.first_error_kind       = 0;
    m->b.yield.first_error_pos  = BIND_ERROR_NO_POS;
    m->rebind_top               = 0;
    m->tape_bind_base_depth     = 0;
    m->aux_depth                = 0;
    m->auxFrames[0].owner_depth = -1;
    m->in_tape_bind             = 1;
    /* Preserve the caller's Value view. A reserve-unknown Value may expose view B
     * over words shared with inline-case content in view A. */
    m->tape_view_mode = m->b.ctx.root_view_mode;
  }

  ndec_bind_parse_inner(m);
}

NOINLINE static void ndec_bind_parse_inner(NdecBindMachine *m) {
  /* cursor is the sole register-resident position for a pass. Resume phases load
   * it from m->cursor once. JSON phases read its idx member, tape phases its tape
   * member, and the phase gate keeps these views mutually exclusive. */
  NdecCursor cursor  = m->cursor;
  const uint8_t *src = m->b.ctx.src;

  BindFrame *frames                 = m->c.frames;
  const BindType *types             = m->b.ctx.types;
  int32_t depth                     = m->c.depth;
  uint8_t *cur_dst                  = m->c.cur_dst;
  BindType cur_type                 = m->c.cur_type;
  uint32_t cur_count                = m->c.cur_count;
  void *cur_aux                     = m->c.cur_aux;
  uint8_t *str_p                    = (uint8_t *)((uintptr_t)m->b.alloc.str_arena + m->c.str_used);
  const BindField *cur_struct_field = (const BindField *)0;

  switch (m->c.phase) {
  case BIND_PHASE_ROOT:
    goto document_start;
  case BIND_PHASE_ROOT_SCANNED:
    goto document_start;
  case BIND_PHASE_ARRAY_VALUE:
    goto array_value;
  case BIND_PHASE_ARRAY_VALUE_BEGIN:
    /* A stream handler may set STREAM_SKIP while this element yield is serviced.
     * Check it only on this resume edge before entering the unconsumed element. */
    if (UNLIKELY(cur_type.flags & BIND_FLAG_STREAM_SKIP)) goto safe_skip_value;
    goto array_value_bind_body;
  case BIND_PHASE_OBJECT_FIELD_VALUE:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto object_field_value;
  case BIND_PHASE_MAP_CONTINUE:
    goto map_continue_resume;
  case BIND_PHASE_MAP_OPEN_RETRY:
    goto map_open;
  case BIND_PHASE_MAP_VALUE:
    goto map_value;
  case BIND_PHASE_DOCUMENT_END:
    goto document_end;
  case BIND_PHASE_ANY_RESUME:
    goto any_value;
  case BIND_PHASE_DEFERRED_RESUME:
    goto deferred_value;
  case BIND_PHASE_VALUE_RESUME:
    goto vd_resume;
  case BIND_PHASE_RESERVE_UNKNOWN_VALUE_RESUME:
    goto vd_resume;
  case BIND_PHASE_ARRAY_CLOSE:
    goto array_close;
  case BIND_PHASE_ROOT_UNWRAP:
    goto root_ptr_unwrap;
  case BIND_PHASE_VARIANT_REBIND_RESUME:
    goto variant_rebind_resume;
  case BIND_PHASE_VARIANT_INLINE_RESUME:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto poly_field_bind;
  case BIND_PHASE_TAPE_BIND_ROOT:
    goto t_document_start;
  case BIND_PHASE_TAPE_BIND_ARRAY_VALUE:
    goto t_array_value;
  case BIND_PHASE_TAPE_BIND_OBJECT_FIELD_VALUE:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto t_object_field_value;
  case BIND_PHASE_TAPE_BIND_FIELD_VALUE_PTR_RESUME:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto t_field_value_ptr_resume;
  case BIND_PHASE_TAPE_BIND_MAP_CONTINUE:
    goto t_map_key_resume;
  case BIND_PHASE_TAPE_BIND_MAP_OPEN_RETRY:
    goto t_map_open;
  case BIND_PHASE_TAPE_BIND_MAP_VALUE:
    goto t_map_value;
  case BIND_PHASE_TAPE_BIND_ROOT_UNWRAP:
    goto t_root_ptr_unwrap;
  case BIND_PHASE_TAPE_BIND_VALUE_RESUME_OBJECT:
    goto t_value_resume_object;
  case BIND_PHASE_TAPE_BIND_VALUE_RESUME_ARRAY:
    goto t_value_resume_array;
  case BIND_PHASE_TAPE_BIND_VALUE_RESUME_MAP:
    goto t_value_resume_map;
  case BIND_PHASE_TAPE_BIND_VALUE_RESUME_ROOT:
    goto t_value_resume_root;
  case BIND_PHASE_TAPE_BIND_ANY_RESUME:
    goto t_any_value;
  case BIND_PHASE_TAPE_BIND_FIELD_VALUE_CASE_RETRY:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto t_field_value_case_lookup;
  case BIND_PHASE_TAPE_BIND_CLOSE_DRAIN_RETRY:
    /* Classification and publication are complete. Retry only case binding. */
    goto phase2_case_bind;
  case BIND_PHASE_PHASE2_POLY_RETRY:
    cur_struct_field = (const BindField *)m->c.stash.field_value.field;
    goto phase2_poly_bind;
  default:
    goto document_start;
  }

document_start: {
  if (UNLIKELY(SRC_EOF())) {
    BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
  }
  uint8_t ch = SRC_PEEK();
  if (UNLIKELY(cur_type.flags | (ch == 'n'))) {
    /* cur_type advances after each pointer allocation, so BLOCK_FULL resumes at
     * the first unresolved layer without advancing the JSON cursor. */
    if (cur_type.kind == BIND_KIND_PTR && ch != 'n') {
    root_ptr_unwrap:
      while (cur_type.kind == BIND_KIND_PTR) {
        uint8_t *pointee = *(uint8_t **)cur_dst;
        if (pointee == NULL) {
          int32_t ci        = cur_type.u.ptr.alloc_class;
          BindSlotClass *sc = &m->b.alloc.slot_classes[ci];
          if (sc->offset >= sc->limit) {
            __BIND_SAVE_LOCALS(m);
            m->c.phase                = BIND_PHASE_ROOT_UNWRAP;
            m->b.yield.pending_action = BIND_YIELD_BLOCK_FULL;
            m->b.yield.arg0           = (uint32_t)ci;
            m->b.yield.arg1           = 0;
            m->b.yield.target         = cur_dst;
            return;
          }
          pointee = sc->block + sc->offset;
          sc->offset += sc->elem_size;
          *(uint8_t **)cur_dst = pointee;
        }
        cur_dst  = pointee;
        cur_type = *(const BindType *)cur_type.child;
      }

      /* Resume enters below ch initialization, so reload the unconsumed token. */
      ch = SRC_PEEK();
    }
    if (BIND_IS_ANY(cur_type.kind)) {
      m->c.stash.any_yield.slot = cur_dst;
      goto any_value;
    }
    /* Deferred dispatch precedes container and null handling so callbacks receive
     * the complete raw value, including a literal null. */
    if (BIND_IS_VALUE(cur_type.kind)) {
      BIND_DISPATCH_VALUE(cur_dst);
    }
    if (BIND_IS_DEFERRED_VALUE(cur_type.kind)) {
      m->c.stash.deferred_yield.slot = cur_dst;
      m->c.stash.deferred_yield.type = (BindType *)&cur_type;
      if (m->b.alloc.deferred_drain_used + sizeof(UnmarshalRecord) > m->b.alloc.deferred_drain_cap) {
        __BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DEFERRED_RESUME;
        m->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;
        return;
      }
      goto deferred_value;
    }
    /* Null clears reference-like roots and Number storage. Scalars and structs
     * retain their value, and an outer pointer still names its original slot. */
    if (ch == 'n') {
      if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      SRC_ADVANCE();
      BIND_NULL_ZERO(cur_dst, m, &cur_type);
      goto document_end;
    }
  }
  if (ch == '{') {
    if (cur_type.kind == BIND_KIND_MAP) {
      BIND_MAP_OPEN(m, &cur_type, cur_dst, bind_push_map);
    }
    SRC_ADVANCE();
    if (cur_type.kind != BIND_KIND_STRUCT) {
      BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS() - 1);
    }
    /* Even an empty object must enter phase2 when the type can owe deferred
     * inline-variant or reserve-unknown work. */
    int empty = SRC_ACCEPT('}');
    if (empty && LIKELY(!(cur_type.flags & BIND_FLAG_MAY_PHASE2))) goto document_end;
    if (bind_push_struct(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);

    if (empty) goto object_close;
    goto object_begin;
  }
  if (ch == '[') {
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      /* Stream empty array at root: yield to activate the handler with an
       * empty batch, then resume at array_close to finish document_end. */
      if (cur_type.kind == BIND_KIND_STREAM) {
        BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)cur_type.type_idx, 0, BIND_PHASE_ARRAY_CLOSE);
      }
      if (BIND_IS_SLICE_LIKE(cur_type.kind)) BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      goto document_end;
    }
    if (bind_push_array_or_slice(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_count = 0;
    goto array_begin;
  }
  goto root_scalar;
}

object_begin:
  cur_aux = (void *)(uintptr_t)m->b.ctx.type_meta[cur_type.type_idx].u.strct.lookup;
object_field: {
  const uint8_t *key = SRC_ADVANCE_PTR();
  if (UNLIKELY(*key != '"')) BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_SYNTAX, (uint32_t)(key - src));
  const BindType *t            = &cur_type;
  const BindField *first_field = (const BindField *)t->child;
  int fidx;
  {
    /* Fieldless structs use an empty lookup. Escaped keys are decoded into
     * str_arena, while unescaped keys borrow source bytes. */
    cur_struct_field      = (const BindField *)NULL;
    const ndec_lookup *lk = (const ndec_lookup *)cur_aux;
    uint32_t klen, bp;
    int32_t st = ndec_str_parse_zc_scan(key + 1, &klen, &bp);
    if (LIKELY(st == 1)) {
      ndec_lookup_key lkey = {(const char *)(key + 1), (size_t)klen};
      fidx                 = ndec_lookup_find(lk, lkey);
    } else if (st == 2) {
      const uint8_t *kd;
      uint32_t kl;
      if (bind_intern_key_for_lookup(str_p, key, &kd, &kl) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (key - src));
      ndec_lookup_key lkey = {(const char *)kd, (size_t)kl};
      fidx                 = ndec_lookup_find(lk, lkey);
    } else {
      BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(key - src));
    }
  }
  if (UNLIKELY(fidx < 0)) {
    /* All miss paths consume ':' before routing or skipping the value. */
    uint16_t iv_idx              = m->b.ctx.type_meta[cur_type.type_idx].u.strct.inline_variant_idx;
    uint32_t reserve_unknown_off = m->b.ctx.type_meta[cur_type.type_idx].u.strct.reserve_unknown_field_off;
    if ((iv_idx != 0xFFFFu) || (reserve_unknown_off != 0xFFFFFFFF)) {
      SRC_EXPECT(':');
      goto object_field_tape;
    }
    if (UNLIKELY(m->b.ctx.opt_flags & BIND_OPT_DISALLOW_UNKNOWN))
      BIND_YIELD_ERR(m, BIND_ERR_UNKNOWN_FIELD, (key - src));
    SRC_EXPECT(':');
    goto skip_value;
  }
  cur_struct_field = &first_field[fidx];
  SRC_EXPECT(':');
  /* Resume restores cur_struct_field and enters after the consumed colon.
   * cur_type remains the parent struct throughout field iteration and phase2. */
object_field_value: {
  uint8_t *body              = cur_dst + cur_struct_field->offset;
  const BindType *child_type = (const BindType *)cur_struct_field->type;
  uint8_t ch                 = SRC_PEEK();
  if (UNLIKELY(cur_struct_field->flags | (ch == 'n'))) {
    /* Resolve promoted pointer hops before null handling. The field offset is
     * relative to the reached pointee even when the field value is null. */
    if (cur_struct_field->flags & BIND_FF_VIA_PTR) {
      uint8_t *field_base = cur_dst;
      uintptr_t hops      = m->b.ctx.type_meta[cur_type.type_idx].u.strct.ptr_hops;
      BIND_RESOLVE_FIELD_HOPS(m, field_base, cur_struct_field, hops, BIND_PHASE_OBJECT_FIELD_VALUE);
      body = field_base + cur_struct_field->offset;
    }

    if (cur_struct_field->flags & (BIND_FF_RESERVE_UNKNOWN | BIND_FF_INLINE_VARIANT | BIND_FF_INLINE_VDISC))
      goto object_field_tape;

    /* This pointer loop stashes cur_struct_field in the field-resume shape. */
    if (ch != 'n') {
      int _depth = 0;
      while (child_type->kind == BIND_KIND_PTR) {
        if (UNLIKELY(++_depth > 32)) BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
        uint8_t *pointee = *(uint8_t **)body;
        if (pointee == NULL) {
          int32_t ci        = child_type->u.ptr.alloc_class;
          BindSlotClass *sc = &m->b.alloc.slot_classes[ci];
          if (sc->offset >= sc->limit) {
            m->c.stash.field_value.field = (uint8_t *)cur_struct_field;
            BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, ci, 0, BIND_PHASE_OBJECT_FIELD_VALUE);
          }
          pointee = sc->block + sc->offset;
          sc->offset += sc->elem_size;
          *(uint8_t **)body = pointee;
        }
        body       = pointee;
        child_type = (const BindType *)child_type->child;
      }
    }

    /* Bind kindof immediately from the JSON kind, and bind a variant once its
     * discriminator is available. Otherwise preserve the entry on the merged
     * tape for phase2 at struct close. */
    if (cur_struct_field->flags & (BIND_FF_VARIANT | BIND_FF_KINDOF)) {
      if (ch == 'n') {
        if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
        SRC_ADVANCE();
        poly_eface_nil(body);
        goto object_continue;
      }
      /* Decide deferral while key remains in scope for merged-tape emission. */
      int kind = poly_kind_of_json_char(ch);
      int site = poly_case_site(m, cur_struct_field, cur_dst, str_p, kind);
      if (site == POLY_SITE_NO_CASE) {
        __BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DOCUMENT_END;
        m->b.yield.pending_action = BIND_YIELD_ERROR;
        BIND_ERROR_PAYLOAD(m, BIND_ERR_KINDOF_UNREGISTERED, kind, SRC_POS(), NULL);
        return;
      }
      if (site == POLY_SITE_DEFER) {
        AUX_LAZY_ALLOC(m, BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0));
        goto object_field_tape;
      }
      goto poly_field_bind;
    }

    /* Direct Value output may split a surrounding merged tape. The next merged
     * write detects the arena gap and spans it by widening the standing seam. */
    if (BIND_IS_ANY(child_type->kind)) {
      BIND_DISPATCH_ANY(child_type, body);
    }
    if (BIND_IS_VALUE(child_type->kind)) {
      BIND_DISPATCH_VALUE(body);
    }
    if (BIND_IS_DEFERRED_VALUE(child_type->kind)) {
      m->c.stash.deferred_yield.slot = (uint8_t *)body;
      m->c.stash.deferred_yield.type = (BindType *)child_type;
      if (m->b.alloc.deferred_drain_used + sizeof(UnmarshalRecord) > m->b.alloc.deferred_drain_cap) {
        __BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DEFERRED_RESUME;
        m->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;
        return;
      }
      goto deferred_value;
    }

    if (ch == 'n') {
      if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      SRC_ADVANCE();
      BIND_NULL_ZERO(body, m, child_type);
      goto object_continue;
    }

    /* `,string` accepts only a JSON string and reparses its content as the target scalar. */
    if (cur_struct_field->flags & BIND_FF_QUOTED) {
      if (ch == '"') {
        const uint8_t *qd = str_p;
        int32_t qn        = ndec_str_parse(SRC_PTR() + 1, str_p, NULL);
        if (qn < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
        if (bind_write_quoted_scalar(&str_p, qd, (uint32_t)qn, child_type->kind, body, m->c.atof) < 0)
          BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
        SRC_ADVANCE();
        goto object_continue;
      }
      BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    }
  }

  BIND_DISPATCH_STRING(child_type, body, ch, object_continue, BIND_TYPE_MISMATCH_SKIP(m, SRC_POS()));
  switch (child_type->kind) {
    BIND_VALUE_SWITCH_COMMON(child_type, body, ch, object_continue, 0, BIND_TYPE_MISMATCH_SKIP(m, SRC_POS()),
                             bind_push_struct);
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY:
  case BIND_KIND_STREAM:
    if (ch != '[') BIND_TYPE_MISMATCH_SKIP(m, SRC_POS());

    if (bind_push_struct(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);

    cur_dst   = body;
    cur_type  = *child_type;
    cur_count = 0;
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      /* Activate an empty stream handler before resuming at array_close. */
      if (cur_type.kind == BIND_KIND_STREAM) {
        BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)cur_type.type_idx, 0, BIND_PHASE_ARRAY_CLOSE);
      }
      BIND_EMPTY_ARRAY_CLOSE(m, &cur_type, object_continue, bind_pop_struct);
    }
    goto array_begin;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

/* Phase1 writes every field that depends on an unresolved inline case to one
 * merged tape. This includes host misses, reserve-unknown carriers, and inline
 * discriminators. Phase2 classifies them after the discriminator is bound.
 * key remains in object_field scope, and the colon is already consumed. */
object_field_tape: {
  AUX_LAZY_ALLOC(m, BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0));
  TapeBuild *a = &m->auxFrames[m->aux_depth].a;
  tape_build_open(m, a);
  tape_build_seam(m, a);
  uint64_t *tp = &m->b.alloc.tape_arena[m->b.alloc.tape_used];
  if (bind_emit_string_copy(m->b.alloc.str_arena, &str_p, &tp, key))
    BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(key - src));
  m->b.alloc.tape_used = (uint32_t)(tp - m->b.alloc.tape_arena);
  goto vd_dispatch_unknown_value;
}
}

object_continue: {
  uint8_t ch = SRC_ADVANCE_CHAR();
  if (ch == ',') goto object_field;
  /* Phase 2 handles delayed entries or empty close-time finalization. */
  if (ch == '}') {
  object_close:
    if (LIKELY(!(cur_type.flags & BIND_FLAG_MAY_PHASE2)) ||
        !struct_needs_phase2(m, &cur_type, m->auxFrames[m->aux_depth].owner_depth == depth)) {
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      if (depth == 0) goto document_end;
      goto scope_end;
    }
    AUX_LAZY_ALLOC(m, BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0));
    BindAuxFrame *ax = &m->auxFrames[m->aux_depth];
    tape_build_close_or_empty(m, &ax->a);
    ax->walk    = PHASE2_FIRST_SEAM(ax->a.start);
    ax->b_count = ax->a.count; /* both views initially contain every entry */
    goto phase2_walk;
  }
  /* 0x20 is the scan sentinel byte at src[len]. At depth 0 it means the
   * top-level value ended cleanly; at depth > 0 the object is unclosed. */
  if (ch == 0x20) {
    if (UNLIKELY(depth > 0)) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
    goto document_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

/* Phase2 classifies the merged tape after the discriminator is bound. The host
 * lookup identifies deferred poly fields, discriminator or selected-case content,
 * and unknown content. View A serves the inline case. When reserve-unknown and an
 * inline case coexist, view B serves the sink over the same words through the
 * second distance in each seam.
 *
 * ax->walk always names the seam before the next unclassified entry. It advances
 * before any descent or allocation yield, so resume cannot consume an entry twice. */
phase2_walk: {
  BindAuxFrame *ax             = &m->auxFrames[m->aux_depth];
  const BindTypeMeta *hm       = &m->b.ctx.type_meta[cur_type.type_idx];
  uint16_t iv_idx              = hm->u.strct.inline_variant_idx;
  uint32_t reserve_unknown_off = hm->u.strct.reserve_unknown_field_off;
  uint64_t *arena              = m->b.alloc.tape_arena;
  const uint64_t *limit        = &arena[ax->a.start + (uint32_t)(arena[ax->a.start] & 0xFFFFFFFFu)];

  int dual = struct_needs_dual_view(m, &cur_type);

  /* Bind the taped discriminator before selecting the case. It remains on the
   * merged tape initially so a Value case can observe the complete object. */
  PolyCase host_case = {-1, 0, 0, NULL};
  /* A case sink receives leftovers only when the shallower host has no sink. */
  int case_has_sink = 0;
  if (iv_idx != 0xFFFFu) {
    /* disc_seen records both presence and completed binding because the walk later
     * drops the discriminator and allocation resume must not bind it again. */
    if (tape_build_bind_disc(m, &cur_type, iv_idx, cur_dst, ax->a.start, src, &str_p, &ax->disc_seen) < 0)
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_TYPE_MISMATCH, 0);
    int disc_bound;
    host_case = poly_case_by_disc(m, iv_idx, cur_dst, str_p, &disc_bound);
    /* An absent discriminator selects no case and leaves the eface nil. A present
     * discriminator must resolve, including an explicitly empty value. */
    if (host_case.case_idx < 0 && ax->disc_seen) {
      __BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_DOCUMENT_END;
      m->b.yield.pending_action = BIND_YIELD_ERROR;
      BIND_ERROR_PAYLOAD(m, disc_bound ? BIND_ERR_VARIANT_UNKNOWN_DISC : BIND_ERR_VARIANT_MISSING_DISC, iv_idx,
                         BIND_ERROR_NO_POS, cur_dst);
      return;
    }
    case_has_sink = poly_case_has_sink(m, &host_case);
  }

  TapeView tv = tape_view(&arena[ax->a.start], limit, TAPE_VIEW_A);

  /* Widening one seam distance removes the entry from that view without moving
   * its words. An entry may remain in one view or leave both. */
#define DROP_A()                                                                                                  \
  do {                                                                                                            \
    tape_build_drop(m, seam, val_end, TAPE_VIEW_A);                                                               \
    ax->a.count--;                                                                                                \
  } while (0)
#define DROP_B()                                                                                                  \
  do {                                                                                                            \
    if (dual) {                                                                                                   \
      tape_build_drop(m, seam, val_end, TAPE_VIEW_B);                                                             \
      ax->b_count--;                                                                                              \
    }                                                                                                             \
  } while (0)

  while (1) {
    uint32_t seam      = ax->walk;
    const uint64_t *kp = tape_seam_skip(&arena[seam], limit, TAPE_VIEW_A);
    if (kp >= limit) break;
    uint32_t key_idx = (uint32_t)(kp - arena);
    uint32_t val_end = (uint32_t)(tape_value_end(kp + 1, tv) - arena);
    ax->walk         = val_end; /* advance before any operation that may yield */

    uint32_t klen;
    const uint8_t *kdata = tape_bind_string_ptr(arena[key_idx], m->b.alloc.str_arena, src, &klen);
    int fidx             = bind_lookup_key((const ndec_lookup *)(uintptr_t)hm->u.strct.lookup, kdata, klen);
    const BindField *f   = fidx < 0 ? (const BindField *)NULL : &((const BindField *)cur_type.child)[fidx];

    if (f != NULL && (f->flags & (BIND_FF_VARIANT | BIND_FF_KINDOF))) {
      /* A deferred poly field leaves both views. Store its value range in the aux
       * frame because cursor belongs to the enclosing walk and must not move. */
      DROP_A();
      DROP_B();
      m->c.stash.field_value.field = (uint8_t *)f;
      cur_struct_field             = f;
      ax->val_at                   = key_idx + 1;
      ax->val_end                  = val_end;
      goto phase2_poly_bind;
    }

    /* Host and selected-case fields share one flattened key space. The case wins
     * a duplicate declaration, remains in view A, and leaves view B. A non-struct
     * case treats every entry as case content. */
    if (host_case.case_idx >= 0 && poly_case_declares(m, &host_case, kdata, klen)) {
      DROP_B();
      continue;
    }

    /* The discriminator is already bound in cur_dst and is not case content, so
     * remove it from both views before a case sink can treat it as leftover. */
    if (f != NULL && (f->flags & BIND_FF_VDISC)) {
      DROP_A();
      DROP_B();
      continue;
    }

    /* The shallowest reserve-unknown sink owns each leftover. */
    if (reserve_unknown_off != 0xFFFFFFFF) {
      /* A host-only sink uses view A. With an inline case, the sink uses view B. */
      if (iv_idx == 0xFFFFu) continue;
      DROP_A();
      continue;
    }
    if (case_has_sink) {
      /* A case sink receives the leftover through view A, so strict mode accepts it. */
      DROP_B();
      continue;
    }
    if (UNLIKELY(m->b.ctx.opt_flags & BIND_OPT_DISALLOW_UNKNOWN))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_UNKNOWN_FIELD, 0);
    DROP_A();
    DROP_B();
  }
#undef DROP_A
#undef DROP_B
  goto phase2_finish;
}

/* Bind a deferred poly field from its aux-frame range. PHASE2_DESCEND saves the
 * enclosing cursor before installing the nested tape cursor. */
phase2_poly_bind: {
  const BindField *f     = cur_struct_field;
  uint16_t poly_idx      = BIND_FIELD_POLY_IDX(f);
  uint8_t *target        = cur_dst + f->offset;
  const BindAuxFrame *ax = &m->auxFrames[m->aux_depth];
  uint64_t word          = m->b.alloc.tape_arena[ax->val_at];

  PolyCase pc;
  if (f->flags & BIND_FF_KINDOF) {
    pc = poly_case_by_kindof(m, poly_idx, poly_kind_of_tape_tag((uint8_t)(word >> 56)));
    if (pc.case_idx < 0) {
      __BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_DOCUMENT_END;
      m->b.yield.pending_action = BIND_YIELD_ERROR;
      BIND_ERROR_PAYLOAD(m, BIND_ERR_KINDOF_UNREGISTERED, poly_kind_of_tape_tag((uint8_t)(word >> 56)),
                         BIND_ERROR_NO_POS, NULL);
      return;
    }
  } else {
    int disc_bound;
    pc = poly_case_by_disc(m, poly_idx, cur_dst, str_p, &disc_bound);
    if (pc.case_idx < 0) {
      __BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_DOCUMENT_END;
      m->b.yield.pending_action = BIND_YIELD_ERROR;
      BIND_ERROR_PAYLOAD(m, disc_bound ? BIND_ERR_VARIANT_UNKNOWN_DISC : BIND_ERR_VARIANT_MISSING_DISC, poly_idx,
                         BIND_ERROR_NO_POS, cur_dst);
      return;
    }
  }
  if (poly_case_slot_full(m, &pc)) {
    /* Resume here because ax->walk already points past this entry. Restore the
     * field from stash and rederive the case without moving either cursor. */
    __BIND_SAVE_LOCALS(m);
    m->c.stash.field_value.field = (uint8_t *)f;
    m->c.phase                   = BIND_PHASE_PHASE2_POLY_RETRY;
    m->b.yield.pending_action    = BIND_YIELD_BLOCK_FULL;
    m->b.yield.arg0              = (uint32_t)pc.slot_class;
    m->b.yield.arg1              = 0;
    m->b.yield.target            = cur_dst;
    return;
  }
  uint8_t *slot = poly_bind_target(m, target, &pc);
  /* Container indices use view A's start as their base. Bind only
   * [val_at, val_end) from the classified entry. The range sits at a nonzero
   * offset, so the descent installs plain view A; only the shared inline root
   * carries ModeInlineDualRoot. */
  PHASE2_DESCEND(m, slot, &m->b.ctx.types[pc.case_type_idx], pc.case_type_idx, ax->a.start, ax->val_at,
                 ax->val_end, TAPE_VIEW_A, BIND_PHASE_VARIANT_REBIND_RESUME);
}

/* Classification is complete here. Publish reserve-unknown, then bind the
 * selected inline case. A case-slot retry resumes at phase2_case_bind and
 * retries only case binding. */
phase2_finish: {
  BindAuxFrame *ax             = &m->auxFrames[m->aux_depth];
  const BindTypeMeta *hm       = &m->b.ctx.type_meta[cur_type.type_idx];
  uint32_t reserve_unknown_off = hm->u.strct.reserve_unknown_field_off;
  uint64_t *arena              = m->b.alloc.tape_arena;

  /* Classification changes seams only, so restamp the shared words with the
   * retained counts. A single view keeps its count in the begin word; a dual
   * shared root publishes the reserve count in the begin and the inline
   * projection's count in the close. */
  uint32_t a_start = ax->a.start;
  uint32_t a_close = a_start + (uint32_t)(arena[a_start] & 0xFFFFFFFFu);
  int dual         = struct_needs_dual_view(m, &cur_type);
  if (dual) {
    tape_build_patch_open(m, a_start, a_start, a_close, ax->b_count);
    tape_build_patch_close_count(m, a_close, ax->a.count);
  } else {
    tape_build_patch_open(m, a_start, a_start, a_close, ax->a.count);
  }

  if (reserve_unknown_off != 0xFFFFFFFF) {
    if (!dual) {
      /* With one consumer, publish the sink directly as view A. Its seams also
       * represent the empty result when classification drops every entry. */
      value_install_tape(m, cur_dst + reserve_unknown_off, a_start, a_close + 1, 0, TAPE_VIEW_A);
    } else {
      /* The reserve view reads view B over the same words. Both logical roots
       * address the shared begin at relative index zero, so the published
       * Value's count is the begin word's high24. */
      value_install_tape(m, cur_dst + reserve_unknown_off, a_start, a_close + 1, 0, TAPE_MODE_RESERVE_DUAL_ROOT);
    }
  }
}

/* Bind the selected inline case after reserve-unknown publication. BLOCK_FULL
 * resumes here and rederives the close and case from stable tape metadata and the
 * bound discriminator. */
phase2_case_bind: {
  BindAuxFrame *ax       = &m->auxFrames[m->aux_depth];
  const BindTypeMeta *hm = &m->b.ctx.type_meta[cur_type.type_idx];
  uint16_t iv_idx        = hm->u.strct.inline_variant_idx;
  uint64_t *arena        = m->b.alloc.tape_arena;
  uint32_t a_start       = ax->a.start;
  uint32_t a_close       = a_start + (uint32_t)(arena[a_start] & 0xFFFFFFFFu);

  if (iv_idx == 0xFFFFu) goto phase2_done;

  int disc_bound;
  PolyCase pc = poly_case_by_disc(m, iv_idx, cur_dst, str_p, &disc_bound);
  if (pc.case_idx < 0) {
    /* disc_seen distinguishes an absent discriminator from a present unresolved
     * one after classification has removed the entry. */
    if (!ax->disc_seen) goto phase2_done;
    __BIND_SAVE_LOCALS(m);
    m->c.phase                = BIND_PHASE_DOCUMENT_END;
    m->b.yield.pending_action = BIND_YIELD_ERROR;
    BIND_ERROR_PAYLOAD(m, disc_bound ? BIND_ERR_VARIANT_UNKNOWN_DISC : BIND_ERR_VARIANT_MISSING_DISC, iv_idx,
                       BIND_ERROR_NO_POS, cur_dst);
    return;
  }
  const BindType *case_type = &m->b.ctx.types[pc.case_type_idx];
  /* Tape binding supports cold cases only when tape data can materialize them
   * without reconstructing source bytes. */
  if ((case_type->flags & BIND_FLAG_COLD) && case_type->kind != BIND_KIND_PTR &&
      case_type->kind != BIND_KIND_VALUE)
    BIND_YIELD_ERR_NO_POS(m, BIND_ERR_UNSUPPORTED_TAG, 0);
  if (poly_case_slot_full(m, &pc)) {
    __BIND_SAVE_LOCALS(m);
    m->c.phase                = BIND_PHASE_TAPE_BIND_CLOSE_DRAIN_RETRY;
    m->b.yield.pending_action = BIND_YIELD_BLOCK_FULL;
    m->b.yield.arg0           = (uint32_t)pc.slot_class;
    m->b.yield.arg1           = 0;
    m->b.yield.target         = cur_dst;
    return;
  }

  uint8_t *eface      = NULL;
  const BindField *hf = (const BindField *)cur_type.child;
  for (uint32_t i = 0, n = cur_type.u.raw; i < n; i++) {
    if (hf[i].flags & BIND_FF_INLINE_VARIANT) {
      eface = cur_dst + hf[i].offset;
      break;
    }
  }
  if (eface == NULL) goto phase2_done;

  uint8_t *slot = poly_bind_target(m, eface, &pc);
  /* The inline case consumes view A's root object and returns directly to
   * phase2_done rather than the completed classification walk. A dual host
   * descends with ModeInlineDualRoot so an escaping value.Value case reads
   * its count from the shared close. */
  uint32_t inline_mode = struct_needs_dual_view(m, &cur_type) ? TAPE_MODE_INLINE_DUAL_ROOT : TAPE_VIEW_A;
  PHASE2_DESCEND(m, slot, case_type, pc.case_type_idx, a_start, a_start, a_close + 1, inline_mode,
                 BIND_PHASE_VARIANT_INLINE_RESUME);
}

phase2_done: {
  AUX_RELEASE(m);
  bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
  if (depth == 0) goto document_end;
  /* A nested case descent is a tape walk even when its outer walk reads JSON.
   * rebind_top therefore participates in continuation selection. */
  if (m->in_tape_bind || m->rebind_top > 0) goto t_scope_end;
  goto scope_end;
}

/* Existing slices use caller-owned backing. Streams use Go-managed batch
 * backing. New slices use allocator backing, and fixed arrays use inline storage.
 * Bump slices charge the borrowed tail immediately and return unused capacity at
 * close. */
array_begin: {
  if (BIND_IS_SLICE_LIKE(cur_type.kind)) {
    __builtin_memcpy(&cur_aux, cur_dst, sizeof(uint8_t *));
    if (cur_aux == NULL) {
      int32_t alloc_class = m->b.ctx.type_meta[cur_type.type_idx].u.slice.alloc_class;
      BindSlotClass *sc   = &m->b.alloc.slot_classes[alloc_class];

      if (UNLIKELY(sc->mode == BIND_SLOT_RECBATCH)) {
        /* A refill installs row 0 before resuming at array_value. */
        void *bk = recbatch_alloc(sc, 0);
        if (UNLIKELY(bk == NULL)) {
          BIND_YIELD(m, BIND_YIELD_RECBATCH_REFILL, (uint32_t)cur_type.type_idx, 0, BIND_PHASE_ARRAY_VALUE);
        }
        *(void **)(cur_dst)         = bk;
        *(intptr_t *)(cur_dst + 8)  = 0;
        *(intptr_t *)(cur_dst + 16) = recbatch_row_cap(0);
        cur_aux                     = bk;
        goto array_value;

      } else {

        if (UNLIKELY(sc->limit == sc->offset)) {
          BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)cur_type.type_idx, 0, BIND_PHASE_ARRAY_VALUE);
        }

        cur_aux                     = (void *)sc->block + sc->offset;
        *(void **)(cur_dst)         = (void *)cur_aux;
        *(intptr_t *)(cur_dst + 8)  = 0;
        *(intptr_t *)(cur_dst + 16) = sc->cap - sc->len;
        /* Charge the borrowed tail before any element write. A parse error may
         * bypass close, so deferred charging could expose written slots to the
         * next parse. Close returns only the unused tail. */
        sc->offset = sc->limit;
      }
    } else {
      __builtin_memcpy(&cur_aux, cur_dst, sizeof(uint8_t *));
    }
  } else if (cur_type.kind == BIND_KIND_ARRAY) {
    cur_aux = cur_dst;
  } else {
    /* Report the consumed opening bracket as the mismatch position. */
    BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS() - 1);
  }
}

array_value: {
  if (LIKELY(cur_type.kind == BIND_KIND_SLICE)) {
    BIND_SLICE_GROW_CHECK(m, cur_type, cur_dst, cur_aux, cur_count);
  } else if (cur_type.kind == BIND_KIND_ARRAY) {
    /* Fixed arrays parse and discard elements beyond their declared length. */
    if (UNLIKELY(cur_count >= m->b.ctx.type_meta[cur_type.type_idx].u.array.array_len)) {
      goto safe_skip_value;
    }
  } else {
    BIND_SLICE_GROW_CHECK(m, cur_type, cur_dst, cur_aux, cur_count);
    /* STREAM_SKIP drains remaining elements after a handler stops. */
    if (UNLIKELY(cur_type.flags & BIND_FLAG_STREAM_SKIP)) {
      goto safe_skip_value;
    }
    /* Before binding an element that contains a nested stream, yield with
     * cur_count as its index and cur_aux as its unbound slot. Go registers the
     * nested handler, then ARRAY_VALUE_BEGIN resumes at the element body. */
    if (cur_type.flags & BIND_FLAG_ELEM_HAS_STREAM) {
      BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)cur_type.type_idx, cur_count, BIND_PHASE_ARRAY_VALUE_BEGIN);
    }
  }

  /* body follows pointer chains while cur_aux remains the element-slot cursor.
   * ARRAY_VALUE_BEGIN enters here after the stream resume edge has rechecked
   * STREAM_SKIP. */
array_value_bind_body: {
  uint8_t *body              = cur_aux;
  const BindType *child_type = (const BindType *)cur_type.child;
  uint8_t ch                 = SRC_PEEK();
  if (UNLIKELY(child_type->flags | (ch == 'n'))) {
    /* Keep cur_count at the current index until pointer allocation completes.
     * BLOCK_FULL resumes at array_value with the JSON cursor still on this element. */
    BIND_RESOLVE_PTR_CHAIN(m, body, child_type, ch, BIND_PHASE_ARRAY_VALUE, 1);

    cur_count = cur_count + 1;
    cur_aux   = (uint8_t *)cur_aux + cur_type.u.slice.child_size;
    if (BIND_IS_ANY(child_type->kind)) {
      BIND_DISPATCH_ANY(child_type, body);
    }
    if (BIND_IS_VALUE(child_type->kind)) {
      BIND_DISPATCH_VALUE(body);
    }
    if (BIND_IS_DEFERRED_VALUE(child_type->kind)) {
      m->c.stash.deferred_yield.slot = (uint8_t *)body;
      m->c.stash.deferred_yield.type = (BindType *)child_type;
      if (m->b.alloc.deferred_drain_used + sizeof(UnmarshalRecord) > m->b.alloc.deferred_drain_cap) {
        __BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DEFERRED_RESUME;
        m->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;
        return;
      }
      goto deferred_value;
    }

    if (ch == 'n') {
      if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      SRC_ADVANCE();
      goto array_continue;
    }
  } else {
    cur_count = cur_count + 1;
    cur_aux   = (uint8_t *)cur_aux + cur_type.u.slice.child_size;
  }

  BIND_DISPATCH_STRING(child_type, body, ch, array_continue,
                       BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS()));
  switch (child_type->kind) {
    // clang-format off
    /* zero_size is zero so reused slice or stream backing and fixed arrays retain
     * omitted struct fields. Newly allocated backing is already zeroed. */
    BIND_VALUE_SWITCH_COMMON(child_type, body, ch, array_continue, 0, BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS()), bind_push_array_or_slice);
    // clang-format on
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY:
    if (ch != '[') BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    /* Save elem_idx + 1 as the parent's live count before descending. */
    if (bind_push_array_or_slice(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);

    cur_dst   = body;
    cur_type  = *child_type;
    cur_count = 0;
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      BIND_EMPTY_ARRAY_CLOSE(m, &cur_type, array_continue, bind_pop_array_or_slice);
    }
    goto array_begin;
  }
  BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_SYNTAX, SRC_POS());
}
}

array_continue: {
  uint8_t ch = SRC_ADVANCE_CHAR();
  if (ch == ',') {
    if (BIND_IS_SLICE_LIKE(cur_type.kind)) {
      *(intptr_t *)(cur_dst + 8) = (intptr_t)cur_count;
    }
    goto array_value;
  }
  if (ch == ']') {
    /* Yield the final stream batch once, then resume inside array_close so the
     * handler cannot run twice. */
    if (cur_type.kind == BIND_KIND_STREAM && !(cur_type.flags & BIND_FLAG_STREAM_SKIP)) {
      BIND_YIELD(m, BIND_YIELD_SLICE_GROW, (uint32_t)cur_type.type_idx, 0, BIND_PHASE_ARRAY_CLOSE);
    }
  array_close: {
    if (BIND_IS_SLICE_LIKE(cur_type.kind)) {

      *(intptr_t *)(cur_dst + 8)  = (intptr_t)cur_count;
      *(intptr_t *)(cur_dst + 16) = (intptr_t)cur_count;

      int32_t alloc_class = m->b.ctx.type_meta[cur_type.type_idx].u.slice.alloc_class;
      BindSlotClass *sc   = &m->b.alloc.slot_classes[alloc_class];

      if (LIKELY(sc->mode != BIND_SLOT_RECBATCH)) {
        /* Return the unused bump tail from the next-write cursor. */
        const uint8_t *data;
        __builtin_memcpy(&data, cur_dst, sizeof(data));
        uint32_t off = (uint32_t)(data - sc->block);
        if (off < sc->limit) {
          sc->offset = (uint32_t)((uint8_t *)cur_aux - sc->block);
          sc->len += cur_count;
        }
      }
    }
    /* An empty root stream resumes here without a pushed frame, so depth zero
     * must complete directly instead of popping. */
    if (depth == 0) goto document_end;
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    if (depth == 0) goto document_end;
    goto scope_end;
  }
  }
  if (ch == 0x20) {
    /* 0x20 is the scan sentinel byte at src[len]. At depth 0 it means the
     * top-level value ended cleanly; at depth > 0 the array is unclosed. */
    if (UNLIKELY(depth > 0)) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
    goto document_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

scope_end: {
  if (cur_type.kind == BIND_KIND_STRUCT) goto object_continue;
  if (BIND_IS_SLICE_LIKE(cur_type.kind) || cur_type.kind == BIND_KIND_ARRAY) goto array_continue;
  if (cur_type.kind == BIND_KIND_MAP) goto map_continue;
}

/* Maps stage entries in fixed regions of a shared noscan buffer. Region headers
 * distinguish complete entries from a live in-progress entry for drain and
 * compaction. Typed SlotClass and retained allocator backings keep every staged
 * referent reachable until deferred records and map regions drain before release. */
map_open: {
  uint32_t map_type_idx       = cur_type.type_idx;
  uint8_t *parent_slot        = cur_dst;
  uint32_t stride             = m->b.ctx.type_meta[map_type_idx].u.map.stride;
  const uint32_t region_slots = BIND_MAP_REGION_SLOTS;
  const uint32_t region_size  = BIND_MAP_REGION_HEADER_SIZE + region_slots * stride;

  int32_t alloc_class = cur_type.u.map.alloc_class;
  BindSlotClass *sc   = &m->b.alloc.slot_classes[alloc_class];

  /* Capacity yields occur before state changes, so map_open is retry-safe. */
  if (UNLIKELY(m->b.alloc.map_buf_used + region_size > m->b.alloc.map_buf_cap)) {
    BIND_YIELD_FLUSH_MAP(m, 0, 0, (uint8_t *)0, BIND_PHASE_MAP_OPEN_RETRY);
  }
  if (sc->offset >= sc->limit) {
    BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)alloc_class, 0, BIND_PHASE_MAP_OPEN_RETRY);
  }
  /* Always overwrite parent_slot because a reused map-buffer slot may hold a
   * stale pointer from an earlier flush. */
  uint8_t *map_hdr = *(uint8_t **)(sc->block + sc->offset);
  sc->offset += sc->elem_size;

  uint32_t region_off = m->b.alloc.map_buf_used;
  m->b.alloc.map_buf_used += region_size;
  BindMapRegionHeader *map_region = (BindMapRegionHeader *)(m->b.alloc.map_buf + region_off);

  map_region->hmap           = map_hdr;
  map_region->parent_slot    = parent_slot;
  map_region->type_idx       = map_type_idx;
  map_region->entry_count    = 0;
  map_region->next_entry_off = 0;
  map_region->stride         = stride;
  *(void **)parent_slot      = map_hdr;

  /* Publish the map frame before child descent so drain can identify its live region. */
  {
    BindFrame *f     = &frames[depth];
    f->dst           = parent_slot;
    f->kind          = cur_type.kind;
    f->type_idx      = cur_type.type_idx;
    f->cs.child_size = cur_type.u.raw;
    f->child_type    = (const void *)cur_type.child;
    f->u.map_region  = map_region;
  }
  cur_aux = map_region;

  cur_dst   = parent_slot;
  cur_count = 0;
  SRC_ADVANCE();
  if (SRC_ACCEPT('}')) {
    /* Retire the empty map's region; drain later reclaims its buffer space. */
    frames[depth].u.map_region = (BindMapRegionHeader *)0;
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    if (depth == 0) goto document_end;
    goto scope_end;
  }
  goto map_key;
}

/* A map region stores [16-byte string key | value] entries at stride intervals.
 * Reserving advances next_entry_off, but entry_count advances only after the
 * value subtree completes, so drain never publishes an in-progress entry. */
map_key: {
  BindMapRegionHeader *map_region = (BindMapRegionHeader *)cur_aux;
  uint32_t next_entry_off         = map_region->next_entry_off;
  uint32_t stride                 = map_region->stride;
  uint32_t region_entry_bytes     = stride << 4;

  /* FLUSH drains complete entries and compacts the in-progress entry. Resume
   * rederives the relocated region before retrying its value. */
  if (UNLIKELY(next_entry_off >= region_entry_bytes)) {
    BIND_YIELD_FLUSH_MAP(m, 0, 0, (uint8_t *)0, BIND_PHASE_MAP_CONTINUE);
  }
  uint8_t *slot              = (uint8_t *)map_region + BIND_MAP_REGION_HEADER_SIZE + next_entry_off;
  map_region->next_entry_off = next_entry_off + stride;
  const uint8_t *key         = SRC_ADVANCE_PTR();
  if (*key != '"') BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_SYNTAX, (uint32_t)(key - src));
  if (bind_visit_str(&str_p, key, slot) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(key - src));
  SRC_EXPECT(':');
  goto map_value;
}

/* The noscan buffer cannot retain heap referents. Pointer values already target
 * typed SlotClass storage; other pointer-bearing values use typed intermediate
 * slots until deferred records drain and map drain copies them into *hmap. */
map_value: {
  const BindType *mt              = &cur_type;
  BindMapRegionHeader *map_region = (BindMapRegionHeader *)cur_aux;
  uint32_t stride                 = map_region->stride;
  uint8_t *body =
      (uint8_t *)map_region + BIND_MAP_REGION_HEADER_SIZE + map_region->next_entry_off - stride + BIND_MAP_VAL_OFF;
  /* Reused value slots may contain stale data. Zero only shapes that do not
   * overwrite the whole area; pointer pointees are already zeroed in typed storage. */
  const BindType *child_type = (const BindType *)mt->child;
  uint8_t ch                 = SRC_PEEK();
  if (UNLIKELY(child_type->flags | (ch == 'n'))) {
    /* Preserve the noscan slot address across pointer unwrapping. If body moves,
     * the pointee is already in scannable typed storage; otherwise pointer-bearing
     * output must be redirected to an intermediate slot. */
    uint8_t *orig_slot = body;
    /* Dispatch uses resolved pointee metadata because pointer flags do not inherit it. */
    BIND_RESOLVE_PTR_CHAIN(m, body, child_type, ch, BIND_PHASE_MAP_VALUE, 0);
    /* Cold non-pointer values use a typed intermediate; resolved pointers write
     * directly into their scannable pointee. */
    if (child_type->flags & BIND_FLAG_CONTAINS_DEFERRED || BIND_IS_ANY(child_type->kind) ||
        BIND_IS_VALUE(child_type->kind) || BIND_IS_DEFERRED_VALUE(child_type->kind)) {
      if (BIND_IS_ANY(child_type->kind) || BIND_IS_DEFERRED_VALUE(child_type->kind)) {
        if (body == orig_slot) __builtin_memset(body, 0, (size_t)stride - BIND_MAP_VAL_OFF);
        if (BIND_IS_ANY(child_type->kind)) {
          BIND_DISPATCH_ANY(child_type, body);
        }
      }
      /* body equality selects an intermediate slot or the resolved pointee. */
      uint8_t *target_slot;
      if (body == orig_slot) {
        const BindMapDrainInfo *di = (const BindMapDrainInfo *)m->b.ctx.type_meta[mt->type_idx].u.map.drain_info;
        int32_t vsc_idx            = di->val_slot_class;
        BindSlotClass *vsc         = &m->b.alloc.slot_classes[vsc_idx];

        if (vsc->offset >= vsc->limit) {
          m->c.stash.deferred_yield.slot = (uint8_t *)body;
          m->c.stash.deferred_yield.type = (BindType *)child_type;
          BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)vsc_idx, 0, BIND_PHASE_MAP_VALUE);
        }
        uint8_t *val_slot = vsc->block + vsc->offset;
        vsc->offset += vsc->elem_size;
        *(void **)body = val_slot;
        target_slot    = val_slot;
      } else {
        target_slot = body;
      }
      if (BIND_IS_VALUE(child_type->kind)) {
        m->c.stash.deferred_yield.slot = target_slot;
        m->c.stash.deferred_yield.type = (BindType *)child_type;
        goto vd_dispatch_value;
      }
      if (BIND_IS_DEFERRED_VALUE(child_type->kind)) {
        m->c.stash.deferred_yield.slot = target_slot;
        m->c.stash.deferred_yield.type = (BindType *)child_type;
        if (m->b.alloc.deferred_drain_used + sizeof(UnmarshalRecord) > m->b.alloc.deferred_drain_cap) {
          __BIND_SAVE_LOCALS(m);
          m->c.phase                = BIND_PHASE_DEFERRED_RESUME;
          m->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;
          return;
        }
        goto deferred_value;
      }
      /* Parse aggregates in the typed intermediate so nested deferred records
       * target scannable storage. */
      uint32_t val_size = m->b.ctx.type_meta[child_type->type_idx].size;
      __builtin_memset(target_slot, 0, (size_t)val_size);
      /* Null publishes the zeroed intermediate through map drain. */
      if (ch == 'n') {
        if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
        SRC_ADVANCE();
        goto map_continue;
      }
      if (child_type->kind == BIND_KIND_STRUCT) {
        BIND_DESCEND_STRUCT(target_slot, child_type, map_continue, bind_push_map);
      }
      if (child_type->kind == BIND_KIND_ARRAY || child_type->kind == BIND_KIND_SLICE) {
        if (ch != '[') BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
        if (bind_push_map(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
          BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
        cur_dst   = target_slot;
        cur_type  = *child_type;
        cur_count = 0;
        SRC_ADVANCE();
        if (SRC_ACCEPT(']')) {
          BIND_EMPTY_ARRAY_CLOSE(m, &cur_type, map_continue, bind_pop_map);
        }
        goto array_begin;
      }
      if (child_type->kind == BIND_KIND_MAP) {
        /* Store a nested *hmap in the typed intermediate that map drain copies by value. */
        if (ch != '{') BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
        BIND_MAP_OPEN(m, child_type, target_slot, bind_push_map);
      }
    }

    if (ch == 'n') {
      if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      __builtin_memset(body, 0, (size_t)stride - BIND_MAP_VAL_OFF);
      SRC_ADVANCE();
      goto map_continue;
    }
  }

  BIND_DISPATCH_STRING(child_type, body, ch, map_continue,
                       BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS()));
  switch (child_type->kind) {
    BIND_VALUE_SWITCH_COMMON(child_type, body, ch, map_continue, (size_t)stride - BIND_MAP_VAL_OFF,
                             BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS()), bind_push_map);
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY:
    if (ch != '[') BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    __builtin_memset(body, 0, (size_t)stride - BIND_MAP_VAL_OFF);
    if (bind_push_map(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = body;
    cur_type  = *child_type;
    cur_count = 0;
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      BIND_EMPTY_ARRAY_CLOSE(m, &cur_type, map_continue, bind_pop_map);
    }
    goto array_begin;
  }
  BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_SYNTAX, SRC_POS());
}

map_continue: {
  /* Publish this staged entry to drain only after its value subtree completes. */
  BindMapRegionHeader *map_region = (BindMapRegionHeader *)cur_aux;
  if (map_region != (BindMapRegionHeader *)0) {
    map_region->entry_count++;
  }
  uint8_t ch = SRC_ADVANCE_CHAR();
  if (ch == ',') {
    goto map_key;
  }
  if (ch == '}') {
    /* Retire the region without flushing during frame teardown. Document-end or
     * the next buffer flush drains its complete entries and reclaims it. */
    frames[depth].u.map_region = (BindMapRegionHeader *)0;
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    if (depth == 0) goto document_end;
    goto scope_end;
  }
  if (ch == 0x20) {
    /* 0x20 is the scan sentinel byte at src[len]. At depth 0 it means the
     * top-level value ended cleanly; at depth > 0 the map is unclosed. */
    if (UNLIKELY(depth > 0)) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
    goto document_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

/* FLUSH_MAP may relocate the live region, so resume reloads cur_aux from its frame. */
map_continue_resume: {
  cur_aux = frames[depth].u.map_region;
  goto map_key;
}

skip_value: {
  if (m->b.ctx.opt_flags & BIND_OPT_SKIP_LENIENT) {
    goto unsafe_skip_value;
  } else {
    goto safe_skip_value;
  }
}

unsafe_skip_value: {
  /* This path tracks container depth without validating scalars or comma order. */
  if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
  uint32_t skip_depth = 0;
  uint8_t ch          = SRC_PEEK();
  if (ch == '{' || ch == '[') {
    skip_depth = 1;
    SRC_ADVANCE();
  } else {
    SRC_ADVANCE();
    goto object_continue;
  }
  for (;;) {
    if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
    ch = SRC_ADVANCE_CHAR();
    if (ch == '{' || ch == '[') skip_depth++;
    else if (ch == '}' || ch == ']') {
      if (--skip_depth == 0) goto object_continue;
    }
  }
}

safe_skip_value: {
  if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
  uint32_t skip_depth = 0;
  uint8_t ch          = SRC_PEEK();
  if (ch == '{' || ch == '[') {
    skip_depth = 1;
    SRC_ADVANCE();
  } else {
    /* str_p is scratch (not advanced); decoded bytes are discarded.
     * Padded parsers rely on the 64-byte 0x20 tail on ctx.src. */
    if (ch == '"') {
      if (UNLIKELY(ndec_str_parse(SRC_PTR() + 1, str_p, NULL) < 0)) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    } else if (ch == 't' || ch == 'f' || ch == 'n') {
      if (UNLIKELY(bind_validate_atom(SRC_PTR(), ch) < 0)) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    } else if (ch == '-' || (ch >= '0' && ch <= '9')) {
      const uint8_t *_end;
      double _dv;
      if (UNLIKELY(ndec_parse_double_padded(SRC_PTR(), &_dv, m->c.atof, &_end)))
        BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      if (UNLIKELY(is_non_delim(*_end))) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
      (void)_dv;
    } else {
      BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    }
    SRC_ADVANCE();
    goto scope_end;
  }
  for (;;) {
    if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
    ch = SRC_ADVANCE_CHAR();
    if (ch == '{' || ch == '[') skip_depth++;
    else if (ch == '}' || ch == ']') {
      if (--skip_depth == 0) goto scope_end;
    } else if (ch == ',') {
      if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
      uint8_t next = SRC_PEEK();
      if (next == ']' || next == '}' || next == ',') BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    }
  }
}
/* any_value boxes scalars in registered typed or static storage and publishes
 * container interfaces before descending through registered []any or
 * map[string]any metadata. The unchanged parent kind selects continuation. */
any_value: {
  const BindAnyMeta *am = (const BindAnyMeta *)m->b.ctx.types[m->b.ctx.any_type_idx].child;
  uint8_t *any_slot     = m->c.stash.any_yield.slot;
  uint8_t ch            = SRC_PEEK();
#define ANY_RETURN()                                                                                              \
  do {                                                                                                            \
    if (depth == 0) goto document_end;                                                                            \
    switch (cur_type.kind) {                                                                                      \
    case BIND_KIND_STRUCT:                                                                                        \
      goto object_continue;                                                                                       \
    case BIND_KIND_SLICE:                                                                                         \
    case BIND_KIND_STREAM:                                                                                        \
    case BIND_KIND_ARRAY:                                                                                         \
      goto array_continue;                                                                                        \
    case BIND_KIND_MAP:                                                                                           \
      goto map_continue;                                                                                          \
    }                                                                                                             \
    BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());                                                         \
  } while (0)

  /* Numbers share parsing and typed-slot allocation. UseNumber preserves the
   * original text as json.Number; the default path stores a finite float64. */
  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    int use_number          = (m->b.ctx.opt_flags & BIND_OPT_USE_NUMBER) != 0;
    uint32_t slot_class_idx = use_number ? am->string_slot_class : am->float64_slot_class;
    const void *type_tag    = use_number ? am->number_type : am->float64_type;
    BindSlotClass *sc       = &m->b.alloc.slot_classes[slot_class_idx];
    if (sc->offset >= sc->limit) {
      BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, slot_class_idx, 0, BIND_PHASE_ANY_RESUME);
    }
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    const uint8_t *_end;
    double dv;
    if (UNLIKELY(ndec_parse_double_padded(SRC_PTR(), &dv, m->c.atof, &_end)))
      BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    if (UNLIKELY(is_non_delim(*_end))) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    if (UNLIKELY(use_number)) {
      (void)dv;
      uint32_t num_len  = (uint32_t)(_end - (SRC_PTR()));
      uint8_t *num_data = str_p;
      __builtin_memcpy(num_data, SRC_PTR(), num_len);
      str_p += num_len;
      bind_write_str_header(data, num_data, num_len);
    } else {
      if (UNLIKELY(!__builtin_isfinite(dv))) BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
      *(double *)data = dv;
    }
    *(const void **)any_slot       = type_tag;
    *(const void **)(any_slot + 8) = data;
    SRC_ADVANCE();
    ANY_RETURN();
  }
  if (ch == '"') {
    BindSlotClass *sc = &m->b.alloc.slot_classes[am->string_slot_class];
    if (sc->offset >= sc->limit) {
      BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)am->string_slot_class, 0, BIND_PHASE_ANY_RESUME);
    }
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    if (bind_visit_str(&str_p, SRC_PTR(), data) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    *(const void **)any_slot       = am->string_type;
    *(const void **)(any_slot + 8) = data;
    SRC_ADVANCE();
    ANY_RETURN();
  }
  if (ch == 't' || ch == 'f') {
    if (bind_validate_atom(SRC_PTR(), ch) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    *(const void **)any_slot       = am->bool_type;
    *(const void **)(any_slot + 8) = (ch == 't') ? (const void *)am->static_true : (const void *)am->static_false;
    SRC_ADVANCE();
    ANY_RETURN();
  }
  if (ch == 'n') {
    if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    *(const void **)any_slot       = am->nil_type;
    *(const void **)(any_slot + 8) = NULL;
    SRC_ADVANCE();
    ANY_RETURN();
  }
  /* Publish the []any interface before descent so its slice-header slot is reachable. */
  if (ch == '[') {
    BindSlotClass *sc = &m->b.alloc.slot_classes[am->slice_slot_class];
    if (sc->offset >= sc->limit) {
      BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)am->slice_slot_class, 0, BIND_PHASE_ANY_RESUME);
    }
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    *(const void **)any_slot       = am->slice_type;
    *(const void **)(any_slot + 8) = data;
    if (bind_push(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = data;
    cur_type  = types[am->slice_any_type_idx];
    cur_count = 0;
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      /* Return through the dynamic parent continuation rather than a fixed label. */
      BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      ANY_RETURN();
    }
    goto array_begin;
  }
  /* map_open writes the typed *hmap directly into the interface data word, so
   * this path must not allocate a separate data slot. */
  if (ch == '{') {
    *(const void **)any_slot = am->map_type;
    BIND_MAP_OPEN(m, &types[am->map_any_type_idx], any_slot + 8, bind_push);
  }
  BIND_ERR_VALUE_OR_EOF(m, BIND_ERR_SYNTAX, SRC_POS());
#undef ANY_RETURN
}

/* Deferred values are staged until FLUSH_UNMARSHAL or document end.
 * Unmarshaler and RawMessage records carry the source byte span; a
 * TextUnmarshaler record carries decoded string bytes from str_arena. JSON
 * null bypasses TextUnmarshaler but remains part of the raw span for the
 * other deferred kinds. */
deferred_value: {
  uint8_t *deferred_slot      = m->c.stash.deferred_yield.slot;
  const BindType *deferred_ct = m->c.stash.deferred_yield.type;
  uint8_t ch                  = SRC_PEEK();
#define DEFERRED_RETURN()                                                                                         \
  do {                                                                                                            \
    if (depth == 0) goto document_end;                                                                            \
    switch (cur_type.kind) {                                                                                      \
    case BIND_KIND_STRUCT:                                                                                        \
      goto object_continue;                                                                                       \
    case BIND_KIND_SLICE:                                                                                         \
    case BIND_KIND_STREAM:                                                                                        \
    case BIND_KIND_ARRAY:                                                                                         \
      goto array_continue;                                                                                        \
    case BIND_KIND_MAP:                                                                                           \
      goto map_continue;                                                                                          \
    }                                                                                                             \
    BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());                                                         \
  } while (0)

  /* TextUnmarshaler is not called for JSON null. Consume the token and retain
   * the receiver's zero value; pointer nulls were excluded before dispatch. */
  if (ch == 'n' && deferred_ct->kind == BIND_KIND_TEXT_UNMARSHALER) {
    if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    SRC_ADVANCE();
    DEFERRED_RETURN();
  }

  UnmarshalRecord *rec = (UnmarshalRecord *)(m->b.alloc.deferred_drain + m->b.alloc.deferred_drain_used);
  rec->target          = deferred_slot;
  rec->type_idx        = deferred_ct->type_idx;
  rec->kind            = deferred_ct->kind;

  if (deferred_ct->kind != BIND_KIND_TEXT_UNMARSHALER) {
    /* Record the raw JSON span from the current structural offset to the first
     * structural offset after the value. */
    uint32_t start_off = SRC_POS();
    if (ch == '{' || ch == '[') {
      uint32_t um_depth = 1;
      SRC_ADVANCE();
      while (um_depth > 0) {
        if (UNLIKELY(SRC_EOF())) BIND_YIELD_ERR(m, BIND_ERR_EOF, SRC_POS());
        uint8_t c = SRC_ADVANCE_CHAR();
        if (c == '{' || c == '[') um_depth++;
        else if (c == '}' || c == ']')
          um_depth--;
      }
    } else {
      SRC_ADVANCE();
    }
    uint32_t end_off = SRC_POS();
    rec->arg0        = start_off;
    rec->arg1        = end_off;
  } else {
    /* TextUnmarshaler receives decoded string bytes from str_arena. */
    if (ch != '"') BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    const uint8_t *str_data;
    uint32_t str_len;
    if (bind_intern_str(&str_p, SRC_PTR(), &str_data, &str_len) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    rec->arg0 = (uint32_t)(str_data - m->b.alloc.str_arena);
    rec->arg1 = str_len;
    SRC_ADVANCE();
  }

  m->b.alloc.deferred_drain_used += sizeof(UnmarshalRecord);
  DEFERRED_RETURN();
#undef DEFERRED_RETURN
}

/* The Value submachine uses separate locals, so the saved parent depth and type
 * remain authoritative for continuation. */
#define VALUE_RETURN()                                                                                            \
  do {                                                                                                            \
    if (depth == 0) goto document_end;                                                                            \
    switch (cur_type.kind) {                                                                                      \
    case BIND_KIND_STRUCT:                                                                                        \
      goto object_continue;                                                                                       \
    case BIND_KIND_SLICE:                                                                                         \
    case BIND_KIND_STREAM:                                                                                        \
    case BIND_KIND_ARRAY:                                                                                         \
      goto array_continue;                                                                                        \
    case BIND_KIND_MAP:                                                                                           \
      goto map_continue;                                                                                          \
    }                                                                                                             \
    BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());                                                         \
  } while (0)

#define VALUE_DISPATCH_ELEM()                                                                                     \
  do {                                                                                                            \
    uint8_t _ch = SRC_PEEK();                                                                                     \
    if (_ch == '{') {                                                                                             \
      SRC_ADVANCE();                                                                                              \
      if (SRC_ACCEPT('}')) {                                                                                      \
        bind_emit_empty_container(&vd_tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT, vd_tape_base);                  \
      } else {                                                                                                    \
        goto vd_obj_begin;                                                                                        \
      }                                                                                                           \
    } else if (_ch == '[') {                                                                                      \
      SRC_ADVANCE();                                                                                              \
      if (SRC_ACCEPT(']')) {                                                                                      \
        bind_emit_empty_container(&vd_tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY, vd_tape_base);                    \
      } else {                                                                                                    \
        goto vd_arr_begin;                                                                                        \
      }                                                                                                           \
    } else {                                                                                                      \
      if (bind_emit_primitive(src, &cursor.idx, vd_str_arena, &vd_str_p, &vd_tape_p, m->c.atof, vd_str_limit))    \
        BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());                                                            \
    }                                                                                                             \
  } while (0)

/* The synchronous Value submachine either appends a standalone Value or appends
 * an entry value to an open merged tape. vd_close uses phase to finalize the
 * selected lifecycle, and frames above the parent enforce the shared depth limit. */

/* A standalone Value appends at tape_used without per-value reservation. Its
 * container indices are relative to the segment start, and value_doc keeps arena
 * backings reachable. Phase zero tells vd_close to patch its end and commit it. */
vd_dispatch_value: {
  uint64_t *vd_tape_base = m->b.alloc.tape_arena + m->b.alloc.tape_used;
  m->b.alloc.value_tape  = vd_tape_base; /* vd_resume reads this */

  /* Standalone Value output is contiguous view A without seams. */
  uint8_t *target = m->c.stash.deferred_yield.slot;
  value_begin_install(m, target, (uint32_t)m->b.alloc.tape_used, 0, TAPE_VIEW_A);
  /* Save str_p before vd_resume reloads it from the machine and later commits it. */
  __BIND_SAVE_LOCALS(m);
  m->c.phase = 0; /* standalone Value lifecycle */
  goto vd_resume;
}

/* A merged entry already has its key. Keep value_tape at the merged object root
 * for relative container indices, but append value words at tape_used. */
vd_dispatch_unknown_value: {
  m->b.alloc.value_tape = m->b.alloc.tape_arena + m->auxFrames[m->aux_depth].a.start;
  __BIND_SAVE_LOCALS(m);
  m->c.phase = BIND_PHASE_RESERVE_UNKNOWN_VALUE_RESUME;
  goto vd_resume;
}

/* Both Value lifecycles share the parse string arena. The merged-entry phase
 * separates its container-index base from its append cursor. */
vd_resume: {
  uint64_t *vd_tape_base = m->b.alloc.value_tape;
  uint8_t *vd_str_arena  = m->b.alloc.str_arena; /* shared with bind */
  /* Bound raw-number copies by the pre-sized document string arena. */
  const uint8_t *vd_str_limit = vd_str_arena + m->b.alloc.str_arena_cap;
  /* Standalone output starts at its base. Merged-entry output appends at tape_used
   * while container close indices remain relative to the merged object root. */
  uint64_t *vd_tape_p = vd_tape_base;
  if (m->c.phase == BIND_PHASE_RESERVE_UNKNOWN_VALUE_RESUME) {
    vd_tape_p = m->b.alloc.tape_arena + m->b.alloc.tape_used;
  }
  uint8_t *vd_str_p          = str_p; /* continue the shared bump cursor */
  int32_t vd_depth           = -1;
  uint32_t vd_cur_count      = 0;
  uint32_t vd_cur_tape_index = 0;
  /* Reuse frame slots above the stable parent depth so combined bind and Value
   * nesting remains within BIND_MAX_DEPTH. */
  BindFrame *vd_stack = &m->c.frames[depth + 1];
  int vd_stack_cap    = BIND_MAX_DEPTH - depth;
  uint8_t ch          = SRC_PEEK();
  if (ch == '{') {
    SRC_ADVANCE();
    if (SRC_ACCEPT('}')) {
      bind_emit_empty_container(&vd_tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT, vd_tape_base);
      goto vd_close;
    }
    goto vd_obj_begin;
  }
  if (ch == '[') {
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      bind_emit_empty_container(&vd_tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY, vd_tape_base);
      goto vd_close;
    }
    goto vd_arr_begin;
  }
  goto vd_root_scalar;

vd_obj_begin: {
  if (bind_emit_start_container(vd_stack, vd_stack_cap, &vd_tape_p, 0, &vd_depth, &vd_cur_count,
                                &vd_cur_tape_index, vd_tape_base))
    BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
  const uint8_t *key = SRC_ADVANCE_PTR();
  if (*key != '"') BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
  vd_cur_count++;
  if (bind_emit_string_copy(vd_str_arena, &vd_str_p, &vd_tape_p, key))
    BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
  if (SRC_ADVANCE_CHAR() != ':') BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
  goto vd_obj_field;
}

vd_obj_field: { VALUE_DISPATCH_ELEM(); }

vd_obj_continue: {
  uint8_t ch = SRC_ADVANCE_CHAR();
  if (ch == ',') {
    vd_cur_count++;
    const uint8_t *key = SRC_ADVANCE_PTR();
    if (*key != '"') BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    if (bind_emit_string_copy(vd_str_arena, &vd_str_p, &vd_tape_p, key))
      BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    if (SRC_ADVANCE_CHAR() != ':') BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    goto vd_obj_field;
  }
  if (ch == '}') {
    bind_emit_end_container(vd_stack, &vd_tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT, &vd_depth, &vd_cur_count,
                            &vd_cur_tape_index, vd_tape_base);
    goto vd_scope_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

vd_arr_begin: {
  if (bind_emit_start_container(vd_stack, vd_stack_cap, &vd_tape_p, 1, &vd_depth, &vd_cur_count,
                                &vd_cur_tape_index, vd_tape_base))
    BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
  vd_cur_count++;
}

vd_arr_value: { VALUE_DISPATCH_ELEM(); }

vd_arr_continue: {
  uint8_t ch = SRC_ADVANCE_CHAR();
  if (ch == ',') {
    vd_cur_count++;
    goto vd_arr_value;
  }
  if (ch == ']') {
    bind_emit_end_container(vd_stack, &vd_tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY, &vd_depth, &vd_cur_count,
                            &vd_cur_tape_index, vd_tape_base);
    goto vd_scope_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

vd_scope_end: {
  if (vd_depth == -1) goto vd_close;
  if (vd_cur_count & 0x80000000u) goto vd_arr_continue;
  goto vd_obj_continue;
}

vd_root_scalar: {
  if (bind_emit_primitive(src, &cursor.idx, vd_str_arena, &vd_str_p, &vd_tape_p, m->c.atof, vd_str_limit))
    BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
  goto vd_close;
}

/* vd_close commits the shared string cursor and dispatches by lifecycle.
 * RESERVE_UNKNOWN_VALUE_RESUME commits the merged entry and trailing seam for
 * phase2 classification. Phase zero patches and commits a standalone Value. */
vd_close: {
  uint32_t tape_len = (uint32_t)(vd_tape_p - vd_tape_base);
  str_p             = vd_str_p;
  vd_depth          = -1;
  if (m->c.phase == BIND_PHASE_RESERVE_UNKNOWN_VALUE_RESUME) {
    m->b.alloc.tape_used = (size_t)(vd_tape_p - m->b.alloc.tape_arena);
    tape_build_entry_end(m, &m->auxFrames[m->aux_depth].a);
    goto object_continue;
  }
  uint8_t *target                      = m->c.stash.deferred_yield.slot;
  *(int32_t *)(target + VALUE_END_OFF) = (int32_t)tape_len;
  m->b.alloc.tape_used                 = (size_t)(vd_tape_p - m->b.alloc.tape_arena);
  VALUE_RETURN();
}
}
#undef VALUE_RETURN
#undef VALUE_DISPATCH_ELEM

variant_rebind_resume: { goto phase2_walk; }

/* Bind a JSON poly field only after its case is resolved. BLOCK_FULL resumes
 * here with cursor still on the value and rederives the case from field and host. */
poly_field_bind: {
  const BindField *f = cur_struct_field;
  uint16_t poly_idx  = BIND_FIELD_POLY_IDX(f);
  uint8_t *target    = cur_dst + f->offset;
  uint8_t ch         = SRC_PEEK();

  PolyCase pc;
  if (f->flags & BIND_FF_KINDOF) {
    pc = poly_case_by_kindof(m, poly_idx, poly_kind_of_json_char(ch));
  } else {
    int disc_bound;
    pc = poly_case_by_disc(m, poly_idx, cur_dst, str_p, &disc_bound);
  }
  if (poly_case_slot_full(m, &pc)) {
    m->c.stash.field_value.field = (uint8_t *)f;
    BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)pc.slot_class, 0, BIND_PHASE_VARIANT_INLINE_RESUME);
  }

  /* Publish the complete interface before descent fills its data storage. */
  uint8_t *body      = poly_bind_target(m, target, &pc);
  const BindType *ct = &m->b.ctx.types[pc.case_type_idx];
  uint32_t zero_size = ct->kind == BIND_KIND_STRUCT ? m->b.ctx.type_meta[ct->type_idx].size : 0;

  /* Keep cur_type on the host until the descent saves its frame. */
  BIND_DISPATCH_STRING(ct, body, ch, object_continue, BIND_TYPE_MISMATCH_SKIP(m, SRC_POS()));
  switch (ct->kind) {
    BIND_VALUE_SWITCH_COMMON(ct, body, ch, object_continue, zero_size, BIND_TYPE_MISMATCH_SKIP(m, SRC_POS()),
                             bind_push_struct);
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY:
    if (ch != '[') BIND_TYPE_MISMATCH_SKIP(m, SRC_POS());
    if (bind_push_struct(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = body;
    cur_type  = *ct;
    cur_count = 0;
    SRC_ADVANCE();
    if (SRC_ACCEPT(']')) {
      BIND_EMPTY_ARRAY_CLOSE(m, &cur_type, object_continue, bind_pop_struct);
    }
    goto array_begin;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

root_scalar: {
  uint8_t ch         = SRC_PEEK();
  const BindType *ct = &cur_type;
  /* PTR chain already unwrapped at document_start; ct and cur_dst point at
   * the ultimate non-pointer type and its storage. For a null root the chain
   * was skipped, so ct stays the outer PTR type and the ch=='n' branch below
   * consumes the literal and leaves the user's pointer nil. */
  if (BIND_IS_ANY(ct->kind)) {
    m->c.stash.any_yield.slot = cur_dst;
    goto any_value;
  }
  if (BIND_IS_VALUE(ct->kind)) {
    BIND_DISPATCH_VALUE(cur_dst);
  }
  if (BIND_IS_DEFERRED_VALUE(ct->kind)) {
    m->c.stash.deferred_yield.slot = (uint8_t *)cur_dst;
    m->c.stash.deferred_yield.type = (BindType *)ct;
    if (m->b.alloc.deferred_drain_used + sizeof(UnmarshalRecord) > m->b.alloc.deferred_drain_cap) {
      __BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_DEFERRED_RESUME;
      m->b.yield.pending_action = BIND_YIELD_FLUSH_UNMARSHAL;
      return;
    }
    goto deferred_value;
  }
  if (ch == '"') {
    if (ct->kind != BIND_KIND_STRING && ct->kind != BIND_KIND_NUMBER)
      BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    if (bind_visit_str(&str_p, SRC_PTR(), cur_dst) < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    SRC_ADVANCE();
    goto document_end;
  }
  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    if (ct->kind == BIND_KIND_NUMBER) BIND_WRITE_NUMBER_AS_STR(cur_dst, SRC_POS(), document_end);
    BIND_WRITE_NUMBER(ct, cur_dst, SRC_POS(), document_end, BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS()));
  }
  if (ch == 'n') {
    if (bind_validate_atom(SRC_PTR(), 'n') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    SRC_ADVANCE();
    goto document_end;
  }
  if (ch == 't') {
    if (ct->kind != BIND_KIND_BOOL) BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    if (bind_validate_atom(SRC_PTR(), 't') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    *(uint8_t *)cur_dst = 1;
    SRC_ADVANCE();
    goto document_end;
  }
  if (ch == 'f') {
    if (ct->kind != BIND_KIND_BOOL) BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, SRC_POS());
    if (bind_validate_atom(SRC_PTR(), 'f') < 0) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
    *(uint8_t *)cur_dst = 0;
    SRC_ADVANCE();
    goto document_end;
  }
  BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, SRC_POS());
}

document_end: {
  m->c.str_used = (size_t)((uintptr_t)str_p - (uintptr_t)m->b.alloc.str_arena);
  m->c.phase    = BIND_PHASE_DOCUMENT_END;
  /* If a type-mismatch was recorded during skip-and-continue, promote it to the yield error now,
   * so the Go driver returns it. Syntax/EOF errors already aborted via BIND_YIELD_ERR. */
  if (m->c.first_error_kind != 0) {
    m->b.yield.pending_action = BIND_YIELD_ERROR;
    BIND_ERROR_PAYLOAD(m, m->c.first_error_kind, m->b.yield.first_error_pos, m->b.yield.first_error_pos, NULL);
  } else if (!SRC_EOF()) {
    /* Input remains past the top-level value: trailing data (e.g. "42 garbage",
     * "[1][]", "{}}"). The scan sentinel at src[len] is excluded because
     * cursor_end stops before it. */
    m->b.yield.pending_action = BIND_YIELD_ERROR;
    BIND_ERROR_PAYLOAD(m, BIND_ERR_TRAILING, SRC_POS(), SRC_POS(), NULL);
  } else {
    m->b.yield.pending_action = BIND_YIELD_NONE;
  }
  m->cursor      = cursor;
  m->c.depth     = depth;
  m->c.cur_dst   = cur_dst;
  m->c.cur_type  = cur_type;
  m->c.cur_count = cur_count;
  m->c.cur_aux   = cur_aux;
  return;
}

  /* Tape binding overlays cursor with a tape position. Nested case descent saves
   * and restores the outer cursor pair through rebind_stack. */

t_document_start: {
  if (UNLIKELY(TAP_EOF())) TAPE_BIND_YIELD_ERR(m, BIND_ERR_EOF, 0);
  uint64_t word = TAP_PEEK();
  uint8_t tag   = (uint8_t)(word >> 56);

  if (UNLIKELY(cur_type.flags | (tag == (TAPE_NULL_VAL >> 56)))) {
    if (cur_type.kind == BIND_KIND_PTR && tag != (TAPE_NULL_VAL >> 56)) {
    t_root_ptr_unwrap:
      /* BLOCK_FULL resumes below word initialization with the tape cursor
       * unadvanced, so reload the current word and tag. */
      word = TAP_PEEK();
      tag  = (uint8_t)(word >> 56);
      while (cur_type.kind == BIND_KIND_PTR) {
        uint8_t *pointee = *(uint8_t **)cur_dst;
        if (pointee == NULL) {
          int32_t ci        = cur_type.u.ptr.alloc_class;
          BindSlotClass *sc = &m->b.alloc.slot_classes[ci];
          if (sc->offset >= sc->limit) {
            __TAPE_BIND_SAVE_LOCALS(m);
            m->c.phase                = BIND_PHASE_TAPE_BIND_ROOT_UNWRAP;
            m->b.yield.pending_action = BIND_YIELD_BLOCK_FULL;
            m->b.yield.arg0           = (uint32_t)ci;
            m->b.yield.arg1           = 0;
            m->b.yield.target         = cur_dst;
            return;
          }
          pointee = sc->block + sc->offset;
          sc->offset += sc->elem_size;
          *(uint8_t **)cur_dst = pointee;
        }
        cur_dst  = pointee;
        cur_type = *(const BindType *)cur_type.child;
      }
    }
    /* A non-null Value root yields so Go can alias the complete tape subtree;
     * resume then skips it. Null clears the Value instead of publishing an alias. */
    if (BIND_IS_VALUE(cur_type.kind)) {
      if (tag == (TAPE_NULL_VAL >> 56)) {
        TAP_ADVANCE();
        __builtin_memset(cur_dst, 0, m->b.ctx.type_meta[cur_type.type_idx].size);
        goto t_document_end;
      }
      TAPE_BIND_VALUE_FIELD(m, cur_dst, BIND_PHASE_TAPE_BIND_VALUE_RESUME_ROOT);
    }
    if (BIND_IS_ANY(cur_type.kind) || BIND_IS_DEFERRED_VALUE(cur_type.kind) || cur_type.kind == BIND_KIND_NUMBER) {
      goto t_unsupported;
    }
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      BIND_NULL_ZERO(cur_dst, m, &cur_type);
      goto t_document_end;
    }
  }

  if (tag == (TAPE_START_OBJECT >> 56)) {
    if (cur_type.kind == BIND_KIND_MAP) {
      /* t_map_open requires cursor after START_OBJECT at the first key or close. */
      TAP_ADVANCE();
      TAPE_BIND_MAP_OPEN(m, &cur_type, cur_dst, bind_push_map);
    }
    TAP_ADVANCE();
    if (cur_type.kind != BIND_KIND_STRUCT) {
      TAPE_BIND_ROOT_TYPE_MISMATCH_SKIP(m, 0);
    }
    int empty = (TAP_TAG() == (TAPE_END_OBJECT >> 56));
    if (empty) {
      TAP_ADVANCE();
      if (LIKELY(!(cur_type.flags & BIND_FLAG_MAY_PHASE2))) goto t_document_end;
    }
    if (bind_push_struct(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    if (empty) goto t_object_close_drain;
    cur_aux = (void *)(uintptr_t)m->b.ctx.type_meta[cur_type.type_idx].u.strct.lookup;
    goto t_object_field;
  }
  if (tag == (TAPE_START_ARRAY >> 56)) {
    TAP_ADVANCE();
    if (cur_type.kind != BIND_KIND_SLICE && cur_type.kind != BIND_KIND_ARRAY) {
      TAPE_BIND_ROOT_TYPE_MISMATCH_SKIP(m, 0);
    }
    if (TAP_TAG() == (TAPE_END_ARRAY >> 56)) {
      TAP_ADVANCE();
      if (cur_type.kind == BIND_KIND_SLICE) BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      goto t_document_end;
    }
    /* The root array still needs a frame because its close uses the shared pop path. */
    if (bind_push(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    goto t_array_begin;
  }
  goto t_root_scalar;
}

t_object_field: {
  ndec_lookup *lookup = (ndec_lookup *)cur_aux;
  uint64_t key_word   = TAP_PEEK();
  uint8_t key_tag     = (uint8_t)(key_word >> 56);
  if (!TAPE_IS_STRING_TAG(key_tag)) {
    TAPE_BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }
  uint32_t key_len;
  const uint8_t *key_data = tape_bind_string_ptr(key_word, m->b.alloc.str_arena, src, &key_len);
  int fidx                = bind_lookup_key(lookup, key_data, key_len);
  TAP_ADVANCE();

  if (fidx < 0) {
    uint16_t iv_idx              = m->b.ctx.type_meta[cur_type.type_idx].u.strct.inline_variant_idx;
    uint32_t reserve_unknown_off = m->b.ctx.type_meta[cur_type.type_idx].u.strct.reserve_unknown_field_off;
    if (iv_idx != 0xFFFFu || reserve_unknown_off != 0xFFFFFFFF) goto t_route_field_to_variant;
    goto t_skip_value_after_key;
  }
  cur_struct_field = &((const BindField *)cur_type.child)[fidx];
  /* Inline carriers bind only at struct close. Their discriminators remain on
   * the merged tape so Value cases can observe them before phase2 classification. */
  if (cur_struct_field->flags & (BIND_FF_INLINE_VARIANT | BIND_FF_INLINE_VDISC)) goto t_route_field_to_variant;
  goto t_object_field_value;
}

t_object_field_value: {
  uint8_t *t_field_base = cur_dst;
  /* Promoted-field offsets are relative to the pointee reached by their hops. */
  if (UNLIKELY(cur_struct_field->flags & BIND_FF_VIA_PTR)) {
    uintptr_t hops = m->b.ctx.type_meta[cur_type.type_idx].u.strct.ptr_hops;
    BIND_RESOLVE_FIELD_HOPS(m, t_field_base, cur_struct_field, hops, BIND_PHASE_TAPE_BIND_OBJECT_FIELD_VALUE);
  }
  uint8_t *body      = t_field_base + cur_struct_field->offset;
  const BindType *ct = (const BindType *)cur_struct_field->type;
  uint64_t word      = TAP_PEEK();
  uint8_t tag        = (uint8_t)(word >> 56);

  /* Bind a resolvable poly case immediately; otherwise preserve the entry for phase2. */
  if (cur_struct_field->flags & (BIND_FF_VARIANT | BIND_FF_KINDOF)) {
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      poly_eface_nil(body);
      goto t_object_continue;
    }
    /* BLOCK_FULL restores the field and rederives the case at the unconsumed value. */
  t_field_value_case_lookup:
    body              = cur_dst + cur_struct_field->offset;
    word              = TAP_PEEK();
    tag               = (uint8_t)(word >> 56);
    uint16_t poly_idx = BIND_FIELD_POLY_IDX(cur_struct_field);
    PolyCase pc;
    if (cur_struct_field->flags & BIND_FF_KINDOF) {
      pc = poly_case_by_kindof(m, poly_idx, poly_kind_of_tape_tag(tag));
      if (pc.case_idx < 0) {
        __TAPE_BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DOCUMENT_END;
        m->b.yield.pending_action = BIND_YIELD_ERROR;
        BIND_ERROR_PAYLOAD(m, BIND_ERR_KINDOF_UNREGISTERED, poly_kind_of_tape_tag(tag), BIND_ERROR_NO_POS, NULL);
        return;
      }
    } else {
      int disc_bound;
      pc = poly_case_by_disc(m, poly_idx, cur_dst, str_p, &disc_bound);
      if (pc.case_idx < 0 && disc_bound) {
        __TAPE_BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_DOCUMENT_END;
        m->b.yield.pending_action = BIND_YIELD_ERROR;
        BIND_ERROR_PAYLOAD(m, BIND_ERR_VARIANT_UNKNOWN_DISC, poly_idx, BIND_ERROR_NO_POS, cur_dst);
        return;
      }
    }
    /* An unresolved discriminator defers to phase2. Resolved cases proceed under
     * the tape path's capability gate without returning to JSON binding. */
    if (pc.case_idx < 0) goto t_route_field_to_variant;

    const BindType *case_type = &m->b.ctx.types[pc.case_type_idx];
    if ((case_type->flags & BIND_FLAG_COLD) && case_type->kind != BIND_KIND_PTR &&
        case_type->kind != BIND_KIND_VALUE)
      goto t_unsupported;
    if (poly_case_slot_full(m, &pc)) {
      __TAPE_BIND_SAVE_LOCALS(m);
      m->c.stash.field_value.field = (uint8_t *)cur_struct_field;
      m->c.phase                   = BIND_PHASE_TAPE_BIND_FIELD_VALUE_CASE_RETRY;
      m->b.yield.pending_action    = BIND_YIELD_BLOCK_FULL;
      m->b.yield.arg0              = (uint32_t)pc.slot_class;
      m->b.yield.arg1              = 0;
      m->b.yield.target            = cur_dst;
      return;
    }
    /* Publish the case interface before dispatch writes through its data word. */
    body = poly_bind_target(m, body, &pc);
    ct   = case_type;
  }
  goto t_field_value_cold_gate;

  /* Pointer allocation resumes at the unconsumed tape value with body and ct no
   * longer live. Poly fields recover both from the published interface; ordinary
   * fields rederive them from the field metadata. */
t_field_value_ptr_resume: {
  if (cur_struct_field->flags & (BIND_FF_VARIANT | BIND_FF_KINDOF)) {
    uint8_t *eface          = cur_dst + cur_struct_field->offset;
    const void *rtype       = *(const void **)eface;
    uint16_t poly_idx       = BIND_FIELD_POLY_IDX(cur_struct_field);
    const BindPolyTable *pt = &m->b.ctx.polys[poly_idx];
    uint32_t n              = pt->case_count;
    const uint16_t *cti     = pt->case_type_idx;
    const void *const *crt  = pt->case_rtype;
    ct                      = (const BindType *)0;
    for (uint32_t i = 0; i < n; i++) {
      if (crt[i] == rtype) {
        ct = &m->b.ctx.types[cti[i]];
        break;
      }
    }
    if (ct == (const BindType *)0) {
      __TAPE_BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_DOCUMENT_END;
      m->b.yield.pending_action = BIND_YIELD_ERROR;
      if (cur_struct_field->flags & BIND_FF_KINDOF) {
        BIND_ERROR_PAYLOAD(m, BIND_ERR_KINDOF_UNREGISTERED, poly_kind_of_tape_tag((uint8_t)(TAP_PEEK() >> 56)),
                           BIND_ERROR_NO_POS, NULL);
      } else {
        BIND_ERROR_PAYLOAD(m, BIND_ERR_VARIANT_UNKNOWN_DISC, poly_idx, BIND_ERROR_NO_POS, cur_dst);
      }
      return;
    }
    body = poly_eface_is_direct(ct->kind) ? eface + 8 : *(uint8_t **)(eface + 8);
  } else {
    /* Rewalk promoted-field hops; allocations completed before the yield are reusable. */
    uint8_t *r_field_base = cur_dst;
    if (UNLIKELY(cur_struct_field->flags & BIND_FF_VIA_PTR)) {
      uintptr_t hops = m->b.ctx.type_meta[cur_type.type_idx].u.strct.ptr_hops;
      BIND_RESOLVE_FIELD_HOPS(m, r_field_base, cur_struct_field, hops, BIND_PHASE_TAPE_BIND_OBJECT_FIELD_VALUE);
    }
    body = r_field_base + cur_struct_field->offset;
    ct   = (const BindType *)cur_struct_field->type;
  }
  word = TAP_PEEK();
  tag  = (uint8_t)(word >> 56);
}
t_field_value_cold_gate:

  if (BIND_IS_ANY(ct->kind)) {
    TAPE_BIND_DISPATCH_ANY(body);
  }

  if (UNLIKELY(ct->flags | (tag == (TAPE_NULL_VAL >> 56)))) {
    if (ct->kind == BIND_KIND_PTR && tag != (TAPE_NULL_VAL >> 56)) {
      TAPE_BIND_RESOLVE_PTR_CHAIN(m, body, ct, tag, BIND_PHASE_TAPE_BIND_FIELD_VALUE_PTR_RESUME, 1,
                                  m->c.stash.field_value.field = (uint8_t *)cur_struct_field);
    }
    if (BIND_IS_VALUE(ct->kind)) {
      if (tag == (TAPE_NULL_VAL >> 56)) {
        TAP_ADVANCE();
        goto t_object_continue;
      }
      TAPE_BIND_VALUE_FIELD(m, body, BIND_PHASE_TAPE_BIND_VALUE_RESUME_OBJECT);
    }
    if (BIND_IS_DEFERRED_VALUE(ct->kind) || ct->kind == BIND_KIND_NUMBER) {
      goto t_unsupported;
    }
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      BIND_NULL_ZERO(body, m, ct);
      goto t_object_continue;
    }
  }

  /* QUOTED (`,string`): only a JSON string is accepted, re-parsed as the
   * target scalar. Null was already handled in the cold gate above. */
  if (cur_struct_field->flags & BIND_FF_QUOTED) {
    if (TAPE_IS_STRING_TAG(tag)) {
      uint32_t qlen;
      const uint8_t *qd = tape_bind_string_ptr(word, m->b.alloc.str_arena, src, &qlen);
      if (bind_write_quoted_scalar(&str_p, qd, qlen, ct->kind, body, m->c.atof) < 0)
        TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
      TAP_ADVANCE();
      goto t_object_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }

  if (LIKELY(ct->kind == BIND_KIND_STRING)) {
    if (TAPE_IS_STRING_TAG(tag)) {
      if (cur_struct_field->flags & BIND_FF_VDISC)
        tape_bind_copy_string_header(word, &str_p, body, m->b.alloc.str_arena, src);
      else
        tape_bind_write_string_header(word, body, m->b.alloc.str_arena, src);
      TAP_ADVANCE();
      goto t_object_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }

  switch (ct->kind) {
  case BIND_KIND_BOOL:
    if (tag == (TAPE_TRUE_VAL >> 56)) {
      *(uint8_t *)body = 1;
      TAP_ADVANCE();
      goto t_object_continue;
    }
    if (tag == (TAPE_FALSE_VAL >> 56)) {
      *(uint8_t *)body = 0;
      TAP_ADVANCE();
      goto t_object_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64:
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64:
  case BIND_KIND_FLOAT32:
  case BIND_KIND_FLOAT64: {
    TAPE_BIND_NUMBER_ARM(m, ct->kind, body, t_object_continue,
                         TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape)));
  }
  case BIND_KIND_STRUCT: {
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    uint32_t zero_size = m->b.ctx.type_meta[ct->type_idx].size;
    __builtin_memset(body, 0, zero_size);
    TAPE_BIND_DESCEND_STRUCT(body, ct, t_object_continue, bind_push_struct);
  }
  case BIND_KIND_MAP: {
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    TAP_ADVANCE();
    TAPE_BIND_MAP_OPEN(m, ct, body, bind_push_map);
  }
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY: {
    if (tag != (TAPE_START_ARRAY >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    if (bind_push(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst  = body;
    cur_type = *ct;
    TAP_ADVANCE();
    if (TAP_TAG() == (TAPE_END_ARRAY >> 56)) {
      TAP_ADVANCE();
      if (cur_type.kind == BIND_KIND_SLICE) BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      goto t_scope_end;
    }
    goto t_array_begin;
  }
  case BIND_KIND_PTR:
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  default:
    goto t_unsupported;
  }
}

t_object_continue: {
  TAP_FOLLOW_SEAMS();
  uint64_t word = TAP_PEEK();
  uint8_t tag   = (uint8_t)(word >> 56);
  if (TAPE_IS_STRING_TAG(tag)) {
    goto t_object_field;
  }
  if (tag == (TAPE_END_OBJECT >> 56)) {
    TAP_ADVANCE();
    goto t_object_close_drain;
  }
  TAPE_BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
}

/* Tape and JSON struct closes share phase2. Ordinary structs pop directly;
 * MAY_PHASE2 hosts enter only when this depth owns or requires an aux slot. */
t_object_close_drain: {
  if (LIKELY(!(cur_type.flags & BIND_FLAG_MAY_PHASE2)) ||
      !struct_needs_phase2(m, &cur_type, m->auxFrames[m->aux_depth].owner_depth == depth)) {
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    goto t_scope_end;
  }
  AUX_LAZY_ALLOC(m, TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0));
  BindAuxFrame *ax = &m->auxFrames[m->aux_depth];
  tape_build_close_or_empty(m, &ax->a);
  ax->walk    = PHASE2_FIRST_SEAM(ax->a.start);
  ax->b_count = ax->a.count; /* both views initially contain every entry */
  goto phase2_walk;
}

t_array_begin: {
  if (cur_type.kind == BIND_KIND_ARRAY) {
    cur_aux   = cur_dst;
    cur_count = 0;
    goto t_array_value;
  }
  BindType *child_type = (BindType *)cur_type.child;
  int32_t ci           = m->b.ctx.type_meta[cur_type.type_idx].u.slice.alloc_class;
  BindSlotClass *sc    = &m->b.alloc.slot_classes[ci];
  uint32_t child_size  = child_type->u.slice.child_size;
  if (sc->mode == BIND_SLOT_RECBATCH) {
    void *bk = recbatch_alloc(sc, 0);
    if (UNLIKELY(bk == NULL)) {
      __TAPE_BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_TAPE_BIND_ARRAY_VALUE;
      m->b.yield.pending_action = BIND_YIELD_RECBATCH_REFILL;
      m->b.yield.arg0           = cur_type.type_idx;
      m->b.yield.arg1           = 0;
      m->b.yield.target         = cur_dst;
      return;
    }
    *(void **)cur_dst           = bk;
    *(intptr_t *)(cur_dst + 8)  = 0;
    *(intptr_t *)(cur_dst + 16) = 1;
    cur_aux                     = bk;
  } else {
    if (sc->offset >= sc->limit) {
      __TAPE_BIND_SAVE_LOCALS(m);
      m->c.phase                = BIND_PHASE_TAPE_BIND_ARRAY_VALUE;
      m->b.yield.pending_action = BIND_YIELD_SLICE_GROW;
      m->b.yield.arg0           = cur_type.type_idx;
      m->b.yield.arg1           = 0;
      m->b.yield.target         = cur_dst;
      return;
    }
    void *bk                    = sc->block + sc->offset;
    *(void **)cur_dst           = bk;
    *(intptr_t *)(cur_dst + 8)  = 0;
    *(intptr_t *)(cur_dst + 16) = (intptr_t)(sc->cap - sc->len);
    cur_aux                     = bk;
    sc->offset                  = sc->limit; /* charge before writing; close returns the tail */
  }
  cur_count = 0;
  goto t_array_value;
}

t_array_value: {
  if (cur_type.kind == BIND_KIND_ARRAY) {
    uint32_t array_len = m->b.ctx.type_meta[cur_type.type_idx].u.array.array_len;
    if (cur_count >= array_len) goto t_skip_value_array;
  } else {
    intptr_t cap_field;
    __builtin_memcpy(&cap_field, cur_dst + 16, sizeof(cap_field));
    if (cur_count == (uint32_t)cap_field) {
      int32_t alloc_class = m->b.ctx.type_meta[cur_type.type_idx].u.slice.alloc_class;
      BindSlotClass *sc   = &m->b.alloc.slot_classes[alloc_class];
      if (UNLIKELY(sc->mode == BIND_SLOT_RECBATCH)) {
        /* RecBatch growth must remain in its matrix or retained bypass path;
         * SLICE_GROW is valid only for bump mode. */
        intptr_t next_cap = cap_field ? cap_field * 2 : 1;
        if (UNLIKELY(next_cap > (intptr_t)BIND_RECBATCH_MAX_CAP)) {
          __TAPE_BIND_SAVE_LOCALS(m);
          m->c.phase                = BIND_PHASE_TAPE_BIND_ARRAY_VALUE;
          m->b.yield.pending_action = BIND_YIELD_RECBATCH_BYPASS;
          m->b.yield.arg0           = (uint32_t)cur_type.type_idx;
          m->b.yield.arg1           = (uint32_t)next_cap;
          m->b.yield.target         = cur_dst;
          return;
        }
        uint32_t row_idx = recbatch_row_idx((uint32_t)next_cap);
        void *bk         = recbatch_alloc(sc, row_idx);
        if (UNLIKELY(bk == NULL)) {
          __TAPE_BIND_SAVE_LOCALS(m);
          m->c.phase                = BIND_PHASE_TAPE_BIND_ARRAY_VALUE;
          m->b.yield.pending_action = BIND_YIELD_RECBATCH_REFILL;
          m->b.yield.arg0           = (uint32_t)cur_type.type_idx;
          m->b.yield.arg1           = row_idx;
          m->b.yield.target         = cur_dst;
          return;
        }
        const uint8_t *old_data;
        __builtin_memcpy(&old_data, cur_dst, sizeof(old_data));
        size_t byte_len = (size_t)((uint8_t *)cur_aux - old_data);
        if (byte_len > 0) __builtin_memmove(bk, old_data, byte_len);
        *(void **)(cur_dst)         = bk;
        *(intptr_t *)(cur_dst + 16) = next_cap;
        cur_aux                     = (uint8_t *)bk + byte_len;
        recbatch_free(sc, (void *)old_data, (uint32_t)cap_field);
      } else {
        __TAPE_BIND_SAVE_LOCALS(m);
        m->c.phase                = BIND_PHASE_TAPE_BIND_ARRAY_VALUE;
        m->b.yield.pending_action = BIND_YIELD_SLICE_GROW;
        m->b.yield.arg0           = cur_type.type_idx;
        m->b.yield.arg1           = 0;
        m->b.yield.target         = cur_dst;
        return;
      }
    }
  }

  uint8_t *body              = (uint8_t *)cur_aux;
  const BindType *child_type = (const BindType *)cur_type.child;
  uint64_t word              = TAP_PEEK();
  uint8_t tag                = (uint8_t)(word >> 56);

  if (BIND_IS_ANY(child_type->kind)) {
    TAPE_BIND_DISPATCH_ANY(body);
  }

  if (UNLIKELY(child_type->flags | (tag == (TAPE_NULL_VAL >> 56)))) {
    if (child_type->kind == BIND_KIND_PTR && tag != (TAPE_NULL_VAL >> 56)) {
      const BindType *ct = child_type;
      TAPE_BIND_RESOLVE_PTR_CHAIN(m, body, ct, tag, BIND_PHASE_TAPE_BIND_ARRAY_VALUE, 0, (void)0);
      child_type = ct;
    }
    if (BIND_IS_VALUE(child_type->kind)) {
      if (tag == (TAPE_NULL_VAL >> 56)) {
        TAP_ADVANCE();
        goto t_array_continue;
      }
      TAPE_BIND_VALUE_FIELD(m, body, BIND_PHASE_TAPE_BIND_VALUE_RESUME_ARRAY);
    }
    if (BIND_IS_DEFERRED_VALUE(child_type->kind) || child_type->kind == BIND_KIND_NUMBER) {
      goto t_unsupported;
    }
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      BIND_NULL_ZERO(body, m, child_type);
      goto t_array_continue;
    }
  }

  if (LIKELY(child_type->kind == BIND_KIND_STRING)) {
    if (TAPE_IS_STRING_TAG(tag)) {
      tape_bind_write_string_header(word, body, m->b.alloc.str_arena, src);
      TAP_ADVANCE();
      goto t_array_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }

  switch (child_type->kind) {
  case BIND_KIND_BOOL:
    if (tag == (TAPE_TRUE_VAL >> 56)) {
      *(uint8_t *)body = 1;
      TAP_ADVANCE();
      goto t_array_continue;
    }
    if (tag == (TAPE_FALSE_VAL >> 56)) {
      *(uint8_t *)body = 0;
      TAP_ADVANCE();
      goto t_array_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64:
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64:
  case BIND_KIND_FLOAT32:
  case BIND_KIND_FLOAT64: {
    TAPE_BIND_NUMBER_ARM(m, child_type->kind, body, t_array_continue,
                         TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape)));
  }
  case BIND_KIND_STRUCT: {
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    uint32_t zero_size = m->b.ctx.type_meta[child_type->type_idx].size;
    __builtin_memset(body, 0, zero_size);
    TAP_ADVANCE();
    int empty = (TAP_TAG() == (TAPE_END_OBJECT >> 56));
    if (empty) {
      TAP_ADVANCE();
      if (!(child_type->flags & BIND_FLAG_MAY_PHASE2)) goto t_array_continue;
    }
    if (bind_push_array_or_slice(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst  = body;
    cur_type = *child_type;
    if (empty) goto t_object_close_drain;
    cur_aux = (void *)(uintptr_t)m->b.ctx.type_meta[cur_type.type_idx].u.strct.lookup;
    goto t_object_field;
  }
  case BIND_KIND_MAP: {
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    if (bind_push_array_or_slice(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst  = body;
    cur_type = *child_type;
    TAP_ADVANCE();
    goto t_map_open;
  }
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY: {
    /* The parent is an array or slice, so its specialized frame preserves the
     * outer slot cursor while the nested element descends. */
    if (tag != (TAPE_START_ARRAY >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    if (bind_push_array_or_slice(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = body;
    cur_type  = *child_type;
    cur_count = 0;
    TAP_ADVANCE();
    if (TAP_TAG() == (TAPE_END_ARRAY >> 56)) {
      TAP_ADVANCE();
      if (cur_type.kind == BIND_KIND_SLICE) BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      goto t_scope_end;
    }
    goto t_array_begin;
  }
  case BIND_KIND_PTR:
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  default:
    goto t_unsupported;
  }
}

t_array_continue: {
  cur_count++;
  cur_aux = (uint8_t *)cur_aux + cur_type.u.slice.child_size;
  TAP_FOLLOW_SEAMS();
  uint64_t word = TAP_PEEK();
  uint8_t tag   = (uint8_t)(word >> 56);
  if (tag == (TAPE_END_ARRAY >> 56)) {
    TAP_ADVANCE();
    if (cur_type.kind == BIND_KIND_SLICE) {
      *(intptr_t *)(cur_dst + 8)  = (intptr_t)cur_count;
      *(intptr_t *)(cur_dst + 16) = (intptr_t)cur_count;
      int32_t ci                  = m->b.ctx.type_meta[cur_type.type_idx].u.slice.alloc_class;
      BindSlotClass *sc           = &m->b.alloc.slot_classes[ci];
      if (sc->mode == BIND_SLOT_BUMP) {
        /* Reclaim a bump tail only when the slice backing belongs to the
         * current block. A standalone bypass cursor must not update the
         * SlotClass offset or length. */
        const uint8_t *data;
        __builtin_memcpy(&data, cur_dst, sizeof(data));
        uint32_t off = (uint32_t)(data - sc->block);
        if (off < sc->limit) {
          sc->offset = (uint32_t)((uint8_t *)cur_aux - sc->block);
          sc->len += cur_count;
        }
      }
    }
    /* Generic pop: parent may be STRUCT (slice field of a struct, pushed via
     * bind_push in t_object_field_value) or SLICE/ARRAY (nested array, pushed
     * via bind_push_array_or_slice in t_array_value). The kind-tagged union
     * is restored correctly by bind_pop's kind dispatch. */
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    goto t_scope_end;
  }
  /* Sync Len to cur_count so a SLICE_GROW yield at the next array_value
   * sees the current element count (matches main bind.h's `,`-sync). Without
   * this, ServeSliceGrow would treat the slice as first-time-alloc and skip
   * memmove, losing already-written elements. */
  if (cur_type.kind == BIND_KIND_SLICE) {
    *(intptr_t *)(cur_dst + 8) = (intptr_t)cur_count;
  }
  goto t_array_value;
}

t_map_open: {
  uint32_t stride      = m->b.ctx.type_meta[cur_type.type_idx].u.map.stride;
  uint32_t region_size = BIND_MAP_REGION_HEADER_SIZE + BIND_MAP_REGION_SLOTS * stride;
  if (m->b.alloc.map_buf_used + region_size > m->b.alloc.map_buf_cap) {
    TAPE_BIND_YIELD_FLUSH_MAP(m, 0, 0, NULL, BIND_PHASE_TAPE_BIND_MAP_OPEN_RETRY);
  }
  int32_t ci        = cur_type.u.map.alloc_class;
  BindSlotClass *sc = &m->b.alloc.slot_classes[ci];
  if (sc->offset >= sc->limit) {
    TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, ci, 0, BIND_PHASE_TAPE_BIND_MAP_OPEN_RETRY);
  }
  void *map_hdr = *(void **)(sc->block + sc->offset);
  sc->offset += sc->elem_size;
  *(void **)cur_dst = map_hdr;

  BindMapRegionHeader *region = (BindMapRegionHeader *)(m->b.alloc.map_buf + m->b.alloc.map_buf_used);
  m->b.alloc.map_buf_used += region_size;
  region->stride         = stride;
  region->next_entry_off = 0;
  region->entry_count    = 0;
  region->type_idx       = cur_type.type_idx;
  region->hmap           = map_hdr;
  region->parent_slot    = cur_dst;
  cur_aux                = (void *)region;
  cur_count              = 0;

  {
    BindFrame *f     = &frames[depth];
    f->dst           = cur_dst;
    f->kind          = cur_type.kind;
    f->type_idx      = cur_type.type_idx;
    f->cs.child_size = cur_type.u.raw;
    f->child_type    = (const void *)cur_type.child;
    f->u.map_region  = region;
  }

  if (TAP_TAG() == (TAPE_END_OBJECT >> 56)) {
    TAP_ADVANCE();
    frames[depth].u.map_region = NULL;
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    goto t_scope_end;
  }
  goto t_map_key;
}

t_map_key: {
  BindMapRegionHeader *region = (BindMapRegionHeader *)cur_aux;
  uint32_t stride             = region->stride;
  if (region->next_entry_off + stride > BIND_MAP_REGION_SLOTS * stride) {
    /* Flushing may relocate this region. Resume re-derives cur_aux from the
     * map frame and stages the still-unconsumed key in the compacted region. */
    TAPE_BIND_YIELD_FLUSH_MAP(m, region->entry_count, 0, (uint8_t *)region, BIND_PHASE_TAPE_BIND_MAP_CONTINUE);
  }
  uint8_t *entry = (uint8_t *)region + BIND_MAP_REGION_HEADER_SIZE + region->next_entry_off;
  region->next_entry_off += stride;

  uint64_t key_word = TAP_PEEK();
  uint8_t key_tag   = (uint8_t)(key_word >> 56);
  if (!TAPE_IS_STRING_TAG(key_tag)) {
    TAPE_BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }
  TAP_ADVANCE();
  tape_bind_write_string_header(key_word, entry, m->b.alloc.str_arena, src);
  goto t_map_value;
}

t_map_value: {
  BindMapRegionHeader *region = (BindMapRegionHeader *)cur_aux;
  uint8_t *body = (uint8_t *)region + BIND_MAP_REGION_HEADER_SIZE + (region->next_entry_off - region->stride) +
                  BIND_MAP_VAL_OFF;
  const BindType *child_type = (const BindType *)cur_type.child;
  uint64_t word              = TAP_PEEK();
  uint8_t tag                = (uint8_t)(word >> 56);

  if (BIND_IS_ANY(child_type->kind)) {
    TAPE_BIND_DISPATCH_ANY(body);
  }

  if (UNLIKELY(child_type->flags | (tag == (TAPE_NULL_VAL >> 56)))) {
    /* Snapshot the KV slot before the PTR chain moves body to the pointee, so the
     * redirect below can tell a non-PTR value (body still the slot) from a PTR one
     * (body is the pointee, already in scannable storage). */
    uint8_t *orig_slot = body;
    if (child_type->kind == BIND_KIND_PTR && tag != (TAPE_NULL_VAL >> 56)) {
      const BindType *ct = child_type;
      TAPE_BIND_RESOLVE_PTR_CHAIN(m, body, ct, tag, BIND_PHASE_TAPE_BIND_MAP_VALUE, 0, (void)0);
      child_type = ct;
    }
    /* A map value that can receive heap pointers cannot live in the noscan KV
     * buffer, so the drain reads its slot as a pointer to scannable storage
     * (MapDrainInfo.val_is_deferred, set for KindValue and for any aggregate whose
     * field tree reaches one). Carve that storage and leave its address in the
     * slot, exactly as map_value does on the JSON path; the arms below then write
     * through body into the intermediate rather than into the buffer.
     * BLOCK_FULL leaves the cursor unchanged; resume re-derives body from the
     * region and repeats the redirect before dispatch. */
    if (body == orig_slot &&
        (BIND_IS_VALUE(child_type->kind) || (child_type->flags & BIND_FLAG_CONTAINS_DEFERRED))) {
      const BindMapDrainInfo *di =
          (const BindMapDrainInfo *)m->b.ctx.type_meta[cur_type.type_idx].u.map.drain_info;
      BindSlotClass *vsc = &m->b.alloc.slot_classes[di->val_slot_class];
      if (vsc->offset >= vsc->limit) {
        TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)di->val_slot_class, 0, BIND_PHASE_TAPE_BIND_MAP_VALUE);
      }
      uint8_t *val_slot = vsc->block + vsc->offset;
      vsc->offset += vsc->elem_size;
      /* The intermediate is recycled across entries and parses, so zero it before
       * the arms write: a null value, or a struct whose fields the input omits,
       * would otherwise publish whatever the previous occupant left. */
      __builtin_memset(val_slot, 0, m->b.ctx.type_meta[child_type->type_idx].size);
      *(void **)body = val_slot;
      body           = val_slot;
    }
    if (BIND_IS_VALUE(child_type->kind)) {
      if (tag == (TAPE_NULL_VAL >> 56)) {
        TAP_ADVANCE();
        goto t_map_continue;
      }
      TAPE_BIND_VALUE_FIELD(m, body, BIND_PHASE_TAPE_BIND_VALUE_RESUME_MAP);
    }
    if (BIND_IS_DEFERRED_VALUE(child_type->kind) || child_type->kind == BIND_KIND_NUMBER) {
      goto t_unsupported;
    }
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      BIND_NULL_ZERO(body, m, child_type);
      goto t_map_continue;
    }
  }

  if (LIKELY(child_type->kind == BIND_KIND_STRING)) {
    if (TAPE_IS_STRING_TAG(tag)) {
      tape_bind_write_string_header(word, body, m->b.alloc.str_arena, src);
      TAP_ADVANCE();
      goto t_map_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }

  switch (child_type->kind) {
  case BIND_KIND_BOOL:
    if (tag == (TAPE_TRUE_VAL >> 56)) {
      *(uint8_t *)body = 1;
      TAP_ADVANCE();
      goto t_map_continue;
    }
    if (tag == (TAPE_FALSE_VAL >> 56)) {
      *(uint8_t *)body = 0;
      TAP_ADVANCE();
      goto t_map_continue;
    }
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64:
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64:
  case BIND_KIND_FLOAT32:
  case BIND_KIND_FLOAT64: {
    TAPE_BIND_NUMBER_ARM(m, child_type->kind, body, t_map_continue,
                         TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape)));
  }
  case BIND_KIND_STRUCT: {
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    uint32_t zero_size = m->b.ctx.type_meta[child_type->type_idx].size;
    __builtin_memset(body, 0, zero_size);
    TAP_ADVANCE();
    int empty = (TAP_TAG() == (TAPE_END_OBJECT >> 56));
    if (empty) {
      TAP_ADVANCE();
      if (!(child_type->flags & BIND_FLAG_MAY_PHASE2)) goto t_map_continue;
    }
    if (bind_push_map(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst  = body;
    cur_type = *child_type;
    if (empty) goto t_object_close_drain;
    cur_aux = (void *)(uintptr_t)m->b.ctx.type_meta[cur_type.type_idx].u.strct.lookup;
    goto t_object_field;
  }
  case BIND_KIND_MAP: {
    /* Preserve the parent map frame before the nested map replaces cur_dst,
     * cur_type, and cur_aux. */
    if (tag != (TAPE_START_OBJECT >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    if (bind_push_map(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst  = body;
    cur_type = *child_type;
    TAP_ADVANCE();
    goto t_map_open;
  }
  case BIND_KIND_SLICE:
  case BIND_KIND_ARRAY: {
    /* Preserve the parent map frame while the nested array or slice owns the
     * current destination and type. */
    if (tag != (TAPE_START_ARRAY >> 56))
      TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    if (bind_push_map(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = body;
    cur_type  = *child_type;
    cur_count = 0;
    TAP_ADVANCE();
    if (TAP_TAG() == (TAPE_END_ARRAY >> 56)) {
      TAP_ADVANCE();
      if (cur_type.kind == BIND_KIND_SLICE) BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      goto t_scope_end;
    }
    goto t_array_begin;
  }
  case BIND_KIND_PTR:
    TAPE_BIND_TYPE_MISMATCH_SKIP(m, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  default:
    goto t_unsupported;
  }
}

t_map_continue: {
  BindMapRegionHeader *region = (BindMapRegionHeader *)cur_aux;
  region->entry_count++;
  TAP_FOLLOW_SEAMS();
  uint64_t word = TAP_PEEK();
  uint8_t tag   = (uint8_t)(word >> 56);
  if (TAPE_IS_STRING_TAG(tag)) {
    goto t_map_key;
  }
  if (tag == (TAPE_END_OBJECT >> 56)) {
    TAP_ADVANCE();
    /* Clear the map's own frame so the drain treats its region as dead. Use
     * generic bind_pop because the parent may not be a map and its union slot
     * must be interpreted according to its own kind. */
    frames[depth].u.map_region = NULL;
    bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
    goto t_scope_end;
  }
  TAPE_BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
}

t_map_key_resume: {
  /* FLUSH_MAP may move the region. The map's own frame at frames[depth] is
   * updated by compaction and is the authoritative cur_aux for the retry. */
  cur_aux = (void *)frames[depth].u.map_region;
  goto t_map_key;
}

t_scope_end: {
  /* tape_bind_base_depth identifies the active walk's root close and routes
   * completion through its state branch. Below that boundary, bind_pop has
   * restored the immediate parent in cur_type; frames[depth-1] would describe
   * the grandparent and cannot select the continuation. */
  if (depth == m->tape_bind_base_depth) goto t_document_end;
  if (depth == 0) goto t_document_end;
  switch (cur_type.kind) {
  case BIND_KIND_STRUCT:
    goto t_object_continue;
  case BIND_KIND_MAP:
    goto t_map_continue;
  default:
    goto t_array_continue; /* SLICE / ARRAY */
  }
}

t_root_scalar: {
  uint64_t word = TAP_PEEK();
  uint8_t tag   = (uint8_t)(word >> 56);
  if (UNLIKELY(cur_type.flags | (tag == (TAPE_NULL_VAL >> 56)))) {
    if (cur_type.kind == BIND_KIND_PTR && tag != (TAPE_NULL_VAL >> 56)) goto t_root_ptr_unwrap;
    if (BIND_IS_ANY(cur_type.kind) || BIND_IS_DEFERRED_VALUE(cur_type.kind) || cur_type.kind == BIND_KIND_NUMBER) {
      goto t_unsupported;
    }
    if (tag == (TAPE_NULL_VAL >> 56)) {
      TAP_ADVANCE();
      BIND_NULL_ZERO(cur_dst, m, &cur_type);
      goto t_document_end;
    }
  }
  if (LIKELY(cur_type.kind == BIND_KIND_STRING)) {
    if (TAPE_IS_STRING_TAG(tag)) {
      tape_bind_write_string_header(word, cur_dst, m->b.alloc.str_arena, src);
      TAP_ADVANCE();
      goto t_document_end;
    }
    TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  }
  switch (cur_type.kind) {
  case BIND_KIND_BOOL:
    if (tag == (TAPE_TRUE_VAL >> 56)) {
      *(uint8_t *)cur_dst = 1;
      TAP_ADVANCE();
      goto t_document_end;
    }
    if (tag == (TAPE_FALSE_VAL >> 56)) {
      *(uint8_t *)cur_dst = 0;
      TAP_ADVANCE();
      goto t_document_end;
    }
    TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
  case BIND_KIND_INT:
  case BIND_KIND_INT8:
  case BIND_KIND_INT16:
  case BIND_KIND_INT32:
  case BIND_KIND_INT64:
  case BIND_KIND_UINT:
  case BIND_KIND_UINT8:
  case BIND_KIND_UINT16:
  case BIND_KIND_UINT32:
  case BIND_KIND_UINT64:
  case BIND_KIND_FLOAT32:
  case BIND_KIND_FLOAT64: {
    TAPE_BIND_NUMBER_ARM(
        m, cur_type.kind, cur_dst, t_document_end,
        TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape)));
  }
  default:
    goto t_unsupported;
  }
}

t_skip_value:
t_skip_value_after_key: {
  TAP_SKIP_VALUE();
  goto t_object_continue;
}

/* t_route_field_to_variant: the tape-bind counterpart of object_field_tape. Copies
 * one (key, value) entry from the input tape onto the merged tape instead of
 * parsing it from source bytes.
 *
 * Three kinds of field arrive here, and phase1 deliberately does not resolve them
 * in place: an unknown host key (an inline case's field, or one the reserve-unknown
 * should collect), the reserve-unknown carrier itself, and an inline variant's
 * discriminator. Telling an inline case's field from an unknown one needs the case;
 * the case needs the discriminator; and the discriminator is an ordinary host field
 * that may appear last. phase2 breaks that cycle once at struct close, when the
 * discriminator is bound and every entry is decidable.
 *
 * Entered with cursor at the VALUE word and the key word immediately before it,
 * matching t_object_field_value's convention. */
t_route_field_to_variant: {
  AUX_LAZY_ALLOC(m, TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0));
  TapeBuild *a = &m->auxFrames[m->aux_depth].a;
  tape_build_open(m, a);
  const uint64_t *val_start = TAP_CURSOR;
  const uint64_t *val_end   = tape_value_end(val_start, TAP_VIEW());
  tape_build_copy_entry(m, a, val_start[-1], val_start, (uint32_t)(val_end - val_start),
                        (uint32_t)(val_start - m->b.alloc.value_tape));
  cursor.tape = val_end;
  goto t_object_continue;
}
t_skip_value_array: {
  TAP_SKIP_VALUE();
  goto t_array_continue;
}

/* Value field resume: Go aliased the source tape into the Value (tidx=sub_start).
 * TAP_CURSOR is at the sub-tree start (saved before the yield); skip past it and
 * continue the enclosing container walk. */
t_value_resume_object: {
  TAP_SKIP_VALUE();
  goto t_object_continue;
}
t_value_resume_array: {
  TAP_SKIP_VALUE();
  goto t_array_continue;
}
t_value_resume_map: {
  TAP_SKIP_VALUE();
  goto t_map_continue;
}
/* Root value resume: Go aliased the source tape into the root Value (tidx=0).
 * TAP_CURSOR is at the sub-tree start (the root); step past it and reach
 * t_document_end, which either completes a cold-start UnmarshalValue or returns a
 * case descent to the merged-tape pass. */
t_value_resume_root: {
  /* tape_value_end, not tape_skip_value: this walk is finishing, not continuing to
   * a sibling. A seam may follow the value (the merged tape reserves one after
   * every entry), and skipping it would carry the cursor past cursor_end and read
   * as trailing data. */
  cursor.tape = tape_value_end(TAP_CURSOR, TAP_VIEW());
  goto t_document_end;
}

/* Tape binding boxes scalars with the shared typed SlotClasses and descends
 * into registered []any or map[string]any container types. Allocation retries
 * re-read the unconsumed tape tag. Container closes return through t_scope_end;
 * scalar and empty-array paths use T_ANY_RETURN. */
#define T_ANY_RETURN()                                                                                            \
  do {                                                                                                            \
    if (depth == m->tape_bind_base_depth) goto t_document_end;                                                    \
    switch (cur_type.kind) {                                                                                      \
    case BIND_KIND_STRUCT:                                                                                        \
      goto t_object_continue;                                                                                     \
    case BIND_KIND_SLICE:                                                                                         \
    case BIND_KIND_ARRAY:                                                                                         \
      goto t_array_continue;                                                                                      \
    case BIND_KIND_MAP:                                                                                           \
      goto t_map_continue;                                                                                        \
    }                                                                                                             \
    TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));               \
  } while (0)
t_any_value: {
  const BindAnyMeta *am = (const BindAnyMeta *)m->b.ctx.types[m->b.ctx.any_type_idx].child;
  uint8_t *any_slot     = m->c.stash.any_yield.slot;
  uint64_t word         = TAP_PEEK();
  uint8_t tag           = (uint8_t)(word >> 56);

  /* JSON number to float64, matching encoding/json's default. The tape stores
   * l/u/d as [tag word][value word]. UseNumber cannot apply uniformly because
   * integer tags retain no source text; honoring only d would make the result
   * depend on the producer's numeric tag choice. TAPE_NUM_RAW always carries the
   * exact text and is handled below. */
  if (tag == (TAPE_INT64 >> 56) || tag == (TAPE_UINT64 >> 56) || tag == (TAPE_DOUBLE >> 56)) {
    BindSlotClass *sc = &m->b.alloc.slot_classes[am->float64_slot_class];
    if (sc->offset >= sc->limit)
      TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)am->float64_slot_class, 0,
                      BIND_PHASE_TAPE_BIND_ANY_RESUME);
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    uint64_t v = TAP_READ_NUMBER();
    double dv;
    if (tag == (TAPE_DOUBLE >> 56)) __builtin_memcpy(&dv, &v, 8);
    else if (tag == (TAPE_INT64 >> 56)) {
      int64_t i;
      __builtin_memcpy(&i, &v, 8);
      dv = (double)i;
    } else
      dv = (double)(uint64_t)v; /* UINT64 */
    *(double *)data                = dv;
    *(const void **)any_slot       = am->float64_type;
    *(const void **)(any_slot + 8) = data;
    T_ANY_RETURN();
  }
  /* A number kept as source text. Both boxings are available here, so this arm
   * mirrors the JSON path's any_value exactly: UseNumber yields json.Number over
   * the text, otherwise float64 over its nearest double. Without this the same
   * document decoded through the tape and through JSON would disagree purely on
   * key order, which is what the two paths must never do.
   *
   * The text already sits in str_arena, so json.Number aliases it in place rather
   * than copying as the JSON path must. */
  if (tag == (TAPE_NUM_RAW >> 56)) {
    int use_number          = (m->b.ctx.opt_flags & BIND_OPT_USE_NUMBER) != 0;
    uint32_t slot_class_idx = use_number ? (uint32_t)am->string_slot_class : (uint32_t)am->float64_slot_class;
    BindSlotClass *sc       = &m->b.alloc.slot_classes[slot_class_idx];
    if (sc->offset >= sc->limit)
      TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, slot_class_idx, 0, BIND_PHASE_TAPE_BIND_ANY_RESUME);
    uint32_t nlen;
    const uint8_t *ntext = tape_bind_string_ptr(word, m->b.alloc.str_arena, src, &nlen);
    double dv;
    if (!use_number) {
      /* Decided before the slot is carved and before the cursor moves, so a
       * rejection leaves both untouched. */
      if (UNLIKELY(ndec_parse_double(ntext, nlen, &dv, m->c.atof)))
        TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
      /* Past float64's range. The JSON path rejects the same source for a float64
       * target, so this is parity rather than a tape-specific limit. */
      if (UNLIKELY(!__builtin_isfinite(dv)))
        TAPE_BIND_YIELD_ERR(m, BIND_ERR_TYPE_MISMATCH, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
    }
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    if (use_number) {
      bind_write_str_header(data, ntext, nlen);
    } else {
      *(double *)data = dv;
    }
    *(const void **)any_slot       = use_number ? am->number_type : am->float64_type;
    *(const void **)(any_slot + 8) = data;
    TAP_ADVANCE();
    T_ANY_RETURN();
  }
  /* JSON string -> Go string (16B header carved from string_slot_class). */
  if (TAPE_IS_STRING_TAG(tag)) {
    BindSlotClass *sc = &m->b.alloc.slot_classes[am->string_slot_class];
    if (sc->offset >= sc->limit)
      TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)am->string_slot_class, 0,
                      BIND_PHASE_TAPE_BIND_ANY_RESUME);
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    tape_bind_write_string_header(word, data, m->b.alloc.str_arena, src);
    TAP_ADVANCE();
    *(const void **)any_slot       = am->string_type;
    *(const void **)(any_slot + 8) = data;
    T_ANY_RETURN();
  }
  if (tag == (TAPE_TRUE_VAL >> 56)) {
    *(const void **)any_slot       = am->bool_type;
    *(const void **)(any_slot + 8) = (const void *)am->static_true;
    TAP_ADVANCE();
    T_ANY_RETURN();
  }
  if (tag == (TAPE_FALSE_VAL >> 56)) {
    *(const void **)any_slot       = am->bool_type;
    *(const void **)(any_slot + 8) = (const void *)am->static_false;
    TAP_ADVANCE();
    T_ANY_RETURN();
  }
  if (tag == (TAPE_NULL_VAL >> 56)) {
    *(const void **)any_slot       = am->nil_type;
    *(const void **)(any_slot + 8) = NULL;
    TAP_ADVANCE();
    T_ANY_RETURN();
  }
  /* Publish the []any eface before descending into its slice-header slot. */
  if (tag == (TAPE_START_ARRAY >> 56)) {
    BindSlotClass *sc = &m->b.alloc.slot_classes[am->slice_slot_class];
    if (sc->offset >= sc->limit)
      TAPE_BIND_YIELD(m, BIND_YIELD_BLOCK_FULL, (uint32_t)am->slice_slot_class, 0,
                      BIND_PHASE_TAPE_BIND_ANY_RESUME);
    uint8_t *data = sc->block + sc->offset;
    sc->offset += sc->elem_size;
    *(const void **)any_slot       = am->slice_type;
    *(const void **)(any_slot + 8) = data;
    if (bind_push(frames, &depth, cur_dst, cur_type, cur_count, cur_aux))
      TAPE_BIND_YIELD_ERR_NO_POS(m, BIND_ERR_DEPTH, 0);
    cur_dst   = data;
    cur_type  = types[am->slice_any_type_idx];
    cur_count = 0;
    TAP_ADVANCE();
    if (TAP_TAG() == (TAPE_END_ARRAY >> 56)) {
      TAP_ADVANCE();
      BIND_WRITE_EMPTY_SLICE(cur_dst, m, cur_type.type_idx);
      bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
      T_ANY_RETURN();
    }
    goto t_array_begin;
  }
  /* JSON object -> map[string]any. Publish eface._type; TAPE_BIND_MAP_OPEN
   * writes the fresh *hmap into eface.data (any_slot+8) and descends into
   * t_map_open. Consume '{' first: t_map_open does not consume it itself
   * (matching the typed-map caller in t_object_field_value). Map close pops
   * and routes via t_scope_end. */
  if (tag == (TAPE_START_OBJECT >> 56)) {
    *(const void **)any_slot = am->map_type;
    TAP_ADVANCE();
    TAPE_BIND_MAP_OPEN(m, &types[am->map_any_type_idx], any_slot + 8, bind_push);
  }
  TAPE_BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
#undef T_ANY_RETURN
}

t_unsupported: {
  TAPE_BIND_YIELD_ERR(m, BIND_ERR_UNSUPPORTED_TAG, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape));
}

/* t_document_end: a tape-bind walk finished. Three outcomes: an error goes back
 * to Go; a cold-start root (UnmarshalValue) is simply done; a case descent hands
 * control back to the merged-tape pass that started it. */
t_document_end: {
  m->c.str_used = (size_t)((uintptr_t)str_p - (uintptr_t)m->b.alloc.str_arena);
  if (m->c.first_error_kind != 0) {
    m->b.yield.pending_action = BIND_YIELD_ERROR;
    BIND_ERROR_PAYLOAD(m, m->c.first_error_kind, m->b.yield.first_error_pos, BIND_ERROR_NO_POS, NULL);
    return;
  }
  if (!TAP_EOF()) {
    m->b.yield.pending_action = BIND_YIELD_ERROR;
    BIND_ERROR_PAYLOAD(m, BIND_ERR_TRAILING, (uint32_t)(TAP_CURSOR - m->b.alloc.value_tape), BIND_ERROR_NO_POS,
                       NULL);
    return;
  }
  if (m->rebind_top == 0) {
    m->b.yield.pending_action = BIND_YIELD_NONE;
    return;
  }
  /* A case descent closed. Release the case's own aux slot if it claimed one,
   * then undo the descent: bind_pop restores the host's hot locals and
   * rebind_stack restores the cursor pair and tape base. */
  if (m->auxFrames[m->aux_depth].owner_depth == depth) AUX_RELEASE(m);
  bind_pop(frames, &depth, &cur_dst, &cur_type, &cur_count, &cur_aux);
  BindAuxRebind *rb       = &m->rebind_stack[--m->rebind_top];
  cursor                  = rb->saved_cursor;
  m->cursor               = cursor;
  m->cursor_end           = rb->saved_cursor_end;
  m->b.alloc.value_tape   = rb->saved_value_tape;
  m->tape_bind_base_depth = rb->saved_base_depth;
  m->tape_view_mode       = rb->saved_view_mode;
  if (rb->return_phase == BIND_PHASE_VARIANT_REBIND_RESUME) goto phase2_walk;
  if (rb->return_phase == BIND_PHASE_VARIANT_INLINE_RESUME) goto phase2_done;
  goto t_scope_end;
}
}

#undef SRC_PEEK
#undef SRC_PTR
#undef SRC_POS
#undef SRC_EOF
#undef SRC_ADVANCE_PTR
#undef SRC_ADVANCE_CHAR
#undef SRC_ADVANCE
#undef SRC_EXPECT
#undef SRC_ACCEPT
#undef BIND_YIELD
#undef BIND_YIELD_ERR
#undef BIND_YIELD_ERR_NO_POS
#undef BIND_ERROR_PAYLOAD
#undef BIND_ERROR_NO_POS
#undef BIND_MAP_OPEN
#undef BIND_YIELD_FLUSH_MAP
#undef BIND_RESOLVE_PTR_CHAIN
#undef BIND_DESCEND_STRUCT
#undef BIND_WRITE_NUMBER
#undef BIND_DISPATCH_ANY
#undef TAPE_BIND_DISPATCH_ANY
#undef BIND_VALUE_SWITCH_COMMON
#undef __BIND_SAVE_LOCALS

#undef TAP_CURSOR
#undef TAP_PEEK
#undef TAP_TAG
#undef TAP_PAYLOAD
#undef TAP_ADVANCE
#undef TAP_FOLLOW_SEAMS
#undef TAP_EOF
#undef TAP_READ_NUMBER
#undef __TAPE_BIND_SAVE_LOCALS
#undef TAPE_BIND_YIELD
#undef TAPE_BIND_YIELD_ERR
#undef TAPE_BIND_YIELD_ERR_NO_POS
#undef TAPE_BIND_YIELD_FLUSH_MAP
#undef TAPE_BIND_RESOLVE_PTR_CHAIN
#undef TAPE_BIND_DESCEND_STRUCT
#undef TAPE_BIND_MAP_OPEN
#undef TAPE_BIND_TYPE_MISMATCH_SKIP

#endif /* NDEC_BIND_H */
