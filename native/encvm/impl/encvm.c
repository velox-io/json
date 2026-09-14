/*
 * encvm entry point.
 *
 * Defines VJ_VM_EXEC_FN_NAME (e.g. vj_vm_exec_full), then includes encvm.h
 * which emits the VM body as that public symbol.
 *
 * Compile once per mode (full/compact/fast); the modes are linked into one
 * arch-canonical blob whose entry names carry no ISA segment, since every
 * arch ships exactly one ISA variant.
 *
 * Build configuration validation */
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

/* Two-level expansion so VJ_MODE_TAG is expanded before pasting. The entry
 * name carries no ISA segment: the blob is arch-canonical and each arch
 * builds exactly one ISA variant, so the Go trampolines call stable,
 * arch-independent names. */
#define VJ_VM_EXEC_NAME2(mode) vj_vm_exec_##mode
#define VJ_VM_EXEC_NAME(mode)  VJ_VM_EXEC_NAME2(mode)

#ifndef VJ_VM_EXEC_FN_NAME
#define VJ_VM_EXEC_FN_NAME VJ_VM_EXEC_NAME(VJ_MODE_TAG)
#endif

#include "encvm.h"
