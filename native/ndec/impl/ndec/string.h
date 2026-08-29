/* str_arena stores decoded strings and retained number text. A decoded string
 * carries a readable quote sentinel for lookup. Retained number text carries a
 * NUL sentinel for bounded numeric reparsing. Neither sentinel is part of the
 * published length. */

#ifndef NDEC_STRING_H
#define NDEC_STRING_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/str.h"
#include "ndec/core/tape.h"

INLINE void bind_write_str_header(uint8_t *dst, const uint8_t *data, uintptr_t len) {
  __builtin_memcpy(dst, &data, sizeof(data));
  *(intptr_t *)(dst + 8) = (intptr_t)len;
}

/* Intern a JSON string body into str_arena and return {data, len}. The body and quote sentinel
 * are committed together, so every published string is directly readable by lookup consumers. */
INLINE int bind_intern_str(uint8_t **str_pp, const uint8_t *open_quote, const uint8_t **data_out,
                           uint32_t *len_out) {
  uint8_t *data = *str_pp;
  int32_t n     = ndec_str_parse(open_quote + 1, data, NULL);
  if (UNLIKELY(n < 0)) return -1;
  uint32_t nu = (uint32_t)n;
  *str_pp     = data + nu + 1;
  *data_out   = data;
  *len_out    = nu;
  return 0;
}

/* Decode an escaped struct key into uncommitted arena scratch. The next key may
 * overwrite it. The quote sentinel makes the span readable by ndec_lookup_find. */
INLINE int bind_intern_key_for_lookup(uint8_t *str_p, const uint8_t *open_quote, const uint8_t **data_out,
                                      uint32_t *len_out) {
  uint8_t *data = str_p;
  int32_t n     = ndec_str_parse(open_quote + 1, data, NULL);
  if (UNLIKELY(n < 0)) return -1;
  *data_out = data;
  *len_out  = (uint32_t)n;
  return 0;
}

/* Commit the decoded string to str_arena (bumps str_p) and write a Go string
 * header {ptr,len} to dst. */
INLINE int bind_visit_str(uint8_t **str_pp, const uint8_t *open_quote, uint8_t *dst) {
  const uint8_t *data;
  uint32_t len;
  if (bind_intern_str(str_pp, open_quote, &data, &len) < 0) return -1;
  bind_write_str_header(dst, data, len);
  return 0;
}

/* Decode the inner JSON literal carried by a `,string` string field. The bounded scalar
 * walk supports an in-place destination one byte behind the source, so a direct JSON bind
 * can reuse its uncommitted outer decode without charging the arena twice. */
INLINE int bind_write_quoted_string(uint8_t **str_pp, const uint8_t *data, uint32_t len, uint8_t *dst) {
  if (len < 2 || data[0] != '"') return -1;
  const uint8_t *si  = data + 1;
  const uint8_t *end = data + len;
  uint8_t *body      = *str_pp;
  /* An overlapping outer decode may not have preserved its closing quote. The
   * bounded inner walk uses len as authority and restores that byte here. */
  ((uint8_t *)data)[len - 1] = '"';
  uint8_t *di                = body;
  while (si < end) {
    uint8_t c = *si++;
    if (c == '"') {
      if (si != end) return -1;
      *di     = '"';
      *str_pp = di + 1;
      bind_write_str_header(dst, body, (uint32_t)(di - body));
      return 0;
    }
    if (UNLIKELY(c == '\\')) {
      if (ndec_str_handle_escape(&si, &di, end) < 0) return -1;
      continue;
    }
    *di++ = c;
  }
  return -1;
}

/* tape_bind_string_ptr: decode (data, len) from any string word.
 * data points into src_buf (TAPE_STRING_RAW, zero-copy) or str_arena
 * (TAPE_STRING decoded copy, TAPE_STRING_FREE verbatim copy).
 * Shared by the string-header writer and the `,string` re-parse path. */
INLINE const uint8_t *tape_bind_string_ptr(uint64_t word, const uint8_t *str_arena, const uint8_t *src_buf,
                                           uint32_t *len_out) {
  uint32_t off = (uint32_t)(word & 0xFFFFFFFFu);
  *len_out     = (uint32_t)((word >> 32) & 0xFFFFFFu); /* 24-bit len */
  return ((uint8_t)(word >> 56) == (uint8_t)(TAPE_STRING_RAW >> 56)) ? src_buf + off : str_arena + off;
}

/* tape_bind_write_string_header: decode (off,len) from a TAPE_STRING / TAPE_STRING_RAW word,
 * pick str_arena or src as the data base, write a Go string header {ptr, len} into dst. */
INLINE void tape_bind_write_string_header(uint64_t word, uint8_t *dst, const uint8_t *str_arena,
                                          const uint8_t *src_buf) {
  uint32_t len;
  const uint8_t *data = tape_bind_string_ptr(word, str_arena, src_buf, &len);
  bind_write_str_header(dst, data, len);
}

/* A discriminator must belong to the current parse generation. Copying a tape
 * string into the append region supplies both that provenance and the lookup
 * sentinel without changing ordinary tape-bound strings. */
INLINE void tape_bind_copy_string_header(uint64_t word, uint8_t **str_pp, uint8_t *dst, const uint8_t *str_arena,
                                         const uint8_t *src_buf) {
  uint32_t len;
  const uint8_t *data = tape_bind_string_ptr(word, str_arena, src_buf, &len);
  uint8_t *copy       = *str_pp;
  __builtin_memcpy(copy, data, len);
  copy[len] = '"';
  *str_pp   = copy + len + 1;
  bind_write_str_header(dst, copy, len);
}

#endif /* NDEC_STRING_H */
