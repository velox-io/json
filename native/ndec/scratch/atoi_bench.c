#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef enum {
  NDEC_NUM_OK       = 0,
  NDEC_NUM_EMPTY    = 1,
  NDEC_NUM_FLOAT    = 2,
  NDEC_NUM_OVERFLOW = 3,
} ndec_num_status;

static inline ndec_num_status ndec_parse_int64(const uint8_t *s, uint32_t n, int64_t *out) {
  int neg            = (*s == '-');
  const uint8_t *p   = s + neg;
  const uint8_t *end = s + n;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9)
      break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0)
    return NDEC_NUM_EMPTY;
  if (p != end)
    return NDEC_NUM_FLOAT;
  if (dc > 19)
    return NDEC_NUM_OVERFLOW;
  if (i > (uint64_t)INT64_MAX + (uint64_t)neg)
    return NDEC_NUM_OVERFLOW;

  *out = neg ? (int64_t)(~i + 1) : (int64_t)i;
  return NDEC_NUM_OK;
}

static inline ndec_num_status ndec_parse_uint64(const uint8_t *s, uint32_t n, uint64_t *out) {
  const uint8_t *p   = s;
  const uint8_t *end = s + n;
  if (*p == '-')
    return NDEC_NUM_EMPTY;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9)
      break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0)
    return NDEC_NUM_EMPTY;
  if (p != end)
    return NDEC_NUM_FLOAT;
  if (dc > 20)
    return NDEC_NUM_OVERFLOW;
  if (dc == 20) {
    if (*start_digits != '1' || i <= (uint64_t)INT64_MAX)
      return NDEC_NUM_OVERFLOW;
  }

  *out = i;
  return NDEC_NUM_OK;
}

static inline int simd_parse_digit(uint8_t c, uint64_t *i) {
  uint8_t digit = (uint8_t)(c - '0');
  if (digit > 9)
    return 0;
  *i = 10 * (*i) + digit;
  return 1;
}

static inline int swar_is_eight_digits(uint64_t val) {
  /* Check each byte in ['0','9'] without branches:
   *   (val & 0xF0...) | (((val + 0x06...) & 0xF0...) >> 4) == 0x33...
   * Adding 6 to a valid ASCII digit high-nibble 0x3 overflows to 0x4;
   * the expression evaluates to 0x33 for each valid digit byte. */
  return ((val & 0xF0F0F0F0F0F0F0F0ULL) | (((val + 0x0606060606060606ULL) & 0xF0F0F0F0F0F0F0F0ULL) >> 4)) ==
         0x3333333333333333ULL;
}

static inline uint32_t swar_parse_eight_digits(const uint8_t *p) {
  uint64_t val;
  __builtin_memcpy(&val, p, 8);
  val = (val & 0x0F0F0F0F0F0F0F0FULL) * 2561 >> 8;
  val = (val & 0x00FF00FF00FF00FFULL) * 6553601 >> 16;
  return (uint32_t)((val & 0x0000FFFF0000FFFFULL) * 42949672960001ULL >> 32);
}

static inline ndec_num_status swar_parse_int64(const uint8_t *s, uint32_t n, int64_t *out) {
  int neg                     = (*s == '-');
  const uint8_t *p            = s + neg;
  const uint8_t *end          = s + n;
  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  while ((size_t)(end - p) >= 8) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!swar_is_eight_digits(val))
      break;
    i = i * 100000000ULL + swar_parse_eight_digits(p);
    p += 8;
  }
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9)
      break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0)
    return NDEC_NUM_EMPTY;
  if (p != end)
    return NDEC_NUM_FLOAT;
  if (dc > 19)
    return NDEC_NUM_OVERFLOW;
  if (i > (uint64_t)INT64_MAX + (uint64_t)neg)
    return NDEC_NUM_OVERFLOW;

  *out = neg ? (int64_t)(~i + 1) : (int64_t)i;
  return NDEC_NUM_OK;
}

static inline ndec_num_status swar_parse_uint64(const uint8_t *s, uint32_t n, uint64_t *out) {
  const uint8_t *p   = s;
  const uint8_t *end = s + n;
  if (*p == '-')
    return NDEC_NUM_EMPTY;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;

  while ((size_t)(end - p) >= 8) {
    uint64_t val;
    __builtin_memcpy(&val, p, 8);
    if (!swar_is_eight_digits(val))
      break;
    i = i * 100000000ULL + swar_parse_eight_digits(p);
    p += 8;
  }
  while (p < end) {
    uint8_t digit = (uint8_t)(*p - '0');
    if (digit > 9)
      break;
    i = 10 * i + digit;
    p++;
  }

  size_t dc = (size_t)(p - start_digits);
  if (dc == 0)
    return NDEC_NUM_EMPTY;
  if (p != end)
    return NDEC_NUM_FLOAT;
  if (dc > 20)
    return NDEC_NUM_OVERFLOW;
  if (dc == 20) {
    if (*start_digits != '1' || i <= (uint64_t)INT64_MAX)
      return NDEC_NUM_OVERFLOW;
  }

  *out = i;
  return NDEC_NUM_OK;
}

static inline int simd_parse_int64(const uint8_t *s, uint32_t n, int64_t *out) {
  int neg            = (*s == '-');
  const uint8_t *p   = s + neg;
  const uint8_t *end = s + n;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;
  while (p < end && simd_parse_digit(*p, &i)) {
    p++;
  }

  size_t digit_count = (size_t)(p - start_digits);
  if (digit_count == 0)
    return 1;
  if (digit_count > 19)
    return 2;
  if (i > (uint64_t)INT64_MAX + (uint64_t)neg)
    return 2;

  *out = neg ? (int64_t)(~i + 1) : (int64_t)i;
  return 0;
}

static inline int simd_parse_uint64(const uint8_t *s, uint32_t n, uint64_t *out) {
  const uint8_t *p   = s;
  const uint8_t *end = s + n;
  if (*p == '-')
    return 3;

  const uint8_t *start_digits = p;
  uint64_t i                  = 0;
  while (p < end && simd_parse_digit(*p, &i)) {
    p++;
  }

  size_t digit_count = (size_t)(p - start_digits);
  if (digit_count == 0)
    return 1;
  if (digit_count > 20)
    return 2;
  if (digit_count == 20) {
    if (*start_digits != '1' || i <= (uint64_t)INT64_MAX)
      return 2;
  }

  *out = i;
  return 0;
}

static inline void black_box(void *p) {
  __asm__ volatile("" : : "r"(p) : "memory");
}

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

#define MAX_INPUTS 20000
#define MAX_LEN    20

typedef struct {
  uint8_t buf[MAX_LEN + 2];
  uint32_t len;
  int64_t expected_i64;
  uint64_t expected_u64;
} TestCase;

static TestCase cases_i64[MAX_INPUTS];
static TestCase cases_u64[MAX_INPUTS];
static int num_cases;

static void generate_cases(int seed, int min_dc, int max_dc) {
  srand(seed);
  num_cases = MAX_INPUTS;

  for (int idx = 0; idx < num_cases; idx++) {
    int neg  = (rand() % 2) ? 1 : 0;
    int span = max_dc - min_dc + 1;
    int dc   = min_dc + (rand() % span);

    uint8_t *p = cases_i64[idx].buf;
    if (neg)
      *p++ = '-';

    for (int j = 0; j < dc; j++) {
      uint8_t d;
      if (j == 0)
        d = (uint8_t)('1' + (rand() % 9));
      else
        d = (uint8_t)('0' + (rand() % 10));
      p[j] = d;
    }

    uint32_t len       = (uint32_t)((neg ? 1 : 0) + dc);
    cases_i64[idx].len = len;

    const uint8_t *s = cases_i64[idx].buf;
    int sign         = 1;
    if (*s == '-') {
      sign = -1;
      s++;
    }
    uint64_t abs_val = 0;
    for (int k = 0; k < dc; k++) {
      abs_val = abs_val * 10 + (uint64_t)(s[k] - '0');
    }

    if (neg) {
      if (abs_val > (uint64_t)INT64_MAX + 1ULL) {
        idx--;
        continue;
      }
      cases_i64[idx].expected_i64 = (int64_t)(0ULL - abs_val);
    } else {
      if (abs_val > (uint64_t)INT64_MAX) {
        idx--;
        continue;
      }
      cases_i64[idx].expected_i64 = (int64_t)abs_val;
    }

    memcpy(cases_u64[idx].buf, cases_i64[idx].buf, len);
    cases_u64[idx].len          = len;
    cases_u64[idx].expected_u64 = neg ? 0 : abs_val;
  }
}

static void bench_int64_simd(const char *name, int (*f)(const uint8_t *, uint32_t, int64_t *), int iters) {
  int64_t sum    = 0;
  int ok         = 0;
  uint64_t start = now_ns();
  for (int iter = 0; iter < iters; iter++) {
    for (int idx = 0; idx < num_cases; idx++) {
      int64_t out;
      if (f(cases_i64[idx].buf, cases_i64[idx].len, &out) == 0) {
        sum += out;
        ok++;
      }
    }
    black_box(&sum);
  }
  uint64_t elapsed = now_ns() - start;
  int total        = iters * num_cases;
  printf("  %-22s  %8.2f ns/op  (sum=%lld, ok=%d/%d)\n", name, (double)elapsed / total, (long long)sum, ok, total);
}

static void bench_int64_ndec(const char *name, ndec_num_status (*f)(const uint8_t *, uint32_t, int64_t *),
                             int iters) {
  int64_t sum    = 0;
  int ok         = 0;
  uint64_t start = now_ns();
  for (int iter = 0; iter < iters; iter++) {
    for (int idx = 0; idx < num_cases; idx++) {
      int64_t out;
      if (f(cases_i64[idx].buf, cases_i64[idx].len, &out) == NDEC_NUM_OK) {
        sum += out;
        ok++;
      }
    }
    black_box(&sum);
  }
  uint64_t elapsed = now_ns() - start;
  int total        = iters * num_cases;
  printf("  %-22s  %8.2f ns/op  (sum=%lld, ok=%d/%d)\n", name, (double)elapsed / total, (long long)sum, ok, total);
}

static void bench_uint64_simd(const char *name, int (*f)(const uint8_t *, uint32_t, uint64_t *), int iters) {
  uint64_t sum   = 0;
  int ok         = 0;
  uint64_t start = now_ns();
  for (int iter = 0; iter < iters; iter++) {
    for (int idx = 0; idx < num_cases; idx++) {
      uint64_t out;
      if (f(cases_u64[idx].buf, cases_u64[idx].len, &out) == 0) {
        sum += out;
        ok++;
      }
    }
    black_box(&sum);
  }
  uint64_t elapsed = now_ns() - start;
  int total        = iters * num_cases;
  printf("  %-22s  %8.2f ns/op  (sum=%llu, ok=%d/%d)\n", name, (double)elapsed / total, (unsigned long long)sum,
         ok, total);
}

static void bench_uint64_ndec(const char *name, ndec_num_status (*f)(const uint8_t *, uint32_t, uint64_t *),
                              int iters) {
  uint64_t sum   = 0;
  int ok         = 0;
  uint64_t start = now_ns();
  for (int iter = 0; iter < iters; iter++) {
    for (int idx = 0; idx < num_cases; idx++) {
      uint64_t out;
      if (f(cases_u64[idx].buf, cases_u64[idx].len, &out) == NDEC_NUM_OK) {
        sum += out;
        ok++;
      }
    }
    black_box(&sum);
  }
  uint64_t elapsed = now_ns() - start;
  int total        = iters * num_cases;
  printf("  %-22s  %8.2f ns/op  (sum=%llu, ok=%d/%d)\n", name, (double)elapsed / total, (unsigned long long)sum,
         ok, total);
}

static int verify_all(void) {
  int errors = 0;
  for (int idx = 0; idx < num_cases; idx++) {
    int64_t out_n, out_s, out_w;
    uint64_t out_un, out_us, out_uw;

    int rn = ndec_parse_int64(cases_i64[idx].buf, cases_i64[idx].len, &out_n);
    int rs = simd_parse_int64(cases_i64[idx].buf, cases_i64[idx].len, &out_s);
    int rw = swar_parse_int64(cases_i64[idx].buf, cases_i64[idx].len, &out_w);
    if (rn == NDEC_NUM_OK && rs == 0 && rw == NDEC_NUM_OK) {
      if (out_n != out_s || out_n != out_w) {
        printf("MISMATCH i64 idx=%d: ndec=%lld simd=%lld swar=%lld\n", idx, (long long)out_n, (long long)out_s,
               (long long)out_w);
        errors++;
      }
    } else if (rn != NDEC_NUM_OK || rs != 0 || rw != NDEC_NUM_OK) {
      printf("STATUS MISMATCH i64 idx=%d: ndec=%d simd=%d swar=%d\n", idx, rn, rs, rw);
      errors++;
    }

    if (cases_u64[idx].buf[0] != '-') {
      rn = ndec_parse_uint64(cases_u64[idx].buf, cases_u64[idx].len, &out_un);
      rs = simd_parse_uint64(cases_u64[idx].buf, cases_u64[idx].len, &out_us);
      rw = swar_parse_uint64(cases_u64[idx].buf, cases_u64[idx].len, &out_uw);
      if (rn == NDEC_NUM_OK && rs == 0 && rw == NDEC_NUM_OK) {
        if (out_un != out_us || out_un != out_uw) {
          printf("MISMATCH u64 idx=%d: ndec=%llu simd=%llu swar=%llu\n", idx, (unsigned long long)out_un,
                 (unsigned long long)out_us, (unsigned long long)out_uw);
          errors++;
        }
      }
    }
  }
  return errors;
}

static void run_suite(const char *label, int min_dc, int max_dc, int iters) {
  generate_cases(42 + min_dc * 7 + max_dc, min_dc, max_dc);
  int errors = verify_all();
  if (errors) {
    printf("[%s] VERIFY FAIL: %d\n", label, errors);
    exit(1);
  }
  printf("=== %s (digits %d..%d, %d cases x %d iters) ===\n", label, min_dc, max_dc, num_cases, iters);
  printf("  int64:\n");
  bench_int64_ndec("    byte loop (current)", ndec_parse_int64, iters);
  bench_int64_ndec("    SWAR 8-digit hybrid", swar_parse_int64, iters);
  printf("  uint64:\n");
  bench_uint64_ndec("    byte loop (current)", ndec_parse_uint64, iters);
  bench_uint64_ndec("    SWAR 8-digit hybrid", swar_parse_uint64, iters);
  printf("\n");
}

int main(void) {
  int iters = 200;
  run_suite("digits 1..2 (very short)", 1, 2, iters);
  run_suite("digits 1..4 (short)", 1, 4, iters);
  run_suite("digits 1..8 (Small bench)", 1, 8, iters);
  run_suite("digits 1..18 (mixed)", 1, 18, iters);
  run_suite("digits 8..18 (long-int)", 8, 18, iters);
  run_suite("digits 16..18 (very long)", 16, 18, iters);
  return 0;
}
