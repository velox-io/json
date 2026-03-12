// clang-format off

#include "uscale.h"
#include "tables.h"

#ifndef ALWAYS_INLINE
#define ALWAYS_INLINE static __attribute__((always_inline)) inline
#endif

/*
 *  Each entry {hi, lo} represents a 128-bit mantissa pm = hi*2^64 - lo
 *  approximating 10^p, scaled so the high bit of hi is always set.
 *  Total: 696 entries = 11136 bytes.
 **/
typedef struct { uint64_t hi; uint64_t lo; } us_pm_hilo;

#include "z_uscale_table.h"

/*  An "unrounded" uint64_t encodes floor(4*x) | sticky_bit.
 *  Bits [63:2] = integer part, bit 1 = half bit, bit 0 = sticky bit.
 */
typedef uint64_t unrounded;

static inline uint64_t floor(unrounded u) {
    return (u + 0) >> 2;
}

static inline uint64_t round(unrounded u) {
    /* Round half-to-even */
    return (u + 1 + ((u >> 2) & 1)) >> 2;
}

static inline uint64_t ceil(unrounded u) {
    return (u + 3) >> 2;
}

static inline unrounded nudge(unrounded u, int delta) {
    return u + (unrounded)(int64_t)delta;
}

static inline unrounded div(unrounded u, uint64_t d) {
    uint64_t x = u;
    return (x / d) | (u & 1) | (x % d != 0 ? 1 : 0);
}

/* Logarithm approximations */
// floor(x * log10(2))
static inline int us_log10Pow2(int x) {
    return (x * 78913) >> 18;
}
// floor(x * log2(10))
static inline int us_log2Pow10(int x) {
    return (x * 108853) >> 15;
}
// floor(log10(3/4 * 2^e)) = floor(e*log10(2) - log10(4/3))
static inline int us_skewed(int e) {
    return (e * 631305 - 261663) >> 21;
}

/* --- Core: prescale and uscale --- */

typedef __uint128_t us_uint128_t;

typedef struct {
    us_pm_hilo pm;
    int s;
} us_scaler;

static inline us_scaler us_prescale(int e, int p, int lp) {
    us_scaler c;
    c.pm = POW10TAB[p + 348];
    c.s = -(e + lp + 3);
    return c;
}

/*
 * uscale returns unround(x * 2^e * 10^p).
 * The caller passes c = prescale(e, p, log2Pow10(p))
 * and x must be left-justified (high bit set).
 */
static inline unrounded us_uscale(uint64_t x, us_scaler c) {
    us_uint128_t full = (us_uint128_t)x * c.pm.hi;
    uint64_t hi = (uint64_t)(full >> 64);
    uint64_t mid = (uint64_t)full;
    uint64_t sticky = 1;
    if ((hi & (((uint64_t)1 << (c.s & 63)) - 1)) == 0) {
        /* Slow path: check low bits via pm.lo correction */
        uint64_t mid2 = (uint64_t)((us_uint128_t)x * c.pm.lo >> 64);
        sticky = (mid - mid2 > 1) ? 1 : 0;
        hi -= (mid < mid2) ? 1 : 0;
    }
    return (hi >> c.s) | sticky;
}

/* trimZeros (division-free trailing zero removal) */
static inline uint64_t us_rotr64(uint64_t x, int k) {
    return (x >> k) | (x << (64 - k));
}

// Remove trailing decimal zeros from x * 10^p. Returns updated (x, p) via pointers.
static inline void us_trim_zeros(uint64_t *xp, int *pp) {
    uint64_t x = *xp;
    int p = *pp;

    static const uint64_t inv5   = 0xcccccccccccccccdULL;
    static const uint64_t inv5p2 = 0x8f5c28f5c28f5c29ULL;
    static const uint64_t inv5p4 = 0xd288ce703afb7e91ULL;
    static const uint64_t inv5p8 = 0xc767074b22e90e21ULL;
    static const uint64_t max64  = ~(uint64_t)0;

    uint64_t d;

    /* Cut 1 zero, or else return. */
    d = us_rotr64(x * inv5, 1);
    if (d <= max64 / 10) {
        x = d; p += 1;
    } else {
        *xp = x; *pp = p; return;
    }

    /* Cut 8, then 4, then 2, then 1. */
    d = us_rotr64(x * inv5p8, 8);
    if (d <= max64 / 100000000ULL) { x = d; p += 8; }

    d = us_rotr64(x * inv5p4, 4);
    if (d <= max64 / 10000ULL) { x = d; p += 4; }

    d = us_rotr64(x * inv5p2, 2);
    if (d <= max64 / 100ULL) { x = d; p += 2; }

    d = us_rotr64(x * inv5, 1);
    if (d <= max64 / 10ULL) { x = d; p += 1; }

    *xp = x;
    *pp = p;
}

static inline void us_unpack64(double f, uint64_t *m, int *e) {
    uint64_t bits;
    __builtin_memcpy(&bits, &f, 8);
    *m = ((uint64_t)1 << 63) | ((bits & (((uint64_t)1 << 52) - 1)) << 11);
    int exp = (int)((bits >> 52) & 0x7FF);
    if (exp == 0) {
        /* Subnormal: clear implicit bit, normalize */
        *m = (bits & (((uint64_t)1 << 52) - 1)) << 11;
        *e = -1074 - 11;  /* 1 - 1023 - 52 - 11 */
        if (*m != 0) {
            int shift = __builtin_clzll(*m);
            *m <<= shift;
            *e -= shift;
        }
    } else {
        *e = exp - 1023 - 52 - 11;  /* (exp-1) + (1-1023-52-11) */
    }
}

static inline void us_unpack32(float f, uint64_t *m, int *e) {
    us_unpack64((double)f, m, e);
}

/*
 * Compute the shortest decimal representation of f.
 * Returns (d, p) such that f = d * 10^p with minimal digits in d.
 * f must be finite and positive.
 */
static inline void us_short64(double f, uint64_t *d_out, int *p_out) {
    const int minExp = -1085;
    uint64_t m;
    int e;
    us_unpack64(f, &m, &e);

    uint64_t mn; /* min boundary */
    int z = 11;  /* extra zero bits at bottom of m */
    int p;

    if (m == ((uint64_t)1 << 63) && e > minExp) {
        /* Power of two: skewed footprint */
        p = -us_skewed(e + z);
        mn = m - ((uint64_t)1 << (z - 2)); /* m - 1/4 * 2^(e+z) */
    } else {
        if (e < minExp) {
            z = 11 + (minExp - e);
        }
        p = -us_log10Pow2(e + z);
        mn = m - ((uint64_t)1 << (z - 1)); /* m - 1/2 * 2^(e+z) */
    }
    uint64_t mx = m + ((uint64_t)1 << (z - 1)); /* m + 1/2 * 2^(e+z) */
    int odd = (int)(m >> z) & 1;

    us_scaler pre = us_prescale(e, p, us_log2Pow10(p));
    uint64_t dmin = ceil(nudge(us_uscale(mn, pre), +odd));
    uint64_t dmax = floor(nudge(us_uscale(mx, pre), -odd));

    uint64_t d;

    /* Try removing one digit from dmax (prefer ending in 0). */
    d = dmax / 10;
    if (d * 10 >= dmin) {
        int pp = -(p - 1);
        us_trim_zeros(&d, &pp);
        *d_out = d;
        *p_out = pp;
        return;
    }

    /* If range contains multiple values, use correctly rounded. */
    d = dmin;
    if (d < dmax) {
        d = round(us_uscale(m, pre));
    }
    *d_out = d;
    *p_out = -p;
}

/* Formatting helpers */

static inline uint32_t us_decimal_length17(const uint64_t v) {
    if (v >= 10000000000000000ULL) return 17;
    if (v >= 1000000000000000ULL) return 16;
    if (v >= 100000000000000ULL) return 15;
    if (v >= 10000000000000ULL) return 14;
    if (v >= 1000000000000ULL) return 13;
    if (v >= 100000000000ULL) return 12;
    if (v >= 10000000000ULL) return 11;
    if (v >= 1000000000ULL) return 10;
    if (v >= 100000000ULL) return 9;
    if (v >= 10000000ULL) return 8;
    if (v >= 1000000ULL) return 7;
    if (v >= 100000ULL) return 6;
    if (v >= 10000ULL) return 5;
    if (v >= 1000ULL) return 4;
    if (v >= 100ULL) return 3;
    if (v >= 10ULL) return 2;
    return 1;
}

static inline int us_write_mantissa_digits(uint8_t *buf, uint64_t mantissa, uint32_t olength) {
    uint32_t i = olength;
    if (mantissa >> 32 != 0) {
        const uint64_t q = mantissa / 100000000;
        uint32_t low8 = (uint32_t)(mantissa - q * 100000000);
        mantissa = q;
        const uint32_t c = low8 % 10000;
        low8 /= 10000;
        const uint32_t dd = low8 % 10000;
        const uint32_t c0 = (c % 100) << 1;
        const uint32_t c1 = (c / 100) << 1;
        const uint32_t d0 = (dd % 100) << 1;
        const uint32_t d1 = (dd / 100) << 1;
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + c0, 2);
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + c1, 2);
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + d0, 2);
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + d1, 2);
    }
    uint32_t output2 = (uint32_t)mantissa;
    while (output2 >= 10000) {
        const uint32_t c = output2 - 10000 * (output2 / 10000);
        output2 /= 10000;
        const uint32_t c0 = (c % 100) << 1;
        const uint32_t c1 = (c / 100) << 1;
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + c0, 2);
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + c1, 2);
    }
    if (output2 >= 100) {
        const uint32_t c = (output2 % 100) << 1;
        output2 /= 100;
        i -= 2; __builtin_memcpy(buf + i, DIGIT_PAIRS + c, 2);
    }
    if (output2 >= 10) {
        const uint32_t c = output2 << 1;
        buf[1] = DIGIT_PAIRS[c + 1];
        buf[0] = DIGIT_PAIRS[c];
    } else {
        buf[0] = (char)('0' + output2);
    }
    return (int)olength;
}

ALWAYS_INLINE int us_format_fixed(uint8_t *buf, uint64_t mantissa, int32_t exponent, int sign) {
    int idx = 0;
    if (sign) buf[idx++] = '-';
    if (mantissa == 0) { buf[idx++] = '0'; return idx; }
    while (mantissa % 10 == 0) { mantissa /= 10; exponent++; }
    uint32_t olength = us_decimal_length17(mantissa);

    if (exponent >= 0) {
        us_write_mantissa_digits(buf + idx, mantissa, olength);
        idx += olength;
        for (int32_t i = 0; i < exponent; i++) buf[idx++] = '0';
    } else {
        int32_t absExp = -exponent;
        if ((int32_t)olength <= absExp) {
            buf[idx++] = '0'; buf[idx++] = '.';
            int32_t leadingZeros = absExp - (int32_t)olength;
            for (int32_t i = 0; i < leadingZeros; i++) buf[idx++] = '0';
            us_write_mantissa_digits(buf + idx, mantissa, olength);
            idx += olength;
        } else {
            int32_t intDigits = (int32_t)olength - absExp;
            us_write_mantissa_digits(buf + idx, mantissa, olength);
            for (int32_t i = olength - 1; i >= intDigits; i--)
                buf[idx + i + 1] = buf[idx + i];
            buf[idx + intDigits] = '.';
            idx += olength + 1;
        }
    }
    return idx;
}

/*
 * us_format_exp — scientific notation output (e.g. "1.7976931348623157e+308")
 *
 * Given mantissa d and exponent p where value = d * 10^p,
 * output: sign + d[0] + '.' + d[1:] + 'e' + exp_sign + exp_digits
 *
 * If d has only 1 digit, omit the decimal point (e.g. "1e+21").
 * Exponent never has leading zeros (e.g. "e-9" not "e-09").
 */
ALWAYS_INLINE int us_format_exp(uint8_t *buf, uint64_t mantissa, int32_t exponent, int sign) {
    int idx = 0;
    if (sign) buf[idx++] = '-';
    if (mantissa == 0) { buf[idx++] = '0'; return idx; }
    while (mantissa % 10 == 0) { mantissa /= 10; exponent++; }
    uint32_t olength = us_decimal_length17(mantissa);

    /* Scientific exponent: value = (mantissa / 10^(n-1)) * 10^(exponent+n-1) */
    int32_t sciExp = exponent + (int32_t)olength - 1;

    /* Write mantissa digits */
    us_write_mantissa_digits(buf + idx, mantissa, olength);

    if (olength > 1) {
        /* Insert decimal point after first digit: shift digits[1:] right by 1 */
        for (int32_t i = olength - 1; i >= 1; i--)
            buf[idx + i + 1] = buf[idx + i];
        buf[idx + 1] = '.';
        idx += olength + 1;
    } else {
        idx += 1;
    }

    /* Write 'e' + sign + exponent digits */
    buf[idx++] = 'e';
    if (sciExp < 0) {
        buf[idx++] = '-';
        sciExp = -sciExp;
    } else {
        buf[idx++] = '+';
    }

    /* Write exponent without leading zeros */
    if (sciExp >= 100) {
        uint32_t q = (uint32_t)sciExp / 100;
        buf[idx++] = (uint8_t)('0' + q);
        uint32_t r = (uint32_t)sciExp - q * 100;
        uint32_t d0 = r << 1;
        __builtin_memcpy(buf + idx, DIGIT_PAIRS + d0, 2);
        idx += 2;
    } else if (sciExp >= 10) {
        uint32_t d0 = (uint32_t)sciExp << 1;
        __builtin_memcpy(buf + idx, DIGIT_PAIRS + d0, 2);
        idx += 2;
    } else {
        buf[idx++] = (uint8_t)('0' + sciExp);
    }

    return idx;
}

/* Format flags for us_write_float* */

int us_write_float64(uint8_t *buf, double value, int flags) {
    uint64_t bits;
    __builtin_memcpy(&bits, &value, 8);
    const int sign = (bits >> 63) != 0;
    const uint64_t ieeeMantissa = bits & (((uint64_t)1 << 52) - 1);
    const uint32_t ieeeExponent = (uint32_t)((bits >> 52) & 0x7FF);

    if (ieeeExponent == 0 && ieeeMantissa == 0) {
        if (sign) { buf[0] = '-'; buf[1] = '0'; return 2; }
        buf[0] = '0'; return 1;
    }

    /* Small integer fast path */
    if (ieeeExponent != 0) {
        uint64_t m2 = ((uint64_t)1 << 52) | ieeeMantissa;
        int32_t e2 = (int32_t)ieeeExponent - 1023 - 52;
        if (e2 <= 0 && e2 >= -52) {
            uint64_t mask = ((uint64_t)1 << -e2) - 1;
            if ((m2 & mask) == 0) {
                uint64_t mantissa = m2 >> -e2;
                int32_t exponent = 0;
                /* Strip trailing zeros */
                while (mantissa != 0) {
                    uint64_t q = mantissa / 10;
                    uint32_t r = (uint32_t)(mantissa - 10 * q);
                    if (r != 0) break;
                    mantissa = q;
                    exponent++;
                }
                return us_format_fixed(buf, mantissa, exponent, sign);
            }
        }
    }

    uint64_t d;
    int p;
    if (sign) {
        us_short64(-value, &d, &p);
    } else {
        us_short64(value, &d, &p);
    }

    /* Format selection */
    if (flags & US_FMT_EXP_AUTO) {
        /* EXP_AUTO: switch to 'e' when abs < 1e-6 || abs >= 1e21 */
        double abs_val = sign ? -value : value;
        if (abs_val < 1e-6 || abs_val >= 1e21) {
            return us_format_exp(buf, d, (int32_t)p, sign);
        }
    }
    return us_format_fixed(buf, d, (int32_t)p, sign);
}

int us_write_float32(uint8_t *buf, float value, int flags) {
    uint32_t bits;
    __builtin_memcpy(&bits, &value, 4);
    const int sign = (bits >> 31) != 0;
    const uint32_t ieeeMantissa = bits & ((1u << 23) - 1);
    const uint32_t ieeeExponent = (bits >> 23) & 0xFF;

    if (ieeeExponent == 0 && ieeeMantissa == 0) {
        if (sign) { buf[0] = '-'; buf[1] = '0'; return 2; }
        buf[0] = '0'; return 1;
    }

    /* Promote to double and use the Short algorithm with float32 ULP width.
     * The key is that z (extra zero bits) reflects the float32 precision:
     *   Normal float32: 24 significant bits -> z = 64 - 24 = 40
     *   Subnormal float32: fewer bits -> z = 64 - significant_bits
     * After us_unpack64, m is left-justified in 64 bits. */
    double dval = sign ? (double)(-value) : (double)value;
    if (dval < 0) dval = -dval;  /* ensure positive for short */

    uint64_t m;
    int e;
    us_unpack64(dval, &m, &e);

    /* Compute z from the float32 mantissa precision.
     * Normal: 24 bits (23 explicit + 1 implicit) -> z = 40
     * Subnormal: count significant bits of ieeeMantissa -> z = 64 - sigbits */
    int sigbits;
    if (ieeeExponent != 0) {
        sigbits = 24;  /* normal: 23 + implicit 1 */
    } else {
        /* Subnormal: significant bits = bit length of ieeeMantissa */
        sigbits = 32 - __builtin_clz(ieeeMantissa);
    }
    int z = 64 - sigbits;

    const int minExp = -1085;
    uint64_t mn;
    int p2;

    /* Power-of-two path: only for normal float32 values where the mantissa
     * is exactly the implicit bit (ieeeMantissa == 0, ieeeExponent > 0).
     * At these points, the ULP below is half the ULP above, creating an
     * asymmetric (skewed) boundary. For subnormals, ULP is uniform. */
    if (m == ((uint64_t)1 << 63) && ieeeExponent > 0 && ieeeMantissa == 0
        && ieeeExponent > 1) {
        p2 = -us_skewed(e + z);
        mn = m - ((uint64_t)1 << (z - 2));
    } else {
        if (e < minExp) {
            z = (64 - sigbits) + (minExp - e);
        }
        p2 = -us_log10Pow2(e + z);
        mn = m - ((uint64_t)1 << (z - 1));
    }
    uint64_t mx = m + ((uint64_t)1 << (z - 1));
    int odd = (int)(m >> z) & 1;

    us_scaler pre = us_prescale(e, p2, us_log2Pow10(p2));
    uint64_t dmin = ceil(nudge(us_uscale(mn, pre), +odd));
    uint64_t dmax = floor(nudge(us_uscale(mx, pre), -odd));

    uint64_t d;
    int pp;
    d = dmax / 10;
    if (d * 10 >= dmin) {
        pp = -(p2 - 1);
        us_trim_zeros(&d, &pp);
    } else {
        d = dmin;
        if (d < dmax) {
            d = round(us_uscale(m, pre));
        }
        pp = -p2;
    }

    /* Format selection */
    if (flags & US_FMT_EXP_AUTO) {
        /* EXP_AUTO: switch to 'e' when float32(abs) < 1e-6 || float32(abs) >= 1e21 */
        float abs_val = sign ? -value : value;
        if (abs_val < 1e-6f || abs_val >= 1e21f) {
            return us_format_exp(buf, d, (int32_t)pp, sign);
        }
    }
    return us_format_fixed(buf, d, (int32_t)pp, sign);
}


