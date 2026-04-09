/*
 * timefmt.h — RFC 3339 Nano time formatting for Go time.Time
 *
 * C-native formatting of time.Time to RFC3339Nano for the encoder VM.
 *
 * Coverage:
 *   - loc == NULL → UTC, suffix "Z"
 *   - loc != NULL && zone_len == 1 → FixedZone, suffix ±HH:MM
 *   - Otherwise → yield to Go (DST / complex Location)
 *   - year ∉ [0, 9999] → yield to Go
 */

#ifndef VJ_TIMEFMT_H
#define VJ_TIMEFMT_H

#include <stddef.h>
#include <stdint.h>

/* Go time.Time memory layout (24 bytes)
 *
 * wall: bit63=hasMonotonic, bits[30..62]=secs since 1885, bits[0..29]=nsec
 * ext:  hasMonotonic ? monotonic ns : full internal secs (since year 1)
 * loc:  *Location; nil = UTC */

typedef struct {
  uint64_t wall;
  int64_t ext;
  const void *loc; /* *time.Location */
} GoTime;

_Static_assert(sizeof(GoTime) == 24, "GoTime must be 24 bytes");

/* Go time.Location layout (partial)
 *
 * Location { name string(16B); zone []zone(24B); ... }
 * zone     { name string(16B); offset int(8B); isDST bool(1B) } */

/* Check whether this Location can be handled natively.
 * NULL=UTC, zone_len==1=FixedZone → native.  Otherwise yield. */
static inline int vj_time_can_native(const void *loc) {
  if (loc == NULL)
    return 1;
  int64_t zone_len = *(const int64_t *)((const uint8_t *)loc + 24);
  return zone_len == 1;
}

/* Get timezone offset (seconds) for a FixedZone.
 * Caller must ensure loc != NULL and vj_time_can_native() == 1. */
static inline int32_t vj_time_get_offset(const void *loc) {
  const uint8_t *zone_ptr =
      *(const uint8_t *const *)((const uint8_t *)loc + 16);
  if (zone_ptr == NULL)
    return 0;
  return (int32_t)(*(const int64_t *)(zone_ptr + 16));
}

/* Constants from Go's time package */

#define VJ_TIME_HAS_MONOTONIC ((uint64_t)1 << 63)
#define VJ_TIME_NSEC_MASK ((uint64_t)((1 << 30) - 1))
#define VJ_TIME_NSEC_SHIFT 30

#define VJ_TIME_WALL_TO_INTERNAL 59453308800LL /* wallToInternal */
#define VJ_TIME_UNIX_TO_INTERNAL 62135596800LL /* unixToInternal */
#define VJ_TIME_INTERNAL_TO_ABS 9223371966606163200LL
#define VJ_TIME_UNIX_TO_ABS                                                    \
  9223372028741760000LL /* unixToInternal + internalToAbsolute */
#define VJ_TIME_ABSOLUTE_YEARS 292277022400ULL
#define VJ_TIME_MARCH_THRU_DEC 306

#define VJ_SECONDS_PER_DAY 86400
#define VJ_SECONDS_PER_HOUR 3600
#define VJ_SECONDS_PER_MINUTE 60

/* Extract internal seconds (since year 1) and nanoseconds from time.Time. */
static inline void vj_time_extract(const GoTime *t, int64_t *out_sec,
                                   int32_t *out_nsec) {
  uint64_t wall = t->wall;
  *out_nsec = (int32_t)(wall & VJ_TIME_NSEC_MASK);
  if (wall & VJ_TIME_HAS_MONOTONIC) {
    *out_sec = VJ_TIME_WALL_TO_INTERNAL +
               (int64_t)(wall << 1 >> (VJ_TIME_NSEC_SHIFT + 1));
  } else {
    *out_sec = t->ext;
  }
}

static inline void vj_write_2d(uint8_t *buf, int val) {
  buf[0] = (uint8_t)('0' + val / 10);
  buf[1] = (uint8_t)('0' + val % 10);
}

/* Format time.Time as RFC3339Nano into buf.  Returns bytes written.
 * Max output: "2006-01-02T15:04:05.999999999+00:00" = 37 bytes (with quotes).
 * year_out receives the computed year (caller checks [0, 9999]). */
static inline int vj_write_rfc3339nano(uint8_t *buf, const GoTime *t,
                                       int32_t tz_offset, int *year_out) {
  uint8_t *start = buf;

  int64_t isec;
  int32_t nsec;
  vj_time_extract(t, &isec, &nsec);

  int64_t unix_sec = isec - VJ_TIME_UNIX_TO_INTERNAL + (int64_t)tz_offset;
  uint64_t abs = (uint64_t)(unix_sec + VJ_TIME_UNIX_TO_ABS);

  /* Split into days and time-of-day */
  uint64_t days = abs / VJ_SECONDS_PER_DAY;
  uint32_t day_sec = (uint32_t)(abs % VJ_SECONDS_PER_DAY);

  /* Neri-Schneider: days → year/month/day */
  uint64_t d4 = 4 * days + 3;
  uint64_t century = d4 / 146097;
  uint32_t cd = (uint32_t)(d4 % 146097) | 3;

  uint64_t mul = (uint64_t)2939745 * (uint64_t)cd;
  uint32_t cyear = (uint32_t)(mul >> 32);
  uint32_t ayday = (uint32_t)((uint32_t)mul / 2939745 / 4);

  uint32_t md = 2141 * ayday + 197913;
  uint32_t amonth = md >> 16;
  uint32_t mday = 1 + (md & 0xFFFF) / 2141;

  uint32_t janFeb = (ayday >= VJ_TIME_MARCH_THRU_DEC) ? 1 : 0;
  int year =
      (int)(century * 100 - VJ_TIME_ABSOLUTE_YEARS) + (int)cyear + (int)janFeb;
  uint32_t month = amonth - janFeb * 12;

  *year_out = year;

  /* Clock */
  uint32_t hour = day_sec / VJ_SECONDS_PER_HOUR;
  uint32_t rem = day_sec % VJ_SECONDS_PER_HOUR;
  uint32_t min = rem / VJ_SECONDS_PER_MINUTE;
  uint32_t sec = rem % VJ_SECONDS_PER_MINUTE;

  /* "YYYY-MM-DDTHH:MM:SS" */
  *buf++ = '"';
  {
    int y = year;
    buf[0] = (uint8_t)('0' + y / 1000);
    y %= 1000;
    buf[1] = (uint8_t)('0' + y / 100);
    y %= 100;
    buf[2] = (uint8_t)('0' + y / 10);
    buf[3] = (uint8_t)('0' + y % 10);
    buf += 4;
  }
  *buf++ = '-';
  vj_write_2d(buf, (int)month);
  buf += 2;
  *buf++ = '-';
  vj_write_2d(buf, (int)mday);
  buf += 2;
  *buf++ = 'T';
  vj_write_2d(buf, (int)hour);
  buf += 2;
  *buf++ = ':';
  vj_write_2d(buf, (int)min);
  buf += 2;
  *buf++ = ':';
  vj_write_2d(buf, (int)sec);
  buf += 2;

  /* Fractional seconds: 9 digits, trailing zeros truncated, omit if 0 */
  if (nsec > 0) {
    *buf++ = '.';
    uint8_t digits[9];
    uint32_t n = (uint32_t)nsec;
    for (int i = 8; i >= 0; i--) {
      digits[i] = (uint8_t)('0' + n % 10);
      n /= 10;
    }
    int last = 8;
    while (last > 0 && digits[last] == '0')
      last--;
    for (int i = 0; i <= last; i++) {
      *buf++ = digits[i];
    }
  }

  /* Timezone: "Z" or "±HH:MM" */
  if (tz_offset == 0) {
    *buf++ = 'Z';
  } else {
    int off = tz_offset;
    if (off < 0) {
      *buf++ = '-';
      off = -off;
    } else {
      *buf++ = '+';
    }
    vj_write_2d(buf, off / VJ_SECONDS_PER_HOUR);
    buf += 2;
    *buf++ = ':';
    vj_write_2d(buf, (off % VJ_SECONDS_PER_HOUR) / VJ_SECONDS_PER_MINUTE);
    buf += 2;
  }

  *buf++ = '"';
  return (int)(buf - start);
}

#endif /* VJ_TIMEFMT_H */
