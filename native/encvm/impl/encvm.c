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
#if !defined(ISA_NEON) && !defined(ISA_SSE42) && !defined(ISA_AVX2) &&         \
    !defined(ISA_AVX512)
#error                                                                         \
    "ISA must be defined (use -DISA_NEON, -DISA_SSE42, -DISA_AVX2, or -DISA_AVX512)"
#endif

/* ---------- Mode configuration ----------
 * Translate build-system MODE_* flags into engine-internal macros.
 *
 * VJ_FAST_STRING_ESCAPE: when defined, the VM unconditionally uses
 *   the fast string escape path (no HTML/UTF-8/line-terminator
 *   checks).  All runtime flag dispatch for string escaping is
 *   eliminated at compile time.
 *
 * VJ_COMPACT_INDENT: when defined, all indent-related variables are
 *   replaced with compile-time constants (indent_step=0, etc.),
 *   allowing the compiler to DCE all indent code paths. */
#if defined(MODE_FAST)
#define VJ_FAST_STRING_ESCAPE
#define VJ_COMPACT_INDENT
#elif defined(MODE_COMPACT)
#define VJ_COMPACT_INDENT
#endif

/* ---------- Engine implementation ---------- */
#include "encvm.h"

/* ---------- Mode + ISA suffixed entry point ----------
 * vj_vm_exec is static (defined in encvm.h).  Here we generate a
 * non-static wrapper whose symbol name carries both the mode and ISA
 * suffix (e.g. vj_vm_exec_default_neon, vj_vm_exec_fast_sse42).
 * The Go trampoline references this suffixed symbol.
 *
 * Stack alignment (x86-64 only):
 * The Go compiler generates an internal ABI wrapper around the Plan9
 * asm trampoline (vjVMExecFast*.abi0).  This wrapper's stack frame
 * layout causes RSP at the .abi0 entry — and therefore at this C
 * wrapper entry after JMP — to be 16-byte aligned (RSP mod 16 == 0),
 * whereas the x86-64 SysV ABI requires RSP mod 16 == 8 at function
 * entry (i.e. RSP+8 is 16-aligned, accounting for the CALL return
 * address).  Verified with dlv: RSP == 0x...ac0 (mod 16 == 0) at
 * vj_vm_exec_fast_sse42 entry.
 *
 * This 8-byte misalignment causes the C compiler's generated movdqa
 * (aligned 128-bit SSE store to stack) to fault with SIGSEGV.
 *
 * We use force_align_arg_pointer rather than adjusting SP in the
 * Plan9 asm trampoline because the Go ABI wrapper's stack layout is
 * an implementation detail of the Go compiler — it depends on the
 * number/size of arguments, whether the function is direct or
 * indirect, and may change across Go versions.  force_align_arg_pointer
 * unconditionally emits AND $-16,%rsp regardless of the incoming SP
 * alignment, making it robust against any upstream layout changes. */

#if defined(__x86_64__)
#define VJ_ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define VJ_ALIGN_STACK
#endif

#if defined(MODE_FAST)
#define VJ_MODE_TAG fast
#elif defined(MODE_COMPACT)
#define VJ_MODE_TAG compact
#elif defined(MODE_DEFAULT)
#define VJ_MODE_TAG default
#else
#define VJ_MODE_TAG default
#endif

/* Two-level expansion so VJ_MODE_TAG is expanded before pasting. */
#define VJ_VM_EXEC_NAME2(mode, isa) vj_vm_exec_##mode##_##isa
#define VJ_VM_EXEC_NAME(mode, isa) VJ_VM_EXEC_NAME2(mode, isa)

#define GEN_VM_EXEC(isa)                                                       \
  VJ_ALIGN_STACK void VJ_VM_EXEC_NAME(VJ_MODE_TAG, isa)(VjExecCtx * ctx) {     \
    vj_vm_exec(ctx);                                                           \
  }

#if defined(ISA_NEON)
GEN_VM_EXEC(neon)
#elif defined(ISA_SSE42)
GEN_VM_EXEC(sse42)
#elif defined(ISA_AVX2)
GEN_VM_EXEC(avx2)
#elif defined(ISA_AVX512)
GEN_VM_EXEC(avx512)
#endif
