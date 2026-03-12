/*
 * log.h — Velox JSON C Engine: Minimal stderr logging
 *
 *
 * Supported format specifiers:
 *   %s   — NUL-terminated string
 *   %d   — signed int (decimal)
 *   %u   — unsigned int (decimal)
 *   %x   — unsigned int (hex, lowercase)
 *   %p   — pointer (0x prefix + hex)
 *   %%   — literal '%'
 */

#ifndef VJ_ENCVM_LOG_H
#define VJ_ENCVM_LOG_H

#ifdef VJ_ENCVM_DEBUG

int vj_fprintf_stderr(const char *fmt, ...);

#else

static inline int vj_fprintf_stderr(const char *fmt, ...) {
  (void)fmt;
  return 0;
}

#endif /* VJ_ENCVM_DEBUG */

#endif /* VJ_ENCVM_LOG_H */
