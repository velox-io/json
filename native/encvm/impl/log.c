/*
 * Minimal stderr logging
 *
 * vj_fprintf_stderr — minimal printf-like formatter that writes directly
 * to stderr via raw syscalls (no libc).  Used by VM_TRACE debug macros.
 * Format specifiers: %s %d %u %x %p %%
 */

#include "log.h"

#ifdef VJ_ENCVM_DEBUG

NOINLINE int vj_fprintf_stderr(const char *fmt, ...) {
  va_list ap;
  va_start(ap, fmt);

  int total        = 0;
  const char *span = fmt;

  for (const char *p = fmt; *p; p++) {
    if (*p != '%')
      continue;

    if (p > span) {
      int n = (int)(p - span);
      vj_raw_write_stderr(span, n);
      total += n;
    }

    p++;
    char tmp[24];
    char *end = tmp + sizeof(tmp);
    char *start;

    switch (*p) {
    case 's': {
      const char *s = va_arg(ap, const char *);
      if (!s)
        s = "(null)";
      int n = vj_strlen_debug(s);
      vj_raw_write_stderr(s, n);
      total += n;
      break;
    }
    case 'd': {
      int32_t v = va_arg(ap, int32_t);
      start     = vj_fmt_i32(end, v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case 'u': {
      uint32_t v = va_arg(ap, uint32_t);
      start      = vj_fmt_u32(end, v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case 'x': {
      uint32_t v = va_arg(ap, uint32_t);
      start      = vj_fmt_hex32(end, v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case 'p': {
      void *v = va_arg(ap, void *);
      vj_raw_write_stderr("0x", 2);
      total += 2;
      start = vj_fmt_hex64(end, (uint64_t)(uintptr_t)v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case '%':
      vj_raw_write_stderr("%", 1);
      total++;
      break;
    default:
      vj_raw_write_stderr(p - 1, 2);
      total += 2;
      break;
    }
    span = p + 1;
  }

  if (*span) {
    int n = vj_strlen_debug(span);
    vj_raw_write_stderr(span, n);
    total += n;
  }

  va_end(ap);
  return total;
}

#endif /* VJ_ENCVM_DEBUG */
