#ifndef NDEC_HASH_H
#define NDEC_HASH_H

#include <stdint.h>
#include "ndec/core/helper.h"

INLINE uint64_t ndec_hash_simple(const uint8_t *s, uint32_t n, uint64_t seed) {
  if (n == 0)
    return seed * 0x9e3779b97f4a7c15ULL;
  uint64_t first = s[0];
  uint64_t last  = s[n - 1];
  uint64_t mid   = s[n / 2];
  uint64_t h     = seed;
  h ^= (uint64_t)n * 0x9e3779b97f4a7c15ULL;
  h ^= first * 0xbf58476d1ce4e5b9ULL;
  h ^= last * 0x94d049bb133111ebULL;
  h ^= mid * 0xff51afd7ed558ccdULL;
  return h;
}

INLINE uint64_t ndec_hash_fnv1a(const uint8_t *s, uint32_t n, uint64_t seed) {
  uint64_t h = seed ^ 0xcbf29ce484222325ULL;
  for (uint32_t i = 0; i < n; i++) {
    h ^= (uint64_t)s[i];
    h *= 0x100000001b3ULL;
  }
  h ^= h >> 33;
  h *= 0xff51afd7ed558ccdULL;
  h ^= h >> 33;
  return h;
}

INLINE uint64_t ndec_hash_mulacc(const uint8_t *s, uint32_t n, uint64_t seed) {
  if (n == 0)
    return seed * 0x9e3779b97f4a7c15ULL;
  uint64_t h = seed + (uint64_t)n * 0x9e3779b97f4a7c15ULL;
  h          = h * 0xbf58476d1ce4e5b9ULL + (uint64_t)s[0];
  h          = h * 0x94d049bb133111ebULL + (uint64_t)s[n - 1];
  h          = h * 0xff51afd7ed558ccdULL + (uint64_t)s[n / 2];
  if (n > 1)
    h = h * 0xc4ceb9fe1a85ec53ULL + (uint64_t)s[1];
  if (n > 2)
    h = h * 0x62a9d9ed799705f5ULL + (uint64_t)s[n - 2];
  return h;
}

#endif /* NDEC_HASH_H */
