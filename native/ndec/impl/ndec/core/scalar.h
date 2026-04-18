/*
 * Scalar token helpers (string, number, keyword).
 *
 * Each helper folds the streaming is_final check internally and returns
 * a small status enum, so the caller dispatches with a single switch.
 */

#ifndef NDEC_SCALAR_H
#define NDEC_SCALAR_H

#include "ndec/core/scanner.h"

/* Keyword match result. */
typedef enum {
  NDEC_KW_OK        = 0, /* atom matched */
  NDEC_KW_TRUNCATED = 1, /* not enough bytes AND !state->is_final */
  NDEC_KW_BAD       = 2, /* wrong content, or truncated under is_final */
} NdecKwResult;

/* Span result status. Returned by ndec_string_span / ndec_number_span. */
typedef enum {
  NDEC_SPAN_OK        = 0, /* closing quote found (string) / end boundary hit (number) */
  NDEC_SPAN_TRUNCATED = 1, /* ran out of data AND !state->is_final; caller must SUSPEND */
  NDEC_SPAN_INVALID   = 2, /* string_span only: unclosed string under is_final */
} NdecSpanStatus;

/* 4-byte atom comparison via uint32_t XOR. Compiler folds the constant
 * string_to_u32("true") at compile time, producing a single LDR+XOR. */
INLINE uint32_t ndec_str4_xor(const uint8_t *src, const char *atom) {
  uint32_t sv, av;
  __builtin_memcpy(&sv, src, 4);
  __builtin_memcpy(&av, atom, 4);
  return sv ^ av;
}

/* Decision table (shared by all three ndec_match_* helpers):
 *   enough bytes + match     -> OK
 *   enough bytes + mismatch  -> BAD
 *   short buffer + !is_final -> TRUNCATED
 *   short buffer + is_final  -> BAD  (truly malformed at end of stream)
 *
 * "enough" is the full keyword length (4 for null/true, 5 for false):
 * the 4-byte str4_xor could safely inspect remaining==3, but a caller
 * that took OK would then advance past buf_end. */
INLINE NdecKwResult ndec_match_null(const uint8_t *cur_pos, const uint8_t *buf_end, NdecScanState *state) {
  if (buf_end >= cur_pos + 4) {
    return ndec_str4_xor(cur_pos, "null") == 0 ? NDEC_KW_OK : NDEC_KW_BAD;
  }
  return state->is_final ? NDEC_KW_BAD : NDEC_KW_TRUNCATED;
}

INLINE NdecKwResult ndec_match_true(const uint8_t *cur_pos, const uint8_t *buf_end, NdecScanState *state) {
  if (buf_end >= cur_pos + 4) {
    return ndec_str4_xor(cur_pos, "true") == 0 ? NDEC_KW_OK : NDEC_KW_BAD;
  }
  return state->is_final ? NDEC_KW_BAD : NDEC_KW_TRUNCATED;
}

INLINE NdecKwResult ndec_match_false(const uint8_t *cur_pos, const uint8_t *buf_end, NdecScanState *state) {
  if (buf_end >= cur_pos + 5) {
    return ndec_str4_xor(cur_pos + 1, "alse") == 0 ? NDEC_KW_OK : NDEC_KW_BAD;
  }
  return state->is_final ? NDEC_KW_BAD : NDEC_KW_TRUNCATED;
}

/* Result of string_span / number_span. All by-value so caller can keep
 * bits and chunk_ptr in registers across the call. */
typedef struct NdecSpanResult {
  uint64_t bits;            /* updated structural bits */
  const uint8_t *chunk_ptr; /* updated chunk base */
  const uint8_t *end;       /* position after token (NULL on error for string) */
  NdecSpanStatus status;
  uint8_t has_escape; /* non-zero iff string content contains backslash escapes */
} NdecSpanResult;

/* Find closing quote. Reads backslash bitmap from state->last_backslash
 * (set by ndec_scan_chunk / ndec_advance_chunk) and ORs it across chunks
 * into has_escape. open_offset is the opening quote's byte offset within
 * the initial chunk; backslash bits before it are masked out so adjacent
 * strings don't pollute each other's has_escape.
 * If advance_chunk runs out of data, callee maps that onto
 *   !is_final -> NDEC_SPAN_TRUNCATED (caller SUSPENDs)
 *    is_final -> NDEC_SPAN_INVALID   (caller errors out) */
INLINE NdecSpanResult ndec_string_span(uint64_t bits, const uint8_t *buf_end, const uint8_t *chunk_ptr,
                                       NdecScanState *state, uint32_t open_offset) {
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
        return (NdecSpanResult){bits, chunk_ptr, hit, NDEC_SPAN_OK, has_escape};
      }
      continue;
    }
    has_escape |= (bs_bits != 0);
    NdecAdvanceResult ar = ndec_advance_chunk(chunk_ptr, buf_end, state);
    if (ar.chunk_ptr == chunk_ptr) {
      NdecSpanStatus st = state->is_final ? NDEC_SPAN_INVALID : NDEC_SPAN_TRUNCATED;
      return (NdecSpanResult){0, chunk_ptr, NULL, st, has_escape};
    }
    chunk_ptr = ar.chunk_ptr;
    bits      = ar.bits;
    bs_bits   = state->last_backslash;
  }
}

/* Find end of number. Does NOT consume the next structural.
 * When the span runs to buf_end without hitting a non-number byte:
 *   !is_final -> NDEC_SPAN_TRUNCATED (caller SUSPENDs; more digits may come)
 *    is_final -> NDEC_SPAN_OK        (number ends at buf_end, commit it) */
INLINE NdecSpanResult ndec_number_span(uint64_t bits, const uint8_t *buf_end, const uint8_t *chunk_ptr,
                                       NdecScanState *state) {
  for (;;) {
    uint32_t idx;
    if (!ndec_ctz64_empty(bits, &idx)) {
      const uint8_t *end = chunk_ptr + idx;
      return (NdecSpanResult){bits, chunk_ptr, end, NDEC_SPAN_OK, 0};
    }
    NdecAdvanceResult ar = ndec_advance_chunk(chunk_ptr, buf_end, state);
    if (ar.chunk_ptr == chunk_ptr) {
      NdecSpanStatus st = state->is_final ? NDEC_SPAN_OK : NDEC_SPAN_TRUNCATED;
      return (NdecSpanResult){0, chunk_ptr, buf_end, st, 0};
    }
    chunk_ptr = ar.chunk_ptr;
    bits      = ar.bits;
  }
}

#endif /* NDEC_SCALAR_H */
