/*
 * ndec_bench.c — ndec benchmark suite (base + parse modes)
 *
 * Supports two sub-tests via CLI arg:
 *   ./ndec_bench base   — validate-only skeleton (zero reactor work)
 *   ./ndec_bench parse  — full parse with inline callbacks
 *   ./ndec_bench        — run both sequentially
 *
 * Loads JSON payload from file (see payload.h for path resolution).
 *
 * Compile & run (via Makefile):
 *   make bench
 *   make bench PAYLOAD=path/to/other.json
 *   ./build/ndec_bench base
 *   ./build/ndec_bench parse
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <time.h>

/* Needed by parse reactor before parser.h is included. */
#include "ndec/core/types.h"
#include "ndec/number.h"
#include "payload.h"

#if defined(__aarch64__)
#define NDEC_PARSE_SELECTOR neon
#elif defined(__x86_64__)
#define NDEC_PARSE_SELECTOR avx2
#else
#error "Unsupported architecture"
#endif

/* Define the parser function names */
#define NDEC_FN_NAME_BASE  ndec_parse_base
#define NDEC_FN_NAME_PARSE ndec_parse_parse
#define NDEC_PARSE_BASE    ndec_parse_base
#define NDEC_PARSE_PARSE   ndec_parse_parse

typedef struct StringView {
  const uint8_t *ptr;
  uint32_t len;
} StringView;

typedef struct FullSink {
  double num_sum;
  uint32_t field_count;
  uint32_t bool_sum;
  uint32_t null_count;
  uint32_t view_count;
  StringView views[128];
} FullSink;

static atof_ctx g_bench_atof_ctx; /* shared scratch for ndec_parse_double */
static inline int32_t r_number(void *ud, NdecRawStr raw) {
  FullSink *s = (FullSink *)ud;
  double v;
  (void)ndec_parse_double(raw.ptr, raw.len, &v, &g_bench_atof_ctx);
  s->num_sum += v;
  return NDEC_PROCEED;
}

static inline void r_emit_view(FullSink *s, const uint8_t *src, uint32_t len) {
  uint32_t i      = s->view_count & 127;
  s->views[i].ptr = src;
  s->views[i].len = len;
  s->view_count++;
}

static inline int32_t r_string(void *ud, NdecStrInfo str) {
  r_emit_view((FullSink *)ud, str.raw.ptr, str.raw.len);
  return NDEC_PROCEED;
}
static inline int32_t r_field(void *ud, NdecStrInfo k) {
  FullSink *s = (FullSink *)ud;
  r_emit_view(s, k.raw.ptr, k.raw.len);
  s->field_count++;
  return NDEC_PROCEED;
}
static inline int32_t r_bool(void *ud, int v) {
  ((FullSink *)ud)->bool_sum += v ? 1 : 0;
  return NDEC_PROCEED;
}
static inline int32_t r_null(void *ud) {
  ((FullSink *)ud)->null_count++;
  return NDEC_PROCEED;
}
static inline int32_t r_begin_obj(void *ud) {
  (void)ud;
  return NDEC_PROCEED;
}
static inline int32_t r_end_obj(void *ud) {
  (void)ud;
  return NDEC_PROCEED;
}
static inline int32_t r_begin_arr(void *ud) {
  (void)ud;
  return NDEC_PROCEED;
}
static inline int32_t r_end_arr(void *ud) {
  (void)ud;
  return NDEC_PROCEED;
}

/* Forward declarations for the two parser variants */
void ndec_parse_base(NdecCtx *ctx);
void ndec_parse_parse(NdecCtx *ctx);

/* Include the two parser variants */
#include "ndec_bench_base.h"
#include "ndec_bench_parse.h"

static inline void black_box(void *p) {
  __asm__ volatile("" : : "r"(p) : "memory");
}

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

/* BASE mode: validate-only, zero reactor work */
static void bench_base(const char *json, uint32_t json_len) {
  int iterations        = 50000;
  const char *iters_env = getenv("BENCH_ITERS");
  if (iters_env)
    iterations = atoi(iters_env);

  fprintf(stderr, "JSON payload size: %u bytes\n", json_len);
  fprintf(stderr, "Running %d iterations (BASE mode)...\n", iterations);

  NdecCtx ctx;

  /* Warmup */
  for (int i = 0; i < 1000; i++) {
    ndec_ctx_init(&ctx, NULL, NULL);
    ndec_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    NDEC_PARSE_BASE(&ctx);
    black_box(&ctx);
  }

  /* Benchmark */
  uint64_t start = now_ns();
  for (int i = 0; i < iterations; i++) {
    ndec_ctx_init(&ctx, NULL, NULL);
    ndec_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    NDEC_PARSE_BASE(&ctx);
    black_box(&ctx);
  }
  uint64_t elapsed = now_ns() - start;

  double ns_per_iter = (double)elapsed / iterations;
  double mb_per_sec  = (double)json_len * iterations / ((double)elapsed / 1e9) / 1e6;
  double gb_per_sec  = mb_per_sec / 1000.0;

  fprintf(stderr, "Done.\n\n");
  printf("ndec base (validate-only):\n");
  printf("  %d iterations, %u bytes each\n", iterations, json_len);
  printf("  %.1f ns/iter\n", ns_per_iter);
  printf("  %.1f MB/s (%.2f GB/s)\n", mb_per_sec, gb_per_sec);
}

/* PARSE mode: full parse with inline callbacks */
static void bench_parse(const char *json, uint32_t json_len) {
  int iterations        = 50000;
  const char *iters_env = getenv("BENCH_ITERS");
  if (iters_env)
    iterations = atoi(iters_env);

  fprintf(stderr, "JSON payload size: %u bytes\n", json_len);
  fprintf(stderr, "Running %d iterations (PARSE mode)...\n", iterations);

  NdecCtx ctx;
  FullSink sink = {0};

  /* Warmup */
  for (int i = 0; i < 1000; i++) {
    memset(&sink, 0, sizeof(sink));
    ndec_ctx_init(&ctx, NULL, &sink);
    ndec_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    NDEC_PARSE_PARSE(&ctx);
    black_box(&ctx);
    black_box(&sink);
  }

  /* Benchmark */
  uint64_t start = now_ns();
  for (int i = 0; i < iterations; i++) {
    memset(&sink, 0, sizeof(sink));
    ndec_ctx_init(&ctx, NULL, &sink);
    ndec_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    NDEC_PARSE_PARSE(&ctx);
    black_box(&ctx);
    black_box(&sink);
  }
  uint64_t elapsed = now_ns() - start;

  double ns_per_iter = (double)elapsed / iterations;
  double mb_per_sec  = (double)json_len * iterations / ((double)elapsed / 1e9) / 1e6;
  double gb_per_sec  = mb_per_sec / 1000.0;

  fprintf(stderr, "Done.\n\n");
  printf("ndec parse (inline callbacks):\n");
  printf("  %d iterations, %u bytes each\n", iterations, json_len);
  printf("  %.1f ns/iter\n", ns_per_iter);
  printf("  %.1f MB/s (%.2f GB/s)\n", mb_per_sec, gb_per_sec);
  fprintf(stderr, "sink.num_sum=%.1f views=%u fields=%u bool=%u null=%u\n", sink.num_sum, sink.view_count,
          sink.field_count, sink.bool_sum, sink.null_count);
}

int main(int argc, char **argv) {
  /* Check for mode arguments and extract them before payload loading */
  int run_base = 0, run_parse = 0;
  int payload_argc = 1; /* Keep program name */

  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "base") == 0) {
      run_base = 1;
    } else if (strcmp(argv[i], "parse") == 0) {
      run_parse = 1;
    } else {
      /* Non-mode argument, treat as potential payload path */
      argv[payload_argc++] = argv[i];
    }
  }

  size_t json_len_sz;
  char *json              = bench_payload_load(payload_argc, argv, &json_len_sz);
  const uint32_t json_len = (uint32_t)json_len_sz;

  /* Default: run both if no explicit mode selected */
  if (!run_base && !run_parse) {
    run_base  = 1;
    run_parse = 1;
  }

  if (run_base) {
    bench_base(json, json_len);
    if (run_parse)
      printf("\n");
  }

  if (run_parse) {
    bench_parse(json, json_len);
  }

  bench_payload_free(json);
  return 0;
}
