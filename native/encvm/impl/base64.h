/*
 * base64.h — Velox JSON C Engine: Base64 Encoder for []byte
 *
 * Out-of-line encoder for Go []byte → base64 JSON string.
 * Marked noinline to keep the VM's hot dispatch loop compact —
 * []byte serialization is an uncommon case.
 *
 * Standard base64 encoding (RFC 4648) with padding, matching
 * Go's encoding/json behavior for []byte fields.
 */

#ifndef VJ_ENCVM_BASE64_H
#define VJ_ENCVM_BASE64_H

#include "types.h"

/* Encode a byte slice as a base64-encoded JSON string (with quotes).
 *
 * Writes: '"' + base64(data[0..len]) + '"'
 *
 * Parameters:
 *   buf   — current output write position
 *   bend  — one past last byte of output buffer
 *   data  — source bytes (must not be NULL; caller handles nil/empty)
 *   len   — number of source bytes (must be > 0)
 *
 * Returns:
 *   Advanced buffer pointer on success.
 *   NULL if the output buffer has insufficient space (VJ_EXIT_BUF_FULL). */
uint8_t *vj_encode_base64(uint8_t *buf, const uint8_t *bend,
                           const uint8_t *data, int64_t len);

#endif /* VJ_ENCVM_BASE64_H */
