/*
 * trace.h — Encoder VM debug trace (ring buffer + macros).
 */

#ifndef VJ_ENCVM_TRACE_H
#define VJ_ENCVM_TRACE_H

#include "types.h"
#include "log.h"

#ifdef VJ_ENCVM_DEBUG

/* ---- Ring-buffer writers (static inline, inlined into noinline entries) ---- */

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

static inline void vj_trace_i32(VjTraceBuf *tb, int32_t v) {
  char tmp[12];
  int n = 0;
  uint32_t u;
  if (v < 0) {
    vj_trace_str(tb, "-");
    u = (uint32_t)(-(int64_t)v);
  } else {
    u = (uint32_t)v;
  }
  do { tmp[n++] = '0' + (char)(u % 10); u /= 10; } while (u);
  for (int i = n - 1; i >= 0; i--) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)tmp[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

static inline void vj_trace_u32(VjTraceBuf *tb, uint32_t v) {
  char tmp[11];
  int n = 0;
  do { tmp[n++] = '0' + (char)(v % 10); v /= 10; } while (v);
  for (int i = n - 1; i >= 0; i--) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)tmp[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

static inline void vj_trace_u64(VjTraceBuf *tb, uint64_t v) {
  char tmp[20];
  int n = 0;
  do { tmp[n++] = '0' + (char)(v % 10); v /= 10; } while (v);
  for (int i = n - 1; i >= 0; i--) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)tmp[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

static inline void vj_trace_ptr16(VjTraceBuf *tb, const void *p) {
  static const char hex[] = "0123456789abcdef";
  uint16_t lo = (uint16_t)(uintptr_t)p;
  char tmp[4] = {
      hex[(lo >> 12) & 0xF], hex[(lo >> 8) & 0xF],
      hex[(lo >> 4) & 0xF],  hex[lo & 0xF],
  };
  vj_trace_write(tb, tmp, 4);
}

static inline void vj_trace_indent(VjTraceBuf *tb, int32_t n) {
  for (int32_t i = 0; i < n; i++) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = '\t';
    tb->head++;
  }
  if (n > 0)
    tb->total += (uint32_t)n;
}

/* ---- Noinline entry points (one CALL per trace site in the VM) ---- */

__attribute__((noinline)) static void vj_trace_op(VjTraceBuf *tb,
                                                  const char *label, int32_t pc,
                                                  int32_t depth,
                                                  const void *base) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  vj_trace_str(tb, " pc=");
  vj_trace_i32(tb, pc);
  vj_trace_str(tb, " depth=");
  vj_trace_i32(tb, depth);
  vj_trace_str(tb, " base=0x");
  vj_trace_ptr16(tb, base);
  vj_trace_str(tb, "\n");
}

__attribute__((noinline)) static void vj_trace_msg(VjTraceBuf *tb,
                                                   const char *msg) {
  vj_trace_str(tb, msg);
  vj_trace_str(tb, "\n");
}

__attribute__((noinline)) static void
vj_trace_kv(VjTraceBuf *tb, const char *label, int32_t pc, int32_t depth,
            const char *key, int32_t val) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  vj_trace_str(tb, " pc=");
  vj_trace_i32(tb, pc);
  vj_trace_str(tb, " ");
  vj_trace_str(tb, key);
  vj_trace_str(tb, "=");
  vj_trace_i32(tb, val);
  vj_trace_str(tb, "\n");
}

__attribute__((noinline)) static void
vj_trace_push(VjTraceBuf *tb, const char *label, int32_t pc, int32_t depth,
              const void *old_base, const void *new_base) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, label);
  vj_trace_str(tb, " pc=");
  vj_trace_i32(tb, pc);
  vj_trace_str(tb, " depth=");
  vj_trace_i32(tb, depth);
  vj_trace_str(tb, " base=0x");
  vj_trace_ptr16(tb, old_base);
  vj_trace_str(tb, "->0x");
  vj_trace_ptr16(tb, new_base);
  vj_trace_str(tb, "\n");
}

__attribute__((noinline)) static void vj_trace_iter(VjTraceBuf *tb, int32_t pc,
                                                    int32_t depth, uint64_t idx,
                                                    uint64_t count,
                                                    const void *new_base) {
  vj_trace_indent(tb, depth);
  vj_trace_str(tb, "slice_next pc=");
  vj_trace_i32(tb, pc);
  vj_trace_str(tb, " idx=");
  vj_trace_u64(tb, idx);
  vj_trace_str(tb, "/");
  vj_trace_u64(tb, count);
  vj_trace_str(tb, " base=0x");
  vj_trace_ptr16(tb, new_base);
  vj_trace_str(tb, "\n");
}

__attribute__((noinline)) static void vj_trace_yield(VjTraceBuf *tb,
                                                     uint16_t op_type,
                                                     int32_t pc, int32_t depth,
                                                     const void *base) {
  const char *label;
  switch (op_type & OP_TYPE_MASK) {
  case OP_FALLBACK:   label = "yield(fallback)";   break;
  case OP_INTERFACE:  label = "yield(interface)";  break;
  case OP_BYTE_SLICE: label = "yield(byte_slice)"; break;
  default:            label = "yield(other)";      break;
  }
  vj_trace_op(tb, label, pc, depth, base);
}

/* ---- High-level macros (guard on tbuf, delegate to noinline fn) ---- */

#define VM_TRACE(label)                                                        \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_op(tbuf, label, (int32_t)(op - ops), VM_DEPTH(), base);         \
  } while (0)

#define VM_TRACE_MSG(msg)                                                      \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_msg(tbuf, msg);                                                 \
  } while (0)

#define VM_TRACE_KV(label, key, val)                                           \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_kv(tbuf, label, (int32_t)(op - ops), VM_DEPTH(), key,           \
                  (int32_t)(val));                                             \
  } while (0)

#define VM_TRACE_PUSH(label, old_base, new_base)                               \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_push(tbuf, label, (int32_t)(op - ops), VM_DEPTH(), old_base,    \
                    new_base);                                                 \
  } while (0)

#define VM_TRACE_ITER(idx, count, new_base)                                    \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_iter(tbuf, (int32_t)(op - ops), VM_DEPTH(), (uint64_t)(idx),    \
                    (uint64_t)(count), new_base);                              \
  } while (0)

#define VM_TRACE_YIELD(op_type)                                                \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_yield(tbuf, (uint16_t)(op_type), (int32_t)(op - ops),           \
                     VM_DEPTH(), base);                                        \
  } while (0)

#else /* !VJ_ENCVM_DEBUG */

#define VM_TRACE(label) ((void)0)
#define VM_TRACE_MSG(msg) ((void)0)
#define VM_TRACE_KV(label, key, val) ((void)0)
#define VM_TRACE_PUSH(label, old_base, new_base) ((void)0)
#define VM_TRACE_ITER(idx, count, new_base) ((void)0)
#define VM_TRACE_YIELD(op_type) ((void)0)

#endif /* VJ_ENCVM_DEBUG */

#endif /* VJ_ENCVM_TRACE_H */
