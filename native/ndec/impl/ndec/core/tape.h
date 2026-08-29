/*
 * Walks the structural index produced by the structural scanner and
 * emits a uint64 tape plus a side string buffer.
 *
 * Bounds caller contract: input has 64 bytes of 0x20 padding past `len`.
 */
#ifndef NDEC_TAPE_H
#define NDEC_TAPE_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/atof.h"
#include "ndec/core/str.h"
#include "ndec/core/number.h"

#define TAPE_PAYLOAD_MASK 0x00FFFFFFFFFFFFFFULL

#define TAPE_START_ARRAY  ((uint64_t)'[' << 56)
#define TAPE_START_OBJECT ((uint64_t)'{' << 56)
#define TAPE_END_ARRAY    ((uint64_t)']' << 56)
#define TAPE_END_OBJECT   ((uint64_t)'}' << 56)
#define TAPE_STRING       ((uint64_t)'"' << 56) /* payload: str_arena off(0..31) | len(32..55) */
#define TAPE_STRING_RAW   ((uint64_t)'R' << 56) /* payload: src off(0..31) | len(32..55) */
/* TAPE_STRING_FREE stores an escape-free body in str_arena. Requoting the body
 * reproduces its JSON spelling. */
#define TAPE_STRING_FREE ((uint64_t)'S' << 56) /* payload: str_arena off(0..31) | len(32..55) */
#define TAPE_INT64       ((uint64_t)'l' << 56)
#define TAPE_UINT64      ((uint64_t)'u' << 56)
#define TAPE_DOUBLE      ((uint64_t)'d' << 56) /* str_arena span off(0..31) | len(32..55); f64 next */

/* TAPE_NUM_RAW stores exact number text in str_arena with a NUL sentinel. Its
 * one-word payload carries the offset and length; l/u/d carry a value word. */
#define TAPE_NUM_RAW ((uint64_t)'D' << 56) /* payload: str_arena off(0..31) | len(32..55) */

#define TAPE_TRUE_VAL  ((uint64_t)'t' << 56)
#define TAPE_FALSE_VAL ((uint64_t)'f' << 56)
#define TAPE_NULL_VAL  ((uint64_t)'n' << 56)

/* Tag-byte predicate covering every string word. Sites that only need "is a
 * string" test the byte; the backing (src vs str_arena) resolves separately
 * through tape_bind_string_ptr. */
#define TAPE_IS_STRING_TAG(t)                                                                                     \
  ((t) == (TAPE_STRING >> 56) || (t) == (TAPE_STRING_RAW >> 56) || (t) == (TAPE_STRING_FREE >> 56))

/* A seam marks a control word with the top bit and packs one 31-bit relative
 * distance per logical view. A distance of one advances past the seam. Merged
 * tapes reserve a seam after each entry and widen a view's distance to bridge
 * arena gaps or omit that entry while words remain stationary. Relative
 * distances remain valid when the physical span is relocated. */
#define TAPE_SEAM_BIT ((uint64_t)1 << 63)
/* TAPE_SEAM is the seam marker with both distances zero. Kept as a name so
 * tape_seam_set reads the same as the other tag constants. */
#define TAPE_SEAM TAPE_SEAM_BIT
/* tape_is_seam: the whole discrimination, on the sign bit. */
INLINE int tape_is_seam(uint64_t w) {
  return (int64_t)w < 0;
}
/* Width of one distance field, and the shift naming each view. 31 bits caps a
 * distance at 2G words. */
#define TAPE_SEAM_BITS 31
#define TAPE_SEAM_MASK 0x7FFFFFFFu
#define TAPE_VIEW_A    0
#define TAPE_VIEW_B    TAPE_SEAM_BITS

/* A packed view mode holds the seam shift in its low bits plus independent
 * flags above TAPE_VIEW_SHIFT_MASK. Seam consumers must mask down to the shift
 * component before shifting. */
#define TAPE_VIEW_SHIFT_MASK 0x1Fu
/* The root count of a dual shared root's inline projection is published in
 * the matching close word's high24 rather than the begin word's. The flag
 * applies only at relative index zero within the descriptor's base. */
#define TAPE_MODE_COUNT_AT_CLOSE (1u << 8)
/* Semantic aliases for the two projections of a dual merged tape: view A is
 * the inline-case projection, view B the reserve-unknown projection. */
#define TAPE_DUAL_VIEW_INLINE       TAPE_VIEW_A
#define TAPE_DUAL_VIEW_RESERVE      TAPE_VIEW_B
#define TAPE_MODE_INLINE_DUAL_ROOT  (TAPE_DUAL_VIEW_INLINE | TAPE_MODE_COUNT_AT_CLOSE)
#define TAPE_MODE_RESERVE_DUAL_ROOT (TAPE_DUAL_VIEW_RESERVE)
/* A newly reserved seam: both views advance one word, so it is inert until a
 * writer widens one of them. */
#define TAPE_SEAM_RESERVED (TAPE_SEAM | 1u | ((uint64_t)1u << TAPE_SEAM_BITS))

/* tape_seam_set: build a seam word from the two per-view distances. */
INLINE uint64_t tape_seam_set(uint32_t dist_a, uint32_t dist_b) {
  return TAPE_SEAM | (uint64_t)dist_a | ((uint64_t)dist_b << TAPE_SEAM_BITS);
}

/* tape_seam_get: read one view's distance out of a seam word. */
INLINE uint32_t tape_seam_get(uint64_t w, uint32_t shift) {
  return (uint32_t)((w >> shift) & TAPE_SEAM_MASK);
}

/* Advance through seams using the selected view. Valid seams have a positive
 * distance; treating zero as one guarantees progress on malformed tape input. */
INLINE const uint64_t *tape_seam_skip(const uint64_t *p, const uint64_t *limit, uint32_t shift) {
  while (p < limit && tape_is_seam(*p)) {
    uint32_t d = tape_seam_get(*p, shift);
    p += d ? d : 1;
  }
  return p;
}

/* TapeView keeps the paired-index base, bounds limit, and seam projection
 * together. All three fields must describe the same physical tape span. */
typedef struct TapeView {
  const uint64_t *base;
  const uint64_t *limit;
  uint32_t shift;
} TapeView;

INLINE TapeView tape_view(const uint64_t *base, const uint64_t *limit, uint32_t shift) {
  TapeView tv = {base, limit, shift};
  return tv;
}

/* tape_copy_subtree_rebase: copy tape words, shifting every container's paired
 * index by delta so the copy reads correctly against its new base.
 *
 * Container START/END payloads hold the matching tag's index relative to the
 * tape's base, so moving words to a tape with a different base invalidates them.
 * delta is (index of src within its base) - (index of dst within its base): the
 * amount each paired index must shrink. Seam words are position-independent by
 * construction and pass through untouched, which is precisely why the seam
 * encoding holds relative distances rather than indices. */
INLINE void tape_copy_subtree_rebase(const uint64_t *src, uint64_t *dst, uint32_t n_words, uint32_t delta) {
  for (uint32_t i = 0; i < n_words; i++) {
    uint64_t w  = src[i];
    uint8_t tag = (uint8_t)(w >> 56);
    if (tag == (TAPE_START_OBJECT >> 56) || tag == (TAPE_START_ARRAY >> 56) || tag == (TAPE_END_OBJECT >> 56) ||
        tag == (TAPE_END_ARRAY >> 56)) {
      uint32_t idx = (uint32_t)(w & 0xFFFFFFFFu);
      w            = (w & ~(uint64_t)0xFFFFFFFFu) | (uint64_t)(idx - delta);
    }
    dst[i] = w;
  }
}

/* tape_value_end: return the position immediately past one complete value, with
 * no seam threaded at either end. Container START tags carry the matching END
 * tag's index in the low 32 bits, so containers jump straight past the END tag
 * (O(1)). Number tags (l/u/d) carry one value word; all other scalars are
 * single-word.
 *
 * The paired index names the close for an empty container as well, so this needs
 * no count check. Uniformity with non-empty containers is the emitters' job
 * (dom_emit_empty_container and friends), since count == 0 cannot distinguish them.
 *
 * Callers that want the next value word want tape_skip_value. This raw form is
 * for the merged-tape walk, which needs the exact position of an entry's
 * trailing seam (the reserved word sits immediately past the value). */
INLINE const uint64_t *tape_value_end(const uint64_t *p, TapeView tv) {
  uint64_t w  = *p++;
  uint8_t tag = (uint8_t)(w >> 56);
  switch (tag) {
  case 'l': /* INT64 */
  case 'u': /* UINT64 */
  case 'd': /* DOUBLE */
    p++;    /* value word */
    break;
  case '{': /* START_OBJECT */
  case '[': /* START_ARRAY */ {
    uint32_t end_idx = (uint32_t)(w & 0xFFFFFFFFu);
    /* >= p, not > p: p already sits one past the open word, and an empty
     * container's close is exactly there. Requiring strictly greater sent every
     * empty container down the slow walk. Zero still means the open word was
     * never patched, which no valid container can look like (its close is at
     * least one word on). */
    if (end_idx != 0 && tv.base + end_idx >= p) {
      p = tv.base + end_idx + 1;
      break;
    }
    /* Defensive: malformed payload (unpatched or backward END). Walk the slow way.
     *
     * The seam test comes FIRST. It happens to be safe last as well, since a seam
     * has its top bit set and so its byte 56 is always >= 0x80, which no ASCII tag
     * can equal (verified). But that makes correctness rest on a property of the
     * distance fields' width rather than on the marker, and the marker is the thing
     * that decides. Testing it first needs no such argument. */
    int d = 1;
    while (d > 0 && p < tv.limit) {
      uint64_t ww = *p++;
      if (tape_is_seam(ww)) {
        uint32_t sd = tape_seam_get(ww, tv.shift);
        p += (sd ? sd : 1) - 1; /* p already past the seam word */
        continue;
      }
      uint8_t t = (uint8_t)(ww >> 56);
      if (t == '{' || t == '[') d++;
      else if (t == '}' || t == ']')
        d--;
      else if (t == 'l' || t == 'u' || t == 'd')
        p++; /* the value word, never inspected */
    }
    break;
  }
  default:
    /* single-word scalar: string, raw string, true, false, null */
    break;
  }
  return p;
}

/* tape_skip_value: advance past one complete value and land on the next value
 * word of the same logical tape (seams threaded at both ends).
 *
 * Returns the advanced cursor rather than mutating in place, so callers
 * that alias the cursor through a type-punned local (TAP_CURSOR -> cursor) can
 * write the result back without taking the address of a cast. */
INLINE const uint64_t *tape_skip_value(const uint64_t *p, TapeView tv) {
  p = tape_seam_skip(p, tv.limit, tv.shift);
  return tape_seam_skip(tape_value_end(p, tv), tv.limit, tv.shift);
}

/* COPY stores decoded bodies in str_arena with a quote sentinel. ZERO_COPY
 * aliases escape-free strings from the immutable source and stores escaped
 * strings in str_arena. The tape word carries the body length in both modes. */
typedef enum {
  JSON_DOM_STR_COPY      = 0,
  JSON_DOM_STR_ZERO_COPY = 1,
} json_dom_str_mode;

typedef struct json_dom_doc {
  uint64_t *tape;
  size_t tape_len;
  uint8_t *str_arena;
  size_t str_used;
  /* ZERO_COPY string entries reference this immutable source by offset. The
   * caller keeps it alive while the document is readable. */
  const uint8_t *src_buf;
  size_t src_len;
} json_dom_doc;

#define JSON_DOM_MAX_DEPTH 256

/* Per-open-container slot on the dom_open_ctn stack.
 * `count` layout:
 *   bit  31     : is_array flag (1 = array, 0 = object), packed both
 *                in the live cur_count register cache and the spilled
 *                stack slot, so push/pop round-trips it for free and
 *                close sites read parent.is_array straight from the
 *                reloaded cur_count without a separate cache var.
 *                Bit 31 is free because real input can never reach 2^31
 *                elements in one container (structural index is u32,
 *                capping element count well below 2^30); the close-time
 *                24-bit clamp in dom_end_container caps it on tape.
 *   bits  0-30  : element count (raw; clamped to 0xFFFFFF on close) */

typedef struct dom_open_ctn {
  uint32_t tape_index;
  uint32_t count;
} dom_open_ctn;

/* Mask for the count field's element-count bits (excludes the is_array
 * flag in bit 31). Used when reloading from ctn_stack on pop. */
#define DOM_CTN_COUNT_MASK 0x7FFFFFFFu

/* tape_emit_ctx carries DOM tape and string-arena views plus the open-container
 * stack. The enclosing json_dom owns those buffers and the borrowed atof state.
 * The bind value walker emits the same format through flat parameters and
 * Go-owned arenas. */
typedef struct tape_emit_ctx {
  json_dom_doc doc;

  size_t str_arena_cap;

  dom_open_ctn ctn_stack[JSON_DOM_MAX_DEPTH];

  /* Borrowed from json_dom and released by json_dom_free. */
  atof_ctx *atof;
} tape_emit_ctx;

INLINE void dom_tape_append_tag(uint64_t **pp, uint64_t tag, uint64_t payload) {
  *(*pp)++ = (tag) | (payload & TAPE_PAYLOAD_MASK);
}

INLINE void dom_tape_skip(uint64_t **pp) {
  (*pp)++;
}

INLINE void dom_tape_append_int64(uint64_t **pp, int64_t v) {
  uint64_t u;
  __builtin_memcpy(&u, &v, 8);
  *(*pp)++ = TAPE_INT64;
  *(*pp)++ = u;
}

INLINE void dom_tape_append_uint64(uint64_t **pp, uint64_t v) {
  *(*pp)++ = TAPE_UINT64;
  *(*pp)++ = v;
}

INLINE void dom_tape_append_atom(uint64_t **pp, uint64_t tag) {
  *(*pp)++ = tag;
}

/*
 * Top-of-stack cache: the cur_* locals in dom_build_tape_impl cache the
 * currently-open container so the per-element comma branch is a register
 * RMW instead of a memory RMW. */
INLINE int dom_start_container(tape_emit_ctx *d, uint64_t **pp, int is_array, int32_t *depthp, uint32_t *cur_cnt,
                               uint32_t *cur_tidx) {
  int32_t depth = *depthp;
  if (depth >= 0) { /* spill parent (root has none) */
    d->ctn_stack[depth].tape_index = *cur_tidx;
    d->ctn_stack[depth].count      = *cur_cnt;
  }
  int32_t nd = depth + 1;
  if (UNLIKELY(nd >= JSON_DOM_MAX_DEPTH)) return -1;
  *depthp   = nd;
  *cur_tidx = (uint32_t)(*pp - d->doc.tape);
  *cur_cnt  = (uint32_t)is_array << 31; /* count=0, is_array in bit 31 */
  dom_tape_skip(pp);                    /* reserve slot for start tag, patched on close */
  return 0;
}

INLINE void dom_end_container(tape_emit_ctx *d, uint64_t **pp, uint64_t start_tag, uint64_t end_tag,
                              int32_t *depthp, uint32_t *cur_cnt, uint32_t *cur_tidx) {
  uint32_t s = *cur_tidx;
  uint32_t c = *cur_cnt & DOM_CTN_COUNT_MASK; /* strip is_array bit before encode */
  if (UNLIKELY(c > 0xFFFFFFu)) c = 0xFFFFFFu; /* 24-bit count clamp; only > 16M elements */
  uint32_t e     = (uint32_t)(*pp - d->doc.tape);
  d->doc.tape[s] = start_tag | (uint64_t)e | ((uint64_t)c << 32);
  dom_tape_append_tag(pp, end_tag, (uint64_t)s);

  int32_t nd = *depthp - 1;
  *depthp    = nd;
  if (nd >= 0) { /* reload parent (bit 31 carries is_array) */
    *cur_tidx = d->ctn_stack[nd].tape_index;
    *cur_cnt  = d->ctn_stack[nd].count;
  }
}

/* dom_emit_empty_container: an empty container as two adjacent words.
 *
 * The open word's paired index names the close, the same value a non-empty
 * container's open word carries. The two must agree, because a reader cannot
 * tell an empty container from one holding no entries any other way: count == 0
 * is what both report. So this stores s+1, matching bind_emit_end_container and
 * tape_build_patch_open; storing s+2 (one past the close) would force every
 * reader to special-case count == 0 and push tape_value_end / json_dom_skip_element
 * a word too far. */
INLINE void dom_emit_empty_container(tape_emit_ctx *d, uint64_t **pp, uint64_t start_tag, uint64_t end_tag) {
  uint32_t s = (uint32_t)(*pp - d->doc.tape);
  dom_tape_append_tag(pp, start_tag, (uint64_t)(s + 1)); /* close sits at s+1 */
  dom_tape_append_tag(pp, end_tag, (uint64_t)s);
}

/* Stage 1 identifies only the atom's opening byte. The full spelling must match
 * and its successor must be a JSON delimiter. The source has 64 readable bytes
 * of 0x20 padding, so the fixed-width comparisons and successor read remain in
 * bounds at EOF. Padding rejects truncated atoms and terminates complete atoms. */

INLINE int dom_validate_atom_ptr(const uint8_t *value, uint8_t tag) {
  if (tag == 'n' || tag == 't') {
    uint32_t sv, av;
    __builtin_memcpy(&sv, value, 4);
    __builtin_memcpy(&av, tag == 'n' ? "null" : "true", 4);
    if (sv != av) return -1;
    if (UNLIKELY(is_non_delim(value[4]))) return -1;
    return 0;
  }
  if (tag == 'f') {
    uint32_t sv, av;
    __builtin_memcpy(&sv, value + 1, 4);
    __builtin_memcpy(&av, "alse", 4);
    if (sv != av) return -1;
    if (UNLIKELY(is_non_delim(value[5]))) return -1;
    return 0;
  }
  return -1;
}

/* Decode one JSON string and emit its tape word. dom_ensure_capacity reserves
 * the whole-document arena bound before this unchecked walk. Every decoded body
 * is charged to a distinct source span. Stored strings carry a quote sentinel;
 * the tape length excludes it. */
INLINE int dom_visit_string(tape_emit_ctx *d, uint8_t **str_pp, uint64_t **pp, const uint8_t *open,
                            json_dom_str_mode mode) {
  switch (mode) {
  case JSON_DOM_STR_ZERO_COPY: {
    /* Two-stage: the scan stage never touches str_arena, so escape-free
     * strings cost zero str_arena bookkeeping. */
    uint32_t len;
    uint32_t prefix_bp;
    int32_t scan = ndec_str_parse_zc_scan(open + 1, &len, &prefix_bp);
    if (UNLIKELY(scan < 0)) return -1;
    if (scan == 1) {
      uint64_t off = (uint64_t)((open + 1) - d->doc.src_buf);
      dom_tape_append_tag(pp, TAPE_STRING_RAW, off | ((uint64_t)len << 32));
      return 0;
    }
    uint8_t *base = *str_pp;
    /* One unsigned compare for the malformed escape (-1) and for a decoded body
     * past the 24-bit length field; see the COPY branch below. The scan exit
     * above carries its own check because it returns len without decoding. */
    len = (uint32_t)ndec_str_parse_zc_continue(open + 1, base, prefix_bp);
    if (UNLIKELY(len > 0xFFFFFFu)) return -1;
    uint64_t off = (uint64_t)(base - d->doc.str_arena);
    dom_tape_append_tag(pp, TAPE_STRING, off | ((uint64_t)len << 32));
    *str_pp = base + len + 1;
    return 0;
  }
  default: {
    uint8_t *base = *str_pp;
    int esc       = 0;
    /* One unsigned compare decides both failures. The malformed-escape return is
     * -1, which as unsigned is 0xFFFFFFFF and so exceeds the 24-bit length
     * field; a body past 2^24 exceeds it directly. Testing the width instead of
     * the sign costs nothing over the `n < 0` this replaces and closes the
     * silent truncation that a separate length check would have had to pay for.
     * See TAPE_STRING for the field layout. */
    uint32_t nu = (uint32_t)ndec_str_parse(open + 1, base, &esc);
    if (UNLIKELY(nu > 0xFFFFFFu)) return -1;
    uint64_t off = (uint64_t)(base - d->doc.str_arena);
    dom_tape_append_tag(pp, esc ? TAPE_STRING : TAPE_STRING_FREE, off | ((uint64_t)nu << 32));
    *str_pp = base + nu + 1;
    return 0;
  }
  }
}

INLINE int dom_visit_primitive(const uint8_t *buf, const uint32_t **idx_pp, tape_emit_ctx *d, uint8_t **str_pp,
                               uint64_t **pp, json_dom_str_mode mode) {
  uint32_t off         = *(*idx_pp)++;
  const uint8_t *value = buf + off;
  uint8_t c            = *value;
  if (c == '"') {
    return dom_visit_string(d, str_pp, pp, value, mode);
  }
  if ((c - '0') < 10 || c == '-') {
    return dom_visit_number(d->atof, value, pp, d->doc.str_arena, str_pp, d->doc.str_arena + d->str_arena_cap);
  }
  switch (c) {
  case 't': {
    if (dom_validate_atom_ptr(value, 't')) return -1;
    dom_tape_append_atom(pp, TAPE_TRUE_VAL);
    return 0;
  }
  case 'f': {
    if (dom_validate_atom_ptr(value, 'f')) return -1;
    dom_tape_append_atom(pp, TAPE_FALSE_VAL);
    return 0;
  }
  case 'n': {
    if (dom_validate_atom_ptr(value, 'n')) return -1;
    dom_tape_append_atom(pp, TAPE_NULL_VAL);
    return 0;
  }
  default:
    return -1;
  }
}

#define SCN_PEEK()         (buf[*idx_p])
#define SCN_ADVANCE()      (buf + *idx_p++)
#define SCN_ADVANCE_CHAR() (buf[*idx_p++])

/* The COPY and ZERO_COPY wrappers supply a constant mode to this shared walker.
 * Their noinline boundary isolates the walker's register state from its caller. */
INLINE int dom_build_tape_impl(tape_emit_ctx *d, const uint8_t *buf, const uint32_t *idx, uint32_t n_idx,
                               json_dom_str_mode mode) {
  uint64_t *tape_p        = d->doc.tape;
  uint8_t *str_p          = d->doc.str_arena;
  const uint32_t *idx_p   = idx;
  const uint32_t *idx_end = idx + n_idx;
  int32_t depth           = -1;

  /* Top-of-stack cache: cur_* cache the unpacked fields of
   * ctn_stack[depth] for the currently-open container, so the
   * per-element comma branch is a register increment, not a
   * memory RMW. is_array rides in cur_count's bit 31, so close
   * sites read it straight from cur_count after pop. */
  uint32_t cur_count      = 0;
  uint32_t cur_tape_index = 0;

  if (UNLIKELY(idx_p >= idx_end)) return -1;

  {
    uint8_t root = SCN_PEEK();
    if (root == '{') {
      idx_p++;
      if (SCN_PEEK() == '}') {
        idx_p++;
        dom_emit_empty_container(d, &tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT);
        goto document_end;
      }
      goto object_begin;
    }
    if (root == '[') {
      idx_p++;
      if (SCN_PEEK() == ']') {
        idx_p++;
        dom_emit_empty_container(d, &tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY);
        goto document_end;
      }
      goto array_begin;
    }
    goto root_scalar;
  }

object_begin:
  if (dom_start_container(d, &tape_p, 0, &depth, &cur_count, &cur_tape_index)) return -1;
  {
    const uint8_t *key = SCN_ADVANCE();
    if (*key != '"') return -1;
    cur_count++; /* count bits 0->1; is_array bit (always 0 here) untouched */
    if (dom_visit_string(d, &str_p, &tape_p, key, mode)) return -1;
    if (SCN_ADVANCE_CHAR() != ':') return -1;
    goto object_field;
  }

object_field: {
  uint8_t ch = SCN_PEEK();
  if (ch == '{') {
    idx_p++;
    if (SCN_PEEK() == '}') {
      idx_p++;
      dom_emit_empty_container(d, &tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT);
    } else {
      goto object_begin;
    }
  } else if (ch == '[') {
    idx_p++;
    if (SCN_PEEK() == ']') {
      idx_p++;
      dom_emit_empty_container(d, &tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY);
    } else {
      goto array_begin;
    }
  } else {
    if (dom_visit_primitive(buf, &idx_p, d, &str_p, &tape_p, mode)) return -1;
  }
}

object_continue: {
  uint8_t ch = SCN_ADVANCE_CHAR();
  if (ch == ',') {
    cur_count++;
    const uint8_t *key = SCN_ADVANCE();
    if (*key != '"') return -1;
    if (dom_visit_string(d, &str_p, &tape_p, key, mode)) return -1;
    if (SCN_ADVANCE_CHAR() != ':') return -1;
    goto object_field;
  }
  if (ch == '}') {
    dom_end_container(d, &tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT, &depth, &cur_count, &cur_tape_index);
    goto scope_end;
  }
  if (ch == 0x20) {
    goto document_end;
  }
  return -1;
}

array_begin:
  if (dom_start_container(d, &tape_p, 1, &depth, &cur_count, &cur_tape_index)) return -1;
  cur_count++; /* count bits 0->1; is_array bit (1) preserved */

array_value: {
  uint8_t ch = SCN_PEEK();
  if (ch == '{') {
    idx_p++;
    if (SCN_PEEK() == '}') {
      idx_p++;
      dom_emit_empty_container(d, &tape_p, TAPE_START_OBJECT, TAPE_END_OBJECT);
    } else {
      goto object_begin;
    }
  } else if (ch == '[') {
    idx_p++;
    if (SCN_PEEK() == ']') {
      idx_p++;
      dom_emit_empty_container(d, &tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY);
    } else {
      goto array_begin;
    }
  } else {
    if (dom_visit_primitive(buf, &idx_p, d, &str_p, &tape_p, mode)) return -1;
  }
}

array_continue: {
  uint8_t ch = SCN_ADVANCE_CHAR();
  if (ch == ',') {
    cur_count++;
    goto array_value;
  }
  if (ch == ']') {
    dom_end_container(d, &tape_p, TAPE_START_ARRAY, TAPE_END_ARRAY, &depth, &cur_count, &cur_tape_index);
    goto scope_end;
  }

  if (ch == 0x20) {
    goto document_end;
  }
  return -1;
}

scope_end:
  if (cur_count & 0x80000000u) goto array_continue;
  goto object_continue;

root_scalar:
  if (dom_visit_primitive(buf, &idx_p, d, &str_p, &tape_p, mode)) return -1;

document_end:
  /* depth == -1 iff the root was closed (or a root scalar / empty container was emitted)
   * and nothing is left open. depth >= 0 here means a container was never closed (the 0x20
   * sentinel was reached while still nested); depth < -1 means unmatched closes ran past the
   * root. The 0x20 sentinel alone cannot tell these apart from a real root close, so depth is
   * the discriminator. */
  if (depth != -1) return -1;
  /* idx_p == idx_end arrives via the clean paths (empty container, root scalar).
   * idx_p == idx_end + 1 arrives when the root container closes and falls through to *_continue,
   * where the 0x20 padding sentinel is consumed as the terminator. Both are valid ends; anything
   * else is trailing structurals or an over-read. */
  if (idx_p != idx_end && idx_p != idx_end + 1) return -1;
  {
    d->doc.tape_len = (size_t)(tape_p - d->doc.tape);
    d->doc.str_used = (size_t)(str_p - d->doc.str_arena);
  }
  return 0;
}

#undef SCN_PEEK
#undef SCN_ADVANCE
#undef SCN_ADVANCE_CHAR

NOINLINE int dom_build_tape_copy(tape_emit_ctx *d, const uint8_t *buf, const uint32_t *idx, uint32_t n_idx) {
  return dom_build_tape_impl(d, buf, idx, n_idx, JSON_DOM_STR_COPY);
}

NOINLINE int dom_build_tape_zc(tape_emit_ctx *d, const uint8_t *buf, const uint32_t *idx, uint32_t n_idx) {
  return dom_build_tape_impl(d, buf, idx, n_idx, JSON_DOM_STR_ZERO_COPY);
}

#endif /* NDEC_TAPE_H */
