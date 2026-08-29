// Scenarios:
//
//   perf 1: yyjson_read                full parse + DOM build
//   perf 2: yyjson_read (insitu)       full parse + DOM build, modify input buffer
//   perf 3: yyjson_read + full walk    parse + recursive visit every token
//   perf 4: yyjson_read (insitu) + walk
//
// Usage:
//   ./build/yyjson file.json                           # run all scenarios
//   ./build/yyjson insitu file.json                     # run only insitu (for profiling)
//   ITERS=100000 ./build/yyjson read-walk file.json
//
// Build: test/build.sh yyjson

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

// Each perf scenario is wrapped in a noinline function to isolate
// register allocation and prevent cross-scenario LICM / dead-code
// elimination at -O3.

static __attribute__((noinline)) void perf1_read(const char *json, size_t json_len, int iters) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    yyjson_doc *doc = yyjson_read(json, json_len, 0);
    if (!doc) {
      fprintf(stderr, "parse failed\n");
      return;
    }
    sink += (size_t)yyjson_get_type(doc->root);
    yyjson_doc_free(doc);
  }
  (void)sink;
}

static __attribute__((noinline)) void perf2_insitu(const char *json, size_t json_len, char *buf, int iters) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    memcpy(buf, json, json_len);
    buf[json_len]   = '\0';
    yyjson_doc *doc = yyjson_read_opts(buf, json_len, YYJSON_READ_INSITU, NULL, NULL);
    if (!doc) {
      fprintf(stderr, "insitu parse failed\n");
      return;
    }
    sink += (size_t)yyjson_get_type(doc->root);
    yyjson_doc_free(doc);
  }
  (void)sink;
}

static __attribute__((noinline)) void perf3_read_walk(const char *json, size_t json_len, int iters) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    yyjson_doc *doc = yyjson_read(json, json_len, 0);
    if (!doc) {
      fprintf(stderr, "parse failed\n");
      return;
    }
    walk(doc->root, &sink);
    yyjson_doc_free(doc);
  }
  (void)sink;
}

static __attribute__((noinline)) void perf4_insitu_walk(const char *json, size_t json_len, char *buf, int iters) {
  size_t sink = 0;
  for (int i = 0; i < iters; i++) {
    memcpy(buf, json, json_len);
    buf[json_len]   = '\0';
    yyjson_doc *doc = yyjson_read_opts(buf, json_len, YYJSON_READ_INSITU, NULL, NULL);
    if (!doc) {
      fprintf(stderr, "insitu parse failed\n");
      return;
    }
    walk(doc->root, &sink);
    yyjson_doc_free(doc);
  }
  (void)sink;
}

int main(int argc, char **argv) {
  int iterations = 10000;
  if (const char *e = getenv("ITERS"))
    iterations = atoi(e);

  /* Parse mode arguments (before payload path) */
  #define NYY_CASES 4
  struct {
    int enabled;
    const char *label;
    void (*fn)(const char *, size_t, int);        // for perf1, perf3
    void (*fn_insitu)(const char *, size_t, char *, int); // for perf2, perf4
  } cases[NYY_CASES] = {
    {0, "perf 1: yyjson_read",                perf1_read,      NULL},
    {0, "perf 2: yyjson_read (insitu)",       NULL,            perf2_insitu},
    {0, "perf 3: yyjson_read + full walk",    perf3_read_walk, NULL},
    {0, "perf 4: yyjson_read (insitu) + walk", NULL,          perf4_insitu_walk},
  };

  const char *payload_path = NULL;
  int any_mode = 0;
  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "read") == 0) {
      cases[0].enabled = 1; any_mode = 1;
    } else if (strcmp(argv[i], "insitu") == 0) {
      cases[1].enabled = 1; any_mode = 1;
    } else if (strcmp(argv[i], "read-walk") == 0) {
      cases[2].enabled = 1; any_mode = 1;
    } else if (strcmp(argv[i], "insitu-walk") == 0) {
      cases[3].enabled = 1; any_mode = 1;
    } else {
      payload_path = argv[i];
    }
  }
  if (!any_mode) {
    cases[0].enabled = 1; /* read */
    cases[1].enabled = 1; /* insitu */
  }

  /* Load payload. Prepend argv[0] so bench_payload_load sees at least one arg */
  char *load_argv[2] = {argv[0], (char *)payload_path};
  int load_argc      = payload_path ? 2 : 1;
  size_t json_len;
  char *JSON = bench_payload_load(load_argc, load_argv, &json_len);
  fprintf(stderr, "JSON payload size: %zu bytes\n", json_len);

  char *buf_insitu = (char *)malloc(json_len + 1);
  if (!buf_insitu) {
    fprintf(stderr, "malloc failed\n");
    return 1;
  }

  int warm = iterations / 10;

  printf("\n=== Perf scenarios (%d iters, %zu bytes) ===\n", iterations, json_len);

  for (int i = 0; i < NYY_CASES; i++) {
    if (!cases[i].enabled)
      continue;
    if (cases[i].fn_insitu) {
      cases[i].fn_insitu(JSON, json_len, buf_insitu, warm);
      uint64_t t0 = now_ns();
      cases[i].fn_insitu(JSON, json_len, buf_insitu, iterations);
      uint64_t t1 = now_ns();
      report(cases[i].label, t0, t1, iterations, json_len);
    } else {
      cases[i].fn(JSON, json_len, warm);
      uint64_t t0 = now_ns();
      cases[i].fn(JSON, json_len, iterations);
      uint64_t t1 = now_ns();
      report(cases[i].label, t0, t1, iterations, json_len);
    }
  }

  /* Verification + stats */
  {
    struct {
      size_t begin_obj, end_obj, field, field_bytes;
      size_t begin_arr, end_arr;
      size_t null_cnt, bool_cnt, number_cnt, string_cnt, string_bytes;
    } st = {};

    auto collect = [](yyjson_val *val, size_t *sink, decltype(st) *s) {
      auto rec = [](auto &self, yyjson_val *v, size_t *sn, decltype(st) *ss) -> void {
        switch (yyjson_get_type(v)) {
        case YYJSON_TYPE_OBJ: {
          ss->begin_obj++;
          size_t idx, max;
          yyjson_val *key, *elem;
          yyjson_obj_foreach(v, idx, max, key, elem) {
            ss->field++;
            ss->field_bytes += yyjson_get_len(key);
            *sn += yyjson_get_len(key);
            self(self, elem, sn, ss);
          }
          ss->end_obj++;
          break;
        }
        case YYJSON_TYPE_ARR: {
          ss->begin_arr++;
          size_t idx, max;
          yyjson_val *elem;
          yyjson_arr_foreach(v, idx, max, elem) {
            self(self, elem, sn, ss);
          }
          ss->end_arr++;
          break;
        }
        case YYJSON_TYPE_STR: {
          ss->string_cnt++;
          ss->string_bytes += yyjson_get_len(v);
          *sn += yyjson_get_len(v);
          break;
        }
        case YYJSON_TYPE_NUM: {
          ss->number_cnt++;
          *sn += (size_t)yyjson_get_num(v);
          break;
        }
        case YYJSON_TYPE_BOOL: {
          ss->bool_cnt++;
          *sn += yyjson_get_bool(v) ? 1 : 0;
          break;
        }
        case YYJSON_TYPE_NULL:
          ss->null_cnt++;
          *sn += 1;
          break;
        default:
          break;
        }
      };
      rec(rec, val, sink, s);
    };

    size_t sink_yy  = 0;
    yyjson_doc *doc = yyjson_read(JSON, json_len, 0);
    if (doc) {
      collect(doc->root, &sink_yy, &st);
      fprintf(stderr,
              "stats: begin_obj=%zu end_obj=%zu field=%zu(%zuB) "
              "begin_arr=%zu end_arr=%zu null=%zu bool=%zu number=%zu "
              "string=%zu(%zuB)\n",
              st.begin_obj, st.end_obj, st.field, st.field_bytes, st.begin_arr, st.end_arr, st.null_cnt,
              st.bool_cnt, st.number_cnt, st.string_cnt, st.string_bytes);
      yyjson_doc_free(doc);
    }
    fprintf(stderr, "sink yyjson=%zu\n", sink_yy);
  }

  free(buf_insitu);
  bench_payload_free(JSON);
  return 0;
}
