#ifndef NDEC_UNESCAPE_H
#define NDEC_UNESCAPE_H

#include <stdint.h>

#define NDEC_UNESCAPE_I_INLINE static inline __attribute__((always_inline))

NDEC_UNESCAPE_I_INLINE int ndec_unescape_is_hex(uint8_t c) {
  return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
}

NDEC_UNESCAPE_I_INLINE uint32_t ndec_unescape_hex_val(uint8_t c) {
  if (c >= '0' && c <= '9')
    return (uint32_t)(c - '0');
  if (c >= 'a' && c <= 'f')
    return (uint32_t)(c - 'a' + 10);
  return (uint32_t)(c - 'A' + 10);
}

NDEC_UNESCAPE_I_INLINE uint32_t ndec_unescape_hex4(const uint8_t *h) {
  return (ndec_unescape_hex_val(h[0]) << 12) | (ndec_unescape_hex_val(h[1]) << 8) |
         (ndec_unescape_hex_val(h[2]) << 4) | (ndec_unescape_hex_val(h[3]));
}

/* UTF-8 encode a single rune to dst (1..4 bytes). The caller must
 * guarantee dst has at least 4 bytes of headroom. */
NDEC_UNESCAPE_I_INLINE int ndec_unescape_utf8_encode(uint32_t r, uint8_t *dst) {
  if (r < 0x80) {
    dst[0] = (uint8_t)r;
    return 1;
  }
  if (r < 0x800) {
    dst[0] = (uint8_t)(0xC0 | (r >> 6));
    dst[1] = (uint8_t)(0x80 | (r & 0x3F));
    return 2;
  }
  if (r < 0x10000) {
    dst[0] = (uint8_t)(0xE0 | (r >> 12));
    dst[1] = (uint8_t)(0x80 | ((r >> 6) & 0x3F));
    dst[2] = (uint8_t)(0x80 | (r & 0x3F));
    return 3;
  }
  dst[0] = (uint8_t)(0xF0 | (r >> 18));
  dst[1] = (uint8_t)(0x80 | ((r >> 12) & 0x3F));
  dst[2] = (uint8_t)(0x80 | ((r >> 6) & 0x3F));
  dst[3] = (uint8_t)(0x80 | (r & 0x3F));
  return 4;
}

/* Single-character escape table. Returns 0 to mean "not a legal escape";
 * the caller must check before writing the result, since 0 is also a
 * valid byte and storing it raw would silently corrupt output. */
NDEC_UNESCAPE_I_INLINE uint8_t ndec_unescape_simple(uint8_t c) {
  switch (c) {
  case '"':
    return '"';
  case '\\':
    return '\\';
  case '/':
    return '/';
  case 'b':
    return '\b';
  case 'f':
    return '\f';
  case 'n':
    return '\n';
  case 'r':
    return '\r';
  case 't':
    return '\t';
  default:
    return 0;
  }
}

/*
 * Decodes JSON string content [src, src+src_len) into dst. Returns the
 * number of bytes written, or -1 on malformed escape.
 *
 * The input is not required to contain any escape; an all-plain run
 * degrades to a byte-by-byte memcpy and still produces correct output.
 */
NDEC_UNESCAPE_I_INLINE int32_t ndec_unescape(const uint8_t *src, uint32_t src_len, uint8_t *dst) {
  uint32_t i   = 0;
  uint32_t pos = 0;
  while (i < src_len) {
    uint8_t c = src[i];
    if (c != '\\') {
      dst[pos++] = c;
      i++;
      continue;
    }
    /* Escape opener. */
    if (i + 1 >= src_len)
      return -1;
    uint8_t next = src[i + 1];
    if (next != 'u') {
      uint8_t mapped = ndec_unescape_simple(next);
      if (mapped == 0)
        return -1;
      dst[pos++] = mapped;
      i += 2;
      continue;
    }
    /* \uXXXX. */
    if (i + 5 >= src_len)
      return -1;
    const uint8_t *hex = src + i + 2;
    if (!ndec_unescape_is_hex(hex[0]) || !ndec_unescape_is_hex(hex[1]) || !ndec_unescape_is_hex(hex[2]) ||
        !ndec_unescape_is_hex(hex[3])) {
      return -1;
    }
    uint32_t r = ndec_unescape_hex4(hex);

    if (r >= 0xD800 && r <= 0xDBFF) {
      /* High surrogate: probe for a paired \uYYYY low surrogate. */
      if (i + 11 < src_len && src[i + 6] == '\\' && src[i + 7] == 'u') {
        const uint8_t *low_hex = src + i + 8;
        if (ndec_unescape_is_hex(low_hex[0]) && ndec_unescape_is_hex(low_hex[1]) &&
            ndec_unescape_is_hex(low_hex[2]) && ndec_unescape_is_hex(low_hex[3])) {
          uint32_t low = ndec_unescape_hex4(low_hex);
          if (low >= 0xDC00 && low <= 0xDFFF) {
            uint32_t combined = ((r - 0xD800) * 0x400) + (low - 0xDC00) + 0x10000;
            pos += (uint32_t)ndec_unescape_utf8_encode(combined, dst + pos);
            i += 12;
            continue;
          }
        }
      }
      /* Lone high surrogate: emit U+FFFD replacement. */
      dst[pos]     = 0xEF;
      dst[pos + 1] = 0xBF;
      dst[pos + 2] = 0xBD;
      pos += 3;
      i += 6;
      continue;
    }
    if (r >= 0xDC00 && r <= 0xDFFF) {
      /* Lone low surrogate: emit U+FFFD replacement. */
      dst[pos]     = 0xEF;
      dst[pos + 1] = 0xBF;
      dst[pos + 2] = 0xBD;
      pos += 3;
      i += 6;
      continue;
    }
    pos += (uint32_t)ndec_unescape_utf8_encode(r, dst + pos);
    i += 6;
  }
  return (int32_t)pos;
}

#undef NDEC_UNESCAPE_I_INLINE

#endif /* NDEC_UNESCAPE_H */
