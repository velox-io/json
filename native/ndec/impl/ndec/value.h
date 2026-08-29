/* The Value walker emits COPY-mode tape through flat parameters. Container
 * state reuses BindFrame slots above the parent bind depth, enforcing
 * bind_depth + value_depth <= 255. */
#ifndef NDEC_VALUE_H
#define NDEC_VALUE_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/number.h"
#include "ndec/core/str.h"
#include "ndec/core/tape.h"
#include "machine.h"
#include "primitive.h" /* bind_validate_atom */

/* str_arena is pre-sized for the document. bind_emit_start_container receives
 * only the BindFrame capacity above the parent and rejects deeper Value input. */

INLINE void bind_emit_tape_tag(uint64_t **pp, uint64_t tag, uint64_t payload) {
  *(*pp)++ = (tag) | (payload & TAPE_PAYLOAD_MASK);
}

/* The open word's paired index names the close, matching what
 * bind_emit_end_container writes for a non-empty container. See
 * dom_emit_empty_container in core/tape.h for why the two must agree. */
INLINE void bind_emit_empty_container(uint64_t **pp, uint64_t start_tag, uint64_t end_tag, uint64_t *tape_base) {
  uint32_t s = (uint32_t)(*pp - tape_base);
  bind_emit_tape_tag(pp, start_tag, (uint64_t)(s + 1)); /* close sits at s+1 */
  bind_emit_tape_tag(pp, end_tag, (uint64_t)s);
}

INLINE int bind_emit_start_container(BindFrame *stack, int stack_cap, uint64_t **pp, int is_array, int32_t *depthp,
                                     uint32_t *cur_cnt, uint32_t *cur_tidx, uint64_t *tape_base) {
  int32_t depth = *depthp;
  if (depth >= 0) { /* spill parent (root has none) */
    stack[depth].u.ctn.tape_index = *cur_tidx;
    stack[depth].u.ctn.count      = *cur_cnt;
  }
  int32_t nd = depth + 1;
  if (UNLIKELY(nd >= stack_cap)) return -1;
  *depthp   = nd;
  *cur_tidx = (uint32_t)(*pp - tape_base);
  *cur_cnt  = (uint32_t)is_array << 31; /* count=0, is_array in bit 31 */
  (*pp)++;                              /* reserve slot for start tag, patched on close */
  return 0;
}

INLINE void bind_emit_end_container(BindFrame *stack, uint64_t **pp, uint64_t start_tag, uint64_t end_tag,
                                    int32_t *depthp, uint32_t *cur_cnt, uint32_t *cur_tidx, uint64_t *tape_base) {
  uint32_t s = *cur_tidx;
  uint32_t c = *cur_cnt & DOM_CTN_COUNT_MASK; /* strip is_array bit before encode */
  if (UNLIKELY(c > 0xFFFFFFu)) c = 0xFFFFFFu; /* 24-bit count clamp; only > 16M elements */
  uint32_t e   = (uint32_t)(*pp - tape_base);
  tape_base[s] = start_tag | (uint64_t)e | ((uint64_t)c << 32);
  bind_emit_tape_tag(pp, end_tag, (uint64_t)s);

  int32_t nd = *depthp - 1;
  *depthp    = nd;
  if (nd >= 0) { /* reload parent (bit 31 carries is_array) */
    *cur_tidx = stack[nd].u.ctn.tape_index;
    *cur_cnt  = stack[nd].u.ctn.count;
  }
}

/* Decode a JSON string body into str_arena and emit a TAPE_STRING /
 * TAPE_STRING_FREE word with (offset, len) relative to str_arena_base. The
 * parser's quote sentinel follows the body: every tape-resident string must be
 * WINDOW-lookup ready, so tape keys read back through bind_lookup_key never
 * stage into scratch. */
INLINE int bind_emit_string_copy(uint8_t *str_arena_base, uint8_t **str_pp, uint64_t **pp, const uint8_t *open) {
  uint8_t *base = *str_pp;
  int esc       = 0;
  /* One unsigned compare for both failures; see dom_visit_string's COPY branch
   * in core/tape.h for why -1 and a >2^24 body land on the same test. */
  uint32_t nu = (uint32_t)ndec_str_parse(open + 1, base, &esc);
  if (UNLIKELY(nu > 0xFFFFFFu)) return -1;
  uint64_t off = (uint64_t)(base - str_arena_base);
  bind_emit_tape_tag(pp, esc ? TAPE_STRING : TAPE_STRING_FREE, off | ((uint64_t)nu << 32));
  *str_pp = base + nu + 1;
  return 0;
}

/* Dispatch one structural primitive (string / number / atom) for the value
 * walk. Uses bind_validate_atom (not dom_validate_atom_ptr) and dom_visit_number
 * (flat params, reusable). str_limit bounds the number-text copy; bind pre-sizes
 * str_arena for the whole document at parse entry and never grows it mid-walk. */
INLINE int bind_emit_primitive(const uint8_t *buf, const uint32_t **idx_pp, uint8_t *str_arena_base,
                               uint8_t **str_pp, uint64_t **pp, atof_ctx *atof, const uint8_t *str_limit) {
  uint32_t off         = *(*idx_pp)++;
  const uint8_t *value = buf + off;
  uint8_t c            = *value;
  if (c == '"') {
    return bind_emit_string_copy(str_arena_base, str_pp, pp, value);
  }
  if ((c - '0') < 10 || c == '-') {
    return dom_visit_number(atof, value, pp, str_arena_base, str_pp, str_limit);
  }
  switch (c) {
  case 't': {
    if (bind_validate_atom(value, 't')) return -1;
    *(*pp)++ = TAPE_TRUE_VAL;
    return 0;
  }
  case 'f': {
    if (bind_validate_atom(value, 'f')) return -1;
    *(*pp)++ = TAPE_FALSE_VAL;
    return 0;
  }
  case 'n': {
    if (bind_validate_atom(value, 'n')) return -1;
    *(*pp)++ = TAPE_NULL_VAL;
    return 0;
  }
  default:
    return -1;
  }
}

#endif /* NDEC_VALUE_H */
