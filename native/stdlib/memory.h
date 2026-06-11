/*
 * Minimal C runtime declarations.
 *
 * These custom memcpy, memset, memmove, and bzero implementations avoid
 * libc dependencies in the .syso object. The function bodies live in
 * memory.c, compiled once without ISA flags, so multiple ISA objects can
 * link against a single shared implementation.
 *
 * bzero is included here because clang may rewrite memset(dst, 0, n)
 * into a bzero call. Assert fallback stubs stay in assert.c because
 * only some consumers need them.
 */

#ifndef VJ_STDLIB_MEMORY_H
#define VJ_STDLIB_MEMORY_H

#include <stddef.h>

#include "vj_compat.h"

/* Platform specific symbol naming:
 * macOS Mach-O: C symbols have _ prefix (_memcpy, _memset, _memmove, _bzero)
 * Linux ELF:    C symbols have no prefix (memcpy, memset, memmove, bzero) */
#if defined(__APPLE__)
#define VJ_MEMCPY_SYM  "_memcpy"
#define VJ_MEMSET_SYM  "_memset"
#define VJ_MEMMOVE_SYM "_memmove"
#define VJ_BZERO_SYM   "_bzero"
#else
#define VJ_MEMCPY_SYM  "memcpy"
#define VJ_MEMSET_SYM  "memset"
#define VJ_MEMMOVE_SYM "memmove"
#define VJ_BZERO_SYM   "bzero"
#endif

/* Declarations: always visible so each ISA TU can link against
 * the single memcpy/memset/memmove/bzero object compiled from memory.c. */
VJ_HIDDEN void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src,
               size_t n) __asm__(VJ_MEMCPY_SYM);

VJ_HIDDEN void *
vj_memset_impl(void *dst, int c, size_t n) __asm__(VJ_MEMSET_SYM);

VJ_HIDDEN void *
vj_memmove_impl(void *dst, const void *src, size_t n) __asm__(VJ_MEMMOVE_SYM);

VJ_HIDDEN void
vj_bzero_impl(void *dst, size_t n) __asm__(VJ_BZERO_SYM);

#endif /* VJ_STDLIB_MEMORY_H */
