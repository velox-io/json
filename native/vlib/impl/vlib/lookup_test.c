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
    printf("Short keys (3): tier = %s (footprint = %zu / alloc = %zu)\n",
           ndec_lookup_tier_name_ex(l), ndec_lookup_footprint(l), sz);

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
    printf("  Tier used: %s (footprint = %zu / alloc = %zu)\n", ndec_lookup_tier_name_ex(l),
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
    printf("\nMany keys (25): tier = %s\n", ndec_lookup_tier_name_ex(l));
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

  printf("\nAll tests passed!\n");
  return 0;
}
