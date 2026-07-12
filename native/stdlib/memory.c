/*
 * Minimal C runtime for memcpy, memset, memmove, bzero, and memcmp.
 *
 * Compiled once without ISA flags and linked alongside the ISA specific
 * objects, so these symbols are defined exactly once in the final .syso.
 *
 * The declarations live in memory.h with __asm__ renaming so every
 * translation unit references the same exported libc symbol names.
 */

#include "memory.h"
#include <stdint.h>

#include "macros.h"

/* Use __builtin_memcpy throughout the code. The compiler will inline
 * small known size copies and call our _memcpy symbol for the rest.
 *
 * Self recursion guard:
 *
 * clang's LoopIdiomRecognize pass can fold hand written byte loops for
 * memcpy, memset, memmove, bzero, and memcmp back into libc calls.
 * Because these functions are exported under libc symbol names via
 * __asm__, such a rewrite resolves back to this file and recurses forever.
 *
 * We rely on two protections in scripts/gen-natives.sh for stdlib
 * translation units:
 *   1. -mllvm -disable-loop-idiom-all
 *      Disables the loop to libcall rewrite for stdlib sources.
 *   2. -fno-builtin-memcpy -fno-builtin-memset -fno-builtin-memmove
 *      -fno-builtin-bzero -fno-builtin-memcmp
 *      Blocks call site rewrites such as turning memset(dst, 0, n)
 *      into bzero.
 *
 * Client code still builds without these flags, so it can keep the usual
 * idiom optimizations while resolving the resulting libc calls to the
 * safe implementations in this file.
 */
HIDDEN void *vj_memcpy_impl(void *__restrict dst, const void *__restrict src, size_t n) {
  uint8_t *d       = (uint8_t *)dst;
  const uint8_t *s = (const uint8_t *)src;
  while (n >= sizeof(uint64_t)) {
    /* Manual word load and store only. __builtin_memcpy may recurse. */
    uint64_t w     = *(const uint64_t *)s;
    *(uint64_t *)d = w;
    d += sizeof(uint64_t);
    s += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  /* Cascading tail for the final 0 to 7 bytes.
   * Manual loads and stores only. __builtin_memcpy may recurse. */
  if (n >= 4) {
    uint32_t w     = *(const uint32_t *)s;
    *(uint32_t *)d = w;
    d += 4;
    s += 4;
    n -= 4;
  }
  if (n >= 2) {
    uint16_t w     = *(const uint16_t *)s;
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

HIDDEN void *vj_memset_impl(void *dst, int c, size_t n) {
  uint8_t *d  = (uint8_t *)dst;
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

/* memmove handles overlapping regions. When src and dst do not overlap
 * this becomes a forward copy. On overlap it copies backward so unread
 * bytes are not clobbered. Required by clients such as atof slow path
 * mp_shl, which shifts a limb array in place. */
HIDDEN void *vj_memmove_impl(void *dst, const void *src, size_t n) {
  uint8_t *d       = (uint8_t *)dst;
  const uint8_t *s = (const uint8_t *)src;
  if (d == s || n == 0)
    return dst;
  if (d < s) {
    while (n--)
      *d++ = *s++;
  } else {
    d += n;
    s += n;
    while (n--)
      *--d = *--s;
  }
  return dst;
}

/* bzero may be synthesized from memset(dst, 0, n) on BSD style targets.
 * In a -nostdlib syso link there is no libc fallback, so we provide an
 * implementation here. atof slow path mp_zero is a known trigger.
 *
 * Self recursion guard:
 *
 * The same LoopIdiomRecognize pass can fold this hand written zeroing
 * loop back into a _bzero libcall, which would recurse after __asm__
 * renaming.
 *
 * -fno-builtin-bzero only recognizes a function literally named bzero.
 * It does not apply to vj_bzero_impl after __asm__ renaming. The
 * no_builtin attribute is not sufficient either because this rewrite
 * happens before builtin lowering.
 *
 * Disabling LoopIdiomRecognize for stdlib translation units keeps this
 * implementation as a plain C loop with no inline assembly and no
 * platform specific branches.
 */
HIDDEN void vj_bzero_impl(void *dst, size_t n) {
  uint8_t *d = (uint8_t *)dst;
  while (n >= sizeof(uint64_t)) {
    *(uint64_t *)d = 0;
    d += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  while (n--) {
    *d++ = 0;
  }
}

/* memcmp is lowered from __builtin_memcmp by clang for non-constant sizes.
 * In a -nostdlib syso link there is no libc fallback, so we provide an
 * implementation here. bind.h field name lookup is a known caller.
 *
 * Returns 0 if equal, otherwise the signed difference of the first
 * differing byte (a[i] - b[i]) so callers see libc-compatible ordering.
 *
 * Self recursion guard:
 *
 * The same LoopIdiomRecognize pass can fold this hand written compare
 * loop back into a _memcmp libcall, which would recurse after __asm__
 * renaming. The stdlib compile step passes -fno-builtin-memcmp and
 * -mllvm -disable-loop-idiom-all, mirroring the protection on
 * memcpy/memset/memmove/bzero above.
 *
 * Manual loads only. __builtin_memcmp may recurse. */
HIDDEN int vj_memcmp_impl(const void *a, const void *b, size_t n) {
  const uint8_t *pa = (const uint8_t *)a;
  const uint8_t *pb = (const uint8_t *)b;
  while (n >= sizeof(uint64_t)) {
    uint64_t wa = *(const uint64_t *)pa;
    uint64_t wb = *(const uint64_t *)pb;
    if (wa != wb) {
      /* Find the first differing byte within the mismatched word.
       * Plain byte scan, no __builtin_ctz, to stay portable to targets
       * like MSVC where ctz intrinsics are not always available. */
      for (uint32_t i = 0; i < sizeof(uint64_t); i++) {
        if (pa[i] != pb[i])
          return (int)pa[i] - (int)pb[i];
      }
      /* Unreachable: wa != wb guarantees a differing byte. */
      return 0;
    }
    pa += sizeof(uint64_t);
    pb += sizeof(uint64_t);
    n -= sizeof(uint64_t);
  }
  /* Cascading tail for the final 0 to 7 bytes.
   * Manual loads only. __builtin_memcmp may recurse. */
  if (n >= 4) {
    uint32_t wa = *(const uint32_t *)pa;
    uint32_t wb = *(const uint32_t *)pb;
    if (wa != wb) {
      for (uint32_t i = 0; i < 4; i++) {
        if (pa[i] != pb[i])
          return (int)pa[i] - (int)pb[i];
      }
      return 0;
    }
    pa += 4;
    pb += 4;
    n -= 4;
  }
  if (n >= 2) {
    uint16_t wa = *(const uint16_t *)pa;
    uint16_t wb = *(const uint16_t *)pb;
    if (wa != wb) {
      if (pa[0] != pb[0])
        return (int)pa[0] - (int)pb[0];
      return (int)pa[1] - (int)pb[1];
    }
    pa += 2;
    pb += 2;
    n -= 2;
  }
  if (n) {
    if (pa[0] != pb[0])
      return (int)pa[0] - (int)pb[0];
  }
  return 0;
}

/*
 * MSVC ABI artifact: when targeting x86_64-pc-windows-msvc, clang emits
 * a reference to _fltused whenever floating point code appears. The CRT
 * normally defines it, but we link with /NODEFAULTLIB. Provide a const
 * definition here so it lands in .rdata and gets merged into .text by
 * /MERGE:.rdata=.text with no extra section or build step.
 */
#ifdef _WIN32
const int _fltused = 1;
#endif
