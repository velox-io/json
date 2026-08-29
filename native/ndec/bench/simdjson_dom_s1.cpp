// True simdjson stage1 microbench.
//
// `build/simdjson stage1` calls ondemand::parser::iterate which runs stage1
// AND sets up the ondemand iterator. This bench instead invokes
// dom::parser::implementation->stage1(buf, len, stage1_mode::regular) directly,
// so we measure the structural indexer in isolation, the same code consumed
// by both DOM and ondemand parsers.
//
// Usage: ITERS=2000 ./simdjson_dom_s1 file.json

#include "simdjson.h"
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <vector>

using namespace simdjson;

static uint64_t now_ns() {
  using clk = std::chrono::steady_clock;
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             clk::now().time_since_epoch())
      .count();
}

static __attribute__((noinline))
size_t run_stage1(dom::parser &p, const padded_string &doc, int iters) {
  size_t ok = 0;
  for (int i = 0; i < iters; i++) {
    error_code ec = p.implementation->stage1(
        reinterpret_cast<const uint8_t *>(doc.data()), doc.size(),
        stage1_mode::regular);
    if (ec == SUCCESS) ok++;
  }
  return ok;
}

int main(int argc, char **argv) {
  if (argc < 2) {
    std::fprintf(stderr, "Usage: %s <json-file> [iters]\n", argv[0]);
    return 1;
  }
  int iters = 10000;
  if (const char *e = std::getenv("ITERS")) iters = std::atoi(e);
  if (iters < 10) iters = 10;

  auto buf_or = padded_string::load(argv[1]);
  if (buf_or.error()) {
    std::fprintf(stderr, "load failed: %s\n", error_message(buf_or.error()));
    return 1;
  }
  const padded_string &doc = buf_or.value_unsafe();

  // Allocate parser buffers once. stage1 will write into
  // implementation->structural_indexes and never reallocates.
  dom::parser parser;
  if (parser.allocate(doc.size()) != SUCCESS) {
    std::fprintf(stderr, "allocate failed\n");
    return 1;
  }

  // Verify
  {
    error_code ec = parser.implementation->stage1(
        reinterpret_cast<const uint8_t *>(doc.data()), doc.size(),
        stage1_mode::regular);
    if (ec != SUCCESS) {
      std::fprintf(stderr, "stage1 verify failed: %s\n", error_message(ec));
      return 1;
    }
    std::printf("Verify: n_structural_indexes=%u\n",
                parser.implementation->n_structural_indexes);
  }

  // Warmup
  run_stage1(parser, doc, iters / 10);

  uint64_t t0 = now_ns();
  size_t ok = run_stage1(parser, doc, iters);
  uint64_t elapsed = now_ns() - t0;
  (void)ok;

  double ns = double(elapsed) / iters;
  double gb = double(doc.size()) * iters / (double(elapsed) / 1e9) / 1e9;
  std::printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n",
              "simdjson stage1 (implementation->stage1, pure)", ns, gb);
  return 0;
}
