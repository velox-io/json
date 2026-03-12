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

__attribute__((visibility("hidden"))) void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src,
               size_t n) __asm__("_memcpy");

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
vj_memset_impl(void *dst, int c, size_t n) __asm__("_memset");

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
 *  Section 1 — Operation Codes (OpType)
 *
 *  Values 0-13 are intentionally aligned with Go-side ElemTypeKind
 *  (typeinfo.go) for the primitive and string types so that the
 *  pre-compiler can use a direct cast for simple fields.
 *
 *  Go ElemTypeKind iota mapping:
 *    0=Bool 1=Int 2=Int8 3=Int16 4=Int32 5=Int64
 *    6=Uint 7=Uint8 8=Uint16 9=Uint32 10=Uint64
 *    11=Float32 12=Float64 13=String
 *    14=Struct 15=Slice 16=Pointer 17=Any 18=Map
 *    19=RawMessage 20=Number
 * ================================================================ */

enum OpType {
  /* --- Primitives (match ElemTypeKind 0-13) --- */
  OP_BOOL = 0,
  OP_INT = 1, /* Go int  — 8 bytes on 64-bit */
  OP_INT8 = 2,
  OP_INT16 = 3,
  OP_INT32 = 4,
  OP_INT64 = 5,
  OP_UINT = 6, /* Go uint — 8 bytes on 64-bit */
  OP_UINT8 = 7,
  OP_UINT16 = 8,
  OP_UINT32 = 9,
  OP_UINT64 = 10,
  OP_FLOAT32 = 11,
  OP_FLOAT64 = 12,
  OP_STRING = 13, /* Go string {ptr, len} */

  /* --- Composite / special (match ElemTypeKind 14-20) --- */
  OP_STRUCT = 14,      /* nested struct — sub_ops points to child OpStep[] */
  OP_SLICE = 15,       /* slice — fallback to Go */
  OP_POINTER = 16,     /* pointer deref + dispatch */
  OP_INTERFACE = 17,   /* interface{} — fallback to Go */
  OP_MAP = 18,         /* map — fallback to Go */
  OP_RAW_MESSAGE = 19, /* json.RawMessage — direct byte copy */
  OP_NUMBER = 20,      /* json.Number — direct string copy */

  /* --- Extended ops (beyond ElemTypeKind range) --- */
  OP_BYTE_SLICE = 21, /* []byte — base64 encode, fallback to Go */

  /* Go-only fallback: fields with custom marshalers, ,string tags,
   * or complex nested structs that cannot be natively encoded.
   * Always routes to vj_op_fallback — never reads field memory.
   * Distinct from OP_INTERFACE which reads the GoEface layout. */
  OP_FALLBACK = 22,

  /* --- Sentinel --- */
  OP_END = 0xFF, /* end of instruction stream */
};

/* Total number of dispatchable opcodes (excluding OP_END). */
#define OP_COUNT 23

/* Flag bit stored in OpStep.op_type to indicate omitempty semantics.
 * When set, the VM checks if the field is its zero value and skips
 * encoding (no key, no value, no comma) if so.  The lower byte holds
 * the real opcode; the flag is stripped before dispatch-table lookup. */
#define OP_FLAG_OMITEMPTY 0x8000
#define OP_TYPE_MASK      0x00FF

/* ================================================================
 *  Section 2 — Instruction Descriptor (OpStep)
 * ================================================================ */

typedef struct OpStep {
  uint16_t op_type;
  uint16_t key_len;
  uint32_t field_off;
  const char *key_ptr;
  void *sub_ops;
} OpStep;

_Static_assert(sizeof(OpStep) <= 32, "OpStep exceeds 32-byte cache budget");
_Static_assert(offsetof(OpStep, key_ptr) == 8,
               "OpStep.key_ptr must be at offset 8");
_Static_assert(offsetof(OpStep, sub_ops) == 16,
               "OpStep.sub_ops must be at offset 16");

/* ================================================================
 *  Section 3 — Go Runtime Type Layouts
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
 *  Section 3b — omitempty Zero-Value Check
 *
 *  Matches Go's makeIsZeroFn (typeinfo.go) exactly.
 *  Uses typed comparison so that float -0.0 compares equal to 0
 *  (matching Go's `*(*float64)(ptr) == 0` semantics).
 * ================================================================ */

static inline int vj_is_zero(const uint8_t *ptr, uint16_t op_type) {
  switch (op_type) {
  case OP_BOOL:
    return *(const uint8_t *)ptr == 0;
  case OP_INT8:
    return *(const int8_t *)ptr == 0;
  case OP_UINT8:
    return *(const uint8_t *)ptr == 0;
  case OP_INT16:
    return *(const int16_t *)ptr == 0;
  case OP_UINT16:
    return *(const uint16_t *)ptr == 0;
  case OP_INT32:
    return *(const int32_t *)ptr == 0;
  case OP_UINT32:
    return *(const uint32_t *)ptr == 0;
  case OP_FLOAT32: {
    float v;
    vj_memcpy(&v, ptr, 4);
    return v == 0;
  }
  case OP_INT:
  case OP_INT64:
    return *(const int64_t *)ptr == 0;
  case OP_UINT:
  case OP_UINT64:
    return *(const uint64_t *)ptr == 0;
  case OP_FLOAT64: {
    double v;
    vj_memcpy(&v, ptr, 8);
    return v == 0;
  }
  case OP_STRING:
    return ((const GoString *)ptr)->len == 0;
  case OP_NUMBER:
    /* json.Number is a Go string — zero means empty string. */
    return ((const GoString *)ptr)->len == 0;
  case OP_RAW_MESSAGE: {
    /* json.RawMessage is a Go []byte — zero means nil or len==0. */
    const GoSlice *s = (const GoSlice *)ptr;
    return s->data == NULL || s->len == 0;
  }
  case OP_POINTER:
    return *(const void *const *)ptr == NULL;
  case OP_INTERFACE:
    /* nil interface = zero value (eface.type_ptr == NULL). */
    return *(const void *const *)ptr == NULL;
  case OP_FALLBACK:
    /* Go-only fallback: the memory layout is unknown to C.
     * Never skip — the Go fallback handler checks omitempty. */
    return 0;
  default:
    return 0; /* unknown type — never skip */
  }
}

/* ================================================================
 *  Section 4 — Error Codes
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
 *  Section 5 — Encoding Flags
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
 *  Section 6 — Stack Frame & Encoding Context
 * ================================================================ */

#define VJ_MAX_DEPTH 16

/* ---- Interface type tag table entry ----
 *
 * Maps a Go *abi.Type pointer to a primitive opcode tag.
 * Used by vj_op_interface to inline-encode simple interface{} values
 * without returning to Go.  The table is built once by Go at init time
 * and sorted by type_ptr for binary search in C. */
typedef struct {
  const void *type_ptr; /* Go *abi.Type (address-comparable) */
  uint8_t tag;          /* OP_BOOL..OP_STRING, or 0 = unknown → fallback */
  uint8_t _pad[7];
} VjIfaceTypeEntry;

_Static_assert(sizeof(VjIfaceTypeEntry) == 16, "VjIfaceTypeEntry must be 16 bytes");

typedef struct {
  const OpStep *ret_op;
  const uint8_t *ret_base;
  int32_t first;
  int32_t _pad;
} VjStackFrame;

_Static_assert(sizeof(VjStackFrame) == 24, "VjStackFrame must be 24 bytes");

typedef struct {
  uint8_t *buf_cur;
  uint8_t *buf_end;
  const OpStep *cur_op;
  const uint8_t *cur_base;
  int32_t depth;
  int32_t error_code;
  uint32_t enc_flags;
  uint32_t esc_op_idx;
  const VjIfaceTypeEntry *iface_type_table; /* sorted by type_ptr */
  int32_t iface_type_count;
  int32_t _pad2;
  VjStackFrame stack[VJ_MAX_DEPTH];
} VjEncodingCtx;

_Static_assert(offsetof(VjEncodingCtx, buf_cur) == 0, "buf_cur offset");
_Static_assert(offsetof(VjEncodingCtx, buf_end) == 8, "buf_end offset");
_Static_assert(offsetof(VjEncodingCtx, cur_op) == 16, "cur_op offset");
_Static_assert(offsetof(VjEncodingCtx, cur_base) == 24, "cur_base offset");
_Static_assert(offsetof(VjEncodingCtx, depth) == 32, "depth offset");
_Static_assert(offsetof(VjEncodingCtx, error_code) == 36, "error_code offset");
_Static_assert(offsetof(VjEncodingCtx, enc_flags) == 40, "enc_flags offset");
_Static_assert(offsetof(VjEncodingCtx, esc_op_idx) == 44, "esc_op_idx offset");
_Static_assert(offsetof(VjEncodingCtx, iface_type_table) == 48, "iface_type_table offset");
_Static_assert(offsetof(VjEncodingCtx, iface_type_count) == 56, "iface_type_count offset");
_Static_assert(offsetof(VjEncodingCtx, stack) == 64, "stack offset");

#endif /* VJ_ENCODER_TYPES_H */
