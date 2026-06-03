/*
 * ftoa.h — Float-to-ASCII conversion API.
 *
 * Encodes IEEE-754 floats to JSON-compatible decimal strings.  The output
 * is always the shortest round-trip representation, with '.' as the decimal
 * point and no locale or thousands separator.  NaN / Inf must be filtered
 * by the caller; behavior on those inputs is undefined.  Buffer must have
 * >= 32 bytes available.
 *
 * Algorithm (current implementation): Unrounded Scaling, after Russ Cox,
 * "Floating-Point to Decimal, in One Multiply"
 * (https://research.swtch.com/fp).  All algorithm internals live in
 * ftoa.c.
 */

#ifndef VJ_ENCVM_FTOA_H
#define VJ_ENCVM_FTOA_H

#include <stdint.h>

/* Output format flags. */
#define VJ_FTOA_FIXED    0 /* Always fixed-point notation. */
#define VJ_FTOA_EXP_AUTO 1 /* Switch to scientific when |x| < 1e-6 or |x| >= 1e21. */

/* Returns number of bytes written to buf. */
int vj_write_float32(uint8_t *buf, float value, int flags);
int vj_write_float64(uint8_t *buf, double value, int flags);

#endif /* VJ_ENCVM_FTOA_H */
