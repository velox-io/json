#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <criterion/criterion.h>

#define NDEC_USE_VTABLE
#include "chunkfeed.h"

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

static int32_t tr_begin_object(void *ud) {
  trace_append(ud, "{");
  return NDEC_PROCEED;
}
static int32_t tr_end_object(void *ud) {
  trace_append(ud, "}");
  return NDEC_PROCEED;
}
static int32_t tr_begin_array(void *ud) {
  trace_append(ud, "[");
  return NDEC_PROCEED;
}
static int32_t tr_end_array(void *ud) {
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
static int32_t tr_object_field(void *ud, NdecStrInfo key) {
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

static NdecChunkFeedStatus feed_all(const char *json, Trace *out) {
  NdecSaxContext ctx;
  ndec_sax_ctx_init(&ctx, &trace_reactor, out);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);
  return ndec_chunkfeed_push_all(&s, (const uint8_t *)json, strlen(json));
}

/* Feed json in fixed-size chunks via a reusable buf; return final status. */
static NdecChunkFeedStatus feed_chunked(const char *json, size_t chunk_size, Trace *out) {
  NdecSaxContext ctx;
  ndec_sax_ctx_init(&ctx, &trace_reactor, out);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);

  size_t total = strlen(json);
  size_t src   = 0;
  uint8_t buf[16 * 1024];
  size_t carry = 0;

  for (;;) {
    if (carry == sizeof(buf))
      return NDEC_CHUNKFEED_ERROR; /* token exceeds buf */

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
    NdecChunkFeedStatus st = ndec_chunkfeed_push(&s, buf, len, is_final, &tail, &tail_len);

    if (st == NDEC_CHUNKFEED_DONE)
      return NDEC_CHUNKFEED_DONE;
    if (st == NDEC_CHUNKFEED_ERROR)
      return NDEC_CHUNKFEED_ERROR;

    if (tail_len > 0)
      memmove(buf, tail, tail_len);
    carry = tail_len;
    if (is_final)
      return st; /* safety: shouldn't loop past final */
  }
}

/* §7.1  feed_all: single-shot, single-value */

Test(chunkfeed_feed_all, simple_object) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":1,\"b\":\"x\"}", &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, "{k<a>n<1>k<b>s<x>}");
}

Test(chunkfeed_feed_all, scalars_and_array) {
  Trace t = {0};
  cr_expect_eq(feed_all("[null,true,false,42,\"z\"]", &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, "[NTFn<42>s<z>]");
}

Test(chunkfeed_feed_all, nested) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":[1,{\"b\":true}]}", &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, "{k<a>[n<1>{k<b>T}]}");
}

Test(chunkfeed_feed_all, empty_invalid) {
  Trace t = {0};
  cr_expect_eq(feed_all("", &t), NDEC_CHUNKFEED_ERROR);
}

Test(chunkfeed_feed_all, invalid_syntax) {
  Trace t = {0};
  cr_expect_eq(feed_all("{a:1}", &t), NDEC_CHUNKFEED_ERROR);
}

Test(chunkfeed_feed_all, truncated_object) {
  Trace t = {0};
  cr_expect_eq(feed_all("{\"a\":1", &t), NDEC_CHUNKFEED_ERROR);
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

Test(chunkfeed_chunked, chunk_1) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 1, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(chunkfeed_chunked, chunk_7) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 7, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(chunkfeed_chunked, chunk_63) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 63, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(chunkfeed_chunked, chunk_64) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 64, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(chunkfeed_chunked, chunk_65) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 65, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

Test(chunkfeed_chunked, chunk_1024) {
  Trace t = {0};
  cr_expect_eq(feed_chunked(k_payload, 1024, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, k_expected);
}

/* §7.3  targeted cross-chunk token splits */

Test(chunkfeed_crosschunk, string_with_escape) {
  const char *j = "{\"msg\":\"line1\\nline2\\t\\\"end\\\"\"}";
  for (size_t cs = 1; cs <= 8; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_CHUNKFEED_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<msg>s<line1\\nline2\\t\\\"end\\\">}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(chunkfeed_crosschunk, long_number) {
  const char *j = "{\"v\":1234567890.12345e-10}";
  for (size_t cs = 1; cs <= 6; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_CHUNKFEED_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<v>n<1234567890.12345e-10>}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(chunkfeed_crosschunk, long_key) {
  const char *j = "{\"averyverylongkeyname\":0}";
  for (size_t cs = 1; cs <= 6; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_CHUNKFEED_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "{k<averyverylongkeyname>n<0>}", "trace mismatch at chunk_size=%zu", cs);
  }
}

Test(chunkfeed_crosschunk, keywords) {
  const char *j = "[true,false,null]";
  for (size_t cs = 1; cs <= 5; cs++) {
    Trace t = {0};
    cr_expect_eq(feed_chunked(j, cs, &t), NDEC_CHUNKFEED_DONE, "failed at chunk_size=%zu", cs);
    cr_expect_str_eq(t.buf, "[TFN]", "trace mismatch at chunk_size=%zu", cs);
  }
}

/* §7.4  boundary: split falls right after a token */

Test(chunkfeed_boundary, split_after_object_brace) {
  /* "{\"a\":1" has 6 bytes; chunk of 6 stops exactly after "1",
   * then remaining "}" completes. */
  Trace t = {0};
  cr_expect_eq(feed_chunked("{\"a\":1}", 6, &t), NDEC_CHUNKFEED_DONE);
  cr_expect_str_eq(t.buf, "{k<a>n<1>}");
}

/* §7.5  final truncation -> ERROR */

Test(chunkfeed_final, truncated_string) {
  Trace t = {0};
  cr_expect_eq(feed_all("\"abc", &t), NDEC_CHUNKFEED_ERROR);
}

Test(chunkfeed_final, truncated_number_in_array) {
  Trace t = {0};
  cr_expect_eq(feed_all("[12", &t), NDEC_CHUNKFEED_ERROR);
}

/* §7.6  error propagation: kernel-level errors surface as ERROR */

Test(chunkfeed_error, syntax_error) {
  NdecSaxContext ctx;
  Trace t = {0};
  ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);

  const char *j       = "{\"a\":@}";
  NdecChunkFeedStatus st = ndec_chunkfeed_push_all(&s, (const uint8_t *)j, strlen(j));
  cr_expect_eq(st, NDEC_CHUNKFEED_ERROR);
  cr_expect_eq(ctx.exit_code, NDEC_ERR_SYNTAX);
}

Test(chunkfeed_error, trailing_garbage) {
  NdecSaxContext ctx;
  Trace t = {0};
  ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);

  const char *j       = "{}xyz";
  NdecChunkFeedStatus st = ndec_chunkfeed_push_all(&s, (const uint8_t *)j, strlen(j));
  cr_expect_eq(st, NDEC_CHUNKFEED_ERROR);
  cr_expect_eq(ctx.exit_code, NDEC_ERR_TRAILING);
}

/* §7.7  ERROR is sticky: re-feed returns ERROR without side effects */

Test(chunkfeed_error, sticky_after_error) {
  NdecSaxContext ctx;
  Trace t = {0};
  ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);

  const char *bad = "@@@";
  cr_expect_eq(ndec_chunkfeed_push_all(&s, (const uint8_t *)bad, strlen(bad)), NDEC_CHUNKFEED_ERROR);

  const char *good = "42";
  cr_expect_eq(ndec_chunkfeed_push_all(&s, (const uint8_t *)good, strlen(good)), NDEC_CHUNKFEED_ERROR);
}

/*
 * §7.8  DONE with trailing whitespace: DONE, tail_len == 0
 *       (kernel consumes trailing whitespace on is_final=1)
 */

Test(chunkfeed_done, trailing_whitespace) {
  NdecSaxContext ctx;
  Trace t = {0};
  ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);

  const char *j       = "42   \n";
  const uint8_t *tail = NULL;
  size_t tail_len     = (size_t)-1;
  NdecChunkFeedStatus st = ndec_chunkfeed_push(&s, (const uint8_t *)j, strlen(j),
                                         /*is_final=*/1, &tail, &tail_len);
  cr_expect_eq(st, NDEC_CHUNKFEED_DONE);
  cr_expect_eq(tail_len, 0);
}

/* --------------------------------------------------------------------
 * §7.9  UTF-8 validation (simdjson lookup4 hooked into structural-scan chunks)
 *
 * The validator runs alongside structural scanning, with state reset
 * at every ndec_chunkfeed_push call (multibyte chars never span buffers,
 * by the tail-copy protocol's token-boundary contract).
 *
 * feed_bytes_all is feed_all but takes a length so the JSON can hold
 * embedded NULs / raw high bytes that would terminate a C string.
 * -------------------------------------------------------------------- */

static NdecChunkFeedStatus feed_bytes_all(const uint8_t *data, size_t len, Trace *out) {
  NdecSaxContext ctx;
  ndec_sax_ctx_init(&ctx, &trace_reactor, out);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);
  return ndec_chunkfeed_push_all(&s, data, len);
}

static uint32_t feed_bytes_all_get_exit(const uint8_t *data, size_t len) {
  NdecSaxContext ctx;
  Trace t = {0};
  ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
  NdecChunkFeed s;
  ndec_chunkfeed_init(&s, &ctx);
  ndec_chunkfeed_push_all(&s, data, len);
  return ctx.exit_code;
}

#define UTF8_CASE(name, accept, ...)                                                                              \
  Test(chunkfeed_utf8, name) {                                                                                       \
    const uint8_t v[]   = __VA_ARGS__;                                                                            \
    Trace t             = {0};                                                                                    \
    NdecChunkFeedStatus st = feed_bytes_all(v, sizeof(v), &t);                                                       \
    if (accept) {                                                                                                 \
      cr_expect_eq(st, NDEC_CHUNKFEED_DONE, "should accept");                                                        \
    } else {                                                                                                      \
      cr_expect_eq(st, NDEC_CHUNKFEED_ERROR, "should reject");                                                       \
    }                                                                                                             \
  }

/* Build [ " <bytes> " ] dynamically and feed. */
static NdecChunkFeedStatus feed_bytes_in_string(const uint8_t *bytes, size_t n) {
  size_t total = n + 4;
  uint8_t *buf = (uint8_t *)malloc(total);
  cr_assert_not_null(buf);
  buf[0] = '[';
  buf[1] = '"';
  memcpy(buf + 2, bytes, n);
  buf[2 + n]         = '"';
  buf[3 + n]         = ']';
  Trace t            = {0};
  NdecChunkFeedStatus s = feed_bytes_all(buf, total, &t);
  free(buf);
  return s;
}

#define UTF8_STR_OK(name, ...)                                                                                    \
  Test(chunkfeed_utf8, name) {                                                                                       \
    const uint8_t v[] = __VA_ARGS__;                                                                              \
    cr_expect_eq(feed_bytes_in_string(v, sizeof(v)), NDEC_CHUNKFEED_DONE);                                           \
  }
#define UTF8_STR_ERR(name, ...)                                                                                   \
  Test(chunkfeed_utf8, name) {                                                                                       \
    const uint8_t v[] = __VA_ARGS__;                                                                              \
    cr_expect_eq(feed_bytes_in_string(v, sizeof(v)), NDEC_CHUNKFEED_ERROR);                                          \
  }

UTF8_STR_OK(valid_two_byte, {0xc2, 0xa9})                  /* (c) */
UTF8_STR_OK(valid_three_byte, {0xe4, 0xb8, 0xad})          /* CJK U+4E2D */
UTF8_STR_OK(valid_four_byte, {0xf0, 0x9f, 0x98, 0x80})     /* U+1F600 */
UTF8_STR_OK(valid_max_codepoint, {0xf4, 0x8f, 0xbf, 0xbf}) /* U+10FFFF */

UTF8_STR_ERR(reject_ff_in_string, {'a', 0xff, 'b'})
UTF8_STR_ERR(reject_lone_continuation, {'a', 0x80, 'b'})
UTF8_STR_ERR(reject_surrogate, {0xed, 0xa0, 0x80})
UTF8_STR_ERR(reject_overlong_2, {0xc0, 0x80})
UTF8_STR_ERR(reject_overlong_3, {0xe0, 0x80, 0xaf})
UTF8_STR_ERR(reject_too_large, {0xf4, 0x90, 0x80, 0x80})
UTF8_STR_ERR(reject_5byte_lead, {0xf8, 0x80, 0x80, 0x80, 0x80})
UTF8_STR_ERR(reject_truncated_2byte_in_str, {0xc2})

/* The headline streaming case: a multibyte character lands across the
 * 64-byte SIMD chunk boundary inside a single buffer. The validator
 * must carry prev_input_block from chunk N to chunk N+1 to validate
 * the continuation bytes. Chunk boundary lands inside the 3-byte
 * sequence intentionally. */
Test(chunkfeed_utf8, valid_multibyte_at_chunk_boundary_single_feed) {
  /* Lay out: '[','"', then 62 'A' so byte 64 is 0xe4, 65 is 0xb8,
   * 66 is 0xad, 67 is '"', 68 is ']'. */
  uint8_t buf[69];
  buf[0] = '[';
  buf[1] = '"';
  for (int i = 0; i < 62; i++)
    buf[2 + i] = 'A';
  buf[64] = 0xe4;
  buf[65] = 0xb8;
  buf[66] = 0xad;
  buf[67] = '"';
  buf[68] = ']';
  Trace t = {0};
  cr_expect_eq(feed_bytes_all(buf, sizeof(buf), &t), NDEC_CHUNKFEED_DONE);
}

/* Same shape but truncated: the 3-byte lead sits at offset 64 with
 * only one continuation. is_final must trigger check_eof and reject. */
Test(chunkfeed_utf8, reject_truncated_at_chunk_boundary_is_final) {
  uint8_t buf[68];
  buf[0] = '[';
  buf[1] = '"';
  for (int i = 0; i < 62; i++)
    buf[2 + i] = 'A';
  buf[64] = 0xe4;
  buf[65] = 0xb8; /* missing 3rd byte */
  buf[66] = '"';
  buf[67] = ']';
  Trace t = {0};
  cr_expect_eq(feed_bytes_all(buf, sizeof(buf), &t), NDEC_CHUNKFEED_ERROR);
}

/* Crosschunk feed: split a buffer that contains a valid multibyte
 * across multiple feeds.  Per the protocol contract, tokens (and so
 * any multibyte char inside one) never span buffers, but the parser
 * may suspend mid-string if the closing quote hasn't arrived. The
 * validator state resets per feed; the multibyte chars are always
 * fully present in whichever feed contains the (still-open) string
 * payload. */
Test(chunkfeed_utf8, multibyte_split_feeds_via_chunked) {
  /* feed_chunked exercises real tail-copy: after each suspend the
   * unconsumed tail is memmoved to buf[0] and more bytes appended.
   * The "abc" + 3-byte CJK + "def" is wrapped in [" "].  We sweep
   * chunk_size 1..6 to land cuts in different places. */
  const char *prefix = "[\"abc";
  const char *suffix = "def\"]";
  /* Synthesize JSON: [\"abc\xe4\xb8\xaddef\"] */
  uint8_t buf[1 + 1 + 3 + 3 + 3 + 1 + 1 + 1];
  size_t pos = 0;
  memcpy(buf + pos, prefix, strlen(prefix));
  pos += strlen(prefix);
  buf[pos++] = 0xe4;
  buf[pos++] = 0xb8;
  buf[pos++] = 0xad;
  memcpy(buf + pos, suffix, strlen(suffix));
  pos += strlen(suffix);

  /* Hand-roll feed_chunked over raw bytes (the existing helper takes
   * a C string, and although all bytes here are < 0xff so strlen
   * would work, let's be explicit). */
  for (size_t cs = 1; cs <= 6; cs++) {
    NdecSaxContext ctx;
    Trace t = {0};
    ndec_sax_ctx_init(&ctx, &trace_reactor, &t);
    NdecChunkFeed s;
    ndec_chunkfeed_init(&s, &ctx);

    uint8_t fbuf[256];
    size_t carry = 0, src = 0;
    NdecChunkFeedStatus st = NDEC_CHUNKFEED_NEED_MORE;
    for (;;) {
      size_t take = pos - src;
      if (take > cs)
        take = cs;
      memcpy(fbuf + carry, buf + src, take);
      src += take;
      size_t flen         = carry + take;
      int is_final        = (src >= pos) ? 1 : 0;
      const uint8_t *tail = NULL;
      size_t tail_len     = 0;
      st                  = ndec_chunkfeed_push(&s, fbuf, flen, is_final, &tail, &tail_len);
      if (st == NDEC_CHUNKFEED_DONE || st == NDEC_CHUNKFEED_ERROR)
        break;
      if (tail_len > 0)
        memmove(fbuf, tail, tail_len);
      carry = tail_len;
      if (is_final)
        break;
    }
    cr_expect_eq(st, NDEC_CHUNKFEED_DONE, "chunk_size=%zu: expected DONE, got %d (exit=%u)", cs, (int)st,
                 (unsigned)ctx.exit_code);
  }
}

/* Outside-string invalid byte: the high-bit byte is between structurals;
 * the parser already rejected this via NDEC_ERR_SYNTAX before UTF-8
 * was added (0xff is neither op, ws, nor scalar-grammar). With the
 * validator now active, the rejection comes earlier as NDEC_ERR_UTF8.
 * Either way: ERROR. */
Test(chunkfeed_utf8, reject_high_byte_outside_string) {
  uint8_t buf[3] = {'[', 0xff, ']'};
  Trace t        = {0};
  cr_expect_eq(feed_bytes_all(buf, sizeof(buf), &t), NDEC_CHUNKFEED_ERROR);
}

/* Concrete exit-code check: reject_ff_in_string returns NDEC_ERR_UTF8
 * specifically (not NDEC_ERR_SYNTAX), so the host can distinguish bad
 * UTF-8 from grammar errors. */
Test(chunkfeed_utf8, exit_code_is_err_utf8) {
  uint8_t buf[6] = {'[', '"', 'a', 0xff, '"', ']'};
  cr_expect_eq(feed_bytes_all_get_exit(buf, sizeof(buf)), NDEC_ERR_UTF8);
}
