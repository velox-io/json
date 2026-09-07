/*
 * window_tests.c -- incremental structural window scanner tests.
 *
 * Oracles:
 *   final window   the window scan must agree with the full-buffer scanner
 *                  on the same bytes: INVALID exactly when the full scan
 *                  fails, and identical indexes otherwise.
 *   prefix window  a non-final window publishes exactly the full-document
 *                  structurals below stable_end; a tail begins at the first
 *                  withheld structural.
 *   feed driver    repeatedly scanning windows, consuming the stable prefix
 *                  and relocating the tail, must reproduce the full-document
 *                  index array exactly.
 */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include <criterion/criterion.h>

#include "ndec/core/extract.h"

#define WIN_TEST_MAX (64 * 1024)

static uint8_t doc_buf[WIN_TEST_MAX];
static uint8_t win_buf[WIN_TEST_MAX];
static uint32_t full_idx[WIN_TEST_MAX / 4];
static uint32_t win_idx[WIN_TEST_MAX / 4];
static uint32_t expect_idx[WIN_TEST_MAX / 4];

static const char *valid_docs[] = {
    "null",
    "true",
    "false",
    "123",
    "-1.5e+10",
    "\"\"",
    "\"hello\"",
    "\"a\\\"b\\\\c\\n\"",
    "\"\\u00e9\\ud83d\\ude00\"",
    "\"\\u4e16\\u754c\"",
    "[]",
    "{}",
    "[1,2,3]",
    "[\"a\",\"b\"]",
    "{\"a\":1}",
    "{\"key\": 1}",
    "  {  \"a\" : [ 1 , 2 ] , \"b\" : { \"c\" : null } }  ",
    "{\"a\":true,\"b\":false,\"c\":null}",
    "[[[]]]",
    "{\"x\":{\"y\":{\"z\":[1,[2,[3]]]}}}",
    "{\"日本語\":\"テスト\",\"emoji\":\"\xe6\x97\xa5\xe6\x9c\xac\"}",
    "[\"\\ud83d\\ude00\\ud83d\\ude01\"]",
    "[0.5,-0.5,1e10,1E-10,1e+10,123456789012345678901234567890]",
    "{\"a\":\"b\",\"c\":\"d\"}",
    "[\"a\"  ,  \"b\"  ,  \"c\"]",
    "{\"nested\":{\"array\":[{\"deep\":\"value\"}]}}",
};

/* Scan-valid documents whose grammar is incomplete: the scanner accepts
 * them and the binder owns the EOF error. */
static const char *incomplete_docs[] = {
    "{",
    "[",
    "{\"a\":",
    "{\"a\":1,",
    "[1,2",
};

/* Documents whose full-buffer scan fails: an unclosed string somewhere. */
static const char *unclosed_docs[] = {
    "{\"a",
    "\"abc",
    "[\"abc",
    "{\"a\\\"",
    "[\"a\\\"b",
};

/* Documents rejected by the strict scanner only: raw control bytes inside
 * strings or malformed UTF-8. */
static const char *strict_invalid_docs[] = {
    "\"a\x01b\"",
    "\"a\nb\"",
    "\"a\x1f" "b\"",
    "[\"\xff\"]",
    "\"\xc3\"",
    "\"\xe6\x97\"",
};

static NdecWindowScan scan_win(const uint8_t *src, size_t len, int is_final, int strict) {
  memcpy(win_buf, src, len);
  memset(win_buf + len, 0x20, 64);
  if (strict) return ndec_scan_window_strict(win_buf, len, win_idx, (uint32_t)len + 64, is_final);
  return ndec_scan_window(win_buf, len, win_idx, (uint32_t)len + 64, is_final);
}

static NdecWindowScan scan_win_str(const char *doc, int is_final, int strict) {
  return scan_win((const uint8_t *)doc, strlen(doc), is_final, strict);
}

static int scan_full(const char *doc, uint32_t *out_idx, uint32_t *out_n, int strict) {
  size_t len = strlen(doc);
  memcpy(doc_buf, doc, len);
  memset(doc_buf + len, 0x20, 64);
  if (strict) return ndec_scan_structurals_strict(doc_buf, len, out_idx, out_n, (uint32_t)len + 64);
  return ndec_scan_structurals(doc_buf, len, out_idx, out_n, (uint32_t)len + 64);
}

/* Final windows must agree with the full-buffer scanner on identical bytes. */
static void check_final_parity(const char *doc, int strict) {
  size_t len = strlen(doc);
  for (size_t s = 0; s <= len; s++) {
    uint32_t cn = 0;
    memcpy(doc_buf, doc, s);
    memset(doc_buf + s, 0x20, 64);
    int cerr = strict ? ndec_scan_structurals_strict(doc_buf, s, expect_idx, &cn, (uint32_t)s + 64)
                      : ndec_scan_structurals(doc_buf, s, expect_idx, &cn, (uint32_t)s + 64);

    NdecWindowScan w = scan_win((const uint8_t *)doc, s, 1, strict);
    cr_assert_eq(w.status == NDEC_WINDOW_INVALID, cerr != 0, "doc '%s' prefix %zu: INVALID mismatch", doc, s);
    if (cerr) continue;

    cr_assert_eq(w.n_idx, cn, "doc '%s' prefix %zu: n_idx %u != %u", doc, s, w.n_idx, cn);
    cr_assert_eq(w.tail_start, (uint32_t)s, "doc '%s' prefix %zu: final tail_start %u", doc, s, w.tail_start);
    for (uint32_t i = 0; i < cn; i++)
      cr_assert_eq(win_idx[i], expect_idx[i], "doc '%s' prefix %zu: index %u", doc, s, i);
    for (int k = 0; k < 3; k++)
      cr_assert_eq(win_idx[w.n_idx + k], (uint32_t)s, "doc '%s' prefix %zu: sentinel %d", doc, s, k);
  }
}

Test(window_final, valid_docs_match_full_scan) {
  for (size_t d = 0; d < sizeof(valid_docs) / sizeof(valid_docs[0]); d++) {
    check_final_parity(valid_docs[d], 0);
    check_final_parity(valid_docs[d], 1);
  }
}

Test(window_final, incomplete_docs_match_full_scan) {
  for (size_t d = 0; d < sizeof(incomplete_docs) / sizeof(incomplete_docs[0]); d++) {
    check_final_parity(incomplete_docs[d], 0);
    check_final_parity(incomplete_docs[d], 1);
  }
}

Test(window_final, unclosed_docs_match_full_scan) {
  for (size_t d = 0; d < sizeof(unclosed_docs) / sizeof(unclosed_docs[0]); d++) {
    check_final_parity(unclosed_docs[d], 0);
    check_final_parity(unclosed_docs[d], 1);
  }
}

Test(window_final, strict_invalid_docs_match_strict_full_scan) {
  for (size_t d = 0; d < sizeof(strict_invalid_docs) / sizeof(strict_invalid_docs[0]); d++) {
    check_final_parity(strict_invalid_docs[d], 1);
    /* The relaxed scanner accepts the byte sequences themselves; only the
     * string-balance failures carry over. */
    check_final_parity(strict_invalid_docs[d], 0);
  }
}

Test(window_prefix, publishes_full_scan_prefix_below_stable_end) {
  for (size_t d = 0; d < sizeof(valid_docs) / sizeof(valid_docs[0]); d++) {
    const char *doc = valid_docs[d];
    size_t len = strlen(doc);
    uint32_t fn = 0;
    cr_assert_eq(scan_full(doc, full_idx, &fn, 0), 0);

    for (size_t s = 0; s <= len; s++) {
      NdecWindowScan w = scan_win((const uint8_t *)doc, s, 0, 0);
      cr_assert_neq(w.status, NDEC_WINDOW_INVALID, "doc '%s' split %zu", doc, s);
      cr_assert_eq(w.tail_start, w.stable_end, "doc '%s' split %zu: tail/stable mismatch", doc, s);
      cr_assert_leq(w.stable_end, (uint32_t)s);

      uint32_t below = 0;
      while (below < fn && full_idx[below] < w.stable_end) below++;
      cr_assert_eq(w.n_idx, below, "doc '%s' split %zu: n_idx %u != %u", doc, s, w.n_idx, below);
      for (uint32_t i = 0; i < below; i++)
        cr_assert_eq(win_idx[i], full_idx[i], "doc '%s' split %zu: index %u", doc, s, i);
      if (w.n_idx < fn)
        cr_assert_geq(full_idx[w.n_idx], w.stable_end, "doc '%s' split %zu: withheld structural", doc, s);
      /* A tail starts exactly at the first withheld structural. */
      if (w.stable_end < (uint32_t)s) {
        cr_assert_lt(w.n_idx, fn, "doc '%s' split %zu: tail without withheld structural", doc, s);
        cr_assert_eq(w.tail_start, full_idx[w.n_idx], "doc '%s' split %zu: tail_start", doc, s);
      }
    }
  }
}

/* Feed driver: consume the stable prefix, relocate the tail, append one
 * chunk per round. The accumulated absolute indexes must reproduce the
 * full-document scan exactly. */
static void feed_driver(const char *doc, size_t chunk, int strict) {
  size_t len = strlen(doc);
  uint32_t fn = 0;
  cr_assert_eq(scan_full(doc, full_idx, &fn, strict), 0, "full scan of feed doc '%s' failed", doc);

  uint32_t abs_idx[2048];
  uint32_t abs_n = 0;
  size_t feed_start = 0, carry = 0;

  for (int guard = 0;; guard++) {
    cr_assert_lt(guard, 100000, "feed driver on '%s' failed to terminate", doc);
    size_t avail = len - feed_start;
    cr_assert_geq(avail, carry);
    size_t take = chunk;
    if (take > avail - carry) take = avail - carry;
    size_t wlen    = carry + take;
    int is_final   = (feed_start + wlen >= len);

    NdecWindowScan w = scan_win((const uint8_t *)doc + feed_start, wlen, is_final, strict);
    cr_assert_neq(w.status, NDEC_WINDOW_INVALID, "doc '%s' feed_start %zu: INVALID", doc, feed_start);

    cr_assert_leq(abs_n + w.n_idx, 2048);
    for (uint32_t i = 0; i < w.n_idx; i++)
      abs_idx[abs_n + i] = (uint32_t)feed_start + win_idx[i];
    abs_n += w.n_idx;

    if (is_final) break;
    feed_start += w.stable_end;
    carry = wlen - w.stable_end;
  }

  cr_assert_eq(abs_n, fn, "doc '%s': index count %u != %u", doc, abs_n, fn);
  for (uint32_t i = 0; i < fn; i++)
    cr_assert_eq(abs_idx[i], full_idx[i], "doc '%s': index %u", doc, i);
}

Test(window_feed, reproduces_full_scan_across_chunk_sizes) {
  static const size_t chunks[] = {1, 2, 3, 7, 31, 32, 63, 64, 65};
  for (size_t d = 0; d < sizeof(valid_docs) / sizeof(valid_docs[0]); d++) {
    for (size_t c = 0; c < sizeof(chunks) / sizeof(chunks[0]); c++) {
      feed_driver(valid_docs[d], chunks[c], 0);
      feed_driver(valid_docs[d], chunks[c], 1);
    }
  }
  for (size_t d = 0; d < sizeof(incomplete_docs) / sizeof(incomplete_docs[0]); d++) {
    for (size_t c = 0; c < sizeof(chunks) / sizeof(chunks[0]); c++) {
      feed_driver(incomplete_docs[d], chunks[c], 0);
    }
  }
}

/* Scan-invalid documents must reject at or before the final window. */
static void feed_driver_expect_invalid(const char *doc, size_t chunk, int strict) {
  size_t len = strlen(doc);
  size_t feed_start = 0, carry = 0;
  for (int guard = 0;; guard++) {
    cr_assert_lt(guard, 100000, "driver on '%s' failed to terminate", doc);
    size_t avail = len - feed_start;
    size_t take  = chunk;
    if (take > avail - carry) take = avail - carry;
    size_t wlen  = carry + take;
    int is_final = (feed_start + wlen >= len);

    NdecWindowScan w = scan_win((const uint8_t *)doc + feed_start, wlen, is_final, strict);
    if (w.status == NDEC_WINDOW_INVALID) return;
    if (is_final) break;
    feed_start += w.stable_end;
    carry = wlen - w.stable_end;
  }
  cr_assert(0, "doc '%s' never rejected", doc);
}

Test(window_feed, invalid_docs_eventually_reject) {
  static const size_t chunks[] = {1, 2, 3, 7, 31, 32, 63, 64, 65};
  for (size_t d = 0; d < sizeof(unclosed_docs) / sizeof(unclosed_docs[0]); d++) {
    for (size_t c = 0; c < sizeof(chunks) / sizeof(chunks[0]); c++) {
      feed_driver_expect_invalid(unclosed_docs[d], chunks[c], 0);
      feed_driver_expect_invalid(unclosed_docs[d], chunks[c], 1);
    }
  }
  for (size_t d = 0; d < sizeof(strict_invalid_docs) / sizeof(strict_invalid_docs[0]); d++) {
    for (size_t c = 0; c < sizeof(chunks) / sizeof(chunks[0]); c++) {
      feed_driver_expect_invalid(strict_invalid_docs[d], chunks[c], 1);
    }
  }
}

Test(window_tail, open_string_relocates_from_quote) {
  NdecWindowScan w = scan_win((const uint8_t *)"{\"abc", 5, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 1);
  cr_assert_eq(w.n_idx, 1);
  cr_assert_eq(win_idx[0], 0);

  w = scan_win((const uint8_t *)"{\"abc", 5, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INVALID);
  cr_assert_eq(w.err, NDEC_WINDOW_ERR_UNCLOSED_STRING);
  cr_assert_eq(w.error_pos, 1);

  /* Escape before the window end keeps the string open. */
  w = scan_win((const uint8_t *)"{\"ab\\\"", 6, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 1);
}

Test(window_tail, trailing_string_relocates_conservatively) {
  NdecWindowScan w = scan_win((const uint8_t *)"{\"key\"", 6, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 1);
  cr_assert_eq(w.n_idx, 1);

  /* Trailing whitespace after the string changes nothing. */
  w = scan_win((const uint8_t *)"{\"key\" ", 7, 0, 0);
  cr_assert_eq(w.tail_start, 1);
  cr_assert_eq(w.n_idx, 1);

  /* Trailing value strings relocate too: the scanner does not know key
   * from value, so any quote without a follower structural withholds. */
  w = scan_win((const uint8_t *)"[\"a\", \"b\"", 9, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 6);
  cr_assert_eq(w.n_idx, 3);

  w = scan_win((const uint8_t *)"{\"a\": \"b\"", 9, 0, 0);
  cr_assert_eq(w.tail_start, 6);
  cr_assert_eq(w.n_idx, 3);

  /* Nested object key withholds. */
  w = scan_win((const uint8_t *)"{\"a\": {\"b\"", 10, 0, 0);
  cr_assert_eq(w.tail_start, 7);
  cr_assert_eq(w.n_idx, 4);

  /* A string with a follower structural publishes. */
  w = scan_win((const uint8_t *)"[\"a\",1]", 7, 0, 0);
  cr_assert_eq(w.tail_start, 7);
  cr_assert_eq(w.n_idx, 5);

  /* Final windows never withhold; the binder owns EOF and trailing
   * errors. */
  w = scan_win((const uint8_t *)"{\"key\"", 6, 1, 0);
  cr_assert_eq(w.tail_start, 6);
  cr_assert_eq(w.n_idx, 2);
}

Test(window_tail, unterminated_scalar_relocates) {
  static const char *docs[] = {
      "{\"key\": 1",
      "[123.45e+6",
      "[tru",
      "[fals",
      "[nul",
      "[1e",
      "[1e-",
      "[1.",
      "[-",
  };
  for (size_t d = 0; d < sizeof(docs) / sizeof(docs[0]); d++) {
    const char *doc = docs[d];
    size_t len = strlen(doc);
    NdecWindowScan w = scan_win((const uint8_t *)doc, len, 0, 0);
    cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE, "doc %zu", d);
    cr_assert_lt(w.tail_start, (uint32_t)len, "doc %zu: expected a tail", d);
    uint8_t c = (uint8_t)doc[w.tail_start];
    cr_assert(c == '-' || (c >= '0' && c <= '9') || c == 't' || c == 'f' || c == 'n', "doc %zu: tail at '%c'", d, c);
  }

  /* A final window publishes the scalar: a number ending at EOF is valid
   * and a truncated keyword is the binder's error. */
  NdecWindowScan w = scan_win((const uint8_t *)"[123", 4, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 4);
  cr_assert_eq(w.n_idx, 2);

  w = scan_win((const uint8_t *)"123", 3, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.n_idx, 1);

  /* Escaped quote bytes belong to the token, so the scalar stays open. */
  w = scan_win((const uint8_t *)"[12\\\"", 5, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 1);
}

Test(window_status, scanner_never_claims_root_completion) {
  NdecWindowScan w;

  w = scan_win((const uint8_t *)"", 0, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.n_idx, 0);
  cr_assert_eq(w.tail_start, 0);

  w = scan_win((const uint8_t *)"   ", 3, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.n_idx, 0);
  cr_assert_eq(w.stable_end, 3);

  /* Complete roots stay Incomplete: completion is the binder's knowledge,
   * and the binder owns trailing-data errors too. */
  w = scan_win((const uint8_t *)"123 ", 4, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  w = scan_win((const uint8_t *)"[1, 2]", 6, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  w = scan_win((const uint8_t *)"{\"a\": 1}", 8, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  w = scan_win((const uint8_t *)"[1] 2", 5, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);

  /* Open containers stay incomplete. */
  w = scan_win((const uint8_t *)"[1,", 3, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  w = scan_win((const uint8_t *)"{\"a\": 1,", 8, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);

  /* Final windows that never complete stay incomplete; the caller maps
   * this to an EOF error. */
  w = scan_win((const uint8_t *)"", 0, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  w = scan_win((const uint8_t *)"[1,", 3, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
}

Test(window_tail, utf8_and_surrogate_splits) {
  const char *doc = "[\"日本語\"]";
  size_t len = strlen(doc);
  /* Splits while the string stays open relocate from the opening quote; the
   * split that already contains the closing quote publishes the string. */
  for (size_t s = 2; s < len - 1; s++) {
    NdecWindowScan w = scan_win((const uint8_t *)doc, s, 0, 0);
    cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE, "split %zu", s);
    cr_assert_eq(w.tail_start, 1, "split %zu", s);
    cr_assert_eq(w.n_idx, 1, "split %zu", s);
  }
  NdecWindowScan w = scan_win((const uint8_t *)doc, len, 0, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, (uint32_t)len);

  /* Surrogate escape pairs split between the escapes. */
  const char *sdoc = "[\"\\ud83d\\ude00\"]";
  size_t slen = strlen(sdoc);
  for (size_t s = 2; s < slen - 1; s++) {
    NdecWindowScan sw = scan_win((const uint8_t *)sdoc, s, 0, 0);
    cr_assert_eq(sw.status, NDEC_WINDOW_INCOMPLETE, "split %zu", s);
    cr_assert_eq(sw.tail_start, 1, "split %zu", s);
  }
}

Test(window_strict, control_and_utf8_errors) {
  /* Raw control byte inside a string: definite, rejects non-final too. */
  NdecWindowScan w = scan_win((const uint8_t *)"\"a\x01b\"", 5, 0, 1);
  cr_assert_eq(w.status, NDEC_WINDOW_INVALID);
  cr_assert_eq(w.err, NDEC_WINDOW_ERR_CONTROL);

  /* The relaxed scanner ignores control bytes, matching the full-buffer
   * scanner. */
  w = scan_win((const uint8_t *)"\"a\x01b\"", 5, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);

  /* Malformed UTF-8 rejects immediately. */
  w = scan_win((const uint8_t *)"\"\xff\"", 3, 0, 1);
  cr_assert_eq(w.status, NDEC_WINDOW_INVALID);
  cr_assert_eq(w.err, NDEC_WINDOW_ERR_UTF8);

  w = scan_win((const uint8_t *)"\"\xff\"", 3, 1, 0);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);

  /* An incomplete multibyte sequence at a non-final window end relocates
   * with the open string. */
  w = scan_win((const uint8_t *)"\"\xe6\x97", 3, 0, 1);
  cr_assert_eq(w.status, NDEC_WINDOW_INCOMPLETE);
  cr_assert_eq(w.tail_start, 0);

  /* The same bytes at a final window are malformed. */
  w = scan_win((const uint8_t *)"\"\xe6\x97", 3, 1, 1);
  cr_assert_eq(w.status, NDEC_WINDOW_INVALID);
  cr_assert_eq(w.err, NDEC_WINDOW_ERR_UTF8);
}

Test(window_capacity, rejects_short_buffers) {
  uint32_t idx[8];
  NdecWindowScan w = ndec_scan_window((const uint8_t *)"[[1]]", 5, idx, 4, 1);
  cr_assert_eq(w.status, NDEC_WINDOW_INVALID);
  cr_assert_eq(w.err, NDEC_WINDOW_ERR_CAPACITY);
}
