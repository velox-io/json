#ifndef VJ_UTIL_H
#define VJ_UTIL_H

#include <stdint.h>

#ifdef __aarch64__
#include "sse2neon.h"
#else
#include <immintrin.h>
#endif

#include "vj_compat.h"

#endif /* VJ_UTIL_H*/
