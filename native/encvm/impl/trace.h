/*
 * trace.h — Velox JSON Encoder VM: Debug Trace Utilities
 *
 */

#ifndef VJ_ENCVM_TRACE_H
#define VJ_ENCVM_TRACE_H

#include "types.h"

#ifdef VJ_ENCVM_DEBUG

/* ---- Low-level ring-buffer writers ----
 * These remain static inline — they are small and only called from
 * the noinline entry points below, so they inline into those (not
 * into the VM function itself). */

/* Write n bytes from s into the trace ring buffer. */
static inline void vj_trace_write(VjTraceBuf *tb, const char *s, int n) {
  for (int i = 0; i < n; i++) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)s[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

/* Write a NUL-terminated string (excluding the NUL). */
static inline void vj_trace_str(VjTraceBuf *tb, const char *s) {
  while (*s) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)*s++;
    tb->head++;
    tb->total++;
  }
}

/* Write an int32 as decimal (no snprintf, no stack alloc beyond 12 bytes). */
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
  do {
    tmp[n++] = '0' + (char)(u % 10);
    u /= 10;
  } while (u);
  for (int i = n - 1; i >= 0; i--) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = (uint8_t)tmp[i];
    tb->head++;
  }
  tb->total += (uint32_t)n;
}

/* Write a uint32 as decimal. */
static inline void vj_trace_u32(VjTraceBuf *tb, uint32_t v) {
  char tmp[11];
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

/* Write a uint64 as decimal. */
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

/* Write low 16 bits of a pointer as 4-digit hex (e.g. "a3f0"). */
static inline void vj_trace_ptr16(VjTraceBuf *tb, const void *p) {
  static const char hex[] = "0123456789abcdef";
  uint16_t lo = (uint16_t)(uintptr_t)p;
  char tmp[4] = {
      hex[(lo >> 12) & 0xF],
      hex[(lo >> 8) & 0xF],
      hex[(lo >> 4) & 0xF],
      hex[lo & 0xF],
  };
  vj_trace_write(tb, tmp, 4);
}

/* Write N tab characters for indentation. */
static inline void vj_trace_indent(VjTraceBuf *tb, int32_t n) {
  for (int32_t i = 0; i < n; i++) {
    tb->data[tb->head & (VJ_TRACE_BUF_SIZE - 1)] = '\t';
    tb->head++;
  }
  if (n > 0)
    tb->total += (uint32_t)n;
}

/* ---- Noinline entry points ----
 * Each corresponds to one VM_TRACE* macro.  The noinline attribute
 * ensures the compiler emits a single CALL instruction per trace
 * site in the VM function, keeping the VM body small. */

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

__attribute__((noinline)) static void
vj_trace_yield(VjTraceBuf *tb, uint16_t op_type, int32_t pc, int32_t depth,
               const void *base) {
  const char *label;
  switch (op_type & OP_TYPE_MASK) {
  case OP_FALLBACK:
    label = "yield(fallback)";
    break;
  case OP_INTERFACE:
    label = "yield(interface)";
    break;
  case OP_BYTE_SLICE:
    label = "yield(byte_slice)";
    break;
  default:
    label = "yield(other)";
    break;
  }
  vj_trace_op(tb, label, pc, depth, base);
}

/* ---- High-level trace macros ----
 * Each macro guards on tbuf != NULL, then delegates to a single
 * noinline function.  The macro expansion in the VM function is
 * just: if (tbuf) { call <noinline_fn>(...); } */

/* Trace an opcode dispatch: "\t*depth label pc=N depth=D base=0xHHHH\n" */
#define VM_TRACE(label)                                                        \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_op(tbuf, label, (int32_t)(op - ops), depth, base);              \
  } while (0)

/* Trace a simple message (no PC). */
#define VM_TRACE_MSG(msg)                                                      \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_msg(tbuf, msg);                                                 \
  } while (0)

/* Trace with a key-value pair: "\t*depth label pc=N key=V\n" */
#define VM_TRACE_KV(label, key, val)                                           \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_kv(tbuf, label, (int32_t)(op - ops), depth, key,                \
                  (int32_t)(val));                                             \
  } while (0)

/* Trace frame push/pop: "\t*depth label pc=N depth=D base=0xHHHH->0xHHHH\n"
 * old_base/new_base show the base pointer change. */
#define VM_TRACE_PUSH(label, old_base, new_base)                               \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_push(tbuf, label, (int32_t)(op - ops), depth, old_base,         \
                    new_base);                                                 \
  } while (0)

/* Trace loop iteration: "\t*depth slice_next pc=N idx=I/C base=0xHHHH\n" */
#define VM_TRACE_ITER(idx, count, new_base)                                    \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_iter(tbuf, (int32_t)(op - ops), depth, (uint64_t)(idx),         \
                    (uint64_t)(count), new_base);                              \
  } while (0)

/* Trace yield reason based on opcode. */
#define VM_TRACE_YIELD(op_type)                                                \
  do {                                                                         \
    if (tbuf)                                                                  \
      vj_trace_yield(tbuf, (uint16_t)(op_type), (int32_t)(op - ops), depth,    \
                     base);                                                    \
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
