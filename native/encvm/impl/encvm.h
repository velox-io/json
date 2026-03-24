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
#include "eface.h"
#include "trace.h"
#include "swissmap.h"
#include "seqiter.h"
#include "base64.h"
#include "timefmt.h"

/* ================================================================
 *  VM Implementation — threaded-code interpreter for variable-length ops
 *
 *  Each handler uses VM_NEXT_SHORT() or VM_NEXT_LONG() — the instruction
 *  width is known at compile time (no runtime size decode).
 * ================================================================ */

/* Save VM state to context and return.
 * Writes the packed vmstate (with exit code set) back to ctx.
 * Go reads exit/yield/depth/first directly from vmstate. */
#define VM_SAVE_AND_RETURN(exit_code)                                           \
  do {                                                                          \
    VM_TRACE_MSG("◀ exit");                                                     \
    ctx->buf_cur = buf;                                                         \
    ctx->ops_ptr = ops;                                                         \
    ctx->pc = (int32_t)((const uint8_t *)op - ops);                             \
    ctx->cur_base = base;                                                       \
    VM_SAVE_INDENT_DEPTH();                                                     \
    VM_SAVE_TRACE_DEPTH_CTX();                                                  \
    VJ_ST_SET_EXIT(vmstate, exit_code);                                         \
    ctx->vmstate = vmstate;                                                     \
    return;                                                                     \
  } while (0)

/* VJ_VM_EXEC_FN_NAME must be defined by the including .c file before
 * #include "encvm.h".  It expands to the suffixed entry-point symbol
 * name (e.g. vj_vm_exec_full_sse42).  This eliminates the wrapper
 * function and avoids an extra jmp/call into the VM body. */
#ifndef VJ_VM_EXEC_FN_NAME
#error "VJ_VM_EXEC_FN_NAME must be defined before including encvm.h"
#endif

/* On Windows, __declspec(dllexport) is needed so that the entry function
 * appears in the PE export directory when linking as a DLL.  */
#ifdef _WIN32
#define VJ_EXPORT __declspec(dllexport)
#else
#define VJ_EXPORT
#endif

VJ_EXPORT VJ_ALIGN_STACK void VJ_VM_EXEC_FN_NAME(VjExecCtx *ctx) {

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

/* Trace-only depth: VM stack depth + OBJ_OPEN/CLOSE count for debug output indentation.
 * NOT used for JSON indent — that uses indent_depth separately. */
#ifdef VJ_ENCVM_DEBUG
  /* trace_obj_depth tracks OBJ_OPEN/CLOSE nesting for trace indentation only.
   * Persisted in ctx->trace_depth across yields (replaces old indent_depth derivation). */
  int trace_obj_depth = ctx->trace_depth;
  VjTraceBuf *tbuf = ctx->trace_buf;
  #define VM_TRACE_DEPTH() (VJ_ST_GET_STACK_DEPTH(vmstate) + trace_obj_depth)
  /* Save/restore trace depth in state bits [24..31] across push/pop.
   * Bit 0 of state is the iter-active flag (used by C-native loops). */
  #define VM_SAVE_TRACE_DEPTH(frame) \
    ((frame)->state = ((frame)->state & 0xFF) | (trace_obj_depth << 24))
  #define VM_RESTORE_TRACE_DEPTH(frame) \
    (trace_obj_depth = (int32_t)((frame)->state >> 24))
  /* Save trace_obj_depth back to ctx on VM exit. */
  #define VM_SAVE_TRACE_DEPTH_CTX() (ctx->trace_depth = trace_obj_depth)
  VM_TRACE_MSG("▶ enter");
#else
  #define VM_TRACE_DEPTH() (VJ_ST_GET_STACK_DEPTH(vmstate))
  #define VM_SAVE_TRACE_DEPTH(frame) ((void)0)
  #define VM_RESTORE_TRACE_DEPTH(frame) ((void)0)
  #define VM_SAVE_TRACE_DEPTH_CTX() ((void)0)
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

      /* Non-primitive data ops (15-18) */
      [OP_INTERFACE] = DT_ENTRY(vj_op_interface),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER] = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE] = DT_ENTRY(vj_op_byte_slice),

      /* Structural control-flow (19-31) */
      [OP_SKIP_IF_ZERO] = DT_ENTRY(vj_op_skip_if_zero),
      [OP_CALL] = DT_ENTRY(vj_op_call),
      [OP_PTR_DEREF] = DT_ENTRY(vj_op_ptr_deref),
      [OP_PTR_END] = DT_ENTRY(vj_op_ptr_end),
      [OP_SLICE_BEGIN] = DT_ENTRY(vj_op_slice_begin),
      [OP_SLICE_END] = DT_ENTRY(vj_op_slice_end),
      [OP_MAP] = DT_ENTRY(vj_op_map),
      /* 26: reserved (was OP_MAP_END) */
      [OP_OBJ_OPEN] = DT_ENTRY(vj_op_obj_open),
      [OP_OBJ_CLOSE] = DT_ENTRY(vj_op_obj_close),
      [OP_ARRAY_BEGIN] = DT_ENTRY(vj_op_array_begin),
      [OP_MAP_STR_STR] = DT_ENTRY(vj_op_map_str_str),
      [OP_RET] = DT_ENTRY(vj_op_ret),

      /* Go-only fallback (32) */
      [OP_FALLBACK] = DT_ENTRY(vj_op_yield),

      /* Keyed-field variants (33-35) */
      [OP_KSTRING] = DT_ENTRY(vj_op_kstring),
      [OP_KINT]    = DT_ENTRY(vj_op_kint),
      [OP_KINT64]  = DT_ENTRY(vj_op_kint64),

      /* C-native Swiss Map variants (36-37) */
      [OP_MAP_STR_INT]   = DT_ENTRY(vj_op_map_str_int),
      [OP_MAP_STR_INT64] = DT_ENTRY(vj_op_map_str_int64),

      /* C-native sequence iterators (38-41) */
      [OP_SEQ_FLOAT64] = DT_ENTRY(vj_op_seq_float64),
      [OP_SEQ_INT]     = DT_ENTRY(vj_op_seq_int),
      [OP_SEQ_INT64]   = DT_ENTRY(vj_op_seq_int64),
      [OP_SEQ_STRING]  = DT_ENTRY(vj_op_seq_string),

      /* C-native Swiss Map key iterator (42-43) */
      [OP_MAP_STR_ITER]     = DT_ENTRY(vj_op_map_str_iter),
      [OP_MAP_STR_ITER_END] = DT_ENTRY(vj_op_map_str_iter_end),

      /* Keyed-field quoted variants (44-45) — ,string tag */
      [OP_KQINT]   = DT_ENTRY(vj_op_kqint),
      [OP_KQINT64] = DT_ENTRY(vj_op_kqint64),

      /* time.Time (46) — native RFC3339Nano */
      [OP_TIME]    = DT_ENTRY(vj_op_time),
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
#define VM_CHECK(n)                                                             \
  do {                                                                          \
    if (__builtin_expect(buf + (n) > bend, 0)) {                                \
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);                                     \
    }                                                                           \
  } while (0)

/* ---- Indent helpers ---- */

/* Max indent bytes for VM_CHECK: '\n' + prefix + indent_depth * indent_step.
 * Returns 0 in compact mode (indent_step == 0). */
#define VM_INDENT_PAD(idepth) (indent_step ? (1 + indent_prefix_len + (idepth) * indent_step) : 0)

/* Write indent: '\n' + prefix + indent for current indent_depth.
 * No-op in compact mode. */
#define VM_WRITE_INDENT()                                                       \
  do {                                                                          \
    if (indent_step) {                                                          \
      int _n = 1 + indent_prefix_len + indent_depth * indent_step;              \
      __builtin_memcpy(buf, indent_tpl, _n);                                    \
      buf += _n;                                                                \
    }                                                                           \
  } while (0)

/* ---- Write pre-encoded key with comma prefix ---- */
#define VM_WRITE_KEY()                                                          \
  do {                                                                          \
    VM_PIN_VMSTATE();                                                           \
    int _was_first;                                                             \
    VJ_ST_BTR_FIRST(vmstate, _was_first);                                       \
    if (indent_step) {                                                          \
      /* Indent mode: branch needed for newline + prefix + indent. */           \
      if (!_was_first) {                                                        \
        *buf++ = ',';                                                           \
        VM_WRITE_INDENT();                                                      \
      }                                                                         \
    } else {                                                                    \
      /* Compact mode: branchless comma.  Always write ',', then                \
       * advance by (1 - was_first).  First element: key overwrites             \
       * the comma; subsequent elements: buf skips past it. */                  \
      *buf = ',';                                                               \
      buf += 1 - _was_first;                                                    \
    }                                                                           \
    if (op->key_len > 0) {                                                      \
      vj_copy_key(buf, (const char *)(key_pool + op->key_off), op->key_len);    \
      buf += op->key_len;                                                       \
      if (indent_step) { *buf++ = ' '; }                                        \
    }                                                                           \
  } while (0)

/* ---- Write pre-encoded key unconditionally (keyed-field variants) ----
 * Same as VM_WRITE_KEY() but without the if (op->key_len > 0) branch.
 * Used by OP_KSTRING/OP_KINT/OP_KINT64 where key is always present. */
#define VM_WRITE_KEY_ALWAYS()                                                   \
  do {                                                                          \
    VM_PIN_VMSTATE();                                                           \
    int _was_first;                                                             \
    VJ_ST_BTR_FIRST(vmstate, _was_first);                                       \
    if (indent_step) {                                                          \
      if (!_was_first) {                                                        \
        *buf++ = ',';                                                           \
        VM_WRITE_INDENT();                                                      \
      }                                                                         \
    } else {                                                                    \
      *buf = ',';                                                               \
      buf += 1 - _was_first;                                                    \
    }                                                                           \
    vj_copy_key(buf, (const char *)(key_pool + op->key_off), op->key_len);      \
    buf += op->key_len;                                                         \
    if (indent_step) { *buf++ = ' '; }                                          \
  } while (0)

/* ---- Dispatch macro (ADR/LEA trick for PIC computed goto) ----
 *
 * Anti-tail-merge: The "r"(op) input operand forces the compiler to
 * treat each expansion as depending on a unique op value, preventing
 * clang -O3 from merging identical dispatch tails across handlers.
 * This keeps each handler's indirect jump at a distinct address for
 * optimal CPU indirect branch prediction (BTB per-site history). */
#if defined(__aarch64__)
#define VM_DISPATCH()                                                           \
  do {                                                                          \
    uint16_t i = op->op_type;                                                   \
    char *_base;                                                                \
    __asm__ volatile("adr %0, %c1"                                              \
                     : "=r"(_base)                                              \
                     : "i"(&&vj_dispatch_base), "r"(op));                       \
    goto *(void *)(_base + dispatch_table[i]);                                  \
  } while (0)
#elif defined(__x86_64__)
#define VM_DISPATCH()                                                           \
  do {                                                                          \
    uint16_t i = op->op_type;                                                   \
    char *_base;                                                                \
    __asm__ volatile("lea %c1(%%rip), %0"                                       \
                     : "=r"(_base)                                              \
                     : "i"(&&vj_dispatch_base), "r"(op));                       \
    goto *(void *)(_base + dispatch_table[i]);                                  \
  } while (0)
#else
#error "VM_DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Static-width advance macros — each handler knows its own instruction
 * size at compile time, so no runtime size decode is needed.            */
#define VM_NEXT_SHORT()                                                         \
  do {                                                                          \
    op = (const VjOpHdr *)((const uint8_t *)op + 8);                            \
    VM_DISPATCH();                                                              \
  } while (0)
#define VM_NEXT_LONG()                                                          \
  do {                                                                          \
    op = (const VjOpHdr *)((const uint8_t *)op + 16);                           \
    VM_DISPATCH();                                                              \
  } while (0)
#define VM_JUMP_BYTES(byte_offset)                                              \
  do {                                                                          \
    op = (const VjOpHdr *)((const uint8_t *)op + (byte_offset));                \
    VM_DISPATCH();                                                              \
  } while (0)

  VM_DISPATCH();

vj_dispatch_base:
  __builtin_unreachable();

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
  trace_obj_depth++;
#endif
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_SHORT();
}

vj_op_slice_begin: {
  const GoSlice *sl = (const GoSlice *)(base + op->field_off);
  const VjOpExt *ext = VJ_OP_EXT(op);
  /* SLICE_BEGIN operands:
   *   operand_a = elem_size
   *   operand_b = body byte length (excluding SLICE_END)
   * This lets nil/empty slices jump over the whole loop body in one step. */

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
  if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->ret_base = base;
  frame->seq.iter_data = sl->data;
  frame->seq.iter_count = sl->len;
  frame->seq.iter_idx = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  base = sl->data; /* base = &elem[0] */
  VM_TRACE_ELEM_IDX(0);
  VJ_ST_SET_FIRST_1(vmstate); /* first element has no comma */
  VM_NEXT_LONG();
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

vj_op_int: {
  VM_TRACE_KEY("INT");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
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

/* --- Keyed-field variants: unconditional key write (no key_len branch) --- */

vj_op_kstring: {
  VM_TRACE_KEY("KSTRING");
  const GoString *s = (const GoString *)(base + op->field_off);
  int64_t max_need = 1 + op->key_len + 2 + (s->len * 6) + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE;
  VM_CHECK(max_need);
  VM_WRITE_KEY_ALWAYS();
#ifdef VJ_FAST_STRING_ESCAPE
  buf += vj_escape_string_fast(buf, (const uint8_t *)s->ptr, s->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
#endif
  VM_NEXT_SHORT();
}

vj_op_kint: {
  VM_TRACE_KEY("KINT");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY_ALWAYS();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT_SHORT();
}

vj_op_kint64: {
  VM_TRACE_KEY("KINT64");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY_ALWAYS();
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  VM_NEXT_SHORT();
}

vj_op_kqint: {
  VM_TRACE_KEY("KQINT");
  VM_CHECK(op->key_len + 1 + 21 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY_ALWAYS();
  *buf++ = '"';
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  *buf++ = '"';
  VM_NEXT_SHORT();
}

vj_op_kqint64: {
  VM_TRACE_KEY("KQINT64");
  VM_CHECK(op->key_len + 1 + 21 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY_ALWAYS();
  *buf++ = '"';
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  *buf++ = '"';
  VM_NEXT_SHORT();
}

vj_op_time: {
  /* time.Time — native RFC3339Nano formatting.
   * Works in any context: struct field (with key), slice/array element (no key),
   * pointer deref body, etc.
   * Must check yield-eligibility BEFORE writing the key, because
   * once the key is written there's no way to undo it on yield. */
  const GoTime *t = (const GoTime *)(base + op->field_off);

  /* Check if we can handle this timezone natively */
  if (!vj_time_can_native(t->loc)) {
    /* Complex timezone (DST) — yield to Go like OP_FALLBACK */
    VM_TRACE_YIELD(op->op_type);
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  }

  int32_t tz_offset = (t->loc == NULL) ? 0 : vj_time_get_offset(t->loc);

  /* Dry-run to get the year for range check */
  int year;
  {
    int64_t isec;
    int32_t nsec_tmp;
    vj_time_extract(t, &isec, &nsec_tmp);
    int64_t unix_sec = isec - VJ_TIME_UNIX_TO_INTERNAL + (int64_t)tz_offset;
    uint64_t abs = (uint64_t)(unix_sec + VJ_TIME_UNIX_TO_ABS);
    uint64_t days = abs / VJ_SECONDS_PER_DAY;
    uint64_t d4 = 4 * days + 3;
    uint64_t century = d4 / 146097;
    uint32_t cd = (uint32_t)(d4 % 146097) | 3;
    uint64_t mul = (uint64_t)2939745 * (uint64_t)cd;
    uint32_t cyear = (uint32_t)(mul >> 32);
    uint32_t ayday = (uint32_t)((uint32_t)mul / 2939745 / 4);
    uint32_t janFeb = (ayday >= VJ_TIME_MARCH_THRU_DEC) ? 1 : 0;
    year = (int)(century * 100 - VJ_TIME_ABSOLUTE_YEARS) + (int)cyear + (int)janFeb;
  }

  if (year < 0 || year > 9999) {
    /* Out of range — yield to Go (let Go report the error) */
    VM_TRACE_YIELD(op->op_type);
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  }

  VM_TRACE_KEY("TIME");
  /* Max output: comma(1) + key + key_space(1) + quote(1) + "2006-01-02T15:04:05.999999999+00:00"(35) + quote(1) + indent */
  VM_CHECK(op->key_len + 1 + 37 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += vj_write_rfc3339nano(buf, t, tz_offset, &year);
  VM_NEXT_SHORT();
}


vj_op_obj_close: {
#ifdef VJ_ENCVM_DEBUG
  if (trace_obj_depth > 0) trace_obj_depth--;
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

vj_op_skip_if_zero: {
  VM_TRACE("SKIP_IF_ZERO");
  const VjOpExt *ext = VJ_OP_EXT(op);
  uint16_t check_type = (uint16_t)ext->operand_b;
  if (vj_is_zero(base + op->field_off, check_type)) {
    VM_JUMP_BYTES(ext->operand_a); /* byte offset from op start to target */
  }
  VM_NEXT_LONG();
}

/* --- slice/array loop body --- */

vj_op_slice_end: {
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  frame->seq.iter_idx++;
  const VjOpExt *ext = VJ_OP_EXT(op);

  if (frame->seq.iter_idx < frame->seq.iter_count) {
    /* More elements: write comma + indent, advance base, jump back.
     * SLICE_END intentionally flips the operand layout used by SLICE_BEGIN:
     *   operand_a = relative jump back to body start
     *   operand_b = elem_size
     * so the hot loop can use operand_b directly for base advance. */
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
    *buf++ = ',';
    VM_WRITE_INDENT();
    base = frame->seq.iter_data + (int64_t)frame->seq.iter_idx * ext->operand_b;
    op = (const VjOpHdr *)((const uint8_t *)op + ext->operand_a); /* relative jump back to body start */
    VM_TRACE_ELEM_IDX(frame->seq.iter_idx);
    VJ_ST_SET_FIRST_1(vmstate); /* reset for element-level encoding (no struct comma) */
    VM_DISPATCH();
  }

  /* Done: write indent + ']', pop frame */
  VM_INDENT_DEC();
  VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
  VM_WRITE_INDENT();
  *buf++ = ']';
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("SLICE_END");
  base = frame->ret_base;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this field */
  VM_NEXT_LONG();
}

  /* -- other primitives + pointer deref */

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

  /* Push ptr frame — only ret_base is needed.
   * PTR_END restores base from ret_base, sets first=0 in vmstate,
   * and advances with VM_NEXT_SHORT(), so no ret_ops/ret_pc state is stored. */
  if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)].ret_base = base;
  VM_SAVE_TRACE_DEPTH(&ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)]);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  base = (const uint8_t *)ptr;
  VJ_ST_SET_FIRST_1(vmstate); /* deref body is a "value" context — no leading comma */
  VM_NEXT_LONG();
}

vj_op_ptr_end: {
  /* Pop the ptr-deref frame, restore parent base */
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("PTR_END");
  base = frame->ret_base;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this ptr field */
  VM_NEXT_SHORT();
}

/* --- call/ret, raw_message, number, byte_slice, array --- */

vj_op_call: {
  VM_TRACE("CALL");
  if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->call.ret_ops = ops;
  frame->call.ret_pc = (int32_t)((const uint8_t *)op - ops) + 16; /* CALL is always 16 bytes */
  frame->ret_base = base;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  const VjOpExt *ext = VJ_OP_EXT(op);
  base = base + op->field_off;
  op = (const VjOpHdr *)(ops + ext->operand_a);
  VJ_ST_SET_FIRST_1(vmstate);
  VM_DISPATCH();
}

vj_op_ret: {
  if (VJ_ST_GET_STACK_DEPTH(vmstate) > 0) {
    /* Subroutine return: pop CALL frame, restore ops/pc/base. */
    VM_TRACE("RET");
    VJ_ST_DEC_STACK_DEPTH(vmstate);
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    VM_RESTORE_TRACE_DEPTH(frame);
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

vj_op_byte_slice: {
  VM_TRACE_KEY("BYTE_SLICE");
  const GoSlice *sl = (const GoSlice *)(base + op->field_off);

  if (sl->data == NULL) {
    /* nil → "null" */
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_NEXT_SHORT();
  }

  /* Non-nil: base64 encode into quoted string.
   * Empty slice → '""' (matching encoding/json behavior). */
  if (sl->len == 0) {
    VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    *buf++ = '"';
    *buf++ = '"';
    VM_NEXT_SHORT();
  }

  /* Worst-case: key + comma + indent + '"' + ceil(len/3)*4 + '"' */
  VM_CHECK(op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  uint8_t *result = vj_encode_base64(buf, bend, sl->data, sl->len);
  if (__builtin_expect(result == NULL, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  }
  buf = result;
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

  if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->ret_base = base;
  frame->seq.iter_data = arr_data;
  frame->seq.iter_count = array_len;
  frame->seq.iter_idx = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  VM_TRACE_KEY_LEN("ARRAY_BEGIN", array_len);
  base = arr_data;
  VM_TRACE_ELEM_IDX(0);
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_LONG();
}

/* --- COLD: map / interface / yield --- */

vj_op_map: {
  VM_TRACE_KEY("MAP");
  /* Yield to Go for full map encoding. */
  VM_TRACE("YIELD(map_handoff)");
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_MAP_HANDOFF);
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

vj_op_map_str_str: {
  VM_TRACE_KEY("MAP_STR_STR");

  /* Resume detection: check state bit 0 (iter active flag).
   * Set on BUF_FULL push, cleared on completion or pop. */
  int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);
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
    if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }
    VjStackFrame *f = &ctx->stack[is_resume ? (_depth - 1) : _depth];

    VjSwissIndent ind = {
      (const uint8_t *)(indent_tpl),
      (int16_t)(indent_depth),
      (uint8_t)(indent_step),
      (uint8_t)(indent_prefix_len),
    };

    VjSwissMapResult r = vj_swiss_iterate_str_str(
        buf, bend, f, m, remaining, di, gi, si,
        entry_first, VJ_ST_GET_FLAGS(vmstate), &ind);
    buf = r.buf;

    if (r.action == VJ_SWISS_BUF_FULL) {
      /* Frame was written by vj_swiss_iterate_str_str.
       * If first entry (not resume), increment depth now. */
      VM_SAVE_TRACE_DEPTH(f);
      f->state |= 1;
      if (!is_resume) {
        VJ_ST_INC_STACK_DEPTH(vmstate);
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
      VJ_ST_DEC_STACK_DEPTH(vmstate);
      VM_RESTORE_TRACE_DEPTH(f);
    }
    VJ_ST_SET_FIRST_0(vmstate);
    VM_NEXT_SHORT();
  }
}

/* ================================================================
 *  Macro-generated Swiss Map handlers for map[string]int variants.
 *
 *  The handler logic is identical to vj_op_map_str_str except for
 *  the iterate function called and the trace label.
 * ================================================================ */

#define VJ_DEFINE_MAP_SWISS_OP(OP_LABEL, TRACE_LABEL, ITERATE_FN)               \
OP_LABEL: {                                                                     \
  VM_TRACE_KEY(TRACE_LABEL);                                                    \
  int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);                              \
  int is_resume = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));           \
  const GoSwissMap *m;                                                          \
  int32_t remaining, di, gi, si;                                                \
  int entry_first;                                                              \
  if (is_resume) {                                                              \
    VjStackFrame *f = &ctx->stack[_depth - 1];                                  \
    m = (const GoSwissMap *)f->map.map_ptr;                                     \
    remaining = f->map.remaining;                                               \
    di = f->map.dir_idx;                                                        \
    gi = f->map.group_idx;                                                      \
    si = f->map.slot_idx;                                                       \
    entry_first = 0;                                                            \
  } else {                                                                      \
    m = *(const GoSwissMap **)(base + op->field_off);                           \
    if (m == NULL || m->used == 0) {                                            \
      if (m == NULL) {                                                          \
        VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth)              \
                 + VM_KEY_SPACE);                                               \
        VM_WRITE_KEY();                                                         \
        __builtin_memcpy(buf, "null", 4);                                       \
        buf += 4;                                                               \
      } else {                                                                  \
        VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth)              \
                 + VM_KEY_SPACE);                                               \
        VM_WRITE_KEY();                                                         \
        *buf++ = '{';                                                           \
        *buf++ = '}';                                                           \
      }                                                                         \
      VJ_ST_SET_FIRST_0(vmstate);                                               \
      VM_NEXT_SHORT();                                                          \
    }                                                                           \
    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth)                  \
             + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));                 \
    VM_WRITE_KEY();                                                             \
    *buf++ = '{';                                                               \
    VM_INDENT_INC();                                                            \
    VM_WRITE_INDENT();                                                          \
    remaining = (int32_t)m->used;                                               \
    di = 0; gi = 0; si = 0;                                                     \
    entry_first = 1;                                                            \
  }                                                                             \
  {                                                                             \
    if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate)                         \
                         >= VJ_MAX_STACK_DEPTH, 0)) {                           \
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);                               \
    }                                                                           \
    VjStackFrame *f = &ctx->stack[is_resume ? (_depth - 1) : _depth];           \
    VjSwissIndent ind = {                                                       \
      (const uint8_t *)(indent_tpl),                                            \
      (int16_t)(indent_depth),                                                  \
      (uint8_t)(indent_step),                                                   \
      (uint8_t)(indent_prefix_len),                                             \
    };                                                                          \
    VjSwissMapResult r = ITERATE_FN(                                            \
        buf, bend, f, m, remaining, di, gi, si,                                 \
        entry_first, VJ_ST_GET_FLAGS(vmstate), &ind);                           \
    buf = r.buf;                                                                \
    if (r.action == VJ_SWISS_BUF_FULL) {                                        \
      VM_SAVE_TRACE_DEPTH(f);                                                   \
      f->state |= 1;                                                            \
      if (!is_resume) { VJ_ST_INC_STACK_DEPTH(vmstate); }                       \
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);                                     \
    }                                                                           \
    VM_INDENT_DEC();                                                            \
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));                                  \
    VM_WRITE_INDENT();                                                          \
    *buf++ = '}';                                                               \
    if (is_resume) {                                                            \
      f->state &= ~1;                                                           \
      VJ_ST_DEC_STACK_DEPTH(vmstate);                                           \
      VM_RESTORE_TRACE_DEPTH(f);                                                \
    }                                                                           \
    VJ_ST_SET_FIRST_0(vmstate);                                                 \
    VM_NEXT_SHORT();                                                            \
  }                                                                             \
}

VJ_DEFINE_MAP_SWISS_OP(vj_op_map_str_int, "MAP_STR_INT", vj_swiss_iterate_str_int)
VJ_DEFINE_MAP_SWISS_OP(vj_op_map_str_int64, "MAP_STR_INT64", vj_swiss_iterate_str_int)

#undef VJ_DEFINE_MAP_SWISS_OP

/* ================================================================
 *  Swiss Map Key Iterator: MAP_STR_ITER / MAP_STR_ITER_END
 *
 *  Generic map[string]<value> encoding using C-native key iteration
 *  with VM-dispatched value body instructions.
 *
 *  MAP_STR_ITER (long, 16 bytes):
 *    field_off: map pointer offset in struct
 *    key_len/key_off: struct field key (if any)
 *    operand_a: slot_size (from Go compiler)
 *    operand_b: body byte length (excl MAP_STR_ITER_END)
 *
 *  MAP_STR_ITER_END (long, 16 bytes):
 *    operand_a: relative jump back offset (negative, to body start)
 *    operand_b: slot_size (redundant, for back-edge use)
 * ================================================================ */

vj_op_map_str_iter: {
  const VjOpExt *ext = VJ_OP_EXT(op);
  int32_t slot_size = ext->operand_a;
  int32_t body_len = ext->operand_b;

  /* Resume detection: check state bit 0 on top frame. */
  int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);
  int is_resume = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));

  if (is_resume) {
    /* ---- Resume after BUF_FULL ---- */
    VjStackFrame *f = &ctx->stack[_depth - 1];
    const GoSwissMap *m = (const GoSwissMap *)f->map.map_ptr;
    int32_t di = f->map.dir_idx;
    int32_t gi = f->map.group_idx;
    int32_t si = f->map.slot_idx;
    int32_t remaining = f->map.remaining;

    /* Find current slot (we saved position before the slot that needs encoding). */
    const uint8_t *slot = vj_swiss_next_full_slot(m, slot_size, &di, &gi, &si);
    if (slot == NULL || remaining <= 0) {
      /* Shouldn't happen on resume, but handle gracefully: end iteration */
      goto map_str_iter_done_resume;
    }

    const GoString *k = (const GoString *)slot;
    const uint8_t *val_ptr = slot + 16; /* elem_off = 16 for all string-key maps */

    /* Write comma + indent + key */
    {
      int ipad = indent_step
        ? (1 + indent_prefix_len + indent_depth * indent_step)
        : 0;
      int key_space = indent_step ? 1 : 0;
      int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1;
      VM_CHECK(need);
    }
    *buf++ = ',';
    if (indent_step) { VM_WRITE_INDENT(); }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += vj_escape_string_fast(buf, (const uint8_t *)k->ptr, k->len);
#else
    buf += vj_escape_string(buf, (const uint8_t *)k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) { *buf++ = ' '; }

    /* Update frame for next iteration */
    si++; /* advance past current slot */
    f->map.dir_idx = di;
    f->map.group_idx = (uint8_t)gi;
    f->map.slot_idx = (uint8_t)si;
    f->map.remaining = remaining - 1;

    /* Set base to value ptr, first=1 for body encoding */
    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_TRACE_KEY("MAP_STR_ITER(resume)");
    VM_NEXT_LONG(); /* enter body */
  }

  /* ---- First entry: read map, handle nil/empty ---- */
  {
    const GoSwissMap *m = *(const GoSwissMap **)(base + op->field_off);

    if (m == NULL || m->used == 0) {
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
      /* Skip body + MAP_STR_ITER_END: self(16) + body + ITER_END(16) */
      VM_JUMP_BYTES(16 + body_len + 16);
    }

    /* ---- Write comma + key + '{' + indent ---- */
    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE
             + VM_INDENT_PAD(indent_depth + 1));
    VM_WRITE_KEY();
    VM_TRACE_KEY("MAP_STR_ITER");
    *buf++ = '{';
    VM_INDENT_INC();
    VM_WRITE_INDENT();

    /* Push map frame */
    if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    frame->ret_base = base;
    frame->map.map_ptr = m;
    frame->map.remaining = (int32_t)m->used;
    frame->map.dir_idx = 0;
    frame->map.group_idx = 0;
    frame->map.slot_idx = 0;
    frame->state = 0;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);

    /* Find first full slot */
    int32_t di = 0, gi = 0, si = 0;
    const uint8_t *slot = vj_swiss_next_full_slot(m, slot_size, &di, &gi, &si);
    /* slot must be non-NULL since m->used > 0 */

    const GoString *k = (const GoString *)slot;
    const uint8_t *val_ptr = slot + 16;

    /* Write first key (no comma) */
    {
      int key_space = indent_step ? 1 : 0;
      int64_t need = 2 + (k->len * 6) + 1 + key_space;
      VM_CHECK(need);
    }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += vj_escape_string_fast(buf, (const uint8_t *)k->ptr, k->len);
#else
    buf += vj_escape_string(buf, (const uint8_t *)k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) { *buf++ = ' '; }

    /* Save position for next iteration (advance past current slot) */
    si++;
    frame->map.dir_idx = di;
    frame->map.group_idx = (uint8_t)gi;
    frame->map.slot_idx = (uint8_t)si;
    frame->map.remaining = (int32_t)m->used - 1;

    /* Set base to value ptr, first=1 for body encoding */
    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_NEXT_LONG(); /* enter body */
  }

map_str_iter_done_resume: {
    /* Reached from resume path when no more entries */
    VjStackFrame *f = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
    VM_INDENT_DEC();
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
    VM_WRITE_INDENT();
    *buf++ = '}';
    f->state &= ~1;
    VJ_ST_DEC_STACK_DEPTH(vmstate);
    base = f->ret_base;
    VM_RESTORE_TRACE_DEPTH(f);
    VJ_ST_SET_FIRST_0(vmstate);
    /* Skip body + MAP_STR_ITER_END from current position */
    VM_JUMP_BYTES(16 + body_len + 16);
  }
}

vj_op_map_str_iter_end: {
  const VjOpExt *ext = VJ_OP_EXT(op);
  int32_t slot_size = ext->operand_b;

  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  int32_t remaining = frame->map.remaining;

  if (remaining > 0) {
    /* More entries: find next full slot */
    const GoSwissMap *m = (const GoSwissMap *)frame->map.map_ptr;
    int32_t di = frame->map.dir_idx;
    int32_t gi = frame->map.group_idx;
    int32_t si = frame->map.slot_idx;

    const uint8_t *slot = vj_swiss_next_full_slot(m, slot_size, &di, &gi, &si);
    if (slot == NULL) {
      /* Shouldn't happen (remaining > 0), but handle gracefully */
      goto map_str_iter_end_done;
    }

    const GoString *k = (const GoString *)slot;
    const uint8_t *val_ptr = slot + 16;

    /* Check buffer space for comma + indent + key + ':' + space */
    {
      int ipad = indent_step
        ? (1 + indent_prefix_len + indent_depth * indent_step)
        : 0;
      int key_space = indent_step ? 1 : 0;
      int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1;
      if (__builtin_expect(buf + need > bend, 0)) {
        /* Save position for resume — frame already has map state.
         * Update position to current slot. */
        frame->map.dir_idx = di;
        frame->map.group_idx = (uint8_t)gi;
        frame->map.slot_idx = (uint8_t)si;
        /* Don't decrement remaining yet — will be decremented on resume */
        frame->state |= 1;
        VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
      }
    }

    /* Write comma + indent + key */
    *buf++ = ',';
    if (indent_step) { VM_WRITE_INDENT(); }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += vj_escape_string_fast(buf, (const uint8_t *)k->ptr, k->len);
#else
    buf += vj_escape_string(buf, (const uint8_t *)k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) { *buf++ = ' '; }

    /* Advance past current slot for next iteration */
    si++;
    frame->map.dir_idx = di;
    frame->map.group_idx = (uint8_t)gi;
    frame->map.slot_idx = (uint8_t)si;
    frame->map.remaining = remaining - 1;

    /* Set base to value ptr and jump back to body start */
    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_TRACE("MAP_STR_ITER_END(next)");
    VM_JUMP_BYTES(ext->operand_a); /* relative jump back to body start */
  }

map_str_iter_end_done:
  /* Done: write indent + '}', pop frame */
  VM_INDENT_DEC();
  VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
  VM_WRITE_INDENT();
  *buf++ = '}';
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("MAP_STR_ITER_END(done)");
  base = frame->ret_base;
  VJ_ST_SET_FIRST_0(vmstate);
  VM_NEXT_LONG();
}

/* --- Sequence iterator opcode handlers (macro defined in seqiter.h) --- */

VJ_DEFINE_SEQ_OP(vj_op_seq_float64, "SEQ_FLOAT64", vj_seq_iterate_float64, 1)
VJ_DEFINE_SEQ_OP(vj_op_seq_int,     "SEQ_INT",     vj_seq_iterate_int,     0)
VJ_DEFINE_SEQ_OP(vj_op_seq_int64,   "SEQ_INT64",   vj_seq_iterate_int64,   0)
VJ_DEFINE_SEQ_OP(vj_op_seq_string,  "SEQ_STRING",  vj_seq_iterate_string,  0)

#undef VJ_DEFINE_SEQ_OP

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
    if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH, 0)) {
      buf = iface_saved_buf;
      vmstate = iface_saved_vmstate;
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }

    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    frame->call.ret_ops = ops;
    frame->call.ret_pc = (int32_t)((const uint8_t *)op - ops) + 8; /* INTERFACE is always 8 bytes */
    frame->ret_base = base;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);

    /* Switch to child Blueprint's ops.
     * The speculative key write stays — child ops produce the value. */
    ops = iface_r.cached_ops;
    op = (const VjOpHdr *)ops;
    base = iface_r.data_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
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
#undef VM_WRITE_KEY_ALWAYS
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
#undef VM_SAVE_TRACE_DEPTH_CTX
#ifdef VJ_COMPACT_INDENT
#undef indent_tpl
#undef indent_depth
#undef indent_step
#undef indent_prefix_len
#endif
}

#undef VM_SAVE_AND_RETURN

#endif /* VJ_ENCVM_H */
