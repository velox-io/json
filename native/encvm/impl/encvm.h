/*
 * encvm.h — Velox JSON C Encoding Engine
 *
 * STACK BUDGET — Go goroutine nosplit constraint
 * ───────────────────────────────────────────────
 * The VM entry function (e.g. vj_vm_exec_full_neon) is called from Go
 * through a NOSPLIT trampoline — no Go stack-growth check fires between
 * the last morestack-enabled Go frame and the C code.  The total stack
 * consumed by the nosplit chain must stay within Go's stackNosplit budget:
 *
 *   stackNosplit = StackNosplitBase (800) × StackGuardMultiplier (1 or 2)
 *                = 800 bytes (normal) / 1600 bytes (-race)
 *
 */

#ifndef VJ_ENCVM_H
#define VJ_ENCVM_H

// clang-format off

#include "types.h"
#include "number.h"
#include "strfn.h"
#include "uscale.h"
#include "iface.h"
#include "trace.h"
#include "swissmap.h"

/* ================================================================
 *  VM Implementation — threaded-code interpreter for variable-length ops
 *
 *  Each handler uses VM_NEXT_SHORT() or VM_NEXT_LONG() — the instruction
 *  width is known at compile time (no runtime size decode).
 * ================================================================ */

/* Save VM state to context and return.
 * Writes the packed vmstate (with exit code set) back to ctx.
 * Go reads exit/yield/depth/first directly from vmstate. */
#define VM_SAVE_AND_RETURN(exit_code)                                          \
  do {                                                                         \
    VM_TRACE_MSG("SAVE_AND_RETURN");                                            \
    ctx->buf_cur = buf;                                                        \
    ctx->ops_ptr = ops;                                                        \
    ctx->pc = (int32_t)((const uint8_t *)op - ops);                            \
    ctx->cur_base = base;                                                      \
    VM_SAVE_INDENT_DEPTH();                                                    \
    VJ_ST_SET_EXIT(vmstate, exit_code);                                         \
    ctx->vmstate = vmstate;                                                    \
    return;                                                                    \
  } while (0)

/* VJ_VM_EXEC_FN_NAME must be defined by the including .c file before
 * #include "encvm.h".  It expands to the suffixed entry-point symbol
 * name (e.g. vj_vm_exec_full_sse42).  This eliminates the wrapper
 * function and avoids an extra jmp/call into the VM body. */
#ifndef VJ_VM_EXEC_FN_NAME
#error "VJ_VM_EXEC_FN_NAME must be defined before including encvm.h"
#endif

VJ_ALIGN_STACK void VJ_VM_EXEC_FN_NAME(VjExecCtx *ctx) {

  /* ---- Load context into registers / locals ---- */
  uint8_t *buf = ctx->buf_cur;
  uint8_t *bend = (uint8_t *)ctx->buf_end;
  const uint8_t *ops = ctx->ops_ptr;
  const VjOpHdr *op = (const VjOpHdr *)(ops + ctx->pc);
  const uint8_t *base = ctx->cur_base;

  /* Global key pool base pointer — loaded once at VM entry.
   * All VjOpHdr key_off values index into this pool.
   * Stable for the entire VM execution (COW snapshot on Go side). */
  const uint8_t *key_pool = ctx->key_pool_base;

  /* Packed VM state — single register holding depth, first, flags,
   * exit_code, yield_reason.  See types.h for layout.
   *
   * The hot-path first-flag check runs every opcode dispatch — a stack
   * round-trip there costs ~2-3 extra cycles per instruction.  We use
   * VM_PIN_VMSTATE() — an empty volatile asm with a "+r" constraint —
   * at key points to prevent the compiler from spilling vmstate to the
   * stack.  The asm generates zero instructions but acts as a register-
   * allocation fence that forces vmstate to stay in *some* register. */
  uint64_t vmstate = ctx->vmstate;
  #define VM_PIN_VMSTATE() __asm__ volatile("" : "+r"(vmstate))
  VM_PIN_VMSTATE();

/* Combined depth for trace output and indent calculations. */
#ifdef VJ_ENCVM_DEBUG
  /* obj_depth tracks OBJ_OPEN/CLOSE nesting for trace indentation only.
   * On resume, derive from indent_depth which is saved across yields. */
#ifdef VJ_COMPACT_INDENT
  int obj_depth = 0;
#else
  int obj_depth = (int)ctx->indent_depth - VJ_ST_GET_DEPTH(vmstate);
#endif
  VjTraceBuf *tbuf = ctx->trace_buf;
  #define VM_DEPTH() (VJ_ST_GET_DEPTH(vmstate) + obj_depth)
  /* Save/restore trace depth in state bits [24..31] across push/pop.
   * Bit 0 of state is the iter-active flag (used by C-native loops). */
  #define VM_SAVE_TRACE_DEPTH(frame) \
    ((frame)->state = ((frame)->state & 0xFF) | (obj_depth << 24))
  #define VM_RESTORE_TRACE_DEPTH(frame) \
    (obj_depth = (int32_t)((frame)->state >> 24))
  VM_TRACE_MSG("VM_ENTER");
#else
  #define VM_DEPTH() (VJ_ST_GET_DEPTH(vmstate))
  #define VM_SAVE_TRACE_DEPTH(frame) ((void)0)
  #define VM_RESTORE_TRACE_DEPTH(frame) ((void)0)
#endif

  /* Indent state: indent_step == 0 means compact mode (no indentation).
   * indent_tpl points to a precomputed "\n" + prefix + indent×MAX_DEPTH buffer.
   * indent_depth tracks logical nesting (incremented at {/[, decremented at }/]).
   * indent_prefix_len is the byte length of the prefix between "\n" and the
   * repeated indent string.
   *
   * NOTE: Indent fields are now at offset 64+ (cache line 1), separate from
   * the hot VM registers in cache line 0. This is intentional — compact mode
   * never touches them, and indent mode accesses them less frequently than
   * ops/pc/base/buf. */
#ifdef VJ_COMPACT_INDENT
  /* Compact mode: indent state eliminated at compile time.
   * All VM_INDENT_PAD → 0, VM_WRITE_INDENT → nop, key space → nop. */
  #define indent_tpl        ((const uint8_t *)0)
  #define indent_depth      ((int16_t)0)
  #define indent_step       ((uint8_t)0)
  #define indent_prefix_len ((uint8_t)0)
  #define VM_KEY_SPACE      0
  #define VM_INDENT_INC()      ((void)0)
  #define VM_INDENT_DEC()      ((void)0)
  #define VM_SAVE_INDENT_DEPTH() ((void)0)
#else
  const uint8_t *indent_tpl = ctx->indent_tpl;
  int16_t indent_depth = ctx->indent_depth;
  const uint8_t indent_step = ctx->indent_step;
  const uint8_t indent_prefix_len = ctx->indent_prefix_len;
  #define VM_KEY_SPACE      (indent_step ? 1 : 0)
  #define VM_INDENT_INC()      (indent_depth++)
  #define VM_INDENT_DEC()      (indent_depth--)
  #define VM_SAVE_INDENT_DEPTH() (ctx->indent_depth = indent_depth)
#endif

  /* The 'first' flag and encoding flags are packed in vmstate.
   * Go sets vmstate with the correct first/flags state before entry.
   * No RESUME flag parsing needed — vmstate is the single source of truth. */

/* ---- Computed goto dispatch table ----
 *
 * int32 offsets from base label.
 * Covers primitive, data, structural, and fallback opcodes.
 * Sparse: unused slots are zero-initialized (caught by bounds check).
 */
#define DT_ENTRY(label) (int32_t)((char *) && label - (char *) && vj_dispatch_base)

  static const int32_t dispatch_table[OP_DISPATCH_COUNT] __attribute__((aligned(64))) = {
      /* Primitives (1-14) */
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

      /* Non-primitive data ops (17-20) */
      [OP_INTERFACE] = DT_ENTRY(vj_op_interface),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER] = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE] = DT_ENTRY(vj_op_yield),

      /* Structural control-flow (33-46) */
      [OP_SKIP_IF_ZERO] = DT_ENTRY(vj_op_skip_if_zero),
      [OP_CALL] = DT_ENTRY(vj_op_call),
      [OP_PTR_DEREF] = DT_ENTRY(vj_op_ptr_deref),
      [OP_PTR_END] = DT_ENTRY(vj_op_ptr_end),
      [OP_SLICE_BEGIN] = DT_ENTRY(vj_op_slice_begin),
      [OP_SLICE_END] = DT_ENTRY(vj_op_slice_end),
      [OP_MAP_BEGIN] = DT_ENTRY(vj_op_map_begin),
      [OP_MAP_END] = DT_ENTRY(vj_op_map_end),
      [OP_OBJ_OPEN] = DT_ENTRY(vj_op_obj_open),
      [OP_OBJ_CLOSE] = DT_ENTRY(vj_op_obj_close),
      [OP_ARRAY_BEGIN] = DT_ENTRY(vj_op_array_begin),
      [OP_MAP_STR_STR] = DT_ENTRY(vj_op_map_str_str),
      [OP_RET] = DT_ENTRY(vj_op_ret),

      /* Go-only fallback (0x40) */
      [OP_FALLBACK] = DT_ENTRY(vj_op_yield),
  };

#undef DT_ENTRY

  #ifdef VJ_ENCVM_DEBUG
  //{
  //    char *base = (char *)&&vj_dispatch_base;
  //    vj_fprintf_stderr("[encvm] dispatch_table=%p base=%p count=%u\n",
  //                      (void *)dispatch_table, (void *)base,
  //                      (uint32_t)OP_DISPATCH_COUNT);

  //    for (uint32_t i = 0; i < (uint32_t)OP_DISPATCH_COUNT; i++) {
  //      int32_t off = dispatch_table[i];
  //      if (off == 0) {
  //        vj_fprintf_stderr("[encvm] dt[%u]=0 (unused)\n", i);
  //      } else {
  //        void *target = (void *)(base + off);
  //        vj_fprintf_stderr("[encvm] dt[%u]=%d (0x%x) -> %p\n",
  //                          i, off, (uint32_t)off, target);
  //      }
  //    }
  //}
  #endif

/* ---- Check buffer space ---- */
#define VM_CHECK(n)                                                            \
  do {                                                                         \
    if (__builtin_expect(buf + (n) > bend, 0)) {                               \
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);                                     \
    }                                                                          \
  } while (0)

/* ---- Indent helpers ---- */

/* Max indent bytes for VM_CHECK: '\n' + prefix + indent_depth * indent_step.
 * Returns 0 in compact mode (indent_step == 0). */
#define VM_INDENT_PAD(idepth) (indent_step ? (1 + indent_prefix_len + (idepth) * indent_step) : 0)

/* Write indent: '\n' + prefix + indent for current indent_depth.
 * No-op in compact mode. */
#define VM_WRITE_INDENT()                                                      \
  do {                                                                         \
    if (indent_step) {                                                         \
      int _n = 1 + indent_prefix_len + indent_depth * indent_step;             \
      __builtin_memcpy(buf, indent_tpl, _n);                                   \
      buf += _n;                                                               \
    }                                                                          \
  } while (0)

/* ---- Write pre-encoded key with comma prefix ---- */
#define VM_WRITE_KEY()                                                         \
  do {                                                                         \
    VM_PIN_VMSTATE();                                                          \
    int _was_first;                                                            \
    VJ_ST_BTR_FIRST(vmstate, _was_first);                                      \
    if (!_was_first) {                                                         \
      *buf++ = ',';                                                            \
      VM_WRITE_INDENT();                                                       \
    }                                                                          \
    if (op->key_len > 0) {                                                     \
      vj_copy_key(buf, (const char *)(key_pool + op->key_off), op->key_len);   \
      buf += op->key_len;                                                      \
      if (indent_step) { *buf++ = ' '; }                                       \
    }                                                                          \
  } while (0)

/* ---- Dispatch macro (ADR/LEA trick for PIC computed goto) ---- */
#if defined(__aarch64__)
#define VM_DISPATCH()                                                          \
  do {                                                                         \
    uint16_t _opc = op->op_type;                                               \
    if (__builtin_expect(_opc >= OP_DISPATCH_COUNT, 0))                        \
      goto vj_op_halt;                                                          \
    char *_base;                                                               \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_dispatch_base));   \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#elif defined(__x86_64__)
#define VM_DISPATCH()                                                          \
  do {                                                                         \
    uint16_t _opc = op->op_type;                                               \
    if (__builtin_expect(_opc >= OP_DISPATCH_COUNT, 0))                        \
      goto vj_op_halt;                                                          \
    char *_base;                                                               \
    __asm__("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_dispatch_base));     \
    goto *(void *)(_base + dispatch_table[_opc]);                              \
  } while (0)
#else
#error "VM_DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Static-width advance macros — each handler knows its own instruction
 * size at compile time, so no runtime size decode is needed.            */
#define VM_NEXT_SHORT()                                                        \
  do {                                                                         \
    op = (const VjOpHdr *)((const uint8_t *)op + 8);                           \
    VM_DISPATCH();                                                             \
  } while (0)
#define VM_NEXT_LONG()                                                         \
  do {                                                                         \
    op = (const VjOpHdr *)((const uint8_t *)op + 16);                          \
    VM_DISPATCH();                                                             \
  } while (0)
#define VM_JUMP_BYTES(byte_offset)                                             \
  do {                                                                         \
    op = (const VjOpHdr *)((const uint8_t *)op + (byte_offset));               \
    VM_DISPATCH();                                                             \
  } while (0)

  /* ---- Begin dispatch ---- */
  VM_DISPATCH();

vj_dispatch_base:
  __builtin_unreachable();

  /* ---- Primitives (1-14) ---- */

vj_op_bool: {
  VM_TRACE_KEY("BOOL");
  VM_CHECK(op->key_len + 1 + 5 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  if (val) {
    __builtin_memcpy(buf, "true", 4);
    buf += 4;
  } else {
    __builtin_memcpy(buf, "false", 5);
    buf += 5;
  }
  VM_NEXT_SHORT();
}

vj_op_int: {
  VM_TRACE_KEY("INT");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT_SHORT();
}

vj_op_int8: {
  VM_TRACE_KEY("INT8");
  VM_CHECK(op->key_len + 1 + 5 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int8_t val = *(const int8_t *)(base + op->field_off);
  buf += write_int64_call(buf, (int64_t)val);
  VM_NEXT_SHORT();
}

vj_op_int16: {
  VM_TRACE_KEY("INT16");
  VM_CHECK(op->key_len + 1 + 7 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int16_t val = *(const int16_t *)(base + op->field_off);
  buf += write_int64_call(buf, (int64_t)val);
  VM_NEXT_SHORT();
}

vj_op_int32: {
  VM_TRACE_KEY("INT32");
  VM_CHECK(op->key_len + 1 + 12 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int32_t val = *(const int32_t *)(base + op->field_off);
  buf += write_int64_call(buf, (int64_t)val);
  VM_NEXT_SHORT();
}

vj_op_int64: {
  VM_TRACE_KEY("INT64");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT_SHORT();
}

vj_op_uint: {
  VM_TRACE_KEY("UINT");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64_call(buf, val);
  VM_NEXT_SHORT();
}

vj_op_uint8: {
  VM_TRACE_KEY("UINT8");
  VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint8_t val = *(const uint8_t *)(base + op->field_off);
  buf += write_uint64_call(buf, (uint64_t)val);
  VM_NEXT_SHORT();
}

vj_op_uint16: {
  VM_TRACE_KEY("UINT16");
  VM_CHECK(op->key_len + 1 + 6 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint16_t val = *(const uint16_t *)(base + op->field_off);
  buf += write_uint64_call(buf, (uint64_t)val);
  VM_NEXT_SHORT();
}

vj_op_uint32: {
  VM_TRACE_KEY("UINT32");
  VM_CHECK(op->key_len + 1 + 11 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint32_t val = *(const uint32_t *)(base + op->field_off);
  buf += write_uint64_call(buf, (uint64_t)val);
  VM_NEXT_SHORT();
}

vj_op_uint64: {
  VM_TRACE_KEY("UINT64");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint64_t val = *(const uint64_t *)(base + op->field_off);
  buf += write_uint64_call(buf, val);
  VM_NEXT_SHORT();
}

vj_op_float32: {
  VM_TRACE_KEY("FLOAT32");
  float fval;
  __builtin_memcpy(&fval, base + op->field_off, 4);
  if (__builtin_expect(__builtin_isnan(fval) || __builtin_isinf(fval), 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 60 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += us_write_float32(buf, fval, (VJ_ST_GET_FLAGS(vmstate) & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO : US_FMT_FIXED);
  VM_NEXT_SHORT();
}

vj_op_float64: {
  VM_TRACE_KEY("FLOAT64");
  double dval;
  __builtin_memcpy(&dval, base + op->field_off, 8);
  if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 330 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += us_write_float64(buf, dval, (VJ_ST_GET_FLAGS(vmstate) & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO : US_FMT_FIXED);
  VM_NEXT_SHORT();
}

vj_op_string: {
  VM_TRACE_KEY("STRING");
  const GoString *s = (const GoString *)(base + op->field_off);
  int64_t max_need = 1 + op->key_len + 2 + (s->len * 6) + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE;
  VM_CHECK(max_need);
  VM_WRITE_KEY();
#ifdef VJ_FAST_STRING_ESCAPE
  buf += vj_escape_string_fast(buf, (const uint8_t *)s->ptr, s->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
#endif
  VM_NEXT_SHORT();
}

  /* ---- Non-primitive data ops (17-20): inline handlers ---- */

vj_op_raw_message: {
  VM_TRACE_KEY("RAW_MESSAGE");
  const GoSlice *raw = (const GoSlice *)(base + op->field_off);
  if (raw->data == NULL || raw->len == 0) {
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
  } else {
    VM_CHECK(op->key_len + 1 + raw->len + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    vj_copy_var(buf, raw->data, raw->len);
    buf += raw->len;
  }
  VM_NEXT_SHORT();
}

vj_op_number: {
  VM_TRACE_KEY("NUMBER");
  const GoString *s = (const GoString *)(base + op->field_off);
  if (s->len == 0) {
    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    *buf++ = '0';
  } else {
    VM_CHECK(op->key_len + 1 + s->len + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    vj_copy_var(buf, s->ptr, s->len);
    buf += s->len;
  }
  VM_NEXT_SHORT();
}

  /* ---- Structural control-flow (33-46) ---- */

vj_op_skip_if_zero: {
  VM_TRACE("SKIP_IF_ZERO");
  const VjOpExt *ext = VJ_OP_EXT(op);
  uint16_t check_type = (uint16_t)ext->operand_b;
  if (vj_is_zero(base + op->field_off, check_type)) {
    VM_JUMP_BYTES(ext->operand_a); /* byte offset from op start to target */
  }
  VM_NEXT_LONG();
}

vj_op_call: {
  VM_TRACE("CALL");
  if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
  frame->call.ret_ops = ops;
  frame->call.ret_pc = (int32_t)((const uint8_t *)op - ops) + 16; /* CALL is always 16 bytes */
  frame->ret_base = base;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_DEPTH(vmstate);

  const VjOpExt *ext = VJ_OP_EXT(op);
  base = base + op->field_off;
  op = (const VjOpHdr *)(ops + ext->operand_a);
  VJ_ST_SET_FIRST_1(vmstate);
  VM_DISPATCH();
}

vj_op_ptr_deref: {
  void *ptr = *(void **)(base + op->field_off);
  const VjOpExt *ext = VJ_OP_EXT(op);

  if (ptr == NULL) {
    /* nil pointer → write key + "null", jump over deref body */
    VM_TRACE_KEY("PTR_DEREF(nil)");
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_JUMP_BYTES(ext->operand_a); /* byte offset from op start to target */
  }

  /* Non-nil: write key, switch base to dereferenced address */
  VM_TRACE_KEY("PTR_DEREF");
  VM_CHECK(op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();

  /* Push call frame — only ret_base is needed.
   * PTR_END always pops this frame (never vj_op_end), so ret_ops and
   * ret_pc are dead stores: PTR_END restores base from ret_base,
   * hardcodes first=0 in vmstate, and advances with VM_NEXT_SHORT(). */
  if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  ctx->stack[VJ_ST_GET_DEPTH(vmstate)].ret_base = base;
  VM_SAVE_TRACE_DEPTH(&ctx->stack[VJ_ST_GET_DEPTH(vmstate)]);
  VJ_ST_INC_DEPTH(vmstate);

  base = (const uint8_t *)ptr;
  VJ_ST_SET_FIRST_1(vmstate); /* deref body is a "value" context — no leading comma */
  VM_NEXT_LONG();
}

vj_op_ptr_end: {
  /* Pop the ptr-deref frame, restore parent base */
  VJ_ST_DEC_DEPTH(vmstate);
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("PTR_END");
  base = frame->ret_base;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this ptr field */
  VM_NEXT_SHORT();
}

vj_op_slice_begin: {
  const GoSlice *sl = (const GoSlice *)(base + op->field_off);
  const VjOpExt *ext = VJ_OP_EXT(op);
  /* ext->operand_a = elem_size, ext->operand_b = body byte length (excl SLICE_END) */

  VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
  VM_WRITE_KEY();

  if (sl->data == NULL) {
    /* nil → "null" */
    VM_TRACE_KEY("SLICE_BEGIN(nil)");
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_JUMP_BYTES(16 + ext->operand_b + 16); /* self(16) + body + SLICE_END(16) */
  }
  if (sl->len == 0) {
    /* empty → "[]" */
    VM_TRACE_KEY("SLICE_BEGIN(empty)");
    *buf++ = '[';
    *buf++ = ']';
    VM_JUMP_BYTES(16 + ext->operand_b + 16); /* self(16) + body + SLICE_END(16) */
  }

  VM_TRACE_KEY_LEN("SLICE_BEGIN", sl->len);
  *buf++ = '[';
  VM_INDENT_INC();
  VM_WRITE_INDENT(); /* indent for first element */

  /* Push iter frame */
  if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
  frame->ret_base = base;
  frame->seq.iter_data = sl->data;
  frame->seq.iter_count = sl->len;
  frame->seq.iter_idx = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_DEPTH(vmstate);

  base = sl->data; /* base = &elem[0] */
  VM_TRACE_ELEM_IDX(0);
  VJ_ST_SET_FIRST_1(vmstate); /* first element has no comma */
  VM_NEXT_LONG();
}

vj_op_slice_end: {
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate) - 1];
  frame->seq.iter_idx++;
  const VjOpExt *ext = VJ_OP_EXT(op);

  if (frame->seq.iter_idx < frame->seq.iter_count) {
    /* More elements: write comma + indent, advance base, jump back.
     * body byte offset and elem_size are compile-time constants. */
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
    *buf++ = ',';
    VM_WRITE_INDENT();
    base = frame->seq.iter_data + (int64_t)frame->seq.iter_idx * ext->operand_b;
    op = (const VjOpHdr *)(ops + ext->operand_a); /* jump to body start (byte offset) */
    VM_TRACE_ELEM_IDX(frame->seq.iter_idx);
    VJ_ST_SET_FIRST_1(vmstate); /* reset for element-level encoding (no struct comma) */
    VM_DISPATCH();
  }

  /* Done: write indent + ']', pop frame */
  VM_INDENT_DEC();
  VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
  VM_WRITE_INDENT();
  *buf++ = ']';
  VJ_ST_DEC_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("SLICE_END");
  base = frame->ret_base;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this field */
  VM_NEXT_LONG();
}

vj_op_obj_open: {
  /* Lightweight nested struct open: write key + '{', flip first flag.
   * No stack frame push, no base switch — child field offsets are
   * pre-computed (absolute from top-level struct base). */
  VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
  VM_WRITE_KEY();
  *buf++ = '{';
  VM_INDENT_INC();
  VM_WRITE_INDENT(); /* indent for first child field */
  VM_TRACE_KEY("OBJ_OPEN");
#ifdef VJ_ENCVM_DEBUG
  obj_depth++;
#endif
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_SHORT();
}

vj_op_obj_close: {
#ifdef VJ_ENCVM_DEBUG
  if (obj_depth > 0) obj_depth--;
#endif
  VM_TRACE("OBJ_CLOSE");
  /* Lightweight nested struct close: write indent + '}', set first=0.
   * No stack frame pop — mirrors vj_op_obj_open. */
  VM_INDENT_DEC();
  VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
  { int _was_first; VJ_ST_BTR_FIRST(vmstate, _was_first);
    if (!_was_first) { VM_WRITE_INDENT(); }
  }
  *buf++ = '}';
  VM_NEXT_SHORT();
}

vj_op_array_begin: {
  /* Fixed-size array: data is inline at base + field_off.
   * ext->operand_a packs elem_size (low 16) | array_len (high 16).
   * ext->operand_b = body byte length (excl SLICE_END).
   * Reuses VJ_FRAME_ITER for the stack frame and opSliceEnd for back-edge. */
  const VjOpExt *ext = VJ_OP_EXT(op);
  int32_t packed = ext->operand_a;
  int32_t arr_elem_size = packed & 0xFFFF;
  int32_t array_len = (uint32_t)packed >> 16;
  const uint8_t *arr_data = base + op->field_off;

  VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
  VM_WRITE_KEY();

  if (array_len == 0) {
    VM_TRACE_KEY("ARRAY_BEGIN(empty)");
    *buf++ = '[';
    *buf++ = ']';
    VM_JUMP_BYTES(16 + ext->operand_b + 16); /* self(16) + body + SLICE_END(16) */
  }

  *buf++ = '[';
  VM_INDENT_INC();
  VM_WRITE_INDENT();

  if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
  frame->ret_base = base;
  frame->seq.iter_data = arr_data;
  frame->seq.iter_count = array_len;
  frame->seq.iter_idx = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_DEPTH(vmstate);

  VM_TRACE_KEY_LEN("ARRAY_BEGIN", array_len);
  base = arr_data;
  VM_TRACE_ELEM_IDX(0);
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_LONG();
}

vj_op_map_begin: {
  VM_TRACE_KEY("MAP_BEGIN");
  /* Map iteration is Go-driven. Yield to Go, which will:
   * - Initialize map iterator
   * - For each entry: write key to buffer, set base to value ptr,
   *   then re-enter C to execute the value body instructions
   * - Finally advance PC past MAP_END */
  VM_TRACE("YIELD(map_handoff:begin)");
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_MAP_HANDOFF);
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

vj_op_map_end: {
  VM_TRACE("MAP_END");
  /* After value encoding, yield back to Go for next entry */
  VM_TRACE("YIELD(map_handoff:end)");
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_MAP_HANDOFF);
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

/* ---- C-native Swiss Map iteration for map[string]string ---- */

vj_op_map_str_str: {
  VM_TRACE_KEY("MAP_STR_STR");

  /* Resume detection: check state bit 0 (iter active flag).
   * Set on BUF_FULL push, cleared on completion or pop. */
  int32_t _depth = VJ_ST_GET_DEPTH(vmstate);
  int is_resume = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));

  const GoSwissMap *m;
  int32_t remaining, di, gi, si;
  int entry_first;

  if (is_resume) {
    /* ---- Resume: read saved state from frame ---- */
    VjStackFrame *f = &ctx->stack[_depth - 1];
    m = (const GoSwissMap *)f->map.map_ptr;
    remaining = f->map.remaining;
    di = f->map.dir_idx;
    gi = f->map.group_idx;
    si = f->map.slot_idx;
    entry_first = 0;
  } else {
    /* ---- First entry: read map, handle nil/empty ---- */
    m = *(const GoSwissMap **)(base + op->field_off);

    if (m == NULL || m->used == 0) {
      /* nil or empty map → write key + "null" or "{}" */
      if (m == NULL) {
        VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
        VM_WRITE_KEY();
        __builtin_memcpy(buf, "null", 4);
        buf += 4;
      } else {
        VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
        VM_WRITE_KEY();
        *buf++ = '{';
        *buf++ = '}';
      }
      VJ_ST_SET_FIRST_0(vmstate);
      VM_NEXT_SHORT();
    }

    /* ---- Write comma + key + '{' + indent ---- */
    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
    VM_WRITE_KEY();
    *buf++ = '{';
    VM_INDENT_INC();
    VM_WRITE_INDENT();

    /* No frame push here — lazy: only pushed by vj_swiss_map_iterate on BUF_FULL */
    remaining = (int32_t)m->used;
    di = 0;
    gi = 0;
    si = 0;
    entry_first = 1;
  }

  /* ---- Delegate iteration to out-of-line function ---- */
  {
    if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }
    VjStackFrame *f = &ctx->stack[is_resume ? (_depth - 1) : _depth];

    VjSwissIndent ind = {
      (const uint8_t *)(indent_tpl),
      (int16_t)(indent_depth),
      (uint8_t)(indent_step),
      (uint8_t)(indent_prefix_len),
    };

    VjSwissMapResult r = vj_swiss_map_iterate(
        buf, bend, f, m, remaining, di, gi, si,
        entry_first, VJ_ST_GET_FLAGS(vmstate), &ind);
    buf = r.buf;

    if (r.action == VJ_SWISS_BUF_FULL) {
      /* Frame was written by vj_swiss_map_iterate.
       * If first entry (not resume), increment depth now. */
      VM_SAVE_TRACE_DEPTH(f);
      f->state |= 1;
      if (!is_resume) {
        VJ_ST_INC_DEPTH(vmstate);
      }
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
    }

    /* All entries encoded: write indent + '}' */
    VM_INDENT_DEC();
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
    VM_WRITE_INDENT();
    *buf++ = '}';

    if (is_resume) {
      f->state &= ~1;
      VJ_ST_DEC_DEPTH(vmstate);
      VM_RESTORE_TRACE_DEPTH(f);
    }
    VJ_ST_SET_FIRST_0(vmstate);
    VM_NEXT_SHORT();
  }
}

  /* ---- Non-primitive data ops (17-20): interface (out-of-line) ---- */

vj_op_interface: {
  VM_TRACE_KEY("INTERFACE");
  const void *type_ptr = *(const void **)(base + op->field_off);

  /* nil interface → "null" (trivial — stays inline) */
  if (type_ptr == NULL) {
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_NEXT_SHORT();
  }

  /* Non-nil: speculatively write key, then delegate to out-of-line handler.
   * Save buf/vmstate so we can undo the key write on yield/miss paths
   * (Go handles its own key+comma when falling back). */
  VM_CHECK(op->key_len + 1 + 330 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  uint8_t *iface_saved_buf = buf;
  uint64_t iface_saved_vmstate = vmstate;
  VM_WRITE_KEY();

  VjIfaceResult iface_r = vj_encode_interface_value(
      buf, bend, base + op->field_off, ctx->iface_cache_ptr,
      ctx->iface_cache_count, VJ_ST_GET_FLAGS(vmstate));

  switch (__builtin_expect(iface_r.action, VJ_IFACE_DONE)) {
  case VJ_IFACE_DONE:
    buf = iface_r.buf;
    VM_NEXT_SHORT();
  case VJ_IFACE_YIELD:
    /* Undo key write — Go fallback handles key+comma itself */
    buf = iface_saved_buf;
    vmstate = iface_saved_vmstate;
    goto vj_op_yield;
  case VJ_IFACE_CACHE_MISS:
    /* Undo key write — Go will re-encode after compilation */
    buf = iface_saved_buf;
    vmstate = iface_saved_vmstate;
    ctx->yield_type_ptr = iface_r.type_ptr;
    VM_TRACE("YIELD(iface_miss)");
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_IFACE_MISS);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  case VJ_IFACE_SWITCH_OPS: {
    /* Cached Blueprint found — push a CALL frame and switch to the
     * child Blueprint's ops.  On vj_op_ret the call frame pop
     * restores parent ops/pc/base automatically.
     *
     * Go-side yield handlers use activeBlueprint(ctx, rootBP) to
     * resolve the correct Blueprint via ctx->ops_ptr, so yields
     * inside the child ops (fallback, map, buf_full) are handled
     * correctly with the child's Fallbacks table. */
    if (__builtin_expect(VJ_ST_GET_DEPTH(vmstate) >= VJ_MAX_DEPTH, 0)) {
      buf = iface_saved_buf;
      vmstate = iface_saved_vmstate;
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }

    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
    frame->call.ret_ops = ops;
    frame->call.ret_pc = (int32_t)((const uint8_t *)op - ops) + 8; /* INTERFACE is always 8 bytes */
    frame->ret_base = base;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_DEPTH(vmstate);

    /* Switch to child Blueprint's ops.
     * The speculative key write stays — child ops produce the value. */
    ops = iface_r.cached_ops;
    op = (const VjOpHdr *)ops;
    base = iface_r.data_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_TRACE("IFACE_SWITCH_OPS");
    VM_DISPATCH();
  }
  case VJ_IFACE_BUF_FULL:
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  case VJ_IFACE_NAN_INF:
    /* Undo key write on error — the error will be reported to Go */
    buf = iface_saved_buf;
    vmstate = iface_saved_vmstate;
    VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
  default:
    buf = iface_saved_buf;
    vmstate = iface_saved_vmstate;
    goto vj_op_yield;
  }
}

  /* ---- End / Yield ---- */

vj_op_ret: {
  if (VJ_ST_GET_DEPTH(vmstate) > 0) {
    /* Subroutine return: pop CALL frame, restore ops/pc/base. */
    VJ_ST_DEC_DEPTH(vmstate);
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_DEPTH(vmstate)];
    VM_RESTORE_TRACE_DEPTH(frame);
    VM_TRACE("RET");
    ops = frame->call.ret_ops;
    op = (const VjOpHdr *)(ops + frame->call.ret_pc);
    base = frame->ret_base;
    VJ_ST_SET_FIRST_0(vmstate);
    VM_DISPATCH();
  }

  /* Top-level done */
  VM_TRACE("HALT");
  ctx->buf_cur = buf;
  VJ_ST_SET_EXIT(vmstate, VJ_EXIT_OK);
  ctx->vmstate = vmstate;
  return;
}

vj_op_halt: {
  if (__builtin_expect(op->op_type != OP_HALT, 0)) {
    goto vj_op_yield;
  }

  /* Top-level done (explicit halt sentinel) */
  VM_TRACE("HALT");
  ctx->buf_cur = buf;
  VJ_ST_SET_EXIT(vmstate, VJ_EXIT_OK);
  ctx->vmstate = vmstate;
  return;
}

vj_op_yield: {
  VM_TRACE_YIELD(op->op_type);
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
  /* The 'first' flag is preserved in vmstate — Go reads it directly. */
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

/* ---- Cleanup macros ---- */
#undef VM_CHECK
#undef VM_KEY_SPACE
#undef VM_WRITE_KEY
#undef VM_WRITE_INDENT
#undef VM_INDENT_PAD
#undef VM_DISPATCH
#undef VM_NEXT_SHORT
#undef VM_NEXT_LONG
#undef VM_JUMP_BYTES
#undef VM_INDENT_INC
#undef VM_INDENT_DEC
#undef VM_SAVE_INDENT_DEPTH
#undef VM_DEPTH
#undef VM_SAVE_TRACE_DEPTH
#undef VM_RESTORE_TRACE_DEPTH
#ifdef VJ_COMPACT_INDENT
#undef indent_tpl
#undef indent_depth
#undef indent_step
#undef indent_prefix_len
#endif
}

#undef VM_SAVE_AND_RETURN

#endif /* VJ_ENCVM_H */
