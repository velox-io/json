/*
 * Native ndec exposes counted DOM scan and build, formatter, and typed binding
 * interfaces. This translation unit defines the DOM and binding entries.
 */

#include <stddef.h>
#include <stdint.h>

#define NDEC_FN_DECL EXPORT ALIGN_STACK

#include "ndec/dom.h"

#define NDEC_DOM_STATE_SIZE 4096
#define NDEC_ATOF_SIZE      2688

_Static_assert(sizeof(json_dom) <= NDEC_DOM_STATE_SIZE,
               "json_dom exceeds NDEC_DOM_STATE_SIZE; bump it on both the C entry point and the Go DOM mirror");
_Static_assert(sizeof(atof_ctx) <= NDEC_ATOF_SIZE,
               "atof_ctx exceeds NDEC_ATOF_SIZE; bump it on both the C entry point and the Go DOM/bind mirrors");

typedef struct NdecDomContext {
  const uint8_t *src; /* off 0  */
  size_t src_len;     /* off 8  */

  uint64_t *tape;  /* off 16; cap >= tape_need from ndec_dom_parse_counted (words) */
  size_t tape_cap; /* off 24 */

  uint8_t *str_arena;   /* off 32; cap per str_mode (see dom_ensure_capacity) */
  size_t str_arena_cap; /* off 40 */

  uint32_t *structural;    /* off 48; cap >= src_len + 24 (u32 slots) */
  uint32_t structural_cap; /* off 56 */
  uint32_t str_mode;       /* off 60; json_dom_str_mode */

  void *dom_state;  /* off 64; >= NDEC_DOM_STATE_SIZE bytes for json_dom */
  void *atof_state; /* off 72; >= NDEC_ATOF_SIZE bytes for atof_ctx */

  /* outputs */
  size_t tape_len; /* off 80; words written */
  size_t str_used; /* off 88; bytes written into str_arena */
  int32_t err;     /* off 96; 0 ok, else parse error code */

  /* The counted entry publishes both fields before build or TAPE_FULL. */
  uint32_t n_structural; /* off 100; structural count (parse_counted out, build in) */
  uint32_t tape_need;    /* off 104; counted tape-word bound (parse_counted out) */

  /* scan_strict selects UTF-8 and raw control-byte validation. */
  uint32_t scan_strict; /* off 108 */
} NdecDomContext;

_Static_assert(offsetof(NdecDomContext, n_structural) == 100, "dom ctx n_structural offset");
_Static_assert(offsetof(NdecDomContext, tape_need) == 104, "dom ctx tape_need offset");
_Static_assert(offsetof(NdecDomContext, scan_strict) == 108, "dom ctx scan_strict offset");
_Static_assert(sizeof(NdecDomContext) == 112, "dom ctx size");

/* The scan mode follows scan_strict. Its scalar count gives the exact bound
 *
 *   tape_need = n_structural + scalars + 3
 *
 * because n_structural includes operators and opening quotes while scalars
 * accounts for number value words and closing quotes. A short tape arena
 * returns NDEC_DOM_ERR_TAPE_FULL with the completed scan available to
 * ndec_dom_build. */
#define NDEC_DOM_ERR_TAPE_FULL 1

NDEC_FN_DECL void ndec_dom_parse_counted(NdecDomContext *ctx) {
  json_dom *d = (json_dom *)ctx->dom_state;
  __builtin_memset(d, 0, sizeof(*d));

  d->emit.doc.tape      = ctx->tape;
  d->tape_cap           = ctx->tape_cap;
  d->emit.doc.str_arena = ctx->str_arena;
  d->emit.str_arena_cap = ctx->str_arena_cap;
  d->structural_indexes = ctx->structural;
  d->structural_cap     = ctx->structural_cap;
  d->emit.atof          = (atof_ctx *)ctx->atof_state;
  __builtin_memset(d->emit.atof, 0, sizeof(atof_ctx));

  if (ctx->src_len == 0) {
    ctx->err = -1;
    return;
  }
  d->emit.doc.src_buf = ctx->src;
  d->emit.doc.src_len = ctx->src_len;

  uint32_t scalars = 0;
  int err = ctx->scan_strict ? ndec_scan_structurals_strict_scount(ctx->src, ctx->src_len, d->structural_indexes,
                                                                    &d->n_structural_indexes, d->structural_cap,
                                                                    &scalars)
                             : ndec_scan_structurals_scount(ctx->src, ctx->src_len, d->structural_indexes,
                                                            &d->n_structural_indexes, d->structural_cap, &scalars);
  if (err) {
    ctx->err = err;
    return;
  }
  ctx->n_structural = d->n_structural_indexes;
  ctx->tape_need    = d->n_structural_indexes + scalars + 3;
  if ((size_t)ctx->tape_need > ctx->tape_cap) {
    ctx->err = NDEC_DOM_ERR_TAPE_FULL;
    return;
  }

  d->emit.doc.tape_len = 0;
  d->emit.doc.str_used = 0;
  switch ((json_dom_str_mode)ctx->str_mode) {
  case JSON_DOM_STR_ZERO_COPY:
    err = dom_build_tape_zc(DOM_EMIT(d), ctx->src, d->structural_indexes, d->n_structural_indexes);
    break;
  default:
    err = dom_build_tape_copy(DOM_EMIT(d), ctx->src, d->structural_indexes, d->n_structural_indexes);
    break;
  }

  ctx->err      = err;
  ctx->tape_len = d->emit.doc.tape_len;
  ctx->str_used = d->emit.doc.str_used;
}

/* Retry half of the counted flow: build the tape from the structural scan
 * ndec_dom_parse_counted already wrote, after the caller grew the tape arena
 * to the tape_need it reported. Reinstalls every buffer from the context
 * because the growth replaced the tape pointer. */
NDEC_FN_DECL void ndec_dom_build(NdecDomContext *ctx) {
  json_dom *d = (json_dom *)ctx->dom_state;
  __builtin_memset(d, 0, sizeof(*d));

  d->emit.doc.tape      = ctx->tape;
  d->tape_cap           = ctx->tape_cap;
  d->emit.doc.str_arena = ctx->str_arena;
  d->emit.str_arena_cap = ctx->str_arena_cap;
  d->emit.atof          = (atof_ctx *)ctx->atof_state;
  __builtin_memset(d->emit.atof, 0, sizeof(atof_ctx));

  d->emit.doc.tape_len = 0;
  d->emit.doc.str_used = 0;
  d->emit.doc.src_buf  = ctx->src;
  d->emit.doc.src_len  = ctx->src_len;

  int err;
  switch ((json_dom_str_mode)ctx->str_mode) {
  case JSON_DOM_STR_ZERO_COPY:
    err = dom_build_tape_zc(DOM_EMIT(d), ctx->src, ctx->structural, ctx->n_structural);
    break;
  default:
    err = dom_build_tape_copy(DOM_EMIT(d), ctx->src, ctx->structural, ctx->n_structural);
    break;
  }

  ctx->err      = err;
  ctx->tape_len = d->emit.doc.tape_len;
  ctx->str_used = d->emit.doc.str_used;
}

#include "ndec/bind.h" // IWYU pragma: keep

#define NDEC_BIND_MACHINE_SIZE 16384

_Static_assert(
    sizeof(NdecBindMachine) <= NDEC_BIND_MACHINE_SIZE,
    "NdecBindMachine exceeds NDEC_BIND_MACHINE_SIZE; bump it on both the C entry point and the Go binding mirror");
