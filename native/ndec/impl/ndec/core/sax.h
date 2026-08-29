#ifndef NDEC_CORE_SAX_H
#define NDEC_CORE_SAX_H

#include <assert.h>

#include "ndec/core/chunk.h"
#include "ndec/core/utf8.h"

#ifdef NDEC_REACTOR_HOOKS
#include NDEC_REACTOR_HOOKS
#endif

#include "ndec/core/sapi.h"

static inline void ndec_sax_ctx_init(NdecSaxContext *ctx, const NdecReactor *reactor, void *user_data) {
  ctx->buf             = NULL;
  ctx->buf_end         = NULL;
  ctx->is_final        = 0;
  ctx->reactor         = reactor;
  ctx->user_data       = user_data;
  ctx->cur_pos         = NULL;
  ctx->chunk_ptr       = NULL;
  ctx->structural_bits = 0;
  ctx->exit_code       = 0;
  ctx->error_pos       = 0;
  ctx->sp              = -1;

  ctx->frames[0].phase = NDEC_PHASE_ROOT_VALUE;
  ctx->frames[0].data  = 0;

  ctx->scan_state.prev_in_string        = 0;
  ctx->scan_state.prev_escape           = 0;
  ctx->scan_state.prev_structural_or_ws = 1;
  ctx->scan_state.last_backslash        = 0;

  utf8_checker_init(&ctx->utf8);
}

/* Only updates buf / buf_end / is_final. The scanner state (cur_pos,
 * chunk_ptr, structural_bits, scan_state) is preserved for resume. */
static inline void ndec_sax_ctx_set_input(NdecSaxContext *ctx, const uint8_t *buf, uint32_t len, int is_final) {
  ctx->buf      = buf;
  ctx->buf_end  = buf + len;
  ctx->is_final = is_final ? 1 : 0;
}

#define NDEC_EOF (-1)

typedef struct NdecAdvanceResult {
  const uint8_t *chunk_ptr; /* updated chunk base (== input on failure) */
  uint64_t bits;            /* structural bits for the new chunk (0 on failure) */
} NdecAdvanceResult;

static NOINLINE NdecAdvanceResult ndec_advance_chunk_tail(const uint8_t *next, ptrdiff_t remaining,
                                                          NdecScanState *state, Utf8Checker *utf8) {
  uint8_t padded[64] __attribute__((aligned(16)));

  /* The ASCII-space fill makes every unread tail byte nonstructural. */
  __builtin_memset(padded + 0, 0x20, 16);
  __builtin_memset(padded + 16, 0x20, 16);
  __builtin_memset(padded + 32, 0x20, 16);
  __builtin_memset(padded + 48, 0x20, 16);

  /* Constant-size copies keep this tail path independent of libc memcpy. */
  size_t r = (size_t)remaining;
  size_t i = 0;
  if (r >= 16) {
    __builtin_memcpy(padded + i, next + i, 16);
    i += 16;
    if (r >= 32) {
      __builtin_memcpy(padded + i, next + i, 16);
      i += 16;
      if (r >= 48) {
        __builtin_memcpy(padded + i, next + i, 16);
        i += 16;
      }
    }
  }
  size_t tail = r & 15; /* 0..15 leftover bytes. */
  if (tail >= 8) {
    __builtin_memcpy(padded + i, next + i, 8);
    i += 8;
    tail -= 8;
  }
  if (tail >= 4) {
    __builtin_memcpy(padded + i, next + i, 4);
    i += 4;
    tail -= 4;
  }
  if (tail >= 2) {
    __builtin_memcpy(padded + i, next + i, 2);
    i += 2;
    tail -= 2;
  }
  if (tail >= 1) {
    padded[i] = next[i];
  }

  NdecChunkResult res = ndec_scan_chunk_sax(padded, state);
  utf8_check_block64(utf8, padded);

  uint64_t mask = ((uint64_t)1 << (uint32_t)remaining) - 1;
  return (NdecAdvanceResult){next, res.structural & mask};
}

/* SAX chunk advancement.
 *
 * ndec_advance_chunk scans chunk_ptr + 64 (the NEXT chunk). The very
 * first chunk must be scanned separately at parse entry (bootstrap),
 * since advance_chunk cannot scan the current position.
 * When !is_final and remaining < 64, returns input chunk_ptr unchanged
 * with bits=0, signalling resume-needed.
 *
 * The NOINLINE boundary keeps one scanner frame shared by all state-machine
 * call sites. */
static NOINLINE NdecAdvanceResult ndec_advance_chunk(const uint8_t *chunk_ptr, const uint8_t *buf_end,
                                                     NdecScanState *state, Utf8Checker *utf8, int is_final) {
  const uint8_t *next = chunk_ptr + 64;
  ptrdiff_t remaining = buf_end - next;

  if (remaining >= 64) {
    NdecChunkResult r = ndec_scan_chunk_sax(next, state);
    utf8_check_block64(utf8, next);
    return (NdecAdvanceResult){next, r.structural};
  }

  if (remaining <= 0 || !is_final) {
    return (NdecAdvanceResult){chunk_ptr, 0};
  }

  return ndec_advance_chunk_tail(next, remaining, state, utf8);
}

/* Status returned by ndec_match_* (keywords) and ndec_*_span (strings,
 * numbers). One enum: all three callers share the same three-state
 * decision (matched / need more bytes / malformed).
 *
 * Decision table (uniform across helpers):
 *   enough bytes + match     -> OK
 *   enough bytes + mismatch  -> INVALID
 *   short buffer + !is_final -> TRUNCATED
 *   short buffer + is_final  -> INVALID  (truly malformed at end of stream)
 *
 * Number span is the one exception: an is_final short buffer reports OK
 * with end == buf_end, because reaching EOF on digit bytes IS a valid
 * terminator (no structural to consume). */
typedef enum {
  NDEC_SCAN_OK        = 0, /* keyword matched / closing quote found / number end hit */
  NDEC_SCAN_TRUNCATED = 1, /* ran out of data AND !is_final; caller must SUSPEND */
  NDEC_SCAN_INVALID   = 2, /* malformed: wrong keyword content, or unclosed string */
} NdecScanStatus;

/* 4-byte atom comparison via uint32_t XOR. Compiler folds the constant
 * string_to_u32("true") at compile time, producing a single LDR+XOR. */
INLINE uint32_t ndec_str4_xor(const uint8_t *src, const char *atom) {
  uint32_t sv, av;
  __builtin_memcpy(&sv, src, 4);
  __builtin_memcpy(&av, atom, 4);
  return sv ^ av;
}

/* A match requires the complete keyword so successful callers may advance to
 * the first byte after it. */
INLINE NdecScanStatus ndec_match_null(const uint8_t *cur_pos, const uint8_t *buf_end, int is_final) {
  if (buf_end >= cur_pos + 4) {
    return ndec_str4_xor(cur_pos, "null") == 0 ? NDEC_SCAN_OK : NDEC_SCAN_INVALID;
  }
  return is_final ? NDEC_SCAN_INVALID : NDEC_SCAN_TRUNCATED;
}

INLINE NdecScanStatus ndec_match_true(const uint8_t *cur_pos, const uint8_t *buf_end, int is_final) {
  if (buf_end >= cur_pos + 4) {
    return ndec_str4_xor(cur_pos, "true") == 0 ? NDEC_SCAN_OK : NDEC_SCAN_INVALID;
  }
  return is_final ? NDEC_SCAN_INVALID : NDEC_SCAN_TRUNCATED;
}

INLINE NdecScanStatus ndec_match_false(const uint8_t *cur_pos, const uint8_t *buf_end, int is_final) {
  if (buf_end >= cur_pos + 5) {
    return ndec_str4_xor(cur_pos + 1, "alse") == 0 ? NDEC_SCAN_OK : NDEC_SCAN_INVALID;
  }
  return is_final ? NDEC_SCAN_INVALID : NDEC_SCAN_TRUNCATED;
}

/* Result of string_span / number_span. All by-value so caller can keep
 * bits and chunk_ptr in registers across the call. */
typedef struct NdecSpanResult {
  uint64_t bits;            /* updated structural bits */
  const uint8_t *chunk_ptr; /* updated chunk base */
  const uint8_t *end;       /* position after token (NULL on error for string) */
  NdecScanStatus status;
  uint8_t has_escape; /* non-zero iff string content contains backslash escapes */
} NdecSpanResult;

/* Find closing quote. Reads backslash bitmap from state->last_backslash
 * (set by ndec_scan_chunk_sax / ndec_advance_chunk) and ORs it across chunks
 * into has_escape. open_offset is the opening quote's byte offset within
 * the initial chunk; backslash bits before it are masked out so adjacent
 * strings don't pollute each other's has_escape.
 * If advance_chunk runs out of data, callee maps that onto
 *   !is_final -> NDEC_SCAN_TRUNCATED (caller SUSPENDs)
 *    is_final -> NDEC_SCAN_INVALID   (caller errors out) */
INLINE NdecSpanResult ndec_string_span(uint64_t bits, const uint8_t *buf_end, const uint8_t *chunk_ptr,
                                       NdecScanState *state, Utf8Checker *utf8, int is_final,
                                       uint32_t open_offset) {
  uint8_t has_escape = 0;
  uint64_t bs_bits   = state->last_backslash & ~(((uint64_t)1 << open_offset) - 1);
  for (;;) {
    uint32_t idx;
    if (!ndec_ctz64_empty(bits, &idx)) {
      const uint8_t *hit = chunk_ptr + idx;
      bits               = ndec_clear_lowest_bit(bits);
      if (*hit == '"') {
        uint64_t content_bs = bs_bits & (((uint64_t)1 << idx) - 1);
        has_escape |= (content_bs != 0);
        return (NdecSpanResult){bits, chunk_ptr, hit, NDEC_SCAN_OK, has_escape};
      }
      continue;
    }
    has_escape |= (bs_bits != 0);
    NdecAdvanceResult ar = ndec_advance_chunk(chunk_ptr, buf_end, state, utf8, is_final);
    if (ar.chunk_ptr == chunk_ptr) {
      NdecScanStatus st = is_final ? NDEC_SCAN_INVALID : NDEC_SCAN_TRUNCATED;
      return (NdecSpanResult){0, chunk_ptr, NULL, st, has_escape};
    }
    chunk_ptr = ar.chunk_ptr;
    bits      = ar.bits;
    bs_bits   = state->last_backslash;
  }
}

/* Find end of number. Does NOT consume the next structural.
 *
 * On NDEC_SCAN_OK with an in-buffer hit (end < buf_end), the returned
 * `bits` retains the bit for *end (the structural at end). Callers may
 * read *end and clear the lowest bit of `bits` to consume it without
 * running ctz a second time; the in-buffer hit is signaled by bits != 0.
 *
 * When the span runs to buf_end without hitting a non-number byte:
 *   !is_final -> NDEC_SCAN_TRUNCATED (caller SUSPENDs; more digits may come)
 *    is_final -> NDEC_SCAN_OK with end == buf_end and bits == 0 (no
 *                structural to consume; *end is not readable). */
INLINE NdecSpanResult ndec_number_span(uint64_t bits, const uint8_t *buf_end, const uint8_t *chunk_ptr,
                                       NdecScanState *state, Utf8Checker *utf8, int is_final) {
  for (;;) {
    uint32_t idx;
    if (!ndec_ctz64_empty(bits, &idx)) {
      const uint8_t *end = chunk_ptr + idx;
      /* Contract: in-buffer hit retains the structural bit so callers can
       * read *end and clear the lowest bit without running ctz again. */
      assert(bits != 0);
      return (NdecSpanResult){bits, chunk_ptr, end, NDEC_SCAN_OK, 0};
    }
    NdecAdvanceResult ar = ndec_advance_chunk(chunk_ptr, buf_end, state, utf8, is_final);
    if (ar.chunk_ptr == chunk_ptr) {
      NdecScanStatus st = is_final ? NDEC_SCAN_OK : NDEC_SCAN_TRUNCATED;
      return (NdecSpanResult){0, chunk_ptr, buf_end, st, 0};
    }
    chunk_ptr = ar.chunk_ptr;
    bits      = ar.bits;
  }
}

#ifndef NDEC_FN_DECL
#define NDEC_FN_DECL
#endif

#ifndef NDEC_FN_NAME
#define NDEC_FN_NAME ndec_sax_parse
#endif

NDEC_FN_DECL void NDEC_FN_NAME(NdecSaxContext *ctx) {

  const uint8_t *buf     = ctx->buf;
  const uint8_t *buf_end = ctx->buf_end;
  int is_final           = (int)ctx->is_final;
  /* Internally cur_pos names the last consumed structural byte. The context
   * exposes the first unconsumed byte, so normal exits publish cur_pos + 1. */
  const uint8_t *cur_pos   = ctx->cur_pos - 1;
  const uint8_t *chunk_ptr = ctx->chunk_ptr;
  uint64_t bits            = ctx->structural_bits;
  NdecScanState scan_state = ctx->scan_state;
  /* UTF-8 carry spans every input segment in the document. */
  Utf8Checker utf8  = ctx->utf8;
  int32_t sp        = ctx->sp;
  NdecFrame *frames = ctx->frames;
  void *ud          = ctx->user_data;

  int32_t _err_code;
  uint32_t _err_pos;
  uint32_t _suspend_phase;

#define CUR_OFFSET() ((uint32_t)(cur_pos - buf))

#define NDEC_SAVE_AND_RETURN(code)                                                                                \
  do {                                                                                                            \
    ctx->cur_pos         = cur_pos + 1;                                                                           \
    ctx->chunk_ptr       = chunk_ptr;                                                                             \
    ctx->structural_bits = bits;                                                                                  \
    ctx->scan_state      = scan_state;                                                                            \
    ctx->utf8            = utf8;                                                                                  \
    ctx->sp              = sp;                                                                                    \
    ctx->exit_code       = (code);                                                                                \
    return;                                                                                                       \
  } while (0)

#define GOTO_ERROR(code, pos)                                                                                     \
  do {                                                                                                            \
    _err_code = (code);                                                                                           \
    _err_pos  = (pos);                                                                                            \
    goto ndec_error_exit;                                                                                         \
  } while (0)

/* Before returning a yield, each call site commits cursor and frame effects.
 * ph names the next operation, so resume invokes each hook exactly once.
 * Negative reactor results share this exit and split into yield or error. */
#define YIELD_OR_ERROR(d, ph)                                                                                     \
  do {                                                                                                            \
    _err_code      = (d);                                                                                         \
    _err_pos       = CUR_OFFSET();                                                                                \
    _suspend_phase = (ph);                                                                                        \
    goto ndec_error_or_yield_exit;                                                                                \
  } while (0)

/* Suspend publishes the first unconsumed byte. NEXT commits the current byte,
 * HERE retries it, and AT restores an earlier atomic token boundary. */
#define SUSPEND_NEXT(ph)                                                                                          \
  do {                                                                                                            \
    _suspend_phase = (ph);                                                                                        \
    goto ndec_suspend_next_exit;                                                                                  \
  } while (0)

#define SUSPEND_HERE(ph)                                                                                          \
  do {                                                                                                            \
    _suspend_phase = (ph);                                                                                        \
    goto ndec_suspend_exit;                                                                                       \
  } while (0)

#define SUSPEND_AT(ph, rollback_pos)                                                                              \
  do {                                                                                                            \
    cur_pos        = (rollback_pos);                                                                              \
    _suspend_phase = (ph);                                                                                        \
    goto ndec_suspend_exit;                                                                                       \
  } while (0)

  /* Token helpers update scanner carry before routing truncated input to the
   * supplied rollback phase. */

#define MATCH_KEYWORD(match_fn, advance_by, resume_phase)                                                         \
  do {                                                                                                            \
    NdecScanStatus _kw = (match_fn)(cur_pos, buf_end, is_final);                                                  \
    if (UNLIKELY(_kw != NDEC_SCAN_OK)) {                                                                          \
      if (_kw == NDEC_SCAN_TRUNCATED) SUSPEND_HERE(resume_phase);                                                 \
      GOTO_ERROR(NDEC_ERR_KEYWORD, CUR_OFFSET());                                                                 \
    }                                                                                                             \
    cur_pos += (advance_by);                                                                                      \
  } while (0)

#define PARSE_STRING_SPAN(out_end, out_has_escape, resume_phase, rollback_pos)                                    \
  do {                                                                                                            \
    uint32_t _open_off = (uint32_t)((rollback_pos) - chunk_ptr);                                                  \
    NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, &utf8, is_final, _open_off);     \
    bits               = _sr.bits;                                                                                \
    chunk_ptr          = _sr.chunk_ptr;                                                                           \
    (out_end)          = _sr.end;                                                                                 \
    (out_has_escape)   = _sr.has_escape;                                                                          \
    if (UNLIKELY(_sr.status != NDEC_SCAN_OK)) {                                                                   \
      if (_sr.status == NDEC_SCAN_TRUNCATED) SUSPEND_AT((resume_phase), (rollback_pos));                          \
      GOTO_ERROR(NDEC_ERR_EOF, CUR_OFFSET());                                                                     \
    }                                                                                                             \
    if (UNLIKELY(utf8_errored(&utf8))) {                                                                          \
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)((rollback_pos) - buf));                                                \
    }                                                                                                             \
  } while (0)

#define PARSE_NUMBER_SPAN(out_end, resume_phase, rollback_pos)                                                    \
  do {                                                                                                            \
    NdecSpanResult _sr = ndec_number_span(bits, buf_end, chunk_ptr, &scan_state, &utf8, is_final);                \
    bits               = _sr.bits;                                                                                \
    chunk_ptr          = _sr.chunk_ptr;                                                                           \
    (out_end)          = _sr.end;                                                                                 \
    if (UNLIKELY(_sr.status == NDEC_SCAN_TRUNCATED)) {                                                            \
      SUSPEND_AT((resume_phase), (rollback_pos));                                                                 \
    }                                                                                                             \
    if (UNLIKELY(utf8_errored(&utf8))) {                                                                          \
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)((rollback_pos) - buf));                                                \
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
    NdecAdvanceResult _ar = ndec_advance_chunk(chunk_ptr, buf_end, &scan_state, &utf8, is_final);                 \
    if (UNLIKELY(_ar.chunk_ptr == chunk_ptr)) {                                                                   \
      (out_ch_var) = NDEC_EOF;                                                                                    \
      break;                                                                                                      \
    }                                                                                                             \
    if (UNLIKELY(utf8_errored(&utf8))) {                                                                          \
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(_ar.chunk_ptr - buf));                                                 \
    }                                                                                                             \
    chunk_ptr = _ar.chunk_ptr;                                                                                    \
    bits      = _ar.bits;                                                                                         \
  } while (1)

#define STACK_PUSH(child_phase)                                                                                   \
  do {                                                                                                            \
    if (UNLIKELY(sp + 1 >= NDEC_MAX_DEPTH)) {                                                                     \
      GOTO_ERROR(NDEC_ERR_DEPTH, CUR_OFFSET());                                                                   \
    }                                                                                                             \
    sp++;                                                                                                         \
    frames[sp].phase = (child_phase);                                                                             \
    frames[sp].data  = 0;                                                                                         \
  } while (0)

#define STACK_POP() (sp--)
#define TOP_FRAME() (&frames[sp])

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

#define NDEC_DISPATCH_PHASE(ph)                                                                                   \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    NDEC_LOAD_BASE(_base);                                                                                        \
    goto *(void *)(_base + dispatch_table[(ph)]);                                                                 \
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

#include "./scb.h" // IWYU pragma: keep

  /* Bootstrap scans the current chunk because ndec_advance_chunk scans its
   * successor. On resume, zero structural bits request the same bootstrap.
   * frames[0] is the parser's root continuation sentinel. */

  if (sp >= 0) {
    if (bits == 0) {
      ptrdiff_t len = buf_end - chunk_ptr;
      if (len >= 64) {
        NdecChunkResult r = ndec_scan_chunk_sax(chunk_ptr, &scan_state);
        utf8_check_block64(&utf8, chunk_ptr);
        bits = r.structural;
      } else if (is_final && buf_end > chunk_ptr) {
        uint8_t padded[64];
        __builtin_memcpy(padded, chunk_ptr, (size_t)len);
        __builtin_memset(padded + len, 0x20, 64 - (size_t)len);
        NdecChunkResult r = ndec_scan_chunk_sax(padded, &scan_state);
        utf8_check_block64(&utf8, padded);
        bits = r.structural & (((uint64_t)1 << (uint32_t)len) - 1);
      }
      if (UNLIKELY(utf8_errored(&utf8))) {
        GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(chunk_ptr - buf));
      }
    }
    NDEC_DISPATCH_PHASE(frames[sp].phase);
  } else {
    chunk_ptr     = buf;
    ptrdiff_t len = buf_end - chunk_ptr;
    if (len >= 64) {
      NdecChunkResult r = ndec_scan_chunk_sax(chunk_ptr, &scan_state);
      utf8_check_block64(&utf8, chunk_ptr);
      bits = r.structural;
    } else if (is_final && buf_end > chunk_ptr) {
      uint8_t padded[64];
      __builtin_memcpy(padded, chunk_ptr, (size_t)len);
      __builtin_memset(padded + len, 0x20, 64 - (size_t)len);
      NdecChunkResult r = ndec_scan_chunk_sax(padded, &scan_state);
      utf8_check_block64(&utf8, padded);
      bits = r.structural & (((uint64_t)1 << (uint32_t)len) - 1);
    } else {
      bits = 0;
    }
    if (UNLIKELY(utf8_errored(&utf8))) {
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(chunk_ptr - buf));
    }

    /* Install the root sentinel: frames[0].phase = ROOT_VALUE, sp = 0.
     * Hosts may pre-fill frames[1] with their own root state before
     * calling in; the first STACK_PUSH on '{' or '[' raises sp to 1
     * and begin_object / begin_array sees that pre-filled frame. */
    frames[0].phase = NDEC_PHASE_ROOT_VALUE;
    frames[0].data  = 0;
    sp              = 0;
  }

ndec_dispatch_base:

ndec_root_value: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);

  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ROOT_DONE;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_ROOT_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ROOT_DONE;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ROOT_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
      YIELD_OR_ERROR(directive, NDEC_PHASE_ARRAY_ELEM_OR_END);
    }
    goto ndec_array_elem_or_end;
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    if (is_final) {
      GOTO_ERROR(NDEC_ERR_EOF, (uint32_t)(buf_end - buf));
    }
    SUSPEND_NEXT(NDEC_PHASE_ROOT_VALUE);
  }
  goto ndec_root_scalar;
}

ndec_root_done: {
  int32_t ch;
  NEXT_STRUCTURAL(ch);
  if (ch == NDEC_EOF) {
    /* Document-final UTF-8 finish: fold any tail multibyte lead with
     * missing continuations into the error vector. Only safe to call
     * here, where the parser has consumed everything; mid-stream we
     * skip check_eof because prev_incomplete may still be filled in
     * by the next host buffer.  is_final is implicit: NEXT_STRUCTURAL
     * only returns NDEC_EOF when ndec_advance_chunk has nothing left
     * to scan, which on a non-final stream causes SUSPEND from
     * elsewhere; here we are dispatching past the root container so
     * the document has truly ended. */
    utf8_check_eof(&utf8);
    if (UNLIKELY(utf8_errored(&utf8))) {
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(buf_end - buf));
    }
    /* Pop the root sentinel; input fully consumed. The error exit path
     * deliberately leaves the sentinel in place so callers seeing
     * sp >= 0 know the parser state is dirty. */
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
      YIELD_OR_ERROR(directive, frames[sp].phase);
    }
    NDEC_DISPATCH_PHASE(frames[sp].phase);
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
    {
      uint32_t _peek_idx;
      if (LIKELY(!ndec_ctz64_empty(bits, &_peek_idx))) {
        const uint8_t *_peek_pos = chunk_ptr + _peek_idx;
        int32_t _peek_ch         = (int32_t)*_peek_pos;
        if (LIKELY(_peek_ch == ',')) {
          cur_pos = _peek_pos;
          bits    = ndec_clear_lowest_bit(bits);
          goto ndec_object_continue_or_end_after_comma;
        }
        if (_peek_ch == '}') {
          cur_pos = _peek_pos;
          bits    = ndec_clear_lowest_bit(bits);
          goto ndec_object_continue_or_end_after_brace;
        }
      }
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
    {
      /* Unroll continue: per ndec_number_span's contract, on an
       * in-buffer hit `bits != 0` and the bit for *end is retained;
       * read *end and clear the lowest bit instead of running ctz
       * again. bits == 0 falls through to the regular dispatch. */
      if (LIKELY(bits != 0)) {
        int32_t _peek_ch = (int32_t)*end;
        if (LIKELY(_peek_ch == ',')) {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_object_continue_or_end_after_comma;
        }
        if (_peek_ch == '}') {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_object_continue_or_end_after_brace;
        }
      }
    }
    goto ndec_object_continue_or_end;
  }
  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_OBJ_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_OBJECT_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_OBJ_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
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
  ndec_object_continue_or_end_after_comma:
    /* Trailing comma is invalid; a key string must follow. Peek the next structural;
     * on EOF we roll back to the comma so the whole `,"key":` remains atomic across suspend. */
    {
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
  }
  if (ch == '}') {
  ndec_object_continue_or_end_after_brace:
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_OBJECT(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[sp].phase);
    }
    NDEC_DISPATCH_PHASE(frames[sp].phase);
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
    {
      if (LIKELY(bits != 0)) {
        int32_t _peek_ch = (int32_t)*end;
        if (LIKELY(_peek_ch == ',')) {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_array_elem_value;
        }
        if (_peek_ch == ']') {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_array_continue_or_end_after_bracket;
        }
      }
    }
    goto ndec_array_continue_or_end;
  }

  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_ARR_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ARR_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
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
  if (ch == ']') {
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_ARRAY(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[sp].phase);
    }
    NDEC_DISPATCH_PHASE(frames[sp].phase);
  }
  if (UNLIKELY(ch == NDEC_EOF)) {
    SUSPEND_NEXT(NDEC_PHASE_ARRAY_ELEM_OR_END);
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
    {
      if (LIKELY(bits != 0)) {
        int32_t _peek_ch = (int32_t)*end;
        if (LIKELY(_peek_ch == ',')) {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_array_elem_value;
        }
        if (_peek_ch == ']') {
          bits = ndec_clear_lowest_bit(bits);
          goto ndec_array_continue_or_end_after_bracket;
        }
      }
    }
    goto ndec_array_continue_or_end;
  }
  if (ch == '{') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_OBJECT_FIELD_OR_END);
    int32_t directive = NDEC_R_ARR_BEGIN_OBJECT(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
      YIELD_OR_ERROR(directive, NDEC_PHASE_OBJECT_FIELD_OR_END);
    }
    goto ndec_object_field_or_end;
  }
  if (ch == '[') {
    TOP_FRAME()->phase = NDEC_PHASE_ARRAY_CONTINUE_OR_END;
    STACK_PUSH(NDEC_PHASE_ARRAY_ELEM_OR_END);
    int32_t directive = NDEC_R_ARR_BEGIN_ARRAY(ud);
    if (UNLIKELY(directive < 0)) {
      cur_pos++;
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
  ndec_array_continue_or_end_after_comma:
    goto ndec_array_elem_value;
  }
  if (ch == ']') {
  ndec_array_continue_or_end_after_bracket:
    cur_pos++;
    STACK_POP();

    int32_t directive = NDEC_R_END_ARRAY(ud);

    if (UNLIKELY(directive < 0)) {
      YIELD_OR_ERROR(directive, frames[sp].phase);
    }
    NDEC_DISPATCH_PHASE(frames[sp].phase);
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
    if (is_final) {
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
    NdecScanStatus status;
    {
      uint32_t _open_off = (uint32_t)(quote_pos - chunk_ptr);
      NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, &utf8, is_final, _open_off);
      bits               = _sr.bits;
      chunk_ptr          = _sr.chunk_ptr;
      end                = _sr.end;
      status             = _sr.status;
    }
    if (UNLIKELY(status != NDEC_SCAN_OK)) {
      /* Both TRUNCATED and INVALID roll back to quote_pos so the whole
       * string is re-parsed on resume with more data. */
      SUSPEND_AT(NDEC_PHASE_SKIP_VALUE, quote_pos);
    }
    if (UNLIKELY(utf8_errored(&utf8))) {
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(quote_pos - buf));
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

ndec_root_scalar: {
  int32_t ch = (int32_t)*cur_pos;
  if (ch == '"') {
    const uint8_t *str_start = cur_pos + 1;
    const uint8_t *end;
    NdecScanStatus status;
    uint8_t has_escape;
    const uint8_t *_root_quote_pos = cur_pos;
    {
      uint32_t _open_off = (uint32_t)(cur_pos - chunk_ptr);
      NdecSpanResult _sr = ndec_string_span(bits, buf_end, chunk_ptr, &scan_state, &utf8, is_final, _open_off);
      bits               = _sr.bits;
      chunk_ptr          = _sr.chunk_ptr;
      end                = _sr.end;
      status             = _sr.status;
      has_escape         = _sr.has_escape;
    }
    if (UNLIKELY(status != NDEC_SCAN_OK)) {
      if (status == NDEC_SCAN_TRUNCATED) SUSPEND_HERE(NDEC_PHASE_ROOT_VALUE);
      GOTO_ERROR(NDEC_ERR_EOF, CUR_OFFSET());
    }
    if (UNLIKELY(utf8_errored(&utf8))) {
      GOTO_ERROR(NDEC_ERR_UTF8, (uint32_t)(_root_quote_pos - buf));
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
    /* The root sentinel keeps frames[sp] valid. Store the call site's next
     * operation before returning control to the host. */
    frames[sp].phase = _suspend_phase;

    /* A drained structural mask advances chunk_ptr past consumed input, so
     * bootstrap resumes from the remaining region. */
    if (bits == 0) {
      const uint8_t *effective = cur_pos < buf_end ? cur_pos : buf_end;
      if (effective > chunk_ptr) {
        chunk_ptr = effective;
      }
    }

    /* Yield and reactor errors publish the same source position contract. */
    ctx->error_pos       = _err_pos;
    ctx->cur_pos         = cur_pos;
    ctx->chunk_ptr       = chunk_ptr;
    ctx->structural_bits = bits;
    ctx->scan_state      = scan_state;
    ctx->utf8            = utf8;
    ctx->sp              = sp;
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
  ctx->utf8            = utf8;
  ctx->sp              = sp;
  ctx->exit_code       = _err_code;
  return;

ndec_suspend_next_exit:
  cur_pos++; /* advance past the single-byte structural just consumed */
  /* fallthrough */
ndec_suspend_exit:
  frames[sp].phase     = _suspend_phase;
  ctx->cur_pos         = cur_pos;
  ctx->chunk_ptr       = chunk_ptr;
  ctx->structural_bits = bits;
  ctx->scan_state      = scan_state;
  ctx->utf8            = utf8;
  ctx->sp              = sp;
  ctx->exit_code       = NDEC_SUSPEND;
  return;

#undef NDEC_SAVE_AND_RETURN
#undef GOTO_ERROR
#undef YIELD_OR_ERROR
#undef SUSPEND_AT
#undef SUSPEND_HERE
#undef SUSPEND_NEXT
#undef MATCH_KEYWORD
#undef PARSE_STRING_SPAN
#undef PARSE_NUMBER_SPAN
#undef NEXT_STRUCTURAL
#undef STACK_PUSH
#undef STACK_POP
#undef TOP_FRAME
#undef CUR_OFFSET
#undef NDEC_DISPATCH_PHASE
#undef NDEC_LOAD_BASE
#undef NDEC_R_BEGIN_OBJECT
#undef NDEC_R_END_OBJECT
#undef NDEC_R_OBJECT_FIELD
#undef NDEC_R_BEGIN_ARRAY
#undef NDEC_R_END_ARRAY
#undef NDEC_R_SCALAR_NULL
#undef NDEC_R_SCALAR_BOOL
#undef NDEC_R_SCALAR_NUMBER
#undef NDEC_R_SCALAR_STRING
#undef NDEC_R_OBJ_SCALAR_NULL
#undef NDEC_R_OBJ_SCALAR_BOOL
#undef NDEC_R_OBJ_SCALAR_NUMBER
#undef NDEC_R_OBJ_SCALAR_STRING
#undef NDEC_R_ARR_SCALAR_NULL
#undef NDEC_R_ARR_SCALAR_BOOL
#undef NDEC_R_ARR_SCALAR_NUMBER
#undef NDEC_R_ARR_SCALAR_STRING
#undef NDEC_R_ROOT_SCALAR_NULL
#undef NDEC_R_ROOT_SCALAR_BOOL
#undef NDEC_R_ROOT_SCALAR_NUMBER
#undef NDEC_R_ROOT_SCALAR_STRING
#undef NDEC_R_ROOT_BEGIN_OBJECT
#undef NDEC_R_ROOT_BEGIN_ARRAY
#undef NDEC_R_OBJ_BEGIN_OBJECT
#undef NDEC_R_OBJ_BEGIN_ARRAY
#undef NDEC_R_ARR_BEGIN_OBJECT
#undef NDEC_R_ARR_BEGIN_ARRAY
}

#endif // !NDEC_CORE_SAX_H
