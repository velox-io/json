#ifndef NDEC_CHUNKFEED_H
#define NDEC_CHUNKFEED_H

#include <stddef.h>
#include <stdint.h>

#include "ndec/core/sax.h"

typedef enum {
  NDEC_CHUNKFEED_DONE      = 0,
  NDEC_CHUNKFEED_NEED_MORE = 1,
  NDEC_CHUNKFEED_ERROR     = 2,
} NdecChunkFeedStatus;

/* Flag bits for NdecChunkFeed.flags. Private. */
#define NDEC_CHUNKFEED_F_DONE  0x01u
#define NDEC_CHUNKFEED_F_ERROR 0x02u

typedef struct NdecChunkFeed {
  NdecSaxContext *ctx;
  uint32_t flags;
} NdecChunkFeed;

/* Forward declaration for the parse function we drive. The symbol is
 * provided by a kernel copy included in the same TU. */
extern void ndec_sax_parse(NdecSaxContext *ctx);

/* Initialize chunk feed and bind ctx. No heap allocation, no destroy needed. */
void ndec_chunkfeed_init(NdecChunkFeed *s, NdecSaxContext *ctx) {
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
NdecChunkFeedStatus ndec_chunkfeed_push(NdecChunkFeed *s, const uint8_t *data, size_t len, int is_final,
                                  const uint8_t **out_tail, size_t *out_tail_len) {
  if (s->flags & NDEC_CHUNKFEED_F_ERROR) {
    if (out_tail)
      *out_tail = data;
    if (out_tail_len)
      *out_tail_len = 0;
    return NDEC_CHUNKFEED_ERROR;
  }
  if (s->flags & NDEC_CHUNKFEED_F_DONE) {
    /* Re-entry after DONE is a caller bug in MVP (no multi-value support). */
    if (out_tail)
      *out_tail = data;
    if (out_tail_len)
      *out_tail_len = 0;
    return NDEC_CHUNKFEED_DONE;
  }

  NdecSaxContext *ctx = s->ctx;

  /* Reset per-feed scanner cursor to the start of the new buffer.
   * Tail-back protocol guarantees data[0] is a parser-fresh boundary:
   *   - fresh call: sp<0, parser bootstraps from buf;
   *   - resume:     SUSPEND_{NEXT,HERE,AT} left ctx->cur_pos at the
   *                 first unconsumed byte, which the caller memmove'd
   *                 to data[0]; scan_state is safely reset to initial
   *                 since the first byte is a token/structural boundary.
   *
   * Same reasoning resets the SIMD UTF-8 validator: multibyte characters
   * live inside string tokens and tokens never span buffers, so the byte
   * immediately before data[0] (in the previous buffer) was an ASCII
   * structural/whitespace byte. There is no multibyte carry to preserve. */
  ctx->cur_pos                          = data;
  ctx->chunk_ptr                        = data;
  ctx->structural_bits                  = 0;
  ctx->scan_state.prev_in_string        = 0;
  ctx->scan_state.prev_escape           = 0;
  ctx->scan_state.prev_structural_or_ws = 1;
  utf8_checker_init(&ctx->utf8);
  ndec_sax_ctx_set_input(ctx, data, (uint32_t)len, is_final);
  ndec_sax_parse(ctx);

  const uint8_t *data_end = data + len;
  const uint8_t *cur      = ctx->cur_pos;
  if (cur < data)
    cur = data;
  if (cur > data_end)
    cur = data_end;

  NdecChunkFeedStatus st;
  switch (ctx->exit_code) {
  case NDEC_OK:
    s->flags |= NDEC_CHUNKFEED_F_DONE;
    st = NDEC_CHUNKFEED_DONE;
    break;
  case NDEC_SUSPEND:
    if (is_final) {
      /* Input exhausted with kernel still mid-parse (unclosed container
       * or unfinished keyword). Report as truncation error; overwrite
       * exit_code so the caller sees a definite failure. */
      ctx->exit_code = NDEC_ERR_EOF;
      s->flags |= NDEC_CHUNKFEED_F_ERROR;
      st = NDEC_CHUNKFEED_ERROR;
    } else {
      st = NDEC_CHUNKFEED_NEED_MORE;
    }
    break;
  default:
    s->flags |= NDEC_CHUNKFEED_F_ERROR;
    st = NDEC_CHUNKFEED_ERROR;
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
NdecChunkFeedStatus ndec_chunkfeed_push_all(NdecChunkFeed *s, const uint8_t *data, size_t len) {
  return ndec_chunkfeed_push(s, data, len, 1, NULL, NULL);
}

#endif /* NDEC_CHUNKFEED_H */
