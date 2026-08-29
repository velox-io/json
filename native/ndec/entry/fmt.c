/*
 * JSON reformat entry: renders a complete document as compact text or
 * with prefix/indent. Syntax-only, single-shot.
 */

#include <stddef.h>
#include <stdint.h>

#define NDEC_FN_DECL EXPORT ALIGN_STACK

#include "ndec/core/fmt.h"

#define NDEC_FMT_STATE_SIZE 4608

_Static_assert(sizeof(NdecFmtState) <= NDEC_FMT_STATE_SIZE,
               "NdecFmtState exceeds NDEC_FMT_STATE_SIZE; bump it on both the C entry point and "
               "the Go mirror");

typedef struct NdecFmtContext {
  const uint8_t *src;  /* off 0  */
  size_t src_len;      /* off 8  */
  uint8_t *dst;        /* off 16 */
  size_t dst_cap;      /* off 24 */
  uint32_t compact;    /* off 32; 1 = compact, 0 = indent */
  uint32_t prefix_len; /* off 36 */
  uint32_t indent_len; /* off 40 */
  uint32_t _pad;       /* off 44 */
  const char *prefix;  /* off 48 */
  const char *indent;  /* off 56 */
  void *state;         /* off 64; >= NDEC_FMT_STATE_SIZE bytes for NdecFmtState */
  size_t dst_len;      /* off 72; bytes written, or the needed size on FULL */
  uint32_t err_pos;    /* off 80; byte offset of the parse error */
  int32_t err;         /* off 84; 0 ok, NDEC_FMT_FULL, or a SAX exit code */
} NdecFmtContext;

NDEC_FN_DECL void ndec_fmt_parse(NdecFmtContext *ctx) {
  NdecFmtOut out = {ctx->dst, 0, ctx->dst_cap};
  ctx->err = ndec_fmt_run((NdecFmtState *)ctx->state, ctx->src, ctx->src_len, (int)ctx->compact,
                          ctx->prefix, ctx->prefix_len, ctx->indent, ctx->indent_len, &out,
                          &ctx->err_pos);
  ctx->dst_len = out.len;
}
