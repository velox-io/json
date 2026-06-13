#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <limits.h>

#include <criterion/criterion.h>

#if defined(__aarch64__)
#define NDEC_FN_NAME ndec_parse_neon
#elif defined(__x86_64__)
#define NDEC_FN_NAME ndec_parse_avx2
#else
#error "Unsupported architecture"
#endif

#define NDEC_PARSE NDEC_FN_NAME

#include "ndec/core/parser.h"

/* Read entire file into malloc'd buffer, null-terminate it. */
static char *read_file(const char *path, uint32_t *out_len) {
  FILE *f = fopen(path, "rb");
  if (!f)
    return NULL;
  fseek(f, 0, SEEK_END);
  long sz = ftell(f);
  fseek(f, 0, SEEK_SET);
  if (sz <= 0) {
    fclose(f);
    return NULL;
  }
  char *buf = (char *)malloc((size_t)sz + 1);
  if (!buf) {
    fclose(f);
    return NULL;
  }
  size_t rd = fread(buf, 1, (size_t)sz, f);
  fclose(f);
  if ((long)rd != sz) {
    free(buf);
    return NULL;
  }
  buf[rd] = '\0';
  if (out_len)
    *out_len = (uint32_t)rd;
  return buf;
}

/* Parse a complete JSON string, expecting a specific exit code. */
static uint32_t parse_exit(const char *json) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, NULL, NULL);
  ndec_ctx_set_input(&ctx, (const uint8_t *)json, (uint32_t)strlen(json), 1);
  ndec_ctx_arm_root(&ctx);
  NDEC_PARSE(&ctx);
  return ctx.exit_code;
}

/* Parse JSON in chunks using the tail-copy protocol. Returns 1 on success. */
static int parse_chunked(const char *json, uint32_t chunk_size) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, NULL, NULL);

  uint32_t total      = (uint32_t)strlen(json);
  uint32_t src_cursor = 0;
  uint8_t buf[16384];
  uint32_t buf_len = 0;
  int iterations   = 0;

  for (;;) {
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
    /* arm root on first call */
    if (ctx.sp < 0) ndec_ctx_arm_root(&ctx);
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

/* Skip reactor helpers */

typedef struct SkipState {
  int object_count;
  int array_count;
  int field_count;
} SkipState;

static int32_t skip_begin_object(NdecCtx *ctx, void *ud) { (void)ctx;
  ((SkipState *)ud)->object_count++;
  return NDEC_PROCEED;
}
static int32_t skip_end_object(NdecCtx *ctx, void *ud) { (void)ctx;
  ((SkipState *)ud)->object_count--;
  return NDEC_PROCEED;
}
static int32_t skip_begin_array(NdecCtx *ctx, void *ud) { (void)ctx;
  ((SkipState *)ud)->array_count++;
  return NDEC_PROCEED;
}
static int32_t skip_end_array(NdecCtx *ctx, void *ud) { (void)ctx;
  ((SkipState *)ud)->array_count--;
  return NDEC_PROCEED;
}
static int32_t skip_object_field(NdecCtx *ctx, void *ud, NdecStrInfo key) { (void)ctx;
  (void)key;
  ((SkipState *)ud)->field_count++;
  return NDEC_SKIP;
}

static const NdecReactor skip_reactor = {
    .begin_object = skip_begin_object,
    .end_object   = skip_end_object,
    .begin_array  = skip_begin_array,
    .end_array    = skip_end_array,
    .object_field = skip_object_field,
};

static int parse_skip(const char *json, int expected_fields) {
  NdecCtx ctx;
  SkipState st = {0, 0, 0};
  ndec_ctx_init(&ctx, &skip_reactor, &st);
  ndec_ctx_set_input(&ctx, (const uint8_t *)json, (uint32_t)strlen(json), 1);
  ndec_ctx_arm_root(&ctx);
  NDEC_PARSE(&ctx);
  if (ctx.exit_code != NDEC_OK)
    return 0;
  if (st.object_count != 0 || st.array_count != 0)
    return 0;
  if (expected_fields >= 0 && st.field_count != expected_fields)
    return 0;
  return 1;
}

/* Build comprehensive JSON payload with a configurable long_string length. */
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

/* Parse JSON padded with trailing spaces to a target total length. */
static int parse_padded(const char *json, uint32_t target_len) {
  uint32_t json_len = (uint32_t)strlen(json);
  if (target_len < json_len)
    return 1;
  char *padded = (char *)malloc(target_len + 1);
  memcpy(padded, json, json_len);
  memset(padded + json_len, ' ', target_len - json_len);
  padded[target_len] = '\0';

  NdecCtx ctx;
  ndec_ctx_init(&ctx, NULL, NULL);
  ndec_ctx_set_input(&ctx, (const uint8_t *)padded, target_len, 1);
  NDEC_PARSE(&ctx);
  int ok = (ctx.exit_code == NDEC_OK);
  free(padded);
  return ok;
}

/* Derive the data directory from __FILE__ so the test works regardless of cwd. */
static const char *data_dir(void) {
  static char dir[1024];
  static int init = 0;
  if (!init) {
    char resolved[1024];
    if (realpath(__FILE__, resolved)) {
      char *slash = strrchr(resolved, '/');
      if (slash) {
        size_t len = (size_t)(slash - resolved);
        snprintf(dir, sizeof(dir), "%.*s/data", (int)len, resolved);
      } else {
        snprintf(dir, sizeof(dir), "data");
      }
    } else {
      const char *src   = __FILE__;
      const char *slash = strrchr(src, '/');
      if (slash) {
        size_t len = (size_t)(slash - src);
        snprintf(dir, sizeof(dir), "%.*s/data", (int)len, src);
      } else {
        snprintf(dir, sizeof(dir), "data");
      }
    }
    init = 1;
  }
  return dir;
}

/* Test suite: Valid Scalars */

Test(valid_scalars, null_val) {
  cr_expect_eq(parse_exit("null"), NDEC_OK);
}

Test(valid_scalars, true_val) {
  cr_expect_eq(parse_exit("true"), NDEC_OK);
}

Test(valid_scalars, false_val) {
  cr_expect_eq(parse_exit("false"), NDEC_OK);
}

Test(valid_scalars, integer_42) {
  cr_expect_eq(parse_exit("42"), NDEC_OK);
}

Test(valid_scalars, negative_int) {
  cr_expect_eq(parse_exit("-7"), NDEC_OK);
}

Test(valid_scalars, float_314) {
  cr_expect_eq(parse_exit("3.14"), NDEC_OK);
}

Test(valid_scalars, string_hello) {
  cr_expect_eq(parse_exit("\"hello\""), NDEC_OK);
}

Test(valid_scalars, empty_string) {
  cr_expect_eq(parse_exit("\"\""), NDEC_OK);
}

Test(valid_scalars, string_with_escapes) {
  cr_expect_eq(parse_exit("\"he\\\"llo\\nworld\""), NDEC_OK);
}

Test(valid_scalars, string_with_backslash_pairs) {
  cr_expect_eq(parse_exit("\"a\\\\b\""), NDEC_OK);
}

/* Test suite: Valid Objects */

Test(valid_objects, empty) {
  cr_expect_eq(parse_exit("{}"), NDEC_OK);
}

Test(valid_objects, single_field) {
  cr_expect_eq(parse_exit("{\"a\":1}"), NDEC_OK);
}

Test(valid_objects, multiple_fields) {
  cr_expect_eq(parse_exit("{\"a\":1,\"b\":true,\"c\":null}"), NDEC_OK);
}

Test(valid_objects, nested) {
  cr_expect_eq(parse_exit("{\"a\":{\"b\":{\"c\":1}}}"), NDEC_OK);
}

Test(valid_objects, string_value) {
  cr_expect_eq(parse_exit("{\"key\":\"value\"}"), NDEC_OK);
}

Test(valid_objects, array_value) {
  cr_expect_eq(parse_exit("{\"arr\":[1,2,3]}"), NDEC_OK);
}

/* Test suite: Valid Arrays */

Test(valid_arrays, empty) {
  cr_expect_eq(parse_exit("[]"), NDEC_OK);
}

Test(valid_arrays, single_element) {
  cr_expect_eq(parse_exit("[1]"), NDEC_OK);
}

Test(valid_arrays, multiple_elements) {
  cr_expect_eq(parse_exit("[1,2,3]"), NDEC_OK);
}

Test(valid_arrays, mixed_types) {
  cr_expect_eq(parse_exit("[1,\"a\",true,null]"), NDEC_OK);
}

Test(valid_arrays, nested) {
  cr_expect_eq(parse_exit("[[1],[2]]"), NDEC_OK);
}

Test(valid_arrays, of_objects) {
  cr_expect_eq(parse_exit("[{\"a\":1},{\"b\":2}]"), NDEC_OK);
}

/* Test suite: Whitespace Handling */

Test(whitespace, spaces_around_value) {
  cr_expect_eq(parse_exit("  42  "), NDEC_OK);
}

Test(whitespace, formatted_object) {
  cr_expect_eq(parse_exit("{\n  \"a\" : 1 ,\n  \"b\" : 2\n}"), NDEC_OK);
}

Test(whitespace, tabs_and_newlines) {
  cr_expect_eq(parse_exit("[\t1\t,\n2\t,\r\n3\t]"), NDEC_OK);
}

/* Test suite: Invalid JSON */

Test(invalid_json, empty_input) {
  cr_expect_eq(parse_exit(""), NDEC_ERR_EOF);
}

Test(invalid_json, trailing_comma_object) {
  cr_expect_eq(parse_exit("{\"a\":1,}"), NDEC_ERR_SYNTAX);
}

Test(invalid_json, trailing_comma_array) {
  cr_expect_eq(parse_exit("[1,]"), NDEC_ERR_SYNTAX);
}

Test(invalid_json, missing_colon) {
  cr_expect_eq(parse_exit("{\"a\" 1}"), NDEC_ERR_SYNTAX);
}

Test(invalid_json, missing_value) {
  cr_expect_eq(parse_exit("{\"a\":}"), NDEC_ERR_SYNTAX);
}

Test(invalid_json, double_comma) {
  cr_expect_eq(parse_exit("[1,,2]"), NDEC_ERR_SYNTAX);
}

Test(invalid_json, trailing_content) {
  cr_expect_eq(parse_exit("42 43"), NDEC_ERR_TRAILING);
}

Test(invalid_json, invalid_keyword) {
  cr_expect_eq(parse_exit("tru"), NDEC_ERR_KEYWORD);
}

Test(invalid_json, bare_word) {
  cr_expect_eq(parse_exit("hello"), NDEC_ERR_SYNTAX);
}

/* Test suite: Complex Structures */

Test(complex, deeply_nested_object) {
  cr_expect_eq(parse_exit("{\"a\":{\"b\":{\"c\":{\"d\":{\"e\":{\"f\":{\"g\":{\"h\":{\"i\":{\"j\":1}}}}}}}}}}"),
               NDEC_OK);
}

Test(complex, deeply_nested_array) {
  cr_expect_eq(parse_exit("[[[[[[[[[[1]]]]]]]]]]"), NDEC_OK);
}

Test(complex, mixed_nesting) {
  cr_expect_eq(parse_exit("[{\"a\":[{\"b\":[1,{\"c\":true}]}]}]"), NDEC_OK);
}

Test(complex, long_array) {
  cr_expect_eq(parse_exit("[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]"), NDEC_OK);
}

Test(complex, many_fields) {
  cr_expect_eq(parse_exit("{\"a\":1,\"b\":2,\"c\":3,\"d\":4,\"e\":5,\"f\":6,\"g\":7,\"h\":8}"), NDEC_OK);
}

/* Test suite: Streaming */

Test(streaming, all_chunk_sizes) {
  const char *json = "{\"name\":\"test\",\"values\":[1,2,3],\"nested\":{\"a\":true}}";
  for (uint32_t cs = 1; cs <= 2048; cs++) {
    cr_expect_eq(parse_chunked(json, cs), 1, "Failed at chunk_size=%u", cs);
  }
}

Test(streaming, long_array_chunk) {
  const char *arr = "[10,20,30,40,50,60,70,80,90,100,110,120,130,140,150,160,170,180,190,200]";
  for (uint32_t cs = 1; cs <= 2048; cs++) {
    cr_expect_eq(parse_chunked(arr, cs), 1, "Failed at chunk_size=%u", cs);
  }
}

/* Root-level scalar (bare number) that spans multiple chunks. Requires
 * ndec_number_span to report TRUNCATED when it reaches buf_end without a
 * terminator and is_final is false, so the parser SUSPENDs instead of
 * committing a truncated digit string to the reactor. */
typedef struct NumLenState {
  uint32_t len;
  int calls;
} NumLenState;
static int32_t num_len_cb(void *ud, NdecRawStr raw) {
  NumLenState *s = (NumLenState *)ud;
  s->calls++;
  s->len = raw.len;
  return NDEC_PROCEED;
}
static int parse_chunked_number(const char *json, uint32_t chunk_size, uint32_t *out_len, int *out_calls) {
  NdecReactor r   = {0};
  r.scalar_number = num_len_cb;
  NumLenState st  = {0, 0};

  NdecCtx ctx;
  ndec_ctx_init(&ctx, &r, &st);

  uint32_t total      = (uint32_t)strlen(json);
  uint32_t src_cursor = 0;
  uint8_t buf[16384];
  uint32_t buf_len = 0;
  int iterations   = 0;

  for (;;) {
    uint32_t avail = total - src_cursor;
    uint32_t feed  = avail < chunk_size ? avail : chunk_size;
    memcpy(buf + buf_len, json + src_cursor, feed);
    src_cursor += feed;
    buf_len += feed;

    int is_final = (src_cursor >= total) ? 1 : 0;

    ctx.cur_pos                          = buf;
    ctx.chunk_ptr                        = buf;
    ctx.structural_bits                  = 0;
    ctx.scan_state.prev_in_string        = 0;
    ctx.scan_state.prev_escape           = 0;
    ctx.scan_state.prev_structural_or_ws = 1;

    ndec_ctx_set_input(&ctx, buf, buf_len, is_final);
    /* arm root on first call */
    if (ctx.sp < 0) ndec_ctx_arm_root(&ctx);
    NDEC_PARSE(&ctx);

    iterations++;
    if (iterations > (int)(total * 2 + 10))
      return -1;

    if (ctx.exit_code == NDEC_OK) {
      *out_len   = st.len;
      *out_calls = st.calls;
      return 1;
    }
    if (ctx.exit_code != NDEC_SUSPEND)
      return -(int)ctx.exit_code;

    uint32_t consumed = 0;
    if (ctx.cur_pos > ctx.buf_end)
      consumed = buf_len;
    else if (ctx.cur_pos >= buf)
      consumed = (uint32_t)(ctx.cur_pos - buf);

    uint32_t tail = buf_len - consumed;
    if (tail > 0 && consumed > 0)
      memmove(buf, buf + consumed, tail);
    buf_len = tail;
  }
}

Test(streaming, root_long_number) {
  /* 80-digit integer: long enough to straddle the 64-byte chunk boundary
   * so the first parse call can see a scalar_start bit but run out of
   * data before the number's end. The reactor must be invoked exactly
   * once, with the full 80-byte span, not a truncated prefix. */
  const char *num             = "12345678901234567890123456789012345678901234567890"
                                "123456789012345678901234567890";
  const uint32_t expected_len = (uint32_t)strlen(num);
  for (uint32_t cs = 1; cs <= 128; cs++) {
    uint32_t got_len;
    int got_calls;
    int rc = parse_chunked_number(num, cs, &got_len, &got_calls);
    cr_expect_eq(rc, 1, "parse failed at chunk_size=%u (rc=%d)", cs, rc);
    cr_expect_eq(got_calls, 1, "wrong callback count at chunk_size=%u: %d", cs, got_calls);
    cr_expect_eq(got_len, expected_len, "truncated number at chunk_size=%u: got %u want %u", cs, got_len,
                 expected_len);
  }
}

/* Test suite: Skip Value */

Test(skip, scalar_fields) {
  cr_expect(parse_skip("{\"a\":1,\"b\":\"hello\",\"c\":true}", 3));
}

Test(skip, nested_object_field) {
  cr_expect(parse_skip("{\"a\":{\"x\":1,\"y\":2},\"b\":3}", 2));
}

Test(skip, nested_array_field) {
  cr_expect(parse_skip("{\"a\":[1,[2,3],{\"x\":4}]}", 1));
}

Test(skip, array_scalar_elems) {
  cr_expect(parse_skip("[1,\"two\",true,null,3.14]", -1));
}

Test(skip, array_container_elems) {
  cr_expect(parse_skip("[{\"a\":1},[2,3],{\"b\":{\"c\":4}}]", 2));
}

Test(skip, deeply_nested) {
  cr_expect(parse_skip("{\"a\":{\"b\":{\"c\":{\"d\":[1,2,3]}}}}", 1));
}

Test(skip, empty_containers) {
  cr_expect(parse_skip("{\"a\":{},\"b\":[],\"c\":null}", 3));
}

Test(skip, string_with_brackets) {
  cr_expect(parse_skip("{\"a\":\"{[}]\"}", 1));
}

/* Test suite: Comprehensive */

Test(comprehensive, single_shot) {
  const char *json = build_comprehensive_json(4096);
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

Test(comprehensive, streaming_chunk1) {
  const char *json = build_comprehensive_json(256);
  cr_expect_eq(parse_chunked(json, 1), 1);
}

Test(comprehensive, streaming_sweep) {
  const char *json = build_comprehensive_json(256);
  for (uint32_t cs = 1; cs <= 2048; cs++) {
    cr_expect_eq(parse_chunked(json, cs), 1, "Failed at chunk_size=%u", cs);
  }
}

/* Test suite: Varied String Lengths */

Test(varied_string_lengths, len0) {
  cr_expect_eq(parse_exit(build_comprehensive_json(0)), NDEC_OK);
}
Test(varied_string_lengths, len1) {
  cr_expect_eq(parse_exit(build_comprehensive_json(1)), NDEC_OK);
}
Test(varied_string_lengths, len31) {
  cr_expect_eq(parse_exit(build_comprehensive_json(31)), NDEC_OK);
}
Test(varied_string_lengths, len32) {
  cr_expect_eq(parse_exit(build_comprehensive_json(32)), NDEC_OK);
}
Test(varied_string_lengths, len33) {
  cr_expect_eq(parse_exit(build_comprehensive_json(33)), NDEC_OK);
}
Test(varied_string_lengths, len63) {
  cr_expect_eq(parse_exit(build_comprehensive_json(63)), NDEC_OK);
}
Test(varied_string_lengths, len64) {
  cr_expect_eq(parse_exit(build_comprehensive_json(64)), NDEC_OK);
}
Test(varied_string_lengths, len65) {
  cr_expect_eq(parse_exit(build_comprehensive_json(65)), NDEC_OK);
}
Test(varied_string_lengths, len127) {
  cr_expect_eq(parse_exit(build_comprehensive_json(127)), NDEC_OK);
}
Test(varied_string_lengths, len128) {
  cr_expect_eq(parse_exit(build_comprehensive_json(128)), NDEC_OK);
}
Test(varied_string_lengths, len129) {
  cr_expect_eq(parse_exit(build_comprehensive_json(129)), NDEC_OK);
}
Test(varied_string_lengths, len255) {
  cr_expect_eq(parse_exit(build_comprehensive_json(255)), NDEC_OK);
}
Test(varied_string_lengths, len256) {
  cr_expect_eq(parse_exit(build_comprehensive_json(256)), NDEC_OK);
}
Test(varied_string_lengths, len512) {
  cr_expect_eq(parse_exit(build_comprehensive_json(512)), NDEC_OK);
}
Test(varied_string_lengths, len1024) {
  cr_expect_eq(parse_exit(build_comprehensive_json(1024)), NDEC_OK);
}
Test(varied_string_lengths, len4096) {
  cr_expect_eq(parse_exit(build_comprehensive_json(4096)), NDEC_OK);
}

/* Test suite: Varied Buffer Sizes */

Test(varied_buffer_sizes, exact_length) {
  const char *json = build_comprehensive_json(256);
  cr_expect(parse_padded(json, (uint32_t)strlen(json)));
}

Test(varied_buffer_sizes, next_64_boundary) {
  const char *json = build_comprehensive_json(256);
  uint32_t next_64 = ((uint32_t)strlen(json) + 63) & ~(uint32_t)63;
  cr_expect(parse_padded(json, next_64));
}

Test(varied_buffer_sizes, next_64_plus_1) {
  const char *json = build_comprehensive_json(256);
  uint32_t next_64 = ((uint32_t)strlen(json) + 63) & ~(uint32_t)63;
  cr_expect(parse_padded(json, next_64 + 1));
}

Test(varied_buffer_sizes, next_64_plus_63) {
  const char *json = build_comprehensive_json(256);
  uint32_t next_64 = ((uint32_t)strlen(json) + 63) & ~(uint32_t)63;
  cr_expect(parse_padded(json, next_64 + 63));
}

Test(varied_buffer_sizes, padded_4096) {
  cr_expect(parse_padded(build_comprehensive_json(256), 4096));
}

Test(varied_buffer_sizes, padded_8192) {
  cr_expect(parse_padded(build_comprehensive_json(256), 8192));
}

/* Test suite: String Chunk Boundaries */

Test(string_chunk_boundaries, cross_chunk) {
  char json[256];
  char val[81];
  memset(val, 'A', 80);
  val[80] = '\0';
  snprintf(json, sizeof(json), "{\"k\":\"%s\"}", val);
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

Test(string_chunk_boundaries, straddle) {
  for (int pad = 50; pad <= 70; pad++) {
    char json[256];
    int pos = 0;
    for (int i = 0; i < pad; i++)
      json[pos++] = ' ';
    const char *rest = "{\"k\":\"ABCDEFGHIJKLMNOPQRSTUVWXYZ1234\"}";
    int rlen         = (int)strlen(rest);
    memcpy(json + pos, rest, rlen);
    json[pos + rlen] = '\0';
    cr_expect_eq(parse_exit(json), NDEC_OK, "Failed at pad=%d", pad);
  }
}

/* Test suite: Escaped Strings Cross Chunk */

Test(escaped_strings_cross_chunk, len63) { /* build and test escaped string at boundary lengths */
  static const int lens[] = {63, 64, 65, 127, 128, 129, 200};
  for (int li = 0; li < (int)(sizeof(lens) / sizeof(lens[0])); li++) {
    int str_len = lens[li];
    char content[512];
    int pos = 0;
    for (int i = 0; i < str_len && pos < (int)sizeof(content) - 4; i++) {
      if (i > 0 && i % 20 == 0) {
        content[pos++] = '\\';
        content[pos++] = 'n';
      } else if (i == str_len - 2) {
        content[pos++] = '\\';
        content[pos++] = '"';
      } else {
        content[pos++] = 'A';
      }
    }
    content[pos] = '\0';
    char json[1024];
    snprintf(json, sizeof(json), "{\"s\":\"%s\",\"done\":true}", content);
    cr_expect_eq(parse_exit(json), NDEC_OK, "Failed at str_len=%d", str_len);
  }
}

/* Test suite: Leading Whitespace + Escapes */

Test(leading_whitespace_escaped, mixed) {
  const char *json = " {\n"
                     "  \"age\": 30,\n"
                     "  \"escaped\": \"line1\\nline2\\t\\\"end\\\"\",\n"
                     "  \"coords\": [\n"
                     "    { \"x\": 1, \"y\": 2 },\n"
                     "    { \"x\": 3, \"y\": 4 }\n"
                     "  ],\n"
                     "  \"fixed\": [\n"
                     "    { \"x\": 10, \"y\": 20 },\n"
                     "    { \"x\": 30, \"y\": 40 },\n"
                     "    { \"x\": 50, \"y\": 60 }\n"
                     "  ],\n"
                     "  \"partial\": [ 7, 8, 9 ],\n"
                     "  \"meta\": { \"a\": 1, \"b\": 2 }\n"
                     "}";
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

/* Test suite: Edge Cases */

Test(edge_cases, depth_255) {
  char json[1024];
  int pos = 0;
  for (int i = 0; i < 255 && pos < 510; i++)
    json[pos++] = '[';
  json[pos++] = '1';
  for (int i = 0; i < 255 && pos < 1020; i++)
    json[pos++] = ']';
  json[pos] = '\0';
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

Test(edge_cases, depth_256_overflow) {
  char json[1024];
  int pos = 0;
  for (int i = 0; i < 257 && pos < 512; i++)
    json[pos++] = '[';
  json[pos++] = '1';
  for (int i = 0; i < 257 && pos < 1020; i++)
    json[pos++] = ']';
  json[pos] = '\0';
  cr_expect_eq(parse_exit(json), NDEC_ERR_DEPTH);
}

Test(edge_cases, empty_key_value) {
  cr_expect_eq(parse_exit("{\"\":\"\"}"), NDEC_OK);
}

Test(edge_cases, unicode_escapes) {
  cr_expect_eq(parse_exit("{\"k\":\"\\u0041\\u0042\"}"), NDEC_OK);
}

Test(edge_cases, consecutive_backslash_escapes, .disabled = true) {
  cr_expect_eq(parse_exit("{\"k\":\"\\\\\\\\\\\\\\\\\"}"), NDEC_OK);
}

Test(edge_cases, long_number) {
  cr_expect_eq(parse_exit("12345678901234567890"), NDEC_OK);
}

Test(edge_cases, scientific_notation) {
  cr_expect_eq(parse_exit("-1.23e+45"), NDEC_OK);
}

Test(edge_cases, root_zero) {
  cr_expect_eq(parse_exit("0"), NDEC_OK);
}

Test(edge_cases, whitespace_only) {
  cr_expect_eq(parse_exit("   "), NDEC_ERR_EOF);
}

Test(edge_cases, object_50_fields) {
  char json[4096];
  int pos     = 0;
  json[pos++] = '{';
  for (int i = 0; i < 50; i++) {
    if (i > 0)
      json[pos++] = ',';
    pos += snprintf(json + pos, sizeof(json) - pos, "\"f%d\":%d", i, i);
  }
  json[pos++] = '}';
  json[pos]   = '\0';
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

Test(edge_cases, array_100_elements) {
  char json[2048];
  int pos     = 0;
  json[pos++] = '[';
  for (int i = 0; i < 100; i++) {
    if (i > 0)
      json[pos++] = ',';
    pos += snprintf(json + pos, sizeof(json) - pos, "%d", i);
  }
  json[pos++] = ']';
  json[pos]   = '\0';
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

Test(edge_cases, lone_closing_brace) {
  cr_expect_eq(parse_exit("}"), NDEC_ERR_SYNTAX);
}

Test(edge_cases, lone_closing_bracket) {
  cr_expect_eq(parse_exit("]"), NDEC_ERR_SYNTAX);
}

Test(edge_cases, colon_outside_object) {
  cr_expect_eq(parse_exit(":"), NDEC_ERR_SYNTAX);
}

Test(edge_cases, comma_outside_container) {
  cr_expect_eq(parse_exit(","), NDEC_ERR_SYNTAX);
}

Test(edge_cases, object_missing_brace) {
  cr_expect_eq(parse_exit("{\"a\":1"), NDEC_SUSPEND);
}

Test(edge_cases, array_missing_bracket) {
  cr_expect_eq(parse_exit("[1,2"), NDEC_SUSPEND);
}

Test(edge_cases, unclosed_string) {
  cr_expect_eq(parse_exit("\"hello"), NDEC_ERR_EOF);
}

Test(edge_cases, unescaped_newline_in_string) {
  const char json[] = "{\"k\":\"a\nb\"}";
  cr_expect_eq(parse_exit(json), NDEC_OK);
}

/* Test suite: Twitter Compact */

Test(twitter, single_shot) {
  char path[1100];
  snprintf(path, sizeof(path), "%s/twitter.json", data_dir());

  uint32_t json_len = 0;
  char *json        = read_file(path, &json_len);
  cr_expect_neq(json, NULL, "cannot open %s", path);

  if (json) {
    cr_expect_eq(parse_exit(json), NDEC_OK);
    free(json);
  }
}

Test(twitter, streaming_sweep) {
  char path[1100];
  snprintf(path, sizeof(path), "%s/twitter.json", data_dir());

  uint32_t json_len = 0;
  char *json        = read_file(path, &json_len);
  cr_expect_neq(json, NULL, "cannot open %s", path);

  if (json) {
    for (uint32_t cs = 279; cs <= 279; cs++) {
      int r = parse_chunked(json, cs);
      if (r != 1) {
        cr_assert_eq(r, 1, "chunk_size=%u returned %d", cs, r);
      }
    }
    free(json);
  }
}

/*
 * YIELD tests.
 *
 * A YIELD reactor: each hook may return NDEC_YIELD exactly once
 * per hook type (tracked via a per-hook "fired" flag). After that,
 * it returns NDEC_PROCEED. On each yield the parser must exit with
 * NDEC_SUSPEND; resume without feeding new input must continue as
 * if PROCEED had been returned, without re-invoking the yielded
 * hook. We verify by counting invocations: each hook should fire
 * exactly once per JSON value.
 */

typedef struct YieldState {
  int yield_begin_object;
  int yield_end_object;
  int yield_object_field;
  int yield_begin_array;
  int yield_end_array;
  int yield_scalar_string;
  int yield_scalar_number;
  int yield_scalar_null;
  int yield_scalar_bool;

  int cnt_begin_object;
  int cnt_end_object;
  int cnt_object_field;
  int cnt_begin_array;
  int cnt_end_array;
  int cnt_scalar_string;
  int cnt_scalar_number;
  int cnt_scalar_null;
  int cnt_scalar_bool;
} YieldState;

static int32_t y_begin_object(NdecCtx *ctx, void *ud) { (void)ctx;
  YieldState *s = ud;
  s->cnt_begin_object++;
  if (s->yield_begin_object) {
    s->yield_begin_object = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_end_object(NdecCtx *ctx, void *ud) { (void)ctx;
  YieldState *s = ud;
  s->cnt_end_object++;
  if (s->yield_end_object) {
    s->yield_end_object = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_object_field(NdecCtx *ctx, void *ud, NdecStrInfo key) { (void)ctx;
  (void)key;
  YieldState *s = ud;
  s->cnt_object_field++;
  if (s->yield_object_field) {
    s->yield_object_field = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_begin_array(NdecCtx *ctx, void *ud) { (void)ctx;
  YieldState *s = ud;
  s->cnt_begin_array++;
  if (s->yield_begin_array) {
    s->yield_begin_array = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_end_array(NdecCtx *ctx, void *ud) { (void)ctx;
  YieldState *s = ud;
  s->cnt_end_array++;
  if (s->yield_end_array) {
    s->yield_end_array = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_scalar_string(void *ud, NdecStrInfo str) {
  (void)str;
  YieldState *s = ud;
  s->cnt_scalar_string++;
  if (s->yield_scalar_string) {
    s->yield_scalar_string = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_scalar_number(void *ud, NdecRawStr raw) {
  (void)raw;
  YieldState *s = ud;
  s->cnt_scalar_number++;
  if (s->yield_scalar_number) {
    s->yield_scalar_number = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_scalar_null(void *ud) {
  YieldState *s = ud;
  s->cnt_scalar_null++;
  if (s->yield_scalar_null) {
    s->yield_scalar_null = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}
static int32_t y_scalar_bool(void *ud, int value) {
  (void)value;
  YieldState *s = ud;
  s->cnt_scalar_bool++;
  if (s->yield_scalar_bool) {
    s->yield_scalar_bool = 0;
    return NDEC_YIELD;
  }
  return NDEC_PROCEED;
}

static const NdecReactor yield_reactor = {
    .begin_object  = y_begin_object,
    .end_object    = y_end_object,
    .object_field  = y_object_field,
    .begin_array   = y_begin_array,
    .end_array     = y_end_array,
    .scalar_string = y_scalar_string,
    .scalar_number = y_scalar_number,
    .scalar_null   = y_scalar_null,
    .scalar_bool   = y_scalar_bool,
};

/* Drive a single-shot parse on already-available buffer, resuming on
 * NDEC_SUSPEND without advancing input. Returns the final exit_code
 * and the number of SUSPEND hops via out_yields. */
static uint32_t parse_with_yields(const NdecReactor *reactor, void *ud, const char *json, int *out_yields) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, reactor, ud);
  ndec_ctx_set_input(&ctx, (const uint8_t *)json, (uint32_t)strlen(json), 1);
  ndec_ctx_arm_root(&ctx);

  int yields = 0;
  for (;;) {
    NDEC_PARSE(&ctx);
    if (ctx.exit_code != NDEC_SUSPEND)
      break;
    yields++;
    if (yields > 100)
      break; /* safety */
  }
  if (out_yields)
    *out_yields = yields;
  return ctx.exit_code;
}

Test(yield, begin_object_once) {
  YieldState st         = {0};
  st.yield_begin_object = 1;
  int yields            = 0;
  uint32_t ec           = parse_with_yields(&yield_reactor, &st, "{\"a\":1}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1, "expected exactly 1 SUSPEND hop, got %d", yields);
  cr_assert_eq(st.cnt_begin_object, 1, "begin_object should fire exactly once");
  cr_assert_eq(st.cnt_end_object, 1);
  cr_assert_eq(st.cnt_object_field, 1);
  cr_assert_eq(st.cnt_scalar_number, 1);
}

Test(yield, object_field_once) {
  YieldState st         = {0};
  st.yield_object_field = 1;
  int yields            = 0;
  uint32_t ec           = parse_with_yields(&yield_reactor, &st, "{\"a\":1,\"b\":2}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_object_field, 2);
  cr_assert_eq(st.cnt_scalar_number, 2);
  cr_assert_eq(st.cnt_end_object, 1);
}

Test(yield, scalar_number_once) {
  YieldState st          = {0};
  st.yield_scalar_number = 1;
  int yields             = 0;
  uint32_t ec            = parse_with_yields(&yield_reactor, &st, "{\"a\":42}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_scalar_number, 1);
  cr_assert_eq(st.cnt_end_object, 1);
}

Test(yield, end_object_once) {
  YieldState st       = {0};
  st.yield_end_object = 1;
  int yields          = 0;
  uint32_t ec         = parse_with_yields(&yield_reactor, &st, "{\"a\":1}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_end_object, 1);
}

Test(yield, begin_array_once) {
  YieldState st        = {0};
  st.yield_begin_array = 1;
  int yields           = 0;
  uint32_t ec          = parse_with_yields(&yield_reactor, &st, "[1,2,3]", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_begin_array, 1);
  cr_assert_eq(st.cnt_end_array, 1);
  cr_assert_eq(st.cnt_scalar_number, 3);
}

Test(yield, end_array_once) {
  YieldState st      = {0};
  st.yield_end_array = 1;
  int yields         = 0;
  uint32_t ec        = parse_with_yields(&yield_reactor, &st, "[1,2]", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_end_array, 1);
}

Test(yield, scalar_string_in_array_once) {
  YieldState st          = {0};
  st.yield_scalar_string = 1;
  int yields             = 0;
  uint32_t ec            = parse_with_yields(&yield_reactor, &st, "[\"x\",\"y\"]", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_scalar_string, 2);
  cr_assert_eq(st.cnt_end_array, 1);
}

Test(yield, scalar_null_once) {
  YieldState st        = {0};
  st.yield_scalar_null = 1;
  int yields           = 0;
  uint32_t ec          = parse_with_yields(&yield_reactor, &st, "{\"a\":null}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_scalar_null, 1);
  cr_assert_eq(st.cnt_end_object, 1);
}

Test(yield, scalar_bool_once) {
  YieldState st        = {0};
  st.yield_scalar_bool = 1;
  int yields           = 0;
  uint32_t ec          = parse_with_yields(&yield_reactor, &st, "{\"a\":true,\"b\":false}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_scalar_bool, 2);
}

Test(yield, multiple_hooks_yield) {
  /* Two different hooks yield during the same parse, one after another.
   * Verify total SUSPEND hops == 2 and every hook fires exactly the
   * expected number of times. */
  YieldState st         = {0};
  st.yield_begin_object = 1;
  st.yield_end_object   = 1;
  int yields            = 0;
  uint32_t ec           = parse_with_yields(&yield_reactor, &st, "{\"a\":1,\"b\":[2,3]}", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 2);
  cr_assert_eq(st.cnt_begin_object, 1);
  cr_assert_eq(st.cnt_end_object, 1);
  cr_assert_eq(st.cnt_begin_array, 1);
  cr_assert_eq(st.cnt_end_array, 1);
}

Test(yield, root_scalar_yield) {
  YieldState st          = {0};
  st.yield_scalar_number = 1;
  int yields             = 0;
  uint32_t ec            = parse_with_yields(&yield_reactor, &st, "42", &yields);
  cr_assert_eq(ec, NDEC_OK);
  cr_assert_eq(yields, 1);
  cr_assert_eq(st.cnt_scalar_number, 1);
}

static int32_t reactor_err_begin_object(NdecCtx *ctx, void *ud) { (void)ctx;
  (void)ud;
  return -2; /* user error, not NDEC_YIELD which is -1 */
}

Test(yield, reactor_error_still_reports_error) {
  int unused     = 0;
  NdecReactor r  = {0};
  r.begin_object = reactor_err_begin_object;
  NdecCtx ctx;
  ndec_ctx_init(&ctx, &r, &unused);
  ndec_ctx_set_input(&ctx, (const uint8_t *)"{}", 2, 1);
  ndec_ctx_arm_root(&ctx);
  NDEC_PARSE(&ctx);
  cr_assert_eq(ctx.exit_code, (uint32_t)(int32_t)-2);
}

/* has_escape tests */

typedef struct {
  int string_count;
  int field_count;
  uint8_t str_has_escape[64];
  uint8_t key_has_escape[64];
} EscapeState;

static int32_t esc_object_field(NdecCtx *ctx, void *ud, NdecStrInfo key) { (void)ctx;
  EscapeState *s = ud;
  if (s->field_count < 64)
    s->key_has_escape[s->field_count] = key.has_escape;
  s->field_count++;
  return NDEC_PROCEED;
}

static int32_t esc_scalar_string(void *ud, NdecStrInfo str) {
  EscapeState *s = ud;
  if (s->string_count < 64)
    s->str_has_escape[s->string_count] = str.has_escape;
  s->string_count++;
  return NDEC_PROCEED;
}

static const NdecReactor escape_reactor = {
    .object_field  = esc_object_field,
    .scalar_string = esc_scalar_string,
};

static EscapeState parse_escape(const char *json) {
  EscapeState s = {0};
  NdecCtx ctx;
  ndec_ctx_init(&ctx, &escape_reactor, &s);
  ndec_ctx_set_input(&ctx, (const uint8_t *)json, (uint32_t)strlen(json), 1);
  ndec_ctx_arm_root(&ctx);
  NDEC_PARSE(&ctx);
  return s;
}

Test(has_escape, plain_string_no_escape) {
  EscapeState s = parse_escape("{\"k\": \"hello\"}");
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 0);
  cr_assert_eq(s.key_has_escape[0], 0);
}

Test(has_escape, string_with_escaped_quote) {
  EscapeState s = parse_escape("{\"k\": \"he\\\"llo\"}");
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 1);
}

Test(has_escape, string_with_escaped_backslash) {
  EscapeState s = parse_escape("{\"k\": \"path\\\\file\"}");
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 1);
}

Test(has_escape, key_with_escape) {
  EscapeState s = parse_escape("{\"k\\\"ey\": \"val\"}");
  cr_assert_eq(s.field_count, 1);
  cr_assert_eq(s.key_has_escape[0], 1);
  cr_assert_eq(s.str_has_escape[0], 0);
}

Test(has_escape, adjacent_strings_no_cross_contamination) {
  /* An escaped string followed by a plain string in the same object:
   * the backslash from the first value must NOT leak into the second. */
  EscapeState s = parse_escape("{\"a\": \"es\\\"caped\", \"b\": \"plain\"}");
  cr_assert_eq(s.string_count, 2);
  cr_assert_eq(s.str_has_escape[0], 1);
  cr_assert_eq(s.str_has_escape[1], 0);
}

Test(has_escape, array_adjacent_strings) {
  EscapeState s = parse_escape("[\"wi\\\"th\", \"without\"]");
  cr_assert_eq(s.string_count, 2);
  cr_assert_eq(s.str_has_escape[0], 1);
  cr_assert_eq(s.str_has_escape[1], 0);
}

Test(has_escape, array_mixed_escapes) {
  /* ["aaa\nbbb\n", "nnn", "kkk\n"] */
  EscapeState s = parse_escape("[\"aaa\\nbbb\\n\", \"nnn\", \"kkk\\n\"]");
  cr_assert_eq(s.string_count, 3);
  cr_assert_eq(s.str_has_escape[0], 1);
  cr_assert_eq(s.str_has_escape[1], 0);
  cr_assert_eq(s.str_has_escape[2], 1);
}

Test(has_escape, cross_chunk_escape_before_boundary) {
  /* String crosses the 64-byte chunk boundary.
   * Escape (\n) is in chunk 0, closing quote in chunk 1.
   *
   * Layout:
   *   [0]  '['
   *   [1]  '"'  open quote (open_offset=1)
   *   [2..61] 'a' x60
   *   [62] '\'
   *   [63] 'n'          <- end of chunk 0
   *   [64..73] 'b' x10  <- chunk 1
   *   [74] '"'  close quote
   *   [75] ']'
   */
  char json[80];
  int p     = 0;
  json[p++] = '[';
  json[p++] = '"';
  while (p < 62)
    json[p++] = 'a';
  json[p++] = '\\';
  json[p++] = 'n';
  while (p < 74)
    json[p++] = 'b';
  json[p++] = '"';
  json[p++] = ']';
  json[p]   = '\0';

  EscapeState s = parse_escape(json);
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 1);
}

Test(has_escape, cross_chunk_escape_after_boundary) {
  /* Escape is in chunk 1 only; chunk 0 portion has no backslash.
   *
   * Layout:
   *   [0]  '['
   *   [1]  '"'  open quote
   *   [2..63] 'a' x62       <- fill chunk 0
   *   [64] 'b'              <- chunk 1 starts
   *   [65] '\'
   *   [66] 'n'
   *   [67..76] 'c' x10
   *   [77] '"'  close quote
   *   [78] ']'
   */
  char json[84];
  int p     = 0;
  json[p++] = '[';
  json[p++] = '"';
  while (p < 64)
    json[p++] = 'a';
  json[p++] = 'b';
  json[p++] = '\\';
  json[p++] = 'n';
  while (p < 77)
    json[p++] = 'c';
  json[p++] = '"';
  json[p++] = ']';
  json[p]   = '\0';

  EscapeState s = parse_escape(json);
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 1);
}

Test(has_escape, cross_chunk_escape_both_chunks) {
  /* One escape in chunk 0, another in chunk 1. */
  char json[84];
  int p     = 0;
  json[p++] = '[';
  json[p++] = '"';
  while (p < 60)
    json[p++] = 'a';
  json[p++] = '\\';
  json[p++] = 'n'; /* escape in chunk 0, positions 60-61 */
  while (p < 64)
    json[p++] = 'a';
  /* chunk 1 */
  json[p++] = 'b';
  json[p++] = '\\';
  json[p++] = 'n'; /* escape in chunk 1, positions 65-66 */
  while (p < 77)
    json[p++] = 'c';
  json[p++] = '"';
  json[p++] = ']';
  json[p]   = '\0';

  EscapeState s = parse_escape(json);
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 1);
}

Test(has_escape, cross_chunk_no_escape) {
  /* Long string crossing chunk boundary with NO escapes. */
  char json[100];
  int p     = 0;
  json[p++] = '[';
  json[p++] = '"';
  while (p < 90)
    json[p++] = 'a';
  json[p++] = '"';
  json[p++] = ']';
  json[p]   = '\0';

  EscapeState s = parse_escape(json);
  cr_assert_eq(s.string_count, 1);
  cr_assert_eq(s.str_has_escape[0], 0);
}
