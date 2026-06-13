/*
 * Frame model (no FR_ROOT): the root is represented by a single
 * FR_STRUCT frame pretending to be "inside a parent struct whose
 * pending field points to the root type". This collapses root handling
 * into the nested-struct path, so slot_for_value has only two
 * branches.
 */

#include <errno.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "bind.h"
#include "field_lookup.h"
#include "ndec/core/types.h"

struct NdecArenaChunk {
  NdecArenaChunk *next;
  size_t cap;
  size_t used;
  /* payload bytes follow inline */
};

#define NDEC_ARENA_DEFAULT_CHUNK (64 * 1024)
#define NDEC_ARENA_ALIGN         (sizeof(void *) * 2) /* 16 on 64-bit */

/* Sentinel returned from ndec_arena_alloc(a, 0). Non-NULL so callers
 * can distinguish "zero-byte request" from "allocation failed"; must
 * never be dereferenced. */
static char ndec_arena_empty_sentinel;

static inline size_t arena_align_up(size_t n, size_t a) {
  return (n + (a - 1)) & ~(a - 1);
}

static inline uint8_t *arena_chunk_data(NdecArenaChunk *c) {
  return (uint8_t *)(c + 1);
}

void ndec_arena_init(NdecArena *a) {
  a->head            = NULL;
  a->chunk_size_hint = NDEC_ARENA_DEFAULT_CHUNK;
}

void ndec_arena_destroy(NdecArena *a) {
  for (NdecArenaChunk *c = a->head, *next; c; c = next) {
    next = c->next;
    free(c);
  }
  a->head = NULL;
}

void ndec_arena_reset(NdecArena *a) {
  for (NdecArenaChunk *c = a->head; c; c = c->next)
    c->used = 0;
}

static NdecArenaChunk *arena_new_chunk(size_t cap) {
  NdecArenaChunk *c = (NdecArenaChunk *)malloc(sizeof(*c) + cap);
  if (!c)
    return NULL;
  c->next = NULL;
  c->cap  = cap;
  c->used = 0;
  return c;
}

void *ndec_arena_alloc(NdecArena *a, size_t n) {
  if (n == 0)
    return &ndec_arena_empty_sentinel;

  NdecArenaChunk *c = a->head;
  if (c) {
    size_t off = arena_align_up(c->used, NDEC_ARENA_ALIGN);
    if (off + n <= c->cap) {
      c->used = off + n;
      return arena_chunk_data(c) + off;
    }
  }

  size_t new_cap = a->chunk_size_hint;
  if (n > new_cap)
    new_cap = n;
  NdecArenaChunk *nc = arena_new_chunk(new_cap);
  if (!nc)
    return NULL;
  nc->next = a->head;
  a->head  = nc;
  nc->used = n;
  return arena_chunk_data(nc);
}

char *ndec_arena_memdup_z(NdecArena *a, const char *src, size_t len) {
  char *p = (char *)ndec_arena_alloc(a, len + 1);
  if (!p)
    return NULL;
  if (len)
    memcpy(p, src, len);
  p[len] = '\0';
  return p;
}

typedef enum {
  FR_STRUCT = 1, /* inside an object */
  FR_ARRAY,      /* inside an array */
} FrameKind;

/* Per-struct-frame UNION state. Allocated lazily from the arena only
 * when the struct's type contains a UNION field. NULL for the common
 * non-union case. */
typedef struct {
  const NdecField *union_field;   /* the parent's UNION field */
  const NdecUnionArm *active_arm; /* selected after discriminator arrives */
} UnionState;

typedef struct {
  void *obj;                  /* base address being filled */
  const NdecTypeInfo *type;   /* struct's reflection table */
  const NdecField *pending;   /* field awaiting a value, NULL between fields */

  /* Absolute offsets for the pending field. For ordinary fields these
   * equal pending->offset / pending->len_offset; for tagged-union arm
   * fields they are pre-resolved to the union-absolute address so
   * slot_for_value never needs to know about the arm indirection. */
  uint32_t pending_offset;
  uint32_t pending_len_offset;

  uint64_t seen_mask;         /* bit i set if fields[i] was matched in JSON */
  UnionState *u;              /* NULL for non-union structs */
} StructFrame;

typedef struct {
  /* Element staging. In dynamic mode (NDEC_KIND_ARRAY) elements grow
   * into `scratch` and end_array memcpy's them into an arena slab. In
   * fixed mode (NDEC_KIND_FIXED_ARRAY) `inline_buf` points directly at
   * the parent's T[N] storage so no copy is needed; `cap` is N. The
   * two modes are mutually exclusive: exactly one of scratch/inline_buf
   * is non-NULL. */
  void *scratch;
  void *inline_buf;
  size_t count;
  size_t cap;
  size_t elem_size;
  NdecKind elem_kind;
  const NdecTypeInfo *elem_type;

  /* Writeback target, resolved at push time. Used in dynamic mode
   * only; NULL/unused for fixed mode (the parent T[N] is already
   * addressed by inline_buf). Stored as absolute addresses so the
   * same code path serves regular fields and tagged-union arm fields,
   * whose offsets are union-absolute. */
  void **dst_items;
  size_t *dst_len;
} ArrayFrame;

typedef struct {
  FrameKind kind;
  union {
    StructFrame s;
    ArrayFrame  a;
  };
} NdecBindFrame;

typedef struct {
  NdecBindFrame stack[NDEC_MAX_DEPTH + 1];
  int depth; /* index of top frame */

  /* Root pseudo-field: stack[0].s.pending points at this, so the very
   * first reactor value enters slot_for_value's FR_STRUCT branch as
   * though it were a regular nested field. */
  NdecField root_field;

  NdecArena *arena;
  bool strict;

  /* Sticky error (first one wins). */
  int err_code;
  const char *err_message;
} NdecBindState;

/* A writable destination resolved by slot_for_value(). */
typedef struct {
  void *dst;
  NdecKind kind;
  const NdecTypeInfo *type; /* only for struct kinds */
} NdecBindSlot;

#define BS_TOP(bs) (&(bs)->stack[(bs)->depth])

static int bs_fail(NdecBindState *bs, int code, const char *msg) {
  if (bs->err_code == 0) {
    bs->err_code    = code;
    bs->err_message = msg;
  }
  return -2; /* negative reactor return => kernel abort */
}

/* Size in bytes of a value of the given kind when laid out as an
 * array element. Returns 0 for kinds that cannot be array elements:
 *   - STRING_LEN needs a paired length field (array elements have no
 *     way to express that pairing);
 *   - ARRAY (nested arrays) unsupported in MVP. */
static size_t kind_size(NdecKind k, const NdecTypeInfo *type) {
  switch (k) {
  case NDEC_KIND_BOOL:
    return sizeof(bool);
  case NDEC_KIND_INT8:
    return sizeof(int8_t);
  case NDEC_KIND_INT16:
    return sizeof(int16_t);
  case NDEC_KIND_INT32:
    return sizeof(int32_t);
  case NDEC_KIND_INT64:
    return sizeof(int64_t);
  case NDEC_KIND_UINT8:
    return sizeof(uint8_t);
  case NDEC_KIND_UINT16:
    return sizeof(uint16_t);
  case NDEC_KIND_UINT32:
    return sizeof(uint32_t);
  case NDEC_KIND_UINT64:
    return sizeof(uint64_t);
  case NDEC_KIND_FLOAT32:
    return sizeof(float);
  case NDEC_KIND_FLOAT64:
    return sizeof(double);
  case NDEC_KIND_STRING:
    return sizeof(char *);
  case NDEC_KIND_STRUCT:
    return type ? type->size : 0;
  case NDEC_KIND_STRUCT_PTR:
    return sizeof(void *);
  case NDEC_KIND_STRING_LEN: /* fall through */
  case NDEC_KIND_ARRAY:
  case NDEC_KIND_FIXED_ARRAY:
  case NDEC_KIND_FIXED_STRING:
  case NDEC_KIND_HEAP_ARRAY:
  case NDEC_KIND_UNION: /* not a value with a size — never an array elem */
    return 0;
  }
  return 0;
}

/* Resolve where the next incoming value should be written.
 * Returns 0 on success, -1 if the current frame has no available slot
 * (e.g. scalar arrived when we expected a field key, or a fixed-array
 * overflowed). The caller maps -1 to a generic TYPE_MISMATCH; for
 * fixed-array overflow we set a specific error here so bs_fail's
 * "first error wins" rule surfaces it instead.
 *
 * FR_STRUCT: dst = obj + pending_offset, kind = pending->kind. The
 *            absolute offset is pre-resolved by r_object_field so this
 *            path is identical for ordinary fields and arm fields.
 * FR_ARRAY:  dst = (inline_buf or scratch)[count], kind = elem_kind.
 *            Dynamic mode grows scratch; fixed mode rejects when
 *            count == cap (== N). count is incremented by bs_advance
 *            after the value has been successfully written. */
static int slot_for_value(NdecBindState *bs, NdecBindSlot *out) {
  NdecBindFrame *f = BS_TOP(bs);
  if (f->kind == FR_STRUCT) {
    if (!f->s.pending)
      return -1;
    out->dst  = (uint8_t *)f->s.obj + f->s.pending_offset;
    out->kind = f->s.pending->kind;
    out->type = f->s.pending->type;
    return 0;
  }
  /* FR_ARRAY */
  ArrayFrame *a = &f->a;
  if (a->inline_buf) {
    if (a->count >= a->cap) {
      bs_fail(bs, NDEC_ERR_BIND_FIXED_OVERFLOW, "fixed array overflow");
      return -1;
    }
    out->dst = (uint8_t *)a->inline_buf + a->count * a->elem_size;
  } else {
    if (a->count >= a->cap) {
      size_t new_cap = a->cap ? a->cap * 2 : 16;
      void *p        = realloc(a->scratch, new_cap * a->elem_size);
      if (!p)
        return -1;
      a->scratch = p;
      a->cap     = new_cap;
    }
    out->dst = (uint8_t *)a->scratch + a->count * a->elem_size;
  }
  out->kind = a->elem_kind;
  out->type = a->elem_type;
  memset(out->dst, 0, a->elem_size);
  return 0;
}

/* Advance the current frame one value forward. Called after a value
 * has been successfully written through slot_for_value's target. */
static void bs_advance(NdecBindState *bs) {
  NdecBindFrame *f = BS_TOP(bs);
  if (f->kind == FR_STRUCT) {
    f->s.pending = NULL;
  } else {
    f->a.count++;
  }
}

/* Push / pop frame helpers. Both set up a fresh frame or clean up its
 * transient resources (array scratch). */
static int bs_push_struct(NdecBindState *bs, void *obj, const NdecTypeInfo *type) {
  if (bs->depth + 1 >= (int)(sizeof(bs->stack) / sizeof(bs->stack[0]))) {
    return bs_fail(bs, NDEC_ERR_DEPTH, "stack overflow");
  }
  bs->depth++;
  NdecBindFrame *f = BS_TOP(bs);
  f->kind          = FR_STRUCT;
  f->s             = (StructFrame){.obj = obj, .type = type};

  /* Cache the (at most one) UNION field so r_object_field doesn't
   * have to scan for it on every key. The state struct itself is
   * arena-allocated only when there is a union to track. */
  if (type) {
    for (uint16_t i = 0; i < type->st.field_count; i++) {
      if (type->st.fields[i].kind == NDEC_KIND_UNION) {
        UnionState *u = (UnionState *)ndec_arena_alloc(bs->arena, sizeof(*u));
        if (!u)
          return bs_fail(bs, NDEC_ERR_BIND_OOM, "arena OOM");
        u->union_field = &type->st.fields[i];
        u->active_arm  = NULL;
        f->s.u         = u;
        break;
      }
    }
  }
  return 0;
}

/* Push an array frame.
 *   inline_buf == NULL  → dynamic slice mode (scratch grows, end_array
 *                          arena-copies into *dst_items / *dst_len)
 *   inline_buf != NULL  → in-place mode (write directly into inline_buf,
 *                          cap is the runtime ceiling). dst_len, when
 *                          non-NULL, receives the actual element count
 *                          at end_array (used by HEAP_ARRAY); fixed
 *                          arrays leave dst_len NULL since N is implicit
 *                          in the type. */
static int bs_push_array(NdecBindState *bs, const NdecField *field, void **dst_items, size_t *dst_len,
                         void *inline_buf, size_t cap) {
  if (bs->depth + 1 >= (int)(sizeof(bs->stack) / sizeof(bs->stack[0]))) {
    return bs_fail(bs, NDEC_ERR_DEPTH, "stack overflow");
  }
  size_t elem_size = kind_size(field->elem_kind, field->type);
  if (elem_size == 0) {
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unsupported array element kind");
  }
  bs->depth++;
  NdecBindFrame *f = BS_TOP(bs);
  f->kind          = FR_ARRAY;
  if (inline_buf) {
    f->a = (ArrayFrame){
        .inline_buf = inline_buf,
        .cap        = cap,
        .elem_size  = elem_size,
        .elem_kind  = field->elem_kind,
        .elem_type  = field->type,
        .dst_len    = dst_len, /* heap-array: write count back; fixed: NULL */
    };
  } else {
    /* Dynamic mode: scratch grows on demand, dst_* receives final items. */
    f->a = (ArrayFrame){
        .elem_kind = field->elem_kind,
        .elem_type = field->type,
        .elem_size = elem_size,
        .dst_items = dst_items,
        .dst_len   = dst_len,
    };
  }
  return 0;
}

static void bs_pop(NdecBindState *bs) {
  NdecBindFrame *f = BS_TOP(bs);
  if (f->kind == FR_ARRAY && f->a.scratch) {
    free(f->a.scratch);
    f->a.scratch = NULL;
  }
  if (bs->depth > 0)
    bs->depth--;
}

#define NDEC_BIND_NUM_BUF 64

static int write_int(NdecBindState *bs, void *dst, NdecKind kind, NdecRawStr raw) {
  char buf[NDEC_BIND_NUM_BUF];
  if (raw.len >= sizeof(buf))
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "number too long");
  memcpy(buf, raw.ptr, raw.len);
  buf[raw.len] = '\0';

  char *end   = NULL;
  errno       = 0;
  long long v = strtoll(buf, &end, 10);
  if (errno == ERANGE || end == buf)
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "number out of range");
  if (*end != '\0')
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected integer");

  switch (kind) {
  case NDEC_KIND_INT8:
    if (v < INT8_MIN || v > INT8_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "int8 overflow");
    *(int8_t *)dst = (int8_t)v;
    return 0;
  case NDEC_KIND_INT16:
    if (v < INT16_MIN || v > INT16_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "int16 overflow");
    *(int16_t *)dst = (int16_t)v;
    return 0;
  case NDEC_KIND_INT32:
    if (v < INT32_MIN || v > INT32_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "int32 overflow");
    *(int32_t *)dst = (int32_t)v;
    return 0;
  case NDEC_KIND_INT64:
    *(int64_t *)dst = (int64_t)v;
    return 0;
  default:
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected int kind");
  }
}

static int write_uint(NdecBindState *bs, void *dst, NdecKind kind, NdecRawStr raw) {
  char buf[NDEC_BIND_NUM_BUF];
  if (raw.len >= sizeof(buf))
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "number too long");
  memcpy(buf, raw.ptr, raw.len);
  buf[raw.len] = '\0';
  if (buf[0] == '-')
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "negative value for unsigned");

  char *end            = NULL;
  errno                = 0;
  unsigned long long v = strtoull(buf, &end, 10);
  if (errno == ERANGE || end == buf)
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "number out of range");
  if (*end != '\0')
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected integer");

  switch (kind) {
  case NDEC_KIND_UINT8:
    if (v > UINT8_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "uint8 overflow");
    *(uint8_t *)dst = (uint8_t)v;
    return 0;
  case NDEC_KIND_UINT16:
    if (v > UINT16_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "uint16 overflow");
    *(uint16_t *)dst = (uint16_t)v;
    return 0;
  case NDEC_KIND_UINT32:
    if (v > UINT32_MAX)
      return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "uint32 overflow");
    *(uint32_t *)dst = (uint32_t)v;
    return 0;
  case NDEC_KIND_UINT64:
    *(uint64_t *)dst = (uint64_t)v;
    return 0;
  default:
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected uint kind");
  }
}

static int write_float(NdecBindState *bs, void *dst, NdecKind kind, NdecRawStr raw) {
  char buf[NDEC_BIND_NUM_BUF];
  if (raw.len >= sizeof(buf))
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "number too long");
  memcpy(buf, raw.ptr, raw.len);
  buf[raw.len] = '\0';

  char *end = NULL;
  errno     = 0;
  double v  = strtod(buf, &end);
  if (end == buf)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected float");
  if (errno == ERANGE)
    return bs_fail(bs, NDEC_ERR_BIND_NUMBER_RANGE, "float out of range");

  switch (kind) {
  case NDEC_KIND_FLOAT32:
    *(float *)dst = (float)v;
    return 0;
  case NDEC_KIND_FLOAT64:
    *(double *)dst = v;
    return 0;
  default:
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected float kind");
  }
}

static int write_string(NdecBindState *bs, NdecBindSlot *slot, NdecRawStr raw) {
  if (slot->kind == NDEC_KIND_STRING) {
    char *s = ndec_arena_memdup_z(bs->arena, (const char *)raw.ptr, raw.len);
    if (!s)
      return bs_fail(bs, NDEC_ERR_BIND_OOM, "arena OOM");
    *(char **)slot->dst = s;
    return 0;
  }
  if (slot->kind == NDEC_KIND_STRING_LEN) {
    /* STRING_LEN requires a length companion field resolved via the
     * pending field's pre-computed absolute len_offset. Array elements
     * don't have a paired length, which is why STRING_LEN is rejected
     * as an array elem kind. */
    NdecBindFrame *f = BS_TOP(bs);
    if (f->kind != FR_STRUCT || !f->s.pending) {
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "STRING_LEN unsupported outside struct");
    }
    char *s = (char *)ndec_arena_alloc(bs->arena, raw.len);
    if (!s)
      return bs_fail(bs, NDEC_ERR_BIND_OOM, "arena OOM");
    if (raw.len)
      memcpy(s, raw.ptr, raw.len);
    *(char **)slot->dst                                  = s;
    *(size_t *)((uint8_t *)f->s.obj + f->s.pending_len_offset) = raw.len;
    return 0;
  }
  if (slot->kind == NDEC_KIND_FIXED_STRING) {
    /* char[N] inlined in the parent struct. The capacity N comes from
     * the pending field; FIXED_STRING is rejected as an array elem
     * kind so f->s.pending is guaranteed non-NULL here. */
    NdecBindFrame *f = BS_TOP(bs);
    if (f->kind != FR_STRUCT || !f->s.pending) {
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "FIXED_STRING unsupported outside struct");
    }
    size_t cap = f->s.pending->fixed_count;
    if (raw.len + 1 > cap)
      return bs_fail(bs, NDEC_ERR_BIND_FIXED_OVERFLOW, "char[N] overflow");
    char *dst = (char *)slot->dst;
    if (raw.len)
      memcpy(dst, raw.ptr, raw.len);
    dst[raw.len] = '\0';
    return 0;
  }
  return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected string kind");
}

static int32_t r_begin_object(NdecCtx *ctx, void *ud_v) {
  (void)ctx;
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindSlot slot;
  if (slot_for_value(bs, &slot) < 0)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected object");

  void *obj;
  const NdecTypeInfo *type;
  if (slot.kind == NDEC_KIND_STRUCT) {
    if (!slot.type)
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "missing struct type");
    obj  = slot.dst;
    type = slot.type;
    /* Zero nested structs so absent fields read as 0; the root struct
     * belongs to the caller and may carry pre-filled state (e.g. heap
     * array {ptr, cap, len} bookkeeping) that we must not clobber. */
    if (bs->depth > 0)
      memset(obj, 0, type->size);
  } else if (slot.kind == NDEC_KIND_STRUCT_PTR) {
    if (!slot.type)
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "missing struct type");
    void *allocated = ndec_arena_alloc(bs->arena, slot.type->size);
    if (!allocated)
      return bs_fail(bs, NDEC_ERR_BIND_OOM, "arena OOM");
    memset(allocated, 0, slot.type->size);
    *(void **)slot.dst = allocated;
    obj                = allocated;
    type               = slot.type;
  } else {
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected object");
  }

  bs_advance(bs);
  return bs_push_struct(bs, obj, type) == 0 ? NDEC_PROCEED : -2;
}

static int32_t r_end_object(NdecCtx *ctx, void *ud_v) {
  (void)ctx;
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindFrame *f  = BS_TOP(bs);
  if (f->kind != FR_STRUCT)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected end_object");

  /* REQUIRED check: every field with NDEC_FFLAG_REQUIRED must have
   * been matched. Bits beyond 63 are not tracked, so REQUIRED on a
   * field at index >= 64 is silently not enforced. (No struct in
   * practice has that many fields.) */
  if (f->s.type) {
    uint16_t n = f->s.type->st.field_count;
    if (n > 64)
      n = 64;
    for (uint16_t i = 0; i < n; i++) {
      const NdecField *fld = &f->s.type->st.fields[i];
      if ((fld->flags & NDEC_FFLAG_REQUIRED) && !(f->s.seen_mask & ((uint64_t)1 << i)))
        return bs_fail(bs, NDEC_ERR_BIND_REQUIRED, "required field missing");
    }
  }

  bs_pop(bs);
  return NDEC_PROCEED;
}

/* Lazy-build the field-name lookup for a STRUCT type, caching the
 * result on the type info itself. Returns NULL on OOM (caller maps to
 * BIND_OOM error). Multiple threads racing the build all run, but
 * only one install wins via atomic CAS; losers free their build.
 *
 * The cast away from const reflects the lazy-cache semantic: the
 * type info is otherwise a static const reflection table, but the
 * cache field is mutable-once for the program's lifetime. */
static NdecFieldLookup *type_lookup(NdecBindState *bs, const NdecTypeInfo *type) {
  NdecTypeInfo *t = (NdecTypeInfo *)type;
  NdecFieldLookup *lk = atomic_load_explicit(&t->st.lookup_cache, memory_order_acquire);
  if (lk) return lk;
  NdecFieldLookup *built = ndec_field_lookup_build(t->st.fields, t->st.field_count);
  if (!built) {
    bs_fail(bs, NDEC_ERR_BIND_OOM, "field lookup build OOM");
    return NULL;
  }
  NdecFieldLookup *expected = NULL;
  if (atomic_compare_exchange_strong_explicit(&t->st.lookup_cache, &expected, built,
                                               memory_order_release, memory_order_acquire)) {
    return built;
  }
  /* Lost the race; another thread published one already. */
  ndec_field_lookup_free(built);
  return expected;
}

static int32_t r_object_field(NdecCtx *ctx, void *ud_v, NdecStrInfo key) {
  (void)ctx;
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindFrame *f  = BS_TOP(bs);
  if (f->kind != FR_STRUCT)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "field outside struct");

  /* Step 1: match a parent field directly via the lazy-built lookup
   * table (covers both primary names and aliases; the UNION
   * discriminator field is matched here too — its name is the JSON
   * discriminator key). */
  NdecFieldLookup *lk = type_lookup(bs, f->s.type);
  if (!lk) return -2;
  int idx = ndec_field_lookup_find(lk, (const char *)key.raw.ptr, key.raw.len);
  if (idx >= 0) {
    const NdecField *fld    = &f->s.type->st.fields[idx];
    f->s.pending            = fld;
    f->s.pending_offset     = fld->offset;
    f->s.pending_len_offset = fld->len_offset;
    if (idx < 64)
      f->s.seen_mask |= (uint64_t)1 << (uint16_t)idx;
    return NDEC_PROCEED;
  }

  /* Step 2: if a union arm is active, try to match an arm field. The
   * arm field's offset is rewritten to the union-absolute address
   * (parent + union_offset + arm_offset + arm_field_offset) and
   * stashed in pending_offset, so slot_for_value never sees the arm
   * indirection. */
  if (f->s.u && f->s.u->active_arm) {
    const UnionState *us       = f->s.u;
    const NdecTypeInfo *at     = us->active_arm->arm_type;
    uint32_t arm_abs_offset_lo = us->union_field->len_offset + us->active_arm->arm_offset;
    NdecFieldLookup *alk = type_lookup(bs, at);
    if (!alk) return -2;
    int aidx = ndec_field_lookup_find(alk, (const char *)key.raw.ptr, key.raw.len);
    if (aidx >= 0) {
      const NdecField *afld   = &at->st.fields[aidx];
      f->s.pending            = afld;
      f->s.pending_offset     = arm_abs_offset_lo + afld->offset;
      f->s.pending_len_offset = afld->len_offset ? arm_abs_offset_lo + afld->len_offset : 0;
      return NDEC_PROCEED;
    }
  }

  /* Step 3: detect "arm field arrived before discriminator". If the
   * struct has a UNION field that hasn't been resolved yet, and the
   * key matches some arm's field, the JSON ordering is invalid. This
   * runs only on a miss, and only inside structs with a UNION field
   * yet-to-be-resolved — rare enough to keep the linear scan. */
  if (f->s.u && !f->s.u->active_arm) {
    const NdecUnionInfo *ui = (const NdecUnionInfo *)f->s.u->union_field->type;
    for (uint16_t a = 0; a < ui->arm_count; a++) {
      const NdecTypeInfo *at = ui->arms[a].arm_type;
      for (uint16_t i = 0; i < at->st.field_count; i++) {
        const NdecField *afld = &at->st.fields[i];
        if (afld->name_len == key.raw.len && memcmp(afld->name, key.raw.ptr, key.raw.len) == 0)
          return bs_fail(bs, NDEC_ERR_BIND_UNION_DISC_LATE, "arm field before discriminator");
        if (afld->alias && afld->alias_len == key.raw.len &&
            memcmp(afld->alias, key.raw.ptr, key.raw.len) == 0)
          return bs_fail(bs, NDEC_ERR_BIND_UNION_DISC_LATE, "arm field before discriminator");
      }
    }
  }

  if (bs->strict)
    return bs_fail(bs, NDEC_ERR_BIND_UNKNOWN_FIELD, "unknown field");
  return NDEC_SKIP;
}

static int32_t r_begin_array(NdecCtx *ctx, void *ud_v) {
  (void)ctx;
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindFrame *f  = BS_TOP(bs);
  if (f->kind != FR_STRUCT || !f->s.pending)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected array");

  const NdecField *fld = f->s.pending;
  if (fld->kind == NDEC_KIND_ARRAY) {
    void **dst_items = (void **)((uint8_t *)f->s.obj + f->s.pending_offset);
    size_t *dst_len  = (size_t *)((uint8_t *)f->s.obj + f->s.pending_len_offset);
    f->s.pending     = NULL;
    return bs_push_array(bs, fld, dst_items, dst_len, NULL, 0) == 0 ? NDEC_PROCEED : -2;
  }
  if (fld->kind == NDEC_KIND_FIXED_ARRAY) {
    void *inline_buf = (uint8_t *)f->s.obj + f->s.pending_offset;
    f->s.pending     = NULL;
    return bs_push_array(bs, fld, NULL, NULL, inline_buf, fld->fixed_count) == 0 ? NDEC_PROCEED : -2;
  }
  if (fld->kind == NDEC_KIND_HEAP_ARRAY) {
    uint8_t *base      = (uint8_t *)f->s.obj;
    void   **items_slot = (void **)(base + f->s.pending_offset);
    size_t  *cap_slot   = (size_t *)(base + f->s.pending_len_offset);
    void    *items_ptr  = *items_slot;
    size_t   cap        = *cap_slot;
    size_t  *dst_len    = (size_t *)(base + fld->aux_offset);
    if (!items_ptr && cap > 0)
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "heap array items pointer NULL");
    f->s.pending = NULL;
    return bs_push_array(bs, fld, NULL, dst_len, items_ptr, cap) == 0 ? NDEC_PROCEED : -2;
  }
  return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected array");
}

static int32_t r_end_array(NdecCtx *ctx, void *ud_v) {
  (void)ctx;
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindFrame *f  = BS_TOP(bs);
  if (f->kind != FR_ARRAY)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected end_array");
  ArrayFrame *a = &f->a;

  if (a->inline_buf) {
    /* In-place mode. dst_len != NULL for HEAP_ARRAY (write actual
     * count back to caller's len field); NULL for FIXED_ARRAY (N is
     * implicit in the type, no len field to update). No arena copy. */
    if (a->dst_len)
      *a->dst_len = a->count;
    bs_pop(bs);
    return NDEC_PROCEED;
  }

  /* Dynamic slice: arena-allocate the final array and write items/len
   * to the user's slot. */
  void *items = NULL;
  if (a->count > 0) {
    items = ndec_arena_alloc(bs->arena, a->count * a->elem_size);
    if (!items)
      return bs_fail(bs, NDEC_ERR_BIND_OOM, "arena OOM");
    memcpy(items, a->scratch, a->count * a->elem_size);
  }
  *a->dst_items = items;
  *a->dst_len   = a->count;

  free(a->scratch);
  a->scratch = NULL;
  bs_pop(bs);
  return NDEC_PROCEED;
}

static int32_t r_scalar_null(void *ud_v) {
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindSlot slot;
  if (slot_for_value(bs, &slot) < 0)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected null");

  switch (slot.kind) {
  case NDEC_KIND_STRING:
  case NDEC_KIND_STRUCT_PTR:
    *(void **)slot.dst = NULL;
    break;
  case NDEC_KIND_STRUCT:
    if (slot.type)
      memset(slot.dst, 0, slot.type->size);
    break;
  case NDEC_KIND_STRING_LEN: {
    *(char **)slot.dst = NULL;
    NdecBindFrame *f   = BS_TOP(bs);
    if (f->kind == FR_STRUCT && f->s.pending)
      *(size_t *)((uint8_t *)f->s.obj + f->s.pending_len_offset) = 0;
    break;
  }
  case NDEC_KIND_FIXED_STRING: {
    /* JSON null on a char[N] writes an empty string. */
    NdecBindFrame *f = BS_TOP(bs);
    if (f->kind == FR_STRUCT && f->s.pending && f->s.pending->fixed_count > 0)
      ((char *)slot.dst)[0] = '\0';
    break;
  }
  case NDEC_KIND_FIXED_ARRAY: {
    /* JSON null on a T[N] zero-fills the whole buffer. */
    NdecBindFrame *f = BS_TOP(bs);
    if (f->kind == FR_STRUCT && f->s.pending) {
      size_t es = kind_size(f->s.pending->elem_kind, f->s.pending->type);
      if (es)
        memset(slot.dst, 0, es * f->s.pending->fixed_count);
    }
    break;
  }
  case NDEC_KIND_HEAP_ARRAY: {
    /* JSON null on a caller-owned heap array: report zero elements
     * written. items buffer is left untouched for the caller to free. */
    NdecBindFrame *f = BS_TOP(bs);
    if (f->kind == FR_STRUCT && f->s.pending)
      *(size_t *)((uint8_t *)f->s.obj + f->s.pending->aux_offset) = 0;
    break;
  }
  default: {
    size_t sz = kind_size(slot.kind, slot.type);
    if (sz)
      memset(slot.dst, 0, sz);
  }
  }
  bs_advance(bs);
  return NDEC_PROCEED;
}

static int32_t r_scalar_bool(void *ud_v, int value) {
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindSlot slot;
  if (slot_for_value(bs, &slot) < 0)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected bool");
  if (slot.kind != NDEC_KIND_BOOL)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected bool");
  *(bool *)slot.dst = value ? true : false;
  bs_advance(bs);
  return NDEC_PROCEED;
}

static int32_t r_scalar_number(void *ud_v, NdecRawStr raw) {
  NdecBindState *bs = (NdecBindState *)ud_v;
  NdecBindSlot slot;
  if (slot_for_value(bs, &slot) < 0)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected number");

  int rc;
  switch (slot.kind) {
  case NDEC_KIND_INT8:
  case NDEC_KIND_INT16:
  case NDEC_KIND_INT32:
  case NDEC_KIND_INT64:
    rc = write_int(bs, slot.dst, slot.kind, raw);
    break;
  case NDEC_KIND_UINT8:
  case NDEC_KIND_UINT16:
  case NDEC_KIND_UINT32:
  case NDEC_KIND_UINT64:
    rc = write_uint(bs, slot.dst, slot.kind, raw);
    break;
  case NDEC_KIND_FLOAT32:
  case NDEC_KIND_FLOAT64:
    rc = write_float(bs, slot.dst, slot.kind, raw);
    break;
  default:
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "expected number");
  }
  if (rc < 0)
    return -2;
  bs_advance(bs);
  return NDEC_PROCEED;
}

static int32_t r_scalar_string(void *ud_v, NdecStrInfo str) {
  NdecBindState *bs = (NdecBindState *)ud_v;

  /* Intercept the discriminator string of a tagged union before the
   * generic write_string path. The pending field is the parent's
   * UNION field; .offset holds the discriminator enum offset and
   * .type points (cast) at the arm table. */
  NdecBindFrame *f = BS_TOP(bs);
  if (f->kind == FR_STRUCT && f->s.pending && f->s.pending->kind == NDEC_KIND_UNION) {
    const NdecField *uf     = f->s.pending;
    const NdecUnionInfo *ui = (const NdecUnionInfo *)uf->type;
    for (uint16_t a = 0; a < ui->arm_count; a++) {
      const NdecUnionArm *arm = &ui->arms[a];
      if (arm->tag_len == str.raw.len && memcmp(arm->tag, str.raw.ptr, str.raw.len) == 0) {
        /* Discriminator written as int (typical enum width). */
        *(int *)((uint8_t *)f->s.obj + uf->offset) = (int)arm->enum_value;
        if (f->s.u)
          f->s.u->active_arm = arm;
        bs_advance(bs);
        return NDEC_PROCEED;
      }
    }
    return bs_fail(bs, NDEC_ERR_BIND_UNION_BAD_TAG, "unknown union tag");
  }

  NdecBindSlot slot;
  if (slot_for_value(bs, &slot) < 0)
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unexpected string");
  if (write_string(bs, &slot, str.raw) < 0)
    return -2;
  bs_advance(bs);
  return NDEC_PROCEED;
}

/*
 * Specialized parser: inline bind reactor into a private ndec_parse.
 *
 * Instead of registering the nine r_* functions as a vtable that the
 * generic ndec_parse would dispatch through indirect calls, we
 * re-emit ndec_parse here under the name `bind_parse` with the
 * NDEC_R_* macros pinned to the bind callbacks. The compiler then
 * turns every reactor callsite inside the parser into a direct call
 * (no vtable load, no BTB pressure, better register allocation
 * across the call boundary). Reactor cost is the dominant fraction
 * of unmarshal time, so the win compounds across every event.
 *
 * NDEC_FN_DECL is redefined to `static` so bind_parse is a TU-local
 * symbol; only ndec_unmarshal_ex below references it.
 */

#define NDEC_R_BEGIN_OBJECT(ctx, ud)       r_begin_object((ctx), (ud))
#define NDEC_R_END_OBJECT(ctx, ud)         r_end_object((ctx), (ud))
#define NDEC_R_OBJECT_FIELD(ctx, ud, key)  r_object_field((ctx), (ud), (key))
#define NDEC_R_BEGIN_ARRAY(ctx, ud)        r_begin_array((ctx), (ud))
#define NDEC_R_END_ARRAY(ctx, ud)          r_end_array((ctx), (ud))
#define NDEC_R_SCALAR_NULL(ctx, ud)        r_scalar_null((ud))
#define NDEC_R_SCALAR_BOOL(ctx, ud, v)     r_scalar_bool((ud), (v))
#define NDEC_R_SCALAR_NUMBER(ctx, ud, raw) r_scalar_number((ud), (raw))
#define NDEC_R_SCALAR_STRING(ctx, ud, raw) r_scalar_string((ud), (raw))

#define NDEC_FN_DECL static
#define NDEC_FN_NAME bind_parse
#include "ndec/core/parser.h"
#undef NDEC_FN_NAME
#undef NDEC_FN_DECL

/* Predefined scalar root descriptors. Defined here (not in the header)
 * so they live in a single translation unit and pointer-equality
 * comparisons remain stable across users. */
#define NDEC_DEFINE_SCALAR_TI(NAME_, KIND_, CTYPE_)                                                               \
  const NdecTypeInfo NAME_ = {                                                                                    \
      .name    = #NAME_,                                                                                          \
      .size    = sizeof(CTYPE_),                                                                                  \
      .ti_kind = NDEC_TI_SCALAR,                                                                                  \
      .sc      = {.kind = (KIND_)},                                                                               \
  }

NDEC_DEFINE_SCALAR_TI(ndec_type_bool,    NDEC_KIND_BOOL,    bool);
NDEC_DEFINE_SCALAR_TI(ndec_type_int8,    NDEC_KIND_INT8,    int8_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_int16,   NDEC_KIND_INT16,   int16_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_int32,   NDEC_KIND_INT32,   int32_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_int64,   NDEC_KIND_INT64,   int64_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_uint8,   NDEC_KIND_UINT8,   uint8_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_uint16,  NDEC_KIND_UINT16,  uint16_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_uint32,  NDEC_KIND_UINT32,  uint32_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_uint64,  NDEC_KIND_UINT64,  uint64_t);
NDEC_DEFINE_SCALAR_TI(ndec_type_float32, NDEC_KIND_FLOAT32, float);
NDEC_DEFINE_SCALAR_TI(ndec_type_float64, NDEC_KIND_FLOAT64, double);
NDEC_DEFINE_SCALAR_TI(ndec_type_string,  NDEC_KIND_STRING,  char *);

#undef NDEC_DEFINE_SCALAR_TI

static int bs_bootstrap(NdecBindState *bs, const NdecTypeInfo *type, void *out) {
  memset(bs, 0, sizeof(*bs));
  bs->depth = 0;

  switch (type->ti_kind) {
  case NDEC_TI_STRUCT:
    bs->root_field = (NdecField){
        .kind = NDEC_KIND_STRUCT,
        .type = type,
    };
    break;

  case NDEC_TI_ARRAY:
    if (type->ar.elem_size == 0)
      return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "array elem_size is 0");
    if (type->ar.is_heap) {
      bs->root_field = (NdecField){
          .kind       = NDEC_KIND_HEAP_ARRAY,
          .offset     = 0,                                               /* items */
          .len_offset = (uint32_t)sizeof(void *),                        /* cap */
          .aux_offset = (uint32_t)(sizeof(void *) + sizeof(size_t)),     /* len */
          .elem_kind  = type->ar.elem_kind,
          .type       = type->ar.elem_type,
      };
    } else {
      bs->root_field = (NdecField){
          .kind       = NDEC_KIND_ARRAY,
          .offset     = 0,
          .len_offset = (uint32_t)sizeof(void *),
          .elem_kind  = type->ar.elem_kind,
          .type       = type->ar.elem_type,
      };
    }
    break;

  case NDEC_TI_SCALAR:
    bs->root_field = (NdecField){
        .kind = type->sc.kind,
    };
    break;

  default:
    return bs_fail(bs, NDEC_ERR_BIND_TYPE_MISMATCH, "unknown ti_kind");
  }

  bs->stack[0].kind = FR_STRUCT;
  bs->stack[0].s    = (StructFrame){
      .obj                = out, /* pending_offset == 0 -> writes to out */
      .type               = NULL,
      .pending            = &bs->root_field,
      .pending_offset     = bs->root_field.offset,
      .pending_len_offset = bs->root_field.len_offset,
  };
  return 0;
}

static int bs_finalize(NdecBindState *bs, uint32_t kernel_exit, uint32_t kernel_pos, NdecUnmarshalError *err) {
  /* Free any scratches left over from error paths (normal paths pop
   * them in end_array). */
  for (int i = 0; i <= bs->depth; i++) {
    if (bs->stack[i].kind == FR_ARRAY && bs->stack[i].a.scratch) {
      free(bs->stack[i].a.scratch);
      bs->stack[i].a.scratch = NULL;
    }
  }

  if (kernel_exit == NDEC_OK && bs->err_code == 0) {
    if (err) {
      err->code    = 0;
      err->pos     = 0;
      err->message = NULL;
    }
    return 0;
  }

  int code = bs->err_code ? bs->err_code : (int)kernel_exit;
  if (err) {
    err->code    = code;
    err->pos     = (size_t)kernel_pos;
    err->message = bs->err_message;
  }
  return -1;
}

int ndec_unmarshal_ex(const NdecTypeInfo *type, void *out, const uint8_t *data, size_t len, NdecArena *arena,
                      const NdecUnmarshalOpts *opts, NdecUnmarshalError *err) {
  if (err) {
    err->code    = 0;
    err->pos     = 0;
    err->message = NULL;
  }

  if (!type || !out || !arena)
    return -1;

  NdecBindState bs;
  /* Bootstrap can record an error (e.g. malformed type info) before
   * parsing starts; bs_finalize will surface it. We still run the
   * kernel so it can produce a kernel-side syntax error position if
   * the JSON itself is bad — bs_finalize's "bs->err_code wins" rule
   * keeps the bind error first. */
  bs_bootstrap(&bs, type, out);
  bs.arena  = arena;
  bs.strict = opts ? (opts->strict ? true : false) : false;

  if (bs.err_code != 0)
    return bs_finalize(&bs, NDEC_OK, 0, err);

  NdecCtx ctx;
  /* reactor=NULL: bind_parse never reads ctx->reactor (NDEC_R_* are
   * pinned to bind's callbacks at compile time). */
  ndec_ctx_init(&ctx, NULL, &bs);
  ndec_ctx_set_input(&ctx, data, (uint32_t)len, 1);
  ndec_ctx_arm_root(&ctx);
  bind_parse(&ctx);

  return bs_finalize(&bs, ctx.exit_code, ctx.error_pos, err);
}

int ndec_unmarshal(const NdecTypeInfo *type, void *out, const uint8_t *data, size_t len, NdecArena *arena) {
  return ndec_unmarshal_ex(type, out, data, len, arena, NULL, NULL);
}
