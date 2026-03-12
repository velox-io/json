package vjson

import (
	"fmt"
	"unicode/utf8"
)

// Escape translation table: maps escape character to its unescaped value.
// Invalid escapes map to 0 (rejected per RFC 8259).
var escapeTable = [256]byte{
	'"':  '"',
	'\\': '\\',
	'/':  '/',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
}

// isHexChar returns true if c is a valid hexadecimal digit
func isHexChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// hexToRune parses exactly 4 hex digits into a rune without allocation.
func hexToRune(hex []byte) rune {
	var r rune
	for _, c := range hex[:4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

// isHighSurrogate returns true if r is a UTF-16 high surrogate (D800-DBFF)
func isHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// isLowSurrogate returns true if r is a UTF-16 low surrogate (DC00-DFFF)
func isLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// decodeSurrogatePair combines a high and low surrogate into a single rune.
// Formula from RFC 2781: 0x10000 + (high - 0xD800) * 0x400 + (low - 0xDC00)
func decodeSurrogatePair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

// unescapeSequence processes a single escape sequence starting at data[i] (which is '\').
// Returns (new read position, new write position, error).
// dst must have enough space for the output.
func unescapeSequence(data []byte, n int, i int, dst []byte, pos int) (int, int, error) {
	if i+1 >= n {
		return i, pos, errUnexpectedEOF
	}

	next := data[i+1]
	if next == 'u' {
		// \uXXXX: need exactly 4 hex digits
		if i+5 < n {
			hexChars := data[i+2 : i+6]
			if isHexChar(hexChars[0]) && isHexChar(hexChars[1]) && isHexChar(hexChars[2]) && isHexChar(hexChars[3]) {
				r := hexToRune(hexChars)

				// Check for UTF-16 surrogate pair
				if isHighSurrogate(r) {
					// Look ahead for low surrogate \uXXXX
					if i+11 < n && data[i+6] == '\\' && data[i+7] == 'u' {
						lowHex := data[i+8 : i+12]
						if isHexChar(lowHex[0]) && isHexChar(lowHex[1]) && isHexChar(lowHex[2]) && isHexChar(lowHex[3]) {
							low := hexToRune(lowHex)
							if isLowSurrogate(low) {
								// Valid surrogate pair - decode to single rune
								combined := decodeSurrogatePair(r, low)
								pos += utf8.EncodeRune(dst[pos:], combined)
								return i + 12, pos, nil
							}
						}
					}
					// Isolated high surrogate - use replacement character
					dst[pos] = 0xEF
					dst[pos+1] = 0xBF
					dst[pos+2] = 0xBD
					return i + 6, pos + 3, nil
				}

				if isLowSurrogate(r) {
					// Isolated low surrogate - use replacement character
					dst[pos] = 0xEF
					dst[pos+1] = 0xBF
					dst[pos+2] = 0xBD
					return i + 6, pos + 3, nil
				}

				// Normal Unicode character
				pos += utf8.EncodeRune(dst[pos:], r)
				return i + 6, pos, nil
			}
		}
		// Incomplete or invalid unicode escape
		if i+5 >= n {
			return i, pos, errUnexpectedEOF
		}
		return i, pos, newSyntaxError(fmt.Sprintf("vjson: invalid unicode escape in string at offset %d", i), i)
	}

	// Lookup table for common escapes
	if unescaped := escapeTable[next]; unescaped != 0 {
		dst[pos] = unescaped
		return i + 2, pos + 1, nil
	}

	// Unknown escape — RFC 8259 only allows " \\ / b f n r t uXXXX
	return i, pos, newSyntaxError(fmt.Sprintf("vjson: invalid escape '\\%c' in string at offset %d", next, i), i)
}
