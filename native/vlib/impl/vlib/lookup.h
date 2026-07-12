// ndec_lookup: perfect hash key selector for JSON field dispatch.
//
// Given a fixed set of keys, builds a tiered dispatcher into caller-provided
// storage and returns a lookup handle for O(1) queries. Zero-allocation:
// the Go side owns storage lifetime, this library only writes into it.
//
// Runtime lookup returns the key's index in [0, n), or -1 on miss.
//
// Tiers (cheap to general; init tries them in order and picks the first fit):
//
//   WINDOW  one 8-bit window (2-byte read + shift + 256-entry table).
//           Cheapest; works when a single window discriminates all keys.
//   GPERF   gperf-style perfect hash: multiple positions with per-position
//           asso_values. Handles heavy shared prefixes/suffixes.
//   HAND    hash-and-displace perfect hash. Powerful fallback for most
//           key sets up to 63 bytes.
//   TABLE   FNV-1a hashtable with linear probing. Ultimate fallback for
//           very large key sets or keys longer than 63 bytes.
//
// WINDOW..HAND are perfect hashes (at most one candidate per slot).
// TABLE uses linear probing.
//
// Contracts:
//   - Keys must be non-empty and distinct.
//   - Keys must not contain 0x00, 0x22 ('"'), or 0x5C ('\\').
//   - WINDOW..HAND require key length <= 63 bytes; TABLE has no upper bound.
//   - ndec_lookup_find: `key.str` must sit in a padded buffer with at least
//     64 bytes of trailing readable padding , AND key.str[key.len] must be '"'.
//     Uniform for all tiers.
//   - `key.len` must be the actual key length (never scanned by find).
//   - Keys are copied into the lookup at init time. The caller may free the
//     input keys array immediately after ndec_lookup_init returns.

#ifndef NDEC_LOOKUP_H
#define NDEC_LOOKUP_H

#include <stddef.h>
#include <stdint.h>

#include "macros.h"

#if defined(__aarch64__) || defined(__ARM_NEON)
#include <arm_neon.h>
#define NDEC_LOOKUP_HAS_NEON 1
#else
#define NDEC_LOOKUP_HAS_NEON 0
#endif
#if defined(__SSE2__)
#include <emmintrin.h>
#define NDEC_LOOKUP_HAS_SSE2 1
#else
#define NDEC_LOOKUP_HAS_SSE2 0
#endif

// ---- Public types ----

typedef struct {
  const char *str;
  size_t len;
} ndec_lookup_key;

typedef enum {
  NDEC_LOOKUP_TIER_NONE   = 0,
  NDEC_LOOKUP_TIER_WINDOW = 1u << 0,
  NDEC_LOOKUP_TIER_GPERF  = 1u << 1,
  NDEC_LOOKUP_TIER_HAND   = 1u << 2,
  NDEC_LOOKUP_TIER_TABLE  = 1u << 3,
} ndec_lookup_tier;

typedef unsigned ndec_lookup_tier_mask;
// clang-format off
#define NDEC_LOOKUP_TIERS_ALL     ((ndec_lookup_tier_mask)(NDEC_LOOKUP_TIER_WINDOW | NDEC_LOOKUP_TIER_GPERF | \
                                                           NDEC_LOOKUP_TIER_HAND  | NDEC_LOOKUP_TIER_TABLE))
#define NDEC_LOOKUP_TIERS_PERFECT ((ndec_lookup_tier_mask)(NDEC_LOOKUP_TIER_WINDOW | NDEC_LOOKUP_TIER_GPERF | \
                                                           NDEC_LOOKUP_TIER_HAND))
// clang-format on

typedef enum {
  NDEC_LOOKUP_ERR_NULL_ARG          = -1,
  NDEC_LOOKUP_ERR_KEYS_EMPTY        = -2,
  NDEC_LOOKUP_ERR_KEYS_TOO_MANY     = -3,
  NDEC_LOOKUP_ERR_KEY_EMPTY         = -4,
  NDEC_LOOKUP_ERR_KEY_TOO_LONG      = -5,
  NDEC_LOOKUP_ERR_KEY_INVALID_BYTE  = -6,
  NDEC_LOOKUP_ERR_KEY_DUPLICATE     = -7,
  NDEC_LOOKUP_ERR_STORAGE_TOO_SMALL = -8,
  NDEC_LOOKUP_ERR_NO_TIER_MATCHES   = -9,
  NDEC_LOOKUP_ERR_SCRATCH_TOO_SMALL = -10,
} ndec_lookup_error;

typedef struct {
  const ndec_lookup_key *keys;
  size_t n;
  ndec_lookup_tier_mask tiers;
  // Caller-owned build scratch. The gperf/hand tier builders need a large
  // (~80 KiB) working set that must NOT live on the C stack: this library is
  // invoked through a //go:nosplit trampoline that runs C on the goroutine
  // stack, so a big frame would overflow it, and a freestanding .syso cannot
  // use large static/thread-local storage. The caller allocates a buffer of at
  // least ndec_lookup_scratch_size() bytes (Go: make([]byte, ...)) and points
  // `scratch` at it; `scratch_size` is its length. May be NULL only when the
  // tier mask excludes GPERF and HAND (WINDOW/TABLE need no scratch).
  void *scratch;
  size_t scratch_size;
} ndec_lookup_config;

// The public handle. Every per-tier struct below shares this exact prefix
// (kind is the first field), so reading `l->kind` on a downcasted tier
// pointer is defined by C's common initial sequence rule.
typedef struct ndec_lookup {
  ndec_lookup_tier kind;
} ndec_lookup;

// ---- Internal shared constants ----

#define NDEC_LOOKUP_MAX_KEYS       255
#define NDEC_LOOKUP_KEY_STRIDE_MAX 64
#define NDEC_LOOKUP_GPERF_MAX_POS  8
#define NDEC_LOOKUP_GPERF_MAX_TAB  512
#define NDEC_LOOKUP_GPERF_LAST_CH  0xFE
#define NDEC_LOOKUP_HD_MAX_TABLE   512

typedef enum {
  NDEC_LOOKUP_CMP_16 = 0,
  NDEC_LOOKUP_CMP_32,
  NDEC_LOOKUP_CMP_48,
  NDEC_LOOKUP_CMP_64,
} ndec_lookup_cmp_kind;

// ---- Per-tier storage layouts ----

typedef struct {
  ndec_lookup_tier kind;
  uint8_t direction;
  uint8_t byte_offset;
  uint8_t shift;
  ndec_lookup_cmp_kind cmp;
  size_t n;
  size_t max_key_len;
  size_t stride;
  size_t key_bytes_off;
  uint8_t window_to_key[256];
  // Tail: uint8_t key_len[n]; char key_bytes[n * stride];
} ndec_lookup_window;

typedef struct {
  ndec_lookup_tier kind;
  ndec_lookup_cmp_kind cmp;
  uint8_t num_positions;
  size_t n;
  size_t max_key_len;
  size_t table_size;
  size_t stride;
  uint8_t positions[NDEC_LOOKUP_GPERF_MAX_POS];
  size_t asso_off;
  size_t slots_off;
  size_t key_len_off;
  size_t key_bytes_off;
  // Tail: asso_values[np*256], slot_to_key[table_size], key_len[n], key_bytes[n*stride]
} ndec_lookup_gperf;

typedef enum { NDEC_LOOKUP_HD_HASH_2 = 0, NDEC_LOOKUP_HD_HASH_4 = 1 } ndec_lookup_hd_variant;

typedef struct {
  ndec_lookup_tier kind;
  ndec_lookup_cmp_kind cmp;
  ndec_lookup_hd_variant variant;
  size_t n;
  size_t max_key_len;
  size_t table_size;
  size_t stride;
  size_t key_bytes_off;
  uint64_t mask;
  uint8_t displacement[256];
  uint8_t slot_to_key[NDEC_LOOKUP_HD_MAX_TABLE];
  // Tail: uint8_t key_len[n]; char key_bytes[n * stride];
} ndec_lookup_hand;

typedef struct {
  uint32_t key_off;
  uint16_t key_len;
  uint16_t value_p1;
} ndec_lookup_fb_slot;

typedef struct {
  ndec_lookup_tier kind;
  size_t n;
  size_t cap;
  uint64_t mask;
  size_t key_data_off;
  size_t key_data_size;
  ndec_lookup_fb_slot slots[1];
} ndec_lookup_table;

// ---- SIMD byte compare (masked to len) ----

#if NDEC_LOOKUP_HAS_NEON || NDEC_LOOKUP_HAS_SSE2
static const uint8_t ndec_lookup_g_idx16[16]
    __attribute__((aligned(16))) = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
#endif

#if NDEC_LOOKUP_HAS_NEON
#define NDEC_LOOKUP_CMP_BODY(NUM_BLOCKS)                                                                          \
  do {                                                                                                            \
    uint8x16_t base = vld1q_u8(ndec_lookup_g_idx16);                                                              \
    uint8x16_t lenv = vdupq_n_u8((uint8_t)len);                                                                   \
    uint8x16_t acc  = vdupq_n_u8(0);                                                                              \
    for (size_t b = 0; b < (NUM_BLOCKS); b++) {                                                                   \
      uint8x16_t vp   = vld1q_u8((const uint8_t *)p + b * 16);                                                    \
      uint8x16_t vs   = vld1q_u8((const uint8_t *)stored + b * 16);                                               \
      uint8x16_t idxv = vaddq_u8(base, vdupq_n_u8((uint8_t)(b * 16)));                                            \
      uint8x16_t mask = vcltq_u8(idxv, lenv);                                                                     \
      acc             = vorrq_u8(acc, veorq_u8(vandq_u8(vp, mask), vs));                                          \
    }                                                                                                             \
    return vmaxvq_u32(vreinterpretq_u32_u8(acc)) == 0;                                                            \
  } while (0)
#elif NDEC_LOOKUP_HAS_SSE2
#define NDEC_LOOKUP_CMP_BODY(NUM_BLOCKS)                                                                          \
  do {                                                                                                            \
    __m128i base = _mm_load_si128((const __m128i *)ndec_lookup_g_idx16);                                          \
    __m128i lenv = _mm_set1_epi8((char)len);                                                                      \
    int eq       = 0xFFFF;                                                                                        \
    for (size_t b = 0; b < (NUM_BLOCKS); b++) {                                                                   \
      __m128i vp   = _mm_loadu_si128((const __m128i *)(p + b * 16));                                              \
      __m128i vs   = _mm_loadu_si128((const __m128i *)(stored + b * 16));                                         \
      __m128i idxv = _mm_add_epi8(base, _mm_set1_epi8((char)(b * 16)));                                           \
      __m128i mask = _mm_cmplt_epi8(idxv, lenv);                                                                  \
      eq &= _mm_movemask_epi8(_mm_cmpeq_epi8(_mm_and_si128(vp, mask), vs));                                       \
    }                                                                                                             \
    return eq == 0xFFFF;                                                                                          \
  } while (0)
#else
#define NDEC_LOOKUP_CMP_BODY(NUM_BLOCKS)                                                                          \
  do {                                                                                                            \
    (void)(NUM_BLOCKS);                                                                                           \
    return __builtin_memcmp(p, stored, len) == 0;                                                                 \
  } while (0)
#endif

#undef NDEC_LOOKUP_HAS_SSE2
#undef NDEC_LOOKUP_HAS_NEON

INLINE int ndec_lookup_cmp16(const char *p, const char *stored, size_t len) {
  NDEC_LOOKUP_CMP_BODY(1);
}
INLINE int ndec_lookup_cmp32(const char *p, const char *stored, size_t len) {
  NDEC_LOOKUP_CMP_BODY(2);
}
INLINE int ndec_lookup_cmp48(const char *p, const char *stored, size_t len) {
  NDEC_LOOKUP_CMP_BODY(3);
}
INLINE int ndec_lookup_cmp64(const char *p, const char *stored, size_t len) {
  NDEC_LOOKUP_CMP_BODY(4);
}

INLINE int ndec_lookup_compare_bytes(ndec_lookup_cmp_kind k, const char *p, const char *stored, size_t len) {
  switch (k) {
  case NDEC_LOOKUP_CMP_16:
    return ndec_lookup_cmp16(p, stored, len);
  case NDEC_LOOKUP_CMP_32:
    return ndec_lookup_cmp32(p, stored, len);
  case NDEC_LOOKUP_CMP_48:
    return ndec_lookup_cmp48(p, stored, len);
  default:
    return ndec_lookup_cmp64(p, stored, len);
  }
}

// ---- Per-tier find (inline hot path) ----

INLINE uint8_t ndec_lookup_read_window(const char *p, uint8_t byte_offset, uint8_t shift) {
  uint16_t w;
  __builtin_memcpy(&w, p + byte_offset, sizeof(w));
  return (uint8_t)((w >> shift) & 0xFFu);
}

INLINE uint8_t ndec_lookup_read_window_back(const char *p, size_t len, uint8_t byte_offset, uint8_t shift) {
  uint16_t w;
  __builtin_memcpy(&w, p + len - byte_offset, sizeof(w));
  return (uint8_t)((w >> shift) & 0xFFu);
}

INLINE int ndec_lookup_window_find(const ndec_lookup_window *w, const char *p, size_t len) {
  uint8_t ki;
  if (w->direction == 0) {
    ki = w->window_to_key[ndec_lookup_read_window(p, w->byte_offset, w->shift)];
  } else {
    if (len < w->byte_offset)
      return -1;
    ki = w->window_to_key[ndec_lookup_read_window_back(p, len, w->byte_offset, w->shift)];
  }
  if (ki >= w->n)
    return -1;
  const uint8_t *klen = (const uint8_t *)w + sizeof(ndec_lookup_window);
  if (p[klen[ki]] != '"')
    return -1;
  const char *stored = (const char *)w + w->key_bytes_off + (size_t)ki * w->stride;
  if (!ndec_lookup_compare_bytes(w->cmp, p, stored, klen[ki]))
    return -1;
  return (int)ki;
}

INLINE int ndec_lookup_gperf_find(const ndec_lookup_gperf *gp, const char *p, size_t len) {
  size_t h             = len;
  uint8_t np           = gp->num_positions;
  const uint8_t *asso0 = (const uint8_t *)gp + gp->asso_off;

  if (LIKELY(np <= 2)) {
    if (np >= 1) {
      uint8_t pos = gp->positions[0];
      size_t idx  = (pos == NDEC_LOOKUP_GPERF_LAST_CH) ? (len - 1) : (size_t)pos;
      if (idx < len)
        h += asso0[(unsigned char)p[idx]];
    }
    if (np == 2) {
      uint8_t pos = gp->positions[1];
      size_t idx  = (pos == NDEC_LOOKUP_GPERF_LAST_CH) ? (len - 1) : (size_t)pos;
      if (idx < len)
        h += asso0[256u + (unsigned char)p[idx]];
    }
  } else {
    for (uint8_t i = 0; i < np; i++) {
      uint8_t pos = gp->positions[i];
      size_t idx  = (pos == NDEC_LOOKUP_GPERF_LAST_CH) ? (len - 1) : (size_t)pos;
      if (idx < len)
        h += asso0[(size_t)i * 256u + (unsigned char)p[idx]];
    }
  }

  size_t slot          = h & (gp->table_size - 1);
  const uint8_t *slots = (const uint8_t *)gp + gp->slots_off;
  uint8_t ki           = slots[slot];
  if (ki >= gp->n)
    return -1;
  const uint8_t *klen = (const uint8_t *)gp + gp->key_len_off;
  if (klen[ki] != len)
    return -1;
  const char *stored = (const char *)gp + gp->key_bytes_off + (size_t)ki * gp->stride;
  if (!ndec_lookup_compare_bytes(gp->cmp, p, stored, klen[ki]))
    return -1;
  return (int)ki;
}

INLINE size_t ndec_lookup_hd_bucket(const char *p, size_t len) {
  size_t c0 = len ? (unsigned char)p[0] : 0;
  size_t c1 = len ? (unsigned char)p[len - 1] : 0;
  return (c0 + c1 * 3 + len * 17) & 0xFF;
}

INLINE size_t ndec_lookup_hd_safe_char(const char *p, size_t len, size_t idx) {
  size_t has = (size_t)(idx < len);
  size_t si  = idx & (size_t)(0 - has);
  return (unsigned char)p[si] & (size_t)(0 - has);
}

INLINE size_t ndec_lookup_hd_hash_2(const char *p, size_t len) {
  size_t kc = len;
  kc        = kc * 31 + (len ? (unsigned char)p[0] : 0);
  kc        = kc * 31 + ndec_lookup_hd_safe_char(p, len, 1);
  return kc;
}

INLINE size_t ndec_lookup_hd_hash_4(const char *p, size_t len) {
  size_t kc = len;
  kc        = kc * 31 + (len ? (unsigned char)p[0] : 0);
  kc        = kc * 31 + ndec_lookup_hd_safe_char(p, len, 1);
  kc        = kc * 31 + ndec_lookup_hd_safe_char(p, len, 2);
  kc        = kc * 31 + ndec_lookup_hd_safe_char(p, len, 3);
  return kc;
}

INLINE int ndec_lookup_hand_find(const ndec_lookup_hand *hd, const char *p, size_t len) {
  size_t bucket = ndec_lookup_hd_bucket(p, len);
  size_t kh =
      (hd->variant == NDEC_LOOKUP_HD_HASH_2) ? ndec_lookup_hd_hash_2(p, len) : ndec_lookup_hd_hash_4(p, len);
  size_t slot = ((size_t)hd->displacement[bucket] + kh) & hd->mask;
  uint8_t ki  = hd->slot_to_key[slot];
  if (ki >= hd->n)
    return -1;
  const uint8_t *klen = (const uint8_t *)hd + sizeof(ndec_lookup_hand);
  if (klen[ki] != len)
    return -1;
  const char *stored = (const char *)hd + hd->key_bytes_off + (size_t)ki * hd->stride;
  if (!ndec_lookup_compare_bytes(hd->cmp, p, stored, klen[ki]))
    return -1;
  return (int)ki;
}

INLINE uint64_t ndec_lookup_table_hash(const char *p, size_t len) {
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

INLINE int ndec_lookup_table_find(const ndec_lookup_table *fb, const char *p, size_t len) {
  uint64_t h = ndec_lookup_table_hash(p, len);
  size_t pos = h & fb->mask;
  for (;;) {
    if (fb->slots[pos].value_p1 == 0)
      return -1;
    if (fb->slots[pos].key_len == len) {
      const char *stored = (const char *)fb + fb->slots[pos].key_off;
      if (__builtin_memcmp(stored, p, len) == 0)
        return (int)fb->slots[pos].value_p1 - 1;
    }
    pos = (pos + 1) & fb->mask;
  }
}

// ---- Public API: find (inline dispatcher) ----

INLINE int ndec_lookup_find(const ndec_lookup *l, ndec_lookup_key key) {
  switch (l->kind) {
  case NDEC_LOOKUP_TIER_WINDOW:
    return ndec_lookup_window_find((const ndec_lookup_window *)l, key.str, key.len);
  case NDEC_LOOKUP_TIER_GPERF:
    return ndec_lookup_gperf_find((const ndec_lookup_gperf *)l, key.str, key.len);
  case NDEC_LOOKUP_TIER_HAND:
    return ndec_lookup_hand_find((const ndec_lookup_hand *)l, key.str, key.len);
  case NDEC_LOOKUP_TIER_TABLE:
    return ndec_lookup_table_find((const ndec_lookup_table *)l, key.str, key.len);
  default:
    return -1;
  }
}

// Robustness: if a runtime key contains 0x00 / 0x22 / 0x5C in [0, key.len),
// ndec_lookup_find safely returns -1 (no false match). Callers seeing raw
// JSON segments with escapes can call ndec_lookup_find directly and fall
// back to a general-purpose hashtable on miss.

// ---- Public API: build + introspection (out-of-line) ----

// Upper bound of storage bytes for this config. Returns 0 on invalid config.
size_t ndec_lookup_size_for(const ndec_lookup_config *cfg);

// Required size in bytes of the caller-provided build scratch buffer
// (ndec_lookup_config.scratch). Constant for the library build; callers can
// allocate once and reuse across ndec_lookup_init calls.
size_t ndec_lookup_scratch_size(void);

// Build a lookup into caller-provided storage. Returns the selected tier
// on success (positive ndec_lookup_tier), or a negative ndec_lookup_error.
int ndec_lookup_init(ndec_lookup *storage, size_t storage_size, const ndec_lookup_config *cfg);

ndec_lookup_tier ndec_lookup_get_tier(const ndec_lookup *l);
const char *ndec_lookup_tier_name(ndec_lookup_tier t);
/* Direction-aware variant: when tier is WINDOW, returns "window_fwd" or
   "window_rev" depending on w->direction; otherwise delegates to
   ndec_lookup_tier_name. */
const char *ndec_lookup_tier_name_ex(const ndec_lookup *l);
size_t ndec_lookup_footprint(const ndec_lookup *l);

#endif // NDEC_LOOKUP_H
