/*
 * bench_payload.h — shared JSON loader for all ndec benchmarks.
 *
 * Loads a JSON file into a heap buffer at startup. Used by bench.c,
 * bench_full.c, yyjson/comparison.cpp, simdjson/comparison.cpp.
 *
 * Path resolution (first match wins):
 *   1. argv[1]              explicit path from command line
 *   2. $NDEC_BENCH_PAYLOAD  environment variable
 *   3. DEFAULT_PAYLOAD_PATH compile-time macro (set by Makefile)
 *   4. "test/data/bench_payload.json" (fallback relative to cwd)
 *
 * The returned buffer is heap-allocated with one trailing '\0'. Callers
 * must free it via bench_payload_free().
 */

#ifndef NDEC_BENCH_PAYLOAD_H
#define NDEC_BENCH_PAYLOAD_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef __cplusplus
extern "C" {
#endif

#ifndef DEFAULT_PAYLOAD_PATH
#define DEFAULT_PAYLOAD_PATH "test/data/bench_payload.json"
#endif

/* Load the entire file at `path` into a freshly allocated buffer.
 * Writes byte count (excluding trailing '\0') into *out_len.
 * Returns NULL on failure (logged to stderr). */
static char *bench_payload_read_file(const char *path, size_t *out_len) {
  FILE *fp = fopen(path, "rb");
  if (!fp) {
    fprintf(stderr, "bench_payload: cannot open %s: ", path);
    perror("");
    return NULL;
  }
  if (fseek(fp, 0, SEEK_END) != 0) { fclose(fp); return NULL; }
  long sz = ftell(fp);
  if (sz < 0) { fclose(fp); return NULL; }
  if (fseek(fp, 0, SEEK_SET) != 0) { fclose(fp); return NULL; }

  char *buf = (char *)malloc((size_t)sz + 1);
  if (!buf) { fclose(fp); return NULL; }
  size_t n = fread(buf, 1, (size_t)sz, fp);
  fclose(fp);
  if (n != (size_t)sz) { free(buf); return NULL; }
  buf[sz] = '\0';
  if (out_len) *out_len = (size_t)sz;
  return buf;
}

/* Resolve the payload path from argv / env / compile-time default. */
static const char *bench_payload_resolve_path(int argc, char **argv) {
  if (argc >= 2 && argv[1] && argv[1][0] != '\0') return argv[1];
  const char *env = getenv("NDEC_BENCH_PAYLOAD");
  if (env && env[0] != '\0') return env;
  return DEFAULT_PAYLOAD_PATH;
}

/* One-shot loader. Exits the process with status 1 on failure. */
static char *bench_payload_load(int argc, char **argv, size_t *out_len) {
  const char *path = bench_payload_resolve_path(argc, argv);
  char *buf = bench_payload_read_file(path, out_len);
  if (!buf) {
    fprintf(stderr, "bench_payload: failed to load payload from %s\n", path);
    fprintf(stderr, "bench_payload: override via argv[1] or $NDEC_BENCH_PAYLOAD\n");
    exit(1);
  }
  fprintf(stderr, "bench_payload: loaded %zu bytes from %s\n",
          out_len ? *out_len : 0, path);
  return buf;
}

static void bench_payload_free(char *buf) { free(buf); }

#ifdef __cplusplus
}
#endif

#endif /* NDEC_BENCH_PAYLOAD_H */
