#ifndef VJ_UTIL_H
#define VJ_UTIL_H

#include <stdint.h>

#ifdef __aarch64__
#include "sse2neon.h"
#else
#include <immintrin.h>
#endif

#define ALWAYS_INLINE static __attribute__((always_inline)) inline
#define NOINLINE      static __attribute__((noinline))

/* force_align_arg_pointer: emit AND $-16,%rsp prologue on x86-64 to fix
 * stack misalignment when called from Go ABI (RSP mod 16 == 0 instead of
 * SysV-required RSP mod 16 == 8).  No-op on non-x86-64 targets. */
#if defined(__x86_64__)
#define VJ_ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define VJ_ALIGN_STACK
#endif

#endif /* VJ_UTIL_H*/
