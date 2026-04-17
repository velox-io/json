// Standalone test for us_write_float64
// Build: cc -O2 -I../../include -I. -o test_uscale test_uscale.c
// Run:   ./test_uscale

#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <stdint.h>

// Avoid math.h conflicts with floor/ceil/round/div in uscale.c
#define floor us_floor
#define round us_round
#define ceil  us_ceil
#define div   us_div

#define INLINE static inline

#include "uscale.c"

#undef floor
#undef round
#undef ceil
#undef div

// Helper: fabs without math.h
static double my_fabs(double x) { return x < 0 ? -x : x; }

typedef struct {
    double value;
    const char *expected;
} test_case;

int main(void) {
    test_case cases[] = {
        { -1.5055146587107769e-13, "-1.5055146587107769e-13" },
        { 1.5055146587107769e-13,  "1.5055146587107769e-13" },
        { 1.0, "1" },
        { 0.1, "0.1" },
        { 1e-7, "1e-7" },
        { 1.7976931348623157e+308, "1.7976931348623157e+308" },
        { 5e-324, "5e-324" },
        { 2.2250738585072014e-308, "2.2250738585072014e-308" },
        { 0.0, "0" },
        { -0.0, "-0" },
        { 3.14159265358979323846, "3.141592653589793" },
        { 1e21, "1e+21" },
        { 1e-6, "0.000001" },
        /* 9.999999999999998e-7 and ...997e-7 map to the same double;
           shortest representation ends in 7, not 8. */
        { 9.999999999999998e-7, "9.999999999999997e-7" },
    };
    int ncases = sizeof(cases) / sizeof(cases[0]);

    uint8_t buf[64];
    int failed = 0;

    for (int i = 0; i < ncases; i++) {
        int n = us_write_float64(buf, cases[i].value, US_FMT_EXP_AUTO);
        buf[n] = '\0';

        if (strcmp((char*)buf, cases[i].expected) != 0) {
            printf("FAIL case %d: value=%.17g\n", i, cases[i].value);
            printf("  expected: %s\n", cases[i].expected);
            printf("  got:      %s\n", (char*)buf);
            failed++;

            // Debug: dump internal state
            uint64_t bits;
            __builtin_memcpy(&bits, &cases[i].value, 8);
            printf("  bits: 0x%016llx\n", (unsigned long long)bits);

            double posval = my_fabs(cases[i].value);
            uint64_t d;
            int p;
            us_short64(posval, &d, &p);
            printf("  short64: d=%llu p=%d\n", (unsigned long long)d, p);
        } else {
            printf("PASS case %d: %s\n", i, (char*)buf);
        }
    }

    printf("\n%d/%d passed\n", ncases - failed, ncases);
    return failed ? 1 : 0;
}
