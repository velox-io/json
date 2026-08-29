/*
 * JSON reformatter: SAX-driven emitters that re-emit a document as
 * compact text or with prefix/indent, writing into a caller buffer.
 * Syntax-only: no UTF-8 validation, and numbers and escaped strings
 * pass through as raw bytes, so reformatting never changes a
 * document's tokens.
 *
 * Include this header before any other ndec core header: the UTF-8
 * checker must compile to its no-op form.
 */
#ifndef NDEC_CORE_FMT_H
#define NDEC_CORE_FMT_H

#ifdef NDEC_UTF8_H
#error "ndec/core/fmt.h must be included before other ndec core headers"
#endif

#include <stddef.h>
#include <stdint.h>

#define NDEC_NO_UTF8_CHECK
#include "ndec/core/sapi.h"

/* Output buffer full: out->len holds the exact needed size. */
#define NDEC_FMT_FULL 1

typedef struct {
  uint8_t *buf; /* NULL with cap 0 selects count-only mode */
  size_t len;   /* bytes written or counted */
  size_t cap;
} NdecFmtOut;

typedef struct {
  int indent;
  int has_child[NDEC_MAX_DEPTH]; /* did the container at depth d emit children */
  int is_array[NDEC_MAX_DEPTH];  /* 1 if the container at depth d is an array */
  int depth;                     /* container nesting depth */
  int need_sep;                  /* comma before next item in container */
  int after_colon;               /* value follows ": ", suppress leading newline */
  int compact;                   /* 1: whitespace-free output */
  const char *prefix;            /* per-line prefix (indent mode) */
  uint32_t prefix_len;
  const char *indent_str; /* per-level indent (indent mode) */
  uint32_t indent_len;
  NdecFmtOut *out;
} FmtCtx;

typedef struct {
  NdecSaxContext sax;
  FmtCtx fmt;
} NdecFmtState;

static void fmt_putc(FmtCtx *f, int ch) {
  if (f->out->len < f->out->cap) f->out->buf[f->out->len] = (uint8_t)ch;
  f->out->len++;
}

static void fmt_write(FmtCtx *f, const void *p, size_t n) {
  if (f->out->len < f->out->cap) {
    size_t room = f->out->cap - f->out->len;
    __builtin_memcpy(f->out->buf + f->out->len, p, n < room ? n : room);
  }
  f->out->len += n;
}

static void fmt_newline_indent(FmtCtx *f) {
  fmt_putc(f, '\n');
  fmt_write(f, f->prefix, f->prefix_len);
  for (int i = 0; i < f->indent; i++)
    fmt_write(f, f->indent_str, f->indent_len);
}

/* Separator that goes before a value: array elements get comma +
 * newline + indent; object field values stay on the key's line; the
 * root level gets nothing. */
static void fmt_sep(FmtCtx *f) {
  if (f->after_colon) {
    f->after_colon = 0;
    return;
  }
  if (f->depth > 0 && f->is_array[f->depth - 1]) {
    if (f->has_child[f->depth - 1]) fmt_putc(f, ',');
    if (!f->compact) fmt_newline_indent(f);
    f->need_sep                = 0;
    f->has_child[f->depth - 1] = 1;
  }
}

static int32_t fmt_begin_object(void *ud) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  fmt_putc(f, '{');
  f->indent++;
  f->need_sep = 0;
  if (f->depth < NDEC_MAX_DEPTH) {
    f->has_child[f->depth] = 0;
    f->is_array[f->depth]  = 0;
  }
  f->depth++;
  return NDEC_PROCEED;
}

static int32_t fmt_end_object(void *ud) {
  FmtCtx *f = (FmtCtx *)ud;
  f->indent--;
  f->depth--;
  f->need_sep = 1;
  if (!f->compact) {
    int had = (f->depth >= 0 && f->depth < NDEC_MAX_DEPTH) ? f->has_child[f->depth] : 1;
    if (had) fmt_newline_indent(f);
  }
  fmt_putc(f, '}');
  return NDEC_PROCEED;
}

static int32_t fmt_object_field(void *ud, NdecStrInfo key) {
  FmtCtx *f = (FmtCtx *)ud;
  if (f->need_sep) fmt_putc(f, ',');
  if (!f->compact) fmt_newline_indent(f);
  f->need_sep                = 0;
  f->has_child[f->depth - 1] = 1;
  fmt_putc(f, '"');
  fmt_write(f, key.raw.ptr, key.raw.len);
  fmt_putc(f, '"');
  fmt_putc(f, ':');
  if (!f->compact) fmt_putc(f, ' ');
  f->after_colon = 1;
  return NDEC_PROCEED;
}

static int32_t fmt_begin_array(void *ud) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  fmt_putc(f, '[');
  f->indent++;
  f->need_sep = 0;
  if (f->depth < NDEC_MAX_DEPTH) {
    f->has_child[f->depth] = 0;
    f->is_array[f->depth]  = 1;
  }
  f->depth++;
  return NDEC_PROCEED;
}

static int32_t fmt_end_array(void *ud) {
  FmtCtx *f = (FmtCtx *)ud;
  f->indent--;
  f->depth--;
  f->need_sep = 1;
  if (!f->compact) {
    int had = (f->depth >= 0 && f->depth < NDEC_MAX_DEPTH) ? f->has_child[f->depth] : 1;
    if (had) fmt_newline_indent(f);
  }
  fmt_putc(f, ']');
  return NDEC_PROCEED;
}

static int32_t fmt_scalar_string(void *ud, NdecStrInfo str) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  fmt_putc(f, '"');
  fmt_write(f, str.raw.ptr, str.raw.len);
  fmt_putc(f, '"');
  f->need_sep = 1;
  return NDEC_PROCEED;
}

/* Indexed by raw byte value: a number span can swallow arbitrary
 * non-structural bytes before the next delimiter, so the table covers
 * the full byte range. */
static const uint8_t fmt_ws_table[256] = {
    [' ']  = 1,
    ['\t'] = 1,
    ['\r'] = 1,
    ['\n'] = 1,
};

static int32_t fmt_scalar_number(void *ud, NdecRawStr raw) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  while (raw.len > 0 && fmt_ws_table[raw.ptr[raw.len - 1]])
    raw.len--;
  fmt_write(f, raw.ptr, raw.len);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static int32_t fmt_scalar_bool(void *ud, int value) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  fmt_write(f, value ? "true" : "false", value ? 4 : 5);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static int32_t fmt_scalar_null(void *ud) {
  FmtCtx *f = (FmtCtx *)ud;
  fmt_sep(f);
  fmt_write(f, "null", 4);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

#define NDEC_REACTOR_HOOKS "ndec/core/fmt_hooks.h"
#include "ndec/core/sax.h"

/*
 * Reformat a complete in-memory document in a single shot.
 * compact = 1 strips all insignificant whitespace; compact = 0 breaks
 * every element onto its own line, prefixed with prefix followed by one
 * copy of indent_str per nesting level.
 * Returns 0 on success, NDEC_FMT_FULL when out->len > out->cap (with
 * out->len holding the exact needed size), or the SAX exit code with
 * *err_pos set to the failing byte offset.
 * src_len must fit in 32 bits.
 */
static int ndec_fmt_run(NdecFmtState *st, const uint8_t *src, size_t src_len, int compact, const char *prefix,
                        uint32_t prefix_len, const char *indent_str, uint32_t indent_len, NdecFmtOut *out,
                        uint32_t *err_pos) {
  if (src_len > 0xFFFFFFFFull) {
    *err_pos = 0;
    return NDEC_ERR_SYNTAX;
  }

  FmtCtx *f      = &st->fmt;
  f->indent      = 0;
  f->depth       = 0;
  f->need_sep    = 0;
  f->after_colon = 0;
  f->compact     = compact;
  f->prefix      = prefix;
  f->prefix_len  = prefix_len;
  f->indent_str  = indent_str;
  f->indent_len  = indent_len;
  f->out         = out;
  __builtin_memset(f->has_child, 0, sizeof(f->has_child));
  __builtin_memset(f->is_array, 0, sizeof(f->is_array));

  NdecSaxContext *ctx = &st->sax;
  ndec_sax_ctx_init(ctx, NULL, f);
  ndec_sax_ctx_set_input(ctx, src, (uint32_t)src_len, 1);
  NDEC_FN_NAME(ctx);

  if (ctx->exit_code != NDEC_OK) {
    /* Single-shot over the whole input: SUSPEND means the document ran
     * out mid-token (e.g. after a colon), which is an EOF error here. */
    if (ctx->exit_code == NDEC_SUSPEND) {
      *err_pos = (uint32_t)src_len;
      return NDEC_ERR_EOF;
    }
    *err_pos = ctx->error_pos;
    return (int)ctx->exit_code;
  }

  /* json.Indent keeps whitespace after the top-level value verbatim. */
  if (!compact) {
    size_t end = src_len;
    while (end > 0 && fmt_ws_table[src[end - 1]])
      end--;
    fmt_write(f, src + end, src_len - end);
  }

  if (out->len > out->cap) return NDEC_FMT_FULL;
  return NDEC_OK;
}

#endif // !NDEC_CORE_FMT_H
