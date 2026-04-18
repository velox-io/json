/*
 * field_lookup.c: struct-field name -> index lookup, auto-picking
 * bitmap8k / perfect-hash / hashmap based on field count.
 */

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "bind.h"   /* NdecField */
#include "field_lookup.h"

/*
 * Each reflected field contributes one entry for its primary name and
 * (if set) one for its alias. Both entries carry the same `index`,
 * the position in the fields[] array.
 */
typedef struct {
  const char *name;
  uint32_t    name_len;
  uint16_t    index;
} entry;

/*
 * Tier 1: bitmap8k (n_entries <= 8 slots).
 *
 * For each of the first K=8 byte positions, a 256-entry bitmap: bit i
 * set iff entry i has that byte at that position. AND-reduce across
 * positions, AND with len_mask, then either ctz directly (keys shorter
 * than K) or memcmp-verify surviving bits.
 */

#define BM8K_K 8

typedef struct {
  uint8_t   max_key_len;
  uint8_t   prefix_k;
  uint8_t  *bitmap;    /* [prefix_k * 256] */
  uint8_t  *len_mask;  /* [max_key_len + 1] */
  const entry *entries;
  size_t    n;
} bm8k;

static int bm8k_find(const bm8k *b, const char *key, size_t klen) {
  if (klen == 0 || klen > b->max_key_len) return -1;
  uint8_t cur = 0xFF;
  size_t pk = b->prefix_k;
  size_t scan = klen < pk ? klen : pk;
  for (size_t j = 0; j < scan; j++) {
    cur &= b->bitmap[j * 256 + (uint8_t)key[j]];
    if (cur == 0) return -1;
  }
  cur &= b->len_mask[klen];
  if (cur == 0) return -1;
  if (klen <= pk) {
    int slot = __builtin_ctz(cur);
    return (int)b->entries[slot].index;
  }
  while (cur != 0) {
    int slot = __builtin_ctz(cur);
    if (b->entries[slot].name_len == klen &&
        memcmp(b->entries[slot].name, key, klen) == 0)
      return (int)b->entries[slot].index;
    cur &= cur - 1;
  }
  return -1;
}

static bm8k *bm8k_build(const entry *ents, size_t n) {
  if (n == 0 || n > 8) return NULL;
  size_t maxlen = 0;
  for (size_t i = 0; i < n; i++) {
    if (ents[i].name_len > maxlen) maxlen = ents[i].name_len;
  }
  if (maxlen == 0) return NULL; /* can't index empty keys */
  size_t pk = maxlen < BM8K_K ? maxlen : BM8K_K;

  bm8k *b = calloc(1, sizeof(*b));
  if (!b) return NULL;
  b->bitmap   = calloc(pk * 256, 1);
  b->len_mask = calloc(maxlen + 1, 1);
  if (!b->bitmap || !b->len_mask) {
    free(b->bitmap); free(b->len_mask); free(b);
    return NULL;
  }
  b->max_key_len = (uint8_t)maxlen;
  b->prefix_k    = (uint8_t)pk;
  b->entries     = ents;
  b->n           = n;

  for (size_t i = 0; i < n; i++) {
    uint8_t bit = (uint8_t)(1u << i);
    const char *s = ents[i].name;
    size_t L = ents[i].name_len;
    size_t scan = L < pk ? L : pk;
    for (size_t j = 0; j < scan; j++)
      b->bitmap[j * 256 + (uint8_t)s[j]] |= bit;
    b->len_mask[L] |= bit;
  }
  return b;
}

static void bm8k_free(bm8k *b) {
  if (!b) return;
  free(b->bitmap); free(b->len_mask); free(b);
}

/*
 * Tier 2: perfect hash (n_entries <= 32).
 *
 * Search (seed, shift) pairs until all entries land in distinct
 * slots of a 2*n-sized power-of-two table. simple_mixer first
 * (fingerprint from first/last/mid byte + length), fnv1a fallback.
 */

#define PH_MAX_SEED_ATTEMPTS (1u << 16)
#define PH_MAX_ENTRIES       256u

typedef uint64_t (*mixer_fn)(const char *s, size_t n, uint64_t seed);

static inline uint64_t simple_mixer(const char *s, size_t n, uint64_t seed) {
  if (n == 0) return seed * 0x9e3779b97f4a7c15ULL;
  uint64_t first = (uint8_t)s[0];
  uint64_t last  = (uint8_t)s[n - 1];
  uint64_t mid   = (uint8_t)s[n / 2];
  uint64_t h = seed;
  h ^= (uint64_t)n * 0x9e3779b97f4a7c15ULL;
  h ^= first       * 0xbf58476d1ce4e5b9ULL;
  h ^= last        * 0x94d049bb133111ebULL;
  h ^= mid         * 0xff51afd7ed558ccdULL;
  return h;
}

static inline uint64_t fnv1a_mixer(const char *s, size_t n, uint64_t seed) {
  uint64_t h = seed ^ 0xcbf29ce484222325ULL;
  for (size_t i = 0; i < n; i++) {
    h ^= (uint8_t)s[i];
    h *= 0x100000001b3ULL;
  }
  h ^= h >> 33;
  h *= 0xff51afd7ed558ccdULL;
  h ^= h >> 33;
  return h;
}

typedef struct {
  const entry *entries;
  size_t    n;
  uint64_t  seed;
  uint8_t   shift;
  uint8_t  *table;       /* 0xFF = empty; else slot index into entries */
  uint64_t  mask;
  mixer_fn  mixer;
} ph;

static int ph_find(const ph *p, const char *key, size_t klen) {
  uint64_t h = p->mixer(key, klen, p->seed);
  uint64_t slot = (h >> p->shift) & p->mask;
  uint8_t idx = p->table[slot];
  if (idx == 0xFF) return -1;
  const entry *e = &p->entries[idx];
  if (e->name_len != klen) return -1;
  if (memcmp(e->name, key, klen) != 0) return -1;
  return (int)e->index;
}

static ph *ph_try_build(const entry *ents, size_t n, mixer_fn mixer) {
  if (n == 0 || n > PH_MAX_ENTRIES) return NULL;
  size_t table_size = 1; while (table_size < n * 2) table_size <<= 1;
  uint64_t mask = table_size - 1;

  uint64_t *hashes = malloc(sizeof(uint64_t) * n);
  uint8_t  *seen   = calloc(table_size, 1);
  if (!hashes || !seen) { free(hashes); free(seen); return NULL; }
  /* `seen` stores generation stamps for the current (seed, shift)
   * trial, so most retries only bump `gen` instead of clearing the
   * whole table. When the byte wraps we do one memset and restart. */
  uint8_t gen = 1;

  for (uint32_t seed = 0; seed < PH_MAX_SEED_ATTEMPTS; seed++) {
    for (size_t i = 0; i < n; i++)
      hashes[i] = mixer(ents[i].name, ents[i].name_len, seed);
    for (uint8_t shift = 0; shift < 64; shift++) {
      if (gen == 255) { memset(seen, 0, table_size); gen = 1; } else gen++;
      int collision = 0;
      for (size_t i = 0; i < n; i++) {
        uint64_t slot = (hashes[i] >> shift) & mask;
        if (seen[slot] == gen) { collision = 1; break; }
        seen[slot] = gen;
      }
      if (!collision) {
        uint8_t *table = malloc(table_size);
        if (!table) { free(hashes); free(seen); return NULL; }
        memset(table, 0xFF, table_size);
        for (size_t i = 0; i < n; i++) {
          uint64_t slot = (hashes[i] >> shift) & mask;
          table[slot] = (uint8_t)i;
        }
        free(hashes); free(seen);
        ph *p = calloc(1, sizeof(*p));
        if (!p) { free(table); return NULL; }
        p->entries = ents; p->n = n;
        p->seed = seed; p->shift = shift;
        p->table = table; p->mask = mask; p->mixer = mixer;
        return p;
      }
    }
  }
  free(hashes); free(seen);
  return NULL;
}

static void ph_free(ph *p) {
  if (!p) return;
  free(p->table); free(p);
}

/*
 * Tier 3: hashmap (n_entries > 32, or pathological fallback).
 *
 * FNV-1a + linear probing, capacity >= 2*n (load < 0.5). Empty
 * slots are marked with index = 0xFFFF (UINT16_MAX) since field
 * indices fit in uint16_t.
 */

typedef struct {
  const char *key;
  uint32_t    klen;
  uint16_t    index;     /* 0xFFFF = empty */
} hm_slot;

typedef struct {
  hm_slot  *slots;
  size_t    cap;
  size_t    mask;
} hm;

static uint32_t fnv32(const char *s, size_t n) {
  uint32_t h = 2166136261u;
  for (size_t i = 0; i < n; i++) { h ^= (uint8_t)s[i]; h *= 16777619u; }
  return h;
}

static int hm_find(const hm *h, const char *key, size_t klen) {
  size_t pos = fnv32(key, klen) & h->mask;
  for (;;) {
    hm_slot s = h->slots[pos];
    if (s.index == 0xFFFF) return -1;
    if (s.klen == klen && memcmp(s.key, key, klen) == 0) return (int)s.index;
    pos = (pos + 1) & h->mask;
  }
}

static hm *hm_build(const entry *ents, size_t n) {
  size_t cap = 16; while (cap < n * 2) cap <<= 1;
  hm *h = calloc(1, sizeof(*h));
  if (!h) return NULL;
  h->slots = malloc(sizeof(hm_slot) * cap);
  if (!h->slots) { free(h); return NULL; }
  h->cap = cap; h->mask = cap - 1;
  for (size_t i = 0; i < cap; i++) h->slots[i].index = 0xFFFF;
  for (size_t i = 0; i < n; i++) {
    size_t pos = fnv32(ents[i].name, ents[i].name_len) & h->mask;
    while (h->slots[pos].index != 0xFFFF) pos = (pos + 1) & h->mask;
    h->slots[pos].key   = ents[i].name;
    h->slots[pos].klen  = ents[i].name_len;
    h->slots[pos].index = ents[i].index;
  }
  return h;
}

static void hm_free(hm *h) {
  if (!h) return;
  free(h->slots); free(h);
}

typedef enum { LK_BM8K, LK_PH, LK_HM } lk_kind;

struct NdecFieldLookup {
  lk_kind kind;
  entry  *entries;     /* owned; may be NULL for n=0 (degenerate) */
  size_t  n_entries;
  union {
    bm8k *bm;
    ph   *ph;
    hm   *hm;
  };
};

NdecFieldLookup *ndec_field_lookup_build(const NdecField *fields, uint16_t n) {
  /* Collect (name|alias, len, index) entries — alias contributes
   * a second row with the same index. */
  size_t max_entries = (size_t)n * 2;
  entry *ents = max_entries ? malloc(sizeof(entry) * max_entries) : NULL;
  if (max_entries && !ents) return NULL;
  size_t k = 0;
  for (uint16_t i = 0; i < n; i++) {
    if (fields[i].name && fields[i].name_len > 0) {
      ents[k].name     = fields[i].name;
      ents[k].name_len = fields[i].name_len;
      ents[k].index    = i;
      k++;
    }
    if (fields[i].alias && fields[i].alias_len > 0) {
      ents[k].name     = fields[i].alias;
      ents[k].name_len = fields[i].alias_len;
      ents[k].index    = i;
      k++;
    }
  }

  NdecFieldLookup *lk = calloc(1, sizeof(*lk));
  if (!lk) { free(ents); return NULL; }
  lk->entries   = ents;
  lk->n_entries = k;

  if (k == 0) {
    /* No names indexed: degenerate lookup, always returns -1. Use
     * hashmap with 0 entries so find() short-circuits on empty slot. */
    lk->kind = LK_HM;
    lk->hm   = hm_build(ents, 0);
    if (!lk->hm) { free(ents); free(lk); return NULL; }
    return lk;
  }

  if (k <= 8) {
    lk->bm = bm8k_build(ents, k);
    if (lk->bm) { lk->kind = LK_BM8K; return lk; }
    /* fallthrough to ph/hm if bitmap8k build failed (e.g. empty key) */
  }
  if (k <= 32) {
    lk->ph = ph_try_build(ents, k, simple_mixer);
    if (lk->ph) { lk->kind = LK_PH; return lk; }
    lk->ph = ph_try_build(ents, k, fnv1a_mixer);
    if (lk->ph) { lk->kind = LK_PH; return lk; }
  } else if (k <= PH_MAX_ENTRIES) {
    lk->ph = ph_try_build(ents, k, fnv1a_mixer);
    if (lk->ph) { lk->kind = LK_PH; return lk; }
  }

  lk->hm = hm_build(ents, k);
  if (!lk->hm) { free(ents); free(lk); return NULL; }
  lk->kind = LK_HM;
  return lk;
}

int ndec_field_lookup_find(const NdecFieldLookup *lk, const char *key, size_t klen) {
  switch (lk->kind) {
  case LK_BM8K: return bm8k_find(lk->bm, key, klen);
  case LK_PH:   return ph_find(lk->ph, key, klen);
  case LK_HM:   return hm_find(lk->hm, key, klen);
  }
  return -1;
}

void ndec_field_lookup_free(NdecFieldLookup *lk) {
  if (!lk) return;
  switch (lk->kind) {
  case LK_BM8K: bm8k_free(lk->bm); break;
  case LK_PH:   ph_free(lk->ph);   break;
  case LK_HM:   hm_free(lk->hm);   break;
  }
  free(lk->entries);
  free(lk);
}
