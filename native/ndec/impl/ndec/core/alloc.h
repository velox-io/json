/* json_dom delegates allocation through a caller-owned vtable and opaque
 * context. The caller installs it before dom_ensure_capacity. A zero vtable
 * rejects growth and returns immediately from release. old_size supports live
 * byte accounting directly in the allocator. */
#ifndef NDEC_ALLOCATOR_H
#define NDEC_ALLOCATOR_H

#include <stddef.h>

typedef struct ndec_allocator ndec_allocator;

/* realloc: grow/shrink/free/malloc. Returns NULL on failure (or when
 * new_size==0, like C realloc). `ptr` may be NULL (malloc); `old_size`
 * is the previous allocation size, 0 if ptr is NULL. */
typedef void *(*ndec_alloc_realloc_fn)(void *ctx, void *ptr, size_t old_size, size_t new_size);

/* free: release the allocation. `old_size` is the size the caller last
 * reallocated to; 0 if unknown. free(NULL, ...) is a no-op. */
typedef void (*ndec_alloc_free_fn)(void *ctx, void *ptr, size_t old_size);

struct ndec_allocator {
  void *ctx;
  ndec_alloc_realloc_fn realloc;
  ndec_alloc_free_fn free;
};

/* A missing allocator rejects growth and ignores release. */
static inline void *ndec_alloc_realloc(const ndec_allocator *a, void *ptr, size_t old_size, size_t new_size) {
  ndec_alloc_realloc_fn fn = a ? a->realloc : NULL;
  return fn ? fn(a->ctx, ptr, old_size, new_size) : NULL;
}
static inline void ndec_alloc_free(const ndec_allocator *a, void *ptr, size_t old_size) {
  ndec_alloc_free_fn fn = a ? a->free : NULL;
  if (fn) fn(a->ctx, ptr, old_size);
}

#endif /* NDEC_ALLOCATOR_H */
