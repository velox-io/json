/*
 encoder.h — Velox JSON C Encoding Engine
 *
 * Three-layer architecture:
 *   Go Pre-compiler  ->  Assembly Bridge  ->  C Engine (this file)
 *
 * This header contains all data structures, constants, and the
 * threaded-code VM that encodes pre-compiled OpStep instruction
 * streams into JSON bytes.
 *
 * Design constraints:
 *   - OpStep must be cache-friendly (<=32 bytes on 64-bit).
 *   - EncodingContext is the sole state exchanged between Go and C.
 *   - All memory referenced by the engine is pinned by Go (runtime.Pinner)
 *     before entry; the engine never allocates.
 */

#ifndef VJ_ENCODER_H
#define VJ_ENCODER_H

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
  while (n--) {
    *d++ = *s++;
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

  /* --- Sentinel --- */
  OP_END = 0xFF, /* end of instruction stream */
};

/* Total number of dispatchable opcodes (excluding OP_END). */
#define OP_COUNT 22

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
_Static_assert(offsetof(VjEncodingCtx, stack) == 48, "stack offset");

/* ================================================================
 *  Section 7 — Helper: Fast integer to ASCII
 *
 *  write_uint64 / write_int64 convert to decimal ASCII in buf.
 *  They return the number of bytes written. buf must have >= 20 bytes.
 * ================================================================ */

static const char digit_pairs[201] = "00010203040506070809"
                                     "10111213141516171819"
                                     "20212223242526272829"
                                     "30313233343536373839"
                                     "40414243444546474849"
                                     "50515253545556575859"
                                     "60616263646566676869"
                                     "70717273747576777879"
                                     "80818283848586878889"
                                     "90919293949596979899";

static inline int write_uint64(uint8_t *buf, uint64_t v) {
  /* Fast path for small numbers (very common). */
  if (v < 10) {
    buf[0] = '0' + (uint8_t)v;
    return 1;
  }
  if (v < 100) {
    vj_memcpy(buf, &digit_pairs[v * 2], 2);
    return 2;
  }

  /* Write digits from right to left into a temp buffer. */
  uint8_t tmp[20];
  int pos = 20;

  while (v >= 100) {
    uint64_t q = v / 100;
    uint32_t r = (uint32_t)(v - q * 100);
    v = q;
    pos -= 2;
    vj_memcpy(&tmp[pos], &digit_pairs[r * 2], 2);
  }

  if (v >= 10) {
    pos -= 2;
    vj_memcpy(&tmp[pos], &digit_pairs[v * 2], 2);
  } else {
    pos--;
    tmp[pos] = '0' + (uint8_t)v;
  }

  int len = 20 - pos;
  vj_memcpy(buf, &tmp[pos], len);
  return len;
}

static inline int write_int64(uint8_t *buf, int64_t v) {
  if (v >= 0) {
    return write_uint64(buf, (uint64_t)v);
  }
  buf[0] = '-';
  /* INT64_MIN = -9223372036854775808, negate carefully. */
  uint64_t uv = (uint64_t)(-(v + 1)) + 1;
  return 1 + write_uint64(buf + 1, uv);
}

/* ================================================================
 *  Section 8 — Helper: String escape (JSON)
 *
 *  Mirrors Go appendEscapedString (vj_escape.go) behavior.
 *  Writes the string content (WITHOUT surrounding quotes) to buf.
 *  Returns number of bytes written.
 *
 *  The caller must ensure buf has enough space (worst case 6x + overhead).
 * ================================================================ */

static const char hex_digits[16] = "0123456789abcdef";

/* Escape table for bytes 0x00-0x1F plus " and \ */
static inline int escape_byte(uint8_t *buf, uint8_t c) {
  switch (c) {
  case '"':
    buf[0] = '\\';
    buf[1] = '"';
    return 2;
  case '\\':
    buf[0] = '\\';
    buf[1] = '\\';
    return 2;
  case '\b':
    buf[0] = '\\';
    buf[1] = 'b';
    return 2;
  case '\f':
    buf[0] = '\\';
    buf[1] = 'f';
    return 2;
  case '\n':
    buf[0] = '\\';
    buf[1] = 'n';
    return 2;
  case '\r':
    buf[0] = '\\';
    buf[1] = 'r';
    return 2;
  case '\t':
    buf[0] = '\\';
    buf[1] = 't';
    return 2;
  default:
    /* Control character: \u00XX */
    buf[0] = '\\';
    buf[1] = 'u';
    buf[2] = '0';
    buf[3] = '0';
    buf[4] = hex_digits[c >> 4];
    buf[5] = hex_digits[c & 0x0F];
    return 6;
  }
}

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int write_unicode_escape(uint8_t *buf, uint32_t cp) {
  buf[0] = '\\';
  buf[1] = 'u';
  buf[2] = hex_digits[(cp >> 12) & 0xF];
  buf[3] = hex_digits[(cp >> 8) & 0xF];
  buf[4] = hex_digits[(cp >> 4) & 0xF];
  buf[5] = hex_digits[cp & 0xF];
  return 6;
}

/* Decode a UTF-8 sequence starting at s[0]. Returns codepoint and
 * advances *consumed to the number of bytes consumed.
 * On invalid sequence returns 0xFFFD with consumed=1. */
static inline uint32_t decode_utf8(const uint8_t *s, int64_t remaining,
                                   int *consumed) {
  uint8_t b0 = s[0];
  if (b0 < 0x80) {
    *consumed = 1;
    return b0;
  }
  if ((b0 & 0xE0) == 0xC0 && remaining >= 2 && (s[1] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x1F) << 6) | (s[1] & 0x3F);
    if (cp >= 0x80) {
      *consumed = 2;
      return cp;
    }
  }
  if ((b0 & 0xF0) == 0xE0 && remaining >= 3 && (s[1] & 0xC0) == 0x80 &&
      (s[2] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) |
                  ((uint32_t)(s[1] & 0x3F) << 6) | (s[2] & 0x3F);
    if (cp >= 0x800 && !(cp >= 0xD800 && cp <= 0xDFFF)) {
      *consumed = 3;
      return cp;
    }
    /* Surrogate codepoint — treat as invalid below. */
    if (cp >= 0xD800 && cp <= 0xDFFF) {
      *consumed = 3;
      return 0xFFFD; /* flagged as surrogate */
    }
  }
  if ((b0 & 0xF8) == 0xF0 && remaining >= 4 && (s[1] & 0xC0) == 0x80 &&
      (s[2] & 0xC0) == 0x80 && (s[3] & 0xC0) == 0x80) {
    uint32_t cp = ((uint32_t)(b0 & 0x07) << 18) |
                  ((uint32_t)(s[1] & 0x3F) << 12) |
                  ((uint32_t)(s[2] & 0x3F) << 6) | (s[3] & 0x3F);
    if (cp >= 0x10000 && cp <= 0x10FFFF) {
      *consumed = 4;
      return cp;
    }
  }
  *consumed = 1;
  return 0xFFFD;
}

/*
 * SIMD helpers: scan 16 bytes, return a bitmask where set bits indicate
 * bytes that need escaping or are non-ASCII (>= 0x80).
 *
 * Uses SSE intrinsics — on ARM64, sse2neon.h translates them to NEON.
 * Same pattern as sjmarker/sj_marker.h.
 *
 * Two branchless variants are generated via VJ_ESCAPE_MASK_FUNC macro:
 *   vj_escape_mask_16      — base: c < 0x20, '"', '\\', c >= 0x80
 *   vj_escape_mask_16_html — adds '<', '>', '&'
 *
 * Callers select the appropriate function pointer once, outside the
 * hot loop, eliminating per-iteration branches.
 */

/*
 * SWAR (SIMD Within A Register) helper: scan 8 bytes packed in a uint64_t,
 * return an 8-bit mask where bit N (N = 0..7, LSB = first byte in memory on
 * little-endian) is set if byte N needs escaping.
 *
 * Detects: c < 0x20, c == '"'(0x22), c == '\\'(0x5C), c >= 0x80.
 * When html != 0, also detects: c == '<'(0x3C), c == '>'(0x3E), c == '&'(0x26).
 *
 * The `html` parameter must be a compile-time constant so the compiler
 * eliminates dead branches.
 *
 * Does not depend on SIMD intrinsics — usable on all platforms.
 */

#define SWAR_BROADCAST(b) ((uint64_t)(b) * 0x0101010101010101ULL)
#define SWAR_HI_BITS      SWAR_BROADCAST(0x80)
#define SWAR_LO_BITS      SWAR_BROADCAST(0x01)

/* has_zero_byte: for each byte lane that is 0x00, sets that lane's high bit.
 * Classic: ((v - 0x0101...) & ~v & 0x8080...) */
#define SWAR_HAS_ZERO(v) (((v) - SWAR_LO_BITS) & ~(v) & SWAR_HI_BITS)

/* has_less_than: for each byte lane < n (where 1 <= n <= 128),
 * sets that lane's high bit.  Works by subtracting n and checking
 * for underflow in the high bit while the original had it clear. */
#define SWAR_HAS_LESS(v, n) (((v) - SWAR_BROADCAST(n)) & ~(v) & SWAR_HI_BITS)

static __attribute__((always_inline)) inline int
vj_escape_mask_8(uint64_t word, const int html) {
  /* Bytes that need escaping will have their high bit set in `bad`. */
  uint64_t bad = 0;

  /* c < 0x20: control characters */
  bad |= SWAR_HAS_LESS(word, 0x20);

  /* c >= 0x80: non-ASCII (high bit already set in those bytes) */
  bad |= word & SWAR_HI_BITS;

  /* c == '"' (0x22) */
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x22));

  /* c == '\\' (0x5C) */
  bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x5C));

  if (html) {
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x3C)); /* '<' */
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x3E)); /* '>' */
    bad |= SWAR_HAS_ZERO(word ^ SWAR_BROADCAST(0x26)); /* '&' */
  }

  /* Extract one bit per byte (the high bit of each lane) into
   * an 8-bit integer.  Multiply-shift trick:
   *   (bad >> 7) isolates the marker bits at positions 0,8,16,...,56.
   *   Multiplying by 0x0102040810204080 accumulates them into the
   *   top byte, then >> 56 brings them to bits 0..7.
   *   The constant has bit k*7 set for k=1..8 so that the marker at
   *   position 8*i is shifted to bit 56+i. */
  return (int)(((bad >> 7) * 0x0102040810204080ULL) >> 56);
}

#undef SWAR_HAS_LESS
#undef SWAR_HAS_ZERO
#undef SWAR_HI_BITS
#undef SWAR_LO_BITS
#undef SWAR_BROADCAST

#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)

#define VJ_ESCAPE_MASK_FUNC(name, html)                                        \
  static inline int name(const uint8_t *src) {                                 \
    __m128i v = _mm_loadu_si128((const __m128i *)src);                         \
                                                                               \
    /* c < 0x20: max_epu8(v, 0x1F) != v → cmpeq gives 0 for ctrl chars. */     \
    __m128i ctrl_safe =                                                        \
        _mm_cmpeq_epi8(_mm_max_epu8(v, _mm_set1_epi8(0x1F)), v);               \
                                                                               \
    /* c == '"' or c == '\\' */                                                \
    __m128i eq_q = _mm_cmpeq_epi8(v, _mm_set1_epi8('"'));                      \
    __m128i eq_bs = _mm_cmpeq_epi8(v, _mm_set1_epi8('\\'));                    \
                                                                               \
    /* c >= 0x80: signed < 0 */                                                \
    __m128i hi = _mm_cmplt_epi8(v, _mm_setzero_si128());                       \
                                                                               \
    __m128i bad = _mm_or_si128(_mm_or_si128(eq_q, eq_bs), hi);                 \
                                                                               \
    if (html) {                                                                \
      __m128i eq_lt = _mm_cmpeq_epi8(v, _mm_set1_epi8('<'));                   \
      __m128i eq_gt = _mm_cmpeq_epi8(v, _mm_set1_epi8('>'));                   \
      __m128i eq_amp = _mm_cmpeq_epi8(v, _mm_set1_epi8('&'));                  \
      bad =                                                                    \
          _mm_or_si128(bad, _mm_or_si128(eq_lt, _mm_or_si128(eq_gt, eq_amp))); \
    }                                                                          \
                                                                               \
    /* safe = ctrl_safe & ~bad;  need_escape = ~safe */                        \
    __m128i safe = _mm_andnot_si128(bad, ctrl_safe);                           \
    return ~_mm_movemask_epi8(safe) & 0xFFFF;                                  \
  }

/* Generate two branchless specializations. The `html` parameter is a
 * compile-time constant (0 or 1), so the compiler eliminates the dead
 * branch entirely — no runtime check in either version. */
VJ_ESCAPE_MASK_FUNC(vj_escape_mask_16, 0)
VJ_ESCAPE_MASK_FUNC(vj_escape_mask_16_html, 1)

#undef VJ_ESCAPE_MASK_FUNC

#endif /* ISA_neon || ISA_sse42 || ISA_avx512 */

/*
 * escape_string_content — write escaped string content (no quotes) to buf.
 *
 * src: raw string bytes, src_len: length.
 * flags: VjEncFlags bitmask.
 * Returns number of bytes written to buf.
 *
 * Two specializations eliminate the check_html branch from the SIMD loop:
 *   escape_string_content_base — no HTML escaping
 *   escape_string_content_html — with HTML escaping (<, >, &)
 *
 * The top-level escape_string_content() dispatches once based on flags.
 */

/* Shared scalar tail: escape a single non-safe byte at src[i].
 * Returns number of bytes advanced in src (always >= 1).
 * Writes escaped output to *out_ptr, advances *out_ptr. */
static inline int vj_escape_one(uint8_t **out_ptr, const uint8_t *src,
                                int64_t i, int64_t src_len, uint32_t flags,
                                int html) {
  uint8_t *out = *out_ptr;
  uint8_t c = src[i];
  const int check_utf8 = (flags & VJ_ENC_ESCAPE_INVALID_UTF8) != 0;
  const int check_line_terms = (flags & VJ_ENC_ESCAPE_LINE_TERMS) != 0;

  if (c < 0x80) {
    if (c < 0x20 || c == '"' || c == '\\') {
      out += escape_byte(out, c);
      *out_ptr = out;
      return 1;
    }
    if (html && (c == '<' || c == '>' || c == '&')) {
      out += write_unicode_escape(out, c);
      *out_ptr = out;
      return 1;
    }
    *out++ = c;
    *out_ptr = out;
    return 1;
  }

  /* Non-ASCII: UTF-8 multibyte. */
  if (!check_utf8 && !check_line_terms) {
    *out++ = c;
    *out_ptr = out;
    return 1;
  }

  int consumed = 0;
  uint32_t cp = decode_utf8(&src[i], src_len - i, &consumed);

  if (cp == 0xFFFD && consumed <= 1 && check_utf8) {
    vj_memcpy(out, "\\ufffd", 6);
    out += 6;
    *out_ptr = out;
    return consumed ? consumed : 1;
  }

  if (check_line_terms && (cp == 0x2028 || cp == 0x2029)) {
    out += write_unicode_escape(out, cp);
    *out_ptr = out;
    return consumed;
  }

  vj_memcpy(out, &src[i], consumed);
  out += consumed;
  *out_ptr = out;
  return consumed;
}

#if defined(ISA_neon) || defined(ISA_sse42) || defined(ISA_avx512)

/*
 * SIMD-accelerated escape core.  The `html` parameter must be a compile-time
 * constant (0 or 1); after always_inline expansion the dead branch and the
 * unused mask function are eliminated entirely by the optimiser.
 */
static __attribute__((always_inline)) inline int
escape_string_content_impl(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags, const int html) {
  uint8_t *out = buf;
  int64_t i = 0;

  while (i < src_len) {
    /* SIMD: try to scan 16 bytes at a time. */
    if (i + 16 <= src_len) {
      int mask = html ? vj_escape_mask_16_html(&src[i])
                      : vj_escape_mask_16(&src[i]);
      if (mask == 0) {
        _mm_storeu_si128((__m128i *)out,
                         _mm_loadu_si128((const __m128i *)&src[i]));
        out += 16;
        i += 16;
        continue;
      }
      /* Copy safe prefix bytes before the first escape byte. */
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        vj_memcpy(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      /* Handle the escape byte with scalar. */
      i += vj_escape_one(&out, src, i, src_len, flags, html);
      continue;
    }
    /* Scalar tail: fewer than 16 bytes remaining. */
    /* SWAR: try to scan 8 bytes at a time. */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, html);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        vj_memcpy(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      i += vj_escape_one(&out, src, i, src_len, flags, html);
      continue;
    }
    /* Byte-by-byte tail: fewer than 8 bytes remaining. */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' &&
        !(html && (c == '<' || c == '>' || c == '&'))) {
      *out++ = c;
      i++;
    } else {
      i += vj_escape_one(&out, src, i, src_len, flags, html);
    }
  }
  return (int)(out - buf);
}

/* Two noinline entry points so the compiler generates separate code for each
 * specialisation (different SIMD mask + scalar checks). */
static __attribute__((noinline)) int
escape_string_content_base(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  return escape_string_content_impl(buf, src, src_len, flags, /*html=*/0);
}

static __attribute__((noinline)) int
escape_string_content_html(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  return escape_string_content_impl(buf, src, src_len, flags, /*html=*/1);
}

#else /* no SIMD */

static __attribute__((noinline)) int
escape_string_content_base(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  uint8_t *out = buf;
  int64_t i = 0;
  while (i < src_len) {
    /* SWAR: try to scan 8 bytes at a time. */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, 0);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        vj_memcpy(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      i += vj_escape_one(&out, src, i, src_len, flags, 0);
      continue;
    }
    /* Byte-by-byte tail. */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\') {
      *out++ = c;
      i++;
    } else {
      i += vj_escape_one(&out, src, i, src_len, flags, 0);
    }
  }
  return (int)(out - buf);
}

static __attribute__((noinline)) int
escape_string_content_html(uint8_t *buf, const uint8_t *src, int64_t src_len,
                           uint32_t flags) {
  uint8_t *out = buf;
  int64_t i = 0;
  while (i < src_len) {
    /* SWAR: try to scan 8 bytes at a time. */
    if (i + 8 <= src_len) {
      uint64_t word;
      vj_memcpy(&word, &src[i], 8);
      int mask = vj_escape_mask_8(word, 1);
      if (mask == 0) {
        vj_memcpy(out, &src[i], 8);
        out += 8;
        i += 8;
        continue;
      }
      int safe = __builtin_ctz(mask);
      if (safe > 0) {
        vj_memcpy(out, &src[i], safe);
        out += safe;
        i += safe;
      }
      i += vj_escape_one(&out, src, i, src_len, flags, 1);
      continue;
    }
    /* Byte-by-byte tail. */
    uint8_t c = src[i];
    if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' && c != '<' &&
        c != '>' && c != '&') {
      *out++ = c;
      i++;
    } else {
      i += vj_escape_one(&out, src, i, src_len, flags, 1);
    }
  }
  return (int)(out - buf);
}

#endif /* ISA */

/* Dispatch to the appropriate specialization. */
static inline int escape_string_content(uint8_t *buf, const uint8_t *src,
                                        int64_t src_len, uint32_t flags) {
  if (flags & VJ_ENC_ESCAPE_HTML)
    return escape_string_content_html(buf, src, src_len, flags);
  return escape_string_content_base(buf, src, src_len, flags);
}

/* ================================================================
 *  Section 9 — Float formatting (Ryu algorithm)
 *
 *  Uses the Ryu algorithm (Ulf Adams, 2018) for shortest-representation
 *  float-to-string conversion. Output matches Go's
 *  strconv.AppendFloat(buf, f, 'f', -1, bitSize) exactly:
 *    - Fixed-point notation only (never scientific notation)
 *    - Minimum digits for exact round-trip
 *    - No trailing zeros in fractional part
 *    - Integer values have no decimal point (1.0 -> "1")
 *
 *  NaN/Inf are detected before calling Ryu and return VJ_ERR_NAN_INF.
 * ================================================================ */

#include "ryu.h"

/* ================================================================
 *  Section 9b — Out-of-line pointer-primitive encoder
 *
 *  Encodes a single dereferenced primitive value (bool, int*, uint*,
 *  float*, string, raw_message, number) into the buffer.
 *
 *  Marked noinline to keep vj_encode_struct's code footprint small
 *  and avoid icache pressure on the hot dispatch loop.
 * ================================================================ */

typedef struct {
  uint8_t *buf;  /* advanced buffer pointer; NULL on error */
  int error;     /* 0 = ok, VJ_ERR_BUF_FULL, VJ_ERR_NAN_INF */
} VjPtrEncResult;

static __attribute__((noinline)) VjPtrEncResult
vj_encode_ptr_value(uint8_t *buf, const uint8_t *bend,
                    const void *ptr, uint16_t etype, uint32_t flags) {
  /* Caller already did CHECK(key_len+1+330) or similar for fixed-size
   * types.  For variable-length types (string, raw_message, number)
   * we do additional bounds checks below. */
  switch (etype) {
  case OP_BOOL: {
    uint8_t val = *(const uint8_t *)ptr;
    if (val) { vj_memcpy(buf, "true", 4); buf += 4; }
    else     { vj_memcpy(buf, "false", 5); buf += 5; }
    break;
  }
  case OP_INT:
  case OP_INT64:
    buf += write_int64(buf, *(const int64_t *)ptr);
    break;
  case OP_INT8:
    buf += write_int64(buf, (int64_t)*(const int8_t *)ptr);
    break;
  case OP_INT16:
    buf += write_int64(buf, (int64_t)*(const int16_t *)ptr);
    break;
  case OP_INT32:
    buf += write_int64(buf, (int64_t)*(const int32_t *)ptr);
    break;
  case OP_UINT:
  case OP_UINT64:
    buf += write_uint64(buf, *(const uint64_t *)ptr);
    break;
  case OP_UINT8:
    buf += write_uint64(buf, (uint64_t)*(const uint8_t *)ptr);
    break;
  case OP_UINT16:
    buf += write_uint64(buf, (uint64_t)*(const uint16_t *)ptr);
    break;
  case OP_UINT32:
    buf += write_uint64(buf, (uint64_t)*(const uint32_t *)ptr);
    break;
  case OP_FLOAT32: {
    float fval;
    vj_memcpy(&fval, ptr, 4);
    if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_NAN_INF};
    }
    buf += vj_write_float32(buf, fval);
    break;
  }
  case OP_FLOAT64: {
    double dval;
    vj_memcpy(&dval, ptr, 8);
    if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_NAN_INF};
    }
    buf += vj_write_float64(buf, dval);
    break;
  }
  case OP_STRING: {
    const GoString *s = (const GoString *)ptr;
    int64_t str_need = 2 + (s->len * 6);
    if (__builtin_expect(buf + str_need > bend, 0)) {
      return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
    }
    *buf++ = '"';
    if (s->len > 0) {
      buf += escape_string_content(buf, s->ptr, s->len, flags);
    }
    *buf++ = '"';
    break;
  }
  case OP_RAW_MESSAGE: {
    const GoSlice *raw = (const GoSlice *)ptr;
    if (raw->data == NULL || raw->len == 0) {
      vj_memcpy(buf, "null", 4);
      buf += 4;
    } else {
      if (__builtin_expect(buf + raw->len > bend, 0)) {
        return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
      }
      vj_memcpy(buf, raw->data, raw->len);
      buf += raw->len;
    }
    break;
  }
  case OP_NUMBER: {
    const GoString *s = (const GoString *)ptr;
    if (s->len == 0) {
      *buf++ = '0';
    } else {
      if (__builtin_expect(buf + s->len > bend, 0)) {
        return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
      }
      vj_memcpy(buf, s->ptr, s->len);
      buf += s->len;
    }
    break;
  }
  default:
    /* Should not happen — Go compiler only emits known types. */
    return (VjPtrEncResult){NULL, VJ_ERR_BUF_FULL};
  }
  return (VjPtrEncResult){buf, 0};
}

/* ================================================================
 *  Section 10 — Threaded-Code VM: vj_encode_struct
 *
 *  Uses computed goto (labels-as-values) with PC-relative dispatch.
 *
 *  The dispatch table stores int32_t offsets: (handler - base_label).
 *  Clang emits ARM64_RELOC_SUBTRACTOR pairs for these, which are
 *  position-independent and live in __TEXT,__const.
 *
 *  At runtime, the base label address is obtained via inline asm
 *  ADR with an "i"(&&label) constraint, producing a pure PC-relative
 *  load entirely within __TEXT — no __DATA literal pool needed.
 *
 *  NOTE: Clang's optimizer merges all DISPATCH() tails into a single
 *  indirect branch basic block, producing the same centralized loop
 *  as a switch statement. However, the computed goto approach has
 *  two advantages:
 *    1. Eliminates the bounds-check + LDRH + shift that switch's
 *       16-bit scaled-offset table requires.
 *    2. The dispatch table uses 32-bit offsets (int32_t) vs switch's
 *       16-bit halfword entries, giving more room for future growth.
 *  On Apple Silicon (M1+), the TAGE-like indirect branch predictor
 *  handles single-site dispatch well regardless.
 * ================================================================ */

/* Save VM state to context and return with an error code. */
#define SAVE_AND_RETURN(err)                                                   \
  do {                                                                         \
    ctx->buf_cur = buf;                                                        \
    ctx->cur_op = op;                                                          \
    ctx->cur_base = base;                                                      \
    ctx->error_code = (err);                                                   \
    return;                                                                    \
  } while (0)

void vj_encode_struct(VjEncodingCtx *ctx) {

  /* ---- Load context into registers / locals ---- */
  uint8_t *buf = ctx->buf_cur;
  uint8_t *bend = ctx->buf_end;
  const OpStep *op = ctx->cur_op;
  const uint8_t *base = ctx->cur_base;
  int32_t depth = ctx->depth;
  uint32_t flags = ctx->enc_flags;
  int first = 1; /* first field in current struct (no comma) */

/* ---- Computed goto dispatch table ----
 *
 * Stores (handler_label - base_label) as int32_t offsets.
 * The difference of two labels within the same function is a
 * link-time constant; both GCC and Clang emit position-independent
 * relocations for this (ARM64_RELOC_SUBTRACTOR on Mach-O,
 * R_AARCH64_PREL32 / R_X86_64_PC32 on ELF).  The resulting
 * table resides in a read-only section (__TEXT,__const or .rodata). */
#define DT_ENTRY(label)                                                        \
  (int32_t)((char *) && label - (char *) && vj_dispatch_base)

  static const int32_t dispatch_table[OP_COUNT] = {
      [OP_BOOL] = DT_ENTRY(vj_op_bool),
      [OP_INT] = DT_ENTRY(vj_op_int),
      [OP_INT8] = DT_ENTRY(vj_op_int8),
      [OP_INT16] = DT_ENTRY(vj_op_int16),
      [OP_INT32] = DT_ENTRY(vj_op_int32),
      [OP_INT64] = DT_ENTRY(vj_op_int64),
      [OP_UINT] = DT_ENTRY(vj_op_uint),
      [OP_UINT8] = DT_ENTRY(vj_op_uint8),
      [OP_UINT16] = DT_ENTRY(vj_op_uint16),
      [OP_UINT32] = DT_ENTRY(vj_op_uint32),
      [OP_UINT64] = DT_ENTRY(vj_op_uint64),
      [OP_FLOAT32] = DT_ENTRY(vj_op_float32),
      [OP_FLOAT64] = DT_ENTRY(vj_op_float64),
      [OP_STRING] = DT_ENTRY(vj_op_string),
      [OP_STRUCT] = DT_ENTRY(vj_op_struct),
      [OP_SLICE] = DT_ENTRY(vj_op_fallback),
      [OP_POINTER] = DT_ENTRY(vj_op_pointer),
      [OP_INTERFACE] = DT_ENTRY(vj_op_fallback),
      [OP_MAP] = DT_ENTRY(vj_op_fallback),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER] = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE] = DT_ENTRY(vj_op_fallback),
  };

#undef DT_ENTRY

/* ---- Macros ---- */

/* Check that N bytes are available in the output buffer. */
#define CHECK(n)                                                               \
  do {                                                                         \
    if (__builtin_expect(buf + (n) > bend, 0)) {                               \
      SAVE_AND_RETURN(VJ_ERR_BUF_FULL);                                        \
    }                                                                          \
  } while (0)

/* Write the pre-encoded key with comma prefix if needed. */
#define WRITE_KEY()                                                            \
  do {                                                                         \
    if (!first) {                                                              \
      *buf++ = ',';                                                            \
    }                                                                          \
    first = 0;                                                                 \
    vj_memcpy(buf, op->key_ptr, op->key_len);                                  \
    buf += op->key_len;                                                        \
  } while (0)

/* Computed goto dispatch.
 *
 * The dispatch table stores int32_t offsets relative to vj_dispatch_base.
 * At runtime we need the base label's address to compute the jump target.
 *
 * Problem: Clang materialises &&label into a __DATA literal pool as an
 * absolute address (.quad Ltmp0) that requires dyld rebase at load time.
 * Go's internal linker does NOT process __DATA rebase fixups, so the
 * pure-C expression  (char *)&&vj_dispatch_base + offset  yields a
 * garbage address and crashes.
 *
 * Solution: on aarch64 we use an inline-asm ADR instruction, which is
 * a pure PC-relative computation entirely within __TEXT — no __DATA
 * literal pool, no rebase needed.  On x86_64 we use LEA %rip-relative.
 *
 * OP_END (0xFF) is caught by the bounds check and routed to
 * vj_op_end via a normal goto (cold path).
 *
 * omitempty: when OP_FLAG_OMITEMPTY is set in op->op_type, the macro
 * strips the flag, checks vj_is_zero(), and skips the field (advance
 * op and re-dispatch via vj_omit_restart) if the value is zero. */
#if defined(__aarch64__)
/* ARM64: ADR loads a PC-relative address in a single instruction. */
#define DISPATCH()                                                             \
  do {                                                                         \
    uint16_t _raw = op->op_type;                                               \
    uint16_t _opc = _raw & OP_TYPE_MASK;                                       \
    if (__builtin_expect(_opc >= OP_COUNT, 0))                                 \
      goto vj_op_end;                                                          \
    if ((_raw & OP_FLAG_OMITEMPTY) &&                                          \
        vj_is_zero(base + op->field_off, _opc)) {                             \
      op++;                                                                    \
      goto vj_omit_restart;                                                    \
    }                                                                          \
    char *_base;                                                               \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_dispatch_base));   \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#elif defined(__x86_64__)
/* x86_64: LEA with RIP-relative addressing — pure __TEXT, no fixups. */
#define DISPATCH()                                                             \
  do {                                                                         \
    uint16_t _raw = op->op_type;                                               \
    uint16_t _opc = _raw & OP_TYPE_MASK;                                       \
    if (__builtin_expect(_opc >= OP_COUNT, 0))                                 \
      goto vj_op_end;                                                          \
    if ((_raw & OP_FLAG_OMITEMPTY) &&                                          \
        vj_is_zero(base + op->field_off, _opc)) {                             \
      op++;                                                                    \
      goto vj_omit_restart;                                                    \
    }                                                                          \
    char *_base;                                                               \
    __asm__("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_dispatch_base));     \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#else
#error "DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Advance to next op and dispatch. */
#define NEXT()                                                                 \
  do {                                                                         \
    op++;                                                                      \
    DISPATCH();                                                                \
  } while (0)

  /* ---- Write opening brace (or skip if resuming) ---- */
  if (flags & VJ_ENC_RESUME) {
    /* Hot resume: Go already wrote the opening '{' and some fields.
     * Restore the 'first' flag from the resume flags, then strip
     * the resume bits so nested struct dispatch is unaffected. */
    first = (flags & VJ_ENC_RESUME_FIRST) ? 1 : 0;
    flags &= ~(uint32_t)(VJ_ENC_RESUME | VJ_ENC_RESUME_FIRST);
  } else {
    CHECK(1);
    *buf++ = '{';
  }

  /* ---- Begin threaded dispatch ---- */
vj_omit_restart:
  DISPATCH();

  /* Base label for dispatch offset calculation.
   * Placed after an unreachable point so it doesn't interfere
   * with fall-through control flow. */
vj_dispatch_base:
  __builtin_unreachable();

  /* ==== Integer handlers ==== */

vj_op_bool: {
  CHECK(op->key_len + 1 + 5); /* comma + key + "false" */
  WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  if (val) {
    vj_memcpy(buf, "true", 4);
    buf += 4;
  } else {
    vj_memcpy(buf, "false", 5);
    buf += 5;
  }
  NEXT();
}

vj_op_int: {
  CHECK(op->key_len + 1 + 21);
  WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  NEXT();
}

vj_op_int8: {
  CHECK(op->key_len + 1 + 5);
  WRITE_KEY();
  int8_t val = *(const int8_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  NEXT();
}

vj_op_int16: {
  CHECK(op->key_len + 1 + 7);
  WRITE_KEY();
  int16_t val = *(const int16_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  NEXT();
}

vj_op_int32: {
  CHECK(op->key_len + 1 + 12);
  WRITE_KEY();
  int32_t val = *(const int32_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  NEXT();
}

vj_op_int64: {
  CHECK(op->key_len + 1 + 21);
  WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  NEXT();
}

vj_op_uint: {
  CHECK(op->key_len + 1 + 21);
  WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64(buf, val);
  NEXT();
}

vj_op_uint8: {
  CHECK(op->key_len + 1 + 4);
  WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  NEXT();
}

vj_op_uint16: {
  CHECK(op->key_len + 1 + 6);
  WRITE_KEY();
  uint16_t val = *(const uint16_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  NEXT();
}

vj_op_uint32: {
  CHECK(op->key_len + 1 + 11);
  WRITE_KEY();
  uint32_t val = *(const uint32_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  NEXT();
}

vj_op_uint64: {
  CHECK(op->key_len + 1 + 21);
  WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64(buf, val);
  NEXT();
}

  /* ==== Float handlers (Ryu) ==== */

vj_op_float32: {
  /* Read 4-byte float from struct, check NaN/Inf, format via Ryu. */
  float fval;
  vj_memcpy(&fval, base + op->field_off, 4);

  if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
    ctx->depth = depth;
    SAVE_AND_RETURN(VJ_ERR_NAN_INF);
  }

  /* Max float32 in 'f' format: ~50 chars. Conservative: 60. */
  CHECK(op->key_len + 1 + 60);
  WRITE_KEY();
  buf += vj_write_float32(buf, fval);
  NEXT();
}

vj_op_float64: {
  /* Read 8-byte double from struct, check NaN/Inf, format via Ryu. */
  double dval;
  vj_memcpy(&dval, base + op->field_off, 8);

  if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
    ctx->depth = depth;
    SAVE_AND_RETURN(VJ_ERR_NAN_INF);
  }

  /* Max float64 in 'f' format: 1e308 needs ~310 chars. Conservative: 330. */
  CHECK(op->key_len + 1 + 330);
  WRITE_KEY();
  buf += vj_write_float64(buf, dval);
  NEXT();
}

  /* ==== String handler ==== */

vj_op_string: {
  const GoString *s = (const GoString *)(base + op->field_off);

  /* Worst case: comma + key + quote + 6x escaped content + quote. */
  int64_t max_need = 1 + op->key_len + 2 + (s->len * 6);
  CHECK(max_need);

  WRITE_KEY();
  *buf++ = '"';

  if (s->len > 0) {
    buf += escape_string_content(buf, s->ptr, s->len, flags);
  }

  *buf++ = '"';
  NEXT();
}

  /* ==== Nested struct handler ==== */

vj_op_struct: {
  /* Need space for comma + key + '{' */
  CHECK(op->key_len + 1 + 1);
  WRITE_KEY();

  /* Check nesting depth. */
  if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
    SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
  }

  /* Push current state onto stack. */
  VjStackFrame *frame = &ctx->stack[depth];
  frame->ret_op = op + 1; /* resume at next op after return */
  frame->ret_base = base;
  frame->first = first;

  depth++;

  /* Enter child struct. */
  base = base + op->field_off; /* child struct base */
  op = (const OpStep *)op->sub_ops;
  first = 1;
  *buf++ = '{';

  DISPATCH(); /* dispatch child's first op */
}

  /* ==== End of instruction stream ====
   * Reached when op_type >= OP_COUNT (including OP_END = 0xFF). */

vj_op_end: {
  /* Any opcode outside OP_COUNT range that isn't OP_END is
   * an unknown type — treat as Go fallback. */
  if (__builtin_expect(op->op_type != OP_END, 0)) {
    goto vj_op_fallback;
  }

  CHECK(1);
  *buf++ = '}';

  if (depth > 0) {
    /* Pop stack: return to parent struct. */
    depth--;
    VjStackFrame *frame = &ctx->stack[depth];
    op = frame->ret_op;
    base = frame->ret_base;
    first = 0; /* parent already wrote at least this field */

    DISPATCH(); /* dispatch parent's next op */
  }

  /* Top-level struct done. */
  ctx->buf_cur = buf;
  ctx->depth = depth;
  ctx->error_code = VJ_OK;
  return;
}

  /* ==== RawMessage: direct byte copy ==== */

vj_op_raw_message: {
  const GoSlice *raw = (const GoSlice *)(base + op->field_off);

  if (raw->data == NULL || raw->len == 0) {
    CHECK(op->key_len + 1 + 4);
    WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
  } else {
    CHECK(op->key_len + 1 + raw->len);
    WRITE_KEY();
    vj_memcpy(buf, raw->data, raw->len);
    buf += raw->len;
  }
  NEXT();
}

  /* ==== Number: direct string copy ==== */

vj_op_number: {
  const GoString *s = (const GoString *)(base + op->field_off);
  if (s->len == 0) {
    CHECK(op->key_len + 1 + 1);
    WRITE_KEY();
    *buf++ = '0';
  } else {
    CHECK(op->key_len + 1 + s->len);
    WRITE_KEY();
    vj_memcpy(buf, s->ptr, s->len);
    buf += s->len;
  }
  NEXT();
}

  /* ==== Pointer handler ==== */

vj_op_pointer: {
  void *ptr = *(void **)(base + op->field_off);

  if (ptr == NULL) {
    /* nil pointer → JSON null */
    CHECK(op->key_len + 1 + 4);
    WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
    NEXT();
  }

  /* Non-nil: inspect sub_ops[0] to determine element type. */
  const OpStep *elem = (const OpStep *)op->sub_ops;
  uint16_t etype = elem->op_type & OP_TYPE_MASK;

  if (etype == OP_STRUCT) {
    /* *Struct: push stack frame, enter child struct.
     * base becomes the dereferenced pointer (NOT base + field_off). */
    CHECK(op->key_len + 1 + 1);
    WRITE_KEY();

    if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
      SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
    }

    VjStackFrame *frame = &ctx->stack[depth];
    frame->ret_op = op + 1;
    frame->ret_base = base;
    frame->first = first;
    depth++;

    base = (const uint8_t *)ptr;
    op = (const OpStep *)elem->sub_ops;
    first = 1;
    *buf++ = '{';
    DISPATCH();
  }

  /* *Primitive: delegate to out-of-line helper to keep
   * vj_encode_struct's code footprint small (icache friendly). */
  CHECK(op->key_len + 1 + 330);
  WRITE_KEY();
  {
    VjPtrEncResult r = vj_encode_ptr_value(buf, bend, ptr, etype, flags);
    if (__builtin_expect(r.buf == NULL, 0)) {
      SAVE_AND_RETURN(r.error);
    }
    buf = r.buf;
  }
  NEXT();
}

  /* ==== Go fallback for unsupported types ==== */

vj_op_fallback: {
  /* Save state so Go can inspect which op triggered the fallback.
   * Pack the 'first' flag into bit 31 of esc_op_idx so Go knows
   * whether a comma is needed when encoding the fallback field.
   * Bits [0:30] = op index, bit 31 = first flag (1 = no field yet). */
  ctx->depth = depth;
  ctx->esc_op_idx = (uint32_t)(op - ctx->cur_op)
                  | (first ? 0x80000000u : 0);
  SAVE_AND_RETURN(VJ_ERR_GO_FALLBACK);
}

/* ---- Cleanup macros ---- */
#undef CHECK
#undef WRITE_KEY
#undef DISPATCH
#undef NEXT
}

#undef SAVE_AND_RETURN

/* ================================================================
 *  Section 11 — Array Encoder: vj_encode_array
 *
 *  Batch-encodes a []NativeStruct slice entirely in C.
 *  Loops over elements calling vj_encode_struct per element,
 *  writing comma separators between them.  The caller (Go)
 *  writes '[' before and ']' after.
 *
 *  On VJ_ERR_BUF_FULL the current element index is saved in
 *  actx->arr_idx so Go can grow the buffer and resume.
 * ================================================================ */

typedef struct {
  VjEncodingCtx enc;        /* offset 0   — reused for each element */
  const uint8_t *arr_data;  /* offset 432 — array base pointer */
  int64_t arr_count;        /* offset 440 — total element count */
  int64_t arr_idx;          /* offset 448 — current element index (resume) */
  int64_t elem_size;        /* offset 456 — sizeof(element) */
  const OpStep *elem_ops;   /* offset 464 — struct ops for each element */
} VjArrayCtx;

void vj_encode_array(VjArrayCtx *actx) {
  VjEncodingCtx *ctx = &actx->enc;
  const uint8_t *data = actx->arr_data;
  int64_t count = actx->arr_count;
  int64_t elem_size = actx->elem_size;
  const OpStep *elem_ops = actx->elem_ops;

  uint8_t *buf = ctx->buf_cur;
  const uint8_t *bend = ctx->buf_end;
  uint32_t flags = ctx->enc_flags;

  for (int64_t i = actx->arr_idx; i < count; i++) {
    /* Comma separator (skip for first element). */
    if (i > 0) {
      if (__builtin_expect(buf + 1 > bend, 0)) {
        actx->arr_idx = i;
        ctx->buf_cur = buf;
        ctx->error_code = VJ_ERR_BUF_FULL;
        return;
      }
      *buf++ = ',';
    }

    /* Set up ctx for this element's struct encoding. */
    ctx->buf_cur = buf;
    ctx->cur_op = elem_ops;
    ctx->cur_base = data + i * elem_size;
    ctx->depth = 0;
    ctx->enc_flags = flags; /* clean flags — no RESUME */

    vj_encode_struct(ctx);

    if (__builtin_expect(ctx->error_code != VJ_OK, 0)) {
      actx->arr_idx = i; /* save progress for retry */
      return;             /* propagate error to Go */
    }

    buf = ctx->buf_cur; /* struct advanced buf */
  }

  /* All elements encoded successfully. */
  ctx->buf_cur = buf;
  ctx->error_code = VJ_OK;
  actx->arr_idx = count;
}

#endif /* VJ_ENCODER_H */
