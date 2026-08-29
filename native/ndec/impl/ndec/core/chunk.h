/*
 * Produces 64-bit structural bitmaps from 64-byte input chunks.
 * Algorithm: classify -> escape resolution -> string mask -> merge.
 *
 * Two emission policies:
 *   ndec_scan_chunk_sax  open + close quotes (the streaming state machine
 *                        needs both to step in / out of string tokens)
 *   ndec_scan_chunk_dom  open quotes only (close quotes are rediscovered
 *                        per-string during DOM build; fewer indexes on
 *                        string-heavy inputs)
 *
 * Both share classify_chunk / compute_escaped / NdecScanState and differ
 * only in the final merge.
 */

#ifndef NDEC_CHUNK_H
#define NDEC_CHUNK_H

#include <stddef.h>
#include <stdint.h>

#if defined(__aarch64__)
#include <arm_neon.h>
#ifdef __ARM_FEATURE_CRYPTO
#include <arm_acle.h>
#endif
#elif defined(__x86_64__)
#include <immintrin.h>
#include <wmmintrin.h>
#endif

#include "macros.h"

/* Cross-chunk carry state for the SIMD scanner. */
typedef struct NdecScanState {
  uint64_t prev_in_string;        /* 0 or ~0 */
  uint64_t prev_escape;           /* 0 or 1 */
  uint64_t prev_structural_or_ws; /* 0 or 1 */
  uint64_t last_backslash;        /* backslash bitmap from last ndec_scan_chunk_* call */
  uint64_t control_error;         /* OR of (cls.control & in_string) across chunks: a raw
                                   * control byte (< 0x20) appeared inside a string. */
} NdecScanState;

INLINE uint64_t ndec_clear_lowest_bit(uint64_t v) {
  return v & (v - 1);
}

/* Branchless probe: tries to get ctz(bits) AND tells whether bits was 0.
 *
 * Writes the trailing-zero count (0..63 for set bits, 64 for empty) to
 * *out_idx, and returns non-zero if bits was zero (i.e. empty bitmap).
 *
 * The x86-64 BMI1 `tzcnt` instruction sets CF=1 exactly when the source
 * was 0. Exposing that flag directly via inline asm lets the compiler
 * emit `tzcntq src, dst; jae happy_path`, one fewer instruction than
 * `test src, src; je; tzcntq src, dst`, and macro-op fusible on modern
 * CPUs. Clang's and GCC's default tzcnt intrinsic does NOT expose this
 * flag, so they fall back to a separate test+branch.
 *
 * On arm64, __builtin_ctzll already maps to rbit+clz which naturally
 * returns 64 for a zero input, and clang marks the intrinsic defined
 * at zero (cttz is_zero_undef=false there), so `idx >= 64` stays a
 * real check and the probe compiles straight-line without an extra
 * branch.
 *
 * Every other target (x86-64 without BMI, 32-bit, ...) takes the
 * explicit zero test. There __builtin_ctzll(0) is undefined, and
 * relying on it returning 64 let clang -O2+ fold the empty check to
 * constant false and delete string_span's only empty-bitmap loop exit,
 * hanging on inputs whose opening quote is the last
 * structural bit of a chunk.
 */
INLINE int ndec_ctz64_empty(uint64_t v, uint32_t *out_idx) {
#if defined(__x86_64__) && defined(__BMI__) && !defined(_MSC_VER)
  uint64_t idx;
  int carry;
  __asm__("tzcntq %2, %0" : "=r"(idx), "=@ccc"(carry) : "r"(v) : "cc");
  *out_idx = (uint32_t)idx;
  return carry;
#elif defined(__aarch64__)
  uint32_t idx = (uint32_t)__builtin_ctzll(v);
  *out_idx     = idx;
  return idx >= 64;
#else
  if (v == 0) {
    *out_idx = 64;
    return 1;
  }
  *out_idx = (uint32_t)__builtin_ctzll(v);
  return 0;
#endif
}

/* prefix_xor: bit i of result = XOR of bits 0..i of input.
 * Converts quote positions into in-string mask. */
INLINE uint64_t ndec_prefix_xor(uint64_t v) {
#if defined(__aarch64__) && defined(__ARM_FEATURE_CRYPTO)
  poly64_t a  = (poly64_t)v;
  poly64_t b  = (poly64_t)(~(uint64_t)0);
  poly128_t r = vmull_p64(a, b);
  return (uint64_t)vgetq_lane_u64(vreinterpretq_u64_p128(r), 0);
#elif defined(__aarch64__)
  /* Apple clang with -march=native does not define __ARM_FEATURE_CRYPTO,
   * but all AArch64 targets with NEON support PMULL. Use inline asm to
   * guarantee the single-instruction path instead of the 6-instruction
   * shift-xor cascade. */
  uint64_t result;
  __asm__("fmov   d0, %[src]      \n"
          "movi.16b v1, #0xff     \n"
          "pmull.1q v0, v0, v1    \n"
          "fmov   %[dst], d0      \n"
          : [dst] "=r"(result)
          : [src] "r"(v)
          : "v0", "v1");
  return result;
#elif defined(__x86_64__) && defined(__PCLMUL__)
  __m128i x    = _mm_set_epi64x(0, (long long)v);
  __m128i ones = _mm_set_epi64x(0, -1LL);
  __m128i r    = _mm_clmulepi64_si128(x, ones, 0);
  return (uint64_t)_mm_cvtsi128_si64(r);
#else
  v ^= v << 1;
  v ^= v << 2;
  v ^= v << 4;
  v ^= v << 8;
  v ^= v << 16;
  v ^= v << 32;
  return v;
#endif
}

/* Chunk classification: 64 bytes -> 4 bitmaps backslash, raw_quote, whitespace, op */

typedef struct NdecChunkClass {
  uint64_t backslash;
  uint64_t raw_quote;
  uint64_t whitespace;
  uint64_t op;
  uint64_t control; /* bytes < 0x20 (U+0000..U+001F) */
} NdecChunkClass;

#if defined(__aarch64__)

/* Pack four 16-byte comparison masks (0xFF/0x00 per byte) into a single
 * 64-bit bitmap, one bit per input byte. */
INLINE uint64_t ndec_pack_mask64(uint8x16_t m0, uint8x16_t m1, uint8x16_t m2, uint8x16_t m3) {
  static const uint8_t bit_mask_data[16] = {
      0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80,
  };
  uint8x16_t bit_mask = vld1q_u8(bit_mask_data);
  uint8x16_t s0       = vpaddq_u8(vandq_u8(m0, bit_mask), vandq_u8(m1, bit_mask));
  uint8x16_t s1       = vpaddq_u8(vandq_u8(m2, bit_mask), vandq_u8(m3, bit_mask));
  s0                  = vpaddq_u8(s0, s1);
  s0                  = vpaddq_u8(s0, s0);
  return vgetq_lane_u64(vreinterpretq_u64_u8(s0), 0);
}

INLINE NdecChunkClass ndec_classify_chunk(const uint8_t *buf) {
  NdecChunkClass c;

  uint8x16_t bs_val = vdupq_n_u8(0x5C); /* backslash */
  uint8x16_t qt_val = vdupq_n_u8(0x22); /* double quote */

  /* Structural characters (op): (d+3)>>4 maps each to a unique table index.
   * Table stores the char value at its index, so vceqq catches the match.
   * The vqtbl1q_u8 clears indices >= 16, and (d+3)>>4 is always 0..15,
   * so only the 6 op chars produce true.  Exhaustively verified. */
  static const uint8_t op_table_data[16] = {
      0xFF, 0, ',', ':', 0, '[', ']', '{', '}', 0, 0, 0, 0, 0, 0, 0,
  };
  uint8x16_t op_table = vld1q_u8(op_table_data);

  /* Whitespace: vqtbx1q_u8 uses the ws_table for low bytes (tab 0x09,
   * LF 0x0A, CR 0x0D) and falls back to the space-eq result for bytes
   * >= 16 (space 0x20 passes, others get 0x00). */
  static const uint8_t ws_table_data[16] = {
      0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0, 0, 0xFF, 0, 0,
  };
  uint8x16_t ws_table = vld1q_u8(ws_table_data);

  /* Bit-packing ladder: AND with positional bit weights, then horizontal add. */
  static const uint8_t bit_mask_data[16] = {
      0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80,
  };
  uint8x16_t bit_mask = vld1q_u8(bit_mask_data);

  uint8x16_t three = vdupq_n_u8(3);
  uint8x16_t space = vdupq_n_u8(' ');

  uint8x16_t d0 = vld1q_u8(buf + 0);
  uint8x16_t d1 = vld1q_u8(buf + 16);
  uint8x16_t d2 = vld1q_u8(buf + 32);
  uint8x16_t d3 = vld1q_u8(buf + 48);

  /* Backslash and quote: simple equality (optimal for single-byte match) */
  uint8x16_t bs0 = vceqq_u8(d0, bs_val);
  uint8x16_t bs1 = vceqq_u8(d1, bs_val);
  uint8x16_t bs2 = vceqq_u8(d2, bs_val);
  uint8x16_t bs3 = vceqq_u8(d3, bs_val);

  uint8x16_t rq0 = vceqq_u8(d0, qt_val);
  uint8x16_t rq1 = vceqq_u8(d1, qt_val);
  uint8x16_t rq2 = vceqq_u8(d2, qt_val);
  uint8x16_t rq3 = vceqq_u8(d3, qt_val);

  /* Op: (d+3)>>4 -> table lookup -> compare with original */
  uint8x16_t op0 = vceqq_u8(vqtbl1q_u8(op_table, vshrq_n_u8(vaddq_u8(d0, three), 4)), d0);
  uint8x16_t op1 = vceqq_u8(vqtbl1q_u8(op_table, vshrq_n_u8(vaddq_u8(d1, three), 4)), d1);
  uint8x16_t op2 = vceqq_u8(vqtbl1q_u8(op_table, vshrq_n_u8(vaddq_u8(d2, three), 4)), d2);
  uint8x16_t op3 = vceqq_u8(vqtbl1q_u8(op_table, vshrq_n_u8(vaddq_u8(d3, three), 4)), d3);

  /* Whitespace: table lookup for tab/LF/CR, fallback to space check */
  uint8x16_t ws0 = vqtbx1q_u8(vceqq_u8(d0, space), ws_table, d0);
  uint8x16_t ws1 = vqtbx1q_u8(vceqq_u8(d1, space), ws_table, d1);
  uint8x16_t ws2 = vqtbx1q_u8(vceqq_u8(d2, space), ws_table, d2);
  uint8x16_t ws3 = vqtbx1q_u8(vceqq_u8(d3, space), ws_table, d3);

  /* Pack op and ws: interleaved horizontal addition, sharing bit_mask */
  uint8x16_t op_sum0 = vpaddq_u8(vandq_u8(op0, bit_mask), vandq_u8(op1, bit_mask));
  uint8x16_t ws_sum0 = vpaddq_u8(vandq_u8(ws0, bit_mask), vandq_u8(ws1, bit_mask));
  uint8x16_t op_sum1 = vpaddq_u8(vandq_u8(op2, bit_mask), vandq_u8(op3, bit_mask));
  uint8x16_t ws_sum1 = vpaddq_u8(vandq_u8(ws2, bit_mask), vandq_u8(ws3, bit_mask));
  op_sum0            = vpaddq_u8(op_sum0, op_sum1);
  ws_sum0            = vpaddq_u8(ws_sum0, ws_sum1);
  op_sum0            = vpaddq_u8(op_sum0, op_sum0);
  ws_sum0            = vpaddq_u8(ws_sum0, ws_sum0);
  c.op               = vgetq_lane_u64(vreinterpretq_u64_u8(op_sum0), 0);
  c.whitespace       = vgetq_lane_u64(vreinterpretq_u64_u8(ws_sum0), 0);

  /* Pack backslash and quote via existing helper */
  c.backslash = ndec_pack_mask64(bs0, bs1, bs2, bs3);
  c.raw_quote = ndec_pack_mask64(rq0, rq1, rq2, rq3);

  /* Control characters: bytes < 0x20 (U+0000..U+001F). RFC 8259 requires
   * these to be escaped inside strings; ndec_scan_chunk_planes flags them via
   * cls.control & in_string. Strict unsigned < 0x20 (space 0x20 stays valid). */
  uint8x16_t ct_val = vdupq_n_u8(0x20);
  uint8x16_t ct0    = vcltq_u8(d0, ct_val);
  uint8x16_t ct1    = vcltq_u8(d1, ct_val);
  uint8x16_t ct2    = vcltq_u8(d2, ct_val);
  uint8x16_t ct3    = vcltq_u8(d3, ct_val);
  c.control         = ndec_pack_mask64(ct0, ct1, ct2, ct3);

  return c;
}

#elif defined(__x86_64__)

INLINE NdecChunkClass ndec_classify_chunk(const uint8_t *buf) {
  NdecChunkClass c;

  __m256i v0 = _mm256_loadu_si256((const __m256i *)(buf));
  __m256i v1 = _mm256_loadu_si256((const __m256i *)(buf + 32));

  __m256i bs_cmp = _mm256_set1_epi8(0x5C);
  uint32_t bs0   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v0, bs_cmp));
  uint32_t bs1   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v1, bs_cmp));
  c.backslash    = (uint64_t)bs0 | ((uint64_t)bs1 << 32);

  __m256i qt_cmp = _mm256_set1_epi8(0x22);
  uint32_t qt0   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v0, qt_cmp));
  uint32_t qt1   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v1, qt_cmp));
  c.raw_quote    = (uint64_t)qt0 | ((uint64_t)qt1 << 32);

  /* Control characters: bytes < 0x20 (U+0000..U+001F). min_epu8 takes the
   * per-byte unsigned minimum, so min_epu8(v, 0x1F) == v holds exactly when
   * v <= 0x1F; multibyte bytes 0x80..0xFF never match. */
  __m256i ctrl_max = _mm256_set1_epi8(0x1F);
  uint32_t ct0     = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_min_epu8(v0, ctrl_max), v0));
  uint32_t ct1     = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_min_epu8(v1, ctrl_max), v1));
  c.control        = (uint64_t)ct0 | ((uint64_t)ct1 << 32);

  __m256i low_mask = _mm256_set1_epi8(0x0F);
  __m256i lo0      = _mm256_and_si256(v0, low_mask);
  __m256i lo1      = _mm256_and_si256(v1, low_mask);

  __m256i ws_lut = _mm256_setr_epi8(0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0x09, 0x0A, 0, 0, 0x0D, 0, 0, 0x20, 0, 0, 0, 0,
                                    0, 0, 0, 0, 0x09, 0x0A, 0, 0, 0x0D, 0, 0);
  uint32_t ws0   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(ws_lut, lo0), v0));
  uint32_t ws1   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(ws_lut, lo1), v1));
  c.whitespace   = (uint64_t)ws0 | ((uint64_t)ws1 << 32);

  /* Operators via OR-0x20 normalization + single LUT.
   *
   * OR with 0x20 turns '[' (0x5B) into '{' (0x7B) and ']' (0x5D) into '}' (0x7D),
   * so all six operators collapse to four distinct lower-4-bit values:
   * ':' (0xA), '{'/'[' (0xB), ',' (0xC), '}'/']' (0xD). A single VPSHUFB
   * looks them up; a final VPCMPEQB against the OR-normalized input
   * rejects any byte whose lower nibble matches a slot but whose upper
   * nibble doesn't (e.g. ';' has low nibble 0xB). */
  __m256i normalize = _mm256_set1_epi8(0x20);
  __m256i cv0       = _mm256_or_si256(v0, normalize);
  __m256i cv1       = _mm256_or_si256(v1, normalize);

  __m256i op_lut = _mm256_setr_epi8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, ':', '{', ',', '}', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
                                    0, 0, ':', '{', ',', '}', 0, 0);
  uint32_t op0   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut, lo0), cv0));
  uint32_t op1   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut, lo1), cv1));
  c.op           = (uint64_t)op0 | ((uint64_t)op1 << 32);
  return c;
}

#else
#error "ndec_classify_chunk: unsupported architecture (need aarch64 or x86_64)"
#endif

typedef struct NdecEscapeResult {
  uint64_t escaped;
} NdecEscapeResult;

/* Escape resolution using ODD_BITS subtraction algorithm.
 *
 * Given a backslash bitmap and cross-chunk carry (prev_escape), computes
 * which positions are escaped (the byte AFTER an odd-length backslash run).
 *
 * The key insight: shift backslashes left by 1 to get "maybe escaped",
 * OR with ODD_BITS (0xAA..AA), subtract backslashes to propagate through
 * runs, XOR with ODD_BITS to correct odd-aligned runs. This yields the
 * "escape_and_terminal_code" which marks real escape characters and the
 * characters they escape. */
INLINE NdecEscapeResult ndec_compute_escaped(uint64_t backslash, NdecScanState *state) {
  NdecEscapeResult r;
  static const uint64_t ODD_BITS = 0xAAAAAAAAAAAAAAAAULL;

  /* Strip the first backslash if it was escaped by the previous chunk */
  uint64_t potential_escape = backslash & ~state->prev_escape;

  /* Core algorithm:
   * 1. Shift left to get "maybe escaped" positions
   * 2. OR with ODD_BITS to seed odd-bit positions
   * 3. Subtract potential_escape to propagate through runs
   * 4. XOR with ODD_BITS to correct odd-aligned runs */
  uint64_t maybe_escaped                  = potential_escape << 1;
  uint64_t maybe_escaped_and_odd_bits     = maybe_escaped | ODD_BITS;
  uint64_t even_series_codes_and_odd_bits = maybe_escaped_and_odd_bits - potential_escape;
  uint64_t escape_and_terminal_code       = even_series_codes_and_odd_bits ^ ODD_BITS;

  /* escaped = positions that are escaped by a real backslash */
  r.escaped = escape_and_terminal_code ^ (backslash | state->prev_escape);

  /* Cross-chunk carry: if the last backslash is a real escape (odd-run),
   * the first byte of the next chunk is escaped. */
  uint64_t escape    = escape_and_terminal_code & backslash;
  state->prev_escape = escape >> 63;

  return r;
}

/* NdecPlanePop: per-plane structural populations, summed across chunks.
 *
 * Kept as three counts rather than one total because the tape cost per structural
 * differs by plane, and only the widest interpretation would be safe from a total:
 *   op           ~ 1 word ({ and [ open, } and ] close, : and , none)
 *   real_quotes  ~ 1 word per PAIR, a string being one word
 *   scalar_start ~ 2 words worst case, an int64 or double being a tag plus a value
 * Separate counts preserve the tighter per-plane word bound. */
typedef struct NdecPlanePop {
  uint32_t op;
  uint32_t quotes;
  uint32_t scalars;
} NdecPlanePop;

/* Accumulate one chunk's populations. A macro rather than a function so the
 * whole thing disappears at the call sites that pass want_counts = 0. */
#define NDEC_PLANE_POP_ADD(dst_, r_)                                                                              \
  do {                                                                                                            \
    (dst_)->op += (r_).plane_pop.op;                                                                              \
    (dst_)->quotes += (r_).plane_pop.quotes;                                                                      \
    (dst_)->scalars += (r_).plane_pop.scalars;                                                                    \
  } while (0)

/* Counting modes for the structural scan. Compile-time constants at every
 * NOINLINE entry point, so each specialization folds to one chunk flavor. */
enum {
  NDEC_COUNT_NONE    = 0, /* planes merged, populations discarded */
  NDEC_COUNT_PLANES  = 1, /* full per-plane populations (bind tape sizing) */
  NDEC_COUNT_SCALARS = 2  /* scalar_start only (DOM tape sizing) */
};

/* ndec_scan_tape_words: an upper bound on the tape words a document needs, from
 * the token mix the scan just counted.
 *
 * Per plane, worst case:
 *   scalars  2 words each. An int64 or double is a tag word plus a value word,
 *            and a scalar_start is exactly one per number, true, false or null.
 *   quotes   1 word per PAIR. A string is one word wherever it appears, as a key
 *            or as a value, and both its delimiters are counted.
 *   op       1 word each is already generous: only { [ } ] produce a word, while
 *            : and , produce none, and all four share this count.
 *
 * Then the seams, which exist only on a merged tape: at most one in front of each
 * entry, plus one lead-in per tape. An entry needs a ':' and an element a ',', so
 * op bounds the entry count; a tape needs a container, and a container costs two
 * ops, so op/2 bounds the tape count. dual adds two words per tape of retained
 * headroom: a dual shared root spends the same words as a single-view tape, but
 * the cumulative span a nested rebind can copy is not yet proven against the
 * seam-only bound, so the term stays until that proof or a checked writer exists.
 *
 * The result can exceed the srcLen-derived bound on token-dense input such as
 * "[1,1,1]", so callers take the minimum of these two independent bounds. */
INLINE uint32_t ndec_scan_tape_words(NdecPlanePop pop, uint32_t dual) {
  uint32_t content = pop.op + pop.quotes / 2 + 2u * pop.scalars;
  uint32_t tapes   = pop.op / 2u;
  uint32_t seams   = pop.op + tapes;
  return content + seams + (dual ? 2u * tapes : 0u);
}

/* Output of the chunk scan, in raw bit-plane form. The two wrappers
 * below pick which planes get OR'd into a single structural bitmap.
 *
 * plane_pop carries the per-plane populations, which the OR destroys: once the
 * planes are merged, "how many of these were numbers" is unanswerable, and that
 * is what sizes a tape (a number costs 2 words, a string half of one). Filled
 * only by the _counted wrapper; the others leave it zero and the adds fold away.
 */
typedef struct NdecChunkResult {
  uint64_t structural;
  NdecPlanePop plane_pop;
} NdecChunkResult;

/* Internal: classify, resolve escapes, compute the four bit planes
 * (op / real_quotes / scalar_start / in_string). Both public wrappers
 * call this and differ only in the final OR. Out-params return planes
 * so the wrappers compile to a single OR / OR-AND-OR. */
INLINE void ndec_scan_chunk_planes(const uint8_t *buf, NdecScanState *state, uint64_t *out_op,
                                   uint64_t *out_real_quotes, uint64_t *out_scalar_start,
                                   uint64_t *out_in_string) {
  NdecChunkClass cls = ndec_classify_chunk(buf);

  /* Fast path: most chunks have no backslashes. */
  uint64_t real_quotes;
  if (__builtin_expect(cls.backslash == 0, 1)) {
    /* Consume cross-chunk escape carry branchlessly.  prev_escape is
     * 0 or 1, so ~0 == all-ones (no-op) and ~1 clears only bit 0. */
    real_quotes        = cls.raw_quote & ~state->prev_escape;
    state->prev_escape = 0;
  } else {
    uint64_t raw_quote_adj = cls.raw_quote & ~state->prev_escape;
    NdecEscapeResult esc   = ndec_compute_escaped(cls.backslash, state);
    real_quotes            = raw_quote_adj & ~esc.escaped;
  }

  uint64_t in_string    = ndec_prefix_xor(real_quotes) ^ state->prev_in_string;
  state->prev_in_string = (int64_t)in_string >> 63;

  /* Control-character validation. A raw byte < 0x20 inside a string is a
   * structural error (RFC 8259 requires U+0000..U+001F escaped). It can never
   * be a valid escape continuation, so escaping needs no special handling.
   * The control plane is computed once per chunk above and folded into
   * in_string here for both the DOM and SAX emission policies. */
  state->control_error |= cls.control & in_string;

  uint64_t op = cls.op & ~in_string;

  /* Scalar start: follows structural/whitespace, is not itself structural/ws/quote/in-string */
  uint64_t s            = op | real_quotes;
  uint64_t follows      = ((s | cls.whitespace) << 1) | state->prev_structural_or_ws;
  uint64_t scalar_start = follows & ~cls.whitespace & ~in_string & ~s;

  state->prev_structural_or_ws = (s | cls.whitespace) >> 63;
  state->last_backslash        = cls.backslash;

  *out_op           = op;
  *out_real_quotes  = real_quotes;
  *out_scalar_start = scalar_start;
  *out_in_string    = in_string;
}

/* SAX mode emits both open and close quotes; the streaming state
 * machine needs the close to step out of a string token. */
INLINE NdecChunkResult ndec_scan_chunk_sax(const uint8_t *buf, NdecScanState *state) {
  uint64_t op, real_quotes, scalar_start, in_string;
  ndec_scan_chunk_planes(buf, state, &op, &real_quotes, &scalar_start, &in_string);
  return (NdecChunkResult){op | real_quotes | scalar_start, {0, 0, 0}};
}

/* DOM mode emits only open quotes. in_string is 1 at the start quote
 * position and 0 at the end quote position (prefix_xor toggles parity
 * at each quote), so `real_quotes & in_string` keeps only opens. */
INLINE NdecChunkResult ndec_scan_chunk_dom(const uint8_t *buf, NdecScanState *state) {
  uint64_t op, real_quotes, scalar_start, in_string;
  ndec_scan_chunk_planes(buf, state, &op, &real_quotes, &scalar_start, &in_string);
  return (NdecChunkResult){op | (real_quotes & in_string) | scalar_start, {0, 0, 0}};
}

/* DOM mode, additionally reporting each plane's population so a caller can size a
 * tape from the token mix rather than from the byte count.
 *
 * Separate from ndec_scan_chunk_dom rather than a runtime flag on it: the three
 * popcounts are the whole cost, so a caller that does not need them must not
 * emit them at all. The planes are already in registers here (the OR below is
 * their only other consumer), which is why counting is three instructions and
 * not a second pass.
 *
 * quotes counts real_quotes UNMASKED, both delimiters of every string, while the
 * structural mask keeps only opens. Halving it is the caller's job, and doing it
 * per chunk would lose the odd one whenever a string spans a chunk boundary. */
INLINE NdecChunkResult ndec_scan_chunk_dom_counted(const uint8_t *buf, NdecScanState *state) {
  uint64_t op, real_quotes, scalar_start, in_string;
  ndec_scan_chunk_planes(buf, state, &op, &real_quotes, &scalar_start, &in_string);
  NdecPlanePop pop = {(uint32_t)__builtin_popcountll(op), (uint32_t)__builtin_popcountll(real_quotes),
                      (uint32_t)__builtin_popcountll(scalar_start)};
  return (NdecChunkResult){op | (real_quotes & in_string) | scalar_start, pop};
}

/* DOM mode reporting only the scalar_start population. The tape-word bound
 * derives every other plane from the structural bitmap the scan already
 * totals: op and the open quotes merge into it, and the closes are the other
 * half of quotes, so per document
 *
 *     n_idx == op + quotes/2 + scalars   and   words == n_idx + scalars + 3.
 *
 * One popcount per chunk is the entire counting cost. */
INLINE NdecChunkResult ndec_scan_chunk_dom_scount(const uint8_t *buf, NdecScanState *state) {
  uint64_t op, real_quotes, scalar_start, in_string;
  ndec_scan_chunk_planes(buf, state, &op, &real_quotes, &scalar_start, &in_string);
  return (NdecChunkResult){op | (real_quotes & in_string) | scalar_start,
                           {0, 0, (uint32_t)__builtin_popcountll(scalar_start)}};
}

#endif /* NDEC_CHUNK_H */
