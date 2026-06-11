/*
 * JSON string escaping — non-ASCII run entry points.
 *
 * vj_escape_nonascii_run  — process a contiguous run of non-ASCII bytes
 * vj_prescan_string_escaped_len — SIMD pre-scan for escaped length bound
 **/

#include "str_escape.h"

/* Public declarations are in strfn.h. */

/* Non-ASCII run dispatcher */

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
 * ================================================================ */
int64_t vj_prescan_string_escaped_len(const uint8_t *src, int64_t src_len, uint32_t flags) {
  int64_t esc_count = 0;
  int64_t i = 0;
  const int html = (flags & VJ_FLAGS_ESCAPE_HTML) != 0;
  const int check_utf8 = (flags & VJ_FLAGS_ESCAPE_INVALID_UTF8) != 0;

#if defined(__AVX2__) || defined(__SSE2__) || defined(__aarch64__)

#if defined(__AVX2__)
  /* AVX2: 32 bytes per iteration */
  for (; i + 32 <= src_len; i += 32) {
    __m256i v = _mm256_loadu_si256((const __m256i *)&src[i]);

    __m256i ctrl_safe = _mm256_cmpeq_epi8(_mm256_max_epu8(v, _mm256_set1_epi8(0x20)), v);

    __m256i eq_q = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('"'));
    __m256i eq_bs = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\'));
    __m256i bad = _mm256_or_si256(eq_q, eq_bs);

    if (html) {
      __m256i eq_lt = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('<'));
      __m256i eq_gt = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('>'));
      __m256i eq_amp = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('&'));
      bad = _mm256_or_si256(bad, _mm256_or_si256(eq_lt, _mm256_or_si256(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm256_movemask_epi8(v); /* sign bit = byte >= 0x80 */
      esc_count += __builtin_popcount(hi_mask);
    }

    __m256i safe = _mm256_andnot_si256(bad, ctrl_safe);
    int mask = ~_mm256_movemask_epi8(safe);

    esc_count += __builtin_popcount(mask);
  }
#endif /* __AVX2__ */

  /* SSE2/NEON: 16 bytes per iteration */
  for (; i + 16 <= src_len; i += 16) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad = _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
    }

    if (check_utf8) {
      int hi_mask = _mm_movemask_epi8(v);
      esc_count += __builtin_popcount(hi_mask);
    }

    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
    int mask = ~_mm_movemask_epi8(safe) & 0xFFFF;

    esc_count += __builtin_popcount(mask);
  }

  /* SIMD tail: < 16 bytes remaining
   * Page-crossing guard: see strfn.h simd_tail comment. */
  if (i < src_len && __builtin_expect(((uintptr_t)&src[i] & 0xFFF) <= (0x1000 - 16), 1)) {
    __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);

    __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

    __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
    __m128i bad = _mm_or_si128(eq_q, eq_bs);

    if (html) {
      __m128i eq_lt = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
      __m128i eq_gt = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
      bad = _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
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
