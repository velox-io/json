// Scenarios:
//
//   perf 1: Document::Parse                full parse + DOM build (DNode)
//   perf 2: Document::Parse + full walk    parse + recursive visit every token
//
// Same payload as test/bench.c / test/bench_full.c. Build:
//
//   clang++ -std=c++17 -O3 -march=native comparison.cpp \
//           -I$SONIC_CPP/include -o /tmp/sonic_cmp
//   nice -20 /tmp/sonic_cmp

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

int main(int argc, char **argv) {
  int iterations = 50000;
  if (const char *e = getenv("BENCH_ITERS"))
    iterations = atoi(e);

  size_t json_len;
  char *JSON = bench_payload_load(argc, argv, &json_len);

  // Warmup
  {
    for (int i = 0; i < 1000; i++) {
      sonic_json::Document doc;
      doc.Parse(JSON, json_len);
      if (doc.HasParseError()) {
        fprintf(stderr, "warmup parse failed\n");
        return 1;
      }
    }
  }

  printf("\n=== Perf scenarios (%d iters, %zu bytes) ===\n", iterations, json_len);

  // Perf 1: Document::Parse.
  {
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      sonic_json::Document doc;
      doc.Parse(JSON, json_len);
      if (doc.HasParseError()) {
        fprintf(stderr, "parse failed\n");
        return 1;
      }
    }
    uint64_t t1 = now_ns();
    report("perf 1: Document::Parse", t0, t1, iterations, json_len);
  }

  // Perf 2: Document::Parse + full recursive walk.
  {
    size_t sink = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      sonic_json::Document doc;
      doc.Parse(JSON, json_len);
      if (doc.HasParseError()) {
        fprintf(stderr, "parse failed\n");
        return 1;
      }
      walk(doc, &sink);
    }
    uint64_t t1 = now_ns();
    report("perf 2: Document::Parse + full walk", t0, t1, iterations, json_len);
    (void)sink;
  }

  bench_payload_free(JSON);
  return 0;
}
