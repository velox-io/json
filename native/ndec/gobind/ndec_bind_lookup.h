/*
 * Field name to index lookup, three tiers (BITMAP8 / PERFECT / MAP).
 *
 * Hash mixers are in ../impl/hash.h; mixer choice and table sort order must
 * stay aligned with the Go-side lookup table builder.
 */

#ifndef NDEC_BIND_LOOKUP_H
#define NDEC_BIND_LOOKUP_H

#include <stdint.h>

#include "ndec/hash.h"
#include "go_abi.h"

INLINE int ndec_bind_lookup_bitmap8(const NdecFieldLookupABI *l, const uint8_t *key, uint32_t klen) {
  if (klen == 0 || klen > l->max_key_len)
    return -1;
  uint8_t cur           = 0xFF;
  const uint8_t *bitmap = l->bitmap;
  for (uint32_t i = 0; i < klen; i++) {
    cur &= bitmap[i * 256 + key[i]];
    if (cur == 0)
      return -1;
  }
  cur &= l->len_mask[klen];
  if (cur == 0)
    return -1;
  return __builtin_ctz((uint32_t)cur);
}

INLINE int ndec_bind_lookup_perfect(const NdecFieldLookupABI *l, const NdecBindTypeInfo *ti, const uint8_t *key,
                                    uint32_t klen) {
  uint64_t h;
  switch (l->hash_mixer) {
  case 0:
    h = ndec_hash_simple(key, klen, l->hash_seed);
    break;
  case 1:
    h = ndec_hash_fnv1a(key, klen, l->hash_seed);
    break;
  case 2:
    h = ndec_hash_mulacc(key, klen, l->hash_seed);
    break;
  default:
    return -1;
  }
  uint32_t mask = ((uint32_t)1u << l->table_size_log2) - 1u;
  uint32_t slot = (uint32_t)(h >> l->hash_shift) & mask;
  uint8_t idx   = l->perfect_table[slot];
  if (idx == 0xFF)
    return -1;
  if (idx >= ti->field_count)
    return -1;
  const NdecBindFieldInfo *fi = &ti->fields[idx];
  if (fi->name_len != klen)
    return -1;
  for (uint32_t i = 0; i < klen; i++) {
    if (fi->name[i] != key[i])
      return -1;
  }
  return (int)idx;
}

INLINE int ndec_bind_lookup_map(const NdecFieldLookupABI *l, const uint8_t *key, uint32_t klen) {
  uint64_t h                  = ndec_hash_fnv1a(key, klen, 0);
  const NdecMapEntry *entries = l->map_entries;
  uint32_t lo = 0, hi = l->entry_count;
  while (lo < hi) {
    uint32_t mid = lo + (hi - lo) / 2;
    if (entries[mid].hash < h) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  for (uint32_t i = lo; i < l->entry_count && entries[i].hash == h; i++) {
    if (entries[i].name_len != klen)
      continue;
    int eq = 1;
    for (uint32_t j = 0; j < klen; j++) {
      if (entries[i].name_ptr[j] != key[j]) {
        eq = 0;
        break;
      }
    }
    if (eq)
      return (int)entries[i].idx;
  }
  return -1;
}

INLINE int ndec_bind_lookup_find(const NdecFieldLookupABI *l, const NdecBindTypeInfo *ti, const uint8_t *key,
                                 uint32_t klen) {
  if (__builtin_expect(l->kind == NDEC_FLK_BITMAP8, 1)) {
    return ndec_bind_lookup_bitmap8(l, key, klen);
  }
  if (l->kind == NDEC_FLK_PERFECT) {
    return ndec_bind_lookup_perfect(l, ti, key, klen);
  }
  if (l->kind == NDEC_FLK_MAP) {
    return ndec_bind_lookup_map(l, key, klen);
  }
  return -1;
}

#endif /* NDEC_BIND_LOOKUP_H */
