/*
 * memfn.h — Minimal C Runtime: memcpy/memset Implementation
 *
 * Custom memcpy/memset implementations to avoid libc dependencies in
 * the .syso object. These provide hidden-visibility implementations
 * that the compiler resolves via __asm__ symbol renaming.
 *
 * The actual function bodies live in memfn.c (compiled once,
 * without ISA flags) to avoid duplicate symbols when multiple ISA
 * objects are linked into a single .syso.
 */

#ifndef VJ_STDLIB_MEMFN_H
#define VJ_STDLIB_MEMFN_H

#include <stddef.h>

/* Platform-specific symbol naming:
 * macOS Mach-O: C symbols have _ prefix (_memcpy, _memset)
 * Linux ELF:    C symbols have no prefix (memcpy, memset) */
#if defined(__APPLE__)
  #define VJ_MEMCPY_SYM "_memcpy"
  #define VJ_MEMSET_SYM "_memset"
#else
  #define VJ_MEMCPY_SYM "memcpy"
  #define VJ_MEMSET_SYM "memset"
#endif

/* Declarations — always visible so each ISA TU can link against
 * the single memcpy/memset compiled from memfn.c. */
__attribute__((visibility("hidden"))) void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src,
               size_t n) __asm__(VJ_MEMCPY_SYM);

__attribute__((visibility("hidden"))) void *
vj_memset_impl(void *dst, int c, size_t n) __asm__(VJ_MEMSET_SYM);

#endif /* VJ_STDLIB_MEMFN_H */
