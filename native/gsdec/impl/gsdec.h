/*
 * gsdec.h — Goto Streaming Decoder types and macros.
 *
 * Shared definitions between gsdec.c and Go (via matching Go structs).
 * All structs are repr(C) and match the rsdec layout exactly.
 */

#ifndef GSDEC_H
#define GSDEC_H

#include <stdint.h>
#include <stddef.h>

/* ============================================================
 *  DecExecCtx — 128 bytes, matches Go/Rust layout exactly
 * ============================================================ */

typedef struct {
    /* Cache line 0: hot path */
    const uint8_t *src_ptr;     /* [0]  input buffer */
    uint32_t       src_len;     /* [8]  buffer length */
    uint32_t       idx;         /* [12] current parse position */
    uint8_t       *cur_base;    /* [16] current struct/array base (GC-visible) */
    const uint8_t *ti_ptr;      /* [24] type descriptor */
    int32_t        exit_code;   /* [32] exit/yield code */
    uint32_t       flags;       /* [36] option flags */
    uint8_t       *scratch_ptr; /* [40] scratch buffer */
    uint32_t       scratch_cap; /* [48] scratch capacity */
    uint32_t       scratch_len; /* [52] scratch used */
    uint32_t       _pad_56;     /* [56] */
    uint32_t       err_detail;  /* [60] error offset */

    /* Cache line 1: yield params + resume */
    uint64_t       yield_param0; /* [64] */
    uint64_t       yield_param1; /* [72] */
    uint64_t       yield_param2; /* [80] */
    uintptr_t      resume_ptr;   /* [88] → ParseStack (Go allocated) */
    uint16_t       resume_cap;   /* [96] max depth */
    uint16_t       resume_depth; /* [98] current depth */
    uint32_t       _pad1;        /* [100] */
    uint8_t       *insn_ptr;     /* [104] instruction buffer */
    uint32_t       insn_len;     /* [112] insn bytes written */
    uint32_t       insn_cap;     /* [116] insn capacity */
    uint8_t        _reserved[8]; /* [120-127] */
} DecExecCtx;

_Static_assert(sizeof(DecExecCtx) == 128, "DecExecCtx must be 128 bytes");

/* ============================================================
 *  Exit / yield codes (must match Go rsdec.go constants)
 * ============================================================ */

#define EXIT_OK              0
#define EXIT_UNEXPECTED_EOF  1
#define EXIT_SYNTAX_ERROR    2
#define EXIT_TYPE_ERROR      3

#define YIELD_STRING         10
#define YIELD_ALLOC_SLICE    11
#define YIELD_GROW_SLICE     12
#define YIELD_ARENA_FULL     13
#define YIELD_MAP_INIT       14
#define YIELD_MAP_ASSIGN     15
#define YIELD_INSN_FLUSH     16
#define YIELD_ALLOC_POINTER  17
#define YIELD_FLOAT_PARSE    18

/* ============================================================
 *  Type kind constants (must match Go ElemTypeKind / Rust kind::)
 * ============================================================ */

#define KIND_BOOL    1
#define KIND_INT     2
#define KIND_INT8    3
#define KIND_INT16   4
#define KIND_INT32   5
#define KIND_INT64   6
#define KIND_UINT    7
#define KIND_UINT8   8
#define KIND_UINT16  9
#define KIND_UINT32  10
#define KIND_UINT64  11
#define KIND_FLOAT32 12
#define KIND_FLOAT64 13
#define KIND_STRING  14
#define KIND_STRUCT  15
#define KIND_SLICE   16
#define KIND_POINTER 17
#define KIND_ANY     18
#define KIND_MAP     19
#define KIND_ARRAY   22

static inline int kind_is_signed_int(uint8_t k) { return k >= KIND_INT && k <= KIND_INT64; }
static inline int kind_is_unsigned_int(uint8_t k) { return k >= KIND_UINT && k <= KIND_UINT64; }
static inline int kind_is_int(uint8_t k) { return kind_is_signed_int(k) || kind_is_unsigned_int(k); }

/* ============================================================
 *  Type descriptors (match Rust repr(C) structs exactly)
 * ============================================================ */

typedef struct {
    uint8_t        kind;
    uint8_t        flags;
    uint16_t       _pad;
    uint32_t       size;
    uint16_t       num_fields;
    uint16_t       _pad2;
    uint32_t       _pad3;
    /* DecFieldDesc array follows immediately (inline) */
} DecStructDesc;

_Static_assert(sizeof(DecStructDesc) == 16, "DecStructDesc must be 16 bytes");

typedef struct {
    uintptr_t      name_ptr;
    uint16_t       name_len;
    uint16_t       offset;
    uint8_t        val_kind;
    uint8_t        val_flags;
    uint16_t       _pad;
    uintptr_t      val_desc;
} DecFieldDesc;

_Static_assert(sizeof(DecFieldDesc) == 24, "DecFieldDesc must be 24 bytes");

typedef struct {
    uint8_t        kind;
    uint8_t        elem_kind;
    uint8_t        elem_has_ptr;
    uint8_t        _pad;
    uint32_t       elem_size;
    uintptr_t      elem_desc;
    uintptr_t      codec_ptr;
    uintptr_t      elem_rtype;
} DecSliceDesc;

_Static_assert(sizeof(DecSliceDesc) == 32, "DecSliceDesc must be 32 bytes");

typedef struct {
    uint8_t        kind;
    uint8_t        val_kind;
    uint8_t        val_has_ptr;
    uint8_t        _pad;
    uint32_t       val_size;
    uintptr_t      val_desc;
    uintptr_t      codec_ptr;
    uintptr_t      map_rtype;
} DecMapDesc;

_Static_assert(sizeof(DecMapDesc) == 32, "DecMapDesc must be 32 bytes");

typedef struct {
    uint8_t        kind;
    uint8_t        elem_kind;
    uint8_t        elem_has_ptr;
    uint8_t        _pad;
    uint32_t       elem_size;
    uintptr_t      elem_desc;
    uint32_t       array_len;
    uint32_t       _pad2;
} DecArrayDesc;

_Static_assert(sizeof(DecArrayDesc) == 24, "DecArrayDesc must be 24 bytes");

typedef struct {
    uint8_t        kind;
    uint8_t        elem_kind;
    uint8_t        elem_has_ptr;
    uint8_t        _pad;
    uint32_t       elem_size;
    uintptr_t      elem_desc;
    uintptr_t      elem_rtype;
} DecPointerDesc;

_Static_assert(sizeof(DecPointerDesc) == 24, "DecPointerDesc must be 24 bytes");

/* ============================================================
 *  Parse stack (explicit, replaces goroutine recursion)
 * ============================================================ */

#define GSDEC_MAX_DEPTH 128

/* State labels — index into the computed goto table */
enum {
    ST_OBJECT_BODY = 0,
    ST_OBJECT_NEXT,
    ST_SLICE_BODY,
    ST_SLICE_NEXT,
    ST_ARRAY_BODY,
    ST_ARRAY_NEXT,
    ST_MAP_BODY,
    ST_MAP_NEXT,
    ST_SKIP_OBJECT,
    ST_SKIP_ARRAY,
    ST_ANY_OBJECT_BODY,
    ST_ANY_ARRAY_BODY,
    ST_ROOT,
    ST_COUNT,
};

/* ============================================================
 *  Instruction tags (must match Go insn_exec.go constants)
 * ============================================================ */

#define INSN_TAG_SET_TARGET    0x01
#define INSN_TAG_SET_KEY       0x02
#define INSN_TAG_MAKE_OBJECT   0x10
#define INSN_TAG_MAKE_ARRAY    0x11
#define INSN_TAG_CLOSE_OBJECT  0x12
#define INSN_TAG_CLOSE_ARRAY   0x13
#define INSN_TAG_EMIT_NULL     0x20
#define INSN_TAG_EMIT_TRUE     0x21
#define INSN_TAG_EMIT_FALSE    0x22
#define INSN_TAG_EMIT_INT      0x23
#define INSN_TAG_EMIT_STRING   0x26
#define INSN_TAG_EMIT_NUMBER   0x28

#define INSN_RESERVE 24

/* Inline insn helpers — write to ctx->insn_ptr + ctx->insn_len */
static inline void insn_write_raw(DecExecCtx *ctx, const uint8_t *data, uint32_t len) {
    uint8_t *dst = ctx->insn_ptr + ctx->insn_len;
    for (uint32_t i = 0; i < len; i++) dst[i] = data[i];
    ctx->insn_len += len;
}

static inline int insn_near_full(DecExecCtx *ctx) {
    if (!ctx->insn_ptr) return 1;
    return (ctx->insn_cap - ctx->insn_len) < INSN_RESERVE;
}

static inline int insn_flush_if_needed(DecExecCtx *ctx) {
    if ((ctx->insn_cap - ctx->insn_len) < INSN_RESERVE) {
        ctx->exit_code = YIELD_INSN_FLUSH;
        return 1;
    }
    return 0;
}

static inline void insn_emit_u8(DecExecCtx *ctx, uint8_t tag) {
    ctx->insn_ptr[ctx->insn_len++] = tag;
}

static inline void insn_write_le64(uint8_t *dst, uint64_t v) {
    for (int i = 0; i < 8; i++) { dst[i] = (uint8_t)(v >> (i*8)); }
}

static inline void insn_write_le32(uint8_t *dst, uint32_t v) {
    for (int i = 0; i < 4; i++) { dst[i] = (uint8_t)(v >> (i*8)); }
}

/* SET_TARGET [0x01][target:8] = 9B */
static inline void insn_emit_set_target(DecExecCtx *ctx, void *target) {
    uint8_t buf[9];
    buf[0] = INSN_TAG_SET_TARGET;
    insn_write_le64(buf+1, (uint64_t)(uintptr_t)target);
    insn_write_raw(ctx, buf, 9);
}

/* SET_KEY [0x02][ptr:8][len:4] = 13B */
static inline void insn_emit_set_key(DecExecCtx *ctx, const uint8_t *ptr, uint32_t len) {
    uint8_t buf[13];
    buf[0] = INSN_TAG_SET_KEY;
    insn_write_le64(buf+1, (uint64_t)(uintptr_t)ptr);
    insn_write_le32(buf+9, len);
    insn_write_raw(ctx, buf, 13);
}

/* MAKE_OBJECT [0x10][hint:4] = 5B */
static inline void insn_emit_make_object(DecExecCtx *ctx) {
    uint8_t buf[5] = { INSN_TAG_MAKE_OBJECT, 0,0,0,0 };
    insn_write_raw(ctx, buf, 5);
}

/* MAKE_ARRAY [0x11][hint:4] = 5B */
static inline void insn_emit_make_array(DecExecCtx *ctx) {
    uint8_t buf[5] = { INSN_TAG_MAKE_ARRAY, 0,0,0,0 };
    insn_write_raw(ctx, buf, 5);
}

/* EMIT_INT [0x23][value:8] = 9B */
static inline void insn_emit_int(DecExecCtx *ctx, int64_t value) {
    uint8_t buf[9];
    buf[0] = INSN_TAG_EMIT_INT;
    insn_write_le64(buf+1, (uint64_t)value);
    insn_write_raw(ctx, buf, 9);
}

/* EMIT_STRING [0x26][ptr:8][len:4] = 13B */
static inline void insn_emit_string(DecExecCtx *ctx, const uint8_t *ptr, uint32_t len) {
    uint8_t buf[13];
    buf[0] = INSN_TAG_EMIT_STRING;
    insn_write_le64(buf+1, (uint64_t)(uintptr_t)ptr);
    insn_write_le32(buf+9, len);
    insn_write_raw(ctx, buf, 13);
}

/* EMIT_NUMBER [0x28][ptr:8][len:4] = 13B */
static inline void insn_emit_number(DecExecCtx *ctx, const uint8_t *ptr, uint32_t len) {
    uint8_t buf[13];
    buf[0] = INSN_TAG_EMIT_NUMBER;
    insn_write_le64(buf+1, (uint64_t)(uintptr_t)ptr);
    insn_write_le32(buf+9, len);
    insn_write_raw(ctx, buf, 13);
}

typedef struct {
    uint8_t        state;
    uint8_t        val_kind;   /* kind of value being parsed into target */
    uint16_t       _pad;
    uint32_t       aux;        /* state-dependent: count (lo31) | value_done (hi1) */
    uint8_t       *base;       /* target pointer */
    const uint8_t *desc;       /* type descriptor */
    uint32_t       aux2;       /* state-dependent: cap (slice/map) */
    uint32_t       _pad2;
} GsdecFrame;

_Static_assert(sizeof(GsdecFrame) == 32, "GsdecFrame must be 32 bytes");

/* ============================================================
 *  Macros for the parser hot loop
 * ============================================================ */

#define SKIP_WS() \
    while (idx < src_len && ((c = src[idx]) == ' ' || c == '\t' || c == '\n' || c == '\r')) idx++

#define SUSPEND_EOF(rollback) do { \
    ctx->idx = (rollback); \
    ctx->exit_code = EXIT_UNEXPECTED_EOF; \
    ctx->resume_depth = depth; \
    return; \
} while(0)

#define SUSPEND_YIELD(code) do { \
    ctx->idx = idx; \
    ctx->exit_code = (code); \
    ctx->resume_depth = depth; \
    return; \
} while(0)

#define ERROR_AT(off, code) do { \
    ctx->idx = (off); \
    ctx->err_detail = (off); \
    ctx->exit_code = (code); \
    return; \
} while(0)

/* ---- PIC-safe computed goto (relative offsets from base label) ----
 *
 * Absolute label addresses don't survive relocation in .syso.
 * Instead, store int32_t offsets from a base label, and compute
 * the base address at runtime via PC-relative addressing
 * (adr on ARM64, lea %rip on x86_64).
 *
 * Pattern borrowed from encvm. */

#define GDT_ENTRY(label) (int32_t)((char *)&&label - (char *)&&gsdec_dispatch_base)

#if defined(__aarch64__)
#define DISPATCH_TOP() do {                                         \
    uint8_t _st = stack[depth - 1].state;                           \
    char *_base;                                                    \
    __asm__ volatile("adr %0, %c1"                                  \
                     : "=r"(_base)                                  \
                     : "i"(&&gsdec_dispatch_base));                 \
    goto *(void *)(_base + dispatch_table[_st]);                    \
} while(0)
#elif defined(__x86_64__)
#define DISPATCH_TOP() do {                                         \
    uint8_t _st = stack[depth - 1].state;                           \
    char *_base;                                                    \
    __asm__ volatile("lea %c1(%%rip), %0"                           \
                     : "=r"(_base)                                  \
                     : "i"(&&gsdec_dispatch_base));                 \
    goto *(void *)(_base + dispatch_table[_st]);                    \
} while(0)
#else
#error "DISPATCH_TOP: unsupported architecture (need aarch64 or x86_64)"
#endif

#define STACK_PUSH(st, b, d) do { \
    stack[depth].state = (st); \
    stack[depth].base = (uint8_t*)(b); \
    stack[depth].desc = (const uint8_t*)(d); \
    stack[depth].aux = 0; \
    stack[depth].val_kind = 0; \
    depth++; \
} while(0)

#define STACK_POP() (--depth)

#endif /* GSDEC_H */
