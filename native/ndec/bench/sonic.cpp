// Scenarios:
//
//   perf 1: Document::Parse                full parse + DOM build (DNode)
//   perf 2: Document::Parse + full walk    parse + recursive visit every token
//
// Build: test/build.sh sonic

#include "sonic/sonic.h"
#include "payload.h"
#include <chrono>
#include <cstdio>

using clk = std::chrono::steady_clock;

static uint64_t now_ns() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(clk::now().time_since_epoch()).count();
}

static void report(const char *label, uint64_t t0, uint64_t t1, int iters, size_t bytes) {
  double ns  = double(t1 - t0) / iters;
  double mbs = double(bytes) * iters / (double(t1 - t0) / 1e9) / 1e6;
  printf("  %-46s %7.1f ns/iter  %6.2f GB/s\n", label, ns, mbs / 1000.0);
}

using Node = sonic_json::DNode<>;

static void walk(const Node &v, size_t *sink) {
  if (v.IsObject()) {
    for (auto m = v.MemberBegin(); m != v.MemberEnd(); ++m) {
      auto key = m->name.GetStringView();
      *sink += key.size();
      walk(m->value, sink);
    }
  } else if (v.IsArray()) {
    for (auto it = v.Begin(); it != v.End(); ++it) {
      walk(*it, sink);
    }
  } else if (v.IsString()) {
    *sink += v.GetStringView().size();
  } else if (v.IsNumber()) {
    *sink += (size_t)v.GetDouble();
  } else if (v.IsBool()) {
    *sink += v.GetBool() ? 1 : 0;
  } else if (v.IsNull()) {
    *sink += 1;
  }
}

// Each perf scenario is a noinline function so the compiler cannot
// share registers or eliminate the parse in perf 1.

static __attribute__((noinline)) void
perf1_parse(const char *json, size_t json_len, int iters) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    sonic_json::Document doc;
    doc.Parse(json, json_len);
    if (doc.HasParseError()) { fprintf(stderr, "parse failed\n"); return; }
    sink += (size_t)doc.GetType();
  }
  (void)sink;
}

static __attribute__((noinline)) void
perf2_parse_walk(const char *json, size_t json_len, int iters, size_t *out_sink) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    sonic_json::Document doc;
    doc.Parse(json, json_len);
    if (doc.HasParseError()) { fprintf(stderr, "parse failed\n"); return; }
    walk(doc, &sink);
  }
  *out_sink = sink;
}

int main(int argc, char **argv) {
  int iterations = 50000;
  if (const char *e = getenv("ITERS"))
    iterations = atoi(e);

  size_t json_len;
  char *JSON = bench_payload_load(argc, argv, &json_len);

  // Warmup each scenario so CPU freq is stable.
  {
    int warm = iterations / 10;
    perf1_parse(JSON, json_len, warm);
    size_t sink;
    perf2_parse_walk(JSON, json_len, warm, &sink);
  }

  printf("\n=== Perf scenarios (%d iters, %zu bytes) ===\n", iterations, json_len);

  {
    uint64_t t0 = now_ns();
    perf1_parse(JSON, json_len, iterations);
    uint64_t t1 = now_ns();
    report("perf 1: Document::Parse", t0, t1, iterations, json_len);
  }

  {
    size_t sink = 0;
    uint64_t t0 = now_ns();
    perf2_parse_walk(JSON, json_len, iterations, &sink);
    uint64_t t1 = now_ns();
    report("perf 2: Document::Parse + full walk", t0, t1, iterations, json_len);
    printf("sink:%zu\n", sink);
  }

  bench_payload_free(JSON);
  return 0;
}
