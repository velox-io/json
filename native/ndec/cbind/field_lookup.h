/*
 * field_lookup.h: 3-tier struct-field name -> index lookup
 *
 * Used by bind's r_object_field to replace the linear O(n) scan over
 * reflection fields. Auto-picks:
 *
 *   n <=  8  bitmap8k     per-position 8-bit mask + memcmp verify
 *   n <= 32  perfect hash simple→fnv1a fallback, 2*n-sized table
 *   n >  32  hashmap      FNV-1a + linear probing, load < 0.5
 *
 * Each field contributes both its primary name and its alias (if any)
 * to the same table; both map to the field's index.
 */

#ifndef NDEC_FIELD_LOOKUP_H
#define NDEC_FIELD_LOOKUP_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

struct NdecField; /* defined in bind.h */

typedef struct NdecFieldLookup NdecFieldLookup;

/* Build a lookup table indexing the fields' primary names and aliases
 * to field indices. Returns NULL on allocation failure. The resulting
 * table holds no pointer into the caller's fields array except for
 * the name/alias string pointers, which must outlive the lookup. */
NdecFieldLookup *ndec_field_lookup_build(const struct NdecField *fields, uint16_t n);

/* Find a field index by JSON key. Returns -1 on miss. */
int ndec_field_lookup_find(const NdecFieldLookup *l, const char *key, size_t klen);

/* Release everything owned by the lookup. */
void ndec_field_lookup_free(NdecFieldLookup *l);

#ifdef __cplusplus
}
#endif

#endif /* NDEC_FIELD_LOOKUP_H */
