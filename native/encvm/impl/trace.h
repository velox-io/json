/*
 * trace.h — Encoder VM debug trace (ring buffer + macros).
 *
 * Output format: indentation-based (2 spaces per depth level),
 * showing opcode label + field name when available.
 */

#ifndef VJ_ENCVM_TRACE_H
#define VJ_ENCVM_TRACE_H

#include "log.h"
#include "types.h"
#include "vj_compat.h"

#ifdef VJ_ENCVM_DEBUG

/* ---- Ring-buffer writers (static inline, inlined into noinline entries) ----
 */

static inline void vj_trace_write(VjTraceBuf *tb, const char *s, int n) {
  for (int i = 0; i < n; i++) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)s[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

static inline void vj_trace_str(VjTraceBuf *tb, const char *s) {
  while (*s) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)*s++;
    tb->head++;
    tb->total++;
  }
}

static inline void vj_trace_u64(VjTraceBuf *tb, uint64_t v) {
  char tmp[20];
  int n = 0;
  do {
    tmp[n++] = '0' + (char)(v % 10);
    v /= 10;
  } while (v);
  for (int i = n - 1; i >= 0; i--) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)tmp[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

static inline void vj_trace_indent(VjTraceBuf *tb, int32_t depth) {
  for (int32_t i = 0; i < depth * 2; i++) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = ' ';
    tb->head++;
  }
  if (depth > 0)
    tb->total += (uint32_t)(depth * 2);
}

/* ---- Extract field name from pre-encoded key ----
 * key_ptr points to a pre-encoded JSON key like: "name":
 * We extract the text between the first pair of double quotes. */

static inline void vj_trace_key_name(VjTraceBuf *tb, const char *key_ptr, uint16_t key_len) {
  /* Find opening quote */
  const char *p = key_ptr;
  const char *end = key_ptr + key_len;
  while (p < end && *p != '"')
    p++;
  if (p >= end)
    return;
  p++; /* skip opening quote */
  /* Write chars until closing quote */
  vj_trace_str(tb, " \"");
  while (p < end && *p != '"') {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)*p++;
    tb->head++;
    tb->total++;
  }
  vj_trace_str(tb, "\"");
}

/* ---- Noinline entry points (one CALL per trace site in the VM) ---- */

/* Simple label-only trace: indent + LABEL + newline. */
NOINLINE static void vj_trace_simple(VjTraceBuf *tb, const char *label, int32_t depth) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  vj_trace_str(tb, "\n");
}

/* Keyed opcode trace: indent + LABEL + " fieldname" (if key present) + newline.
 */
NOINLINE static void vj_trace_opkey(VjTraceBuf *tb, const char *label, int32_t depth, const VjOpHdr *op,
                                    const uint8_t *key_pool) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  if (op->key_len > 0 && key_pool) {
    vj_trace_key_name(tb, (const char *)(key_pool + op->key_off), op->key_len);
  }
  vj_trace_str(tb, "\n");
}

NOINLINE static void vj_trace_msg(VjTraceBuf *tb, const char *msg) {
  vj_trace_str(tb, msg);
  vj_trace_str(tb, "\n");
}

/* Element index marker: indent + [idx]: + newline. */
NOINLINE static void vj_trace_elem_idx(VjTraceBuf *tb, int32_t depth, uint64_t idx) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, "[");
  vj_trace_u64(tb, idx);
  vj_trace_str(tb, "]:\n");
}

/* Keyed opcode trace with collection length: indent + LABEL + " fieldname" + "
 * [count]" + newline. */
NOINLINE static void vj_trace_opkey_len(VjTraceBuf *tb, const char *label, int32_t depth, const VjOpHdr *op,
                                        const uint8_t *key_pool, uint64_t count) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  if (op->key_len > 0 && key_pool) {
    vj_trace_key_name(tb, (const char *)(key_pool + op->key_off), op->key_len);
  }
  vj_trace_str(tb, " [");
  vj_trace_u64(tb, count);
  vj_trace_str(tb, "]\n");
}

/* Yield trace: indent + yield label + field name (if key present) + newline.
 *
 * optnone: prevent clang from generating a jump table for the inner switch.
 * Jump tables contain absolute pointers that require relocations, but our
 * .syso has zero relocations — the Go linker cannot fix them up.
 * This is debug-only code so the optimization loss is irrelevant. */
NOINLINE OPTNONE static void vj_trace_yield(VjTraceBuf *tb, uint16_t op_type, int32_t depth, const VjOpHdr *op,
                                            const uint8_t *key_pool) {
  const char *label;
  switch (op_type) {
  case OP_FALLBACK:
    label = "YIELD(fallback)";
    break;
  case OP_INTERFACE:
    label = "YIELD(interface)";
    break;
  case OP_BYTE_SLICE:
    label = "YIELD(byte_slice)";
    break;
  default:
    label = "YIELD(other)";
    break;
  }
  vj_trace_opkey(tb, label, depth, op, key_pool);
}

/* ---- High-level macros (guard on tbuf, delegate to noinline fn) ---- */

/* Simple label-only trace — no key, no pc, no base. */
#define VM_TRACE(label)                                                                                                \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_simple(tbuf, label, VM_TRACE_DEPTH());                                                                  \
  } while (0)

/* Keyed opcode trace — prints field name from key pool if present. */
#define VM_TRACE_KEY(label)                                                                                            \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_opkey(tbuf, label, VM_TRACE_DEPTH(), op, key_pool);                                                     \
  } while (0)

#define VM_TRACE_MSG(msg)                                                                                              \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_msg(tbuf, msg);                                                                                         \
  } while (0)

#define VM_TRACE_ELEM_IDX(idx)                                                                                         \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_elem_idx(tbuf, VM_TRACE_DEPTH(), (uint64_t)(idx));                                                      \
  } while (0)

#define VM_TRACE_KEY_LEN(label, count)                                                                                 \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_opkey_len(tbuf, label, VM_TRACE_DEPTH(), op, key_pool, (uint64_t)(count));                              \
  } while (0)

#define VM_TRACE_YIELD(op_type)                                                                                        \
  do {                                                                                                                 \
    if (tbuf)                                                                                                          \
      vj_trace_yield(tbuf, (uint16_t)(op_type), VM_TRACE_DEPTH(), op, key_pool);                                       \
  } while (0)

#else /* !VJ_ENCVM_DEBUG */

#define VM_TRACE(label)                ((void)0)
#define VM_TRACE_KEY(label)            ((void)0)
#define VM_TRACE_MSG(msg)              ((void)0)
#define VM_TRACE_ELEM_IDX(idx)         ((void)0)
#define VM_TRACE_KEY_LEN(label, count) ((void)0)
#define VM_TRACE_YIELD(op_type)        ((void)0)

#endif /* VJ_ENCVM_DEBUG */

#endif /* VJ_ENCVM_TRACE_H */
