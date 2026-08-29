/*
 * simdjson dom::parser.parse benchmark.
 *
 * Measures stage1 + tape build + string_buf copy via dom::parser.parse.
 * Pure stage1 (no tape) is in simdjson_dom_s1.cpp.
 *
 * Build: bench/build.sh simdjson_dom_tape
 * Usage: ITERS=10000 ./build/simdjson_dom_tape file.json
 */

#include "simdjson.h"
#include "payload.h"
#include <chrono>
#include <cstdio>
#include <cstring>

using namespace simdjson;

using clk = std::chrono::steady_clock;

static uint64_t now_ns() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(clk::now().time_since_epoch()).count();
}

static __attribute__((noinline)) void run_dom_parse(const padded_string &doc, int iters) {
  dom::parser parser;
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    dom::element root;
    auto err = parser.parse(doc).get(root);
    if (err) {
      fprintf(stderr, "err %d\n", (int)err);
      return;
    }
    sink += (size_t)root.type();
  }
  (void)sink;
}

int main(int argc, char **argv) {
  int iterations = 10000;
  if (const char *e = getenv("ITERS"))
    iterations = atoi(e);
  if (iterations < 10)
    iterations = 10;

  size_t json_len;
  char *JSON = bench_payload_load(argc, argv, &json_len);
  padded_string doc{JSON, json_len};

  run_dom_parse(doc, iterations / 10);

  uint64_t t0 = now_ns();
  run_dom_parse(doc, iterations);
  uint64_t t1 = now_ns();

  double ns = double(t1 - t0) / iterations;
  double gb = double(json_len) * iterations / (double(t1 - t0) / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", "simdjson dom::parser.parse", ns, gb);

  bench_payload_free(JSON);
  return 0;
}
