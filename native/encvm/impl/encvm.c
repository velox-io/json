/*
 * encvm entry point — build configuration and public symbol.
 *
 * Defines VJ_VM_EXEC_FN_NAME (e.g. vj_vm_exec_full_neon), then
 * includes encvm.h which emits the VM body as that public symbol.
 *
 * Compile once per mode (full/compact/fast) × ISA (neon/avx2). */

/* Build configuration validation */
#if !defined(OS)
#error "OS must be defined (linux, darwin, or windows)"
#endif
#if !defined(ARCH)
#error "ARCH must be defined (arm64 or amd64)"
#endif
#if !defined(ISA_NEON) && !defined(ISA_AVX2) && !defined(ISA_AVX512)
#error "ISA must be defined (use -DISA_NEON, -DISA_AVX2, or -DISA_AVX512)"
#endif

/* Mode configuration
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
#define VJ_VM_EXEC_NAME(mode, isa)  VJ_VM_EXEC_NAME2(mode, isa)

#ifndef VJ_VM_EXEC_FN_NAME
#if defined(ISA_NEON)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, neon)
#elif defined(ISA_AVX2)
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG, avx2)
#endif
#endif

#include "encvm.h"
