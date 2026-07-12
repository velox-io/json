// lookup build path.
//
// Runtime find path. This TU implements the tier construction algorithms plus the public dispatchers:
// ndec_lookup_size_for / ndec_lookup_init / ndec_lookup_get_tier / ndec_lookup_tier_name / ndec_lookup_footprint

#include "lookup.h"

// ---- Layout helpers ----

static ndec_lookup_cmp_kind cmp_kind_for(size_t max_key_len) {
  if (max_key_len <= 16)
    return NDEC_LOOKUP_CMP_16;
  if (max_key_len <= 32)
    return NDEC_LOOKUP_CMP_32;
  if (max_key_len <= 48)
    return NDEC_LOOKUP_CMP_48;
  return NDEC_LOOKUP_CMP_64;
}

static size_t stride_for(size_t max_key_len) {
  if (max_key_len <= 16)
    return 16;
  if (max_key_len <= 32)
    return 32;
  if (max_key_len <= 48)
    return 48;
  return 64;
}

static size_t round_up16(size_t v) {
  return (v + 15u) & ~(size_t)15u;
}

static size_t next_pow2(size_t v) {
  size_t p = 1;
  while (p < v)
    p <<= 1;
  return p;
}

// ---- Tier 1: window ----

static size_t window_size_for(size_t n, size_t max_key_len) {
  size_t stride = stride_for(max_key_len);
  size_t off    = round_up16(sizeof(ndec_lookup_window) + n);
  return off + n * stride;
}

static unsigned window_byte_fwd(const ndec_lookup_key *keys, size_t i, size_t idx) {
  size_t L = keys[i].len;
  return (idx < L) ? (unsigned char)keys[i].str[idx] : (unsigned)'"';
}

static int window_init_forward(ndec_lookup_window *w, const ndec_lookup_key *keys, size_t n, size_t min_len) {
  for (size_t off = 0; off <= min_len; off++) {
    for (size_t shift = 0; shift < 8; shift++) {
      if (shift != 0 && off + 1 > min_len)
        continue;

      int distinct = 1;
      for (size_t i = 0; i < n && distinct; i++) {
        for (size_t j = i + 1; j < n; j++) {
          unsigned lo_i = window_byte_fwd(keys, i, off);
          unsigned hi_i = window_byte_fwd(keys, i, off + 1);
          unsigned lo_j = window_byte_fwd(keys, j, off);
          unsigned hi_j = window_byte_fwd(keys, j, off + 1);
          unsigned vi   = (((lo_i | (hi_i << 8)) >> shift) & 0xFFu);
          unsigned vj   = (((lo_j | (hi_j << 8)) >> shift) & 0xFFu);
          if (vi == vj) {
            distinct = 0;
            break;
          }
        }
      }
      if (!distinct)
        continue;

      w->direction   = 0;
      w->byte_offset = (uint8_t)off;
      w->shift       = (uint8_t)shift;
      for (size_t b = 0; b < 256; b++)
        w->window_to_key[b] = (uint8_t)n;
      for (size_t i = 0; i < n; i++) {
        unsigned lo           = window_byte_fwd(keys, i, off);
        unsigned hi           = window_byte_fwd(keys, i, off + 1);
        unsigned val          = (((lo | (hi << 8)) >> shift) & 0xFFu);
        w->window_to_key[val] = (uint8_t)i;
      }
      return 1;
    }
  }
  return 0;
}

static int window_init_backward(ndec_lookup_window *w, const ndec_lookup_key *keys, size_t n, size_t min_len) {
  for (size_t k = 1; k <= min_len; k++) {
    for (size_t shift = 0; shift < 8; shift++) {
      int distinct = 1;
      for (size_t i = 0; i < n && distinct; i++) {
        for (size_t j = i + 1; j < n; j++) {
          size_t li     = keys[i].len;
          size_t lj     = keys[j].len;
          unsigned lo_i = (unsigned char)keys[i].str[li - k];
          unsigned hi_i = (k == 1) ? (unsigned)'"' : (unsigned char)keys[i].str[li - k + 1];
          unsigned lo_j = (unsigned char)keys[j].str[lj - k];
          unsigned hi_j = (k == 1) ? (unsigned)'"' : (unsigned char)keys[j].str[lj - k + 1];
          unsigned vi   = (((lo_i | (hi_i << 8)) >> shift) & 0xFFu);
          unsigned vj   = (((lo_j | (hi_j << 8)) >> shift) & 0xFFu);
          if (vi == vj) {
            distinct = 0;
            break;
          }
        }
      }
      if (!distinct)
        continue;

      w->direction   = 1;
      w->byte_offset = (uint8_t)k;
      w->shift       = (uint8_t)shift;
      for (size_t b = 0; b < 256; b++)
        w->window_to_key[b] = (uint8_t)n;
      for (size_t i = 0; i < n; i++) {
        size_t li             = keys[i].len;
        unsigned lo           = (unsigned char)keys[i].str[li - k];
        unsigned hi           = (k == 1) ? (unsigned)'"' : (unsigned char)keys[i].str[li - k + 1];
        unsigned val          = (((lo | (hi << 8)) >> shift) & 0xFFu);
        w->window_to_key[val] = (uint8_t)i;
      }
      return 1;
    }
  }
  return 0;
}

static int window_build(ndec_lookup_window *w, const ndec_lookup_key *keys, size_t n) {
  if (n == 0 || n > NDEC_LOOKUP_MAX_KEYS)
    return 0;
  w->n = n;

  size_t min_len = keys[0].len;
  size_t max_len = min_len;
  for (size_t i = 1; i < n; i++) {
    size_t L = keys[i].len;
    if (L < min_len)
      min_len = L;
    if (L > max_len)
      max_len = L;
  }
  if (max_len >= NDEC_LOOKUP_KEY_STRIDE_MAX)
    return 0;
  w->max_key_len = max_len;

  int ok = window_init_forward(w, keys, n, min_len);
  if (!ok)
    ok = window_init_backward(w, keys, n, min_len);
  if (!ok)
    return 0;

  w->cmp           = cmp_kind_for(max_len);
  w->stride        = stride_for(max_len);
  w->key_bytes_off = round_up16(sizeof(ndec_lookup_window) + n);

  uint8_t *klen = (uint8_t *)w + sizeof(ndec_lookup_window);
  for (size_t i = 0; i < n; i++) {
    size_t L  = keys[i].len;
    klen[i]   = (uint8_t)L;
    char *dst = (char *)w + w->key_bytes_off + i * w->stride;
    __builtin_memset(dst, 0, w->stride);
    __builtin_memcpy(dst, keys[i].str, L);
  }
  return 1;
}

// ---- Tier 2: gperf ----

static size_t gperf_size_for(size_t n, size_t max_key_len) {
  size_t off = sizeof(ndec_lookup_gperf);
  off        = round_up16(off + NDEC_LOOKUP_GPERF_MAX_POS * 256u);
  off += NDEC_LOOKUP_GPERF_MAX_TAB;
  off = round_up16(off + n);
  return off + n * stride_for(max_key_len);
}

static unsigned gperf_char_at(const char *key, size_t len, uint8_t pos) {
  if (pos == NDEC_LOOKUP_GPERF_LAST_CH)
    return (unsigned char)key[len - 1];
  if ((size_t)pos >= len)
    return 256;
  return (unsigned char)key[pos];
}

static size_t gperf_undistinguished_pairs(const ndec_lookup_key *keys, size_t n, const uint8_t *positions,
                                          size_t num_positions, size_t modulus) {
  size_t count = 0;
  for (size_t i = 0; i < n; i++) {
    size_t li = keys[i].len;
    for (size_t j = i + 1; j < n; j++) {
      size_t lj = keys[j].len;
      if (li % modulus != lj % modulus)
        continue;
      int distinguished = 0;
      for (size_t p = 0; p < num_positions; p++) {
        if (gperf_char_at(keys[i].str, li, positions[p]) != gperf_char_at(keys[j].str, lj, positions[p])) {
          distinguished = 1;
          break;
        }
      }
      if (!distinguished)
        count++;
    }
  }
  return count;
}

static size_t gperf_select_positions(const ndec_lookup_key *keys, size_t n, size_t max_key_len, size_t modulus,
                                     uint8_t *out_positions) {
  uint8_t candidates[257];
  size_t n_cand = 0;
  for (size_t p = 0; p < max_key_len && n_cand < 256; p++)
    candidates[n_cand++] = (uint8_t)p;
  candidates[n_cand++] = NDEC_LOOKUP_GPERF_LAST_CH;

  size_t num_pos = 0;
  if (gperf_undistinguished_pairs(keys, n, out_positions, 0, modulus) == 0)
    return 0;

  while (num_pos < NDEC_LOOKUP_GPERF_MAX_POS) {
    size_t best_reduction = 0;
    int best_ci           = -1;
    size_t before         = gperf_undistinguished_pairs(keys, n, out_positions, num_pos, modulus);
    for (size_t ci = 0; ci < n_cand; ci++) {
      int already = 0;
      for (size_t p = 0; p < num_pos; p++)
        if (out_positions[p] == candidates[ci]) {
          already = 1;
          break;
        }
      if (already)
        continue;
      out_positions[num_pos] = candidates[ci];
      size_t after           = gperf_undistinguished_pairs(keys, n, out_positions, num_pos + 1, modulus);
      size_t reduction       = before - after;
      if (reduction > best_reduction) {
        best_reduction = reduction;
        best_ci        = (int)ci;
      }
    }
    if (best_ci < 0)
      break;
    out_positions[num_pos] = candidates[best_ci];
    num_pos++;
    if (gperf_undistinguished_pairs(keys, n, out_positions, num_pos, modulus) == 0)
      return num_pos;
  }
  if (gperf_undistinguished_pairs(keys, n, out_positions, num_pos, modulus) != 0)
    return SIZE_MAX;
  return num_pos;
}

typedef struct {
  uint8_t num_positions;
  uint8_t positions[NDEC_LOOKUP_GPERF_MAX_POS];
  uint8_t asso_values[NDEC_LOOKUP_GPERF_MAX_POS][256];
  uint8_t slot_to_key[NDEC_LOOKUP_GPERF_MAX_TAB];
} gperf_scratch;

/* Build-time symbol descriptor used by gperf_try's greedy asso assignment. */
struct gperf_sym {
  uint8_t pos;
  unsigned ch;
  size_t freq;
};

/* gperf builder working set (~70 KiB). Lives in caller-provided scratch, not
 * on the C stack: the //go:nosplit lookup trampoline runs C on the goroutine
 * stack (a big frame would overflow it), and the freestanding .syso cannot use
 * large static/TLS storage. See ndec_lookup_config.scratch. */
typedef struct {
  gperf_scratch out; /* result the builder copies into final storage */
  unsigned kchars[NDEC_LOOKUP_MAX_KEYS][NDEC_LOOKUP_GPERF_MAX_POS];
  struct gperf_sym syms[NDEC_LOOKUP_GPERF_MAX_POS * 256];
  uint64_t salt[NDEC_LOOKUP_GPERF_MAX_POS][256];
  uint64_t sig[NDEC_LOOKUP_MAX_KEYS];
  size_t phash[NDEC_LOOKUP_MAX_KEYS];
  size_t order[NDEC_LOOKUP_MAX_KEYS];
  size_t slot_gen[NDEC_LOOKUP_GPERF_MAX_TAB];
  size_t freq[257];
} gperf_workspace;

/* hand builder working set (~10 KiB). Same caller-scratch rationale. */
typedef struct {
  size_t bucket_of[NDEC_LOOKUP_MAX_KEYS];
  size_t buckets[256];
  size_t counts[256];
  size_t bucket_keys[NDEC_LOOKUP_MAX_KEYS];
  size_t bucket_slots[NDEC_LOOKUP_MAX_KEYS];
} hand_workspace;

/* gperf and hand builds run sequentially (never concurrently within one
 * ndec_lookup_init), so their scratch overlays one buffer. */
typedef union {
  gperf_workspace gperf;
  hand_workspace hand;
} ndec_lookup_build_scratch;

size_t ndec_lookup_scratch_size(void) {
  return sizeof(ndec_lookup_build_scratch);
}

static int gperf_try(gperf_workspace *ws, size_t max_key_len, const ndec_lookup_key *keys, size_t n,
                     size_t modulus) {
  gperf_scratch *gs = &ws->out;
  size_t np = gperf_select_positions(keys, n, max_key_len, modulus, gs->positions);
  if (np == SIZE_MAX)
    return 0;
  gs->num_positions = (uint8_t)np;
  __builtin_memset(gs->asso_values, 0, sizeof(gs->asso_values));

  if (np == 0) {
    for (size_t s = 0; s < modulus; s++)
      gs->slot_to_key[s] = (uint8_t)n;
    for (size_t i = 0; i < n; i++) {
      size_t slot = keys[i].len % modulus;
      if (gs->slot_to_key[slot] != n)
        return 0;
      gs->slot_to_key[slot] = (uint8_t)i;
    }
    return 1;
  }

  unsigned(*kchars)[NDEC_LOOKUP_GPERF_MAX_POS] = ws->kchars;
  for (size_t k = 0; k < n; k++) {
    size_t L = keys[k].len;
    for (size_t p = 0; p < np; p++)
      kchars[k][p] = gperf_char_at(keys[k].str, L, gs->positions[p]);
  }

  struct gperf_sym *syms = ws->syms;
  size_t nsyms           = 0;
  size_t *freq           = ws->freq;
  for (size_t p = 0; p < np; p++) {
    __builtin_memset(freq, 0, 257 * sizeof(freq[0]));
    for (size_t k = 0; k < n; k++) {
      unsigned c = kchars[k][p];
      if (c < 256)
        freq[c]++;
    }
    for (size_t c = 0; c < 256; c++) {
      if (freq[c] > 0) {
        syms[nsyms].pos  = (uint8_t)p;
        syms[nsyms].ch   = (unsigned)c;
        syms[nsyms].freq = freq[c];
        nsyms++;
      }
    }
  }
  for (size_t i = 1; i < nsyms; i++) {
    struct gperf_sym x = syms[i];
    size_t j           = i;
    while (j > 0 && syms[j - 1].freq < x.freq) {
      syms[j] = syms[j - 1];
      j--;
    }
    syms[j] = x;
  }

  uint64_t(*salt)[256] = ws->salt;
  {
    uint64_t s = 0x9e3779b97f4a7c15ULL;
    for (size_t p = 0; p < np; p++) {
      for (size_t c = 0; c < 256; c++) {
        s          = s * 6364136223846793005ULL + 1442695040888963407ULL;
        salt[p][c] = s;
      }
    }
  }
  uint64_t *sig = ws->sig;
  for (size_t k = 0; k < n; k++) {
    uint64_t s = 0;
    for (size_t p = 0; p < np; p++) {
      unsigned c = kchars[k][p];
      if (c < 256)
        s ^= salt[p][c];
    }
    sig[k] = s;
  }

  size_t *phash = ws->phash;
  for (size_t k = 0; k < n; k++)
    phash[k] = keys[k].len;

  size_t *order = ws->order;
  for (size_t k = 0; k < n; k++)
    order[k] = k;

  size_t *slot_gen = ws->slot_gen;
  __builtin_memset(slot_gen, 0, modulus * sizeof(slot_gen[0]));
  size_t gen = 0;

  size_t search_limit = modulus < 32 ? 32 : modulus;

  for (size_t si = 0; si < nsyms; si++) {
    uint8_t sp       = syms[si].pos;
    unsigned sc      = syms[si].ch;
    uint64_t sp_salt = salt[sp][sc];

    for (size_t k = 0; k < n; k++)
      if (kchars[k][sp] == sc)
        sig[k] ^= sp_salt;

    for (size_t i = 1; i < n; i++) {
      size_t x    = order[i];
      uint64_t xs = sig[x];
      size_t j    = i;
      while (j > 0 && sig[order[j - 1]] > xs) {
        order[j] = order[j - 1];
        j--;
      }
      order[j] = x;
    }

    int found = 0;
    for (size_t v = 0; v < search_limit && !found; v++) {
      int collision = 0;
      size_t ci     = 0;
      while (ci < n && !collision) {
        uint64_t class_sig = sig[order[ci]];
        size_t cj          = ci;
        while (cj < n && sig[order[cj]] == class_sig)
          cj++;
        if (cj - ci > 1) {
          gen++;
          for (size_t x = ci; x < cj; x++) {
            size_t k = order[x];
            size_t h = phash[k];
            if (kchars[k][sp] == sc)
              h += v;
            h &= (modulus - 1);
            if (slot_gen[h] == gen) {
              collision = 1;
              break;
            }
            slot_gen[h] = gen;
          }
        }
        ci = cj;
      }
      if (!collision) {
        gs->asso_values[sp][sc] = (uint8_t)v;
        for (size_t k = 0; k < n; k++)
          if (kchars[k][sp] == sc)
            phash[k] += v;
        found = 1;
      }
    }
    if (!found)
      return 0;
  }

  for (size_t s = 0; s < modulus; s++)
    gs->slot_to_key[s] = (uint8_t)n;
  for (size_t i = 0; i < n; i++) {
    size_t slot = phash[i] & (modulus - 1);
    if (gs->slot_to_key[slot] != n)
      return 0;
    gs->slot_to_key[slot] = (uint8_t)i;
  }
  return 1;
}

static int gperf_build(ndec_lookup_gperf *gp, const ndec_lookup_key *keys, size_t n, gperf_workspace *ws) {
  if (n == 0 || n > NDEC_LOOKUP_MAX_KEYS)
    return 0;
  size_t max_len = 0;
  for (size_t i = 0; i < n; i++) {
    size_t L = keys[i].len;
    if (L > max_len)
      max_len = L;
  }
  if (max_len >= NDEC_LOOKUP_KEY_STRIDE_MAX)
    return 0;
  gp->n           = n;
  gp->max_key_len = max_len;

  for (size_t m = next_pow2(n); m <= NDEC_LOOKUP_GPERF_MAX_TAB; m <<= 1) {
    if (gperf_try(ws, max_len, keys, n, m)) {
      gperf_scratch *scratch = &ws->out;
      size_t np              = scratch->num_positions;
      gp->num_positions      = scratch->num_positions;
      gp->table_size         = m;
      gp->cmp                = cmp_kind_for(max_len);
      gp->stride             = stride_for(max_len);
      __builtin_memcpy(gp->positions, scratch->positions, NDEC_LOOKUP_GPERF_MAX_POS);

      size_t off        = sizeof(ndec_lookup_gperf);
      gp->asso_off      = off;
      off               = round_up16(off + np * 256u);
      gp->slots_off     = off;
      off               = round_up16(off + m);
      gp->key_len_off   = off;
      off               = round_up16(off + n);
      gp->key_bytes_off = off;

      if (np > 0)
        __builtin_memcpy((uint8_t *)gp + gp->asso_off, scratch->asso_values, np * 256u);
      __builtin_memcpy((uint8_t *)gp + gp->slots_off, scratch->slot_to_key, m);

      uint8_t *klen = (uint8_t *)gp + gp->key_len_off;
      for (size_t i = 0; i < n; i++) {
        size_t L  = keys[i].len;
        klen[i]   = (uint8_t)L;
        char *dst = (char *)gp + gp->key_bytes_off + i * gp->stride;
        __builtin_memset(dst, 0, gp->stride);
        __builtin_memcpy(dst, keys[i].str, L);
      }
      return 1;
    }
  }
  return 0;
}

// ---- Tier 3: hash-and-displace ----

static size_t hand_size_for(size_t n, size_t max_key_len) {
  size_t off = round_up16(sizeof(ndec_lookup_hand) + n);
  return off + n * stride_for(max_key_len);
}

static size_t hand_bucket_hash(const char *p, size_t len) {
  size_t c0 = len ? (unsigned char)p[0] : 0;
  size_t c1 = len ? (unsigned char)p[len - 1] : 0;
  return (c0 + c1 * 3 + len * 17) & 0xFF;
}

static size_t hand_safe_char(const char *p, size_t len, size_t idx) {
  size_t has = (size_t)(idx < len);
  size_t si  = idx & (size_t)(0 - has);
  return (unsigned char)p[si] & (size_t)(0 - has);
}

static size_t hand_key_hash_2(const char *p, size_t len) {
  size_t kc = len;
  kc        = kc * 31 + (len ? (unsigned char)p[0] : 0);
  kc        = kc * 31 + hand_safe_char(p, len, 1);
  return kc;
}

static size_t hand_key_hash_4(const char *p, size_t len) {
  size_t kc = len;
  kc        = kc * 31 + (len ? (unsigned char)p[0] : 0);
  kc        = kc * 31 + hand_safe_char(p, len, 1);
  kc        = kc * 31 + hand_safe_char(p, len, 2);
  kc        = kc * 31 + hand_safe_char(p, len, 3);
  return kc;
}

static int hand_try_placement(ndec_lookup_hand *hd, const ndec_lookup_key *keys, size_t n, const size_t *bucket_of,
                              const size_t *buckets_ordered, size_t num_buckets,
                              size_t (*key_hash)(const char *, size_t), hand_workspace *ws) {
  size_t M = hd->table_size;
  __builtin_memset(hd->displacement, 0, sizeof(hd->displacement));
  for (size_t s = 0; s < M; s++)
    hd->slot_to_key[s] = (uint8_t)n;

  size_t *bucket_keys  = ws->bucket_keys;
  size_t *bucket_slots = ws->bucket_slots;
  for (size_t b = 0; b < num_buckets; b++) {
    size_t ch       = buckets_ordered[b];
    size_t bk_count = 0;
    for (size_t i = 0; i < n; i++)
      if (bucket_of[i] == ch)
        bucket_keys[bk_count++] = i;
    int placed   = 0;
    size_t max_d = M < 255 ? M : 255;
    for (size_t d = 0; d < max_d && !placed; d++) {
      int ok = 1;
      for (size_t k = 0; k < bk_count && ok; k++) {
        size_t L = keys[bucket_keys[k]].len;
        size_t s = (d + key_hash(keys[bucket_keys[k]].str, L)) & hd->mask;
        if (hd->slot_to_key[s] != (uint8_t)n) {
          ok = 0;
          break;
        }
        for (size_t k2 = 0; k2 < k; k2++)
          if (bucket_slots[k2] == s) {
            ok = 0;
            break;
          }
        bucket_slots[k] = s;
      }
      if (ok) {
        hd->displacement[ch] = (uint8_t)d;
        for (size_t k = 0; k < bk_count; k++)
          hd->slot_to_key[bucket_slots[k]] = (uint8_t)bucket_keys[k];
        placed = 1;
      }
    }
    if (!placed)
      return 0;
  }
  return 1;
}

static int hand_try_size(ndec_lookup_hand *hd, const ndec_lookup_key *keys, size_t n, size_t M,
                         hand_workspace *ws) {
  if (M > NDEC_LOOKUP_HD_MAX_TABLE)
    return 0;
  hd->table_size = M;
  hd->mask       = M - 1;
  hd->n          = n;

  size_t *bucket_of = ws->bucket_of;
  for (size_t i = 0; i < n; i++)
    bucket_of[i] = hand_bucket_hash(keys[i].str, keys[i].len);

  size_t *buckets    = ws->buckets;
  size_t *counts     = ws->counts;
  size_t num_buckets = 0;
  for (size_t i = 0; i < n; i++) {
    size_t bk = bucket_of[i];
    int found = 0;
    for (size_t b = 0; b < num_buckets; b++)
      if (buckets[b] == bk) {
        counts[b]++;
        found = 1;
        break;
      }
    if (!found) {
      buckets[num_buckets] = bk;
      counts[num_buckets]  = 1;
      num_buckets++;
    }
  }
  for (size_t i = 1; i < num_buckets; i++) {
    for (size_t j = i; j > 0 && counts[j] > counts[j - 1]; j--) {
      size_t tb      = buckets[j];
      buckets[j]     = buckets[j - 1];
      buckets[j - 1] = tb;
      size_t tc      = counts[j];
      counts[j]      = counts[j - 1];
      counts[j - 1]  = tc;
    }
  }

  if (hand_try_placement(hd, keys, n, bucket_of, buckets, num_buckets, hand_key_hash_2, ws)) {
    hd->variant = NDEC_LOOKUP_HD_HASH_2;
    return 1;
  }
  if (hand_try_placement(hd, keys, n, bucket_of, buckets, num_buckets, hand_key_hash_4, ws)) {
    hd->variant = NDEC_LOOKUP_HD_HASH_4;
    return 1;
  }
  return 0;
}

static int hand_build(ndec_lookup_hand *hd, const ndec_lookup_key *keys, size_t n, hand_workspace *ws) {
  if (n == 0 || n > NDEC_LOOKUP_MAX_KEYS)
    return 0;
  size_t max_len = 0;
  for (size_t i = 0; i < n; i++) {
    size_t L = keys[i].len;
    if (L > max_len)
      max_len = L;
  }
  if (max_len >= NDEC_LOOKUP_KEY_STRIDE_MAX)
    return 0;
  hd->max_key_len = max_len;

  for (size_t M = next_pow2(n); M <= NDEC_LOOKUP_HD_MAX_TABLE; M <<= 1) {
    if (hand_try_size(hd, keys, n, M, ws)) {
      hd->cmp           = cmp_kind_for(max_len);
      hd->stride        = stride_for(max_len);
      hd->key_bytes_off = round_up16(sizeof(ndec_lookup_hand) + n);
      uint8_t *klen     = (uint8_t *)hd + sizeof(ndec_lookup_hand);
      for (size_t i = 0; i < n; i++) {
        size_t L  = keys[i].len;
        klen[i]   = (uint8_t)L;
        char *dst = (char *)hd + hd->key_bytes_off + i * hd->stride;
        __builtin_memset(dst, 0, hd->stride);
        __builtin_memcpy(dst, keys[i].str, L);
      }
      return 1;
    }
  }
  return 0;
}

// ---- Tier 4: table (fallback) ----

static size_t table_size_for(size_t n, size_t total_key_bytes) {
  size_t cap = 16;
  while (cap < n * 2)
    cap <<= 1;
  if (cap > 65536)
    return 0;
  size_t off = offsetof(ndec_lookup_table, slots) + cap * sizeof(ndec_lookup_fb_slot);
  return off + total_key_bytes;
}

static uint64_t table_hash(const char *p, size_t len) {
  uint64_t h = 0xcbf29ce484222325ULL;
  for (size_t i = 0; i < len; i++) {
    h ^= (uint8_t)p[i];
    h *= 0x100000001b3ULL;
  }
  h ^= h >> 33;
  h *= 0xff51afd7ed558ccdULL;
  h ^= h >> 33;
  return h;
}

static int table_build(ndec_lookup_table *fb, const ndec_lookup_key *keys, size_t n, size_t storage_size) {
  if (n == 0 || n > NDEC_LOOKUP_MAX_KEYS)
    return 0;
  size_t total_key_bytes = 0;
  for (size_t i = 0; i < n; i++)
    total_key_bytes += keys[i].len;
  size_t needed = table_size_for(n, total_key_bytes);
  if (needed == 0 || needed > storage_size)
    return 0;

  size_t cap = 16;
  while (cap < n * 2)
    cap <<= 1;

  fb->n             = n;
  fb->cap           = cap;
  fb->mask          = cap - 1;
  fb->key_data_off  = offsetof(ndec_lookup_table, slots) + cap * sizeof(ndec_lookup_fb_slot);
  fb->key_data_size = total_key_bytes;

  for (size_t i = 0; i < cap; i++) {
    fb->slots[i].key_off  = 0;
    fb->slots[i].key_len  = 0;
    fb->slots[i].value_p1 = 0;
  }

  char *key_data      = (char *)fb + fb->key_data_off;
  size_t write_offset = 0;
  for (size_t i = 0; i < n; i++) {
    size_t L = keys[i].len;
    __builtin_memcpy(key_data + write_offset, keys[i].str, L);
    uint64_t h = table_hash(keys[i].str, L);
    size_t pos = h & fb->mask;
    while (fb->slots[pos].value_p1 != 0)
      pos = (pos + 1) & fb->mask;
    fb->slots[pos].key_off  = (uint32_t)(fb->key_data_off + write_offset);
    fb->slots[pos].key_len  = (uint16_t)L;
    fb->slots[pos].value_p1 = (uint16_t)(i + 1);
    write_offset += L;
  }
  return 1;
}

// ---- Public dispatcher ----

static int validate_keys(const ndec_lookup_key *keys, size_t n, ndec_lookup_tier_mask tiers,
                         size_t *max_seen_len) {
  if (n == 0)
    return NDEC_LOOKUP_ERR_KEYS_EMPTY;
  if (n > NDEC_LOOKUP_MAX_KEYS)
    return NDEC_LOOKUP_ERR_KEYS_TOO_MANY;
  if (!keys) {
    *max_seen_len = NDEC_LOOKUP_KEY_STRIDE_MAX - 1;
    return 0;
  }
  size_t max_len    = 0;
  int allow_over_63 = (tiers & NDEC_LOOKUP_TIER_TABLE) != 0;
  for (size_t i = 0; i < n; i++) {
    size_t L = keys[i].len;
    if (L == 0)
      return NDEC_LOOKUP_ERR_KEY_EMPTY;
    if (L >= NDEC_LOOKUP_KEY_STRIDE_MAX && !allow_over_63)
      return NDEC_LOOKUP_ERR_KEY_TOO_LONG;
    for (size_t j = 0; j < L; j++) {
      unsigned char c = (unsigned char)keys[i].str[j];
      if (c == 0x00 || c == 0x22 || c == 0x5C)
        return NDEC_LOOKUP_ERR_KEY_INVALID_BYTE;
    }
    if (L > max_len)
      max_len = L;
  }
  for (size_t i = 0; i < n; i++)
    for (size_t j = i + 1; j < n; j++)
      if (keys[i].len == keys[j].len && __builtin_memcmp(keys[i].str, keys[j].str, keys[i].len) == 0)
        return NDEC_LOOKUP_ERR_KEY_DUPLICATE;
  *max_seen_len = max_len;
  return 0;
}

static size_t size_for_tier(ndec_lookup_tier tier, size_t n, size_t max_len, size_t total_key_bytes) {
  switch (tier) {
  case NDEC_LOOKUP_TIER_WINDOW:
    return window_size_for(n, max_len);
  case NDEC_LOOKUP_TIER_GPERF:
    return gperf_size_for(n, max_len);
  case NDEC_LOOKUP_TIER_HAND:
    return hand_size_for(n, max_len);
  case NDEC_LOOKUP_TIER_TABLE:
    return table_size_for(n, total_key_bytes);
  default:
    return 0;
  }
}

size_t ndec_lookup_size_for(const ndec_lookup_config *cfg) {
  if (!cfg)
    return 0;
  ndec_lookup_tier_mask tiers = cfg->tiers ? cfg->tiers : NDEC_LOOKUP_TIERS_ALL;
  size_t max_len              = 0;
  int err                     = validate_keys(cfg->keys, cfg->n, tiers, &max_len);
  if (err < 0)
    return 0;
  size_t total_key_bytes = 0;
  if (cfg->keys) {
    for (size_t i = 0; i < cfg->n; i++)
      total_key_bytes += cfg->keys[i].len;
  } else {
    total_key_bytes = (size_t)cfg->n * (NDEC_LOOKUP_KEY_STRIDE_MAX - 1);
  }
  size_t max_needed                     = 0;
  static const ndec_lookup_tier order[] = {NDEC_LOOKUP_TIER_WINDOW, NDEC_LOOKUP_TIER_GPERF,
                                           NDEC_LOOKUP_TIER_HAND, NDEC_LOOKUP_TIER_TABLE};
  for (size_t i = 0; i < sizeof(order) / sizeof(order[0]); i++) {
    if (!(tiers & order[i]))
      continue;
    size_t s = size_for_tier(order[i], cfg->n, max_len, total_key_bytes);
    if (s > max_needed)
      max_needed = s;
  }
  return max_needed;
}

static ndec_lookup_tier try_tier(ndec_lookup_tier tier, void *storage, size_t storage_size,
                                 const ndec_lookup_key *keys, size_t n, size_t max_len, size_t total_key_bytes,
                                 ndec_lookup_build_scratch *scratch) {
  size_t needed = size_for_tier(tier, n, max_len, total_key_bytes);
  if (needed == 0 || storage_size < needed)
    return NDEC_LOOKUP_TIER_NONE;
  int ok = 0;
  switch (tier) {
  case NDEC_LOOKUP_TIER_WINDOW: {
    ndec_lookup_window *w = (ndec_lookup_window *)storage;
    w->kind               = tier;
    ok                    = window_build(w, keys, n);
    break;
  }
  case NDEC_LOOKUP_TIER_GPERF: {
    ndec_lookup_gperf *gp = (ndec_lookup_gperf *)storage;
    gp->kind              = tier;
    ok                    = gperf_build(gp, keys, n, &scratch->gperf);
    break;
  }
  case NDEC_LOOKUP_TIER_HAND: {
    ndec_lookup_hand *hd = (ndec_lookup_hand *)storage;
    hd->kind             = tier;
    ok                   = hand_build(hd, keys, n, &scratch->hand);
    break;
  }
  case NDEC_LOOKUP_TIER_TABLE: {
    ndec_lookup_table *fb = (ndec_lookup_table *)storage;
    fb->kind              = tier;
    ok                    = table_build(fb, keys, n, storage_size);
    break;
  }
  default:
    return NDEC_LOOKUP_TIER_NONE;
  }
  return ok ? tier : NDEC_LOOKUP_TIER_NONE;
}

int ndec_lookup_init(ndec_lookup *storage, size_t storage_size, const ndec_lookup_config *cfg) {
  if (!storage || !cfg || !cfg->keys)
    return NDEC_LOOKUP_ERR_NULL_ARG;
  ndec_lookup_tier_mask tiers = cfg->tiers ? cfg->tiers : NDEC_LOOKUP_TIERS_ALL;
  size_t max_len              = 0;
  int err                     = validate_keys(cfg->keys, cfg->n, tiers, &max_len);
  if (err < 0)
    return err;
  size_t total_key_bytes = 0;
  for (size_t i = 0; i < cfg->n; i++)
    total_key_bytes += cfg->keys[i].len;

  // gperf/hand builders need caller scratch; WINDOW/TABLE do not. Require a
  // sufficient scratch buffer only when a scratch-using tier is in the mask.
  int needs_scratch = (tiers & (NDEC_LOOKUP_TIER_GPERF | NDEC_LOOKUP_TIER_HAND)) != 0;
  if (needs_scratch && (cfg->scratch == NULL || cfg->scratch_size < sizeof(ndec_lookup_build_scratch)))
    return NDEC_LOOKUP_ERR_SCRATCH_TOO_SMALL;
  ndec_lookup_build_scratch *scratch = (ndec_lookup_build_scratch *)cfg->scratch;

  static const ndec_lookup_tier order[] = {NDEC_LOOKUP_TIER_WINDOW, NDEC_LOOKUP_TIER_GPERF,
                                           NDEC_LOOKUP_TIER_HAND, NDEC_LOOKUP_TIER_TABLE};
  int saw_size_error                    = 0;
  for (size_t i = 0; i < sizeof(order) / sizeof(order[0]); i++) {
    if (!(tiers & order[i]))
      continue;
    size_t needed = size_for_tier(order[i], cfg->n, max_len, total_key_bytes);
    if (needed == 0)
      continue;
    if (storage_size < needed) {
      saw_size_error = 1;
      continue;
    }
    ndec_lookup_tier picked =
        try_tier(order[i], storage, storage_size, cfg->keys, cfg->n, max_len, total_key_bytes, scratch);
    if (picked != NDEC_LOOKUP_TIER_NONE)
      return (int)picked;
  }
  return saw_size_error ? NDEC_LOOKUP_ERR_STORAGE_TOO_SMALL : NDEC_LOOKUP_ERR_NO_TIER_MATCHES;
}

ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l) {
  return *(const ndec_lookup_tier *)l;
}

const char *ndec_lookup_tier_name(ndec_lookup_tier t) {
  switch (t) {
  case NDEC_LOOKUP_TIER_WINDOW:
    return "window";
  case NDEC_LOOKUP_TIER_GPERF:
    return "gperf";
  case NDEC_LOOKUP_TIER_HAND:
    return "hand";
  case NDEC_LOOKUP_TIER_TABLE:
    return "table";
  case NDEC_LOOKUP_TIER_NONE:
    return "none";
  }
  return "unknown";
}

const char *ndec_lookup_tier_name_ex(const ndec_lookup *l) {
  ndec_lookup_tier t = ndec_lookup_get_tier(l);
  if (t != NDEC_LOOKUP_TIER_WINDOW)
    return ndec_lookup_tier_name(t);
  const ndec_lookup_window *w = (const ndec_lookup_window *)l;
  return w->direction ? "window_rev" : "window_fwd";
}

size_t ndec_lookup_footprint(const ndec_lookup *l) {
  switch (ndec_lookup_get_tier(l)) {
  case NDEC_LOOKUP_TIER_WINDOW: {
    const ndec_lookup_window *w = (const ndec_lookup_window *)l;
    return w->key_bytes_off + w->n * w->stride;
  }
  case NDEC_LOOKUP_TIER_GPERF: {
    const ndec_lookup_gperf *gp = (const ndec_lookup_gperf *)l;
    return gp->key_bytes_off + gp->n * gp->stride;
  }
  case NDEC_LOOKUP_TIER_HAND: {
    const ndec_lookup_hand *hd = (const ndec_lookup_hand *)l;
    return hd->key_bytes_off + hd->n * hd->stride;
  }
  case NDEC_LOOKUP_TIER_TABLE: {
    const ndec_lookup_table *fb = (const ndec_lookup_table *)l;
    return fb->key_data_off + fb->key_data_size;
  }
  default:
    return 0;
  }
}
