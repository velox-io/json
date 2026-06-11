/*
 * Minimal stderr logging
 *
 * Supported format specifiers:
 *   %s   — NUL-terminated string
 *   %d   — signed int (decimal)
 *   %u   — unsigned int (decimal)
 *   %x   — unsigned int (hex, lowercase)
 *   %p   — pointer (0x prefix + hex)
 *   %%   — literal '%'
 *
 */

#ifndef VJ_ENCVM_LOG_H
#define VJ_ENCVM_LOG_H

#include "types.h"
#include "vj_compat.h"

#ifdef VJ_ENCVM_DEBUG

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
  register long x8 __asm__("x8") = num;
  register long x0 __asm__("x0") = a1;
  register long x1 __asm__("x1") = a2;
  register long x2 __asm__("x2") = a3;
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

static inline char *vj_fmt_u32(char *end, uint32_t v) {
  do {
    *--end = '0' + (char)(v % 10);
    v /= 10;
  } while (v);
  return end;
}

static inline char *vj_fmt_i32(char *end, int32_t v) {
  uint32_t u = (v < 0) ? (uint32_t)(-(int64_t)v) : (uint32_t)v;
  end = vj_fmt_u32(end, u);
  if (v < 0)
    *--end = '-';
  return end;
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

/* NOINLINE entry point — implementation in log.c. */
int vj_fprintf_stderr(const char *fmt, ...);

#else /* !VJ_ENCVM_DEBUG */

/* Release builds: compile to no-op. */
static inline int vj_fprintf_stderr(const char *fmt, ...) {
  (void)fmt;
  return 0;
}

#endif /* VJ_ENCVM_DEBUG */

#endif /* VJ_ENCVM_LOG_H */
