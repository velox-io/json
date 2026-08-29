/*
 * Inline helpers for JSON string escape.
 *
 * vj_escape_line_terms: SIMD scan for U+2028/U+2029
 * vj_validate_utf8_run: rune-by-rune UTF-8 validation
 *
 * */

#ifndef VJ_ENCVM_STR_ESCAPE_H
#define VJ_ENCVM_STR_ESCAPE_H

#include "util/memfn.h"
#include "types.h"

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
 * UTF-8) are copied verbatim, so no rune-by-rune decoding is needed.
 *
 * Uses SIMD to scan 16 bytes at a time for the 0xE2 leading byte.
 * Since U+2028/29 are extremely rare, the fast path (no 0xE2 in the
 * window) simply bulk-copies via SIMD store. */
INLINE void vj_escape_line_terms(uint8_t **out_ptr, const uint8_t *src, int64_t start, int64_t end) {
  uint8_t *out = *out_ptr;
  int64_t i    = start;

#if defined(__aarch64__)
  /* Nibble-mask scan for 0xE2.  Each 4-bit nibble of the 64-bit
   * packed mask covers one source byte; ctz/4 gives the offset. */
  while (i + 16 <= end) {
    uint8x16_t v   = vld1q_u8(&src[i]);
    uint8x16_t cmp = vceqq_u8(v, vdupq_n_u8(0xE2));
    uint64_t nm    = vget_lane_u64(vreinterpret_u64_u8(vshrn_n_u16(vreinterpretq_u16_u8(cmp), 4)), 0);
    if (nm == 0) {
      vst1q_u8(out, v);
      out += 16;
      i += 16;
      continue;
    }
    int safe = __builtin_ctzll(nm) >> 2;
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
#elif defined(__SSE2__)
  const __m128i ve2 = _mm_set1_epi8((char)0xE2);

  while (i + 16 <= end) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
    int mask  = _mm_movemask_epi8(_mm_cmpeq_epi8(v, ve2));
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
#endif

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
 * Invalid bytes and surrogate codepoints are replaced with U+FFFD.
 * When raw_replacement is set, U+FFFD is written as raw UTF-8 bytes
 * (ef bf bd, 3 bytes); otherwise as the \ufffd escape (6 bytes).
 * Valid bytes are bulk-copied via lazy flush.
 *
 * Line terminator escaping (check_line_terms) is piggybacked here rather
 * than run as a separate pass: since we're already decoding rune-by-rune,
 * intercepting U+2028/2029 costs just one extra byte comparison per rune. */
INLINE void vj_validate_utf8_run(uint8_t **out_ptr, const uint8_t *src, int64_t start, int64_t end,
                                 const int check_line_terms, const int raw_replacement) {
  uint8_t *out        = *out_ptr;
  int64_t i           = start;
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
      if (i + 4 <= end && (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80 &&
          (src[i + 3] & 0xC0) == 0x80) {
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
    if (raw_replacement) {
      __builtin_memcpy(out, "\xef\xbf\xbd", 3);
      out += 3;
    } else {
      __builtin_memcpy(out, "\\ufffd", 6);
      out += 6;
    }
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

/* Non-ASCII run dispatcher
 *
 * Processes an entire contiguous run of non-ASCII bytes (>= 0x80) starting at
 * src[i]. Dispatches to the appropriate handler based on flags:
 *   - No validation: bulk copy or line-terminator scan only.
 *   - Validation: delegates to vj_validate_utf8_run for rune-by-rune checking.
 *
 * Inlined into every escape_string variant: the 96B callee-saved GP set
 * (x19-x30) it would save is identical to the caller's, so inlining lets the
 * two share their saves. This drops the escape chain by ~112B on arm64.
 *
 * Returns number of source bytes consumed (the entire non-ASCII run).
 * Writes escaped output to *out_ptr and advances it. */
INLINE int64_t vj_escape_nonascii_run(uint8_t **out_ptr, const uint8_t *src, int64_t i, int64_t src_len,
                                      uint32_t flags) {
  const int check_utf8       = (flags & VJ_FLAGS_ESCAPE_INVALID_UTF8) != 0;
  const int check_line_terms = (flags & VJ_FLAGS_ESCAPE_LINE_TERMS) != 0;
  const int raw_replacement  = (flags & VJ_FLAGS_RAW_UTF8_REPLACEMENT) != 0;

  /* Find end of non-ASCII run. */
  int64_t run_end = i;
  while (run_end < src_len && src[run_end] >= 0x80)
    run_end++;

  if (!check_utf8) {
    if (check_line_terms) {
      vj_escape_line_terms(out_ptr, src, i, run_end);
    } else {
      uint8_t *out    = *out_ptr;
      int64_t run_len = run_end - i;
      __builtin_memcpy(out, &src[i], run_len);
      *out_ptr = out + run_len;
    }
  } else {
    vj_validate_utf8_run(out_ptr, src, i, run_end, check_line_terms, raw_replacement);
  }

  return run_end - i;
}

/*
 * vj_prescan_string_escaped_len: SIMD pre-scan for buffer sizing
 *
 * Counts bytes that will be expanded by JSON string escaping, then
 * returns a tight upper bound on the escaped string length (including
 * the two surrounding quotes).
 *
 * This is much cheaper than the actual escape (read-only, no writes,
 * no branches per byte) and produces a bound that is typically within
 * a few percent of the true length.  The VM uses it for strings longer
 * than a threshold to avoid pessimistic s->len * 6 estimates that
 * cause frequent BufFull exits.
 *
 * Accuracy:
 *   - ASCII bytes needing escape expand to at most 6 bytes (+5).
 *     We count each as +5, which is exact for \u00XX / \uXXXX forms
 *     and a slight overcount for 2-byte short escapes (\n, \t, etc.).
 *   - When VJ_FLAGS_ESCAPE_INVALID_UTF8 is set, non-ASCII bytes (>= 0x80)
 *     are also counted as needing escape (+5 each).  This is pessimistic
 *     for valid multi-byte UTF-8 (where continuation bytes don't expand),
 *     but necessary for correctness: each invalid byte expands to either
 *     \ufffd (6 bytes) or raw U+FFFD (3 bytes) depending on
 *     VJ_FLAGS_RAW_UTF8_REPLACEMENT; +5 covers both, and
 *     underestimating causes buffer overflow.
 *   - Without VJ_FLAGS_ESCAPE_INVALID_UTF8, non-ASCII bytes are NOT
 *     counted; they pass through as-is.
 * NOINLINE: reached only when the pessimistic 6x estimate overflows the
 * buffer, so the scan loop stays out of the VM function.
 */
NOINLINE static int64_t vj_prescan_string_escaped_len(const uint8_t *src, int64_t src_len, uint32_t flags) {
  int64_t esc_count    = 0;
  int64_t i            = 0;
  const int html       = (flags & VJ_FLAGS_ESCAPE_HTML) != 0;
  const int check_utf8 = (flags & VJ_FLAGS_ESCAPE_INVALID_UTF8) != 0;

#if defined(__AVX2__) || defined(__SSE2__) || defined(__aarch64__)

#if defined(__AVX2__)
  /* AVX2: 32 bytes per iteration */
  for (; i + 32 <= src_len; i += 32) {
    __m256i v = _mm256_loadu_si256((const __m256i *)&src[i]);

    __m256i ctrl_safe = _mm256_cmpeq_epi8(_mm256_max_epu8(v, _mm256_set1_epi8(0x20)), v);

    __m256i eq_q  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('"'));
    __m256i eq_bs = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\'));
    __m256i bad   = _mm256_or_si256(eq_q, eq_bs);

    if (html) {
      __m256i eq_lt  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('<'));
      __m256i eq_gt  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('>'));
      __m256i eq_amp = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('&'));
      bad            = _mm256_or_si256(bad, _mm256_or_si256(eq_lt, _mm256_or_si256(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm256_movemask_epi8(v); /* sign bit = byte >= 0x80 */
      esc_count += __builtin_popcount(hi_mask);
    }

    __m256i safe = _mm256_andnot_si256(bad, ctrl_safe);
    int mask     = ~_mm256_movemask_epi8(safe);

    esc_count += __builtin_popcount(mask);
  }
#endif /* __AVX2__ */

#if defined(__aarch64__)
  /* 16 bytes per iteration.  Byte-level popcount via vshr.7 + vaddvq_u8. */
  for (; i + 16 <= src_len; i += 16) {
    uint8x16_t v = vld1q_u8(&src[i]);

    uint8x16_t ctrl = vcltq_u8(v, vdupq_n_u8(0x20));

    uint8x16_t eq_q  = vceqq_u8(v, vdupq_n_u8('"'));
    uint8x16_t eq_bs = vceqq_u8(v, vdupq_n_u8('\\'));
    uint8x16_t bad   = vorrq_u8(eq_q, eq_bs);

    if (html) {
      uint8x16_t eq_lt  = vceqq_u8(v, vdupq_n_u8('<'));
      uint8x16_t eq_gt  = vceqq_u8(v, vdupq_n_u8('>'));
      uint8x16_t eq_amp = vceqq_u8(v, vdupq_n_u8('&'));
      bad               = vorrq_u8(bad, vorrq_u8(eq_lt, vorrq_u8(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      esc_count += vaddvq_u8(vshrq_n_u8(v, 7));
    }

    uint8x16_t escape = vorrq_u8(ctrl, bad);
    esc_count += vaddvq_u8(vshrq_n_u8(escape, 7));
  }
#else
  /* SSE2: 16 bytes per iteration */
  for (; i + 16 <= src_len; i += 16) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q  = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad   = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad            = _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm_movemask_epi8(v);
      esc_count += __builtin_popcount(hi_mask);
    }

    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
    int mask     = ~_mm_movemask_epi8(safe) & 0xFFFF;

    esc_count += __builtin_popcount(mask);
  }
#endif

  /* SIMD tail: < 16 bytes remaining
   * Page-crossing guard: see strfn.h simd_tail comment. */
  if (i < src_len && __builtin_expect(((uintptr_t)&src[i] & 0xFFF) <= (0x1000 - 16), 1)) {
    int remaining = (int)(src_len - i);

#if defined(__aarch64__)
    uint8x16_t v = vld1q_u8(&src[i]);

    uint8x16_t ctrl = vcltq_u8(v, vdupq_n_u8(0x20));

    uint8x16_t eq_q  = vceqq_u8(v, vdupq_n_u8('"'));
    uint8x16_t eq_bs = vceqq_u8(v, vdupq_n_u8('\\'));
    uint8x16_t bad   = vorrq_u8(eq_q, eq_bs);

    if (html) {
      uint8x16_t eq_lt  = vceqq_u8(v, vdupq_n_u8('<'));
      uint8x16_t eq_gt  = vceqq_u8(v, vdupq_n_u8('>'));
      uint8x16_t eq_amp = vceqq_u8(v, vdupq_n_u8('&'));
      bad               = vorrq_u8(bad, vorrq_u8(eq_lt, vorrq_u8(eq_gt, eq_amp)));
    }

    /* Build a per-byte 1/0 marker, mask out the over-read tail, then
     * sum across the vector.  vshrq_n_u8(x, 7) extracts the high bit
     * (1 for matching bytes, since vceqq/vcltq produce 0xFF for true).
     *
     * Branchless lane mask: lane index < remaining -> 0xFF, else 0x00.
     * Avoids a stack array plus fill loop, which clang lowers to a
     * per-call memset (a source of uniform marshal slowdown on the
     * short-string tail path). */
    static const uint8_t VJ_IOTA16[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
    uint8x16_t lane_mask               = vcltq_u8(vld1q_u8(VJ_IOTA16), vdupq_n_u8((uint8_t)remaining));

    if (check_utf8) {
      uint8x16_t hi = vandq_u8(vshrq_n_u8(v, 7), lane_mask);
      esc_count += vaddvq_u8(hi);
    }

    uint8x16_t escape = vandq_u8(vshrq_n_u8(vorrq_u8(ctrl, bad), 7), lane_mask);
    esc_count += vaddvq_u8(escape);
#else
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q  = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad   = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad            = _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm_movemask_epi8(v) & ((1 << remaining) - 1);
      esc_count += __builtin_popcount(hi_mask);
    }

    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
    int mask     = ~_mm_movemask_epi8(safe) & 0xFFFF;

    mask &= (1 << remaining) - 1;
    esc_count += __builtin_popcount(mask);
#endif
    i = src_len; /* consumed all remaining bytes */
  }

  /* Scalar tail for bytes not handled by the SIMD tail above
   * (page-crossing guard skipped, or no SIMD). */
  for (; i < src_len; i++) {
    uint8_t c = src[i];
    if (c < 0x20 || c == '"' || c == '\\') {
      esc_count++;
    } else if (html && (c == '<' || c == '>' || c == '&')) {
      esc_count++;
    } else if (check_utf8 && c >= 0x80) {
      esc_count++;
    }
  }

#else
  /* Scalar fallback */
  for (; i < src_len; i++) {
    uint8_t c = src[i];
    if (c < 0x20 || c == '"' || c == '\\') {
      esc_count++;
    } else if (html && (c == '<' || c == '>' || c == '&')) {
      esc_count++;
    } else if (check_utf8 && c >= 0x80) {
      esc_count++;
    }
  }
#endif

  /* Each escaped byte expands by at most 5 bytes (1 → 6 for \u00XX).
   * Total: 2 quotes + original length + expansion. */
  return 2 + src_len + esc_count * 5;
}

#endif /* VJ_ENCVM_STR_ESCAPE_H */
