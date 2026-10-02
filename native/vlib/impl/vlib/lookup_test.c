// Build:
//   cc -O2 -std=c11 -Wall -I../../include  lookup_test.c lookup.c -o build/lookup_test
//
#include "lookup.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Build scratch for gperf/hand tiers. One static buffer is fine for the
// single-threaded test harness.
static char g_scratch[80 * 1024];
#define TEST_SCRATCH .scratch = g_scratch, .scratch_size = sizeof(g_scratch)

static ndec_lookup *build_tier(const ndec_lookup_key *keys, size_t n, ndec_lookup_tier_mask tiers) {
  ndec_lookup_config cfg = {.keys = keys, .n = n, .tiers = tiers, TEST_SCRATCH};
  size_t sz              = ndec_lookup_size_for(&cfg);
  assert(sz > 0);
  ndec_lookup *l = malloc(sz);
  assert(ndec_lookup_init(l, sz, &cfg) > 0);
  return l;
}

// match_text reports ndec_lookup_match_quoted over text in a padded buffer.
static int match_text(const ndec_lookup *l, size_t idx, const char *text) {
  char buf[256];
  memset(buf, ' ', sizeof(buf));
  memcpy(buf, text, strlen(text));
  return ndec_lookup_match_quoted(l, idx, buf);
}

// check_predicted pins every key of a perfect tier against itself, its
// neighbours, an unquoted continuation, and the one-past index.
static void check_predicted(const ndec_lookup *l, const ndec_lookup_key *keys, size_t n) {
  char quoted[128], longer[128];
  for (size_t i = 0; i < n; i++) {
    snprintf(quoted, sizeof(quoted), "%.*s\":1", (int)keys[i].len, keys[i].str);
    snprintf(longer, sizeof(longer), "%.*sx\":1", (int)keys[i].len, keys[i].str);
    for (size_t j = 0; j < n; j++)
      assert(match_text(l, j, quoted) == (i == j));
    assert(match_text(l, i, longer) == 0);
    assert(match_text(l, n, quoted) == 0);
    assert(match_text(l, i, "\":1") == 0);
  }
  // The empty key's body is its closing quote, which a slot read past the
  // last key would accept.
  assert(match_text(l, n, "\":1") == 0);
}

int main(void) {
  assert(ndec_lookup_scratch_size() <= sizeof(g_scratch));
  printf("Testing ndec_lookup...\n\n");

  // ---- Case 1: short keys, WINDOW tier expected. ----
  {
    ndec_lookup_key keys[] = {{"id", 2}, {"name", 4}, {"value", 5}};
    ndec_lookup_config cfg = {.keys = keys, .n = 3, .tiers = NDEC_LOOKUP_TIERS_ALL, TEST_SCRATCH};

    size_t sz      = ndec_lookup_size_for(&cfg);
    ndec_lookup *l = malloc(sz);
    int r          = ndec_lookup_init(l, sz, &cfg);
    assert(r > 0);
    printf("Short keys (3): tier = %s (footprint = %zu / alloc = %zu)\n", ndec_lookup_tier_name(ndec_lookup_get_tier(l)),
           ndec_lookup_footprint(l), sz);

    char buf[128] = {0};
    strcpy(buf, "id");
    buf[2] = '"';
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 2}) == 0);
    strcpy(buf, "name");
    buf[4] = '"';
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 4}) == 1);
    strcpy(buf, "value");
    buf[5] = '"';
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 5}) == 2);
    memset(buf, 0, sizeof(buf));
    strcpy(buf, "missing");
    buf[7] = '"';
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 7}) == -1);
    free(l);
  }

  // ---- Case 1b: WINDOW tier, query keys with an embedded '"'. ----
  // A decoded JSON key (e.g. "age\"") can carry '"' inside its body. The
  // embedded quote must not masquerade as the terminator of a shorter stored
  // key whose prefix the query extends.
  {
    ndec_lookup_key keys[] = {{"id", 2}, {"name", 4}, {"value", 5}};
    ndec_lookup_config cfg = {.keys = keys, .n = 3, .tiers = NDEC_LOOKUP_TIER_WINDOW, TEST_SCRATCH};

    size_t sz      = ndec_lookup_size_for(&cfg);
    ndec_lookup *l = malloc(sz);
    assert(ndec_lookup_init(l, sz, &cfg) > 0);
    assert(ndec_lookup_get_tier(l) == NDEC_LOOKUP_TIER_WINDOW);

    char buf[128] = {0};
    memcpy(buf, "id\"", 3);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 3}) == -1);
    memcpy(buf, "name\"", 5);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 5}) == -1);
    memcpy(buf, "value\"", 6);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 6}) == -1);
    /* A query shorter than a stored key with padding '"' bytes must miss too. */
    memcpy(buf, "na\"me", 5);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 5}) == -1);
    /* Exact keys still hit. */
    memcpy(buf, "id", 2);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 2}) == 0);
    memcpy(buf, "value", 5);
    assert(ndec_lookup_find(l, (ndec_lookup_key){buf, 5}) == 2);
    free(l);
    printf("\nEmbedded-quote queries OK.\n");
  }

  // ---- Case 2: long keys, requires TABLE tier. ----
  {
    ndec_lookup_key keys[] = {
        {"veryveryverylongkeyname_that_exceeds_sixtythreebytes_number_one_xyz", 67},
        {"another_extremely_long_key_that_is_definitely_more_than_sixty_three_bytes_long", 78},
        {"thirdkeywithmorethansixtythreebytesofnamepaddingtoexceedthelimitforalltests", 75},
    };
    ndec_lookup_config cfg = {.keys = keys, .n = 3, .tiers = NDEC_LOOKUP_TIERS_ALL, TEST_SCRATCH};

    printf("\nLong keys (3):\n");
    for (int i = 0; i < 3; i++)
      printf("  Key %d: %zu bytes\n", i, keys[i].len);

    size_t sz      = ndec_lookup_size_for(&cfg);
    ndec_lookup *l = malloc(sz);
    int r          = ndec_lookup_init(l, sz, &cfg);
    assert(r > 0);
    printf("  Tier used: %s (footprint = %zu / alloc = %zu)\n", ndec_lookup_tier_name(ndec_lookup_get_tier(l)),
           ndec_lookup_footprint(l), sz);
    assert(ndec_lookup_get_tier(l) == NDEC_LOOKUP_TIER_TABLE);

    char buf[512];
    for (int i = 0; i < 3; i++) {
      memset(buf, 0, sizeof(buf));
      strcpy(buf, keys[i].str);
      buf[keys[i].len] = '"';
      assert(ndec_lookup_find(l, (ndec_lookup_key){buf, keys[i].len}) == i);
    }
    free(l);
  }

  // ---- Case 3: many keys, GPERF or HAND expected. ----
  {
    ndec_lookup_key keys[] = {
        {"field_001", 9}, {"field_002", 9}, {"field_003", 9}, {"field_004", 9}, {"field_005", 9},
        {"field_006", 9}, {"field_007", 9}, {"field_008", 9}, {"field_009", 9}, {"field_010", 9},
        {"field_011", 9}, {"field_012", 9}, {"field_013", 9}, {"field_014", 9}, {"field_015", 9},
        {"field_016", 9}, {"field_017", 9}, {"field_018", 9}, {"field_019", 9}, {"field_020", 9},
        {"field_021", 9}, {"field_022", 9}, {"field_023", 9}, {"field_024", 9}, {"field_025", 9}};
    ndec_lookup_config cfg = {.keys = keys, .n = 25, .tiers = NDEC_LOOKUP_TIERS_PERFECT, TEST_SCRATCH};

    size_t sz      = ndec_lookup_size_for(&cfg);
    ndec_lookup *l = malloc(sz);
    int r          = ndec_lookup_init(l, sz, &cfg);
    assert(r > 0);
    printf("\nMany keys (25): tier = %s\n", ndec_lookup_tier_name(ndec_lookup_get_tier(l)));
    assert(ndec_lookup_get_tier(l) != NDEC_LOOKUP_TIER_TABLE);

    char buf[128] = {0};
    for (int i = 0; i < 25; i++) {
      memset(buf, 0, sizeof(buf));
      strcpy(buf, keys[i].str);
      buf[keys[i].len] = '"';
      int idx          = ndec_lookup_find(l, (ndec_lookup_key){buf, keys[i].len});
      assert(idx == i);
    }
    free(l);
  }

  // ---- Case 4: error paths. ----
  {
    ndec_lookup_config empty = {.keys = NULL, .n = 0, .tiers = 0};
    assert(ndec_lookup_init(NULL, 0, &empty) == NDEC_LOOKUP_ERR_NULL_ARG);
    ndec_lookup_key one[]  = {{"a", 1}};
    ndec_lookup_config cfg = {.keys = one, .n = 1, .tiers = NDEC_LOOKUP_TIERS_ALL, TEST_SCRATCH};
    char tiny[8];
    assert(ndec_lookup_init((ndec_lookup *)tiny, 8, &cfg) == NDEC_LOOKUP_ERR_STORAGE_TOO_SMALL);
    ndec_lookup_key dup[]   = {{"a", 1}, {"a", 1}};
    ndec_lookup_config dcfg = {.keys = dup, .n = 2, .tiers = NDEC_LOOKUP_TIERS_ALL};
    size_t sz               = ndec_lookup_size_for(&dcfg);
    assert(sz == 0);
    ndec_lookup *l = malloc(64);
    assert(ndec_lookup_init(l, 64, &dcfg) == NDEC_LOOKUP_ERR_KEY_DUPLICATE);
    free(l);
    ndec_lookup_key bad[]   = {{"a\"b", 3}};
    ndec_lookup_config bcfg = {.keys = bad, .n = 1, .tiers = NDEC_LOOKUP_TIERS_ALL};
    l                       = malloc(64);
    assert(ndec_lookup_init(l, 64, &bcfg) == NDEC_LOOKUP_ERR_KEY_INVALID_BYTE);
    free(l);
    printf("\nError paths OK.\n");
  }

  // ---- Case 5: predicted match on every tier. ----
  {
    ndec_lookup_key prefix[] = {{"id", 2}, {"idx", 3}, {"name", 4}, {"exactly_sixteen_", 16}};
    ndec_lookup_key many[]   = {
        {"apiVersion", 10},     {"kind", 4},          {"metadata", 8},         {"spec", 4},
        {"status", 6},          {"name", 4},          {"namespace", 9},        {"labels", 6},
        {"annotations", 11},    {"creationTimestamp", 17}, {"resourceVersion", 15}, {"uid", 3},
        {"ownerReferences", 15}, {"finalizers", 10},  {"managedFields", 13},   {"conditions", 10},
    };
    ndec_lookup_key long63[] = {
        {"a23456789012345678901234567890123456789012345678901234567890123", 63},
        {"b23456789012345678901234567890123456789012345678901234567890123", 63},
        {"c", 1},
    };
    ndec_lookup_tier_mask perfect[] = {NDEC_LOOKUP_TIER_WINDOW, NDEC_LOOKUP_TIER_GPERF, NDEC_LOOKUP_TIER_HAND};
    struct {
      const ndec_lookup_key *keys;
      size_t n;
    } sets[] = {{prefix, 4}, {many, 16}, {long63, 3}};
    int built = 0;
    for (size_t s = 0; s < sizeof(sets) / sizeof(sets[0]); s++) {
      for (size_t t = 0; t < 3; t++) {
        ndec_lookup_config cfg = {.keys = sets[s].keys, .n = sets[s].n, .tiers = perfect[t], TEST_SCRATCH};
        size_t sz              = ndec_lookup_size_for(&cfg);
        ndec_lookup *l         = malloc(sz ? sz : 64);
        if (sz == 0 || ndec_lookup_init(l, sz, &cfg) <= 0) {
          free(l);
          continue;
        }
        assert(ndec_lookup_get_tier(l) == (ndec_lookup_tier)perfect[t]);
        check_predicted(l, sets[s].keys, sets[s].n);
        built |= 1 << t;
        free(l);
      }
    }
    assert(built == 7);

    ndec_lookup *l = build_tier(prefix, 4, NDEC_LOOKUP_TIERS_ALL);
    assert(match_text(l, 0, "idx\"") == 0);
    assert(match_text(l, 1, "id\"") == 0);
    assert(match_text(l, 0, "\\u0069d\"") == 0);
    assert(match_text(l, 0, "i\"") == 0);
    free(l);

    ndec_lookup_key longk[] = {{"veryveryverylongkeyname_that_exceeds_sixtythreebytes_number_one_xyz", 67}, {"a", 1}};
    l                       = build_tier(longk, 2, NDEC_LOOKUP_TIERS_ALL);
    assert(ndec_lookup_get_tier(l) == NDEC_LOOKUP_TIER_TABLE);
    assert(match_text(l, 1, "a\"") == 0);
    free(l);

    uint32_t none = NDEC_LOOKUP_TIER_NONE;
    assert(ndec_lookup_match_quoted((const ndec_lookup *)&none, 0, "a\"") == 0);
    printf("\nPredicted match OK.\n");
  }

  printf("\nAll tests passed!\n");
  return 0;
}
