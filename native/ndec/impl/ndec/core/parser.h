/*
 * ndec parser kernel
 *
 * Single pass JSON parser core with SIMD structural scanning,
 * incremental suspend and resume, reactor callbacks, and a
 * zero allocation oriented hot path.
 *
 * This header emits a parser implementation whose behavior can be
 * customized by the host before inclusion, such as the exported
 * function name, stack alignment, reactor dispatch hooks, and
 * frame extension fields.
 *
 * The kernel is designed for binding style parsing inside an
 * embedding runtime. Parser state lives in NdecCtx so callers can
 * feed partial input, suspend on demand, and resume with more
 * bytes without rebuilding parser state.
 */
#ifndef NDEC_PARSER_H
#define NDEC_PARSER_H

#include "ndec/core/scanner.h"
#include "ndec/core/scalar.h"

#ifndef NDEC_FN_DECL
#define NDEC_FN_DECL
#endif

#ifdef NDEC_FN_NAME
NDEC_FN_DECL void NDEC_FN_NAME(NdecCtx *ctx)
#else
void ndec_parse_default(NdecCtx *ctx)
#endif
{

  const uint8_t *buf     = ctx->buf;
  const uint8_t *buf_end = ctx->buf_end;
  /* Hot path keeps cur_pos at the "last NEXT_STRUCTURAL hit", with a
   * bootstrap sentinel cur_pos = ctx->cur_pos - 1 so that cur_pos + 1
   * yields the first unconsumed byte before any hit. Cold suspend paths
   * convert back to first-unconsumed via +1 or an explicit pointer;
   * callers observe ctx->cur_pos as first unconsumed on entry and exit. */
  const uint8_t *cur_pos     = ctx->cur_pos - 1;
  const uint8_t *chunk_ptr   = ctx->chunk_ptr;
  uint64_t bits              = ctx->structural_bits;
  NdecScanState scan_state   = ctx->scan_state;
  uint32_t depth             = ctx->depth;
  NdecFrame *frames          = ctx->frames;
  const NdecReactor *reactor = ctx->reactor;
  void *ud                   = ctx->user_data;

  int32_t _err_code;
  uint32_t _err_pos;
  uint32_t _suspend_phase;

/* Reactor dispatch macros */
#ifndef NDEC_R_BEGIN_OBJECT
#define NDEC_R_BEGIN_OBJECT(ud) ((reactor && reactor->begin_object) ? reactor->begin_object(ud) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_END_OBJECT
#define NDEC_R_END_OBJECT(ud) ((reactor && reactor->end_object) ? reactor->end_object(ud) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_OBJECT_FIELD
#define NDEC_R_OBJECT_FIELD(ud, key)                                                                              \
  ((reactor && reactor->object_field) ? reactor->object_field((ud), (key)) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_BEGIN_ARRAY
#define NDEC_R_BEGIN_ARRAY(ud) ((reactor && reactor->begin_array) ? reactor->begin_array(ud) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_END_ARRAY
#define NDEC_R_END_ARRAY(ud) ((reactor && reactor->end_array) ? reactor->end_array(ud) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_NULL
#define NDEC_R_SCALAR_NULL(ud) ((reactor && reactor->scalar_null) ? reactor->scalar_null(ud) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_BOOL
#define NDEC_R_SCALAR_BOOL(ud, v)                                                                                 \
  ((reactor && reactor->scalar_bool) ? reactor->scalar_bool((ud), (v)) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_NUMBER
#define NDEC_R_SCALAR_NUMBER(ud, raw)                                                                             \
  ((reactor && reactor->scalar_number) ? reactor->scalar_number((ud), (raw)) : NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_STRING
#define NDEC_R_SCALAR_STRING(ud, raw)                                                                             \
  ((reactor && reactor->scalar_string) ? reactor->scalar_string((ud), (raw)) : NDEC_PROCEED)
#endif

/* Container-specialized scalar macros. */
#ifndef NDEC_R_OBJ_SCALAR_NULL
#define NDEC_R_OBJ_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_OBJ_SCALAR_BOOL
#define NDEC_R_OBJ_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_OBJ_SCALAR_NUMBER
#define NDEC_R_OBJ_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_OBJ_SCALAR_STRING
#define NDEC_R_OBJ_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

#ifndef NDEC_R_ARR_SCALAR_NULL
#define NDEC_R_ARR_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_ARR_SCALAR_BOOL
#define NDEC_R_ARR_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_ARR_SCALAR_NUMBER
#define NDEC_R_ARR_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_ARR_SCALAR_STRING
#define NDEC_R_ARR_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

/*
 *  Root-specialized scalar macros.
 *
 *  Top-level non-container values ("null" / 42 / "hi" / true / false)
 *  are fundamentally different from OBJECT field values or ARRAY
 *  elements: there is no parent frame, no field index, no array slot.
 *  Embedders that want to bind a root scalar to a host-side target
 *  need a separate hook that knows the root frame layout.
 *
 *  Default: forward to the generic NDEC_R_SCALAR_* form. Embedders
 *  that want a root specialization override these four macros to
 *  point at their own inline hooks, writing through the root frame
 *  directly without touching the OBJECT / ARRAY paths.
 */
#ifndef NDEC_R_ROOT_SCALAR_NULL
#define NDEC_R_ROOT_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_ROOT_SCALAR_BOOL
#define NDEC_R_ROOT_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_ROOT_SCALAR_NUMBER
#define NDEC_R_ROOT_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_ROOT_SCALAR_STRING
#define NDEC_R_ROOT_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

#define CUR_OFFSET() ((uint32_t)(cur_pos - buf))

#define NDEC_SAVE_AND_RETURN(code)                                                                                \
  do {                                                                                                            \
    ctx->cur_pos         = cur_pos + 1;                                                                           \
    ctx->chunk_ptr       = chunk_ptr;                                                                             \
    ctx->structural_bits = bits;                                                                                  \
    ctx->scan_state      = scan_state;                                                                            \
    ctx->depth           = depth;                                                                                 \
    ctx->exit_code       = (code);                                                                                \
    return;                                                                                                       \
  } while (0)

#define GOTO_ERROR(code, pos)                                                                                     \
  do {                                                                                                            \
    _err_code = (code);                                                                                           \
    _err_pos  = (pos);                                                                                            \
    goto ndec_error_exit;                                                                                         \
  } while (0)

/* YIELD_OR_ERROR: NDEC_YIELD shares the hot-path `directive < 0` branch
 * with reactor errors; the cold handler splits on _err_code == NDEC_YIELD
 * vs error.
 *
 * Callsite contract for YIELD-supporting hooks:
 *   Container enter/exit (begin_*, end_*): commit all structural
 *   bookkeeping (TOP_FRAME()->phase, STACK_PUSH/POP, cur_pos advance)
 *   before the hook call. On YIELD the saved frames already describe
 *   the post-hook state, so resume dispatches to the child or parent
 *   phase without re-invoking the hook.
 *
 *   Scalars and object_field: cur_pos must be advanced past the scalar
 *   (end or end+1) before the hook call so NEXT_STRUCTURAL on resume
 *   skips past the consumed value. resume_phase selects the logical
 *   next phase (e.g. OBJECT_FIELD_VALUE after object_field); the cold
 *   path writes it only on YIELD. */
#define YIELD_OR_ERROR(d, resume_phase)                                                                           \
  do {                                                                                                            \
    _err_code      = (d);                                                                                         \
    _err_pos       = CUR_OFFSET();                                                                                \
    _suspend_phase = (resume_phase);                                                                              \
    goto ndec_error_or_yield_exit;                                                                                \
  } while (0)

/* Suspend variants. Hot path keeps cur_pos at the last NEXT_STRUCTURAL
 * hit (with a pre-hit sentinel of ctx->cur_pos - 1); cold suspend
 * resolves "first unconsumed byte" three ways:
 *
 *   SUSPEND_NEXT(phase):    commit = cur_pos + 1. Used after the byte
 *                           at cur_pos has been consumed, or before any
 *                           hit (sentinel yields buf).
 *
 *   SUSPEND_HERE(phase):    commit = cur_pos. Used when cur_pos sits on
 *                           the first byte of an unconsumed token
 *                           (keyword first letter, quote of a failed
 *                           string span, sign/digit of a failed number).
 *
 *   SUSPEND_AT(phase, ptr): commit = ptr. Explicit rollback anchor,
 *                           e.g. cur_pos = quote_pos to re-parse the
 *                           whole `"key":` atomically on resume. */
#define SUSPEND_NEXT(phase_val)                                                                                   \
  do {                                                                                                            \
    _suspend_phase = (phase_val);                                                                                 \
    goto ndec_suspend_next_exit;                                                                                  \
  } while (0)

#define SUSPEND_HERE(phase_val)                                                                                   \
  do {                                                                                                            \
    _suspend_phase = (phase_val);                                                                                 \
    goto ndec_suspend_exit;                                                                                       \
  } while (0)

#define SUSPEND_AT(phase_val, ptr)                                                                                \
  do {                                                                                                            \
    cur_pos        = (ptr);                                                                                       \
    _suspend_phase = (phase_val);                                                                                 \
    goto ndec_suspend_exit;                                                                                       \
  } while (0)

  /* Scalar token dispatch macros.
   *
   * Each macro collapses the uniform "call scalar helper, then branch on
   * status" epilogue into one callsite. They rely on locals named
   * `bits`, `chunk_ptr`, `scan_state`, `buf_end` and expand into gotos
   * to the parser's shared suspend / error exits.
   *
   * MATCH_KEYWORD:
   *   Full keyword handoff: match the atom, advance cur_pos on OK,
   *   SUSPEND_HERE on TRUNCATED, GOTO_ERROR(NDEC_ERR_KEYWORD) on BAD.
   *   Used for null / true / false.
   *
   * PARSE_STRING_SPAN / PARSE_NUMBER_SPAN:
   *   Advance bits/chunk_ptr past the scanned span and bind `out_end` to
   *   the span endpoint. Failure branches:
   *     string: TRUNCATED -> SUSPEND_AT(rollback), INVALID -> ERR_EOF
   *     number: TRUNCATED -> SUSPEND_AT(rollback); OK covers is_final
   *             stream end, so there is no invalid case.
   *   Special-case callsites (root-scalar string using SUSPEND_HERE, or
   *   skip_value where both statuses roll back) stay inline. */

#define MATCH_KEYWORD(match_fn, advance_by, resume_phase)                                                         \
  do {                                                                                                            \
    NdecKwResult _kw = (match_fn)(cur_pos, buf_end, &scan_state);                                                 \
    if (UNLIKELY(_kw != NDEC_KW_OK)) {                                                                            \
      if (_kw == NDEC_KW_TRUNCATED)                                                                               \
        SUSPEND_HERE(resume_phase);                                                                               \
      GOTO_ERROR(NDEC_ERR_KEYWORD, CUR_OFFSET());                                                                 \
    }                                                                                                             \
    cur_pos += (advance_by);                                                                                      \
  } while (0)

#define PARSE_STRING_SPAN(out_end, out_has_escape, resume_phase, rollback_pos)                                    \
  do {                                                                                                            \
    uint32_t _open_off = (uint32_t)((rollback_pos) - chunk_ptr);                                                  \
    NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, _open_off);                      \
    bits               = _sr.bits;                                                                                \
    chunk_ptr          = _sr.chunk_ptr;                                                                           \
    (out_end)          = _sr.end;                                                                                 \
    (out_has_escape)   = _sr.has_escape;                                                                          \
    if (UNLIKELY(_sr.status != NDEC_SPAN_OK)) {                                                                   \
      if (_sr.status == NDEC_SPAN_TRUNCATED)                                                                      \
        SUSPEND_AT((resume_phase), (rollback_pos));                                                               \
      GOTO_ERROR(NDEC_ERR_EOF, CUR_OFFSET());                                                                     \
    }                                                                                                             \
  } while (0)

#define PARSE_NUMBER_SPAN(out_end, resume_phase, rollback_pos)                                                    \
  do {                                                                                                            \
    NdecSpanResult _sr = ndec_number_span(bits, buf_end, chunk_ptr, &scan_state);                                 \
    bits               = _sr.bits;                                                                                \
    chunk_ptr          = _sr.chunk_ptr;                                                                           \
    (out_end)          = _sr.end;                                                                                 \
    if (UNLIKELY(_sr.status == NDEC_SPAN_TRUNCATED)) {                                                            \
      SUSPEND_AT((resume_phase), (rollback_pos));                                                                 \
    }                                                                                                             \
  } while (0)

#define NEXT_STRUCTURAL(out_ch_var)                                                                               \
  do {                                                                                                            \
    uint32_t _idx;                                                                                                \
    if (LIKELY(!ndec_ctz64_empty(bits, &_idx))) {                                                                 \
      cur_pos      = chunk_ptr + _idx;                                                                            \
      bits         = ndec_clear_lowest_bit(bits);                                                                 \
      (out_ch_var) = (int32_t)*cur_pos;                                                                           \
      break;                                                                                                      \
    }                                                                                                             \
    NdecAdvanceResult _ar = ndec_advance_chunk(chunk_ptr, buf_end, &scan_state);                                  \
    if (UNLIKELY(_ar.chunk_ptr == chunk_ptr)) {                                                                   \
      (out_ch_var) = NDEC_EOF;                                                                                    \
      break;                                                                                                      \
    }                                                                                                             \
    chunk_ptr = _ar.chunk_ptr;                                                                                    \
    bits      = _ar.bits;                                                                                         \
  } while (1)

#define STACK_PUSH(child_phase)                                                                                   \
  do {                                                                                                            \
    if (UNLIKELY(depth >= NDEC_MAX_DEPTH)) {                                                                      \
      GOTO_ERROR(NDEC_ERR_DEPTH, CUR_OFFSET());                                                                   \
    }                                                                                                             \
    frames[depth].phase = (child_phase);                                                                          \
    frames[depth].data  = 0;                                                                                      \
    depth++;                                                                                                      \
  } while (0)

#define STACK_POP() (depth--)
#define TOP_FRAME() (&frames[depth - 1])

  /* Dispatch table. */

#define DT_ENTRY(label) (int32_t)((char *) && label - (char *) && ndec_dispatch_base)

  static const int32_t dispatch_table[NDEC_PHASE_COUNT] = {
      [NDEC_PHASE_ROOT_VALUE]             = DT_ENTRY(ndec_root_value),
      [NDEC_PHASE_OBJECT_FIELD_OR_END]    = DT_ENTRY(ndec_object_field_or_end),
      [NDEC_PHASE_OBJECT_FIELD_VALUE]     = DT_ENTRY(ndec_object_field_value),
      [NDEC_PHASE_OBJECT_CONTINUE_OR_END] = DT_ENTRY(ndec_object_continue_or_end),
      [NDEC_PHASE_ARRAY_ELEM_OR_END]      = DT_ENTRY(ndec_array_elem_or_end),
      [NDEC_PHASE_ARRAY_ELEM_VALUE]       = DT_ENTRY(ndec_array_elem_value),
      [NDEC_PHASE_ARRAY_CONTINUE_OR_END]  = DT_ENTRY(ndec_array_continue_or_end),
      [NDEC_PHASE_ROOT_DONE]              = DT_ENTRY(ndec_root_done),
      [NDEC_PHASE_SKIP_VALUE]             = DT_ENTRY(ndec_skip_value),
  };

#undef DT_ENTRY

#define NDEC_DISPATCH_PHASE(phase_val)                                                                            \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    NDEC_LOAD_BASE(_base);                                                                                        \
    goto *(void *)(_base + dispatch_table[(phase_val)]);                                                          \
  } while (0)

#if defined(__aarch64__)
#define NDEC_LOAD_BASE(var) __asm__ volatile("adr %0, %c1" : "=r"(var) : "i"(&&ndec_dispatch_base))
#elif defined(__x86_64__)
#define NDEC_LOAD_BASE(var) __asm__ volatile("lea %c1(%%rip), %0" : "=r"(var) : "i"(&&ndec_dispatch_base))
#elif defined(__riscv)
#define NDEC_LOAD_BASE(var) __asm__ volatile("lla %0, %c1" : "=r"(var) : "i"(&&ndec_dispatch_base))
#elif defined(__loongarch64)
#define NDEC_LOAD_BASE(var) __asm__ volatile("la.local %0, %c1" : "=r"(var) : "i"(&&ndec_dispatch_base))
#else
#error "NDEC_LOAD_BASE: unsupported architecture"
#endif

  /* Bootstrap.
   *
   * advance_chunk always scans chunk_ptr + 64 (the next chunk), so it
   * cannot produce bits for the first chunk of a call. We seed them
   * here at entry, and again on resume when bits == 0 (the previous
   * call stopped with a partial or padded tail chunk).
   *
   * The three-way scan (full / padded-tail / zero) is duplicated
   * between the depth == 0 and depth > 0 branches intentionally.
   *
   * frames[0] is the root sentinel: the parser stores its own state
   * machine progress in its phase (ROOT_VALUE -> ROOT_DONE), leaving
   * frames[1] as the first slot host reactors can claim for their root
   * binding. */

  if ((depth != 0)) {
    if (bits == 0) {
      ptrdiff_t len = buf_end - chunk_ptr;
      if (len >= 64) {
        NdecChunkResult r = ndec_scan_chunk(chunk_ptr, &scan_state);
        bits              = r.structural;
      } else if (scan_state.is_final && buf_end > chunk_ptr) {
        uint8_t padded[64];
        __builtin_memcpy(padded, chunk_ptr, (size_t)len);
        __builtin_memset(padded + len, 0x20, 64 - (size_t)len);
        NdecChunkResult r = ndec_scan_chunk(padded, &scan_state);
        bits              = r.structural & (((uint64_t)1 << (uint32_t)len) - 1);
      }
    }
    NDEC_DISPATCH_PHASE(frames[depth - 1].phase);
  } else {
    chunk_ptr     = buf;
    ptrdiff_t len = buf_end - chunk_ptr;
    if (len >= 64) {
      NdecChunkResult r = ndec_scan_chunk(chunk_ptr, &scan_state);
      bits              = r.structural;
    } else if (scan_state.is_final && buf_end > chunk_ptr) {
      uint8_t padded[64];
      __builtin_memcpy(padded, chunk_ptr, (size_t)len);
      __builtin_memset(padded + len, 0x20, 64 - (size_t)len);
      NdecChunkResult r = ndec_scan_chunk(padded, &scan_state);
      bits              = r.structural & (((uint64_t)1 << (uint32_t)len) - 1);
    } else {
      bits = 0;
    }

    /* Install the root sentinel: frames[0].phase = ROOT_VALUE, depth = 1.
     * Hosts may pre-fill frames[1] with their own root state before
     * calling in; the first STACK_PUSH on '{' or '[' raises depth to 2
     * and begin_object / begin_array sees that pre-filled frame. */
    frames[0].phase = NDEC_PHASE_ROOT_VALUE;
    frames[0].data  = 0;
    depth           = 1;
    // goto ndec_root_value;
  }

ndec_dispatch_base:
  // __builtin_unreachable();

ndec_root_value: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);

  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ROOT_DONE;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ROOT_DONE;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_ELEM_OR_END);
    }
    goto ndec_array_elem_or_end;
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    if (scan_state.is_final) {
      GOTO_ERROR(NDEC_ERR_EOF, (uint32_t)(buf_end - buf));
    }
    SUSPEND_NEXT(NDEC_PHASE_ROOT_VALUE);
  }
  /* Top-level non-container values are rare; handled out-of-line. */
  goto ndec_root_scalar;
}

ndec_root_done: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (ch == NDEC_EOF) {
    /* Pop the root sentinel; input fully consumed. The error exit path
     * deliberately leaves the sentinel in place so callers seeing
     * depth != 0 know the parser state is dirty. */
    STACK_POP();
    NDEC_SAVE_AND_RETURN(NDEC_OK);
  }
  GOTO_ERROR(NDEC_ERR_TRAILING, CUR_OFFSET());
}

  /* OBJECT.
   *
   *  `"key":` is atomic: EOF anywhere inside key or before the colon
   *  rolls back to the field entry phase, so the whole `"key":` is
   *  re-parsed on resume.
   *
   *  Lazy phase: the hot path does not write frame.phase. Only cold
   *  suspend paths and container push/pop update it. */

ndec_object_field_or_end: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (ch == '"') {
    const uint8_t *quote_pos = cur_pos; /* atomic "key": rollback anchor */
    const uint8_t *key_start = cur_pos + 1;
    const uint8_t *end;
    uint8_t _has_esc;
    PARSE_STRING_SPAN(end, _has_esc, NDEC_PHASE_OBJECT_FIELD_OR_END, quote_pos);
    int32_t colon;
    NEXT_STRUCTURAL(colon);
    if (UNLIKELY(colon != ':')) {
      if (colon == NDEC_EOF) {
        SUSPEND_AT(NDEC_PHASE_OBJECT_FIELD_OR_END, quote_pos);
      }
      GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
    }
    NdecStrInfo key   = {{key_start, (uint32_t)(end - key_start)}, _has_esc};
    int32_t directive = NDEC_R_OBJECT_FIELD(ud, key);
    if (UNLIKELY(directive != NDEC_PROCEED)) {
      /* Cold-path classifier: folds SKIP and negative (YIELD / error)
       * into one hot-path branch so PROCEED is a single cbnz/jne. */
      if (directive == NDEC_SKIP) {
        TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
        TOP_FRAME()->data  = 0;
        goto ndec_skip_value;
      }
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_VALUE);
    }
    goto ndec_object_field_value;
  }
  if (ch == '}') {
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_OBJECT(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[depth - 1].phase);
    }
    NDEC_DISPATCH_PHASE(frames[depth - 1].phase);
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    SUSPEND_NEXT(NDEC_PHASE_OBJECT_FIELD_OR_END);
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_object_field_value: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (LIKELY(ch == '"')) {
    const uint8_t *value_begin = cur_pos;
    const uint8_t *str_start   = cur_pos + 1;
    const uint8_t *end;
    uint8_t _has_esc;
    PARSE_STRING_SPAN(end, _has_esc, NDEC_PHASE_OBJECT_FIELD_VALUE, value_begin);
    NdecStrInfo str   = {{str_start, (uint32_t)(end - str_start)}, _has_esc};
    cur_pos           = end + 1;
    int32_t directive = NDEC_R_OBJ_SCALAR_STRING(ud, str);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_CONTINUE_OR_END);
    }
    goto ndec_object_continue_or_end;
  }
  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    const uint8_t *num_start = cur_pos;
    const uint8_t *end;
    PARSE_NUMBER_SPAN(end, NDEC_PHASE_OBJECT_FIELD_VALUE, num_start);
    NdecRawStr raw    = {num_start, (uint32_t)(end - num_start)};
    cur_pos           = end; /* number_span's end is already one-past the last digit */
    int32_t directive = NDEC_R_OBJ_SCALAR_NUMBER(ud, raw);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_CONTINUE_OR_END);
    }
    goto ndec_object_continue_or_end;
  }
  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_ELEM_OR_END);
    }
    goto ndec_array_elem_or_end;
  }
  if (ch == 'n') {
    MATCH_KEYWORD(ndec_match_null, 4, NDEC_PHASE_OBJECT_FIELD_VALUE);
    int32_t directive = NDEC_R_OBJ_SCALAR_NULL(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_CONTINUE_OR_END);
    }
    goto ndec_object_continue_or_end;
  }
  if (ch == 't') {
    MATCH_KEYWORD(ndec_match_true, 4, NDEC_PHASE_OBJECT_FIELD_VALUE);
    int32_t directive = NDEC_R_OBJ_SCALAR_BOOL(ud, 1);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_CONTINUE_OR_END);
    }
    goto ndec_object_continue_or_end;
  }
  if (ch == 'f') {
    MATCH_KEYWORD(ndec_match_false, 5, NDEC_PHASE_OBJECT_FIELD_VALUE);
    int32_t directive = NDEC_R_OBJ_SCALAR_BOOL(ud, 0);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_CONTINUE_OR_END);
    }
    goto ndec_object_continue_or_end;
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    /* cur_pos is at the consumed ':'; SUSPEND_NEXT commits ':' + 1. */
    SUSPEND_NEXT(NDEC_PHASE_OBJECT_FIELD_VALUE);
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_object_continue_or_end: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (ch == ',') {
    /* Trailing comma is invalid; a key string must follow. Peek the next structural;
     * on EOF we roll back to the comma so the whole `,"key":` remains atomic across suspend. */
    const uint8_t *comma_pos = cur_pos;
    int32_t nch;
    NEXT_STRUCTURAL(nch);
    if (UNLIKELY(nch == NDEC_EOF)) {
      SUSPEND_AT(NDEC_PHASE_OBJECT_CONTINUE_OR_END, comma_pos);
    }
    if (UNLIKELY(nch != '"')) {
      GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
    }
    /* Inline key parse (NEXT_STRUCTURAL already consumed the '"'). */
    const uint8_t *key_start = cur_pos + 1;
    const uint8_t *end;
    uint8_t _has_esc;
    PARSE_STRING_SPAN(end, _has_esc, NDEC_PHASE_OBJECT_CONTINUE_OR_END, comma_pos);
    int32_t colon;
    NEXT_STRUCTURAL(colon);
    if (UNLIKELY(colon != ':')) {
      if (colon == NDEC_EOF) {
        SUSPEND_AT(NDEC_PHASE_OBJECT_CONTINUE_OR_END, comma_pos);
      }
      GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
    }
    NdecStrInfo key   = {{key_start, (uint32_t)(end - key_start)}, _has_esc};
    int32_t directive = NDEC_R_OBJECT_FIELD(ud, key);
    if (UNLIKELY(directive != NDEC_PROCEED)) {
      /* Cold-path classifier: folds SKIP and negative (YIELD / error)
       * into one hot-path branch so PROCEED is a single cbnz/jne. */
      if (directive == NDEC_SKIP) {
        TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
        TOP_FRAME()->data  = 0;
        goto ndec_skip_value;
      }
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_VALUE);
    }
    goto ndec_object_field_value;
  }
  if (ch == '}') {
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_OBJECT(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[depth - 1].phase);
    }
    NDEC_DISPATCH_PHASE(frames[depth - 1].phase);
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    /* cur_pos was committed past the previous value's end, so it is
     * already first-unconsumed. */
    SUSPEND_HERE(NDEC_PHASE_OBJECT_CONTINUE_OR_END);
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_array_elem_or_end: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);

  if (ch == ']') {
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_ARRAY(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[depth - 1].phase);
    }
    NDEC_DISPATCH_PHASE(frames[depth - 1].phase);
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    SUSPEND_NEXT(NDEC_PHASE_ARRAY_ELEM_OR_END);
  }

  if (ch == '"') {
    const uint8_t *value_begin = cur_pos;
    const uint8_t *str_start   = cur_pos + 1;
    const uint8_t *end;
    uint8_t _has_esc;
    PARSE_STRING_SPAN(end, _has_esc, NDEC_PHASE_ARRAY_ELEM_OR_END, value_begin);
    NdecStrInfo str   = {{str_start, (uint32_t)(end - str_start)}, _has_esc};
    cur_pos           = end + 1;
    int32_t directive = NDEC_R_ARR_SCALAR_STRING(ud, str);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }

  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    const uint8_t *num_start = cur_pos;
    const uint8_t *end;
    PARSE_NUMBER_SPAN(end, NDEC_PHASE_ARRAY_ELEM_OR_END, num_start);
    NdecRawStr raw    = {num_start, (uint32_t)(end - num_start)};
    cur_pos           = end;
    int32_t directive = NDEC_R_ARR_SCALAR_NUMBER(ud, raw);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }

  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_ELEM_OR_END);
    }
    goto ndec_array_elem_or_end;
  }
  if (ch == 'n') {
    MATCH_KEYWORD(ndec_match_null, 4, NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ARR_SCALAR_NULL(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == 't') {
    MATCH_KEYWORD(ndec_match_true, 4, NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ARR_SCALAR_BOOL(ud, 1);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == 'f') {
    MATCH_KEYWORD(ndec_match_false, 5, NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ARR_SCALAR_BOOL(ud, 0);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_array_elem_value: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);

  if (ch == '"') {
    const uint8_t *value_begin = cur_pos;
    const uint8_t *str_start   = cur_pos + 1;
    const uint8_t *end;
    uint8_t _has_esc;
    PARSE_STRING_SPAN(end, _has_esc, NDEC_PHASE_ARRAY_ELEM_VALUE, value_begin);
    NdecStrInfo str   = {{str_start, (uint32_t)(end - str_start)}, _has_esc};
    cur_pos           = end + 1;
    int32_t directive = NDEC_R_ARR_SCALAR_STRING(ud, str);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    const uint8_t *num_start = cur_pos;
    const uint8_t *end;
    PARSE_NUMBER_SPAN(end, NDEC_PHASE_ARRAY_ELEM_VALUE, num_start);
    NdecRawStr raw    = {num_start, (uint32_t)(end - num_start)};
    cur_pos           = end;
    int32_t directive = NDEC_R_ARR_SCALAR_NUMBER(ud, raw);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_ELEM_OR_END);
    }
    goto ndec_array_elem_or_end;
  }
  if (ch == 'n') {
    MATCH_KEYWORD(ndec_match_null, 4, NDEC_PHASE_ARRAY_ELEM_VALUE);
    int32_t directive = NDEC_R_ARR_SCALAR_NULL(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == 't') {
    MATCH_KEYWORD(ndec_match_true, 4, NDEC_PHASE_ARRAY_ELEM_VALUE);
    int32_t directive = NDEC_R_ARR_SCALAR_BOOL(ud, 1);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == 'f') {
    MATCH_KEYWORD(ndec_match_false, 5, NDEC_PHASE_ARRAY_ELEM_VALUE);
    int32_t directive = NDEC_R_ARR_SCALAR_BOOL(ud, 0);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_CONTINUE_OR_END);
    }
    cur_pos += 5;
    goto ndec_array_continue_or_end;
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    /* cur_pos is at the consumed ','; SUSPEND_NEXT commits ',' + 1. */
    SUSPEND_NEXT(NDEC_PHASE_ARRAY_ELEM_VALUE);
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_array_continue_or_end: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (ch == ',') {
    goto ndec_array_elem_value;
  }
  if (ch == ']') {
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_ARRAY(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[depth - 1].phase);
    }
    NDEC_DISPATCH_PHASE(frames[depth - 1].phase);
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    /* cur_pos is past the previous element or past the closing bracket
     * the end_array/end_object committed; either way it is first-unconsumed. */
    SUSPEND_HERE(NDEC_PHASE_ARRAY_CONTINUE_OR_END);
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

/*
 *  SKIP VALUE
 *
 *  Consume an entire JSON value without emitting reactor callbacks.
 *  The caller sets TOP_FRAME()->phase to its continuation before the
 *  goto.
 *
 *  Entry: cur_pos points at the first byte of the value (already
 *  consumed by the caller's NEXT_STRUCTURAL). We read *cur_pos to
 *  classify without consuming another structural.
 *
 *  Structural bits exclude characters inside strings, so quoted
 *  brackets never participate in the container skip loop.
 *  TOP_FRAME()->data holds skip_depth across suspend/resume.
 */
ndec_skip_value: {
  if (TOP_FRAME()->data > 0) {
    /* Resuming inside a container skip: continue the loop. */
    goto ndec_skip_container;
  }

  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (UNLIKELY(ch == NDEC_EOF)) {
    if (scan_state.is_final) {
      GOTO_ERROR(NDEC_ERR_EOF, CUR_OFFSET());
    }
    /* cur_pos is at the consumed ':'; SUSPEND_NEXT commits ':' + 1. */
    SUSPEND_NEXT(NDEC_PHASE_SKIP_VALUE);
  }
  goto ndec_skip_dispatch;
}

ndec_skip_dispatch: {
  int32_t ch = (int32_t)*cur_pos;
  if (ch == '"') {
    const uint8_t *quote_pos = cur_pos;
    const uint8_t *end;
    NdecSpanStatus status;
    {
      uint32_t _open_off = (uint32_t)(quote_pos - chunk_ptr);
      NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, _open_off);
      bits               = _sr.bits;
      chunk_ptr          = _sr.chunk_ptr;
      end                = _sr.end;
      status             = _sr.status;
    }
    if (UNLIKELY(status != NDEC_SPAN_OK)) {
      /* Both TRUNCATED and INVALID roll back to quote_pos so the whole
       * string is re-parsed on resume with more data. */
      SUSPEND_AT(NDEC_PHASE_SKIP_VALUE, quote_pos);
    }
    cur_pos = end + 1;
    NDEC_DISPATCH_PHASE(TOP_FRAME()->phase);
  }
  if (ch != '{' && ch != '[') {
    /* Scalar (keyword or number): advance one byte so the parent's
     * NEXT_STRUCTURAL re-syncs to the next real structural. cur_pos
     * may land inside the scalar body, breaking the last-hit invariant
     * temporarily; the next successful NEXT_STRUCTURAL restores it. */
    cur_pos++;
    NDEC_DISPATCH_PHASE(TOP_FRAME()->phase);
  }

  TOP_FRAME()->data = 1;
  goto ndec_skip_container;
}

ndec_skip_container: {
  uint32_t skip_depth = TOP_FRAME()->data;
  for (;;) {
    int32_t ch;
    NEXT_STRUCTURAL(ch);
    if (ch == '{' || ch == '[') {
      skip_depth++;
    } else if (ch == '}' || ch == ']') {
      if (--skip_depth == 0) {
        cur_pos++;
        NDEC_DISPATCH_PHASE(TOP_FRAME()->phase);
      }
    } else if (UNLIKELY(ch == NDEC_EOF)) {
      TOP_FRAME()->data = skip_depth;
      /* cur_pos is at the last structural consumed inside the
       * container; SUSPEND_NEXT commits cur_pos + 1, which is safe
       * because the skipped content past it will be re-scanned on
       * resume. */
      SUSPEND_NEXT(NDEC_PHASE_SKIP_VALUE);
    }
    /* Quotes, commas, colons, and scalar starts: just consume. */
  }
}

/* Cold paths: root scalars, error/suspend exits. */
ndec_root_scalar: {
  int32_t ch = (int32_t)*cur_pos;
  if (ch == '"') {
    const uint8_t *str_start = cur_pos + 1;
    const uint8_t *end;
    NdecSpanStatus status;
    uint8_t has_escape;
    {
      uint32_t _open_off = (uint32_t)(cur_pos - chunk_ptr);
      NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, _open_off);
      bits               = _sr.bits;
      chunk_ptr          = _sr.chunk_ptr;
      end                = _sr.end;
      status             = _sr.status;
      has_escape         = _sr.has_escape;
    }
    if (UNLIKELY(status != NDEC_SPAN_OK)) {
      if (status == NDEC_SPAN_TRUNCATED)
        SUSPEND_HERE(NDEC_PHASE_ROOT_VALUE);
      GOTO_ERROR(NDEC_ERR_EOF, CUR_OFFSET());
    }
    NdecStrInfo str   = {{str_start, (uint32_t)(end - str_start)}, has_escape};
    cur_pos           = end + 1;
    int32_t directive = NDEC_R_ROOT_SCALAR_STRING(ud, str);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ROOT_DONE);
    }
    goto ndec_root_done;
  }
  if (ch == 'n') {
    MATCH_KEYWORD(ndec_match_null, 4, NDEC_PHASE_ROOT_VALUE);
    int32_t directive = NDEC_R_ROOT_SCALAR_NULL(ud);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ROOT_DONE);
    }
    goto ndec_root_done;
  }
  if (ch == 't') {
    MATCH_KEYWORD(ndec_match_true, 4, NDEC_PHASE_ROOT_VALUE);
    int32_t directive = NDEC_R_ROOT_SCALAR_BOOL(ud, 1);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ROOT_DONE);
    }
    goto ndec_root_done;
  }
  if (ch == 'f') {
    MATCH_KEYWORD(ndec_match_false, 5, NDEC_PHASE_ROOT_VALUE);
    int32_t directive = NDEC_R_ROOT_SCALAR_BOOL(ud, 0);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ROOT_DONE);
    }
    goto ndec_root_done;
  }
  if (ch == '-' || (ch >= '0' && ch <= '9')) {
    const uint8_t *num_start = cur_pos;
    const uint8_t *end;
    /* Roll back to num_start so resume re-enters root_value and
     * re-reads the same '-' or digit; more digits may arrive next. */
    PARSE_NUMBER_SPAN(end, NDEC_PHASE_ROOT_VALUE, num_start);
    NdecRawStr raw    = {num_start, (uint32_t)(end - num_start)};
    cur_pos           = end;
    int32_t directive = NDEC_R_ROOT_SCALAR_NUMBER(ud, raw);
    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, NDEC_PHASE_ROOT_DONE);
    }
    goto ndec_root_done;
  }
  GOTO_ERROR(NDEC_ERR_SYNTAX, CUR_OFFSET());
}

ndec_error_or_yield_exit:
  if (_err_code == NDEC_YIELD) {
    /* _suspend_phase was supplied by the callsite as the top frame's
     * resume phase. For container enter/exit callsites this matches
     * the pre-written phase; for object_field and scalars it is a
     * genuine update. Either way, one cold store.
     *
     * The root sentinel guarantees depth >= 1 on yield (bootstrap leaves
     * depth=1, root container close returns to the sentinel at depth=1),
     * so frames[depth-1] is always valid; no depth==0 guard is needed. */
    frames[depth - 1].phase = _suspend_phase;

    /* If bits == 0 we have consumed every structural of the current
     * chunk. The bootstrap rescan on resume cannot tell the chunk is
     * already drained and would revive the same structural bits.
     * Advancing chunk_ptr to cur_pos (clamped to buf_end) makes the
     * bootstrap's `avail` non-positive so the rescan is skipped;
     * NEXT_STRUCTURAL's advance_chunk then picks up any remaining
     * data from there. */
    if (bits == 0) {
      const uint8_t *effective = cur_pos < buf_end ? cur_pos : buf_end;
      if (effective > chunk_ptr) {
        chunk_ptr = effective;
      }
    }

    /* Mirror the error path: reactor-initiated yields share the
     * YIELD_OR_ERROR prologue with reactor errors, so write _err_pos
     * back here too. Hosts that surface yields as user-visible errors
     * (e.g. type mismatch on a binding target) read this position. */
    ctx->error_pos       = _err_pos;
    ctx->cur_pos         = cur_pos;
    ctx->chunk_ptr       = chunk_ptr;
    ctx->structural_bits = bits;
    ctx->scan_state      = scan_state;
    ctx->depth           = depth;
    ctx->exit_code       = NDEC_SUSPEND;
    return;
  }

/* fallthrough: reactor error */
ndec_error_exit:
  ctx->error_pos       = _err_pos;
  ctx->cur_pos         = cur_pos;
  ctx->chunk_ptr       = chunk_ptr;
  ctx->structural_bits = bits;
  ctx->scan_state      = scan_state;
  ctx->depth           = depth;
  ctx->exit_code       = _err_code;
  return;

ndec_suspend_next_exit:
  cur_pos++; /* advance past the single-byte structural just consumed */
  /* fallthrough */
ndec_suspend_exit:
  frames[depth - 1].phase = _suspend_phase;
  ctx->cur_pos            = cur_pos;
  ctx->chunk_ptr          = chunk_ptr;
  ctx->structural_bits    = bits;
  ctx->scan_state         = scan_state;
  ctx->depth              = depth;
  ctx->exit_code          = NDEC_SUSPEND;
  return;

#undef NDEC_SAVE_AND_RETURN
#undef GOTO_ERROR
#undef YIELD_OR_ERROR
#undef NEXT_STRUCTURAL
#undef STACK_PUSH
#undef STACK_POP
#undef TOP_FRAME
#undef SUSPEND_AT
#undef SUSPEND_HERE
#undef SUSPEND_NEXT
#undef MATCH_KEYWORD
#undef PARSE_STRING_SPAN
#undef PARSE_NUMBER_SPAN
#undef CUR_OFFSET
#undef UNLIKELY
#undef LIKELY
#undef NDEC_DISPATCH_PHASE
#undef NDEC_LOAD_BASE
#undef NDEC_FN_DECL
#undef NDEC_R_ROOT_SCALAR_NULL
#undef NDEC_R_ROOT_SCALAR_BOOL
#undef NDEC_R_ROOT_SCALAR_NUMBER
#undef NDEC_R_ROOT_SCALAR_STRING
}

#endif /* NDEC_PARSER_H */
