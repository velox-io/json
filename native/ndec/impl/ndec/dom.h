/* The copy and zero-copy DOM entries share a structural scan and flat tape
 * emitter. Each tape word stores an ASCII tag in the high byte and a 56-bit
 * payload; binary numbers carry an adjacent value word. */
#ifndef JSON_DOM_H
#define JSON_DOM_H

#include "core/extract.h"
#include "core/tape.h"
#include "macros.h"
#include "ndec/core/alloc.h"

/* json_dom owns the DOM structural buffer and allocator state. emit is first so
 * DOM helpers can use &d->emit as a tape_emit_ctx. The bind path uses parallel
 * flat-emitter primitives with its own cursor and Go-provided arenas. */
typedef struct json_dom {
  tape_emit_ctx emit; /* first field, offset 0: &d->emit is a valid tape_emit_ctx* */

  uint32_t *structural_indexes;
  uint32_t structural_cap;
  uint32_t n_structural_indexes;

  size_t tape_cap;

  /* Allocator for tape/str_arena/structural_indexes/atof.
   * Set this before dom_ensure_capacity to use a custom allocator. */
  ndec_allocator allocator;
} json_dom;
_Static_assert(offsetof(json_dom, emit) == 0, "emit must be at offset 0 for json_dom* -> tape_emit_ctx* decay");

/* Convenience macro: pass a json_dom* to dom_* primitives, which take
 * tape_emit_ctx*. Safe because emit is at offset 0 (asserted above). */
#define DOM_EMIT(d) (&(d)->emit)

/* Ensure the DOM buffers support allocation-free parsing of len input bytes.
 * The caller installs d->allocator first. The bounds cover both string modes. */
INLINE int dom_ensure_capacity(json_dom *d, size_t len) {
  /* structural indexes: every byte could be a structural, plus
   * slack for dom_extract_bits' unconditional EMIT4_INDEXES stores
   * past the actual count (overwritten by the next chunk). */
  if (d->structural_cap < len + 24) {
    size_t cap   = len + 24;
    size_t bytes = cap * sizeof(uint32_t);
    size_t old   = (size_t)d->structural_cap * sizeof(uint32_t);
    uint32_t *p  = (uint32_t *)ndec_alloc_realloc(&d->allocator, d->structural_indexes, old, bytes);
    if (!p) return -1;
    d->structural_indexes = p;
    d->structural_cap     = (uint32_t)cap;
  }

  /* tape: pathological "1,1,1,..." and "[[[[..." each cost at most
   * capacity entries, plus a few for root/container tags. */
  if (d->tape_cap < len + 3) {
    size_t cap   = len + 3;
    size_t bytes = cap * sizeof(uint64_t);
    size_t old   = d->tape_cap * sizeof(uint64_t);
    uint64_t *p  = (uint64_t *)ndec_alloc_realloc(&d->allocator, d->emit.doc.tape, old, bytes);
    if (!p) return -1;
    d->emit.doc.tape = p;
    d->tape_cap      = cap;
  }

  /* string buffer: sized once here for the whole document, so no parse ever
   * grows it, and mode-independently, so acceptance never depends on which
   * mode is in play.
   *
   * Why `len` bounds it. Every arena byte is either a decoded string byte or a
   * byte of a number kept as text, and each is charged to a distinct span of
   * the source. A string costs decoded + 1 for the '"' WINDOW terminator,
   * against a source body plus its two quotes, so it never breaks even. A
   * TAPE_NUM_RAW number is copied verbatim plus a '\0' reparse sentinel
   * (dom_store_num_text), so it costs n + 1 against a token of n bytes plus
   * its trailing separator byte; only the document's final token can lack
   * that separator inside len, so the total overhang is at most 1 and the
   * 64-byte tail below absorbs it. Since a source byte is inside a string or
   * inside a number, never both, the sum is at most len.
   *
   * ZERO_COPY uses at most the same arena space because escape-free strings may
   * alias the source.
   *
   * The 64-byte tail absorbs the SIMD copy loop's overshoot. Each tick stores a
   * full NDEC_STR_CHUNK at the write cursor before testing the mask, so the last
   * one can reach NDEC_STR_CHUNK (32) bytes past the decoded end; the '"'
   * terminator adds 1. 64 covers that with room to spare, and it is the same
   * figure bind uses for the same writer (strArenaTail in vbind/allocator.go).
   * Adding NDEC_STR_CHUNK on top would be double-counting the one overshoot the
   * tail exists for. */
  {
    size_t need = len + 64;
    if (d->emit.str_arena_cap < need) {
      uint8_t *p =
          (uint8_t *)ndec_alloc_realloc(&d->allocator, d->emit.doc.str_arena, d->emit.str_arena_cap, need);
      if (!p) return -1;
      d->emit.doc.str_arena = p;
      d->emit.str_arena_cap = need;
    }
  }

  /* atof context: allocate once, never freed until json_dom_free.
   * Owned by json_dom (DOM path); bind path borrows m->c.atof instead. */
  if (!d->emit.atof) {
    d->emit.atof = (atof_ctx *)ndec_alloc_realloc(&d->allocator, NULL, 0, sizeof(atof_ctx));
    if (!d->emit.atof) return -1;
    __builtin_memset(d->emit.atof, 0, sizeof(atof_ctx));
  }

  return 0;
}

INLINE void json_dom_free(json_dom *d) {
  ndec_alloc_free(&d->allocator, d->emit.atof, sizeof(atof_ctx));
  ndec_alloc_free(&d->allocator, d->emit.doc.tape, d->tape_cap * sizeof(uint64_t));
  ndec_alloc_free(&d->allocator, d->emit.doc.str_arena, d->emit.str_arena_cap);
  ndec_alloc_free(&d->allocator, d->structural_indexes, (size_t)d->structural_cap * sizeof(uint32_t));
  __builtin_memset(d, 0, sizeof(*d));
}

/* Parse len bytes into d through the COPY or ZERO_COPY entry. COPY stores
 * decoded bodies in str_arena. ZERO_COPY aliases escape-free bodies and stores
 * escaped bodies in str_arena. The source is immutable and must outlive d when
 * the tape contains TAPE_STRING_RAW entries. */
INLINE int json_dom_parse_impl(json_dom *d, const uint8_t *buf, size_t len, json_dom_str_mode mode) {
  if (len == 0) return -1;

  d->emit.doc.tape_len = 0;
  d->emit.doc.str_used = 0;

  d->emit.doc.src_buf = buf;
  d->emit.doc.src_len = len;

  /* The direct DOM entries validate UTF-8 and raw control bytes. */
  int err =
      ndec_scan_structurals_strict(buf, len, d->structural_indexes, &d->n_structural_indexes, d->structural_cap);
  if (err) return err;

  switch (mode) {
  case JSON_DOM_STR_ZERO_COPY:
    return dom_build_tape_zc(DOM_EMIT(d), buf, d->structural_indexes, d->n_structural_indexes);
  default:
    return dom_build_tape_copy(DOM_EMIT(d), buf, d->structural_indexes, d->n_structural_indexes);
  }
}

INLINE int json_dom_parse_copy(json_dom *d, const uint8_t *buf, size_t len) {
  return json_dom_parse_impl(d, buf, len, JSON_DOM_STR_COPY);
}

INLINE int json_dom_parse_zc(json_dom *d, const uint8_t *buf, size_t len) {
  return json_dom_parse_impl(d, buf, len, JSON_DOM_STR_ZERO_COPY);
}

INLINE uint8_t json_dom_tape_type(const json_dom_doc *doc, size_t idx) {
  return (uint8_t)(doc->tape[idx] >> 56);
}

INLINE size_t json_dom_skip_element(const json_dom_doc *doc, size_t idx) {
  switch (json_dom_tape_type(doc, idx)) {
  case '[':
  case '{':
    /* The paired index names the close for an empty container too, so no count
     * check is needed here. See tape_value_end. */
    return (size_t)(doc->tape[idx] & 0xFFFFFFFF) + 1;
  case 'l':
  case 'u':
  case 'd':
    return idx + 2;
  default:
    /* Single-word: strings, atoms, and 'D' (a number kept as source text, which
     * packs its offset and length into the one word). */
    return idx + 1;
  }
}

/* Decode a TAPE_STRING payload: bits 0..31 = str_arena offset, bits 32..55 =
 * 24-bit length. The returned pointer is into str_arena; body[len] is the quote
 * sentinel and the length in the tape word excludes it. */
INLINE const uint8_t *json_dom_get_string(const json_dom_doc *doc, uint64_t payload, uint32_t *out_len) {
  uint32_t off = (uint32_t)(payload & 0xFFFFFFFFu);
  *out_len     = (uint32_t)((payload >> 32) & 0xFFFFFFu);
  return doc->str_arena + off;
}

/* Decode a tag-free TAPE_STRING_RAW payload. Bits 0..31 hold the src_buf
 * offset and bits 32..55 hold the 24-bit length. The returned span is
 * unterminated. */
INLINE const uint8_t *json_dom_get_string_raw(const json_dom_doc *doc, uint64_t payload, uint32_t *out_len) {
  uint32_t off = (uint32_t)(payload & 0xFFFFFFFFu);
  *out_len     = (uint32_t)((payload >> 32) & 0xFFFFFFu);
  return doc->src_buf + off;
}

#endif /* JSON_DOM_H */
