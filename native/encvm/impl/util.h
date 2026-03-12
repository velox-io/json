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

#endif /* VJ_UTIL_H*/
