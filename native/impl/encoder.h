/*
 * encoder.h — Velox JSON C Encoding Engine
 *
 * Three-layer architecture:
 *   Go Pre-compiler  ->  Assembly Bridge  ->  C Engine (this file)
 *
 * This is the top-level header that assembles the encoder from
 * modular sub-headers.  The VM implementation is included below.
 *
 * Sub-headers (included in dependency order):
 *   encoder_memory.h  — memcpy/memset impls, SIMD copy helpers
 *   encoder_types.h   — enums, structs, constants
 *   encoder_number.h  — integer-to-ASCII formatting
 *   encoder_string.h  — JSON string escaping (SIMD/SWAR)
 *   ryu.h             — float-to-ASCII (Ryu algorithm)
 *   encoder_pointer.h — out-of-line pointer primitive encoder
 *   encoder_interface.h — out-of-line interface value encoder
 *
 * Design constraints:
 *   - All memory referenced by the engine is pinned by Go (runtime.Pinner)
 *     before entry; the engine never allocates.
 */

#ifndef VJ_ENCODER_H
#define VJ_ENCODER_H

// clang-format off

/* ---- Sub-headers (order matters: each may depend on predecessors) ---- */
#include "encoder_types.h"
#include "encoder_number.h"
#include "encoder_string.h"
#include "ryu.h"
#include "encoder_pointer.h"
#include "encoder_interface.h"

/* ================================================================
 *  VM Implementation — threaded-code interpreter for []VjOpStep
 * ================================================================ */

/* Save VM state to context and return with an error code.
 * Saves PC as an int32 index (op - ops).
 * Packs the 'first' flag into enc_flags so Go can restore it on resume. */
#define VM_SAVE_AND_RETURN(err)                                                \
  do {                                                                         \
    ctx->buf_cur = buf;                                                        \
    ctx->pc = (int32_t)(op - ops);                                             \
    ctx->cur_base = base;                                                      \
    ctx->depth = depth;                                                        \
    ctx->enc_flags =                                                           \
        flags | VJ_ENC_RESUME | (first ? VJ_ENC_RESUME_FIRST : 0);             \
    ctx->error_code = (err);                                                   \
    return;                                                                    \
  } while (0)

static void vj_vm_exec(VjExecCtx *ctx) {

  /* ---- Load context into registers / locals ---- */
  uint8_t *buf = ctx->buf_cur;
  uint8_t *bend = (uint8_t *)ctx->buf_end;
  const VjOpStep *ops = ctx->ops_ptr;
  const VjOpStep *op = &ops[ctx->pc];
  const uint8_t *base = ctx->cur_base;
  int32_t depth = ctx->depth;
  uint32_t flags = ctx->enc_flags;
  int first;

  /* Restore the 'first' flag for resume.
   * VJ_ENC_RESUME is set by Go when re-entering after yield/buf_full.
   * VJ_ENC_RESUME_FIRST indicates no field has been written yet.
   * On initial entry (no RESUME), first=1 because no field exists yet,
   * but we'll hit STRUCT_BEGIN which resets first=1 anyway. */
  if (flags & VJ_ENC_RESUME) {
    first = (flags & VJ_ENC_RESUME_FIRST) ? 1 : 0;
    flags &= ~(uint32_t)(VJ_ENC_RESUME | VJ_ENC_RESUME_FIRST);
  } else {
    first = 1;
  }

/* ---- Computed goto dispatch table ----
 *
 * int32 offsets from base label.
 * Covers primitive, data, structural, and fallback opcodes.
 * Sparse: unused slots are zero-initialized (caught by bounds check).
 */
#define DT_ENTRY(label)                                                        \
  (int32_t)((char *) && label - (char *) && vj_dispatch_base)

  static const int32_t dispatch_table[OP_DISPATCH_COUNT] = {
      /* Primitives (0-13) */
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

      /* Non-primitive data ops (16-19) */
      [OP_INTERFACE] = DT_ENTRY(vj_op_interface),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER] = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE] = DT_ENTRY(vj_op_yield),

      /* Structural control-flow (32-40) */
      [OP_SKIP_IF_ZERO] = DT_ENTRY(vj_op_skip_if_zero),
      [OP_STRUCT_BEGIN] = DT_ENTRY(vj_op_struct_begin),
      [OP_STRUCT_END] = DT_ENTRY(vj_op_struct_end),
      [OP_PTR_DEREF] = DT_ENTRY(vj_op_ptr_deref),
      [OP_PTR_END] = DT_ENTRY(vj_op_ptr_end),
      [OP_SLICE_BEGIN] = DT_ENTRY(vj_op_slice_begin),
      [OP_SLICE_END] = DT_ENTRY(vj_op_slice_end),
      [OP_MAP_BEGIN] = DT_ENTRY(vj_op_map_begin),
      [OP_MAP_END] = DT_ENTRY(vj_op_map_end),
      [OP_OBJ_OPEN] = DT_ENTRY(vj_op_obj_open),
      [OP_OBJ_CLOSE] = DT_ENTRY(vj_op_obj_close),

      /* Go-only fallback (0x3F) */
      [OP_FALLBACK] = DT_ENTRY(vj_op_yield),
  };

#undef DT_ENTRY

/* ---- Check buffer space ---- */
#define VM_CHECK(n)                                                            \
  do {                                                                         \
    if (__builtin_expect(buf + (n) > bend, 0)) {                               \
      VM_SAVE_AND_RETURN(VJ_ERR_BUF_FULL);                                     \
    }                                                                          \
  } while (0)

/* ---- Write pre-encoded key with comma prefix ---- */
#define VM_WRITE_KEY()                                                         \
  do {                                                                         \
    if (!first) {                                                              \
      *buf++ = ',';                                                            \
    }                                                                          \
    first = 0;                                                                 \
    if (op->key_len > 0) {                                                     \
      vj_copy_key(buf, op->key_ptr, op->key_len);                              \
      buf += op->key_len;                                                      \
    }                                                                          \
  } while (0)

/* ---- Dispatch macro (ADR/LEA trick for PIC computed goto) ---- */
#if defined(__aarch64__)
#define VM_DISPATCH()                                                          \
  do {                                                                         \
    uint16_t _raw = op->op_type;                                               \
    uint16_t _opc = _raw & OP_TYPE_MASK;                                       \
    if (__builtin_expect(_opc >= OP_DISPATCH_COUNT, 0))                        \
      goto vj_op_end;                                                          \
    char *_base;                                                               \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_dispatch_base));   \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#elif defined(__x86_64__)
#define VM_DISPATCH()                                                          \
  do {                                                                         \
    uint16_t _raw = op->op_type;                                               \
    uint16_t _opc = _raw & OP_TYPE_MASK;                                       \
    if (__builtin_expect(_opc >= OP_DISPATCH_COUNT, 0))                        \
      goto vj_op_end;                                                          \
    char *_base;                                                               \
    __asm__("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_dispatch_base));     \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#else
#error "VM_DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

#define VM_NEXT()                                                              \
  do {                                                                         \
    op++;                                                                      \
    VM_DISPATCH();                                                             \
  } while (0)
#define VM_JUMP(n)                                                             \
  do {                                                                         \
    op += (n);                                                                 \
    VM_DISPATCH();                                                             \
  } while (0)

  /* ---- Begin dispatch ---- */
  VM_DISPATCH();

vj_dispatch_base:
  __builtin_unreachable();

  /* ---- Primitives (0-13) ---- */

vj_op_bool: {
  VM_CHECK(op->key_len + 1 + 5);
  VM_WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  if (val) {
    vj_memcpy(buf, "true", 4);
    buf += 4;
  } else {
    vj_memcpy(buf, "false", 5);
    buf += 5;
  }
  VM_NEXT();
}

vj_op_int: {
  VM_CHECK(op->key_len + 1 + 21);
  VM_WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT();
}

vj_op_int8: {
  VM_CHECK(op->key_len + 1 + 5);
  VM_WRITE_KEY();
  int8_t val = *(const int8_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  VM_NEXT();
}

vj_op_int16: {
  VM_CHECK(op->key_len + 1 + 7);
  VM_WRITE_KEY();
  int16_t val = *(const int16_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  VM_NEXT();
}

vj_op_int32: {
  VM_CHECK(op->key_len + 1 + 12);
  VM_WRITE_KEY();
  int32_t val = *(const int32_t *)(base + op->field_off);
  buf += write_int64(buf, (int64_t)val);
  VM_NEXT();
}

vj_op_int64: {
  VM_CHECK(op->key_len + 1 + 21);
  VM_WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT();
}

vj_op_uint: {
  VM_CHECK(op->key_len + 1 + 21);
  VM_WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64(buf, val);
  VM_NEXT();
}

vj_op_uint8: {
  VM_CHECK(op->key_len + 1 + 4);
  VM_WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  VM_NEXT();
}

vj_op_uint16: {
  VM_CHECK(op->key_len + 1 + 6);
  VM_WRITE_KEY();
  uint16_t val = *(const uint16_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  VM_NEXT();
}

vj_op_uint32: {
  VM_CHECK(op->key_len + 1 + 11);
  VM_WRITE_KEY();
  uint32_t val = *(const uint32_t *)(base + op->field_off);
  buf += write_uint64(buf, (uint64_t)val);
  VM_NEXT();
}

vj_op_uint64: {
  VM_CHECK(op->key_len + 1 + 21);
  VM_WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64(buf, val);
  VM_NEXT();
}

vj_op_float32: {
  float fval;
  vj_memcpy(&fval, base + op->field_off, 4);
  if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
    ctx->depth = depth;
    VM_SAVE_AND_RETURN(VJ_ERR_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 60);
  VM_WRITE_KEY();
  buf += vj_write_float32(buf, fval);
  VM_NEXT();
}

vj_op_float64: {
  double dval;
  vj_memcpy(&dval, base + op->field_off, 8);
  if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
    ctx->depth = depth;
    VM_SAVE_AND_RETURN(VJ_ERR_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 330);
  VM_WRITE_KEY();
  buf += vj_write_float64(buf, dval);
  VM_NEXT();
}

vj_op_string: {
  const GoString *s = (const GoString *)(base + op->field_off);
  int64_t max_need = 1 + op->key_len + 2 + (s->len * 6);
  VM_CHECK(max_need);
  VM_WRITE_KEY();
  buf += vj_escape_string(buf, (const uint8_t *)s->ptr, s->len, flags);
  VM_NEXT();
}

  /* ---- Non-primitive data ops (16-19): inline handlers ---- */

vj_op_raw_message: {
  const GoSlice *raw = (const GoSlice *)(base + op->field_off);
  if (raw->data == NULL || raw->len == 0) {
    VM_CHECK(op->key_len + 1 + 4);
    VM_WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
  } else {
    VM_CHECK(op->key_len + 1 + raw->len);
    VM_WRITE_KEY();
    vj_copy_var(buf, raw->data, raw->len);
    buf += raw->len;
  }
  VM_NEXT();
}

vj_op_number: {
  const GoString *s = (const GoString *)(base + op->field_off);
  if (s->len == 0) {
    VM_CHECK(op->key_len + 1 + 1);
    VM_WRITE_KEY();
    *buf++ = '0';
  } else {
    VM_CHECK(op->key_len + 1 + s->len);
    VM_WRITE_KEY();
    vj_copy_var(buf, s->ptr, s->len);
    buf += s->len;
  }
  VM_NEXT();
}

  /* ---- Structural control-flow (32-40) ---- */

vj_op_skip_if_zero: {
  /* High byte of op_type encodes the ZeroCheckTag. */
  uint16_t check_type = op->op_type >> 8;
  if (vj_is_zero(base + op->field_off, check_type)) {
    VM_JUMP(1 + op->operand_a); /* skip operand_a instructions + self */
  }
  VM_NEXT(); /* not zero: proceed to next (the guarded instruction) */
}

vj_op_struct_begin: {
  /* Write comma + key + '{' */
  VM_CHECK(op->key_len + 1 + 1);
  VM_WRITE_KEY();

  if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
  }

  /* Push stack frame */
  VjStackFrame *frame = &ctx->stack[depth];
  frame->ret_op = op + 1 + op->operand_a; /* points to after STRUCT_END */
  frame->ret_base = base;
  frame->first = first;
  frame->frame_type = VJ_FRAME_STRUCT;
  depth++;

  /* Switch base to child struct */
  base = base + op->field_off;
  first = 1;
  *buf++ = '{';
  VM_NEXT();
}

vj_op_struct_end: {
  VM_CHECK(1);
  *buf++ = '}';

  depth--;
  VjStackFrame *frame = &ctx->stack[depth];
  base = frame->ret_base;
  first = 0; /* parent had at least this struct field */
  /* Continue with parent's next instruction (linear) */
  VM_NEXT();
}

vj_op_ptr_deref: {
  void *ptr = *(void **)(base + op->field_off);

  if (ptr == NULL) {
    /* nil pointer → write key + "null", jump over deref body */
    VM_CHECK(op->key_len + 1 + 4);
    VM_WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
    VM_JUMP(1 + op->operand_a); /* skip deref body */
  }

  /* Non-nil: write key, switch base to dereferenced address */
  VM_CHECK(op->key_len + 1);
  VM_WRITE_KEY();

  /* Push frame to restore base after deref body */
  if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[depth];
  frame->ret_op = op + 1 + op->operand_a; /* after deref body */
  frame->ret_base = base;
  frame->first = first;
  frame->frame_type = VJ_FRAME_STRUCT; /* reuse struct frame for ptr */
  depth++;

  base = (const uint8_t *)ptr;
  first = 1; /* deref body is a "value" context — no leading comma */
  VM_NEXT();
}

vj_op_ptr_end: {
  /* Pop the ptr-deref frame, restore parent base */
  depth--;
  VjStackFrame *frame = &ctx->stack[depth];
  base = frame->ret_base;
  first = 0; /* parent had at least this ptr field */
  VM_NEXT();
}

vj_op_slice_begin: {
  const GoSlice *sl = (const GoSlice *)(base + op->field_off);

  VM_CHECK(op->key_len + 1 + 4);
  VM_WRITE_KEY();

  if (sl->data == NULL) {
    /* nil → "null" */
    vj_memcpy(buf, "null", 4);
    buf += 4;
    VM_JUMP(1 + op->operand_b + 1); /* skip body + SLICE_END */
  }
  if (sl->len == 0) {
    /* empty → "[]" */
    *buf++ = '[';
    *buf++ = ']';
    VM_JUMP(1 + op->operand_b + 1); /* skip body + SLICE_END */
  }

  *buf++ = '[';

  /* Push loop frame */
  if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[depth];
  frame->iter_data = sl->data;
  frame->iter_count = sl->len;
  frame->iter_idx = 0;
  frame->elem_size = op->operand_a; /* element size */
  frame->loop_pc_op = op + 1;       /* first body instruction */
  frame->ret_base = base;
  frame->first = first;
  frame->frame_type = VJ_FRAME_SLICE;
  depth++;

  base = sl->data; /* base = &elem[0] */
  first = 1;       /* first element has no comma */
  VM_NEXT();
}

vj_op_slice_end: {
  VjStackFrame *frame = &ctx->stack[depth - 1];
  frame->iter_idx++;

  if (frame->iter_idx < frame->iter_count) {
    /* More elements: write comma, advance base, jump back */
    VM_CHECK(1);
    *buf++ = ',';
    base = frame->iter_data + frame->iter_idx * frame->elem_size;
    op = frame->loop_pc_op;
    first = 1; /* reset for element-level encoding (no struct comma) */
    VM_DISPATCH();
  }

  /* Done: write ']', pop frame */
  VM_CHECK(1);
  *buf++ = ']';
  depth--;
  base = frame->ret_base;
  first = 0; /* parent had at least this field */
  VM_NEXT();
}

vj_op_obj_open: {
  /* Lightweight nested struct open: write key + '{', flip first flag.
   * No stack frame push, no base switch — child field offsets are
   * pre-computed (absolute from top-level struct base). */
  VM_CHECK(op->key_len + 1 + 1);
  VM_WRITE_KEY();
  *buf++ = '{';
  first = 1;
  VM_NEXT();
}

vj_op_obj_close: {
  /* Lightweight nested struct close: write '}', set first=0.
   * No stack frame pop — mirrors vj_op_obj_open. */
  VM_CHECK(1);
  *buf++ = '}';
  first = 0;
  VM_NEXT();
}

vj_op_map_begin: {
  /* Map iteration is Go-driven. Yield to Go, which will:
   * - Initialize map iterator
   * - For each entry: write key to buffer, set base to value ptr,
   *   then re-enter C to execute the value body instructions
   * - Finally advance PC past MAP_END */
  ctx->yield_info = VJ_YIELD_MAP_NEXT;
  VM_SAVE_AND_RETURN(VJ_ERR_YIELD);
}

vj_op_map_end: {
  /* After value encoding, yield back to Go for next entry */
  ctx->yield_info = VJ_YIELD_MAP_NEXT;
  VM_SAVE_AND_RETURN(VJ_ERR_YIELD);
}

  /* ---- Non-primitive data ops (16-19): interface (out-of-line) ---- */

vj_op_interface: {
  const void *type_ptr = *(const void **)(base + op->field_off);

  /* nil interface → "null" (trivial — stays inline) */
  if (type_ptr == NULL) {
    VM_CHECK(op->key_len + 1 + 4);
    VM_WRITE_KEY();
    vj_memcpy(buf, "null", 4);
    buf += 4;
    VM_NEXT();
  }

  /* Non-nil: speculatively write key, then delegate to out-of-line handler.
   * Save buf/first so we can undo the key write on yield/miss paths
   * (Go handles its own key+comma when falling back). */
  VM_CHECK(op->key_len + 1 + 330);
  uint8_t *iface_saved_buf = buf;
  int iface_saved_first = first;
  VM_WRITE_KEY();

  VjIfaceResult iface_r = vj_encode_interface_value(
      buf, bend, base + op->field_off, ctx->iface_cache_ptr,
      ctx->iface_cache_count, flags);

  switch (__builtin_expect(iface_r.action, VJ_IFACE_DONE)) {
  case VJ_IFACE_DONE:
    buf = iface_r.buf;
    VM_NEXT();
  case VJ_IFACE_YIELD:
    /* Undo key write — Go fallback handles key+comma itself */
    buf = iface_saved_buf;
    first = iface_saved_first;
    goto vj_op_yield;
  case VJ_IFACE_CACHE_MISS:
    /* Undo key write — Go will re-encode after compilation */
    buf = iface_saved_buf;
    first = iface_saved_first;
    ctx->yield_type_ptr = iface_r.type_ptr;
    ctx->yield_info = VJ_YIELD_IFACE_MISS;
    VM_SAVE_AND_RETURN(VJ_ERR_YIELD);
  case VJ_IFACE_SWITCH_OPS:
    /* Key was written; push interface frame and switch to cached Blueprint */
    if (__builtin_expect(depth >= VJ_MAX_DEPTH, 0)) {
      VM_SAVE_AND_RETURN(VJ_ERR_STACK_OVERFLOW);
    }
    {
      VjStackFrame *frame = &ctx->stack[depth];
      frame->ret_op = op + 1;
      frame->ret_base = base;
      frame->first = first;
      frame->frame_type = VJ_FRAME_IFACE;
      frame->ret_ops = ops;
      depth++;
    }
    ops = iface_r.cached_ops;
    op = &ops[0];
    base = iface_r.data_ptr; /* set base to struct data inside interface */
    first = 1;               /* interface value context: no leading comma */
    VM_DISPATCH();           /* execute cached Blueprint ops */
  case VJ_IFACE_BUF_FULL:
    VM_SAVE_AND_RETURN(VJ_ERR_BUF_FULL);
  case VJ_IFACE_NAN_INF:
    /* Undo key write on error — the error will be reported to Go */
    buf = iface_saved_buf;
    first = iface_saved_first;
    VM_SAVE_AND_RETURN(VJ_ERR_NAN_INF);
  default:
    buf = iface_saved_buf;
    first = iface_saved_first;
    goto vj_op_yield;
  }
}

  /* ---- End / Yield ---- */

vj_op_end: {
  if (__builtin_expect(op->op_type != OP_END, 0)) {
    goto vj_op_yield;
  }

  if (depth > 0) {
    depth--;
    VjStackFrame *frame = &ctx->stack[depth];

    if (frame->frame_type == VJ_FRAME_IFACE) {
      /* Return from interface call: restore parent ops and continue */
      ops = frame->ret_ops;
      op = frame->ret_op;
      base = frame->ret_base;
      first = 0;
      VM_DISPATCH();
    }

    /* Normal struct end (shouldn't reach here — STRUCT_END handles it) */
    op = frame->ret_op;
    base = frame->ret_base;
    first = 0;
    VM_DISPATCH();
  }

  /* Top-level done */
  ctx->buf_cur = buf;
  ctx->depth = depth;
  ctx->error_code = VJ_OK;
  return;
}

vj_op_yield: {
  ctx->yield_info = VJ_YIELD_FALLBACK;
  /* Pack first flag into bit 31 of yield_field_idx so Go knows
   * whether a comma is needed when encoding the fallback field. */
  ctx->yield_field_idx = op->operand_a | (first ? (int32_t)0x80000000 : 0);
  VM_SAVE_AND_RETURN(VJ_ERR_YIELD);
}

/* ---- Cleanup macros ---- */
#undef VM_CHECK
#undef VM_WRITE_KEY
#undef VM_DISPATCH
#undef VM_NEXT
#undef VM_JUMP
}

#undef VM_SAVE_AND_RETURN

#endif /* VJ_ENCODER_H */
