/*
 * encvm_types.h — Velox JSON C Engine: Type Definitions & Constants
 *
 * Shared enums, structs, and constants used by all encoder modules.
 * Included first by encvm.h — no dependencies on other encvm_*.h files.
 */

#ifndef VJ_ENCVM_TYPES_H
#define VJ_ENCVM_TYPES_H

// clang-format off

#include "encvm_memory.h"

/* ================================================================
 *  Constants
 * ================================================================ */

#define VJ_MAX_DEPTH 16

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

  /* Hot resume: skip opening '{' and resume mid-struct encoding.
   * Set by Go when re-entering C after handling a fallback field. */
  VJ_ENC_RESUME = 1 << 7,

  /* Used with VJ_ENC_RESUME: if set, no field has been written yet
   * (first=1, no comma prefix needed). If clear, at least one field
   * was written (first=0, comma needed before next field). */
  VJ_ENC_RESUME_FIRST = 1 << 8,
};

#define VJ_ENC_DEFAULT (VJ_ENC_ESCAPE_INVALID_UTF8 | VJ_ENC_ESCAPE_LINE_TERMS)
#define VJ_ENC_STD_COMPAT (VJ_ENC_DEFAULT | VJ_ENC_ESCAPE_HTML)


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
 * ================================================================ */

enum VjFrameType {
  VJ_FRAME_STRUCT = 0,
  VJ_FRAME_SLICE  = 1,
  VJ_FRAME_IFACE  = 2,
};

typedef struct VjStackFrame {
  /* Common fields (all frame types) */
  const VjOpStep  *ret_op;      /*  0: return instruction pointer */
  const uint8_t   *ret_base;    /*  8: parent struct/elem base address */
  int32_t  first;               /* 16: parent first-field flag */
  int32_t  frame_type;          /* 20: VJ_FRAME_STRUCT / SLICE / IFACE */

  /* Interface call (VJ_FRAME_IFACE only) */
  const VjOpStep  *ret_ops;     /* 24: parent ops base address */

  /* Loop fields (VJ_FRAME_SLICE) */
  const uint8_t   *iter_data;   /* 32: slice data start */
  int64_t  iter_count;          /* 40: total elements */
  int64_t  iter_idx;            /* 48: current index */
  int32_t  elem_size;           /* 56: element size in bytes */
  int32_t  _pad;                /* 60: alignment */
  const VjOpStep  *loop_pc_op;  /* 64: loop body first instruction */
} VjStackFrame;

_Static_assert(sizeof(VjStackFrame) == 72, "VjStackFrame must be 72 bytes");

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
 *  ExecCtx — per-Marshal runtime context (1248 bytes)
 * ================================================================ */

typedef struct VjExecCtx {
  /* Output buffer */
  uint8_t         *buf_cur;           /*   0: current write position */
  uintptr_t        buf_end;           /*   8: one past last byte (not a GC ptr) */

  /* Instruction pointer */
  int32_t          pc;                /*  16: current instruction index */
  int32_t          _pad1;             /*  20: alignment */

  /* Data source */
  const uint8_t   *cur_base;         /*  24: current struct/elem base */

  /* State */
  int32_t          depth;             /*  32: stack depth */
  int32_t          error_code;        /*  36: VjError value */
  uint32_t         enc_flags;         /*  40: VjEncFlags bitmask */
  uint32_t         yield_info;        /*  44: VjYieldReason */

  /* Instruction reference (read-only) */
  const VjOpStep  *ops_ptr;          /*  48: &Blueprint.Ops[0] */
  uintptr_t        _reserved56;      /*  56: reserved */

  /* Interface cache */
  const VjIfaceCacheEntry *iface_cache_ptr;  /*  64: sorted array */
  int32_t          iface_cache_count;        /*  72: count */
  int32_t          _pad2;                    /*  76: alignment */

  /* Yield metadata */
  const void      *yield_type_ptr;   /*  80: eface.type_ptr on iface miss */
  int32_t          yield_field_idx;   /*  88: field index for fallback */
  int32_t          _pad3;             /*  92: alignment */

  /* Stack */
  VjStackFrame     stack[VJ_MAX_DEPTH]; /*  96: stack frames */
} VjExecCtx;

_Static_assert(sizeof(VjExecCtx) == 1248, "VjExecCtx must be 1248 bytes");
_Static_assert(offsetof(VjExecCtx, buf_cur) == 0, "buf_cur offset");
_Static_assert(offsetof(VjExecCtx, buf_end) == 8, "buf_end offset");
_Static_assert(offsetof(VjExecCtx, pc) == 16, "pc offset");
_Static_assert(offsetof(VjExecCtx, cur_base) == 24, "cur_base offset");
_Static_assert(offsetof(VjExecCtx, depth) == 32, "depth offset");
_Static_assert(offsetof(VjExecCtx, error_code) == 36, "error_code offset");
_Static_assert(offsetof(VjExecCtx, enc_flags) == 40, "enc_flags offset");
_Static_assert(offsetof(VjExecCtx, yield_info) == 44, "yield_info offset");
_Static_assert(offsetof(VjExecCtx, ops_ptr) == 48, "ops_ptr offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_ptr) == 64,
               "iface_cache_ptr offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_count) == 72,
               "iface_cache_count offset");
_Static_assert(offsetof(VjExecCtx, yield_type_ptr) == 80,
               "yield_type_ptr offset");
_Static_assert(offsetof(VjExecCtx, yield_field_idx) == 88,
               "yield_field_idx offset");
_Static_assert(offsetof(VjExecCtx, stack) == 96, "stack offset");

#endif /* VJ_ENCVM_TYPES_H */
