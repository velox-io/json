/* JSON number parsing for typed binding and tape emission.
 *
 * Typed integer targets parse with atoi so binary64 rounding cannot affect range
 * checks. Float targets parse at their destination precision; the float32 wrappers
 * remain out of line because that target is uncommon. The shared atof_ctx keeps
 * multi-precision correction scratch off the native call stack.
 *
 * Tape emission preserves exact integers as signed or unsigned payloads, ordinary
 * real numbers as binary64 plus a stable StrArena text span, and numbers that
 * require source fidelity as TAPE_NUM_RAW. Construction copies the span but does
 * not parse binary32; a later float32 bind performs that uncommon work lazily.
 * Callers provide readable padding after the input so digit and SWAR probes can
 * discover token boundaries without per-byte bounds checks. */
#ifndef NDEC_NUM_H
#define NDEC_NUM_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"
#include "ndec/core/atof.h"
#include "ndec/core/atoi.h" // IWYU pragma: keep
#include "ndec/core/delim.h"

INLINE int ndec_parse_double(const uint8_t *src, uint32_t readable_bytes, double *out, atof_ctx *ctx) {
  atof_result_f64 r = atof_parse_f64_json_ctx((const char *)src, (int)readable_bytes, ctx);
  if (r.end == (const char *)src) return 1;
  *out = r.val;
  return 0;
}

NOINLINE static int ndec_parse_float32(const uint8_t *src, uint32_t readable_bytes, float *out, atof_ctx *ctx) {
  atof_result_f32 r = atof_parse_f32_json_ctx((const char *)src, (int)readable_bytes, ctx);
  if (r.end == (const char *)src) return 1;
  *out = r.val;
  return 0;
}

/* Padded variants use the 64B trailing padding on ndec source buffers as their
 * readable horizon and return the token boundary through end_out. Longer tokens
 * still parse through the byte loop after the SWAR path reaches that horizon. */
#define NDEC_NUMBER_PADDED_MAX 64
NOINLINE static int ndec_parse_double_padded(const uint8_t *src, double *out, atof_ctx *ctx,
                                             const uint8_t **end_out) {
  atof_result_f64 r = atof_parse_f64_json_ctx((const char *)src, NDEC_NUMBER_PADDED_MAX, ctx);
  if (r.end == (const char *)src) return 1;
  *out     = r.val;
  *end_out = (const uint8_t *)r.end;
  return 0;
}

NOINLINE static int ndec_parse_float32_padded(const uint8_t *src, float *out, atof_ctx *ctx,
                                              const uint8_t **end_out) {
  /* The f64 padded scanner can discover a token beyond the 64-byte SWAR horizon.
   * Parse the resulting exact span again at binary32 precision. */
  atof_result_f64 scan = atof_parse_f64_json_ctx((const char *)src, NDEC_NUMBER_PADDED_MAX, ctx);
  if (scan.end == (const char *)src) return 1;
  atof_result_f32 r = atof_parse_f32_json_ctx((const char *)src, (int)(scan.end - (const char *)src), ctx);
  if (r.end != scan.end) return 1;
  *out     = r.val;
  *end_out = (const uint8_t *)scan.end;
  return 0;
}

/* Cold f64 exit for >19-digit JSON integer tokens that overflow uint64.
 * NOINLINE keeps the EL/finalize/refine machinery off the inlined hot
 * integer/float path. */
NOINLINE static atof_result_f64 ndec_parse_bigint_f64(const char *s, int readable_bytes, atof_ctx *ctx) {
  return atof_parse_f64_json_ctx(s, readable_bytes, ctx);
}

INLINE int dom_parse_digit(uint8_t c, uint64_t *i) {
  uint8_t d = (uint8_t)(c - '0');
  if (d > 9) return 0;
  *i = 10 * *i + d;
  return 1;
}

INLINE int dom_is_made_of_eight_digits(const uint8_t *p) {
  uint64_t v;
  __builtin_memcpy(&v, p, 8);
  return (((v & 0xF0F0F0F0F0F0F0F0ULL) | (((v + 0x0606060606060606ULL) & 0xF0F0F0F0F0F0F0F0ULL) >> 4)) ==
          0x3333333333333333ULL);
}

INLINE uint32_t dom_parse_eight_digits(const uint8_t *p) {
  uint64_t v;
  __builtin_memcpy(&v, p, 8);
  v = (v & 0x0F0F0F0F0F0F0F0FULL) * 2561 >> 8;
  v = (v & 0x00FF00FF00FF00FFULL) * 6553601 >> 16;
  return (uint32_t)((v & 0x0000FFFF0000FFFFULL) * 42949672960001ULL >> 32);
}

/* Parse decimal after separator: p points at the first byte after '.'.
 * Updates *pp past the last fractional digit. Returns 0 on success,
 * -1 if no digits followed the dot.
 * */
INLINE int dom_parse_decimal(const uint8_t **pp, uint64_t *i, int64_t *exp) {
  const uint8_t *p     = *pp;
  const uint8_t *first = p;
  if (dom_is_made_of_eight_digits(p)) {
    *i = *i * 100000000ULL + dom_parse_eight_digits(p);
    p += 8;
  }
  while (dom_parse_digit(*p, i))
    p++;
  *exp = (int64_t)(first - p); /* negative: -frac_digits */
  *pp  = p;
  return p == first ? -1 : 0;
}

INLINE int dom_parse_exponent(const uint8_t **pp, int64_t *exponent) {
  const uint8_t *p = *pp;
  int neg_exp      = (*p == '-');
  if (neg_exp || *p == '+') p++;
  const uint8_t *start_exp = p;
  uint64_t exp_n           = 0;
  while (dom_parse_digit(*p, &exp_n))
    p++;
  if (UNLIKELY(p == start_exp)) return -1;
  if (UNLIKELY(p > start_exp + 18)) {
    while (*start_exp == '0')
      start_exp++;
    if (p > start_exp + 18) exp_n = 999999999999999999ULL;
  }
  *exponent += neg_exp ? -(int64_t)exp_n : (int64_t)exp_n;
  *pp = p;
  return 0;
}

/*
 * Clinger fast path then Eisel-Lemire main path.
 *
 * Clinger requires mant <= 2^53-1 and |power| <= 22 so both operands
 * are exactly representable in binary64; one mul or div produces a
 * correctly rounded result.
 *
 * atof_i_eisel_lemire_f64 handles the rest of [smallest_power, largest_power]
 * via a 128-bit multiply against a precomputed power-of-five table.
 * */

INLINE int dom_parse_float_64(int64_t power, uint64_t i, int neg, double *out) {
  if (LIKELY(-22 <= power && power <= 22 && i <= 9007199254740991ULL)) {
    double d = (double)i;
    if (power < 0) {
      d = d / atof_i_f64_exact_pow10[-power];
    } else {
      d = d * atof_i_f64_exact_pow10[power];
    }
    *out = neg ? -d : d;
    return 1;
  }
  if (i == 0) {
    *out = neg ? -0.0 : 0.0;
    return 1;
  }
  if (UNLIKELY(power > 308)) return 0; /* +Inf -> caller errors */
  if (UNLIKELY(power < -342)) {
    *out = neg ? -0.0 : 0.0;
    return 1;
  }
  uint64_t bits;
  atof_i_eisel_lemire_f64(i, (int)power, &bits);
  bits |= (uint64_t)neg << 63;
  __builtin_memcpy(out, &bits, sizeof(*out));
  return 1;
}

/* Number tags preserve exact integer forms as l or u, decimal binary64 as d,
 * and source text as D when faithful representation requires the original
 * decimal spelling. The d and D forms retain their validated token in str_arena
 * for typed reparsing.
 *
 * Retained text carries a NUL sentinel because numeric scanners may inspect one
 * byte past the declared span. Number and string tokens occupy disjoint source
 * spans, preserving the document-sized arena bound. Payloads limit offsets to
 * 32 bits and lengths to 24 bits. */
INLINE int dom_store_num_text(const uint8_t *src, const uint8_t *end, uint8_t *str_arena_base, uint8_t **str_pp,
                              const uint8_t *str_limit, uint64_t *payload) {
  size_t n = (size_t)(end - src);
  if (UNLIKELY(n > 0xFFFFFFu)) return 1;
  uint8_t *base = *str_pp;
  if (UNLIKELY(base + n + 1 > str_limit)) return 1;
  uint64_t off = (uint64_t)(base - str_arena_base);
  if (UNLIKELY(off > UINT32_MAX)) return 1;
  __builtin_memcpy(base, src, n);
  base[n]  = 0;
  *str_pp  = base + n + 1;
  *payload = off | ((uint64_t)n << 32);
  return 0;
}

INLINE int dom_emit_num_raw(const uint8_t *src, const uint8_t *end, uint64_t **tape_pp, uint8_t *str_arena_base,
                            uint8_t **str_pp, const uint8_t *str_limit) {
  uint64_t payload;
  if (dom_store_num_text(src, end, str_arena_base, str_pp, str_limit, &payload)) return 1;
  /* Tag byte is literal because tape.h includes this file before TAPE_NUM_RAW is declared. */
  *(*tape_pp)++ = ((uint64_t)'D' << 56) | payload;
  return 0;
}

INLINE int dom_visit_number(atof_ctx *atof, const uint8_t *src, uint64_t **tape_pp, uint8_t *str_arena_base,
                            uint8_t **str_pp, const uint8_t *str_limit) {
  uint64_t *tape_p = *tape_pp;

  int neg          = (*src == '-');
  const uint8_t *p = src + neg;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  while (dom_parse_digit(*p, &i)) {
    p++;
  };

  size_t digit_count = (size_t)(p - start_digits);
  /* JSON integer grammar requires a digit and permits a leading zero only when
   * it is the complete integer part, as in "0", "0.5", or "0e1". */
  if (UNLIKELY(digit_count == 0 || (*start_digits == '0' && digit_count > 1))) {
    return -1;
  }

  int64_t exponent = 0;
  int is_real      = 0;
  if (*p == '.') {
    is_real = 1;
    p++;
    if (dom_parse_decimal(&p, &i, &exponent)) return -1;
    digit_count = (size_t)(p - start_digits);
  }
  if (UNLIKELY(*p == 'e' || *p == 'E')) {
    is_real = 1;
    p++;
    if (dom_parse_exponent(&p, &exponent)) return -1;
  }
  if (UNLIKELY(is_real)) {
    /* Capture dirty_end before we may jump to the bigint delegate, which
     * advances no further but whose result still needs the same trailing
     * structural check as the fast path. */
    int dirty_end = is_non_delim(*p);
    double dv;

    if (UNLIKELY(digit_count > 19)) {
      /* digit_count counts a leading '0' and the '.' as well; strip them
       * before deciding whether the *significant* digit count actually
       * exceeds 19. "0.0000000000000000001" has digit_count=21 but only
       * one significant digit and must take the fast path. */
      const uint8_t *s = start_digits;
      // clang-format off
      while (*s == '0' || *s == '.') s++;
      // clang-format on
      if (digit_count - (size_t)(s - start_digits) > 19) {
        /* Past 19 significant digits no double is the value the source named,
         * so the text itself is the only faithful form. The trailing structural
         * check still has to run: dirty_end was captured before this branch. */
        if (UNLIKELY(dirty_end)) return -1;
        if (dom_emit_num_raw(src, p, &tape_p, str_arena_base, str_pp, str_limit) == 0) {
          *tape_pp = tape_p;
          return 0;
        }
        atof_result_f64 r = ndec_parse_bigint_f64((const char *)src, (int)(p - src), atof);
        if (UNLIKELY(r.end == (const char *)src)) return -1;
        /* This delegate bypasses the exponent > 308 range gate below,
         * so an Inf from atof would otherwise reach the tape via
         * goto float_done. Reject here, same policy as bigint label. */
        if (UNLIKELY(!__builtin_isfinite(r.val))) return -1;
        dv = r.val;
        goto float_done;
      }
    }
    /* Range gates before dom_parse_float_64: it asserts power is within
     * the Eisel-Lemire table [-342, 308]. Above 308 with nonzero mantissa
     * is +Inf (rejected); the symmetric underflow flushes to signed zero. */
    if (UNLIKELY(exponent > 308)) {
      if (i != 0) {
        /* Past double's range. The token is well-formed JSON, so keeping the
         * text lets a json.Number or value.Value target hold it; only a
         * float64 target has to fail, and it fails where it is bound. */
        if (UNLIKELY(dirty_end)) return -1;
        if (dom_emit_num_raw(src, p, &tape_p, str_arena_base, str_pp, str_limit) == 0) {
          *tape_pp = tape_p;
          return 0;
        }
        return -1; /* +Inf with the text unretainable: token too long or arena exhausted */
      }
      dv = neg ? -0.0 : 0.0;
    } else if (UNLIKELY(exponent < -342)) {
      dv = neg ? -0.0 : 0.0;
    } else if (!dom_parse_float_64(exponent, i, neg, &dv)) {
      return -1;
    }

  float_done:
    if (UNLIKELY(dirty_end)) return -1;
    /* Store the decimal span in stable str_arena storage. The rare float32
     * binder reparses it out of line at binary32 precision. */
    uint64_t raw;
    if (UNLIKELY(dom_store_num_text(src, p, str_arena_base, str_pp, str_limit, &raw))) return -1;
    *tape_p++ = ((uint64_t)'d' << 56) | raw;
    uint64_t u;
    __builtin_memcpy(&u, &dv, 8);
    *tape_p++ = u;
    *tape_pp  = tape_p;
    return 0;
  }

  /* Integer path. The 19/20-digit boundary is the tightest cut that still
   * lets a uint64 hold the accumulator: 18 digits always fit, 20 digits
   * always overflow uint64, 19 digits straddle int64's 2^63 boundary. */
  size_t longest = neg ? 19 : 20;
  if (UNLIKELY(digit_count > longest)) goto bigint;
  if (UNLIKELY(digit_count == longest)) {
    if (neg) {
      /* Negative 19-digit token: i holds the unsigned magnitude, so
       * INT64_MIN appears as i == 2^63. ~i+1 is the two's-complement
       * negation that yields INT64_MIN without ever forming +2^63 as a
       * signed value (which would be UB). */
      if (i > (uint64_t)INT64_MAX + 1ULL) goto bigint;
      int64_t v = (int64_t)(~i + 1);
      if (UNLIKELY(is_non_delim(*p))) return -1;
      *tape_p++ = ((uint64_t)'l' << 56);
      uint64_t u;
      __builtin_memcpy(&u, &v, 8);
      *tape_p++ = u;
      *tape_pp  = tape_p;
      return 0;
    }
    /* Positive 20-digit token: only those starting with '1' can possibly
     * fit uint64 (max uint64 is 1.8e19). Anything else, or a '1...' that
     * wrapped past 2^64-1 during accumulation (detected as i <= INT64_MAX
     * after wrap-around), must fall back to the bigint -> double path. */
    if (*src != '1' || i <= (uint64_t)INT64_MAX) {
      if (UNLIKELY(is_non_delim(*p))) return -1;
      goto bigint;
    }
  }

  if (i > (uint64_t)INT64_MAX) {
    *tape_p++ = ((uint64_t)'u' << 56);
    *tape_p++ = i;
  } else {
    int64_t v = neg ? (int64_t)(~i + 1) : (int64_t)i;
    *tape_p++ = ((uint64_t)'l' << 56);
    uint64_t u;
    __builtin_memcpy(&u, &v, 8);
    *tape_p++ = u;
  }
  if (UNLIKELY(is_non_delim(*p))) return -1;
  *tape_pp = tape_p;
  return 0;

bigint:
  /* Integer token that exceeds uint64. Widening to double is lossy past 2^53,
   * so the text is kept when there is a str_arena; the cold NOINLINE delegate
   * behind it keeps the EL/finalize/refine machinery out of this translation
   * unit's hot path. */
  {
    if (UNLIKELY(is_non_delim(*p))) return -1;
    if (dom_emit_num_raw(src, p, &tape_p, str_arena_base, str_pp, str_limit) == 0) {
      *tape_pp = tape_p;
      return 0;
    }
    atof_result_f64 r = ndec_parse_bigint_f64((const char *)src, (int)(p - src), atof);
    if (UNLIKELY(r.end == (const char *)src)) return -1;
    /* atof is policy-free: an integer token past 1e308 overflows to
     * +Inf. JSON has no Inf literal, so reject before the tape write.
     * Mirrors the BIND path's __builtin_isfinite guard. */
    if (UNLIKELY(!__builtin_isfinite(r.val))) return -1;

    uint64_t raw;
    if (UNLIKELY(dom_store_num_text(src, p, str_arena_base, str_pp, str_limit, &raw))) return -1;
    *tape_p++ = ((uint64_t)'d' << 56) | raw;
    uint64_t u;
    __builtin_memcpy(&u, &r.val, 8);
    *tape_p++ = u;
    *tape_pp  = tape_p;
    return 0;
  }
}

#endif /* NDEC_NUM_H */
