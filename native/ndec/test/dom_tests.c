/*
 * dom_tests.c -- end-to-end DOM parser tests.
 *
 * Covers string decoding (bytes-out correctness), escape handling,
 * \uXXXX / surrogate pairs, structural-scan reject paths, and structural shapes
 * including SIMD-chunk and short-input boundaries. These are regression
 * gates for extract.h / tape.h: any change that touches the string
 * scanner, escape handler, or structural-scan finish/sentinel logic should
 * keep all of these green.
 *
 * Run via `make test` or directly:
 *   build/dom_tests --filter=dom_escapes
 *   build/dom_tests --filter=dom_unicode/surrogate_pair
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <criterion/criterion.h>

#include "ndec/dom.h"
#include "ndec_libc_allocator.h"

/* --------------------------------------------------------------------
 * Helpers: parse + tape-walk
 *
 * dom_parse_str copies json into a freshly malloc'd buffer with 64
 * bytes of trailing 0x20 padding (the structurals/tape contract: the
 * SIMD scanners speculatively read past `len`). Caller owns the
 * returned buffer and must free both it and the json_dom.
 * -------------------------------------------------------------------- */

static uint8_t *dom_parse_str(const char *json, json_dom *d, int *out_err, json_dom_str_mode mode) {
  size_t len   = strlen(json);
  uint8_t *buf = (uint8_t *)malloc(len + 64);
  cr_assert_not_null(buf, "OOM");
  memcpy(buf, json, len);
  memset(buf + len, ' ', 64);

  memset(d, 0, sizeof(*d));
  d->allocator = NDEC_LIBC_ALLOCATOR;
  cr_assert_eq(dom_ensure_capacity(d, len), 0, "dom_ensure_capacity failed");
  switch (mode) {
  case JSON_DOM_STR_ZERO_COPY:
    *out_err = json_dom_parse_zc(d, buf, len);
    break;
  default:
    *out_err = json_dom_parse_copy(d, buf, len);
    break;
  }
  return buf;
}

/* Find the n-th STRING entry on the tape (0-indexed). Returns the
 * decoded byte pointer and writes the length via *out_len. Any parse
 * may also carry 'R' (raw, uncopied) or 'S' (copied, escape-free)
 * entries; we accept all three. If `out_is_raw` is
 * non-NULL it receives 1 for raw, 0 for copied (bytes in str_arena, length in
 * the tape word; no NUL terminator). cr_asserts on missing entry. */
static const uint8_t *find_string_n(json_dom *d, size_t n, uint32_t *out_len, int *out_is_raw) {
  size_t seen = 0;
  for (size_t i = 0; i < d->emit.doc.tape_len; i++) {
    uint8_t tag = (uint8_t)(d->emit.doc.tape[i] >> 56);
    if (tag == '"' || tag == 'S') {
      if (seen == n) {
        if (out_is_raw) *out_is_raw = 0;
        uint64_t payload = d->emit.doc.tape[i] & 0x00FFFFFFFFFFFFFFULL;
        return json_dom_get_string(&d->emit.doc, payload, out_len);
      }
      seen++;
    } else if (tag == 'R') {
      if (seen == n) {
        if (out_is_raw) *out_is_raw = 1;
        uint64_t payload = d->emit.doc.tape[i] & 0x00FFFFFFFFFFFFFFULL;
        return json_dom_get_string_raw(&d->emit.doc, payload, out_len);
      }
      seen++;
    }
  }
  cr_assert_fail("string #%zu not found on tape (saw %zu)", n, seen);
  return NULL;
}

/* Parse json, expect success, expect first STRING on tape to equal
 * `expected` byte-for-byte. */
static void expect_string(const char *json, const uint8_t *expected, uint32_t elen) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(json, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0, "parse should succeed: %s", json);

  uint32_t got_len;
  int is_raw         = 0;
  const uint8_t *got = find_string_n(&d, 0, &got_len, &is_raw);
  cr_assert_eq(got_len, elen, "string length mismatch for %s: got %u, want %u", json, got_len, elen);
  for (uint32_t i = 0; i < elen; i++) {
    cr_assert_eq(got[i], expected[i], "string byte %u mismatch for %s: got %02x, want %02x", i, json, got[i],
                 expected[i]);
  }
  /* String bodies in str_arena are NOT NUL-terminated; the tape word carries
   * the length. Skip the trailing-byte check. */
  (void)is_raw;

  json_dom_free(&d);
  free(buf);
}

/* Parse json, expect success. */
static void expect_ok(const char *json) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(json, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0, "parse should succeed: %s", json);
  json_dom_free(&d);
  free(buf);
}

/* Parse json, expect parse error (any non-zero return). */
static void expect_err(const char *json) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(json, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_neq(err, 0, "parse should fail: %s", json);
  json_dom_free(&d);
  free(buf);
}

/* String literal helper: byte sequence as a uint8_t array literal. */
#define BS(...) ((const uint8_t[]){__VA_ARGS__}), (sizeof((const uint8_t[]){__VA_ARGS__}))

/* --------------------------------------------------------------------
 * dom_strings: decoded byte sequence
 * -------------------------------------------------------------------- */

Test(dom_strings, empty) {
  expect_string("[\"\"]", (const uint8_t *)"", 0);
}

Test(dom_strings, single_byte) {
  expect_string("[\"a\"]", BS('a'));
}

Test(dom_strings, ascii_run) {
  expect_string("[\"hello\"]", BS('h', 'e', 'l', 'l', 'o'));
}

/* DOM_STR_CHUNK = 32 (AVX2) or 16 (NEON). A 64-char run forces at
 * least one full SIMD chunk advance even on AVX2, exercising the
 * `bs == 0 && qt == 0` fall-through. */
Test(dom_strings, simd_full_chunk) {
  const char *json = "[\"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"]";
  /* 64 'A' bytes */
  uint8_t expected[64];
  memset(expected, 'A', 64);
  expect_string(json, expected, 64);
}

/* Exact SIMD chunk boundary: 32 chars, then close quote at chunk[32]. */
Test(dom_strings, simd_chunk_boundary_exact) {
  /* 32 'B' bytes inside the string */
  expect_string("[\"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\"]",
                BS('B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B',
                   'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B', 'B'));
}

/* --------------------------------------------------------------------
 * dom_escapes: simple 1:1 escapes
 *
 * The pre-existing dom_handle_escape_ptr bug (offset/pointer mixup)
 * surfaced as wrong bytes when an escape sat after a copied prefix.
 * These three patterns specifically exercise the failure mode:
 *   - escape at start (no prefix)
 *   - escape after one byte
 *   - escape after >SIMD chunk worth of prefix
 * -------------------------------------------------------------------- */

Test(dom_escapes, all_simple) {
  expect_string("[\"\\n\"]", BS(0x0a));
  expect_string("[\"\\t\"]", BS(0x09));
  expect_string("[\"\\r\"]", BS(0x0d));
  expect_string("[\"\\b\"]", BS(0x08));
  expect_string("[\"\\f\"]", BS(0x0c));
  expect_string("[\"\\\"\"]", BS(0x22));
  expect_string("[\"\\\\\"]", BS(0x5c));
  expect_string("[\"\\/\"]", BS(0x2f));
}

Test(dom_escapes, escape_at_start) {
  expect_string("[\"\\nb\"]", BS(0x0a, 'b'));
}

Test(dom_escapes, escape_in_middle) {
  /* Original pre-existing bug: dom_handle_escape_ptr produced
   * 0a 6e 62 instead of 61 0a 62. Keep this test as a sentinel. */
  expect_string("[\"a\\nb\"]", BS('a', 0x0a, 'b'));
}

Test(dom_escapes, escape_after_short_prefix) {
  expect_string("[\"aa\\nbb\"]", BS('a', 'a', 0x0a, 'b', 'b'));
}

Test(dom_escapes, escape_after_long_prefix) {
  /* 32 'C' chars, escape, then 'D'; the escape lands past the first
   * SIMD chunk on AVX2, exercising the chunk-then-escape path. */
  expect_string("[\"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC\\nD\"]",
                BS('C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C',
                   'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 'C', 0x0a, 'D'));
}

Test(dom_escapes, literal_backslash_then_n) {
  /* "\\n" must decode to '\\' '\n', i.e. backslash followed by
   * literal 'n', not a newline. */
  expect_string("[\"\\\\n\"]", BS(0x5c, 'n'));
}

Test(dom_escapes, multiple_escapes) {
  expect_string("[\"a\\nb\\tc\"]", BS('a', 0x0a, 'b', 0x09, 'c'));
}

Test(dom_escapes, quote_in_string) {
  expect_string("[\"\\\"q\"]", BS(0x22, 'q'));
}

/* --------------------------------------------------------------------
 * dom_unicode: \uXXXX and surrogate pairs
 * -------------------------------------------------------------------- */

Test(dom_unicode, ascii_via_u) {
  /* A should decode to 'A' (1-byte UTF-8) */
  expect_string("[\"\\u0041\"]", BS('A'));
}

Test(dom_unicode, latin1_two_bytes) {
  /* U+00E9 'é' -> c3 a9 */
  expect_string("[\"\\u00e9\"]", BS(0xc3, 0xa9));
}

Test(dom_unicode, bmp_three_bytes) {
  /* U+4E2D '中' -> e4 b8 ad */
  expect_string("[\"\\u4e2d\"]", BS(0xe4, 0xb8, 0xad));
}

Test(dom_unicode, two_byte_boundary) {
  /* U+0080 -> c2 80 (lowest 2-byte UTF-8 codepoint) */
  expect_string("[\"\\u0080\"]", BS(0xc2, 0x80));
}

Test(dom_unicode, three_byte_boundary) {
  /* U+0800 -> e0 a0 80 (lowest 3-byte UTF-8 codepoint) */
  expect_string("[\"\\u0800\"]", BS(0xe0, 0xa0, 0x80));
}

Test(dom_unicode, surrogate_pair_clef) {
  /* G clef U+1D11E: 𝄞 -> f0 9d 84 9e (4-byte UTF-8) */
  expect_string("[\"\\uD834\\uDD1E\"]", BS(0xf0, 0x9d, 0x84, 0x9e));
}

Test(dom_unicode, surrogate_pair_lowercase_hex) {
  /* Same codepoint with lowercase hex */
  expect_string("[\"\\ud834\\udd1e\"]", BS(0xf0, 0x9d, 0x84, 0x9e));
}

Test(dom_unicode, unpaired_high_then_ascii) {
  /* \uD834 not followed by \u... -> U+FFFD then 'x' */
  expect_string("[\"\\uD834x\"]", BS(0xef, 0xbf, 0xbd, 'x'));
}

Test(dom_unicode, unpaired_high_then_non_low_surrogate) {
  /* \uD834A: the second \u parses but isn't a low surrogate ->
   * emit U+FFFD then 'A' */
  expect_string("[\"\\uD834\\u0041\"]", BS(0xef, 0xbf, 0xbd, 'A'));
}

Test(dom_unicode, lone_low_surrogate) {
  /* \uDC00 alone -> U+FFFD */
  expect_string("[\"\\uDC00\"]", BS(0xef, 0xbf, 0xbd));
}

Test(dom_unicode, hex_too_short) {
  /* \u12 is missing 2 hex digits */
  expect_err("[\"\\u12\"]");
}

Test(dom_unicode, hex_non_digit) {
  /* 'Z' is not a hex digit */
  expect_err("[\"\\u12ZZ\"]");
}

/* --------------------------------------------------------------------
 * dom_invalid: structurals / tape reject paths
 *
 * The unclosed-string and lone-quote cases both rely on the structural
 * scanner's prev_in_string finish check that was added when the tape
 * builder dropped its buf_end guards. If that check regresses, these
 * tests turn red.
 * -------------------------------------------------------------------- */

Test(dom_invalid, unclosed_string_in_array) {
  expect_err("[\"hello");
}

Test(dom_invalid, lone_open_quote) {
  /* Single '"' in the document -- exercises the structural scanner's
   * short-input (len < 64) path's prev_in_string check. */
  expect_err("\"");
}

Test(dom_invalid, bad_simple_escape) {
  /* '\q' is not a valid escape character. */
  expect_err("[\"\\q\"]");
}

Test(dom_invalid, short_atom_true) {
  expect_err("tru");
}

Test(dom_invalid, short_atom_false) {
  expect_err("fals");
}

Test(dom_invalid, short_atom_null) {
  expect_err("nul");
}

/* `true`/`false`/`null` followed by a non-structural byte must reject
 * (simdjson is_valid_{true,false,null}_atom semantics). The 4-byte
 * memcmp would otherwise match `true` and the trailing 'e' would be
 * silently accepted because the structural scanner emits no structural for it. */
Test(dom_invalid, true_with_trailing_byte) {
  expect_err("truee");
}

Test(dom_invalid, false_with_trailing_byte) {
  expect_err("falsee");
}

Test(dom_invalid, null_with_trailing_byte) {
  expect_err("nullx");
}

/* The same atoms ARE valid when followed by a structural or
 * whitespace byte. The 64-byte 0x20 padding makes EOF count as
 * whitespace, so a bare "true" / "false" / "null" document accepts. */
Test(dom_invalid, atom_at_eof_accepts) {
  expect_ok("true");
  expect_ok("false");
  expect_ok("null");
  expect_ok("true ");
  expect_ok("[true,false,null]");
}

Test(dom_invalid, empty_input) {
  /* Both DOM parse entries reject len == 0. */
  json_dom d;
  memset(&d, 0, sizeof(d));
  cr_assert_neq(json_dom_parse_copy(&d, (const uint8_t *)"", 0), 0);
  json_dom_free(&d);
}

Test(dom_invalid, unmatched_open_brace) {
  expect_err("{\"k\":1");
}

Test(dom_invalid, unmatched_close_brace) {
  expect_err("{\"k\":1}}");
}

Test(dom_invalid, trailing_garbage) {
  expect_err("[1] junk");
}

/* --------------------------------------------------------------------
 * dom_numbers: integer / float / bigint dispatch on the tape
 *
 * Exercises the cold ">19-digit integer -> double" fallback path
 * that lives in num.h's bigint label. With ndec_parse_bigint_f64
 * it sits behind a noinline call boundary; these tests guard
 * (a) that it's still wired up and (b) that the round-trip through
 * the stack scratch buffer doesn't lose precision.
 * -------------------------------------------------------------------- */

/* Find the n-th non-string scalar tape entry and return its tag.
 *
 * 'D' counts as a scalar and is ONE word, where l/u/d are two, so the skip is
 * per-tag rather than unconditional. */
static uint8_t find_scalar_tag_n(json_dom *d, size_t n) {
  size_t seen = 0;
  for (size_t i = 0; i < d->emit.doc.tape_len; i++) {
    uint8_t t = (uint8_t)(d->emit.doc.tape[i] >> 56);
    if (t == 'l' || t == 'u' || t == 'd' || t == 'D') {
      if (seen == n) return t;
      seen++;
      if (t != 'D') i++; /* skip value word */
    }
  }
  cr_assert_fail("scalar #%zu not found", n);
  return 0;
}

static double get_double_n(json_dom *d, size_t n) {
  size_t seen = 0;
  for (size_t i = 0; i < d->emit.doc.tape_len; i++) {
    uint8_t t = (uint8_t)(d->emit.doc.tape[i] >> 56);
    if (t == 'l' || t == 'u' || t == 'd' || t == 'D') {
      if (seen == n) {
        cr_assert_neq(t, 'D', "scalar #%zu is 'D' (source text); it has no value word", n);
        double dv;
        __builtin_memcpy(&dv, &d->emit.doc.tape[i + 1], 8);
        return dv;
      }
      seen++;
      if (t != 'D') i++;
    }
  }
  cr_assert_fail("scalar #%zu not found", n);
  return 0.0;
}

/* Return the n-th scalar's source text, which must be a 'D' entry. Payload is
 * (str_arena off, len) exactly as TAPE_STRING packs it. */
static void get_num_raw_n(json_dom *d, size_t n, const char **out, uint32_t *len_out) {
  size_t seen = 0;
  for (size_t i = 0; i < d->emit.doc.tape_len; i++) {
    uint64_t w = d->emit.doc.tape[i];
    uint8_t t  = (uint8_t)(w >> 56);
    if (t == 'l' || t == 'u' || t == 'd' || t == 'D') {
      if (seen == n) {
        cr_assert_eq(t, 'D', "scalar #%zu tag is '%c', want 'D'", n, t);
        *out     = (const char *)d->emit.doc.str_arena + (uint32_t)(w & 0xFFFFFFFFu);
        *len_out = (uint32_t)((w >> 32) & 0xFFFFFFu);
        return;
      }
      seen++;
      if (t != 'D') i++;
    }
  }
  cr_assert_fail("scalar #%zu not found", n);
}

/* A number kept as source text round-trips byte for byte. */
static void expect_num_raw(const char *json, const char *want) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(json, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0, "parse failed: %s", json);
  const char *text;
  uint32_t len;
  get_num_raw_n(&d, 0, &text, &len);
  cr_assert_eq(len, (uint32_t)strlen(want), "%s: len=%u want=%zu", json, len, strlen(want));
  cr_assert_eq(memcmp(text, want, len), 0, "%s: got=%.*s want=%s", json, (int)len, text, want);
  json_dom_free(&d);
  free(buf);
}

Test(dom_numbers, plain_int_tagged_int64) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[42]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);
  cr_assert_eq(find_scalar_tag_n(&d, 0), 'l');
  json_dom_free(&d);
  free(buf);
}

Test(dom_numbers, plain_float_tagged_double) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[3.14]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);
  cr_assert_eq(find_scalar_tag_n(&d, 0), 'd');
  json_dom_free(&d);
  free(buf);
}

Test(dom_numbers, max_uint64_tagged_uint64) {
  /* 18446744073709551615 = UINT64_MAX, fits in 'u'. */
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[18446744073709551615]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);
  cr_assert_eq(find_scalar_tag_n(&d, 0), 'u');
  json_dom_free(&d);
  free(buf);
}

Test(dom_numbers, bigint_overflow_tagged_num_raw) {
  /* 21 digits -- overflows uint64. No integer type holds it and the nearest
   * double is not the value written, so the token is kept as text. */
  expect_num_raw("[123456789012345678901]", "123456789012345678901");
}

Test(dom_numbers, negative_bigint_tagged_num_raw) {
  /* 20 digits negative -- exceeds INT64 range, same reasoning. */
  expect_num_raw("[-12345678901234567890]", "-12345678901234567890");
}

Test(dom_numbers, bigint_long_run_num_raw) {
  /* 50 digits: far past what any binary form carries. */
  expect_num_raw("[12345678901234567890123456789012345678901234567890]",
                 "12345678901234567890123456789012345678901234567890");
}

Test(dom_numbers, bigint_overflow_to_inf_kept_as_text) {
  /* 1 followed by 400 zeros -- past double's range, where atof yields +Inf.
   * JSON has no Inf literal, so the value cannot go on the tape as a double;
   * the token is well-formed JSON though, so the text is kept and it is the
   * binding target that decides (a float64 target fails, json.Number does not). */
  char json[420];
  char want[404];
  json[0] = '[';
  json[1] = '1';
  want[0] = '1';
  for (int i = 0; i < 400; i++) {
    json[2 + i] = '0';
    want[1 + i] = '0';
  }
  json[402] = ']';
  json[403] = '\0';
  want[401] = '\0';
  expect_num_raw(json, want);
}

Test(dom_numbers, float_bigint_overflow_to_inf_kept_as_text) {
  /* 30 significant digits + e400: the float-with-bigint delegate (digit_count
   * > 19), which bypasses the exponent > 308 gate. Lossy twice over, so text. */
  expect_num_raw("[123456789012345678901234567890e400]", "123456789012345678901234567890e400");
  expect_num_raw("[-123456789012345678901234567890e400]", "-123456789012345678901234567890e400");
}

Test(dom_numbers, float_to_inf_kept_as_text) {
  /* 1e400 in the float (non-bigint) path: exponent > 308 with a nonzero
   * mantissa. Same policy, reached through a different branch. */
  expect_num_raw("1e400", "1e400");
}

Test(dom_numbers, float_to_zero_underflow_accepts) {
  /* 1e-400 underflows to 0.0, valid result. */
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[1e-400]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);
  cr_assert_eq(find_scalar_tag_n(&d, 0), 'd');
  cr_assert_eq(get_double_n(&d, 0), 0.0);
  json_dom_free(&d);
  free(buf);
}

/* trunc path (>19 sig digits via ndec_parse_bigint_f64): parameters fed to
 * atof_i_finalize_f64 follow atof's contract (nd excludes the dot, exp is
 * the e value only, sig_start skips leading '0'/'.'). Each case below covers
 * a different combination -- long fraction, long integer part, mixed, leading
 * zeros, explicit exponent -- so a regression in the conversion would surface
 * here rather than in canada_geometry which tops out at nd=18.
 *
 * Either tag is correct here and which one appears is the point of the 'D' cut:
 * SIGNIFICANT digits decide it, so "0.00000000000000000123" is 21 characters but
 * 3 significant digits and stays 'd', while "1.2345678901234567890" is lossy and
 * becomes 'D'. Both are checked against strtod on the same bytes, so the
 * conversion stays covered either way -- for 'D' it runs at bind time, which is
 * what this calls directly. */
static void expect_double_eq_strtod(const char *json) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(json, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0, "parse failed: %s", json);

  double got;
  uint8_t tag = find_scalar_tag_n(&d, 0);
  if (tag == 'D') {
    const char *text;
    uint32_t len;
    get_num_raw_n(&d, 0, &text, &len);
    /* json is "[<token>]"; the tape text must be the token verbatim. */
    size_t want_len = strlen(json) - 2;
    cr_assert_eq((size_t)len, want_len, "%s: len=%u want=%zu", json, len, want_len);
    cr_assert_eq(memcmp(text, json + 1, len), 0, "%s: got=%.*s", json, (int)len, text);
    cr_assert_eq(ndec_parse_double((const uint8_t *)text, len, &got, d.emit.atof), 0, "convert: %s", json);
  } else {
    cr_assert_eq(tag, 'd', "%s: tag '%c', want 'd' or 'D'", json, tag);
    got = get_double_n(&d, 0);
  }
  double ref = strtod(json + 1, NULL); /* skip leading '[' */
  uint64_t gb, rb;
  __builtin_memcpy(&gb, &got, 8);
  __builtin_memcpy(&rb, &ref, 8);
  cr_assert_eq(gb, rb, "%s: ndec=%.17g strtod=%.17g", json, got, ref);
  json_dom_free(&d);
  free(buf);
}

Test(dom_numbers, long_fraction_matches_strtod) {
  expect_double_eq_strtod("[1.2345678901234567890]");
  expect_double_eq_strtod("[1.234567890123456789012345]");
  expect_double_eq_strtod("[0.123456789012345678901234567890]");
}

Test(dom_numbers, long_integer_part_matches_strtod) {
  expect_double_eq_strtod("[12345678901234567890.5]");
  expect_double_eq_strtod("[123456789012345678901234567890.5]");
  expect_double_eq_strtod("[12345678901234567890.987654321098]");
}

Test(dom_numbers, long_with_exponent_matches_strtod) {
  expect_double_eq_strtod("[12345678901234567890e2]");
  expect_double_eq_strtod("[1.2345678901234567890e25]");
  expect_double_eq_strtod("[1.234567890123456789012345e-300]");
  expect_double_eq_strtod("[1.234567890123456789012345e300]");
  /* 50-digit integer mantissa with explicit exponent: forces i to wrap
   * during accumulation, then bigint path re-scans from src. */
  expect_double_eq_strtod("[12345678901234567890123456789012345678901234567890e10]");
  /* Long integer part AND long fraction together with negative exponent. */
  expect_double_eq_strtod("[12345678901234567890.12345678901234567890e-50]");
}

Test(dom_numbers, leading_zero_long_fraction_matches_strtod) {
  expect_double_eq_strtod("[0.00000000000000000123]");
  expect_double_eq_strtod("[0.0000000000000000012345678901234567890]");
}

/* Bigint path must reject trailing junk just like the int/float paths.
 * atof stops at the first non-grammar byte so r.end may equal src + n
 * with garbage still ahead; the trailing is_non_delim check
 * in num.h's bigint label is what closes the gap. */
Test(dom_numbers, bigint_trailing_junk_rejects) {
  expect_err("[123456789012345678901A]");
  expect_err("[1234567890123456789012345678901234567890ABC]");
  expect_err("123456789012345678901A");
  expect_err("[123456789012345678901xyz]");
}

Test(dom_numbers, bigint_trailing_struct_accepts) {
  /* 21-digit bigint followed by structural bytes is well-formed. */
  expect_ok("[123456789012345678901,2]");
  expect_ok("[123456789012345678901]");
}

/* --------------------------------------------------------------------
 * dom_structure: containers, tape shape, length boundaries
 * -------------------------------------------------------------------- */

Test(dom_structure, empty_array) {
  expect_ok("[]");
}

Test(dom_structure, empty_object) {
  expect_ok("{}");
}

Test(dom_structure, array_of_ints) {
  expect_ok("[1,2,3]");
}

Test(dom_structure, single_key_object) {
  expect_ok("{\"k\":1}");
}

Test(dom_structure, nested_arrays) {
  expect_ok("[[[]]]");
}

Test(dom_structure, mixed_nested) {
  expect_ok("[{\"a\":[1,2,{\"b\":null}]}]");
}

Test(dom_structure, root_scalar_int) {
  expect_ok("42");
}

Test(dom_structure, root_scalar_string) {
  expect_ok("\"hi\"");
}

Test(dom_structure, root_scalar_atom) {
  expect_ok("true");
  expect_ok("false");
  expect_ok("null");
}

/* Stage1 has a `len < 64` short-input branch that fills sentinels and
 * runs the same finish check. Cover it. */
Test(dom_structure, short_input_array) {
  expect_ok("[1]");
}

Test(dom_structure, short_input_object) {
  expect_ok("{\"a\":1}");
}

/* Boundary: input exactly 64 bytes: the structural scanner takes the
 * full-chunk path not the short-input path. */
Test(dom_structure, sixty_four_byte_input) {
  /* Pad with whitespace inside to hit exactly 64 bytes total */
  const char *json = "[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23]";
  cr_assert_eq(strlen(json), 61);
  /* Add 3 chars of ws to make 64 */
  expect_ok("[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23]   ");
}

/* Max-depth stress: JSON_DOM_MAX_DEPTH defined in tape.h is 256.
 * Build an array nested ~250 deep; it should succeed. 260 deep should
 * fail with the depth-overflow error. */
static void run_nested_arrays(int depth, int expect_success) {
  size_t buflen = (size_t)depth * 2 + 16;
  char *buf     = (char *)malloc(buflen);
  cr_assert_not_null(buf);
  for (int i = 0; i < depth; i++)
    buf[i] = '[';
  for (int i = 0; i < depth; i++)
    buf[depth + i] = ']';
  buf[depth * 2] = '\0';
  if (expect_success) expect_ok(buf);
  else
    expect_err(buf);
  free(buf);
}

Test(dom_structure, deep_nesting_within_limit) {
  run_nested_arrays(250, 1);
}

Test(dom_structure, deep_nesting_over_limit) {
  run_nested_arrays(260, 0);
}

/* Stress: large string forces the dom_parse_string SIMD loop through
 * many chunks. This catches unconditional-store bugs (writes past
 * close quote leaking into adjacent str_arena entries) when a second
 * string follows. */
Test(dom_structure, two_long_strings_back_to_back) {
  /* 100 'X' + comma + 100 'Y' */
  char buf[256];
  buf[0] = '[';
  buf[1] = '"';
  for (int i = 0; i < 100; i++)
    buf[2 + i] = 'X';
  buf[102] = '"';
  buf[103] = ',';
  buf[104] = '"';
  for (int i = 0; i < 100; i++)
    buf[105 + i] = 'Y';
  buf[205] = '"';
  buf[206] = ']';
  buf[207] = '\0';

  json_dom d;
  int err;
  uint8_t *raw = dom_parse_str(buf, &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);

  uint32_t l0, l1;
  int r0 = 0, r1 = 0;
  const uint8_t *s0 = find_string_n(&d, 0, &l0, &r0);
  const uint8_t *s1 = find_string_n(&d, 1, &l1, &r1);
  cr_assert_eq(l0, 100);
  cr_assert_eq(l1, 100);
  for (int i = 0; i < 100; i++) {
    cr_assert_eq(s0[i], 'X', "s0[%d] = %02x, want 'X'", i, s0[i]);
    cr_assert_eq(s1[i], 'Y', "s1[%d] = %02x, want 'Y'", i, s1[i]);
  }
  /* Under COPY both strings are copied into str_arena (no NUL terminator now;
   * tape word carries the length). */
  cr_assert_eq(r0, 0, "COPY mode: strings must be copied");
  cr_assert_eq(r1, 0, "COPY mode: strings must be copied");

  json_dom_free(&d);
  free(raw);
}

/* Zero-copy mode: assert tag dispatch on mixed-content documents.
 *   - first string has an escape  -> copied ('"')
 *   - second string has no escape -> raw   ('R'), aliases src buf
 *   - 24-bit length cap stays implicit (small inputs); separately,
 *     verify the raw payload's length field round-trips. */
Test(dom_zero_copy, mixed_raw_and_copied) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[\"a\\nb\",\"hello\"]", &d, &err, JSON_DOM_STR_ZERO_COPY);
  cr_assert_eq(err, 0);

  uint32_t l0, l1;
  int r0 = 0, r1 = 0;
  const uint8_t *s0 = find_string_n(&d, 0, &l0, &r0);
  const uint8_t *s1 = find_string_n(&d, 1, &l1, &r1);

  /* "a\nb" -> 3 bytes, copied (had escape). */
  cr_assert_eq(r0, 0, "string with escape must be copied");
  cr_assert_eq(l0, 3);
  cr_assert_eq(s0[0], 'a');
  cr_assert_eq(s0[1], 0x0a);
  cr_assert_eq(s0[2], 'b');

  /* "hello" -> 5 bytes, raw, aliases source. */
  cr_assert_eq(r1, 1, "escape-free string must be raw");
  cr_assert_eq(l1, 5);
  cr_assert_eq(s1[0], 'h');
  /* Source buffer at this offset is exactly "hello" + closing quote. */
  cr_assert_eq(s1, d.emit.doc.src_buf + (s1 - d.emit.doc.src_buf));
  cr_assert_eq(buf[(s1 - buf) + 5], '"', "raw ptr must end at close quote");

  json_dom_free(&d);
  free(buf);
}

/* Copy mode: escape-free bodies carry TAPE_STRING_FREE ('S'), escaped ones
 * stay TAPE_STRING ('"'). Both resolve through str_arena. */
Test(dom_copy, strfree_tag_dispatch) {
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[\"a\\nb\",\"hello\"]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_eq(err, 0);

  uint8_t tags[2] = {0, 0};
  size_t seen     = 0;
  for (size_t i = 0; i < d.emit.doc.tape_len && seen < 2; i++) {
    uint8_t t = (uint8_t)(d.emit.doc.tape[i] >> 56);
    if (t == '"' || t == 'S' || t == 'R') tags[seen++] = t;
  }
  cr_assert_eq(seen, 2);
  cr_assert_eq(tags[0], '"', "escaped body must stay TAPE_STRING");
  cr_assert_eq(tags[1], 'S', "escape-free body must be TAPE_STRING_FREE");

  uint32_t l1;
  int r1 = 0;
  const uint8_t *s1 = find_string_n(&d, 1, &l1, &r1);
  cr_assert_eq(r1, 0);
  cr_assert_eq(l1, 5);
  cr_assert_eq(s1[0], 'h');

  json_dom_free(&d);
  free(buf);
}

/* Both strings escape-free: ZERO_COPY must keep both raw and aliasing
 * the source buffer. */
Test(dom_zero_copy, two_long_strings_both_raw) {
  char buf[256];
  buf[0] = '[';
  buf[1] = '"';
  for (int i = 0; i < 100; i++)
    buf[2 + i] = 'X';
  buf[102] = '"';
  buf[103] = ',';
  buf[104] = '"';
  for (int i = 0; i < 100; i++)
    buf[105 + i] = 'Y';
  buf[205] = '"';
  buf[206] = ']';
  buf[207] = '\0';

  json_dom d;
  int err;
  uint8_t *raw = dom_parse_str(buf, &d, &err, JSON_DOM_STR_ZERO_COPY);
  cr_assert_eq(err, 0);

  uint32_t l0, l1;
  int r0 = 0, r1 = 0;
  const uint8_t *s0 = find_string_n(&d, 0, &l0, &r0);
  const uint8_t *s1 = find_string_n(&d, 1, &l1, &r1);
  cr_assert_eq(r0, 1, "expected raw tag for first string");
  cr_assert_eq(r1, 1, "expected raw tag for second string");
  cr_assert_eq(s0, raw + 2, "raw ptr should alias source buffer");
  cr_assert_eq(s1, raw + 105, "raw ptr should alias source buffer");

  json_dom_free(&d);
  free(raw);
}

/* Lazy copy: a string whose escape appears AFTER one SIMD chunk's worth
 * of clean bytes. The state machine must memcpy the leading clean chunk
 * into str_arena before switching to the eager loop. */
Test(dom_zero_copy, escape_after_clean_prefix) {
  /* 40 'X' then '\n' then 'Y' -- forces the raw scanner to consume at
   * least one full chunk (16/32 bytes) before hitting the backslash. */
  char src[80];
  size_t n = 0;
  src[n++] = '[';
  src[n++] = '"';
  for (int i = 0; i < 40; i++)
    src[n++] = 'X';
  src[n++] = '\\';
  src[n++] = 'n';
  src[n++] = 'Y';
  src[n++] = '"';
  src[n++] = ']';
  src[n]   = '\0';

  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str(src, &d, &err, JSON_DOM_STR_ZERO_COPY);
  cr_assert_eq(err, 0);

  uint32_t got_len;
  int is_raw         = 0;
  const uint8_t *got = find_string_n(&d, 0, &got_len, &is_raw);
  cr_assert_eq(is_raw, 0, "string with escape must be copied even if escape is late");
  cr_assert_eq(got_len, 42);
  for (int i = 0; i < 40; i++)
    cr_assert_eq(got[i], 'X', "prefix copy mismatch at %d", i);
  cr_assert_eq(got[40], 0x0a);
  cr_assert_eq(got[41], 'Y');

  json_dom_free(&d);
  free(buf);
}

/* --------------------------------------------------------------------
 * dom_utf8: simdjson lookup4 UTF-8 validator integration
 *
 * Stage1 now folds the per-block UTF-8 checker (utf8.h) into
 * its scan loop. Failure modes covered: surrogate codepoint (U+D800..),
 * overlong forms (2/3/4-byte), bytes > U+10FFFF, lone continuations,
 * truncated multibyte tails, and lead-followed-by-lead. Each case is
 * inserted as a raw byte sequence inside a JSON string so it traverses
 * the full structurals -> tape path the production parser uses.
 * -------------------------------------------------------------------- */

/* Build [<bytes>] with `bytes` raw between two JSON quotes. Caller
 * must ensure the bytes don't include 0x22 / 0x5c (would terminate
 * the string or kick the escape handler). */
static void parse_raw_in_string(const uint8_t *bytes, size_t n, int *out_err) {
  size_t total = n + 4; /* [ " ... " ] */
  char *buf    = (char *)malloc(total + 1);
  cr_assert_not_null(buf);
  buf[0] = '[';
  buf[1] = '"';
  memcpy(buf + 2, bytes, n);
  buf[2 + n] = '"';
  buf[3 + n] = ']';
  buf[total] = '\0';

  json_dom d;
  int err;
  uint8_t *raw = dom_parse_str(buf, &d, &err, JSON_DOM_STR_COPY);
  *out_err     = err;
  json_dom_free(&d);
  free(raw);
  free(buf);
}

#define PARSE_BYTES_OK(...)                                                                                       \
  do {                                                                                                            \
    const uint8_t v[] = __VA_ARGS__;                                                                              \
    int err;                                                                                                      \
    parse_raw_in_string(v, sizeof(v), &err);                                                                      \
    cr_assert_eq(err, 0, "should accept valid UTF-8");                                                            \
  } while (0)

#define PARSE_BYTES_ERR(...)                                                                                      \
  do {                                                                                                            \
    const uint8_t v[] = __VA_ARGS__;                                                                              \
    int err;                                                                                                      \
    parse_raw_in_string(v, sizeof(v), &err);                                                                      \
    cr_assert_neq(err, 0, "should reject invalid UTF-8");                                                         \
  } while (0)

Test(dom_utf8, valid_two_byte) {
  /* U+00A9 -> c2 a9 (copyright sign) */
  PARSE_BYTES_OK({0xc2, 0xa9});
}

Test(dom_utf8, valid_three_byte) {
  /* U+4E2D -> e4 b8 ad (CJK ideograph) */
  PARSE_BYTES_OK({0xe4, 0xb8, 0xad});
}

Test(dom_utf8, valid_four_byte) {
  /* U+1F600 grinning face -> f0 9f 98 80 */
  PARSE_BYTES_OK({0xf0, 0x9f, 0x98, 0x80});
}

Test(dom_utf8, valid_max_codepoint) {
  /* U+10FFFF -> f4 8f bf bf (largest legal codepoint) */
  PARSE_BYTES_OK({0xf4, 0x8f, 0xbf, 0xbf});
}

Test(dom_utf8, reject_surrogate_low) {
  /* U+D800 -> ed a0 80 (lone high-surrogate code point not allowed
   * in UTF-8; legal \uXXXX surrogate PAIRS go through dom_unicode). */
  PARSE_BYTES_ERR({0xed, 0xa0, 0x80});
}

Test(dom_utf8, reject_surrogate_high) {
  /* U+DFFF -> ed bf bf */
  PARSE_BYTES_ERR({0xed, 0xbf, 0xbf});
}

Test(dom_utf8, reject_overlong_2) {
  /* 2-byte form of U+0000 */
  PARSE_BYTES_ERR({0xc0, 0x80});
}

Test(dom_utf8, reject_overlong_3) {
  /* 3-byte form of U+007F */
  PARSE_BYTES_ERR({0xe0, 0x80, 0xaf});
}

Test(dom_utf8, reject_overlong_4) {
  /* 4-byte form of U+0000 */
  PARSE_BYTES_ERR({0xf0, 0x80, 0x80, 0xaf});
}

Test(dom_utf8, reject_too_large) {
  /* U+110000 -> f4 90 80 80, just past the legal range */
  PARSE_BYTES_ERR({0xf4, 0x90, 0x80, 0x80});
}

Test(dom_utf8, reject_5byte_lead) {
  /* 0xf8 starts a 5-byte sequence which doesn't exist in UTF-8 */
  PARSE_BYTES_ERR({0xf8, 0x80, 0x80, 0x80, 0x80});
}

Test(dom_utf8, reject_lone_continuation) {
  /* 0x80 in isolation is a continuation byte without a lead */
  PARSE_BYTES_ERR({'a', 0x80, 'b'});
}

Test(dom_utf8, reject_truncated_2byte) {
  /* c2 needs one continuation; followed by close-quote should fail
   * when the validator sees the next byte is not a continuation. */
  PARSE_BYTES_ERR({0xc2});
}

Test(dom_utf8, reject_truncated_3byte) {
  PARSE_BYTES_ERR({0xe0, 0xa0});
}

Test(dom_utf8, reject_truncated_4byte) {
  PARSE_BYTES_ERR({0xf0, 0x90, 0x80});
}

Test(dom_utf8, reject_lead_after_lead) {
  /* Two leads in a row: the first lead's required continuation is
   * itself a lead, which is malformed. */
  PARSE_BYTES_ERR({0xc2, 0xc2, 0x80});
}

Test(dom_utf8, reject_ff_byte) {
  /* 0xff never appears in valid UTF-8 */
  PARSE_BYTES_ERR({0xff});
}

/* Multibyte sequence that crosses a 64-byte SIMD chunk boundary.
 * Build 63 ASCII bytes, then a 3-byte CJK character starting at
 * offset 64 (which lands in chunk 2 of the structural scanner's pipeline).
 * The lead sits at the chunk boundary so prev1/prev2 in the next block must
 * see it via prev_input_block carry. */
Test(dom_utf8, valid_multibyte_at_chunk_boundary) {
  uint8_t v[63 + 3];
  for (int i = 0; i < 63; i++)
    v[i] = 'A';
  v[63] = 0xe4;
  v[64] = 0xb8;
  v[65] = 0xad;
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_eq(err, 0);
}

/* Same shape but truncate the 3-byte sequence right at the boundary
 * to validate that prev_incomplete carries across blocks. */
Test(dom_utf8, reject_truncated_at_chunk_boundary) {
  uint8_t v[63 + 1];
  for (int i = 0; i < 63; i++)
    v[i] = 'A';
  v[63] = 0xe4; /* lead expecting 2 continuations, none follow */
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0);
}

/* Outside-string invalid UTF-8: bytes between structurals get scanned
 * by the structural scanner too, so a lone 0xff outside any string still
 * rejects. */
Test(dom_utf8, reject_invalid_outside_string) {
  /* [\xff]: 0xff is not inside any string; the structural scanner's UTF-8 checker
   * still sees it. */
  json_dom d;
  int err;
  uint8_t *buf = dom_parse_str("[\xff]", &d, &err, JSON_DOM_STR_COPY);
  cr_assert_neq(err, 0);
  json_dom_free(&d);
  free(buf);
}

/* The headline "bad byte hidden in a long ASCII string" cases: bury
 * one invalid UTF-8 byte deep inside a long ASCII run so the bug is
 * not at a chunk boundary or at either end. Stage1 must still flag
 * it because the checker scans every byte regardless of the JSON
 * grammar context. */
Test(dom_utf8, reject_ff_in_middle_of_long_string) {
  /* 50 'A' + 0xff + 50 'B' inside one JSON string. */
  uint8_t v[101];
  for (int i = 0; i < 50; i++)
    v[i] = 'A';
  v[50] = 0xff;
  for (int i = 0; i < 50; i++)
    v[51 + i] = 'B';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0, "0xff buried in ASCII string must reject");
}

Test(dom_utf8, reject_lone_continuation_in_middle_of_long_string) {
  /* 80 'A' + 0x80 + 80 'B'; the 0x80 byte is a continuation
   * appearing without a preceding multibyte lead, well past any
   * chunk boundary. */
  uint8_t v[161];
  for (int i = 0; i < 80; i++)
    v[i] = 'A';
  v[80] = 0x80;
  for (int i = 0; i < 80; i++)
    v[81 + i] = 'B';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0, "lone continuation buried in ASCII string must reject");
}

Test(dom_utf8, reject_truncated_lead_in_middle_of_long_string) {
  /* 100 'A' + 0xc2 (lead expecting continuation) + 'A' + ...; the
   * byte after 0xc2 is ASCII, which is not a valid continuation. */
  uint8_t v[201];
  for (int i = 0; i < 100; i++)
    v[i] = 'A';
  v[100] = 0xc2;
  for (int i = 0; i < 100; i++)
    v[101 + i] = 'A';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0, "truncated lead buried in ASCII string must reject");
}

Test(dom_utf8, reject_overlong_in_middle_of_long_string) {
  /* 100 'A' + overlong null (c0 80) + 100 'B'; overlong forms are
   * the security-relevant failure mode (encode NUL or '/' in a
   * non-canonical way to dodge filters). */
  uint8_t v[202];
  for (int i = 0; i < 100; i++)
    v[i] = 'A';
  v[100] = 0xc0;
  v[101] = 0x80;
  for (int i = 0; i < 100; i++)
    v[102 + i] = 'B';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0, "overlong NUL buried in ASCII string must reject");
}

Test(dom_utf8, reject_surrogate_in_middle_of_long_string) {
  /* 100 'A' + lone high surrogate U+D800 (ed a0 80) + 100 'B' */
  uint8_t v[203];
  for (int i = 0; i < 100; i++)
    v[i] = 'A';
  v[100] = 0xed;
  v[101] = 0xa0;
  v[102] = 0x80;
  for (int i = 0; i < 100; i++)
    v[103 + i] = 'B';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_neq(err, 0, "surrogate codepoint buried in ASCII string must reject");
}

/* Negative control: a *valid* long string with a multibyte char in
 * the middle must still parse. Pins the difference: rejection above
 * is about the byte sequence, not just length. */
Test(dom_utf8, valid_multibyte_in_middle_of_long_string) {
  /* 100 'A' + valid 3-byte CJK U+4E2D (e4 b8 ad) + 100 'B' */
  uint8_t v[203];
  for (int i = 0; i < 100; i++)
    v[i] = 'A';
  v[100] = 0xe4;
  v[101] = 0xb8;
  v[102] = 0xad;
  for (int i = 0; i < 100; i++)
    v[103 + i] = 'B';
  int err;
  parse_raw_in_string(v, sizeof(v), &err);
  cr_assert_eq(err, 0, "valid multibyte buried in ASCII string must accept");
}

/* --------------------------------------------------------------------
 * dom_allocator: custom allocator routing
 *
 * Pins the allocator interface: a json_dom with a custom allocator
 * must route every internal allocation (structural_indexes, tape,
 * str_arena, atof, str_arena grow) through the caller's vtable. The
 * counting allocator here delegates to libc but tracks live bytes so
 * we can assert (a) it was actually called and (b) json_dom_free
 * releases everything.
 * -------------------------------------------------------------------- */

typedef struct count_ctx {
  size_t live_bytes;
  size_t alloc_calls;
  size_t free_calls;
} count_ctx;

static void *count_realloc(void *ctx_, void *ptr, size_t old_size, size_t new_size) {
  count_ctx *c = (count_ctx *)ctx_;
  c->alloc_calls++;
  c->live_bytes -= old_size;
  c->live_bytes += new_size;
  if (new_size == 0) {
    free(ptr);
    return NULL;
  }
  return realloc(ptr, new_size);
}
static void count_free(void *ctx_, void *ptr, size_t old_size) {
  count_ctx *c = (count_ctx *)ctx_;
  c->free_calls++;
  c->live_bytes -= old_size;
  free(ptr);
}

Test(dom_allocator, routes_through_vtable) {
  count_ctx cc = {0};

  const char *json = "[\"a\\nb\",\"hello\"]";
  size_t len       = strlen(json);
  uint8_t *buf     = (uint8_t *)malloc(len + 64);
  cr_assert_not_null(buf, "OOM");
  memcpy(buf, json, len);
  memset(buf + len, ' ', 64);

  json_dom d;
  memset(&d, 0, sizeof(d));
  d.allocator = (ndec_allocator){&cc, count_realloc, count_free};

  cr_assert_eq(dom_ensure_capacity(&d, len), 0);
  /* 4 allocations: structural_indexes, tape, str_arena, atof. */
  cr_assert_eq(cc.alloc_calls, 4, "ensure_capacity should make 4 allocations");
  cr_assert(cc.live_bytes > 0, "live_bytes should be positive after alloc");

  /* Parse a small doc; ZERO_COPY may trigger one more str_arena grow. */
  int err = json_dom_parse_zc(&d, buf, len);
  cr_assert_eq(err, 0);

  size_t allocs_before_free = cc.alloc_calls;
  json_dom_free(&d);
  /* json_dom_free makes 4 free calls (atof, tape, str_arena, structural). */
  cr_assert_eq(cc.free_calls, 4, "json_dom_free should free 4 allocations");
  cr_assert_eq(cc.live_bytes, 0, "all bytes should be returned after free");
  /* alloc_calls unchanged by free (free is a separate counter). */
  cr_assert_eq(cc.alloc_calls, allocs_before_free, "free must not call realloc");
  free(buf);
}

Test(dom_allocator, zero_init_refuses_alloc) {
  /* Zero-init json_dom leaves the allocator as {NULL, NULL}, the
   * "uninitialized" state. dom_ensure_capacity must refuse (return -1)
   * rather than silently fall back to libc: the embeddable core has no
   * libc dependency, so a forgotten allocator is a caller bug we want
   * surfaced loudly. json_dom_free on the same zero-init d stays safe
   * (no-op frees). */
  json_dom d;
  memset(&d, 0, sizeof(d));
  cr_assert_neq(dom_ensure_capacity(&d, 64), 0, "ensure_capacity must fail without an installed allocator");
  cr_assert_null(d.emit.doc.tape, "nothing should be allocated on failure");
  cr_assert_null(d.emit.doc.str_arena, "nothing should be allocated on failure");
  cr_assert_null(d.structural_indexes, "nothing should be allocated on failure");
  cr_assert_null(d.emit.atof, "nothing should be allocated on failure");
  json_dom_free(&d);
}

/* --------------------------------------------------------------------
 * Scan-derived tape bound (ndec_scan_tape_words)
 *
 * The bind path sizes its tape arena from the plane populations the root scan
 * counts, so an understated bound there is not a slow parse but C writing past a
 * Go-owned buffer that has no bounds check. These tests are the layer with teeth:
 * they compare the bound against a real tape, with no allocator slack in between
 * to absorb a mistake.
 *
 * The DOM tape is the reference because it is the same tape builder; a merged tape
 * only ever adds seams, which the bound budgets separately.
 * -------------------------------------------------------------------- */

/* Bound vs. the tape a document actually produces. Deliberately includes the
 * shapes where the bound is tightest: a bare scalar (2 words for 1 structural),
 * dense integers, and containers that hold nothing. */
Test(scan_tape_bound, covers_actual_tape) {
  static const char *cases[] = {
      "1",
      "true",
      "null",
      "-1.5e300",
      "\"s\"",
      "{}",
      "[]",
      "[1]",
      "[1,2,3]",
      "{\"a\":1}",
      "{\"a\":1,\"b\":2,\"c\":3}",
      "[1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1]",
      "{\"a\":{\"b\":{\"c\":[1,2,3,{\"d\":null}]}}}",
      "[\"aa\",\"bb\",\"cc\",\"dd\"]",
      "[{},{},{},{},{},{},{},{}]",
      "[[[[[[1]]]]]]",
      "[-1.5e300,2.25,-0.0,1e-300]",
      "{\"\":\"\"}",
      "[\"\",\"\",\"\"]",
  };
  for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
    const char *json = cases[i];
    size_t len       = strlen(json);
    uint8_t *buf     = (uint8_t *)malloc(len + 64);
    cr_assert_not_null(buf, "OOM");
    memcpy(buf, json, len);
    memset(buf + len, ' ', 64);
    uint32_t *idx = (uint32_t *)malloc((len + 64) * sizeof(uint32_t));
    cr_assert_not_null(idx, "OOM");

    uint32_t n_idx   = 0;
    NdecPlanePop pop = {0, 0, 0};
    int err          = ndec_scan_structurals_counted(buf, len, idx, &n_idx, (uint32_t)(len + 64), &pop);
    cr_assert_eq(err, 0, "scan failed for %s", json);
    uint32_t bound = ndec_scan_tape_words(pop, 0);

    json_dom d;
    int perr      = 0;
    uint8_t *dbuf = dom_parse_str(json, &d, &perr, JSON_DOM_STR_COPY);
    cr_assert_eq(perr, 0, "parse failed for %s", json);
    cr_assert_geq(bound, (uint32_t)d.emit.doc.tape_len,
                  "bound %u understates the %zu-word tape for %s: the bind path sizes a "
                  "Go-owned arena from this and C does not bounds-check tape writes",
                  bound, (size_t)d.emit.doc.tape_len, json);
    json_dom_free(&d);
    free(dbuf);
    free(idx);
    free(buf);
  }
}

/* The DOM sizing bound over the same corpus. The counted entry derives it as
 * n_idx + scalars + 3: op and the open quotes merge into the structural total
 * the scan reports, and the closes are the other half of quotes. This test
 * pins both the identity against the per-plane formula and the bound's
 * dominance over the actual tape: an understated bound is C writing past a
 * Go-owned buffer, which has no bounds check. */
Test(dom_tape_bound, covers_actual_tape) {
  static const char *cases[] = {
      "1",
      "true",
      "null",
      "-1.5e300",
      "\"s\"",
      "{}",
      "[]",
      "[1]",
      "[1,2,3]",
      "{\"a\":1}",
      "{\"a\":1,\"b\":2,\"c\":3}",
      "[1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1]",
      "{\"a\":{\"b\":{\"c\":[1,2,3,{\"d\":null}]}}}",
      "[\"aa\",\"bb\",\"cc\",\"dd\"]",
      "[{},{},{},{},{},{},{},{}]",
      "[[[[[1]]]]]",
      "[-1.5e300,2.25,-0.0,1e-300]",
      "{\"\":\"\"}",
      "[\"\",\"\",\"\"]",
      "{\"key\":\"a string long enough that words are far cheaper than bytes\"}",
      "[18446744073709551615,18446744073709551615]",
  };
  for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
    const char *json = cases[i];
    size_t len       = strlen(json);
    uint8_t *buf     = (uint8_t *)malloc(len + 64);
    cr_assert_not_null(buf, "OOM");
    memcpy(buf, json, len);
    memset(buf + len, ' ', 64);
    uint32_t *idx = (uint32_t *)malloc((len + 64) * sizeof(uint32_t));
    cr_assert_not_null(idx, "OOM");

    uint32_t n_idx    = 0;
    uint32_t scalars  = 0;
    int err           = ndec_scan_structurals_strict_scount(buf, len, idx, &n_idx, (uint32_t)(len + 64), &scalars);
    uint32_t bound    = n_idx + scalars + 3;
    cr_assert_eq(err, 0, "scan failed for %s", json);

    /* Identity pin: the scalar-only derivation equals the per-plane formula,
     * tying the one-popcount count to the plane accounting. */
    uint32_t n2      = 0;
    NdecPlanePop pop = {0, 0, 0};
    cr_assert_eq(ndec_scan_structurals_strict_counted(buf, len, idx, &n2, (uint32_t)(len + 64), &pop), 0);
    cr_assert_eq(n2, n_idx, "scount and counted scans disagree on n_idx for %s", json);
    cr_assert_eq(bound, pop.op + pop.quotes / 2 + 2 * pop.scalars + 3,
                 "bound identity broken for %s: %u vs %u", json, bound,
                 pop.op + pop.quotes / 2 + 2 * pop.scalars + 3);

    json_dom d;
    int perr      = 0;
    uint8_t *dbuf = dom_parse_str(json, &d, &perr, JSON_DOM_STR_COPY);
    cr_assert_eq(perr, 0, "parse failed for %s", json);
    cr_assert_geq(bound, (uint32_t)d.emit.doc.tape_len,
                  "bound %u understates the %zu-word tape for %s: the counted DOM entry "
                  "sizes a Go-owned arena from this and C does not bounds-check tape writes",
                  bound, (size_t)d.emit.doc.tape_len, json);
    json_dom_free(&d);
    free(dbuf);
    free(idx);
    free(buf);
  }
}

/* The plane counts themselves. quotes counts BOTH delimiters of every string, and
 * must stay even across a chunk boundary: halving a per-chunk count would lose the
 * odd one whenever a string spans 64 bytes. */
Test(scan_tape_bound, plane_counts) {
  struct {
    const char *json;
    uint32_t op, quotes, scalars;
  } cases[] = {
      {"{}", 2, 0, 0},
      {"[1,2,3]", 4, 0, 3},
      {"{\"a\":1}", 3, 2, 1},
      {"{\"a\":\"b\"}", 3, 4, 0},
      {"[true,false,null]", 4, 0, 3},
      {"{\"a\":{\"b\":[1,2]}}", 9, 4, 2},
  };
  for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
    size_t len   = strlen(cases[i].json);
    uint8_t *buf = (uint8_t *)malloc(len + 64);
    memcpy(buf, cases[i].json, len);
    memset(buf + len, ' ', 64);
    uint32_t *idx    = (uint32_t *)malloc((len + 64) * sizeof(uint32_t));
    uint32_t n_idx   = 0;
    NdecPlanePop pop = {0, 0, 0};
    cr_assert_eq(ndec_scan_structurals_counted(buf, len, idx, &n_idx, (uint32_t)(len + 64), &pop), 0);
    cr_assert_eq(pop.op, cases[i].op, "op for %s", cases[i].json);
    cr_assert_eq(pop.quotes, cases[i].quotes, "quotes for %s", cases[i].json);
    cr_assert_eq(pop.scalars, cases[i].scalars, "scalars for %s", cases[i].json);
    free(idx);
    free(buf);
  }
}

/* A string straddling the 64-byte chunk boundary: its two quotes land in different
 * chunks, so the total is what must stay even. */
Test(scan_tape_bound, quotes_even_across_chunk_boundary) {
  for (size_t pad = 55; pad < 75; pad++) {
    char json[256];
    size_t n  = 0;
    json[n++] = '{';
    json[n++] = '"';
    for (size_t i = 0; i < pad; i++)
      json[n++] = 'a';
    json[n++] = '"';
    json[n++] = ':';
    json[n++] = '1';
    json[n++] = '}';
    json[n]   = 0;

    uint8_t *buf = (uint8_t *)malloc(n + 64);
    memcpy(buf, json, n);
    memset(buf + n, ' ', 64);
    uint32_t *idx    = (uint32_t *)malloc((n + 64) * sizeof(uint32_t));
    uint32_t n_idx   = 0;
    NdecPlanePop pop = {0, 0, 0};
    cr_assert_eq(ndec_scan_structurals_counted(buf, n, idx, &n_idx, (uint32_t)(n + 64), &pop), 0);
    cr_assert_eq(pop.quotes % 2, 0, "pad=%zu: %u quotes is odd, so a string lost a delimiter", pad, pop.quotes);
    cr_assert_eq(pop.quotes, 2, "pad=%zu", pad);
    free(idx);
    free(buf);
  }
}

Test(scan_tape_bound, strict_counted_matches_valid_scan) {
  const uint8_t json[] = "{\"key\":\"世界\",\"n\":1}";
  const size_t len     = sizeof(json) - 1;
  uint32_t lax_idx[len + 64];
  uint32_t strict_idx[len + 64];
  uint32_t lax_n = 0, strict_n = 0;
  NdecPlanePop lax_pop = {0, 0, 0}, strict_pop = {0, 0, 0};

  cr_assert_eq(ndec_scan_structurals_counted(json, len, lax_idx, &lax_n, len + 64, &lax_pop), 0);
  cr_assert_eq(ndec_scan_structurals_strict_counted(json, len, strict_idx, &strict_n, len + 64, &strict_pop), 0);
  cr_assert_eq(strict_n, lax_n);
  cr_assert_arr_eq(strict_idx, lax_idx, lax_n * sizeof(uint32_t));
  cr_assert_eq(strict_pop.op, lax_pop.op);
  cr_assert_eq(strict_pop.quotes, lax_pop.quotes);
  cr_assert_eq(strict_pop.scalars, lax_pop.scalars);
}

Test(scan_tape_bound, strict_counted_rejects_invalid_raw_bytes) {
  uint8_t cases[][80] = {
      {'[', '"', 'a', 0x00, 'b', '"', ']'},
      {'[', '"', 0xff, '"', ']'},
  };
  const size_t lengths[] = {7, 5};

  for (size_t i = 0; i < sizeof(lengths) / sizeof(lengths[0]); i++) {
    uint32_t idx[144];
    uint32_t n_idx   = 0;
    NdecPlanePop pop = {0, 0, 0};
    cr_assert_eq(ndec_scan_structurals_counted(cases[i], lengths[i], idx, &n_idx, 144, &pop), 0);
    cr_assert_neq(ndec_scan_structurals_strict_counted(cases[i], lengths[i], idx, &n_idx, 144, &pop), 0);
  }

  uint8_t boundary[72];
  boundary[0] = '[';
  boundary[1] = '"';
  memset(boundary + 2, 'a', 61);
  boundary[63] = 0xff;
  boundary[64] = 'b';
  boundary[65] = '"';
  boundary[66] = ']';
  uint32_t idx[136];
  uint32_t n_idx   = 0;
  NdecPlanePop pop = {0, 0, 0};
  cr_assert_eq(ndec_scan_structurals_counted(boundary, 67, idx, &n_idx, 136, &pop), 0);
  cr_assert_neq(ndec_scan_structurals_strict_counted(boundary, 67, idx, &n_idx, 136, &pop), 0);
}
