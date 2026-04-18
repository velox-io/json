// Final three-tier struct field lookup, integrated and benchmarked.
//
// Tiers:
//   n <= 8       -> bitmap8k (K=8 prefix bitmap + memcmp verify on tail)
//   9 <= n <= 32 -> perfect hash (simple mixer, fnv1a fallback)
//   n >= 33      -> hashmap (FNV-1a + linear probing, load < 0.5)
//
// All three implementations share a single Lookup interface so the bench
// loop is identical across tiers. The 2D bench at the bottom exercises every
// (n, strategy) combination so we can see how each strategy behaves outside
// its recommended range.
//
// Build:
//   cc -O3 -std=c11 -Wall lookup3.c -o lookup3
//
// Run:
//   ./lookup3            # full 2D matrix
//   ./lookup3 quick      # smaller workload, faster

#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

// Common Lookup interface

typedef struct lookup lookup;
struct lookup {
  int (*find)(const lookup *self, const char *key, size_t klen);
  void (*free)(lookup *self);
  const char *kind;
};

// Tier 1: bitmap8k (n <= 8).
//
// For each of the first K=8 character positions, maintain a 256-entry bitmap
// where bit i is set iff field i has that byte at that position. AND-reduce
// across positions, AND with len_mask, then either ctz directly (if klen<=K
// the prefix exactly identifies the field) or memcmp surviving bits to
// disambiguate fields sharing the first K bytes.
//
// Memory: K*256 + (maxlen+1) + n*12 ~= 1.8 KB regardless of maxlen.

#define BM8K_K 8

typedef struct {
  lookup base;
  uint8_t max_key_len;
  uint8_t prefix_k;
  uint8_t *bitmap;   // [prefix_k * 256]
  uint8_t *len_mask; // [max_key_len + 1]
  const char **names;
  uint32_t *lens;
  size_t n;
} bm8k;

static int bm8k_find(const lookup *self, const char *key, size_t klen) {
  const bm8k *b = (const bm8k *)self;
  if (klen == 0 || klen > b->max_key_len)
    return -1;
  uint8_t cur = 0xFF;
  size_t pk   = b->prefix_k;
  size_t scan = klen < pk ? klen : pk;
  for (size_t j = 0; j < scan; j++) {
    cur &= b->bitmap[j * 256 + (uint8_t)key[j]];
    if (cur == 0)
      return -1;
  }
  cur &= b->len_mask[klen];
  if (cur == 0)
    return -1;
  if (klen <= pk)
    return __builtin_ctz(cur);
  while (cur != 0) {
    int i = __builtin_ctz(cur);
    if (b->lens[i] == klen && memcmp(b->names[i], key, klen) == 0)
      return i;
    cur &= cur - 1;
  }
  return -1;
}

static void bm8k_free(lookup *self) {
  bm8k *b = (bm8k *)self;
  free(b->bitmap);
  free(b->len_mask);
  free(b->lens);
  free(b);
}

static lookup *bm8k_new(const char **names, size_t n) {
  if (n == 0 || n > 8)
    return NULL;
  size_t maxlen = 0;
  for (size_t i = 0; i < n; i++) {
    size_t L = strlen(names[i]);
    if (L > maxlen)
      maxlen = L;
  }
  size_t pk      = maxlen < BM8K_K ? maxlen : BM8K_K;
  bm8k *b        = calloc(1, sizeof(*b));
  b->base        = (lookup){bm8k_find, bm8k_free, "bitmap8k"};
  b->max_key_len = (uint8_t)maxlen;
  b->prefix_k    = (uint8_t)pk;
  b->bitmap      = calloc(pk * 256, 1);
  b->len_mask    = calloc(maxlen + 1, 1);
  b->names       = names;
  b->lens        = malloc(sizeof(uint32_t) * n);
  b->n           = n;
  for (size_t i = 0; i < n; i++) {
    uint8_t bit   = (uint8_t)(1u << i);
    const char *s = names[i];
    size_t L      = strlen(s);
    b->lens[i]    = (uint32_t)L;
    size_t scan   = L < pk ? L : pk;
    for (size_t j = 0; j < scan; j++)
      b->bitmap[j * 256 + (uint8_t)s[j]] |= bit;
    b->len_mask[L] |= bit;
  }
  return &b->base;
}

// Tier 2: perfect hash (9 <= n <= 32).
//
// Brute-force search over (seed, shift) until all field hashes occupy
// distinct slots in a 2N-sized table. simple_mixer (4-byte fingerprint)
// is tried first for fast lookup; fnv1a fallback handles pathological
// field-name distributions where simple cannot find a collision-free seed.

typedef uint64_t (*mixer_fn)(const char *s, size_t n, uint64_t seed);

static inline uint64_t simple_mixer(const char *s, size_t n, uint64_t seed) {
  if (n == 0)
    return seed * 0x9e3779b97f4a7c15ULL;
  uint64_t first = (uint8_t)s[0];
  uint64_t last  = (uint8_t)s[n - 1];
  uint64_t mid   = (uint8_t)s[n / 2];
  uint64_t h     = seed;
  h ^= (uint64_t)n * 0x9e3779b97f4a7c15ULL;
  h ^= first * 0xbf58476d1ce4e5b9ULL;
  h ^= last * 0x94d049bb133111ebULL;
  h ^= mid * 0xff51afd7ed558ccdULL;
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
  lookup base;
  const char **names;
  uint32_t *lens;
  size_t n;
  uint64_t seed;
  uint8_t shift;
  uint8_t *table; // 0xFF = empty
  uint64_t mask;
  mixer_fn mixer;
} ph;

static int ph_find(const lookup *self, const char *key, size_t klen) {
  const ph *p   = (const ph *)self;
  uint64_t h    = p->mixer(key, klen, p->seed);
  uint64_t slot = (h >> p->shift) & p->mask;
  uint8_t idx   = p->table[slot];
  if (idx == 0xFF)
    return -1;
  if (p->lens[idx] != klen)
    return -1;
  if (memcmp(p->names[idx], key, klen) != 0)
    return -1;
  return (int)idx;
}

static void ph_free(lookup *self) {
  ph *p = (ph *)self;
  free(p->lens);
  free(p->table);
  free(p);
}

#define PH_MAX_SEED_ATTEMPTS (1u << 16)
#define PH_MAX_FIELDS        32u

static lookup *ph_try_new(const char **names, size_t n, mixer_fn mixer, const char *kind) {
  if (n == 0 || n > PH_MAX_FIELDS)
    return NULL;
  size_t table_size = 1;
  while (table_size < n * 2)
    table_size <<= 1;
  uint64_t mask = table_size - 1;

  uint64_t hashes[PH_MAX_FIELDS];
  uint8_t *seen = calloc(table_size, 1);
  uint8_t gen   = 1;

  uint32_t *lens = malloc(sizeof(uint32_t) * n);
  for (size_t i = 0; i < n; i++)
    lens[i] = (uint32_t)strlen(names[i]);

  for (uint32_t seed = 0; seed < PH_MAX_SEED_ATTEMPTS; seed++) {
    for (size_t i = 0; i < n; i++)
      hashes[i] = mixer(names[i], lens[i], seed);
    for (uint8_t shift = 0; shift < 64; shift++) {
      if (gen == 255) {
        memset(seen, 0, table_size);
        gen = 1;
      } else
        gen++;
      int collision = 0;
      for (size_t i = 0; i < n; i++) {
        uint64_t slot = (hashes[i] >> shift) & mask;
        if (seen[slot] == gen) {
          collision = 1;
          break;
        }
        seen[slot] = gen;
      }
      if (!collision) {
        uint8_t *table = malloc(table_size);
        memset(table, 0xFF, table_size);
        for (size_t i = 0; i < n; i++) {
          uint64_t slot = (hashes[i] >> shift) & mask;
          table[slot]   = (uint8_t)i;
        }
        free(seen);
        ph *p    = calloc(1, sizeof(*p));
        p->base  = (lookup){ph_find, ph_free, kind};
        p->names = names;
        p->lens  = lens;
        p->n     = n;
        p->seed  = seed;
        p->shift = shift;
        p->table = table;
        p->mask  = mask;
        p->mixer = mixer;
        return &p->base;
      }
    }
  }
  free(seen);
  free(lens);
  return NULL;
}

// Tier 3: hashmap (n >= 33, or fallback)

typedef struct {
  const char *key;
  uint32_t klen;
  int32_t value; // -1 = empty
} hm_slot;

typedef struct {
  lookup base;
  hm_slot *slots;
  size_t cap;
  size_t mask;
} hm;

static uint32_t fnv32(const char *s, size_t n) {
  uint32_t h = 2166136261u;
  for (size_t i = 0; i < n; i++) {
    h ^= (uint8_t)s[i];
    h *= 16777619u;
  }
  return h;
}

static int hm_find(const lookup *self, const char *key, size_t klen) {
  const hm *h = (const hm *)self;
  size_t pos  = fnv32(key, klen) & h->mask;
  for (;;) {
    hm_slot s = h->slots[pos];
    if (s.value == -1)
      return -1;
    if (s.klen == klen && memcmp(s.key, key, klen) == 0)
      return s.value;
    pos = (pos + 1) & h->mask;
  }
}

static void hm_free_(lookup *self) {
  hm *h = (hm *)self;
  free(h->slots);
  free(h);
}

static lookup *hm_new(const char **names, size_t n) {
  size_t cap = 16;
  while (cap < n * 2)
    cap <<= 1;
  hm *h    = calloc(1, sizeof(*h));
  h->base  = (lookup){hm_find, hm_free_, "hashmap"};
  h->cap   = cap;
  h->mask  = cap - 1;
  h->slots = malloc(sizeof(hm_slot) * cap);
  for (size_t i = 0; i < cap; i++)
    h->slots[i].value = -1;
  for (size_t i = 0; i < n; i++) {
    size_t L   = strlen(names[i]);
    size_t pos = fnv32(names[i], L) & h->mask;
    while (h->slots[pos].value != -1)
      pos = (pos + 1) & h->mask;
    h->slots[pos].key   = names[i];
    h->slots[pos].klen  = (uint32_t)L;
    h->slots[pos].value = (int32_t)i;
  }
  return &h->base;
}

// Auto strategy selection

static lookup *build_auto(const char **names, size_t n) {
  if (n == 0)
    return hm_new(names, n);
  if (n <= 8)
    return bm8k_new(names, n);
  if (n <= 32) {
    lookup *p = ph_try_new(names, n, simple_mixer, "ph-simple");
    if (p)
      return p;
    p = ph_try_new(names, n, fnv1a_mixer, "ph-fnv");
    if (p)
      return p;
    return hm_new(names, n);
  }
  // n > 32: try ph one last time, otherwise fall back to hashmap
  lookup *p = ph_try_new(names, n, fnv1a_mixer, "ph-fnv");
  if (p)
    return p;
  return hm_new(names, n);
}

// Workload generation

static const char *prefixes[] = {
    "id",    "name",   "type",    "value",   "items",   "count", "status", "error", "data",    "result",
    "msg",   "code",   "ts",      "user",    "email",   "phone", "addr",   "city",  "country", "zip",
    "title", "desc",   "tags",    "cat",     "price",   "cur",   "qty",    "disc",  "total",   "ship",
    "pay",   "method", "created", "updated", "deleted", "ver",   "flags",  "meta",  "url",     "path",
    "key",   "val",    "len",     "size",    "start",   "end",   "min",    "max",   "avg",     "sum",
};
static const size_t n_prefixes = sizeof(prefixes) / sizeof(prefixes[0]);

static char **make_keys(size_t n) {
  char **out = malloc(sizeof(char *) * n);
  for (size_t i = 0; i < n; i++) {
    char buf[64];
    if (i < n_prefixes)
      snprintf(buf, sizeof(buf), "%s", prefixes[i]);
    else
      snprintf(buf, sizeof(buf), "%s_%zu", prefixes[i % n_prefixes], i);
    out[i] = strdup(buf);
  }
  return out;
}
static void free_keys(char **k, size_t n) {
  for (size_t i = 0; i < n; i++)
    free(k[i]);
  free(k);
}

typedef struct {
  const char *s;
  size_t len;
} query;

static query *make_workload(char **keys, size_t n, size_t count, double hit, char ***miss_storage,
                            size_t *miss_count) {
  query *qs     = malloc(sizeof(query) * count);
  char **misses = malloc(sizeof(char *) * count);
  size_t mn     = 0;
  srand(42);
  for (size_t i = 0; i < count; i++) {
    if ((double)rand() / RAND_MAX < hit) {
      size_t k  = (size_t)rand() % n;
      qs[i].s   = keys[k];
      qs[i].len = strlen(keys[k]);
    } else {
      char buf[64];
      snprintf(buf, sizeof(buf), "miss_%d_xyz", rand());
      misses[mn] = strdup(buf);
      qs[i].s    = misses[mn];
      qs[i].len  = strlen(misses[mn]);
      mn++;
    }
  }
  *miss_storage = misses;
  *miss_count   = mn;
  return qs;
}

// Bench driver

static double now_ns(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (double)t.tv_sec * 1e9 + (double)t.tv_nsec;
}

static volatile int sink;

// Bench one (lookup, workload) pair. Returns ns/op.
static double bench(const lookup *L, const query *qs, size_t qn, int reps) {
  // warm
  for (size_t i = 0; i < qn; i++)
    sink ^= L->find(L, qs[i].s, qs[i].len);
  double t = 0;
  for (int r = 0; r < reps; r++) {
    double t0 = now_ns();
    for (size_t i = 0; i < qn; i++)
      sink ^= L->find(L, qs[i].s, qs[i].len);
    t += now_ns() - t0;
  }
  return t / ((double)qn * reps);
}

// Verify two lookups agree on the workload.
static void check_agree(const lookup *a, const lookup *b, const query *qs, size_t qn) {
  for (size_t i = 0; i < qn; i++) {
    int x = a->find(a, qs[i].s, qs[i].len);
    int y = b->find(b, qs[i].s, qs[i].len);
    if (x != y) {
      fprintf(stderr, "MISMATCH q=%s %s=%d %s=%d\n", qs[i].s, a->kind, x, b->kind, y);
      exit(2);
    }
  }
}

// 2D matrix bench: rows = n, columns = strategy

typedef struct {
  const char *name;
  lookup *(*build)(const char **, size_t);
  int min_n, max_n; // applicability range
} strategy_def;

static lookup *build_bm8k(const char **names, size_t n) {
  return bm8k_new(names, n);
}
static lookup *build_ph_simple(const char **names, size_t n) {
  return ph_try_new(names, n, simple_mixer, "ph-simple");
}
static lookup *build_ph_fnv(const char **names, size_t n) {
  return ph_try_new(names, n, fnv1a_mixer, "ph-fnv");
}
static lookup *build_hm(const char **names, size_t n) {
  return hm_new(names, n);
}

static const strategy_def STRATEGIES[] = {
    {"bitmap8k", build_bm8k, 1, 8},
    {"ph-simple", build_ph_simple, 1, 32},
    {"ph-fnv", build_ph_fnv, 1, 256},
    {"hashmap", build_hm, 1, 1 << 20},
};
static const size_t N_STRATEGIES = sizeof(STRATEGIES) / sizeof(STRATEGIES[0]);

static void print_matrix(const size_t *ns, size_t n_ns, double hit, size_t qn, int reps) {
  printf("\n--- hit=%.0f%% ---\n", hit * 100);
  // Header
  printf("%6s |", "n");
  for (size_t s = 0; s < N_STRATEGIES; s++)
    printf(" %10s", STRATEGIES[s].name);
  printf("\n");
  printf("-------+");
  for (size_t s = 0; s < N_STRATEGIES; s++)
    printf("-----------");
  printf("\n");

  for (size_t row = 0; row < n_ns; row++) {
    size_t n      = ns[row];
    char **keys   = make_keys(n);
    char **misses = NULL;
    size_t mn     = 0;
    query *qs     = make_workload(keys, n, qn, hit, &misses, &mn);

    // Cross-validate: hashmap is the oracle; check every applicable strategy.
    lookup *oracle = hm_new((const char **)keys, n);

    printf("%6zu |", n);
    for (size_t s = 0; s < N_STRATEGIES; s++) {
      const strategy_def *sd = &STRATEGIES[s];
      if ((int)n < sd->min_n || (int)n > sd->max_n) {
        printf(" %10s", "-");
        continue;
      }
      lookup *L = sd->build((const char **)keys, n);
      if (!L) {
        printf(" %10s", "build-fail");
        continue;
      }
      check_agree(L, oracle, qs, qn);
      double ns_op = bench(L, qs, qn, reps);
      printf(" %9.2fns", ns_op);
      L->free(L);
    }
    printf("\n");

    oracle->free(oracle);
    for (size_t i = 0; i < mn; i++)
      free(misses[i]);
    free(misses);
    free(qs);
    free_keys(keys, n);
  }
}

// Auto vs each strategy summary (sanity)

static void print_auto_summary(const size_t *ns, size_t n_ns, double hit, size_t qn, int reps) {
  printf("\n--- auto pick @ hit=%.0f%% ---\n", hit * 100);
  printf("%6s | %-12s %10s\n", "n", "auto-kind", "ns/op");
  printf("-------+----------------------------\n");
  for (size_t row = 0; row < n_ns; row++) {
    size_t n      = ns[row];
    char **keys   = make_keys(n);
    char **misses = NULL;
    size_t mn     = 0;
    query *qs     = make_workload(keys, n, qn, hit, &misses, &mn);
    lookup *L     = build_auto((const char **)keys, n);
    double ns_op  = bench(L, qs, qn, reps);
    printf("%6zu | %-12s %9.2fns\n", n, L->kind, ns_op);
    L->free(L);
    for (size_t i = 0; i < mn; i++)
      free(misses[i]);
    free(misses);
    free(qs);
    free_keys(keys, n);
  }
}

int main(int argc, char **argv) {
  int quick = (argc > 1 && strcmp(argv[1], "quick") == 0);
  size_t qn = quick ? 50000 : 200000;
  int reps  = quick ? 3 : 5;

  static const size_t ns[] = {1, 2, 4, 8, 9, 12, 16, 24, 32, 33, 48, 64, 128, 256, 512, 1024, 4096};
  size_t n_ns              = sizeof(ns) / sizeof(ns[0]);

  printf("=== lookup3: 3-tier strategy + 2D bench ===\n");
  printf("queries/cell: %zu  reps: %d  total per cell: %ld lookups\n", qn, reps, (long)qn * reps);
  printf("strategies: ");
  for (size_t s = 0; s < N_STRATEGIES; s++)
    printf("%s%s", STRATEGIES[s].name, s + 1 < N_STRATEGIES ? ", " : "\n");

  // Full 2D matrix at three hit ratios.
  for (double hit_arr[] = {1.0, 0.5, 0.0}, *p = hit_arr; p != hit_arr + 3; p++) {
    print_matrix(ns, n_ns, *p, qn, reps);
  }

  // Auto-pick verification.
  for (double hit_arr[] = {1.0, 0.5, 0.0}, *p = hit_arr; p != hit_arr + 3; p++) {
    print_auto_summary(ns, n_ns, *p, qn, reps);
  }

  (void)sink;
  return 0;
}
