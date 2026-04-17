#ifndef VJ_ENCVM_STRING_H
#define VJ_ENCVM_STRING_H

#include "memfn.h"
#include "types.h"
#include "util.h"

// clang-format off

/* ---- String escape (JSON) ----
 *
 * Writes the string content (WITHOUT surrounding quotes) to buf.
 * Returns number of bytes written.
 *
 * The caller must ensure buf has enough space (worst case 6x + overhead). */

static const char HEX_DIGITS[] = "0123456789abcdef";

/* ---- Escape lookup table ----
 *
 * For bytes that need escaping (c < 0x20, '"', '\\'), this table gives:
 *   ESCAPE_LUT[c] = replacement char for the \X form (e.g. 'n' for \n)
 *   0 means use \u00XX form (control chars without a short escape).
 *
 * Entries for safe bytes (>= 0x20, not " or \) are unused and zero. */
static const uint8_t ESCAPE_LUT[256] = {
    /* 0x00-0x07: \u00XX */ 0, 0, 0, 0, 0, 0, 0, 0,
    /* 0x08 \b */ 'b',
    /* 0x09 \t */ 't',
    /* 0x0A \n */ 'n',
    /* 0x0B    */ 0,
    /* 0x0C \f */ 'f',
    /* 0x0D \r */ 'r',
    /* 0x0E-0x0F */ 0, 0,
    /* 0x10-0x1F: \u00XX */ 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    /* 0x20 ' ' */ 0,
    /* 0x21 '!' */ 0,
    /* 0x22 '"' */ '"',
    /* 0x23-0x5B: safe */ [0x23 ... 0x5B] = 0,
    /* 0x5C '\\' */ [0x5C] = '\\',
    /* 0x5D-0xFF: safe/non-ASCII */ [0x5D ... 0xFF] = 0,
};

// clang-format on

/* Escape a byte that needs escaping.  Uses the lookup table for the
 * common cases (\", \\, \n, \t, etc.) and falls through to \u00XX
 * for the remaining control characters (0x00-0x1F without a short form).
 * Returns number of bytes written (2 or 6). */
static inline int escape_byte(uint8_t *buf, uint8_t c) {
  uint8_t repl = ESCAPE_LUT[c];
  if (__builtin_expect(repl != 0, 1)) {
    buf[0] = '\\';
    buf[1] = repl;
    return 2;
  }
  /* Control character without short form: \u00XX */
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = '0';
  buf[3] = '0';
  buf[4] = HEX_DIGITS[c >> 4];
  buf[5] = HEX_DIGITS[c & 0x0F];
  return 6;
}

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int write_unicode_escape(uint8_t *buf, uint32_t cp) {
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = HEX_DIGITS[(cp >> 12) & 0xF];
  buf[3] = HEX_DIGITS[(cp >> 8) & 0xF];
  buf[4] = HEX_DIGITS[(cp >> 4) & 0xF];
  buf[5] = HEX_DIGITS[cp & 0xF];
  return 6;
}

/*
 * SWAR (SIMD Within A Register) helper: scan 8 bytes packed in a uint64_t,
 * return an 8-bit mask where bit N (N = 0..7, LSB = first byte in memory on
 * little-endian) is set if byte N needs escaping.
 *
 * Detects: c < 0x20, c == '"'(0x22), c == '\\'(0x5C), c >= 0x80.
 * When html != 0, also detects: c == '<'(0x3C), c == '>'(0x3E), c == '&'(0x26).
 *
 * The `html` parameter must be a compile-time constant so the compiler
 * eliminates dead branches.
 *
 * Does not depend on SIMD intrinsics — usable on all platforms.
 */

#define SWAR_BROADCAST(b) ((uint64_t)(b) * 0x0101010101010101ULL)
#define SWAR_HI_BITS      SWAR_BROADCAST(0x80)
#define SWAR_LO_BITS      SWAR_BROADCAST(0x01)

/* has_zero_byte: for each byte lane that is 0x00, sets that lane's high bit.
 * Classic: ((v - 0x0101...) & ~v & 0x8080...) */
#define SWAR_HAS_ZERO(v) (((v) - SWAR_LO_BITS) & ~(v) & SWAR_HI_BITS)

/* has_less_than: for each byte lane < n (where 1 <= n <= 128),
 * sets that lane's high bit.  Works by subtracting n and checking
 * for underflow in the high bit while the original had it clear. */
#define SWAR_HAS_LESS(v, n) (((v) - SWAR_BROADCAST(n)) & ~(v) & SWAR_HI_BITS)

static inline int vj_escape_mask_8(uint64_t word, const int html) {
  /* Bytes that need escaping will have their high bit set in `bad`. */
  uint64_t bad = 0;

  /* c < 0x20: control characters */
  bad |= SWAR_HAS_LESS(word, 0x20);

  /* c >= 0x80: non-ASCII (high bit already set in those bytes) */
  bad |= word & SWAR_HI_BITS;

  /* c == '"' (0x22) */
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x22));

  /* c == '\\' (0x5C) */
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x5C));

  if (html) {
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x3C)); /* '<' */
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x3E)); /* '>' */
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x26)); /* '&' */
  }

  /* Extract one bit per byte (the high bit of each lane) into
   * an 8-bit integer.  Multiply-shift trick:
   *   (bad >> 7) isolates the marker bits at positions 0,8,16,...,56.
   *   Multiplying by 0x0102040810204080 accumulates them into the
   *   top byte, then >> 56 brings them to bits 0..7.
   *   The constant has bit k*7 set for k=1..8 so that the marker at
   *   position 8*i is shifted to bit 56+i. */
  return (int)(((bad >> 7) * 0x0102040810204080ULL) >> 56);
}

/* Fast variant: only detects c < 0x20, '"', '\\'.
 * Non-ASCII bytes (>= 0x80) are treated as safe — NOT flagged.
 * No HTML detection. */
static inline int vj_escape_mask_8_fast(uint64_t word) {
  uint64_t bad = 0;
  bad |= SWAR_HAS_LESS(word, 0x20);
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x22));
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x5C));
  return (int)(((bad >> 7) * 0x0102040810204080ULL) >> 56);
}

#undef SWAR_HAS_LESS
#undef SWAR_HAS_ZERO
#undef SWAR_HI_BITS
#undef SWAR_LO_BITS
#undef SWAR_BROADCAST

#if defined(__SSE2__) || defined(__aarch64__)

/* 16-byte escape mask: scan 16 bytes, return bitmask of bytes needing escape.
 * `html` must be a compile-time constant — the compiler eliminates the dead
 * branch entirely via constant folding + static inline. */
static inline int vj_escape_mask_16_impl_v(__m128i v, const int html) {
  /* c < 0x20: max_epu8(v, 0x20) != v → cmpeq gives 0 for ctrl chars.
   * (Must use 0x20, not 0x1F, so that byte 0x1F is correctly flagged.) */
  __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);

  /* c == '"' or c == '\\' */
  __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
  __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));

  /* c >= 0x80: signed < 0 */
  __m128i hi = _mm_cmplt_epi8(v, _mm_setzero_si128());

  __m128i bad = _mm_or_si128(_mm_or_si128(eq_q, eq_bs), hi);

  if (html) {
    __m128i eq_lt = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));
    __m128i eq_gt = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));
    __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));
    bad = _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp)));
  }

  /* safe = ctrl_safe & ~bad;  need_escape = ~safe */
  __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
  return ~_mm_movemask_epi8(safe) & 0xFFFF;
}

/* Same as above but loads from an address (convenience wrapper). */
static inline int vj_escape_mask_16_impl(const uint8_t *src, const int html) {
  return vj_escape_mask_16_impl_v(_mm_loadu_si128((const __m128i *)src), html);
}

static inline int vj_escape_mask_16(const uint8_t *src) {
  return vj_escape_mask_16_impl(src, 0);
}
static inline int vj_escape_mask_16_html(const uint8_t *src) {
  return vj_escape_mask_16_impl(src, 1);
}

/* Fast 16-byte mask: only c < 0x20, '"', '\\'.  No non-ASCII, no HTML. */
static inline int vj_escape_mask_16_fast_v(__m128i v) {
  __m128i ctrl_safe = _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x20)), v);
  __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));
  __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));
  __m128i bad = _mm_or_si128(eq_q, eq_bs);
  __m128i safe = _mm_andnot_si128(bad, ctrl_safe);
  return ~_mm_movemask_epi8(safe) & 0xFFFF;
}

static inline int vj_escape_mask_16_fast(const uint8_t *src) {
  return vj_escape_mask_16_fast_v(_mm_loadu_si128((const __m128i *)src));
}

/* ---- AVX2 32-byte escape mask ---- */
#if defined(__AVX2__)

/* 32-byte escape mask (AVX2): same logic as 16-byte but with 256-bit vectors.
 */
static inline int vj_escape_mask_32_impl(const uint8_t *src, const int html) {
  __m256i v = _mm256_loadu_si256((const __m256i *)src);

  __m256i ctrl_safe = _mm256_cmpeq_epi8(_mm256_max_epu8(v, _mm256_set1_epi8(0x20)), v);

  __m256i eq_q = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('"'));
  __m256i eq_bs = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\'));
  __m256i hi = _mm256_cmpgt_epi8(_mm256_setzero_si256(), v);

  __m256i bad = _mm256_or_si256(_mm256_or_si256(eq_q, eq_bs), hi);

  if (html) {
    __m256i eq_lt = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('<'));
    __m256i eq_gt = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('>'));
    __m256i eq_amp = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('&'));
    bad = _mm256_or_si256(bad, _mm256_or_si256(eq_lt, _mm256_or_si256(eq_gt, eq_amp)));
  }

  __m256i safe = _mm256_andnot_si256(bad, ctrl_safe);
  return ~_mm256_movemask_epi8(safe);
}

static inline int vj_escape_mask_32(const uint8_t *src) {
  return vj_escape_mask_32_impl(src, 0);
}
static inline int vj_escape_mask_32_html(const uint8_t *src) {
  return vj_escape_mask_32_impl(src, 1);
}

/* Fast 32-byte mask: only c < 0x20, '"', '\\'.  No non-ASCII, no HTML. */
static inline int vj_escape_mask_32_fast(const uint8_t *src) {
  __m256i v = _mm256_loadu_si256((const __m256i *)src);
  __m256i ctrl_safe = _mm256_cmpeq_epi8(_mm256_max_epu8(v, _mm256_set1_epi8(0x20)), v);
  __m256i eq_q = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('"'));
  __m256i eq_bs = _mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\'));
  __m256i bad = _mm256_or_si256(eq_q, eq_bs);
  __m256i safe = _mm256_andnot_si256(bad, ctrl_safe);
  return ~_mm256_movemask_epi8(safe);
}

#endif /* __AVX2__ */

#endif /* __SSE2__ || __aarch64__ */

/* ---- Non-ASCII run dispatcher (implemented in strfn_nonascii.c) ----
 *
 * Processes an entire contiguous run of non-ASCII bytes (>= 0x80) starting at
 * src[i]. Dispatches to the appropriate handler based on flags:
 *   - No validation: bulk copy or line-terminator scan only.
 *   - Validation: delegates to vj_validate_utf8_run for rune-by-rune checking.
 *
 * This function is ISA-independent and MODE-independent: its behavior depends
 * only on the runtime `flags` parameter.  It uses at most SSE2 intrinsics
 * (for the line-terminator scan), which are available on all target ISAs.
 * Placing it in a separate translation unit avoids identical copies.
 *
 * Returns number of source bytes consumed (the entire non-ASCII run).
 * Writes escaped output to *out_ptr and advances it. */
int64_t vj_escape_nonascii_run(uint8_t **out_ptr, const uint8_t *src, int64_t i, int64_t src_len, uint32_t flags);

/* Pre-scan a string to compute a tight upper bound on the escaped length
 * (including quotes).  Much cheaper than the actual escape — read-only SIMD
 * scan with popcount.  Used by the VM for long strings to avoid the
 * pessimistic s->len * 6 estimate that causes frequent BufFull exits. */
int64_t vj_prescan_string_escaped_len(const uint8_t *src, int64_t src_len, uint32_t flags);

/* ---- Inline ASCII escape macro ----
 *
 * Handles a single byte at src[i] that was flagged by SIMD/SWAR.
 * For ASCII (< 0x80): inlines the escape directly (no function call).
 * For non-ASCII (>= 0x80): delegates to vj_escape_nonascii_run which
 * batch-processes the entire contiguous non-ASCII segment.
 *
 * Uses the ESCAPE_LUT lookup table for branchless escape selection.
 * The `html` parameter must be a compile-time constant. */
#define ESCAPE_ONE_INLINE(html)                                                                                        \
  do {                                                                                                                 \
    uint8_t _c = src[i];                                                                                               \
    if (__builtin_expect(_c < 0x80, 1)) {                                                                              \
      if (_c < 0x20 || _c == '"' || _c == '\\') {                                                                      \
        out += escape_byte(out, _c);                                                                                   \
      } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {                                                    \
        out += write_unicode_escape(out, _c);                                                                          \
      } else {                                                                                                         \
        *out++ = _c;                                                                                                   \
      }                                                                                                                \
      i++;                                                                                                             \
    } else {                                                                                                           \
      i += vj_escape_nonascii_run(&out, src, i, src_len, flags);                                                       \
    }                                                                                                                  \
  } while (0)

/*
 * Escape core.  The `html` parameter must be a compile-time constant (0 or 1);
 * after static inline expansion the dead branch and the unused mask function
 * are eliminated entirely by the optimiser.
 *
 * On platforms with SIMD the function uses wide scans; on all others it
 * degrades gracefully to SWAR (8 bytes) + byte-by-byte.
 *
 * Key optimizations:
 *   1. Inline ASCII escape — no function call for the common case.
 *   2. Lookup table for escape_byte — branchless, no switch.
 *   3. Multi-escape per SIMD/SWAR window — when a window contains multiple
 *      escape bytes, iterate the bitmask (ctz + shift) to process ALL of
 *      them before re-scanning.  Avoids redundant SIMD load + mask ops.
 *      For non-ASCII (>= 0x80) bytes the mask loop breaks out early since
 *      the non-ASCII run may extend beyond the current window.
 *   4. copy_small for sub-16-byte chunks — avoids memcpy call overhead.
 */
static inline int escape_string_content_impl(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags,
                                             const int html) {
  uint8_t *out = buf;
  int64_t i = 0;

#if defined(__SSE2__) || defined(__aarch64__)
  /* Short-string optimization: for strings <= 16 bytes, jump directly
   * to the SIMD tail path which handles the partial-vector case. */
  if (src_len <= 16)
    goto simd_tail;
#endif

  while (i < src_len) {
#if defined(__AVX2__)
    /* ---- AVX2: scan 32 bytes at a time ---- */
    if (i + 32 <= src_len) {
      int mask = html ? vj_escape_mask_32_html(&src[i]) : vj_escape_mask_32(&src[i]);

      if (mask == 0) {
        /* All 32 bytes are safe — bulk copy via AVX2 store. */
        _mm256_storeu_si256((__m256i *)out, _mm256_loadu_si256((const __m256i *)&src[i]));
        out += 32;
        i += 32;
        continue;
      }
      /* Process ALL escape bytes in this 32-byte window.
       * For non-ASCII (>= 0x80), delegate to the run handler and
       * break out — the run may extend beyond this window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          if (safe < 16) {
            copy_small(out, &src[i], safe);
          } else {
            _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
            copy_small(out + 16, &src[i + 16], safe - 16);
          }
          out += safe;
          i += safe;
        }
        uint8_t _c = src[i];
        if (__builtin_expect(_c >= 0x80, 0)) {
          i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
          break;
        }
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          /* SWAR mask false positive (borrow propagation) — copy byte as-is. */
          *out++ = _c;
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }
#endif /* __AVX2__ */

#if defined(__SSE2__) || defined(__aarch64__)
    /* ---- SIMD: scan 16 bytes at a time ---- */
    if (i + 16 <= src_len) {
      int mask = html ? vj_escape_mask_16_html(&src[i]) : vj_escape_mask_16(&src[i]);

      if (mask == 0) {
        /* All 16 bytes are safe — bulk copy via SIMD store. */
        _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }
      /* Process ALL escape bytes in this 16-byte window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        uint8_t _c = src[i];
        if (__builtin_expect(_c >= 0x80, 0)) {
          i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
          break;
        }
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          /* SWAR mask false positive (borrow propagation) — copy byte as-is. */
          *out++ = _c;
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* ---- SIMD tail: < 16 bytes remaining (or short string entry) ----
     *
     * Load 16 bytes from &src[i], masking the result to [i, src_len).
     * The load may over-read past src_len, which is safe as long as it
     * stays within the same page (virtual memory is page-granular).
     *
     * Guard: if the 16-byte load would cross a page boundary, fall
     * through to the scalar path.  This is extremely rare (~0.4% of
     * calls) but prevents faults on unmapped pages — observed on
     * Windows with the Go race detector where string data can land
     * at a page boundary. */
  simd_tail:
    if (__builtin_expect(((uintptr_t)&src[i] & 0xFFF) > (0x1000 - 16), 0))
      goto simd_tail_scalar;
    {
      int remaining = (int)(src_len - i);
      __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
      int mask = html ? vj_escape_mask_16_impl_v(v, 1) : vj_escape_mask_16_impl_v(v, 0);
      int relevant_mask = mask & ((1 << remaining) - 1);
      if (__builtin_expect(relevant_mask == 0, 1)) {
        _mm_storeu_si128((__m128i *)out, v);
        out += remaining;
        return (int)(out - buf);
      }
      /* Has escapes — fall through to SWAR / byte-by-byte below */
    }
  simd_tail_scalar:;
#endif /* __SSE2__ || __aarch64__ */

    /* ---- SWAR: scan 8 bytes at a time (scalar tail) ---- */
    if (i + 8 <= src_len) {
      uint64_t word;
      __builtin_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, html);
      if (mask == 0) {
        __builtin_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      /* Process ALL escape bytes in this 8-byte window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        uint8_t _c = src[i];
        if (__builtin_expect(_c >= 0x80, 0)) {
          i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
          break;
        }
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          /* SWAR mask false positive (borrow propagation) — copy byte as-is. */
          *out++ = _c;
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* ---- Byte-by-byte tail: fewer than 8 bytes remaining ---- */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' && !(html && (c == '<' || c == '>' || c == '&'))) {
      *out++ = c;
      i++;
    } else {
      ESCAPE_ONE_INLINE(html);
    }
  }
  return (int)(out - buf);
}

#undef ESCAPE_ONE_INLINE

/* ================================================================
 *  Fast escape: ASCII-only — no non-ASCII detection, no HTML,
 *  no UTF-8 validation, no line terminator escaping.
 *
 *  Only handles: c < 0x20 (control chars), '"', '\\'.
 *  Non-ASCII bytes (>= 0x80) pass through untouched.
 * ================================================================ */

#define ESCAPE_ONE_INLINE_FAST                                                                                         \
  do {                                                                                                                 \
    uint8_t _c = src[i];                                                                                               \
    if (_c < 0x20 || _c == '"' || _c == '\\') {                                                                        \
      out += escape_byte(out, _c);                                                                                     \
    } else {                                                                                                           \
      *out++ = _c;                                                                                                     \
    }                                                                                                                  \
    i++;                                                                                                               \
  } while (0)

static inline int escape_string_content_fast(uint8_t *buf, const uint8_t *src, int64_t src_len) {
  uint8_t *out = buf;
  int64_t i = 0;

#if defined(__SSE2__) || defined(__aarch64__)
  /* Short-string optimization: for strings <= 16 bytes, jump directly
   * to the SIMD tail path which handles the partial-vector case. */
  if (src_len <= 16)
    goto simd_tail_fast;
#endif

  while (i < src_len) {
#if defined(__AVX2__)
    if (i + 32 <= src_len) {
      int mask = vj_escape_mask_32_fast(&src[i]);
      if (mask == 0) {
        _mm256_storeu_si256((__m256i *)out, _mm256_loadu_si256((const __m256i *)&src[i]));
        out += 32;
        i += 32;
        continue;
      }
      /* Process ALL escape bytes in this 32-byte window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          if (safe < 16) {
            copy_small(out, &src[i], safe);
          } else {
            _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
            copy_small(out + 16, &src[i + 16], safe - 16);
          }
          out += safe;
          i += safe;
        }
        {
          uint8_t _c = src[i];
          if (_c < 0x20 || _c == '"' || _c == '\\') {
            out += escape_byte(out, _c);
          } else {
            /* SWAR mask false positive (borrow propagation) — copy byte as-is.
             */
            *out++ = _c;
          }
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }
#endif /* __AVX2__ */

#if defined(__SSE2__) || defined(__aarch64__)
    if (i + 16 <= src_len) {
      int mask = vj_escape_mask_16_fast(&src[i]);
      if (mask == 0) {
        _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }
      /* Process ALL escape bytes in this 16-byte window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        {
          uint8_t _c = src[i];
          if (_c < 0x20 || _c == '"' || _c == '\\') {
            out += escape_byte(out, _c);
          } else {
            /* SWAR mask false positive (borrow propagation) — copy byte as-is.
             */
            *out++ = _c;
          }
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* ---- SIMD tail: page-crossing guard + fast path. */
  simd_tail_fast:
    if (__builtin_expect(((uintptr_t)&src[i] & 0xFFF) > (0x1000 - 16), 0))
      goto simd_tail_fast_scalar;
    {
      int remaining = (int)(src_len - i);
      __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
      int mask = vj_escape_mask_16_fast_v(v);
      int relevant_mask = mask & ((1 << remaining) - 1);
      if (__builtin_expect(relevant_mask == 0, 1)) {
        _mm_storeu_si128((__m128i *)out, v);
        out += remaining;
        return (int)(out - buf);
      }
      /* Has escapes — fall through to SWAR / byte-by-byte below */
    }
  simd_tail_fast_scalar:;
#endif /* __SSE2__ || __aarch64__ */

    if (i + 8 <= src_len) {
      uint64_t word;
      __builtin_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8_fast(word);
      if (mask == 0) {
        __builtin_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      /* Process ALL escape bytes in this 8-byte window. */
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        {
          uint8_t _c = src[i];
          if (_c < 0x20 || _c == '"' || _c == '\\') {
            out += escape_byte(out, _c);
          } else {
            /* SWAR mask false positive (borrow propagation) — copy byte as-is.
             */
            *out++ = _c;
          }
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* Byte-by-byte tail */
    uint8_t c = src[i];
    if (c >= 0x20 && c != '"' && c != '\\') {
      *out++ = c;
      i++;
    } else {
      ESCAPE_ONE_INLINE_FAST;
    }
  }
  return (int)(out - buf);
}

#undef ESCAPE_ONE_INLINE_FAST

/* Dispatch to the appropriate specialization. */
static inline int escape_string_content(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags) {
  if (flags & VJ_FLAGS_ESCAPE_HTML)
    return escape_string_content_impl(buf, src, src_len, flags, /*html=*/1);
  return escape_string_content_impl(buf, src, src_len, flags, /*html=*/0);
}

/* ================================================================
 *  vj_escape_string — write a complete JSON string (with quotes)
 *
 *  Returns number of bytes written (including the two quote bytes).
 *  Caller must ensure buf has room for 2 + src_len * 6 bytes.
 * ================================================================ */
static inline int vj_escape_string(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags) {
  uint8_t *out = buf;
  *out++ = '"';
  if (src_len > 0) {
    out += escape_string_content(out, src, src_len, flags);
  }
  *out++ = '"';
  return (int)(out - buf);
}

/* ================================================================
 *  vj_escape_string_fast — fast-path JSON string (with quotes)
 *
 *  Only escapes control chars (< 0x20), '"', and '\\'.
 *  Non-ASCII bytes pass through untouched — no UTF-8 validation,
 *  no HTML escaping, no line terminator escaping.
 *
 *  Returns number of bytes written (including the two quote bytes).
 *  Caller must ensure buf has room for 2 + src_len * 6 bytes.
 * ================================================================ */
static inline int vj_escape_string_fast(uint8_t *buf, const uint8_t *src, int64_t src_len) {
  uint8_t *out = buf;
  *out++ = '"';
  if (src_len > 0) {
    out += escape_string_content_fast(out, src, src_len);
  }
  *out++ = '"';
  return (int)(out - buf);
}

/*
 *  AVX2 length-based dispatch: SSE-only escape functions
 *
 *  When compiled with -mavx2, the main escape functions use 256-bit
 *  AVX2 instructions.  For short strings, the ymm register setup
 *  overhead makes AVX2 slower than pure 128-bit SSE.
 * */

#if defined(__AVX2__)

/* Threshold for switching from SSE to AVX2 escape path.
 * Below this length, SSE-only functions avoid ymm register pressure
 * and vzeroupper overhead.  Tunable via benchmarks. */
#define VJ_AVX2_STRING_THRESHOLD 64

/* ---- SSE-only escape (full flags path) ---- */
static inline int escape_string_content_impl_sse(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags,
                                                 const int html) {
  uint8_t *out = buf;
  int64_t i = 0;

  if (src_len <= 16)
    goto simd_tail_sse_impl;

  while (i < src_len) {
    if (i + 16 <= src_len) {
      int mask = html ? vj_escape_mask_16_html(&src[i]) : vj_escape_mask_16(&src[i]);

      if (mask == 0) {
        _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        uint8_t _c = src[i];
        if (__builtin_expect(_c >= 0x80, 0)) {
          i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
          break;
        }
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          *out++ = _c;
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

  /* SIMD tail: page-crossing guard. */
  simd_tail_sse_impl:
    if (__builtin_expect(((uintptr_t)&src[i] & 0xFFF) > (0x1000 - 16), 0))
      goto simd_tail_sse_impl_scalar;
    {
      int remaining = (int)(src_len - i);
      __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
      int mask = html ? vj_escape_mask_16_impl_v(v, 1) : vj_escape_mask_16_impl_v(v, 0);
      int relevant_mask = mask & ((1 << remaining) - 1);
      if (__builtin_expect(relevant_mask == 0, 1)) {
        _mm_storeu_si128((__m128i *)out, v);
        out += remaining;
        return (int)(out - buf);
      }
    }
  simd_tail_sse_impl_scalar:;

    if (i + 8 <= src_len) {
      uint64_t word;
      __builtin_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, html);
      if (mask == 0) {
        __builtin_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        uint8_t _c = src[i];
        if (__builtin_expect(_c >= 0x80, 0)) {
          i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
          break;
        }
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          *out++ = _c;
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* Byte-by-byte tail */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' && !(html && (c == '<' || c == '>' || c == '&'))) {
      *out++ = c;
      i++;
    } else {
      uint8_t _c = src[i];
      if (__builtin_expect(_c < 0x80, 1)) {
        if (_c < 0x20 || _c == '"' || _c == '\\') {
          out += escape_byte(out, _c);
        } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {
          out += write_unicode_escape(out, _c);
        } else {
          *out++ = _c;
        }
        i++;
      } else {
        i += vj_escape_nonascii_run(&out, src, i, src_len, flags);
      }
    }
  }
  return (int)(out - buf);
}

/* ---- SSE-only fast escape ---- */
static inline int escape_string_content_fast_sse(uint8_t *buf, const uint8_t *src, int64_t src_len) {
  uint8_t *out = buf;
  int64_t i = 0;

  if (src_len <= 16)
    goto simd_tail_fast_sse_impl;

  while (i < src_len) {
    if (i + 16 <= src_len) {
      int mask = vj_escape_mask_16_fast(&src[i]);
      if (mask == 0) {
        _mm_storeu_si128((__m128i *)out, _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        {
          uint8_t _c = src[i];
          if (_c < 0x20 || _c == '"' || _c == '\\') {
            out += escape_byte(out, _c);
          } else {
            *out++ = _c;
          }
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

  /* SIMD tail: page-crossing guard. */
  simd_tail_fast_sse_impl:
    if (__builtin_expect(((uintptr_t)&src[i] & 0xFFF) > (0x1000 - 16), 0))
      goto simd_tail_fast_sse_impl_scalar;
    {
      int remaining = (int)(src_len - i);
      __m128i v = _mm_loadu_si128((const __m128i *)&src[i]);
      int mask = vj_escape_mask_16_fast_v(v);
      int relevant_mask = mask & ((1 << remaining) - 1);
      if (__builtin_expect(relevant_mask == 0, 1)) {
        _mm_storeu_si128((__m128i *)out, v);
        out += remaining;
        return (int)(out - buf);
      }
    }
  simd_tail_fast_sse_impl_scalar:;

    if (i + 8 <= src_len) {
      uint64_t word;
      __builtin_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8_fast(word);
      if (mask == 0) {
        __builtin_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      do {
        int safe = __builtin_ctz(mask);
        if (safe > 0) {
          copy_small(out, &src[i], safe);
          out += safe;
          i += safe;
        }
        {
          uint8_t _c = src[i];
          if (_c < 0x20 || _c == '"' || _c == '\\') {
            out += escape_byte(out, _c);
          } else {
            *out++ = _c;
          }
        }
        i++;
        mask = (unsigned)mask >> 1 >> safe;
      } while (mask != 0);
      continue;
    }

    /* Byte-by-byte tail */
    uint8_t c = src[i];
    if (c >= 0x20 && c != '"' && c != '\\') {
      *out++ = c;
      i++;
    } else {
      if (c < 0x20 || c == '"' || c == '\\') {
        out += escape_byte(out, c);
      } else {
        *out++ = c;
      }
      i++;
    }
  }
  return (int)(out - buf);
}

/* ---- SSE-only wrappers with quotes ---- */

static inline int escape_string_content_sse(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags) {
  if (flags & VJ_FLAGS_ESCAPE_HTML)
    return escape_string_content_impl_sse(buf, src, src_len, flags, /*html=*/1);
  return escape_string_content_impl_sse(buf, src, src_len, flags, /*html=*/0);
}

static inline int vj_escape_string_sse(uint8_t *buf, const uint8_t *src, int64_t src_len, uint32_t flags) {
  uint8_t *out = buf;
  *out++ = '"';
  if (src_len > 0) {
    out += escape_string_content_sse(out, src, src_len, flags);
  }
  *out++ = '"';
  return (int)(out - buf);
}

static inline int vj_escape_string_fast_sse(uint8_t *buf, const uint8_t *src, int64_t src_len) {
  uint8_t *out = buf;
  *out++ = '"';
  if (src_len > 0) {
    out += escape_string_content_fast_sse(out, src, src_len);
  }
  *out++ = '"';
  return (int)(out - buf);
}

/* ---- Dispatch macros: always use SSE-only path under AVX2 ----
 * AVX2 string escape showed regressions in benchmarks (ymm register
 * pressure, vzeroupper overhead).  Route everything through the
 * 128-bit SSE path which is consistently faster on tested hardware. */

#define VJ_ESCAPE_STRING_DISPATCH(buf, ptr, len, flags)                                                                \
  vj_escape_string_sse((buf), (const uint8_t *)(ptr), (len), (flags))

#define VJ_ESCAPE_STRING_FAST_DISPATCH(buf, ptr, len) vj_escape_string_fast_sse((buf), (const uint8_t *)(ptr), (len))

#else /* !__AVX2__ */

/* Non-AVX2 builds: dispatch macros call the standard functions directly. */
#define VJ_ESCAPE_STRING_DISPATCH(buf, ptr, len, flags) vj_escape_string((buf), (const uint8_t *)(ptr), (len), (flags))

#define VJ_ESCAPE_STRING_FAST_DISPATCH(buf, ptr, len) vj_escape_string_fast((buf), (const uint8_t *)(ptr), (len))

#endif /* __AVX2__ */

#endif /* VJ_ENCVM_STRING_H */
