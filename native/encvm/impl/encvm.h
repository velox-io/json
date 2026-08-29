/*
 * VM bytecode interpreter
 *
 * vj_exec() walks a variable-length opcode stream encoding Go values to JSON.
 *
 */

#ifndef VJ_ENCVM_H
#define VJ_ENCVM_H

#include "base64.h"
#include "eface.h"
#include "ftoa.h"
#include "seqiter.h"
#include "strfn.h"
#include "swissmap.h"
#include "tapewalk.h"
#include "timefmt.h"
#include "trace.h"
#include "types.h"
#include "macros.h"

/* Save VM state to context and return.
 * Writes the packed vmstate (with exit code set) back to ctx.
 * Go reads exit/yield/depth/first directly from vmstate. */
#define VM_SAVE_AND_RETURN(exit_code)                                                                             \
  do {                                                                                                            \
    VM_TRACE_MSG("◀ exit");                                                                                       \
    ctx->buf_cur  = buf;                                                                                          \
    ctx->ops_ptr  = ops;                                                                                          \
    ctx->pc       = (int32_t)((const uint8_t *)op - ops);                                                         \
    ctx->cur_base = base;                                                                                         \
    VM_SAVE_INDENT_DEPTH();                                                                                       \
    VM_SAVE_TRACE_DEPTH_CTX();                                                                                    \
    VJ_ST_SET_EXIT(vmstate, exit_code);                                                                           \
    ctx->vmstate = vmstate;                                                                                       \
    return;                                                                                                       \
  } while (0)

/* vj_exec is the VM entry point.
 * When VJ_VM_EXEC_FN_NAME is defined, emits the public symbol directly. */
#ifdef VJ_VM_EXEC_FN_NAME
EXPORT ALIGN_STACK void VJ_VM_EXEC_FN_NAME(VjExecCtx *ctx)
#else
INLINE void vj_exec(VjExecCtx *ctx)
#endif
{
  /* Load context into registers / locals */
  uint8_t *buf        = ctx->buf_cur;
  uint8_t *bend       = (uint8_t *)ctx->buf_end;
  const uint8_t *ops  = ctx->ops_ptr;
  const VjOpHdr *op   = (const VjOpHdr *)(ops + ctx->pc);
  const uint8_t *base = ctx->cur_base;

  /* Scratch for the goto tail-call shells (values cross the goto through
   * memory): leaf_intval serves write_int64/write_uint64 (same bits, read
   * back through the respective type); leaf_b64 carries the base64 args;
   * leaf_walk carries the tape-walk entry (value pointer + mode). */
  volatile int64_t leaf_intval = 0;
  volatile struct {
    const uint8_t *data;
    int64_t len;
  } leaf_b64 = {0};
  volatile struct {
    const GoValue *v;
    uint32_t mode; /* 0 = full value, 1 = spread (members, no braces) */
  } leaf_walk = {0};

  /* Global key pool base pointer, loaded once at VM entry.
   * All VjOpHdr key_off values index into this pool.
   * Stable for the entire VM execution (COW snapshot on Go side). */
  const uint8_t *key_pool = ctx->key_pool_base;

  /* Packed VM state: a single register holding depth, first, flags,
   * exit_code, yield_reason.  See types.h for layout.
   *
   * The hot-path first-flag check runs every opcode dispatch, where a stack
   * round-trip there costs ~2-3 extra cycles per instruction.  We use
   * VM_PIN_VMSTATE(), an empty volatile asm with a "+r" constraint,
   * at key points to prevent the compiler from spilling vmstate to the
   * stack.  The asm generates zero instructions but acts as a register-
   * allocation fence that forces vmstate to stay in *some* register. */
  uint64_t vmstate = ctx->vmstate;
#define VM_PIN_VMSTATE() __asm__ volatile("" : "+r"(vmstate))
  VM_PIN_VMSTATE();

/* Trace-only depth: VM stack depth + OBJ_OPEN/CLOSE count for debug output
 * indentation. NOT used for JSON indent, which uses indent_depth separately. */
#ifdef VJ_DEBUG
  /* trace_obj_depth tracks OBJ_OPEN/CLOSE nesting for trace indentation only.
   * Persisted in ctx->trace_depth across yields (replaces old indent_depth
   * derivation). */
  int trace_obj_depth = ctx->trace_depth;
  VjTraceBuf *tbuf    = ctx->trace_buf;
#define VM_TRACE_DEPTH() (VJ_ST_GET_STACK_DEPTH(vmstate) + trace_obj_depth)
/* Save/restore trace depth in state bits [24..31] across push/pop.
 * Bit 0 of state is the iter-active flag (used by C-native loops). */
#define VM_SAVE_TRACE_DEPTH(frame)    ((frame)->state = ((frame)->state & 0xFF) | (trace_obj_depth << 24))
#define VM_RESTORE_TRACE_DEPTH(frame) (trace_obj_depth = (int32_t)((frame)->state >> 24))
/* Save trace_obj_depth back to ctx on VM exit. */
#define VM_SAVE_TRACE_DEPTH_CTX() (ctx->trace_depth = trace_obj_depth)
  VM_TRACE_MSG("▶ enter");
#else
#define VM_TRACE_DEPTH()              (VJ_ST_GET_STACK_DEPTH(vmstate))
#define VM_SAVE_TRACE_DEPTH(frame)    ((void)0)
#define VM_RESTORE_TRACE_DEPTH(frame) ((void)0)
#define VM_SAVE_TRACE_DEPTH_CTX()     ((void)0)
#endif

  /* Indent state: indent_step == 0 means compact mode (no indentation).
   * indent_tpl points to a precomputed "\n" + prefix + indent×MAX_DEPTH buffer.
   * indent_depth tracks logical nesting (incremented at {/[, decremented at
   * }/]). indent_prefix_len is the byte length of the prefix between "\n" and
   * the repeated indent string.
   *
   * NOTE: Indent fields are now at offset 64+ (cache line 1), separate from
   * the hot VM registers in cache line 0. This is intentional: compact mode
   * never touches them, and indent mode accesses them less frequently than
   * ops/pc/base/buf. */
#ifdef VJ_COMPACT_INDENT
/* Compact mode: indent state eliminated at compile time.
 * All VM_INDENT_PAD → 0, VM_WRITE_INDENT → nop, key space → nop. */
#define indent_tpl             ((const uint8_t *)0)
#define indent_depth           ((int16_t)0)
#define indent_step            ((uint8_t)0)
#define indent_prefix_len      ((uint8_t)0)
#define VM_KEY_SPACE           0
#define VM_INDENT_INC()        ((void)0)
#define VM_INDENT_DEC()        ((void)0)
#define VM_SAVE_INDENT_DEPTH() ((void)0)
#else
  /* indent_depth is mutable (INC/DEC per nesting level), kept as a local.
   * indent_tpl/step/prefix_len are invariant for the whole VM call (set
   * once by Go before entry); read them directly from ctx instead of
   * caching in locals. ctx lives in a callee-saved register, so each
   * access is a single load. Drops 3 stack slots from vm_exec. */
  int16_t indent_depth = ctx->indent_depth;
#define indent_tpl             (ctx->indent_tpl)
#define indent_step            (ctx->indent_step)
#define indent_prefix_len      (ctx->indent_prefix_len)
#define VM_KEY_SPACE           (indent_step ? 1 : 0)
#define VM_INDENT_INC()        (indent_depth++)
#define VM_INDENT_DEC()        (indent_depth--)
#define VM_SAVE_INDENT_DEPTH() (ctx->indent_depth = indent_depth)
#endif

/* Computed goto dispatch table
 *
 * int32 offsets from base label.
 * Covers primitive, data, structural, and fallback opcodes.
 * Sparse: unused slots are zero-initialized (caught by bounds check).
 */
#define DT_ENTRY(label) (int32_t)((char *) && label - (char *) && vj_dispatch_base)

  ALIGNED_DECL(64)
  static const int32_t dispatch_table[OP_DISPATCH_COUNT] ALIGNED(64) = {
      /* Primitives (1-14) */
      [OP_BOOL]    = DT_ENTRY(vj_op_bool),
      [OP_INT]     = DT_ENTRY(vj_op_int),
      [OP_INT8]    = DT_ENTRY(vj_op_int8),
      [OP_INT16]   = DT_ENTRY(vj_op_int16),
      [OP_INT32]   = DT_ENTRY(vj_op_int32),
      [OP_INT64]   = DT_ENTRY(vj_op_int64),
      [OP_UINT]    = DT_ENTRY(vj_op_uint),
      [OP_UINT8]   = DT_ENTRY(vj_op_uint8),
      [OP_UINT16]  = DT_ENTRY(vj_op_uint16),
      [OP_UINT32]  = DT_ENTRY(vj_op_uint32),
      [OP_UINT64]  = DT_ENTRY(vj_op_uint64),
      [OP_FLOAT32] = DT_ENTRY(vj_op_float32),
      [OP_FLOAT64] = DT_ENTRY(vj_op_float64),
      [OP_STRING]  = DT_ENTRY(vj_op_string),

      /* Non-primitive data ops (15-18) */
      [OP_INTERFACE]   = DT_ENTRY(vj_op_interface),
      [OP_RAW_MESSAGE] = DT_ENTRY(vj_op_raw_message),
      [OP_NUMBER]      = DT_ENTRY(vj_op_number),
      [OP_BYTE_SLICE]  = DT_ENTRY(vj_op_byte_slice),

      /* Structural control-flow (19-31) */
      [OP_SKIP_IF_ZERO] = DT_ENTRY(vj_op_skip_if_zero),
      [OP_CALL]         = DT_ENTRY(vj_op_call),
      [OP_PTR_DEREF]    = DT_ENTRY(vj_op_ptr_deref),
      [OP_PTR_END]      = DT_ENTRY(vj_op_ptr_end),
      [OP_SLICE_BEGIN]  = DT_ENTRY(vj_op_slice_begin),
      [OP_SLICE_END]    = DT_ENTRY(vj_op_slice_end),
      [OP_MAP]          = DT_ENTRY(vj_op_map),
      /* 26: reserved (was OP_MAP_END) */
      [OP_OBJ_OPEN]    = DT_ENTRY(vj_op_obj_open),
      [OP_OBJ_CLOSE]   = DT_ENTRY(vj_op_obj_close),
      [OP_ARRAY_BEGIN] = DT_ENTRY(vj_op_array_begin),
      [OP_MAP_STR_STR] = DT_ENTRY(vj_op_map_str_str),
      [OP_RET]         = DT_ENTRY(vj_op_ret),

      /* Go-only fallback (32) */
      [OP_FALLBACK] = DT_ENTRY(vj_op_yield),

      /* Keyed-field variants (33-35): unconditional key write (no key_len branch) */
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

      /* Keyed-field quoted variants (44-45), ,string tag */
      [OP_KQINT]   = DT_ENTRY(vj_op_kqint),
      [OP_KQINT64] = DT_ENTRY(vj_op_kqint64),

      /* time.Time (46): native RFC3339Nano */
      [OP_TIME] = DT_ENTRY(vj_op_time),

      /* value.Value (47): native tape walk */
      [OP_VALUE] = DT_ENTRY(vj_op_value),

      /* value.Value reserve-unknown spread (48) */
      [OP_VALUE_SPREAD] = DT_ENTRY(vj_op_value_spread),

      /* inline variant unfold (49) */
      [OP_UNFOLD] = DT_ENTRY(vj_op_unfold),
  };

#undef DT_ENTRY

#ifdef VJ_DEBUG
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

/* Check buffer space */
#define VM_CHECK(n)                                                                                               \
  do {                                                                                                            \
    if (UNLIKELY(buf + (n) > bend)) {                                                                             \
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);                                                                       \
    }                                                                                                             \
  } while (0)

/* Indent helpers */

/* Max indent bytes for VM_CHECK: '\n' + prefix + indent_depth * indent_step.
 * Returns 0 in compact mode (indent_step == 0). */
#define VM_INDENT_PAD(idepth) (indent_step ? (1 + indent_prefix_len + (idepth) * indent_step) : 0)

/* Write indent: '\n' + prefix + indent for current indent_depth.
 * No-op in compact mode. */
#define VM_WRITE_INDENT()                                                                                         \
  do {                                                                                                            \
    if (indent_step) {                                                                                            \
      int _n = 1 + indent_prefix_len + indent_depth * indent_step;                                                \
      __builtin_memcpy(buf, indent_tpl, _n);                                                                      \
      buf += _n;                                                                                                  \
    }                                                                                                             \
  } while (0)

/* Close-token reservation: newline + prefix + (depth-1) indent units + the
 * closing brace/bracket. The pad uses the POST-decrement depth, clamped at 0.
 * The check MUST run before VM_INDENT_DEC(): on window-full the VM exits and
 * later re-executes the op from its start, so a decrement applied before the
 * failing check would be applied twice on resume, driving indent_depth
 * negative (negative depth makes VM_WRITE_INDENT compute a negative length,
 * which memcpy interprets as a huge size_t, a hard crash). */
#define VM_CHECK_CLOSE() VM_CHECK(1 + VM_INDENT_PAD(indent_depth > 0 ? (int16_t)(indent_depth - 1) : 0))

/* Write pre-encoded key with comma prefix */
#define VM_WRITE_KEY()                                                                                            \
  do {                                                                                                            \
    VM_PIN_VMSTATE();                                                                                             \
    int _was_first;                                                                                               \
    VJ_ST_BTR_FIRST(vmstate, _was_first);                                                                         \
    if (indent_step) {                                                                                            \
      /* Indent mode: branch needed for newline + prefix + indent. */                                             \
      if (!_was_first) {                                                                                          \
        *buf++ = ',';                                                                                             \
        VM_WRITE_INDENT();                                                                                        \
      }                                                                                                           \
    } else {                                                                                                      \
      /* Compact mode: branchless comma.  Always write ',', then                                                  \
       * advance by (1 - was_first).  First element: key overwrites                                               \
       * the comma; subsequent elements: buf skips past it. */                                                    \
      *buf = ',';                                                                                                 \
      buf += 1 - _was_first;                                                                                      \
    }                                                                                                             \
    if (op->key_len > 0) {                                                                                        \
      vj_copy_key(buf, (const char *)(key_pool + op->key_off), op->key_len);                                      \
      buf += op->key_len;                                                                                         \
      if (indent_step) {                                                                                          \
        *buf++ = ' ';                                                                                             \
      }                                                                                                           \
    }                                                                                                             \
  } while (0)

/* Write pre-encoded key unconditionally (keyed-field variants)
 * Same as VM_WRITE_KEY() but without the if (op->key_len > 0) branch.
 * Used by OP_KSTRING/OP_KINT/OP_KINT64 where key is always present. */
#define VM_WRITE_KEY_ALWAYS()                                                                                     \
  do {                                                                                                            \
    VM_PIN_VMSTATE();                                                                                             \
    int _was_first;                                                                                               \
    VJ_ST_BTR_FIRST(vmstate, _was_first);                                                                         \
    if (indent_step) {                                                                                            \
      if (!_was_first) {                                                                                          \
        *buf++ = ',';                                                                                             \
        VM_WRITE_INDENT();                                                                                        \
      }                                                                                                           \
    } else {                                                                                                      \
      *buf = ',';                                                                                                 \
      buf += 1 - _was_first;                                                                                      \
    }                                                                                                             \
    vj_copy_key(buf, (const char *)(key_pool + op->key_off), op->key_len);                                        \
    buf += op->key_len;                                                                                           \
    if (indent_step) {                                                                                            \
      *buf++ = ' ';                                                                                               \
    }                                                                                                             \
  } while (0)

/* Dispatch macro (ADR/LEA trick for PIC computed goto)
 * The `__asm__ volatile("" : "+r"(_tgt))` identity barrier on the resolved
 * target pointer is load bearing. Without it, clang 17 (and lld at link
 * time) tail-merge the identical `goto *` tails of every opcode handler
 * into one shared dispatch site. The barrier makes each handler's target
 * value opaque at the point of the branch, so the tails are no longer
 * mergeable and each handler keeps its own `br` with its own BTB history.
 **/
#if defined(__aarch64__)
#define VM_DISPATCH()                                                                                             \
  do {                                                                                                            \
    uint16_t i = op->op_type;                                                                                     \
    char *_base;                                                                                                  \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_dispatch_base), "r"(op));                             \
    char *_tgt = _base + dispatch_table[i];                                                                       \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#elif defined(__x86_64__)
#define VM_DISPATCH()                                                                                             \
  do {                                                                                                            \
    uint16_t i = op->op_type;                                                                                     \
    char *_base;                                                                                                  \
    __asm__ volatile("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_dispatch_base), "r"(op));                      \
    char *_tgt = _base + dispatch_table[i];                                                                       \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#else
#error "VM_DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Resume a cold-outline leaf caller by base+offset goto.
 *
 * Mirrors VM_DISPATCH's adr/lea-based base computation so the prelinker
 * carries no .quad relocation for the resume label: ret_off is a
 * compile-time int32 offset from vj_dispatch_base, materialized at the
 * call site as (char*)&&resume - (char*)&&vj_dispatch_base.  Storing the
 * raw label address instead puts it in an unnamed __DATA,__const pool
 * entry whose .quad relocation the prelinker drops. */
#if defined(__aarch64__)
#define VM_RESUME(off)                                                                                            \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_dispatch_base));                                      \
    char *_tgt = _base + (off);                                                                                   \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#elif defined(__x86_64__)
#define VM_RESUME(off)                                                                                            \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    __asm__ volatile("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_dispatch_base));                               \
    char *_tgt = _base + (off);                                                                                   \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#else
#error "VM_RESUME: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Static-width advance macros: each handler knows its own instruction
 * size at compile time, so no runtime size decode is needed.            */
#define VM_NEXT_SHORT()                                                                                           \
  do {                                                                                                            \
    op = (const VjOpHdr *)((const uint8_t *)op + 8);                                                              \
    VM_DISPATCH();                                                                                                \
  } while (0)
#define VM_NEXT_LONG()                                                                                            \
  do {                                                                                                            \
    op = (const VjOpHdr *)((const uint8_t *)op + 16);                                                             \
    VM_DISPATCH();                                                                                                \
  } while (0)
#define VM_JUMP_BYTES(byte_offset)                                                                                \
  do {                                                                                                            \
    op = (const VjOpHdr *)((const uint8_t *)op + (byte_offset));                                                  \
    VM_DISPATCH();                                                                                                \
  } while (0)

  VM_DISPATCH();

vj_dispatch_base:
  __builtin_unreachable();

vj_op_obj_open: {
  /* Lightweight nested struct open: write key + '{', flip first flag.
   * No stack frame push, no base switch; child field offsets are
   * pre-computed (absolute from top-level struct base). */
  VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
  VM_WRITE_KEY();
  *buf++ = '{';
  VM_INDENT_INC();
  VM_WRITE_INDENT(); /* indent for first child field */
  VM_TRACE_KEY("OBJ_OPEN");
#ifdef VJ_DEBUG
  trace_obj_depth++;
#endif
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_SHORT();
}

vj_op_slice_begin: {
  const GoSlice *sl  = (const GoSlice *)(base + op->field_off);
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
  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame   = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->ret_base       = base;
  frame->seq.iter_data  = sl->data;
  frame->seq.iter_count = sl->len;
  frame->seq.iter_idx   = 0;
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
  int64_t overhead  = 1 + op->key_len + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE;
  int64_t max_need  = overhead + 2 + (s->len * 6);
  if (UNLIKELY(buf + max_need > bend)) {
    goto vj_state_prescan_escaped;
  }
  VM_WRITE_KEY();
#ifdef VJ_FAST_STRING_ESCAPE
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, s->ptr, s->len);
#else
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
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
  if (UNLIKELY(__builtin_isnan(dval) || __builtin_isinf(dval))) {
    VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 330 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += vj_write_float64(buf, dval,
                          (VJ_ST_GET_FLAGS(vmstate) & VJ_FLAGS_FLOAT_EXP_AUTO) ? VJ_FTOA_EXP_AUTO : VJ_FTOA_FIXED);
  VM_NEXT_SHORT();
}

/* Keyed-field variants: unconditional key write (no key_len branch) */
vj_op_kstring: {
  VM_TRACE_KEY("KSTRING");
  const GoString *s = (const GoString *)(base + op->field_off);
  int64_t overhead  = 1 + op->key_len + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE;
  int64_t max_need  = overhead + 2 + (s->len * 6);
  if (UNLIKELY(buf + max_need > bend)) {
    /* Pessimistic estimate says buffer is too small.  For long strings the 6x multiplier
     * is wildly conservative: pre-scan to get a tight bound before giving up with BufFull. */
    goto vj_state_prescan_escaped;
  }
  VM_WRITE_KEY_ALWAYS();
#ifdef VJ_FAST_STRING_ESCAPE
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, s->ptr, s->len);
#else
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
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
  *buf++      = '"';
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  *buf++ = '"';
  VM_NEXT_SHORT();
}

vj_op_kqint64: {
  VM_TRACE_KEY("KQINT64");
  VM_CHECK(op->key_len + 1 + 21 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY_ALWAYS();
  *buf++      = '"';
  int64_t val = *(const int64_t *)(base + op->field_off);
  buf += write_int64(buf, val);
  *buf++ = '"';
  VM_NEXT_SHORT();
}

vj_op_time: {
  /* time.Time: native RFC3339Nano formatting.
   * Works in any context: struct field (with key), slice/array element (no
   * key), pointer deref body, etc. Must check yield-eligibility BEFORE writing
   * the key, because once the key is written there's no way to undo it on
   * yield. */
  const GoTime *t = (const GoTime *)(base + op->field_off);

  /* Check if we can handle this timezone natively */
  if (!vj_time_can_native(t->loc)) {
    /* Complex timezone (DST): yield to Go like OP_FALLBACK */
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
    uint64_t abs     = (uint64_t)(unix_sec + VJ_TIME_UNIX_TO_ABS);
    uint64_t days    = abs / VJ_SECONDS_PER_DAY;
    uint64_t d4      = 4 * days + 3;
    uint64_t century = d4 / 146097;
    uint32_t cd      = (uint32_t)(d4 % 146097) | 3;
    uint64_t mul     = (uint64_t)2939745 * (uint64_t)cd;
    uint32_t cyear   = (uint32_t)(mul >> 32);
    uint32_t ayday   = (uint32_t)((uint32_t)mul / 2939745 / 4);
    uint32_t janFeb  = (ayday >= VJ_TIME_MARCH_THRU_DEC) ? 1 : 0;
    year             = (int)(century * 100 - VJ_TIME_ABSOLUTE_YEARS) + (int)cyear + (int)janFeb;
  }

  if (year < 0 || year > 9999) {
    /* Out of range: yield to Go (let Go report the error) */
    VM_TRACE_YIELD(op->op_type);
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  }

  VM_TRACE_KEY("TIME");
  /* Max output: comma(1) + key + key_space(1) + quote(1) +
   * "2006-01-02T15:04:05.999999999+00:00"(35) + quote(1) + indent */
  VM_CHECK(op->key_len + 1 + 37 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += vj_write_rfc3339nano(buf, t, tz_offset, &year);
  VM_NEXT_SHORT();
}

vj_op_value: {
  /* value.Value: re-serialize the tape straight into the buffer. Works in
   * any context (keyed struct field, keyless slice/map element, pointer
   * body). The heavy lifting lives in vj_tape_walk_step (tapewalk.h); this
   * shell owns the prefix, the guards, and the frame lifecycle. */
  VM_TRACE_KEY("VALUE");
  leaf_walk.v    = (const GoValue *)(base + op->field_off);
  leaf_walk.mode = 0;
  goto vj_state_value_walk_enter;
}

vj_op_value_spread: {
  /* Reserve-unknown Value: emit the collected object's members inline at
   * the host level, no enclosing braces, no key of its own. The first
   * member consumes the host first latch; an empty value leaves it. */
  VM_TRACE("VALUE_SPREAD");
  leaf_walk.v    = (const GoValue *)(base + op->field_off);
  leaf_walk.mode = 1;
  goto vj_state_value_walk_enter;
}

vj_op_obj_close: {
#ifdef VJ_DEBUG
  if (trace_obj_depth > 0) trace_obj_depth--;
#endif
  VM_TRACE("OBJ_CLOSE");
  /* Lightweight nested struct close: write indent + '}', set first=0.
   * No stack frame pop; mirrors vj_op_obj_open. */
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  {
    int _was_first;
    VJ_ST_BTR_FIRST(vmstate, _was_first);
    if (!_was_first) {
      VM_WRITE_INDENT();
    }
  }
  *buf++ = '}';
  VM_NEXT_SHORT();
}

vj_op_skip_if_zero: {
  VM_TRACE("SKIP_IF_ZERO");
  const VjOpExt *ext  = VJ_OP_EXT(op);
  uint16_t check_type = (uint16_t)ext->operand_b;
  if (vj_is_zero(base + op->field_off, check_type)) {
    VM_JUMP_BYTES(ext->operand_a); /* byte offset from op start to target */
  }
  VM_NEXT_LONG();
}

  /* slice/array loop body */

vj_op_slice_end: {
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  const VjOpExt *ext  = VJ_OP_EXT(op);

  if (frame->seq.iter_idx + 1 < frame->seq.iter_count) {
    /* More elements: write comma + indent, advance base, jump back.
     * SLICE_END intentionally flips the operand layout used by SLICE_BEGIN:
     *   operand_a = relative jump back to body start
     *   operand_b = elem_size
     * so the hot loop can use operand_b directly for base advance. */
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));
    frame->seq.iter_idx++; /* commit only after the check: resume re-executes this op;
                            * an increment committed pre-check would skip an element */
    *buf++ = ',';
    VM_WRITE_INDENT();
    base = frame->seq.iter_data + (int64_t)frame->seq.iter_idx * ext->operand_b;
    op   = (const VjOpHdr *)((const uint8_t *)op + ext->operand_a); /* relative jump back to body start */
    VM_TRACE_ELEM_IDX(frame->seq.iter_idx);
    VJ_ST_SET_FIRST_1(vmstate); /* reset for element-level encoding (no struct comma) */
    VM_DISPATCH();
  }

  /* Done: write indent + ']', pop frame */
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  VM_WRITE_INDENT();
  *buf++ = ']';
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("SLICE_END");
  base            = frame->ret_base;
  frame->ret_base = NULL;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this field */
  VM_NEXT_LONG();
}

  /* other primitives + pointer deref */

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
  leaf_intval = (int64_t)*(const int8_t *)(base + op->field_off);
  goto vj_state_write_int64;
}

vj_op_int16: {
  VM_TRACE_KEY("INT16");
  VM_CHECK(op->key_len + 1 + 7 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = (int64_t)*(const int16_t *)(base + op->field_off);
  goto vj_state_write_int64;
}

vj_op_int32: {
  VM_TRACE_KEY("INT32");
  VM_CHECK(op->key_len + 1 + 12 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = (int64_t)*(const int32_t *)(base + op->field_off);
  goto vj_state_write_int64;
}

vj_op_uint: {
  VM_TRACE_KEY("UINT");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = *(const uint64_t *)(base + op->field_off);
  goto vj_state_write_uint64;
}

vj_op_uint8: {
  VM_TRACE_KEY("UINT8");
  VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = (uint64_t)*(const uint8_t *)(base + op->field_off);
  goto vj_state_write_uint64;
}

vj_op_uint16: {
  VM_TRACE_KEY("UINT16");
  VM_CHECK(op->key_len + 1 + 6 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = (uint64_t)*(const uint16_t *)(base + op->field_off);
  goto vj_state_write_uint64;
}

vj_op_uint32: {
  VM_TRACE_KEY("UINT32");
  VM_CHECK(op->key_len + 1 + 11 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = (uint64_t)*(const uint32_t *)(base + op->field_off);
  goto vj_state_write_uint64;
}

vj_op_uint64: {
  VM_TRACE_KEY("UINT64");
  VM_CHECK(op->key_len + 1 + 21 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_intval = *(const uint64_t *)(base + op->field_off);
  goto vj_state_write_uint64;
}

vj_op_float32: {
  VM_TRACE_KEY("FLOAT32");
  float fval;
  __builtin_memcpy(&fval, base + op->field_off, 4);
  if (UNLIKELY(__builtin_isnan(fval) || __builtin_isinf(fval))) {
    VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
  }
  VM_CHECK(op->key_len + 1 + 60 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  buf += vj_write_float32(buf, fval,
                          (VJ_ST_GET_FLAGS(vmstate) & VJ_FLAGS_FLOAT_EXP_AUTO) ? VJ_FTOA_EXP_AUTO : VJ_FTOA_FIXED);
  VM_NEXT_SHORT();
}

vj_op_ptr_deref: {
  void *ptr          = *(void **)(base + op->field_off);
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

  /* Push ptr frame; only ret_base is needed.
   * PTR_END restores base from ret_base, sets first=0 in vmstate,
   * and advances with VM_NEXT_SHORT(), so no ret_ops/ret_pc state is stored. */
  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)].ret_base = base;
  VM_SAVE_TRACE_DEPTH(&ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)]);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  base = (const uint8_t *)ptr;
  VJ_ST_SET_FIRST_1(vmstate); /* deref body is a "value" context with no leading comma */
  VM_NEXT_LONG();
}

vj_op_ptr_end: {
  /* Pop the ptr-deref frame, restore parent base */
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("PTR_END");
  base            = frame->ret_base;
  frame->ret_base = NULL;
  VJ_ST_SET_FIRST_0(vmstate); /* parent had at least this ptr field */
  VM_NEXT_SHORT();
}

  /* call/ret, raw_message, number, byte_slice, array */

vj_op_call: {
  VM_TRACE("CALL");
  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->call.ret_ops = ops;
  frame->call.ret_pc  = (int32_t)((const uint8_t *)op - ops) + 16; /* CALL is always 16 bytes */
  frame->ret_base     = base;
  frame->state        = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  const VjOpExt *ext = VJ_OP_EXT(op);
  base               = base + op->field_off;
  op                 = (const VjOpHdr *)(ops + ext->operand_a);
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
    ops             = frame->call.ret_ops;
    op              = (const VjOpHdr *)(ops + frame->call.ret_pc);
    base            = frame->ret_base;
    frame->ret_base = NULL;
    /* An unfold body that wrote nothing must leave the host's first latch
     * untouched (an empty case struct is invisible in the output). Any
     * field write already cleared the latch, so preserving it only matters
     * exactly when nothing was emitted. */
    if (!(frame->state & VJ_FRAME_STATE_PRESERVE_FIRST)) {
      VJ_ST_SET_FIRST_0(vmstate);
    }
    VM_DISPATCH();
  }

  /* Top-level done */
  VM_TRACE("HALT");
  ctx->buf_cur = buf;
  VJ_ST_SET_EXIT(vmstate, VJ_EXIT_OK);
  ctx->vmstate = vmstate;
  return;
}

vj_op_unfold: {
  /* Inline variant field (json:",embed" on an interface): dispatch the
   * stored concrete struct's body-only Blueprint. No key, no braces: the
   * body's fields continue the host object at the host's indent level.
   * The stored value decides the case at runtime; the encode side needs
   * no variant tables. */
  VM_TRACE("UNFOLD");
  const uint8_t *iface_ptr = base + op->field_off;
  const void *type_ptr     = *(const void **)iface_ptr;
  if (type_ptr == NULL) {
    /* nil case: nothing emitted, first latch untouched. */
    VM_NEXT_SHORT();
  }

  /* A non-empty interface stores an itab in word 0; the concrete type is
   * the itab's second word. An eface stores the rtype directly. */
  if (op->flags & VJ_OP_FLAG_IFACE_FIELD) {
    type_ptr = *(const void **)((const uint8_t *)type_ptr + 8);
  }

  const VjIfaceCacheEntry *e = vj_iface_cache_lookup(ctx->iface_cache_ptr, ctx->iface_cache_count, type_ptr);
  if (UNLIKELY(e == NULL || e->body_ops == NULL)) {
    /* Miss (or body not yet compiled): yield for Go compilation. No key
     * exists, so nothing has been written. */
    ctx->yield_type_ptr = type_ptr;
    VM_TRACE_YIELD(op->op_type);
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_IFACE_MISS);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  }

  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }

  {
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    frame->call.ret_ops = ops;
    frame->call.ret_pc  = (int32_t)((const uint8_t *)op - ops) + 8; /* UNFOLD is 8 bytes */
    frame->ret_base     = base;
    frame->state        = VJ_FRAME_STATE_PRESERVE_FIRST;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);
    /* The data word is a pointer for both boxed structs and pointer cases,
     * so one deref addresses the body's field zero. */
    ops  = e->body_ops;
    op   = (const VjOpHdr *)ops;
    base = *(const uint8_t **)(iface_ptr + 8);
    VM_DISPATCH();
  }
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

  /* key + colon + indent + '"' + ceil(len/3)*4 + '"' */
  VM_CHECK(op->key_len + 1 + 2 + ((sl->len + 2) / 3) * 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  VM_WRITE_KEY();
  leaf_b64.data = sl->data;
  leaf_b64.len  = sl->len;
  goto vj_state_encode_base64;
}

vj_op_array_begin: {
  /* Fixed-size array: data is inline at base + field_off.
   * ext->operand_a packs elem_size (low 16) | array_len (high 16).
   * ext->operand_b = body byte length (excl SLICE_END).
   * Reuses VJ_FRAME_ITER for the stack frame and opSliceEnd for back-edge. */
  const VjOpExt *ext      = VJ_OP_EXT(op);
  int32_t packed          = ext->operand_a;
  int32_t arr_elem_size   = packed & 0xFFFF;
  int32_t array_len       = (uint32_t)packed >> 16;
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

  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }
  VjStackFrame *frame   = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
  frame->ret_base       = base;
  frame->seq.iter_data  = arr_data;
  frame->seq.iter_count = array_len;
  frame->seq.iter_idx   = 0;
  VM_SAVE_TRACE_DEPTH(frame);
  VJ_ST_INC_STACK_DEPTH(vmstate);

  VM_TRACE_KEY_LEN("ARRAY_BEGIN", array_len);
  base = arr_data;
  VM_TRACE_ELEM_IDX(0);
  VJ_ST_SET_FIRST_1(vmstate);
  VM_NEXT_LONG();
}

#define VJ_DEFINE_MAP_SWISS_OP_SM(OP_LABEL, TRACE_LABEL, STATE_LABEL)                                             \
  OP_LABEL: {                                                                                                     \
    VM_TRACE_KEY(TRACE_LABEL);                                                                                    \
    int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);                                                              \
    int is_resume  = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));                                          \
    const GoSwissMap *m;                                                                                          \
    int32_t remaining, di, gi, si;                                                                                \
    int entry_first;                                                                                              \
    if (is_resume) {                                                                                              \
      VjStackFrame *f = &ctx->stack[_depth - 1];                                                                  \
      m               = (const GoSwissMap *)f->map.map_ptr;                                                       \
      remaining       = f->map.remaining;                                                                         \
      di              = f->map.dir_idx;                                                                           \
      gi              = f->map.group_idx;                                                                         \
      si              = f->map.slot_idx;                                                                          \
      entry_first     = f->map.entry_first;                                                                       \
    } else {                                                                                                      \
      m = *(const GoSwissMap **)(base + op->field_off);                                                           \
      if (m == NULL || m->used == 0) {                                                                            \
        if (m == NULL) {                                                                                          \
          VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);                             \
          VM_WRITE_KEY();                                                                                         \
          __builtin_memcpy(buf, "null", 4);                                                                       \
          buf += 4;                                                                                               \
        } else {                                                                                                  \
          VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);                             \
          VM_WRITE_KEY();                                                                                         \
          *buf++ = '{';                                                                                           \
          *buf++ = '}';                                                                                           \
        }                                                                                                         \
        VJ_ST_SET_FIRST_0(vmstate);                                                                               \
        VM_NEXT_SHORT();                                                                                          \
      }                                                                                                           \
      VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE +                                 \
               VM_INDENT_PAD(indent_depth + 1));                                                                  \
      VM_WRITE_KEY();                                                                                             \
      *buf++ = '{';                                                                                               \
      VM_INDENT_INC();                                                                                            \
      VM_WRITE_INDENT();                                                                                          \
      remaining   = (int32_t)m->used;                                                                             \
      di          = 0;                                                                                            \
      gi          = 0;                                                                                            \
      si          = 0;                                                                                            \
      entry_first = 1;                                                                                            \
    }                                                                                                             \
    {                                                                                                             \
      if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {                                       \
        VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);                                                               \
      }                                                                                                           \
      VjStackFrame *f    = &ctx->stack[is_resume ? (_depth - 1) : _depth];                                        \
      f->ret_base        = base;                                                                                  \
      f->map.map_ptr     = m;                                                                                     \
      f->map.remaining   = remaining;                                                                             \
      f->map.dir_idx     = di;                                                                                    \
      f->map.group_idx   = (uint8_t)gi;                                                                           \
      f->map.slot_idx    = (uint8_t)si;                                                                           \
      f->map.entry_first = (uint8_t)entry_first;                                                                  \
      if (!is_resume) {                                                                                           \
        f->state = 0;                                                                                             \
        VJ_ST_INC_STACK_DEPTH(vmstate);                                                                           \
      }                                                                                                           \
      goto STATE_LABEL;                                                                                           \
    }                                                                                                             \
  }

  VJ_DEFINE_MAP_SWISS_OP_SM(vj_op_map_str_str, "MAP_STR_STR", vj_state_swiss_str_str_iter)
  VJ_DEFINE_MAP_SWISS_OP_SM(vj_op_map_str_int, "MAP_STR_INT", vj_state_swiss_str_int_iter)
  VJ_DEFINE_MAP_SWISS_OP_SM(vj_op_map_str_int64, "MAP_STR_INT64", vj_state_swiss_str_int_iter)

#undef VJ_DEFINE_MAP_SWISS_OP_SM

  VJ_DEFINE_SEQ_OP(vj_op_seq_string, "SEQ_STRING", vj_seq_iterate_string, 0)
  VJ_DEFINE_SEQ_OP(vj_op_seq_float64, "SEQ_FLOAT64", vj_seq_iterate_float64, 1)
  VJ_DEFINE_SEQ_OP(vj_op_seq_int, "SEQ_INT", vj_seq_iterate_int, 0)
  VJ_DEFINE_SEQ_OP(vj_op_seq_int64, "SEQ_INT64", vj_seq_iterate_int64, 0)

  /* ================================================================
   *  Swiss Map Key Iterator: MAP_STR_ITER / MAP_STR_ITER_END
   *
   *  Generic map[string]<value> encoding using C-native key iteration
   *  with VM-dispatched value body instructions.
   *
   *  Requires a map whose element Go stores INLINE in the slot. This walk
   *  addresses a value as group + elems_off + slot_idx * elem_stride and hands
   *  that address to the value body, so an element Go keeps behind a pointer
   *  (type larger than abi.MapMaxElemBytes) would be encoded from the pointer's
   *  own bytes. Emission is gated on that Go-side: the layout probe declines such
   *  a map, SlotSize stays 0, and canSwissMapIterInC routes it to the generic
   *  iteration, which dereferences properly. Nothing here can tell the two apart,
   *  since a stride is all this opcode receives.
   *
   *  MAP_STR_ITER (long, 16 bytes):
   *    operand_a: interleaved=slot_size, split=elem_stride
   *    operand_b: body byte length (excl MAP_STR_ITER_END)
   *
   *  MAP_STR_ITER_END (long, 16 bytes):
   *    operand_a: relative jump back offset (negative)
   *    operand_b: same as MAP_STR_ITER operand_a
   * ================================================================ */

vj_op_map_str_iter: {
  const VjOpExt *ext = VJ_OP_EXT(op);
  int32_t operand    = ext->operand_a;
  int32_t body_len   = ext->operand_b;

  /* Derive layout from flag + operand. */
  uint32_t _flags     = VJ_ST_GET_FLAGS(vmstate);
  int _split          = _flags & VJ_FLAGS_SPLIT_GROUP;
  int32_t key_stride  = _split ? SWISS_SPLIT_KEY_STRIDE : operand;
  int32_t elems_off   = _split ? SWISS_SPLIT_ELEMS_OFF : (int32_t)(SWISS_CTRL_SIZE + 16);
  int32_t elem_stride = _split ? operand : operand;
  int32_t group_size  = _split ? (SWISS_SPLIT_ELEMS_OFF + SWISS_GROUP_SLOTS * operand)
                               : (SWISS_CTRL_SIZE + SWISS_GROUP_SLOTS * operand);

  int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);
  int is_resume  = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));

  if (is_resume) {
    VjStackFrame *f     = &ctx->stack[_depth - 1];
    const GoSwissMap *m = (const GoSwissMap *)f->map.map_ptr;
    int32_t remaining   = f->map.remaining;

    /* Callee reads/writes f->map.{dir,group,slot}_idx directly; we skip the
     * local {di,gi,si} shuffle to prevent clang from spilling them. */
    VjSwissSlot slot = vj_swiss_next_full_slot(m, key_stride, group_size, f);
    if (slot.key_ptr == NULL || remaining <= 0) {
      goto map_str_iter_done_resume;
    }
    const GoString *k      = (const GoString *)slot.key_ptr;
    const uint8_t *val_ptr = slot.group + elems_off + f->map.slot_idx * elem_stride;

    {
      int ipad      = indent_step ? (1 + indent_prefix_len + indent_depth * indent_step) : 0;
      int key_space = indent_step ? 1 : 0;
      int64_t need  = 1 + ipad + key_space + 2 + (k->len * 6) + 1;
      VM_CHECK(need);
    }
    *buf++ = ',';
    if (indent_step) {
      VM_WRITE_INDENT();
    }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, k->ptr, k->len);
#else
    buf += VJ_ESCAPE_STRING_DISPATCH(buf, k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) {
      *buf++ = ' ';
    }

    /* dir_idx/group_idx are already up-to-date in the frame (callee
     * wrote them on hit); only slot_idx needs post-increment. */
    f->map.slot_idx  = (uint8_t)(f->map.slot_idx + 1);
    f->map.remaining = remaining - 1;

    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_TRACE_KEY("MAP_STR_ITER(resume)");
    VM_NEXT_LONG();
  }

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

    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));
    VM_WRITE_KEY();
    VM_TRACE_KEY("MAP_STR_ITER");
    *buf++ = '{';
    VM_INDENT_INC();
    VM_WRITE_INDENT();

    if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
    }
    VjStackFrame *frame  = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    frame->ret_base      = base;
    frame->map.map_ptr   = m;
    frame->map.remaining = (int32_t)m->used;
    frame->map.dir_idx   = 0;
    frame->map.group_idx = 0;
    frame->map.slot_idx  = 0;
    frame->state         = 0;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);

    /* frame->map.{dir,group,slot}_idx were zeroed above; callee updates
     * them in place on hit, so we skip the local {di,gi,si} shuffle. */
    VjSwissSlot slot       = vj_swiss_next_full_slot(m, key_stride, group_size, frame);
    const GoString *k      = (const GoString *)slot.key_ptr;
    const uint8_t *val_ptr = slot.group + elems_off + frame->map.slot_idx * elem_stride;

    /* Write first key (no comma) */
    {
      int key_space = indent_step ? 1 : 0;
      int64_t need  = 2 + (k->len * 6) + 1 + key_space;
      VM_CHECK(need);
    }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, k->ptr, k->len);
#else
    buf += VJ_ESCAPE_STRING_DISPATCH(buf, k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) {
      *buf++ = ' ';
    }

    /* Post-increment slot_idx only; dir/group already reflect the hit. */
    frame->map.slot_idx  = (uint8_t)(frame->map.slot_idx + 1);
    frame->map.remaining = (int32_t)m->used - 1;

    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_NEXT_LONG();
  }

map_str_iter_done_resume: {
  /* Resume path: no more entries */
  VjStackFrame *f = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  VM_WRITE_INDENT();
  *buf++ = '}';
  f->state &= ~1;
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  base        = f->ret_base;
  f->ret_base = NULL;
  VM_RESTORE_TRACE_DEPTH(f);
  VJ_ST_SET_FIRST_0(vmstate);
  /* Skip body + MAP_STR_ITER_END from current position */
  VM_JUMP_BYTES(16 + body_len + 16);
}
}

vj_op_map_str_iter_end: {
  const VjOpExt *ext = VJ_OP_EXT(op);
  int32_t operand    = ext->operand_b;

  /* Derive layout (same as MAP_STR_ITER). */
  uint32_t _flags     = VJ_ST_GET_FLAGS(vmstate);
  int _split          = _flags & VJ_FLAGS_SPLIT_GROUP;
  int32_t key_stride  = _split ? SWISS_SPLIT_KEY_STRIDE : operand;
  int32_t elems_off   = _split ? SWISS_SPLIT_ELEMS_OFF : (int32_t)(SWISS_CTRL_SIZE + 16);
  int32_t elem_stride = operand;
  int32_t group_size  = _split ? (SWISS_SPLIT_ELEMS_OFF + SWISS_GROUP_SLOTS * operand)
                               : (SWISS_CTRL_SIZE + SWISS_GROUP_SLOTS * operand);

  VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  int32_t remaining   = frame->map.remaining;

  if (remaining > 0) {
    const GoSwissMap *m = (const GoSwissMap *)frame->map.map_ptr;

    /* Callee reads/writes frame->map.{dir,group,slot}_idx in place. */
    VjSwissSlot slot = vj_swiss_next_full_slot(m, key_stride, group_size, frame);
    if (slot.key_ptr == NULL) {
      goto map_str_iter_end_done;
    }
    const GoString *k      = (const GoString *)slot.key_ptr;
    const uint8_t *val_ptr = slot.group + elems_off + frame->map.slot_idx * elem_stride;

    /* Check buffer space. On BUF_FULL, frame->map.{dir,group,slot}_idx are
     * already the current-slot indices; the resume path re-scans from there. */
    {
      int ipad      = indent_step ? (1 + indent_prefix_len + indent_depth * indent_step) : 0;
      int key_space = indent_step ? 1 : 0;
      int64_t need  = 1 + ipad + key_space + 2 + (k->len * 6) + 1;
      if (UNLIKELY(buf + need > bend)) {
        frame->state |= 1;
        VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
      }
    }

    *buf++ = ',';
    if (indent_step) {
      VM_WRITE_INDENT();
    }
#ifdef VJ_FAST_STRING_ESCAPE
    buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, k->ptr, k->len);
#else
    buf += VJ_ESCAPE_STRING_DISPATCH(buf, k->ptr, k->len, VJ_ST_GET_FLAGS(vmstate));
#endif
    *buf++ = ':';
    if (indent_step) {
      *buf++ = ' ';
    }

    frame->map.slot_idx  = (uint8_t)(frame->map.slot_idx + 1);
    frame->map.remaining = remaining - 1;

    base = val_ptr;
    VJ_ST_SET_FIRST_1(vmstate);
    VM_TRACE("MAP_STR_ITER_END(next)");
    VM_JUMP_BYTES(ext->operand_a);
  }

map_str_iter_end_done:
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  VM_WRITE_INDENT();
  *buf++ = '}';
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(frame);
  VM_TRACE("MAP_STR_ITER_END(done)");
  base            = frame->ret_base;
  frame->ret_base = NULL;
  VJ_ST_SET_FIRST_0(vmstate);
  VM_NEXT_LONG();
}

  /* COLD: map fallback / generic map iter / interface / yield */

vj_op_map: {
  VM_TRACE_KEY("MAP");
  /* Yield to Go for full map encoding. */
  VM_TRACE("YIELD(map_handoff)");
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_MAP_HANDOFF);
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

vj_op_interface: {
  VM_TRACE_KEY("INTERFACE");
  const uint8_t *iface_ptr = base + op->field_off;
  const void *type_ptr     = *(const void **)iface_ptr;

  /* nil interface → "null" (trivial, stays inline) */
  if (type_ptr == NULL) {
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_NEXT_SHORT();
  }

  /* Non-nil: resolve through the cache (inline binary search), pre-check
   * every failure condition, and only then write the key: no speculative
   * key write, no undo state.  The bulky primitive encode switch lives in
   * vj_iface_encode_primitive (eface.h, NOINLINE). */
  VM_CHECK(op->key_len + 1 + 330 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
  const VjIfaceCacheEntry *e = vj_iface_cache_lookup(ctx->iface_cache_ptr, ctx->iface_cache_count, type_ptr);
  if (UNLIKELY(e == NULL)) {
    /* Cache miss: yield for compilation. No key written. */
    ctx->yield_type_ptr = type_ptr;
    VM_TRACE_YIELD(op->op_type);
    VJ_ST_SET_YIELD(vmstate, VJ_YIELD_IFACE_MISS);
    VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
  }

  if (e->tag != 0) {
    /* Primitive: variable-length and NaN/Inf failure modes are checked
     * here, before the key write. */
    const uint8_t *data_ptr = *(const uint8_t **)(iface_ptr + 8);

    if (e->tag == OP_VALUE) {
      /* A boxed value.Value: the data word points at the heap copy. The
       * tape walk is resumable (frame-based), so it cannot run through
       * vj_iface_encode_primitive, which cannot fail; route it through the
       * keyed walk instead. On a window-full exit the op re-executes, the
       * cache hits again, and the resume check picks the walk back up. */
      leaf_walk.v    = *(const GoValue **)(iface_ptr + 8);
      leaf_walk.mode = 0;
      goto vj_state_value_walk_enter;
    }

    switch (e->tag) {
    case OP_FLOAT32: {
      float fval;
      __builtin_memcpy(&fval, data_ptr, 4);
      if (UNLIKELY(__builtin_isnan(fval) || __builtin_isinf(fval))) VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
      break;
    }
    case OP_FLOAT64: {
      double dval;
      __builtin_memcpy(&dval, data_ptr, 8);
      if (UNLIKELY(__builtin_isnan(dval) || __builtin_isinf(dval))) VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);
      break;
    }
    case OP_STRING: {
      const GoString *s = (const GoString *)data_ptr;
      int64_t need      = (int64_t)op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + 2 + (s->len * 6);
      if (UNLIKELY(buf + need > bend)) VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
      break;
    }
    case OP_RAW_MESSAGE: {
      const GoSlice *raw = (const GoSlice *)data_ptr;
      if (raw->data != NULL && raw->len > 0) {
        int64_t need = (int64_t)op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + raw->len;
        if (UNLIKELY(buf + need > bend)) VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
      }
      break;
    }
    case OP_NUMBER: {
      const GoString *s = (const GoString *)data_ptr;
      if (s->len > 0) {
        int64_t need = (int64_t)op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + s->len;
        if (UNLIKELY(buf + need > bend)) VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
      }
      break;
    }
    case OP_BOOL:
    case OP_INT:
    case OP_INT8:
    case OP_INT16:
    case OP_INT32:
    case OP_INT64:
    case OP_UINT:
    case OP_UINT8:
    case OP_UINT16:
    case OP_UINT32:
    case OP_UINT64:
      /* Fixed-size: the entry check above already guaranteed space. */
      break;
    default:
      /* Unknown tag: yield before writing key. */
      goto vj_op_yield;
    }

    VM_WRITE_KEY();
    buf = vj_iface_encode_primitive(buf, data_ptr, e->tag, VJ_ST_GET_FLAGS(vmstate));
    VM_NEXT_SHORT();
  }

  if (e->ops == NULL) {
    /* Not compilable, fall back to Go. No key written. */
    goto vj_op_yield;
  }

  /* SWITCH_OPS: cached Blueprint. Stack check before the key write. */
  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }

  VM_WRITE_KEY();

  {
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    frame->call.ret_ops = ops;
    frame->call.ret_pc  = (int32_t)((const uint8_t *)op - ops) + 8;
    frame->ret_base     = base;
    frame->state        = 0;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);
    ops  = e->ops;
    op   = (const VjOpHdr *)ops;
    base = (e->flags & VJ_IFACE_FLAG_INDIRECT) ? (iface_ptr + 8) : *(const uint8_t **)(iface_ptr + 8);
    VJ_ST_SET_FIRST_1(vmstate);
    VM_DISPATCH();
  }
}

vj_op_yield: {
  VM_TRACE_YIELD(op->op_type);
  VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
  /* The 'first' flag is preserved in vmstate; Go reads it directly. */
  VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
}

  /* Inline state machine for OP_MAP_STR_INT / OP_MAP_STR_INT64 */

vj_state_swiss_str_int_iter: {
  VjStackFrame *f     = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  const GoSwissMap *m = (const GoSwissMap *)f->map.map_ptr;
  VjSwissIndent ind   = {indent_tpl, indent_depth, indent_step, indent_prefix_len};
  VjSwissMapResult r =
      vj_swiss_iterate_str_int(buf, bend, f, m, f->map.remaining, f->map.dir_idx, f->map.group_idx,
                               f->map.slot_idx, f->map.entry_first, VJ_ST_GET_FLAGS(vmstate), &ind);
  buf = r.buf;
  if (UNLIKELY(r.action == VJ_SWISS_BUF_FULL)) {
    VM_SAVE_TRACE_DEPTH(f);
    f->state |= 1;
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  }
  goto vj_state_swiss_str_int_done;
}

vj_state_swiss_str_int_done: {
  VjStackFrame *f = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  VM_WRITE_INDENT();
  *buf++ = '}';
  f->state &= ~1;
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(f);
  VJ_ST_SET_FIRST_0(vmstate);
  VM_NEXT_SHORT();
}

  /* ---- str_str state machine (mirrors str_int, but value is GoString
   *      that also goes through escape, and layout consts use STR_STR) ---- */

vj_state_swiss_str_str_iter: {
  VjStackFrame *f     = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  const GoSwissMap *m = (const GoSwissMap *)f->map.map_ptr;
  VjSwissIndent ind   = {indent_tpl, indent_depth, indent_step, indent_prefix_len};
  VjSwissMapResult r =
      vj_swiss_iterate_str_str(buf, bend, f, m, f->map.remaining, f->map.dir_idx, f->map.group_idx,
                               f->map.slot_idx, f->map.entry_first, VJ_ST_GET_FLAGS(vmstate), &ind);
  buf = r.buf;
  if (UNLIKELY(r.action == VJ_SWISS_BUF_FULL)) {
    VM_SAVE_TRACE_DEPTH(f);
    f->state |= 1;
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  }
  goto vj_state_swiss_str_str_done;
}

vj_state_swiss_str_str_done: {
  VjStackFrame *f = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  VM_CHECK_CLOSE();
  VM_INDENT_DEC();
  VM_WRITE_INDENT();
  *buf++ = '}';
  f->state &= ~1;
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(f);
  VJ_ST_SET_FIRST_0(vmstate);
  VM_NEXT_SHORT();
}

vj_state_encode_base64: {
  buf = vj_encode_base64(buf, bend, leaf_b64.data, leaf_b64.len);
  VM_NEXT_SHORT();
}

vj_state_prescan_escaped: {
  /* Both entry ops (STRING/KSTRING) locate the string at base + field_off. */
  const GoString *s = (const GoString *)(base + op->field_off);
  int64_t overhead  = 1 + op->key_len + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE;
  int64_t tight =
      overhead + vj_prescan_string_escaped_len((const uint8_t *)s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
  if (UNLIKELY(buf + tight > bend)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  }
  VM_WRITE_KEY();
#ifdef VJ_FAST_STRING_ESCAPE
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, s->ptr, s->len);
#else
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, s->ptr, s->len, VJ_ST_GET_FLAGS(vmstate));
#endif
  VM_NEXT_SHORT();
}

  /* ---- value.Value tape walk state machine (shared by OP_VALUE, the
   *      OP_INTERFACE interception of boxed Values, and OP_VALUE_SPREAD) ---- */

vj_state_value_walk_enter: {
  const GoValue *v = (const GoValue *)leaf_walk.v;
  const int spread = (int)leaf_walk.mode;

  /* Resume: a walk frame on top means this op re-executes mid-walk after a
   * window-full exit. The frame is uniquely recognizable by its state bits,
   * so an enclosing loop frame below never aliases. */
  {
    int32_t d = VJ_ST_GET_STACK_DEPTH(vmstate);
    if (d > 0 && (ctx->stack[d - 1].state & (VJ_FRAME_STATE_ACTIVE | VJ_FRAME_STATE_WALK)) ==
                     (VJ_FRAME_STATE_ACTIVE | VJ_FRAME_STATE_WALK)) {
      goto vj_state_value_walk_run;
    }
  }

  if (v->doc == NULL || v->doc->tape.len == 0) {
    /* Zero Value: null in full mode, nothing in spread mode. */
    if (spread) VM_NEXT_SHORT();
    VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE);
    VM_WRITE_KEY();
    __builtin_memcpy(buf, "null", 4);
    buf += 4;
    VM_NEXT_SHORT();
  }

  /* Guards before any write. Indent mode bounds the walk by the template
   * depth and the kindstack popcount, so it prescans the nesting first;
   * deeper values yield to Go, whose recursive walk has no depth bound.
   * Compact mode needs no guard: the walk's exit is cursor-bounded, so a
   * kindstack shifted past 16 containers cannot truncate output. */
#ifndef VJ_COMPACT_INDENT
  {
    int32_t maxd  = vj_tape_max_depth(v);
    int32_t extra = maxd - (spread ? 1 : 0);
    if (UNLIKELY(maxd > 16 || (indent_step && (int32_t)indent_depth + extra > VJ_MAX_STACK_DEPTH))) {
      VM_TRACE_YIELD(op->op_type);
      VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
      VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
    }
  }
#endif

  if (UNLIKELY(VJ_ST_GET_STACK_DEPTH(vmstate) >= VJ_MAX_STACK_DEPTH)) {
    VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);
  }

  if (!spread) {
    /* Prefix: the op's key (or the host element comma when keyless); the
     * walk's own separators are frame-internal. On a window-full exit the
     * frame resume path above skips this. */
    VM_CHECK(op->key_len + 1 + VM_INDENT_PAD(indent_depth) + VM_KEY_SPACE + 1);
    VM_WRITE_KEY();
  } else {
    /* Spread requires an object; anything else is a misuse of a decode-side
     * construct, so yield and let the Go handler report it. */
    uint64_t rootw = ((const uint64_t *)v->doc->tape.data)[(size_t)(v->base + v->tidx)];
    if ((rootw & 0xFF00000000000000ULL) != VJ_TOBJ_BEG) {
      VM_TRACE_YIELD(op->op_type);
      VJ_ST_SET_YIELD(vmstate, VJ_YIELD_FALLBACK);
      VM_SAVE_AND_RETURN(VJ_EXIT_YIELD);
    }
  }

  {
    VjStackFrame *frame = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate)];
    VjTapeWalkCtx c0;
    c0.tape               = (const uint64_t *)v->doc->tape.data;
    c0.base               = v->base;
    c0.shift              = (uint32_t)v->mode & VJ_TVIEW_SHIFT_MASK;
    frame->ret_base       = base;
    frame->walk.value     = v;
    frame->walk.kindstack = spread ? 0b11u : 0u;
    if (spread) {
      frame->walk.cursor = vj_tw_skip_seams(&c0, v->tidx + 1);
      frame->walk.flags  = VJ_WALK_SPREAD | (VJ_ST_GET_FIRST(vmstate) ? VJ_WALK_HOST_FIRST : 0);
    } else {
      frame->walk.cursor = v->tidx;
      frame->walk.flags  = 0;
    }
    frame->state = VJ_FRAME_STATE_WALK;
    VM_SAVE_TRACE_DEPTH(frame);
    VJ_ST_INC_STACK_DEPTH(vmstate);
  }
  goto vj_state_value_walk_run;
}

vj_state_value_walk_run: {
  VjStackFrame *f  = &ctx->stack[VJ_ST_GET_STACK_DEPTH(vmstate) - 1];
  const GoValue *v = (const GoValue *)f->walk.value;

  VjTapeWalkCtx c;
  c.tape      = (const uint64_t *)v->doc->tape.data;
  c.str_arena = (const uint8_t *)v->doc->str_arena.data;
  c.src       = (const uint8_t *)v->doc->src.data;
  c.base      = v->base;
  c.shift     = (uint32_t)v->mode & VJ_TVIEW_SHIFT_MASK;
  c.flags     = VJ_ST_GET_FLAGS(vmstate) &
                ~(uint32_t)(VJ_FLAGS_ESCAPE_HTML | VJ_FLAGS_ESCAPE_LINE_TERMS | VJ_FLAGS_ESCAPE_INVALID_UTF8);
  VjSwissIndent ind = {indent_tpl, indent_depth, indent_step, indent_prefix_len};

  int action = vj_tape_walk_step(&buf, bend, f, &c, &ind);
  if (UNLIKELY(action == VJ_TAPE_WALK_BUF_FULL)) {
    f->state |= VJ_FRAME_STATE_ACTIVE;
    VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);
  }

  /* Done. Full mode always wrote the value, so the host's next field gets a
   * comma. Spread mode clears the latch only when a member was emitted: an
   * empty spread leaves the host's first-element state untouched. */
  int spread_done = (f->walk.flags & VJ_WALK_SPREAD) != 0;
  VJ_ST_DEC_STACK_DEPTH(vmstate);
  VM_RESTORE_TRACE_DEPTH(f);
  if (!spread_done || (f->walk.flags & VJ_WALK_WROTE_ANY)) {
    VJ_ST_SET_FIRST_0(vmstate);
  }
  VM_NEXT_SHORT();
}

vj_state_write_int64: {
  buf += write_int64(buf, leaf_intval);
  VM_NEXT_SHORT();
}

vj_state_write_uint64: {
  buf += write_uint64(buf, (uint64_t)leaf_intval);
  VM_NEXT_SHORT();
}

/* Cleanup macros */
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
#undef VJ_COMPACT_INDENT
#undef indent_depth
#endif
#undef indent_tpl
#undef indent_step
#undef indent_prefix_len
}

#undef VM_SAVE_AND_RETURN

#endif /* VJ_ENCVM_H */
