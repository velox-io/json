/*
 * Inline helpers for JSON string escape.
 *
 * vj_escape_line_terms — SIMD scan for U+2028/U+2029
 * vj_validate_utf8_run — rune-by-rune UTF-8 validation
 *
 * Used by the entry points in str_escape.c. */

#ifndef VJ_ENCVM_STR_ESCAPE_H
#define VJ_ENCVM_STR_ESCAPE_H

#include "memfn.h"
#include "types.h"
#include "util.h"

static const char ESCAPE_HEX_DIGITS[] = "0123456789abcdef";

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int vj_write_unicode_escape(uint8_t *buf, uint32_t cp) {
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = ESCAPE_HEX_DIGITS[(cp >> 12) & 0xF];
  buf[3] = ESCAPE_HEX_DIGITS[(cp >> 8) & 0xF];
  buf[4] = ESCAPE_HEX_DIGITS[(cp >> 4) & 0xF];
  buf[5] = ESCAPE_HEX_DIGITS[cp & 0xF];
  return 6;
}

/* Line terminator scan (no UTF-8 validation)
 *
 * Scans a non-ASCII run for U+2028 (E2 80 A8) and U+2029 (E2 80 A9),
 * escaping them as \u2028 / \u2029.  All other bytes (including invalid
 * UTF-8) are copied verbatim — no rune-by-rune decoding needed.
 *
 * Uses SIMD to scan 16 bytes at a time for the 0xE2 leading byte.
 * Since U+2028/29 are extremely rare, the fast path (no 0xE2 in the
 * window) simply bulk-copies via SIMD store. */
static inline void vj_escape_line_terms(uint8_t **out_ptr, const uint8_t *src, int64_t start, int64_t end) {
  uint8_t *out = *out_ptr;
  int64_t i = start;

#if defined(__SSE2__) || defined(__aarch64__)
  const __m128i ve2 = _mm_set1_epi8((char)0xE2);

  while (i + 16 <= end) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
    int mask = _mm_movemask_epi8(_mm_cmpeq_epi8(v, ve2));
    if (mask == 0) {
      _mm_storeu_si128((__m128i *)out, v);
      out += 16;
      i += 16;
      continue;
    }
    int safe = __builtin_ctz(mask);
    if (safe > 0) {
      copy_small(out, &src[i], safe);
      out += safe;
      i += safe;
    }
    /* Check the two continuation bytes for line terminator:
     * U+2028 = E2 80 A8,  U+2029 = E2 80 A9. */
    if (i + 2 < end && src[i + 1] == 0x80 && (src[i + 2] == 0xA8 || src[i + 2] == 0xA9)) {
      uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
      out += vj_write_unicode_escape(out, cp);
      i += 3;
    } else {
      *out++ = 0xE2;
      i += 1;
    }
  }
#endif /* __SSE2__ || __aarch64__ */

  /* Scalar tail: fewer than 16 bytes remaining. */
  int64_t flush_start = i;
  while (i + 2 < end) {
    if (src[i] != 0xE2) {
      i++;
      continue;
    }
    if (src[i + 1] != 0x80) {
      i++;
      continue;
    }
    if (src[i + 2] != 0xA8 && src[i + 2] != 0xA9) {
      i++;
      continue;
    }
    if (i > flush_start) {
      int64_t n = i - flush_start;
      __builtin_memcpy(out, &src[flush_start], n);
      out += n;
    }
    uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
    out += vj_write_unicode_escape(out, cp);
    i += 3;
    flush_start = i;
  }
  if (end > flush_start) {
    int64_t n = end - flush_start;
    __builtin_memcpy(out, &src[flush_start], n);
    out += n;
  }
  *out_ptr = out;
}

/* UTF-8 validation with lazy-flush
 *
 * Validates UTF-8 sequences rune-by-rune within src[start..end).
 * Invalid bytes and surrogate codepoints are replaced with \ufffd.
 * Valid bytes are bulk-copied via lazy flush.
 *
 * Line terminator escaping (check_line_terms) is piggybacked here rather
 * than run as a separate pass: since we're already decoding rune-by-rune,
 * intercepting U+2028/2029 costs just one extra byte comparison per rune. */
static inline void vj_validate_utf8_run(uint8_t **out_ptr, const uint8_t *src, int64_t start, int64_t end,
                                        const int check_line_terms) {
  uint8_t *out = *out_ptr;
  int64_t i = start;
  int64_t flush_start = i;

  while (i < end) {
    /* Line terminator fast check (byte-level)
     * U+2028 = E2 80 A8,  U+2029 = E2 80 A9.
     * Only need full decode if first byte is 0xE2. */
    if (check_line_terms && src[i] == 0xE2 && i + 2 < end && src[i + 1] == 0x80 &&
        (src[i + 2] == 0xA8 || src[i + 2] == 0xA9)) {
      if (i > flush_start) {
        int64_t n = i - flush_start;
        __builtin_memcpy(out, &src[flush_start], n);
        out += n;
      }
      uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
      out += vj_write_unicode_escape(out, cp);
      i += 3;
      flush_start = i;
      continue;
    }

    /* UTF-8 validation with length-from-leading-byte */
    uint8_t b0 = src[i];

    if ((b0 & 0xE0) == 0xC0) {
      if (i + 2 <= end && (src[i + 1] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x1F) << 6) | (src[i + 1] & 0x3F);
        if (cp >= 0x80) {
          i += 2;
          continue;
        }
      }
      goto invalid_byte;
    } else if ((b0 & 0xF0) == 0xE0) {
      if (i + 3 <= end && (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) | ((uint32_t)(src[i + 1] & 0x3F) << 6) | (src[i + 2] & 0x3F);
        if (cp >= 0x800) {
          if (cp >= 0xD800 && cp <= 0xDFFF) {
            goto invalid_byte;
          }
          i += 3;
          continue;
        }
      }
      goto invalid_byte;
    } else if ((b0 & 0xF8) == 0xF0) {
      if (i + 4 <= end && (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80 && (src[i + 3] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x07) << 18) | ((uint32_t)(src[i + 1] & 0x3F) << 12) |
                      ((uint32_t)(src[i + 2] & 0x3F) << 6) | (src[i + 3] & 0x3F);
        if (cp >= 0x10000 && cp <= 0x10FFFF) {
          i += 4;
          continue;
        }
      }
      goto invalid_byte;
    } else {
      goto invalid_byte;
    }

  invalid_byte:
    if (i > flush_start) {
      int64_t n = i - flush_start;
      __builtin_memcpy(out, &src[flush_start], n);
      out += n;
    }
    __builtin_memcpy(out, "\\ufffd", 6);
    out += 6;
    i += 1;
    flush_start = i;
    continue;
  }

  if (i > flush_start) {
    int64_t n = i - flush_start;
    __builtin_memcpy(out, &src[flush_start], n);
    out += n;
  }

  *out_ptr = out;
}

#endif /* VJ_ENCVM_STR_ESCAPE_H */
