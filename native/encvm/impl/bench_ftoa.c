/*
 * bench_ftoa.c - Benchmark comparing Ryu vs Uscale float-to-string
 *
 * Compile: cc -O3 -o bench_ftoa bench_ftoa.c -lm
 * Run:     ./bench_ftoa
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <time.h>
#include <float.h>

/* Include both implementations */
#include "ryu.h"
#include "uscale.h"

/* ================================================================
 *  Timing helpers
 * ================================================================ */

static inline uint64_t now_ns(void) {
#if defined(__APPLE__)
    /* Use mach_absolute_time on macOS for high-resolution timing */
    #include <mach/mach_time.h>
    static mach_timebase_info_data_t info;
    if (info.denom == 0) mach_timebase_info(&info);
    return mach_absolute_time() * info.numer / info.denom;
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
#endif
}

/* Prevent compiler from optimizing away the result */
static volatile int sink;

/* ================================================================
 *  Test data
 * ================================================================ */

/* Simple xoshiro256** PRNG for reproducible random doubles */
static uint64_t rng_state[4] = {
    0x180ec6d33cfd0abaULL, 0xd5a61266f0c9392cULL,
    0xa9582618e03fc9aaULL, 0x39abdc4529b1661cULL
};

static inline uint64_t rotl64(uint64_t x, int k) {
    return (x << k) | (x >> (64 - k));
}

static uint64_t rng_next(void) {
    uint64_t *s = rng_state;
    const uint64_t result = rotl64(s[1] * 5, 7) * 9;
    const uint64_t t = s[1] << 17;
    s[2] ^= s[0]; s[3] ^= s[1]; s[1] ^= s[2]; s[0] ^= s[3];
    s[2] ^= t;
    s[3] = rotl64(s[3], 45);
    return result;
}

static double random_double(void) {
    uint64_t bits;
    double d;
    for (;;) {
        bits = rng_next();
        /* Clear sign bit, ensure not NaN/Inf */
        bits &= 0x7FFFFFFFFFFFFFFFULL;
        if ((bits >> 52) != 0x7FF) {
            __builtin_memcpy(&d, &bits, 8);
            if (d != 0.0) return d;
        }
    }
}

static float random_float(void) {
    uint32_t bits;
    float f;
    for (;;) {
        bits = (uint32_t)rng_next();
        bits &= 0x7FFFFFFFU;
        if ((bits >> 23) != 0xFF) {
            __builtin_memcpy(&f, &bits, 4);
            if (f != 0.0f) return f;
        }
    }
}

/* ================================================================
 *  Correctness check
 * ================================================================ */

static int correctness_errors = 0;

static void check_f64(double val, const char *label) {
    uint8_t buf_ryu[512];
    uint8_t buf_us[512];

    int n_ryu = vj_write_float64(buf_ryu, val);
    int n_us  = us_write_float64(buf_us, val);

    buf_ryu[n_ryu] = '\0';
    buf_us[n_us] = '\0';

    if (n_ryu != n_us || memcmp(buf_ryu, buf_us, n_ryu) != 0) {
        correctness_errors++;
        if (correctness_errors <= 20) {
            uint64_t bits;
            __builtin_memcpy(&bits, &val, 8);
            printf("  MISMATCH f64 [%s] val=%.17g bits=0x%016llx\n",
                   label, val, (unsigned long long)bits);
            printf("    ryu:    \"%s\" (len=%d)\n", buf_ryu, n_ryu);
            printf("    uscale: \"%s\" (len=%d)\n", buf_us, n_us);
        }
    }
}

static void check_f32(float val, const char *label) {
    uint8_t buf_ryu[128];
    uint8_t buf_us[128];

    int n_ryu = vj_write_float32(buf_ryu, val);
    int n_us  = us_write_float32(buf_us, val);

    buf_ryu[n_ryu] = '\0';
    buf_us[n_us] = '\0';

    if (n_ryu != n_us || memcmp(buf_ryu, buf_us, n_ryu) != 0) {
        correctness_errors++;
        if (correctness_errors <= 20) {
            uint32_t bits;
            __builtin_memcpy(&bits, &val, 4);
            printf("  MISMATCH f32 [%s] val=%.9g bits=0x%08x\n",
                   label, val, bits);
            printf("    ryu:    \"%s\" (len=%d)\n", buf_ryu, n_ryu);
            printf("    uscale: \"%s\" (len=%d)\n", buf_us, n_us);
        }
    }
}

static void run_correctness(void) {
    printf("=== Correctness Check ===\n");
    correctness_errors = 0;

    /* Specific float64 values */
    double f64_tests[] = {
        0.0, -0.0, 1.0, -1.0, 0.1, 0.01, 0.001,
        3.14, 3.14159265358979, 100.0, 1000000.0,
        1e10, 1e20, 1e50, 1e100, 1e200, 1e300,
        1e-10, 1e-20, 1e-50, 1e-100, 1e-200, 1e-300,
        5e-324,  /* smallest subnormal */
        2.2250738585072014e-308,  /* smallest normal */
        1.7976931348623157e+308,  /* largest normal */
        2.2250738585072009e-308,  /* largest subnormal */
        0.5, 0.25, 0.125,
        1.0/3.0, 2.0/3.0,
        42.0, 999999999999999.0,
        1.23456789012345678,
        9.999999999999998,
        0.3,
    };
    int n_f64 = sizeof(f64_tests) / sizeof(f64_tests[0]);
    for (int i = 0; i < n_f64; i++) {
        check_f64(f64_tests[i], "specific");
        if (f64_tests[i] > 0) check_f64(-f64_tests[i], "specific-neg");
    }

    /* Random float64 values */
    for (int i = 0; i < 100000; i++) {
        double d = random_double();
        check_f64(d, "random-f64");
        check_f64(-d, "random-f64-neg");
    }

    /* Specific float32 values */
    float f32_tests[] = {
        0.0f, -0.0f, 1.0f, -1.0f, 0.1f, 0.01f,
        3.14f, 100.0f, 1e10f, 1e20f, 1e30f, 1e38f,
        1e-10f, 1e-20f, 1e-30f, 1e-38f,
        1.17549435e-38f,  /* smallest normal */
        1.4012985e-45f,   /* smallest subnormal */
        3.4028235e+38f,   /* largest */
        0.5f, 0.25f, 42.0f,
    };
    int n_f32 = sizeof(f32_tests) / sizeof(f32_tests[0]);
    for (int i = 0; i < n_f32; i++) {
        check_f32(f32_tests[i], "specific");
        if (f32_tests[i] > 0) check_f32(-f32_tests[i], "specific-neg");
    }

    /* Random float32 values */
    for (int i = 0; i < 100000; i++) {
        float f = random_float();
        check_f32(f, "random-f32");
        check_f32(-f, "random-f32-neg");
    }

    if (correctness_errors == 0) {
        printf("  All tests passed (200000+ random + specific values)\n");
    } else {
        printf("  %d ERRORS found!\n", correctness_errors);
    }
    printf("\n");
}

/* ================================================================
 *  Benchmark runner
 * ================================================================ */

#define BENCH_ITERS 2000000
#define WARMUP_ITERS 10000

typedef int (*f64_writer)(uint8_t *, double);
typedef int (*f32_writer)(uint8_t *, float);

static double bench_f64(f64_writer fn, double *values, int nvals) {
    uint8_t buf[512];
    int total = 0;

    /* Warmup */
    for (int i = 0; i < WARMUP_ITERS; i++) {
        total += fn(buf, values[i % nvals]);
    }
    sink = total;

    /* Measure */
    uint64_t start = now_ns();
    total = 0;
    for (int i = 0; i < BENCH_ITERS; i++) {
        total += fn(buf, values[i % nvals]);
    }
    uint64_t elapsed = now_ns() - start;
    sink = total;

    return (double)elapsed / BENCH_ITERS;
}

static double bench_f32(f32_writer fn, float *values, int nvals) {
    uint8_t buf[128];
    int total = 0;

    /* Warmup */
    for (int i = 0; i < WARMUP_ITERS; i++) {
        total += fn(buf, values[i % nvals]);
    }
    sink = total;

    /* Measure */
    uint64_t start = now_ns();
    total = 0;
    for (int i = 0; i < BENCH_ITERS; i++) {
        total += fn(buf, values[i % nvals]);
    }
    uint64_t elapsed = now_ns() - start;
    sink = total;

    return (double)elapsed / BENCH_ITERS;
}

static void bench_category_f64(const char *name, double *vals, int nvals) {
    double ryu_ns    = bench_f64(vj_write_float64, vals, nvals);
    double uscale_ns = bench_f64(us_write_float64, vals, nvals);
    double ratio = uscale_ns / ryu_ns;
    printf("  %-24s  ryu: %6.1f ns   uscale: %6.1f ns   ratio: %.2fx\n",
           name, ryu_ns, uscale_ns, ratio);
}

static void bench_category_f32(const char *name, float *vals, int nvals) {
    double ryu_ns    = bench_f32(vj_write_float32, vals, nvals);
    double uscale_ns = bench_f32(us_write_float32, vals, nvals);
    double ratio = uscale_ns / ryu_ns;
    printf("  %-24s  ryu: %6.1f ns   uscale: %6.1f ns   ratio: %.2fx\n",
           name, ryu_ns, uscale_ns, ratio);
}

/* ================================================================
 *  Main
 * ================================================================ */

int main(void) {
    printf("Float-to-String Benchmark: Ryu vs Uscale (Unrounded Scaling)\n");
    printf("Iterations per test: %d\n\n", BENCH_ITERS);

    /* --- Correctness --- */
    run_correctness();
    if (correctness_errors > 0) {
        printf("Skipping benchmarks due to correctness errors.\n");
        return 1;
    }

    /* --- Float64 benchmarks --- */
    printf("=== Float64 Benchmarks ===\n");

    {
        double vals[] = {1.0, 42.0, 100.0, 1000000.0, 123456789.0};
        bench_category_f64("small integers", vals, 5);
    }
    {
        double vals[] = {0.1, 3.14, 0.001, 1.23456789, 0.3};
        bench_category_f64("common fractions", vals, 5);
    }
    {
        double vals[] = {1e50, 1e100, 1e200, 1e300, 1.7976931348623157e308};
        bench_category_f64("large numbers", vals, 5);
    }
    {
        double vals[] = {1e-50, 1e-100, 1e-200, 1e-300, 5e-324};
        bench_category_f64("small numbers", vals, 5);
    }
    {
        double vals[] = {
            2.2250738585072009e-308, /* largest subnormal */
            5e-324,                   /* smallest subnormal */
            1e-310, 1e-320, 4.9e-324
        };
        bench_category_f64("subnormals", vals, 5);
    }
    {
        /* Generate random doubles */
        #define NRAND 256
        double rand_vals[NRAND];
        for (int i = 0; i < NRAND; i++) rand_vals[i] = random_double();
        bench_category_f64("random (uniform bits)", rand_vals, NRAND);
    }

    printf("\n=== Float32 Benchmarks ===\n");
    {
        float vals[] = {1.0f, 42.0f, 100.0f, 1000000.0f, 12345.0f};
        bench_category_f32("small integers", vals, 5);
    }
    {
        float vals[] = {0.1f, 3.14f, 0.001f, 1.23456f, 0.3f};
        bench_category_f32("common fractions", vals, 5);
    }
    {
        float vals[] = {1e10f, 1e20f, 1e30f, 1e38f, 3.4028235e38f};
        bench_category_f32("large numbers", vals, 5);
    }
    {
        float vals[] = {1e-10f, 1e-20f, 1e-30f, 1e-38f, 1.4012985e-45f};
        bench_category_f32("small numbers", vals, 5);
    }
    {
        float rand_vals[NRAND];
        for (int i = 0; i < NRAND; i++) rand_vals[i] = random_float();
        bench_category_f32("random (uniform bits)", rand_vals, NRAND);
    }

    printf("\nNote: ratio < 1.0 means uscale is faster\n");
    return 0;
}
