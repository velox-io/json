/*
 * scanner.h — Inline scanning helpers for gsdec.
 *
 * All functions are static inline for zero-overhead inclusion.
 * No external dependencies beyond stdint.h.
 */

#ifndef GSDEC_SCANNER_H
#define GSDEC_SCANNER_H

#include <stdint.h>

/* ============================================================
 *  String scanning
 * ============================================================ */

/*
 * scan_string: scan past a JSON string body (opening " already consumed).
 * On success: *idx points past the closing ".
 * Returns: 0 = ok, 1 = eof, 2 = error.
 * Sets *has_escape if any backslash found.
 * Sets *str_start to the first byte after opening ".
 * Sets *str_end to the byte before closing ".
 */
static inline int scan_string(const uint8_t *src, uint32_t src_len,
                               uint32_t *idx, uint32_t *str_start,
                               uint32_t *str_end, int *has_escape) {
    uint32_t i = *idx;
    *str_start = i;
    *has_escape = 0;

    while (i < src_len) {
        uint8_t c = src[i];
        if (c == '"') {
            *str_end = i;
            *idx = i + 1; /* past closing " */
            return 0;
        }
        if (c == '\\') {
            *has_escape = 1;
            i++; /* skip backslash */
            if (i >= src_len) {
                *idx = i;
                return 1; /* eof mid-escape */
            }
            /* skip escaped char (including \uXXXX — simplified) */
        }
        i++;
    }
    *idx = i;
    return 1; /* eof */
}

/* ============================================================
 *  Number scanning
 * ============================================================ */

/*
 * scan_number: scan a JSON number (integer only for now).
 * On success: *idx points past the last digit.
 * Returns: 0 = ok, 1 = eof (number at buffer boundary).
 */
static inline int scan_number(const uint8_t *src, uint32_t src_len,
                               uint32_t *idx) {
    uint32_t i = *idx;
    if (i < src_len && src[i] == '-') i++;
    if (i >= src_len) { *idx = i; return 1; }

    /* At least one digit required */
    if (src[i] < '0' || src[i] > '9') return 2; /* error */
    while (i < src_len && src[i] >= '0' && src[i] <= '9') i++;

    /* Skip fractional part */
    if (i < src_len && src[i] == '.') {
        i++;
        while (i < src_len && src[i] >= '0' && src[i] <= '9') i++;
    }

    /* Skip exponent */
    if (i < src_len && (src[i] == 'e' || src[i] == 'E')) {
        i++;
        if (i < src_len && (src[i] == '+' || src[i] == '-')) i++;
        while (i < src_len && src[i] >= '0' && src[i] <= '9') i++;
    }

    /* Streaming guard: if we consumed up to buffer end, the number
     * might be truncated (e.g., "123" could be "1234" with more data).
     * Return eof so caller retries after refill. */
    if (i >= src_len) {
        *idx = i;
        return 1;
    }

    *idx = i;
    return 0;
}

/* ============================================================
 *  Integer parsing (from scanned bytes)
 * ============================================================ */

static inline int64_t parse_int64(const uint8_t *src, uint32_t start, uint32_t end) {
    int neg = 0;
    uint32_t i = start;
    if (i < end && src[i] == '-') { neg = 1; i++; }
    int64_t val = 0;
    while (i < end) {
        val = val * 10 + (src[i] - '0');
        i++;
    }
    return neg ? -val : val;
}

static inline uint64_t parse_uint64(const uint8_t *src, uint32_t start, uint32_t end) {
    uint64_t val = 0;
    for (uint32_t i = start; i < end; i++) {
        val = val * 10 + (src[i] - '0');
    }
    return val;
}

/* ============================================================
 *  String unescape (into arena buffer)
 * ============================================================ */

static inline int hex_digit(uint8_t c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/*
 * unescape_to: unescape JSON string body into dst.
 * src points to the content between quotes (may contain backslash escapes).
 * Returns number of bytes written to dst.
 */
static inline uint32_t unescape_to(const uint8_t *src, uint32_t src_len,
                                     uint8_t *dst, uint32_t dst_cap) {
    uint32_t si = 0, di = 0;
    while (si < src_len && di < dst_cap) {
        if (src[si] != '\\') {
            dst[di++] = src[si++];
            continue;
        }
        si++; /* skip backslash */
        if (si >= src_len) break;
        switch (src[si]) {
        case '"':  dst[di++] = '"';  si++; break;
        case '\\': dst[di++] = '\\'; si++; break;
        case '/':  dst[di++] = '/';  si++; break;
        case 'b':  dst[di++] = '\b'; si++; break;
        case 'f':  dst[di++] = '\f'; si++; break;
        case 'n':  dst[di++] = '\n'; si++; break;
        case 'r':  dst[di++] = '\r'; si++; break;
        case 't':  dst[di++] = '\t'; si++; break;
        case 'u': {
            /* \uXXXX → UTF-8 (simplified: BMP only) */
            si++;
            if (si + 4 > src_len) goto done;
            int h0 = hex_digit(src[si]), h1 = hex_digit(src[si+1]);
            int h2 = hex_digit(src[si+2]), h3 = hex_digit(src[si+3]);
            if (h0 < 0 || h1 < 0 || h2 < 0 || h3 < 0) { si += 4; break; }
            uint32_t cp = (h0 << 12) | (h1 << 8) | (h2 << 4) | h3;
            si += 4;
            /* UTF-8 encode */
            if (cp < 0x80) {
                dst[di++] = (uint8_t)cp;
            } else if (cp < 0x800) {
                if (di + 2 > dst_cap) goto done;
                dst[di++] = 0xC0 | (cp >> 6);
                dst[di++] = 0x80 | (cp & 0x3F);
            } else {
                if (di + 3 > dst_cap) goto done;
                dst[di++] = 0xE0 | (cp >> 12);
                dst[di++] = 0x80 | ((cp >> 6) & 0x3F);
                dst[di++] = 0x80 | (cp & 0x3F);
            }
            break;
        }
        default:
            dst[di++] = src[si++];
            break;
        }
    }
done:
    return di;
}

/* ============================================================
 *  Field lookup (linear scan, matches rsdec)
 * ============================================================ */

static inline int bytes_equal(const uint8_t *a, uint32_t alen,
                               const uint8_t *b, uint32_t blen) {
    if (alen != blen) return 0;
    for (uint32_t i = 0; i < alen; i++) {
        if (a[i] != b[i]) return 0;
    }
    return 1;
}

#endif /* GSDEC_SCANNER_H */
