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

#define VJ_MAX_DEPTH 64

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
 *  OpType — VM instruction opcodes (compact, contiguous numbering)
 *
 *  1-14:  Primitives (= ElemTypeKind, must not change)
 *  15-18: Non-primitive data ops
 *  19-31: Structural control-flow
 *  32:    Go-only fallback
 *
 *  Compact layout — no gaps; dispatch table = 33 entries (2 cache lines).
 * ================================================================ */

enum OpType {
  /* --- Primitives (1-14, = ElemTypeKind) --- */
  OP_BOOL    = 1,
  OP_INT     = 2,  /* Go int  — 8 bytes on 64-bit */
  OP_INT8    = 3,
  OP_INT16   = 4,
  OP_INT32   = 5,
  OP_INT64   = 6,
  OP_UINT    = 7,  /* Go uint — 8 bytes on 64-bit */
  OP_UINT8   = 8,
  OP_UINT16  = 9,
  OP_UINT32  = 10,
  OP_UINT64  = 11,
  OP_FLOAT32 = 12,
  OP_FLOAT64 = 13,
  OP_STRING  = 14, /* Go string {ptr, len} */

  /* --- Non-primitive data ops (15-18) --- */
  OP_INTERFACE   = 15, /* interface{} — noinline C encoder or yield */
  OP_RAW_MESSAGE = 16, /* json.RawMessage — direct byte copy */
  OP_NUMBER      = 17, /* json.Number — direct string copy */
  OP_BYTE_SLICE  = 18, /* []byte — base64 encode, yield to Go */

  /* --- Structural control-flow opcodes (19-31) --- */
  OP_SKIP_IF_ZERO = 19, /* conditional forward jump (omitempty) */
  OP_CALL         = 20, /* subroutine call: push CALL frame, jump to ops[operand_a] */
  OP_PTR_DEREF    = 21, /* deref pointer, nil→null+jump */
  OP_PTR_END      = 22, /* pop ptr-deref frame, restore base */
  OP_SLICE_BEGIN  = 23, /* slice loop start */
  OP_SLICE_END    = 24, /* slice loop end / back-edge */
  OP_MAP_BEGIN    = 25, /* map iteration start (yield-driven) */
  OP_MAP_END      = 26, /* map iteration end */
  OP_OBJ_OPEN     = 27, /* write key + '{', set first=1 (no frame) */
  OP_OBJ_CLOSE    = 28, /* write '}', set first=0 (no frame) */
  OP_ARRAY_BEGIN  = 29, /* array loop start (inline data, fixed length) */
  OP_MAP_STR_STR  = 30, /* C-native Swiss Map iteration for map[string]string */
  OP_RET          = 31, /* subroutine return: pop CALL frame, restore ops/pc/base */

  /* --- Go-only fallback --- */
  OP_FALLBACK    = 32, /* custom marshalers, ,string, complex structs */

  /* --- Keyed-field variants (33-35) --- */
  OP_KSTRING     = 33, /* struct field string — unconditional key write */
  OP_KINT        = 34, /* struct field int — unconditional key write */
  OP_KINT64      = 35, /* struct field int64 — unconditional key write */
};

/* Dispatch table size — compact: covers all opcodes 0..35 (36 entries). */
#define OP_DISPATCH_COUNT 36


/* ================================================================
 *  ZeroCheckTag — omitempty zero-value check tags
 *
 *  Encoded in OP_SKIP_IF_ZERO's operand_b field.
 *  Values = Go ElemTypeKind (1-based); Go casts directly.
 * ================================================================ */

enum ZeroCheckTag {
  ZCT_BOOL    = 1,
  ZCT_INT     = 2,
  ZCT_INT8    = 3,
  ZCT_INT16   = 4,
  ZCT_INT32   = 5,
  ZCT_INT64   = 6,
  ZCT_UINT    = 7,
  ZCT_UINT8   = 8,
  ZCT_UINT16  = 9,
  ZCT_UINT32  = 10,
  ZCT_UINT64  = 11,
  ZCT_FLOAT32 = 12,
  ZCT_FLOAT64 = 13,
  ZCT_STRING  = 14,
  ZCT_STRUCT  = 15,
  ZCT_SLICE   = 16,
  ZCT_POINTER = 17,
  ZCT_INTERFACE = 18,
  ZCT_MAP     = 19,
  ZCT_RAW_MESSAGE = 20,
  ZCT_NUMBER  = 21,
  ZCT_BYTE_SLICE = 22,
  ZCT_FALLBACK = 23,
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

/* VM exit status codes.
 * NOTE: includes control-flow exits (BUF_FULL) in addition to terminal errors. */
enum VjExitCode {
  VJ_EXIT_OK = 0,
  VJ_EXIT_BUF_FULL = 1,
  VJ_EXIT_GO_FALLBACK = 2,
  VJ_EXIT_STACK_OVERFLOW = 3,
  VJ_EXIT_CYCLE = 4,
  VJ_EXIT_NAN_INF = 5,
};


/* ================================================================
 *  VMState — packed 64-bit VM state register
 *
 *  Packs depth, first flag, encoding config flags, exit code, and
 *  yield reason into a single uint64 register to reduce register
 *  pressure on x86-64.
 *
 *  Layout:
 *    bits [0..7]   = depth        (unified stack depth, max 24)
 *    bits [8..15]  = reserved
 *    bit  [16]     = first        (first-field flag: no comma prefix)
 *    bits [17..31] = enc_flags    (encoding config: escape, float fmt)
 *    bits [32..39] = exit_code    (VjExitCode value)
 *    bits [40..47] = yield_reason (VjYieldReason; valid when exit_code=VJ_EXIT_YIELD)
 *    bits [48..63] = reserved
 *
 *  Hot path uses low 32 bits (single-register ops on x86-64).
 *  Cold path (exit/yield) in high 32 bits (only on VM exit).
 *  'first' is a one-shot comma latch: 0 => write ',' before next item;
 *  VM_WRITE_KEY test-and-clears this bit on each emitted item.
 * ================================================================ */

/* Low 32 bits: hot-path state */
#define VJ_ST_DEPTH_SHIFT    0
#define VJ_ST_DEPTH_MASK     ((uint64_t)0x000000FF)       /* bits [0..7]  */
#define VJ_ST_FIRST_BIT      ((uint64_t)1 << 16)          /* bit  [16]    */
#define VJ_ST_FLAGS_SHIFT    17
#define VJ_ST_FLAGS_MASK     ((uint64_t)0xFFFE0000)       /* bits [17..31]*/

/* High 32 bits: cold-path state */
#define VJ_ST_EXIT_SHIFT      32
#define VJ_ST_EXIT_MASK       ((uint64_t)0x000000FF00000000ULL) /* bits [32..39] */
#define VJ_ST_YIELD_SHIFT    40
#define VJ_ST_YIELD_MASK     ((uint64_t)0x0000FF0000000000ULL) /* bits [40..47] */

/* Access macros — extract fields from vmstate. */
#define VJ_ST_GET_DEPTH(st)   ((int32_t)((st) & VJ_ST_DEPTH_MASK))
#define VJ_ST_GET_FIRST(st)   ((int)(((st) & VJ_ST_FIRST_BIT) != 0))
#define VJ_ST_GET_FLAGS(st)   ((uint32_t)(((st) & VJ_ST_FLAGS_MASK) >> VJ_ST_FLAGS_SHIFT))
#define VJ_ST_GET_EXIT(st)     ((int32_t)(((st) >> VJ_ST_EXIT_SHIFT) & 0xFF))
#define VJ_ST_GET_YIELD(st)   ((uint32_t)(((st) >> VJ_ST_YIELD_SHIFT) & 0xFF))

/* Mutate macros — modify fields within vmstate. */

/* First-flag mutators */
#define VJ_ST_SET_FIRST_1(st)   ((st) |= VJ_ST_FIRST_BIT)
#define VJ_ST_SET_FIRST_0(st)   ((st) &= ~VJ_ST_FIRST_BIT)
#define VJ_ST_SET_EXIT(st, v)    ((st) = ((st) & ~VJ_ST_EXIT_MASK) | (((uint64_t)(v) & 0xFF) << VJ_ST_EXIT_SHIFT))
#define VJ_ST_SET_YIELD(st, v)  ((st) = ((st) & ~VJ_ST_YIELD_MASK) | (((uint64_t)(v) & 0xFF) << VJ_ST_YIELD_SHIFT))

/* Depth increment/decrement — depth at bits [0..7]: +1/-1 directly.
 * Callers MUST check overflow BEFORE incrementing. */
#define VJ_ST_INC_DEPTH(st)   ((st) += 1)
#define VJ_ST_DEC_DEPTH(st)   ((st) -= 1)

/* VJ_ST_BTR_FIRST — test-and-clear of the first flag (bit 16).
 *
 * On x86-64 this emits a single `btrq $16, %reg` instruction which
 * copies bit 16 into CF and clears it in one shot, replacing the
 * compiler's separate testl + unconditional andq sequence.
 *
 * Usage:  int was_first; VJ_ST_BTR_FIRST(vmstate, was_first);
 *         if (!was_first) { write comma; }
 *
 * was_first receives 1 if bit was set (i.e. first element), 0 otherwise.
 */
#if defined(__x86_64__) && (defined(__GNUC__) || defined(__clang__))
#define VJ_ST_BTR_FIRST(st, was_first)                                         \
  __asm__ volatile(                                                            \
      "btrq $16, %[state]"                                                     \
      : [state] "+r"(st), "=@ccc"(was_first)                                   \
      : /* no input-only */                                                     \
      : "cc"                                                                    \
  )
#else
/* Portable fallback: separate test + clear. */
#define VJ_ST_BTR_FIRST(st, was_first)                                         \
  do {                                                                         \
    (was_first) = (int)(((st) & VJ_ST_FIRST_BIT) != 0);                        \
    (st) &= ~VJ_ST_FIRST_BIT;                                                  \
  } while (0)
#endif

/* Encoding config flag masks (bits 0-3 of extracted flags).
 * Used by helper functions that receive a uint32_t 'flags' parameter
 * (e.g. vj_escape_string) and by VJ_ST_GET_FLAGS() extraction. */
#define VJ_FLAGS_ESCAPE_HTML          (1 << 0)
#define VJ_FLAGS_ESCAPE_LINE_TERMS    (1 << 1)
#define VJ_FLAGS_ESCAPE_INVALID_UTF8  (1 << 2)
#define VJ_FLAGS_FLOAT_EXP_AUTO       (1 << 3)

/* ================================================================
 *  Variable-Length Instruction Format
 *
 *  Instructions are either 8 bytes (short) or 16 bytes (long).
 *  The ops stream is a packed byte array with 8-byte alignment.
 *
 *  Short (8 bytes): VjOpHdr only — primitives, OBJ_OPEN/CLOSE, etc.
 *  Long  (16 bytes): VjOpHdr + VjOpExt — SKIP_IF_ZERO, loops, etc.
 *
 *  Each handler knows its own instruction width at compile time.
 *  No runtime size decoding is needed — op_type stores the raw opcode.
 * ================================================================ */

/* VjOpHdr: 8-byte instruction header (common to all instructions) */
typedef struct VjOpHdr {
  uint16_t op_type;     /*  0: opcode (raw value, no flag bits) */
  uint8_t  key_len;     /*  2: pre-encoded key length (max 255) */
  uint8_t  _pad0;       /*  3: alignment padding */
  uint16_t field_off;   /*  4: field offset in struct (max 65535) */
  uint16_t key_off;     /*  6: offset into global key pool */
} VjOpHdr;

_Static_assert(sizeof(VjOpHdr) == 8, "VjOpHdr must be 8 bytes");
_Static_assert(offsetof(VjOpHdr, key_len) == 2, "VjOpHdr.key_len offset");
_Static_assert(offsetof(VjOpHdr, field_off) == 4, "VjOpHdr.field_off offset");
_Static_assert(offsetof(VjOpHdr, key_off) == 6, "VjOpHdr.key_off offset");

/* VjOpExt: 8-byte extension for long instructions */
typedef struct VjOpExt {
  int32_t  operand_a;   /*  0: jump byte offset / elem_size / field_idx */
  int32_t  operand_b;   /*  4: body byte length / ZeroCheckTag */
} VjOpExt;

_Static_assert(sizeof(VjOpExt) == 8, "VjOpExt must be 8 bytes");

/* Access extension (only valid for long instructions). */
#define VJ_OP_EXT(hdr)     ((const VjOpExt *)((const uint8_t *)(hdr) + 8))

/*  Unified Stack Frame
 *
 *  Single interleaved stack.  Instruction pairing (begin/end) guarantees
 *  correct push/pop without per-frame type tags.
 *
 *  Frame type constants are only used for debug/documentation purposes.
 *
 *  Stack depth limit:  VJ_MAX_DEPTH
 * */

/* Frame type constants — documentation/debug only, not stored in vmstate. */
#define VJ_FRAME_CALL           0  /* subroutine call (recurse / ptr deref / iface switch-ops) */
#define VJ_FRAME_ITER           1  /* linear iteration (slice / array) */
#define VJ_FRAME_ITER_STR_STR_LEAF  2  /* map[string]string iteration; leaf = no sub-frame push */

/* VjStackFrame — unified frame pushed by all stack-using ops.
 * 32 bytes.
 *
 * NOTE: 'first' lives in vmstate bit 16 (set on object entry,
 * test-and-clear on key write). Stack frames do not store/restore it.
 *
 * Instruction pairing (begin/end) ensures correct pop semantics
 * without per-frame type tags.
 *
 * body_pc and elem_size for iteration are compile-time constants
 * encoded in the SLICE_END instruction's operands.
 *
 * ret_ops/ret_pc are only used by CALL frames (OP_CALL, INTERFACE
 * switch-ops).  PTR_DEREF, SLICE, ARRAY, MAP_STR_STR never use them.
 * Keeping them inside the call union branch saves 8 bytes per frame. */
typedef struct __attribute__((aligned(8))) VjStackFrame {
  const uint8_t  *ret_base;    /*  0: parent data base (all frame types) */

#pragma pack(push, 4)
  union {                      /*  8-27: frame-type-specific (20 bytes) */
    struct {
      const uint8_t *ret_ops;  /*  8: parent ops byte stream base */
      int32_t        ret_pc;   /* 16: return byte offset */
    } call;  /* 12 bytes */
    struct {
      const uint8_t *iter_data;   /*  8: data start pointer */
      int64_t        iter_count;  /* 16: total element count */
      int32_t        iter_idx;    /* 24: current index (0-based, max ~2B) */
    } seq;  /* 20 bytes */
    struct {
      const void *map_ptr;     /*  8: GoSwissMap* */
      int32_t     dir_idx;     /* 16: directory index (large map) */
      int32_t     remaining;   /* 20: entries left to encode */
      uint8_t     group_idx;   /* 24: group index within current table (max 127) */
      uint8_t     slot_idx;    /* 25: slot index within current group (0-7) */
      uint16_t    _pad;        /* 26: alignment padding */
    } map;  /* 20 bytes */
  };
#pragma pack(pop)

  int32_t         state;       /* 28: bit 0 = iter active (resume detect);
                                *     bits 24-31 = trace depth (debug only) */
} VjStackFrame;

_Static_assert(sizeof(VjStackFrame) == 32, "VjStackFrame must be 32 bytes");
_Static_assert(offsetof(VjStackFrame, ret_base) == 0, "VjStackFrame.ret_base offset");
_Static_assert(offsetof(VjStackFrame, state) == 28, "VjStackFrame.state offset");

enum {
  /* Separate from VJ_EXIT_BUF_FULL:
   * - VJ_EXIT_BUF_FULL: capacity retry (grow/flush buffer, then re-enter C)
   * - VJ_EXIT_YIELD: semantic handoff; Go dispatches by VjYieldReason
   *   before re-entering C, if needed. */
  VJ_EXIT_YIELD = 6,
};

enum VjYieldReason {
  VJ_YIELD_FALLBACK   = 1,  /* custom marshaler / unsupported */
  VJ_YIELD_IFACE_MISS = 2,  /* interface cache miss */
  VJ_YIELD_MAP_HANDOFF = 3,  /* map encoding handoff to Go */
};

/* FallbackReason — encoded in OP_FALLBACK's operand_b by the Go compiler.
 * Describes WHY this field was delegated to Go-side encoding.
 * Only used by debug trace; zero cost in release builds.
 * Values: 0=unknown, 1=json.Marshaler, 2=encoding.TextMarshaler,
 *         3=`,string` tag, 4=[]byte, 5=[N]byte, 6=map+omitempty.
 * Authoritative definition: fbReason* constants in vj_encvm.go.
 * C trace writes "YIELD(fb:N)"; Go post-processes to human labels. */

/* ================================================================
 *  Interface Cache Entry — 24 bytes
 * ================================================================ */

typedef struct VjIfaceCacheEntry {
  const void      *type_ptr;  /*  0: Go *abi.Type address */
  const uint8_t   *ops;       /*  8: Blueprint ops byte stream, or NULL */
  uint8_t          tag;       /* 16: opcode (= ElemTypeKind) for primitives; 0 = none */
  uint8_t          _pad[7];   /* 17: alignment */
} VjIfaceCacheEntry;

_Static_assert(sizeof(VjIfaceCacheEntry) == 24,
               "VjIfaceCacheEntry must be 24 bytes");

/* ================================================================
 *  ExecCtx — per-Marshal runtime context
 *
 *  Layout optimized for cache locality:
 *    Cache line 0 (0-63):  hot VM registers (buf, ops, pc, base, flags)
 *    Cache line 1 (64-95): indent state, yield metadata
 *    96+:                  unified stack + debug trace
 * ================================================================ */

typedef struct VjExecCtx {
  /* ===== Cache Line 0: Hot VM Registers (0-63) ===== */

  /* Output buffer */
  uint8_t         *buf_cur;           /*   0: current write position */
  uintptr_t        buf_end;           /*   8: one past last byte (not a GC ptr) */

  /* Program counter (ops_ptr + pc form the "instruction pointer") */
  const uint8_t   *ops_ptr;           /*  16: &Blueprint.Ops[0] (byte stream) */
  int32_t          pc;                /*  24: current byte offset */
  int32_t          _pad_pc;           /*  28: alignment padding */

  /* Data source */
  const uint8_t   *cur_base;          /*  32: current struct/elem base */

  /* Packed VM state — see VMState layout in types.h. */
  uint64_t         vmstate;           /*  40: packed state register */

  /* Interface cache (hot: checked on every interface{} field) */
  const VjIfaceCacheEntry *iface_cache_ptr;  /*  48: sorted array */
  int32_t          iface_cache_count;        /*  56: entry count */
  int32_t          _pad_iface;               /*  60: alignment padding */

  /* ===== Cache Line 1: Less-Hot State (64-95) ===== */

  /* Indent state (cold in compact mode, warm in indent mode) */
  const uint8_t   *indent_tpl;        /*  64: precomputed indent template */
  int16_t          indent_depth;      /*  72: logical nesting depth */
  uint8_t          indent_step;       /*  74: bytes per indent level (0 = compact) */
  uint8_t          indent_prefix_len; /*  75: bytes of prefix before indent */
  int32_t          _pad1;             /*  76: alignment padding */

  /* Yield metadata (cold: only accessed on yield) */
  const void      *yield_type_ptr;    /*  80: eface.type_ptr on iface miss */
  const uint8_t   *key_pool_base;     /*  88: global key pool base pointer */

  /* ===== Unified Stack (96-2143) ===== */
  VjStackFrame     stack[VJ_MAX_DEPTH]; /*  96: 64 x 32 = 2048 bytes */

  /* Debug trace (always present for layout stability; only written when
   * VJ_ENCVM_DEBUG is defined and the pointer is non-NULL). */
  VjTraceBuf      *trace_buf;          /* 2144: Go-allocated trace buffer */
} VjExecCtx;

_Static_assert(sizeof(VjExecCtx) == 2152, "VjExecCtx size check");
_Static_assert(offsetof(VjExecCtx, buf_cur) == 0, "buf_cur offset");
_Static_assert(offsetof(VjExecCtx, buf_end) == 8, "buf_end offset");
_Static_assert(offsetof(VjExecCtx, ops_ptr) == 16, "ops_ptr offset");
_Static_assert(offsetof(VjExecCtx, pc) == 24, "pc offset");
_Static_assert(offsetof(VjExecCtx, cur_base) == 32, "cur_base offset");
_Static_assert(offsetof(VjExecCtx, vmstate) == 40, "vmstate offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_ptr) == 48,
               "iface_cache_ptr offset");
_Static_assert(offsetof(VjExecCtx, iface_cache_count) == 56,
               "iface_cache_count offset");
_Static_assert(offsetof(VjExecCtx, indent_tpl) == 64, "indent_tpl offset");
_Static_assert(offsetof(VjExecCtx, indent_depth) == 72,
               "indent_depth offset");
_Static_assert(offsetof(VjExecCtx, indent_step) == 74, "indent_step offset");
_Static_assert(offsetof(VjExecCtx, indent_prefix_len) == 75,
               "indent_prefix_len offset");
_Static_assert(offsetof(VjExecCtx, yield_type_ptr) == 80,
               "yield_type_ptr offset");
_Static_assert(offsetof(VjExecCtx, key_pool_base) == 88,
               "key_pool_base offset");
_Static_assert(offsetof(VjExecCtx, stack) == 96, "stack offset");
_Static_assert(offsetof(VjExecCtx, trace_buf) == 2144, "trace_buf offset");

#endif /* VJ_ENCVM_TYPES_H */
