/* bind_fixture.h constructs the ABI type tables the Go vbind builder produces
 * for the typed-binding state machine: BindType/BindField/BindTypeMeta records,
 * SlotClass metadata, poly tables, the any singleton, and the lookup blobs both
 * sides build through vlib. Only fields the machine reads are set; Go-only
 * payload fields (elem_rtype, map_rtype, ptr_child_size) stay zero or carry
 * opaque identity tokens.
 *
 * A fixture tree is read-only for the machine and for every driver run, so one
 * tree can back multiple sequential runs (parity tests reuse it).
 */
#ifndef NDEC_BIND_FIXTURE_H
#define NDEC_BIND_FIXTURE_H

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "ndec/bind_bridge.h"
#include "vlib/lookup.h"

#define FX_MAX_TYPES   64
#define FX_MAX_FIELDS  256
#define FX_MAX_SLOTS   48
#define FX_MAX_POLY    16
#define FX_POLY_CASES  80
#define FX_MAPINFO     16
#define FX_BLOBS       (FX_MAX_TYPES + FX_MAX_POLY)
#define FX_BLOB_BYTES  4096
#define FX_SCRATCH     (96 * 1024)

#define FX_CHECK(cond)                                                                                            \
  do {                                                                                                            \
    if (!(cond)) {                                                                                                \
      fprintf(stderr, "fx: %s:%d: %s\n", __FILE__, __LINE__, #cond);                                              \
      __builtin_trap();                                                                                           \
    }                                                                                                             \
  } while (0)

/* Opaque identity tokens for eface type words and case rtype slots. Distinct
 * addresses are all the machine and the tests need. */
static const char fx_tok_pool[256] = {0};
static const void *fx_token(unsigned id) {
  return &fx_tok_pool[id & 0xFFu];
}
/* Case rtype token for a case type index; tests compare eface.type against it. */
static const void *fx_case_rtype(uint16_t case_type_idx) {
  return fx_token(32u + case_type_idx);
}

static const uint64_t fx_static_true  = 1;
static const uint64_t fx_static_false = 0;
static const uint64_t fx_empty_slice_data;

typedef struct FxCase {
  const char *name; /* variant case name; ignored for kindof entries */
  uint16_t type_idx; /* 0xFFFF marks an unregistered kindof kind */
  int32_t slot_class; /* storage for value-kind cases; -1 for PTR/MAP direct */
} FxCase;

typedef struct FxTree {
  BindType types[FX_MAX_TYPES];
  BindTypeMeta meta[FX_MAX_TYPES];
  int n_types;

  BindField fields[FX_MAX_FIELDS]; /* per-struct contiguous runs */
  const char *names[FX_MAX_FIELDS];
  int n_fields;
  int field_base[FX_MAX_TYPES];

  BindSlotClass slots[FX_MAX_SLOTS]; /* backing block installed by the driver */
  int n_slots;
  uint32_t slot_elem[FX_MAX_SLOTS];
  uint8_t slot_is_map[FX_MAX_SLOTS];
  int8_t slot_mapinfo[FX_MAX_SLOTS];

  BindPolyTable polys[FX_MAX_POLY];
  int n_poly;
  uint16_t pc_type_idx[FX_POLY_CASES];
  const void *pc_rtype[FX_POLY_CASES];
  int32_t pc_slot[FX_POLY_CASES];
  int n_pc;

  BindAnyMeta any; /* filled by fx_build */
  BindMapDrainInfo mapinfo[FX_MAPINFO];
  int n_mapinfo;

  /* Lookup blobs hold structs with size_t members, so the storage is aligned. */
  union {
    uint64_t align;
    uint8_t bytes[FX_BLOB_BYTES];
  } blobs[FX_BLOBS];
  int n_blobs;
  uint8_t lookup_scratch[FX_SCRATCH];

  /* Singleton type indices, fixed by fx_init. */
  uint16_t t_any, t_iface, t_value;
  uint16_t t_bool, t_int, t_i8, t_i16, t_i32, t_i64;
  uint16_t t_u, t_u8, t_u16, t_u32, t_u64, t_f32, t_f64;
  uint16_t t_string, t_number, t_slice_any, t_map_any;
  int any_f64_slot, any_str_slot, any_slice_slot, any_map_slot;

  int needs_tape; /* any Value output or poly field: tape arena required */
  int has_value;  /* any Value type: value_doc required */
} FxTree;

static uint32_t fx_kind_size(uint8_t kind) {
  switch (kind) {
  case BIND_KIND_BOOL:
  case BIND_KIND_INT8:
  case BIND_KIND_UINT8:
    return 1;
  case BIND_KIND_INT16:
  case BIND_KIND_UINT16:
    return 2;
  case BIND_KIND_INT32:
  case BIND_KIND_UINT32:
  case BIND_KIND_FLOAT32:
    return 4;
  case BIND_KIND_INT:
  case BIND_KIND_INT64:
  case BIND_KIND_UINT:
  case BIND_KIND_UINT64:
  case BIND_KIND_FLOAT64:
    return 8;
  case BIND_KIND_STRING:
  case BIND_KIND_NUMBER:
  case BIND_KIND_ANY:
  case BIND_KIND_IFACE:
    return 16;
  case BIND_KIND_SLICE:
  case BIND_KIND_STREAM:
  case BIND_KIND_RAW_MESSAGE:
  case BIND_KIND_VALUE:
    return 24;
  case BIND_KIND_PTR:
  case BIND_KIND_MAP:
    return 8;
  default:
    return 0;
  }
}

static uint16_t fx_new_type(FxTree *t, uint8_t kind, uint8_t flags) {
  FX_CHECK(t->n_types < FX_MAX_TYPES);
  uint16_t idx = (uint16_t)t->n_types++;
  memset(&t->types[idx], 0, sizeof(BindType));
  memset(&t->meta[idx], 0, sizeof(BindTypeMeta));
  t->types[idx].kind     = kind;
  t->types[idx].flags    = flags;
  t->types[idx].type_idx = idx;
  t->meta[idx].size      = fx_kind_size(kind);
  return idx;
}

/* Register a SlotClass in BUMP mode. The driver installs the backing block and,
 * for map classes, prewires HxMap pointers into it. */
static int fx_slot(FxTree *t, uint32_t elem_size) {
  FX_CHECK(t->n_slots < FX_MAX_SLOTS);
  int i            = t->n_slots++;
  BindSlotClass *sc = &t->slots[i];
  memset(sc, 0, sizeof(*sc));
  sc->mode           = BIND_SLOT_BUMP;
  sc->elem_size      = elem_size;
  t->slot_elem[i]    = elem_size;
  t->slot_is_map[i]  = 0;
  t->slot_mapinfo[i] = -1;
  return i;
}

/* Deferred-hook kinds are cold and sized per receiver; RawMessage reuses the
 * slice-header size. */
static uint16_t fx_deferred(FxTree *t, uint8_t kind, uint32_t size) {
  FX_CHECK(kind == BIND_KIND_RAW_MESSAGE || kind == BIND_KIND_TEXT_UNMARSHALER ||
           kind == BIND_KIND_UNMARSHALER);
  uint16_t idx = fx_new_type(t, kind, BIND_FLAG_COLD);
  t->meta[idx].size = size ? size : fx_kind_size(kind);
  return idx;
}

static uint16_t fx_slice(FxTree *t, uint16_t child, uint32_t child_size) {
  uint16_t idx = fx_new_type(t, BIND_KIND_SLICE, 0);
  int sc             = fx_slot(t, child_size);
  t->types[idx].u.slice.child_size = child_size;
  t->types[idx].child              = (uintptr_t)&t->types[child];
  t->meta[idx].u.slice.alloc_class = sc;
  t->meta[idx].u.slice.empty_slice_data = (uintptr_t)&fx_empty_slice_data;
  return idx;
}

static uint16_t fx_array(FxTree *t, uint16_t child, uint32_t child_size, uint32_t len) {
  uint16_t idx = fx_new_type(t, BIND_KIND_ARRAY, 0);
  t->types[idx].u.raw            = child_size; /* ARRAY reads child_size through u.raw */
  t->types[idx].child            = (uintptr_t)&t->types[child];
  t->meta[idx].u.array.array_len = len;
  t->meta[idx].size              = len * child_size;
  return idx;
}

static uint16_t fx_ptr(FxTree *t, uint16_t child) {
  uint32_t pointee = t->meta[child].size;
  uint16_t idx = fx_new_type(t, BIND_KIND_PTR, BIND_FLAG_COLD);
  int sc              = fx_slot(t, pointee);
  t->types[idx].u.ptr.alloc_class = sc;
  t->types[idx].child             = (uintptr_t)&t->types[child];
  t->meta[idx].u.ptr.ptr_child_size = pointee;
  return idx;
}

/* String-keyed map. val_slot_class names the intermediate slot for deferred or
 * Value-bearing element types and must use elem_size == val_size. */
static uint16_t fx_map(FxTree *t, uint16_t val_type, uint32_t val_size, int val_is_deferred,
                       int32_t val_slot_class) {
  uint16_t idx = fx_new_type(t, BIND_KIND_MAP, 0);
  FX_CHECK(t->n_mapinfo < FX_MAPINFO);
  int mi                = t->n_mapinfo++;
  uint32_t stride       = (16 + val_size + 7u) & ~(uint32_t)7u;
  BindMapDrainInfo *di  = &t->mapinfo[mi];
  memset(di, 0, sizeof(*di));
  di->map_rtype       = fx_token(7u + mi);
  di->kv_stride       = stride;
  di->key_kind        = BIND_KIND_STRING;
  di->val_size        = val_size;
  di->val_is_deferred = (uint8_t)(val_is_deferred ? 1 : 0);
  di->val_slot_class  = val_slot_class;

  int hs = fx_slot(t, 8); /* hmap pointer slots, prewired by the driver */
  t->slot_is_map[hs]  = 1;
  t->slot_mapinfo[hs] = (int8_t)mi;

  t->types[idx].u.map.alloc_class = hs;
  t->types[idx].child             = (uintptr_t)&t->types[val_type];
  t->meta[idx].u.map.key_type     = t->t_string;
  t->meta[idx].u.map.drain_info   = (uintptr_t)di;
  t->meta[idx].u.map.stride       = stride;
  t->meta[idx].size               = 8;
  return idx;
}

/* ---- struct fields ---- */

static uint16_t fx_struct(FxTree *t, uint32_t size) {
  uint16_t idx = fx_new_type(t, BIND_KIND_STRUCT, 0);
  t->meta[idx].size    = size;
  t->field_base[idx]   = t->n_fields;
  return idx;
}

/* Append one field. Returns its ordinal within the struct, for
 * fx_variant/fx_reserve_unknown.
 *
 * The JSON field path gates predispatch on the field flags being nonzero, so
 * the child type's flags (COLD for pointer/Value/any/interface/deferred kinds,
 * CONTAINS_DEFERRED, ELEM_HAS_STREAM) fold in here, mirroring vbind's
 * reapplyFieldFlags. MAY_PHASE2 stays type-local and is excluded. */
static int fx_fld(FxTree *t, uint16_t st, const char *name, uint16_t type_idx, uint32_t off,
                  uint32_t flags) {
  FX_CHECK(t->types[st].kind == BIND_KIND_STRUCT);
  FX_CHECK(t->n_fields < FX_MAX_FIELDS);
  int ord       = t->n_fields - t->field_base[st];
  BindField *f  = &t->fields[t->n_fields];
  uint32_t type_flags = t->types[type_idx].flags & ~(uint32_t)BIND_FLAG_MAY_PHASE2;
  f->type       = (uintptr_t)&t->types[type_idx];
  f->offset     = off;
  f->flags      = flags | type_flags;
  t->names[t->n_fields] = name;
  t->n_fields++;
  return ord;
}

/* Build one lookup blob over n keys through vlib, all tiers. */
static uintptr_t fx_lookup_blob(FxTree *t, const char *const *keys, int n) {
  FX_CHECK(t->n_blobs < FX_BLOBS);
  uint8_t *blob = t->blobs[t->n_blobs].bytes;
  if (n == 0) {
    /* Fieldless structs use the 4-byte TIER_NONE sentinel: find always misses. */
    memset(blob, 0, 4);
    t->n_blobs++;
    return (uintptr_t)blob;
  }
  ndec_lookup_key lk[FX_MAX_FIELDS];
  for (int i = 0; i < n; i++) {
    lk[i].str = keys[i];
    lk[i].len = strlen(keys[i]);
  }
  ndec_lookup_config cfg = {lk, (size_t)n, NDEC_LOOKUP_TIERS_ALL, t->lookup_scratch, FX_SCRATCH};
  size_t need = ndec_lookup_size_for(&cfg);
  FX_CHECK(need > 0 && need <= FX_BLOB_BYTES);
  int tier = ndec_lookup_init((ndec_lookup *)blob, FX_BLOB_BYTES, &cfg);
  FX_CHECK(tier > 0);
  t->n_blobs++;
  return (uintptr_t)blob;
}

/* Link the field run and build the field-name lookup. Poly features
 * (fx_variant/fx_kindof/fx_reserve_unknown) run after this. */
static void fx_struct_done(FxTree *t, uint16_t st) {
  int base = t->field_base[st];
  int n    = t->n_fields - base;
  t->types[st].u.strct.field_count = (uint32_t)n;
  t->types[st].child               = (uintptr_t)&t->fields[base];
  t->meta[st].u.strct.lookup       = fx_lookup_blob(t, &t->names[base], n);
  t->meta[st].u.strct.inline_variant_idx = 0xFFFFu;
  t->meta[st].u.strct.reserve_unknown_field_off = 0xFFFFFFFFu;
  t->meta[st].u.strct.ptr_hops = 0;
}

/* ---- poly tables ---- */

/* Variant table: disc_fld is the host's string discriminator field and
 * carrier_fld the eface carrier (inline) or the variant field itself. inline_
 * selects the dual-view shape: the discriminator is INLINE_VDISC, the carrier
 * INLINE_VARIANT, and meta.inline_variant_idx names this table. */
static int fx_variant(FxTree *t, uint16_t host, int disc_fld, int carrier_fld, const FxCase *cases,
                      int n_cases, int default_case, int inline_) {
  FX_CHECK(t->n_poly < FX_MAX_POLY);
  FX_CHECK(n_cases > 0 && n_cases <= 64);
  int base = t->field_base[host];
  FX_CHECK(disc_fld >= 0 && disc_fld < (int)t->types[host].u.strct.field_count);
  FX_CHECK(carrier_fld >= 0 && carrier_fld < (int)t->types[host].u.strct.field_count);

  int pi = t->n_poly++;
  FX_CHECK(t->n_pc + n_cases <= FX_POLY_CASES);
  int pc0 = t->n_pc;
  const char *names[64];
  for (int i = 0; i < n_cases; i++) {
    t->pc_type_idx[t->n_pc] = cases[i].type_idx;
    t->pc_rtype[t->n_pc]    = fx_case_rtype(cases[i].type_idx);
    t->pc_slot[t->n_pc]     = cases[i].slot_class;
    names[i]                = cases[i].name;
    t->n_pc++;
  }
  BindPolyTable *pt = &t->polys[pi];
  memset(pt, 0, sizeof(*pt));
  pt->disc_off         = t->fields[base + disc_fld].offset;
  pt->default_case_idx = default_case < 0 ? BIND_POLY_NO_DEFAULT_CASE : (uint16_t)default_case;
  pt->case_count       = (uint16_t)n_cases;
  pt->case_type_idx    = &t->pc_type_idx[pc0];
  pt->case_rtype       = &t->pc_rtype[pc0];
  pt->case_slot_class  = &t->pc_slot[pc0];
  pt->lookup           = fx_lookup_blob(t, names, n_cases);

  BindField *df = &t->fields[base + disc_fld];
  df->flags |= BIND_FF_VDISC | ((uint32_t)pi << 16);
  BindField *cf = &t->fields[base + carrier_fld];
  cf->flags |= BIND_FF_VARIANT | ((uint32_t)pi << 16);
  if (inline_) {
    df->flags |= BIND_FF_INLINE_VDISC;
    cf->flags |= BIND_FF_INLINE_VARIANT;
    t->meta[host].u.strct.inline_variant_idx = (uint16_t)pi;
  }
  t->types[host].flags |= BIND_FLAG_MAY_PHASE2;
  return pi;
}

/* Kindof table: the five FxCase entries are indexed by JSON kind
 * (bool, number, string, array, object); type_idx 0xFFFF marks an
 * unregistered kind. */
static int fx_kindof(FxTree *t, uint16_t host, int carrier_fld, const FxCase kinds[5]) {
  FX_CHECK(t->n_poly < FX_MAX_POLY);
  int base = t->field_base[host];
  FX_CHECK(carrier_fld >= 0 && carrier_fld < (int)t->types[host].u.strct.field_count);

  int pi = t->n_poly++;
  int pc0 = t->n_pc;
  FX_CHECK(t->n_pc + 5 <= FX_POLY_CASES);
  for (int i = 0; i < 5; i++) {
    uint16_t ct   = kinds[i].type_idx;
    t->pc_type_idx[t->n_pc] = ct == 0xFFFFu ? 0 : ct;
    t->pc_rtype[t->n_pc]    = ct == 0xFFFFu ? NULL : fx_case_rtype(ct);
    t->pc_slot[t->n_pc]     = kinds[i].slot_class;
    t->n_pc++;
  }
  BindPolyTable *pt = &t->polys[pi];
  memset(pt, 0, sizeof(*pt));
  pt->disc_off         = 0;
  pt->default_case_idx = BIND_POLY_NO_DEFAULT_CASE;
  pt->case_count       = BIND_POLY_KIND_COUNT;
  pt->case_type_idx    = &t->pc_type_idx[pc0];
  pt->case_rtype       = &t->pc_rtype[pc0];
  pt->case_slot_class  = &t->pc_slot[pc0];
  pt->lookup           = 0;

  t->fields[base + carrier_fld].flags |= BIND_FF_KINDOF | ((uint32_t)pi << 16);
  t->types[host].flags |= BIND_FLAG_MAY_PHASE2;
  return pi;
}

/* The reserve-unknown carrier field must have the VALUE singleton type. */
static void fx_reserve_unknown(FxTree *t, uint16_t host, int fld) {
  int base = t->field_base[host];
  FX_CHECK(fld >= 0 && fld < (int)t->types[host].u.strct.field_count);
  BindField *f = &t->fields[base + fld];
  FX_CHECK(((const BindType *)(uintptr_t)f->type)->kind == BIND_KIND_VALUE);
  f->flags |= BIND_FF_RESERVE_UNKNOWN;
  t->meta[host].u.strct.reserve_unknown_field_off = f->offset;
  t->types[host].flags |= BIND_FLAG_MAY_PHASE2;
}

/* Extra type flags: BIND_FLAG_CONTAINS_DEFERRED for aggregates whose field tree
 * reaches any/Value/deferred kinds, consumed by map-value staging. */
static void fx_flag(FxTree *t, uint16_t type_idx, uint8_t flag) {
  t->types[type_idx].flags |= flag;
}

static void fx_init(FxTree *t) {
  memset(t, 0, sizeof(*t));
  t->t_any   = fx_new_type(t, BIND_KIND_ANY, BIND_FLAG_COLD);
  t->t_iface = fx_new_type(t, BIND_KIND_IFACE, BIND_FLAG_COLD);
  t->t_value = fx_new_type(t, BIND_KIND_VALUE, BIND_FLAG_COLD);

  t->t_bool   = fx_new_type(t, BIND_KIND_BOOL, 0);
  t->t_int    = fx_new_type(t, BIND_KIND_INT, 0);
  t->t_i8     = fx_new_type(t, BIND_KIND_INT8, 0);
  t->t_i16    = fx_new_type(t, BIND_KIND_INT16, 0);
  t->t_i32    = fx_new_type(t, BIND_KIND_INT32, 0);
  t->t_i64    = fx_new_type(t, BIND_KIND_INT64, 0);
  t->t_u      = fx_new_type(t, BIND_KIND_UINT, 0);
  t->t_u8     = fx_new_type(t, BIND_KIND_UINT8, 0);
  t->t_u16    = fx_new_type(t, BIND_KIND_UINT16, 0);
  t->t_u32    = fx_new_type(t, BIND_KIND_UINT32, 0);
  t->t_u64    = fx_new_type(t, BIND_KIND_UINT64, 0);
  t->t_f32    = fx_new_type(t, BIND_KIND_FLOAT32, 0);
  t->t_f64    = fx_new_type(t, BIND_KIND_FLOAT64, 0);
  t->t_string = fx_new_type(t, BIND_KIND_STRING, 0);
  t->t_number = fx_new_type(t, BIND_KIND_NUMBER, 0);

  /* Any eface data slots and the registered []any / map[string]any types any
   * descent reuses. */
  t->any_f64_slot = fx_slot(t, 8);
  t->any_str_slot = fx_slot(t, 16);
  t->any_slice_slot = fx_slot(t, 24);
  t->t_slice_any  = fx_slice(t, t->t_any, 16);
  t->t_map_any    = fx_map(t, t->t_any, 16, 0, -1);
  t->any_map_slot = t->n_slots - 1; /* hmap slot registered by fx_map */
}

static void fx_build(FxTree *t) {
  /* The any singleton's child pointer and metadata; slot classes exist since
   * fx_init. */
  t->types[t->t_any].child = (uintptr_t)&t->any;
  BindAnyMeta *am          = &t->any;
  memset(am, 0, sizeof(*am));
  am->float64_type       = fx_token(1);
  am->string_type        = fx_token(2);
  am->bool_type          = fx_token(3);
  am->nil_type           = NULL;
  am->slice_type         = fx_token(4);
  am->map_type           = fx_token(5);
  am->static_true        = &fx_static_true;
  am->static_false       = &fx_static_false;
  am->float64_slot_class = t->any_f64_slot;
  am->string_slot_class  = t->any_str_slot;
  am->slice_slot_class   = t->any_slice_slot;
  am->map_slot_class     = t->any_map_slot;
  am->slice_any_type_idx = t->t_slice_any;
  am->map_any_type_idx   = t->t_map_any;
  am->number_type        = fx_token(6);

  for (int i = 0; i < t->n_types; i++) {
    if (t->types[i].kind == BIND_KIND_VALUE) {
      t->has_value  = 1;
      t->needs_tape = 1;
    }
  }
  for (int i = 0; i < t->n_fields; i++) {
    if (t->fields[i].flags &
        (BIND_FF_VARIANT | BIND_FF_KINDOF | BIND_FF_INLINE_VARIANT | BIND_FF_RESERVE_UNKNOWN))
      t->needs_tape = 1;
  }
}

#endif /* NDEC_BIND_FIXTURE_H */
