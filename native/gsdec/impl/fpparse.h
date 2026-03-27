/*
 * fpparse.h -- Three-tier float64 parsing for gsdec (no libc).
 *
 * Tier 1: Exact pow10 (<=15 digits, |power10| <= 22)
 * Tier 2: Eisel-Lemire algorithm (<=19 digits)
 * Tier 3: uscale/fpparse fallback
 *
 * All functions are static inline. No external dependencies.
 */

#ifndef GSDEC_FPPARSE_H
#define GSDEC_FPPARSE_H

#include <stdint.h>

/* IEEE 754 double bit manipulation (no math.h needed) */
static inline double fp_bits_to_double(uint64_t bits) {
    double d;
    unsigned char *dp = (unsigned char *)&d;
    unsigned char *bp = (unsigned char *)&bits;
    for (int i = 0; i < 8; i++) dp[i] = bp[i];
    return d;
}

static inline uint64_t fp_double_to_bits(double d) {
    uint64_t bits;
    unsigned char *bp = (unsigned char *)&bits;
    unsigned char *dp = (unsigned char *)&d;
    for (int i = 0; i < 8; i++) bp[i] = dp[i];
    return bits;
}

/* 128-bit multiply helper */
static inline void mul64(uint64_t a, uint64_t b, uint64_t *hi, uint64_t *lo) {
#if defined(__SIZEOF_INT128__)
    __uint128_t r = (__uint128_t)a * b;
    *hi = (uint64_t)(r >> 64);
    *lo = (uint64_t)r;
#else
    uint64_t a_lo = (uint32_t)a, a_hi = a >> 32;
    uint64_t b_lo = (uint32_t)b, b_hi = b >> 32;
    uint64_t p0 = a_lo * b_lo;
    uint64_t p1 = a_lo * b_hi;
    uint64_t p2 = a_hi * b_lo;
    uint64_t p3 = a_hi * b_hi;
    uint64_t mid = p1 + (p0 >> 32);
    uint64_t carry = (mid < p1) ? 1 : 0;
    mid += p2;
    carry += (mid < p2) ? 1 : 0;
    *hi = p3 + (mid >> 32) + (carry << 32);
    *lo = (mid << 32) | (uint32_t)p0;
#endif
}

static inline int fp_log2_pow10(int x) {
    return (x * 108853) >> 15;
}

static inline int fp_bits_len64(uint64_t x) {
    return x == 0 ? 0 : 64 - __builtin_clzll(x);
}

static inline int fp_min_int(int a, int b) { return a < b ? a : b; }

/* Tables are in separate include files to keep this header readable */
#include "fpparse_tables.h"

/* ============================================================
 *  Scan float from JSON number bytes
 * ============================================================ */

static inline void scan_float_parts(const uint8_t *src, uint32_t start, uint32_t end,
                                     uint64_t *out_mantissa, int *out_power10,
                                     int *out_digit_count, int *out_negative) {
    uint32_t i = start;
    int negative = 0;
    if (i < end && src[i] == '-') { negative = 1; i++; }
    *out_negative = negative;

    uint64_t mantissa = 0;
    int digit_count = 0;
    int frac_start = -1;

    /* Integer digits */
    if (i < end && src[i] == '0') {
        i++;
    } else {
        while (i < end && src[i] >= '0' && src[i] <= '9') {
            if (digit_count < 19) {
                mantissa = mantissa * 10 + (src[i] - '0');
            }
            digit_count++;
            i++;
        }
    }

    /* Fractional digits */
    if (i < end && src[i] == '.') {
        i++;
        frac_start = digit_count;
        if (digit_count == 0) {
            while (i < end && src[i] == '0') {
                digit_count++;
                i++;
            }
        }
        while (i < end && src[i] >= '0' && src[i] <= '9') {
            if (digit_count < 19) {
                mantissa = mantissa * 10 + (src[i] - '0');
            }
            digit_count++;
            i++;
        }
    }

    /* Exponent */
    int exponent = 0;
    int exp_overflow = 0;
    if (i < end && (src[i] == 'e' || src[i] == 'E')) {
        i++;
        int exp_neg = 0;
        if (i < end && (src[i] == '+' || src[i] == '-')) {
            exp_neg = (src[i] == '-');
            i++;
        }
        while (i < end && src[i] >= '0' && src[i] <= '9') {
            exponent = exponent * 10 + (src[i] - '0');
            if (exponent > 400) {
                exp_overflow = 1;
                while (++i < end && src[i] >= '0' && src[i] <= '9') {}
                break;
            }
            i++;
        }
        if (exp_neg) exponent = -exponent;
    }

    int power10 = exponent;
    if (frac_start >= 0) {
        power10 -= (digit_count - frac_start);
    }
    /* For tier 3: if digit_count > 19, mantissa only holds 19 digits */
    (void)exp_overflow;

    *out_mantissa = mantissa;
    *out_power10 = power10;
    *out_digit_count = digit_count;
}

/* ============================================================
 *  Tier 1: Exact pow10
 * ============================================================ */

static inline int parse_float64_tier1(uint64_t mantissa, int power10, int digit_count,
                                       int negative, double *result) {
    if (digit_count > 15 || power10 < -22 || power10 > 22) return 0;
    double f = (double)mantissa;
    if (power10 >= 0) {
        f *= pow10f64[power10];
    } else {
        f /= pow10f64[-power10];
    }
    if (negative) f = -f;
    *result = f;
    return 1;
}

/* ============================================================
 *  Tier 2: Eisel-Lemire
 * ============================================================ */

static inline int eisel_lemire(uint64_t mantissa, int power10, uint64_t *result_bits) {
    if (power10 < EL_POW10_MIN || power10 > EL_POW10_MAX) return 0;

    const uint64_t *power = el_pow10_tab[power10 - EL_POW10_MIN];

    /* Binary exponent estimate */
    int binary_exp = 1 + ((power10 * 108853) >> 15);

    /* Normalize mantissa */
    int leading_zeros = __builtin_clzll(mantissa);
    mantissa <<= leading_zeros;
    uint64_t result_exp = (uint64_t)(binary_exp + 63 - (-1023)) - (uint64_t)leading_zeros;

    /* 128-bit multiply */
    uint64_t prod_hi, prod_lo;
    mul64(mantissa, power[0], &prod_hi, &prod_lo);

    /* Refinement check */
    if ((prod_hi & 0x1FF) == 0x1FF && prod_lo + mantissa < mantissa) {
        uint64_t cross_hi, cross_lo;
        mul64(mantissa, power[1], &cross_hi, &cross_lo);
        uint64_t refined_lo = prod_lo + cross_hi;
        if (refined_lo < prod_lo) prod_hi++;
        if ((prod_hi & 0x1FF) == 0x1FF && refined_lo + 1 == 0 && cross_lo + mantissa < mantissa) {
            return 0;
        }
        prod_lo = refined_lo;
    }

    /* Extract 54-bit mantissa */
    uint64_t top_bit = prod_hi >> 63;
    uint64_t result_mantissa = prod_hi >> (top_bit + 9);
    result_exp -= 1 ^ top_bit;

    /* Ambiguous halfway */
    if (prod_lo == 0 && (prod_hi & 0x1FF) == 0 && (result_mantissa & 3) == 1) {
        return 0;
    }

    /* Round to nearest even */
    result_mantissa += result_mantissa & 1;
    result_mantissa >>= 1;
    if (result_mantissa >> 53 > 0) {
        result_mantissa >>= 1;
        result_exp++;
    }

    /* Overflow or underflow */
    if (result_exp - 1 >= 0x7FF - 1) return 0;

    *result_bits = result_exp << 52 | (result_mantissa & ((1ULL << 52) - 1));
    return 1;
}

/* ============================================================
 *  Tier 3: uscale (fpparse)
 * ============================================================ */

static inline double fpparse_uscale(uint64_t d, int p) {
    if (d == 0) return 0.0;

    if (p < US_POW10_MIN) return 0.0;
    if (p > US_POW10_MAX) {
        return fp_bits_to_double(0x7FF0000000000000ULL); /* +Inf */
    }

    int b = fp_bits_len64(d);
    int lp = fp_log2_pow10(p);
    int e = fp_min_int(1074, 53 - b - lp);

    int s = -((e - (64 - b)) + lp + 3);
    uint64_t pm_hi = us_pow10_tab[p - US_POW10_MIN][0];
    uint64_t pm_lo = us_pow10_tab[p - US_POW10_MIN][1];

    uint64_t x = d << (64 - b);

    uint64_t hi, mid;
    mul64(x, pm_hi, &hi, &mid);
    uint64_t sticky = 1;
    uint64_t s_mask = ((uint64_t)1 << (s & 63)) - 1;
    if ((hi & s_mask) == 0) {
        uint64_t mid2, dummy;
        mul64(x, pm_lo, &mid2, &dummy);
        sticky = (mid - mid2 > 1) ? 1 : 0;
        if (mid < mid2) hi--;
    }
    uint64_t u = (hi >> s) | sticky;

    /* Branch-free normalization */
    uint64_t unmin_threshold = (1ULL << 55) - 2;
    int shift = (u >= unmin_threshold) ? 1 : 0;
    u = (u >> shift) | (u & 1);
    e = e - shift;

    /* round() */
    uint64_t m = (u + 1 + ((u >> 2) & 1)) >> 2;

    /* pack64 */
    if ((m & (1ULL << 52)) == 0) {
        return fp_bits_to_double(m);
    }
    return fp_bits_to_double((m & ~(1ULL << 52)) | ((uint64_t)(1075 + (-e)) << 52));
}

/* ============================================================
 *  Top-level dispatcher
 * ============================================================ */

static inline double parse_float64(const uint8_t *src, uint32_t start, uint32_t end) {
    uint64_t mantissa;
    int power10, digit_count, negative;
    scan_float_parts(src, start, end, &mantissa, &power10, &digit_count, &negative);

    /* Tier 1 */
    double result;
    if (parse_float64_tier1(mantissa, power10, digit_count, negative, &result)) {
        return result;
    }

    /* Zero */
    if (mantissa == 0) {
        if (negative) return fp_bits_to_double(1ULL << 63);
        return 0.0;
    }

    /* Tier 2: Eisel-Lemire */
    if (digit_count <= 19) {
        uint64_t bits;
        if (eisel_lemire(mantissa, power10, &bits)) {
            if (negative) bits |= 1ULL << 63;
            return fp_bits_to_double(bits);
        }
    }

    /* Tier 3: uscale */
    int p3_power10 = power10;
    if (digit_count > 19) {
        p3_power10 += digit_count - 19;
    }
    double f = fpparse_uscale(mantissa, p3_power10);
    if (negative) f = -f;
    return f;
}

#endif /* GSDEC_FPPARSE_H */
