/*
 * Generic assert fallback stubs for syso builds.
 *
 * These stubs live next to memory.c in the shared stdlib but are wired
 * per consumer through STDLIB_SOURCES. The primary defense is building
 * syso sources with -DNDEBUG so assert(...) disappears during
 * preprocessing. This file only provides fallback symbols for cases
 * where that does not happen, such as a missing NDEBUG define or a
 * debug build with NO_NDEBUG=1.
 *
 * The syso link uses -nostdlib, so there is no libc fallback. Reaching
 * either entry point is treated as fatal and ends in __builtin_trap().
 *
 * Current reference consumer: ndec. encvm does not use this file.
 */

#include "macros.h"

#if defined(__APPLE__)
/* macOS assert failure entry point: __assert_rtn(func, file, line, expr). */
HIDDEN void
vj_assert_rtn_impl(const char *func, const char *file, int line,
                   const char *expr) __asm__("___assert_rtn");

HIDDEN void
vj_assert_rtn_impl(const char *func, const char *file, int line,
                   const char *expr) {
  (void)func;
  (void)file;
  (void)line;
  (void)expr;
  __builtin_trap();
}
#else
/* glibc assert failure entry point: __assert_fail(expr, file, line, func). */
HIDDEN void
vj_assert_fail_impl(const char *expr, const char *file, unsigned int line,
                    const char *func) __asm__("__assert_fail");

HIDDEN void
vj_assert_fail_impl(const char *expr, const char *file, unsigned int line,
                    const char *func) {
  (void)expr;
  (void)file;
  (void)line;
  (void)func;
  __builtin_trap();
}
#endif
