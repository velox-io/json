/* bind_tests.c drives the typed-binding state machine through the C driver in
 * bind_driver.h, which mirrors the Go driver's yield loop. M1 verifies the
 * driver itself against the machine's core paths; later suites cover the
 * merged-tape phase 2 machinery and the tape-bind root. */
#include <criterion/criterion.h>

#include "bind_driver.h"

static FxTree g_fx;

static void fx_setup(void) {
  fx_init(&g_fx);
}

/* ---- M1: driver shakeout ---- */

Test(bind_m1, root_scalars, .init = fx_setup) {
  FxTree *fx = &g_fx;
  fx_build(fx);
  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});

  int64_t i = 0;
  cr_assert(hx_run_json(&d, "-42", 3, fx->t_i64, &i, 0) == 0);
  cr_assert(i == -42);

  double f = 0;
  cr_assert(hx_run_json(&d, "2.5", 3, fx->t_f64, &f, 0) == 0);
  cr_assert(f == 2.5);

  uint8_t b = 9;
  cr_assert(hx_run_json(&d, "false", 5, fx->t_bool, &b, 0) == 0);
  cr_assert(b == 0);

  HxStr s = {0};
  cr_assert(hx_run_json(&d, "\"a\\nb\\u0041\"", 12, fx->t_string, &s, 0) == 0);
  cr_assert(hx_str_eq(&s, "a\nbA"));

  /* json.Number keeps the source token text. */
  HxStr num = {0};
  cr_assert(hx_run_json(&d, "12.50", 5, fx->t_number, &num, BIND_OPT_USE_NUMBER) == 0);
  cr_assert(hx_str_eq(&num, "12.50"));

  hx_destroy(&d);
}

Test(bind_m1, struct_fields, .init = fx_setup) {
  FxTree *fx = &g_fx;
  typedef struct {
    int64_t x; /* off 0 */
    int64_t y; /* off 8 */
    HxStr s;   /* off 16 */
    uint8_t b; /* off 32 */
    int32_t q; /* off 36, `,string` */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "x", fx->t_i64, offsetof(Dst, x), 0);
  fx_fld(fx, ts, "y", fx->t_i64, offsetof(Dst, y), 0);
  fx_fld(fx, ts, "s", fx->t_string, offsetof(Dst, s), 0);
  fx_fld(fx, ts, "b", fx->t_bool, offsetof(Dst, b), 0);
  fx_fld(fx, ts, "q", fx->t_i32, offsetof(Dst, q), BIND_FF_QUOTED);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst = {0};
  cr_assert(hx_run_json(&d, "{\"x\":1,\"y\":2,\"s\":\"hi\",\"b\":true,\"q\":\"123\",\"zz\":9}", 48, ts, &dst,
                        0) == 0);
  cr_assert(dst.x == 1);
  cr_assert(dst.y == 2);
  cr_assert(hx_str_eq(&dst.s, "hi"));
  cr_assert(dst.b == 1);
  cr_assert(dst.q == 123);
  cr_assert(d.err == 0);

  /* Unknown fields skip by default; DISALLOW_UNKNOWN rejects them. */
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"x\":1,\"nope\":2}", 16, ts, &dst, BIND_OPT_DISALLOW_UNKNOWN) == 1);
  cr_assert(d.err == BIND_ERR_UNKNOWN_FIELD);

  hx_destroy(&d);
}

Test(bind_m1, nested_slice_array_ptr, .init = fx_setup) {
  FxTree *fx = &g_fx;
  typedef struct {
    int64_t v; /* off 0 */
  } Elem;
  uint16_t t_elem = fx_struct(fx, sizeof(Elem));
  fx_fld(fx, t_elem, "v", fx->t_i64, 0, 0);
  fx_struct_done(fx, t_elem);

  uint16_t t_slice = fx_slice(fx, t_elem, sizeof(Elem));
  uint16_t t_arr = fx_array(fx, fx->t_i64, 8, 3);
  uint16_t t_iptr = fx_ptr(fx, fx->t_i64);

  typedef struct {
    HxSlice list; /* off 0 */
    int64_t fixed[3]; /* off 24 */
    int64_t *p1; /* off 48 */
    int64_t *p2; /* off 56 */
    int64_t *p3; /* off 64 */
    Elem nest; /* off 72 */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "list", t_slice, offsetof(Dst, list), 0);
  fx_fld(fx, ts, "fixed", t_arr, offsetof(Dst, fixed), 0);
  fx_fld(fx, ts, "p1", t_iptr, offsetof(Dst, p1), 0);
  fx_fld(fx, ts, "p2", t_iptr, offsetof(Dst, p2), 0);
  fx_fld(fx, ts, "p3", t_iptr, offsetof(Dst, p3), 0);
  fx_fld(fx, ts, "nest", t_elem, offsetof(Dst, nest), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  /* slot_batch 8 exercises both bump growth (<= 8) and the standalone path
   * (needCap 16 > 8) for a 20-element slice. */
  HxOpts opts = {0};
  opts.slot_batch = 8;
  opts.init_batch = 2;
  HxDriver d;
  hx_init(&d, fx, opts);

  Dst dst = {0};
  /* list elements are Elem structs: {"v":i}. */
  char json[2048];
  int pos = snprintf(json, sizeof(json), "{\"list\":[");
  for (int i = 0; i < 20; i++)
    pos += snprintf(json + pos, sizeof(json) - pos, "%s{\"v\":%d}", i ? "," : "", i);
  pos += snprintf(json + pos, sizeof(json) - pos,
                  "],\"fixed\":[7,8,9,10],\"p1\":11,\"p2\":12,\"p3\":13,\"nest\":{\"v\":13}}");
  int rc = hx_run_json(&d, json, (size_t)pos, ts, &dst, 0);
  cr_assert(rc == 0, "err=%d pos=%llu", d.err, (unsigned long long)d.err_pos);

  cr_assert(dst.list.len == 20);
  cr_assert(dst.list.cap >= 20);
  for (int i = 0; i < 20; i++) cr_assert(((Elem *)dst.list.data)[i].v == i);
  cr_assert(dst.fixed[0] == 7 && dst.fixed[1] == 8 && dst.fixed[2] == 9);
  cr_assert(dst.p1 != NULL && *dst.p1 == 11);
  cr_assert(dst.p2 != NULL && *dst.p2 == 12);
  cr_assert(dst.p3 != NULL && *dst.p3 == 13);
  cr_assert(dst.nest.v == 13);
  cr_assert(hx_yield_count(&d, BIND_YIELD_SLICE_GROW) >= 3);
  cr_assert(hx_yield_count(&d, BIND_YIELD_BLOCK_FULL) >= 1);

  hx_destroy(&d);
}

Test(bind_m1, map_basic_and_flush, .init = fx_setup) {
  FxTree *fx = &g_fx;
  typedef struct {
    int64_t v;
  } Elem;
  uint16_t t_elem = fx_struct(fx, sizeof(Elem));
  fx_fld(fx, t_elem, "v", fx->t_i64, 0, 0);
  fx_struct_done(fx, t_elem);

  uint16_t t_mi = fx_map(fx, fx->t_i64, 8, 0, -1);
  uint16_t t_ms = fx_map(fx, t_elem, sizeof(Elem), 0, -1);

  typedef struct {
    void *counts; /* map[string]int64 */
    void *objs;   /* map[string]Elem */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "counts", t_mi, offsetof(Dst, counts), 0);
  fx_fld(fx, ts, "objs", t_ms, offsetof(Dst, objs), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  /* One region holds 16 entries, so 20 keys force a FLUSH_MAP and compaction. */
  HxOpts opts = {0};
  HxDriver d;
  hx_init(&d, fx, opts);
  Dst dst = {0};

  char json[512];
  int pos = snprintf(json, sizeof(json), "{\"counts\":{");
  for (int i = 0; i < 20; i++)
    pos += snprintf(json + pos, sizeof(json) - pos, "%s\"k%d\":%d", i ? "," : "", i, i * 3);
  pos += snprintf(json + pos, sizeof(json) - pos, "},\"objs\":{\"e\":{\"v\":7}}}");
  cr_assert(hx_run_json(&d, json, (size_t)pos, ts, &dst, 0) == 0);

  HxMap *counts = (HxMap *)dst.counts;
  HxMap *objs = (HxMap *)dst.objs;
  cr_assert(counts != NULL && objs != NULL);
  cr_assert(hx_map_len(counts) == 20);
  for (int i = 0; i < 20; i++) {
    char key[8];
    snprintf(key, sizeof(key), "k%d", i);
    const int64_t *v = (const int64_t *)hx_map_get(counts, key);
    cr_assert(v != NULL && *v == i * 3);
  }
  cr_assert(hx_map_len(objs) == 1);
  const Elem *e = (const Elem *)hx_map_get(objs, "e");
  cr_assert(e != NULL && e->v == 7);
  cr_assert(hx_yield_count(&d, BIND_YIELD_FLUSH_MAP) >= 1);

  hx_destroy(&d);
}

Test(bind_m1, map_deferred_values, .init = fx_setup) {
  FxTree *fx = &g_fx;
  int vs = fx_slot(fx, 24); /* Value intermediate slots */
  uint16_t t_mv = fx_map(fx, fx->t_value, 24, 1, vs);

  typedef struct {
    void *vals;
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "vals", t_mv, 0, 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst = {0};
  const char *json = "{\"vals\":{\"a\":1,\"b\":{\"x\":2}}}";
  cr_assert(hx_run_json(&d, json, strlen(json), ts, &dst, 0) == 0);

  HxMap *vals = (HxMap *)dst.vals;
  cr_assert(hx_map_len(vals) == 2);
  const HxValue *a = (const HxValue *)hx_map_get(vals, "a");
  const HxValue *b = (const HxValue *)hx_map_get(vals, "b");
  cr_assert(a != NULL && b != NULL);
  cr_assert(a->doc != NULL);
  /* The staged Value binds its end relative to its base at install time. */
  cr_assert(a->base == 0 && a->mode == (int32_t)TAPE_VIEW_A);
  cr_assert((uint8_t)(d.doc.tape[a->base + a->tidx] >> 56) == (uint8_t)(TAPE_INT64 >> 56));
  cr_assert((uint8_t)(d.doc.tape[b->base + b->tidx] >> 56) == (uint8_t)(TAPE_START_OBJECT >> 56));

  hx_destroy(&d);
}

Test(bind_m1, any_kinds, .init = fx_setup) {
  FxTree *fx = &g_fx;
  typedef struct {
    HxEface f; /* off 0 */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "f", fx->t_any, 0, 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst;

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":5}", 7, ts, &dst, 0) == 0);
  cr_assert(dst.f.type == fx->any.float64_type);
  cr_assert(*(const double *)dst.f.data == 5.0);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":\"sx\"}", 10, ts, &dst, 0) == 0);
  cr_assert(dst.f.type == fx->any.string_type);
  cr_assert(hx_str_eq((const HxStr *)dst.f.data, "sx"));

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":true}", 10, ts, &dst, 0) == 0);
  cr_assert(dst.f.type == fx->any.bool_type);
  cr_assert(dst.f.data == (void *)&fx_static_true || *(const uint64_t *)dst.f.data == 1);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":null}", 10, ts, &dst, 0) == 0);
  cr_assert(dst.f.type == NULL && dst.f.data == NULL);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":[1,2]}", 11, ts, &dst, 0) == 0);
  cr_assert(dst.f.type == fx->any.slice_type);
  HxSlice *sl = (HxSlice *)dst.f.data;
  cr_assert(sl->len == 2);
  cr_assert(((HxEface *)sl->data)[0].type == fx->any.float64_type);
  cr_assert(*(const double *)((HxEface *)sl->data)[0].data == 1.0);
  cr_assert(*(const double *)((HxEface *)sl->data)[1].data == 2.0);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"f\":{\"a\":1}}", strlen("{\"f\":{\"a\":1}}"), ts, &dst, 0) == 0);
  cr_assert(dst.f.type == fx->any.map_type);
  HxMap *mp = (HxMap *)dst.f.data;
  cr_assert(hx_map_len(mp) == 1);
  const HxEface *av = (const HxEface *)hx_map_get(mp, "a");
  cr_assert(av != NULL && av->type == fx->any.float64_type);
  cr_assert(*(const double *)av->data == 1.0);

  hx_destroy(&d);
}

Test(bind_m1, value_root, .init = fx_setup) {
  FxTree *fx = &g_fx;
  fx_build(fx);
  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});

  HxValue v = {0};
  const char *json = "{\"a\":1,\"b\":\"s\",\"c\":[1,2],\"d\":{\"e\":true},\"f\":1.5}";
  cr_assert(hx_value_root(&d, json, strlen(json), &v) == 0);
  cr_assert(v.doc == (void *)&d.doc);
  cr_assert(v.base == 0 && v.tidx == 0);
  cr_assert(v.mode == (int32_t)TAPE_VIEW_A);

  HxEnt ents[8];
  int n = hx_walk_object(&d.doc, &v, ents, 8);
  cr_assert(n == 5);
  cr_assert(hx_value_count(&d.doc, &v) == 5);

  const HxEnt *a = hx_ent_find(ents, n, "a");
  cr_assert(a != NULL && a->vtag == (uint8_t)(TAPE_INT64 >> 56) && a->inum == 1);
  const HxEnt *b = hx_ent_find(ents, n, "b");
  cr_assert(b != NULL && hx_str_eq(&b->str, "s"));
  const HxEnt *c = hx_ent_find(ents, n, "c");
  cr_assert(c != NULL && c->vtag == (uint8_t)(TAPE_START_ARRAY >> 56));
  const HxEnt *dd = hx_ent_find(ents, n, "d");
  cr_assert(dd != NULL && dd->vtag == (uint8_t)(TAPE_START_OBJECT >> 56));
  const HxEnt *f = hx_ent_find(ents, n, "f");
  cr_assert(f != NULL && f->vtag == (uint8_t)(TAPE_DOUBLE >> 56) && f->num == 1.5);

  hx_destroy(&d);
}

Test(bind_m1, errors, .init = fx_setup) {
  FxTree *fx = &g_fx;
  typedef struct {
    int64_t x;
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "x", fx->t_i64, 0, 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst;

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"x\":", 5, ts, &dst, 0) == 1);
  cr_assert(d.err == BIND_ERR_EOF);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"x\":!}", 7, ts, &dst, 0) == 1);
  cr_assert(d.err == BIND_ERR_SYNTAX);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "1 2", 3, fx->t_i64, &dst, 0) == 1);
  cr_assert(d.err == BIND_ERR_TRAILING);

  /* Depth: 255 nested containers fit, 256 overflow. */
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"x\":\"s\",\"x\":2}", strlen("{\"x\":\"s\",\"x\":2}"), ts, &dst, 0) == 1);
  cr_assert(d.err == BIND_ERR_TYPE_MISMATCH);
  cr_assert(d.err_pos == 5);
  cr_assert(dst.x == 2);

  /* Depth: 255 nested containers fit, 256 overflow. A []any root lets each '['
   * nest as an any array. */
  char deep[600];
  int pos = 0;
  for (int i = 0; i < 300; i++) deep[pos++] = '[';
  for (int i = 0; i < 300; i++) deep[pos++] = ']';
  HxSlice dst_slice;
  memset(&dst_slice, 0, sizeof(dst_slice));
  cr_assert(hx_run_json(&d, deep, (size_t)pos, fx->t_slice_any, &dst_slice, 0) == 1);
  cr_assert(d.err == BIND_ERR_DEPTH);

  hx_destroy(&d);
}

Test(bind_m1, nulls_and_empty, .init = fx_setup) {
  FxTree *fx = &g_fx;
  uint16_t t_iptr = fx_ptr(fx, fx->t_i64);
  uint16_t t_slice = fx_slice(fx, fx->t_i64, 8);
  uint16_t t_mi = fx_map(fx, fx->t_i64, 8, 0, -1);

  typedef struct {
    int64_t *p; /* off 0 */
    HxSlice s;  /* off 8 */
    void *m;    /* off 32 */
    HxStr str;  /* off 40 */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "p", t_iptr, offsetof(Dst, p), 0);
  fx_fld(fx, ts, "s", t_slice, offsetof(Dst, s), 0);
  fx_fld(fx, ts, "m", t_mi, offsetof(Dst, m), 0);
  fx_fld(fx, ts, "str", fx->t_string, offsetof(Dst, str), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst;
  /* Null zeroes reference-like kinds only; scalar strings retain their value,
   * so a zero destination stays zero under null. */
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"p\":null,\"s\":null,\"m\":null,\"str\":null}",
                        strlen("{\"p\":null,\"s\":null,\"m\":null,\"str\":null}"), ts, &dst, 0) == 0);
  cr_assert(dst.p == NULL);
  cr_assert(dst.s.data == NULL && dst.s.len == 0);
  cr_assert(dst.m == NULL);
  cr_assert(dst.str.p == NULL && dst.str.len == 0);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"p\":null,\"s\":[],\"m\":{}}",
                        strlen("{\"p\":null,\"s\":[],\"m\":{}}"), ts, &dst, 0) == 0);
  cr_assert(dst.p == NULL);
  cr_assert(dst.s.data != NULL && dst.s.len == 0 && dst.s.cap == 0);
  cr_assert(dst.m != NULL && hx_map_len((HxMap *)dst.m) == 0);

  hx_destroy(&d);
}

Test(bind_m1, deferred_records, .init = fx_setup) {
  FxTree *fx = &g_fx;
  uint16_t t_raw = fx_deferred(fx, BIND_KIND_RAW_MESSAGE, 24);
  typedef struct {
    HxSlice a; /* off 0, RawMessage keeps a slice header */
    HxSlice b; /* off 24 */
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "a", t_raw, offsetof(Dst, a), 0);
  fx_fld(fx, ts, "b", t_raw, offsetof(Dst, b), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d;
  hx_init(&d, fx, (HxOpts){0});
  Dst dst;
  memset(&dst, 0, sizeof(dst));
  const char *json = "{\"a\":{\"x\":1 ,\"y\":[2,3]},\"b\":\"z\"}";
  cr_assert(hx_run_json(&d, json, strlen(json), ts, &dst, 0) == 0);
  cr_assert(d.n_records == 2);

  const HxRecord *ra = &d.records[0];
  const HxRecord *rb = &d.records[1];
  cr_assert(ra->kind == BIND_KIND_RAW_MESSAGE);
  cr_assert(ra->blen == 18);
  cr_assert(memcmp(ra->bytes, "{\"x\":1 ,\"y\":[2,3]}", 18) == 0);
  cr_assert(ra->target == (void *)&dst.a);
  cr_assert(rb->kind == BIND_KIND_RAW_MESSAGE && rb->blen == 3);
  cr_assert(memcmp(rb->bytes, "\"z\"", 3) == 0);

  hx_destroy(&d);
}

Test(bind_m1, tape_arena_yield, .init = fx_setup) {
  FxTree *fx = &g_fx;
  fx_build(fx);

  const char *json = "{\"a\":1,\"b\":\"s\",\"c\":[1,2,3]}";

  /* A tiny guess forces BIND_YIELD_TAPE_ARENA; the result must equal the
   * presized run exactly. */
  HxOpts forced = {0};
  forced.tape_guess = 4;
  HxDriver d1;
  hx_init(&d1, fx, forced);
  HxValue v1 = {0};
  cr_assert(hx_value_root(&d1, json, strlen(json), &v1) == 0);
  cr_assert(hx_yield_count(&d1, BIND_YIELD_TAPE_ARENA) == 1);

  HxDriver d2;
  hx_init(&d2, fx, (HxOpts){0});
  HxValue v2 = {0};
  cr_assert(hx_value_root(&d2, json, strlen(json), &v2) == 0);
  cr_assert(hx_yield_count(&d2, BIND_YIELD_TAPE_ARENA) == 0);

  cr_assert(v1.tidx == v2.tidx && v1.end == v2.end && v1.base == v2.base && v1.mode == v2.mode);
  cr_assert(d1.m->b.alloc.tape_used == d2.m->b.alloc.tape_used);
  cr_assert(memcmp(d1.doc.tape, d2.doc.tape, d1.m->b.alloc.tape_used * 8) == 0);

  hx_destroy(&d1);
  hx_destroy(&d2);
}

/* ---- M2: merged-tape phase 2 / dual-view ---- */

/* Inline variant + reserve-unknown host. CaseA {x int32; label string};
 * CaseB {y float64}. The host carries a string discriminator, a plain field,
 * an eface carrier, and a reserve-unknown Value. */
typedef struct {
  int32_t x;  /* off 0 */
  HxStr label; /* off 8 */
} M2CaseA; /* size 24 */
typedef struct {
  double y; /* off 0 */
} M2CaseB; /* size 8 */
typedef struct {
  HxStr type; /* off 0, discriminator */
  int32_t n; /* off 16 */
  HxEface payload; /* off 24, inline carrier */
  HxValue extra; /* off 40, reserve-unknown Value */
} M2Host; /* size 64 */

typedef struct {
  uint16_t host, case_a, case_b;
} M2DualView;

static M2DualView fx_build_dual_view(FxTree *fx) {
  fx_init(fx);
  uint16_t ca = fx_struct(fx, sizeof(M2CaseA));
  fx_fld(fx, ca, "x", fx->t_i32, offsetof(M2CaseA, x), 0);
  fx_fld(fx, ca, "label", fx->t_string, offsetof(M2CaseA, label), 0);
  fx_struct_done(fx, ca);
  uint16_t cb = fx_struct(fx, sizeof(M2CaseB));
  fx_fld(fx, cb, "y", fx->t_f64, offsetof(M2CaseB, y), 0);
  fx_struct_done(fx, cb);
  int sa = fx_slot(fx, sizeof(M2CaseA));
  int sb = fx_slot(fx, sizeof(M2CaseB));

  uint16_t host = fx_struct(fx, sizeof(M2Host));
  fx_fld(fx, host, "type", fx->t_string, offsetof(M2Host, type), 0);
  fx_fld(fx, host, "n", fx->t_i32, offsetof(M2Host, n), 0);
  fx_fld(fx, host, "payload", fx->t_any, offsetof(M2Host, payload), 0);
  fx_fld(fx, host, "extra", fx->t_value, offsetof(M2Host, extra), 0);
  fx_struct_done(fx, host);

  FxCase cases[2] = {
      {"circle", ca, sa},
      {"box", cb, sb},
  };
  fx_variant(fx, host, 0, 2, cases, 2, -1, 1);
  fx_reserve_unknown(fx, host, 3);
  fx_build(fx);
  M2DualView dv = {host, ca, cb};
  return dv;
}

Test(bind_m2, dual_view_basic) {
  static FxTree fx;
  M2DualView dv = fx_build_dual_view(&fx);
  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  M2Host dst;
  memset(&dst, 0, sizeof(dst));

  const char *json = "{\"label\":\"L\",\"type\":\"circle\",\"x\":7,\"zzz\":[1,2],\"n\":9}";
  cr_assert(hx_run_json(&d, json, strlen(json), dv.host, &dst, 0) == 0);

  /* Discriminator and plain host field bound at phase 2. */
  cr_assert(hx_str_eq(&dst.type, "circle"));
  cr_assert(dst.n == 9);

  /* Inline case bound through the carrier eface into case storage. */
  cr_assert(dst.payload.type == fx_case_rtype(dv.case_a));
  M2CaseA *a = (M2CaseA *)dst.payload.data;
  cr_assert(a != NULL && a->x == 7 && hx_str_eq(&a->label, "L"));

  /* Reserve-unknown Value reads view B: exactly the unknowns survive. */
  cr_assert(dst.extra.doc != NULL);
  cr_assert(dst.extra.mode == (int32_t)TAPE_MODE_RESERVE_DUAL_ROOT);
  cr_assert(hx_value_count(&d.doc, &dst.extra) == 1);
  HxEnt ents[8];
  int n = hx_walk_object(&d.doc, &dst.extra, ents, 8);
  cr_assert(n == 1);
  cr_assert(hx_ent_find(ents, n, "zzz") != NULL);
  cr_assert(hx_ent_find(ents, n, "x") == NULL);
  cr_assert(hx_ent_find(ents, n, "label") == NULL);
  cr_assert(hx_ent_find(ents, n, "type") == NULL);
  cr_assert(hx_ent_find(ents, n, "n") == NULL);

  hx_destroy(&d);
}

/* A case with its own reserve-unknown sink and a host WITHOUT one: leftovers
 * route to the case sink (view A), because the host reserve branch is skipped
 * when the host has no sink. */
Test(bind_m2, variant_case_sink) {
  static FxTree fx;
  fx_init(&fx);
  /* CaseA { x int32; rest Value(reserve) } */
  typedef struct {
    int32_t x; /* off 0 */
    HxValue rest; /* off 8 */
  } M2SinkCase;
  uint16_t ca = fx_struct(&fx, sizeof(M2SinkCase));
  fx_fld(&fx, ca, "x", fx.t_i32, offsetof(M2SinkCase, x), 0);
  fx_fld(&fx, ca, "rest", fx.t_value, offsetof(M2SinkCase, rest), 0);
  fx_struct_done(&fx, ca);
  fx_reserve_unknown(&fx, ca, 1);
  uint16_t cb = fx_struct(&fx, sizeof(M2CaseB));
  fx_fld(&fx, cb, "y", fx.t_f64, 0, 0);
  fx_struct_done(&fx, cb);
  int sa = fx_slot(&fx, sizeof(M2SinkCase));
  int sb = fx_slot(&fx, sizeof(M2CaseB));

  /* Host has a discriminator and a carrier but no reserve-unknown sink. */
  typedef struct {
    HxStr type; /* off 0 */
    HxEface payload; /* off 16 */
  } M2VHost;
  uint16_t host = fx_struct(&fx, sizeof(M2VHost));
  fx_fld(&fx, host, "type", fx.t_string, 0, 0);
  fx_fld(&fx, host, "payload", fx.t_any, offsetof(M2VHost, payload), 0);
  fx_struct_done(&fx, host);
  FxCase cases[2] = {{"circle", ca, sa}, {"box", cb, sb}};
  fx_variant(&fx, host, 0, 1, cases, 2, -1, 1);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  M2VHost dst;
  memset(&dst, 0, sizeof(dst));
  /* "x" is a case field; "left" is a case leftover (case sink); "hosty" too,
   * because the host has no sink of its own. */
  const char *json = "{\"type\":\"circle\",\"x\":1,\"left\":2,\"hosty\":3}";
  cr_assert(hx_run_json(&d, json, strlen(json), host, &dst, 0) == 0);

  cr_assert(hx_str_eq(&dst.type, "circle"));
  M2SinkCase *c = (M2SinkCase *)dst.payload.data;
  cr_assert(c != NULL && c->x == 1);
  cr_assert(c->rest.doc != NULL);
  HxEnt case_ents[4];
  int cn = hx_walk_object(&d.doc, &c->rest, case_ents, 4);
  cr_assert(cn == 2);
  cr_assert(hx_ent_find(case_ents, cn, "left") != NULL);
  cr_assert(hx_ent_find(case_ents, cn, "hosty") != NULL);
  cr_assert(hx_ent_find(case_ents, cn, "x") == NULL);
  hx_destroy(&d);
}

/* reserve-unknown alone (no inline variant) publishes a single view-A Value. */
Test(bind_m2, reserve_only) {
  static FxTree fx;
  fx_init(&fx);
  typedef struct {
    int32_t n; /* off 0 */
    HxValue rest; /* off 8 */
  } RHost;
  uint16_t host = fx_struct(&fx, sizeof(RHost));
  fx_fld(&fx, host, "n", fx.t_i32, offsetof(RHost, n), 0);
  fx_fld(&fx, host, "rest", fx.t_value, offsetof(RHost, rest), 0);
  fx_struct_done(&fx, host);
  fx_reserve_unknown(&fx, host, 1);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  RHost dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"n\":1,\"a\":2,\"b\":3}", strlen("{\"n\":1,\"a\":2,\"b\":3}"), host,
                         &dst, 0) == 0);
  cr_assert(dst.n == 1);
  cr_assert(dst.rest.mode == (int32_t)TAPE_VIEW_A);
  cr_assert(hx_value_count(&d.doc, &dst.rest) == 2);
  HxEnt ents[4];
  int n = hx_walk_object(&d.doc, &dst.rest, ents, 4);
  cr_assert(n == 2);
  cr_assert(hx_ent_find(ents, n, "a") != NULL && hx_ent_find(ents, n, "b") != NULL);
  hx_destroy(&d);
}

/* inline variant alone (no reserve-unknown) binds the case without a dual view. */
Test(bind_m2, variant_only) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ca = fx_struct(&fx, sizeof(M2CaseA));
  fx_fld(&fx, ca, "x", fx.t_i32, offsetof(M2CaseA, x), 0);
  fx_fld(&fx, ca, "label", fx.t_string, offsetof(M2CaseA, label), 0);
  fx_struct_done(&fx, ca);
  int sa = fx_slot(&fx, sizeof(M2CaseA));
  typedef struct {
    HxStr type; /* off 0 */
    HxEface payload; /* off 16 */
  } VHost;
  uint16_t host = fx_struct(&fx, sizeof(VHost));
  fx_fld(&fx, host, "type", fx.t_string, 0, 0);
  fx_fld(&fx, host, "payload", fx.t_any, offsetof(VHost, payload), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"circle", ca, sa}};
  fx_variant(&fx, host, 0, 1, cases, 1, -1, 1);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  VHost dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"type\":\"circle\",\"x\":5,\"label\":\"L\"}",
                        strlen("{\"type\":\"circle\",\"x\":5,\"label\":\"L\"}"), host, &dst, 0) == 0);
  cr_assert(hx_str_eq(&dst.type, "circle"));
  cr_assert(dst.payload.type == fx_case_rtype(ca));
  M2CaseA *a = (M2CaseA *)dst.payload.data;
  cr_assert(a != NULL && a->x == 5 && hx_str_eq(&a->label, "L"));
  hx_destroy(&d);
}

/* An absent discriminator selects no case (nil eface) but the reserve-unknown
 * still collects every leftover. */
Test(bind_m2, missing_disc) {
  static FxTree fx;
  M2DualView dv = fx_build_dual_view(&fx);
  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  M2Host dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"x\":7,\"zzz\":1}", strlen("{\"x\":7,\"zzz\":1}"), dv.host, &dst, 0) == 0);
  cr_assert(dst.payload.type == NULL && dst.payload.data == NULL);
  cr_assert(hx_value_count(&d.doc, &dst.extra) == 2);
  HxEnt ents[4];
  int n = hx_walk_object(&d.doc, &dst.extra, ents, 4);
  cr_assert(n == 2);
  cr_assert(hx_ent_find(ents, n, "x") != NULL && hx_ent_find(ents, n, "zzz") != NULL);
  hx_destroy(&d);
}

/* A discriminator that names no case fails unless a default is declared. */
Test(bind_m2, unknown_disc) {
  static FxTree fx;
  M2DualView dv = fx_build_dual_view(&fx); /* no default case */
  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  M2Host dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"type\":\"zzz\",\"x\":7}", strlen("{\"type\":\"zzz\",\"x\":7}"), dv.host, &dst,
                        0) == 1);
  cr_assert(d.err == BIND_ERR_VARIANT_UNKNOWN_DISC);
  hx_destroy(&d);
}

/* The same unknown discriminator selects the declared default case. */
Test(bind_m2, variant_default) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ca = fx_struct(&fx, sizeof(M2CaseA));
  fx_fld(&fx, ca, "x", fx.t_i32, offsetof(M2CaseA, x), 0);
  fx_fld(&fx, ca, "label", fx.t_string, offsetof(M2CaseA, label), 0);
  fx_struct_done(&fx, ca);
  int sa = fx_slot(&fx, sizeof(M2CaseA));
  typedef struct {
    HxStr type; /* off 0 */
    HxEface payload; /* off 16 */
  } VHost;
  uint16_t host = fx_struct(&fx, sizeof(VHost));
  fx_fld(&fx, host, "type", fx.t_string, 0, 0);
  fx_fld(&fx, host, "payload", fx.t_any, offsetof(VHost, payload), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"circle", ca, sa}};
  fx_variant(&fx, host, 0, 1, cases, 1, 0 /* default = case 0 */, 1);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  VHost dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"type\":\"zzz\",\"x\":3}", strlen("{\"type\":\"zzz\",\"x\":3}"), host, &dst,
                        0) == 0);
  cr_assert(dst.payload.type == fx_case_rtype(ca));
  M2CaseA *a = (M2CaseA *)dst.payload.data;
  cr_assert(a != NULL && a->x == 3);
  hx_destroy(&d);
}

/* Kindof selects a case by JSON kind; an unregistered kind is an error. */
Test(bind_m2, kindof_field) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ca = fx_struct(&fx, sizeof(M2CaseA)); /* object case */
  fx_fld(&fx, ca, "x", fx.t_i32, 0, 0);
  fx_struct_done(&fx, ca);
  int sa = fx_slot(&fx, sizeof(M2CaseA));
  typedef struct {
    HxEface val; /* off 0 */
  } KHost;
  uint16_t host = fx_struct(&fx, sizeof(KHost));
  fx_fld(&fx, host, "val", fx.t_any, 0, 0);
  fx_struct_done(&fx, host);
  FxCase kinds[5] = {
      {NULL, 0xFFFF, -1},   /* bool: unregistered */
      {NULL, 0xFFFF, -1},   /* number: unregistered */
      {NULL, 0xFFFF, -1},   /* string: unregistered */
      {NULL, 0xFFFF, -1},   /* array: unregistered */
      {NULL, ca, sa},       /* object */
  };
  fx_kindof(&fx, host, 0, kinds);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  KHost dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"val\":{\"x\":8}}", strlen("{\"val\":{\"x\":8}}"), host, &dst, 0) == 0);
  cr_assert(dst.val.type == fx_case_rtype(ca));
  M2CaseA *a = (M2CaseA *)dst.val.data;
  cr_assert(a != NULL && a->x == 8);

  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"val\":5}", strlen("{\"val\":5}"), host, &dst, 0) == 1);
  cr_assert(d.err == BIND_ERR_KINDOF_UNREGISTERED);
  hx_destroy(&d);
}

/* A non-inline variant field binds its case from a sibling discriminator. */
Test(bind_m2, variant_field_immediate) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ca = fx_struct(&fx, sizeof(M2CaseA));
  fx_fld(&fx, ca, "x", fx.t_i32, offsetof(M2CaseA, x), 0);
  fx_fld(&fx, ca, "label", fx.t_string, offsetof(M2CaseA, label), 0);
  fx_struct_done(&fx, ca);
  int sa = fx_slot(&fx, sizeof(M2CaseA));
  typedef struct {
    HxStr kind; /* off 0, discriminator */
    HxEface shape; /* off 16, variant field */
  } NHost;
  uint16_t host = fx_struct(&fx, sizeof(NHost));
  fx_fld(&fx, host, "kind", fx.t_string, 0, 0);
  fx_fld(&fx, host, "shape", fx.t_any, offsetof(NHost, shape), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"circle", ca, sa}};
  fx_variant(&fx, host, 0, 1, cases, 1, -1, 0 /* non-inline */);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  NHost dst;
  memset(&dst, 0, sizeof(dst));
  /* Discriminator precedes the variant field, so it binds immediately. */
  const char *json = "{\"kind\":\"circle\",\"shape\":{\"x\":4,\"label\":\"L\"}}";
  cr_assert(hx_run_json(&d, json, strlen(json), host, &dst, 0) == 0);
  cr_assert(hx_str_eq(&dst.kind, "circle"));
  cr_assert(dst.shape.type == fx_case_rtype(ca));
  M2CaseA *a = (M2CaseA *)dst.shape.data;
  cr_assert(a != NULL && a->x == 4 && hx_str_eq(&a->label, "L"));
  hx_destroy(&d);
}

/* When the variant field precedes its discriminator, binding defers to phase 2
 * and the case descends over the merged-tape range. */
Test(bind_m2, variant_field_deferred) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ca = fx_struct(&fx, sizeof(M2CaseA));
  fx_fld(&fx, ca, "x", fx.t_i32, offsetof(M2CaseA, x), 0);
  fx_fld(&fx, ca, "label", fx.t_string, offsetof(M2CaseA, label), 0);
  fx_struct_done(&fx, ca);
  int sa = fx_slot(&fx, sizeof(M2CaseA));
  typedef struct {
    HxEface shape; /* off 0, variant field (comes first in JSON) */
    HxStr kind; /* off 16, discriminator */
  } NHost2;
  uint16_t host = fx_struct(&fx, sizeof(NHost2));
  fx_fld(&fx, host, "shape", fx.t_any, offsetof(NHost2, shape), 0);
  fx_fld(&fx, host, "kind", fx.t_string, offsetof(NHost2, kind), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"circle", ca, sa}};
  fx_variant(&fx, host, 1 /* disc */, 0 /* carrier */, cases, 1, -1, 0);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  NHost2 dst;
  memset(&dst, 0, sizeof(dst));
  const char *json = "{\"shape\":{\"x\":4,\"label\":\"L\"},\"kind\":\"circle\"}";
  cr_assert(hx_run_json(&d, json, strlen(json), host, &dst, 0) == 0);
  cr_assert(hx_str_eq(&dst.kind, "circle"));
  cr_assert(dst.shape.type == fx_case_rtype(ca));
  M2CaseA *a = (M2CaseA *)dst.shape.data;
  cr_assert(a != NULL && a->x == 4 && hx_str_eq(&a->label, "L"));
  hx_destroy(&d);
}

/* A cold pointer case defers to phase 2 and descends the merged-tape range
 * through the tape labels and rebind stack, allocating the pointee. */
Test(bind_m2, cold_ptr_case) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t t_iptr = fx_ptr(&fx, fx.t_i64); /* COLD */
  typedef struct {
    HxStr kind; /* off 0, discriminator */
    HxEface shape; /* off 16, variant field */
  } NHost;
  uint16_t host = fx_struct(&fx, sizeof(NHost));
  fx_fld(&fx, host, "kind", fx.t_string, 0, 0);
  fx_fld(&fx, host, "shape", fx.t_any, offsetof(NHost, shape), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"num", t_iptr, -1}};
  fx_variant(&fx, host, 0, 1, cases, 1, -1, 0);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  NHost dst;
  memset(&dst, 0, sizeof(dst));
  const char *json = "{\"kind\":\"num\",\"shape\":5}";
  cr_assert(hx_run_json(&d, json, strlen(json), host, &dst, 0) == 0);
  cr_assert(hx_str_eq(&dst.kind, "num"));
  cr_assert(dst.shape.type == fx_case_rtype(t_iptr));
  cr_assert(dst.shape.data != NULL && *(const int64_t *)dst.shape.data == 5);
  hx_destroy(&d);
}

/* An empty-struct case binds through a zero-size slot class whose saturated
 * limit keeps the stationary cursor from livelocking. */
Test(bind_m2, empty_struct_case) {
  static FxTree fx;
  fx_init(&fx);
  uint16_t ce = fx_struct(&fx, 0); /* zero-size, fieldless */
  fx_struct_done(&fx, ce);
  int se = fx_slot(&fx, 0);
  typedef struct {
    HxStr type; /* off 0 */
    HxEface payload; /* off 16 */
  } VHost;
  uint16_t host = fx_struct(&fx, sizeof(VHost));
  fx_fld(&fx, host, "type", fx.t_string, 0, 0);
  fx_fld(&fx, host, "payload", fx.t_any, offsetof(VHost, payload), 0);
  fx_struct_done(&fx, host);
  FxCase cases[1] = {{"empty", ce, se}};
  fx_variant(&fx, host, 0, 1, cases, 1, -1, 1);
  fx_build(&fx);

  HxDriver d;
  hx_init(&d, &fx, (HxOpts){0});
  VHost dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_json(&d, "{\"type\":\"empty\"}", strlen("{\"type\":\"empty\"}"), host, &dst, 0) == 0);
  cr_assert(hx_str_eq(&dst.type, "empty"));
  cr_assert(dst.payload.type == fx_case_rtype(ce));
  cr_assert(dst.payload.data != NULL); /* zero-size slot still yields a stable address */
  hx_destroy(&d);
}

/* Forcing every SlotClass tiny exhausts the case slot mid-phase-2 and must
 * resume idempotently: the tape, string arena, and each host's results are
 * byte-identical to an unforced run. */
Test(bind_m2, forced_yield_reentry) {
  static FxTree fx;
  M2DualView dv = fx_build_dual_view(&fx);
  uint16_t t_hosts = fx_slice(&fx, dv.host, sizeof(M2Host));

  const char *json =
      "[{\"type\":\"circle\",\"x\":1,\"a\":1},{\"type\":\"box\",\"y\":2,\"b\":2},"
      "{\"type\":\"circle\",\"x\":3,\"c\":3},{\"type\":\"box\",\"y\":4,\"d\":4},"
      "{\"type\":\"circle\",\"x\":5,\"e\":5},{\"type\":\"box\",\"y\":6,\"f\":6}]";

  HxDriver d1, d2;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){.init_batch = 1, .slot_batch = 2});

  HxSlice s1, s2;
  memset(&s1, 0, sizeof(s1));
  memset(&s2, 0, sizeof(s2));
  cr_assert(hx_run_json(&d1, json, strlen(json), t_hosts, &s1, 0) == 0);
  cr_assert(hx_run_json(&d2, json, strlen(json), t_hosts, &s2, 0) == 0);

  /* The tape and string arena contents are identical across the two runs. */
  cr_assert(d1.m->b.alloc.tape_used == d2.m->b.alloc.tape_used);
  cr_assert(memcmp(d1.doc.tape, d2.doc.tape, d1.m->b.alloc.tape_used * 8) == 0);
  cr_assert(d1.m->c.str_used == d2.m->c.str_used);
  cr_assert(memcmp(d1.doc.str_arena, d2.doc.str_arena, d1.m->c.str_used) == 0);

  /* The forced run actually yielded BLOCK_FULL inside phase 2. */
  cr_assert(hx_yield_phase_seen(&d2, BIND_YIELD_BLOCK_FULL, BIND_PHASE_TAPE_BIND_CLOSE_DRAIN_RETRY) ||
            hx_yield_phase_seen(&d2, BIND_YIELD_BLOCK_FULL, BIND_PHASE_PHASE2_POLY_RETRY) ||
            hx_yield_phase_seen(&d2, BIND_YIELD_BLOCK_FULL, BIND_PHASE_VARIANT_INLINE_RESUME));

  /* Each host binds identically: scalar field, case content, reserve keys. */
  cr_assert(s1.len == 6 && s2.len == 6);
  M2Host *h1 = (M2Host *)s1.data;
  M2Host *h2 = (M2Host *)s2.data;
  for (int i = 0; i < 6; i++) {
    cr_assert(h1[i].n == h2[i].n);
    cr_assert(h1[i].payload.type == h2[i].payload.type);
    if (h1[i].payload.type == fx_case_rtype(dv.case_a)) {
      M2CaseA *a1 = (M2CaseA *)h1[i].payload.data;
      M2CaseA *a2 = (M2CaseA *)h2[i].payload.data;
      cr_assert(a1->x == a2->x);
    } else {
      M2CaseB *b1 = (M2CaseB *)h1[i].payload.data;
      M2CaseB *b2 = (M2CaseB *)h2[i].payload.data;
      cr_assert(b1->y == b2->y);
    }
    cr_assert(hx_value_count(&d1.doc, &h1[i].extra) == hx_value_count(&d2.doc, &h2[i].extra));
  }

  hx_destroy(&d1);
  hx_destroy(&d2);
}

/* ---- M3: tape-bind root (UnmarshalValue path) ---- */

/* A Value built by a JSON parse binds back into scalar roots. */
Test(bind_m3, tape_scalar_roots) {
  static FxTree fx;
  fx_init(&fx);
  fx_build(&fx);

  HxDriver d1, d2;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){0});

  HxValue v;
  int64_t i = 0;
  cr_assert(hx_value_root(&d1, "5", 1, &v) == 0);
  cr_assert(hx_run_tape(&d2, &d1, &v, fx.t_i64, &i) == 0);
  cr_assert(i == 5);

  hx_destroy(&d1);
  hx_destroy(&d2);
}

/* Struct parity: the same JSON bound through a Value then a tape walk matches
 * a direct JSON parse field for field. */
Test(bind_m3, tape_struct_parity) {
  static FxTree fx;
  fx_init(&fx);
  typedef struct {
    int64_t x; /* off 0 */
    HxStr s;   /* off 8 */
    HxSlice list; /* off 24 */
  } SDst;
  uint16_t t_elem = fx_struct(&fx, 8);
  fx_fld(&fx, t_elem, "v", fx.t_i64, 0, 0);
  fx_struct_done(&fx, t_elem);
  uint16_t t_slice = fx_slice(&fx, t_elem, 8);
  uint16_t ts = fx_struct(&fx, sizeof(SDst));
  fx_fld(&fx, ts, "x", fx.t_i64, offsetof(SDst, x), 0);
  fx_fld(&fx, ts, "s", fx.t_string, offsetof(SDst, s), 0);
  fx_fld(&fx, ts, "list", t_slice, offsetof(SDst, list), 0);
  fx_struct_done(&fx, ts);
  fx_build(&fx);

  const char *json = "{\"x\":7,\"s\":\"hi\",\"list\":[{\"v\":1},{\"v\":2},{\"v\":3}]}";

  HxDriver d1, d2, d3;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){0});
  hx_init(&d3, &fx, (HxOpts){0});

  HxValue v;
  cr_assert(hx_value_root(&d1, json, strlen(json), &v) == 0);
  SDst via_tape, via_json;
  memset(&via_tape, 0, sizeof(via_tape));
  memset(&via_json, 0, sizeof(via_json));
  cr_assert(hx_run_tape(&d2, &d1, &v, ts, &via_tape) == 0);
  cr_assert(hx_run_json(&d3, json, strlen(json), ts, &via_json, 0) == 0);

  cr_assert(via_tape.x == via_json.x);
  cr_assert(hx_str_eq(&via_tape.s, "hi"));
  cr_assert(hx_str_eq(&via_json.s, "hi"));
  cr_assert(via_tape.list.len == 3 && via_json.list.len == 3);
  cr_assert(((struct { int64_t v; } *)via_tape.list.data)[1].v == 2);
  cr_assert(((struct { int64_t v; } *)via_json.list.data)[1].v == 2);

  hx_destroy(&d1);
  hx_destroy(&d2);
  hx_destroy(&d3);
}

/* Dual-view parity on the tape path: t_route_field_to_variant + phase 2 over a
 * tape input matches the JSON path exactly. */
Test(bind_m3, tape_dual_view_parity) {
  static FxTree fx;
  M2DualView dv = fx_build_dual_view(&fx);
  const char *json = "{\"label\":\"L\",\"type\":\"circle\",\"x\":7,\"zzz\":[1,2],\"n\":9}";

  HxDriver d1, d2, d3;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){0});
  hx_init(&d3, &fx, (HxOpts){0});

  HxValue v;
  cr_assert(hx_value_root(&d1, json, strlen(json), &v) == 0);
  M2Host via_tape, via_json;
  memset(&via_tape, 0, sizeof(via_tape));
  memset(&via_json, 0, sizeof(via_json));
  cr_assert(hx_run_tape(&d2, &d1, &v, dv.host, &via_tape) == 0);
  cr_assert(hx_run_json(&d3, json, strlen(json), dv.host, &via_json, 0) == 0);

  cr_assert(hx_str_eq(&via_tape.type, "circle") && hx_str_eq(&via_json.type, "circle"));
  cr_assert(via_tape.n == via_json.n);
  cr_assert(via_tape.payload.type == via_json.payload.type);
  cr_assert(via_tape.payload.type == fx_case_rtype(dv.case_a));
  M2CaseA *a1 = (M2CaseA *)via_tape.payload.data;
  M2CaseA *a2 = (M2CaseA *)via_json.payload.data;
  cr_assert(a1->x == a2->x && hx_str_eq(&a1->label, "L"));

  cr_assert(via_tape.extra.mode == via_json.extra.mode);
  cr_assert(via_tape.extra.mode == (int32_t)TAPE_MODE_RESERVE_DUAL_ROOT);
  cr_assert(hx_value_count(&d2.doc, &via_tape.extra) == hx_value_count(&d3.doc, &via_json.extra));
  HxEnt te[8], je[8];
  int tn = hx_walk_object(&d2.doc, &via_tape.extra, te, 8);
  int jn = hx_walk_object(&d3.doc, &via_json.extra, je, 8);
  cr_assert(tn == jn && tn == 1);
  cr_assert(hx_ent_find(te, tn, "zzz") != NULL && hx_ent_find(je, jn, "zzz") != NULL);

  hx_destroy(&d1);
  hx_destroy(&d2);
  hx_destroy(&d3);
}

/* A plain Value field on the tape path yields TAPE_BIND_VALUE; the harness
 * aliases the source subtree into the descriptor. */
Test(bind_m3, value_field_alias) {
  static FxTree fx;
  fx_init(&fx);
  typedef struct {
    HxValue v; /* off 0 */
    int32_t n; /* off 24 */
  } VFDst;
  uint16_t ts = fx_struct(&fx, sizeof(VFDst));
  fx_fld(&fx, ts, "v", fx.t_value, offsetof(VFDst, v), 0);
  fx_fld(&fx, ts, "n", fx.t_i32, offsetof(VFDst, n), 0);
  fx_struct_done(&fx, ts);
  fx_build(&fx);

  HxDriver d1, d2;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){0});
  HxValue v;
  const char *json = "{\"v\":{\"a\":1,\"b\":[2,3]},\"n\":4}";
  cr_assert(hx_value_root(&d1, json, strlen(json), &v) == 0);
  VFDst dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_tape(&d2, &d1, &v, ts, &dst) == 0);
  cr_assert(dst.n == 4);
  cr_assert(hx_yield_count(&d2, BIND_YIELD_TAPE_BIND_VALUE) == 1);
  cr_assert(dst.v.doc != NULL);
  /* The aliased Value walks its subtree from the source tape. */
  HxEnt ents[4];
  int n = hx_walk_object((const HxDoc *)dst.v.doc, &dst.v, ents, 4);
  cr_assert(n == 2);
  cr_assert(hx_ent_find(ents, n, "a") != NULL && hx_ent_find(ents, n, "b") != NULL);

  hx_destroy(&d1);
  hx_destroy(&d2);
}

/* A json.Number field is unsupported on the tape path (no source text). */
Test(bind_m3, tape_unsupported_kind) {
  static FxTree fx;
  fx_init(&fx);
  typedef struct {
    HxStr num; /* json.Number, off 0 */
  } NDst;
  uint16_t ts = fx_struct(&fx, sizeof(NDst));
  fx_fld(&fx, ts, "num", fx.t_number, 0, 0);
  fx_struct_done(&fx, ts);
  fx_build(&fx);

  HxDriver d1, d2;
  hx_init(&d1, &fx, (HxOpts){0});
  hx_init(&d2, &fx, (HxOpts){0});
  HxValue v;
  cr_assert(hx_value_root(&d1, "{\"num\":5}", strlen("{\"num\":5}"), &v) == 0);
  NDst dst;
  memset(&dst, 0, sizeof(dst));
  cr_assert(hx_run_tape(&d2, &d1, &v, ts, &dst) == 1);
  cr_assert(d2.err == BIND_ERR_UNSUPPORTED_TAG);

  hx_destroy(&d1);
  hx_destroy(&d2);
}
