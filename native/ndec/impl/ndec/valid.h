/*
 * Validation-only walk over the structural index produced by the DOM
 * scanner. Mirrors dom_build_tape_impl's state machine but writes no tape,
 * no arena, and no counts: every accept/reject decision comes from the same
 * index discipline, so the two walkers agree on the accepted language.
 *
 * The stage-2 primitives stay read-only: escape-free strings validate for
 * free (the scanner already proved their close quote and rejected control
 * bytes), escaped bodies walk only their escape sequences, numbers check
 * grammar without parsing a value, and high-bit bytes pass because
 * encoding/json accepts malformed UTF-8 in strings.
 *
 * Bounds caller contract: input has 64 bytes of 0x20 padding past `len`, and
 * the index array carries the scanner's three `len` sentinels.
 */
#ifndef NDEC_VALID_H
#define NDEC_VALID_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/delim.h"
#include "ndec/core/str.h"
#include "ndec/core/tape.h" /* JSON_DOM_MAX_DEPTH, dom_validate_atom_ptr */

/* Validate one escape sequence. *pp points at the byte after '\'. Advances
 * *pp past the escape on success. Surrogate pairing is decode policy, not
 * validity: each \uXXXX stands alone. */
INLINE int ndec_valid_escape(const uint8_t **pp) {
  const uint8_t *s = *pp;
  uint8_t c        = *s;
  if (c != 'u') {
    uint8_t mapped;
    if (!ndec_str_simple_escape(c, &mapped)) return -1;
    *pp = s + 1;
    return 0;
  }
  uint32_t r;
  if (!ndec_str_hex4(s + 1, &r)) return -1;
  *pp = s + 5;
  return 0;
}

/* Validate one string body. body points just past the opening quote. The
 * scanner's unclosed-string rejection plus the 0x20 padding contract
 * guarantee a real close quote ahead, so the walk needs no bounds checks.
 * High-bit bytes are accepted verbatim: malformed UTF-8 remains valid JSON
 * to encoding/json and to this library's Valid. */
NOINLINE static int ndec_valid_string(const uint8_t *body) {
  const uint8_t *si = body;
#if NDEC_STR_CHUNK
  for (;;) {
    ndec_str_mask bs, qt;
    int hi;
    ndec_str_chunk_scan_noload(si, &bs, &qt, &hi);
    (void)hi;
    /* Quote strictly before the lowest backslash closes the string. */
    if (((bs - 1) & qt) != 0) return 0;
    if (UNLIKELY(bs != 0)) {
      const uint8_t *next = si + ndec_str_mask_ctz(bs) + 1;
      if (ndec_valid_escape(&next) < 0) return -1;
      si = next;
      continue;
    }
    si += NDEC_STR_CHUNK;
  }
#else
  for (;;) {
    uint8_t c = *si;
    if (c == '"') return 0;
    if (UNLIKELY(c == '\\')) {
      si++;
      if (ndec_valid_escape(&si) < 0) return -1;
      continue;
    }
    si++;
  }
#endif
}

/* Grammar-only number check: leading zero, mandatory digits after '-' and
 * '.', mandatory digits after an exponent sign, and a delimiter after the
 * token. No value is parsed, so out-of-range tokens such as 1e999 stay
 * valid exactly as they are to encoding/json. */
INLINE int ndec_valid_number(const uint8_t *p) {
  if (*p == '-') p++;
  const uint8_t *digits = p;
  while (*p >= '0' && *p <= '9')
    p++;
  size_t n = (size_t)(p - digits);
  if (n == 0 || (digits[0] == '0' && n > 1)) return -1;
  if (*p == '.') {
    p++;
    if (UNLIKELY(*p < '0' || *p > '9')) return -1;
    while (*p >= '0' && *p <= '9')
      p++;
  }
  if (*p == 'e' || *p == 'E') {
    p++;
    if (*p == '+' || *p == '-') p++;
    if (UNLIKELY(*p < '0' || *p > '9')) return -1;
    while (*p >= '0' && *p <= '9')
      p++;
  }
  if (UNLIKELY(is_non_delim(*p))) return -1;
  return 0;
}

/* Validate one scalar whose first byte is neither a quote nor a container
 * open. The scanner marks every non-structural, non-whitespace byte after a
 * structural as a scalar start, so rejects land here as unknown first
 * bytes, bad atoms, or bad numbers. Advances *idx_pp past the scalar's
 * index. */
INLINE int ndec_valid_primitive(const uint8_t *buf, const uint32_t **idx_pp) {
  const uint8_t *value = buf + *(*idx_pp)++;
  uint8_t c            = *value;
  if (c == '"') return ndec_valid_string(value + 1);
  if (c == '-' || (c - '0') < 10) return ndec_valid_number(value);
  if (c == 't' || c == 'f' || c == 'n') return dom_validate_atom_ptr(value, c);
  return -1;
}

#define VAL_PEEK()         (buf[*idx_p])
#define VAL_ADVANCE()      (buf + *idx_p++)
#define VAL_ADVANCE_CHAR() (buf[*idx_p++])

/* One bit per open container: set for arrays, clear for objects. Each
 * push writes its own bit and each pop leaves every bit above the new
 * depth untouched, so after a pop bit[depth] names the parent and
 * bit[depth + 1] the just-closed root when depth falls to -1. */
typedef struct NdecValidFrames {
  uint64_t is_array[JSON_DOM_MAX_DEPTH / 64];
} NdecValidFrames;

#define VAL_PUSH(fr_, is_arr_)                                                                                    \
  do {                                                                                                            \
    int32_t _nd = depth + 1;                                                                                      \
    if (UNLIKELY(_nd >= JSON_DOM_MAX_DEPTH)) return -1;                                                           \
    if (is_arr_) (fr_)->is_array[_nd >> 6] |= (uint64_t)1 << (_nd & 63);                                          \
    else                                                                                                          \
      (fr_)->is_array[_nd >> 6] &= ~((uint64_t)1 << (_nd & 63));                                                  \
    depth = _nd;                                                                                                  \
  } while (0)

NOINLINE static int ndec_valid_walk_impl(const uint8_t *buf, const uint32_t *idx, uint32_t n_idx,
                                         NdecValidFrames *fr) {
  const uint32_t *idx_p   = idx;
  const uint32_t *idx_end = idx + n_idx;
  int32_t depth           = -1;

  if (UNLIKELY(idx_p >= idx_end)) return -1;

  {
    uint8_t root = VAL_PEEK();
    if (root == '{') {
      idx_p++;
      if (VAL_PEEK() == '}') {
        idx_p++;
        goto document_end;
      }
      goto object_begin;
    }
    if (root == '[') {
      idx_p++;
      if (VAL_PEEK() == ']') {
        idx_p++;
        goto document_end;
      }
      goto array_begin;
    }
    goto root_scalar;
  }

object_begin:
  VAL_PUSH(fr, 0);
  {
    const uint8_t *key = VAL_ADVANCE();
    if (*key != '"') return -1;
    if (ndec_valid_string(key + 1)) return -1;
    if (VAL_ADVANCE_CHAR() != ':') return -1;
    goto object_field;
  }

object_field: {
  uint8_t ch = VAL_PEEK();
  if (ch == '{') {
    idx_p++;
    if (VAL_PEEK() == '}') {
      idx_p++;
    } else {
      goto object_begin;
    }
  } else if (ch == '[') {
    idx_p++;
    if (VAL_PEEK() == ']') {
      idx_p++;
    } else {
      goto array_begin;
    }
  } else {
    if (ndec_valid_primitive(buf, &idx_p)) return -1;
  }
}

object_continue: {
  uint8_t ch = VAL_ADVANCE_CHAR();
  if (ch == ',') {
    const uint8_t *key = VAL_ADVANCE();
    if (*key != '"') return -1;
    if (ndec_valid_string(key + 1)) return -1;
    if (VAL_ADVANCE_CHAR() != ':') return -1;
    goto object_field;
  }
  if (ch == '}') {
    depth--;
    goto scope_end;
  }
  if (ch == 0x20) {
    goto document_end;
  }
  return -1;
}

array_begin:
  VAL_PUSH(fr, 1);

array_value: {
  uint8_t ch = VAL_PEEK();
  if (ch == '{') {
    idx_p++;
    if (VAL_PEEK() == '}') {
      idx_p++;
    } else {
      goto object_begin;
    }
  } else if (ch == '[') {
    idx_p++;
    if (VAL_PEEK() == ']') {
      idx_p++;
    } else {
      goto array_begin;
    }
  } else {
    if (ndec_valid_primitive(buf, &idx_p)) return -1;
  }
}

array_continue: {
  uint8_t ch = VAL_ADVANCE_CHAR();
  if (ch == ',') {
    goto array_value;
  }
  if (ch == ']') {
    depth--;
    goto scope_end;
  }
  if (ch == 0x20) {
    goto document_end;
  }
  return -1;
}

scope_end: {
  /* Parent container at bit[depth]; at root close (depth == -1) the closed
   * root itself sits at bit[0]. Either way the target continue phase reads
   * the next index: the 0x20 sentinel ends the document, anything else but
   * the phase's own operators is trailing content. */
  uint32_t kidx = (depth >= 0) ? (uint32_t)depth : 0u;
  if ((fr->is_array[kidx >> 6] >> (kidx & 63)) & 1) goto array_continue;
  goto object_continue;
}

root_scalar:
  if (ndec_valid_primitive(buf, &idx_p)) return -1;

document_end:
  /* depth == -1 iff the root was closed or a root scalar was consumed and
   * nothing is left open; depth >= 0 means a container never closed and
   * depth < -1 means unmatched closes ran past the root. */
  if (depth != -1) return -1;
  /* idx_p == idx_end arrives via the clean paths; idx_p == idx_end + 1 when
   * a root container's close fell through to *_continue and consumed the
   * sentinel. Both are valid ends; anything else is trailing content or an
   * over-read. */
  if (idx_p != idx_end && idx_p != idx_end + 1) return -1;
  return 0;
}

#undef VAL_PEEK
#undef VAL_ADVANCE
#undef VAL_ADVANCE_CHAR
#undef VAL_PUSH

/* The exported walk: owns the frame bitset so the impl body keeps its
 * locals in registers. */
NOINLINE static int ndec_valid_walk(const uint8_t *buf, const uint32_t *idx, uint32_t n_idx) {
  NdecValidFrames fr = {0, 0, 0, 0};
  return ndec_valid_walk_impl(buf, idx, n_idx, &fr);
}

#endif /* NDEC_VALID_H */
