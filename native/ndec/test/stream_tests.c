#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <criterion/criterion.h>

#if defined(__aarch64__)
#define NDEC_FN_NAME ndec_parse_neon
#elif defined(__x86_64__)
#define NDEC_FN_NAME ndec_parse_avx2
#else
#error "Unsupported architecture"
#endif

#define NDEC_STREAM_PARSE NDEC_FN_NAME

#include "ndec/core/parser.h"
#include "ndec/stream.h"

/*
 * Event-capture reactor: collects a compact trace of kernel events
 * so we can compare chunked feeds against single-shot feeds.
 */

typedef struct Trace {
  char buf[8192];
  size_t len;
} Trace;

static void trace_append(Trace *t, const char *fmt, ...) {
  if (t->len >= sizeof(t->buf) - 1)
    return;
  va_list ap;
  va_start(ap, fmt);
  int n = vsnprintf(t->buf + t->len, sizeof(t->buf) - t->len, fmt, ap);
  va_end(ap);
  if (n > 0)
    t->len += (size_t)n;
}

static int32_t tr_begin_object(NdecCtx *ctx, void *ud) { (void)ctx;
  trace_append(ud, "{");
  return NDEC_PROCEED;
}
static int32_t tr_end_object(NdecCtx *ctx, void *ud) { (void)ctx;
  trace_append(ud, "}");
  return NDEC_PROCEED;
}
static int32_t tr_begin_array(NdecCtx *ctx, void *ud) { (void)ctx;
  trace_append(ud, "[");
  return NDEC_PROCEED;
}
static int32_t tr_end_array(NdecCtx *ctx, void *ud) { (void)ctx;
  trace_append(ud, "]");
  return NDEC_PROCEED;
}
static int32_t tr_scalar_null(void *ud) {
  trace_append(ud, "N");
  return NDEC_PROCEED;
}
static int32_t tr_scalar_bool(void *ud, int v) {
  trace_append(ud, v ? "T" : "F");
  return NDEC_PROCEED;
}
static int32_t tr_object_field(NdecCtx *ctx, void *ud, NdecStrInfo key) { (void)ctx;
  trace_append(ud, "k<%.*s>", (int)key.raw.len, key.raw.ptr);
  return NDEC_PROCEED;
}
static int32_t tr_scalar_number(void *ud, NdecRawStr raw) {
  trace_append(ud, "n<%.*s>", (int)raw.len, raw.ptr);
  return NDEC_PROCEED;
}
static int32_t tr_scalar_string(void *ud, NdecStrInfo str) {
  trace_append(ud, "s<%.*s>", (int)str.raw.len, str.raw.ptr);
  return NDEC_PROCEED;
}

static const NdecReactor trace_reactor = {
    .begin_object  = tr_begin_object,
    .end_object    = tr_end_object,
    .begin_array   = tr_begin_array,
    .end_array     = tr_end_array,
    .object_field  = tr_object_field,
    .scalar_null   = tr_scalar_null,
    .scalar_bool   = tr_scalar_bool,
    .scalar_number = tr_scalar_number,
    .scalar_string = tr_scalar_string,
};

/* Helpers */

static NdecStreamStatus feed_all(const char *json, Trace *out) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, &trace_reactor, out);
  NdecStream s;
  ndec_stream_init(&s, &ctx);
  return ndec_stream_feed_all(&s, (const uint8_t *)json, strlen(json));
}

/* Feed json in fixed-size chunks via a reusable buf; return final status. */
static NdecStreamStatus feed_chunked(const char *json, size_t chunk_size, Trace *out) {
  NdecCtx ctx;
  ndec_ctx_init(&ctx, &trace_reactor, out);
  NdecStream s;
  ndec_stream_init(&s, &ctx);

  size_t total = strlen(json);
  size_t src   = 0;
  uint8_t buf[16 * 1024];
  size_t carry = 0;

  for (;;) {
    if (carry == sizeof(buf))
      return NDEC_STREAM_ERROR; /* token exceeds buf */

    size_t room = sizeof(buf) - carry;
    size_t take = total - src;
    if (take > chunk_size)
      take = chunk_size;
    if (take > room)
      take = room;
    memcpy(buf + carry, json + src, take);
    src += take;
    size_t len   = carry + take;
    int is_final = (src >= total) ? 1 : 0;

    const uint8_t *tail = NULL;
    size_t tail_len     = 0;
    NdecStreamStatus st = ndec_stream_feed(&s, buf, len, is_final, &tail, &tail_len);

    if (st == NDEC_STREAM_DONE)
      return NDEC_STREAM_DONE;
    if (st == NDEC_STREAM_ERROR)
      return NDEC_STREAM_ERROR;

    if (tail_len > 0)
      memmove(buf, tail, tail_len);
    carry = tail_len;
    if (is_final)
      return st; /* safety: shouldn't loop past final */
  }
}

/* §7.1  feed_all: single-shot, single-value */

Test(stream_feed_all, simple_object) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":1,\"b\":\"x\"}", &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, "{k<a>n<1>k<b>s<x>}");
}

Test(stream_feed_all, scalars_and_array) {
  Trace t = {0};
  cr_expect_eq(feed_all("[null,true,false,42,\"z\"]", &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, "[NTFn<42>s<z>]");
}

Test(stream_feed_all, nested) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":[1,{\"b\":true}]}", &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, "{k<a>[n<1>{k<b>T}]}");
}

Test(stream_feed_all, empty_invalid) {
  Trace t = {0};
  cr_expect_eq(feed_all("", &t), NDEC_STREAM_ERROR);
}

Test(stream_feed_all, invalid_syntax) {
  Trace t = {0};
  cr_expect_eq(feed_all("{a:1}", &t), NDEC_STREAM_ERROR);
}

Test(stream_feed_all, truncated_object) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":1", &t), NDEC_STREAM_ERROR);
}

/* §7.2  chunked: same input, varying chunk sizes, same trace */

static const char *k_payload = "{\"name\":\"alexander\","
                               "\"age\":30,"
                               "\"tags\":[\"go\",\"rust\",\"json\"],"
                               "\"flags\":[true,false,null],"
                               "\"nested\":{\"x\":1.5e2,\"y\":-3}}";

static const char *k_expected = "{k<name>s<alexander>"
                                "k<age>n<30>"
                                "k<tags>[s<go>s<rust>s<json>]"
                                "k<flags>[TFN]"
                                "k<nested>{k<x>n<1.5e2>k<y>n<-3>}}";

Test(stream_chunked, chunk_1) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 1, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(stream_chunked, chunk_7) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 7, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(stream_chunked, chunk_63) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 63, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(stream_chunked, chunk_64) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 64, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(stream_chunked, chunk_65) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 65, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(stream_chunked, chunk_1024) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 1024, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

/* §7.3  targeted cross-chunk token splits */

Test(stream_crosschunk, string_with_escape) {
  const char *j = "{\"msg\":\"line1\\nline2\\t\\\"end\\\"\"}";
  for (size_t cs = 1; cs <= 8; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_STREAM_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<msg>s<line1\\nline2\\t\\\"end\\\">}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(stream_crosschunk, long_number) {
  const char *j = "{\"v\":1234567890.12345e-10}";
  for (size_t cs = 1; cs <= 6; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_STREAM_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<v>n<1234567890.12345e-10>}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(stream_crosschunk, long_key) {
  const char *j = "{\"averyverylongkeyname\":0}";
  for (size_t cs = 1; cs <= 6; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_STREAM_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<averyverylongkeyname>n<0>}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(stream_crosschunk, keywords) {
  const char *j = "[true,false,null]";
  for (size_t cs = 1; cs <= 5; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_STREAM_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "[TFN]", "trace mismatch at chunk_size=%zu", cs);
  }
}

/* §7.4  boundary: split falls right after a token */

Test(stream_boundary, split_after_object_brace) {
  /* "{\"a\":1" has 6 bytes; chunk of 6 stops exactly after "1",
   * then remaining "}" completes. */
  Trace t = {0};
  cr_expect_eq(feed_chunked("{\"a\":1}", 6, &t), NDEC_STREAM_DONE);
  cr_expect_str_eq(t.buf, "{k<a>n<1>}");
}

/* §7.5  final truncation -> ERROR */

Test(stream_final, truncated_string) {
  Trace t = {0};
  cr_expect_eq(feed_all("\"abc", &t), NDEC_STREAM_ERROR);
}

Test(stream_final, truncated_number_in_array) {
  Trace t = {0};
  cr_expect_eq(feed_all("[12", &t), NDEC_STREAM_ERROR);
}

/* §7.6  error propagation: kernel-level errors surface as ERROR */

Test(stream_error, syntax_error) {
  NdecCtx ctx;
  Trace t = {0};
  ndec_ctx_init(&ctx, &trace_reactor, &t);
  NdecStream s;
  ndec_stream_init(&s, &ctx);

  const char *j       = "{\"a\":@}";
  NdecStreamStatus st = ndec_stream_feed_all(&s, (const uint8_t *)j, strlen(j));
  cr_expect_eq(st, NDEC_STREAM_ERROR);
  cr_expect_eq(ctx.exit_code, NDEC_ERR_SYNTAX);
}

Test(stream_error, trailing_garbage) {
  NdecCtx ctx;
  Trace t = {0};
  ndec_ctx_init(&ctx, &trace_reactor, &t);
  NdecStream s;
  ndec_stream_init(&s, &ctx);

  const char *j       = "{}xyz";
  NdecStreamStatus st = ndec_stream_feed_all(&s, (const uint8_t *)j, strlen(j));
  cr_expect_eq(st, NDEC_STREAM_ERROR);
  cr_expect_eq(ctx.exit_code, NDEC_ERR_TRAILING);
}

/* §7.7  ERROR is sticky: re-feed returns ERROR without side effects */

Test(stream_error, sticky_after_error) {
  NdecCtx ctx;
  Trace t = {0};
  ndec_ctx_init(&ctx, &trace_reactor, &t);
  NdecStream s;
  ndec_stream_init(&s, &ctx);

  const char *bad = "@@@";
  cr_expect_eq(ndec_stream_feed_all(&s, (const uint8_t *)bad, strlen(bad)), NDEC_STREAM_ERROR);

  const char *good = "42";
  cr_expect_eq(ndec_stream_feed_all(&s, (const uint8_t *)good, strlen(good)), NDEC_STREAM_ERROR);
}

/*
 * §7.8  DONE with trailing whitespace: DONE, tail_len == 0
 *       (kernel consumes trailing whitespace on is_final=1)
 */

Test(stream_done, trailing_whitespace) {
  NdecCtx ctx;
  Trace t = {0};
  ndec_ctx_init(&ctx, &trace_reactor, &t);
  NdecStream s;
  ndec_stream_init(&s, &ctx);

  const char *j       = "42   \n";
  const uint8_t *tail = NULL;
  size_t tail_len     = (size_t)-1;
  NdecStreamStatus st = ndec_stream_feed(&s, (const uint8_t *)j, strlen(j),
                                         /*is_final=*/1, &tail, &tail_len);
  cr_expect_eq(st, NDEC_STREAM_DONE);
  cr_expect_eq(tail_len, 0);
}
