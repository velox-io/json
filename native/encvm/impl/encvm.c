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

/* ---------- Mode configuration ----------
 * Translate build-system MODE_* flags into engine-internal macros.
 *
 * VJ_FAST_STRING_ESCAPE: when defined, the VM unconditionally uses
 *   the fast string escape path (no HTML/UTF-8/line-terminator
 *   checks).  All runtime flag dispatch for string escaping is
 *   eliminated at compile time. */
#if defined(MODE_fast)
  #define VJ_FAST_STRING_ESCAPE
#endif

/* ---------- Engine implementation ---------- */
#include "encvm.h"

/* ---------- Mode + ISA suffixed entry point ----------
 * vj_vm_exec is static (defined in encvm.h).  Here we generate a
 * non-static wrapper whose symbol name carries both the mode and ISA
 * suffix (e.g. vj_vm_exec_default_neon, vj_vm_exec_fast_sse42).
 * The Go trampoline references this suffixed symbol. */

#if defined(MODE_fast)
  #define VJ_MODE_TAG fast
#else
  #define VJ_MODE_TAG default
#endif

/* Two-level expansion so VJ_MODE_TAG is expanded before pasting. */
#define VJ_VM_EXEC_NAME2(mode, isa) vj_vm_exec_##mode##_##isa
#define VJ_VM_EXEC_NAME(mode, isa)  VJ_VM_EXEC_NAME2(mode, isa)

#define GEN_VM_EXEC(isa) \
  void VJ_VM_EXEC_NAME(VJ_MODE_TAG, isa)(VjExecCtx *ctx) { vj_vm_exec(ctx); }

#if defined(ISA_neon)
GEN_VM_EXEC(neon)
#elif defined(ISA_sse42)
GEN_VM_EXEC(sse42)
#elif defined(ISA_avx2)
GEN_VM_EXEC(avx2)
#elif defined(ISA_avx512)
GEN_VM_EXEC(avx512)
#endif
