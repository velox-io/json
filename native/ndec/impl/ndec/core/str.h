/*
 * Shared string-decode primitives used by both the streaming SAX decoder and the DOM string copier.
 */

#ifndef NDEC_CORE_STR_H
#define NDEC_CORE_STR_H

#include <stddef.h>
#include <stdint.h>

#if defined(__aarch64__)
#include <arm_neon.h>
#elif defined(__x86_64__)
#include <immintrin.h>
#endif

#include "macros.h"

/* hex digit -> [0..15]. Returns 1 on success, 0 if c is not a hex digit. */
INLINE int ndec_str_hex_val(uint8_t c, uint32_t *out) {
  if (c >= '0' && c <= '9') {
    *out = c - '0';
    return 1;
  }
  if (c >= 'a' && c <= 'f') {
    *out = c - 'a' + 10;
    return 1;
  }
  if (c >= 'A' && c <= 'F') {
    *out = c - 'A' + 10;
    return 1;
  }
  return 0;
}

/* Decode 4 hex digits at h[0..3] into a 16-bit codepoint. Returns 1 on
 * success, 0 if any of the four bytes is not a hex digit. */
INLINE int ndec_str_hex4(const uint8_t *h, uint32_t *out) {
  uint32_t a, b, c, d;
  if (!ndec_str_hex_val(h[0], &a) | !ndec_str_hex_val(h[1], &b) | !ndec_str_hex_val(h[2], &c) |
      !ndec_str_hex_val(h[3], &d))
    return 0;
  *out = (a << 12) | (b << 8) | (c << 4) | d;
  return 1;
}

/* UTF-8 encode one rune (assumed valid, <= 0x10FFFF) into dst. Caller
 * must guarantee 4 bytes of headroom. Returns bytes written (1..4). */
INLINE int ndec_str_utf8_encode(uint32_t r, uint8_t *dst) {
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

/* Map the byte AFTER '\' to its single-character escape, e.g. 'n' -> '\n'.
 * Returns 1 and writes the result to *out on success, 0 if c is not a
 * legal single-character escape (caller must then try \uXXXX or fail).
 *
 * Implemented as a 256-byte lookup table rather than a switch so the
 * dispatch collapses to one indexed load + one branchless store. The
 * `switch` form forces a two-load dependency chain (byte-after-backslash,
 * then a jump-table entry) plus an indirect jmp. With the table the
 * two loads (the source byte and the table entry) can pipeline and
 * there is no indirect branch.
 *
 * Sentinel 0 means "not a legal simple escape"; none of the eight
 * mapped outputs is 0 so the test against 0 is unambiguous. */
static const uint8_t ndec_str_simple_escape_table[256] = {
    // clang-format off
    ['"']  = '"',
    ['\\'] = '\\',
    ['/']  = '/',
    ['b']  = '\b',
    ['f']  = '\f',
    ['n']  = '\n',
    ['r']  = '\r',
    ['t']  = '\t',
    // clang-format on
};

INLINE int ndec_str_simple_escape(uint8_t c, uint8_t *out) {
  uint8_t m = ndec_str_simple_escape_table[c];
  *out      = m;
  return m != 0;
}

/* Decode one JSON escape starting at *sip (which points at the byte
 * AFTER '\'). Advances *sip past the escape and *dip past the decoded
 * bytes written to **dip. Returns 0 on success, -1 on malformed input.
 *
 * Bounds policy:
 * With src_end == NULL, the caller guarantees a closing quote and at least 12
 * bytes of trailing 0x20 padding. With a non-NULL src_end, every read is bounded
 * by the exact readable body and a truncated escape returns -1.
 * A NULL compile-time src_end removes the bounded-reader checks. */
static __attribute__((noinline)) int ndec_str_handle_escape(const uint8_t **sip, uint8_t **dip,
                                                            const uint8_t *src_end) {
  const uint8_t *s = *sip;
  uint8_t *d       = *dip;
  if (src_end != NULL && s >= src_end) return -1;
  uint8_t c = *s;
  if (c != 'u') {
    uint8_t mapped;
    if (!ndec_str_simple_escape(c, &mapped)) return -1;
    *d   = mapped;
    *sip = s + 1;
    *dip = d + 1;
    return 0;
  }
  /* \uXXXX needs 4 hex digits after 'u'. */
  if (src_end != NULL && s + 5 > src_end) return -1;
  uint32_t r;
  if (!ndec_str_hex4(s + 1, &r)) return -1;
  if (r >= 0xD800 && r <= 0xDBFF) {
    /* High surrogate: probe for a paired \uYYYY low surrogate. The
     * lookahead reads 6 bytes past s (s[5..10] = '\\','u',h,h,h,h). */
    int have_pair = (src_end == NULL) || (s + 11 <= src_end);
    if (have_pair && s[5] == '\\' && s[6] == 'u') {
      uint32_t low;
      if (ndec_str_hex4(s + 7, &low) && low >= 0xDC00 && low <= 0xDFFF) {
        uint32_t cp = ((r - 0xD800) << 10) + (low - 0xDC00) + 0x10000;
        *dip        = d + (uint32_t)ndec_str_utf8_encode(cp, d);
        *sip        = s + 11;
        return 0;
      }
    }
    /* Unpaired high surrogate: emit U+FFFD. */
    d[0] = 0xEF;
    d[1] = 0xBF;
    d[2] = 0xBD;
    *sip = s + 5;
    *dip = d + 3;
    return 0;
  }
  if (r >= 0xDC00 && r <= 0xDFFF) {
    /* Lone low surrogate: emit U+FFFD. */
    d[0] = 0xEF;
    d[1] = 0xBF;
    d[2] = 0xBD;
    *sip = s + 5;
    *dip = d + 3;
    return 0;
  }
  *dip = d + (uint32_t)ndec_str_utf8_encode(r, d);
  *sip = s + 5;
  return 0;
}

/*
 * SIMD chunk scan for JSON string bodies.
 *
 * Each tick reads NDEC_STR_CHUNK bytes from src, optionally stores them
 * to dst, and emits two masks: `bs` flags '\' positions, `qt` flags '"'
 * positions. The DOM string decoder walks these masks with ctz to find
 * the next backslash or close-quote without re-reading bytes.
 *
 * The mask representation differs per ISA so consumers must reach for
 * `ndec_str_mask_ctz` (not bare __builtin_ctz) to get a byte offset.
 *
 * ARM64 nibble trick: vshrn collapses each input byte into 4 mask bits,
 *                     so ctz(mask)/4 gives the byte position.
 * AVX2 pmovmskb:      one bit per byte directly.
 *
 * NDEC_STR_CHUNK == 0 selects a scalar-only fallback (no SIMD path).
 */
#if defined(__AVX2__)

#define NDEC_STR_CHUNK 32
typedef uint32_t ndec_str_mask;

INLINE void ndec_str_chunk_scan(const uint8_t *src, uint8_t *dst, ndec_str_mask *bs, ndec_str_mask *qt) {
  __m256i v = _mm256_loadu_si256((const __m256i *)src);
  _mm256_storeu_si256((__m256i *)dst, v);
  *bs = (ndec_str_mask)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\')));
  *qt = (ndec_str_mask)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v, _mm256_set1_epi8('"')));
}
/* Same scan, no store. Used by the raw (uncopied) phase of the zero-copy
 * string state machine. */
INLINE void ndec_str_chunk_scan_noload(const uint8_t *src, ndec_str_mask *bs, ndec_str_mask *qt) {
  __m256i v = _mm256_loadu_si256((const __m256i *)src);
  *bs       = (ndec_str_mask)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v, _mm256_set1_epi8('\\')));
  *qt       = (ndec_str_mask)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v, _mm256_set1_epi8('"')));
}
INLINE uint32_t ndec_str_mask_ctz(ndec_str_mask m) {
  return (uint32_t)__builtin_ctz(m);
}

#elif defined(__ARM_NEON)

#define NDEC_STR_CHUNK 32
typedef uint32_t ndec_str_mask;

INLINE uint32_t ndec_str_neon_pack32(uint8x16_t a, uint8x16_t b) {
  static const uint8_t k_bit_mask[16] = {0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80,
                                         0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80};
  uint8x16_t bm                       = vld1q_u8(k_bit_mask);
  uint8x16_t s                        = vpaddq_u8(vandq_u8(a, bm), vandq_u8(b, bm));
  s                                   = vpaddq_u8(s, s);
  s                                   = vpaddq_u8(s, s);
  return (uint32_t)vgetq_lane_u32(vreinterpretq_u32_u8(s), 0);
}

INLINE void ndec_str_chunk_scan(const uint8_t *src, uint8_t *dst, ndec_str_mask *bs, ndec_str_mask *qt) {
  uint8x16_t v0 = vld1q_u8(src);
  uint8x16_t v1 = vld1q_u8(src + 16);
  vst1q_u8(dst, v0);
  vst1q_u8(dst + 16, v1);
  uint8x16_t bs0 = vceqq_u8(v0, vdupq_n_u8('\\'));
  uint8x16_t bs1 = vceqq_u8(v1, vdupq_n_u8('\\'));
  uint8x16_t qt0 = vceqq_u8(v0, vdupq_n_u8('"'));
  uint8x16_t qt1 = vceqq_u8(v1, vdupq_n_u8('"'));
  *bs            = ndec_str_neon_pack32(bs0, bs1);
  *qt            = ndec_str_neon_pack32(qt0, qt1);
}
INLINE void ndec_str_chunk_scan_noload(const uint8_t *src, ndec_str_mask *bs, ndec_str_mask *qt) {
  uint8x16_t v0  = vld1q_u8(src);
  uint8x16_t v1  = vld1q_u8(src + 16);
  uint8x16_t bs0 = vceqq_u8(v0, vdupq_n_u8('\\'));
  uint8x16_t bs1 = vceqq_u8(v1, vdupq_n_u8('\\'));
  uint8x16_t qt0 = vceqq_u8(v0, vdupq_n_u8('"'));
  uint8x16_t qt1 = vceqq_u8(v1, vdupq_n_u8('"'));
  *bs            = ndec_str_neon_pack32(bs0, bs1);
  *qt            = ndec_str_neon_pack32(qt0, qt1);
}
INLINE uint32_t ndec_str_mask_ctz(ndec_str_mask m) {
  return (uint32_t)__builtin_ctz(m);
}

#else

#define NDEC_STR_CHUNK 0

#endif

/* Decode a complete JSON string body into dst. `src` points at the byte
 * AFTER the opening quote; the structural scanner's unclosed-string
 * guarantee + 64-byte 0x20 padding contract let the loop run without
 * per-step bounds checks.
 *
 * Returns the decoded body length, or -1 on malformed escape. dst[ret] is the
 * terminating quote. The SIMD store carries it for disjoint source and
 * destination; scalar exits store it explicitly. Published strings commit
 * ret + 1 bytes, while scratch callers may overwrite the result in place.
 *
 * esc_seen (NULL allowed) receives 1 when any escape was decoded. An esc_seen
 * of 0 means the body is verbatim source text: no backslash preceded the
 * closing quote, the TAPE_STRING_FREE predicate. */
INLINE int32_t ndec_str_parse(const uint8_t *src, uint8_t *dst, int *esc_seen) {
  const uint8_t *si = src;
  uint8_t *di       = dst;

#if NDEC_STR_CHUNK
#if defined(__ARM_NEON)
  /* On NEON, an escape in the first chunk selects the scalar decoder for the
   * remaining body. Later escapes stay in the SIMD loop. */
  {
    ndec_str_mask bs, qt;
    ndec_str_chunk_scan(si, di, &bs, &qt);
    if (((bs - 1) & qt) != 0) {
      return (int32_t)ndec_str_mask_ctz(qt);
    }
    if (UNLIKELY(bs != 0)) goto scalar_tail;
    si += NDEC_STR_CHUNK;
    di += NDEC_STR_CHUNK;
  }
#endif
  for (;;) {
    ndec_str_mask bs, qt;
    ndec_str_chunk_scan(si, di, &bs, &qt);
    /* Quote first: `bs - 1` clears all bits >= lowest bs (or is all-ones
     * if bs == 0); ANDing with qt detects a quote at a strictly earlier
     * position, including the no-bs case. */
    if (((bs - 1) & qt) != 0) {
      return (int32_t)(di - dst + ndec_str_mask_ctz(qt));
    }
    if (UNLIKELY(bs != 0)) {
      uint32_t bp = ndec_str_mask_ctz(bs);
      si += bp + 1; /* skip past the `\` */
      di += bp;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      if (esc_seen) *esc_seen = 1;
      continue;
    }
    si += NDEC_STR_CHUNK;
    di += NDEC_STR_CHUNK;
  }
#if defined(__ARM_NEON)
scalar_tail:
  for (;;) {
    uint8_t c = *si;
    if (c == '"') {
      *di = c; /* sentinel; see the contract above */
      return (int32_t)(di - dst);
    }
    if (UNLIKELY(c == '\\')) {
      si++;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      if (esc_seen) *esc_seen = 1;
      continue;
    }
    *di++ = c;
    si++;
  }
#endif
#else
  /* Scalar-only fallback (no SIMD). Stage1's close-quote contract still
   * applies; the loop is guaranteed to terminate on the close quote. */
  for (;;) {
    uint8_t c = *si;
    if (c == '"') {
      *di = c; /* sentinel; see the contract above */
      return (int32_t)(di - dst);
    }
    if (UNLIKELY(c == '\\')) {
      si++;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      if (esc_seen) *esc_seen = 1;
      continue;
    }
    *di++ = c;
    si++;
  }
#endif
}

/* Read a zero-copy candidate and preserve the destination. Return 1 with the
 * body length for an escape-free string, 2 with the first backslash offset when
 * decoding is required, or -1 when the raw length exceeds 24 bits. The caller
 * handles result 2 with ndec_str_parse_zc_continue under the same bounds contract. */
INLINE int32_t ndec_str_parse_zc_scan(const uint8_t *src, uint32_t *out_len, uint32_t *prefix_bp) {
  const uint8_t *si = src;

#if NDEC_STR_CHUNK
  for (;;) {
    ndec_str_mask bs, qt;
    ndec_str_chunk_scan_noload(si, &bs, &qt);
    if (((bs - 1) & qt) != 0) {
      uint32_t len = (uint32_t)(si - src) + ndec_str_mask_ctz(qt);
      if (UNLIKELY(len > 0xFFFFFFu)) return -1;
      *out_len = len;
      return 1;
    }
    if (UNLIKELY(bs != 0)) {
      *prefix_bp = (uint32_t)(si - src) + ndec_str_mask_ctz(bs);
      return 2;
    }
    si += NDEC_STR_CHUNK;
  }
#else
  for (;;) {
    uint8_t c = *si;
    if (c == '"') {
      uint32_t len = (uint32_t)(si - src);
      if (UNLIKELY(len > 0xFFFFFFu)) return -1;
      *out_len = len;
      return 1;
    }
    if (UNLIKELY(c == '\\')) {
      *prefix_bp = (uint32_t)(si - src);
      return 2;
    }
    si++;
  }
#endif
}

/* Continue from the first escaped byte reported by ndec_str_parse_zc_scan.
 * The result is the decoded length or -1 for malformed input, and dst[result]
 * carries the quote sentinel on success. */
INLINE int32_t ndec_str_parse_zc_continue(const uint8_t *src, uint8_t *dst, uint32_t prefix_bp) {
  __builtin_memcpy(dst, src, prefix_bp);
  const uint8_t *si = src + prefix_bp + 1; /* skip the `\` */
  uint8_t *di       = dst + prefix_bp;
  if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;

#if NDEC_STR_CHUNK
#if defined(__ARM_NEON)
  /* On NEON, another escape in the first continuation chunk selects the scalar
   * decoder. Later escapes stay in the SIMD loop. */
  {
    ndec_str_mask bs, qt;
    ndec_str_chunk_scan(si, di, &bs, &qt);
    if (((bs - 1) & qt) != 0) {
      return (int32_t)(di - dst) + (int32_t)ndec_str_mask_ctz(qt);
    }
    if (UNLIKELY(bs != 0)) goto zc_scalar_tail;
    si += NDEC_STR_CHUNK;
    di += NDEC_STR_CHUNK;
  }
#endif
  for (;;) {
    ndec_str_mask bs, qt;
    ndec_str_chunk_scan(si, di, &bs, &qt);
    if (((bs - 1) & qt) != 0) {
      return (int32_t)(di - dst) + (int32_t)ndec_str_mask_ctz(qt);
    }
    if (UNLIKELY(bs != 0)) {
      uint32_t bp = ndec_str_mask_ctz(bs);
      si += bp + 1;
      di += bp;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      continue;
    }
    si += NDEC_STR_CHUNK;
    di += NDEC_STR_CHUNK;
  }
#if defined(__ARM_NEON)
zc_scalar_tail:
  for (;;) {
    uint8_t c = *si;
    if (c == '"') {
      *di = c; /* sentinel; see the contract above */
      return (int32_t)(di - dst);
    }
    if (UNLIKELY(c == '\\')) {
      si++;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      continue;
    }
    *di++ = c;
    si++;
  }
#endif
#else
  for (;;) {
    uint8_t c = *si;
    if (c == '"') {
      *di = c; /* sentinel; see the contract above */
      return (int32_t)(di - dst);
    }
    if (UNLIKELY(c == '\\')) {
      si++;
      if (ndec_str_handle_escape(&si, &di, NULL) < 0) return -1;
      continue;
    }
    *di++ = c;
    si++;
  }
#endif
}

#endif /* NDEC_CORE_STR_H */
