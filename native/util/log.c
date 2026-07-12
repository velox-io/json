/*
 * Minimal stderr logging (util library)
 *
 * vj_fprintf_stderr: minimal printf-like formatter that writes directly
 * to stderr via raw syscalls (no libc). Safe to call from NOSPLIT Go
 * trampolines and any C state machine.
 *
 * Format specifiers: %s %d %u %x %p %%
 * Length modifiers : l, ll, z (all promote to 64-bit on Go's targets;
 *                    one shared 64-bit print path).
 *
 * Compile with -DVJ_DEBUG to enable; otherwise the header's inline stub
 * compiles this TU's callsite to a no-op and the linker drops log.o.
 */

#include "log.h"

#ifdef VJ_LOG_ENABLED

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
    /* Length modifier: l, ll, or z. We only track "is it 64-bit?" because
     * on every Go-supported target long, long long, and size_t are all 64
     * bits. h / hh are not supported (callers use plain %d / %u). */
    const char *spec_start = p - 1; /* points at the '%' */
    int is_long            = 0;
    if (*p == 'l') {
      is_long = 1;
      p++;
      if (*p == 'l')
        p++;
    } else if (*p == 'z') {
      is_long = 1;
      p++;
    }

    /* Trailing '%' or '%l' / '%ll' / '%z' with no conversion char: emit
     * the partial directive verbatim and let the outer loop terminate. */
    if (*p == '\0') {
      int n = (int)(p - spec_start);
      vj_raw_write_stderr(spec_start, n);
      total += n;
      span = p;
      break;
    }

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
      int64_t v = is_long ? va_arg(ap, int64_t) : (int64_t)va_arg(ap, int32_t);
      start     = vj_fmt_i64(end, v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case 'u': {
      uint64_t v = is_long ? va_arg(ap, uint64_t) : (uint64_t)va_arg(ap, uint32_t);
      start      = vj_fmt_u64(end, v);
      vj_raw_write_stderr(start, (int)(end - start));
      total += (int)(end - start);
      break;
    }
    case 'x': {
      uint64_t v = is_long ? va_arg(ap, uint64_t) : (uint64_t)va_arg(ap, uint32_t);
      start      = vj_fmt_hex64(end, v);
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
    default: {
      /* Print the entire unknown directive verbatim (% + any length
       * modifier + the trailing char) so misuses are obvious in trace
       * output rather than silently consuming a vararg. */
      int n = (int)(p - spec_start + 1);
      vj_raw_write_stderr(spec_start, n);
      total += n;
      break;
    }
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

#endif /* VJ_LOG_ENABLED */
