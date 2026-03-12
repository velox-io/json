/*
 * native/impl/encoder.h — Velox JSON C Encoding Engine
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

#include <stdint.h>
#include <stddef.h>
/* math.h not needed: we use __builtin_isnan / __builtin_isinf */
/* stdio.h not needed: float formatting falls back to Go in Phase 4 */

/*
 * Provide our own memcpy to avoid libc dependency. Go's internal linker
 * cannot resolve external _memcpy. __builtin_memcpy sometimes still emits
 * _memcpy calls for variable-length copies, so we provide the actual symbol
 * via asm label to avoid preprocessor conflicts with builtin memcpy macros.
 *
 * IMPORTANT: This implementation must NOT use __builtin_memcpy internally,
 * because the compiler may lower that to a _memcpy call, creating recursion.
 */
__attribute__((visibility("hidden"), noinline))
void* vj_memcpy_impl(void* __restrict dst, const void* __restrict src, size_t n)
    __asm__("_memcpy");

__attribute__((visibility("hidden"), noinline))
void* vj_memcpy_impl(void* __restrict dst, const void* __restrict src, size_t n) {
    uint8_t*       d = (uint8_t*)dst;
    const uint8_t* s = (const uint8_t*)src;
    while (n >= sizeof(uint64_t)) {
        /* Manual word load/store to avoid __builtin_memcpy which
         * the compiler may turn into a recursive _memcpy call. */
        uint64_t w = *(const uint64_t*)s;
        *(uint64_t*)d = w;
        d += sizeof(uint64_t);
        s += sizeof(uint64_t);
        n -= sizeof(uint64_t);
    }
    while (n--) {
        *d++ = *s++;
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
    OP_BOOL         = 0,
    OP_INT          = 1,    /* Go int  — 8 bytes on 64-bit */
    OP_INT8         = 2,
    OP_INT16        = 3,
    OP_INT32        = 4,
    OP_INT64        = 5,
    OP_UINT         = 6,    /* Go uint — 8 bytes on 64-bit */
    OP_UINT8        = 7,
    OP_UINT16       = 8,
    OP_UINT32       = 9,
    OP_UINT64       = 10,
    OP_FLOAT32      = 11,
    OP_FLOAT64      = 12,
    OP_STRING       = 13,   /* Go string {ptr, len} */

    /* --- Composite / special (match ElemTypeKind 14-20) --- */
    OP_STRUCT       = 14,   /* nested struct — sub_ops points to child OpStep[] */
    OP_SLICE        = 15,   /* slice — fallback to Go */
    OP_POINTER      = 16,   /* pointer deref + dispatch */
    OP_INTERFACE    = 17,   /* interface{} — fallback to Go */
    OP_MAP          = 18,   /* map — fallback to Go */
    OP_RAW_MESSAGE  = 19,   /* json.RawMessage — direct byte copy */
    OP_NUMBER       = 20,   /* json.Number — direct string copy */

    /* --- Extended ops (beyond ElemTypeKind range) --- */
    OP_BYTE_SLICE   = 21,   /* []byte — base64 encode, fallback to Go */

    /* --- Sentinel --- */
    OP_END          = 0xFF, /* end of instruction stream */
};

/* Total number of dispatchable opcodes (excluding OP_END). */
#define OP_COUNT 22


/* ================================================================
 *  Section 2 — Instruction Descriptor (OpStep)
 * ================================================================ */

typedef struct OpStep {
    uint16_t    op_type;
    uint16_t    key_len;
    uint32_t    field_off;
    const char* key_ptr;
    void*       sub_ops;
} OpStep;

_Static_assert(sizeof(OpStep) <= 32,
    "OpStep exceeds 32-byte cache budget");
_Static_assert(offsetof(OpStep, key_ptr) == 8,
    "OpStep.key_ptr must be at offset 8");
_Static_assert(offsetof(OpStep, sub_ops) == 16,
    "OpStep.sub_ops must be at offset 16");


/* ================================================================
 *  Section 3 — Go Runtime Type Layouts
 * ================================================================ */

typedef struct {
    const uint8_t*  ptr;
    int64_t         len;
} GoString;

_Static_assert(sizeof(GoString) == 16,
    "GoString must be 16 bytes (matching Go string layout)");

typedef struct {
    const uint8_t*  data;
    int64_t         len;
    int64_t         cap;
} GoSlice;

_Static_assert(sizeof(GoSlice) == 24,
    "GoSlice must be 24 bytes (matching Go slice layout)");


/* ================================================================
 *  Section 4 — Error Codes
 * ================================================================ */

enum VjError {
    VJ_OK                   = 0,
    VJ_ERR_BUF_FULL         = 1,
    VJ_ERR_GO_FALLBACK      = 2,
    VJ_ERR_STACK_OVERFLOW   = 3,
    VJ_ERR_CYCLE            = 4,
    VJ_ERR_NAN_INF          = 5,
};


/* ================================================================
 *  Section 5 — Encoding Flags
 * ================================================================ */

enum VjEncFlags {
    VJ_ENC_ESCAPE_HTML          = 1 << 0,
    VJ_ENC_ESCAPE_LINE_TERMS    = 1 << 1,
    VJ_ENC_ESCAPE_INVALID_UTF8  = 1 << 2,
};

#define VJ_ENC_DEFAULT  (VJ_ENC_ESCAPE_INVALID_UTF8 | VJ_ENC_ESCAPE_LINE_TERMS)
#define VJ_ENC_STD_COMPAT (VJ_ENC_DEFAULT | VJ_ENC_ESCAPE_HTML)


/* ================================================================
 *  Section 6 — Stack Frame & Encoding Context
 * ================================================================ */

#define VJ_MAX_DEPTH 64

typedef struct {
    const OpStep*   ret_op;
    const uint8_t*  ret_base;
    int32_t         first;
    int32_t         _pad;
} VjStackFrame;

_Static_assert(sizeof(VjStackFrame) == 24,
    "VjStackFrame must be 24 bytes");

typedef struct {
    uint8_t*        buf_cur;
    uint8_t*        buf_end;
    const OpStep*   cur_op;
    const uint8_t*  cur_base;
    int32_t         depth;
    int32_t         error_code;
    uint32_t        enc_flags;
    uint32_t        esc_op_idx;
    VjStackFrame    stack[VJ_MAX_DEPTH];
} VjEncodingCtx;

_Static_assert(offsetof(VjEncodingCtx, buf_cur)    == 0,   "buf_cur offset");
_Static_assert(offsetof(VjEncodingCtx, buf_end)    == 8,   "buf_end offset");
_Static_assert(offsetof(VjEncodingCtx, cur_op)     == 16,  "cur_op offset");
_Static_assert(offsetof(VjEncodingCtx, cur_base)   == 24,  "cur_base offset");
_Static_assert(offsetof(VjEncodingCtx, depth)      == 32,  "depth offset");
_Static_assert(offsetof(VjEncodingCtx, error_code) == 36,  "error_code offset");
_Static_assert(offsetof(VjEncodingCtx, enc_flags)  == 40,  "enc_flags offset");
_Static_assert(offsetof(VjEncodingCtx, esc_op_idx) == 44,  "esc_op_idx offset");
_Static_assert(offsetof(VjEncodingCtx, stack)      == 48,  "stack offset");


/* ================================================================
 *  Section 7 — Helper: Fast integer to ASCII
 *
 *  write_uint64 / write_int64 convert to decimal ASCII in buf.
 *  They return the number of bytes written. buf must have >= 20 bytes.
 * ================================================================ */

static const char digit_pairs[201] =
    "00010203040506070809"
    "10111213141516171819"
    "20212223242526272829"
    "30313233343536373839"
    "40414243444546474849"
    "50515253545556575859"
    "60616263646566676869"
    "70717273747576777879"
    "80818283848586878889"
    "90919293949596979899"
    ;

static inline int write_uint64(uint8_t* buf, uint64_t v) {
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

static inline int write_int64(uint8_t* buf, int64_t v) {
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
static inline int escape_byte(uint8_t* buf, uint8_t c) {
    switch (c) {
    case '"':  buf[0] = '\\'; buf[1] = '"';  return 2;
    case '\\': buf[0] = '\\'; buf[1] = '\\'; return 2;
    case '\b': buf[0] = '\\'; buf[1] = 'b';  return 2;
    case '\f': buf[0] = '\\'; buf[1] = 'f';  return 2;
    case '\n': buf[0] = '\\'; buf[1] = 'n';  return 2;
    case '\r': buf[0] = '\\'; buf[1] = 'r';  return 2;
    case '\t': buf[0] = '\\'; buf[1] = 't';  return 2;
    default:
        /* Control character: \u00XX */
        buf[0] = '\\'; buf[1] = 'u'; buf[2] = '0'; buf[3] = '0';
        buf[4] = hex_digits[c >> 4];
        buf[5] = hex_digits[c & 0x0F];
        return 6;
    }
}

/* Write \uXXXX for a BMP codepoint. Returns 6. */
static inline int write_unicode_escape(uint8_t* buf, uint32_t cp) {
    buf[0] = '\\'; buf[1] = 'u';
    buf[2] = hex_digits[(cp >> 12) & 0xF];
    buf[3] = hex_digits[(cp >> 8)  & 0xF];
    buf[4] = hex_digits[(cp >> 4)  & 0xF];
    buf[5] = hex_digits[cp & 0xF];
    return 6;
}

/* Decode a UTF-8 sequence starting at s[0]. Returns codepoint and
 * advances *consumed to the number of bytes consumed.
 * On invalid sequence returns 0xFFFD with consumed=1. */
static inline uint32_t decode_utf8(const uint8_t* s, int64_t remaining, int* consumed) {
    uint8_t b0 = s[0];
    if (b0 < 0x80) {
        *consumed = 1;
        return b0;
    }
    if ((b0 & 0xE0) == 0xC0 && remaining >= 2 && (s[1] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x1F) << 6) | (s[1] & 0x3F);
        if (cp >= 0x80) { *consumed = 2; return cp; }
    }
    if ((b0 & 0xF0) == 0xE0 && remaining >= 3 &&
        (s[1] & 0xC0) == 0x80 && (s[2] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x0F) << 12) |
                      ((uint32_t)(s[1] & 0x3F) << 6) | (s[2] & 0x3F);
        if (cp >= 0x800 && !(cp >= 0xD800 && cp <= 0xDFFF)) {
            *consumed = 3; return cp;
        }
        /* Surrogate codepoint — treat as invalid below. */
        if (cp >= 0xD800 && cp <= 0xDFFF) {
            *consumed = 3; return 0xFFFD; /* flagged as surrogate */
        }
    }
    if ((b0 & 0xF8) == 0xF0 && remaining >= 4 &&
        (s[1] & 0xC0) == 0x80 && (s[2] & 0xC0) == 0x80 && (s[3] & 0xC0) == 0x80) {
        uint32_t cp = ((uint32_t)(b0 & 0x07) << 18) |
                      ((uint32_t)(s[1] & 0x3F) << 12) |
                      ((uint32_t)(s[2] & 0x3F) << 6) | (s[3] & 0x3F);
        if (cp >= 0x10000 && cp <= 0x10FFFF) { *consumed = 4; return cp; }
    }
    *consumed = 1;
    return 0xFFFD;
}

/*
 * escape_string_content — write escaped string content (no quotes) to buf.
 *
 * src: raw string bytes, src_len: length.
 * flags: VjEncFlags bitmask.
 * Returns number of bytes written to buf.
 */
static int escape_string_content(uint8_t* buf, const uint8_t* src, int64_t src_len,
                                 uint32_t flags)
{
    uint8_t* out = buf;
    const int check_html       = (flags & VJ_ENC_ESCAPE_HTML) != 0;
    const int check_utf8       = (flags & VJ_ENC_ESCAPE_INVALID_UTF8) != 0;
    const int check_line_terms = (flags & VJ_ENC_ESCAPE_LINE_TERMS) != 0;

    int64_t i = 0;
    while (i < src_len) {
        uint8_t c = src[i];

        /* Fast path: safe ASCII byte — copy directly. */
        if (c >= 0x20 && c < 0x80 && c != '"' && c != '\\' &&
            !(check_html && (c == '<' || c == '>' || c == '&'))) {
            /* Bulk copy safe ASCII run. */
            int64_t run_start = i;
            i++;
            while (i < src_len) {
                c = src[i];
                if (c < 0x20 || c == '"' || c == '\\' || c >= 0x80) break;
                if (check_html && (c == '<' || c == '>' || c == '&')) break;
                i++;
            }
            int64_t run_len = i - run_start;
            vj_memcpy(out, &src[run_start], run_len);
            out += run_len;
            continue;
        }

        /* ASCII that needs escaping. */
        if (c < 0x80) {
            if (c < 0x20 || c == '"' || c == '\\') {
                out += escape_byte(out, c);
                i++;
                continue;
            }
            /* HTML special: <, >, & */
            if (check_html && (c == '<' || c == '>' || c == '&')) {
                out += write_unicode_escape(out, c);
                i++;
                continue;
            }
            *out++ = c;
            i++;
            continue;
        }

        /* Non-ASCII: UTF-8 multibyte. */
        if (!check_utf8 && !check_line_terms) {
            /* No validation needed — just copy bytes. */
            *out++ = c;
            i++;
            continue;
        }

        int consumed = 0;
        uint32_t cp = decode_utf8(&src[i], src_len - i, &consumed);

        if (cp == 0xFFFD && consumed <= 1 && check_utf8) {
            /* Invalid UTF-8 or surrogate → \ufffd */
            vj_memcpy(out, "\\ufffd", 6);
            out += 6;
            i += consumed;
            if (consumed == 0) i++; /* safety: avoid infinite loop */
            continue;
        }

        if (check_line_terms && (cp == 0x2028 || cp == 0x2029)) {
            out += write_unicode_escape(out, cp);
            i += consumed;
            continue;
        }

        /* Valid multi-byte: copy raw bytes. */
        vj_memcpy(out, &src[i], consumed);
        out += consumed;
        i += consumed;
    }

    return (int)(out - buf);
}


/* ================================================================
 *  Section 9 — Float formatting
 *
 *  In Phase 4, float types (OP_FLOAT32, OP_FLOAT64) trigger a Go
 *  fallback (VJ_ERR_GO_FALLBACK) so that Go's strconv.AppendFloat
 *  handles the formatting. This avoids any libc dependency (snprintf)
 *  and ensures the internal Go linker works without -linkmode=external.
 *
 *  TODO(Phase 8): Implement ryu or Grisu2 for native float formatting.
 * ================================================================ */


/* ================================================================
 *  Section 10 — Threaded-Code VM: vj_encode_struct
 *
 *  Uses a switch-based dispatch loop. We cannot use computed goto
 *  (label-as-value) because Go's internal linker does not correctly
 *  handle ARM64_RELOC_UNSIGNED relocations with non-zero addends
 *  in Mach-O .syso files — it drops the offset, causing all dispatch
 *  entries to point to the function prologue (infinite recursion).
 *
 *  A switch statement compiles to a jump table with PC-relative offsets
 *  stored as 32-bit integers (ARM64_RELOC_SUBTRACTOR), which the Go
 *  linker handles correctly. At -O3, Clang generates an indirect branch
 *  through the jump table that performs comparably to computed goto.
 * ================================================================ */

/* Save VM state to context and return with an error code. */
#define SAVE_AND_RETURN(err) do {       \
    ctx->buf_cur    = buf;              \
    ctx->cur_op     = op;               \
    ctx->cur_base   = base;             \
    ctx->error_code = (err);            \
    return;                             \
} while(0)

void vj_encode_struct(VjEncodingCtx* ctx) {

    /* ---- Load context into registers / locals ---- */
    uint8_t*       buf   = ctx->buf_cur;
    uint8_t*       bend  = ctx->buf_end;
    const OpStep*  op    = ctx->cur_op;
    const uint8_t* base  = ctx->cur_base;
    int32_t        depth = ctx->depth;
    uint32_t       flags = ctx->enc_flags;
    int            first = 1;  /* first field in current struct (no comma) */

    /* ---- Macros ---- */

    /* Check that N bytes are available in the output buffer. */
    #define CHECK(n) do {                       \
        if (__builtin_expect(buf + (n) > bend, 0)) { \
            SAVE_AND_RETURN(VJ_ERR_BUF_FULL);  \
        }                                       \
    } while(0)

    /* Write the pre-encoded key with comma prefix if needed. */
    #define WRITE_KEY() do {                    \
        if (!first) { *buf++ = ','; }           \
        first = 0;                              \
        vj_memcpy(buf, op->key_ptr, op->key_len);  \
        buf += op->key_len;                     \
    } while(0)

    /* Advance to next op and re-enter dispatch loop.
     * NOTE: We must use goto, not continue. The do{...}while(0) idiom
     * means `continue` would jump to the do-while condition (while(0)),
     * completing the do-while and falling through the switch case.
     * goto reliably re-enters the for(;;) dispatch loop. */
    #define NEXT() do { op++; goto vj_dispatch_next; } while(0)

    /* ---- Write opening brace ---- */
    CHECK(1);
    *buf++ = '{';

    /* ---- Main dispatch loop ---- */
    for (;;) {
vj_dispatch_next: ;
    switch (op->op_type) {

    /* ==== Integer handlers ==== */

    case OP_BOOL: {
        CHECK(op->key_len + 1 + 5); /* comma + key + "false" */
        WRITE_KEY();
        uint8_t val = *(const uint8_t*)(base + op->field_off);
        if (val) {
            vj_memcpy(buf, "true", 4); buf += 4;
        } else {
            vj_memcpy(buf, "false", 5); buf += 5;
        }
        NEXT();
    }

    case OP_INT: {
        CHECK(op->key_len + 1 + 21);
        WRITE_KEY();
        int64_t val = *(const int64_t*)(base + op->field_off);
        buf += write_int64(buf, val);
        NEXT();
    }

    case OP_INT8: {
        CHECK(op->key_len + 1 + 5);
        WRITE_KEY();
        int8_t val = *(const int8_t*)(base + op->field_off);
        buf += write_int64(buf, (int64_t)val);
        NEXT();
    }

    case OP_INT16: {
        CHECK(op->key_len + 1 + 7);
        WRITE_KEY();
        int16_t val = *(const int16_t*)(base + op->field_off);
        buf += write_int64(buf, (int64_t)val);
        NEXT();
    }

    case OP_INT32: {
        CHECK(op->key_len + 1 + 12);
        WRITE_KEY();
        int32_t val = *(const int32_t*)(base + op->field_off);
        buf += write_int64(buf, (int64_t)val);
        NEXT();
    }

    case OP_INT64: {
        CHECK(op->key_len + 1 + 21);
        WRITE_KEY();
        int64_t val = *(const int64_t*)(base + op->field_off);
        buf += write_int64(buf, val);
        NEXT();
    }

    case OP_UINT: {
        CHECK(op->key_len + 1 + 21);
        WRITE_KEY();
        uint64_t val = *(const uint64_t*)(base + op->field_off);
        buf += write_uint64(buf, val);
        NEXT();
    }

    case OP_UINT8: {
        CHECK(op->key_len + 1 + 4);
        WRITE_KEY();
        uint8_t val = *(const uint8_t*)(base + op->field_off);
        buf += write_uint64(buf, (uint64_t)val);
        NEXT();
    }

    case OP_UINT16: {
        CHECK(op->key_len + 1 + 6);
        WRITE_KEY();
        uint16_t val = *(const uint16_t*)(base + op->field_off);
        buf += write_uint64(buf, (uint64_t)val);
        NEXT();
    }

    case OP_UINT32: {
        CHECK(op->key_len + 1 + 11);
        WRITE_KEY();
        uint32_t val = *(const uint32_t*)(base + op->field_off);
        buf += write_uint64(buf, (uint64_t)val);
        NEXT();
    }

    case OP_UINT64: {
        CHECK(op->key_len + 1 + 21);
        WRITE_KEY();
        uint64_t val = *(const uint64_t*)(base + op->field_off);
        buf += write_uint64(buf, val);
        NEXT();
    }

    /* ==== Float handlers (Phase 4: fallback to Go) ==== */
    /* case OP_FLOAT32 / OP_FLOAT64: handled by default → GO_FALLBACK */

    /* ==== String handler ==== */

    case OP_STRING: {
        const GoString* s = (const GoString*)(base + op->field_off);

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

    case OP_STRUCT: {
        /* Need space for comma + key + '{' */
        CHECK(op->key_len + 1 + 1);
        WRITE_KEY();

        /* Check nesting depth. */
        if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
            SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
        }

        /* Push current state onto stack. */
        VjStackFrame* frame = &ctx->stack[depth];
        frame->ret_op   = op + 1;     /* resume at next op after return */
        frame->ret_base = base;
        frame->first    = first;

        depth++;

        /* Enter child struct. */
        base = base + op->field_off;  /* child struct base */
        op = (const OpStep*)op->sub_ops;
        first = 1;
        *buf++ = '{';

        continue;  /* dispatch child's first op */
    }

    /* ==== End of instruction stream ==== */

    case OP_END: {
        CHECK(1);
        *buf++ = '}';

        if (depth > 0) {
            /* Pop stack: return to parent struct. */
            depth--;
            VjStackFrame* frame = &ctx->stack[depth];
            op    = frame->ret_op;
            base  = frame->ret_base;
            first = 0;  /* parent already wrote at least this field */

            continue;  /* dispatch parent's next op */
        }

        /* Top-level struct done. */

        /* Top-level struct done. */
        ctx->buf_cur    = buf;
        ctx->depth      = depth;
        ctx->error_code = VJ_OK;
        return;
    }

    /* ==== RawMessage: direct byte copy ==== */

    case OP_RAW_MESSAGE: {
        const GoSlice* raw = (const GoSlice*)(base + op->field_off);

        if (raw->data == NULL || raw->len == 0) {
            CHECK(op->key_len + 1 + 4);
            WRITE_KEY();
            vj_memcpy(buf, "null", 4); buf += 4;
        } else {
            CHECK(op->key_len + 1 + raw->len);
            WRITE_KEY();
            vj_memcpy(buf, raw->data, raw->len);
            buf += raw->len;
        }
        NEXT();
    }

    /* ==== Number: direct string copy ==== */

    case OP_NUMBER: {
        const GoString* s = (const GoString*)(base + op->field_off);
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

    /* ==== Go fallback for unsupported types ==== */

    default: {
        /* Save state so Go can inspect which op triggered the fallback. */
        ctx->depth     = depth;
        ctx->esc_op_idx = (uint32_t)(op - ctx->cur_op);
        SAVE_AND_RETURN(VJ_ERR_GO_FALLBACK);
    }

    } /* switch */
    } /* for */

    /* ---- Cleanup macros ---- */
    #undef CHECK
    #undef WRITE_KEY
    #undef NEXT
}

#undef SAVE_AND_RETURN


#endif /* VJ_ENCODER_H */
