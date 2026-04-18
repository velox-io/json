/*
 * String unescape into the scratch buffer.
 *
 * Scratch protocol:
 *   The driver writes d.scratch's backing array into ud.scratch_ptr /
 *   scratch_cap at entry, with scratch_len = 0. Each unescape writes its
 *   output to ud.scratch_ptr + scratch_len, advances scratch_len, and points
 *   the resulting string header at the just-written sub-interval.
 *
 *   Capacity is bounded by the input length: any \uXXXX sequence is at least
 *   6 bytes of input and yields at most 4 bytes of UTF-8 output, so output
 *   length never exceeds input length. The driver pre-allocates
 *   scratch_cap = len(input) and the reactor skips the bounds check.
 *   Returns NULL on illegal escape.
 */

#ifndef NDEC_BIND_STRING_H
#define NDEC_BIND_STRING_H

#include <stdint.h>

#include "ndec/unescape.h"
#include "go_abi.h"

/* Decodes a raw escaped string into scratch_ptr + scratch_len and advances
 * scratch_len. Returns the start address (= scratch tail before decode),
 * or NULL on failure. */
INLINE const uint8_t *ndec_bind_unescape_into_scratch(NdecBindUserData *ud,
                                                              const uint8_t *raw,
                                                              uint32_t raw_len,
                                                              uint32_t *out_len) {
  uint8_t *dst = ud->scratch_ptr + ud->scratch_len;
  int32_t written = ndec_unescape(raw, raw_len, dst);
  if (written < 0) return 0;
  ud->scratch_len += (uint32_t)written;
  *out_len = (uint32_t)written;
  return dst;
}

#endif /* NDEC_BIND_STRING_H */
