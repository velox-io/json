#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <criterion/criterion.h>

#include "bind.h"

/* Helpers */

static int try_unmarshal(const NdecTypeInfo *ti, void *out, const char *json, NdecArena *arena) {
  return ndec_unmarshal(ti, out, (const uint8_t *)json, strlen(json), arena);
}

static int try_unmarshal_strict(const NdecTypeInfo *ti, void *out, const char *json, NdecArena *arena,
                                NdecUnmarshalError *err) {
  NdecUnmarshalOpts opts = {.strict = 1};
  return ndec_unmarshal_ex(ti, out, (const uint8_t *)json, strlen(json), arena, &opts, err);
}

/* Scalars */

typedef struct Scalars {
  bool b;
  int8_t i8;
  int16_t i16;
  int32_t i32;
  int64_t i64;
  uint8_t u8;
  uint16_t u16;
  uint32_t u32;
  uint64_t u64;
  float f32;
  double f64;
} Scalars;

// clang-format off
NDEC_REFLECT(Scalars,
  FIELD_BOOL   ("b",   b),
  FIELD_INT8   ("i8",  i8),
  FIELD_INT16  ("i16", i16),
  FIELD_INT32  ("i32", i32),
  FIELD_INT64  ("i64", i64),
  FIELD_UINT8  ("u8",  u8),
  FIELD_UINT16 ("u16", u16),
  FIELD_UINT32 ("u32", u32),
  FIELD_UINT64 ("u64", u64),
  FIELD_FLOAT32("f32", f32),
  FIELD_FLOAT64("f64", f64)
);
// clang-format on

Test(bind_scalars, all_types) {
  Scalars s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);

  const char *json = "{\"b\":true,"
                     "\"i8\":-12,\"i16\":-1200,\"i32\":-120000,\"i64\":-12000000000,"
                     "\"u8\":250,\"u16\":60000,\"u32\":4000000000,\"u64\":18000000000000000000,"
                     "\"f32\":1.5,\"f64\":3.141592653589793}";

  cr_assert_eq(try_unmarshal(NDEC_TYPE(Scalars), &s, json, &arena), 0);
  cr_expect_eq(s.b, true);
  cr_expect_eq(s.i8, -12);
  cr_expect_eq(s.i16, -1200);
  cr_expect_eq(s.i32, -120000);
  cr_expect_eq(s.i64, -12000000000LL);
  cr_expect_eq(s.u8, 250);
  cr_expect_eq(s.u16, 60000);
  cr_expect_eq(s.u32, 4000000000U);
  cr_expect_eq(s.u64, 18000000000000000000ULL);
  cr_expect_float_eq(s.f32, 1.5f, 1e-6f);
  cr_expect_float_eq(s.f64, 3.141592653589793, 1e-12);

  ndec_arena_destroy(&arena);
}

Test(bind_scalars, int_overflow) {
  Scalars s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};

  int rc = ndec_unmarshal_ex(NDEC_TYPE(Scalars), &s, (const uint8_t *)"{\"i8\":200}", 10, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_NUMBER_RANGE);

  ndec_arena_destroy(&arena);
}

Test(bind_scalars, type_mismatch) {
  Scalars s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};

  int rc =
      ndec_unmarshal_ex(NDEC_TYPE(Scalars), &s, (const uint8_t *)"{\"i32\":\"oops\"}", 15, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_TYPE_MISMATCH);

  ndec_arena_destroy(&arena);
}

Test(bind_scalars, float_accepts_integer) {
  Scalars s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Scalars), &s, "{\"f64\":42}", &arena), 0);
  cr_expect_float_eq(s.f64, 42.0, 1e-12);
  ndec_arena_destroy(&arena);
}

/* Strings */

typedef struct StrSimple {
  char *name;
} StrSimple;

// clang-format off
NDEC_REFLECT(StrSimple,
  FIELD_STRING("name", name)
);
// clang-format on

typedef struct StrLen {
  char *blob;
  size_t blob_len;
} StrLen;

// clang-format off
NDEC_REFLECT(StrLen,
  FIELD_STRING_LEN("blob", blob, blob_len)
);
// clang-format on

Test(bind_string, nul_terminated) {
  StrSimple s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSimple), &s, "{\"name\":\"hello\"}", &arena), 0);
  cr_assert_not_null(s.name);
  cr_expect_str_eq(s.name, "hello");
  ndec_arena_destroy(&arena);
}

Test(bind_string, string_len_with_size) {
  StrLen s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrLen), &s, "{\"blob\":\"abc\"}", &arena), 0);
  cr_assert_not_null(s.blob);
  cr_expect_eq(s.blob_len, 3);
  cr_expect_eq(memcmp(s.blob, "abc", 3), 0);
  ndec_arena_destroy(&arena);
}

Test(bind_string, null_sets_pointer_null) {
  StrSimple s;
  s.name = (char *)0xdeadbeef;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSimple), &s, "{\"name\":null}", &arena), 0);
  cr_expect_null(s.name);
  ndec_arena_destroy(&arena);
}

/* Nested structs */

typedef struct Point {
  int32_t x, y;
} Point;

// clang-format off
NDEC_REFLECT(Point,
  FIELD_INT32("x", x),
  FIELD_INT32("y", y)
);
// clang-format on

typedef struct Line {
  Point a;
  Point *b;
} Line;

// clang-format off
NDEC_REFLECT(Line,
  FIELD_STRUCT    ("a", a, Point),
  FIELD_STRUCT_PTR("b", b, Point)
);
// clang-format on

Test(bind_nested, value_and_pointer) {
  Line l = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Line), &l,
                             "{\"a\":{\"x\":1,\"y\":2},"
                             "\"b\":{\"x\":3,\"y\":4}}",
                             &arena),
               0);
  cr_expect_eq(l.a.x, 1);
  cr_expect_eq(l.a.y, 2);
  cr_assert_not_null(l.b);
  cr_expect_eq(l.b->x, 3);
  cr_expect_eq(l.b->y, 4);
  ndec_arena_destroy(&arena);
}

Test(bind_nested, null_pointer) {
  Line l = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Line), &l, "{\"a\":{\"x\":5,\"y\":6},\"b\":null}", &arena), 0);
  cr_expect_eq(l.a.x, 5);
  cr_expect_null(l.b);
  ndec_arena_destroy(&arena);
}

/* Arrays */

typedef struct NumList {
  int32_t *nums;
  size_t nums_len;
} NumList;

// clang-format off
NDEC_REFLECT(NumList,
  FIELD_ARRAY_INT32("nums", nums, nums_len)
);
// clang-format on

Test(bind_array, int32) {
  NumList v = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(NumList), &v, "{\"nums\":[1,2,3,4,5]}", &arena), 0);
  cr_assert_eq(v.nums_len, 5);
  for (int i = 0; i < 5; i++)
    cr_expect_eq(v.nums[i], i + 1);
  ndec_arena_destroy(&arena);
}

Test(bind_array, empty) {
  NumList v;
  v.nums     = (int32_t *)0xdead;
  v.nums_len = 99;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(NumList), &v, "{\"nums\":[]}", &arena), 0);
  cr_expect_eq(v.nums_len, 0);
  ndec_arena_destroy(&arena);
}

typedef struct Tag {
  char *name;
  int32_t score;
} Tag;

// clang-format off
NDEC_REFLECT(Tag,
  FIELD_STRING("name",  name),
  FIELD_INT32 ("score", score)
);
// clang-format on

typedef struct User {
  char *name;
  int32_t age;
  Tag *tags;
  size_t tags_len;
} User;

// clang-format off
NDEC_REFLECT(User,
  FIELD_STRING      ("name", name),
  FIELD_INT32       ("age",  age),
  FIELD_ARRAY_STRUCT("tags", tags, tags_len, Tag)
);
// clang-format on

Test(bind_array, struct_elements) {
  User u = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(User), &u,
                             "{\"name\":\"Alice\",\"age\":30,"
                             "\"tags\":["
                             "{\"name\":\"go\",\"score\":5},"
                             "{\"name\":\"rust\",\"score\":4},"
                             "{\"name\":\"json\",\"score\":3}]}",
                             &arena),
               0);
  cr_expect_str_eq(u.name, "Alice");
  cr_expect_eq(u.age, 30);
  cr_assert_eq(u.tags_len, 3);
  cr_expect_str_eq(u.tags[0].name, "go");
  cr_expect_eq(u.tags[0].score, 5);
  cr_expect_str_eq(u.tags[1].name, "rust");
  cr_expect_eq(u.tags[1].score, 4);
  cr_expect_str_eq(u.tags[2].name, "json");
  cr_expect_eq(u.tags[2].score, 3);
  ndec_arena_destroy(&arena);
}

typedef struct StringList {
  char **items;
  size_t items_len;
} StringList;

// clang-format off
NDEC_REFLECT(StringList,
  FIELD_ARRAY_STRING("items", items, items_len)
);
// clang-format on

Test(bind_array, strings) {
  StringList sl = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StringList), &sl, "{\"items\":[\"a\",\"bb\",\"ccc\"]}", &arena), 0);
  cr_assert_eq(sl.items_len, 3);
  cr_expect_str_eq(sl.items[0], "a");
  cr_expect_str_eq(sl.items[1], "bb");
  cr_expect_str_eq(sl.items[2], "ccc");
  ndec_arena_destroy(&arena);
}

/* Unknown fields */

Test(bind_unknown, silent_skip_default) {
  StrSimple s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSimple), &s, "{\"extra\":42,\"name\":\"x\",\"more\":[1,2]}", &arena), 0);
  cr_expect_str_eq(s.name, "x");
  ndec_arena_destroy(&arena);
}

Test(bind_unknown, strict_reports_error) {
  StrSimple s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc = try_unmarshal_strict(NDEC_TYPE(StrSimple), &s, "{\"extra\":42,\"name\":\"x\"}", &arena, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_UNKNOWN_FIELD);
  ndec_arena_destroy(&arena);
}

/*
 * Missing fields stay at caller's pre-fill value.
 *
 * bind only writes fields that appear in the JSON. The root struct
 * is the caller's; bind never memsets it. Callers commonly pass
 * Foo f = {0} so absent JSON fields read as 0, but pre-filled state
 * (e.g. heap-array bookkeeping) is also preserved.
 */

Test(bind_missing, left_at_default) {
  Scalars s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Scalars), &s, "{\"i32\":7}", &arena), 0);
  cr_expect_eq(s.i32, 7);
  cr_expect_eq(s.i8, 0);
  cr_expect_eq(s.b, false);
  cr_expect_eq(s.u64, 0);
  cr_expect_float_eq(s.f64, 0.0, 1e-12);
  ndec_arena_destroy(&arena);
}

Test(bind_missing, prefilled_preserved) {
  Scalars s = {.i32 = 100, .i8 = 99, .u64 = 12345};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Scalars), &s, "{\"i32\":7}", &arena), 0);
  cr_expect_eq(s.i32, 7); /* JSON wrote it */
  cr_expect_eq(s.i8, 99); /* preserved */
  cr_expect_eq(s.u64, 12345);
  ndec_arena_destroy(&arena);
}

/* Arena behavior */

Test(bind_arena, reset_reuses_chunks) {
  NdecArena arena;
  ndec_arena_init(&arena);
  StrSimple s = {0};

  for (int i = 0; i < 10; i++) {
    ndec_arena_reset(&arena);
    memset(&s, 0, sizeof(s));
    cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSimple), &s, "{\"name\":\"reuseable\"}", &arena), 0);
    cr_expect_str_eq(s.name, "reuseable");
  }
  ndec_arena_destroy(&arena);
}

Test(bind_arena, destroy_is_idempotent) {
  NdecArena arena;
  ndec_arena_init(&arena);
  StrSimple s = {0};
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSimple), &s, "{\"name\":\"x\"}", &arena), 0);
  ndec_arena_destroy(&arena);
  ndec_arena_destroy(&arena);
}

Test(bind_arena, zero_alloc_returns_non_null) {
  NdecArena arena;
  ndec_arena_init(&arena);
  void *p = ndec_arena_alloc(&arena, 0);
  cr_expect_not_null(p); /* safe sentinel, don't dereference */
  ndec_arena_destroy(&arena);
}

/* Annotations: ALIAS, REQUIRED */

typedef struct AliasUser {
  char *name;
  int32_t id;
} AliasUser;

// clang-format off
NDEC_REFLECT(AliasUser,
  FIELD_STRING(ANNOT(ALIAS("user_name")), "name", name),
  FIELD_INT32 (ANNOT(ALIAS("uid")),       "id",   id)
);
// clang-format on

Test(bind_annot_alias, primary_key_still_works) {
  AliasUser u = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(AliasUser), &u, "{\"name\":\"alice\",\"id\":7}", &arena), 0);
  cr_expect_str_eq(u.name, "alice");
  cr_expect_eq(u.id, 7);
  ndec_arena_destroy(&arena);
}

Test(bind_annot_alias, secondary_key_matches) {
  AliasUser u = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  /* JSON uses the alias; the primary key never appears */
  cr_assert_eq(try_unmarshal(NDEC_TYPE(AliasUser), &u, "{\"user_name\":\"bob\",\"uid\":42}", &arena), 0);
  cr_expect_str_eq(u.name, "bob");
  cr_expect_eq(u.id, 42);
  ndec_arena_destroy(&arena);
}

Test(bind_annot_alias, mixed_keys) {
  AliasUser u = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(AliasUser), &u, "{\"user_name\":\"carol\",\"id\":3}", &arena), 0);
  cr_expect_str_eq(u.name, "carol");
  cr_expect_eq(u.id, 3);
  ndec_arena_destroy(&arena);
}

typedef struct ReqRecord {
  int32_t id;
  char *name;
  int32_t version;
} ReqRecord;

// clang-format off
NDEC_REFLECT(ReqRecord,
  FIELD_INT32 (ANNOT(REQUIRED), "id",      id),
  FIELD_STRING(ANNOT(REQUIRED), "name",    name),
  FIELD_INT32 (        "version", version)
);
// clang-format on

Test(bind_annot_required, all_present_ok) {
  ReqRecord r = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(ReqRecord), &r, "{\"id\":1,\"name\":\"x\",\"version\":2}", &arena), 0);
  cr_expect_eq(r.id, 1);
  cr_expect_str_eq(r.name, "x");
  cr_expect_eq(r.version, 2);
  ndec_arena_destroy(&arena);
}

Test(bind_annot_required, optional_missing_ok) {
  ReqRecord r = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  /* "version" has no REQUIRED annotation -> may be absent */
  cr_assert_eq(try_unmarshal(NDEC_TYPE(ReqRecord), &r, "{\"id\":1,\"name\":\"x\"}", &arena), 0);
  cr_expect_eq(r.id, 1);
  cr_expect_str_eq(r.name, "x");
  cr_expect_eq(r.version, 0);
  ndec_arena_destroy(&arena);
}

Test(bind_annot_required, missing_required_fails) {
  ReqRecord r = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  /* "name" is REQUIRED but absent */
  int rc = ndec_unmarshal_ex(NDEC_TYPE(ReqRecord), &r, (const uint8_t *)"{\"id\":1}", 8, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_REQUIRED);
  ndec_arena_destroy(&arena);
}

Test(bind_annot_required, alias_satisfies_required) {
  /* A REQUIRED field matched via its ALIAS still counts as present. */
  typedef struct AR {
    int32_t id;
  } AR;
  /* Inline reflection block; safe because the typedef is local-only. */
  static const NdecField AR_local_fields[] = {
      {
          .name      = "id",
          .name_len  = 2,
          .kind      = NDEC_KIND_INT32,
          .offset    = (uint32_t)offsetof(AR, id),
          .alias     = "uid",
          .alias_len = 3,
          .flags     = NDEC_FFLAG_REQUIRED,
      },
  };
  static NdecTypeInfo AR_local_info = {
      .name    = "AR",
      .size    = sizeof(AR),
      .ti_kind = NDEC_TI_STRUCT,
      .st      = {.fields = AR_local_fields, .field_count = 1},
  };

  AR r = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(ndec_unmarshal(&AR_local_info, &r, (const uint8_t *)"{\"uid\":99}", 10, &arena), 0);
  cr_expect_eq(r.id, 99);
  ndec_arena_destroy(&arena);
}

/* Tagged union: internal-tagged, string discriminator */

typedef enum {
  SHAPE_NONE = 0,
  SHAPE_CIRCLE,
  SHAPE_RECT,
} ShapeKind;

typedef struct ShapeCircle {
  double radius;
} ShapeCircle;

typedef struct ShapeRect {
  double w, h;
} ShapeRect;

typedef union ShapeBody {
  ShapeCircle circle;
  ShapeRect rect;
} ShapeBody;

typedef struct Shape {
  char *color;    /* common field */
  ShapeKind kind; /* discriminator */
  ShapeBody body;
} Shape;

// clang-format off
NDEC_REFLECT(ShapeCircle,
  FIELD_FLOAT64("radius", radius)
);

NDEC_REFLECT(ShapeRect,
  FIELD_FLOAT64("w", w),
  FIELD_FLOAT64("h", h)
);

NDEC_UNION(ShapeArms, ShapeBody,
  ARM("circle", SHAPE_CIRCLE, ShapeCircle, circle),
  ARM("rect",   SHAPE_RECT,   ShapeRect,   rect)
);

NDEC_REFLECT(Shape,
  FIELD_STRING("color", color),
  FIELD_UNION ("kind",  kind, body, ShapeArms)
);
// clang-format on

Test(bind_union, basic_circle) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Shape), &s, "{\"kind\":\"circle\",\"radius\":2.5}", &arena), 0);
  cr_expect_eq(s.kind, SHAPE_CIRCLE);
  cr_expect_float_eq(s.body.circle.radius, 2.5, 1e-12);
  ndec_arena_destroy(&arena);
}

Test(bind_union, basic_rect) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Shape), &s, "{\"kind\":\"rect\",\"w\":4,\"h\":5}", &arena), 0);
  cr_expect_eq(s.kind, SHAPE_RECT);
  cr_expect_float_eq(s.body.rect.w, 4.0, 1e-12);
  cr_expect_float_eq(s.body.rect.h, 5.0, 1e-12);
  ndec_arena_destroy(&arena);
}

Test(bind_union, common_field_with_arm) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Shape), &s, "{\"color\":\"red\",\"kind\":\"circle\",\"radius\":1}", &arena),
               0);
  cr_expect_str_eq(s.color, "red");
  cr_expect_eq(s.kind, SHAPE_CIRCLE);
  cr_expect_float_eq(s.body.circle.radius, 1.0, 1e-12);
  ndec_arena_destroy(&arena);
}

Test(bind_union, common_field_after_arm_ok) {
  /* common field after arm is fine; only arm fields require
   * discriminator to come first */
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(
      try_unmarshal(NDEC_TYPE(Shape), &s, "{\"kind\":\"rect\",\"w\":1,\"h\":2,\"color\":\"blue\"}", &arena), 0);
  cr_expect_str_eq(s.color, "blue");
  cr_expect_eq(s.kind, SHAPE_RECT);
  cr_expect_float_eq(s.body.rect.w, 1.0, 1e-12);
  cr_expect_float_eq(s.body.rect.h, 2.0, 1e-12);
  ndec_arena_destroy(&arena);
}

Test(bind_union, unknown_tag_fails) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc =
      ndec_unmarshal_ex(NDEC_TYPE(Shape), &s, (const uint8_t *)"{\"kind\":\"triangle\"}", 19, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_UNION_BAD_TAG);
  ndec_arena_destroy(&arena);
}

Test(bind_union, late_discriminator_fails) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc = ndec_unmarshal_ex(NDEC_TYPE(Shape), &s, (const uint8_t *)"{\"radius\":3,\"kind\":\"circle\"}", 28,
                             &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_UNION_DISC_LATE);
  ndec_arena_destroy(&arena);
}

Test(bind_union, missing_discriminator_with_arm_field) {
  Shape s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  /* No "kind" anywhere; "radius" is unknown to parent + matches an
   * arm -> diagnosed as DISC_LATE (it's the same root cause). */
  int rc = ndec_unmarshal_ex(NDEC_TYPE(Shape), &s, (const uint8_t *)"{\"radius\":3}", 12, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_UNION_DISC_LATE);
  ndec_arena_destroy(&arena);
}

typedef struct Pt2 {
  int32_t x, y;
} Pt2;

NDEC_REFLECT(Pt2, FIELD_INT32("x", x), FIELD_INT32("y", y));

typedef enum {
  EVT_NONE = 0,
  EVT_CLICK,
  EVT_KEY,
} EventKind;

typedef struct ClickEvent {
  Pt2 at;
  int32_t button;
} ClickEvent;

typedef struct KeyEvent {
  char *key;
} KeyEvent;

typedef union EventBody {
  ClickEvent click;
  KeyEvent key;
} EventBody;

typedef struct Event {
  EventKind kind;
  EventBody body;
} Event;

// clang-format off
NDEC_REFLECT(ClickEvent,
  FIELD_STRUCT("at",     at, Pt2),
  FIELD_INT32 ("button", button)
);

NDEC_REFLECT(KeyEvent,
  FIELD_STRING("key", key)
);

NDEC_UNION(EventArms, EventBody,
  ARM("click", EVT_CLICK, ClickEvent, click),
  ARM("key",   EVT_KEY,   KeyEvent,   key)
);

NDEC_REFLECT(Event,
  FIELD_UNION("type", kind, body, EventArms)
);
// clang-format on

Test(bind_union, nested_struct_in_arm) {
  Event e = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(
      try_unmarshal(NDEC_TYPE(Event), &e, "{\"type\":\"click\",\"at\":{\"x\":10,\"y\":20},\"button\":1}", &arena),
      0);
  cr_expect_eq(e.kind, EVT_CLICK);
  cr_expect_eq(e.body.click.at.x, 10);
  cr_expect_eq(e.body.click.at.y, 20);
  cr_expect_eq(e.body.click.button, 1);
  ndec_arena_destroy(&arena);
}

Test(bind_union, string_in_arm) {
  Event e = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Event), &e, "{\"type\":\"key\",\"key\":\"Enter\"}", &arena), 0);
  cr_expect_eq(e.kind, EVT_KEY);
  cr_assert_not_null(e.body.key.key);
  cr_expect_str_eq(e.body.key.key, "Enter");
  ndec_arena_destroy(&arena);
}

typedef struct ListPayload {
  int32_t *items;
  size_t items_len;
} ListPayload;

typedef enum {
  PL_NONE = 0,
  PL_LIST,
} PayloadKind;

typedef union PayloadBody {
  ListPayload list;
} PayloadBody;

typedef struct Payload {
  PayloadKind kind;
  PayloadBody body;
} Payload;

// clang-format off
NDEC_REFLECT(ListPayload,
  FIELD_ARRAY_INT32("items", items, items_len)
);

NDEC_UNION(PayloadArms, PayloadBody,
  ARM("list", PL_LIST, ListPayload, list)
);

NDEC_REFLECT(Payload,
  FIELD_UNION("kind", kind, body, PayloadArms)
);
// clang-format on

Test(bind_union, array_in_arm) {
  Payload p = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Payload), &p, "{\"kind\":\"list\",\"items\":[1,2,3]}", &arena), 0);
  cr_expect_eq(p.kind, PL_LIST);
  cr_assert_eq(p.body.list.items_len, 3);
  cr_expect_eq(p.body.list.items[0], 1);
  cr_expect_eq(p.body.list.items[1], 2);
  cr_expect_eq(p.body.list.items[2], 3);
  ndec_arena_destroy(&arena);
}

typedef struct ShapeReq {
  char *color;
  ShapeKind kind;
  ShapeBody body;
} ShapeReq;

// clang-format off
NDEC_REFLECT(ShapeReq,
  FIELD_STRING(        "color", color),
  FIELD_UNION (ANNOT(REQUIRED), "kind",  kind, body, ShapeArms)
);
// clang-format on

Test(bind_union, required_discriminator_missing) {
  ShapeReq s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc =
      ndec_unmarshal_ex(NDEC_TYPE(ShapeReq), &s, (const uint8_t *)"{\"color\":\"red\"}", 15, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_REQUIRED);
  ndec_arena_destroy(&arena);
}

/* Root array (Go-style: json.Unmarshal(data, &slice)) */

typedef struct {
  int32_t *items;
  size_t len;
} IntSlice;
NDEC_REFLECT_ARRAY(IntSlice, NDEC_KIND_INT32, sizeof(int32_t));

typedef struct {
  char **items;
  size_t len;
} StrSlice;
NDEC_REFLECT_ARRAY(StrSlice, NDEC_KIND_STRING, sizeof(char *));

typedef struct {
  Point *items;
  size_t len;
} PointSlice;
NDEC_REFLECT_ARRAY_STRUCT(PointSlice, Point);

Test(bind_root_array, scalar_int_array) {
  IntSlice s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(IntSlice), &s, "[1,2,3,4,5]", &arena), 0);
  cr_assert_eq(s.len, 5);
  cr_expect_eq(s.items[0], 1);
  cr_expect_eq(s.items[2], 3);
  cr_expect_eq(s.items[4], 5);
  ndec_arena_destroy(&arena);
}

Test(bind_root_array, string_array) {
  StrSlice s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(StrSlice), &s, "[\"a\",\"bb\",\"ccc\"]", &arena), 0);
  cr_assert_eq(s.len, 3);
  cr_expect_str_eq(s.items[0], "a");
  cr_expect_str_eq(s.items[1], "bb");
  cr_expect_str_eq(s.items[2], "ccc");
  ndec_arena_destroy(&arena);
}

Test(bind_root_array, struct_array) {
  PointSlice s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(PointSlice), &s, "[{\"x\":1,\"y\":2},{\"x\":3,\"y\":4}]", &arena), 0);
  cr_assert_eq(s.len, 2);
  cr_expect_eq(s.items[0].x, 1);
  cr_expect_eq(s.items[0].y, 2);
  cr_expect_eq(s.items[1].x, 3);
  cr_expect_eq(s.items[1].y, 4);
  ndec_arena_destroy(&arena);
}

Test(bind_root_array, empty_array) {
  IntSlice s = {(int32_t *)0x1, 99}; /* prove unmarshal overwrites */
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(IntSlice), &s, "[]", &arena), 0);
  cr_expect_eq(s.len, 0);
  cr_expect_null(s.items);
  ndec_arena_destroy(&arena);
}

Test(bind_root_array, type_mismatch) {
  IntSlice s = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc = ndec_unmarshal_ex(NDEC_TYPE(IntSlice), &s, (const uint8_t *)"{\"x\":1}", 7, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_TYPE_MISMATCH);
  ndec_arena_destroy(&arena);
}

/* Root scalar */

Test(bind_root_scalar, int32) {
  int32_t n = -1;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE_INT32, &n, "42", &arena), 0);
  cr_expect_eq(n, 42);
  ndec_arena_destroy(&arena);
}

Test(bind_root_scalar, string) {
  char *s = NULL;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE_STRING, &s, "\"hello\"", &arena), 0);
  cr_assert_not_null(s);
  cr_expect_str_eq(s, "hello");
  ndec_arena_destroy(&arena);
}

Test(bind_root_scalar, bool_true) {
  bool b = false;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE_BOOL, &b, "true", &arena), 0);
  cr_expect(b);
  ndec_arena_destroy(&arena);
}

Test(bind_root_scalar, float64) {
  double d = 0.0;
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE_FLOAT64, &d, "3.14", &arena), 0);
  cr_expect_float_eq(d, 3.14, 1e-9);
  ndec_arena_destroy(&arena);
}

Test(bind_root_scalar, type_mismatch) {
  int32_t n = -1;
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc = ndec_unmarshal_ex(NDEC_TYPE_INT32, &n, (const uint8_t *)"\"not a number\"", 14, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_TYPE_MISMATCH);
  ndec_arena_destroy(&arena);
}

/* Fixed-size C arrays: T[N] inlined into struct */

typedef struct {
  int32_t scores[4];
  Point pts[3];
  char name[16];
  char tiny[4];
} Game;

// clang-format off
NDEC_REFLECT(Game,
  FIELD_FIXED_ARRAY_INT32 ("scores", scores),
  FIELD_FIXED_ARRAY_STRUCT("pts",    pts, Point),
  FIELD_CHAR_ARRAY        ("name",   name),
  FIELD_CHAR_ARRAY        ("tiny",   tiny)
);
// clang-format on

Test(bind_fixed_array, scalar_full) {
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g, "{\"scores\":[10,20,30,40]}", &arena), 0);
  cr_expect_eq(g.scores[0], 10);
  cr_expect_eq(g.scores[1], 20);
  cr_expect_eq(g.scores[2], 30);
  cr_expect_eq(g.scores[3], 40);
  ndec_arena_destroy(&arena);
}

Test(bind_fixed_array, scalar_short_leaves_tail) {
  /* Shorter JSON arrays leave trailing slots at their value at the
   * time the array is processed. r_begin_object zeros the struct on
   * entry, so the tail ends up zero here even though the caller
   * pre-filled before unmarshal. */
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g, "{\"scores\":[1,2]}", &arena), 0);
  cr_expect_eq(g.scores[0], 1);
  cr_expect_eq(g.scores[1], 2);
  cr_expect_eq(g.scores[2], 0);
  cr_expect_eq(g.scores[3], 0);
  ndec_arena_destroy(&arena);
}

Test(bind_fixed_array, scalar_overflow) {
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc =
      ndec_unmarshal_ex(NDEC_TYPE(Game), &g, (const uint8_t *)"{\"scores\":[1,2,3,4,5]}", 22, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_FIXED_OVERFLOW);
  ndec_arena_destroy(&arena);
}

Test(bind_fixed_array, struct_elements) {
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g,
                             "{\"pts\":[{\"x\":1,\"y\":2},{\"x\":3,\"y\":4},{\"x\":5,\"y\":6}]}", &arena),
               0);
  cr_expect_eq(g.pts[0].x, 1);
  cr_expect_eq(g.pts[0].y, 2);
  cr_expect_eq(g.pts[1].x, 3);
  cr_expect_eq(g.pts[2].y, 6);
  ndec_arena_destroy(&arena);
}

Test(bind_char_array, simple) {
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g, "{\"name\":\"alice\"}", &arena), 0);
  cr_expect_str_eq(g.name, "alice");
  ndec_arena_destroy(&arena);
}

Test(bind_char_array, exact_fit) {
  /* tiny[4] holds at most 3 chars + NUL */
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g, "{\"tiny\":\"abc\"}", &arena), 0);
  cr_expect_str_eq(g.tiny, "abc");
  ndec_arena_destroy(&arena);
}

Test(bind_char_array, overflow) {
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  /* tiny[4]: "abcd" needs 5 bytes including NUL → overflow */
  int rc = ndec_unmarshal_ex(NDEC_TYPE(Game), &g, (const uint8_t *)"{\"tiny\":\"abcd\"}", 15, &arena, NULL, &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_FIXED_OVERFLOW);
  ndec_arena_destroy(&arena);
}

Test(bind_char_array, null_writes_empty) {
  /* JSON null on a char[N] writes a single '\0' so the buffer is a
   * valid empty string. */
  Game g = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Game), &g, "{\"name\":null}", &arena), 0);
  cr_expect_str_eq(g.name, "");
  ndec_arena_destroy(&arena);
}

/* Heap arrays: caller-allocated T*+cap+len */

typedef struct {
  int32_t *data;
  size_t cap;
  size_t len;
} HeapInts;

typedef struct {
  Point *pts;
  size_t pts_cap;
  size_t pts_len;
} HeapPts;

typedef struct {
  HeapInts buf;
} HeapHolder;

// clang-format off
NDEC_REFLECT(HeapInts,
  FIELD_HEAP_ARRAY_INT32("data", data, cap, len)
);

NDEC_REFLECT(HeapPts,
  FIELD_HEAP_ARRAY_STRUCT("pts", pts, pts_cap, pts_len, Point)
);
// clang-format on

Test(bind_heap_array, scalar_full) {
  int32_t *buf = malloc(4 * sizeof(int32_t));
  HeapInts h   = {.data = buf, .cap = 4, .len = 99};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(HeapInts), &h, "{\"data\":[10,20,30,40]}", &arena), 0);
  cr_expect_eq(h.cap, 4); /* unchanged */
  cr_expect_eq(h.len, 4);
  cr_expect_eq(buf[0], 10);
  cr_expect_eq(buf[1], 20);
  cr_expect_eq(buf[2], 30);
  cr_expect_eq(buf[3], 40);
  free(buf);
  ndec_arena_destroy(&arena);
}

Test(bind_heap_array, scalar_partial_writes_len) {
  int32_t *buf = malloc(4 * sizeof(int32_t));
  buf[2]       = 777;
  buf[3]       = 888;
  HeapInts h   = {.data = buf, .cap = 4, .len = 99};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(HeapInts), &h, "{\"data\":[1,2]}", &arena), 0);
  cr_expect_eq(h.len, 2);
  cr_expect_eq(buf[0], 1);
  cr_expect_eq(buf[1], 2);
  cr_expect_eq(buf[2], 777); /* tail untouched */
  cr_expect_eq(buf[3], 888);
  free(buf);
  ndec_arena_destroy(&arena);
}

Test(bind_heap_array, scalar_overflow) {
  int32_t *buf = malloc(4 * sizeof(int32_t));
  HeapInts h   = {.data = buf, .cap = 4, .len = 0};
  NdecArena arena;
  ndec_arena_init(&arena);
  NdecUnmarshalError err = {0};
  int rc = ndec_unmarshal_ex(NDEC_TYPE(HeapInts), &h, (const uint8_t *)"{\"data\":[1,2,3,4,5]}", 20, &arena, NULL,
                             &err);
  cr_expect_lt(rc, 0);
  cr_expect_eq(err.code, NDEC_ERR_BIND_FIXED_OVERFLOW);
  free(buf);
  ndec_arena_destroy(&arena);
}

Test(bind_heap_array, struct_elements) {
  Point *buf = malloc(3 * sizeof(Point));
  HeapPts h  = {.pts = buf, .pts_cap = 3, .pts_len = 0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(HeapPts), &h,
                             "{\"pts\":[{\"x\":1,\"y\":2},{\"x\":3,\"y\":4},{\"x\":5,\"y\":6}]}", &arena),
               0);
  cr_expect_eq(h.pts_len, 3);
  cr_expect_eq(buf[0].x, 1);
  cr_expect_eq(buf[1].y, 4);
  cr_expect_eq(buf[2].x, 5);
  free(buf);
  ndec_arena_destroy(&arena);
}

Test(bind_heap_array, null_writes_zero_len) {
  int32_t *buf = malloc(4 * sizeof(int32_t));
  HeapInts h   = {.data = buf, .cap = 4, .len = 99};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(HeapInts), &h, "{\"data\":null}", &arena), 0);
  cr_expect_eq(h.len, 0);
  cr_expect_eq(h.data, buf); /* items pointer untouched */
  free(buf);
  ndec_arena_destroy(&arena);
}

Test(bind_heap_array, empty_array) {
  int32_t *buf = malloc(4 * sizeof(int32_t));
  HeapInts h   = {.data = buf, .cap = 4, .len = 99};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(HeapInts), &h, "{\"data\":[]}", &arena), 0);
  cr_expect_eq(h.len, 0);
  free(buf);
  ndec_arena_destroy(&arena);
}

typedef struct {
  int32_t *items;
  size_t cap;
  size_t len;
} RootIntHeap;
NDEC_REFLECT_HEAP_ARRAY(RootIntHeap, NDEC_KIND_INT32, sizeof(int32_t));

Test(bind_root_heap_array, basic) {
  int32_t *buf  = malloc(10 * sizeof(int32_t));
  RootIntHeap h = {.items = buf, .cap = 10, .len = 0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(RootIntHeap), &h, "[100,200,300]", &arena), 0);
  cr_expect_eq(h.len, 3);
  cr_expect_eq(h.cap, 10); /* unchanged */
  cr_expect_eq(buf[0], 100);
  cr_expect_eq(buf[1], 200);
  cr_expect_eq(buf[2], 300);
  free(buf);
  ndec_arena_destroy(&arena);
}

/* Field lookup: cover the three tiers (bm8k / ph / hm) and aliases. */

typedef struct {
  int32_t a, b, c, d, e, f, g, h;
} Eight;

NDEC_REFLECT(Eight, FIELD_INT32("a", a), FIELD_INT32("b", b), FIELD_INT32("c", c), FIELD_INT32("d", d),
             FIELD_INT32("e", e), FIELD_INT32("f", f), FIELD_INT32("g", g), FIELD_INT32("h", h));

Test(bind_lookup, bm8k_eight_fields) {
  Eight x = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Eight), &x, "{\"a\":1,\"d\":4,\"h\":8}", &arena), 0);
  cr_expect_eq(x.a, 1);
  cr_expect_eq(x.d, 4);
  cr_expect_eq(x.h, 8);
  cr_expect_eq(x.c, 0);
  /* Run twice to exercise the cached lookup path. */
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Eight), &x, "{\"b\":2,\"g\":7}", &arena), 0);
  cr_expect_eq(x.b, 2);
  cr_expect_eq(x.g, 7);
  ndec_arena_destroy(&arena);
}

typedef struct {
  int32_t f00, f01, f02, f03, f04, f05, f06, f07, f08, f09, f10, f11;
} Twelve;

NDEC_REFLECT(Twelve, FIELD_INT32("f00", f00), FIELD_INT32("f01", f01), FIELD_INT32("f02", f02),
             FIELD_INT32("f03", f03), FIELD_INT32("f04", f04), FIELD_INT32("f05", f05), FIELD_INT32("f06", f06),
             FIELD_INT32("f07", f07), FIELD_INT32("f08", f08), FIELD_INT32("f09", f09), FIELD_INT32("f10", f10),
             FIELD_INT32("f11", f11));

Test(bind_lookup, ph_twelve_fields) {
  Twelve x = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Twelve), &x, "{\"f00\":1,\"f05\":6,\"f11\":12,\"f08\":9}", &arena), 0);
  cr_expect_eq(x.f00, 1);
  cr_expect_eq(x.f05, 6);
  cr_expect_eq(x.f11, 12);
  cr_expect_eq(x.f08, 9);
  cr_expect_eq(x.f01, 0);
  ndec_arena_destroy(&arena);
}

typedef struct {
  int32_t v[32];
} BigSchema;

#define F(N) FIELD_INT32("k" #N, v[N])

NDEC_REFLECT(BigSchema, F(0), F(1), F(2), F(3), F(4), F(5), F(6), F(7), F(8), F(9), F(10), F(11), F(12), F(13),
             F(14), F(15), F(16), F(17), F(18), F(19), F(20), F(21), F(22), F(23), F(24), F(25), F(26), F(27),
             F(28), F(29), F(30), F(31));

#undef F

Test(bind_lookup, ph_thirtytwo_fields) {
  BigSchema x = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(BigSchema), &x, "{\"k0\":100,\"k15\":150,\"k31\":310,\"k7\":70}", &arena),
               0);
  cr_expect_eq(x.v[0], 100);
  cr_expect_eq(x.v[15], 150);
  cr_expect_eq(x.v[31], 310);
  cr_expect_eq(x.v[7], 70);
  cr_expect_eq(x.v[1], 0);
  /* Unknown key in non-strict mode is silently skipped. */
  cr_assert_eq(try_unmarshal(NDEC_TYPE(BigSchema), &x, "{\"k99\":999,\"k1\":1}", &arena), 0);
  cr_expect_eq(x.v[1], 1);
  ndec_arena_destroy(&arena);
}

/* Alias is indexed in the same lookup as the primary name; either
 * key matches the same field. */
typedef struct {
  int32_t id;
  char *name;
} Aliased;

NDEC_REFLECT(Aliased, FIELD_INT32(ANNOT(ALIAS("uid")), "id", id),
             FIELD_STRING(ANNOT(ALIAS("user_name")), "name", name));

Test(bind_lookup, alias_indexed) {
  Aliased a = {0};
  NdecArena arena;
  ndec_arena_init(&arena);
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Aliased), &a, "{\"uid\":42,\"user_name\":\"alice\"}", &arena), 0);
  cr_expect_eq(a.id, 42);
  cr_expect_str_eq(a.name, "alice");
  /* Primary name still works too. */
  Aliased b = {0};
  cr_assert_eq(try_unmarshal(NDEC_TYPE(Aliased), &b, "{\"id\":7,\"name\":\"bob\"}", &arena), 0);
  cr_expect_eq(b.id, 7);
  cr_expect_str_eq(b.name, "bob");
  ndec_arena_destroy(&arena);
}
