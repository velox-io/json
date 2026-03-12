/*
 * encoder.h — Velox JSON C Encoding Engine
 *
 * Three-layer architecture:
 *   Go Pre-compiler  ->  Assembly Bridge  ->  C Engine (this file)
 *
 * This is the top-level header that assembles the encoder from
 * modular sub-headers and contains the threaded-code VM
 * (vj_encode_struct) and the batch array encoder (vj_encode_array).
 *
 * Sub-headers (included in dependency order):
 *   encoder_types.h   — enums, structs, constants
 *   encoder_number.h  — integer-to-ASCII formatting
 *   encoder_string.h  — JSON string escaping (SIMD/SWAR)
 *   ryu.h             — float-to-ASCII (Ryu algorithm)
 *   encoder_pointer.h — out-of-line pointer primitive encoder
 *
 * Design constraints:
 *   - OpStep must be cache-friendly (<=32 bytes on 64-bit).
 *   - EncodingContext is the sole state exchanged between Go and C.
 *   - All memory referenced by the engine is pinned by Go (runtime.Pinner)
 *     before entry; the engine never allocates.
 */

#ifndef VJ_ENCODER_H
#define VJ_ENCODER_H

/* ---- Sub-headers (order matters: each may depend on predecessors) ---- */
#include "encoder_types.h"
#include "encoder_number.h"
#include "encoder_string.h"

/* ================================================================
 *  Float formatting (Ryu algorithm)
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

#include "encoder_pointer.h"

/* ================================================================
 *  Threaded-Code VM: vj_encode_struct
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
      [OP_INTERFACE] = DT_ENTRY(vj_op_interface),
      [OP_MAP] = DT_ENTRY(vj_op_fallback),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER] = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE] = DT_ENTRY(vj_op_fallback),
      [OP_FALLBACK] = DT_ENTRY(vj_op_fallback),
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
    vj_copy_key(buf, op->key_ptr, op->key_len);                                  \
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
    vj_copy_var(buf, raw->data, raw->len);
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
    vj_copy_var(buf, s->ptr, s->len);
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

  /* ==== Inline interface{} encoding ==== */

vj_op_interface: {
  /* GoEface layout: {type_ptr *abi.Type, data_ptr unsafe.Pointer}.
   * If type_ptr is nil, the interface is nil → "null".
   * Otherwise, look up the concrete type in the type tag table and
   * encode primitive values inline.  Unknown types fall through to
   * vj_op_fallback (Go handles structs, slices, maps, etc.). */

  const void *type_ptr = *(const void **)(base + op->field_off);

  /* --- Fast path: nil interface → "null" --- */
  if (type_ptr == NULL) {
    CHECK(op->key_len + 1 + 4);
    WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
    NEXT();
  }

  /* Binary search for type_ptr in sorted type tag table. */
  uint8_t tag = 0;
  {
    const VjIfaceTypeEntry *table = ctx->iface_type_table;
    int32_t lo = 0, hi = ctx->iface_type_count - 1;
    while (lo <= hi) {
      int32_t mid = (lo + hi) >> 1;
      const void *mid_ptr = table[mid].type_ptr;
      if (mid_ptr == type_ptr) { tag = table[mid].tag; break; }
      if ((uintptr_t)mid_ptr < (uintptr_t)type_ptr) lo = mid + 1;
      else hi = mid - 1;
    }
  }

  if (__builtin_expect(tag == 0, 0)) {
    goto vj_op_fallback;
  }

  /* data_ptr: always a pointer to the actual value for scalar types
   * (Go runtime stores all non-pointer types indirectly in eface). */
  const uint8_t *vptr = *(const uint8_t **)(base + op->field_off + 8);

  switch (tag) {
  case OP_BOOL: {
    CHECK(op->key_len + 1 + 5);
    WRITE_KEY();
    if (*(const uint8_t *)vptr) {
      vj_memcpy(buf, "true", 4);
      buf += 4;
    } else {
      vj_memcpy(buf, "false", 5);
      buf += 5;
    }
    break;
  }
  case OP_INT:
  case OP_INT64: {
    CHECK(op->key_len + 1 + 21);
    WRITE_KEY();
    buf += write_int64(buf, *(const int64_t *)vptr);
    break;
  }
  case OP_INT8: {
    CHECK(op->key_len + 1 + 5);
    WRITE_KEY();
    buf += write_int64(buf, (int64_t)*(const int8_t *)vptr);
    break;
  }
  case OP_INT16: {
    CHECK(op->key_len + 1 + 7);
    WRITE_KEY();
    buf += write_int64(buf, (int64_t)*(const int16_t *)vptr);
    break;
  }
  case OP_INT32: {
    CHECK(op->key_len + 1 + 12);
    WRITE_KEY();
    buf += write_int64(buf, (int64_t)*(const int32_t *)vptr);
    break;
  }
  case OP_UINT:
  case OP_UINT64: {
    CHECK(op->key_len + 1 + 21);
    WRITE_KEY();
    buf += write_uint64(buf, *(const uint64_t *)vptr);
    break;
  }
  case OP_UINT8: {
    CHECK(op->key_len + 1 + 4);
    WRITE_KEY();
    buf += write_uint64(buf, (uint64_t)*(const uint8_t *)vptr);
    break;
  }
  case OP_UINT16: {
    CHECK(op->key_len + 1 + 6);
    WRITE_KEY();
    buf += write_uint64(buf, (uint64_t)*(const uint16_t *)vptr);
    break;
  }
  case OP_UINT32: {
    CHECK(op->key_len + 1 + 11);
    WRITE_KEY();
    buf += write_uint64(buf, (uint64_t)*(const uint32_t *)vptr);
    break;
  }
  case OP_FLOAT32: {
    float fval;
    vj_memcpy(&fval, vptr, 4);
    if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
      goto vj_op_fallback; /* Go reports the error */
    }
    CHECK(op->key_len + 1 + 330);
    WRITE_KEY();
    buf += vj_write_float32(buf, fval);
    break;
  }
  case OP_FLOAT64: {
    double dval;
    vj_memcpy(&dval, vptr, 8);
    if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
      goto vj_op_fallback; /* Go reports the error */
    }
    CHECK(op->key_len + 1 + 330);
    WRITE_KEY();
    buf += vj_write_float64(buf, dval);
    break;
  }
  case OP_STRING: {
    const GoString *s = (const GoString *)vptr;
    int64_t max_need = 1 + op->key_len + 2 + (s->len * 6);
    CHECK(max_need);
    WRITE_KEY();
    *buf++ = '"';
    if (s->len > 0) {
      buf += escape_string_content(buf, s->ptr, s->len, flags);
    }
    *buf++ = '"';
    break;
  }
  default:
    goto vj_op_fallback;
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
 *  Array Encoder: vj_encode_array
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
  const uint8_t *arr_data;  /* offset 448 — array base pointer */
  int64_t arr_count;        /* offset 456 — total element count */
  int64_t arr_idx;          /* offset 464 — current element index (resume) */
  int64_t elem_size;        /* offset 472 — sizeof(element) */
  const OpStep *elem_ops;   /* offset 480 — struct ops for each element */
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
    /* Save buf position before any output for this element (including
     * the comma separator).  On BUF_FULL we rewind here so Go only
     * sees fully-encoded elements — no partial output retained. */
    uint8_t *elem_start = buf;

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
      /* Rewind: discard both the comma and partial struct output.
       * Go absorbs only fully-encoded elements, grows the buffer,
       * and retries this element from scratch. */
      ctx->buf_cur = elem_start;
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
