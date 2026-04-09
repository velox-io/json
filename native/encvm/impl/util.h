#ifndef VJ_UTIL_H
#define VJ_UTIL_H

#include <stdint.h>

#ifdef __aarch64__
#include "sse2neon.h"
#else
#include <immintrin.h>
#endif

#include "vj_compat.h"

/* ---- Inline / noinline aliases (backward compat) ------------------- */
/* INLINE and NOINLINE are defined in vj_compat.h */

/* force_align_arg_pointer: emit AND $-16,%rsp prologue on x86-64 to fix
 * stack misalignment when called from Go ABI (RSP mod 16 == 0 instead of
 * SysV-required RSP mod 16 == 8).  No-op on non-x86-64 targets.
 *
 * On Windows x64 the trampoline uses CALL (not JMP), so RSP is already
 * mod 16 == 8 at entry — no realignment needed.  The attribute would be
 * a no-op (AND on already-aligned RSP) but is excluded for clarity. */
#if defined(__x86_64__) && !defined(_WIN32)
#define VJ_ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define VJ_ALIGN_STACK
#endif

#endif /* VJ_UTIL_H*/
