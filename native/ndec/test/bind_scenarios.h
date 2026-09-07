/* bind_scenarios.h names the concrete fixture scenarios the debug CLI exposes.
 * Each builder and the dump helper live here so the CLI stays a thin main(). */
#ifndef NDEC_BIND_SCENARIOS_H
#define NDEC_BIND_SCENARIOS_H

/* Shared dump of driver state after a run. */
static void hx_dump(HxDriver *d) {
  NdecBindMachine *m = d->m;
  fprintf(stderr, "== yields: %d ==\n", d->n_yields);
  for (int i = 0; i < d->n_yields; i++) {
    HxYieldInfo *y = &d->yields[i];
    fprintf(stderr, "  [%d] action=%u arg0=%u arg1=%u phase=%u target=%p\n", i, y->action, y->arg0, y->arg1,
            y->phase, y->target);
  }
  fprintf(stderr, "== err=%d pos=%u depth=%d aux_depth=%d rebind_top=%u ==\n", d->err, d->err_pos, m->c.depth,
          m->aux_depth, m->rebind_top);
  fprintf(stderr, "== str_used=%zu tape_used=%zu/%zu map_used=%u ==\n", m->c.str_used, m->b.alloc.tape_used,
          m->b.alloc.tape_arena_cap, m->b.alloc.map_buf_used);
  if (d->err == 0 && d->doc.tape) {
    fprintf(stderr, "== tape (%zu words) ==\n", m->b.alloc.tape_used);
    for (size_t i = 0; i < m->b.alloc.tape_used; i++) {
      uint64_t w = d->doc.tape[i];
      fprintf(stderr, "  %04zu %c %016llx\n", i, (w >> 56) & 0x80 ? '!' : (char)(w >> 56),
              (unsigned long long)w);
    }
  }
  for (int i = 0; i < d->n_records; i++) {
    HxRecord *r = &d->records[i];
    fprintf(stderr, "== record[%d] kind=%u span(%u)=\"%.*s\" ==\n", i, r->kind, r->blen,
            r->blen < 60 ? (int)r->blen : 60, r->bytes);
  }
}

/* ---- fixture: flat struct ---- */

static void fxdbg_struct(HxDriver *d, FxTree *fx, const char *json, size_t len) {
  fx_init(fx);
  typedef struct {
    int64_t x;
    int64_t y;
    HxStr s;
  } Dst;
  uint16_t ts = fx_struct(fx, sizeof(Dst));
  fx_fld(fx, ts, "x", fx->t_i64, offsetof(Dst, x), 0);
  fx_fld(fx, ts, "y", fx->t_i64, offsetof(Dst, y), 0);
  fx_fld(fx, ts, "s", fx->t_string, offsetof(Dst, s), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  hx_init(d, fx, (HxOpts){0});
  Dst dst;
  memset(&dst, 0, sizeof(dst));
  int rc = hx_run_json(d, json, len, ts, &dst, 0);
  fprintf(stderr, "rc=%d x=%lld y=%lld s=%.*s\n", rc, (long long)dst.x, (long long)dst.y,
          (int)dst.s.len, dst.s.p);
  hx_dump(d);
}

/* ---- fixture: root Value ---- */

static void fxdbg_value(HxDriver *d, FxTree *fx, const char *json, size_t len) {
  fx_init(fx);
  fx_build(fx);
  hx_init(d, fx, (HxOpts){0});
  HxValue v;
  memset(&v, 0, sizeof(v));
  int rc = hx_value_root(d, json, len, &v);
  fprintf(stderr, "rc=%d base=%d tidx=%d end=%d mode=%u\n", rc, v.base, v.tidx, v.end, v.mode);
  hx_dump(d);
  if (rc == 0) {
    HxEnt ents[64];
    int n = hx_walk_object(&d->doc, &v, ents, 64);
    fprintf(stderr, "== walk: %d entries ==\n", n);
    for (int i = 0; i < n; i++)
      fprintf(stderr, "  %s -> %s\n", ents[i].key, hx_tag_name(ents[i].vtag));
  }
}

/* ---- fixture: root Value with a forced TAPE_ARENA yield ---- */

static void fxdbg_value_ta(HxDriver *d, FxTree *fx, const char *json, size_t len) {
  fx_init(fx);
  fx_build(fx);

  HxOpts forced = {0};
  forced.tape_guess = 4;
  HxDriver d1;
  hx_init(&d1, fx, forced);
  HxValue v1;
  memset(&v1, 0, sizeof(v1));
  int r1 = hx_value_root(&d1, json, len, &v1);
  fprintf(stderr, "d1 rc=%d base=%d tidx=%d end=%d mode=%u tape_used=%zu\n", r1, v1.base, v1.tidx, v1.end,
          v1.mode, d1.m->b.alloc.tape_used);

  HxDriver d2;
  hx_init(&d2, fx, (HxOpts){0});
  HxValue v2;
  memset(&v2, 0, sizeof(v2));
  int r2 = hx_value_root(&d2, json, len, &v2);
  fprintf(stderr, "d2 rc=%d base=%d tidx=%d end=%d mode=%u tape_used=%zu\n", r2, v2.base, v2.tidx, v2.end,
          v2.mode, d2.m->b.alloc.tape_used);
  fprintf(stderr, "memcmp=%d\n", memcmp(d1.doc.tape, d2.doc.tape, d1.m->b.alloc.tape_used * 8));
  hx_dump(&d1);
  (void)d;
  hx_destroy(&d1);
  hx_destroy(&d2);
}

/* ---- fixture: Value field alias on the tape path ---- */

static void fxdbg_vfield(HxDriver *d, FxTree *fx, const char *json, size_t len) {
  fx_init(fx);
  typedef struct {
    HxValue v; /* off 0 */
    int32_t n; /* off 24 */
  } VFDst;
  uint16_t ts = fx_struct(fx, sizeof(VFDst));
  fx_fld(fx, ts, "v", fx->t_value, offsetof(VFDst, v), 0);
  fx_fld(fx, ts, "n", fx->t_i32, offsetof(VFDst, n), 0);
  fx_struct_done(fx, ts);
  fx_build(fx);

  HxDriver d1;
  hx_init(&d1, fx, (HxOpts){0});
  HxValue v;
  memset(&v, 0, sizeof(v));
  int r1 = hx_value_root(&d1, json, len, &v);
  fprintf(stderr, "stage1 rc=%d base=%d tidx=%d end=%d\n", r1, v.base, v.tidx, v.end);
  hx_dump(&d1);

  hx_init(d, fx, (HxOpts){0});
  VFDst dst;
  memset(&dst, 0, sizeof(dst));
  int r2 = hx_run_tape(d, &d1, &v, ts, &dst);
  fprintf(stderr, "stage2 rc=%d err=%d n=%d v.doc=%p\n", r2, d->err, dst.n, dst.v.doc);
  hx_dump(d);
  hx_destroy(&d1);
}

/* ---- fixture: inline variant + reserve-unknown (dual view) ---- */

typedef struct {
  int32_t x; /* off 0 */
  HxStr label; /* off 8 */
} FxCaseA;
typedef struct {
  double y; /* off 0 */
} FxCaseB;
typedef struct {
  HxStr type; /* off 0, discriminator */
  int32_t n; /* off 16 */
  HxEface payload; /* off 24, inline carrier */
  HxValue extra; /* off 40, reserve-unknown Value */
} FxDualHost;

static void fxdbg_dual_view(HxDriver *d, FxTree *fx, const char *json, size_t len) {
  fx_init(fx);
  uint16_t ca = fx_struct(fx, sizeof(FxCaseA));
  fx_fld(fx, ca, "x", fx->t_i32, offsetof(FxCaseA, x), 0);
  fx_fld(fx, ca, "label", fx->t_string, offsetof(FxCaseA, label), 0);
  fx_struct_done(fx, ca);
  uint16_t cb = fx_struct(fx, sizeof(FxCaseB));
  fx_fld(fx, cb, "y", fx->t_f64, 0, 0);
  fx_struct_done(fx, cb);
  int sa = fx_slot(fx, sizeof(FxCaseA));
  int sb = fx_slot(fx, sizeof(FxCaseB));

  uint16_t host = fx_struct(fx, sizeof(FxDualHost));
  fx_fld(fx, host, "type", fx->t_string, offsetof(FxDualHost, type), 0);
  fx_fld(fx, host, "n", fx->t_i32, offsetof(FxDualHost, n), 0);
  fx_fld(fx, host, "payload", fx->t_any, offsetof(FxDualHost, payload), 0);
  fx_fld(fx, host, "extra", fx->t_value, offsetof(FxDualHost, extra), 0);
  fx_struct_done(fx, host);
  FxCase cases[2] = {{"circle", ca, sa}, {"box", cb, sb}};
  fx_variant(fx, host, 0, 2, cases, 2, -1, 1);
  fx_reserve_unknown(fx, host, 3);
  fx_build(fx);

  hx_init(d, fx, (HxOpts){0});
  FxDualHost dst;
  memset(&dst, 0, sizeof(dst));
  int rc = hx_run_json(d, json, len, host, &dst, 0);
  fprintf(stderr, "rc=%d err=%d type=%.*s n=%d payload.type=%p\n", rc, d->err, (int)dst.type.len, dst.type.p,
          dst.n, dst.payload.type);
  if (rc == 0 && dst.payload.data) {
    fprintf(stderr, "  caseA.x=%d caseA.label=%.*s\n", ((FxCaseA *)dst.payload.data)->x,
            (int)((FxCaseA *)dst.payload.data)->label.len, ((FxCaseA *)dst.payload.data)->label.p);
  }
  hx_dump(d);
  if (rc == 0 && dst.extra.doc) {
    HxEnt ents[16];
    int n = hx_walk_object(&d->doc, &dst.extra, ents, 16);
    fprintf(stderr, "== reserve (view %u, count %u): %d entries ==\n", dst.extra.mode,
            hx_value_count(&d->doc, &dst.extra), n);
    for (int i = 0; i < n; i++) fprintf(stderr, "  %s -> %s\n", ents[i].key, hx_tag_name(ents[i].vtag));
  }
}

typedef struct HxDbgFixture {
  const char *name;
  void (*run)(HxDriver *, FxTree *, const char *, size_t);
} HxDbgFixture;

static const HxDbgFixture g_fixtures[] = {
    {"struct", fxdbg_struct},
    {"value", fxdbg_value},
    {"value_ta", fxdbg_value_ta},
    {"vfield", fxdbg_vfield},
    {"dual_view", fxdbg_dual_view},
};

#endif /* NDEC_BIND_SCENARIOS_H */
