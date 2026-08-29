/*
 * Pure structural validation benchmark.
 *
 * Drives ndec_sax_parse_base over a JSON payload in a tight loop.
 * No reactor callbacks fire, so it measures the lower bound of parsing
 * cost (stage 1 + sax state machine + string scan).
 *
 * Build: bench/build.sh ndec_sax_base
 * Usage: ITERS=10000 ./build/ndec_sax_base file.json
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "payload.h"

#define NDEC_FN_NAME ndec_sax_parse_base
#include "ndec/core/sax.h" // IWYU pragma: keep

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

__attribute__((noinline)) static void run_base(const char *json, uint32_t json_len, int iters) {
  NdecSaxContext ctx;
  for (int i = 0; i < iters; i++) {
    ndec_sax_ctx_init(&ctx, NULL, NULL);
    ndec_sax_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    ndec_sax_parse_base(&ctx);
  }
}

int main(int argc, char **argv) {
  int iterations = 10000;
  const char *e  = getenv("ITERS");
  if (e)
    iterations = atoi(e);
  if (iterations < 10)
    iterations = 10;

  size_t json_len;
  char *json = bench_payload_load(argc, argv, &json_len);

  run_base(json, (uint32_t)json_len, iterations / 10);

  uint64_t t0       = now_ns();
  run_base(json, (uint32_t)json_len, iterations);
  uint64_t elapsed  = now_ns() - t0;

  double ns = (double)elapsed / iterations;
  double gb = (double)json_len * iterations / ((double)elapsed / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", "ndec sax base (structural validation)", ns, gb);

  bench_payload_free(json);
  return 0;
}
