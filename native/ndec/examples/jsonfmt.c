/*
 * Reads JSON from a file (or stdin), parses via ndec with a reactor
 * that emits indented output. Uses the streaming tail-copy protocol
 * so input is consumed incrementally without loading it all at once.
 *
 * Usage: jsonfmt [file]
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#if defined(__aarch64__)
#define NDEC_FN_NAME ndec_parse_neon
#elif defined(__x86_64__)
#define NDEC_FN_NAME ndec_parse_avx2
#else
#error "Unsupported architecture"
#endif

#define NDEC_PARSE NDEC_FN_NAME

#include "ndec/core/parser.h"

#define MAX_DEPTH 256

typedef struct {
  int indent;
  int has_child[MAX_DEPTH]; /* did current container at depth d have children? */
  int is_array[MAX_DEPTH];  /* 1 if container at depth d is an array, 0 if object */
  int depth;                /* container nesting depth */
  int need_sep;             /* need comma before next item in container */
  int after_colon;          /* value follows ": ", suppress leading newline */
  int compact;              /* 1 when indent_str is empty: minimal output */
  const char *indent_str;   /* per-level indent template (e.g. "  ", "\t") */
  FILE *out;
} FmtCtx;

static void emit_indent(FmtCtx *f) {
  for (int i = 0; i < f->indent; i++)
    fputs(f->indent_str, f->out);
}

static void emit_quoted(FmtCtx *f, NdecStrInfo str) {
  fputc('"', f->out);
  fwrite(str.raw.ptr, 1, str.raw.len, f->out);
  fputc('"', f->out);
}

static void emit_bare(FmtCtx *f, NdecRawStr raw) {
  fwrite(raw.ptr, 1, raw.len, f->out);
}

/* Emit the separator that goes before a value.
 * For array elements: comma + newline + indent.
 * For object field values: nothing (value is on the same line as the key,
 *   handled by clearing after_colon).
 * For root level: nothing. */
static void emit_sep(FmtCtx *f) {
  if (f->after_colon) {
    f->after_colon = 0;
    return;
  }
  if (f->depth > 0 && f->is_array[f->depth - 1]) {
    if (f->has_child[f->depth - 1])
      fputc(',', f->out);
    if (!f->compact) {
      fputc('\n', f->out);
      emit_indent(f);
    }
    f->need_sep                = 0;
    f->has_child[f->depth - 1] = 1;
  }
}

static int32_t on_begin_object(NdecCtx *ctx, void *ud, uint32_t child_phase) { (void)ctx;(void)child_phase;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  fputc('{', f->out);
  f->indent++;
  f->need_sep = 0;
  if (f->depth < MAX_DEPTH) {
    f->has_child[f->depth] = 0;
    f->is_array[f->depth]  = 0;
  }
  f->depth++;
  return NDEC_PROCEED;
}

static int32_t on_end_object(NdecCtx *ctx, void *ud) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  f->indent--;
  f->depth--;
  f->need_sep = 1;
  if (!f->compact) {
    int had = (f->depth >= 0 && f->depth < MAX_DEPTH) ? f->has_child[f->depth] : 1;
    if (had) {
      fputc('\n', f->out);
      emit_indent(f);
    }
  }
  fputc('}', f->out);
  return NDEC_PROCEED;
}

static int32_t on_object_field(NdecCtx *ctx, void *ud, NdecStrInfo key) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  if (f->need_sep)
    fputc(',', f->out);
  if (!f->compact) {
    fputc('\n', f->out);
    emit_indent(f);
  }
  f->need_sep                = 0;
  f->has_child[f->depth - 1] = 1;
  emit_quoted(f, key);
  fputs(f->compact ? ":" : ": ", f->out);
  f->after_colon = 1;
  return NDEC_PROCEED;
}

static int32_t on_begin_array(NdecCtx *ctx, void *ud, uint32_t child_phase) { (void)ctx;(void)child_phase;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  fputc('[', f->out);
  f->indent++;
  f->need_sep = 0;
  if (f->depth < MAX_DEPTH) {
    f->has_child[f->depth] = 0;
    f->is_array[f->depth]  = 1;
  }
  f->depth++;
  return NDEC_PROCEED;
}

static int32_t on_end_array(NdecCtx *ctx, void *ud) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  f->indent--;
  f->depth--;
  f->need_sep = 1;
  if (!f->compact) {
    int had = (f->depth >= 0 && f->depth < MAX_DEPTH) ? f->has_child[f->depth] : 1;
    if (had) {
      fputc('\n', f->out);
      emit_indent(f);
    }
  }
  fputc(']', f->out);
  return NDEC_PROCEED;
}

static int32_t on_scalar_string(NdecCtx *ctx, void *ud, NdecStrInfo str) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  emit_quoted(f, str);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static const uint8_t ws_after_number_table[64] = {
    [' ']  = 1,
    ['\t'] = 1,
    ['\r'] = 1,
    ['\n'] = 1,
};

static void trim_trailing_ws(NdecRawStr *raw) {
  while (raw->len > 0 && ws_after_number_table[raw->ptr[raw->len - 1]])
    raw->len--;
}

static int32_t on_scalar_number(NdecCtx *ctx, void *ud, NdecRawStr raw) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  trim_trailing_ws(&raw);
  emit_bare(f, raw);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static int32_t on_scalar_bool(NdecCtx *ctx, void *ud, int value) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  fputs(value ? "true" : "false", f->out);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static int32_t on_scalar_null(NdecCtx *ctx, void *ud) { (void)ctx;
  FmtCtx *f = (FmtCtx *)ud;
  emit_sep(f);
  fputs("null", f->out);
  f->need_sep = 1;
  return NDEC_PROCEED;
}

static const NdecReactor fmt_reactor = {
    .begin_object  = on_begin_object,
    .end_object    = on_end_object,
    .object_field  = on_object_field,
    .begin_array   = on_begin_array,
    .end_array     = on_end_array,
    .scalar_null   = on_scalar_null,
    .scalar_bool   = on_scalar_bool,
    .scalar_number = on_scalar_number,
    .scalar_string = on_scalar_string,
};

#define BUF_CAP   2048
#define CHUNK     1
#define MIN(a, b) (((a) < (b)) ? (a) : (b))

static void usage(void) {
  fprintf(stderr, "Usage: jsonfmt [-indent str] [file]\n");
  fprintf(stderr, "  -indent str  per-level indent string (default: two spaces, empty for compact)\n");
}

int main(int argc, char **argv) {
  const char *indent_str = NULL;
  const char *filepath   = NULL;

  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "-indent") == 0) {
      if (++i >= argc) {
        fprintf(stderr, "jsonfmt: -indent requires an argument\n");
        return 1;
      }
      indent_str = argv[i];
    } else if (strcmp(argv[i], "-h") == 0 || strcmp(argv[i], "--help") == 0) {
      usage();
      return 0;
    } else {
      filepath = argv[i];
    }
  }

  FILE *fp = stdin;
  if (filepath) {
    fp = fopen(filepath, "rb");
    if (!fp) {
      fprintf(stderr, "jsonfmt: cannot open %s\n", filepath);
      return 1;
    }
  }

  FmtCtx fmt = {
      .indent       = 0,
      .depth        = 0,
      .need_sep     = 0,
      .after_colon  = 0,
      .compact      = indent_str && indent_str[0] == '\0',
      .indent_str   = indent_str ? indent_str : "  ",
      .out          = stdout,
  };
  memset(fmt.has_child, 0, sizeof(fmt.has_child));
  memset(fmt.is_array, 0, sizeof(fmt.is_array));

  NdecCtx ctx;
  ndec_ctx_init(&ctx, &fmt_reactor, &fmt);

  uint8_t *buf     = malloc(sizeof(u_int16_t) * BUF_CAP);
  uint32_t buf_len = 0;
  int feof_seen    = 0;
  for (;;) {
    /* Append next chunk from input. */
    uint32_t avail = (uint32_t)(BUF_CAP - buf_len);
    uint32_t want  = MIN(avail, CHUNK);
    size_t n       = fread(buf + buf_len, 1, want, fp);
    buf_len += (uint32_t)n;

    if (n == 0)
      feof_seen = 1;
    int is_final = feof_seen ? 1 : 0;

    /* Reset scanner state for fresh buffer, preserve parser state. */
    ctx.cur_pos                          = (const uint8_t *)buf - 1;
    ctx.chunk_ptr                        = (const uint8_t *)buf;
    ctx.structural_bits                  = 0;
    ctx.scan_state.prev_in_string        = 0;
    ctx.scan_state.prev_escape           = 0;
    ctx.scan_state.prev_structural_or_ws = 1;

    ndec_ctx_set_input(&ctx, buf, buf_len, is_final);
    NDEC_PARSE(&ctx);

    if (ctx.exit_code == NDEC_OK) {
      if (!fmt.compact)
        fputc('\n', stdout);
      if (fp != stdin)
        fclose(fp);
      return 0;
    }

    if (ctx.exit_code != NDEC_SUSPEND) {
      fprintf(stderr, "jsonfmt: parse error %u at byte %u\n", ctx.exit_code, ctx.error_pos);
      if (fp != stdin)
        fclose(fp);
      return 1;
    }

    /* Tail-copy: keep unprocessed data from cur_pos onward. */
    uint32_t consumed = 0;
    if (ctx.cur_pos >= buf) {
      consumed = (uint32_t)(ctx.cur_pos - buf);
    }
    if (consumed > buf_len)
      consumed = buf_len;
    uint32_t tail = buf_len - consumed;
    if (tail > 0 && consumed > 0) {
      memmove(buf, buf + consumed, tail);
    }
    buf_len = tail;
  }
}
