/*
 * Freestanding <assert.h> replacement for cross builds without a target
 * SDK. windows syso builds run clang with --target=x86_64-pc-windows-msvc
 * -ffreestanding on a host that has no MSVC headers, and assert.h is the
 * only hosted libc header the syso sources include (stdint.h, stddef.h,
 * immintrin.h, ... are clang builtins). gen-natives.sh puts this directory
 * on -isystem for the windows target only; darwin/linux keep their
 * platform assert.h and route assert failures through the __assert_rtn /
 * __assert_fail aliases in assert.c.
 */
#ifndef VJ_STDLIB_ASSERT_H
#define VJ_STDLIB_ASSERT_H

#undef assert

#ifdef NDEBUG
#define assert(expr) ((void)0)
#else
/* Fatal trap entry point, provided by assert.c for windows targets. */
__attribute__((__noreturn__)) void
vj_assert_fail(const char *expr, const char *file, int line);

#define assert(expr)                                                       \
  ((expr) ? (void)0 : vj_assert_fail(#expr, __FILE__, __LINE__))
#endif

#endif /* VJ_STDLIB_ASSERT_H */
