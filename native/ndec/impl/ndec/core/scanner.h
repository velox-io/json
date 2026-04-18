/*
 * Produces 64-bit structural bitmaps from 64-byte input chunks.
 * Algorithm: classify -> escape resolution -> string mask -> merge.
 *
 * arm64: NEON + PMULL (crypto extension for prefix_xor)
 * x86-64: AVX2 + PCLMULQDQ
 */

#ifndef NDEC_SCANNER_H
#define NDEC_SCANNER_H

#if defined(__aarch64__)
#include <arm_neon.h>
#ifdef __ARM_FEATURE_CRYPTO
#include <arm_acle.h>
#endif
#elif defined(__x86_64__)
#include <immintrin.h>
#include <wmmintrin.h>
#endif

#include "ndec/core/types.h"

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
 * On arm64, BMI is not a thing; we use the plain `if (v == 0)` branch.
 * The hot paths matter mostly on amd64 anyway (AVX2 scanner).
 */
INLINE int ndec_ctz64_empty(uint64_t v, uint32_t *out_idx) {
#if defined(__x86_64__) && defined(__BMI__) && !defined(_MSC_VER)
  uint64_t idx;
  int carry;
  __asm__("tzcntq %2, %0" : "=r"(idx), "=@ccc"(carry) : "r"(v) : "cc");
  *out_idx = (uint32_t)idx;
  return carry;
#else
  *out_idx = v ? (uint32_t)__builtin_ctzll(v) : 64;
  return v == 0;
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
} NdecChunkClass;

#if defined(__aarch64__)

/* Pack four 16-byte comparison masks (0xFF/0x00 per byte) into a single
 * 64-bit bitmap, one bit per input byte. Uses simdjson's vpaddq_u8 ladder:
 * AND each mask with bit_mask (positional bit weights), then cascade
 * pair-wise adds until 8 bytes contain the 64-bit result. 4 vands +
 * 4 vpaddq + 1 lane extract for 64 bytes of input. */
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

  /* Dual-class split-nibble LUTs. For byte b, sig = lo_lut[lo(b)] & hi_lut[hi(b)]:
   *   bits 0..5 (mask 0x3F): op bit set iff b in { ',' ':' '[' ']' '{' '}' }
   *   bits 6..7 (mask 0xC0): ws bit set iff b in { ' ' \t \n \r }
   * Each op char gets a unique bit so no (hi,lo) collision can false-positive.
   * Verified exhaustively for all 256 bytes. */
  static const uint8_t lo_lut_data[16] = {
      0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0x82, 0x14, 0x01, 0xA8, 0x00, 0x00,
  };
  static const uint8_t hi_lut_data[16] = {
      0x80, 0x00, 0x41, 0x02, 0x00, 0x0C, 0x00, 0x30, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
  };

  uint8x16_t lo_lut      = vld1q_u8(lo_lut_data);
  uint8x16_t hi_lut      = vld1q_u8(hi_lut_data);
  uint8x16_t nibble_mask = vdupq_n_u8(0x0F);
  uint8x16_t bs_val      = vdupq_n_u8(0x5C);
  uint8x16_t qt_val      = vdupq_n_u8(0x22);
  uint8x16_t op_mask     = vdupq_n_u8(0x3F);
  uint8x16_t ws_mask     = vdupq_n_u8(0xC0);

  /* Accumulate 4 x uint8x16_t masks per category, pack as a batch. */
  uint8x16_t bs[4], rq[4], ws[4], ops[4];

  for (int i = 0; i < 4; i++) {
    uint8x16_t v          = vld1q_u8(buf + i * 16);
    uint8x16_t low_nibble = vandq_u8(v, nibble_mask);
    uint8x16_t hi_nibble  = vshrq_n_u8(v, 4);

    uint8x16_t sig = vandq_u8(vqtbl1q_u8(lo_lut, low_nibble), vqtbl1q_u8(hi_lut, hi_nibble));

    /* op / ws: signature has any op (bits0..5) / ws (bits 6..7) bit set.
     * Encode as 0xFF/0x00 byte mask via TST (vtstq_u8 == nonzero AND). */
    ops[i] = vtstq_u8(sig, op_mask);
    ws[i]  = vtstq_u8(sig, ws_mask);

    bs[i] = vceqq_u8(v, bs_val);
    rq[i] = vceqq_u8(v, qt_val);
  }

  c.backslash  = ndec_pack_mask64(bs[0], bs[1], bs[2], bs[3]);
  c.raw_quote  = ndec_pack_mask64(rq[0], rq[1], rq[2], rq[3]);
  c.whitespace = ndec_pack_mask64(ws[0], ws[1], ws[2], ws[3]);
  c.op         = ndec_pack_mask64(ops[0], ops[1], ops[2], ops[3]);
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

  __m256i low_mask = _mm256_set1_epi8(0x0F);
  __m256i lo0      = _mm256_and_si256(v0, low_mask);
  __m256i lo1      = _mm256_and_si256(v1, low_mask);

  __m256i ws_lut = _mm256_setr_epi8(0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0x09, 0x0A, 0, 0, 0x0D, 0, 0, 0x20, 0, 0, 0, 0,
                                    0, 0, 0, 0, 0x09, 0x0A, 0, 0, 0x0D, 0, 0);
  uint32_t ws0   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(ws_lut, lo0), v0));
  uint32_t ws1   = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_shuffle_epi8(ws_lut, lo1), v1));
  c.whitespace   = (uint64_t)ws0 | ((uint64_t)ws1 << 32);

  __m256i op_lut1 = _mm256_setr_epi8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x3A, 0, 0x2C, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
                                     0, 0, 0x3A, 0, 0x2C, 0, 0, 0);
  __m256i op_lut2 = _mm256_setr_epi8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x5B, 0, 0x5D, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
                                     0, 0, 0, 0x5B, 0, 0x5D, 0, 0);
  __m256i op_lut3 = _mm256_setr_epi8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x7B, 0, 0x7D, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
                                     0, 0, 0, 0x7B, 0, 0x7D, 0, 0);

  __m256i m1_0 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut1, lo0), v0);
  __m256i m2_0 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut2, lo0), v0);
  __m256i m3_0 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut3, lo0), v0);
  uint32_t op0 = (uint32_t)_mm256_movemask_epi8(_mm256_or_si256(_mm256_or_si256(m1_0, m2_0), m3_0));

  __m256i m1_1 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut1, lo1), v1);
  __m256i m2_1 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut2, lo1), v1);
  __m256i m3_1 = _mm256_cmpeq_epi8(_mm256_shuffle_epi8(op_lut3, lo1), v1);
  uint32_t op1 = (uint32_t)_mm256_movemask_epi8(_mm256_or_si256(_mm256_or_si256(m1_1, m2_1), m3_1));

  c.op = (uint64_t)op0 | ((uint64_t)op1 << 32);
  return c;
}

#else
#error "ndec_classify_chunk: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Escape resolution (simdjson subtraction algorithm) */

typedef struct NdecEscapeResult {
  uint64_t escaped;
} NdecEscapeResult;

/* Escape resolution using simdjson's ODD_BITS subtraction algorithm.
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

  /* Core simdjson algorithm:
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

/* ndec_scan_chunk: classify + string mask + structural merge */

typedef struct NdecChunkResult {
  uint64_t structural;
} NdecChunkResult;

INLINE NdecChunkResult ndec_scan_chunk(const uint8_t *buf, NdecScanState *state) {
  NdecChunkResult result;

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

  uint64_t op = cls.op & ~in_string;

  /* Scalar start: follows structural/whitespace, is not itself structural/ws/quote/in-string */
  uint64_t s            = op | real_quotes;
  uint64_t follows      = ((s | cls.whitespace) << 1) | state->prev_structural_or_ws;
  uint64_t scalar_start = follows & ~cls.whitespace & ~in_string & ~s;

  state->prev_structural_or_ws = (s | cls.whitespace) >> 63;

  result.structural     = op | real_quotes | scalar_start;
  state->last_backslash = cls.backslash;
  return result;
}

/* Chunk advancement.
 *
 * ndec_advance_chunk scans chunk_ptr + 64 (the NEXT chunk). The very
 * first chunk must be scanned separately at parse entry (bootstrap),
 * since advance_chunk cannot scan the current position. */

#define NDEC_EOF (-1)

/* All by-value; on AArch64 the 16-byte struct returns in x0/x1 so the
 * caller never spills chunk_ptr across the call. On failure (insufficient
 * data and !is_final), chunk_ptr is returned unchanged; caller detects EOF
 * via `result.chunk_ptr == old_chunk_ptr`. */
typedef struct NdecAdvanceResult {
  const uint8_t *chunk_ptr; /* updated chunk base (== input on failure) */
  uint64_t bits;            /* structural bits for the new chunk (0 on failure) */
} NdecAdvanceResult;

/* Cold path: remaining < 64 and is_final. Pads the tail chunk in a scratch
 * buffer and scans it. Split out so the hot path doesn't pay for the stack
 * array (and thus the stack-protector canary).
 *
 * memcpy/memset note: __builtin_memcpy/memset with a runtime size emit
 * external memcpy/memset calls. In syso builds those route to
 * native/stdlib/memory.c (no PLT), but it is still a `bl` to a separate
 * function with prologue/epilogue. For tiny payloads this tail runs every
 * call and the call overhead is measurable.
 *
 * Workaround: explicitly type-pun the input as fixed-size 16 byte chunks.
 * Each chunk is read/stored via uint8x16_t (Apple ARM64 NEON intrinsics),
 * which clang will lower to single-instruction LDR/STR Q register pairs
 * inline, no function call. The 0x20 fill is similarly a single
 * vdupq_n_u8 stored four times.
 *
 * On x86_64 the equivalent path is still through __builtin_memcpy with
 * fixed 16 B sizes; clang inlines those as movdqu/movdqa. */
NOINLINE NdecAdvanceResult ndec_advance_chunk_tail(const uint8_t *next, ptrdiff_t remaining,
                                                   NdecScanState *state) {
  uint8_t padded[64] __attribute__((aligned(16)));

  /* Pre-fill all 64 bytes with 0x20. Fixed size 16 unrolled stores let
   * clang emit four NEON dup+str pairs (no function call). */
  __builtin_memset(padded + 0, 0x20, 16);
  __builtin_memset(padded + 16, 0x20, 16);
  __builtin_memset(padded + 32, 0x20, 16);
  __builtin_memset(padded + 48, 0x20, 16);

  /* Copy `remaining` (0..63) bytes from next into padded. Use a chain of
   * compile-time-constant-size __builtin_memcpy calls so clang inlines each
   * one as a NEON ldp/stp pair (no function call). A `while (r >= 16)` loop
   * looks cleaner but clang collapses it into a libc memcpy call with a
   * runtime size; the explicit unrolled if-chain prevents that. */
  size_t r = (size_t)remaining;
  size_t i = 0;
  if (r >= 16) {
    __builtin_memcpy(padded + i, next + i, 16);
    i += 16;
    if (r >= 32) {
      __builtin_memcpy(padded + i, next + i, 16);
      i += 16;
      if (r >= 48) {
        __builtin_memcpy(padded + i, next + i, 16);
        i += 16;
      }
    }
  }
  size_t tail = r & 15; /* 0..15 leftover bytes. */
  if (tail >= 8) {
    __builtin_memcpy(padded + i, next + i, 8);
    i += 8;
    tail -= 8;
  }
  if (tail >= 4) {
    __builtin_memcpy(padded + i, next + i, 4);
    i += 4;
    tail -= 4;
  }
  if (tail >= 2) {
    __builtin_memcpy(padded + i, next + i, 2);
    i += 2;
    tail -= 2;
  }
  if (tail >= 1) {
    padded[i] = next[i];
  }

  NdecChunkResult res = ndec_scan_chunk(padded, state);

  uint64_t mask = ((uint64_t)1 << (uint32_t)remaining) - 1;
  return (NdecAdvanceResult){next, res.structural & mask};
}

/* When !is_final and remaining < 64, returns input chunk_ptr unchanged
 * with bits=0, signalling resume-needed. `is_final` is read from
 * `state->is_final` (set by ndec_ctx_set_input).
 *
 * NOINLINE is deliberate: NEXT_STRUCTURAL calls this from many sites in
 * the state machine, and inlining the full SIMD scan_chunk body at each
 * one bloats the parser and causes a net hot-path regression. Keep it
 * out-of-line. */
NOINLINE NdecAdvanceResult ndec_advance_chunk(const uint8_t *chunk_ptr, const uint8_t *buf_end,
                                              NdecScanState *state) {
  const uint8_t *next = chunk_ptr + 64;
  ptrdiff_t remaining = buf_end - next;

  if (remaining >= 64) {
    NdecChunkResult r = ndec_scan_chunk(next, state);
    return (NdecAdvanceResult){next, r.structural};
  }

  if (remaining <= 0 || !state->is_final) {
    return (NdecAdvanceResult){chunk_ptr, 0};
  }

  return ndec_advance_chunk_tail(next, remaining, state);
}

#endif /* NDEC_SCANNER_H */
