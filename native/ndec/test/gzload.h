/* Gzip loader over the zlib gzFile API. Returns a malloc'd buffer with a
 * NUL terminator one past *out_len. */
#ifndef NDEC_TEST_GZLOAD_H
#define NDEC_TEST_GZLOAD_H

#include <stdio.h>
#include <stdlib.h>
#include <zlib.h>

static unsigned char *gzload(const char *path, size_t *out_len) {
  gzFile f = gzopen(path, "rb");
  if (!f) {
    fprintf(stderr, "gzload: cannot open %s\n", path);
    return NULL;
  }

  size_t cap = 1 << 16;
  size_t len = 0;
  unsigned char *buf = (unsigned char *)malloc(cap);
  if (!buf) {
    gzclose(f);
    return NULL;
  }

  for (;;) {
    /* Keep one spare byte for the NUL terminator. */
    if (len + 1 >= cap) {
      cap *= 2;
      unsigned char *nb = (unsigned char *)realloc(buf, cap);
      if (!nb) {
        free(buf);
        gzclose(f);
        return NULL;
      }
      buf = nb;
    }
    int n = gzread(f, buf + len, (unsigned)(cap - 1 - len));
    if (n < 0) {
      int errnum = 0;
      fprintf(stderr, "gzload: %s: %s\n", path, gzerror(f, &errnum));
      free(buf);
      gzclose(f);
      return NULL;
    }
    if (n == 0)
      break;
    len += (size_t)n;
  }

  gzclose(f);
  buf[len] = '\0';
  if (out_len)
    *out_len = len;
  return buf;
}

#endif // !NDEC_TEST_GZLOAD_H
