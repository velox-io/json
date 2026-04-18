// Empirical FP-collision study for the bitmap8k lookup strategy.
//
// bitmap8k filters queries through an 8-byte prefix bitmap + length mask.
// For field names <= 8 bytes the filter is exact (0 memcmps on match).
// For field names > 8 bytes, multiple fields may share the first 8 bytes,
// causing multiple bits to survive the filter and requiring memcmp(s) to
// disambiguate.
//
// This bench measures:
//   1. Average bits-surviving per query (across realistic field sets)
//   2. Worst-case memcmp count
//   3. How often the FAST path (klen <= 8 -> 0 memcmps) applies
//
// We use realistic Go JSON struct field name sets pulled from common
// patterns (API DTOs, AWS SDK, Kubernetes types, logging events).
//
// Build: cc -O2 -std=c11 -Wall bitmap8k_collision.c -o bitmap8k_collision

#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define K 8

typedef struct {
    uint8_t   max_key_len;
    uint8_t   prefix_k;
    uint8_t  *bitmap;     // [prefix_k * 256]
    uint8_t  *len_mask;   // [max_key_len + 1]
    const char **names;
    uint32_t *lens;
    size_t    n;
} bitmap8k;

static bitmap8k *bitmap8k_new(const char **names, size_t n) {
    if (n == 0 || n > 8) return NULL;
    size_t maxlen = 0;
    for (size_t i = 0; i < n; i++) {
        size_t L = strlen(names[i]);
        if (L > maxlen) maxlen = L;
    }
    size_t pk = maxlen < K ? maxlen : K;
    bitmap8k *b = calloc(1, sizeof(*b));
    b->max_key_len = (uint8_t)maxlen;
    b->prefix_k    = (uint8_t)pk;
    b->bitmap   = calloc(pk * 256, 1);
    b->len_mask = calloc(maxlen + 1, 1);
    b->names = names;
    b->lens = malloc(sizeof(uint32_t) * n);
    b->n = n;
    for (size_t i = 0; i < n; i++) {
        uint8_t bit = (uint8_t)(1u << i);
        const char *s = names[i];
        size_t L = strlen(s);
        b->lens[i] = (uint32_t)L;
        size_t scan = L < pk ? L : pk;
        for (size_t j = 0; j < scan; j++)
            b->bitmap[j * 256 + (uint8_t)s[j]] |= bit;
        b->len_mask[L] |= bit;
    }
    return b;
}

static void bitmap8k_free(bitmap8k *b) {
    if (!b) return;
    free(b->bitmap); free(b->len_mask); free(b->lens); free(b);
}

// Returns {idx, memcmp_count}. memcmp_count = 0 means fast path.
typedef struct { int idx; int memcmps; int surviving_bits; } probe_result;

static probe_result bitmap8k_probe(const bitmap8k *b, const char *key, size_t klen) {
    probe_result r = {-1, 0, 0};
    if (klen == 0 || klen > b->max_key_len) return r;
    uint8_t cur = 0xFF;
    size_t pk = b->prefix_k;
    size_t scan = klen < pk ? klen : pk;
    for (size_t j = 0; j < scan; j++) {
        cur &= b->bitmap[j * 256 + (uint8_t)key[j]];
        if (cur == 0) return r;
    }
    cur &= b->len_mask[klen];
    if (cur == 0) return r;
    r.surviving_bits = __builtin_popcount(cur);

    if (klen <= pk) { r.idx = __builtin_ctz(cur); return r; }

    while (cur != 0) {
        int i = __builtin_ctz(cur);
        r.memcmps++;
        if (b->lens[i] == klen && memcmp(b->names[i], key, klen) == 0) {
            r.idx = i;
            return r;
        }
        cur &= cur - 1;
    }
    return r;
}

// Realistic field-name datasets. Each set has <= 8 fields, drawn from
// common Go JSON struct patterns (API DTOs, AWS SDK, Kubernetes, logging).
// The focus is on long-field-name sets (maxlen >= 8) where collision matters.

typedef struct { const char *label; const char **fields; size_t n; } dataset;

static const char *ds_short_api[] = {
    "id", "type", "code", "name", "data", "msg", "ts", "ok"
};
static const char *ds_rest_dto[] = {
    "userId", "traceId", "status", "region", "tenant", "payload", "method", "result"
};
static const char *ds_timestamps[] = {
    "createdAt", "updatedAt", "deletedAt", "startedAt", "finishedAt",
    "publishedAt", "expiresAt", "scheduledAt"
};
static const char *ds_k8s_obj[] = {
    "apiVersion", "kind", "metadata", "spec", "status", "items", "continue", "selfLink"
};
static const char *ds_aws_s3[] = {
    "ETag", "Key", "LastModified", "Owner", "Size", "StorageClass", "ChecksumAlgorithm"
};
static const char *ds_prefix_heavy[] = {
    "customerId", "customerName", "customerEmail", "customerPhone",
    "customerAddress", "customerType", "customerStatus", "customerBalance"
};
static const char *ds_very_long[] = {
    "transactionProcessingTimestamp",
    "transactionCorrelationIdentifier",
    "httpResponseStatusCode",
    "deploymentRegionIdentifier",
    "tenantOrganizationSlug",
    "requestBodyPayloadChecksum",
    "httpRequestMethodUpperCase",
    "clientApplicationUserAgent"
};
static const char *ds_user_profile[] = {
    "firstName", "lastName", "emailAddress", "phoneNumber",
    "dateOfBirth", "profilePicture", "accountStatus", "lastLoginAt"
};
static const char *ds_logging[] = {
    "timestamp", "level", "message", "logger",
    "thread", "exception", "spanId", "traceId"
};

static const dataset datasets[] = {
    {"short-api",      ds_short_api,     8},
    {"rest-dto",       ds_rest_dto,      8},
    {"timestamps",     ds_timestamps,    8},
    {"k8s-object",     ds_k8s_obj,       8},
    {"aws-s3",         ds_aws_s3,        7},
    {"prefix-heavy",   ds_prefix_heavy,  8},
    {"very-long",      ds_very_long,     8},
    {"user-profile",   ds_user_profile,  8},
    {"logging",        ds_logging,       8},
};
static const size_t n_datasets = sizeof(datasets)/sizeof(datasets[0]);

// Probe each dataset with a mix of (hit, miss, prefix-only-miss) queries.

static void analyze(const dataset *ds) {
    printf("\n[%s] fields (maxlen=", ds->label);
    size_t maxlen = 0;
    for (size_t i = 0; i < ds->n; i++) {
        size_t L = strlen(ds->fields[i]);
        if (L > maxlen) maxlen = L;
    }
    printf("%zu):", maxlen);
    for (size_t i = 0; i < ds->n; i++) printf(" %s", ds->fields[i]);
    printf("\n");

    bitmap8k *b = bitmap8k_new(ds->fields, ds->n);

    // Statistics
    long hits = 0, misses = 0, fast_path = 0;
    long total_memcmps = 0, max_memcmps = 0;
    long total_surviving = 0, max_surviving = 0;

    // Hit queries: each field name
    for (size_t i = 0; i < ds->n; i++) {
        const char *q = ds->fields[i];
        size_t qlen = strlen(q);
        probe_result r = bitmap8k_probe(b, q, qlen);
        if (r.idx != (int)i) {
            printf("  !!! BUG: lookup(%s) returned %d, expected %zu\n", q, r.idx, i);
            exit(2);
        }
        hits++;
        if (r.memcmps == 0) fast_path++;
        total_memcmps += r.memcmps;
        if (r.memcmps > max_memcmps) max_memcmps = r.memcmps;
        total_surviving += r.surviving_bits;
        if (r.surviving_bits > max_surviving) max_surviving = r.surviving_bits;
    }

    // Miss queries: synthesize keys that share prefix/length with real fields
    const char *miss_queries[] = {
        // random misses
        "xyz", "foobar", "unknown_field", "somethingElse",
        // prefix-colliding misses targeting the datasets above
        "customerXYZ",            // hits prefix-heavy up through "customer"
        "transactionXYZ",         // hits very-long up through "transaction"
        "createXYZ",              // hits timestamps through "create"
        "traceXYZ",               // hits rest-dto / logging through "trace"
        // length-matching misses
        "aaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccccccc",
    };
    size_t n_miss = sizeof(miss_queries)/sizeof(miss_queries[0]);
    for (size_t i = 0; i < n_miss; i++) {
        const char *q = miss_queries[i];
        size_t qlen = strlen(q);
        probe_result r = bitmap8k_probe(b, q, qlen);
        misses++;
        if (r.memcmps == 0) fast_path++;
        total_memcmps += r.memcmps;
        if (r.memcmps > max_memcmps) max_memcmps = r.memcmps;
        total_surviving += r.surviving_bits;
        if (r.surviving_bits > max_surviving) max_surviving = r.surviving_bits;
    }

    long total = hits + misses;
    size_t pk = maxlen < K ? maxlen : K;
    size_t mem_bytes = pk * 256 + (maxlen + 1) + ds->n * 4;

    printf("  prefix_k=%zu mem=%zu B\n", pk, mem_bytes);
    printf("  hits=%ld  misses=%ld  total=%ld\n", hits, misses, total);
    printf("  fast-path (no memcmp): %ld / %ld  (%.1f%%)\n",
           fast_path, total, 100.0 * fast_path / total);
    printf("  avg memcmps/query:    %.3f   max: %ld\n",
           (double)total_memcmps / total, max_memcmps);
    printf("  avg surviving bits:   %.3f   max: %ld\n",
           (double)total_surviving / total, max_surviving);
}

int main(void) {
    printf("=== bitmap8k FP-collision analysis (K=%d) ===\n", K);
    printf("Goal: measure how often multiple bits survive the prefix filter\n");
    printf("and trigger additional memcmp(s) on the slow path.\n");
    for (size_t i = 0; i < n_datasets; i++) analyze(&datasets[i]);

    printf("\n=== Summary ===\n");
    printf("Fast path = klen <= K (no memcmp needed; prefix is exact filter).\n");
    printf("Slow path = klen > K; memcmp count depends on bit collisions.\n");
    printf("Worst case (prefix-heavy set): see avg/max memcmps above.\n");
    return 0;
}
