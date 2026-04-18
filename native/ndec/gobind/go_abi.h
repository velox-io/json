#ifndef NDEC_GO_ABI_H
#define NDEC_GO_ABI_H

#include <stdint.h>

/*
 * Yield action: written to ud.pending_action when the reactor voluntarily
 * yields control.
 *
 * Yields are reserved for work that MUST happen on the Go side. Byte-level
 * data processing (integer parsing, string unescape) is handled inline in C
 * and never yields. All actions fall into two buckets:
 *   1. GC-managed memory: BEGIN_PTR / GROW_SLICE / GROW_SLICE_STRUCT /
 *                         BEGIN_MAP / FLUSH_MAP / BEGIN_PTR_MAP_VALUE
 *   2. Error channel: TYPE_MISMATCH / UNKNOWN_FIELD
 *
 * MAP protocol: C-side hooks lay out each (key, value) pair sequentially in
 * the frame's KV buffer sub-interval (16B GoStringHeader + value elem_size,
 * 8B aligned). When the buffer fills or end_object fires, FLUSH_MAP yields
 * and the driver performs batch mapassign + typedmemmove. Single-frame
 * buffer capacity = NDEC_MAP_KV_BUF_COUNT. See bind_hooks.h MAP branches and
 * Go-side yield_map.go.
 *
 * Append new constants here; do not reserve placeholder numbers. */
typedef enum NdecYieldAction {
  NDEC_YA_NONE                = 0, /* true SUSPEND, input exhausted */
  NDEC_YA_BEGIN_PTR           = 1, /* alloc *T (scalar or struct) */
  NDEC_YA_GROW_SLICE          = 2, /* slice cap exhausted (scalar elem, raw write included) */
  NDEC_YA_TYPE_MISMATCH       = 3, /* JSON token incompatible with target kind */
  NDEC_YA_UNKNOWN_FIELD       = 4, /* DisallowUnknownFields hit */
  NDEC_YA_GROW_SLICE_STRUCT   = 5, /* SLICE<struct> cap exhausted (begin_object triggers, only expand cap) */
  NDEC_YA_BEGIN_MAP           = 6, /* '{' seen on a map field; driver allocs map + pushes MAP frame */
  NDEC_YA_FLUSH_MAP           = 7, /* MAP frame KV buffer full / end_object final flush; driver batch mapassign */
  NDEC_YA_BEGIN_PTR_MAP_VALUE = 8, /* map[string]*T value: driver allocs pointee and writes pointer slot */
  NDEC_YA_BASE64_SLICE        = 9, /* []byte base64 decode: decode raw string, alloc/grow backing, write */
  NDEC_YA_GROW_SLICE_PTR_STRUCT = 10, /* SLICE<*struct>: grow backing + alloc pointee + fill child frame */
} NdecYieldAction;

/*
 * Yield flags: auxiliary marker bits on the yield payload.
 *
 * Side channel in the reactor to driver protocol, distinguishing multiple
 * semantics under the same action.
 * Current uses:
 *   BEGIN_PTR yield distinguishes *<scalar> from *<struct>:
 *     *<scalar>:  yield_flags = 0,    raw = token bytes
 *     *<struct>:  yield_flags = 0xFF, raw bytes meaningless (driver allocs then pushes frame)
 *   BEGIN_PTR_MAP_VALUE distinguishes map-value *<scalar> from *<struct>:
 *     *<scalar>:  yield_flags = 0, raw = token bytes
 *     *<struct>:  yield_flags = 2, raw bytes meaningless (parser has already pushed child)
 *   GROW_SLICE yield distinguishes real value from JSON null sentinel:
 *     real value: yield_flags = 0,  raw = elem bytes, driver writes
 *     null:       yield_flags = 1,  raw = "null" literal, driver writes zero value
 *   TYPE_MISMATCH yield encodes the triggering JSON token category in the
 *     upper bits (NDEC_YF_TOKEN_*). The driver's errCtx uses this to render a
 *     stdlib-compatible UnmarshalTypeError.Value (e.g. "string", "number",
 *     "null", "bool", "object", "array"). When not encoded, the raw_ptr path
 *     serves as fallback, preserving existing call site behavior. */
enum {
  NDEC_YF_NONE                 = 0,
  NDEC_YF_GROW_NULL            = 1,    /* GROW_SLICE: elem is JSON null */
  NDEC_YF_MAP_VALUE_PTR_STRUCT = 2,    /* BEGIN_PTR_MAP_VALUE: map value is *struct */
  NDEC_YF_PTR_TO_SLICE         = 0x10, /* BEGIN_PTR: pointee is []T container */
  NDEC_YF_PTR_TO_MAP           = 0x20, /* BEGIN_PTR: pointee is map[K]V container */
  NDEC_YF_GROW_ALLOC_PTR       = 0x40, /* GROW_SLICE: elem is *scalar */
  NDEC_YF_PTR_TO_STRUCT        = 0xFF,

  /* Token kind for TYPE_MISMATCH path: used primarily by errCtx.valueDesc.
   * Values are chosen from the low range, outside NDEC_YF_PTR_TO_* territory
   * (0x10/0x20/0xFF), so they never collide with BEGIN_PTR yield_flags on
   * the same yield. 0 (= NONE) is reserved for existing callers; 1..6 encode
   * JSON token categories. */
  NDEC_YF_TOKEN_NULL   = 1,
  NDEC_YF_TOKEN_BOOL   = 2,
  NDEC_YF_TOKEN_NUMBER = 3,
  NDEC_YF_TOKEN_STRING = 4,
  NDEC_YF_TOKEN_OBJECT = 5,
  NDEC_YF_TOKEN_ARRAY  = 6,
};

/* Bind kind: same ordering as typ.ElemTypeKind, bridged through a Go-side
 * mapping table rather than sharing enum values directly, preserving room for
 * future divergence. */
typedef enum NdecBindKind {
  NDEC_BK_INVALID = 0,
  NDEC_BK_BOOL,
  NDEC_BK_INT,
  NDEC_BK_INT8,
  NDEC_BK_INT16,
  NDEC_BK_INT32,
  NDEC_BK_INT64,
  NDEC_BK_UINT,
  NDEC_BK_UINT8,
  NDEC_BK_UINT16,
  NDEC_BK_UINT32,
  NDEC_BK_UINT64,
  NDEC_BK_FLOAT32,
  NDEC_BK_FLOAT64,
  NDEC_BK_STRING,
  NDEC_BK_STRUCT,
  NDEC_BK_FIXED_ARRAY, /* fixed-size array, scalar elements only */
  NDEC_BK_SLICE,
  NDEC_BK_PTR,
  NDEC_BK_ANY,
  NDEC_BK_MAP,
  NDEC_BK_IFACE,
  NDEC_BK_RAW_MESSAGE,
  NDEC_BK_NUMBER, /* json.Number */
} NdecBindKind;

/* TypeInfo flags. */
enum {
  NDEC_BTF_HAS_UNMARSHALER      = 1u << 0,
  NDEC_BTF_HAS_TEXT_UNMARSHALER = 1u << 1,
  NDEC_BTF_RAW_MESSAGE          = 1u << 2,
  NDEC_BTF_NUMBER               = 1u << 3,
};

/* FieldInfo tag flags: values align with typ.TagFlag. */
enum {
  NDEC_BFF_QUOTED      = 1u << 0,
  NDEC_BFF_OMITEMPTY   = 1u << 1,
  NDEC_BFF_COPY_STRING = 1u << 2,
};

enum {
  NDEC_OPT_DISALLOW_UNKNOWN = 1u << 0,
};

/* Field lookup ABI.
 *
 * Immutable read-only tables cached in a Go sync.Map, read directly by C's
 * ndec_bind_lookup_find. The data pointer points to a Go slice backing array;
 * the slice header itself is kept alive by bindTypeInfo.
 */
typedef enum NdecFieldLookupKind {
  NDEC_FLK_EMPTY   = 0, /* 0 fields; find always returns -1 */
  NDEC_FLK_BITMAP8 = 1, /* n <= 8 */
  NDEC_FLK_PERFECT = 2, /* n <= 32 */
  NDEC_FLK_MAP     = 3, /* n > 32, binary search */
} NdecFieldLookupKind;

/* MAP tier entry: sorted by hash ascending, binary-searched by C. Layout
 * mirrors Go's mapEntry (24 bytes). */
typedef struct NdecMapEntry {
  const uint8_t *name_ptr; /* off  0 */
  uint32_t name_len;       /* off  8 */
  uint32_t idx;            /* off 12 field index in typeinfo.fields */
  uint64_t hash;           /* off 16 FNV1a(name, seed=0) */
} NdecMapEntry;

_Static_assert(sizeof(NdecMapEntry) == 24, "NdecMapEntry size drift");

typedef struct NdecFieldLookupABI {
  uint8_t kind;           /* off  0 NdecFieldLookupKind */
  uint8_t has_mixed_case; /* off  1 any field name contains uppercase ASCII */
  uint8_t _pad0[2];       /* off  2 */
  uint8_t max_key_len;    /* off  4 BITMAP8 only */
  uint8_t _pad1[3];       /* off  5 */

  const uint8_t *bitmap;   /* off  8 BITMAP8: max_key_len * 256 bytes */
  const uint8_t *len_mask; /* off 16 BITMAP8: max_key_len + 1 bytes */

  uint64_t hash_seed;       /* off 24 PERFECT */
  uint8_t hash_shift;       /* off 32 PERFECT */
  uint8_t hash_mixer;       /* off 33 PERFECT: 0=simple, 1=fnv1a, 2=mulacc */
  uint16_t table_size_log2; /* off 34 PERFECT */
  uint32_t _pad2;           /* off 36 */

  const uint8_t *perfect_table; /* off 40 PERFECT: len = 1 << table_size_log2 */

  uint32_t entry_count;            /* off 48 MAP */
  uint32_t _pad3;                  /* off 52 */
  const NdecMapEntry *map_entries; /* off 56 MAP */
} NdecFieldLookupABI;

_Static_assert(sizeof(NdecFieldLookupABI) == 64, "NdecFieldLookupABI size drift");

/* FieldInfo / TypeInfo: immutable type descriptors generated by Go,
 * read directly by C. All pointers point to Go heap objects whose lifetimes
 * are managed by the Go GC. */
struct NdecBindTypeInfo;

typedef struct NdecBindFieldInfo {
  uint8_t kind;                        /* off  0 NdecBindKind */
  uint8_t tag_flags;                   /* off  1 NDEC_BFF_* */
  uint16_t name_len;                   /* off  2 */
  uint32_t offset;                     /* off  4 field offset within parent struct */
  const uint8_t *name;                 /* off  8 JSON key bytes, not NUL-terminated */
  const struct NdecBindTypeInfo *type; /* off 16 non-null for non-scalar kinds */
} NdecBindFieldInfo;

_Static_assert(sizeof(NdecBindFieldInfo) == 24, "NdecBindFieldInfo size drift");

typedef struct NdecBindTypeInfo {
  uint8_t kind;         /* off  0 NdecBindKind */
  uint8_t type_flags;   /* off  1 NDEC_BTF_* */
  uint16_t field_count; /* off  2 STRUCT field count */
  uint32_t size;        /* off  4 Go type size */

  uint8_t elem_kind;    /* off  8 FIXED_ARRAY/SLICE/MAP elem kind */
  uint8_t _pad0[3];     /* off  9 */
  uint32_t elem_size;   /* off 12 elem size */
  uint32_t fixed_count; /* off 16 FIXED_ARRAY length */
  uint32_t _pad1;       /* off 20 */

  const NdecBindFieldInfo *fields;          /* off 24 STRUCT field table */
  const NdecFieldLookupABI *lookup;         /* off 32 STRUCT lookup table */
  const struct NdecBindTypeInfo *elem_type; /* off 40 container elem typeinfo */

  /* SLICE-only: zerobase data pointer from reflect.MakeSlice(t, 0, 0),
   * used as the shared sentinel for "[]" empty arrays. The begin_array path
   * uses lazy alloc: the parent field header is written as
   * (empty_slice_data, 0, 0). Seeing ']' closes with 0 allocs, 0 yields.
   * Seeing a non-']' element triggers a grow yield where the driver allocs
   * real backing and overwrites hdr->data/cap. Matches stdlib
   * reflect.DeepEqual's "empty slice != nil slice" semantics. */
  void *empty_slice_data; /* off 48 SLICE only */

  /* cap_hint: SLICE-only. EMA-adaptive initial capacity, modelled after
   * vdec's DecSliceInfo.CapHint. Updated on every end_array with alpha=2 EMA
   * (relaxed atomic): hint = (old + observed_len) / 2. On the next
   * begin_array's first-element GROW yield, the driver uses
   * max(cap_hint, initialSliceCap) as the initial capacity, enabling
   * single-shot allocation for arrays of known length and skipping the
   * doubling grow.
   *
   * Only meaningful on SLICE wrappers. Other typeinfo instances have this
   * field set to 0, and the hot path never reads it. Relaxed semantics
   * suffice: a dirty read across goroutines at worst causes one extra
   * doubling, never affecting correctness. The _Atomic declaration makes
   * C's __atomic_load_n / store_n emit 4-byte atomic instructions
   * (ARM ldr/str natural-aligned is already atomic).
   *
   * No padding bytes remain after this field; total size 64 B (8-byte
   * aligned, one cache line). */
  _Atomic int32_t cap_hint; /* off 56 */
  uint32_t _pad2;           /* off 60 */
} NdecBindTypeInfo;

_Static_assert(sizeof(NdecBindTypeInfo) == 64, "NdecBindTypeInfo size drift");

/* Frame: binding fields are inlined into NdecFrame via NDEC_FRAME_EXTRA_FIELDS
 * (see bind_hooks.h). Field names are bind_*-prefixed. There is no separate
 * NdecBindFrame type; binding rides on the parser's frame stack. */

/* User data: written to NdecCtx.user_data by the driver, cast and
 * read/written by the reactor. Holds only non-frame state: yield channel,
 * scratch buffer, decode options, shared atof context, and buf_end (used
 * for token boundary checks on the number padded path). */
typedef struct NdecBindUserData {
  uint32_t pending_action;    /* off  0 NdecYieldAction */
  uint32_t pending_field_idx; /* off  4 field index that triggered the yield */

  const uint8_t *raw_ptr; /* off  8 yield payload */
  uint32_t raw_len;       /* off 16 */
  uint8_t yield_flags;    /* off 20 NdecYieldFlags */
  uint8_t _pad0[3];       /* off 21 */

  uint32_t opt_flags; /* off 24 NDEC_OPT_* */
  uint32_t _pad1;     /* off 28 */

  uint8_t *scratch_ptr; /* off 32 unescape scratch buffer */
  uint32_t scratch_cap; /* off 40 */
  uint32_t scratch_len; /* off 44 */

  /* atof_ctx reuse slot: the driver pre-fills this at entry with a pointer
   * to a driver-owned 1976 B buffer, shared across all number fields in a
   * single Unmarshal call. void* decouples the atof.h header dependency;
   * the binding layer (ndec_bind_number.h) casts it back to atof_ctx*.
   * See the atof_ctx comment in number.h: it must not live on the main
   * function stack, otherwise the 1976 B buffer bloats the stack frame
   * and overflows under Go's NOSPLIT trampoline. */
  void *atof_ctx; /* off 48 */

  /* buf_end: end of input buffer (one past the last byte). The number
   * scalar hook checks buf_end - raw.ptr - raw.len in the float case to
   * determine whether at least NDEC_ATOF_PADDED_TAIL bytes remain after
   * the token; if so, it takes atof's padded path (skip bound checks).
   * Written once at driver entry; stable across tokens. */
  const uint8_t *buf_end; /* off 56 */

  /* kvBuf cursor: used by the BEGIN_MAP fast path. The driver pre-fills a
   * segment from driverState.kvBuf at entry, syncing (base, cap) here;
   * re-synced after every reserveMapKVBuf grow / shrinkKvBufTo. When C's
   * ndec_bind_begin_map_fast sees a STRUCT.map field or MAP<map>, it bumps
   * len directly; only on capacity exhaustion does it fall back to
   * NDEC_YA_BEGIN_MAP yield.
   *
   * When Go grows, the old base is invalidated. The sync must rebase all
   * live MAP frames' bind_slice_hdr (Go-side rebaseLiveMapFrameBases
   * handles this). */
  uint8_t *kv_buf_base; /* off 64 */
  uint32_t kv_buf_len;  /* off 72 */
  uint32_t kv_buf_cap;  /* off 76 */

  uint8_t _pad2[16]; /* off 80 pad to 96 (cache line + 1/2) */
} NdecBindUserData;

_Static_assert(sizeof(NdecBindUserData) == 96, "NdecBindUserData size drift");

#endif /* NDEC_GO_ABI_H */
