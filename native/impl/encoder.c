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
#if !defined(ISA_neon) && !defined(ISA_sse42) && !defined(ISA_avx2) && !defined(ISA_avx512)
  #error "ISA must be defined (use -DISA_neon, -DISA_sse42, -DISA_avx2, or -DISA_avx512)"
#endif

/* ---------- Engine implementation ---------- */
#include "encoder.h"

/* ---------- ISA-suffixed entry point ----------
 * vj_vm_exec is static (defined in encoder.h).  Here we generate a
 * single non-static wrapper whose symbol name carries the ISA suffix
 * (e.g. vj_vm_exec_neon, vj_vm_exec_sse42).  The Go trampoline
 * references this suffixed symbol. */
#define GEN_VM_EXEC(isa) \
  void vj_vm_exec_##isa(VjExecCtx *ctx) { vj_vm_exec(ctx); }

#if defined(ISA_neon)
GEN_VM_EXEC(neon)
#elif defined(ISA_sse42)
GEN_VM_EXEC(sse42)
#elif defined(ISA_avx2)
GEN_VM_EXEC(avx2)
#elif defined(ISA_avx512)
GEN_VM_EXEC(avx512)
#endif
