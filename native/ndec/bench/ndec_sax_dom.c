/*
 * DOM build benchmark (yyjson-compatible layout, zero-copy
 * strings: unescaped strings point into the source buffer).
 *
 * Build: bench/build.sh ndec_sax_dom
 * Usage:
 *   ./build/ndec_sax_dom file.json            # default: build-only
 *   ./build/ndec_sax_dom walk file.json       # build + walk
 *   ITERS=10000 ./build/ndec_sax_dom file.json
 */

#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <time.h>

#include "ndec/core/number.h"
#include "ndec/core/str.h"

#define NDEC_R_ROOT_BEGIN_OBJECT(ud)      domz_cb_root_begin_obj((ud), frames, sp)
#define NDEC_R_ROOT_BEGIN_ARRAY(ud)       domz_cb_root_begin_arr((ud), frames, sp)
#define NDEC_R_OBJ_BEGIN_OBJECT(ud)       domz_cb_obj_begin_obj((ud), frames, sp)
#define NDEC_R_OBJ_BEGIN_ARRAY(ud)        domz_cb_obj_begin_arr((ud), frames, sp)
#define NDEC_R_ARR_BEGIN_OBJECT(ud)       domz_cb_arr_begin_obj((ud), frames, sp)
#define NDEC_R_ARR_BEGIN_ARRAY(ud)        domz_cb_arr_begin_arr((ud), frames, sp)
#define NDEC_R_END_OBJECT(ud)             domz_cb_end_obj((ud), frames, sp)
#define NDEC_R_END_ARRAY(ud)              domz_cb_end_arr((ud), frames, sp)
#define NDEC_R_OBJECT_FIELD(ud, key)      domz_cb_field((ud), frames, sp, (key))
#define NDEC_R_SCALAR_NULL(ud)            domz_cb_null(ud)
#define NDEC_R_SCALAR_BOOL(ud, v)         domz_cb_bool((ud), (v))
#define NDEC_R_SCALAR_NUMBER(ud, raw)     domz_cb_number((ud), (raw))
#define NDEC_R_SCALAR_STRING(ud, raw)     domz_cb_string((ud), (raw))
#define NDEC_R_OBJ_SCALAR_NULL(ud)        domz_cb_null(ud)
#define NDEC_R_OBJ_SCALAR_BOOL(ud, v)     domz_cb_bool((ud), (v))
#define NDEC_R_OBJ_SCALAR_NUMBER(ud, raw) domz_cb_number((ud), (raw))
#define NDEC_R_OBJ_SCALAR_STRING(ud, raw) domz_cb_string((ud), (raw))
#define NDEC_R_ARR_SCALAR_NULL(ud)        domz_cb_arr_null((ud), frames, sp)
#define NDEC_R_ARR_SCALAR_BOOL(ud, v)     domz_cb_arr_bool((ud), frames, sp, (v))
#define NDEC_R_ARR_SCALAR_NUMBER(ud, raw) domz_cb_arr_number((ud), frames, sp, (raw))
#define NDEC_R_ARR_SCALAR_STRING(ud, raw) domz_cb_arr_string((ud), frames, sp, (raw))
#include "ndec/core/sapi.h" // IWYU pragma: keep

#include "payload.h"

/* --- yyjson-compatible type tags (lower 3 bits) --- */
#define DOMZ_TYPE_NONE 0
#define DOMZ_TYPE_NULL 2
#define DOMZ_TYPE_BOOL 3
#define DOMZ_TYPE_NUM  4
#define DOMZ_TYPE_STR  5
#define DOMZ_TYPE_ARR  6
#define DOMZ_TYPE_OBJ  7

/* Subtype (bits 3-4) */
#define DOMZ_SUBTYPE_NONE  0
#define DOMZ_SUBTYPE_FALSE (0 << 3)
#define DOMZ_SUBTYPE_TRUE  (1 << 3)
#define DOMZ_SUBTYPE_REAL  (2 << 3)

#define DOMZ_TAG_BIT   8
#define DOMZ_HDR_SLOTS 4

/* --- Value / Raw --- */
typedef union domz_val_uni {
  uint64_t u64;
  int64_t i64;
  double f64;
  const char *str;
  // void       *ptr;
  size_t ofs;
} domz_val_uni;

typedef struct domz_val {
  uint64_t tag;
  domz_val_uni uni;
} domz_val;

typedef struct domz_doc {
  domz_val *root;
  size_t val_read;
} domz_doc;

/* Container state: dedicated extras array indexed by parser stack depth,
 * separate from NdecFrame.  Keeps NdecFrame at 8 bytes. */

typedef struct ndec_dom_extra {
  uint32_t ctn_idx;
  uint32_t child_count;
} ndec_dom_extra;

/* --- Builder --- */
typedef struct domz_builder {
  uint8_t *buf;
  size_t buf_cap;
  domz_val *vals;
  size_t val_cap;
  size_t val_count;
  char *str_pool; /* for unescaped strings */
  size_t str_cap;
  size_t str_used;
  atof_ctx atof;
  ndec_dom_extra extras[256]; /* parallel to NdecFrame stack */
} domz_builder;

/* --- Reset --- */
static void domz_builder_reset(domz_builder *b) {
  b->val_count = DOMZ_HDR_SLOTS;
  b->str_used  = 0;
}

/* --- Init --- */
static int domz_builder_init(domz_builder *b, size_t json_len) {
  size_t est_vals  = json_len / 4 + 16;
  size_t val_cap   = DOMZ_HDR_SLOTS + est_vals;
  size_t val_bytes = val_cap * sizeof(domz_val);
  size_t str_cap   = json_len;
  size_t total     = val_bytes + str_cap;

  b->buf = (uint8_t *)malloc(total);
  if (!b->buf)
    return -1;
  memset(b->buf, 0, val_bytes);
  b->buf_cap  = total;
  b->vals     = (domz_val *)b->buf;
  b->val_cap  = val_cap;
  b->str_pool = (char *)(b->buf + val_bytes);
  b->str_cap  = str_cap;
  domz_builder_reset(b);
  return 0;
}

static void domz_builder_free(domz_builder *b) {
  free(b->buf);
  memset(b, 0, sizeof(*b));
}

/* --- Dynamic growth --- */

NOINLINE
static int domz_grow_vals(domz_builder *b) {
  size_t old_val_bytes = b->val_cap * sizeof(domz_val);
  size_t new_val_cap   = b->val_cap + b->val_cap / 2 + 32;
  if (new_val_cap <= b->val_cap)
    new_val_cap = b->val_cap + 64;
  size_t new_val_bytes = new_val_cap * sizeof(domz_val);
  size_t new_total     = new_val_bytes + b->str_cap;

  uint8_t *new_buf = (uint8_t *)realloc(b->buf, new_total);
  if (!new_buf)
    return -1;

  /* Slide string pool forward and zero new val slots */
  if (new_val_bytes > old_val_bytes) {
    memmove(new_buf + new_val_bytes, new_buf + old_val_bytes, b->str_used);
    memset(new_buf + old_val_bytes, 0, new_val_bytes - old_val_bytes);
  }

  b->buf      = new_buf;
  b->buf_cap  = new_total;
  b->vals     = (domz_val *)new_buf;
  b->val_cap  = new_val_cap;
  b->str_pool = (char *)(new_buf + new_val_bytes);

  return 0;
}

NOINLINE
static int domz_grow_strpool(domz_builder *b, size_t needed) {
  size_t n = b->str_used + needed + b->str_cap / 2 + 32;
  if (n <= b->str_cap)
    n = b->str_cap + 64;

  size_t val_bytes = b->val_cap * sizeof(domz_val);
  size_t new_total = val_bytes + n;

  uint8_t *new_buf = (uint8_t *)realloc(b->buf, new_total);
  if (!new_buf)
    return -1;

  b->buf      = new_buf;
  b->buf_cap  = new_total;
  b->vals     = (domz_val *)new_buf;
  b->str_pool = (char *)(new_buf + val_bytes);
  b->str_cap  = n;

  return 0;
}

static domz_doc *domz_builder_result(domz_builder *b) {
  domz_doc *doc = (domz_doc *)b->vals;
  doc->root     = DOMZ_HDR_SLOTS < b->val_count ? &b->vals[DOMZ_HDR_SLOTS] : NULL;
  doc->val_read = b->val_count - DOMZ_HDR_SLOTS;
  b->buf        = NULL;
  b->vals       = NULL;
  return doc;
}

/* --- Primitives --- */

static inline void domz_set_tag(domz_val *v, uint64_t type, uint64_t subtype, size_t len) {
  v->tag = ((uint64_t)len << DOMZ_TAG_BIT) | subtype | type;
}

__attribute__((always_inline)) static inline domz_val *domz_write_val(domz_builder *b) {
  /* Caller guarantees val_cap is large enough (domz_builder_init over-allocates). */
  return &b->vals[b->val_count++];
}

__attribute__((always_inline)) static inline void domz_copy_small(char *dst, const uint8_t *src, uint32_t len) {
  if (len == 0)
    return;
  if (len <= 8) {
    __builtin_memcpy(dst, src, 8);
  } else if (len <= 16) {
    __builtin_memcpy(dst, src, 8);
    __builtin_memcpy(dst + len - 8, src + len - 8, 8);
  } else if (len <= 32) {
    __builtin_memcpy(dst, src, 16);
    __builtin_memcpy(dst + len - 16, src + len - 16, 16);
  } else if (len <= 64) {
    __builtin_memcpy(dst, src, 32);
    __builtin_memcpy(dst + len - 32, src + len - 32, 32);
  } else {
    __builtin_memcpy(dst, src, len);
  }
}

__attribute__((always_inline)) static inline const char *domz_intern_str(domz_builder *b, const uint8_t *src,
                                                                         uint32_t len) {
  (void)b;
  (void)len;
  return (const char *)src;
}

/* Decode JSON string content [src, src+src_len) into dst. Returns bytes
 * written, or -1 on malformed escape. All-plain input degrades to a
 * byte-by-byte copy and still produces correct output. */
NOINLINE static int32_t ndec_unescape(const uint8_t *src, uint32_t src_len, uint8_t *dst) {
  const uint8_t *si  = src;
  const uint8_t *end = src + src_len;
  uint8_t *di        = dst;
  while (si < end) {
    uint8_t c = *si;
    if (c != '\\') {
      *di++ = c;
      si++;
      continue;
    }
    si++; /* step past '\' */
    if (ndec_str_handle_escape(&si, &di, end) < 0)
      return -1;
  }
  return (int32_t)(di - dst);
}

/* --- Access helpers (for walk) --- */
static inline int domz_get_type(const domz_val *v) {
  return (int)(v->tag & 0x07);
}

static inline int domz_is_ctn(const domz_val *v) {
  int t = domz_get_type(v);
  return t == DOMZ_TYPE_ARR || t == DOMZ_TYPE_OBJ;
}

static inline size_t domz_get_len(const domz_val *v) {
  return v->tag >> DOMZ_TAG_BIT;
}

static inline domz_val *domz_first_child(const domz_val *ctn) {
  return (domz_val *)ctn + 1;
}

static inline domz_val *domz_next_sibling(const domz_val *v) {
  if (domz_is_ctn(v))
    return (domz_val *)((const uint8_t *)v + v->uni.ofs);
  return (domz_val *)v + 1;
}

/* --- Callbacks ---
 *
 * Container state lives in b->extras[], indexed by parser sp.
 * begin: writes extras after STACK_PUSH; parent bumps child_count
 *   when it is an array.
 * end:   STACK_POP runs first; just-popped entry at extras[sp+1].
 * arr scalars: bump extras[sp].child_count (parent array).
 *   obj scalars rely on field's bump.
 */

/* Container-open: write extras entry. */
__attribute__((always_inline)) static inline int32_t domz_open_ctn(domz_builder *b, int32_t sp) {
  b->extras[sp].ctn_idx     = (uint32_t)b->val_count;
  b->extras[sp].child_count = 0;
  domz_write_val(b);
  return NDEC_PROCEED;
}

/* Six begin entries: parent is root sentinel, an object, or an array;
 * new container is object or array. Only ARR-parent variants bump the
 * parent's child_count.  Root and OBJ variants skip that store entirely. */

static __attribute__((always_inline)) inline int32_t domz_cb_root_begin_obj(void *ud, NdecFrame *frames,
                                                                            int32_t sp) {
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_root_begin_arr(void *ud, NdecFrame *frames,
                                                                            int32_t sp) {
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_obj_begin_obj(void *ud, NdecFrame *frames,
                                                                           int32_t sp) {
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_obj_begin_arr(void *ud, NdecFrame *frames,
                                                                           int32_t sp) {
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_begin_obj(void *ud, NdecFrame *frames,
                                                                           int32_t sp) {
  ((domz_builder *)ud)->extras[sp - 1].child_count++;
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_begin_arr(void *ud, NdecFrame *frames,
                                                                           int32_t sp) {
  ((domz_builder *)ud)->extras[sp - 1].child_count++;
  return domz_open_ctn((domz_builder *)ud, sp);
}

static __attribute__((always_inline)) inline int32_t domz_cb_end_obj(void *ud, NdecFrame *frames, int32_t sp) {
  domz_builder *b = (domz_builder *)ud;
  uint32_t idx    = b->extras[sp + 1].ctn_idx;
  domz_val *ctn   = &b->vals[idx];
  ctn->uni.ofs    = (b->val_count - (size_t)idx) * sizeof(domz_val);
  domz_set_tag(ctn, DOMZ_TYPE_OBJ, DOMZ_SUBTYPE_NONE, b->extras[sp + 1].child_count);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_field(void *ud, NdecFrame *frames, int32_t sp,
                                                                   NdecStrInfo key) {
  domz_builder *b = (domz_builder *)ud;
  b->extras[sp].child_count++;
  domz_val *v = domz_write_val(b);
  if (key.has_escape) {
    if (UNLIKELY(b->str_used + key.raw.len > b->str_cap)) {
      if (domz_grow_strpool(b, key.raw.len) != 0)
        return -2;
    }
    uint8_t *tmp    = (uint8_t *)(b->str_pool + b->str_used);
    int32_t written = ndec_unescape(key.raw.ptr, key.raw.len, tmp);
    if (UNLIKELY(written < 0))
      return -2;
    if ((uint32_t)written <= 7) {
      domz_copy_small((char *)&v->uni.u64, tmp, (uint32_t)written);
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    } else {
      v->uni.str = b->str_pool + b->str_used;
      b->str_used += (size_t)written;
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    }
  } else {
    v->uni.str = (const char *)key.raw.ptr;
    domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, key.raw.len);
  }
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_end_arr(void *ud, NdecFrame *frames, int32_t sp) {
  domz_builder *b = (domz_builder *)ud;
  uint32_t idx    = b->extras[sp + 1].ctn_idx;
  domz_val *ctn   = &b->vals[idx];
  ctn->uni.ofs    = (b->val_count - (size_t)idx) * sizeof(domz_val);
  domz_set_tag(ctn, DOMZ_TYPE_ARR, DOMZ_SUBTYPE_NONE, b->extras[sp + 1].child_count);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_null(void *ud) {
  domz_builder *b = (domz_builder *)ud;
  domz_val *v     = domz_write_val(b);
  domz_set_tag(v, DOMZ_TYPE_NULL, DOMZ_SUBTYPE_NONE, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_bool(void *ud, int val) {
  domz_builder *b = (domz_builder *)ud;
  domz_val *v     = domz_write_val(b);
  domz_set_tag(v, DOMZ_TYPE_BOOL, val ? DOMZ_SUBTYPE_TRUE : DOMZ_SUBTYPE_FALSE, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_number(void *ud, NdecRawStr raw) {
  domz_builder *b = (domz_builder *)ud;
  domz_val *v     = domz_write_val(b);
  double d;
  (void)ndec_parse_double(raw.ptr, raw.len, &d, &b->atof);
  v->uni.f64 = d;
  domz_set_tag(v, DOMZ_TYPE_NUM, DOMZ_SUBTYPE_REAL, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_string(void *ud, NdecStrInfo str) {
  domz_builder *b = (domz_builder *)ud;
  domz_val *v     = domz_write_val(b);
  if (str.has_escape) {
    if (UNLIKELY(b->str_used + str.raw.len > b->str_cap)) {
      if (domz_grow_strpool(b, str.raw.len) != 0)
        return -2;
    }
    uint8_t *tmp    = (uint8_t *)(b->str_pool + b->str_used);
    int32_t written = ndec_unescape(str.raw.ptr, str.raw.len, tmp);
    if (UNLIKELY(written < 0))
      return -2;
    if ((uint32_t)written <= 7) {
      domz_copy_small((char *)&v->uni.u64, tmp, (uint32_t)written);
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    } else {
      v->uni.str = b->str_pool + b->str_used;
      b->str_used += (size_t)written;
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    }
  } else {
    v->uni.str = (const char *)str.raw.ptr;
    domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, str.raw.len);
  }
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_null(void *ud, NdecFrame *frames, int32_t sp) {
  domz_builder *b = (domz_builder *)ud;
  b->extras[sp].child_count++;
  domz_val *v = domz_write_val(b);
  domz_set_tag(v, DOMZ_TYPE_NULL, DOMZ_SUBTYPE_NONE, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_bool(void *ud, NdecFrame *frames, int32_t sp,
                                                                      int val) {
  domz_builder *b = (domz_builder *)ud;
  b->extras[sp].child_count++;
  domz_val *v = domz_write_val(b);
  domz_set_tag(v, DOMZ_TYPE_BOOL, val ? DOMZ_SUBTYPE_TRUE : DOMZ_SUBTYPE_FALSE, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_number(void *ud, NdecFrame *frames, int32_t sp,
                                                                        NdecRawStr raw) {
  domz_builder *b = (domz_builder *)ud;
  b->extras[sp].child_count++;
  domz_val *v = domz_write_val(b);
  double d;
  (void)ndec_parse_double(raw.ptr, raw.len, &d, &b->atof);
  v->uni.f64 = d;
  domz_set_tag(v, DOMZ_TYPE_NUM, DOMZ_SUBTYPE_REAL, 0);
  return NDEC_PROCEED;
}

static __attribute__((always_inline)) inline int32_t domz_cb_arr_string(void *ud, NdecFrame *frames, int32_t sp,
                                                                        NdecStrInfo str) {
  domz_builder *b = (domz_builder *)ud;
  b->extras[sp].child_count++;
  domz_val *v = domz_write_val(b);
  if (str.has_escape) {
    if (UNLIKELY(b->str_used + str.raw.len > b->str_cap)) {
      if (domz_grow_strpool(b, str.raw.len) != 0)
        return -2;
    }
    uint8_t *tmp    = (uint8_t *)(b->str_pool + b->str_used);
    int32_t written = ndec_unescape(str.raw.ptr, str.raw.len, tmp);
    if (UNLIKELY(written < 0))
      return -2;
    if ((uint32_t)written <= 7) {
      domz_copy_small((char *)&v->uni.u64, tmp, (uint32_t)written);
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    } else {
      v->uni.str = b->str_pool + b->str_used;
      b->str_used += (size_t)written;
      domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, (size_t)written);
    }
  } else {
    v->uni.str = (const char *)str.raw.ptr;
    domz_set_tag(v, DOMZ_TYPE_STR, DOMZ_SUBTYPE_NONE, str.raw.len);
  }
  return NDEC_PROCEED;
}

/* --- Walk --- */
static void domz_walk(const domz_val *v, size_t *sink) {
  switch (domz_get_type(v)) {
  case DOMZ_TYPE_OBJ: {
    size_t i, max = domz_get_len(v);
    const domz_val *key = domz_first_child(v);
    for (i = 0; i < max; i++) {
      *sink += domz_get_len(key);
      const domz_val *val = domz_next_sibling(key);
      domz_walk(val, sink);
      key = domz_next_sibling(val);
    }
    break;
  }
  case DOMZ_TYPE_ARR: {
    size_t i, max = domz_get_len(v);
    const domz_val *elem = domz_first_child(v);
    for (i = 0; i < max; i++) {
      domz_walk(elem, sink);
      elem = domz_next_sibling(elem);
    }
    break;
  }
  case DOMZ_TYPE_STR:
    *sink += domz_get_len(v);
    break;
  case DOMZ_TYPE_NUM:
    *sink += (size_t)v->uni.f64;
    break;
  case DOMZ_TYPE_BOOL:
    *sink += (v->tag & (1UL << 3)) ? 1 : 0;
    break;
  case DOMZ_TYPE_NULL:
    *sink += 1;
    break;
  default:
    break;
  }
}

// entry
#define NDEC_FN_NAME ndec_sax_parse_dom_yy_zerocopy
#include "ndec/core/sax.h" // IWYU pragma: keep

__attribute__((noinline)) static void *run_build(const char *json, uint32_t json_len, int iters) {
  domz_builder b;
  if (domz_builder_init(&b, json_len) != 0)
    return NULL;
  for (int i = 0; i < iters; i++) {
    domz_builder_reset(&b);
    NdecSaxContext ctx;
    ndec_sax_ctx_init(&ctx, NULL, &b);
    ndec_sax_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    ndec_sax_parse_dom_yy_zerocopy(&ctx);
  }
  return domz_builder_result(&b);
}

__attribute__((noinline)) static void *run_build_walk(const char *json, uint32_t json_len, int iters) {
  domz_builder b;
  if (domz_builder_init(&b, json_len) != 0)
    return NULL;
  for (int i = 0; i < iters; i++) {
    domz_builder_reset(&b);
    NdecSaxContext ctx;
    ndec_sax_ctx_init(&ctx, NULL, &b);
    ndec_sax_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
    ndec_sax_parse_dom_yy_zerocopy(&ctx);
    size_t sink = 0;
    domz_walk(&b.vals[DOMZ_HDR_SLOTS], &sink);
  }
  return domz_builder_result(&b);
}

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

/* Independent verification: build once, walk, return the accumulator so
 * cross-backend sink comparisons can spot divergence in tape layout. */
static size_t verify(const char *json, uint32_t json_len) {
  domz_builder b;
  if (domz_builder_init(&b, json_len) != 0)
    return 0;
  NdecSaxContext ctx;
  ndec_sax_ctx_init(&ctx, NULL, &b);
  ndec_sax_ctx_set_input(&ctx, (const uint8_t *)json, json_len, 1);
  ndec_sax_parse_dom_yy_zerocopy(&ctx);
  size_t sink = 0;
  domz_walk(&b.vals[DOMZ_HDR_SLOTS], &sink);
  domz_builder_free(&b);
  return sink;
}

int main(int argc, char **argv) {
  int iterations = 10000;
  const char *e  = getenv("ITERS");
  if (e)
    iterations = atoi(e);
  if (iterations < 10)
    iterations = 10;

  /* Mode parsing: "walk" before the payload path enables the build+walk
   * variant. Strip from argv so bench_payload_load sees the path at argv[1]. */
  int walk = 0;
  char *load_argv[8];
  int load_argc          = 0;
  load_argv[load_argc++] = argv[0];
  for (int i = 1; i < argc && load_argc < 8; i++) {
    if (strcmp(argv[i], "walk") == 0) {
      walk = 1;
    } else {
      load_argv[load_argc++] = argv[i];
    }
  }

  size_t json_len;
  char *json              = bench_payload_load(load_argc, load_argv, &json_len);
  const uint32_t json_u32 = (uint32_t)json_len;

  /* Warm up. */
  void *warm_doc =
      walk ? run_build_walk(json, json_u32, iterations / 10) : run_build(json, json_u32, iterations / 10);
  free(warm_doc);

  uint64_t t0      = now_ns();
  void *doc        = walk ? run_build_walk(json, json_u32, iterations) : run_build(json, json_u32, iterations);
  uint64_t elapsed = now_ns() - t0;
  free(doc);

  double ns = (double)elapsed / iterations;
  double gb = (double)json_len * iterations / ((double)elapsed / 1e9) / 1e9;
  printf("  %-50s %7.1f ns/iter  %6.2f GB/s\n", walk ? "ndec dom-yy zero-copy + walk" : "ndec dom-yy zero-copy",
         ns, gb);

  size_t sink = verify(json, json_u32);
  fprintf(stderr, "verify sink=%zu\n", sink);

  bench_payload_free(json);
  return 0;
}
