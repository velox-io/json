/*
 * pointer.h — Velox JSON C Engine: Pointer Primitive Encoder
 *
 * Out-of-line encoder for dereferenced pointer values (*bool, *int, etc.).
 * Marked noinline to keep the VM's code footprint small
 * and avoid icache pressure on the hot dispatch loop.
 */

#ifndef VJ_ENCVM_POINTER_H
#define VJ_ENCVM_POINTER_H

// clang-format off

#include "types.h"
#include "number.h"
#include "strfn.h"

/* ---- Out-of-line pointer-primitive encoder ----
 *
 * Encodes a single dereferenced primitive value (bool, int*, uint*,
 * float*, string, raw_message, number) into the buffer. */

typedef struct {
  uint8_t *buf;  /* advanced buffer pointer; NULL on error */
  int error;     /* 0 = ok, VJ_ERR_BUF_FULL, VJ_ERR_NAN_INF */
} VjPtrEncResult;

VjPtrEncResult vj_encode_ptr_value(uint8_t *buf, const uint8_t *bend,
                                   const void *ptr, uint16_t etype,
                                   uint32_t flags);

#endif /* VJ_ENCVM_POINTER_H */
