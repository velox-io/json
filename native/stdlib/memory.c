/*
 * memory.c — Minimal C Runtime: memcpy/memset Implementation
 *
 * Compiled once (no ISA flags) and linked alongside the ISA-specific
 * objects, so that memcpy/memset symbols appear exactly once in the
 * final .syso.
 *
 * The declarations (with __asm__ symbol renaming) live in memory.h
 * so that every translation unit sees them as extern.
 */

#include "memory.h"
#include <stdint.h>

#include "vj_compat.h"

/* Use __builtin_memcpy throughout the code. The compiler will inline
 * small known-size copies and call our _memcpy symbol for the rest. */
VJ_HIDDEN void *
vj_memcpy_impl(void *__restrict dst, const void *__restrict src, size_t n) {
  uint8_t *d = (uint8_t *)dst;
  const uint8_t *s = (const uint8_t *)src;
  while (n >= sizeof(uint64_t)) {
    /* Manual word load/store to avoid __builtin_memcpy which
     * the compiler may turn into a recursive _memcpy call. */
    uint64_t w = *(const uint64_t *)s;
    *(uint64_t *)d = w;
    d += sizeof(uint64_t);
    s += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  /* Cascading word tail: 0-7 remaining bytes.
   * Manual loads/stores only — __builtin_memcpy would recurse. */
  if (n >= 4) {
    uint32_t w = *(const uint32_t *)s;
    *(uint32_t *)d = w;
    d += 4;
    s += 4;
    n -= 4;
  }
  if (n >= 2) {
    uint16_t w = *(const uint16_t *)s;
    *(uint16_t *)d = w;
    d += 2;
    s += 2;
    n -= 2;
  }
  if (n) {
    *d = *s;
  }
  return dst;
}

VJ_HIDDEN void *vj_memset_impl(void *dst, int c,
                                                           size_t n) {
  uint8_t *d = (uint8_t *)dst;
  uint8_t val = (uint8_t)c;
  while (n >= sizeof(uint64_t)) {
    uint64_t w = val;
    w |= w << 8;
    w |= w << 16;
    w |= w << 32;
    *(uint64_t *)d = w;
    d += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  while (n--) {
    *d++ = val;
  }
  return dst;
}

/*
 * MSVC ABI artifact: when targeting x86_64-pc-windows-msvc, clang emits
 * a reference to _fltused whenever floating-point code appears.  The CRT
 * normally defines it, but we link with /NODEFAULTLIB.  Provide a const
 * definition here so it lands in .rdata and gets merged into .text by
 * /MERGE:.rdata=.text — no extra section, no extra build step.
 */
#ifdef _WIN32
const int _fltused = 1;
#endif
