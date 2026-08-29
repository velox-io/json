/* ndec_dom_tape.c -- DOM parse benchmark (dom.h, stage1 + tape).
 *
 * One binary, two string-storage flavors. Each flavor calls its
 * matching noinline entry point (json_dom_parse_{copy,zc}),
 * which in turn calls its matching noinline walker (dom_build_tape_*).
 * The harness helpers are INLINE so the per-flavor call sites in main
 * specialize end-to-end -- structurally identical to the old
 * per-macro build, just both flavors in one binary.
 *
 * Two measurement modes, mirroring the original macro-per-TU bench:
 *   tape  parse-only: src is read-only, no per-iter memcpy. Emitted
 *                     for the COPY flavor only; ZC matches it on this
 *                     path since neither mutates the buffer. Used by
 *                     run.sh's `tape` namespace for cross-engine
 *                     comparison.
 *   dom   e2e: per-iter memcpy of src -> work, then parse work.
 *                     Neither flavor needs the copy, but it keeps this
 *                     number comparable to the other engines' e2e
 *                     figures. Emitted once per flavor as
 *                     `dom (copy|zc)`. Used by run.sh's `dom`
 *                     namespace for flavor comparison.
 *
 * Output label contract for run.sh:
 *   - first "GB/s" line MUST be the `tape` (parse-only) number, so
 *     run_tape's parse_gb (which takes the first match) picks it up.
 *   - flavor e2e lines contain "dom (flavor)" so run_ndec_dom's
 *     parse_gb_at "dom (copy)" etc. can select each.
 *
 * Build: bench/build.sh ndec_dom_tape
 * Usage: ITERS=2000 taskset -c 2 build/ndec_dom_tape file.json
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "ndec/dom.h"
#include "../test/ndec_libc_allocator.h"

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

/* Parse-only loop: no per-iter memcpy. Only valid for flavors that do
 * not mutate the buffer (COPY, ZC). `entry` is the flavor's noinline
 * parse entry point; passing it as a function pointer (rather than a
 * mode enum) keeps the dispatch compile-time-literal at each call site
 * -- the INLINE marker below lets the caller specialize. */
typedef int (*parse_fn)(json_dom *d, const uint8_t *buf, size_t len);

INLINE int run_parse_only(json_dom *d, const uint8_t *buf, size_t len, int iters, parse_fn entry) {
  for (int i = 0; i < iters; i++) {
    int err = entry(d, buf, len);
    if (err) return err;
  }
  return 0;
}

/* E2e loop: per-iter memcpy of src -> work, then parse work. Neither
 * flavor requires the copy; it is here to match the other engines'
 * e2e accounting. */
INLINE int run_parse_e2e(json_dom *d, uint8_t *work, const uint8_t *src, size_t len,
                         int iters, parse_fn entry) {
  for (int i = 0; i < iters; i++) {
    __builtin_memcpy(work, src, len + 64);
    int err = entry(d, work, len);
    if (err) return err;
  }
  return 0;
}

INLINE void bench_parse_only(const char *label, json_dom *d, const uint8_t *buf, size_t len,
                             int iters, parse_fn entry) {
  run_parse_only(d, buf, len, iters / 10, entry);
  uint64_t t0      = now_ns();
  int err          = run_parse_only(d, buf, len, iters, entry);
  uint64_t elapsed = now_ns() - t0;
  if (err) { fprintf(stderr, "%s: parse error: %d\n", label, err); return; }
  double ns = (double)elapsed / iters;
  double gb = (double)len * iters / ((double)elapsed / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", label, ns, gb);
}

INLINE void bench_e2e(const char *label, json_dom *d, uint8_t *work, const uint8_t *src,
                      size_t len, int iters, parse_fn entry) {
  run_parse_e2e(d, work, src, len, iters / 10, entry);
  uint64_t t0      = now_ns();
  int err          = run_parse_e2e(d, work, src, len, iters, entry);
  uint64_t elapsed = now_ns() - t0;
  if (err) { fprintf(stderr, "%s: parse error: %d\n", label, err); return; }
  double ns = (double)elapsed / iters;
  double gb = (double)len * iters / ((double)elapsed / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", label, ns, gb);
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "Usage: %s <json> [iters]\n", argv[0]);
    return 1;
  }
  int iters = 1000;
  if (argc >= 3) iters = atoi(argv[2]);
  const char *e = getenv("ITERS");
  if (e) iters = atoi(e);
  if (iters < 10) iters = 10;

  FILE *f = fopen(argv[1], "rb");
  if (!f) { perror("fopen"); return 1; }
  fseek(f, 0, SEEK_END);
  size_t len = (size_t)ftell(f);
  fseek(f, 0, SEEK_SET);
  uint8_t *src  = (uint8_t *)malloc(len + 64);
  uint8_t *work = (uint8_t *)malloc(len + 64);
  if (fread(src, 1, len, f) != len) { perror("fread"); return 1; }
  fclose(f);
  memset(src + len, ' ', 64);
  memcpy(work, src, len + 64);

  json_dom d;
  memset(&d, 0, sizeof(d));
  d.allocator = NDEC_LIBC_ALLOCATOR;
  /* Sizing covers both flavors; it does not depend on the mode. */
  if (dom_ensure_capacity(&d, len)) {
    fprintf(stderr, "ensure_capacity failed\n");
    return 1;
  }

  /* Verify once on COPY (str_arena-backed, no mutation, so `work` survives). */
  int err = json_dom_parse_copy(&d, work, len);
  if (err) { fprintf(stderr, "parse failed: %d\n", err); return 1; }
  printf("Verify: tape_len=%zu str_used=%zu n_struct=%u\n",
         d.emit.doc.tape_len, d.emit.doc.str_used, d.n_structural_indexes);

  /* parse-only: COPY only (cross-engine `tape` namespace representative).
   * First GB/s line in the output -- run_tape's parse_gb picks it up. */
  bench_parse_only("tape", &d, work, len, iters, json_dom_parse_copy);

  /* e2e: both flavors, labeled `dom (flavor)` for run_ndec_dom. */
  bench_e2e("dom (copy)", &d, work, src, len, iters, json_dom_parse_copy);
  bench_e2e("dom (zc)",   &d, work, src, len, iters, json_dom_parse_zc);

  json_dom_free(&d);
  free(src);
  free(work);
  return 0;
}
