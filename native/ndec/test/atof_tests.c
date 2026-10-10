/*
 * atof_tests.c -- decimal conversion bounds and rounding tests.
 *
 * Oracles:
 *   bounds    the general entries and the binary32 JSON entry never read at
 *             or past s + readable_bytes. Each input sits flush against a
 *             PROT_NONE page, so any over-read faults without a sanitizer
 *             runtime; every prefix of every input is replayed so the scan
 *             stops at each position.
 *   rounding  an accepted token converts to the value glibc strtod/strtof
 *             produce, including mantissas far beyond the multiprecision
 *             digit cap and exact midpoints with zero or sticky tails.
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#include <criterion/criterion.h>

#include "ndec/core/atof.h"

#define ATOF_TEST_MAX 8192

static atof_ctx ctx;

/* Copy src so that its last byte is the last readable byte of the mapping. */
typedef struct {
  uint8_t *map;
  size_t map_len;
  const char *s;
} guarded;

static guarded guard_place(const char *src, size_t len) {
  size_t page  = (size_t)sysconf(_SC_PAGESIZE);
  size_t pages = (len + page - 1) / page + 1;
  guarded g;
  g.map_len = (pages + 1) * page;
  g.map     = mmap(NULL, g.map_len, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
  cr_assert(g.map != MAP_FAILED);
  cr_assert(mprotect(g.map + pages * page, page, PROT_NONE) == 0);
  char *dst = (char *)g.map + pages * page - len;
  memcpy(dst, src, len);
  g.s = dst;
  return g;
}

static void guard_free(guarded g) {
  munmap(g.map, g.map_len);
}

/* Parse src through the bounded entries; when a general entry consumes the
 * whole token, its value must match the libc oracle. */
static void check_token(const char *src, size_t len) {
  char z[ATOF_TEST_MAX + 1];
  cr_assert(len <= ATOF_TEST_MAX);
  memcpy(z, src, len);
  z[len] = 0;

  guarded g           = guard_place(src, len);
  atof_result_f64 r64 = atof_parse_f64_ctx(g.s, (int)len, &ctx);
  atof_result_f32 r32 = atof_parse_f32_ctx(g.s, (int)len, &ctx);
  atof_result_f32 j32 = atof_parse_f32_json_ctx(g.s, (int)len, &ctx);
  const char *end     = g.s + len;
  int whole64         = r64.end == end;
  int whole32         = r32.end == end;
  int wholej          = j32.end == end;
  double v64          = r64.val;
  float v32           = r32.val;
  float vj            = j32.val;
  guard_free(g);

  if (whole64) {
    double want = strtod(z, NULL);
    if (want == want)
      cr_expect(memcmp(&v64, &want, sizeof want) == 0, "f64 %.40s... (len %zu): got %.17g want %.17g", z, len, v64,
                want);
  }
  if (whole32 || wholej) {
    float want = strtof(z, NULL);
    if (want == want) {
      if (whole32)
        cr_expect(memcmp(&v32, &want, sizeof want) == 0, "f32 %.40s... (len %zu): got %.9g want %.9g", z, len, v32,
                  want);
      if (wholej)
        cr_expect(memcmp(&vj, &want, sizeof want) == 0, "f32 json %.40s... (len %zu): got %.9g want %.9g", z, len,
                  vj, want);
    }
  }
}

static void check_all_prefixes(const char *src) {
  size_t len = strlen(src);
  for (size_t k = 0; k <= len; k++)
    check_token(src, k);
}

Test(atof, bounded_short_tokens) {
  static const char *inputs[] = {
      "",
      "-",
      "+",
      ".",
      "e",
      "0",
      "1",
      "-0",
      "1.",
      ".5",
      "1e",
      "1e+",
      "1e-5",
      "12345678",
      "0.12345678",
      "1.23456789",
      "0.00000001",
      "1.5e308",
      "inf",
      "-Infinity",
      "nan",
      "in",
      "infin",
      "5e-324",
      "1.4e-45",
      "3.4028235e38",
      "1.7976931348623157e308",
      "123456789012345678901234",
      "0.1234567890123456789",
      "9100000000000000.999",
      "100.0000000000800",
      /* Hex spellings where strtod is a valid oracle (no separators;
       * the separator cases live in quoted_hex_grammar, strtod stops at
       * an underscore). */
      "0x0p0",
      "0X1P2",
      "+0x1p0",
      "-0x.8p1",
      "0x.8p1",
      "0x1.p0",
      "0x1e2p3",
      "0x1p-2",
      "0x1p9999",
      "0x1p-99999",
      "0x1234567890abcdef123p0",
  };
  for (size_t i = 0; i < sizeof inputs / sizeof *inputs; i++)
    check_all_prefixes(inputs[i]);
}

static char buf[ATOF_TEST_MAX + 1];

/* buf = head + fill * n + tail */
static size_t build(const char *head, char fill, size_t n, const char *tail) {
  size_t h = strlen(head), t = strlen(tail);
  cr_assert(h + n + t <= ATOF_TEST_MAX);
  memcpy(buf, head, h);
  memset(buf + h, fill, n);
  memcpy(buf + h + n, tail, t);
  buf[h + n + t] = 0;
  return h + n + t;
}

Test(atof, long_mantissa_rounding) {
  /* Exact midpoints: 1 + 2^-53 (binary64) and 1 + 2^-24 (binary32). */
  static const char *mids[] = {
      "1.00000000000000011102230246251565404236316680908203125",
      "1.000000059604644775390625",
  };
  for (size_t i = 0; i < 2; i++) {
    for (size_t n = 0; n < 3000; n += 97) {
      check_token(buf, build(mids[i], '0', n, ""));
      check_token(buf, build(mids[i], '0', n, "1"));
      check_token(buf, build(mids[i], '0', n, "1e-30"));
    }
  }
  for (size_t n = 1; n < 3000; n += 7) {
    check_token(buf, build("1.", '7', n, ""));
    check_token(buf, build("0.", '0', n, "3"));
    check_token(buf, build("9", '9', n, "e-300"));
  }
}

Test(atof, regressions) {
  /* 1587 significant digits overflowed the 80-limb mpint (fuzz SIGSEGV). */
  size_t n = build("1.000000000000000", '5', 1538, "");
  memset(buf + n, '0', 31);
  n += 31;
  buf[n++] = '5';
  buf[n]   = 0;
  check_token(buf, n);
  /* A >19-digit mantissa wraps d to a small value; binary32 must not take
   * the fast path on it. */
  check_token(buf, build("1", '0', 70, "e-60"));
  check_token(buf, build("9.4825950", '0', 62, "1e+09"));
}

/* ---- quoted-number literal extensions: hex floats and separators ---- */

typedef struct {
  const char *in;
  int whole64;
  uint64_t b64;
  int whole32;
  uint32_t b32;
} quoted_case;

/* Bounds-only replay: every prefix of every case parses under the
 * PROT_NONE guard, catching over-reads at each scan stop. */
static void replay_bounds(const char *src) {
  size_t len = strlen(src);
  for (size_t k = 0; k <= len; k++) {
    guarded g           = guard_place(src, k);
    atof_result_f64 r64 = atof_parse_f64_ctx(g.s, (int)k, &ctx);
    atof_result_f32 r32 = atof_parse_f32_ctx(g.s, (int)k, &ctx);
    cr_assert(r64.end <= g.s + k);
    cr_assert(r32.end <= g.s + k);
    guard_free(g);
  }
}

/* The general entries follow the strconv.ParseFloat quoted grammar:
 * hex floats with a mandatory p exponent, and separators strictly
 * between digits. Bit patterns come from the Go oracle; wholeX records
 * full-token consumption, which ErrRange keeps (the quoted wrapper
 * rejects the overflow later). */
Test(atof, quoted_hex_grammar) {
  static const quoted_case t[] = {
      {"0x0p0", 1, UINT64_C(0x0000000000000000), 1, 0x00000000u},
      {"0X1P2", 1, UINT64_C(0x4010000000000000), 1, 0x40800000u},
      {"+0x1p0", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"-0x.8p1", 1, UINT64_C(0xbff0000000000000), 1, 0xbf800000u},
      {"0x.8p1", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0x1.p0", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0x1e2p3", 1, UINT64_C(0x40ae200000000000), 1, 0x45710000u},
      {"0x1p-2", 1, UINT64_C(0x3fd0000000000000), 1, 0x3e800000u},
      {"0x1_0p0", 1, UINT64_C(0x4030000000000000), 1, 0x41800000u},
      {"0x_1p0", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0x1_0_0p0", 1, UINT64_C(0x4070000000000000), 1, 0x43800000u},
      {"0x1p-10_75", 1, UINT64_C(0x0000000000000000), 1, 0x00000000u},
      {"0x1p1_0", 1, UINT64_C(0x4090000000000000), 1, 0x44800000u},
      {"0x1p-1075", 1, UINT64_C(0x0000000000000000), 1, 0x00000000u},
      {"0x1.0000000000001p-1075", 1, UINT64_C(0x0000000000000001), 1, 0x00000000u},
      {"0x1p-1074", 1, UINT64_C(0x0000000000000001), 1, 0x00000000u},
      {"-0x1p-1075", 1, UINT64_C(0x8000000000000000), 1, 0x80000000u},
      {"0x1.0000000000001p0", 1, UINT64_C(0x3ff0000000000001), 1, 0x3f800000u},
      {"0x1.00000000000008p0", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0x1.00000000000008001p0", 1, UINT64_C(0x3ff0000000000001), 1, 0x3f800000u},
      {"0x1.00000000000007fff8p0", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0x1234567890abcdef123p0", 1, UINT64_C(0x447234567890abce), 1, 0x6391a2b4u},
      {"0x1.fffffffffffff8p0", 1, UINT64_C(0x4000000000000000), 1, 0x40000000u},
      {"0x1.fffffffffffff7p1023", 1, UINT64_C(0x7fefffffffffffff), 1, 0x7f800000u},
      {"0x1.fffffffffffff8p1023", 1, UINT64_C(0x7ff0000000000000), 1, 0x7f800000u},
      {"0x1p99999999", 1, UINT64_C(0x7ff0000000000000), 1, 0x7f800000u},
      {"0x1p-99999999", 1, UINT64_C(0x0000000000000000), 1, 0x00000000u},
      {"0x0p999999", 1, UINT64_C(0x0000000000000000), 1, 0x00000000u},
      {"-0x0p-99999", 1, UINT64_C(0x8000000000000000), 1, 0x80000000u},
      {"0x1p-150", 1, UINT64_C(0x3690000000000000), 1, 0x00000000u},
      {"0x1p-149", 1, UINT64_C(0x36a0000000000000), 1, 0x00000001u},
      {"0x1.800001p-150", 1, UINT64_C(0x3698000010000000), 1, 0x00000001u},
      {"0x1.000001p0", 1, UINT64_C(0x3ff0000010000000), 1, 0x3f800000u},
      {"0x1.000002p0", 1, UINT64_C(0x3ff0000020000000), 1, 0x3f800001u},
      {"0x1.000003p0", 1, UINT64_C(0x3ff0000030000000), 1, 0x3f800002u},
      {"0x1.fffffep127", 1, UINT64_C(0x47efffffe0000000), 1, 0x7f7fffffu},
      {"0x1.ffffffp127", 1, UINT64_C(0x47effffff0000000), 1, 0x7f800000u},
      {"1_000.5", 1, UINT64_C(0x408f440000000000), 1, 0x447a2000u},
      {"1_0_0.5", 1, UINT64_C(0x4059200000000000), 1, 0x42c90000u},
      {"1e2_0", 1, UINT64_C(0x4415af1d78b58c40), 1, 0x60ad78ecu},
      {"1_0.5e1_0", 1, UINT64_C(0x4238727cda000000), 1, 0x51c393e7u},
      {"0_1", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"00_1", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0.1_5", 1, UINT64_C(0x3fc3333333333333), 1, 0x3e19999au},
      {"1_000_000", 1, UINT64_C(0x412e848000000000), 1, 0x49742400u},
      {"5e-32_4", 1, UINT64_C(0x0000000000000001), 1, 0x00000000u},
      {"0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_1", 1, UINT64_C(0x3ff0000000000000), 1, 0x3f800000u},
      {"0.0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_1", 1, UINT64_C(0x3abef2d0f5da7dd9), 1, 0x15f79688u},
      {"0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_1_234_567_890_123_456_789_012_3", 1,
       UINT64_C(0x4484ea15b273b38a), 1, 0x642750aeu},
      {"0.0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_0_1_234_567_890_123_456_789_012_3", 1,
       UINT64_C(0x3ac31aa094e075f3), 1, 0x1618d505u},
  };
  static const char *rejects[] = {
      "0x1",   "0x.8",  "0x8",    "0xe",     "0x1e2",       "0xp0",   "0x.p1",  "0x",
      "0x1p",  "0x1p+", "0x1p-",  "0x1p0x",  "0x1p0e",      "0x1_p0", "0x1p_0", "0x1p0_",
      "0x1_.0p0", "0x1._0p0", "0x1.0_p0", "0x_", "0x__1p0", "0x0_0", "1__000", "1_",
      "_1",    "1_000_.5", "1_.5", "1._5", "1e_0", "1_e2", "1e_", "_1000", "1000_",
      "1_000.5_", "_.5", "._5", "1e1__0", "+_1", "-_1", "in_f", "i_nf", "na_n",
      "_inf",  "_nan", "0b101", "0o17", "0_", "0._", "1_0_", "0_._1", "1_000.5e_1",
  };
  for (size_t i = 0; i < sizeof t / sizeof *t; i++) {
    size_t len    = strlen(t[i].in);
    guarded g     = guard_place(t[i].in, len);
    const char *e = g.s + len;
    atof_result_f64 r64 = atof_parse_f64_ctx(g.s, (int)len, &ctx);
    double v64          = r64.val;
    atof_result_f32 r32 = atof_parse_f32_ctx(g.s, (int)len, &ctx);
    float v32           = r32.val;
    guard_free(g);
    cr_expect((r64.end == e) == t[i].whole64, "%s f64 whole=%d want %d", t[i].in, r64.end == e, t[i].whole64);
    cr_expect((r32.end == e) == t[i].whole32, "%s f32 whole=%d want %d", t[i].in, r32.end == e, t[i].whole32);
    if (t[i].whole64)
      cr_expect(memcmp(&v64, &t[i].b64, sizeof v64) == 0, "%s f64 bits %016llx want %016llx", t[i].in,
                (unsigned long long)*(uint64_t *)&v64, (unsigned long long)t[i].b64);
    if (t[i].whole32)
      cr_expect(memcmp(&v32, &t[i].b32, sizeof v32) == 0, "%s f32 bits %08x want %08x", t[i].in,
                (unsigned)*(uint32_t *)&v32, t[i].b32);
    replay_bounds(t[i].in);
  }
  for (size_t i = 0; i < sizeof rejects / sizeof *rejects; i++) {
    size_t len    = strlen(rejects[i]);
    guarded g     = guard_place(rejects[i], len);
    const char *e = g.s + len;
    atof_result_f64 r64 = atof_parse_f64_ctx(g.s, (int)len, &ctx);
    atof_result_f32 r32 = atof_parse_f32_ctx(g.s, (int)len, &ctx);
    guard_free(g);
    cr_expect(r64.end != e, "%s f64 must not consume whole", rejects[i]);
    cr_expect(r32.end != e, "%s f32 must not consume whole", rejects[i]);
    replay_bounds(rejects[i]);
  }
}

/* buf = head + "0_0" * n + tail; digits with separators. */
static size_t build_us(const char *head, size_t n, const char *tail) {
  size_t h = strlen(head), t = strlen(tail);
  cr_assert(h + 3 * n + t <= ATOF_TEST_MAX);
  memcpy(buf, head, h);
  for (size_t i = 0; i < n; i++)
    memcpy(buf + h + 3 * i, "0_0", 3);
  memcpy(buf + h + 3 * n, tail, t);
  buf[h + 3 * n + t] = 0;
  return h + 3 * n + t;
}

/* Fraction digits of the exact midpoints 1 + 2^-53 and 1 + 2^-24. */
static const char mid64_frac[] = "00000000000000011102230246251565404236316680908203125";
static const char mid32_frac[] = "000000059604644775390625";

/* buf = "1." + frac + zero_count separated zeros + an optional final
 * one digit. */
static size_t build_mid_us(const char *frac, int zero_count, int with_one) {
  size_t mf = strlen(frac);
  size_t n  = 0;
  buf[n++]  = '1';
  buf[n++]  = '.';
  memcpy(buf + n, frac, mf);
  n += mf;
  for (int i = 0; i < zero_count; i++) {
    if (i) buf[n++] = '_';
    buf[n++] = '0';
  }
  if (with_one) {
    buf[n++] = '_';
    buf[n++] = '1';
  }
  buf[n] = 0;
  return n;
}

/* Separator bodies on the refine path: the dense digit copy, the fold
 * past the digit cap, and the underscored exact midpoints at both
 * precisions. Expected bits come from big.Rat. */
Test(atof, quoted_us_refine) {
  /* 0: 1 + 2^-53 with separators, the exact binary64 midpoint rounds to
   * even. 1: 1 . 800 zeros . e-800 = 1.0 exactly, through the >768 fold.
   * 2: 1 . 800 zeros . 1e-800 = 10.000...01, a nonzero tail past the cap.
   * 3: 201 significant digits on the plain refine path, f32 overflows.
   * 4/5: the binary64 midpoint padded past the digit cap: an all-zero
   * tail folds without sticky and rounds to even, a final one digit sets
   * the sticky and rounds up. 6/7: 1 + 2^-24 with separators, the exact
   * binary32 midpoint rounds to even and a trailing one digit rounds up.
   * 8/9: the binary32 midpoint padded past the cap, as 4/5. */
  static const uint64_t b64[10] = {
      UINT64_C(0x3ff0000000000000), UINT64_C(0x3ff0000000000000), UINT64_C(0x4024000000000000),
      UINT64_C(0x6126c2d4256ffcc3), UINT64_C(0x3ff0000000000000), UINT64_C(0x3ff0000000000001),
      UINT64_C(0x3ff0000010000000), UINT64_C(0x3ff0000010000000), UINT64_C(0x3ff0000010000000),
      UINT64_C(0x3ff0000010000000),
  };
  static const uint32_t b32[10] = {
      0x3f800000u, 0x3f800000u, 0x41200000u, 0x7f800000u, 0x3f800000u,
      0x3f800000u, 0x3f800000u, 0x3f800001u, 0x3f800000u, 0x3f800001u,
  };
  for (int i = 0; i < 10; i++) {
    /* each body builds fresh: every case shares the static buf */
    const char *src = buf;
    size_t len;
    switch (i) {
    case 0:
      src = "1.000_00000000000011102230246251565404236316680908203125";
      len = strlen(src);
      break;
    case 1:
      len = build_us("1", 400, "e-800");
      break;
    case 2:
      len = build_us("1", 400, "1e-800");
      break;
    case 3:
      len = build_us("1", 100, "e-40");
      break;
    case 4:
      len = build_mid_us(mid64_frac, 744, 0);
      break;
    case 5:
      len = build_mid_us(mid64_frac, 743, 1);
      break;
    case 6:
      src = "1.000_000_059_604_644_775_390_625";
      len = strlen(src);
      break;
    case 7:
      src = "1.000_000_059_604_644_775_390_625_1";
      len = strlen(src);
      break;
    case 8:
      len = build_mid_us(mid32_frac, 760, 0);
      break;
    default:
      len = build_mid_us(mid32_frac, 760, 1);
      break;
    }
    guarded g           = guard_place(src, len);
    const char *e       = g.s + len;
    atof_result_f64 r64 = atof_parse_f64_ctx(g.s, (int)len, &ctx);
    double v64          = r64.val;
    atof_result_f32 r32 = atof_parse_f32_ctx(g.s, (int)len, &ctx);
    float v32           = r32.val;
    guard_free(g);
    cr_expect(r64.end == e, "refine[%d] f64 must consume whole", i);
    cr_expect(r32.end == e, "refine[%d] f32 must consume whole", i);
    cr_expect(memcmp(&v64, &b64[i], sizeof v64) == 0, "refine[%d] f64 bits %016llx", i,
              (unsigned long long)*(uint64_t *)&v64);
    cr_expect(memcmp(&v32, &b32[i], sizeof v32) == 0, "refine[%d] f32 bits %08x", i, (unsigned)*(uint32_t *)&v32);
  }
}
