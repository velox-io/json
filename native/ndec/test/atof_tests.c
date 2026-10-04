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
