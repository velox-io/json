/*
 * Yield setup macros and Go runtime memory layout mirrors.
 *
 * Three flavors of yield helper, chosen by what payload the driver reads:
 *   - NDEC_BIND_YIELD: pending_action only.
 *   - NDEC_BIND_YIELD_RAW: also writes raw_ptr / raw_len / yield_flags
 *     (BEGIN_PTR scalar values, GROW_SLICE elem bytes, UNKNOWN_FIELD name).
 *   - NDEC_BIND_YIELD_PTR_STRUCT: PTR-to-struct shortcut. Writes pending_action
 *     + yield_flags=NDEC_YF_PTR_TO_STRUCT only; the driver does not read raw_*
 *     on this path, so the two raw stores are skipped to keep begin_object's
 *     PTR-to-struct branch tight.
 *
 * yield_flags is a side channel for distinguishing semantics under the same
 * action (see go_abi.h NDEC_YF_*); for BEGIN_PTR it separates *<scalar> (= 0)
 * from *<struct> (= 0xFF).
 *
 * NdecGoStringHeader / NdecGoSliceHeader mirror Go's runtime layouts (see
 * goStringHeader / goSliceHeader in ndec/go_layout.go) byte-for-byte; the
 * reactor writes string/slice fields directly through these structs.
 */

#ifndef NDEC_BIND_YIELD_H
#define NDEC_BIND_YIELD_H

#include <stdint.h>

#include "ndec/core/types.h"

#define NDEC_BIND_YIELD(ud, action_)                                                                              \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)(action_);                                                                   \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* TYPE_MISMATCH: encodes the JSON token kind into yield_flags so the
 * driver's errCtx.valueDesc can render a stdlib-compatible
 * UnmarshalTypeError.Value ("string"/"number"/...) without dereferencing
 * raw_ptr. Callsites may still set raw_ptr/raw_len but the driver does
 * not depend on them on this path. */
#define NDEC_BIND_YIELD_TYPE_MISMATCH(ud, token_kind_)                                                            \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)NDEC_YA_TYPE_MISMATCH;                                                       \
    (ud)->yield_flags    = (uint8_t)(token_kind_);                                                                \
    return NDEC_YIELD;                                                                                            \
  } while (0)

#define NDEC_BIND_YIELD_RAW(ud, action_, raw_ptr_, raw_len_, yield_flags_)                                        \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)(action_);                                                                   \
    (ud)->raw_ptr        = (raw_ptr_);                                                                            \
    (ud)->raw_len        = (raw_len_);                                                                            \
    (ud)->yield_flags    = (yield_flags_);                                                                        \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* PTR-to-struct yield: the driver only reads the yield_flags sentinel
 * NDEC_YF_PTR_TO_STRUCT. raw_ptr/raw_len are not read. Skip those two stores. */
#define NDEC_BIND_YIELD_PTR_STRUCT(ud)                                                                            \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)NDEC_YA_BEGIN_PTR;                                                           \
    (ud)->yield_flags    = NDEC_YF_PTR_TO_STRUCT;                                                                 \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* PTR-to-slice (container) yield */
#define NDEC_BIND_YIELD_PTR_TO_SLICE(ud)                                                                          \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)NDEC_YA_BEGIN_PTR;                                                           \
    (ud)->yield_flags    = NDEC_YF_PTR_TO_SLICE;                                                                  \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* PTR-to-map (container) yield */
#define NDEC_BIND_YIELD_PTR_TO_MAP(ud)                                                                            \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)NDEC_YA_BEGIN_PTR;                                                           \
    (ud)->yield_flags    = NDEC_YF_PTR_TO_MAP;                                                                    \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* MAP value = *scalar: forwards raw bytes to the driver, which allocs the
 * pointee and writes the current KV slot's value pointer. */
#define NDEC_BIND_YIELD_MAP_VALUE_PTR_RAW(ud, raw_ptr_, raw_len_)                                                 \
  NDEC_BIND_YIELD_RAW((ud), NDEC_YA_BEGIN_PTR_MAP_VALUE, (raw_ptr_), (raw_len_), NDEC_YF_NONE)

/* MAP value = *struct: parser has already STACK_PUSH'd to the child slot.
 * The driver allocs the pointee and fills the child frame. No raw payload needed. */
#define NDEC_BIND_YIELD_MAP_VALUE_PTR_STRUCT(ud)                                                                  \
  do {                                                                                                            \
    (ud)->pending_action = (uint32_t)NDEC_YA_BEGIN_PTR_MAP_VALUE;                                                 \
    (ud)->yield_flags    = NDEC_YF_MAP_VALUE_PTR_STRUCT;                                                          \
    return NDEC_YIELD;                                                                                            \
  } while (0)

/* SLICE frame cap exhausted: forwards raw bytes to driver for realloc + elem write.
 * yield_flags is always NDEC_YF_NONE (string elems already unescaped by the reactor). */
#define NDEC_BIND_SLICE_GROW(ud, raw_ptr_, raw_len_)                                                              \
  NDEC_BIND_YIELD_RAW((ud), NDEC_YA_GROW_SLICE, (raw_ptr_), (raw_len_), NDEC_YF_NONE)

/* Go string header mirror: {data ptr; len int}. */
typedef struct NdecGoStringHeader {
  const uint8_t *data;
  intptr_t len;
} NdecGoStringHeader;

/* Go slice header mirror: {data ptr; len int; cap int}.
 * end_array writes hdr->len directly; the grow yield handler in the driver
 * writes data/cap. The driver stores the parent field's slice header address
 * into the SLICE frame's bind_slice_hdr slot so the close path avoids a yield. */
typedef struct NdecGoSliceHeader {
  void *data;
  intptr_t len;
  intptr_t cap;
} NdecGoSliceHeader;

#endif /* NDEC_BIND_YIELD_H */
