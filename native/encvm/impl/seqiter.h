/*
 * seqiter.h — C-native sequence iterators for primitive slice/array encoding.
 *
 * Single-instruction loop: one opcode encodes the entire [elem,elem,...],
 * eliminating per-element VM dispatch overhead.
 *
 * Architecture: always_inline generic impl + noinline specialized wrappers.
 * VJ_DEFINE_SEQ_OP macro generates VM opcode handlers (expanded inside
 * the VM dispatch function in encvm.h).
 */

#ifndef VJ_ENCVM_SEQITER_H
#define VJ_ENCVM_SEQITER_H

#include "number.h"
#include "strfn.h"
#include "types.h"
#include "uscale.h"

// clang-format off

/* --- Indent helpers --- */

typedef struct {
  const uint8_t *indent_tpl;
  int16_t        indent_depth;
  uint8_t        indent_step;
  uint8_t        indent_prefix_len;
} VjSeqIndent;

static inline uint8_t *vj_seq_write_indent(uint8_t *buf, const VjSeqIndent *ind) {
  if (ind->indent_step) {
    int n = 1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step;
    __builtin_memcpy(buf, ind->indent_tpl, n);
    buf += n;
  }
  return buf;
}

static inline int vj_seq_indent_pad(const VjSeqIndent *ind) {
  return ind->indent_step
           ? (1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step)
           : 0;
}

/* --- Result struct and action codes --- */

enum VjSeqAction {
  VJ_SEQ_DONE     = 0,
  VJ_SEQ_BUF_FULL = 1,
  VJ_SEQ_NAN_INF  = 2,
};

typedef struct {
  uint8_t *buf;
  int32_t  action;
  int32_t  resume_idx;
} VjSeqResult;

/* --- Encode-one helpers --- */

/* float64: encode with NaN/Inf check */
static inline uint8_t *
vj_seq_encode_float64(uint8_t *buf, const uint8_t *elem_ptr,
                      uint32_t flags, int *nan_inf) {
  double dval;
  __builtin_memcpy(&dval, elem_ptr, 8);
  if (__builtin_expect(__builtin_isnan(dval) || __builtin_isinf(dval), 0)) {
    *nan_inf = 1;
    return buf;
  }
  int fmt = (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? US_FMT_EXP_AUTO : US_FMT_FIXED;
  buf += us_write_float64(buf, dval, fmt);
  return buf;
}

/* int / int64: encode as int64 */
static inline uint8_t *
vj_seq_encode_int64(uint8_t *buf, const uint8_t *elem_ptr,
                    uint32_t flags, int *nan_inf) {
  (void)flags; (void)nan_inf;
  int64_t val = *(const int64_t *)elem_ptr;
  buf += write_int64(buf, val);
  return buf;
}

/* string: encode with escaping */
static inline uint8_t *
vj_seq_encode_string(uint8_t *buf, const uint8_t *elem_ptr,
                     uint32_t flags, int *nan_inf) {
  (void)nan_inf;
  const GoString *s = (const GoString *)elem_ptr;
#ifdef VJ_FAST_STRING_ESCAPE
  (void)flags;
  buf += vj_escape_string_fast(buf, (const uint8_t *)s->ptr, s->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)s->ptr, s->len, flags);
#endif
  return buf;
}

/* --- Need calculators --- */

static inline int64_t
vj_seq_need_float64(const uint8_t *elem_ptr, int ipad) {
  (void)elem_ptr;
  return 1 + ipad + 330;
}

static inline int64_t
vj_seq_need_int64(const uint8_t *elem_ptr, int ipad) {
  (void)elem_ptr;
  return 1 + ipad + 21;
}

static inline int64_t
vj_seq_need_string(const uint8_t *elem_ptr, int ipad) {
  const GoString *s = (const GoString *)elem_ptr;
  return 1 + ipad + 2 + (s->len * 6);
}

/* --- Callback types --- */
typedef uint8_t *(*vj_seq_encode_fn)(uint8_t *buf, const uint8_t *elem_ptr,
                                     uint32_t flags, int *nan_inf);
typedef int64_t  (*vj_seq_need_fn)(const uint8_t *elem_ptr, int ipad);

/* --- Core iteration logic (always_inline, parameterized).
 * Inlined into each noinline wrapper with constant params. --- */
__attribute__((always_inline)) static inline VjSeqResult
vj_seq_iterate_impl(uint8_t *buf, const uint8_t *bend,
                    const uint8_t *data, int64_t count, int32_t start_idx,
                    uint32_t flags, const VjSeqIndent *ind,
                    const int32_t elem_size,
                    const int has_nan_check,
                    vj_seq_encode_fn encode_one,
                    vj_seq_need_fn   calc_need)
{
  int ipad = vj_seq_indent_pad(ind);

  for (int32_t i = start_idx; i < (int32_t)count; i++) {
    const uint8_t *elem_ptr = data + (int64_t)i * elem_size;
    int64_t need = calc_need(elem_ptr, ipad);
    if (__builtin_expect(buf + need > bend, 0)) {
      return (VjSeqResult){buf, VJ_SEQ_BUF_FULL, i};
    }
    if (i > 0) {
      *buf++ = ',';
      buf = vj_seq_write_indent(buf, ind);
    }
    if (has_nan_check) {
      int nan_inf = 0;
      buf = encode_one(buf, elem_ptr, flags, &nan_inf);
      if (__builtin_expect(nan_inf, 0)) {
        return (VjSeqResult){buf, VJ_SEQ_NAN_INF, i};
      }
    } else {
      buf = encode_one(buf, elem_ptr, flags, NULL);
    }
  }
  return (VjSeqResult){buf, VJ_SEQ_DONE, 0};
}

/* --- Specialized noinline wrappers --- */

__attribute__((noinline)) static VjSeqResult
vj_seq_iterate_float64(uint8_t *buf, const uint8_t *bend,
                       const uint8_t *data, int64_t count, int32_t start_idx,
                       uint32_t flags, const VjSeqIndent *ind)
{
  return vj_seq_iterate_impl(buf, bend, data, count, start_idx, flags, ind,
                             8, 1, vj_seq_encode_float64, vj_seq_need_float64);
}

__attribute__((noinline)) static VjSeqResult
vj_seq_iterate_int(uint8_t *buf, const uint8_t *bend,
                   const uint8_t *data, int64_t count, int32_t start_idx,
                   uint32_t flags, const VjSeqIndent *ind)
{
  return vj_seq_iterate_impl(buf, bend, data, count, start_idx, flags, ind,
                             8, 0, vj_seq_encode_int64, vj_seq_need_int64);
}

__attribute__((noinline)) static VjSeqResult
vj_seq_iterate_int64(uint8_t *buf, const uint8_t *bend,
                     const uint8_t *data, int64_t count, int32_t start_idx,
                     uint32_t flags, const VjSeqIndent *ind)
{
  return vj_seq_iterate_impl(buf, bend, data, count, start_idx, flags, ind,
                             8, 0, vj_seq_encode_int64, vj_seq_need_int64);
}

__attribute__((noinline)) static VjSeqResult
vj_seq_iterate_string(uint8_t *buf, const uint8_t *bend,
                      const uint8_t *data, int64_t count, int32_t start_idx,
                      uint32_t flags, const VjSeqIndent *ind)
{
  return vj_seq_iterate_impl(buf, bend, data, count, start_idx, flags, ind,
                             16, 0, vj_seq_encode_string, vj_seq_need_string);
}

/* --- VM opcode handler macro.
 * Reads slice/array source, handles nil/empty, delegates to iterate fn.
 * On BUF_FULL pushes seq frame for resume.
 * Expanded inside the VM dispatch function in encvm.h. --- */

#define VJ_DEFINE_SEQ_OP(OP_LABEL, TRACE_LABEL, ITERATE_FN, HAS_NAN_CHECK)      \
OP_LABEL: {                                                                     \
  VM_TRACE_KEY(TRACE_LABEL);                                                    \
  int32_t _depth = VJ_ST_GET_STACK_DEPTH(vmstate);                              \
  int is_resume = (_depth > 0 && (ctx->stack[_depth - 1].state & 1));           \
                                                                                \
  const uint8_t *seq_data;                                                      \
  int64_t seq_count;                                                            \
  int32_t seq_start_idx;                                                        \
  int is_long;                                                                  \
                                                                                \
  if (is_resume) {                                                              \
    /* Resume from BUF_FULL: read saved state from frame */                     \
    VjStackFrame *f = &ctx->stack[_depth - 1];                                  \
    seq_data = f->seq.iter_data;                                                \
    seq_count = f->seq.iter_count;                                              \
    seq_start_idx = f->seq.iter_idx;                                            \
    is_long = (VJ_OP_EXT(op)->operand_a != 0);                                  \
  } else {                                                                      \
    /* First entry: determine slice vs array from operand_a */                  \
    const VjOpExt *ext = VJ_OP_EXT(op);                                         \
    is_long = (ext->operand_a != 0);                                            \
                                                                                \
    if (!is_long) {                                                             \
      /* Slice: GoSlice* at base+field_off */                                   \
      const GoSlice *sl = (const GoSlice *)(base + op->field_off);              \
      if (sl->data == NULL) {                                                   \
        VM_CHECK(op->key_len + 1 + 4 + VM_INDENT_PAD(indent_depth)              \
                 + VM_KEY_SPACE);                                               \
        VM_WRITE_KEY();                                                         \
        __builtin_memcpy(buf, "null", 4);                                       \
        buf += 4;                                                               \
        VM_NEXT_LONG();                                                         \
      }                                                                         \
      seq_data = sl->data;                                                      \
      seq_count = sl->len;                                                      \
    } else {                                                                    \
      /* Array: inline data at base+field_off */                                \
      int32_t packed = ext->operand_a;                                          \
      seq_count = (int64_t)((uint32_t)packed >> 16);                            \
      seq_data = base + op->field_off;                                          \
    }                                                                           \
                                                                                \
    if (seq_count == 0) {                                                       \
      VM_CHECK(op->key_len + 1 + 2 + VM_INDENT_PAD(indent_depth)                \
               + VM_KEY_SPACE);                                                 \
      VM_WRITE_KEY();                                                           \
      *buf++ = '[';                                                             \
      *buf++ = ']';                                                             \
      if (is_long) { VM_NEXT_LONG(); }                                          \
      else { VM_NEXT_LONG(); }                                                  \
    }                                                                           \
                                                                                \
    /* Write comma + key + '[' + indent */                                      \
    VM_CHECK(op->key_len + 1 + 1 + VM_INDENT_PAD(indent_depth)                  \
             + VM_KEY_SPACE + VM_INDENT_PAD(indent_depth + 1));                 \
    VM_WRITE_KEY();                                                             \
    *buf++ = '[';                                                               \
    VM_INDENT_INC();                                                            \
    VM_WRITE_INDENT();                                                          \
    seq_start_idx = 0;                                                          \
  }                                                                             \
                                                                                \
  /* Delegate iteration to out-of-line function */                              \
  {                                                                             \
    if (__builtin_expect(VJ_ST_GET_STACK_DEPTH(vmstate)                         \
                         >= VJ_MAX_STACK_DEPTH, 0)) {                           \
      VM_SAVE_AND_RETURN(VJ_EXIT_STACK_OVERFLOW);                               \
    }                                                                           \
    VjStackFrame *f = &ctx->stack[is_resume ? (_depth - 1) : _depth];           \
                                                                                \
    VjSeqIndent ind = {                                                         \
      (const uint8_t *)(indent_tpl),                                            \
      (int16_t)(indent_depth),                                                  \
      (uint8_t)(indent_step),                                                   \
      (uint8_t)(indent_prefix_len),                                             \
    };                                                                          \
                                                                                \
    VjSeqResult r = ITERATE_FN(                                                 \
        buf, bend, seq_data, seq_count, seq_start_idx,                          \
        VJ_ST_GET_FLAGS(vmstate), &ind);                                        \
    buf = r.buf;                                                                \
                                                                                \
    if (r.action == VJ_SEQ_BUF_FULL) {                                          \
      f->ret_base = base;                                                       \
      f->seq.iter_data = seq_data;                                              \
      f->seq.iter_count = seq_count;                                            \
      f->seq.iter_idx = r.resume_idx;                                           \
      VM_SAVE_TRACE_DEPTH(f);                                                   \
      f->state |= 1;                                                            \
      if (!is_resume) { VJ_ST_INC_STACK_DEPTH(vmstate); }                       \
      VM_SAVE_AND_RETURN(VJ_EXIT_BUF_FULL);                                     \
    }                                                                           \
                                                                                \
    if (HAS_NAN_CHECK && r.action == VJ_SEQ_NAN_INF) {                          \
      VM_SAVE_AND_RETURN(VJ_EXIT_NAN_INF);                                      \
    }                                                                           \
                                                                                \
    /* All elements encoded: write indent + ']' */                              \
    VM_INDENT_DEC();                                                            \
    VM_CHECK(1 + VM_INDENT_PAD(indent_depth));                                  \
    VM_WRITE_INDENT();                                                          \
    *buf++ = ']';                                                               \
                                                                                \
    if (is_resume) {                                                            \
      f->state &= ~1;                                                           \
      VJ_ST_DEC_STACK_DEPTH(vmstate);                                           \
      VM_RESTORE_TRACE_DEPTH(f);                                                \
    }                                                                           \
    VJ_ST_SET_FIRST_0(vmstate);                                                 \
    if (is_long) { VM_NEXT_LONG(); }                                            \
    else { VM_NEXT_LONG(); }                                                    \
  }                                                                             \
}

#endif /* VJ_ENCVM_SEQITER_H */
