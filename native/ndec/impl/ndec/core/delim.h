#ifndef NDEC_CORE_DELIM_H
#define NDEC_CORE_DELIM_H

#include <stdint.h>

#include "macros.h"

/*
 * JSON delimiter bytes are the structural characters { } [ ] , : and
 * whitespace (space, tab, lf, cr). These are the only bytes that may
 * appear between tokens.
 *
 * non_delim[c] is 1 for every byte that is NOT a delimiter, 0 otherwise.
 * is_non_delim returns that bit. Call sites use the nonzero result to
 * flag a dirty byte in a position where a delimiter is required.
 */
// clang-format off
static const uint8_t non_delim[256] = {
  1,1,1,1,1,1,1,1,1,0,0,1,1,0,1,1,  /* 0x00 */
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,  /* 0x10 */
  0,1,1,1,1,1,1,1,1,1,1,1,0,1,1,1,  /* 0x20 */
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,  /* 0x30 */
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,  /* 0x40 */
  1,1,1,1,1,1,1,1,1,1,1,0,1,0,1,1,  /* 0x50 */
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,  /* 0x60 */
  1,1,1,1,1,1,1,1,1,1,1,0,1,0,1,1,  /* 0x70 */
  /* 0x80..0xFF: not structural */
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
  1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,
};
// clang-format on

INLINE int is_non_delim(uint8_t c) {
  return non_delim[c];
}

#endif /* NDEC_CORE_DELIM_H */
