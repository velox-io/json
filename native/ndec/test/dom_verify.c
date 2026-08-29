/*
 * dom_verify.c -- check dom.h numeric tape values against strtod for all
 * numbers in a JSON file. Used to validate num.h refactors.
 *
 * Usage: dom_verify <json-file>
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <ctype.h>

#include "ndec/dom.h"
#include "ndec_libc_allocator.h"

static const uint8_t *skip_ws(const uint8_t *p, const uint8_t *end) {
  while (p < end) {
    uint8_t c = *p;
    if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
      p++;
      continue;
    }
    break;
  }
  return p;
}

static const uint8_t *skip_string(const uint8_t *p, const uint8_t *end) {
  /* p points at opening quote */
  p++;
  while (p < end) {
    if (*p == '\\') {
      p += 2;
      continue;
    }
    if (*p == '"') {
      return p + 1;
    }
    p++;
  }
  return end;
}

static int next_number(const uint8_t **pp, const uint8_t *end, const uint8_t **num_start,
                       const uint8_t **num_end) {
  const uint8_t *p = *pp;
  while (p < end) {
    uint8_t c = *p;
    if (c == '"') {
      p = skip_string(p, end);
      continue;
    }
    if (c == '-' || (c >= '0' && c <= '9')) {
      const uint8_t *s = p;
      if (c == '-')
        p++;
      while (p < end && *p >= '0' && *p <= '9')
        p++;
      if (p < end && *p == '.') {
        p++;
        while (p < end && *p >= '0' && *p <= '9')
          p++;
      }
      if (p < end && (*p == 'e' || *p == 'E')) {
        p++;
        if (p < end && (*p == '+' || *p == '-'))
          p++;
        while (p < end && *p >= '0' && *p <= '9')
          p++;
      }
      *num_start = s;
      *num_end   = p;
      *pp        = p;
      return 1;
    }
    p++;
  }
  *pp = p;
  return 0;
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "usage: %s <json-file>\n", argv[0]);
    return 1;
  }
  FILE *f = fopen(argv[1], "rb");
  if (!f) {
    perror("fopen");
    return 1;
  }
  fseek(f, 0, SEEK_END);
  size_t len = (size_t)ftell(f);
  fseek(f, 0, SEEK_SET);
  uint8_t *buf = (uint8_t *)malloc(len + 64);
  if (fread(buf, 1, len, f) != len) {
    perror("fread");
    return 1;
  }
  fclose(f);
  memset(buf + len, ' ', 64);

  json_dom d;
  memset(&d, 0, sizeof(d));
  d.allocator = NDEC_LIBC_ALLOCATOR;
  if (dom_ensure_capacity(&d, len)) {
    fprintf(stderr, "ensure_capacity failed\n");
    return 1;
  }
  int err = json_dom_parse_copy(&d, buf, len);
  if (err) {
    fprintf(stderr, "parse failed: %d\n", err);
    return 1;
  }

  /* Walk tape, collect numeric entries. 'D' keeps the number as source text and
   * is ONE word, so the value-word skip is per-tag; its payload is (str_arena
   * off, len) rather than a value, recorded here as the text pointer. */
  size_t cap = 1024;
  size_t n_t = 0;
  struct entry {
    uint8_t tag;
    uint64_t bits;
    const char *text;
    uint32_t len;
  } *tape_nums = (struct entry *)malloc(cap * sizeof *tape_nums);

  for (size_t i = 0; i < d.emit.doc.tape_len; i++) {
    uint64_t w  = d.emit.doc.tape[i];
    uint8_t tag = (uint8_t)(w >> 56);
    if (tag == 'l' || tag == 'u' || tag == 'd' || tag == 'D') {
      if (n_t == cap) {
        cap *= 2;
        tape_nums = (struct entry *)realloc(tape_nums, cap * sizeof *tape_nums);
      }
      tape_nums[n_t].tag  = tag;
      tape_nums[n_t].text = NULL;
      tape_nums[n_t].len  = 0;
      if (tag == 'D') {
        tape_nums[n_t].bits = 0;
        tape_nums[n_t].text = (const char *)d.emit.doc.str_arena + (uint32_t)(w & 0xFFFFFFFFu);
        tape_nums[n_t].len  = (uint32_t)((w >> 32) & 0xFFFFFFu);
      } else {
        tape_nums[n_t].bits = d.emit.doc.tape[i + 1];
        i++;
      }
      n_t++;
    }
  }

  /* Walk source, parse numbers via strtod / strtoll, compare. */
  const uint8_t *p   = buf;
  const uint8_t *end = buf + len;
  size_t idx         = 0;
  size_t mismatches  = 0;
  size_t total       = 0;
  for (;;) {
    const uint8_t *s, *e;
    if (!next_number(&p, end, &s, &e))
      break;
    if (idx >= n_t) {
      fprintf(stderr, "src has more numbers than tape (idx=%zu)\n", idx);
      break;
    }
    total++;
    char tmp[64];
    size_t L = (size_t)(e - s);
    if (L > 63)
      L = 63;
    memcpy(tmp, s, L);
    tmp[L] = 0;

    int has_dot_exp = 0;
    for (size_t i = 0; i < L; i++) {
      if (tmp[i] == '.' || tmp[i] == 'e' || tmp[i] == 'E') {
        has_dot_exp = 1;
        break;
      }
    }

    struct entry te = tape_nums[idx];
    /* 'D' is the faithful case: the tape must carry the token's own bytes, so
     * compare text against text rather than routing through a double, which is
     * exactly the conversion this tag exists to avoid. */
    if (te.tag == 'D') {
      if (te.len != (uint32_t)(e - s) || memcmp(te.text, s, te.len) != 0) {
        mismatches++;
        if (mismatches < 5)
          fprintf(stderr, "[numraw %zu] %s tape=%.*s\n", idx, tmp, (int)te.len, te.text);
      }
    } else if (!has_dot_exp) {
      if (te.tag == 'l') {
        long long ref = strtoll(tmp, NULL, 10);
        long long got;
        memcpy(&got, &te.bits, 8);
        if (ref != got) {
          mismatches++;
          if (mismatches < 5)
            fprintf(stderr, "[int %zu] %s ref=%lld got=%lld\n", idx, tmp, ref, got);
        }
      } else if (te.tag == 'u') {
        unsigned long long ref = strtoull(tmp, NULL, 10);
        if (ref != te.bits) {
          mismatches++;
          if (mismatches < 5)
            fprintf(stderr, "[uint %zu] %s ref=%llu got=%llu\n", idx, tmp, ref, (unsigned long long)te.bits);
        }
      } else { /* tag 'd' for big int -> compare as double */
        double ref = strtod(tmp, NULL);
        double got;
        memcpy(&got, &te.bits, 8);
        if (ref != got) {
          mismatches++;
          if (mismatches < 5)
            fprintf(stderr, "[bigint-as-d %zu] %s ref=%.17g got=%.17g\n", idx, tmp, ref, got);
        }
      }
    } else {
      if (te.tag != 'd') {
        mismatches++;
        if (mismatches < 5)
          fprintf(stderr, "[expected-d %zu] %s tag=%c\n", idx, tmp, te.tag);
      } else {
        double ref = strtod(tmp, NULL);
        double got;
        memcpy(&got, &te.bits, 8);
        uint64_t rb, gb;
        memcpy(&rb, &ref, 8);
        memcpy(&gb, &got, 8);
        if (rb != gb) {
          mismatches++;
          if (mismatches < 5)
            fprintf(stderr, "[d %zu] %s ref=%.17g (%016llx) got=%.17g (%016llx)\n", idx, tmp, ref,
                    (unsigned long long)rb, got, (unsigned long long)gb);
        }
      }
    }
    idx++;
  }

  printf("file=%s total=%zu mismatches=%zu\n", argv[1], total, mismatches);
  json_dom_free(&d);
  free(buf);
  free(tape_nums);
  return mismatches ? 2 : 0;
}
