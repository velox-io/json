// Scenarios:
//
//   perf 1: yyjson_read (non-insitu)   full parse + DOM build
//   perf 2: yyjson_read (insitu)       full parse + DOM build, modify input buffer
//   perf 3: yyjson_read + full walk    parse + recursive visit every token
//
// Same 1031 byte payload as test/bench.c / test/bench_full.c. Build:
//
//   clang -O3 -march=native comparison.cpp \
//         $YYJSON/src/yyjson.c -I$YYJSON/src -o /tmp/yyjson_cmp
//   nice -20 /tmp/yyjson_cmp

#define YYJSON_IMPLEMENTATION
#include "yyjson.h"
#include "payload.h"
#include <chrono>
#include <cstdio>
#include <cstring>

using clk = std::chrono::steady_clock;

static uint64_t now_ns() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(clk::now().time_since_epoch()).count();
}

static void report(const char *label, uint64_t t0, uint64_t t1, int iters, size_t bytes) {
  double ns  = double(t1 - t0) / iters;
  double mbs = double(bytes) * iters / (double(t1 - t0) / 1e9) / 1e6;
  printf("  %-46s %7.1f ns/iter  %6.2f GB/s\n", label, ns, mbs / 1000.0);
}

static void walk(yyjson_val *val, size_t *sink) {
  switch (yyjson_get_type(val)) {
  case YYJSON_TYPE_OBJ: {
    size_t idx, max;
    yyjson_val *key, *elem;
    yyjson_obj_foreach(val, idx, max, key, elem) {
      *sink += yyjson_get_len(key);
      walk(elem, sink);
    }
    break;
  }
  case YYJSON_TYPE_ARR: {
    size_t idx, max;
    yyjson_val *elem;
    yyjson_arr_foreach(val, idx, max, elem) {
      walk(elem, sink);
    }
    break;
  }
  case YYJSON_TYPE_STR:
    *sink += yyjson_get_len(val);
    break;
  case YYJSON_TYPE_NUM:
    *sink += (size_t)yyjson_get_num(val);
    break;
  case YYJSON_TYPE_BOOL:
    *sink += yyjson_get_bool(val) ? 1 : 0;
    break;
  case YYJSON_TYPE_NULL:
    *sink += 1;
    break;
  default:
    break;
  }
}

int main(int argc, char **argv) {
  int iterations = 50000;
  if (const char *e = getenv("BENCH_ITERS"))
    iterations = atoi(e);

  size_t json_len;
  char *JSON = bench_payload_load(argc, argv, &json_len);
  fprintf(stderr, "JSON payload size: %zu bytes\n", json_len);

  // Mutable buffer for insitu mode.
  char *buf_insitu = (char *)malloc(json_len + 1);
  if (!buf_insitu) {
    fprintf(stderr, "malloc failed\n");
    return 1;
  }

  // Warmup
  {
    for (int i = 0; i < 1000; i++) {
      yyjson_doc *doc = yyjson_read(JSON, json_len, YYJSON_READ_NOFLAG);
      if (!doc) {
        fprintf(stderr, "warmup parse failed\n");
        return 1;
      }
      yyjson_doc_free(doc);
    }
  }

  printf("\n=== Perf scenarios (%d iters, %zu bytes) ===\n", iterations, json_len);

  // Perf 1: yyjson_read (non-insitu).
  {
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      yyjson_doc *doc = yyjson_read(JSON, json_len, YYJSON_READ_NOFLAG);
      if (!doc) {
        fprintf(stderr, "parse failed\n");
        return 1;
      }
      yyjson_doc_free(doc);
    }
    uint64_t t1 = now_ns();
    report("perf 1: yyjson_read (non-insitu)", t0, t1, iterations, json_len);
  }

  // Perf 2: yyjson_read_opts (insitu).
  {
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      memcpy(buf_insitu, JSON, json_len);
      buf_insitu[json_len] = '\0';
      yyjson_doc *doc      = yyjson_read_opts(buf_insitu, json_len, YYJSON_READ_INSITU, NULL, NULL);
      if (!doc) {
        fprintf(stderr, "insitu parse failed\n");
        return 1;
      }
      yyjson_doc_free(doc);
    }
    uint64_t t1 = now_ns();
    report("perf 2: yyjson_read (insitu)", t0, t1, iterations, json_len);
  }

  // Perf 3: yyjson_read + full recursive walk.
  {
    size_t sink = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      yyjson_doc *doc = yyjson_read(JSON, json_len, YYJSON_READ_NOFLAG);
      if (!doc) {
        fprintf(stderr, "parse failed\n");
        return 1;
      }
      walk(doc->root, &sink);
      yyjson_doc_free(doc);
    }
    uint64_t t1 = now_ns();
    report("perf 3: yyjson_read + full walk", t0, t1, iterations, json_len);
    (void)sink;
  }

  // Perf 4: yyjson_read (insitu) + full recursive walk.
  {
    size_t sink = 0;
    uint64_t t0 = now_ns();
    for (int i = 0; i < iterations; i++) {
      memcpy(buf_insitu, JSON, json_len);
      buf_insitu[json_len] = '\0';
      yyjson_doc *doc      = yyjson_read_opts(buf_insitu, json_len, YYJSON_READ_INSITU, NULL, NULL);
      if (!doc) {
        fprintf(stderr, "insitu parse failed\n");
        return 1;
      }
      walk(doc->root, &sink);
      yyjson_doc_free(doc);
    }
    uint64_t t1 = now_ns();
    report("perf 4: yyjson_read (insitu) + full walk", t0, t1, iterations, json_len);
    (void)sink;
  }

  free(buf_insitu);
  bench_payload_free(JSON);
  return 0;
}
