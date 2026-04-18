/*
 * schema-driven JSON unmarshal on top of ndec kernel
 *
 * The C struct typedef and its JSON schema are written separately:
 *
 *   typedef struct Person {
 *     char *name;
 *     int32_t age;
 *   } Person;
 *
 *   NDEC_REFLECT(Person,
 *     FIELD_STRING("name", name),
 *     FIELD_INT32 (ANNOT(REQUIRED), "age",  age)
 *   );
 *
 * The first arg of every FIELD_* macro is an optional ANNOT(...)
 * annotation block. Omit it entirely when no annotations are needed.
 * Available annotations:
 *
 *   ALIAS("alt_name")  Match this JSON key in addition to the primary
 *                      one. Useful for migration / camelCase aliases.
 *   REQUIRED           Unmarshal fails with NDEC_ERR_BIND_REQUIRED if
 *                      the field is absent from the JSON object.
 *
 * Multiple annotations may be combined: ANNOT(ALIAS("uid"), REQUIRED).
 *
 * Tagged unions (internal-tagged: discriminator + arm fields share
 * the same JSON object):
 *
 *   typedef enum { SHAPE_CIRCLE = 1, SHAPE_RECT } ShapeKind;
 *   typedef struct ShapeCircle { double radius; } ShapeCircle;
 *   typedef struct ShapeRect   { double w, h;  } ShapeRect;
 *   typedef union  ShapeBody {
 *     ShapeCircle circle;
 *     ShapeRect   rect;
 *   } ShapeBody;
 *   typedef struct Shape {
 *     ShapeKind kind;
 *     ShapeBody body;
 *   } Shape;
 *
 *   NDEC_REFLECT(ShapeCircle, FIELD_FLOAT64("radius", radius));
 *   NDEC_REFLECT(ShapeRect,
 *     FIELD_FLOAT64("w", w),
 *     FIELD_FLOAT64("h", h)
 *   );
 *   NDEC_UNION(ShapeArms, ShapeBody,
 *     ARM("circle", SHAPE_CIRCLE, ShapeCircle, circle),
 *     ARM("rect",   SHAPE_RECT,   ShapeRect,   rect)
 *   );
 *   NDEC_REFLECT(Shape,
 *     FIELD_UNION("kind", kind, body, ShapeArms)
 *   );
 *
 * The discriminator must appear before any arm field in the JSON
 * object. Sample JSON: {"kind":"circle","radius":3}.
 *
 * Then unmarshal JSON into an instance of T:
 *
 *   Person out = {0};
 *   NdecArena arena; ndec_arena_init(&arena);
 *   int rc = ndec_unmarshal(NDEC_TYPE(Person), &out, data, len, &arena);
 *   ndec_arena_destroy(&arena);
 *
 * The same entry point also accepts root arrays and root scalars:
 *
 *   typedef struct { int32_t *items; size_t len; } IntSlice;
 *   NDEC_REFLECT_ARRAY(IntSlice, NDEC_KIND_INT32, sizeof(int32_t));
 *   IntSlice xs = {0};
 *   ndec_unmarshal(NDEC_TYPE(IntSlice), &xs, data, len, &arena);
 *
 *   typedef struct { Item *items; size_t len; } ItemSlice;
 *   NDEC_REFLECT_ARRAY_STRUCT(ItemSlice, Item);
 *   ItemSlice items = {0};
 *   ndec_unmarshal(NDEC_TYPE(ItemSlice), &items, data, len, &arena);
 *
 *   int32_t n;
 *   ndec_unmarshal(NDEC_TYPE_INT32, &n, data, len, &arena);
 *
 * All variable-length output (strings, array storage) comes from the
 * caller-provided NdecArena, freed in one shot by ndec_arena_destroy.
 */

#ifndef NDEC_BIND_H
#define NDEC_BIND_H

#include <stdbool.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#include "ndec/core/types.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Forward declaration; impl/field_lookup.h owns the definition. */
typedef struct NdecFieldLookup NdecFieldLookup;

/* ================================================================
 *  Type kinds
 * ================================================================ */

typedef enum NdecKind {
  NDEC_KIND_BOOL = 1,

  NDEC_KIND_INT8,
  NDEC_KIND_INT16,
  NDEC_KIND_INT32,
  NDEC_KIND_INT64,

  NDEC_KIND_UINT8,
  NDEC_KIND_UINT16,
  NDEC_KIND_UINT32,
  NDEC_KIND_UINT64,

  NDEC_KIND_FLOAT32,
  NDEC_KIND_FLOAT64,

  NDEC_KIND_STRING,     /* char *  (NUL-terminated) */
  NDEC_KIND_STRING_LEN, /* char * + size_t len */

  NDEC_KIND_STRUCT,     /* nested struct by value */
  NDEC_KIND_STRUCT_PTR, /* nested struct pointer (arena allocated) */

  NDEC_KIND_ARRAY, /* T* + size_t len, elem kind in elem_kind */

  NDEC_KIND_FIXED_ARRAY,  /* T[N] inline; elem in elem_kind, N in fixed_count */
  NDEC_KIND_FIXED_STRING, /* char[N] inline NUL-terminated; capacity in fixed_count */
  NDEC_KIND_HEAP_ARRAY,   /* caller-allocated T*+cap+len; bind writes elements only */

  NDEC_KIND_UNION, /* tagged union: discriminator string -> arm */
} NdecKind;

/* ================================================================
 *  Type metadata
 * ================================================================ */

typedef struct NdecTypeInfo NdecTypeInfo;

/* Per-field flag bits (set via REQUIRED, etc. annotations). */
enum {
  NDEC_FFLAG_REQUIRED = 1u << 0,
};

typedef struct NdecField {
  const char *name;
  uint16_t name_len;
  NdecKind kind;
  uint32_t offset;          /* offsetof primary field (UNION: discriminator enum) */
  uint32_t len_offset;      /* offsetof length / union body field
                             * (ARRAY / STRING_LEN / UNION; HEAP_ARRAY: cap in) */
  uint32_t aux_offset;      /* HEAP_ARRAY: offsetof len-out field; else unused */
  NdecKind elem_kind;       /* ARRAY/FIXED_ARRAY/HEAP_ARRAY element kind; else unused */
  uint32_t fixed_count;     /* FIXED_ARRAY: declared N; FIXED_STRING: buffer cap */
  const NdecTypeInfo *type; /* STRUCT/STRUCT_PTR/ARRAY-of-struct/FIXED_ARRAY-of-struct/HEAP_ARRAY-of-struct: elem
                             * typeinfo UNION: cast to (const NdecUnionInfo *) for arm table */

  /* Annotation-driven metadata. Defaulted to zero/NULL when no
   * matching annotation is present. */
  const char *alias; /* secondary key; NULL = no alias */
  uint16_t alias_len;
  uint8_t flags; /* bitmask of NDEC_FFLAG_* */
} NdecField;

/* Top-level shape of a type description. Most descriptors describe a
 * struct (NDEC_TI_STRUCT, produced by NDEC_REFLECT). The other two
 * shapes let a root array or root scalar feed ndec_unmarshal directly,
 * mirroring Go's ability to do `json.Unmarshal(data, &slice)` or
 * `json.Unmarshal(data, &n)` through the same entry point. */
typedef enum {
  NDEC_TI_STRUCT = 1,
  NDEC_TI_ARRAY  = 2,
  NDEC_TI_SCALAR = 3,
} NdecTypeInfoKind;

struct NdecTypeInfo {
  const char *name;
  size_t size;
  NdecTypeInfoKind ti_kind;
  union {
    /* NDEC_TI_STRUCT */
    struct {
      const NdecField *fields;
      uint16_t field_count;
      /* Lazy-built field-name lookup table, populated on first
       * r_object_field that targets this type. Atomic so concurrent
       * builds across threads see a consistent install — losers free
       * their build, winners observe the published one. NULL until
       * first use. */
      _Atomic(NdecFieldLookup *) lookup_cache;
    } st;

    /* NDEC_TI_ARRAY: out layout depends on is_heap.
     *   is_heap == false: {void *items; size_t len;}, bind allocates from arena.
     *   is_heap == true:  {void *items; size_t cap; size_t len;}, caller
     *                     pre-allocates items + cap; bind only writes elements
     *                     and the trailing len. */
    struct {
      NdecKind elem_kind;
      const NdecTypeInfo *elem_type; /* NULL for scalar elems */
      size_t elem_size;
      bool is_heap;
    } ar;

    /* NDEC_TI_SCALAR */
    struct {
      NdecKind kind;
    } sc;
  };
};

/* ----------------------------------------------------------------
 *  Tagged union support
 *
 *  An NdecUnionArm describes one alternative inside a C union: the
 *  JSON discriminator string that selects it, the enum value to write
 *  into the parent's discriminator field, the arm's offset within the
 *  union, and the arm's own NdecTypeInfo (so its fields participate
 *  in normal field-by-field unmarshaling once selected). */
typedef struct NdecUnionArm {
  const char *tag; /* JSON discriminator value */
  uint16_t tag_len;
  int64_t enum_value;           /* written into parent's enum field */
  uint32_t arm_offset;          /* offsetof(union_T, arm_field) */
  const NdecTypeInfo *arm_type; /* arm struct's reflection table */
} NdecUnionArm;

typedef struct NdecUnionInfo {
  const char *name;
  const NdecUnionArm *arms;
  uint16_t arm_count;
} NdecUnionInfo;

/* Shorthand to reference a type's info pointer by the C type name. */
#define NDEC_TYPE(T_) (&T_##_info)

/* ================================================================
 *  Declaration macros
 *
 *  Users write:
 *
 *      NDEC_REFLECT(T,
 *        FIELD_*([ANNOT(...),] "key", field [, ...]),
 *        FIELD_*([ANNOT(...),] "key", field [, ...])
 *      );
 *
 *  T appears once. Each FIELD_* macro takes an optional ANNOT(...)
 *  annotation block as its first argument; omit it when no annotations
 *  are needed. Reference the resulting type meta via NDEC_TYPE(T).
 *
 *  Annotations:
 *    ALIAS("name")  add a secondary JSON key
 *    REQUIRED       fail unmarshal if the field is missing
 * ================================================================ */

/* Annotation container. Wraps its contents in parens so the resulting
 * (possibly multi-comma) token sequence can be passed around as a
 * single macro argument. The optional-annotation shim detects this
 * paren wrapper to distinguish "(ANNOT, key, ...)" from "(key, ...)". */
#define ANNOT(...) (__VA_ARGS__)

/* Individual annotation primitives. Each expands to one or more
 * "designator = value" pairs (no trailing comma — the comma between
 * annotations is supplied by the user inside ANNOT(...)). Adding a
 * new annotation is purely a matter of (a) adding a field to NdecField
 * and (b) defining a new macro here that sets that designator. */
#define ALIAS(s_) .alias = (s_), .alias_len = (uint16_t)(sizeof(s_) - 1)
#define REQUIRED  .flags = (uint8_t)NDEC_FFLAG_REQUIRED

/* The wrapper. Builds the field array and the type info struct.
 * Up to NDEC_BIND_MAX_FIELDS_ fields per call. */
#define NDEC_REFLECT(T_, ...)                                                                                     \
  static const NdecField T_##_fields[] = {NDEC__FOR_EACH(T_, __VA_ARGS__)};                                       \
  static NdecTypeInfo T_##_info        = {                                                                        \
      .name    = #T_,                                                                                             \
      .size    = sizeof(T_),                                                                                      \
      .ti_kind = NDEC_TI_STRUCT,                                                                                  \
      .st =                                                                                                       \
          {                                                                                                       \
              .fields      = T_##_fields,                                                                         \
              .field_count = (uint16_t)(sizeof(T_##_fields) / sizeof(T_##_fields[0])),                            \
          },                                                                                                      \
  }

/* Declare a root-array type. The C type SliceT_ MUST have layout
 * `{ ELEM_T *items; size_t len; }` (matching FIELD_ARRAY_*'s in-struct
 * convention). ELEM_KIND_ is one of NDEC_KIND_* for scalars or
 * NDEC_KIND_STRING; for struct elements use NDEC_REFLECT_ARRAY_STRUCT.
 *
 *   NDEC_REFLECT_ARRAY(IntSlice, NDEC_KIND_INT32, sizeof(int32_t));
 */
#define NDEC_REFLECT_ARRAY(SliceT_, ELEM_KIND_, ELEM_SIZE_)                                                       \
  static NdecTypeInfo SliceT_##_info = {                                                                          \
      .name    = #SliceT_,                                                                                        \
      .size    = sizeof(SliceT_),                                                                                 \
      .ti_kind = NDEC_TI_ARRAY,                                                                                   \
      .ar =                                                                                                       \
          {                                                                                                       \
              .elem_kind = (ELEM_KIND_),                                                                          \
              .elem_type = NULL,                                                                                  \
              .elem_size = (ELEM_SIZE_),                                                                          \
          },                                                                                                      \
  }

/* Declare a root-array type whose elements are reflected struct ElemT_.
 * ElemT_ must already have an NDEC_REFLECT block.
 *
 *   NDEC_REFLECT_ARRAY_STRUCT(ItemSlice, Item);
 */
#define NDEC_REFLECT_ARRAY_STRUCT(SliceT_, ElemT_)                                                                \
  static NdecTypeInfo SliceT_##_info = {                                                                          \
      .name    = #SliceT_,                                                                                        \
      .size    = sizeof(SliceT_),                                                                                 \
      .ti_kind = NDEC_TI_ARRAY,                                                                                   \
      .ar =                                                                                                       \
          {                                                                                                       \
              .elem_kind = NDEC_KIND_STRUCT,                                                                      \
              .elem_type = NDEC_TYPE(ElemT_),                                                                     \
              .elem_size = sizeof(ElemT_),                                                                        \
          },                                                                                                      \
  }

/* Declare a root heap-array type. Layout MUST be exactly
 *   { void *items; size_t cap; size_t len; }
 * (or a typed equivalent). Caller pre-allocates items + cap; bind
 * writes elements and the trailing len. */
#define NDEC_REFLECT_HEAP_ARRAY(SliceT_, ELEM_KIND_, ELEM_SIZE_)                                                  \
  static NdecTypeInfo SliceT_##_info = {                                                                          \
      .name    = #SliceT_,                                                                                        \
      .size    = sizeof(SliceT_),                                                                                 \
      .ti_kind = NDEC_TI_ARRAY,                                                                                   \
      .ar =                                                                                                       \
          {                                                                                                       \
              .elem_kind = (ELEM_KIND_),                                                                          \
              .elem_type = NULL,                                                                                  \
              .elem_size = (ELEM_SIZE_),                                                                          \
              .is_heap   = true,                                                                                  \
          },                                                                                                      \
  }

#define NDEC_REFLECT_HEAP_ARRAY_STRUCT(SliceT_, ElemT_)                                                           \
  static NdecTypeInfo SliceT_##_info = {                                                                          \
      .name    = #SliceT_,                                                                                        \
      .size    = sizeof(SliceT_),                                                                                 \
      .ti_kind = NDEC_TI_ARRAY,                                                                                   \
      .ar =                                                                                                       \
          {                                                                                                       \
              .elem_kind = NDEC_KIND_STRUCT,                                                                      \
              .elem_type = NDEC_TYPE(ElemT_),                                                                     \
              .elem_size = sizeof(ElemT_),                                                                        \
              .is_heap   = true,                                                                                  \
          },                                                                                                      \
  }

/* Predefined scalar root descriptors. Use these as the type argument
 * to ndec_unmarshal when the JSON root is a single scalar value:
 *
 *   int32_t n;
 *   ndec_unmarshal(NDEC_TYPE_INT32, &n, data, len, &arena);
 */
extern const NdecTypeInfo ndec_type_bool;
extern const NdecTypeInfo ndec_type_int8;
extern const NdecTypeInfo ndec_type_int16;
extern const NdecTypeInfo ndec_type_int32;
extern const NdecTypeInfo ndec_type_int64;
extern const NdecTypeInfo ndec_type_uint8;
extern const NdecTypeInfo ndec_type_uint16;
extern const NdecTypeInfo ndec_type_uint32;
extern const NdecTypeInfo ndec_type_uint64;
extern const NdecTypeInfo ndec_type_float32;
extern const NdecTypeInfo ndec_type_float64;
extern const NdecTypeInfo ndec_type_string;

#define NDEC_TYPE_BOOL    (&ndec_type_bool)
#define NDEC_TYPE_INT8    (&ndec_type_int8)
#define NDEC_TYPE_INT16   (&ndec_type_int16)
#define NDEC_TYPE_INT32   (&ndec_type_int32)
#define NDEC_TYPE_INT64   (&ndec_type_int64)
#define NDEC_TYPE_UINT8   (&ndec_type_uint8)
#define NDEC_TYPE_UINT16  (&ndec_type_uint16)
#define NDEC_TYPE_UINT32  (&ndec_type_uint32)
#define NDEC_TYPE_UINT64  (&ndec_type_uint64)
#define NDEC_TYPE_FLOAT32 (&ndec_type_float32)
#define NDEC_TYPE_FLOAT64 (&ndec_type_float64)
#define NDEC_TYPE_STRING  (&ndec_type_string)

/* --- Scalar fields --- */
#define FIELD_BOOL(...)    NDEC__FIELD_PLAIN_(NDEC_KIND_BOOL, NULL, __VA_ARGS__)
#define FIELD_INT8(...)    NDEC__FIELD_PLAIN_(NDEC_KIND_INT8, NULL, __VA_ARGS__)
#define FIELD_INT16(...)   NDEC__FIELD_PLAIN_(NDEC_KIND_INT16, NULL, __VA_ARGS__)
#define FIELD_INT32(...)   NDEC__FIELD_PLAIN_(NDEC_KIND_INT32, NULL, __VA_ARGS__)
#define FIELD_INT64(...)   NDEC__FIELD_PLAIN_(NDEC_KIND_INT64, NULL, __VA_ARGS__)
#define FIELD_UINT8(...)   NDEC__FIELD_PLAIN_(NDEC_KIND_UINT8, NULL, __VA_ARGS__)
#define FIELD_UINT16(...)  NDEC__FIELD_PLAIN_(NDEC_KIND_UINT16, NULL, __VA_ARGS__)
#define FIELD_UINT32(...)  NDEC__FIELD_PLAIN_(NDEC_KIND_UINT32, NULL, __VA_ARGS__)
#define FIELD_UINT64(...)  NDEC__FIELD_PLAIN_(NDEC_KIND_UINT64, NULL, __VA_ARGS__)
#define FIELD_FLOAT32(...) NDEC__FIELD_PLAIN_(NDEC_KIND_FLOAT32, NULL, __VA_ARGS__)
#define FIELD_FLOAT64(...) NDEC__FIELD_PLAIN_(NDEC_KIND_FLOAT64, NULL, __VA_ARGS__)

/* --- Strings --- */
#define FIELD_STRING(...) NDEC__FIELD_PLAIN_(NDEC_KIND_STRING, NULL, __VA_ARGS__)

#define FIELD_STRING_LEN(...) NDEC__FIELD_LEN_(NDEC_KIND_STRING_LEN, 0, NULL, __VA_ARGS__)

/* --- Nested struct --- */
#define FIELD_STRUCT(...)     NDEC__FIELD_STRUCT_(NDEC_KIND_STRUCT, __VA_ARGS__)
#define FIELD_STRUCT_PTR(...) NDEC__FIELD_STRUCT_(NDEC_KIND_STRUCT_PTR, __VA_ARGS__)

/* --- Arrays of scalars --- */
#define FIELD_ARRAY_BOOL(...)    NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_BOOL, NULL, __VA_ARGS__)
#define FIELD_ARRAY_INT8(...)    NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_INT8, NULL, __VA_ARGS__)
#define FIELD_ARRAY_INT16(...)   NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_INT16, NULL, __VA_ARGS__)
#define FIELD_ARRAY_INT32(...)   NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_INT32, NULL, __VA_ARGS__)
#define FIELD_ARRAY_INT64(...)   NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_INT64, NULL, __VA_ARGS__)
#define FIELD_ARRAY_UINT8(...)   NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_UINT8, NULL, __VA_ARGS__)
#define FIELD_ARRAY_UINT16(...)  NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_UINT16, NULL, __VA_ARGS__)
#define FIELD_ARRAY_UINT32(...)  NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_UINT32, NULL, __VA_ARGS__)
#define FIELD_ARRAY_UINT64(...)  NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_UINT64, NULL, __VA_ARGS__)
#define FIELD_ARRAY_FLOAT32(...) NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_FLOAT32, NULL, __VA_ARGS__)
#define FIELD_ARRAY_FLOAT64(...) NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_FLOAT64, NULL, __VA_ARGS__)
#define FIELD_ARRAY_STRING(...)  NDEC__FIELD_LEN_(NDEC_KIND_ARRAY, NDEC_KIND_STRING, NULL, __VA_ARGS__)

/* --- Array of structs --- */
#define FIELD_ARRAY_STRUCT(...) NDEC__FIELD_ARRAY_STRUCT_(__VA_ARGS__)

/* --- Fixed-size C arrays: T[N] inlined into the parent struct ---
 *
 *   FIELD_FIXED_ARRAY_INT32("scores", scores)         // int32_t scores[N]
 *   FIELD_FIXED_ARRAY_STRUCT("pts", pts, Point)       // Point pts[N]
 *
 * N is auto-derived via sizeof; do not pass it. JSON arrays longer
 * than N error with NDEC_ERR_BIND_FIXED_OVERFLOW; shorter JSON arrays
 * leave the trailing slots untouched (callers typically pre-zero). */
#define FIELD_FIXED_ARRAY_BOOL(...)    NDEC__FIELD_FIXED_(NDEC_KIND_BOOL, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_INT8(...)    NDEC__FIELD_FIXED_(NDEC_KIND_INT8, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_INT16(...)   NDEC__FIELD_FIXED_(NDEC_KIND_INT16, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_INT32(...)   NDEC__FIELD_FIXED_(NDEC_KIND_INT32, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_INT64(...)   NDEC__FIELD_FIXED_(NDEC_KIND_INT64, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_UINT8(...)   NDEC__FIELD_FIXED_(NDEC_KIND_UINT8, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_UINT16(...)  NDEC__FIELD_FIXED_(NDEC_KIND_UINT16, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_UINT32(...)  NDEC__FIELD_FIXED_(NDEC_KIND_UINT32, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_UINT64(...)  NDEC__FIELD_FIXED_(NDEC_KIND_UINT64, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_FLOAT32(...) NDEC__FIELD_FIXED_(NDEC_KIND_FLOAT32, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_FLOAT64(...) NDEC__FIELD_FIXED_(NDEC_KIND_FLOAT64, NULL, __VA_ARGS__)
#define FIELD_FIXED_ARRAY_STRING(...)  NDEC__FIELD_FIXED_(NDEC_KIND_STRING, NULL, __VA_ARGS__)

#define FIELD_FIXED_ARRAY_STRUCT(...) NDEC__FIELD_FIXED_STRUCT_(__VA_ARGS__)

/* --- char[N] inlined NUL-terminated string ---
 *
 *   FIELD_CHAR_ARRAY("name", name)   // char name[N], string + NUL must fit
 *
 * JSON strings whose byte length + 1 (for NUL) exceeds N error with
 * NDEC_ERR_BIND_FIXED_OVERFLOW. JSON null writes a single '\0' byte.
 * Distinct from FIELD_FIXED_ARRAY_INT8 because the semantic is "char
 * buffer holding a string" rather than "byte array". */
#define FIELD_CHAR_ARRAY(...) NDEC__FIELD_CHARARRAY_(__VA_ARGS__)

/* --- Heap arrays: caller-allocated T*+cap+len ---
 *
 * Use when the caller wants full control of element storage (own
 * malloc/free, custom allocator) but still wants bind to drive the
 * write loop and report how many elements actually came in. Layout:
 *
 *   typedef struct {
 *     T *items;     // caller-allocated, e.g. malloc(cap*sizeof(T))
 *     size_t cap;   // IN  : capacity caller is willing to accept
 *     size_t len;   // OUT : actual element count written
 *   } MyHeap;
 *
 *   FIELD_HEAP_ARRAY_INT32("data", items, cap, len)
 *   FIELD_HEAP_ARRAY_STRUCT("pts", pts, pts_cap, pts_len, Point)
 *
 * JSON arrays longer than `cap` error with NDEC_ERR_BIND_FIXED_OVERFLOW.
 * JSON null writes len = 0 and leaves items untouched. */
#define FIELD_HEAP_ARRAY_BOOL(...)    NDEC__FIELD_HEAP_(NDEC_KIND_BOOL, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_INT8(...)    NDEC__FIELD_HEAP_(NDEC_KIND_INT8, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_INT16(...)   NDEC__FIELD_HEAP_(NDEC_KIND_INT16, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_INT32(...)   NDEC__FIELD_HEAP_(NDEC_KIND_INT32, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_INT64(...)   NDEC__FIELD_HEAP_(NDEC_KIND_INT64, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_UINT8(...)   NDEC__FIELD_HEAP_(NDEC_KIND_UINT8, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_UINT16(...)  NDEC__FIELD_HEAP_(NDEC_KIND_UINT16, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_UINT32(...)  NDEC__FIELD_HEAP_(NDEC_KIND_UINT32, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_UINT64(...)  NDEC__FIELD_HEAP_(NDEC_KIND_UINT64, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_FLOAT32(...) NDEC__FIELD_HEAP_(NDEC_KIND_FLOAT32, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_FLOAT64(...) NDEC__FIELD_HEAP_(NDEC_KIND_FLOAT64, NULL, __VA_ARGS__)
#define FIELD_HEAP_ARRAY_STRING(...)  NDEC__FIELD_HEAP_(NDEC_KIND_STRING, NULL, __VA_ARGS__)

#define FIELD_HEAP_ARRAY_STRUCT(...) NDEC__FIELD_HEAP_STRUCT_(__VA_ARGS__)

/* --- Tagged union ---
 *
 *   FIELD_UNION([ANNOT(...),] "json_key", kind_field, union_field, ArmsName)
 *
 *   json_key     JSON discriminator key, e.g. "type" or "kind"
 *   kind_field   parent struct field receiving the enum value
 *   union_field  parent struct field that holds the C union
 *   ArmsName     name passed to NDEC_UNION (resolves to the arm table)
 */
#define FIELD_UNION(...) NDEC__FIELD_UNION_(__VA_ARGS__)

/* --- Union arm table ---
 *
 *   NDEC_UNION(ArmsName, UnionT,
 *     ARM("tag1", ENUM_VAL_1, ArmType1, union_field_1),
 *     ARM("tag2", ENUM_VAL_2, ArmType2, union_field_2)
 *   );
 *
 *   UnionT     the C union typedef (e.g. ShapeBody)
 *   ArmType*   each arm's struct type, declared via NDEC_REFLECT
 *   union_field_*  the union member name housing this arm
 */
#define NDEC_UNION(NAME_, UnionT_, ...)                                                                           \
  static const NdecUnionArm NAME_##_arms[]      = {NDEC__FOR_EACH(UnionT_, __VA_ARGS__)};                         \
  static const NdecUnionInfo NAME_##_union_info = {                                                               \
      #NAME_,                                                                                                     \
      NAME_##_arms,                                                                                               \
      (uint16_t)(sizeof(NAME_##_arms) / sizeof(NAME_##_arms[0])),                                                 \
  }

#define ARM(tag_, enum_val_, ArmType_, union_field_) NDEC__TUP_ARM_(tag_, enum_val_, ArmType_, union_field_)

/* ================================================================
 *  Internal macro implementation (not user-facing)
 *
 *  Each FIELD_* macro above expands to a parenthesized tuple whose
 *  first element names the per-shape emitter that knows how to turn
 *  the rest of the tuple into an NdecField struct literal once the
 *  owning type T_ becomes available. NDEC__FOR_EACH walks the tuple
 *  list and threads T_ into each emitter call.
 * ================================================================ */

#define NDEC__TUP_PLAIN_(annot_, kind_, key_, f_, tp_) (NDEC__EMIT_PLAIN_, annot_, kind_, key_, f_, tp_)

#define NDEC__TUP_LEN_(annot_, kind_, key_, f_, lf_, ek_, tp_)                                                    \
  (NDEC__EMIT_LEN_, annot_, kind_, key_, f_, lf_, ek_, tp_)

#define NDEC__TUP_UNION_(annot_, key_, kind_f_, union_f_, ArmsName_)                                              \
  (NDEC__EMIT_UNION_, annot_, key_, kind_f_, union_f_, ArmsName_)

/* User-facing FIELD_* shims: normalize the optional ANNOT(...) prefix
 * (NDEC__NORMALIZE_ injects an empty () block when omitted) and then
 * splice the normalized args into the matching NDEC__TUP_* macro.
 * The two-step indirection forces __VA_ARGS__ to be re-scanned so the
 * normalized output is split into the named parameters. */
#define NDEC__FIELD_PLAIN_(kind_, tp_, ...)   NDEC__FIELD_PLAIN_I_(kind_, tp_, NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_PLAIN_I_(kind_, tp_, ...) NDEC__FIELD_PLAIN_II_(kind_, tp_, __VA_ARGS__)
#define NDEC__FIELD_PLAIN_II_(kind_, tp_, annot_, key_, f_) NDEC__TUP_PLAIN_(annot_, kind_, key_, f_, tp_)

#define NDEC__FIELD_LEN_(kind_, ek_, tp_, ...)   NDEC__FIELD_LEN_I_(kind_, ek_, tp_, NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_LEN_I_(kind_, ek_, tp_, ...) NDEC__FIELD_LEN_II_(kind_, ek_, tp_, __VA_ARGS__)
#define NDEC__FIELD_LEN_II_(kind_, ek_, tp_, annot_, key_, f_, lf_)                                               \
  NDEC__TUP_LEN_(annot_, kind_, key_, f_, lf_, ek_, tp_)

#define NDEC__FIELD_UNION_(...)   NDEC__FIELD_UNION_I_(NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_UNION_I_(...) NDEC__FIELD_UNION_II_(__VA_ARGS__)
#define NDEC__FIELD_UNION_II_(annot_, key_, kind_f_, union_f_, ArmsName_)                                         \
  NDEC__TUP_UNION_(annot_, key_, kind_f_, union_f_, ArmsName_)

/* Struct shims: like NDEC__FIELD_PLAIN_ but the user's last positional
 * arg is an inner type name that we wrap in NDEC_TYPE() before storing. */
#define NDEC__FIELD_STRUCT_(kind_, ...)   NDEC__FIELD_STRUCT_I_(kind_, NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_STRUCT_I_(kind_, ...) NDEC__FIELD_STRUCT_II_(kind_, __VA_ARGS__)
#define NDEC__FIELD_STRUCT_II_(kind_, annot_, key_, f_, InnerT_)                                                  \
  NDEC__TUP_PLAIN_(annot_, kind_, key_, f_, NDEC_TYPE(InnerT_))

#define NDEC__FIELD_ARRAY_STRUCT_(...)   NDEC__FIELD_ARRAY_STRUCT_I_(NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_ARRAY_STRUCT_I_(...) NDEC__FIELD_ARRAY_STRUCT_II_(__VA_ARGS__)
#define NDEC__FIELD_ARRAY_STRUCT_II_(annot_, key_, f_, lf_, ElemT_)                                               \
  NDEC__TUP_LEN_(annot_, NDEC_KIND_ARRAY, key_, f_, lf_, NDEC_KIND_STRUCT, NDEC_TYPE(ElemT_))

/* Fixed-array shims. Carry kind = NDEC_KIND_FIXED_ARRAY plus the
 * elem_kind chosen by the user-facing macro; N is recovered from
 * sizeof at emit time, so users supply only key + field name. */
#define NDEC__FIELD_FIXED_(ek_, tp_, ...)   NDEC__FIELD_FIXED_I_(ek_, tp_, NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_FIXED_I_(ek_, tp_, ...) NDEC__FIELD_FIXED_II_(ek_, tp_, __VA_ARGS__)
#define NDEC__FIELD_FIXED_II_(ek_, tp_, annot_, key_, f_) NDEC__TUP_FIXED_(annot_, key_, f_, ek_, tp_)

#define NDEC__FIELD_FIXED_STRUCT_(...)   NDEC__FIELD_FIXED_STRUCT_I_(NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_FIXED_STRUCT_I_(...) NDEC__FIELD_FIXED_STRUCT_II_(__VA_ARGS__)
#define NDEC__FIELD_FIXED_STRUCT_II_(annot_, key_, f_, ElemT_)                                                    \
  NDEC__TUP_FIXED_(annot_, key_, f_, NDEC_KIND_STRUCT, NDEC_TYPE(ElemT_))

#define NDEC__FIELD_CHARARRAY_(...)                 NDEC__FIELD_CHARARRAY_I_(NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_CHARARRAY_I_(...)               NDEC__FIELD_CHARARRAY_II_(__VA_ARGS__)
#define NDEC__FIELD_CHARARRAY_II_(annot_, key_, f_) NDEC__TUP_CHARARRAY_(annot_, key_, f_)

#define NDEC__TUP_FIXED_(annot_, key_, f_, ek_, tp_) (NDEC__EMIT_FIXED_, annot_, key_, f_, ek_, tp_)

#define NDEC__TUP_CHARARRAY_(annot_, key_, f_) (NDEC__EMIT_CHARARRAY_, annot_, key_, f_)

/* Heap-array shims. Carry kind = NDEC_KIND_HEAP_ARRAY plus the user's
 * (items, cap, len) field triple. The struct variant additionally
 * carries the elem type. */
#define NDEC__FIELD_HEAP_(ek_, tp_, ...)   NDEC__FIELD_HEAP_I_(ek_, tp_, NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_HEAP_I_(ek_, tp_, ...) NDEC__FIELD_HEAP_II_(ek_, tp_, __VA_ARGS__)
#define NDEC__FIELD_HEAP_II_(ek_, tp_, annot_, key_, items_f_, cap_f_, len_f_)                                    \
  NDEC__TUP_HEAP_(annot_, key_, items_f_, cap_f_, len_f_, ek_, tp_)

#define NDEC__FIELD_HEAP_STRUCT_(...)   NDEC__FIELD_HEAP_STRUCT_I_(NDEC__NORMALIZE_(__VA_ARGS__))
#define NDEC__FIELD_HEAP_STRUCT_I_(...) NDEC__FIELD_HEAP_STRUCT_II_(__VA_ARGS__)
#define NDEC__FIELD_HEAP_STRUCT_II_(annot_, key_, items_f_, cap_f_, len_f_, ElemT_)                               \
  NDEC__TUP_HEAP_(annot_, key_, items_f_, cap_f_, len_f_, NDEC_KIND_STRUCT, NDEC_TYPE(ElemT_))

#define NDEC__TUP_HEAP_(annot_, key_, items_f_, cap_f_, len_f_, ek_, tp_)                                         \
  (NDEC__EMIT_HEAP_, annot_, key_, items_f_, cap_f_, len_f_, ek_, tp_)

/* ARM tuples flow through NDEC__FOR_EACH inside NDEC_UNION; the
 * macro injects the union typedef as the first emitter argument. */
#define NDEC__TUP_ARM_(tag_, ev_, at_, uf_) (NDEC__EMIT_ARM_, tag_, ev_, at_, uf_)

/* Emitters. Each builds an NdecField struct literal using designated
 * initializers, then splices the annotation block in. The annotation
 * block arrives wrapped in parens (built by ANNOT(...)); NDEC__OPEN_
 * unwraps it back into a comma-separated list of designators that
 * extend the initializer. Unset designators stay at their {0}-default. */
#define NDEC__EMIT_PLAIN_(T_, annot_, kind_, key_, f_, tp_)                                                       \
  {.name     = (key_),                                                                                            \
   .name_len = (uint16_t)(sizeof(key_) - 1),                                                                      \
   .kind     = (kind_),                                                                                           \
   .offset   = (uint32_t)offsetof(T_, f_),                                                                        \
   .type     = (tp_),                                                                                             \
   NDEC__OPEN_ annot_},

#define NDEC__EMIT_LEN_(T_, annot_, kind_, key_, f_, lf_, ek_, tp_)                                               \
  {.name       = (key_),                                                                                          \
   .name_len   = (uint16_t)(sizeof(key_) - 1),                                                                    \
   .kind       = (kind_),                                                                                         \
   .offset     = (uint32_t)offsetof(T_, f_),                                                                      \
   .len_offset = (uint32_t)offsetof(T_, lf_),                                                                     \
   .elem_kind  = (ek_),                                                                                           \
   .type       = (tp_),                                                                                           \
   NDEC__OPEN_ annot_},

/* UNION emitter: stash discriminator offset in .offset, union body
 * offset in .len_offset, and the arm table cast as a NdecTypeInfo *
 * in .type (runtime casts back when kind == NDEC_KIND_UNION). */
#define NDEC__EMIT_UNION_(T_, annot_, key_, kind_f_, union_f_, ArmsName_)                                         \
  {.name       = (key_),                                                                                          \
   .name_len   = (uint16_t)(sizeof(key_) - 1),                                                                    \
   .kind       = NDEC_KIND_UNION,                                                                                 \
   .offset     = (uint32_t)offsetof(T_, kind_f_),                                                                 \
   .len_offset = (uint32_t)offsetof(T_, union_f_),                                                                \
   .type       = (const NdecTypeInfo *)&ArmsName_##_union_info,                                                   \
   NDEC__OPEN_ annot_},

/* FIXED_ARRAY emitter: T[N] inlined in T_. fixed_count is recovered
 * from sizeof(field) / sizeof(field[0]) at compile time. type is
 * NULL for scalar elem kinds, NDEC_TYPE(ElemT) for struct elems. */
#define NDEC__EMIT_FIXED_(T_, annot_, key_, f_, ek_, tp_)                                                         \
  {.name        = (key_),                                                                                         \
   .name_len    = (uint16_t)(sizeof(key_) - 1),                                                                   \
   .kind        = NDEC_KIND_FIXED_ARRAY,                                                                          \
   .offset      = (uint32_t)offsetof(T_, f_),                                                                     \
   .elem_kind   = (ek_),                                                                                          \
   .fixed_count = (uint32_t)(sizeof(((T_ *)0)->f_) / sizeof(((T_ *)0)->f_[0])),                                   \
   .type        = (tp_),                                                                                          \
   NDEC__OPEN_ annot_},

/* CHAR_ARRAY emitter: char[N] holding a NUL-terminated string. */
#define NDEC__EMIT_CHARARRAY_(T_, annot_, key_, f_)                                                               \
  {.name        = (key_),                                                                                         \
   .name_len    = (uint16_t)(sizeof(key_) - 1),                                                                   \
   .kind        = NDEC_KIND_FIXED_STRING,                                                                         \
   .offset      = (uint32_t)offsetof(T_, f_),                                                                     \
   .fixed_count = (uint32_t)sizeof(((T_ *)0)->f_),                                                                \
   NDEC__OPEN_ annot_},

/* HEAP_ARRAY emitter: caller-allocated T*+cap+len triple. .offset is
 * the items pointer field, .len_offset is cap (input), .aux_offset is
 * len (output), .elem_kind/.type describe the element. */
#define NDEC__EMIT_HEAP_(T_, annot_, key_, items_f_, cap_f_, len_f_, ek_, tp_)                                    \
  {.name       = (key_),                                                                                          \
   .name_len   = (uint16_t)(sizeof(key_) - 1),                                                                    \
   .kind       = NDEC_KIND_HEAP_ARRAY,                                                                            \
   .offset     = (uint32_t)offsetof(T_, items_f_),                                                                \
   .len_offset = (uint32_t)offsetof(T_, cap_f_),                                                                  \
   .aux_offset = (uint32_t)offsetof(T_, len_f_),                                                                  \
   .elem_kind  = (ek_),                                                                                           \
   .type       = (tp_),                                                                                           \
   NDEC__OPEN_ annot_},

/* ARM emitter: UnionT_ is supplied by NDEC__FOR_EACH inside NDEC_UNION. */
#define NDEC__EMIT_ARM_(UnionT_, tag_, ev_, ArmType_, union_field_)                                               \
  {                                                                                                               \
      .tag        = (tag_),                                                                                       \
      .tag_len    = (uint16_t)(sizeof(tag_) - 1),                                                                 \
      .enum_value = (int64_t)(ev_),                                                                               \
      .arm_offset = (uint32_t)offsetof(UnionT_, union_field_),                                                    \
      .arm_type   = NDEC_TYPE(ArmType_),                                                                          \
  },

/* Open a parenthesized tuple back into a plain comma-separated list. */
#define NDEC__OPEN_(...) __VA_ARGS__

/* Apply the tuple's emitter (the first element of the tuple) to
 * (T_, rest_of_tuple_elements). Two indirection steps so __VA_ARGS__
 * is rescanned and the tuple is fully unpacked before NDEC__APPLY_II_
 * sees its arguments. */
#define NDEC__APPLY_(T_, tup_)          NDEC__APPLY_I_(T_, NDEC__OPEN_ tup_)
#define NDEC__APPLY_I_(T_, ...)         NDEC__APPLY_II_(T_, __VA_ARGS__)
#define NDEC__APPLY_II_(T_, EMIT_, ...) EMIT_(T_, __VA_ARGS__)

/* ---------- FOR_EACH over the field-tuple list (cap 32) ---------- */

#define NDEC__CAT_(a_, b_)   NDEC__CAT_I_(a_, b_)
#define NDEC__CAT_I_(a_, b_) a_##b_

/* ---------- ANNOT-optional normalization ----------
 *
 * Each user-facing FIELD_* accepts the ANNOT(...) block as an optional
 * first argument. The detection trick: ANNOT(X) expands to (X), which
 * is a parenthesized token; a bare key literal "name" is not.
 *
 * NDEC__IS_PAREN_(x) returns 1 iff x is parenthesized. We then dispatch
 * NDEC__NORMALIZE_ to either pass the args through (already has ANNOT)
 * or prepend an empty () annotation block. */
#define NDEC__IS_PAREN_(x)              NDEC__IS_PAREN_PROBE_(NDEC__IS_PAREN_TAG_ x)
#define NDEC__IS_PAREN_PROBE_(...)      NDEC__IS_PAREN_PICK_(__VA_ARGS__, 0)
#define NDEC__IS_PAREN_PICK_(a, b, ...) b
#define NDEC__IS_PAREN_TAG_(...)        ~, 1

#define NDEC__FIRST_(x, ...) x

#define NDEC__NORMALIZE_(...) NDEC__CAT_(NDEC__NORMALIZE_, NDEC__IS_PAREN_(NDEC__FIRST_(__VA_ARGS__)))(__VA_ARGS__)
#define NDEC__NORMALIZE_1(...) __VA_ARGS__     /* already (ANNOT, ...) */
#define NDEC__NORMALIZE_0(...) (), __VA_ARGS__ /* prepend empty ANNOT */

#define NDEC__NARG_(...)   NDEC__NARG_I_(__VA_ARGS__, NDEC__RSEQ_())
#define NDEC__NARG_I_(...) NDEC__GET_33RD_(__VA_ARGS__)
#define NDEC__GET_33RD_(_1, _2, _3, _4, _5, _6, _7, _8, _9, _10, _11, _12, _13, _14, _15, _16, _17, _18, _19,     \
                        _20, _21, _22, _23, _24, _25, _26, _27, _28, _29, _30, _31, _32, N, ...)                  \
  N
#define NDEC__RSEQ_()                                                                                             \
  32, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4,   \
      3, 2, 1, 0

#define NDEC__FOR_EACH(T_, ...) NDEC__CAT_(NDEC__FE_, NDEC__NARG_(__VA_ARGS__))(T_, __VA_ARGS__)

#define NDEC__FE_1(T_, X)       NDEC__APPLY_(T_, X)
#define NDEC__FE_2(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_1(T_, __VA_ARGS__)
#define NDEC__FE_3(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_2(T_, __VA_ARGS__)
#define NDEC__FE_4(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_3(T_, __VA_ARGS__)
#define NDEC__FE_5(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_4(T_, __VA_ARGS__)
#define NDEC__FE_6(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_5(T_, __VA_ARGS__)
#define NDEC__FE_7(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_6(T_, __VA_ARGS__)
#define NDEC__FE_8(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_7(T_, __VA_ARGS__)
#define NDEC__FE_9(T_, X, ...)  NDEC__APPLY_(T_, X) NDEC__FE_8(T_, __VA_ARGS__)
#define NDEC__FE_10(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_9(T_, __VA_ARGS__)
#define NDEC__FE_11(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_10(T_, __VA_ARGS__)
#define NDEC__FE_12(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_11(T_, __VA_ARGS__)
#define NDEC__FE_13(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_12(T_, __VA_ARGS__)
#define NDEC__FE_14(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_13(T_, __VA_ARGS__)
#define NDEC__FE_15(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_14(T_, __VA_ARGS__)
#define NDEC__FE_16(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_15(T_, __VA_ARGS__)
#define NDEC__FE_17(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_16(T_, __VA_ARGS__)
#define NDEC__FE_18(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_17(T_, __VA_ARGS__)
#define NDEC__FE_19(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_18(T_, __VA_ARGS__)
#define NDEC__FE_20(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_19(T_, __VA_ARGS__)
#define NDEC__FE_21(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_20(T_, __VA_ARGS__)
#define NDEC__FE_22(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_21(T_, __VA_ARGS__)
#define NDEC__FE_23(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_22(T_, __VA_ARGS__)
#define NDEC__FE_24(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_23(T_, __VA_ARGS__)
#define NDEC__FE_25(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_24(T_, __VA_ARGS__)
#define NDEC__FE_26(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_25(T_, __VA_ARGS__)
#define NDEC__FE_27(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_26(T_, __VA_ARGS__)
#define NDEC__FE_28(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_27(T_, __VA_ARGS__)
#define NDEC__FE_29(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_28(T_, __VA_ARGS__)
#define NDEC__FE_30(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_29(T_, __VA_ARGS__)
#define NDEC__FE_31(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_30(T_, __VA_ARGS__)
#define NDEC__FE_32(T_, X, ...) NDEC__APPLY_(T_, X) NDEC__FE_31(T_, __VA_ARGS__)

/* ================================================================
 *  Arena
 * ================================================================ */

typedef struct NdecArenaChunk NdecArenaChunk;

typedef struct NdecArena {
  NdecArenaChunk *head;
  size_t chunk_size_hint;
} NdecArena;

/* Initialize an empty arena. Does not allocate. Cannot fail. */
void ndec_arena_init(NdecArena *a);

/* Release every chunk owned by the arena. Idempotent. */
void ndec_arena_destroy(NdecArena *a);

/* Keep chunks, reset bump cursors to beginning. Safe to reuse. */
void ndec_arena_reset(NdecArena *a);

/* Allocate `n` bytes with max_align_t-compatible alignment.
 * Returns NULL on OOM. Calling with n == 0 returns a non-NULL sentinel
 * (don't dereference), making it safe to use result as an "empty but
 * not-null" marker. */
void *ndec_arena_alloc(NdecArena *a, size_t n);

/* Allocate `len + 1` bytes, memcpy `src[0..len)` in, NUL-terminate.
 * Returns NULL on OOM. */
char *ndec_arena_memdup_z(NdecArena *a, const char *src, size_t len);

/* ================================================================
 *  Unmarshal
 * ================================================================ */

/* Error codes. Kernel codes 0..6 are reused (NDEC_OK..NDEC_ERR_TRAILING).
 * Bind-specific codes start at 32. These values populate
 * NdecUnmarshalError.code; the unmarshal function itself returns 0 on
 * success and a negative value on failure. */
enum {
  NDEC_ERR_BIND_TYPE_MISMATCH   = 32,
  NDEC_ERR_BIND_NUMBER_RANGE    = 33,
  NDEC_ERR_BIND_UNKNOWN_FIELD   = 34,
  NDEC_ERR_BIND_OOM             = 35,
  NDEC_ERR_BIND_REQUIRED        = 36, /* a REQUIRED field was absent */
  NDEC_ERR_BIND_UNION_BAD_TAG   = 37, /* discriminator value not in arm table */
  NDEC_ERR_BIND_UNION_DISC_LATE = 38, /* arm field arrived before discriminator */
  NDEC_ERR_BIND_FIXED_OVERFLOW  = 39, /* JSON exceeded a FIXED_ARRAY/FIXED_STRING capacity */
};

typedef struct NdecUnmarshalOpts {
  int strict; /* 1: unknown field -> ERROR; 0: silently SKIP */
} NdecUnmarshalOpts;

typedef struct NdecUnmarshalError {
  int code;            /* positive kernel/bind code, 0 on success */
  size_t pos;          /* JSON byte offset, 0 if unknown */
  const char *message; /* static literal; do not free */
} NdecUnmarshalError;

/* Returns 0 on success, a negative value on failure.
 * On failure, the contents of `out` are unspecified; discard the arena
 * or reset it before reuse. */
int ndec_unmarshal(const NdecTypeInfo *type, void *out, const uint8_t *data, size_t len, NdecArena *arena);

/* Extended entry point with options and optional error detail. */
int ndec_unmarshal_ex(const NdecTypeInfo *type, void *out, const uint8_t *data, size_t len, NdecArena *arena,
                      const NdecUnmarshalOpts *opts, /* NULL = defaults */
                      NdecUnmarshalError *err);      /* NULL = discarded */

#ifdef __cplusplus
}
#endif

#endif /* NDEC_BIND_H */
