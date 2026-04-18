/*
 * Unified entry point for JSON number parsing.
 *
 * Integer and float paths are kept separate by design: double has only
 * a 53-bit mantissa, so values past +/-2^53 (e.g. INT64_MAX) round and
 * narrow back as out-of-range. Integer targets must therefore go
 * through ndec_parse_int64 / uint64 directly; on NUM_FLOAT the caller
 * may fall back to ndec_parse_double for lossless narrowing.
 *
 * atof_ctx is caller-provided. It carries 3 atof_mpint (~1976 B) used
 * by rare trunc paths; putting it on the stack would, after the Go
 * syso inlines the binding hooks, fill the main reactor frame and
 * trip NOSPLIT. Callers keep one shared instance per parsing context.
 */

#ifndef NDEC_NUMBER_H
#define NDEC_NUMBER_H

#include <stdint.h>

#include "ndec/atof.h"
#include "ndec/atoi.h" // IWYU pragma: keep

#define NDEC_ATOF_PADDED_TAIL 8

/* Returns 0 on success (writes *out), 1 if no digits were consumed.
 *
 * always_inline: the body wraps a single atof call invoked from each
 * scalar number hook in the Go syso; not inlining costs a `bl` per
 * number field. */
static inline __attribute__((always_inline)) int ndec_parse_double(const uint8_t *src, uint32_t len, double *out,
                                                                   atof_ctx *ctx) {
  atof_result_f64 r = atof_parse_f64_json_ctx((const char *)src, (int)len, ctx);
  if (r.end == (const char *)src)
    return 1;
  *out = r.val;
  return 0;
}

/* Padded variant: callers must guarantee NDEC_ATOF_PADDED_TAIL readable
 * bytes past src + len. atof's padded entry then drops every internal
 * lim check (per-iteration in the SWAR fractional pass, per-digit in
 * the integer pass). */
static inline __attribute__((always_inline)) int ndec_parse_double_padded(const uint8_t *src, uint32_t len,
                                                                          double *out, atof_ctx *ctx) {
  atof_result_f64 r = atof_parse_f64_json_padded_ctx((const char *)src, (int)len, ctx);
  if (r.end == (const char *)src)
    return 1;
  *out = r.val;
  return 0;
}

#endif /* NDEC_NUMBER_H */
