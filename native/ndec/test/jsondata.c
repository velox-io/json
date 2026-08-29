/* Dataset materializer: for every .json.gz file in <gz-dir>, write
 * <out-dir>/<name>.c.json (compact) and <out-dir>/<name>.json (pretty).
 * Feeds bench/run.sh.
 *
 * Usage: jsondata <gz-dir> <out-dir>
 */

#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#include "ndec/core/fmt.h"
#include "gzload.h"

static int emit_file(const char *out_dir, const char *name, const char *ext, const unsigned char *data,
                     size_t len, int compact) {
  char path[4096];
  snprintf(path, sizeof(path), "%s/%s%s", out_dir, name, ext);

  NdecFmtState st;
  NdecFmtOut out = {NULL, 0, 0};
  uint32_t err_pos = 0;
  int err = ndec_fmt_run(&st, data, len, compact, NULL, 0, "  ", compact ? 0 : 2, &out, &err_pos);
  if (err != NDEC_OK && err != NDEC_FMT_FULL) {
    fprintf(stderr, "jsondata: %s: parse error %d at byte %u\n", path, err, err_pos);
    return -1;
  }

  FILE *f = fopen(path, "wb");
  if (!f) {
    fprintf(stderr, "jsondata: cannot write %s\n", path);
    return -1;
  }
  unsigned char *dst = (unsigned char *)malloc(out.len);
  if (!dst) {
    fclose(f);
    return -1;
  }
  out.buf = dst;
  out.cap = out.len;
  out.len = 0;
  err     = ndec_fmt_run(&st, data, len, compact, NULL, 0, "  ", compact ? 0 : 2, &out, &err_pos);
  if (err != NDEC_OK) {
    fprintf(stderr, "jsondata: %s: write pass failed (%d)\n", path, err);
    free(dst);
    fclose(f);
    return -1;
  }
  fwrite(dst, 1, out.len, f);
  free(dst);
  fclose(f);
  return 0;
}

int main(int argc, char **argv) {
  if (argc != 3) {
    fprintf(stderr, "Usage: jsondata <gz-dir> <out-dir>\n");
    return 2;
  }
  const char *gz_dir  = argv[1];
  const char *out_dir = argv[2];

  DIR *d = opendir(gz_dir);
  if (!d) {
    fprintf(stderr, "jsondata: cannot open %s\n", gz_dir);
    return 1;
  }
  mkdir(out_dir, 0755);

  int rc = 0;
  struct dirent *e;
  while ((e = readdir(d)) != NULL) {
    size_t l = strlen(e->d_name);
    if (l < 9 || l - 8 >= 256 || strcmp(e->d_name + l - 8, ".json.gz") != 0)
      continue;
    char name[256];
    memcpy(name, e->d_name, l - 8);
    name[l - 8] = '\0';

    char gz_path[4096];
    snprintf(gz_path, sizeof(gz_path), "%s/%s", gz_dir, e->d_name);

    size_t len = 0;
    unsigned char *data = gzload(gz_path, &len);
    if (!data) {
      rc = 1;
      continue;
    }
    if (emit_file(out_dir, name, ".c.json", data, len, 1) != 0)
      rc = 1;
    if (emit_file(out_dir, name, ".json", data, len, 0) != 0)
      rc = 1;
    printf("materialized %-20s %zu bytes\n", name, len);
    free(data);
  }
  closedir(d);
  return rc;
}
