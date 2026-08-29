/* jsonfmt: reformat a JSON file (or stdin) as pretty or compact text. */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "ndec/core/fmt.h"

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

  size_t cap = 1 << 16, len = 0;
  uint8_t *src = (uint8_t *)malloc(cap);
  if (!src) {
    return 1;
  }
  for (;;) {
    if (len + 1 >= cap) {
      cap *= 2;
      uint8_t *nb = (uint8_t *)realloc(src, cap);
      if (!nb) {
        free(src);
        return 1;
      }
      src = nb;
    }
    size_t n = fread(src + len, 1, cap - 1 - len, fp);
    if (n == 0)
      break;
    len += n;
  }
  if (fp != stdin)
    fclose(fp);

  int compact    = indent_str && indent_str[0] == '\0';
  const char *ind = indent_str ? indent_str : "  ";
  uint32_t ind_len = compact ? 0 : (uint32_t)strlen(ind);

  /* Counting pass (cap 0) sizes the output exactly, then one write pass. */
  NdecFmtState st;
  NdecFmtOut out = {NULL, 0, 0};
  uint32_t err_pos = 0;
  int err = ndec_fmt_run(&st, src, len, compact, NULL, 0, ind, ind_len, &out, &err_pos);
  if (err != NDEC_OK && err != NDEC_FMT_FULL) {
    fprintf(stderr, "jsonfmt: parse error %d at byte %u\n", err, err_pos);
    free(src);
    return 1;
  }

  uint8_t *dst = (uint8_t *)malloc(out.len + 1);
  if (!dst) {
    free(src);
    return 1;
  }
  out.buf = dst;
  out.cap = out.len;
  out.len = 0;
  err     = ndec_fmt_run(&st, src, len, compact, NULL, 0, ind, ind_len, &out, &err_pos);
  if (err != NDEC_OK) {
    fprintf(stderr, "jsonfmt: write pass failed (%d)\n", err);
    free(src);
    free(dst);
    return 1;
  }

  fwrite(dst, 1, out.len, stdout);
  if (!compact)
    fputc('\n', stdout);
  free(src);
  free(dst);
  return 0;
}
