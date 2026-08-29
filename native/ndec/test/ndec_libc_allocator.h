/*
 * ndec_libc_allocator.h -- libc-backed ndec_allocator vtable.
 *
 * Host-side helper for code that wants the parser routed through libc
 * malloc/realloc/free (tests, benchmarks, verify tools). This is NOT
 * part of the embeddable core (impl/ndec/); the core ships only the
 * allocator interface and stays libc-free. Embedders that use a custom
 * allocator never need this header.
 *
 * Usage:
 *   json_dom d;
 *   memset(&d, 0, sizeof(d));
 *   d.allocator = NDEC_LIBC_ALLOCATOR;
 *   dom_ensure_capacity(&d, len);
 */
#ifndef NDEC_LIBC_ALLOCATOR_H
#define NDEC_LIBC_ALLOCATOR_H

#include <stdlib.h>

#include "ndec/core/alloc.h"

static inline void *ndec_libc_realloc(void *ctx, void *ptr, size_t old_size, size_t new_size) {
  (void)ctx;
  (void)old_size;
  return new_size ? realloc(ptr, new_size) : (free(ptr), (void *)NULL);
}

static inline void ndec_libc_free(void *ctx, void *ptr, size_t old_size) {
  (void)ctx;
  (void)old_size;
  free(ptr);
}

#define NDEC_LIBC_ALLOCATOR ((ndec_allocator){NULL, ndec_libc_realloc, ndec_libc_free})

#endif /* NDEC_LIBC_ALLOCATOR_H */
