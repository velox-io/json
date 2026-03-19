/* iface.h -- interface{} (eface) value encoder.
 *
 * Out-of-line to keep the VM dispatch loop compact and avoid register
 * spill from the cold interface path (binary search + tag switch). */

#ifndef VJ_ENCVM_IFACE_H
#define VJ_ENCVM_IFACE_H

#include "types.h"

enum VjIfaceAction {
  VJ_IFACE_DONE = 0,       /* primitive encoded into buf */
  VJ_IFACE_YIELD = 1,      /* not compilable, fallback to Go */
  VJ_IFACE_CACHE_MISS = 2, /* type not in cache, yield for compilation */
  VJ_IFACE_SWITCH_OPS = 3, /* cached Blueprint, caller pushes frame */
  VJ_IFACE_BUF_FULL = 4,   /* buffer full */
  VJ_IFACE_NAN_INF = 5,    /* float NaN/Inf */
};

typedef struct {
  uint8_t *buf;
  const uint8_t *cached_ops; /* Blueprint ops (SWITCH_OPS) */
  const void *type_ptr;      /* eface.type_ptr (CACHE_MISS) */
  const uint8_t *data_ptr;   /* eface.data_ptr (SWITCH_OPS) */
  int32_t action;
} VjIfaceResult;

VjIfaceResult vj_encode_interface_value(uint8_t *buf, const uint8_t *bend,
                                        const uint8_t *iface_ptr,
                                        const VjIfaceCacheEntry *cache,
                                        int32_t cache_count, uint32_t flags);

#endif /* VJ_ENCVM_IFACE_H */
