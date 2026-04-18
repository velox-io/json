// Progressive scenarios, cheapest to strictest:
//
//   perf 1: stage1 only           (SIMD structural scan, no validation)
//   perf 2: dom::parser.parse     (stage1 + tape + string_buf copy)
//   perf 3: ondemand full walk    (stage1 + parse every token)
//   perf 4: dom::parser + walk    (tape build + recursive tape read)
//
// Same 1031 byte payload as test/bench.c / test/bench_full.c. Build:
//
//   clang++ -std=c++17 -O3 -march=native comparison.cpp \
//           $SIMDJSON/singleheader/simdjson.cpp \
//           -I$SIMDJSON/singleheader -o /tmp/simdjson_cmp
//   nice -20 /tmp/simdjson_cmp

#include "simdjson.h"
#include "payload.h"
#include <chrono>
#include <cstdio>

using namespace simdjson;

using clk = std::chrono::steady_clock;

static uint64_t now_ns() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(clk::now().time_since_epoch()).count();
}

static void report(const char *label, uint64_t t0, uint64_t t1, int iters, size_t bytes) {
  double ns  = double(t1 - t0) / iters;
  double mbs = double(bytes) * iters / (double(t1 - t0) / 1e9) / 1e6;
  printf("  %-46s %7.1f ns/iter  %6.2f GB/s\n", label, ns, mbs / 1000.0);
}

static void walk(ondemand::value v, size_t *sink) {
  switch (v.type()) {
  case ondemand::json_type::object: {
    for (auto field : v.get_object()) {
      auto key = field.unescaped_key();
      *sink += key.value().size();
      walk(field.value(), sink);
    }
    break;
  }
  case ondemand::json_type::array: {
    for (auto e : v.get_array())
      walk(e.value(), sink);
    break;
  }
  case ondemand::json_type::string: {
    auto s = v.get_string();
    *sink += s.value().size();
    break;
  }
  case ondemand::json_type::number: {
    double d;
    (void)v.get_double().get(d);
    *sink += (size_t)d;
    break;
  }
  case ondemand::json_type::boolean: {
    bool b;
    (void)v.get_bool().get(b);
    *sink += b ? 1 : 0;
    break;
  }
  case ondemand::json_type::null:
    *sink += 1;
    break;
  default:
    break;
  }
}

static void dom_walk(dom::element el, size_t *sink) {
  switch (el.type()) {
  case dom::element_type::OBJECT: {
    dom::object obj;
    if (el.get(obj))
      break;
    for (auto [key, val] : obj) {
      *sink += key.size();
      dom_walk(val, sink);
    }
    break;
  }
  case dom::element_type::ARRAY: {
    dom::array arr;
    if (el.get(arr))
      break;
    for (auto v : arr)
      dom_walk(v, sink);
    break;
  }
  case dom::element_type::STRING: {
    std::string_view s;
    if (!el.get(s))
      *sink += s.size();
    break;
  }
  case dom::element_type::INT64:
  case dom::element_type::UINT64:
  case dom::element_type::DOUBLE: {
    double d;
    if (!el.get(d))
      *sink += (size_t)d;
    break;
  }
  case dom::element_type::BOOL: {
    bool b;
    if (!el.get(b))
      *sink += b ? 1 : 0;
    break;
  }
  case dom::element_type::NULL_VALUE:
    *sink += 1;
    break;
  default:
    break;
  }
}

static void run_perf(const padded_string &doc, size_t json_len) {
  int iterations = 50000;
  if (const char *e = getenv("BENCH_ITERS"))
    iterations = atoi(e);

  printf("\n=== Perf scenarios (%d iters, %zu bytes) ===\n", iterations, json_len);

  // Warmup
  {
    dom::parser warm;
    for (int i = 0; i < 1000; i++) {
      dom::element root;
      (void)warm.parse(doc).get(root);
    }
  }

  // Perf 1: stage1 only (ondemand iterate without walk).
  {
    ondemand::parser parser;
    size_t ok   = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      auto d = parser.iterate(doc);
      if (!d.error())
        ok++;
    }
    uint64_t t1 = now_ns();
    report("perf 1: ondemand iterate only", t0, t1, iterations, json_len);
    (void)ok;
  }

  // Perf 2: dom::parser.parse.
  {
    dom::parser parser;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      dom::element root;
      auto err = parser.parse(doc).get(root);
      if (err) {
        fprintf(stderr, "err %d\n", (int)err);
        return;
      }
    }
    uint64_t t1 = now_ns();
    report("perf 2: dom::parser.parse (DOM)", t0, t1, iterations, json_len);
  }

  // Perf 3: ondemand full recursive walk.
  {
    ondemand::parser parser;
    size_t sink = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      auto doc2            = parser.iterate(doc);
      ondemand::value root = doc2.get_value();
      walk(root, &sink);
    }
    uint64_t t1 = now_ns();
    report("perf 3: ondemand + full walk", t0, t1, iterations, json_len);
    (void)sink;
  }

  // Perf 4: dom::parser.parse + full recursive walk over the built tape.
  {
    dom::parser parser;
    size_t sink = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      dom::element root;
      auto err = parser.parse(doc).get(root);
      if (err) {
        fprintf(stderr, "err %d\n", (int)err);
        return;
      }
      dom_walk(root, &sink);
    }
    uint64_t t1 = now_ns();
    report("perf 4: dom::parser.parse + full walk", t0, t1, iterations, json_len);
    (void)sink;
  }
}

int main(int argc, char **argv) {
  size_t json_len;
  char *JSON = bench_payload_load(argc, argv, &json_len);
  padded_string doc{JSON, json_len};

  run_perf(doc, json_len);
  bench_payload_free(JSON);
  return 0;
}
