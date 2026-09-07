/* The DOM scanner writes a dense array of structural byte offsets. Callers
 * reserve len + 24 slots because stepped extraction may write up to 24 entries
 * beyond the logical count before later chunks overwrite them. */
#ifndef NDEC_EXTRACT_H
#define NDEC_EXTRACT_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/chunk.h"
#include "ndec/core/utf8.h"

/* Each step writes four indexes while count advances by the actual popcount.
 * Tail writes may be nonlogical and are overwritten by the next chunk. BMI
 * tzcnt defines the zero input as 64, so every stored index is defined. The
 * caller reserves len + 24 slots for the maximum write overshoot. */
#define EMIT4_INDEXES(P, BITS, BASE, START)                                                                       \
  do {                                                                                                            \
    uint64_t _b0     = (BITS);                                                                                    \
    uint64_t _b1     = _b0 & (_b0 - 1);                                                                           \
    uint64_t _b2     = _b1 & (_b1 - 1);                                                                           \
    uint64_t _b3     = _b2 & (_b2 - 1);                                                                           \
    (BITS)           = _b3 & (_b3 - 1);                                                                           \
    (P)[(START) + 0] = (BASE) + (uint32_t)__builtin_ctzll(_b0);                                                   \
    (P)[(START) + 1] = (BASE) + (uint32_t)__builtin_ctzll(_b1);                                                   \
    (P)[(START) + 2] = (BASE) + (uint32_t)__builtin_ctzll(_b2);                                                   \
    (P)[(START) + 3] = (BASE) + (uint32_t)__builtin_ctzll(_b3);                                                   \
  } while (0)

INLINE uint32_t ndec_extract_bits(uint64_t bits, uint32_t *out, uint32_t count, uint32_t base) {
  if (bits == 0) return count;
  uint32_t *p    = out + count;
  uint64_t saved = bits;

  /* First 4 writes unconditional: handles 1..4 bit chunks with one
   * branch total. popcnt runs in parallel with these writes. */
  EMIT4_INDEXES(p, bits, base, 0);
  int total = (int)__builtin_popcountll(saved);
  if (total <= 4) return count + (uint32_t)total;

  EMIT4_INDEXES(p, bits, base, 4);
  if (total <= 8) return count + (uint32_t)total;
  EMIT4_INDEXES(p, bits, base, 8);
  if (total <= 12) return count + (uint32_t)total;
  EMIT4_INDEXES(p, bits, base, 12);
  if (total <= 16) return count + (uint32_t)total;
  EMIT4_INDEXES(p, bits, base, 16);
  if (total <= 20) return count + (uint32_t)total;
  EMIT4_INDEXES(p, bits, base, 20);
  if (total <= 24) return count + (uint32_t)total;

  /* > 24 bits: rare, simple tail loop */
  for (int i = 24; i < total; i++) {
    p[i] = base + (uint32_t)__builtin_ctzll(bits);
    bits &= bits - 1;
  }
  return count + (uint32_t)total;
}
#undef EMIT4_INDEXES

/* Scan a partial final chunk (< 64 bytes). Copies src into the caller-owned
 * 64-byte padded buffer, runs one chunk scan, and appends the masked
 * structural indexes to out_indexes at `count` with `base`.
 *
 * `padded` is threaded down from ndec_scan_structurals' caller so this
 * helper's own frame stays near zero. The buffer is transient (overwritten
 * each call), so a single caller-owned slot serves every invocation.
 *
 * always_inline: padded_chunk saves the same 6 callee-saved GP regs as
 * scan_structurals (rbp/r15/r14/r13/r12/rbx). Inlining lets the pushes
 * merge, eliminating padded_chunk's 56B frame from the nosplit chain.
 * The inlined body (scan_chunk_dom + utf8_check_block64) shares spill
 * slots with scan_structurals' own inlined copies of the same functions. */
INLINE int ndec_scan_padded_chunk(const uint8_t *src, uint32_t src_len, NdecScanState *state, Utf8Checker *utf8,
                                  uint8_t *padded /* 64B aligned */, uint32_t *out_indexes, uint32_t count,
                                  uint32_t base, int strict, int count_mode, NdecPlanePop *pop) {
  __builtin_memset(padded, 0x20, 64);
  __builtin_memcpy(padded, src, src_len);
  /* The pad is 0x20 (whitespace), so it contributes to no plane and the counts
   * need no masking even though the index extraction below does. */
  NdecChunkResult r = count_mode == NDEC_COUNT_PLANES    ? ndec_scan_chunk_dom_counted(padded, state)
                      : count_mode == NDEC_COUNT_SCALARS ? ndec_scan_chunk_dom_scount(padded, state)
                                                         : ndec_scan_chunk_dom(padded, state);
  if (count_mode != NDEC_COUNT_NONE) NDEC_PLANE_POP_ADD(pop, r);
  if (strict) utf8_check_block64(utf8, padded);
  uint64_t mask = ((uint64_t)1 << src_len) - 1;
  return (int)ndec_extract_bits(r.structural & mask, out_indexes, count, base);
}

/* Structural scan core. `strict` and `ctl` are compile-time constants at every
 * NOINLINE entry point below, so the validation paths fold away entirely.
 * Keeping one body behind always_inline lets the compiler share SIMD spill
 * slots between structural and UTF-8 scanning. A runtime branch inside this
 * body would keep both paths' spills live and exceed the amd64 frame budget.
 *
 * Strict scanning validates raw UTF-8 across the complete input and rejects
 * unescaped bytes below 0x20 inside JSON strings. ctl rejects the control
 * bytes alone, without the UTF-8 pass: Valid accepts malformed UTF-8 the way
 * encoding/json does, while still refusing raw control bytes. Escape syntax
 * and the rest of the JSON grammar are validated independently by the
 * binding walk.
 *
 * Owns its scratch (NdecScanState + Utf8Checker + 64B padded input) inline.
 * Callers pass only the source buffer and output storage. */
static inline __attribute__((always_inline)) int
ndec_scan_structurals_impl(const uint8_t *buf, size_t len, uint32_t *out_indexes, uint32_t *out_count,
                           uint32_t capacity, int strict, int ctl, int count_mode, NdecPlanePop *out_pop) {
  /* Accumulated at scan time, not at extraction time. The hot loop defers each
   * chunk's index extraction by one iteration (prev_bits / prev_r1_bits), so
   * counting there would mean carrying the planes across the pipeline too. The
   * counts have no such dependency: they are loop-invariant accumulators. */
  NdecPlanePop pop = {0, 0, 0};
#define SCAN_CHUNK(p_, st_)                                                                                       \
  __extension__({                                                                                                 \
    NdecChunkResult r_ = count_mode == NDEC_COUNT_PLANES                                                          \
                             ? ndec_scan_chunk_dom_counted((p_), (st_))                                           \
                             : (count_mode == NDEC_COUNT_SCALARS ? ndec_scan_chunk_dom_scount((p_), (st_))        \
                                                                 : ndec_scan_chunk_dom((p_), (st_)));             \
    if (count_mode != NDEC_COUNT_NONE) NDEC_PLANE_POP_ADD(&pop, r_);                                              \
    r_;                                                                                                           \
  })
  NdecScanState state;
  Utf8Checker utf8;
  uint8_t padded[64] __attribute__((aligned(16)));
  __builtin_memset(&state, 0, sizeof(state));
  state.prev_structural_or_ws = 1;
  if (strict) utf8_checker_init(&utf8);

  /* Buffer must hold at most one structural per input byte plus 24
   * slots of slack for dom_extract_bits' unconditional EMIT4_INDEXES stores
   * past the actual count (overwritten by next chunk; final chunk's
   * garbage is bounded). */
  if (capacity < (uint32_t)len + 24) return -1;

  const uint8_t *buf_end = buf + len;
  uint32_t count         = 0;

  if (len < 64) {
    count = (uint32_t)ndec_scan_padded_chunk(buf, (uint32_t)len, &state, &utf8, padded, out_indexes, count, 0u,
                                             strict, count_mode, &pop);
    if (state.prev_in_string) return -1;
    if ((strict || ctl) && state.control_error) return -1;
    if (strict) {
      utf8_check_eof(&utf8);
      if (utf8_errored(&utf8)) return -1;
    }
    out_indexes[count]     = (uint32_t)len;
    out_indexes[count + 1] = (uint32_t)len;
    out_indexes[count + 2] = (uint32_t)len;
    *out_count             = count;
    if (count_mode != NDEC_COUNT_NONE) *out_pop = pop;
    return 0;
  }

  NdecChunkResult r0 = SCAN_CHUNK(buf, &state);
  if (strict) utf8_check_block64(&utf8, buf);
  uint64_t prev_bits = r0.structural;
  uint32_t prev_base = 0;

  /* Pre-compute how many 128-byte steps follow the first chunk.
   * This eliminates per-iteration bounds checks in the hot loop. */
  size_t data_after   = len - 64;
  size_t full_steps   = data_after / 128;
  const uint8_t *rest = buf + 64 + full_steps * 128;

  uint64_t prev_r1_bits = 0;
  uint32_t prev_r1_base = 0;
  int have_r1           = 0;

  for (size_t i = 0; i < full_steps; i++) {
    uint32_t b1_base = 64 + (uint32_t)(i * 128);
    uint32_t b2_base = b1_base + 64;

    NdecChunkResult r1 = SCAN_CHUNK(buf + b1_base, &state);
    NdecChunkResult r2 = SCAN_CHUNK(buf + b2_base, &state);
    if (strict) {
      utf8_check_block64(&utf8, buf + b1_base);
      utf8_check_block64(&utf8, buf + b2_base);
    }

    count = ndec_extract_bits(prev_bits, out_indexes, count, prev_base);
    if (have_r1) {
      count = ndec_extract_bits(prev_r1_bits, out_indexes, count, prev_r1_base);
    }
    have_r1 = 1;

    prev_bits    = r1.structural;
    prev_r1_bits = r2.structural;
    prev_base    = b1_base;
    prev_r1_base = b2_base;
  }

  for (;;) {
    ptrdiff_t rem = buf_end - rest;

    if (rem >= 64) {
      NdecChunkResult r = SCAN_CHUNK(rest, &state);
      if (strict) utf8_check_block64(&utf8, rest);
      uint32_t cur_base = (uint32_t)(rest - buf);

      count = ndec_extract_bits(prev_bits, out_indexes, count, prev_base);
      if (have_r1) {
        count   = ndec_extract_bits(prev_r1_bits, out_indexes, count, prev_r1_base);
        have_r1 = 0;
      }

      prev_bits = r.structural;
      prev_base = cur_base;
      rest += 64;
    } else if (rem > 0) {
      count = ndec_extract_bits(prev_bits, out_indexes, count, prev_base);
      if (have_r1) {
        count = ndec_extract_bits(prev_r1_bits, out_indexes, count, prev_r1_base);
      }
      count = (uint32_t)ndec_scan_padded_chunk(rest, (uint32_t)rem, &state, &utf8, padded, out_indexes, count,
                                               (uint32_t)(rest - buf), strict, count_mode, &pop);
      break;
    } else {
      /* flush deferred chunks */
      count = ndec_extract_bits(prev_bits, out_indexes, count, prev_base);
      if (have_r1) {
        count = ndec_extract_bits(prev_r1_bits, out_indexes, count, prev_r1_base);
      }
      break;
    }
  }

  /* Unclosed-string guard: If we exited with prev_in_string set, the document
   * has an open quote with no matching close. Reject here so the tape builder's
   * string parser can assume every string has a terminating quote within the
   * input buffer and drop its per-byte buf_end checks. */
  if (state.prev_in_string) return -1;
  if ((strict || ctl) && state.control_error) return -1;

  /* Fold any tail multibyte lead with missing continuations into the error
   * vector and reject if any byte was malformed. */
  if (strict) {
    utf8_check_eof(&utf8);
    if (utf8_errored(&utf8)) return -1;
  }

  /* Sentinel offsets past the last real structural so the tape builder can omit
   * per-step at_end guards. Stage2 will read up to 2 sentinel slots ahead
   * (object_continue / array_continue + lookahead). All point to `len`,
   * which addresses padded space (0x20) in the input buffer; any read of
   * buf[len] returns whitespace, falling through the tape builder's switch defaults
   * to -1 instead of misparsing. The caller's structural_cap is
   * (max_json_bytes + 64), so 3 extra uint32 slots are always available. */
  out_indexes[count]     = (uint32_t)len;
  out_indexes[count + 1] = (uint32_t)len;
  out_indexes[count + 2] = (uint32_t)len;

  *out_count = count;
  if (count_mode != NDEC_COUNT_NONE) *out_pop = pop;
  return 0;
}
#undef SCAN_CHUNK

/* Entry points. NOINLINE keeps scratch on the scanner frame. Each call passes
 * literal strict, ctl, and count modes into the always_inline body, producing
 * branch-free specializations. */

NOINLINE static int ndec_scan_structurals(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                          uint32_t *out_count, uint32_t capacity) {
  NdecPlanePop ignored;
  return ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 0, 0, NDEC_COUNT_NONE, &ignored);
}

NOINLINE static int ndec_scan_structurals_strict(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                 uint32_t *out_count, uint32_t capacity) {
  NdecPlanePop ignored;
  return ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 1, 0, NDEC_COUNT_NONE, &ignored);
}

/* Control-byte validation without the UTF-8 pass: the scanner mode Valid
 * needs, because encoding/json accepts malformed UTF-8 in strings while
 * still rejecting raw control bytes. */
NOINLINE static int ndec_scan_structurals_ctl(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                              uint32_t *out_count, uint32_t capacity) {
  NdecPlanePop ignored;
  return ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 0, 1, NDEC_COUNT_NONE, &ignored);
}

NOINLINE static int ndec_scan_structurals_counted(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                  uint32_t *out_count, uint32_t capacity, NdecPlanePop *out_pop) {
  return ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 0, 0, NDEC_COUNT_PLANES, out_pop);
}

NOINLINE static int ndec_scan_structurals_strict_counted(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                         uint32_t *out_count, uint32_t capacity,
                                                         NdecPlanePop *out_pop) {
  return ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 1, 0, NDEC_COUNT_PLANES, out_pop);
}

/* Scalar-only counting for the DOM tape bound: one popcount per chunk, with
 * every other plane derived from the structural total the scan already
 * reports (see ndec_scan_chunk_dom_scount). */
NOINLINE static int ndec_scan_structurals_scount(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                 uint32_t *out_count, uint32_t capacity, uint32_t *out_scalars) {
  NdecPlanePop pop;
  int err = ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 0, 0, NDEC_COUNT_SCALARS, &pop);
  if (!err) *out_scalars = pop.scalars;
  return err;
}

NOINLINE static int ndec_scan_structurals_strict_scount(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                        uint32_t *out_count, uint32_t capacity,
                                                        uint32_t *out_scalars) {
  NdecPlanePop pop;
  int err = ndec_scan_structurals_impl(buf, len, out_indexes, out_count, capacity, 1, 0, NDEC_COUNT_SCALARS, &pop);
  if (!err) *out_scalars = pop.scalars;
  return err;
}

/* ================= Window scanner (incremental input) =================
 *
 * Scans one input window and publishes a stable prefix: the structural
 * indexes and the atomic tail that the caller relocates to the head of the
 * next window. Every window starts at a token boundary (the previous
 * window's tail_start, or document position 0), so the scan state starts
 * fresh each call.
 *
 * The stable prefix ends before any token whose completion depends on bytes
 * past the window. Three withholding rules decide the tail, each O(1) or a
 * one-token scan away and each withholding exactly one structural:
 *   open string          the scan ends inside a string; the tail starts at
 *                        its opening quote, the last emitted structural
 *   trailing quote       the last structural is an opening quote with no
 *                        follower; it may be a key whose ':' has not
 *                        arrived, so the quote relocates
 *   unterminated scalar  the last structural starts a scalar with no
 *                        delimiter after it; more digits or keyword letters
 *                        may arrive
 * The trailing-quote rule is conservative: a trailing value string also
 * relocates and the next window rescans one token. Precision belongs to
 * the binder, which holds the real parse context. The scanner's contract
 * is only that each published structural is safe to consume: a published
 * quote has its follower structural in the window, so the string is closed
 * and a key's ':' is present; a published scalar is delimited, or ends a
 * final window. A final window skips the withholding rules entirely, so
 * its last quote may publish without a follower.
 *
 * status is a scan-level fact; the caller owns policy:
 *   INCOMPLETE the window is usable. Root completion is the binder's
 *              knowledge; the scanner never claims it.
 *   INVALID    a scan-level error that more bytes cannot repair: a raw
 *              control byte inside a string, malformed UTF-8, or an
 *              unclosed string at end of input. The capacity overrun
 *              reports INVALID with error_pos = UINT32_MAX.
 *
 * Callers reserve len + 24 index slots and provide 64 bytes of 0x20 padding
 * past len; the three sentinels written at [n_idx, n_idx + 2] hold len and
 * address that padding. */

typedef enum {
  NDEC_WINDOW_INCOMPLETE = 0,
  NDEC_WINDOW_INVALID    = 2,
} NdecWindowStatus;

enum {
  NDEC_WINDOW_ERR_NONE            = 0,
  NDEC_WINDOW_ERR_CAPACITY        = 1,
  NDEC_WINDOW_ERR_CONTROL         = 2,
  NDEC_WINDOW_ERR_UTF8            = 3,
  NDEC_WINDOW_ERR_UNCLOSED_STRING = 4,
};

typedef struct NdecWindowScan {
  uint32_t status;     /* NdecWindowStatus */
  uint32_t err;        /* NDEC_WINDOW_ERR_* when INVALID */
  uint32_t n_idx;      /* structurals in the stable prefix */
  uint32_t stable_end; /* exclusive end of the stable prefix */
  uint32_t tail_start; /* first byte of the atomic tail; equals len with no tail */
  uint32_t error_pos;  /* window-local offset when INVALID (chunk granularity
                        * for control/UTF-8, the opening quote for unclosed
                        * strings) */
} NdecWindowScan;

/* Single-pointer entry context for the window scanner. The driver owns the
 * window buffer, tail relocation, and 0x20 padding; the entry publishes the
 * stable prefix's structural indexes and the scan verdict into result. */
typedef struct NdecWindowScanCtx {
  const uint8_t *src;    /* off 0 */
  size_t len;            /* off 8 */
  uint32_t *out_indexes; /* off 16; capacity >= len + 24 */
  uint32_t capacity;     /* off 24 */
  uint32_t is_final;     /* off 28 */
  uint32_t strict;       /* off 32 */
  NdecWindowScan result; /* off 36 */
} NdecWindowScanCtx;

/* Reports whether a delimiter (whitespace or operator or real quote) appears
 * in (q, len). Escaped quotes belong to the token: escape resolution drops
 * them from the quote plane, so they emit no structural. */
INLINE int ndec_window_scalar_terminated(const uint8_t *buf, uint32_t q, uint32_t len) {
  int esc = 0;
  for (uint32_t i = q + 1; i < len; i++) {
    uint8_t c = buf[i];
    if (esc) {
      esc = 0;
      continue;
    }
    if (c == '\\') {
      esc = 1;
      continue;
    }
    if (c == '"' || c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ',' || c == ' ' ||
        c == '\t' || c == '\n' || c == '\r')
      return 1;
  }
  return 0;
}

/* Completes a multibyte sequence that straddles a non-final window edge.
 * The 0x20 pad after a partial chunk would otherwise terminate the pending
 * sequence and report it as malformed, while the next window may still
 * deliver its continuation bytes. The completion picks a second byte the
 * lead accepts (E0 wants A0+, F0..F3 want 90+, the rest accept 80) and
 * plain continuations afterwards, so the pad itself reports no error and
 * leaves real errors inside the chunk untouched. Returns the number of
 * bytes written. */
INLINE uint32_t ndec_window_complete_pending(uint8_t *padded, uint32_t rem, const uint8_t *buf, uint32_t len) {
  uint32_t look  = len < 4 ? len : 4;
  uint32_t conts = 0;
  uint8_t lead   = 0;
  for (uint32_t i = len; i-- > len - look;) {
    uint8_t c = buf[i];
    if (c >= 0x80 && c < 0xC0) {
      conts++;
      continue;
    }
    if (c >= 0xC2 && c <= 0xF4) lead = c;
    break;
  }
  if (!lead) return 0;
  uint32_t need = (lead >= 0xF0) ? 3 : (lead >= 0xE0) ? 2 : 1;
  if (conts >= need) return 0;
  uint32_t pend = need - conts;
  if (rem + pend > 64) pend = 64 - rem;
  uint8_t b2 = 0x80;
  if (conts == 0) {
    if (lead == 0xE0) b2 = 0xA0;
    else if (lead >= 0xF0 && lead <= 0xF3)
      b2 = 0x90;
  }
  for (uint32_t t = 0; t < pend; t++)
    padded[rem + t] = (t == 0 && conts == 0) ? b2 : 0x80;
  return pend;
}

static inline __attribute__((always_inline)) NdecWindowScan ndec_scan_window_impl(const uint8_t *buf, size_t len,
                                                                                  uint32_t *out_indexes,
                                                                                  uint32_t capacity, int is_final,
                                                                                  int strict) {
  NdecWindowScan res;
  res.status     = NDEC_WINDOW_INCOMPLETE;
  res.err        = NDEC_WINDOW_ERR_NONE;
  res.n_idx      = 0;
  res.stable_end = (uint32_t)len;
  res.tail_start = (uint32_t)len;
  res.error_pos  = 0;

  if (capacity < (uint32_t)len + 24) {
    res.status    = NDEC_WINDOW_INVALID;
    res.err       = NDEC_WINDOW_ERR_CAPACITY;
    res.error_pos = UINT32_MAX;
    return res;
  }

  NdecScanState state;
  Utf8Checker utf8;
  uint8_t padded[64] __attribute__((aligned(16)));
  __builtin_memset(&state, 0, sizeof(state));
  state.prev_structural_or_ws = 1;
  if (strict) utf8_checker_init(&utf8);

  uint32_t count      = 0;
  uint32_t ctrl_chunk = 0, utf8_chunk = 0;
  int ctrl_hit = 0, utf8_hit = 0;

  size_t off = 0;
  while (off < len) {
    size_t rem    = len - off;
    uint32_t base = (uint32_t)off;
    uint64_t mask;
    NdecChunkResult r;
    if (rem >= 64) {
      r = ndec_scan_chunk_dom(buf + off, &state);
      if (strict) utf8_check_block64(&utf8, buf + off);
      mask = ~(uint64_t)0;
      off += 64;
    } else {
      /* The pad is 0x20 and contributes to no plane, matching
       * ndec_scan_padded_chunk. A non-final window additionally completes
       * a pending multibyte sequence so the UTF-8 check cannot fault on
       * the pad. */
      __builtin_memset(padded, 0x20, 64);
      __builtin_memcpy(padded, buf + off, rem);
      if (strict && !is_final) ndec_window_complete_pending(padded, (uint32_t)rem, buf, (uint32_t)len);
      r = ndec_scan_chunk_dom(padded, &state);
      if (strict) utf8_check_block64(&utf8, padded);
      mask = ((uint64_t)1 << rem) - 1;
      off  = len;
    }
    if (strict && !ctrl_hit && state.control_error) {
      ctrl_hit   = 1;
      ctrl_chunk = base;
    }
    if (strict && !utf8_hit && utf8_errored(&utf8)) {
      utf8_hit   = 1;
      utf8_chunk = base;
    }

    count = ndec_extract_bits(r.structural & mask, out_indexes, count, base);
  }

  /* Definite scan errors win over tail analysis: appended bytes cannot
   * repair a raw control byte or malformed UTF-8 already inside the
   * window. */
  if (ctrl_hit) {
    res.status    = NDEC_WINDOW_INVALID;
    res.err       = NDEC_WINDOW_ERR_CONTROL;
    res.error_pos = ctrl_chunk;
    return res;
  }
  if (utf8_hit) {
    res.status    = NDEC_WINDOW_INVALID;
    res.err       = NDEC_WINDOW_ERR_UTF8;
    res.error_pos = utf8_chunk;
    return res;
  }
  if (state.prev_in_string) {
    /* The string interior emits no structurals, so the opening quote is the
     * last emitted one. */
    if (is_final) {
      res.status    = NDEC_WINDOW_INVALID;
      res.err       = NDEC_WINDOW_ERR_UNCLOSED_STRING;
      res.error_pos = out_indexes[count - 1];
      return res;
    }
    res.tail_start = out_indexes[count - 1];
    count -= 1;
  } else if (strict && is_final) {
    /* An incomplete multibyte sequence at end of input is only an error
     * once the input is final. */
    utf8_check_eof(&utf8);
    if (utf8_errored(&utf8)) {
      res.status    = NDEC_WINDOW_INVALID;
      res.err       = NDEC_WINDOW_ERR_UTF8;
      res.error_pos = (uint32_t)len;
      return res;
    }
  }

  if (count > 0 && !is_final && res.tail_start == (uint32_t)len) {
    uint32_t last_p = out_indexes[count - 1];
    uint8_t b       = buf[last_p];
    if (b == '"') {
      /* Trailing quote with no follower structural. The follower of a key
       * quote is its ':', so withholding keeps the "key": unit atomic for
       * the binder; a trailing value string relocates too, which the next
       * window rescans as one token. */
      res.tail_start = last_p;
      count -= 1;
    } else if (b != '{' && b != '}' && b != '[' && b != ']' && b != ':' && b != ',') {
      if (!ndec_window_scalar_terminated(buf, last_p, (uint32_t)len)) {
        /* Digits or keyword letters may still arrive; a scalar ending at a
         * final window is complete, so only non-final windows withhold. */
        res.tail_start = last_p;
        count -= 1;
      }
    }
  }

  out_indexes[count]     = (uint32_t)len;
  out_indexes[count + 1] = (uint32_t)len;
  out_indexes[count + 2] = (uint32_t)len;

  res.n_idx      = count;
  res.stable_end = res.tail_start;
  res.status     = NDEC_WINDOW_INCOMPLETE;
  return res;
}

NOINLINE static NdecWindowScan ndec_scan_window(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                uint32_t capacity, int is_final) {
  return ndec_scan_window_impl(buf, len, out_indexes, capacity, is_final, 0);
}

NOINLINE static NdecWindowScan ndec_scan_window_strict(const uint8_t *buf, size_t len, uint32_t *out_indexes,
                                                       uint32_t capacity, int is_final) {
  return ndec_scan_window_impl(buf, len, out_indexes, capacity, is_final, 1);
}
#endif /* NDEC_EXTRACT_H */
