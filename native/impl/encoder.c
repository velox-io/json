#include <math.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

/* ---------- Build configuration validation ---------- */
#if !defined(OS)
  #error "OS must be defined (linux, darwin, or windows)"
#endif
#if !defined(ARCH)
  #error "ARCH must be defined (arm64 or amd64)"
#endif
#if !defined(ISA_neon) && !defined(ISA_sse42) && !defined(ISA_avx512)
  #error "ISA must be defined (use -DISA_neon, -DISA_sse42, or -DISA_avx512)"
#endif

/* ---------- Engine implementation ---------- */
#include "encoder.h"
