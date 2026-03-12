/*
 * types.h — Velox JSON C Engine: Type Definitions & Constants
 *
 * Shared enums, structs, and constants used by all encoder modules.
 * Included first by encvm.h — no dependencies on other impl headers.
 */

#ifndef VJ_ENCVM_TYPES_H
#define VJ_ENCVM_TYPES_H

// clang-format off

#include <stddef.h>
#include <stdint.h>

#define VJ_MAX_DEPTH 24

/* ================================================================
 *  Debug Trace Ring Buffer
 *
 *  When VJ_ENCVM_DEBUG is defined, VjExecCtx gains a pointer to
 *  an external VjTraceBuf.  C writes trace entries via VM_TRACE();
 *  Go reads/prints them on each buffer exit.
 *  Size must be a power of 2 for fast modular indexing.
 * ================================================================ */

#define VJ_TRACE_BUF_SIZE 16384  /* 16KB */

typedef struct VjTraceBuf {
  uint32_t head;                      /* write position (wraps via & mask) */
  uint32_t total;                     /* total bytes ever written (overflow detect) */
  uint8_t  data[VJ_TRACE_BUF_SIZE];  /* ring buffer */
} VjTraceBuf;

/* ================================================================
 *  OpType — VM instruction opcodes
 *
 *  0-13:  Primitives (aligned with Go ElemTypeKind for direct cast)
 *  16-19: Non-primitive data ops (interface, raw, number, byte_slice)
 *  32-40: Structural control-flow (skip, struct, ptr, slice, map)
 *  0x3F:  Go-only fallback
 *  0xFF:  End sentinel
 *
 *  Sparse layout — gaps between groups allow future expansion.
 *  Dispatch table is 0x40 entries; unused slots caught by bounds check.
 * ================================================================ */

enum OpType {
  /* --- Primitives (0-13, match ElemTypeKind) --- */
  OP_BOOL    = 0,
  OP_INT     = 1,  /* Go int  — 8 bytes on 64-bit */
  OP_INT8    = 2,
  OP_INT16   = 3,
  OP_INT32   = 4,
  OP_INT64   = 5,
  OP_UINT    = 6,  /* Go uint — 8 bytes on 64-bit */
  OP_UINT8   = 7,
  OP_UINT16  = 8,
  OP_UINT32  = 9,
  OP_UINT64  = 10,
  OP_FLOAT32 = 11,
  OP_FLOAT64 = 12,
  OP_STRING  = 13, /* Go string {ptr, len} */

  /* --- Non-primitive data ops (16-19) --- */
  OP_INTERFACE   = 16, /* interface{} — noinline C encoder or yield */
  OP_RAW_MESSAGE = 17, /* json.RawMessage — direct byte copy */
  OP_NUMBER      = 18, /* json.Number — direct string copy */
  OP_BYTE_SLICE  = 19, /* []byte — base64 encode, yield to Go */

  /* --- Structural control-flow opcodes (32-40) --- */
  OP_SKIP_IF_ZERO = 32, /* conditional forward jump (omitempty) */
  OP_STRUCT_BEGIN = 33, /* push frame, write '{' */
  OP_STRUCT_END   = 34, /* write '}', pop frame */
  OP_PTR_DEREF    = 35, /* deref pointer, nil→null+jump */
  OP_PTR_END      = 36, /* pop ptr-deref frame, restore base */
  OP_SLICE_BEGIN  = 37, /* slice loop start */
  OP_SLICE_END    = 38, /* slice loop end / back-edge */
  OP_MAP_BEGIN    = 39, /* map iteration start (yield-driven) */
  OP_MAP_END      = 40, /* map iteration end */
  OP_OBJ_OPEN     = 41, /* write key + '{', set first=1 (no frame) */
  OP_OBJ_CLOSE    = 42, /* write '}', set first=0 (no frame) */
  OP_ARRAY_BEGIN  = 43, /* array loop start (inline data, fixed length) */
  OP_MAP_STR_KV   = 44, /* encode one map[string]string entry (key + value, both escaped) */
  OP_MAP_STR_STR  = 45, /* C-native Swiss Map iteration for map[string]string */

  /* --- Go-only fallback --- */
  OP_FALLBACK    = 0x3F, /* custom marshalers, ,string, complex structs */

  /* --- Sentinel --- */
  OP_END = 0xFF, /* end of instruction stream */
};

/* Dispatch table size — must cover all opcodes up to OP_FALLBACK (0x3F). */
#define OP_DISPATCH_COUNT 0x40

/* Lower byte = real opcode, stripped before dispatch-table lookup. */
#define OP_TYPE_MASK      0x00FF

/* ================================================================
 *  ZeroCheckTag — omitempty zero-value check tags
 *
 *  Encoded in the high byte of OP_SKIP_IF_ZERO's op_type field.
 *  Values match Go ElemTypeKind (0-22) so Go can cast directly.
 *  Separate from OpType: Struct/Slice/Pointer/Map have zero-check
 *  tags but no corresponding instruction opcodes.
 * ================================================================ */

enum ZeroCheckTag {
  ZCT_BOOL    = 0,
  ZCT_INT     = 1,
  ZCT_INT8    = 2,
  ZCT_INT16   = 3,
  ZCT_INT32   = 4,
  ZCT_INT64   = 5,
  ZCT_UINT    = 6,
  ZCT_UINT8   = 7,
  ZCT_UINT16  = 8,
  ZCT_UINT32  = 9,
  ZCT_UINT64  = 10,
  ZCT_FLOAT32 = 11,
  ZCT_FLOAT64 = 12,
  ZCT_STRING  = 13,
  ZCT_STRUCT  = 14,
  ZCT_SLICE   = 15,
  ZCT_POINTER = 16,
  ZCT_INTERFACE = 17,
  ZCT_MAP     = 18,
  ZCT_RAW_MESSAGE = 19,
  ZCT_NUMBER  = 20,
  ZCT_BYTE_SLICE = 21,
  ZCT_FALLBACK = 22,
};

/* ================================================================
 *  Go Runtime Type Layouts
 * ================================================================ */

typedef struct {
  const uint8_t *ptr;
  int64_t len;
} GoString;

_Static_assert(sizeof(GoString) == 16,
               "GoString must be 16 bytes (matching Go string layout)");

typedef struct {
  const uint8_t *data;
  int64_t len;
  int64_t cap;
} GoSlice;

_Static_assert(sizeof(GoSlice) == 24,
               "GoSlice must be 24 bytes (matching Go slice layout)");

/* ================================================================
 *  vj_is_zero — omitempty zero-value check
 *
 *  Matches Go's semantics: float -0.0 == 0, nil pointer/interface,
 *  empty string/slice, nil map.  Struct/fallback always return false.
 * ================================================================ */

static inline int vj_is_zero(const uint8_t *ptr, uint16_t zct) {
  switch (zct) {
  case ZCT_BOOL:
    return *(const uint8_t *)ptr == 0;
  case ZCT_INT8:
    return *(const int8_t *)ptr == 0;
  case ZCT_UINT8:
    return *(const uint8_t *)ptr == 0;
  case ZCT_INT16:
    return *(const int16_t *)ptr == 0;
  case ZCT_UINT16:
    return *(const uint16_t *)ptr == 0;
  case ZCT_INT32:
    return *(const int32_t *)ptr == 0;
  case ZCT_UINT32:
    return *(const uint32_t *)ptr == 0;
  case ZCT_FLOAT32: {
    float v;
    __builtin_memcpy(&v, ptr, 4);
    return v == 0;
  }
  case ZCT_INT:
  case ZCT_INT64:
    return *(const int64_t *)ptr == 0;
  case ZCT_UINT:
  case ZCT_UINT64:
    return *(const uint64_t *)ptr == 0;
  case ZCT_FLOAT64: {
    double v;
    __builtin_memcpy(&v, ptr, 8);
    return v == 0;
  }
  case ZCT_STRING:
    return ((const GoString *)ptr)->len == 0;
  case ZCT_NUMBER:
    /* json.Number is a Go string — zero means empty string. */
    return ((const GoString *)ptr)->len == 0;
  case ZCT_RAW_MESSAGE: {
    /* json.RawMessage is a Go []byte — zero means nil or len==0. */
    const GoSlice *s = (const GoSlice *)ptr;
    return s->data == NULL || s->len == 0;
  }
  case ZCT_POINTER:
    return *(const void *const *)ptr == NULL;
  case ZCT_INTERFACE:
    /* nil interface = zero value (eface.type_ptr == NULL). */
    return *(const void *const *)ptr == NULL;
  case ZCT_SLICE:
  case ZCT_BYTE_SLICE: {
    /* Slice is "empty" for omitempty when len == 0. */
    const GoSlice *s = (const GoSlice *)ptr;
    return s->len == 0;
  }
  case ZCT_MAP: {
    /* Map header is a single pointer; nil map → zero. */
    return *(const void *const *)ptr == NULL;
  }
  case ZCT_STRUCT: {
    /* Struct is never considered "zero" for omitempty by stdlib.
     * The Go fallback handles struct omitempty via IsZeroFn. */
    return 0;
  }
  case ZCT_FALLBACK:
    /* Go-only fallback: the memory layout is unknown to C.
     * Never skip — the Go fallback handler checks omitempty. */
    return 0;
  default:
    return 0; /* unknown tag — never skip */
  }
}

/* ================================================================
 *  Error Codes
 * ================================================================ */

enum VjError {
  VJ_OK = 0,
  VJ_ERR_BUF_FULL = 1,
  VJ_ERR_GO_FALLBACK = 2,
  VJ_ERR_STACK_OVERFLOW = 3,
  VJ_ERR_CYCLE = 4,
  VJ_ERR_NAN_INF = 5,
};

/* ================================================================
 *  Encoding Flags
 * ================================================================ */

enum VjEncFlags {
  VJ_ENC_ESCAPE_HTML = 1 << 0,
  VJ_ENC_ESCAPE_LINE_TERMS = 1 << 1,
  VJ_ENC_ESCAPE_INVALID_UTF8 = 1 << 2,
  VJ_ENC_FLOAT_EXP_AUTO = 1 << 3, /* scientific notation for |f|<1e-6 or |f|>=1e21 */

  /* Hot resume: skip opening '{' and resume mid-struct encoding.
   * Set by Go when re-entering C after handling a fallback field. */
  VJ_ENC_RESUME = 1 << 7,

  /* Used with VJ_ENC_RESUME: if set, no field has been written yet
   * (first=1, no comma prefix needed). If clear, at least one field
   * was written (first=0, comma needed before next field). */
  VJ_ENC_RESUME_FIRST = 1 << 8,
};

#define VJ_ENC_DEFAULT (VJ_ENC_ESCAPE_INVALID_UTF8 | VJ_ENC_ESCAPE_LINE_TERMS)
#define VJ_ENC_STD_COMPAT (VJ_ENC_DEFAULT | VJ_ENC_ESCAPE_HTML | VJ_ENC_FLOAT_EXP_AUTO)


/* ================================================================
 *  OpStep — 24-byte VM instruction
 * ================================================================ */


typedef struct VjOpStep {
  uint16_t op_type;     /*  0: opcode | flags (high byte = ZeroCheckTag for OP_SKIP_IF_ZERO) */
  uint16_t key_len;     /*  2: pre-encoded key length */
  uint32_t field_off;   /*  4: field offset in struct */
  const char *key_ptr;  /*  8: pointer to pre-encoded key bytes */
  int32_t  operand_a;   /* 16: jump offset / elem_size / field_idx */
  int32_t  operand_b;   /* 20: body length / reserved */
} VjOpStep;

_Static_assert(sizeof(VjOpStep) == 24, "VjOpStep must be 24 bytes");
_Static_assert(offsetof(VjOpStep, key_ptr) == 8, "VjOpStep.key_ptr offset");
_Static_assert(offsetof(VjOpStep, operand_a) == 16, "VjOpStep.operand_a offset");
_Static_assert(offsetof(VjOpStep, operand_b) == 20, "VjOpStep.operand_b offset");

/* ================================================================
 *  Stack Frame Types & Layout
 *
 *  Unified call/loop stack.  Every frame stores a full "return
 *  address" (ret_ops, ret_pc, ret_base) so the VM can switch
 *  between multiple ops arrays (needed for future OP_CALL/OP_RET
 *  and ops-based JIT).
 *
 *  VJ_FRAME_CALL — struct, ptr, interface, future call frames.
 *  VJ_FRAME_LOOP — slice / array iteration (needs extra state).
 * ================================================================ */

enum VjFrameType {
  VJ_FRAME_CALL = 0,  /* generic call frame (struct, ptr, iface) */
  VJ_FRAME_LOOP = 1,  /* loop iteration frame (slice, array) */
  VJ_FRAME_MAP  = 2,  /* map[string]string C-native iteration */
};

typedef struct VjStackFrame {
  /* ---- Common fields (0-31): return address ---- */
  const VjOpStep *ret_ops;     /*  0: parent ops array base */
  int32_t         ret_pc;      /*  8: return PC (index into ret_ops) */
  int32_t         frame_type;  /* 12: VJ_FRAME_CALL or VJ_FRAME_LOOP */
  const uint8_t  *ret_base;    /* 16: parent data base address */
  int32_t         first;       /* 24: parent first-field flag */
  int32_t         elem_size;   /* 28: element size (LOOP only, 0 for CALL) */

  /* ---- Frame-type-specific (32-55) ---- */
  union {
    /* VJ_FRAME_CALL: no extra data; ret_ops/ret_pc/ret_base suffice. */
    struct {
      int64_t _reserved[3];  /* 24 bytes reserved */
    } call;

    /* VJ_FRAME_LOOP: slice/array iteration state */
    struct {
      const uint8_t  *iter_data;   /* 32: data start pointer */
      int64_t         iter_count;  /* 40: total element count */
      int64_t         iter_idx;    /* 48: current index (0-based) */
    } loop;  /* 24 bytes */

    /* VJ_FRAME_MAP: Swiss Map (map[string]string) iteration state */
    struct {
      const void *map_ptr;       /* 32: GoSwissMap* */
      int32_t     dir_idx;       /* 40: directory index (large map) */
      int32_t     group_idx;     /* 44: group index within current table */
      int32_t     slot_idx;      /* 48: slot index within current group (0-7) */
      int32_t     remaining;     /* 52: entries left to encode */
    } map;  /* 24 bytes */
  };
} VjStackFrame;

_Static_assert(sizeof(VjStackFrame) == 56, "VjStackFrame must be 56 bytes");
_Static_assert(sizeof(((VjStackFrame *)0)->map) == 24, "VjStackFrame.map must be 24 bytes");
_Static_assert(offsetof(VjStackFrame, ret_ops) == 0, "ret_ops offset");
_Static_assert(offsetof(VjStackFrame, ret_pc) == 8, "ret_pc offset");
_Static_assert(offsetof(VjStackFrame, frame_type) == 12, "frame_type offset");
_Static_assert(offsetof(VjStackFrame, ret_base) == 16, "ret_base offset");
_Static_assert(offsetof(VjStackFrame, first) == 24, "first offset");
_Static_assert(offsetof(VjStackFrame, elem_size) == 28, "elem_size offset");

/* ================================================================
 *  YieldCodes
 * ================================================================ */

enum {
  VJ_ERR_YIELD = 6,  /* VM yielded to Go */
};

enum VjYieldReason {
  VJ_YIELD_FALLBACK   = 1,  /* custom marshaler / unsupported */
  VJ_YIELD_IFACE_MISS = 2,  /* interface cache miss */
  VJ_YIELD_MAP_NEXT   = 3,  /* map iteration */
};

/* ================================================================
 *  Interface Cache Entry — 24 bytes
 * ================================================================ */

typedef struct VjIfaceCacheEntry {
  const void      *type_ptr;  /*  0: Go *abi.Type address */
  const VjOpStep  *ops;       /*  8: Blueprint.Ops[0], or NULL */
  uint8_t          tag;       /* 16: (opcode+1) for primitives, 0 = none */
  uint8_t          _pad[7];   /* 17: alignment */
} VjIfaceCacheEntry;

_Static_assert(sizeof(VjIfaceCacheEntry) == 24,
               "VjIfaceCacheEntry must be 24 bytes");

/* ================================================================
 *  ExecCtx — per-Marshal runtime context (1448 bytes)
 *
 *  Layout optimized for cache locality:
 *    Cache line 0 (0-63):  hot VM registers (buf, ops, pc, base, flags)
 *    Cache line 1 (64-95): indent state, yield metadata
 *    96+:                  stack frames + debug trace
 * ================================================================ */

typedef struct VjExecCtx {
  /* ===== Cache Line 0: Hot VM Registers (0-63) ===== */

  /* Output buffer */
  uint8_t         *buf_cur;           /*   0: current write position */
  uintptr_t        buf_end;           /*   8: one past last byte (not a GC ptr) */

  /* Program counter (ops_ptr + pc form the "instruction pointer") */
  const VjOpStep  *ops_ptr;           /*  16: &Blueprint.Ops[0] (current ops array) */
  int32_t          pc;                /*  24: current instruction index */
  int32_t          depth;             /*  28: stack depth (0 = top-level) */

  /* Data source */
  const uint8_t   *cur_base;          /*  32: current struct/elem base */

  /* State flags */
  uint32_t         enc_flags;         /*  40: VjEncFlags bitmask */
  int32_t          error_code;        /*  44: VjError value */

  /* Interface cache (hot: checked on every interface{} field) */
  const VjIfaceCacheEntry *iface_cache_ptr;  /*  48: sorted array */
  int32_t          iface_cache_count;        /*  56: entry count */
  uint32_t         yield_info;               /*  60: VjYieldReason */

  /* ===== Cache Line 1: Less-Hot State (64-95) ===== */

  /* Indent state (cold in compact mode, warm in indent mode) */
  const uint8_t   *indent_tpl;        /*  64: precomputed indent template */
  int16_t          indent_depth;      /*  72: logical nesting depth */
  uint8_t          indent_step;       /*  74: bytes per indent level (0 = compact) */
  uint8_t          indent_prefix_len; /*  75: bytes of prefix before indent */
  int32_t          _pad1;             /*  76: alignment padding */

  /* Yield metadata (cold: only accessed on yield) */
  const void      *yield_type_ptr;    /*  80: eface.type_ptr on iface miss */
  int32_t          yield_field_idx;   /*  88: field index for fallback */
  int32_t          _pad2;             /*  92: alignment padding */

  /* ===== Stack (96-1439) ===== */
  VjStackFrame     stack[VJ_MAX_DEPTH]; /*  96: 24 frames × 56 bytes = 1344 */

  /* Debug trace (always present for layout stability; only written when
   * VJ_ENCVM_DEBUG is defined and the pointer is non-NULL). */
  VjTraceBuf      *trace_buf;          /* 1440: Go-allocated trace buffer */
} VjExecCtx;

_Static_assert(sizeof(VjExecCtx) == 1448, "VjExecCtx must be 1448 bytes");
_Static_assert(offsetof(VjExecCtx, buf_cur) == 0, "buf_cur offset");
_Static_assert(offsetof(VjExecCtx, buf_end) == 8, "buf_end offset");
_Static_assert(offsetof(VjExecCtx, ops_ptr) == 16, "ops_ptr offset");
_Static_assert(offsetof(VjExecCtx, pc) == 24, "pc offset");
_Static_assert(offsetof(VjExecCtx, depth) == 28, "depth offset");
_Static_assert(offsetof(VjExecCtx, cur_base) == 32, "cur_base offset");
_Static_assert(offsetof(VjExecCtx, enc_flags) == 40, "enc_flags offset");
_Static_assert(offsetof(VjExecCtx, error_code) == 44, "error_code offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_ptr) == 48,
               "iface_cache_ptr offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_count) == 56,
               "iface_cache_count offset");
_Static_assert(offsetof(VjExecCtx, yield_info) == 60, "yield_info offset");
_Static_assert(offsetof(VjExecCtx, indent_tpl) == 64, "indent_tpl offset");
_Static_assert(offsetof(VjExecCtx, indent_depth) == 72,
               "indent_depth offset");
_Static_assert(offsetof(VjExecCtx, indent_step) == 74, "indent_step offset");
_Static_assert(offsetof(VjExecCtx, indent_prefix_len) == 75,
               "indent_prefix_len offset");
_Static_assert(offsetof(VjExecCtx, yield_type_ptr) == 80,
               "yield_type_ptr offset");
_Static_assert(offsetof(VjExecCtx, yield_field_idx) == 88,
               "yield_field_idx offset");
_Static_assert(offsetof(VjExecCtx, stack) == 96, "stack offset");
_Static_assert(offsetof(VjExecCtx, trace_buf) == 1440, "trace_buf offset");

/* ================================================================
 *  Swiss Map Structs — map[string]string only
 *
 *  These mirror the Go runtime's internal/runtime/maps layout.
 *  Offsets verified at Go init time (rt_internal.go).
 *  Only used for readonly iteration — C never writes to these.
 * ================================================================ */

/* Constants for map[string]string (strings < 128 bytes → inline slots) */
#define SWISS_GROUP_SLOTS     8
#define SWISS_CTRL_SIZE       8      /* sizeof(ctrlGroup) = uint64 */
#define SWISS_SLOT_SIZE       32     /* sizeof(GoString key) + sizeof(GoString elem) */
#define SWISS_ELEM_OFF        16     /* elem starts at key + 16 */
#define SWISS_GROUP_SIZE      264    /* CTRL_SIZE + GROUP_SLOTS * SLOT_SIZE */
#define SWISS_CTRL_EMPTY      0x80   /* bit 7 set = empty or deleted */

/* GoSwissMap mirrors internal/runtime/maps.Map (48 bytes). */
typedef struct GoSwissMap {
  uint64_t  used;           /*  0: element count */
  uintptr_t seed;           /*  8: hash seed (unused by us) */
  void     *dir_ptr;        /* 16: → group (small) or → *table[] (large) */
  int64_t   dir_len;        /* 24: 0 = small map, else 1<<globalDepth */
  uint8_t   global_depth;   /* 32 */
  uint8_t   global_shift;   /* 33 */
  uint8_t   writing;        /* 34 (unused by us) */
  uint8_t   _pad_tombstone; /* 35: tombstonePossible (unused by us) */
  uint32_t  _pad36;         /* 36: alignment padding */
  uint64_t  clear_seq;      /* 40 (unused by us) */
} GoSwissMap;

_Static_assert(sizeof(GoSwissMap) == 48, "GoSwissMap must be 48 bytes");
_Static_assert(offsetof(GoSwissMap, used) == 0, "GoSwissMap.used offset");
_Static_assert(offsetof(GoSwissMap, dir_ptr) == 16, "GoSwissMap.dir_ptr offset");
_Static_assert(offsetof(GoSwissMap, dir_len) == 24, "GoSwissMap.dir_len offset");
_Static_assert(offsetof(GoSwissMap, global_depth) == 32, "GoSwissMap.global_depth offset");
_Static_assert(offsetof(GoSwissMap, clear_seq) == 40, "GoSwissMap.clear_seq offset");

/* GoSwissTable mirrors internal/runtime/maps.table (32 bytes).
 * The groups field is a groupsReference {data unsafe.Pointer, lengthMask uint64}. */
typedef struct GoSwissTable {
  uint16_t  used;           /*  0: entries in this table */
  uint16_t  capacity;       /*  2: total capacity */
  uint16_t  growth_left;    /*  4 */
  uint8_t   local_depth;    /*  6 */
  uint8_t   _pad7;          /*  7: alignment */
  int64_t   index;          /*  8: -1 = stale */
  /* groupsReference: */
  void     *groups_data;    /* 16: → first group */
  uint64_t  groups_mask;    /* 24: num_groups - 1 */
} GoSwissTable;

_Static_assert(sizeof(GoSwissTable) == 32, "GoSwissTable must be 32 bytes");
_Static_assert(offsetof(GoSwissTable, used) == 0, "GoSwissTable.used offset");
_Static_assert(offsetof(GoSwissTable, local_depth) == 6, "GoSwissTable.local_depth offset");
_Static_assert(offsetof(GoSwissTable, index) == 8, "GoSwissTable.index offset");
_Static_assert(offsetof(GoSwissTable, groups_data) == 16, "GoSwissTable.groups_data offset");
_Static_assert(offsetof(GoSwissTable, groups_mask) == 24, "GoSwissTable.groups_mask offset");

#endif /* VJ_ENCVM_TYPES_H */
