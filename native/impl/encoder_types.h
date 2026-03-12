/*
 * encoder_types.h — Velox JSON C Engine: Type Definitions & Constants
 *
 * Shared enums, structs, and constants used by all encoder modules.
 * Included first by encoder.h — no dependencies on other encoder_*.h files.
 */

#ifndef VJ_ENCODER_TYPES_H
#define VJ_ENCODER_TYPES_H

#include <stddef.h>
#include <stdint.h>
/* math.h not needed: we use __builtin_isnan / __builtin_isinf */
/* stdio.h not needed: float formatting falls back to Go in Phase 4 */

/* SIMD: use SSE intrinsics as the unified API. On ARM64, sse2neon.h
 * translates them to NEON. Same pattern as sjmarker/sj_marker.h. */
#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)
#ifdef __aarch64__
#include "sse2neon.h"
#else
#include <immintrin.h>
#endif
#endif

/* Platform-specific symbol naming:
 * macOS Mach-O: C symbols have _ prefix (_memcpy, _memset)
 * Linux ELF:    C symbols have no prefix (memcpy, memset) */
#if defined(__APPLE__)
  #define VJ_MEMCPY_SYM "_memcpy"
  #define VJ_MEMSET_SYM "_memset"
#else
  #define VJ_MEMCPY_SYM "memcpy"
  #define VJ_MEMSET_SYM "memset"
#endif

__attribute__((visibility("hidden"))) void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src,
               size_t n) __asm__(VJ_MEMCPY_SYM);

__attribute__((visibility("hidden"))) void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src, size_t n) {
  uint8_t *d = (uint8_t *)dst;
  const uint8_t *s = (const uint8_t *)src;
  while (n >= sizeof(uint64_t)) {
    /* Manual word load/store to avoid __builtin_memcpy which
     * the compiler may turn into a recursive _memcpy call. */
    uint64_t w = *(const uint64_t *)s;
    *(uint64_t *)d = w;
    d += sizeof(uint64_t);
    s += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  /* Cascading word tail: 0-7 remaining bytes.
   * Manual loads/stores only — __builtin_memcpy would recurse. */
  if (n >= 4) {
    uint32_t w = *(const uint32_t *)s;
    *(uint32_t *)d = w;
    d += 4;
    s += 4;
    n -= 4;
  }
  if (n >= 2) {
    uint16_t w = *(const uint16_t *)s;
    *(uint16_t *)d = w;
    d += 2;
    s += 2;
    n -= 2;
  }
  if (n) {
    *d = *s;
  }
  return dst;
}

__attribute__((visibility("hidden"))) void *
vj_memset_impl(void *dst, int c, size_t n) __asm__(VJ_MEMSET_SYM);

__attribute__((visibility("hidden"))) void *
vj_memset_impl(void *dst, int c, size_t n) {
  uint8_t *d = (uint8_t *)dst;
  uint8_t val = (uint8_t)c;
  while (n >= sizeof(uint64_t)) {
    uint64_t w = val;
    w |= w << 8;
    w |= w << 16;
    w |= w << 32;
    *(uint64_t *)d = w;
    d += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  while (n--) {
    *d++ = val;
  }
  return dst;
}
/* Use __builtin_memcpy throughout the code. The compiler will inline
 * small known-size copies and call our _memcpy symbol for the rest. */
#define vj_memcpy __builtin_memcpy

/* ---- Small copy helper ----
 *
 * Inline word-sized copies for 0-15 bytes.  Avoids function-call
 * overhead of memcpy for these common small sizes.  Uses
 * __builtin_memcpy with compile-time-constant sizes so the compiler
 * emits optimal load/store pairs (never a _memcpy call). */
static __attribute__((always_inline)) inline void
copy_small(uint8_t *dst, const uint8_t *src, int n) {
  if (n >= 8) {
    __builtin_memcpy(dst, src, 8);
    dst += 8;
    src += 8;
    n -= 8;
  }
  if (n >= 4) {
    __builtin_memcpy(dst, src, 4);
    dst += 4;
    src += 4;
    n -= 4;
  }
  if (n >= 2) {
    __builtin_memcpy(dst, src, 2);
    dst += 2;
    src += 2;
    n -= 2;
  }
  if (n) {
    *dst = *src;
  }
}

/* ================================================================
 *  Inline copy functions — SIMD-accelerated for common sizes
 *
 *  These replace vj_memcpy (which generates a `bl _memcpy` call)
 *  at hot call sites where the copy size is runtime-determined but
 *  typically small.  The overlapping load/store technique avoids
 *  branch misprediction: for sizes 8-16 we load first 8 + last 8
 *  bytes and store both, regardless of the exact size.
 * ================================================================ */

#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)

/* vj_copy_key — optimized for WRITE_KEY (the #1 hot call site).
 * Typical key lengths: 4-32 bytes (JSON `"field_name":`).
 * Uses overlapping SIMD loads to avoid branching on exact size.
 * Always inlined — no function call overhead. */
static __attribute__((always_inline)) inline void
vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
  if (__builtin_expect(n <= 8, 1)) {
    copy_small(dst, (const uint8_t *)src, (int)n);
    return;
  }
  if (__builtin_expect(n <= 16, 1)) {
    /* Overlapping 8-byte copies: first 8 + last 8 */
    __builtin_memcpy(dst, src, 8);
    __builtin_memcpy(dst + n - 8, src + n - 8, 8);
    return;
  }
  if (__builtin_expect(n <= 32, 1)) {
    /* Overlapping 16-byte SIMD copies */
    __m128i v0 = _mm_loadu_si128((const __m128i *)src);
    __m128i v1 = _mm_loadu_si128((const __m128i *)(src + n - 16));
    _mm_storeu_si128((__m128i *)dst, v0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), v1);
    return;
  }
  /* n > 32: rare for keys — loop with 16-byte SIMD + overlapping tail */
  uint16_t i = 0;
  for (; i + 16 <= n; i += 16) {
    __m128i v = _mm_loadu_si128((const __m128i *)(src + i));
    _mm_storeu_si128((__m128i *)(dst + i), v);
  }
  if (i < n) {
    __m128i v = _mm_loadu_si128((const __m128i *)(src + n - 16));
    _mm_storeu_si128((__m128i *)(dst + n - 16), v);
  }
}

/* vj_copy_var — general-purpose inline copy for variable-size data.
 * Used for OP_RAW_MESSAGE, OP_NUMBER, integer digit output, etc.
 * Handles up to 128 bytes inline; falls through to _memcpy for larger. */
static __attribute__((always_inline)) inline void
vj_copy_var(uint8_t *dst, const void *src, size_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (__builtin_expect(n <= 8, 1)) {
    copy_small(dst, s, (int)n);
    return;
  }
  if (__builtin_expect(n <= 16, 1)) {
    __builtin_memcpy(dst, s, 8);
    __builtin_memcpy(dst + n - 8, s + n - 8, 8);
    return;
  }
  if (__builtin_expect(n <= 32, 1)) {
    __m128i v0 = _mm_loadu_si128((const __m128i *)s);
    __m128i v1 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, v0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), v1);
    return;
  }
  if (__builtin_expect(n <= 64, 1)) {
    /* 2x overlapping 16-byte: first 32 + last 32 */
    __m128i a0 = _mm_loadu_si128((const __m128i *)s);
    __m128i a1 = _mm_loadu_si128((const __m128i *)(s + 16));
    __m128i b0 = _mm_loadu_si128((const __m128i *)(s + n - 32));
    __m128i b1 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, a0);
    _mm_storeu_si128((__m128i *)(dst + 16), a1);
    _mm_storeu_si128((__m128i *)(dst + n - 32), b0);
    _mm_storeu_si128((__m128i *)(dst + n - 16), b1);
    return;
  }
  if (__builtin_expect(n <= 128, 1)) {
    /* 4x overlapping 16-byte: first 64 + last 64 */
    __m128i a0 = _mm_loadu_si128((const __m128i *)s);
    __m128i a1 = _mm_loadu_si128((const __m128i *)(s + 16));
    __m128i a2 = _mm_loadu_si128((const __m128i *)(s + 32));
    __m128i a3 = _mm_loadu_si128((const __m128i *)(s + 48));
    __m128i b0 = _mm_loadu_si128((const __m128i *)(s + n - 64));
    __m128i b1 = _mm_loadu_si128((const __m128i *)(s + n - 48));
    __m128i b2 = _mm_loadu_si128((const __m128i *)(s + n - 32));
    __m128i b3 = _mm_loadu_si128((const __m128i *)(s + n - 16));
    _mm_storeu_si128((__m128i *)dst, a0);
    _mm_storeu_si128((__m128i *)(dst + 16), a1);
    _mm_storeu_si128((__m128i *)(dst + 32), a2);
    _mm_storeu_si128((__m128i *)(dst + 48), a3);
    _mm_storeu_si128((__m128i *)(dst + n - 64), b0);
    _mm_storeu_si128((__m128i *)(dst + n - 48), b1);
    _mm_storeu_si128((__m128i *)(dst + n - 32), b2);
    _mm_storeu_si128((__m128i *)(dst + n - 16), b3);
    return;
  }
  /* > 128 bytes: fall through to _memcpy (call overhead negligible) */
  vj_memcpy(dst, src, n);
}

#else /* No SIMD — scalar fallback */

static __attribute__((always_inline)) inline void
vj_copy_key(uint8_t *dst, const char *src, uint16_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 15) {
    copy_small(dst, s, (int)n);
    return;
  }
  /* Word loop + tail for larger keys (rare without SIMD) */
  while (n >= 8) {
    __builtin_memcpy(dst, s, 8);
    dst += 8;
    s += 8;
    n -= 8;
  }
  copy_small(dst, s, (int)n);
}

static __attribute__((always_inline)) inline void
vj_copy_var(uint8_t *dst, const void *src, size_t n) {
  const uint8_t *s = (const uint8_t *)src;
  if (n <= 15) {
    copy_small(dst, s, (int)n);
    return;
  }
  /* Fall through to _memcpy for larger copies */
  vj_memcpy(dst, src, n);
}

#endif /* ISA check */

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

/* omitempty flag: OR-ed into op_type high bits.
 * Lower byte = real opcode, stripped before dispatch-table lookup. */
#define OP_FLAG_OMITEMPTY 0x8000
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
    vj_memcpy(&v, ptr, 4);
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
    vj_memcpy(&v, ptr, 8);
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
 *  Constants
 * ================================================================ */

#define VJ_MAX_DEPTH 16

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
 *  Yield Codes
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

#endif /* VJ_ENCODER_TYPES_H */
