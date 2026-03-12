/*
 * encoder_string.h — Velox JSON C Engine: String Escape
 *
 * JSON string escaping with SIMD (SSE/NEON) and SWAR fast paths.
 * Mirrors Go appendEscapedString (vj_escape.go) behavior.
 *
 * Depends on: encoder_types.h (VjEncFlags, GoString, vj_memcpy, SIMD headers).
 */

#ifndef VJ_ENCODER_STRING_H
#define VJ_ENCODER_STRING_H

/* ---- String escape (JSON) ----
 *
 * Writes the string content (WITHOUT surrounding quotes) to buf.
 * Returns number of bytes written.
 *
 * The caller must ensure buf has enough space (worst case 6x + overhead). */

static const char hex_digits[16] = "0123456789abcdef";

/* ---- Escape lookup table ----
 *
 * For bytes that need escaping (c < 0x20, '"', '\\'), this table gives:
 *   escape_lut[c] = replacement char for the \X form (e.g. 'n' for \n)
 *   0 means use \u00XX form (control chars without a short escape).
 *
 * Entries for safe bytes (>= 0x20, not " or \) are unused and zero. */
static const uint8_t escape_lut[256] = {
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

/* Escape a byte that needs escaping.  Uses the lookup table for the
 * common cases (\", \\, \n, \t, etc.) and falls through to \u00XX
 * for the remaining control characters (0x00-0x1F without a short form).
 * Returns number of bytes written (2 or 6). */
static __attribute__((always_inline)) inline int escape_byte(uint8_t *buf,
                                                             uint8_t c) {
  uint8_t repl = escape_lut[c];
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
  buf[4] = hex_digits[c >> 4];
  buf[5] = hex_digits[c & 0x0F];
  return 6;
}

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int write_unicode_escape(uint8_t *buf, uint32_t cp) {
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = hex_digits[(cp >> 12) & 0xF];
  buf[3] = hex_digits[(cp >> 8) & 0xF];
  buf[4] = hex_digits[(cp >> 4) & 0xF];
  buf[5] = hex_digits[cp & 0xF];
  return 6;
}

/* Decode a UTF-8 sequence starting at s[0]. Returns codepoint and
 * advances *consumed to the number of bytes consumed.
 * On invalid sequence returns 0xFFFD with consumed=1. */
static inline uint32_t decode_utf8(const uint8_t *s, int64_t remaining,
                                   int *consumed) {
  uint8_t b0 = s[0];
  if (b0 < 0x80) {
    *consumed = 1;
    return b0;
  }
  if ((b0 & 0xE0) == 0xC0 && remaining >= 2 && (s[1] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x1F) << 6) | (s[1] & 0x3F);
    if (cp >= 0x80) {
      *consumed = 2;
      return cp;
    }
  }
  if ((b0 & 0xF0) == 0xE0 && remaining >= 3 && (s[1] & 0xC0) == 0x80 &&
      (s[2] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) |
                  ((uint32_t)(s[1] & 0x3F) << 6) | (s[2] & 0x3F);
    if (cp >= 0x800 && !(cp >= 0xD800 && cp <= 0xDFFF)) {
      *consumed = 3;
      return cp;
    }
    /* Surrogate codepoint — treat as invalid below. */
    if (cp >= 0xD800 && cp <= 0xDFFF) {
      *consumed = 3;
      return 0xFFFD; /* flagged as surrogate */
    }
  }
  if ((b0 & 0xF8) == 0xF0 && remaining >= 4 && (s[1] & 0xC0) == 0x80 &&
      (s[2] & 0xC0) == 0x80 && (s[3] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x07) << 18) |
                  ((uint32_t)(s[1] & 0x3F) << 12) |
                  ((uint32_t)(s[2] & 0x3F) << 6) | (s[3] & 0x3F);
    if (cp >= 0x10000 && cp <= 0x10FFFF) {
      *consumed = 4;
      return cp;
    }
  }
  *consumed = 1;
  return 0xFFFD;
}

/*
 * SIMD helpers: scan 16 bytes, return a bitmask where set bits indicate
 * bytes that need escaping or are non-ASCII (>= 0x80).
 *
 * Uses SSE intrinsics — on ARM64, sse2neon.h translates them to NEON.
 * Same pattern as sjmarker/sj_marker.h.
 *
 * Two branchless variants are generated via VJ_ESCAPE_MASK_FUNC macro:
 *   vj_escape_mask_16      — base: c < 0x20, '"', '\\', c >= 0x80
 *   vj_escape_mask_16_html — adds '<', '>', '&'
 *
 * Callers select the appropriate function pointer once, outside the
 * hot loop, eliminating per-iteration branches.
 */

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

static __attribute__((always_inline)) inline int
vj_escape_mask_8(uint64_t word, const int html) {
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

#undef SWAR_HAS_LESS
#undef SWAR_HAS_ZERO
#undef SWAR_HI_BITS
#undef SWAR_LO_BITS
#undef SWAR_BROADCAST

#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)

#define VJ_ESCAPE_MASK_FUNC(name, html)                                        \
  static inline int name(const uint8_t *src) {                                 \
    __m128i v = _mm_loadu_si128((const __m128i *)src);                         \
                                                                               \
    /* c < 0x20: max_epu8(v, 0x1F) != v → cmpeq gives 0 for ctrl chars. */     \
    __m128i ctrl_safe =                                                        \
        _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x1F)), v);               \
                                                                               \
    /* c == '"' or c == '\\' */                                                \
    __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));                      \
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));                    \
                                                                               \
    /* c >= 0x80: signed < 0 */                                                \
    __m128i hi = _mm_cmplt_epi8(v, _mm_setzero_si128());                       \
                                                                               \
    __m128i bad = _mm_or_si128(_mm_or_si128(eq_q, eq_bs), hi);                 \
                                                                               \
    if (html) {                                                                \
      __m128i eq_lt = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));                   \
      __m128i eq_gt = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));                   \
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));                  \
      bad =                                                                    \
          _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp))); \
    }                                                                          \
                                                                               \
    /* safe = ctrl_safe & ~bad;  need_escape = ~safe */                        \
    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);                           \
    return ~_mm_movemask_epi8(safe) & 0xFFFF;                                  \
  }

/* Generate two branchless specializations. The `html` parameter is a
 * compile-time constant (0 or 1), so the compiler eliminates the dead
 * branch entirely — no runtime check in either version. */
VJ_ESCAPE_MASK_FUNC(vj_escape_mask_16, 0)
VJ_ESCAPE_MASK_FUNC(vj_escape_mask_16_html, 1)

#undef VJ_ESCAPE_MASK_FUNC

#endif /* ISA_neon || ISA_sse42 || ISA_avx512 */

/*
 * escape_string_content — write escaped string content (no quotes) to buf.
 *
 * src: raw string bytes, src_len: length.
 * flags: VjEncFlags bitmask.
 * Returns number of bytes written to buf.
 *
 * Two specializations eliminate the check_html branch from the SIMD loop:
 *   escape_string_content_base — no HTML escaping
 *   escape_string_content_html — with HTML escaping (<, >, &)
 *
 * The top-level escape_string_content() dispatches once based on flags.
 */

/* ---- Batch UTF-8 run handler ----
 *
 * Processes an entire contiguous run of non-ASCII bytes (>= 0x80) starting
 * at src[i].  Uses lazy-flush: scans ahead through all non-ASCII bytes,
 * only flushing + escaping on invalid UTF-8 or line terminators (U+2028/29).
 * Valid UTF-8 bytes are bulk-copied in one memcpy at the end of the run.
 *
 * This replaces the old per-rune vj_escape_one_utf8 which was called once
 * per non-ASCII byte, causing N function calls + N memcpy calls for a run
 * of N runes.  Now it's 1 call + typically 1 memcpy.
 *
 * Returns number of source bytes consumed (the entire non-ASCII run).
 * Writes escaped output to *out_ptr and advances it. */
static __attribute__((always_inline)) inline int64_t
vj_escape_utf8_run(uint8_t **out_ptr, const uint8_t *src,
                   int64_t i, int64_t src_len, uint32_t flags) {
  uint8_t *out = *out_ptr;
  const int check_utf8 = (flags & VJ_ENC_ESCAPE_INVALID_UTF8) != 0;
  const int check_line_terms = (flags & VJ_ENC_ESCAPE_LINE_TERMS) != 0;
  const int64_t run_start = i;

  /* Fast path: no validation needed — scan to end of non-ASCII run,
   * bulk copy the entire segment. */
  if (!check_utf8 && !check_line_terms) {
    while (i < src_len && src[i] >= 0x80)
      i++;
    int64_t run_len = i - run_start;
    vj_memcpy(out, &src[run_start], run_len);
    out += run_len;
    *out_ptr = out;
    return run_len;
  }

  /* Validation path: scan rune-by-rune but defer memcpy (lazy flush).
   * `flush_start` marks the beginning of the current un-flushed segment. */
  int64_t flush_start = i;

  while (i < src_len && src[i] >= 0x80) {
    /* --- Line terminator fast check (byte-level) ---
     * U+2028 = E2 80 A8,  U+2029 = E2 80 A9.
     * Only need full decode if first byte is 0xE2. */
    if (check_line_terms && src[i] == 0xE2 &&
        i + 2 < src_len && src[i + 1] == 0x80 &&
        (src[i + 2] == 0xA8 || src[i + 2] == 0xA9)) {
      /* Flush preceding valid bytes */
      if (i > flush_start) {
        int64_t n = i - flush_start;
        vj_memcpy(out, &src[flush_start], n);
        out += n;
      }
      uint32_t cp = (src[i + 2] == 0xA8) ? 0x2028 : 0x2029;
      out += write_unicode_escape(out, cp);
      i += 3;
      flush_start = i;
      continue;
    }

    /* --- UTF-8 validation with length-from-leading-byte --- */
    uint8_t b0 = src[i];

    if ((b0 & 0xE0) == 0xC0) {
      /* 2-byte: 110xxxxx 10xxxxxx */
      if (i + 2 <= src_len && (src[i + 1] & 0xC0) == 0x80) {
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
      if (i + 3 <= src_len &&
          (src[i + 1] & 0xC0) == 0x80 && (src[i + 2] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) |
                      ((uint32_t)(src[i + 1] & 0x3F) << 6) |
                      (src[i + 2] & 0x3F);
        if (cp >= 0x800) {
          if (check_utf8 && cp >= 0xD800 && cp <= 0xDFFF) {
            /* Surrogate codepoint — replace with \ufffd */
            goto replace_ufffd_3;
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
      if (i + 4 <= src_len &&
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

    /* --- Surrogate replacement (3-byte sequence) --- */
  replace_ufffd_3:
    if (i > flush_start) {
      int64_t n = i - flush_start;
      vj_memcpy(out, &src[flush_start], n);
      out += n;
    }
    vj_memcpy(out, "\\ufffd", 6);
    out += 6;
    i += 3;
    flush_start = i;
    continue;

    /* --- Invalid byte handler --- */
  invalid_byte:
    if (check_utf8) {
      if (i > flush_start) {
        int64_t n = i - flush_start;
        vj_memcpy(out, &src[flush_start], n);
        out += n;
      }
      vj_memcpy(out, "\\ufffd", 6);
      out += 6;
      i += 1;
      flush_start = i;
    } else {
      /* No UTF-8 validation — copy invalid byte as-is */
      i += 1;
    }
    continue;
  }

  /* Flush remaining valid bytes from this run */
  if (i > flush_start) {
    int64_t n = i - flush_start;
    vj_memcpy(out, &src[flush_start], n);
    out += n;
  }

  *out_ptr = out;
  return i - run_start;
}

/* ---- Inline ASCII escape macro ----
 *
 * Handles a single byte at src[i] that was flagged by SIMD/SWAR.
 * For ASCII (< 0x80): inlines the escape directly (no function call).
 * For non-ASCII (>= 0x80): delegates to vj_escape_utf8_run which
 * batch-processes the entire contiguous non-ASCII segment.
 *
 * Uses the escape_lut lookup table for branchless escape selection.
 * The `html` parameter must be a compile-time constant. */
#define ESCAPE_ONE_INLINE(html)                                                \
  do {                                                                         \
    uint8_t _c = src[i];                                                       \
    if (__builtin_expect(_c < 0x80, 1)) {                                      \
      if (_c < 0x20 || _c == '"' || _c == '\\') {                             \
        out += escape_byte(out, _c);                                           \
      } else if ((html) && (_c == '<' || _c == '>' || _c == '&')) {            \
        out += write_unicode_escape(out, _c);                                  \
      } else {                                                                 \
        *out++ = _c;                                                           \
      }                                                                        \
      i++;                                                                     \
    } else {                                                                   \
      i += vj_escape_utf8_run(&out, src, i, src_len, flags);                   \
    }                                                                          \
  } while (0)

#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)

/*
 * SIMD-accelerated escape core.  The `html` parameter must be a compile-time
 * constant (0 or 1); after always_inline expansion the dead branch and the
 * unused mask function are eliminated entirely by the optimiser.
 *
 * Key optimizations vs the previous implementation:
 *   1. Inline ASCII escape — no function call for the common case.
 *   2. Lookup table for escape_byte — branchless, no switch.
 *   3. Multi-escape per SIMD window — process ALL flagged bytes in one
 *      pass through the 16-byte mask, avoiding redundant re-scans.
 *   4. copy_small for sub-16-byte chunks — avoids memcpy call overhead.
 */
static __attribute__((always_inline)) inline int
escape_string_content_impl(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags, const int html) {
  uint8_t *out = buf;
  int64_t i = 0;

  while (i < src_len) {
    /* ---- SIMD: scan 16 bytes at a time ---- */
    if (i + 16 <= src_len) {
      int mask = html ? vj_escape_mask_16_html(&src[i])
                      : vj_escape_mask_16(&src[i]);
      if (mask == 0) {
        /* All 16 bytes are safe — bulk copy via SIMD store. */
        _mm_storeu_si128((__m128i *)out,
                         _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }

      /* Copy safe prefix before the first escape byte. */
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        copy_small(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      /* Handle the escape byte inline (no function call for ASCII). */
      ESCAPE_ONE_INLINE(html);
      continue;
    }

    /* ---- SWAR: scan 8 bytes at a time (scalar tail) ---- */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, html);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        copy_small(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      ESCAPE_ONE_INLINE(html);
      continue;
    }

    /* ---- Byte-by-byte tail: fewer than 8 bytes remaining ---- */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' &&
        !(html && (c == '<' || c == '>' || c == '&'))) {
      *out++ = c;
      i++;
    } else {
      ESCAPE_ONE_INLINE(html);
    }
  }
  return (int)(out - buf);
}

/* Two noinline entry points so the compiler generates separate code for each
 * specialisation (different SIMD mask + scalar checks). */
static __attribute__((noinline)) int
escape_string_content_base(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  return escape_string_content_impl(buf, src, src_len, flags, /*html=*/0);
}

static __attribute__((noinline)) int
escape_string_content_html(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  return escape_string_content_impl(buf, src, src_len, flags, /*html=*/1);
}

#else /* no SIMD */

static __attribute__((noinline)) int
escape_string_content_base(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  uint8_t *out = buf;
  int64_t i = 0;
  while (i < src_len) {
    /* SWAR: try to scan 8 bytes at a time. */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, 0);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        copy_small(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      ESCAPE_ONE_INLINE(0);
      continue;
    }
    /* Byte-by-byte tail. */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\') {
      *out++ = c;
      i++;
    } else {
      ESCAPE_ONE_INLINE(0);
    }
  }
  return (int)(out - buf);
}

static __attribute__((noinline)) int
escape_string_content_html(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  uint8_t *out = buf;
  int64_t i = 0;
  while (i < src_len) {
    /* SWAR: try to scan 8 bytes at a time. */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, 1);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        copy_small(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      ESCAPE_ONE_INLINE(1);
      continue;
    }
    /* Byte-by-byte tail. */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' && c != '<' &&
        c != '>' && c != '&') {
      *out++ = c;
      i++;
    } else {
      ESCAPE_ONE_INLINE(1);
    }
  }
  return (int)(out - buf);
}

#endif /* ISA */

#undef ESCAPE_ONE_INLINE

/* Dispatch to the appropriate specialization. */
static inline int escape_string_content(uint8_t *buf, const uint8_t *src,
                                        int64_t src_len, uint32_t flags) {
  if (flags & VJ_ENC_ESCAPE_HTML)
    return escape_string_content_html(buf, src, src_len, flags);
  return escape_string_content_base(buf, src, src_len, flags);
}

#endif /* VJ_ENCODER_STRING_H */
