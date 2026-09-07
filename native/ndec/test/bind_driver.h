/* bind_driver.h drives the typed-binding state machine from C, reproducing the
 * Go driver (decode/bind) action by action: machine seeding, the yield-serving
 * loop, SlotClass block installation, slice growth, map staging drain with
 * buffer compaction, deferred-record capture, and the tape-bind root entry.
 *
 * The engine body compiles into this translation unit twice, exactly as
 * entry/ndec.c does, so both the contiguous and streaming entries exist. The
 * harness drives the contiguous entry; BIND_YIELD_INPUT traps because the
 * contiguous engine can never publish it.
 *
 * Result views: strings are 16-byte {data,len} headers, slices 24-byte
 * {data,len,cap} headers, poly carriers 16-byte efaces, and Values 24-byte
 * descriptors, all matching the Go layouts the machine writes.
 */
#ifndef NDEC_BIND_DRIVER_H
#define NDEC_BIND_DRIVER_H

#include <stdlib.h>
#include <string.h>

#include "bind_fixture.h"

/* Instantiate both engines from the one machine body. */
#define NDEC_STREAM_MODE 0
#include "ndec/bind.h"
#undef NDEC_STREAM_MODE

#define NDEC_STREAM_MODE 1
#include "ndec/bind.h"
#undef NDEC_STREAM_MODE

#define HX_MACHINE_SIZE 16384
#define HX_ATOF_SIZE    2688
#define HX_RETAIN_MAX   2048
#define HX_YIELD_MAX    1024
#define HX_MAP_MAX      256
#define HX_REC_MAX      256
#define HX_ALIAS_MAX    16

typedef struct HxStr {
  const uint8_t *p;
  uintptr_t len;
} HxStr;

typedef struct HxSlice {
  void *data;
  intptr_t len;
  intptr_t cap;
} HxSlice;

typedef struct HxEface {
  const void *type;
  void *data;
} HxEface;

typedef struct HxValue {
  void *doc;
  int32_t base;
  int32_t tidx;
  int32_t end;
  int32_t mode;
} HxValue;

typedef struct HxDoc {
  const uint8_t *src;
  const uint8_t *str_arena;
  const uint64_t *tape;
} HxDoc;

typedef struct HxYieldInfo {
  uint32_t action;
  uint32_t arg0;
  uint32_t arg1;
  uint32_t phase; /* resume phase the machine installed before yielding */
  void *target;
} HxYieldInfo;

typedef struct HxMapEnt {
  char *key;
  uint32_t klen;
  uint8_t *val;
} HxMapEnt;

/* Open-flat map standing in for the runtime *hmap. Keys and values are copied
 * at drain; duplicate keys overwrite, matching mapassign semantics. The entry
 * array is heap-grown so HxDriver stays small enough to live on the stack. */
typedef struct HxMap {
  uint32_t val_size;
  HxMapEnt *ents;
  uint32_t n;
  uint32_t cap;
} HxMap;

typedef struct HxRecord {
  void *target;
  uint32_t type_idx;
  uint8_t kind;
  uint8_t backing;
  uint32_t arg0;
  uint32_t arg1;
  uint8_t bytes[256];
  uint32_t blen;
} HxRecord;

typedef struct HxOpts {
  uint32_t init_batch;   /* elements in a slot's first block; 0 selects 4 */
  uint32_t slot_batch;   /* BLOCK_FULL install size; 0 selects 128 */
  uint32_t map_buf_cap;  /* bytes; 0 selects 64 KiB; small values force FLUSH_MAP */
  uint32_t deferred_cap; /* bytes; 0 selects 384; small values force FLUSH_UNMARSHAL */
  uint32_t tape_guess;   /* initial tape arena words; 0 presizes to the ceiling so
                          * BIND_YIELD_TAPE_ARENA never fires */
} HxOpts;

typedef struct HxDriver {
  FxTree *fx;
  HxOpts opts;
  NdecBindMachine *m;
  uint8_t *atof;
  BindSlotClass slots[FX_MAX_SLOTS];

  /* Per-run buffers; every allocation joins retained and is freed at destroy,
   * so docs published by an earlier run stay readable by a later one. */
  uint8_t *src_padded;
  size_t src_len;
  uint8_t *str_arena;
  size_t str_cap;
  uint64_t *tape_arena;
  size_t tape_cap;
  uint32_t *structural;
  uint32_t structural_cap;

  void *retained[HX_RETAIN_MAX];
  int n_retained;

  HxYieldInfo yields[HX_YIELD_MAX];
  int n_yields;
  HxMap maps[HX_MAP_MAX];
  int n_maps;
  HxRecord records[HX_REC_MAX];
  int n_records;
  HxDoc alias_docs[HX_ALIAS_MAX];
  int n_alias_docs;
  HxDoc doc; /* published views for Value output of the latest run */
  uint32_t published_str;

  int err; /* BIND_ERR_* captured from BIND_YIELD_ERROR */
  uint32_t err_arg1;
  uint64_t err_pos;
} HxDriver;

/* ---- small helpers ---- */

static void hx_retain(HxDriver *d, void *p) {
  FX_CHECK(d->n_retained < HX_RETAIN_MAX);
  d->retained[d->n_retained++] = p;
}

static void *hx_alloc(HxDriver *d, size_t n) {
  void *p = calloc(1, n ? n : 1);
  FX_CHECK(p != NULL);
  hx_retain(d, p);
  return p;
}

static int hx_str_eq(const HxStr *s, const char *lit) {
  size_t n = strlen(lit);
  return s->len == n && (n == 0 || memcmp(s->p, lit, n) == 0);
}

static const char *hx_tag_name(uint8_t tag) {
  switch (tag) {
  case '{':
    return "object";
  case '[':
    return "array";
  case '}':
    return "end_object";
  case ']':
    return "end_array";
  case '"':
    return "string";
  case 'R':
    return "string_raw";
  case 'S':
    return "string_free";
  case 'l':
    return "int64";
  case 'u':
    return "uint64";
  case 'd':
    return "double";
  case 'D':
    return "num_raw";
  case 't':
    return "true";
  case 'f':
    return "false";
  case 'n':
    return "null";
  default:
    return "?";
  }
}

/* ---- HxMap ---- */

static void hx_map_put(HxMap *mp, const uint8_t *key, uint32_t klen, const uint8_t *val) {
  for (uint32_t i = 0; i < mp->n; i++) {
    if (mp->ents[i].klen == klen && memcmp(mp->ents[i].key, key, klen) == 0) {
      memcpy(mp->ents[i].val, val, mp->val_size);
      return;
    }
  }
  if (mp->n == mp->cap) {
    uint32_t ncap = mp->cap ? mp->cap * 2 : 8;
    mp->ents = (HxMapEnt *)realloc(mp->ents, (size_t)ncap * sizeof(HxMapEnt));
    FX_CHECK(mp->ents != NULL);
    memset(&mp->ents[mp->cap], 0, (size_t)(ncap - mp->cap) * sizeof(HxMapEnt));
    mp->cap = ncap;
  }
  HxMapEnt *e = &mp->ents[mp->n++];
  e->key = calloc(1, klen + 1);
  FX_CHECK(e->key != NULL);
  memcpy(e->key, key, klen);
  e->klen = klen;
  e->val = calloc(1, mp->val_size ? mp->val_size : 1);
  FX_CHECK(e->val != NULL);
  memcpy(e->val, val, mp->val_size);
}

static const uint8_t *hx_map_get(const HxMap *mp, const char *key) {
  uint32_t n = (uint32_t)strlen(key);
  for (uint32_t i = 0; i < mp->n; i++) {
    if (mp->ents[i].klen == n && memcmp(mp->ents[i].key, key, n) == 0) return mp->ents[i].val;
  }
  return NULL;
}

static uint32_t hx_map_len(const HxMap *mp) {
  return mp->n;
}

/* ---- SlotClass blocks ---- */

static HxMap *hx_new_map(HxDriver *d, int mapinfo_idx) {
  FX_CHECK(d->n_maps < HX_MAP_MAX);
  HxMap *mp = &d->maps[d->n_maps++];
  memset(mp, 0, sizeof(*mp));
  mp->val_size = d->fx->mapinfo[mapinfo_idx].val_size;
  return mp;
}

/* Install a fresh bump backing, mirroring Allocator.installBlock. A zero
 * element size saturates the limit so the stationary cursor never reads full. */
static void hx_install_block(HxDriver *d, uint32_t ci, uint32_t n) {
  FxTree *fx = d->fx;
  FX_CHECK(ci < (uint32_t)fx->n_slots);
  BindSlotClass *sc = &d->slots[ci];
  uint32_t elem = fx->slot_elem[ci];
  sc->block = hx_alloc(d, elem ? (size_t)n * elem : 1);
  sc->offset = 0;
  sc->limit = elem ? n * elem : 0xFFFFFFFFu;
  sc->len = 0;
  sc->cap = n;
  if (fx->slot_is_map[ci]) {
    HxMap **ms = (HxMap **)sc->block; /* elem_size 8 */
    for (uint32_t i = 0; i < n; i++) ms[i] = hx_new_map(d, fx->slot_mapinfo[ci]);
  }
}

/* ---- lifecycle ---- */

static void hx_init(HxDriver *d, FxTree *fx, HxOpts opts) {
  memset(d, 0, sizeof(*d));
  d->fx   = fx;
  d->opts = opts;
  if (d->opts.init_batch == 0) d->opts.init_batch = 4;
  if (d->opts.slot_batch == 0) d->opts.slot_batch = 128;
  if (d->opts.map_buf_cap == 0) d->opts.map_buf_cap = 64 * 1024;
  if (d->opts.deferred_cap == 0) d->opts.deferred_cap = 384;

  FX_CHECK(sizeof(NdecBindMachine) <= HX_MACHINE_SIZE);
  d->m    = (NdecBindMachine *)hx_alloc(d, HX_MACHINE_SIZE);
  d->atof = hx_alloc(d, HX_ATOF_SIZE);

  memcpy(d->slots, fx->slots, sizeof(BindSlotClass) * (size_t)fx->n_slots);
  for (int i = 0; i < fx->n_slots; i++) hx_install_block(d, (uint32_t)i, d->opts.init_batch);
}

static void hx_destroy(HxDriver *d) {
  for (int i = 0; i < d->n_retained; i++) free(d->retained[i]);
  for (uint32_t i = 0; i < (uint32_t)d->n_maps; i++) {
    for (uint32_t j = 0; j < d->maps[i].n; j++) {
      free(d->maps[i].ents[j].key);
      free(d->maps[i].ents[j].val);
    }
    free(d->maps[i].ents);
  }
  memset(d, 0, sizeof(*d));
}

/* ---- deferred records ---- */

/* Materialize UnmarshalRecords into the driver's record list, mirroring
 * drainDeferredRecords: source spans trim trailing whitespace, TextUnmarshaler
 * spans read str_arena bytes, and the buffer cursor resets. */
static void hx_drain_records(HxDriver *d) {
  NdecBindMachine *m = d->m;
  uint32_t used = m->b.alloc.deferred_drain_used;
  for (uint32_t off = 0; off < used; off += sizeof(UnmarshalRecord)) {
    UnmarshalRecord *r = (UnmarshalRecord *)(m->b.alloc.deferred_drain + off);
    FX_CHECK(d->n_records < HX_REC_MAX);
    HxRecord *rec = &d->records[d->n_records++];
    rec->target   = r->target;
    rec->type_idx = r->type_idx;
    rec->kind     = r->kind;
    rec->backing  = r->backing;
    rec->arg0     = r->arg0;
    rec->arg1     = r->arg1;

    const uint8_t *span;
    uint32_t blen;
    if (r->kind == BIND_KIND_TEXT_UNMARSHALER) {
      span = m->b.alloc.str_arena + r->arg0;
      blen = r->arg1;
    } else if (r->backing == BIND_RECORD_BACKING_SOURCE) {
      span = m->b.ctx.src + r->arg0;
      blen = r->arg1 - r->arg0;
      while (blen > 0 && (span[blen - 1] == ' ' || span[blen - 1] == '\t' || span[blen - 1] == '\n' ||
                          span[blen - 1] == '\r'))
        blen--;
    } else {
      span = m->raw_arena + r->arg0;
      blen = r->arg1 - r->arg0;
    }
    uint32_t copy = blen < sizeof(rec->bytes) ? blen : (uint32_t)sizeof(rec->bytes);
    memcpy(rec->bytes, span, copy);
    rec->blen = blen;
  }
  m->b.alloc.deferred_drain_used = 0;
}

/* ---- map staging drain (port of drainAllMapSlots) ---- */

typedef struct {
  uint8_t *old_e;
  uint8_t *new_e;
  uint32_t stride;
} HxInprogMove;

typedef struct {
  uint32_t old_off;
  uint32_t new_off;
} HxRegionMove;

/* Drain every region's complete entries into its HxMap, then compact live
 * regions toward the buffer front carrying each in-progress entry. Pointer
 * fixups follow the Go driver exactly: frame map regions, frames[].dst, live
 * regions' parent_slot, and Core.CurDst. */
static void hx_drain_maps(HxDriver *d) {
  NdecBindMachine *m = d->m;
  uint8_t *buf = m->b.alloc.map_buf;
  uint32_t used = m->b.alloc.map_buf_used;
  if (used == 0) return;

  uint32_t offs[128];
  int n_off = 0;
  for (uint32_t off = 0; off < used;) {
    BindMapRegionHeader *r = (BindMapRegionHeader *)(buf + off);
    FX_CHECK(n_off < 128);
    offs[n_off++] = off;
    off += BIND_MAP_REGION_HEADER_SIZE + BIND_MAP_REGION_SLOTS * r->stride;
  }

  for (int i = 0; i < n_off; i++) {
    BindMapRegionHeader *r = (BindMapRegionHeader *)(buf + offs[i]);
    BindMapDrainInfo *di =
        (BindMapDrainInfo *)(uintptr_t)m->b.ctx.type_meta[r->type_idx].u.map.drain_info;
    uint8_t *ents = (uint8_t *)r + BIND_MAP_REGION_HEADER_SIZE;
    for (uint32_t e = 0; e < r->entry_count; e++) {
      uint8_t *slot = ents + (size_t)e * r->stride;
      const uint8_t *val = slot + BIND_MAP_VAL_OFF;
      if (di->val_is_deferred) val = *(uint8_t **)val;
      HxStr *k = (HxStr *)slot;
      FX_CHECK(r->hmap != NULL);
      hx_map_put((HxMap *)r->hmap, k->p, (uint32_t)k->len, val);
    }
  }

  int32_t depth = m->c.depth;
  int32_t max_live = depth > 0 ? depth : -1;
  uint8_t live[128] = {0};
  for (int32_t dd = 0; dd <= max_live; dd++) {
    BindFrame *f = &m->c.frames[dd];
    if (f->kind != BIND_KIND_MAP || f->u.map_region == NULL) continue;
    for (int i = 0; i < n_off; i++) {
      if ((uint8_t *)f->u.map_region == buf + offs[i]) {
        live[i] = 1;
        break;
      }
    }
  }

  HxInprogMove moves[128];
  int n_moves = 0;
  HxRegionMove rmoves[128];
  int n_rmoves = 0;
  uint32_t write_pos = 0;
  for (int i = 0; i < n_off; i++) {
    if (!live[i]) continue;
    BindMapRegionHeader *old_r = (BindMapRegionHeader *)(buf + offs[i]);
    uint32_t stride = old_r->stride;
    int has_inprog = old_r->next_entry_off > old_r->entry_count * old_r->stride;
    uint32_t new_off = write_pos;
    if (new_off != offs[i]) memmove(buf + new_off, old_r, BIND_MAP_REGION_HEADER_SIZE);
    BindMapRegionHeader *new_r = (BindMapRegionHeader *)(buf + new_off);
    if (has_inprog) {
      uint32_t old_eoff = BIND_MAP_REGION_HEADER_SIZE + old_r->next_entry_off - old_r->stride;
      uint8_t *old_e = (uint8_t *)old_r + old_eoff;
      uint8_t *new_e = (uint8_t *)new_r + BIND_MAP_REGION_HEADER_SIZE;
      if (old_e != new_e) {
        memmove(new_e, old_e, stride);
        FX_CHECK(n_moves < 128);
        moves[n_moves].old_e = old_e;
        moves[n_moves].new_e = new_e;
        moves[n_moves].stride = stride;
        n_moves++;
      }
      new_r->next_entry_off = new_r->stride;
    } else {
      new_r->next_entry_off = 0;
    }
    new_r->entry_count = 0;
    FX_CHECK(n_rmoves < 128);
    rmoves[n_rmoves].old_off = offs[i];
    rmoves[n_rmoves].new_off = new_off;
    n_rmoves++;
    write_pos += BIND_MAP_REGION_HEADER_SIZE + BIND_MAP_REGION_SLOTS * stride;
  }
  m->b.alloc.map_buf_used = write_pos;

  for (int32_t dd = 0; dd <= max_live; dd++) {
    BindFrame *f = &m->c.frames[dd];
    if (f->kind != BIND_KIND_MAP || f->u.map_region == NULL) continue;
    uint32_t old_off = (uint32_t)((uint8_t *)f->u.map_region - buf);
    for (int j = 0; j < n_rmoves; j++) {
      if (rmoves[j].old_off == old_off) {
        f->u.map_region = (BindMapRegionHeader *)(buf + rmoves[j].new_off);
        break;
      }
    }
  }

  for (int i = 0; i < n_moves; i++) {
    if (moves[i].old_e == moves[i].new_e) continue;
    intptr_t delta = moves[i].new_e - moves[i].old_e;
    uintptr_t s = (uintptr_t)moves[i].old_e;
    uintptr_t e = s + moves[i].stride;
    for (int32_t dd = 0; dd <= depth; dd++) {
      uintptr_t dst = (uintptr_t)m->c.frames[dd].dst;
      if (dst >= s && dst < e) m->c.frames[dd].dst += delta;
    }
    for (int j = 0; j < n_rmoves; j++) {
      BindMapRegionHeader *r = (BindMapRegionHeader *)(buf + rmoves[j].new_off);
      uintptr_t ps = (uintptr_t)r->parent_slot;
      if (ps >= s && ps < e) r->parent_slot += delta;
    }
    uintptr_t cd = (uintptr_t)m->c.cur_dst;
    if (cd >= s && cd < e) m->c.cur_dst += delta;
  }
}

/* ---- yield serving ---- */

static void hx_serve_slice_grow(HxDriver *d) {
  NdecBindMachine *m = d->m;
  FxTree *fx = d->fx;
  HxSlice *hdr = (HxSlice *)m->b.yield.target;
  FX_CHECK(hdr != NULL);
  uint32_t ci = fx->meta[m->b.yield.arg0].u.slice.alloc_class;
  FX_CHECK(ci < (uint32_t)fx->n_slots);
  BindSlotClass *sc = &d->slots[ci];

  uint32_t floor = d->opts.slot_batch / 4;
  uint32_t need_cap = (uint32_t)hdr->len * 2;
  if (need_cap < floor) need_cap = floor;
  if (need_cap < 1) need_cap = 1;

  if (need_cap > d->opts.slot_batch) {
    /* Standalone retained backing beyond the batch ceiling. */
    uint8_t *data = hx_alloc(d, (size_t)need_cap * sc->elem_size);
    if (hdr->data && hdr->len) memcpy(data, hdr->data, (size_t)hdr->len * sc->elem_size);
    hdr->data = data;
    hdr->cap = need_cap;
  } else {
    /* Whole-block install; close returns the unused tail. */
    hx_install_block(d, ci, need_cap);
    uint8_t *data = sc->block;
    sc->offset = sc->limit;
    if (hdr->data && hdr->len) memcpy(data, hdr->data, (size_t)hdr->len * sc->elem_size);
    hdr->data = data;
    hdr->cap = need_cap;
  }
  /* finishGrow: the machine resumes at ARRAY_VALUE with cur_aux as its write
   * cursor recovered from the slice header. */
  m->c.cur_aux = (uint8_t *)hdr->data + (size_t)hdr->len * sc->elem_size;
}

static void hx_serve_tape_arena(HxDriver *d) {
  NdecBindMachine *m = d->m;
  size_t need = m->b.alloc.tape_need;
  d->tape_arena = hx_alloc(d, need * 8);
  d->tape_cap = need;
  /* ROOT_SCANNED has written no tape words, so the fresh arena needs no copy. */
  m->b.alloc.tape_arena = d->tape_arena;
  m->b.alloc.tape_arena_cap = need;
}

static void hx_serve_tape_bind_value(HxDriver *d) {
  NdecBindMachine *m = d->m;
  FX_CHECK(d->n_alias_docs < HX_ALIAS_MAX);
  HxDoc *ad = &d->alias_docs[d->n_alias_docs++];
  ad->src = m->b.ctx.src;
  ad->str_arena = m->b.alloc.str_arena;
  ad->tape = m->b.alloc.value_tape;

  uint32_t sub_start = m->b.yield.arg0;
  uint32_t sub_words = m->b.yield.arg1;
  uint8_t *slot = m->c.stash.tape_value_yield.slot;
  HxValue v;
  v.doc = ad;
  v.base = 0;
  v.tidx = (int32_t)sub_start;
  v.end = (int32_t)(sub_start + sub_words);
  v.mode = (int32_t)m->c.stash.tape_value_yield.view_mode;
  memcpy(slot, &v, sizeof(v));
}

static void hx_trace_yield(HxDriver *d) {
  NdecBindMachine *m = d->m;
  FX_CHECK(d->n_yields < HX_YIELD_MAX);
  HxYieldInfo *y = &d->yields[d->n_yields++];
  y->action = m->b.yield.pending_action;
  y->arg0 = m->b.yield.arg0;
  y->arg1 = m->b.yield.arg1;
  y->phase = m->c.phase;
  y->target = m->b.yield.target;
}

static int hx_yield_count(const HxDriver *d, uint32_t action) {
  int n = 0;
  for (int i = 0; i < d->n_yields; i++)
    if (d->yields[i].action == action) n++;
  return n;
}

static int hx_yield_phase_seen(const HxDriver *d, uint32_t action, uint32_t phase) {
  for (int i = 0; i < d->n_yields; i++)
    if (d->yields[i].action == action && d->yields[i].phase == phase) return 1;
  return 0;
}

/* Machine-wide post-conditions after a clean document end. */
static int hx_finish(HxDriver *d) {
  NdecBindMachine *m = d->m;
  if (m->b.alloc.deferred_drain_used) hx_drain_records(d);
  if (m->b.alloc.map_buf_used) hx_drain_maps(d);

  d->doc.src = m->b.ctx.src;
  d->doc.str_arena = m->b.alloc.str_arena;
  d->doc.tape = m->b.alloc.tape_arena;
  d->published_str = (uint32_t)m->c.str_used;

  /* t_document_end publishes str_used but leaves depth, cursor, and the hot
   * locals stale (only the JSON document_end spills them). A clean tape close
   * is instead witnessed by rebind_top and the machine's own TAP_EOF check. */
  if (!m->in_tape_bind) FX_CHECK(m->c.depth == 0);
  FX_CHECK(m->aux_depth == 0);
  FX_CHECK(m->rebind_top == 0);
  FX_CHECK(m->c.str_used <= m->b.alloc.str_arena_cap);
  FX_CHECK(m->b.alloc.tape_used <= m->b.alloc.tape_arena_cap);
  FX_CHECK(m->b.alloc.deferred_drain_used == 0);
  FX_CHECK(m->b.alloc.map_buf_used == 0);
  return 0;
}

static void hx_reset_run(HxDriver *d) {
  d->n_yields = 0;
  d->n_records = 0;
  d->n_alias_docs = 0;
  d->err = 0;
  d->err_arg1 = 0;
  d->err_pos = 0;
  /* Prewired HxMap objects stay owned by their slot blocks; only their
   * contents reset so a reused driver starts from empty maps. */
  for (uint32_t i = 0; i < (uint32_t)d->n_maps; i++) {
    HxMap *mp = &d->maps[i];
    for (uint32_t j = 0; j < mp->n; j++) {
      free(mp->ents[j].key);
      free(mp->ents[j].val);
    }
    mp->n = 0;
  }
}

/* The unified loop: run the contiguous engine and service each yield exactly
 * as serveYield does. Returns 0 on success, 1 on a machine error yield. */
static int hx_drive(HxDriver *d) {
  NdecBindMachine *m = d->m;
  for (;;) {
    ndec_bind_parse((void *)m);
    hx_trace_yield(d);
    switch (m->b.yield.pending_action) {
    case BIND_YIELD_NONE:
      return hx_finish(d);
    case BIND_YIELD_ERROR:
      d->err = (int)m->b.yield.arg0;
      d->err_arg1 = m->b.yield.arg1;
      d->err_pos = m->b.yield.first_error_pos;
      return 1;
    case BIND_YIELD_BLOCK_FULL:
      FX_CHECK(m->b.yield.arg0 < (uint32_t)d->fx->n_slots);
      hx_install_block(d, m->b.yield.arg0, d->opts.slot_batch);
      break;
    case BIND_YIELD_SLICE_GROW:
      hx_serve_slice_grow(d);
      break;
    case BIND_YIELD_TAPE_ARENA:
      hx_serve_tape_arena(d);
      break;
    case BIND_YIELD_FLUSH_MAP:
      if (m->b.alloc.deferred_drain_used) hx_drain_records(d);
      hx_drain_maps(d);
      break;
    case BIND_YIELD_FLUSH_UNMARSHAL:
      hx_drain_records(d);
      break;
    case BIND_YIELD_TAPE_BIND_VALUE:
      hx_serve_tape_bind_value(d);
      break;
    default:
      /* BIND_YIELD_INPUT is unreachable in the contiguous engine; RecBatch
       * yields are unreachable because every fixture slot is BUMP mode. */
      FX_CHECK(!"unexpected yield action");
      break;
    }
  }
}

/* Seed the shared context fields; per-entry callers complete the setup. */
static void hx_seed_ctx(HxDriver *d) {
  FxTree *fx = d->fx;
  NdecBindMachine *m = d->m;
  memset(m, 0, sizeof(*m));
  m->c.atof = (atof_ctx *)d->atof;
  m->b.ctx.types = fx->types;
  m->b.ctx.type_meta = fx->meta;
  m->b.ctx.any_type_idx = fx->t_any;
  m->b.ctx.polys = fx->n_poly ? fx->polys : NULL;
  m->b.alloc.slot_classes = d->slots;
  m->b.alloc.deferred_drain = hx_alloc(d, d->opts.deferred_cap);
  m->b.alloc.deferred_drain_cap = d->opts.deferred_cap;
  m->b.alloc.map_buf = hx_alloc(d, d->opts.map_buf_cap);
  m->b.alloc.map_buf_cap = d->opts.map_buf_cap;
}

/* Parse one JSON document into root_dst. Returns 0 on success. */
static int hx_run_json(HxDriver *d, const char *json, size_t len, uint16_t root_type, void *root_dst,
                       uint32_t opt_flags) {
  FxTree *fx = d->fx;
  hx_reset_run(d);
  NdecBindMachine *m = d->m;
  hx_seed_ctx(d);

  d->src_len = len;
  d->src_padded = hx_alloc(d, len + 64);
  memcpy(d->src_padded, json, len);
  memset(d->src_padded + len, 0x20, 64);
  m->b.ctx.src = d->src_padded;
  m->b.ctx.src_len = len;
  m->b.ctx.root_type = root_type;
  m->b.ctx.root_dst = (uint8_t *)root_dst;
  m->b.ctx.opt_flags = opt_flags;

  uint32_t need_u32 = (uint32_t)len + 64;
  if (d->structural_cap < need_u32) {
    d->structural = hx_alloc(d, (size_t)need_u32 * 4);
    d->structural_cap = need_u32;
  }
  m->b.alloc.structural = d->structural;
  m->b.alloc.structural_cap = d->structural_cap;

  d->str_arena = hx_alloc(d, len + 64);
  d->str_cap = len + 64;
  m->b.alloc.str_arena = d->str_arena;
  m->b.alloc.str_arena_cap = d->str_cap;
  m->b.alloc.str_gen_start = 0;
  m->c.str_used = 0;

  if (fx->needs_tape) {
    /* SIZE_TAPE + TAPE_DUAL count with the dual prologue included, and the
     * ceiling stays a proven superset bound (2*srcLen) so the presized arena
     * is never short. A nonzero tape_guess forces the TAPE_ARENA yield. */
    m->b.ctx.opt_flags |= BIND_OPT_SIZE_TAPE | BIND_OPT_TAPE_DUAL;
    uint32_t ceiling = 2 * (uint32_t)len + 8;
    m->b.alloc.tape_need = ceiling;
    size_t words = d->opts.tape_guess ? d->opts.tape_guess : ceiling;
    d->tape_arena = hx_alloc(d, words * 8);
    d->tape_cap = words;
    m->b.alloc.tape_arena = d->tape_arena;
    m->b.alloc.tape_arena_cap = words;
    m->b.alloc.tape_used = 0;
  }

  m->b.alloc.value_doc = fx->has_value ? (void *)&d->doc : NULL;
  m->c.phase = BIND_PHASE_ROOT;
  return hx_drive(d);
}

/* Parse one JSON document whose root type is the Value singleton, publishing a
 * standalone Value plus the doc views a later tape-bind run consumes. */
static int hx_value_root(HxDriver *d, const char *json, size_t len, HxValue *out) {
  int rc = hx_run_json(d, json, len, d->fx->t_value, out, 0);
  if (rc == 0) FX_CHECK(out->doc == (void *)&d->doc && out->base == 0 && out->tidx == 0);
  return rc;
}

/* Tape-bind root: bind a published Value's tape into root_dst, mirroring
 * unmarshalValue. The source driver keeps the tape, str_arena, and padded
 * source alive for this run's raw-string and provenance reads. */
static int hx_run_tape(HxDriver *d, const HxDriver *src_drv, const HxValue *src, uint16_t root_type,
                       void *root_dst) {
  FxTree *fx = d->fx;
  const HxDoc *sd = &src_drv->doc;
  hx_reset_run(d);
  NdecBindMachine *m = d->m;
  hx_seed_ctx(d);

  m->b.ctx.src = sd->src;
  m->b.ctx.src_len = src_drv->src_len;
  m->b.ctx.root_type = root_type;
  m->b.ctx.root_dst = (uint8_t *)root_dst;
  m->b.ctx.root_view_mode = (uint32_t)src->mode;

  /* Owned copy of the published string prefix; append room covers quoted and
   * discriminator copies the tape walk may add. */
  uint32_t published = src_drv->published_str;
  size_t append = 4 * (size_t)src->end + 256;
  d->str_arena = hx_alloc(d, published + append + 64);
  d->str_cap = published + append + 64;
  memcpy(d->str_arena, sd->str_arena, published);
  m->b.alloc.str_arena = d->str_arena;
  m->b.alloc.str_arena_cap = d->str_cap;
  m->b.alloc.str_gen_start = published;
  m->c.str_used = published;

  /* The source tape is borrowed; scratch merged tapes land in a fresh arena. */
  m->b.alloc.value_tape = (uint64_t *)sd->tape + src->base;
  m->cursor.tape = sd->tape + src->base + src->tidx;
  m->cursor_end.tape = sd->tape + src->base + src->end;

  size_t words = 2 * (size_t)src->end + 64;
  d->tape_arena = hx_alloc(d, words * 8);
  d->tape_cap = words;
  m->b.alloc.tape_arena = d->tape_arena;
  m->b.alloc.tape_arena_cap = words;
  m->b.alloc.tape_used = 0;
  m->b.alloc.tape_need = (uint32_t)words;

  m->b.alloc.value_doc = fx->has_value ? (void *)&d->doc : NULL;
  m->c.phase = BIND_PHASE_TAPE_BIND_ROOT;
  return hx_drive(d);
}

/* ---- result walkers ---- */

typedef struct HxEnt {
  char key[80];
  uint32_t klen;
  uint8_t vtag;
  HxStr str;
  double num;
  int64_t inum;
} HxEnt;

static const HxEnt *hx_ent_find(const HxEnt *ents, int n, const char *key) {
  uint32_t klen = (uint32_t)strlen(key);
  for (int i = 0; i < n; i++) {
    if (ents[i].klen == klen && memcmp(ents[i].key, key, klen) == 0) return &ents[i];
  }
  return NULL;
}

/* Published count of a Value's root container, honoring the count-at-close
 * mode of a dual merged root. */
static uint32_t hx_value_count(const HxDoc *doc, const HxValue *v) {
  const uint64_t *tape = doc->tape;
  uint64_t root = tape[v->base + v->tidx];
  if (v->mode & TAPE_MODE_COUNT_AT_CLOSE) {
    uint32_t close_rel = (uint32_t)(root & 0xFFFFFFFFu);
    return (uint32_t)((tape[v->base + close_rel] >> 32) & 0xFFFFFFu);
  }
  return (uint32_t)((root >> 32) & 0xFFFFFFu);
}

/* Walk one object Value in its descriptor's seam view, collecting entries.
 * Compact vd tapes carry no seams and traverse identically. */
static int hx_walk_object(const HxDoc *doc, const HxValue *v, HxEnt *out, int max) {
  const uint64_t *tape = doc->tape;
  uint32_t shift = (uint32_t)v->mode & TAPE_VIEW_SHIFT_MASK;
  const uint64_t *root = tape + v->base + v->tidx;
  FX_CHECK((uint8_t)(*root >> 56) == (uint8_t)(TAPE_START_OBJECT >> 56));
  uint32_t close_rel = (uint32_t)(*root & 0xFFFFFFFFu);
  const uint64_t *limit = tape + v->base + close_rel;
  TapeView tv = tape_view(tape + v->base, limit, shift);
  const uint64_t *p = tape_seam_skip(root + 1, limit, shift);
  int n = 0;
  while (p < limit) {
    FX_CHECK(n < max);
    uint64_t kw = *p++;
    uint32_t klen;
    const uint8_t *kd = tape_bind_string_ptr(kw, doc->str_arena, doc->src, &klen);
    HxEnt *e = &out[n++];
    uint32_t kcopy = klen < sizeof(e->key) - 1 ? klen : (uint32_t)sizeof(e->key) - 1;
    memcpy(e->key, kd, kcopy);
    e->key[kcopy] = 0;
    e->klen = klen;

    const uint64_t *vend = tape_value_end(p, tv);
    e->vtag = (uint8_t)(*p >> 56);
    e->str.p = NULL;
    e->str.len = 0;
    e->num = 0;
    e->inum = 0;
    if (TAPE_IS_STRING_TAG(e->vtag)) {
      uint32_t vl;
      e->str.p = tape_bind_string_ptr(*p, doc->str_arena, doc->src, &vl);
      e->str.len = vl;
    } else if (e->vtag == (uint8_t)(TAPE_INT64 >> 56)) {
      int64_t i;
      memcpy(&i, p + 1, 8);
      e->inum = i;
      e->num = (double)i;
    } else if (e->vtag == (uint8_t)(TAPE_UINT64 >> 56)) {
      uint64_t u;
      memcpy(&u, p + 1, 8);
      e->inum = (int64_t)u;
      e->num = (double)u;
    } else if (e->vtag == (uint8_t)(TAPE_DOUBLE >> 56)) {
      memcpy(&e->num, p + 1, 8);
    }
    p = tape_seam_skip(vend, limit, shift);
  }
  return n;
}

#endif /* NDEC_BIND_DRIVER_H */
