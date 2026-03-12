/*
 * iface.h -- Velox JSON C Engine: Go Interface Value Encoder
 *
 * Out-of-line encoder for interface{} (eface) values.
 * Marked noinline to keep the VM's hot dispatch loop compact
 * and avoid register spill from the cold interface logic
 * (binary search, primitive tag switch, Blueprint dispatch).
 *
 */

#ifndef VJ_ENCVM_IFACE_H
#define VJ_ENCVM_IFACE_H

#include "types.h"

enum VjIfaceAction {
  VJ_IFACE_DONE = 0,  /* primitive encoded into buf */
  VJ_IFACE_YIELD = 1, /* fallback to Go (not compilable or unsupported tag) */
  VJ_IFACE_CACHE_MISS = 2, /* type not in cache, yield for Go compilation */
  VJ_IFACE_SWITCH_OPS = 3, /* cached Blueprint found, caller pushes frame */
  VJ_IFACE_BUF_FULL = 4,   /* buffer space insufficient */
  VJ_IFACE_NAN_INF = 5,    /* float NaN/Inf encountered */
};

/* Result struct — returned by value (fits in 3-4 registers on arm64/x86_64). */
typedef struct {
  uint8_t *buf; /* advanced buffer pointer (valid when action=DONE) */
  const uint8_t *cached_ops; /* Blueprint ops byte stream (valid when action=SWITCH_OPS) */
  const void *type_ptr;    /* eface.type_ptr (valid when action=CACHE_MISS) */
  const uint8_t *data_ptr; /* eface.data_ptr (valid when action=SWITCH_OPS) */
  int32_t action;          /* VjIfaceAction */
} VjIfaceResult;

typedef struct {
  uint8_t *buf; /* advanced buffer pointer; NULL on error */
  int exit_code; /* 0 = ok; otherwise one of the internal VJ_EXIT_* sentinels */
} VjPtrEncResult;

VjIfaceResult vj_encode_interface_value(uint8_t *buf, const uint8_t *bend,
                                        const uint8_t *iface_ptr,
                                        const VjIfaceCacheEntry *cache,
                                        int32_t cache_count, uint32_t flags);

#endif /* VJ_ENCVM_IFACE_H */
