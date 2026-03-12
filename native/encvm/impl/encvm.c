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

/* ---------- Entry-point symbol name ----------
 * Define VJ_VM_EXEC_FN_NAME before including encvm.h so that the VM
 * function body is emitted directly as the public entry point
 * (e.g. vj_vm_exec_full_sse42), eliminating the wrapper+jmp. */

#if defined(MODE_FAST)
#define VJ_MODE_TAG fast
#elif defined(MODE_COMPACT)
#define VJ_MODE_TAG compact
#elif defined(MODE_FULL)
#define VJ_MODE_TAG full
#else
#error "MODE is not defined"
#endif

/* Two-level expansion so VJ_MODE_TAG is expanded before pasting. */
#define VJ_VM_EXEC_NAME2(mode, isa) vj_vm_exec_##mode##_##isa
#define VJ_VM_EXEC_NAME(mode, isa) VJ_VM_EXEC_NAME2(mode, isa)

#if defined(ISA_NEON)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, neon)
#elif defined(ISA_SSE42)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, sse42)
#elif defined(ISA_AVX2)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, avx2)
#elif defined(ISA_AVX512)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, avx512)
#endif

/* ---------- Engine implementation ----------
 *
 * Stack alignment (x86-64 only):
 * The Go compiler generates an internal ABI wrapper around the Plan9
 * asm trampoline (vjVMExecFast*.abi0).  This wrapper's stack frame
 * layout causes RSP at the .abi0 entry — and therefore at this C
 * entry after JMP — to be 16-byte aligned (RSP mod 16 == 0), whereas
 * the x86-64 SysV ABI requires RSP mod 16 == 8 at function entry
 * (i.e. RSP+8 is 16-aligned, accounting for the CALL return address).
 *
 * This 8-byte misalignment causes the C compiler's generated movdqa
 * (aligned 128-bit SSE store to stack) to fault with SIGSEGV.
 *
 * VJ_ALIGN_STACK (force_align_arg_pointer) on the VM function in
 * encvm.h unconditionally emits AND $-16,%rsp, making it robust
 * against any upstream Go ABI layout changes. */
#include "encvm.h"
