/*
 * seqiter.h — C-native sequence iterators for primitive slice/array encoding.
 *
 * Single-instruction loop: one opcode encodes the entire [elem,elem,...] sequence,
 * eliminating per-element VM dispatch overhead.
 *
 * Uses a macro template (VJ_DEFINE_SEQ_ITERATE) to generate noinline functions
 * for each element type, parameterized by encode logic and buffer-need calculation.
 *
 * Resume protocol: on BUF_FULL the caller saves iter state in VjStackFrame.seq,
 * then re-enters the same opcode handler which calls the iterate function with
 * the saved resume index.
 */

#ifndef VJ_ENCVM_SEQITER_H
#define VJ_ENCVM_SEQITER_H

#include "types.h"
#include "number.h"
#include "strfn.h"
#include "uscale.h"

// clang-format off

/* ================================================================
 *  Indent helpers (mirror swissmap.h pattern)
 * ================================================================ */

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

/* ================================================================
 *  Result struct and action codes
 * ================================================================ */

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

/* ================================================================
 *  Macro template: generates a specialized iterate function
 *
 *  Parameters:
 *    FN_NAME        — function name (e.g. vj_seq_iterate_float64)
 *    ELEM_SIZE      — sizeof one element (e.g. 8 for float64/int64)
 *    ENCODE_ONE     — macro/inline: ENCODE_ONE(buf, elem_ptr, flags, fmt)
 *                     returns new buf position; for float64 sets *nan_inf on error
 *    CALC_NEED      — macro: CALC_NEED(elem_ptr, ipad) → int64_t max bytes needed
 *    HAS_NAN_CHECK  — 1 if ENCODE_ONE can produce NaN/Inf errors, 0 otherwise
 * ================================================================ */

#define VJ_DEFINE_SEQ_ITERATE(FN_NAME, ELEM_SIZE, ENCODE_ONE, CALC_NEED,    \
                              HAS_NAN_CHECK)                                 \
__attribute__((noinline)) static VjSeqResult                                 \
FN_NAME(uint8_t *buf, const uint8_t *bend,                                  \
        const uint8_t *data, int64_t count, int32_t start_idx,              \
        uint32_t flags, const VjSeqIndent *ind) {                           \
  int ipad = vj_seq_indent_pad(ind);                                        \
                                                                             \
  for (int32_t i = start_idx; i < (int32_t)count; i++) {                    \
    const uint8_t *elem_ptr = data + (int64_t)i * (ELEM_SIZE);             \
    int64_t need = CALC_NEED(elem_ptr, ipad);                               \
    if (__builtin_expect(buf + need > bend, 0)) {                           \
      return (VjSeqResult){buf, VJ_SEQ_BUF_FULL, i};                       \
    }                                                                        \
    /* Write comma + indent (skip for first element, i == 0 && start == 0   \
     * is the first call; on resume start_idx > 0 so always write comma) */ \
    if (i > 0) {                                                             \
      *buf++ = ',';                                                          \
      buf = vj_seq_write_indent(buf, ind);                                  \
    }                                                                        \
    if (HAS_NAN_CHECK) {                                                     \
      int _nan_inf = 0;                                                      \
      buf = ENCODE_ONE(buf, elem_ptr, flags, &_nan_inf);                    \
      if (__builtin_expect(_nan_inf, 0)) {                                   \
        return (VjSeqResult){buf, VJ_SEQ_NAN_INF, i};                       \
      }                                                                      \
    } else {                                                                 \
      buf = ENCODE_ONE(buf, elem_ptr, flags, NULL);                         \
    }                                                                        \
  }                                                                          \
  return (VjSeqResult){buf, VJ_SEQ_DONE, 0};                               \
}

/* ================================================================
 *  Encode-one helpers
 * ================================================================ */

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

#define VJ_SEQ_NEED_FLOAT64(elem_ptr, ipad) \
  (1 + (ipad) + 330)

/* int / int64: encode as int64 */
static inline uint8_t *
vj_seq_encode_int64(uint8_t *buf, const uint8_t *elem_ptr,
                    uint32_t flags, int *nan_inf) {
  (void)flags; (void)nan_inf;
  int64_t val = *(const int64_t *)elem_ptr;
  buf += write_int64(buf, val);
  return buf;
}

#define VJ_SEQ_NEED_INT64(elem_ptr, ipad) \
  (1 + (ipad) + 21)

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

#define VJ_SEQ_NEED_STRING(elem_ptr, ipad) \
  (1 + (ipad) + 2 + (((const GoString *)(elem_ptr))->len * 6))

/* ================================================================
 *  Generate specialized iterate functions
 * ================================================================ */

VJ_DEFINE_SEQ_ITERATE(vj_seq_iterate_float64, 8,
                       vj_seq_encode_float64, VJ_SEQ_NEED_FLOAT64, 1)

VJ_DEFINE_SEQ_ITERATE(vj_seq_iterate_int, 8,
                       vj_seq_encode_int64, VJ_SEQ_NEED_INT64, 0)

VJ_DEFINE_SEQ_ITERATE(vj_seq_iterate_int64, 8,
                       vj_seq_encode_int64, VJ_SEQ_NEED_INT64, 0)

VJ_DEFINE_SEQ_ITERATE(vj_seq_iterate_string, 16,
                       vj_seq_encode_string, VJ_SEQ_NEED_STRING, 0)

#undef VJ_DEFINE_SEQ_ITERATE

#endif /* VJ_ENCVM_SEQITER_H */
