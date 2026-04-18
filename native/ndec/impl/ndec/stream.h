#ifndef NDEC_STREAM_H
#define NDEC_STREAM_H

#include <stddef.h>
#include <stdint.h>

#include "ndec/core/types.h"

#ifndef NDEC_STREAM_PARSE
#define NDEC_STREAM_PARSE ndec_parse
#endif

typedef enum {
  NDEC_STREAM_DONE      = 0,
  NDEC_STREAM_NEED_MORE = 1,
  NDEC_STREAM_ERROR     = 2,
} NdecStreamStatus;

/* Flag bits for NdecStream.flags. Private. */
#define NDEC_STREAM_F_DONE  0x01u
#define NDEC_STREAM_F_ERROR 0x02u

typedef struct NdecStream {
  NdecCtx *ctx;
  uint32_t flags;
} NdecStream;

/* Forward declaration for the parse function we drive. The symbol is
 * provided by a kernel copy included in the same TU. */
extern void NDEC_STREAM_PARSE(NdecCtx *ctx);

/* Initialize stream and bind ctx. No heap allocation, no destroy needed. */
void ndec_stream_init(NdecStream *s, NdecCtx *ctx) {
  s->ctx   = ctx;
  s->flags = 0;
}

/* Feed one contiguous buffer.
 *
 * data/len:    current buffer span; data must stay valid until this call returns.
 * is_final:    1 iff this is the last segment (EOF).
 * out_tail:    if non-NULL, receives pointer to first unconsumed byte
 *              (always within [data, data+len]).
 * out_tail_len: if non-NULL, receives tail length.
 *
 * Return value and tail:
 *   DONE      -> out_tail[out_tail_len] points at any bytes remaining after
 *                the completed top-level value (kernel currently treats
 *                non-whitespace after root as TRAILING error, so MVP tail_len
 *                is effectively 0 on DONE).
 *   NEED_MORE -> kernel suspended before finishing; caller must relocate
 *                [tail, tail+tail_len) to the start of the next buffer and
 *                append new bytes after it.
 *   ERROR     -> ctx->exit_code holds the kernel error; tail contents undefined.
 */
NdecStreamStatus ndec_stream_feed(NdecStream *s, const uint8_t *data, size_t len, int is_final,
                                  const uint8_t **out_tail, size_t *out_tail_len) {
  if (s->flags & NDEC_STREAM_F_ERROR) {
    if (out_tail)
      *out_tail = data;
    if (out_tail_len)
      *out_tail_len = 0;
    return NDEC_STREAM_ERROR;
  }
  if (s->flags & NDEC_STREAM_F_DONE) {
    /* Re-entry after DONE is a caller bug in MVP (no multi-value support). */
    if (out_tail)
      *out_tail = data;
    if (out_tail_len)
      *out_tail_len = 0;
    return NDEC_STREAM_DONE;
  }

  NdecCtx *ctx = s->ctx;

  /* Reset per-feed scanner cursor to the start of the new buffer.
   * Tail-back protocol guarantees data[0] is a parser-fresh boundary:
   *   - fresh call: depth==0, parser bootstraps from buf;
   *   - resume:     SUSPEND_{NEXT,HERE,AT} left ctx->cur_pos at the
   *                 first unconsumed byte, which the caller memmove'd
   *                 to data[0]; scan_state is safely reset to initial
   *                 since the first byte is a token/structural boundary. */
  ctx->cur_pos                          = data;
  ctx->chunk_ptr                        = data;
  ctx->structural_bits                  = 0;
  ctx->scan_state.prev_in_string        = 0;
  ctx->scan_state.prev_escape           = 0;
  ctx->scan_state.prev_structural_or_ws = 1;
  ndec_ctx_set_input(ctx, data, (uint32_t)len, is_final);
  NDEC_STREAM_PARSE(ctx);

  const uint8_t *data_end = data + len;
  const uint8_t *cur      = ctx->cur_pos;
  if (cur < data)
    cur = data;
  if (cur > data_end)
    cur = data_end;

  NdecStreamStatus st;
  switch (ctx->exit_code) {
  case NDEC_OK:
    s->flags |= NDEC_STREAM_F_DONE;
    st = NDEC_STREAM_DONE;
    break;
  case NDEC_SUSPEND:
    if (is_final) {
      /* Input exhausted with kernel still mid-parse (unclosed container
       * or unfinished keyword). Report as truncation error; overwrite
       * exit_code so the caller sees a definite failure. */
      ctx->exit_code = NDEC_ERR_EOF;
      s->flags |= NDEC_STREAM_F_ERROR;
      st = NDEC_STREAM_ERROR;
    } else {
      st = NDEC_STREAM_NEED_MORE;
    }
    break;
  default:
    s->flags |= NDEC_STREAM_F_ERROR;
    st = NDEC_STREAM_ERROR;
    break;
  }

  if (out_tail)
    *out_tail = cur;
  if (out_tail_len)
    *out_tail_len = (size_t)(data_end - cur);
  return st;
}

/* Convenience: treat data as the complete input.
 * Equivalent to feed(s, data, len, is_final=1, NULL, NULL). */
NdecStreamStatus ndec_stream_feed_all(NdecStream *s, const uint8_t *data, size_t len) {
  return ndec_stream_feed(s, data, len, 1, NULL, NULL);
}

#endif /* NDEC_STREAM_H */
