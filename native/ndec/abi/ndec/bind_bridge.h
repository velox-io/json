/*
 * NdecBindBridge is the shared ABI prefix of every bind machine. Go allocates
 * the machine in noscan storage, so pointer fields do not retain their referents;
 * typed owners and KeepAlive calls preserve reachability through every yield.
 * C reads the context, advances allocator cursors, and publishes yield requests.
 * Core.Phase selects the exact C continuation on reentry.
 */
#ifndef NDEC_BIND_BRIDGE_H
#define NDEC_BIND_BRIDGE_H

#include <stddef.h>
#include <stdint.h>

/* Values must match vbind.Kind and typ.ElemTypeKind. */
typedef enum {
  BIND_KIND_BOOL             = 1,
  BIND_KIND_INT              = 2,
  BIND_KIND_INT8             = 3,
  BIND_KIND_INT16            = 4,
  BIND_KIND_INT32            = 5,
  BIND_KIND_INT64            = 6,
  BIND_KIND_UINT             = 7,
  BIND_KIND_UINT8            = 8,
  BIND_KIND_UINT16           = 9,
  BIND_KIND_UINT32           = 10,
  BIND_KIND_UINT64           = 11,
  BIND_KIND_FLOAT32          = 12,
  BIND_KIND_FLOAT64          = 13,
  BIND_KIND_STRING           = 14,
  BIND_KIND_STRUCT           = 15,
  BIND_KIND_SLICE            = 16,
  BIND_KIND_PTR              = 17,
  BIND_KIND_ANY              = 18,
  BIND_KIND_MAP              = 19,
  BIND_KIND_RAW_MESSAGE      = 20,
  BIND_KIND_NUMBER           = 21,
  BIND_KIND_ARRAY            = 22,
  BIND_KIND_IFACE            = 23,
  BIND_KIND_UNMARSHALER      = 24,
  BIND_KIND_TEXT_UNMARSHALER = 25,
  BIND_KIND_VALUE            = 26,
  BIND_KIND_STREAM           = 27,
} BindKind;

/*
 * Slice and Stream share a Go slice header at cur_dst and SlotClass backing.
 * Stream differs only at explicit yield and drain policy points.
 */
#define BIND_IS_SLICE_LIKE(kind) ((kind) == BIND_KIND_SLICE || (kind) == BIND_KIND_STREAM)

enum {
  BIND_FF_QUOTED = 1u << 0,
  /*
   * Go sets STREAM_SKIP only on the active CurType copy. Push and pop preserve
   * it through nested values and discard it when that stream closes. Static
   * BindType records and non-stream containers must never carry this bit.
   */
  BIND_FLAG_STREAM_SKIP = 1u << 1,
  /* Field flags are built into BindField.flags and are not type properties. */
  BIND_FF_VARIANT = 1u << 3, /* The discriminator selects a concrete TypeIdx. */
  BIND_FF_VDISC   = 1u << 4, /* The bound Go string is the discriminator state. */
  /* Bit 5 is type-local BIND_FLAG_MAY_PHASE2. The builder excludes it from
   * BindField.flags. */
  BIND_FF_KINDOF = 1u << 8, /* The JSON value kind selects a concrete TypeIdx. */
  /* The high 16 bits carry VariantIdx for both variant forms. */
  BIND_FF_INLINE_VARIANT = 1u << 9,
  /* Unknown object fields are accumulated into this value.Value field. */
  BIND_FF_RESERVE_UNKNOWN = 1u << 10,
  /*
   * An inline host discriminator must enter the merged tape because a selected
   * value.Value case must observe it as part of the same object.
   */
  BIND_FF_INLINE_VDISC = 1u << 11,
  /*
   * The field offset is relative to the last embedded pointer pointee. The high
   * 16 bits select its BindPtrHop run, which ends at BIND_PTR_HOP_LAST. Promoted
   * fields cannot also use those bits for variant or kindof metadata.
   */
  BIND_FF_VIA_PTR = 1u << 12,
};

enum {
  /*
   * A non-leaf Stream yields after claiming each element slot but before
   * binding its body, allowing Go to register nested stream handlers.
   */
  BIND_FLAG_ELEM_HAS_STREAM = 1u << 2,
  /*
   * This type-local gate marks structs that may require merged-tape phase 2.
   * The builder excludes it from BindField.flags, so field dispatch never
   * interprets it.
   */
  BIND_FLAG_MAY_PHASE2 = 1u << 5,
  /* Pointer, Value, interface, and deferred-hook kinds use cold dispatch. */
  BIND_FLAG_COLD = 1u << 6,
  /*
   * A map value that can carry pointers is staged in a scannable SlotClass slot.
   * This covers Unmarshaler, TextUnmarshaler, RawMessage, and value.Value through
   * aggregate propagation. The noscan entry stores the slot address, and this bit
   * must agree with BindMapDrainInfo.val_is_deferred.
   */
  BIND_FLAG_CONTAINS_DEFERRED = 1u << 7,
};

/* Values must match the Go SlotClass mode. Bump and RecBump share C dispatch. */
enum {
  BIND_SLOT_BUMP     = 0,
  BIND_SLOT_RECBUMP  = 1,
  BIND_SLOT_RECBATCH = 2,
};

/* Go writes these options in NdecBindContext.opt_flags before entry. */
enum {
  BIND_OPT_DISALLOW_UNKNOWN = 1u << 0,
  BIND_OPT_USE_NUMBER       = 1u << 2,
  // Enable scan-time tape sizing only for types that can emit tape content.
  BIND_OPT_SIZE_TAPE = 1u << 3,
  // Budget the two-word dual-view prologue of each qualifying merged tape.
  BIND_OPT_TAPE_DUAL = 1u << 4,
  // Validate raw UTF-8 and reject unescaped C0 bytes during the root scan.
  BIND_OPT_STRICT_SCAN = 1u << 5,
  /* Structural-depth skip trusts prevalidated input and advances by extent. */
  BIND_OPT_SKIP_LENIENT = 1u << 6,
};

/*
 * Layout matches vbind.BindType. type_idx indexes both parallel type tables and
 * limits them to 65536 entries. child remains uintptr_t so Go keeps the record
 * noscan; TypeTree roots every referenced target while C may read it.
 */
typedef struct BindType {
  uint8_t kind;      /* off 0, BindKind */
  uint8_t flags;     /* off 1, BIND_FLAG_* */
  uint16_t type_idx; /* off 2, index into ctx.types and ctx.type_meta */
  /* The 32-bit payload at offset 4 is kind-specific. */
  union {
    struct {
      uint32_t field_count; /* off 4 */
    } strct;
    struct {
      uint32_t child_size; /* off 4, bytes per element */
    } slice;
    struct {
      uint32_t child_size; /* off 4, bytes per element */
    } array;
    struct {
      int32_t alloc_class; /* off 4, slot_classes index; -1 means none */
    } ptr;
    struct {
      int32_t alloc_class; /* off 4, hmap slot_classes index */
    } map;
    /* Scalar kinds require a zero payload. */
    uint32_t raw;
  } u;

  /*
   * off 8: BindType pointer for containers, first BindField pointer for structs,
   * and zero for scalars. The slot is stored as uintptr_t for Go GC semantics.
   */
  uintptr_t child;
} BindType;
_Static_assert(sizeof(BindType) == 16, "BindType size drift");
_Static_assert(offsetof(BindType, kind) == 0, "BindType.kind off");
_Static_assert(offsetof(BindType, flags) == 1, "BindType.flags off");
_Static_assert(offsetof(BindType, type_idx) == 2, "BindType.type_idx off");
_Static_assert(offsetof(BindType, u) == 4, "BindType.u off");
_Static_assert(offsetof(BindType, child) == 8, "BindType.child off");
_Static_assert(offsetof(BindType, child) == 8, "BindType.child off");

/*
 * Layout matches vbind.BindField. type is uintptr_t so the Go field table stays
 * noscan and remains rooted by TypeTree. flags combines field bits with inherited
 * type bits for one dispatch test. Its high 16 bits carry a poly table index or
 * BindPtrHop start index when the corresponding field flag is set.
 */
#define BIND_FIELD_VARIANT_IDX(f) ((uint16_t)((f)->flags >> 16))
typedef struct BindField {
  uintptr_t type;  /* off 0, BindType pointer stored as uintptr_t */
  uint32_t offset; /* off 8, byte offset within the owning struct or final pointee */
  uint32_t flags;  /* off 12, BIND_FF_*, inherited BIND_FLAG_*, and high-bit index */
} BindField;
_Static_assert(sizeof(BindField) == 16, "BindField size drift");
_Static_assert(offsetof(BindField, type) == 0, "BindField.type");
_Static_assert(offsetof(BindField, offset) == 8, "BindField.offset");
_Static_assert(offsetof(BindField, flags) == 12, "BindField.flags");

/*
 * This 32-byte record matches vbind.TypeMeta and runs parallel to ctx.types.
 * Its Go payload is a noscan uintptr array, so independent TypeTree owners must
 * keep every referenced object alive while C reads the record.
 */
typedef struct BindTypeMeta {
  uint32_t _;    /* off 0, ABI placeholder */
  uint32_t size; /* off 4, Go type size in bytes */
  union {        /* off 8, 24-byte kind-specific payload */
    struct {
      uintptr_t lookup;                   /* off 8, field-name perfect-hash blob */
      uint16_t inline_variant_idx;        /* off 16, ctx.variants index; 0xFFFF means none */
      uint16_t _pad;                      /* off 18 */
      uint32_t reserve_unknown_field_off; /* off 20, byte offset; 0xFFFFFFFF means none */
      /* off 24, BindPtrHop base owned and rooted by TypeTree; NULL when unused */
      uintptr_t ptr_hops;
    } strct;
    struct {
      uintptr_t elem_rtype;       /* off 8, Go runtime element type; Go only */
      uintptr_t empty_slice_data; /* off 16, zero-length backing sentinel */
      int32_t alloc_class;        /* off 24, element backing slot_classes index */
    } slice;
    struct {
      uint32_t array_len; /* off 8, element count */
    } array;
    struct {
      uint32_t ptr_child_size; /* off 8, pointee size in bytes; Go only */
    } ptr;
    struct {
      uint32_t key_type;    /* off 8, key index in types; build only */
      uintptr_t drain_info; /* off 16, BindMapDrainInfo pointer shared by C and Go */
      /* off 24, map entry stride in bytes; copied into each region header */
      uint32_t stride;
    } map;
  } u;
} BindTypeMeta;
_Static_assert(sizeof(BindTypeMeta) == 32, "BindTypeMeta size drift");
_Static_assert(offsetof(BindTypeMeta, size) == 4, "BindTypeMeta.size");
_Static_assert(offsetof(BindTypeMeta, u) == 8, "BindTypeMeta.u off 8");
_Static_assert(offsetof(BindTypeMeta, u.strct.lookup) == 8, "BindTypeMeta.u.strct.lookup");
_Static_assert(offsetof(BindTypeMeta, u.strct.inline_variant_idx) == 16,
               "BindTypeMeta.u.strct.inline_variant_idx");
_Static_assert(offsetof(BindTypeMeta, u.strct.reserve_unknown_field_off) == 20,
               "BindTypeMeta.u.strct.reserve_unknown_field_off");
_Static_assert(offsetof(BindTypeMeta, u.strct.ptr_hops) == 24, "BindTypeMeta.u.strct.ptr_hops");

/*
 * Layout matches vbind.BindPtrHop. Each record crosses one embedded pointer,
 * allocating the pointee from alloc_class when NULL. The sign bit marks the last
 * record because valid SlotClass indices are nonnegative.
 */
typedef struct BindPtrHop {
  uint32_t slot_offset; /* off 0, byte offset from the previous hop base */
  int32_t alloc_class;  /* off 4, SlotClass index with optional BIND_PTR_HOP_LAST */
} BindPtrHop;
_Static_assert(sizeof(BindPtrHop) == 8, "BindPtrHop size drift");
_Static_assert(offsetof(BindPtrHop, slot_offset) == 0, "BindPtrHop.slot_offset");
_Static_assert(offsetof(BindPtrHop, alloc_class) == 4, "BindPtrHop.alloc_class");

#define BIND_PTR_HOP_LAST       ((int32_t)(1u << 31))
#define BIND_PTR_HOP_CLASS(h)   ((h)->alloc_class & ~BIND_PTR_HOP_LAST)
#define BIND_PTR_HOP_IS_LAST(h) (((h)->alloc_class & BIND_PTR_HOP_LAST) != 0)

/*
 * Each map region stores this header before its entries in the shared noscan map
 * buffer. The first eight bytes hold the immutable entry stride and the next
 * unreserved-entry cursor. hmap and parent_slot remain rooted outside this noscan
 * buffer while C and the Go drain use them.
 */
typedef struct BindMapRegionHeader {
  uint32_t stride;         /* off 0, immutable entry stride in bytes */
  uint32_t next_entry_off; /* off 4, byte cursor to the next unreserved entry */
  uint32_t entry_count;    /* off 8, number of complete entries */
  uint32_t type_idx;       /* off 12, ctx.type_meta index */
  void *hmap;              /* off 16, cached Go *hmap */
  void *parent_slot;       /* off 24, publication and relocation fixup target */
} BindMapRegionHeader;
_Static_assert(sizeof(BindMapRegionHeader) == 32, "BindMapRegionHeader size drift");
_Static_assert(offsetof(BindMapRegionHeader, stride) == 0, "BindMapRegionHeader.stride");
_Static_assert(offsetof(BindMapRegionHeader, next_entry_off) == 4, "BindMapRegionHeader.next_entry_off");
_Static_assert(offsetof(BindMapRegionHeader, entry_count) == 8, "BindMapRegionHeader.entry_count");
_Static_assert(offsetof(BindMapRegionHeader, type_idx) == 12, "BindMapRegionHeader.type_idx");
_Static_assert(offsetof(BindMapRegionHeader, hmap) == 16, "BindMapRegionHeader.hmap");
_Static_assert(offsetof(BindMapRegionHeader, parent_slot) == 24, "BindMapRegionHeader.parent_slot");

#define BIND_MAP_REGION_HEADER_SIZE 32
#define BIND_MAP_KEY_OFF            0  /* byte offset of key within an entry slot */
#define BIND_MAP_VAL_OFF            16 /* byte offset of value within an entry slot */
#define BIND_MAP_REGION_SLOTS       16

/*
 * Layout matches vbind.MapDrainInfo. Go owns and roots map_rtype. Deferred map
 * values are staged in the indicated scannable SlotClass; independently,
 * val_indirect selects generic map assignment for runtime-indirect values.
 */
typedef struct BindMapDrainInfo {
  const void *map_rtype;   /* off 0, Go runtime map type */
  uint32_t kv_stride;      /* off 8, SAX staging stride in bytes */
  uint32_t key_kind;       /* off 12, key conversion kind */
  uint32_t val_size;       /* off 16, map value size in bytes */
  uint8_t val_is_deferred; /* off 20, entry stores a SlotClass address */
  uint8_t val_indirect;    /* off 21, Go runtime stores the value indirectly */
  uint8_t _pad[2];         /* off 22 */
  int32_t val_slot_class;  /* off 24, SlotClass index; -1 means unused */

} BindMapDrainInfo;

/*
 * BindType.child points here for BIND_KIND_ANY. Fixed runtime type pointers and
 * SlotClass indices let C construct an eface without reflection. Nested arrays
 * and objects recurse through the registered []any and map[string]any types.
 * TypeTree roots this record and every referenced object while C may read them.
 */
typedef struct BindAnyMeta {
  const void *float64_type; /* off 0, runtime type for float64 */
  const void *string_type;  /* off 8, runtime type for string */
  const void *bool_type;    /* off 16, runtime type for bool */
  const void *nil_type;     /* off 24, NULL is valid for a nil eface */
  const void *slice_type;   /* off 32, runtime type for []any */
  const void *map_type;     /* off 40, runtime type for map[string]any */
  /* Package-level values keep allocation-free bool data slots alive. */
  const uint64_t *static_true;  /* off 48, points to uint64 value 1 */
  const uint64_t *static_false; /* off 56, points to uint64 value 0 */
  int32_t float64_slot_class;   /* off 64, 8-byte value slots */
  int32_t string_slot_class;    /* off 68, 16-byte string header slots */
  int32_t slice_slot_class;     /* off 72, 24-byte slice header slots */
  int32_t map_slot_class;       /* off 76, map[string]any hmap slots */
  uint16_t slice_any_type_idx;  /* off 80, BindType index for []any */
  uint16_t map_any_type_idx;    /* off 82, BindType index for map[string]any */
  uint32_t _pad;                /* off 84 */
  const void *number_type;      /* off 88, runtime type for json.Number */
} BindAnyMeta;
_Static_assert(sizeof(BindAnyMeta) == 96, "BindAnyMeta size drift");
_Static_assert(offsetof(BindAnyMeta, float64_type) == 0, "BindAnyMeta.float64_type");
_Static_assert(offsetof(BindAnyMeta, string_type) == 8, "BindAnyMeta.string_type");
_Static_assert(offsetof(BindAnyMeta, bool_type) == 16, "BindAnyMeta.bool_type");
_Static_assert(offsetof(BindAnyMeta, nil_type) == 24, "BindAnyMeta.nil_type");
_Static_assert(offsetof(BindAnyMeta, slice_type) == 32, "BindAnyMeta.slice_type");
_Static_assert(offsetof(BindAnyMeta, map_type) == 40, "BindAnyMeta.map_type");
_Static_assert(offsetof(BindAnyMeta, static_true) == 48, "BindAnyMeta.static_true");
_Static_assert(offsetof(BindAnyMeta, static_false) == 56, "BindAnyMeta.static_false");
_Static_assert(offsetof(BindAnyMeta, float64_slot_class) == 64, "BindAnyMeta.float64_slot_class");
_Static_assert(offsetof(BindAnyMeta, string_slot_class) == 68, "BindAnyMeta.string_slot_class");
_Static_assert(offsetof(BindAnyMeta, slice_slot_class) == 72, "BindAnyMeta.slice_slot_class");
_Static_assert(offsetof(BindAnyMeta, map_slot_class) == 76, "BindAnyMeta.map_slot_class");
_Static_assert(offsetof(BindAnyMeta, slice_any_type_idx) == 80, "BindAnyMeta.slice_any_type_idx");
_Static_assert(offsetof(BindAnyMeta, map_any_type_idx) == 82, "BindAnyMeta.map_any_type_idx");
_Static_assert(offsetof(BindAnyMeta, number_type) == 88, "BindAnyMeta.number_type");

/*
 * This 56-byte layout matches vbind.BindVariantTable. BindField.flags selects a
 * table through its high 16 bits. A discriminator value maps through lookup to
 * parallel type, runtime boxing, and SlotClass arrays. default_case_idx handles
 * only an unmatched value; an absent discriminator still selects no case. Go
 * keeps the lookup reachable and VariantCases owns the parallel arrays.
 */
typedef struct BindVariantTable {
  uint32_t disc_off;             /* off 0, discriminator byte offset in host */
  uint32_t case_count;           /* off 4, number of explicit cases */
  uintptr_t lookup;              /* off 8, case string to case index lookup */
  const uint16_t *case_type_idx; /* off 16, case index to ctx.types index */
  const void *const *case_rtype; /* off 24, case index to runtime type or itab */
  uint16_t default_case_idx;     /* off 32, fallback case; 0xFFFF means none */
  uint16_t _pad;
  uint32_t _pad2;                 /* off 36 */
  const int32_t *case_slot_class; /* off 40, case index to SlotClass index */
  /* off 48, host ctx.types index for inline variants; zero for sibling variants */
  uint16_t host_type_idx;
  uint16_t _pad4; /* off 50 */
  uint32_t _pad5; /* off 52, padding to 56 bytes */
} BindVariantTable;
_Static_assert(sizeof(BindVariantTable) == 56, "BindVariantTable size drift");
_Static_assert(offsetof(BindVariantTable, disc_off) == 0, "BindVariantTable.disc_off");
_Static_assert(offsetof(BindVariantTable, lookup) == 8, "BindVariantTable.lookup");
_Static_assert(offsetof(BindVariantTable, case_type_idx) == 16, "BindVariantTable.case_type_idx");
_Static_assert(offsetof(BindVariantTable, case_rtype) == 24, "BindVariantTable.case_rtype");
_Static_assert(offsetof(BindVariantTable, default_case_idx) == 32, "BindVariantTable.default_case_idx");
_Static_assert(offsetof(BindVariantTable, case_slot_class) == 40, "BindVariantTable.case_slot_class");
_Static_assert(offsetof(BindVariantTable, host_type_idx) == 48, "BindVariantTable.host_type_idx");

/*
 * This 56-byte layout matches vbind.BindKindofTable. JSON kind directly selects
 * a case, with -1 meaning unregistered. The parallel case arrays occupy offsets
 * 16, 24, and 40 as in BindVariantTable so close-time boxing shares one access
 * pattern. KindofCaseData owns these arrays while C may read them.
 */
typedef struct BindKindofTable {
  /* off 0, order is bool, number, string, array, object; -1 means absent */
  int8_t case_idx_by_kind[5];
  uint8_t _pad0[3];               /* off 5 */
  uintptr_t _pad1;                /* off 8 */
  const uint16_t *case_type_idx;  /* off 16, case index to ctx.types index */
  const void *const *case_rtype;  /* off 24, case index to Go runtime type */
  uint32_t case_count;            /* off 32, at most 5 */
  uint32_t _pad2;                 /* off 36 */
  const int32_t *case_slot_class; /* off 40, case index to SlotClass index */
  uint64_t _pad3;                 /* off 48, padding to 56 bytes */
} BindKindofTable;
_Static_assert(sizeof(BindKindofTable) == 56, "BindKindofTable size drift");
_Static_assert(offsetof(BindKindofTable, case_type_idx) == 16, "BindKindofTable.case_type_idx");
_Static_assert(offsetof(BindKindofTable, case_rtype) == 24, "BindKindofTable.case_rtype");
_Static_assert(offsetof(BindKindofTable, case_count) == 32, "BindKindofTable.case_count");
_Static_assert(offsetof(BindKindofTable, case_slot_class) == 40, "BindKindofTable.case_slot_class");

/*
 * Layout matches vbind.SlotClass. The mode selects valid overlays. BUMP and
 * RECBUMP use offset and limit. Only BUMP uses len and cap. aux holds the BUMP
 * predictor or the recursive detach group. RECBATCH uses block as the installed
 * RecBatchMatrix and ignores offset, limit, len, and cap. A NULL block means the
 * selected mode has no backing installed; streams may remain detached until Go
 * supplies batch backing.
 */
typedef struct BindSlotClass {
  uint8_t *block;     /* off 0, mode-specific installed backing */
  void *rtype;        /* off 8, Go-owned runtime type; Go only */
  uint32_t elem_size; /* off 16, immutable element size in bytes */
  uint8_t mode;       /* off 20, BIND_SLOT_* */
  uint8_t map_flag;   /* off 21, Go-only map marker */
  uint8_t _pad0[2];   /* off 22 */
  uint32_t offset;    /* off 24, BUMP and RECBUMP byte cursor */
  uint32_t limit;     /* off 28, BUMP and RECBUMP byte limit */
  uint32_t len;       /* off 32, BUMP completed element count */
  uint32_t cap;       /* off 36, BUMP element capacity */
  uint32_t aux;       /* off 40, BUMP predictor or recursive group */
  uint32_t _pad1;     /* off 44, padding to 48 bytes */
} BindSlotClass;
_Static_assert(sizeof(BindSlotClass) == 48, "BindSlotClass size drift");
_Static_assert(offsetof(BindSlotClass, block) == 0, "BindSlotClass.block off 0");
_Static_assert(offsetof(BindSlotClass, rtype) == 8, "BindSlotClass.rtype off 8");
_Static_assert(offsetof(BindSlotClass, elem_size) == 16, "BindSlotClass.elem_size off 16");
_Static_assert(offsetof(BindSlotClass, mode) == 20, "BindSlotClass.mode off 20");
_Static_assert(offsetof(BindSlotClass, offset) == 24, "BindSlotClass.offset off 24");
_Static_assert(offsetof(BindSlotClass, limit) == 28, "BindSlotClass.limit off 28");
_Static_assert(offsetof(BindSlotClass, len) == 32, "BindSlotClass.len off 32");
_Static_assert(offsetof(BindSlotClass, cap) == 36, "BindSlotClass.cap off 36");
_Static_assert(offsetof(BindSlotClass, aux) == 40, "BindSlotClass.aux off 40");

/*
 * RecBatch provides bounded typed backings for recursive slices at capacities 1
 * through 128. C never replaces a row base. Go roots each base, retains replaced
 * backings through refill, and installs a fresh matrix at detach. A freed slot
 * must be zeroed before reuse so GC cannot trace stale pointers.
 */
#define BIND_RECBATCH_ROW_COUNT 8u
#define BIND_RECBATCH_MAX_CAP   128u

typedef struct RecBatchRow {
  /* off 0, Go-rooted typed array; slot i starts at i * capacity * elem_size */
  void *base;
  uint64_t bitmap;     /* off 8, bit value 1 means free */
  uint32_t free_count; /* off 16, number of free slots */
  uint32_t _pad;       /* off 20 */
} RecBatchRow;
_Static_assert(sizeof(RecBatchRow) == 24, "RecBatchRow size drift");
_Static_assert(offsetof(RecBatchRow, base) == 0, "RecBatchRow.base off");
_Static_assert(offsetof(RecBatchRow, bitmap) == 8, "RecBatchRow.bitmap off");
_Static_assert(offsetof(RecBatchRow, free_count) == 16, "RecBatchRow.free_count off");

/* Row capacity is 1 << row index; element size remains in BindSlotClass. */
typedef struct RecBatchMatrix {
  RecBatchRow rows[BIND_RECBATCH_ROW_COUNT]; /* off 0 */
} RecBatchMatrix;
_Static_assert(sizeof(RecBatchMatrix) == 192, "RecBatchMatrix size drift");
_Static_assert(offsetof(RecBatchMatrix, rows) == 0, "RecBatchMatrix.rows off");

/*
 * Values and argument contracts must match the Go BindYield constants. Before
 * publishing an action, C stores a phase whose continuation expects Go to have
 * completed that action. Go services the request, updates shared fields, and
 * reenters the same machine.
 */
enum {
  BIND_YIELD_NONE  = 0,
  BIND_YIELD_ERROR = 1,
  /* Arg0 is the SlotClass index. Arg1 is reserved. Target is the active destination. */
  BIND_YIELD_BLOCK_FULL = 2,
  /* Arg0 is the slice type index. Arg1 is the stream element index or zero.
   * Target is the slice or Stream header. */
  BIND_YIELD_SLICE_GROW = 3,
  /* Arg0 is the slice type index. Arg1 is the row index. Target is the slice header. */
  BIND_YIELD_RECBATCH_REFILL = 4,
  /* Arg0 is the slice type index. Arg1 is the requested capacity. Target is the slice header. */
  BIND_YIELD_RECBATCH_BYPASS = 5,
  BIND_YIELD_FLUSH_MAP       = 6,
  BIND_YIELD_FLUSH_UNMARSHAL = 7,
  /*
   * The scan has completed but no tape word has been written. Go grows tape_arena
   * to alloc.tape_need. Arg0, Arg1, and Target are unused.
   */
  BIND_YIELD_TAPE_ARENA = 11,
  /*
   * A tape-bind walk reached value.Value. arg0 is its absolute source-tape word
   * offset and arg1 is its word count. stash.deferred_yield.slot identifies the
   * destination. Go aliases the source ValueDoc tape without copying.
   */
  BIND_YIELD_TAPE_BIND_VALUE = 10,
};

enum {
  BIND_PHASE_ROOT               = 0,
  BIND_PHASE_ARRAY_VALUE        = 2, /* Resume the active array element loop. */
  BIND_PHASE_OBJECT_FIELD_VALUE = 3,
  BIND_PHASE_MAP_CONTINUE       = 4,
  BIND_PHASE_MAP_OPEN_RETRY     = 5,
  BIND_PHASE_MAP_VALUE          = 6,
  BIND_PHASE_DOCUMENT_END       = 7,
  BIND_PHASE_ANY_RESUME         = 8,
  BIND_PHASE_DEFERRED_RESUME    = 9,
  BIND_PHASE_ROOT_UNWRAP        = 10,
  BIND_PHASE_VALUE_RESUME       = 11,
  BIND_PHASE_ARRAY_CLOSE        = 12, /* Go has consumed the final stream batch. */
  /* Shared by variant and kindof; depth alone selects the outer continuation. */
  BIND_PHASE_VARIANT_REBIND_RESUME = 13,
  BIND_PHASE_VARIANT_INLINE_RESUME = 14,
  BIND_PHASE_KINDOF_INLINE_RESUME  = 15,
  /* Numeric values must match the Go BindPhase constants. */
  BIND_PHASE_TAPE_BIND_ROOT                = 16,
  BIND_PHASE_TAPE_BIND_ARRAY_VALUE         = 17,
  BIND_PHASE_TAPE_BIND_OBJECT_FIELD_VALUE  = 18,
  BIND_PHASE_TAPE_BIND_MAP_CONTINUE        = 19,
  BIND_PHASE_TAPE_BIND_MAP_OPEN_RETRY      = 20,
  BIND_PHASE_TAPE_BIND_MAP_VALUE           = 21,
  BIND_PHASE_TAPE_BIND_ROOT_UNWRAP         = 22,
  BIND_PHASE_TAPE_BIND_VALUE_RESUME_OBJECT = 24, /* Skip the aliased subtree and continue the object. */
  BIND_PHASE_TAPE_BIND_VALUE_RESUME_ARRAY  = 25, /* Skip the aliased subtree and continue the array. */
  BIND_PHASE_TAPE_BIND_VALUE_RESUME_MAP    = 26, /* Skip the aliased subtree and continue the map. */
  BIND_PHASE_TAPE_BIND_ANY_RESUME          = 27,
  /* Recompute the selected case after Go refills its SlotClass. */
  BIND_PHASE_TAPE_BIND_FIELD_VALUE_CASE_RETRY = 28,
  /* Classification and reserve-unknown publication are complete. Retry case binding. */
  BIND_PHASE_TAPE_BIND_CLOSE_DRAIN_RETRY = 29,
  /* Recompute field state after a pointer-chain SlotClass refill. */
  BIND_PHASE_TAPE_BIND_FIELD_VALUE_PTR_RESUME = 30,
  BIND_PHASE_TAPE_BIND_VALUE_RESUME_ROOT      = 31,
  /* The stream element slot is committed; Go registered nested handlers. */
  BIND_PHASE_ARRAY_VALUE_BEGIN = 32,
  /*
   * Resume the unmatched-value tape writer. The enclosing struct close, not this
   * value close, finalizes the reserve-unknown Value header.
   */
  BIND_PHASE_RESERVE_UNKNOWN_VALUE_RESUME = 33,
  /* The merged-tape cursor already passed the poly entry, so retry its binding. */
  BIND_PHASE_PHASE2_POLY_RETRY = 34,
  /*
   * The root scan and core seeding are complete, but the bind walk has not begun.
   * This phase survives BIND_YIELD_TAPE_ARENA so reentry does not rescan.
   */
  BIND_PHASE_ROOT_SCANNED = 35,
};

enum {
  BIND_ERR_SYNTAX               = 1,
  BIND_ERR_EOF                  = 2,
  BIND_ERR_DEPTH                = 3,
  BIND_ERR_UTF8                 = 4,
  BIND_ERR_TRAILING             = 5,
  BIND_ERR_TYPE_MISMATCH        = 32,
  BIND_ERR_UNKNOWN_FIELD        = 33,
  BIND_ERR_FIXED_OVERFLOW       = 34,
  BIND_ERR_UNSUPPORTED_TAG      = 35,
  BIND_ERR_VARIANT_UNKNOWN_DISC = 36,
  BIND_ERR_VARIANT_MISSING_DISC = 37,
  /* arg1 is the stable kind ordinal: bool, number, string, array, or object. */
  BIND_ERR_KINDOF_UNREGISTERED = 38,
};

/*
 * Go initializes this 80-byte input view before entry and keeps every referenced
 * object alive through all yields until the parse completes.
 */
typedef struct NdecBindContext {
  const BindType *types;         /* off 0 */
  const BindTypeMeta *type_meta; /* off 8 */
  const uint8_t *src;            /* off 16, immutable input bytes */
  size_t src_len;                /* off 24, input length in bytes */
  uint32_t root_type;            /* off 32, ctx.types index */
  /*
   * off 36: packed view mode for the root tape walk. The low bits select seam
   * view A or B; the remaining bits are mode flags such as count-at-close.
   * Phase 2 may switch modes during a nested case descent and preserves that
   * state separately. Seam consumers mask with TAPE_VIEW_SHIFT_MASK.
   */
  uint32_t root_view_mode;
  /* off 40, pointer-valued ABI field; the caller retains the typed GC root */
  uint8_t *root_dst;
  uint32_t opt_flags;               /* off 48, BIND_OPT_* */
  uint32_t any_type_idx;            /* off 52, ctx.types index for ANY */
  const BindVariantTable *variants; /* off 56, TypeTree-owned table or NULL */
  uint32_t variants_count;          /* off 64, table entry count */
  uint32_t kindofs_count;           /* off 68, table entry count */
  const BindKindofTable *kindofs;   /* off 72, TypeTree-owned table or NULL */
} NdecBindContext;
_Static_assert(sizeof(NdecBindContext) == 80, "NdecBindContext size drift");
_Static_assert(offsetof(NdecBindContext, types) == 0, "ctx.types");
_Static_assert(offsetof(NdecBindContext, type_meta) == 8, "ctx.type_meta");
_Static_assert(offsetof(NdecBindContext, src) == 16, "ctx.src");
_Static_assert(offsetof(NdecBindContext, src_len) == 24, "ctx.src_len");
_Static_assert(offsetof(NdecBindContext, root_type) == 32, "ctx.root_type");
_Static_assert(offsetof(NdecBindContext, root_view_mode) == 36, "ctx.root_view_mode");
_Static_assert(offsetof(NdecBindContext, root_dst) == 40, "ctx.root_dst");
_Static_assert(offsetof(NdecBindContext, opt_flags) == 48, "ctx.opt_flags");
_Static_assert(offsetof(NdecBindContext, any_type_idx) == 52, "ctx.any_type_idx");
_Static_assert(offsetof(NdecBindContext, variants) == 56, "ctx.variants");
_Static_assert(offsetof(NdecBindContext, variants_count) == 64, "ctx.variants_count");
_Static_assert(offsetof(NdecBindContext, kindofs_count) == 68, "ctx.kindofs_count");
_Static_assert(offsetof(NdecBindContext, kindofs) == 72, "ctx.kindofs");

/*
 * Go owns every backing in this 120-byte view. C advances cursors and writes
 * within the published capacities. str_arena and tape_arena may back returned
 * Values, so prior aliases keep replaced backings alive. structural,
 * deferred_drain, and map_buf are parse scratch and do not escape publication.
 */
typedef struct NdecBindAllocator {
  BindSlotClass *slot_classes; /* off 0 */
  uint8_t *str_arena;          /* off 8, user-visible string arena */
  size_t str_arena_cap;        /* off 16, capacity in bytes */
  size_t str_gen_start;        /* off 24, root generation start in bytes */
  const uint32_t *structural;  /* off 32, full-buffer scan scratch */
  uint32_t structural_cap;     /* off 40, capacity in uint32 entries */
  /*
   * tape_need is bidirectional when BIND_OPT_SIZE_TAPE is set. Go provides a
   * src_len ceiling, C replaces it with the smaller scan-derived bound, and Go
   * uses the result to service BIND_YIELD_TAPE_ARENA.
   */
  uint32_t tape_need;           /* off 44, capacity request in uint64 words */
  uint8_t *deferred_drain;      /* off 48, Go-owned record buffer */
  uint32_t deferred_drain_cap;  /* off 56, capacity in bytes */
  uint32_t deferred_drain_used; /* off 60, byte cursor reset by Go after drain */
  uint8_t *map_buf;             /* off 64, Go-owned noscan scratch buffer */
  uint32_t map_buf_cap;         /* off 72, capacity in bytes */
  uint32_t map_buf_used;        /* off 76, byte cursor */
  /* off 80, active tape base from tape_arena or a borrowed source ValueDoc */
  uint64_t *value_tape;
  /*
   * off 88, Go-owned ValueDoc pointer copied into Values. The machine backing is
   * noscan, so a typed Go local remains its GC root through drain and publication.
   */
  void *value_doc;
  uint64_t *tape_arena;  /* off 96, user-visible tape arena */
  size_t tape_arena_cap; /* off 104, capacity in uint64 words */
  size_t tape_used;      /* off 112, C-written word cursor reset by Go at entry */
} NdecBindAllocator;
_Static_assert(sizeof(NdecBindAllocator) == 120, "NdecBindAllocator size drift");
_Static_assert(offsetof(NdecBindAllocator, slot_classes) == 0, "alloc.slot_classes");
_Static_assert(offsetof(NdecBindAllocator, str_arena) == 8, "alloc.str_arena");
_Static_assert(offsetof(NdecBindAllocator, str_arena_cap) == 16, "alloc.str_arena_cap");
_Static_assert(offsetof(NdecBindAllocator, str_gen_start) == 24, "alloc.str_gen_start");
_Static_assert(offsetof(NdecBindAllocator, structural) == 32, "alloc.structural");
_Static_assert(offsetof(NdecBindAllocator, structural_cap) == 40, "alloc.structural_cap");
_Static_assert(offsetof(NdecBindAllocator, tape_need) == 44, "alloc.tape_need");
_Static_assert(offsetof(NdecBindAllocator, deferred_drain) == 48, "alloc.deferred_drain");
_Static_assert(offsetof(NdecBindAllocator, deferred_drain_cap) == 56, "alloc.deferred_drain_cap");
_Static_assert(offsetof(NdecBindAllocator, deferred_drain_used) == 60, "alloc.deferred_drain_used");
_Static_assert(offsetof(NdecBindAllocator, map_buf) == 64, "alloc.map_buf");
_Static_assert(offsetof(NdecBindAllocator, map_buf_cap) == 72, "alloc.map_buf_cap");
_Static_assert(offsetof(NdecBindAllocator, map_buf_used) == 76, "alloc.map_buf_used");
_Static_assert(offsetof(NdecBindAllocator, value_tape) == 80, "alloc.value_tape");
_Static_assert(offsetof(NdecBindAllocator, value_doc) == 88, "alloc.value_doc");
_Static_assert(offsetof(NdecBindAllocator, tape_arena) == 96, "alloc.tape_arena");
_Static_assert(offsetof(NdecBindAllocator, tape_arena_cap) == 104, "alloc.tape_arena_cap");
_Static_assert(offsetof(NdecBindAllocator, tape_used) == 112, "alloc.tape_used");

/*
 * C appends these 24-byte records and Go drains them in batches. target must be
 * GC-scannable because a hook may publish heap pointers there. arg0 and arg1 are
 * source byte bounds for JSON and RawMessage, or a byte offset and length in the
 * bump-only str_arena for TextUnmarshaler.
 */
typedef struct UnmarshalRecord {
  void *target;      /* off 0, receiver slot pointer */
  uint32_t type_idx; /* off 8, ctx.types and hook-table index */
  uint8_t kind;      /* off 12, deferred BindKind */
  uint8_t _pad[3];   /* off 13 */
  uint32_t arg0;     /* off 16, source start or str_arena byte offset */
  uint32_t arg1;     /* off 20, source end or string byte length */
} UnmarshalRecord;
_Static_assert(sizeof(UnmarshalRecord) == 24, "UnmarshalRecord size drift");
_Static_assert(offsetof(UnmarshalRecord, target) == 0, "urec.target");
_Static_assert(offsetof(UnmarshalRecord, type_idx) == 8, "urec.type_idx");
_Static_assert(offsetof(UnmarshalRecord, kind) == 12, "urec.kind");
_Static_assert(offsetof(UnmarshalRecord, arg0) == 16, "urec.arg0");
_Static_assert(offsetof(UnmarshalRecord, arg1) == 20, "urec.arg1");

/*
 * These byte offsets must match the Go value.Value layout:
 *
 *   doc    offset 0,  8-byte pointer to the shared ValueDoc
 *   base   offset 8,  4-byte absolute tape root word index
 *   tidx   offset 12, 4-byte navigation index relative to base
 *   end    offset 16, 4-byte word count patched when the value closes
 *   mode   offset 20,  4-byte packed view mode: seam shift in the low bits,
 *                    mode flags (count-at-close) above TAPE_VIEW_SHIFT_MASK
 *
 * Go owns and roots doc. Each Value retains its own base because encoded close
 * distances are relative to that base. C installs the pointer only into
 * GC-scannable destinations and writes all coordinates at full width.
 */
#define VALUE_DOC_OFF  0
#define VALUE_BASE_OFF 8
#define VALUE_TIDX_OFF 12
#define VALUE_END_OFF  16
#define VALUE_MODE_OFF 20
#define VALUE_SIZE     24

/* Go layout tests compare these constants with unsafe.Offsetof. */
_Static_assert(VALUE_DOC_OFF == 0, "Value.doc leads: a single pointer at offset 0");
_Static_assert(VALUE_BASE_OFF == VALUE_DOC_OFF + sizeof(void *), "Value.base follows doc");
_Static_assert(VALUE_TIDX_OFF == VALUE_BASE_OFF + sizeof(int32_t), "Value.tidx follows base");
_Static_assert(VALUE_END_OFF == VALUE_TIDX_OFF + sizeof(int32_t), "Value.end follows tidx");
_Static_assert(VALUE_MODE_OFF == VALUE_END_OFF + sizeof(int32_t), "Value.mode follows end");
_Static_assert(VALUE_SIZE == VALUE_MODE_OFF + sizeof(int32_t), "Value size covers mode with no padding");
_Static_assert(VALUE_SIZE % sizeof(void *) == 0, "Value size is pointer-aligned");
_Static_assert(sizeof(int32_t) == 4, "Value coordinate stores are full-width");

/*
 * C publishes one action and its arguments here before returning to Go. Go must
 * service it before reentry. For ERROR, arg0 is BIND_ERR_*, arg1 is error-specific
 * detail, and first_error_pos is the only source byte position. UINT32_MAX means
 * no source position is available. target names only a variant host for ERROR;
 * other errors publish NULL. Non-error actions use target as their borrowed slot.
 */
typedef struct NdecBindYield {
  uint32_t pending_action;  /* off 0, BIND_YIELD_* */
  uint32_t arg0;            /* off 4, action-specific argument */
  uint32_t arg1;            /* off 8, action-specific argument or error detail */
  uint32_t first_error_pos; /* off 12, source byte offset or UINT32_MAX */
  uint8_t *target;          /* off 16, action slot or variant host */
} NdecBindYield;
_Static_assert(sizeof(NdecBindYield) == 24, "NdecBindYield size drift");
_Static_assert(offsetof(NdecBindYield, pending_action) == 0, "yield.pending_action");
_Static_assert(offsetof(NdecBindYield, arg0) == 4, "yield.arg0");
_Static_assert(offsetof(NdecBindYield, arg1) == 8, "yield.arg1");
_Static_assert(offsetof(NdecBindYield, first_error_pos) == 12, "yield.first_error_pos");
_Static_assert(offsetof(NdecBindYield, target) == 16, "yield.target");

typedef struct NdecBindBridge {
  NdecBindContext ctx;     /* off 0, 80 bytes */
  NdecBindAllocator alloc; /* off 80, 120 bytes */
  NdecBindYield yield;     /* off 200, 24 bytes */
} NdecBindBridge;
_Static_assert(sizeof(NdecBindBridge) == 224, "NdecBindBridge size drift");
_Static_assert(offsetof(NdecBindBridge, ctx) == 0, "bridge.ctx");
_Static_assert(offsetof(NdecBindBridge, alloc) == 80, "bridge.alloc");
_Static_assert(offsetof(NdecBindBridge, yield) == 200, "bridge.yield");

#endif /* NDEC_BIND_BRIDGE_H */
