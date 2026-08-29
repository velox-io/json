/* ndec_dom_s1.c -- benchmark dom_scan_structurals (extract.h) directly.
 *
 * Drives the stage-1 structural indexer in isolation: no dom.h, no tape build.
 *
 * Build: bench/build.sh ndec_dom_s1
 * Run:   ITERS=2000 taskset -c 2 build/ndec_dom_s1 file.json
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "ndec/core/extract.h"

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

static __attribute__((noinline)) int run_stage1(const uint8_t *buf, size_t len, uint32_t *idx, uint32_t cap,
                                                uint32_t *out_count, int iters) {
  for (int i = 0; i < iters; i++) {
    *out_count = 0;
    int err    = ndec_scan_structurals(buf, len, idx, out_count, cap);
    if (err)
      return err;
  }
  return 0;
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "Usage: %s <json>\n", argv[0]);
    return 1;
  }
  int iters     = 10000;
  const char *e = getenv("ITERS");
  if (e)
    iters = atoi(e);
  if (iters < 10)
    iters = 10;

  FILE *f = fopen(argv[1], "rb");
  if (!f) {
    perror("fopen");
    return 1;
  }
  fseek(f, 0, SEEK_END);
  size_t len = (size_t)ftell(f);
  fseek(f, 0, SEEK_SET);
  uint8_t *buf = (uint8_t *)malloc(len);
  if (fread(buf, 1, len, f) != len) {
    perror("fread");
    return 1;
  }
  fclose(f);

  uint32_t cap  = (uint32_t)(len + 64);
  uint32_t *idx = (uint32_t *)malloc((size_t)cap * sizeof(uint32_t));
  uint32_t cnt  = 0;

  /* Verify */
  if (run_stage1(buf, len, idx, cap, &cnt, 1)) {
    fprintf(stderr, "stage1 failed\n");
    return 1;
  }
  printf("Verify: count=%u\n", cnt);

  /* Warmup */
  run_stage1(buf, len, idx, cap, &cnt, iters / 10);

  uint64_t t0 = now_ns();
  run_stage1(buf, len, idx, cap, &cnt, iters);
  uint64_t elapsed = now_ns() - t0;

  double ns = (double)elapsed / iters;
  double gb = (double)len * iters / ((double)elapsed / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", "extract.h (dom_scan_structurals)", ns, gb);

  free(idx);
  free(buf);
  return 0;
}
