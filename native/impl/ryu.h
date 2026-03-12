/*
 * ryu.h — Ryu float-to-string conversion for Velox JSON encoder
 *
 * Self-contained implementation of the Ryu algorithm (Ulf Adams, 2018)
 * adapted for fixed-point ('f' format) output matching Go's
 * strconv.AppendFloat(buf, f, 'f', -1, bitSize).
 *
 * Based on the reference C implementation:
 *   https://github.com/ulfjack/ryu
 *   Copyright 2018 Ulf Adams
 *   Licensed under Apache License 2.0 / Boost Software License 1.0
 *
 * Modifications for Velox JSON:
 *   - Merged all headers into a single self-contained file
 *   - Removed libc dependencies (no assert, no malloc, no stdio)
 *   - Output format: fixed-point decimal (never scientific notation)
 *   - Matches Go's strconv.AppendFloat(buf, f, 'f', -1, bitSize) exactly
 *   - All functions are static inline — no external symbols
 *
 * Design constraints:
 *   - No heap allocation
 *   - No libc calls (no snprintf, no printf, no assert)
 *   - Compiles with -O3 -fPIC, no external linker dependencies
 *   - Uses __uint128_t (available on GCC/Clang for aarch64 and x86_64)
 *   - Lookup tables are static const (read-only, PIC-safe)
 */

#ifndef VJ_RYU_H
#define VJ_RYU_H

#include <stdint.h>

/* ================================================================
 *  Section 1 — IEEE 754 constants and bit manipulation
 * ================================================================ */

#define VJ_FLOAT_MANTISSA_BITS 23
#define VJ_FLOAT_EXPONENT_BITS 8
#define VJ_FLOAT_BIAS 127

#define VJ_DOUBLE_MANTISSA_BITS 52
#define VJ_DOUBLE_EXPONENT_BITS 11
#define VJ_DOUBLE_BIAS 1023

static inline uint32_t vj_float_to_bits(const float f) {
  uint32_t bits;
  __builtin_memcpy(&bits, &f, sizeof(float));
  return bits;
}

static inline uint64_t vj_double_to_bits(const double d) {
  uint64_t bits;
  __builtin_memcpy(&bits, &d, sizeof(double));
  return bits;
}

/* ================================================================
 *  Section 2 — Common helper functions
 * ================================================================ */

/* Returns ceil(log_2(5^e)); requires 0 <= e <= 3528. */
static inline int32_t vj_pow5bits(const int32_t e) {
  return (int32_t)(((((uint32_t)e) * 1217359) >> 19) + 1);
}

/* Returns floor(log_10(2^e)); requires 0 <= e <= 1650. */
static inline uint32_t vj_log10Pow2(const int32_t e) {
  return (((uint32_t)e) * 78913) >> 18;
}

/* Returns floor(log_10(5^e)); requires 0 <= e <= 2620. */
static inline uint32_t vj_log10Pow5(const int32_t e) {
  return (((uint32_t)e) * 732923) >> 20;
}

/* Number of decimal digits in a 9-digit value. */
static inline uint32_t vj_decimalLength9(const uint32_t v) {
  if (v >= 100000000) return 9;
  if (v >= 10000000) return 8;
  if (v >= 1000000) return 7;
  if (v >= 100000) return 6;
  if (v >= 10000) return 5;
  if (v >= 1000) return 4;
  if (v >= 100) return 3;
  if (v >= 10) return 2;
  return 1;
}

/* Number of decimal digits in a 17-digit value. */
static inline uint32_t vj_decimalLength17(const uint64_t v) {
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

/* Digit pair table for fast two-digit conversion. */
static const char VJ_DIGIT_TABLE[200] = {
    '0', '0', '0', '1', '0', '2', '0', '3', '0', '4', '0', '5', '0', '6',
    '0', '7', '0', '8', '0', '9', '1', '0', '1', '1', '1', '2', '1', '3',
    '1', '4', '1', '5', '1', '6', '1', '7', '1', '8', '1', '9', '2', '0',
    '2', '1', '2', '2', '2', '3', '2', '4', '2', '5', '2', '6', '2', '7',
    '2', '8', '2', '9', '3', '0', '3', '1', '3', '2', '3', '3', '3', '4',
    '3', '5', '3', '6', '3', '7', '3', '8', '3', '9', '4', '0', '4', '1',
    '4', '2', '4', '3', '4', '4', '4', '5', '4', '6', '4', '7', '4', '8',
    '4', '9', '5', '0', '5', '1', '5', '2', '5', '3', '5', '4', '5', '5',
    '5', '6', '5', '7', '5', '8', '5', '9', '6', '0', '6', '1', '6', '2',
    '6', '3', '6', '4', '6', '5', '6', '6', '6', '7', '6', '8', '6', '9',
    '7', '0', '7', '1', '7', '2', '7', '3', '7', '4', '7', '5', '7', '6',
    '7', '7', '7', '8', '7', '9', '8', '0', '8', '1', '8', '2', '8', '3',
    '8', '4', '8', '5', '8', '6', '8', '7', '8', '8', '8', '9', '9', '0',
    '9', '1', '9', '2', '9', '3', '9', '4', '9', '5', '9', '6', '9', '7',
    '9', '8', '9', '9'};

/* ================================================================
 *  Section 3 — 128-bit arithmetic helpers
 *
 *  Uses __uint128_t (GCC/Clang on 64-bit platforms).
 * ================================================================ */

typedef __uint128_t vj_uint128_t;

/* 64x64 -> 128 bit multiply, return high 64 bits. */
static inline uint64_t vj_umul128_hi(const uint64_t a, const uint64_t b) {
  return (uint64_t)(((vj_uint128_t)a * b) >> 64);
}

/* (hi*2^64 + lo) >> dist, with 0 < dist < 64. */
static inline uint64_t vj_shiftright128(const uint64_t lo, const uint64_t hi,
                                         const uint32_t dist) {
  return (hi << (64 - dist)) | (lo >> dist);
}

/* 64x128 -> high 64 bits, shifted right. */
static inline uint64_t vj_mulShift64(const uint64_t m,
                                      const uint64_t *const mul,
                                      const int32_t j) {
  const vj_uint128_t b0 = ((vj_uint128_t)m) * mul[0];
  const vj_uint128_t b2 = ((vj_uint128_t)m) * mul[1];
  return (uint64_t)(((b0 >> 64) + b2) >> (j - 64));
}

static inline uint64_t vj_mulShiftAll64(const uint64_t m,
                                         const uint64_t *const mul,
                                         const int32_t j, uint64_t *const vp,
                                         uint64_t *const vm,
                                         const uint32_t mmShift) {
  *vp = vj_mulShift64(4 * m + 2, mul, j);
  *vm = vj_mulShift64(4 * m - 1 - mmShift, mul, j);
  return vj_mulShift64(4 * m, mul, j);
}

/* 32x64 -> result >> shift. For float32 Ryu. */
static inline uint32_t vj_mulShift32(const uint32_t m, const uint64_t factor,
                                      const int32_t shift) {
  const uint64_t bits0 = (uint64_t)m * (uint32_t)(factor);
  const uint64_t bits1 = (uint64_t)m * (uint32_t)(factor >> 32);
  const uint64_t sum = (bits0 >> 32) + bits1;
  return (uint32_t)(sum >> (shift - 32));
}

/* ================================================================
 *  Section 4 — Lookup tables (small table variant)
 *
 *  These tables are from Ulf Adams' reference implementation.
 *  Total: ~6 KB in __TEXT,__const.
 * ================================================================ */

#define VJ_DOUBLE_POW5_INV_BITCOUNT 125
#define VJ_DOUBLE_POW5_BITCOUNT 125

static const uint64_t VJ_DOUBLE_POW5_INV_SPLIT2[15][2] = {
    {1u, 2305843009213693952u},
    {5955668970331000884u, 1784059615882449851u},
    {8982663654677661702u, 1380349269358112757u},
    {7286864317269821294u, 2135987035920910082u},
    {7005857020398200553u, 1652639921975621497u},
    {17965325103354776697u, 1278668206209430417u},
    {8928596168509315048u, 1978643211784836272u},
    {10075671573058298858u, 1530901034580419511u},
    {597001226353042382u, 1184477304306571148u},
    {1527430471115325346u, 1832889850782397517u},
    {12533209867169019542u, 1418129833677084982u},
    {5577825024675947042u, 2194449627517475473u},
    {11006974540203867551u, 1697873161311732311u},
    {10313493231639821582u, 1313665730009899186u},
    {12701016819766672773u, 2032799256770390445u},
};

static const uint32_t VJ_POW5_INV_OFFSETS[22] = {
    0x54544554, 0x04055545, 0x10041000, 0x00400414, 0x40010000, 0x41155555,
    0x00000454, 0x00010044, 0x40000000, 0x44000041, 0x50454450, 0x55550054,
    0x51655554, 0x40004000, 0x01000001, 0x00010500, 0x51515411, 0x05555554,
    0x50411500, 0x40040000, 0x05040110, 0x00000000,
};

static const uint64_t VJ_DOUBLE_POW5_SPLIT2[13][2] = {
    {0u, 1152921504606846976u},
    {0u, 1490116119384765625u},
    {1032610780636961552u, 1925929944387235853u},
    {7910200175544436838u, 1244603055572228341u},
    {16941905809032713930u, 1608611746708759036u},
    {13024893955298202172u, 2079081953128979843u},
    {6607496772837067824u, 1343575221513417750u},
    {17332926989895652603u, 1736530273035216783u},
    {13037379183483547984u, 2244412773384604712u},
    {1605989338741628675u, 1450417759929778918u},
    {9630225068416591280u, 1874621017369538693u},
    {665883850346957067u, 1211445438634777304u},
    {14931890668723713708u, 1565756531257009982u},
};

static const uint32_t VJ_POW5_OFFSETS[21] = {
    0x00000000, 0x00000000, 0x00000000, 0x00000000, 0x40000000,
    0x59695995, 0x55545555, 0x56555515, 0x41150504, 0x40555410,
    0x44555145, 0x44504540, 0x45555550, 0x40004000, 0x96440440,
    0x55565565, 0x54454045, 0x40154151, 0x55559155, 0x51405555,
    0x00000105,
};

#define VJ_POW5_TABLE_SIZE 26

static const uint64_t VJ_DOUBLE_POW5_TABLE[VJ_POW5_TABLE_SIZE] = {
    1ull,
    5ull,
    25ull,
    125ull,
    625ull,
    3125ull,
    15625ull,
    78125ull,
    390625ull,
    1953125ull,
    9765625ull,
    48828125ull,
    244140625ull,
    1220703125ull,
    6103515625ull,
    30517578125ull,
    152587890625ull,
    762939453125ull,
    3814697265625ull,
    19073486328125ull,
    95367431640625ull,
    476837158203125ull,
    2384185791015625ull,
    11920928955078125ull,
    59604644775390625ull,
    298023223876953125ull,
};

/* ================================================================
 *  Section 5 — Power-of-5 computation (small table variant)
 * ================================================================ */

/* Compute 5^i as a 128-bit value for Ryu. */
static inline void vj_double_computePow5(const uint32_t i,
                                          uint64_t *const result) {
  const uint32_t base = i / VJ_POW5_TABLE_SIZE;
  const uint32_t base2 = base * VJ_POW5_TABLE_SIZE;
  const uint32_t offset = i - base2;
  const uint64_t *const mul = VJ_DOUBLE_POW5_SPLIT2[base];
  if (offset == 0) {
    result[0] = mul[0];
    result[1] = mul[1];
    return;
  }
  const uint64_t m = VJ_DOUBLE_POW5_TABLE[offset];
  const vj_uint128_t b0 = ((vj_uint128_t)m) * mul[0];
  const vj_uint128_t b2 = ((vj_uint128_t)m) * mul[1];
  const uint32_t delta = vj_pow5bits(i) - vj_pow5bits(base2);
  const vj_uint128_t shiftedSum =
      (b0 >> delta) + (b2 << (64 - delta)) +
      ((VJ_POW5_OFFSETS[i / 16] >> ((i % 16) << 1)) & 3);
  result[0] = (uint64_t)shiftedSum;
  result[1] = (uint64_t)(shiftedSum >> 64);
}

/* Compute 5^(-i) as a 128-bit value for Ryu. */
static inline void vj_double_computeInvPow5(const uint32_t i,
                                              uint64_t *const result) {
  const uint32_t base = (i + VJ_POW5_TABLE_SIZE - 1) / VJ_POW5_TABLE_SIZE;
  const uint32_t base2 = base * VJ_POW5_TABLE_SIZE;
  const uint32_t offset = base2 - i;
  const uint64_t *const mul = VJ_DOUBLE_POW5_INV_SPLIT2[base];
  if (offset == 0) {
    result[0] = mul[0];
    result[1] = mul[1];
    return;
  }
  const uint64_t m = VJ_DOUBLE_POW5_TABLE[offset];
  const vj_uint128_t b0 = ((vj_uint128_t)m) * (mul[0] - 1);
  const vj_uint128_t b2 = ((vj_uint128_t)m) * mul[1];
  const uint32_t delta = vj_pow5bits(base2) - vj_pow5bits(i);
  const vj_uint128_t shiftedSum =
      ((b0 >> delta) + (b2 << (64 - delta))) + 1 +
      ((VJ_POW5_INV_OFFSETS[i / 16] >> ((i % 16) << 1)) & 3);
  result[0] = (uint64_t)shiftedSum;
  result[1] = (uint64_t)(shiftedSum >> 64);
}

/* ================================================================
 *  Section 6 — Divisibility helpers
 * ================================================================ */

static inline uint64_t vj_div5(const uint64_t x) { return x / 5; }
static inline uint64_t vj_div10(const uint64_t x) { return x / 10; }
static inline uint64_t vj_div100(const uint64_t x) { return x / 100; }

static inline uint32_t vj_pow5Factor(uint64_t value) {
  const uint64_t m_inv_5 = 14757395258967641293u;
  const uint64_t n_div_5 = 3689348814741910323u;
  uint32_t count = 0;
  for (;;) {
    value *= m_inv_5;
    if (value > n_div_5)
      break;
    ++count;
  }
  return count;
}

static inline int vj_multipleOfPowerOf5(const uint64_t value,
                                         const uint32_t p) {
  return vj_pow5Factor(value) >= p;
}

static inline int vj_multipleOfPowerOf2(const uint64_t value,
                                         const uint32_t p) {
  return (value & ((1ull << p) - 1)) == 0;
}

/* Float32 helpers. */
static inline uint32_t vj_pow5factor_32(uint32_t value) {
  uint32_t count = 0;
  for (;;) {
    const uint32_t q = value / 5;
    const uint32_t r = value % 5;
    if (r != 0)
      break;
    value = q;
    ++count;
  }
  return count;
}

static inline int vj_multipleOfPowerOf5_32(const uint32_t value,
                                            const uint32_t p) {
  return vj_pow5factor_32(value) >= p;
}

static inline int vj_multipleOfPowerOf2_32(const uint32_t value,
                                            const uint32_t p) {
  return (value & ((1u << p) - 1)) == 0;
}

/* Float32 multiplication helpers using the double-precision tables. */
#define VJ_FLOAT_POW5_INV_BITCOUNT (VJ_DOUBLE_POW5_INV_BITCOUNT - 64)
#define VJ_FLOAT_POW5_BITCOUNT (VJ_DOUBLE_POW5_BITCOUNT - 64)

static inline uint32_t vj_mulPow5InvDivPow2(const uint32_t m,
                                              const uint32_t q,
                                              const int32_t j) {
  uint64_t pow5[2];
  vj_double_computeInvPow5(q, pow5);
  return vj_mulShift32(m, pow5[1] + 1, j);
}

static inline uint32_t vj_mulPow5divPow2(const uint32_t m, const uint32_t i,
                                           const int32_t j) {
  uint64_t pow5[2];
  vj_double_computePow5(i, pow5);
  return vj_mulShift32(m, pow5[1], j);
}

/* ================================================================
 *  Section 7 — Core Ryu: float32 -> decimal (f2d)
 * ================================================================ */

typedef struct {
  uint32_t mantissa;
  int32_t exponent;
} vj_floating_decimal_32;

static inline vj_floating_decimal_32 vj_f2d(const uint32_t ieeeMantissa,
                                              const uint32_t ieeeExponent) {
  int32_t e2;
  uint32_t m2;

  if (ieeeExponent == 0) {
    e2 = 1 - VJ_FLOAT_BIAS - VJ_FLOAT_MANTISSA_BITS - 2;
    m2 = ieeeMantissa;
  } else {
    e2 = (int32_t)ieeeExponent - VJ_FLOAT_BIAS - VJ_FLOAT_MANTISSA_BITS - 2;
    m2 = (1u << VJ_FLOAT_MANTISSA_BITS) | ieeeMantissa;
  }

  const int even = (m2 & 1) == 0;
  const int acceptBounds = even;

  const uint32_t mv = 4 * m2;
  const uint32_t mp = 4 * m2 + 2;
  const uint32_t mmShift = ieeeMantissa != 0 || ieeeExponent <= 1;
  const uint32_t mm = 4 * m2 - 1 - mmShift;

  uint32_t vr, vp, vm;
  int32_t e10;
  int vmIsTrailingZeros = 0;
  int vrIsTrailingZeros = 0;
  uint8_t lastRemovedDigit = 0;

  if (e2 >= 0) {
    const uint32_t q = vj_log10Pow2(e2);
    e10 = (int32_t)q;
    const int32_t k =
        VJ_FLOAT_POW5_INV_BITCOUNT + vj_pow5bits((int32_t)q) - 1;
    const int32_t i = -e2 + (int32_t)q + k;

    vr = vj_mulPow5InvDivPow2(mv, q, i);
    vp = vj_mulPow5InvDivPow2(mp, q, i);
    vm = vj_mulPow5InvDivPow2(mm, q, i);

    if (q != 0 && (vp - 1) / 10 <= vm / 10) {
      const int32_t l =
          VJ_FLOAT_POW5_INV_BITCOUNT + vj_pow5bits((int32_t)(q - 1)) - 1;
      lastRemovedDigit =
          (uint8_t)(vj_mulPow5InvDivPow2(mv, q - 1,
                                           -e2 + (int32_t)q - 1 + l) %
                    10);
    }

    if (q <= 9) {
      if (mv % 5 == 0) {
        vrIsTrailingZeros = vj_multipleOfPowerOf5_32(mv, q);
      } else if (acceptBounds) {
        vmIsTrailingZeros = vj_multipleOfPowerOf5_32(mm, q);
      } else {
        vp -= vj_multipleOfPowerOf5_32(mp, q);
      }
    }
  } else {
    const uint32_t q = vj_log10Pow5(-e2);
    e10 = (int32_t)q + e2;
    const int32_t i = -e2 - (int32_t)q;
    const int32_t k = vj_pow5bits(i) - VJ_FLOAT_POW5_BITCOUNT;
    int32_t j = (int32_t)q - k;

    vr = vj_mulPow5divPow2(mv, (uint32_t)i, j);
    vp = vj_mulPow5divPow2(mp, (uint32_t)i, j);
    vm = vj_mulPow5divPow2(mm, (uint32_t)i, j);

    if (q != 0 && (vp - 1) / 10 <= vm / 10) {
      j = (int32_t)q - 1 - (vj_pow5bits(i + 1) - VJ_FLOAT_POW5_BITCOUNT);
      lastRemovedDigit =
          (uint8_t)(vj_mulPow5divPow2(mv, (uint32_t)(i + 1), j) % 10);
    }

    if (q <= 1) {
      vrIsTrailingZeros = 1;
      if (acceptBounds) {
        vmIsTrailingZeros = mmShift == 1;
      } else {
        --vp;
      }
    } else if (q < 31) {
      vrIsTrailingZeros = vj_multipleOfPowerOf2_32(mv, q - 1);
    }
  }

  /* Step 4: Find shortest decimal representation. */
  int32_t removed = 0;
  uint32_t output;

  if (vmIsTrailingZeros || vrIsTrailingZeros) {
    while (vp / 10 > vm / 10) {
      vmIsTrailingZeros &= vm % 10 == 0;
      vrIsTrailingZeros &= lastRemovedDigit == 0;
      lastRemovedDigit = (uint8_t)(vr % 10);
      vr /= 10;
      vp /= 10;
      vm /= 10;
      ++removed;
    }

    if (vmIsTrailingZeros) {
      while (vm % 10 == 0) {
        vrIsTrailingZeros &= lastRemovedDigit == 0;
        lastRemovedDigit = (uint8_t)(vr % 10);
        vr /= 10;
        vp /= 10;
        vm /= 10;
        ++removed;
      }
    }

    if (vrIsTrailingZeros && lastRemovedDigit == 5 && vr % 2 == 0) {
      lastRemovedDigit = 4;
    }

    output =
        vr +
        ((vr == vm && (!acceptBounds || !vmIsTrailingZeros)) ||
         lastRemovedDigit >= 5);
  } else {
    while (vp / 10 > vm / 10) {
      lastRemovedDigit = (uint8_t)(vr % 10);
      vr /= 10;
      vp /= 10;
      vm /= 10;
      ++removed;
    }

    output = vr + (vr == vm || lastRemovedDigit >= 5);
  }

  vj_floating_decimal_32 fd;
  fd.exponent = e10 + removed;
  fd.mantissa = output;
  return fd;
}

/* ================================================================
 *  Section 8 — Core Ryu: float64 -> decimal (d2d)
 * ================================================================ */

typedef struct {
  uint64_t mantissa;
  int32_t exponent;
} vj_floating_decimal_64;

/* Fast path for small integers representable exactly as doubles. */
static inline int vj_d2d_small_int(const uint64_t ieeeMantissa,
                                    const uint32_t ieeeExponent,
                                    vj_floating_decimal_64 *const v) {
  const uint64_t m2 = (1ull << VJ_DOUBLE_MANTISSA_BITS) | ieeeMantissa;
  const int32_t e2 =
      (int32_t)ieeeExponent - VJ_DOUBLE_BIAS - VJ_DOUBLE_MANTISSA_BITS;

  if (e2 > 0)
    return 0; /* f >= 2^53 */
  if (e2 < -52)
    return 0; /* f < 1 */

  const uint64_t mask = (1ull << -e2) - 1;
  if ((m2 & mask) != 0)
    return 0; /* not an integer */

  v->mantissa = m2 >> -e2;
  v->exponent = 0;
  return 1;
}

static inline vj_floating_decimal_64 vj_d2d(const uint64_t ieeeMantissa,
                                              const uint32_t ieeeExponent) {
  int32_t e2;
  uint64_t m2;

  if (ieeeExponent == 0) {
    e2 = 1 - VJ_DOUBLE_BIAS - VJ_DOUBLE_MANTISSA_BITS - 2;
    m2 = ieeeMantissa;
  } else {
    e2 = (int32_t)ieeeExponent - VJ_DOUBLE_BIAS - VJ_DOUBLE_MANTISSA_BITS - 2;
    m2 = (1ull << VJ_DOUBLE_MANTISSA_BITS) | ieeeMantissa;
  }

  const int even = (m2 & 1) == 0;
  const int acceptBounds = even;

  const uint64_t mv = 4 * m2;
  const uint32_t mmShift = ieeeMantissa != 0 || ieeeExponent <= 1;

  uint64_t vr, vp, vm;
  int32_t e10;
  int vmIsTrailingZeros = 0;
  int vrIsTrailingZeros = 0;

  if (e2 >= 0) {
    const uint32_t q = vj_log10Pow2(e2) - (e2 > 3);
    e10 = (int32_t)q;
    const int32_t k =
        VJ_DOUBLE_POW5_INV_BITCOUNT + vj_pow5bits((int32_t)q) - 1;
    const int32_t i = -e2 + (int32_t)q + k;

    uint64_t pow5[2];
    vj_double_computeInvPow5(q, pow5);
    vr = vj_mulShiftAll64(m2, pow5, i, &vp, &vm, mmShift);

    if (q <= 21) {
      const uint32_t mvMod5 =
          ((uint32_t)mv) - 5 * ((uint32_t)vj_div5(mv));
      if (mvMod5 == 0) {
        vrIsTrailingZeros = vj_multipleOfPowerOf5(mv, q);
      } else if (acceptBounds) {
        vmIsTrailingZeros =
            vj_multipleOfPowerOf5(mv - 1 - mmShift, q);
      } else {
        vp -= vj_multipleOfPowerOf5(mv + 2, q);
      }
    }
  } else {
    const uint32_t q = vj_log10Pow5(-e2) - (-e2 > 1);
    e10 = (int32_t)q + e2;
    const int32_t i = -e2 - (int32_t)q;
    const int32_t k = vj_pow5bits(i) - VJ_DOUBLE_POW5_BITCOUNT;
    const int32_t j = (int32_t)q - k;

    uint64_t pow5[2];
    vj_double_computePow5(i, pow5);
    vr = vj_mulShiftAll64(m2, pow5, j, &vp, &vm, mmShift);

    if (q <= 1) {
      vrIsTrailingZeros = 1;
      if (acceptBounds) {
        vmIsTrailingZeros = mmShift == 1;
      } else {
        --vp;
      }
    } else if (q < 63) {
      vrIsTrailingZeros = vj_multipleOfPowerOf2(mv, q);
    }
  }

  /* Step 4: Find shortest decimal representation. */
  int32_t removed = 0;
  uint8_t lastRemovedDigit = 0;
  uint64_t output;

  if (vmIsTrailingZeros || vrIsTrailingZeros) {
    for (;;) {
      const uint64_t vpDiv10 = vj_div10(vp);
      const uint64_t vmDiv10 = vj_div10(vm);
      if (vpDiv10 <= vmDiv10)
        break;

      const uint32_t vmMod10 =
          ((uint32_t)vm) - 10 * ((uint32_t)vmDiv10);
      const uint64_t vrDiv10 = vj_div10(vr);
      const uint32_t vrMod10 =
          ((uint32_t)vr) - 10 * ((uint32_t)vrDiv10);

      vmIsTrailingZeros &= vmMod10 == 0;
      vrIsTrailingZeros &= lastRemovedDigit == 0;
      lastRemovedDigit = (uint8_t)vrMod10;

      vr = vrDiv10;
      vp = vpDiv10;
      vm = vmDiv10;
      ++removed;
    }

    if (vmIsTrailingZeros) {
      for (;;) {
        const uint64_t vmDiv10 = vj_div10(vm);
        const uint32_t vmMod10 =
            ((uint32_t)vm) - 10 * ((uint32_t)vmDiv10);
        if (vmMod10 != 0)
          break;

        const uint64_t vpDiv10 = vj_div10(vp);
        const uint64_t vrDiv10 = vj_div10(vr);
        const uint32_t vrMod10 =
            ((uint32_t)vr) - 10 * ((uint32_t)vrDiv10);

        vrIsTrailingZeros &= lastRemovedDigit == 0;
        lastRemovedDigit = (uint8_t)vrMod10;

        vr = vrDiv10;
        vp = vpDiv10;
        vm = vmDiv10;
        ++removed;
      }
    }

    if (vrIsTrailingZeros && lastRemovedDigit == 5 && vr % 2 == 0) {
      lastRemovedDigit = 4;
    }

    output = vr + ((vr == vm && (!acceptBounds || !vmIsTrailingZeros)) ||
                   lastRemovedDigit >= 5);
  } else {
    /* Optimized common case (~99.3%). */
    int roundUp = 0;
    const uint64_t vpDiv100 = vj_div100(vp);
    const uint64_t vmDiv100 = vj_div100(vm);

    if (vpDiv100 > vmDiv100) {
      const uint64_t vrDiv100 = vj_div100(vr);
      const uint32_t vrMod100 =
          ((uint32_t)vr) - 100 * ((uint32_t)vrDiv100);
      roundUp = vrMod100 >= 50;
      vr = vrDiv100;
      vp = vpDiv100;
      vm = vmDiv100;
      removed += 2;
    }

    for (;;) {
      const uint64_t vpDiv10 = vj_div10(vp);
      const uint64_t vmDiv10 = vj_div10(vm);
      if (vpDiv10 <= vmDiv10)
        break;

      const uint64_t vrDiv10 = vj_div10(vr);
      const uint32_t vrMod10 =
          ((uint32_t)vr) - 10 * ((uint32_t)vrDiv10);
      roundUp = vrMod10 >= 5;

      vr = vrDiv10;
      vp = vpDiv10;
      vm = vmDiv10;
      ++removed;
    }

    output = vr + (vr == vm || roundUp);
  }

  vj_floating_decimal_64 fd;
  fd.exponent = e10 + removed;
  fd.mantissa = output;
  return fd;
}

/* ================================================================
 *  Section 9 — Fixed-point formatting: (mantissa, exponent) -> string
 *
 *  Matches Go's strconv.AppendFloat(buf, f, 'f', -1, bitSize):
 *    - No scientific notation
 *    - Shortest representation (minimum digits for exact round-trip)
 *    - No trailing zeros in fractional part
 *    - Integer values have no decimal point (1.0 -> "1")
 *    - Negative zero: -0.0 -> "-0"
 * ================================================================ */

/*
 * Write the digits of a uint64_t into a temporary buffer.
 * Returns the number of digits written.
 * Digits are written in correct forward order to buf.
 */
static inline int vj_write_mantissa_digits(uint8_t *buf, uint64_t mantissa,
                                            uint32_t olength) {
  /* Write digits from right to left into buf[0..olength-1]. */
  uint32_t i = olength;

  /* Handle the upper part if mantissa > 2^32. */
  if (mantissa >> 32 != 0) {
    /* Split into: mantissa = q * 10^8 + low8 */
    const uint64_t q = mantissa / 100000000;
    uint32_t low8 = (uint32_t)(mantissa - q * 100000000);
    mantissa = q;

    /* Write 8 digits of low8. */
    const uint32_t c = low8 % 10000;
    low8 /= 10000;
    const uint32_t d = low8 % 10000;

    const uint32_t c0 = (c % 100) << 1;
    const uint32_t c1 = (c / 100) << 1;
    const uint32_t d0 = (d % 100) << 1;
    const uint32_t d1 = (d / 100) << 1;

    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + c0, 2);
    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + c1, 2);
    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + d0, 2);
    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + d1, 2);
  }

  uint32_t output2 = (uint32_t)mantissa;
  while (output2 >= 10000) {
    const uint32_t c = output2 - 10000 * (output2 / 10000);
    output2 /= 10000;

    const uint32_t c0 = (c % 100) << 1;
    const uint32_t c1 = (c / 100) << 1;

    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + c0, 2);
    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + c1, 2);
  }

  if (output2 >= 100) {
    const uint32_t c = (output2 % 100) << 1;
    output2 /= 100;
    i -= 2;
    __builtin_memcpy(buf + i, VJ_DIGIT_TABLE + c, 2);
  }

  if (output2 >= 10) {
    const uint32_t c = output2 << 1;
    buf[1] = VJ_DIGIT_TABLE[c + 1];
    buf[0] = VJ_DIGIT_TABLE[c];
  } else {
    buf[0] = (char)('0' + output2);
  }

  return (int)olength;
}

/*
 * Format a (mantissa, exponent, sign) triple in fixed-point notation.
 *
 * The decimal value is: (-1)^sign * mantissa * 10^exponent
 *
 * This produces the same output as Go's strconv.AppendFloat with fmt='f', prec=-1.
 *
 * Returns the number of bytes written.
 */
static inline int vj_format_fixed(uint8_t *buf, uint64_t mantissa,
                                   int32_t exponent, int sign) {
  int idx = 0;

  if (sign) {
    buf[idx++] = '-';
  }

  /* mantissa == 0 was handled by caller for zero values, but guard. */
  if (mantissa == 0) {
    buf[idx++] = '0';
    return idx;
  }

  /* Strip trailing zeros from mantissa (adjust exponent accordingly).
   * Go's strconv also strips trailing fractional zeros, but since Ryu
   * already produces the shortest representation, trailing zeros in
   * the mantissa correspond to powers of 10 that can be folded into
   * the exponent. */
  while (mantissa % 10 == 0) {
    mantissa /= 10;
    exponent++;
  }

  uint32_t olength = vj_decimalLength17(mantissa);

  if (exponent >= 0) {
    /* Integer: write mantissa digits followed by exponent zeros.
     * Example: mantissa=1, exponent=20 -> "100000000000000000000" */
    vj_write_mantissa_digits(buf + idx, mantissa, olength);
    idx += olength;
    for (int32_t i = 0; i < exponent; i++) {
      buf[idx++] = '0';
    }
  } else {
    /* Has fractional part. */
    int32_t absExp = -exponent;

    if ((int32_t)olength <= absExp) {
      /* All digits are after the decimal point.
       * Example: mantissa=1, exponent=-1 -> "0.1"
       * Example: mantissa=5, exponent=-324 -> "0.000...0005" */
      buf[idx++] = '0';
      buf[idx++] = '.';

      /* Write leading zeros. */
      int32_t leadingZeros = absExp - (int32_t)olength;
      for (int32_t i = 0; i < leadingZeros; i++) {
        buf[idx++] = '0';
      }

      /* Write mantissa digits. */
      vj_write_mantissa_digits(buf + idx, mantissa, olength);
      idx += olength;
    } else {
      /* Decimal point splits the digits.
       * Example: mantissa=95, exponent=-1 -> "9.5"
       * Example: mantissa=314159, exponent=-5 -> "3.14159" */
      int32_t intDigits = (int32_t)olength - absExp;

      /* Write the integer part and fractional part using the digit
       * writer, then insert the decimal point by shifting. */
      vj_write_mantissa_digits(buf + idx, mantissa, olength);

      /* Shift fractional digits right by 1 to make room for '.'. */
      for (int32_t i = olength - 1; i >= intDigits; i--) {
        buf[idx + i + 1] = buf[idx + i];
      }
      buf[idx + intDigits] = '.';

      idx += olength + 1;
    }
  }

  return idx;
}

/* ================================================================
 *  Section 10 — Public API: vj_write_float32 / vj_write_float64
 *
 *  These functions format float/double values to a buffer in
 *  fixed-point notation matching Go's strconv.AppendFloat behavior.
 *
 *  The caller must ensure sufficient buffer space:
 *    float32: >= 50 bytes
 *    float64: >= 330 bytes
 *
 *  NaN/Inf must be checked by the caller (not handled here).
 *  Returns the number of bytes written.
 * ================================================================ */

static inline int vj_write_float32(uint8_t *buf, float value) {
  const uint32_t bits = vj_float_to_bits(value);
  const int sign =
      ((bits >> (VJ_FLOAT_MANTISSA_BITS + VJ_FLOAT_EXPONENT_BITS)) & 1) != 0;
  const uint32_t ieeeMantissa = bits & ((1u << VJ_FLOAT_MANTISSA_BITS) - 1);
  const uint32_t ieeeExponent =
      (bits >> VJ_FLOAT_MANTISSA_BITS) &
      ((1u << VJ_FLOAT_EXPONENT_BITS) - 1);

  /* Zero. */
  if (ieeeExponent == 0 && ieeeMantissa == 0) {
    if (sign) {
      buf[0] = '-';
      buf[1] = '0';
      return 2;
    }
    buf[0] = '0';
    return 1;
  }

  const vj_floating_decimal_32 v = vj_f2d(ieeeMantissa, ieeeExponent);
  return vj_format_fixed(buf, (uint64_t)v.mantissa, v.exponent, sign);
}

static inline int vj_write_float64(uint8_t *buf, double value) {
  const uint64_t bits = vj_double_to_bits(value);
  const int sign =
      ((bits >> (VJ_DOUBLE_MANTISSA_BITS + VJ_DOUBLE_EXPONENT_BITS)) & 1) != 0;
  const uint64_t ieeeMantissa =
      bits & ((1ull << VJ_DOUBLE_MANTISSA_BITS) - 1);
  const uint32_t ieeeExponent =
      (uint32_t)((bits >> VJ_DOUBLE_MANTISSA_BITS) &
                 ((1u << VJ_DOUBLE_EXPONENT_BITS) - 1));

  /* Zero. */
  if (ieeeExponent == 0 && ieeeMantissa == 0) {
    if (sign) {
      buf[0] = '-';
      buf[1] = '0';
      return 2;
    }
    buf[0] = '0';
    return 1;
  }

  vj_floating_decimal_64 v;
  const int isSmallInt = vj_d2d_small_int(ieeeMantissa, ieeeExponent, &v);

  if (isSmallInt) {
    /* For small integers, strip trailing zeros to match Ryu's behavior.
     * This mirrors what Go's strconv does internally. */
    while (v.mantissa != 0) {
      const uint64_t q = v.mantissa / 10;
      const uint32_t r = (uint32_t)(v.mantissa - 10 * q);
      if (r != 0)
        break;
      v.mantissa = q;
      v.exponent++;
    }
  } else {
    v = vj_d2d(ieeeMantissa, ieeeExponent);
  }

  return vj_format_fixed(buf, v.mantissa, v.exponent, sign);
}

#endif /* VJ_RYU_H */
