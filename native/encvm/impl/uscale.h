/*
 * Unrounded Scaling float-to-string conversion
 *
 * (BSD-3-Clause, Copyright 2025 The Go Authors)
 *
 * C implementation of the algorithm described in:
 *   "Floating-Point to Decimal, in One Multiply" by Russ Cox
 *   https://research.swtch.com/fp
 *
 */

#ifndef VJ_USCALE_H
#define VJ_USCALE_H

#include <stdint.h>

// clang-format off

int us_write_float32(uint8_t *buf, float value, int flags);
int us_write_float64(uint8_t *buf, double value, int flags);

#define US_FMT_FIXED  0  /* Always use fixed-point notation */
#define US_FMT_EXP_AUTO  1  /* Auto-switch to 'e' when abs < 1e-6 || abs >= 1e21 */

#endif /* VJ_USCALE_H */
