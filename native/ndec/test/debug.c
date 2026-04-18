#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#if defined(__aarch64__)
#define NDEC_FN_NAME ndec_parse_neon
#elif defined(__x86_64__)
#define NDEC_FN_NAME ndec_parse_avx2
#else
#error "Unsupported architecture"
#endif

#define NDEC_PARSE NDEC_FN_NAME

#include "ndec/core/parser.h"

static int parse_chunked(const char *json, uint32_t chunk_size, int *out_iterations) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, NULL, NULL);

  uint32_t total      = (uint32_t)strlen(json);
  uint32_t src_cursor = 0;
  uint8_t buf[16384]  = {0};
  uint32_t buf_len    = 0;
  int iterations      = 0;

  for (;;) {
    if (out_iterations)
      *out_iterations = iterations;
    uint32_t avail = total - src_cursor;
    uint32_t feed  = avail < chunk_size ? avail : chunk_size;
    if (buf_len + feed > sizeof(buf))
      return 0;
    memcpy(buf + buf_len, json + src_cursor, feed);
    src_cursor += feed;
    buf_len += feed;

    int is_final = (src_cursor >= total) ? 1 : 0;

    ctx.cur_pos                          = (const uint8_t *)buf;
    ctx.chunk_ptr                        = (const uint8_t *)buf;
    ctx.structural_bits                  = 0;
    ctx.scan_state.prev_in_string        = 0;
    ctx.scan_state.prev_escape           = 0;
    ctx.scan_state.prev_structural_or_ws = 1;

    ndec_ctx_set_input(&ctx, buf, buf_len, is_final);
    NDEC_PARSE(&ctx);

    iterations++;
    if (iterations > (int)(total * 2 + 10))
      return -1;

    if (ctx.exit_code == NDEC_OK)
      return 1;

    if (ctx.exit_code != NDEC_SUSPEND)
      return ctx.exit_code;

    uint32_t consumed = 0;
    if (ctx.cur_pos > ctx.buf_end) {
      consumed = buf_len;
    } else if (ctx.cur_pos >= buf) {
      consumed = (uint32_t)(ctx.cur_pos - buf);
    }

    uint32_t tail = buf_len - consumed;
    if (tail > 0 && consumed > 0) {
      memmove(buf, buf + consumed, tail);
    }
    buf_len = tail;
  }
}

static char *build_comprehensive_json(int long_str_len) {
  static char buf[32768];
  char long_str[8192];
  for (int i = 0; i < long_str_len && i < (int)sizeof(long_str) - 1; i++)
    long_str[i] = 'A' + (i % 26);
  long_str[long_str_len < (int)sizeof(long_str) ? long_str_len : (int)sizeof(long_str) - 1] = '\0';

  snprintf(
      buf, sizeof(buf),
      "{"
      "\"name\":\"Alice\","
      "\"age\":30,"
      "\"escaped\":\"line1\\nline2\\t\\\"end\\\"\","
      "\"tags\":[\"go\",\"rust\",\"json\"],"
      "\"coords\":[{\"x\":1,\"y\":2},{\"x\":3,\"y\":4}],"
      "\"fixed\":[{\"x\":10,\"y\":20},{\"x\":30,\"y\":40},{\"x\":50,\"y\":60}],"
      "\"overflow\":[100,200,300,400],"
      "\"partial\":[7,8,9],"
      "\"meta\":{\"a\":1,\"b\":2},"
      "\"nested_map\":{\"p\":{\"x\":5,\"y\":6},\"q\":{\"x\":7,\"y\":8}},"
      "\"any_map\":{\"k1\":42,\"k2\":\"hello\",\"k3\":true},"
      "\"inner_map\":{\"m1\":{\"label\":\"first\",\"value\":99},\"m2\":{\"label\":\"second\",\"value\":\"txt\"}},"
      "\"any_int\":12345,"
      "\"any_str\":\"world\","
      "\"any_obj\":{\"nested_key\":\"nested_val\"},"
      "\"any_arr\":[1,\"two\",false],"
      "\"any_null\":null,"
      "\"any_bool\":true,"
      "\"inner\":{\"label\":\"inner_label\",\"value\":3.14},"
      "\"long_string\":\"%s\""
      "}",
      long_str);
  return buf;
}

int demo1() {
  const char *json = build_comprehensive_json(256);
  // int ct           = 33;
  for (uint32_t cs = 1; cs <= 2048; cs++) {
    int iters = 0;
    int r     = parse_chunked(json, cs, &iters);
    if (r != 1) {
      fprintf(stderr, "FAIL: chunk_size=%u returned %d (iterations=%d)\n", cs, r, iters);
      return 1;
    }
  }
  printf("demo1 PASS\n");
  return 0;
}

int demo2() {

  static char buf[256];
  char long_ws_prefix[128];
  int length = 58;
  for (int i = 0; i < length; i++) {
    long_ws_prefix[i] = ' ';
  }
  long_ws_prefix[length] = '\0';
  snprintf(buf, sizeof(buf), "%s{\"a\":{},\"b\":\"y\"}", long_ws_prefix);
  const char *json = buf;

  uint32_t cs = 64;
  int iters   = 0;
  int r       = parse_chunked(json, cs, &iters);
  if (r != 1) {
    fprintf(stderr, "FAIL: chunk_size=%u returned %d (iterations=%d)\n", cs, r, iters);
    return 1;
  }
  printf("demo2 PASS\n");
  return 0;
}

int main() {
  int code;
  code = demo1();
  if (code)
    return code;
  code = demo2();
  if (code)
    return code;
}
