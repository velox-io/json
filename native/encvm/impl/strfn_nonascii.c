/*
 * strfn_nonascii.c — Non-ASCII run processing for JSON string escaping
 *
 * Contains vj_escape_nonascii_run and its helper functions:
 *   - vj_escape_line_terms: SIMD-accelerated U+2028/U+2029 scan
 *   - vj_validate_utf8_run: rune-by-rune UTF-8 validation with lazy flush
 *
 * These are extracted into a separate translation unit because they are
 * ISA-independent and MODE-independent:
 *   - Their behavior depends only on the runtime `flags` parameter.
 *   - They use at most SSE2 intrinsics (for the line-terminator scan),
 *     which are available on all target ISAs (sse42, avx2, avx512).
 *   - They do not reference any MODE_* or ISA_* preprocessor macros.
 *
 * When these lived as `static inline` in strfn.h, the compiler produced
 * 7+ identical copies (one per ISA×MODE encvm.c compilation) because the
 * function bodies were too large to inline.  Moving them here eliminates
 * the duplication: all TUs share a single copy via external linkage.
 */

#include "memfn.h"
#include "types.h"
#include "util.h"

// clang-format off

static const char HEX_DIGITS_NA[] = "0123456789abcdef";

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int write_unicode_escape_na(uint8_t *buf, uint32_t cp) {
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = HEX_DIGITS_NA[(cp >> 12) & 0xF];
  buf[3] = HEX_DIGITS_NA[(cp >> 8) & 0xF];
  buf[4] = HEX_DIGITS_NA[(cp >> 4) & 0xF];
  buf[5] = HEX_DIGITS_NA[cp & 0xF];
  return 6;
}

/* ---- Line terminator scan (no UTF-8 validation) ----
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
      /* No 0xE2 in this 16-byte window — bulk copy. */
      _mm_storeu_si128((__m128i *)out, v);
      out += 16;
      i += 16;
      continue;
    }
    /* Copy safe prefix up to the first 0xE2 byte. */
    int safe = __builtin_ctz(mask);
    if (safe > 0) {
      copy_small(out, &src[i], safe);
      out += safe;
      i += safe;
    }
    /* Check the two continuation bytes for line terminator:
     * U+2028 = E2 80 A8,  U+2029 = E2 80 A9. */
    if (i + 2 < end && src[i + 1] == 0x80 &&
        (src[i + 2] == 0xA8 || src[i + 2] == 0xA9)) {
      uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
      out += write_unicode_escape_na(out, cp);
      i += 3;
    } else {
      /* 0xE2 but not a line terminator — copy it through. */
      *out++ = 0xE2;
      i += 1;
    }
  }
#endif /* __SSE2__ || __aarch64__ */

  /* Scalar tail: fewer than 16 bytes remaining. */
  int64_t flush_start = i;
  while (i + 2 < end) {
    if (src[i] != 0xE2) { i++; continue; }
    if (src[i + 1] != 0x80)                        { i++; continue; }
    if (src[i + 2] != 0xA8 && src[i + 2] != 0xA9)  { i++; continue; }

    /* Found U+2028 or U+2029 — flush preceding bytes. */
    if (i > flush_start) {
      int64_t n = i - flush_start;
      __builtin_memcpy(out, &src[flush_start], n);
      out += n;
    }
    uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
    out += write_unicode_escape_na(out, cp);
    i += 3;
    flush_start = i;
  }

  /* Flush remaining bytes. */
  if (end > flush_start) {
    int64_t n = end - flush_start;
    __builtin_memcpy(out, &src[flush_start], n);
    out += n;
  }
  *out_ptr = out;
}

/* ---- UTF-8 validation with lazy-flush ----
 *
 * Validates UTF-8 sequences rune-by-rune within src[start..end).
 * Invalid bytes and surrogate codepoints are replaced with \ufffd.
 * Valid bytes are bulk-copied via lazy flush.
 *
 * Line terminator escaping (check_line_terms) is piggybacked here rather
 * than run as a separate pass: since we're already decoding rune-by-rune,
 * intercepting U+2028/2029 costs just one extra byte comparison per rune,
 * whereas a separate pass would require either a second scan over the
 * output or an intermediate buffer. */
static inline void vj_validate_utf8_run(uint8_t **out_ptr, const uint8_t *src, int64_t start, int64_t end, const int check_line_terms) {
  uint8_t *out = *out_ptr;
  int64_t i = start;
  int64_t flush_start = i;

  while (i < end) {
    /* --- Line terminator fast check (byte-level) ---
     * U+2028 = E2 80 A8,  U+2029 = E2 80 A9.
     * Only need full decode if first byte is 0xE2. */
    if (check_line_terms && src[i] == 0xE2 &&
        i + 2 < end && src[i + 1] == 0x80 &&
        (src[i + 2] == 0xA8 || src[i + 2] == 0xA9)) {
      /* Flush preceding valid bytes */
      if (i > flush_start) {
        int64_t n = i - flush_start;
        __builtin_memcpy(out, &src[flush_start], n);
        out += n;
      }
      uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
      out += write_unicode_escape_na(out, cp);
      i += 3;
      flush_start = i;
      continue;
    }

    /* --- UTF-8 validation with length-from-leading-byte --- */
    uint8_t b0 = src[i];

    if ((b0 & 0xE0) == 0xC0) {
      /* 2-byte: 110xxxxx 10xxxxxx */
      if (i + 2 <= end && (src[i + 1] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x1F) << 6) | (src[i + 1] & 0x3F);
        if (cp >= 0x80) {
          i += 2;
          continue; /* valid — stay in run, no copy yet */
        }
      }
      /* Invalid: overlong or truncated */
      goto invalid_byte;
    } else if ((b0 & 0xF0) == 0xE0) {
      /* 3-byte: 1110xxxx 10xxxxxx 10xxxxxx */
      if (i + 3 <= end &&
          (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) |
                      ((uint32_t)(src[i + 1] & 0x3F) << 6) |
                      (src[i + 2] & 0x3F);
        if (cp >= 0x800) {
          if (cp >= 0xD800 && cp <= 0xDFFF) {
            /* Surrogate codepoint — replace byte-by-byte (matching stdlib).
             * Each of the 3 bytes becomes an individual \ufffd. */
            goto invalid_byte;
          }
          /* Note: line terminators (0xE2 prefix) already handled above */
          i += 3;
          continue; /* valid */
        }
      }
      /* Invalid: overlong or truncated */
      goto invalid_byte;
    } else if ((b0 & 0xF8) == 0xF0) {
      /* 4-byte: 11110xxx 10xxxxxx 10xxxxxx 10xxxxxx */
      if (i + 4 <= end &&
          (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80 &&
          (src[i + 3] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x07) << 18) |
                      ((uint32_t)(src[i + 1] & 0x3F) << 12) |
                      ((uint32_t)(src[i + 2] & 0x3F) << 6) |
                      (src[i + 3] & 0x3F);
        if (cp >= 0x10000 && cp <= 0x10FFFF) {
          i += 4;
          continue; /* valid */
        }
      }
      /* Invalid: overlong, out-of-range, or truncated */
      goto invalid_byte;
    } else {
      /* Continuation byte (10xxxxxx) or invalid leading byte (11111xxx) */
      goto invalid_byte;
    }

    /* --- Invalid byte handler --- */
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

  /* Flush remaining valid bytes from this run */
  if (i > flush_start) {
    int64_t n = i - flush_start;
    __builtin_memcpy(out, &src[flush_start], n);
    out += n;
  }

  *out_ptr = out;
}

/* ---- Non-ASCII run dispatcher ---- */

int64_t vj_escape_nonascii_run(uint8_t **out_ptr, const uint8_t *src, int64_t i, int64_t src_len, uint32_t flags) {
  const int check_utf8 = (flags & VJ_FLAGS_ESCAPE_INVALID_UTF8) != 0;
  const int check_line_terms = (flags & VJ_FLAGS_ESCAPE_LINE_TERMS) != 0;

  /* Find end of non-ASCII run. */
  int64_t run_end = i;
  while (run_end < src_len && src[run_end] >= 0x80)
    run_end++;

  if (!check_utf8) {
    if (check_line_terms) {
      vj_escape_line_terms(out_ptr, src, i, run_end);
    } else {
      uint8_t *out = *out_ptr;
      int64_t run_len = run_end - i;
      __builtin_memcpy(out, &src[i], run_len);
      *out_ptr = out + run_len;
    }
  } else {
    vj_validate_utf8_run(out_ptr, src, i, run_end, check_line_terms);
  }

  return run_end - i;
}

/* ================================================================
 *  vj_prescan_string_escaped_len — SIMD pre-scan for buffer sizing
 *
 *  Counts bytes that will be expanded by JSON string escaping, then
 *  returns a tight upper bound on the escaped string length (including
 *  the two surrounding quotes).
 *
 *  This is much cheaper than the actual escape (read-only, no writes,
 *  no branches per byte) and produces a bound that is typically within
 *  a few percent of the true length.  The VM uses it for strings longer
 *  than a threshold to avoid pessimistic s->len * 6 estimates that
 *  cause frequent BufFull exits.
 *
 *  Accuracy:
 *    - ASCII bytes needing escape expand to at most 6 bytes (+5).
 *      We count each as +5, which is exact for \u00XX / \uXXXX forms
 *      and a slight overcount for 2-byte short escapes (\n, \t, etc.).
 *    - When VJ_FLAGS_ESCAPE_INVALID_UTF8 is set, non-ASCII bytes (>= 0x80)
 *      are also counted as needing escape (+5 each).  This is pessimistic
 *      for valid multi-byte UTF-8 (where continuation bytes don't expand),
 *      but necessary for correctness: each invalid byte expands to \ufffd
 *      (6 bytes), and underestimating causes buffer overflow.
 *    - Without VJ_FLAGS_ESCAPE_INVALID_UTF8, non-ASCII bytes are NOT
 *      counted — they pass through as-is.
 *
 *  Cost: read-only SIMD scan — AVX2 processes 32 bytes/iter,
 *  SSE2/NEON 16 bytes/iter, plus popcount per chunk.
 * ================================================================ */
int64_t vj_prescan_string_escaped_len(const uint8_t *src, int64_t src_len, uint32_t flags) {
  int64_t esc_count = 0;
  int64_t i = 0;
  const int html = (flags & VJ_FLAGS_ESCAPE_HTML) != 0;
  const int check_utf8 = (flags & VJ_FLAGS_ESCAPE_INVALID_UTF8) != 0;

#if defined(__AVX2__) || defined(__SSE2__) || defined(__aarch64__)

#if defined(__AVX2__)
  /* ---- AVX2: 32 bytes per iteration ---- */
  for (; i + 32 <= src_len; i += 32) {
    __m256i v = _mm256_loadu_si256((const __m256i *)&src[i]);

    __m256i ctrl_safe =
        _mm256_cmpeq_epi8(_mm256_max_epu8(v, _mm256_set1_epi8(0x20)), v);

    __m256i eq_q  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('"'));
    __m256i eq_bs = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\'));
    __m256i bad   = _mm256_or_si256(eq_q, eq_bs);

    if (html) {
      __m256i eq_lt  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('<'));
      __m256i eq_gt  = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('>'));
      __m256i eq_amp = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('&'));
      bad = _mm256_or_si256(bad,
              _mm256_or_si256(eq_lt, _mm256_or_si256(eq_gt, eq_amp)));
    }

    /* When UTF-8 validation is enabled, non-ASCII bytes (>= 0x80) may
     * each expand to \ufffd (6 bytes).  Count them as needing escape. */
    if (check_utf8) {
      int hi_mask = _mm256_movemask_epi8(v);  /* sign bit = byte >= 0x80 */
      esc_count += __builtin_popcount(hi_mask);
    }

    __m256i safe = _mm256_andnot_si256(bad, ctrl_safe);
    int mask = ~_mm256_movemask_epi8(safe);

    esc_count += __builtin_popcount(mask);
  }
#endif /* __AVX2__ */

  /* ---- SSE2/NEON: 16 bytes per iteration ---- */
  for (; i + 16 <= src_len; i += 16) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe =
        _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q  = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad   = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad = _mm_or_si128(bad,
              _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm_movemask_epi8(v);
      esc_count += __builtin_popcount(hi_mask);
    }

    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
    int mask = ~_mm_movemask_epi8(safe) & 0xFFFF;

    esc_count += __builtin_popcount(mask);
  }

  /* ---- SIMD tail: < 16 bytes remaining ---- */
  if (i < src_len) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe =
        _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q  = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad   = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt  = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad = _mm_or_si128(bad,
              _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
    }

    int remaining = (int)(src_len - i);

    if (check_utf8) {
      int hi_mask = _mm_movemask_epi8(v) & ((1 << remaining) - 1);
      esc_count += __builtin_popcount(hi_mask);
    }

    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
    int mask = ~_mm_movemask_epi8(safe) & 0xFFFF;

    mask &= (1 << remaining) - 1;
    esc_count += __builtin_popcount(mask);
  }

#else
  /* ---- Scalar fallback ---- */
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
