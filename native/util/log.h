/*
 * Minimal stderr logging
 *
 * Self-contained: Writes directly to stderr via raw syscalls, safe to
 * call from NOSPLIT Go trampolines and any C state machine.
 *
 * Compile log.c with -DVJ_DEBUG to enable; without it the entry point
 * compiles to a no-op.
 *
 * Supported format specifiers:
 *   %s            NUL-terminated string
 *   %d %u %x      32-bit signed/unsigned int (decimal/hex-lowercase)
 *   %ld %lu %lx   long
 *   %lld %llu %llx long long
 *   %zd %zu %zx   ssize_t / size_t
 *   %p            pointer (0x prefix + hex)
 *   %%            literal '%'
 *
 * Length modifiers l, ll, z all promote to 64-bit on every supported
 * target; reusing the 64-bit format paths keeps the formatter small.
 * Unrecognized specifiers print verbatim instead of consuming a vararg,
 * keeping unknown sequences obvious in trace output.
 */

#ifndef VJ_UTIL_LOG_H
#define VJ_UTIL_LOG_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"

/* Trigger on VJ_DEBUG, the generic debug flag shared by all native
 * components (encvm, ndec bindings, etc). Build with -DVJ_DEBUG to enable. */
#if defined(VJ_DEBUG)
#define VJ_LOG_ENABLED 1
#else
#undef VJ_LOG_ENABLED
#endif

#ifdef VJ_LOG_ENABLED

#include <stdarg.h>

/* Raw write syscall */

#if defined(__aarch64__)

#if defined(__APPLE__)
#define VJ_SYS_WRITE 4
#elif defined(__linux__)
#define VJ_SYS_WRITE 64
#endif

static inline long vj_raw_syscall3(long num, long a1, long a2, long a3) {
  register long x16 __asm__("x16") = num;
  register long x8 __asm__("x8")   = num;
  register long x0 __asm__("x0")   = a1;
  register long x1 __asm__("x1")   = a2;
  register long x2 __asm__("x2")   = a3;
  __asm__ volatile("svc #0x80" : "=r"(x0) : "r"(x16), "r"(x8), "r"(x0), "r"(x1), "r"(x2) : "memory", "cc");
  return x0;
}

#elif defined(__x86_64__)

#if defined(__APPLE__)
#define VJ_SYS_WRITE 0x2000004
#elif defined(__linux__)
#define VJ_SYS_WRITE 1
#endif

static inline long vj_raw_syscall3(long num, long a1, long a2, long a3) {
  long ret;
  __asm__ volatile("syscall" : "=a"(ret) : "a"(num), "D"(a1), "S"(a2), "d"(a3) : "rcx", "r11", "memory", "cc");
  return ret;
}

#else
#error "vj_raw_syscall3: unsupported architecture"
#endif

static inline void vj_raw_write_stderr(const char *buf, int len) {
  vj_raw_syscall3(VJ_SYS_WRITE, 2, (long)buf, (long)len);
}

/* Number formatting (stack-only, right-aligned) */

static inline char *vj_fmt_u64(char *end, uint64_t v) {
  do {
    *--end = '0' + (char)(v % 10);
    v /= 10;
  } while (v);
  return end;
}

static inline char *vj_fmt_u32(char *end, uint32_t v) {
  return vj_fmt_u64(end, (uint64_t)v);
}

static inline char *vj_fmt_i64(char *end, int64_t v) {
  uint64_t u;
  if (v < 0) {
    /* Avoid -INT64_MIN UB: cast first, then negate the unsigned value. */
    u = ~(uint64_t)v + 1u;
  } else {
    u = (uint64_t)v;
  }
  end = vj_fmt_u64(end, u);
  if (v < 0)
    *--end = '-';
  return end;
}

static inline char *vj_fmt_i32(char *end, int32_t v) {
  return vj_fmt_i64(end, (int64_t)v);
}

static inline char *vj_fmt_hex64(char *end, uint64_t v) {
  static const char hx[] = "0123456789abcdef";
  do {
    *--end = hx[v & 0xF];
    v >>= 4;
  } while (v);
  return end;
}

static inline char *vj_fmt_hex32(char *end, uint32_t v) {
  return vj_fmt_hex64(end, (uint64_t)v);
}

/* Prevent LTO from replacing this with a call to libc strlen. */
NO_BUILTIN_FUNC(strlen)
static inline int vj_strlen_debug(const char *s) {
  const char *p = s;
  while (*p)
    p++;
  return (int)(p - s);
}

/* NOINLINE entry point. Implementation in log.c. */
int vj_fprintf_stderr(const char *fmt, ...);

#else /* !VJ_LOG_ENABLED */

/* Release builds: compile to no-op. */
static inline int vj_fprintf_stderr(const char *fmt, ...) {
  (void)fmt;
  return 0;
}

#endif /* VJ_LOG_ENABLED */

#endif /* VJ_UTIL_LOG_H */
