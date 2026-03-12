package vjson

import (
	"unicode/utf8"
	"unsafe"
)

type escapeFlags uint8

const (
	escapeHTML        escapeFlags = 1 << iota // escape <, >, &
	escapeLineTerms                           // escape U+2028, U+2029
	escapeInvalidUTF8                         // replace invalid UTF-8 with \ufffd
)

const escapeDefault = escapeInvalidUTF8 | escapeLineTerms
const escapeStdCompat = escapeDefault | escapeHTML

var needsEscape [256]bool

func init() {
	needsEscape['"'] = true
	needsEscape['\\'] = true
	for i := range byte(0x20) {
		needsEscape[i] = true
	}
}

var escapeReplacement [256][2]byte
var escapeHasReplacement [256]bool

func init() {
	set := func(c byte, r0, r1 byte) {
		escapeReplacement[c] = [2]byte{r0, r1}
		escapeHasReplacement[c] = true
	}
	set('"', '\\', '"')
	set('\\', '\\', '\\')
	set('\b', '\\', 'b')
	set('\f', '\\', 'f')
	set('\n', '\\', 'n')
	set('\r', '\\', 'r')
	set('\t', '\\', 't')
}

const hexDigits = "0123456789abcdef"

// appendEscapedString appends s as a quoted JSON string to buf.
// SWAR scans 8 bytes at a time; non-ASCII bytes fall back to rune decoding.
func appendEscapedString(buf []byte, s string, flags escapeFlags) []byte {
	buf = append(buf, '"')

	n := len(s)
	if n == 0 {
		return append(buf, '"')
	}

	checkUTF8 := flags&escapeInvalidUTF8 != 0
	checkLineTerms := flags&escapeLineTerms != 0
	checkHTML := flags&escapeHTML != 0
	needRuneScan := checkUTF8 || checkLineTerms

	base := unsafe.StringData(s)
	i := 0
	start := 0

	for i+8 <= n {
		w := *(*uint64)(unsafe.Add(unsafe.Pointer(base), i))

		mq := hasZeroByte(w ^ (lo64 * 0x22)) // "
		mb := hasZeroByte(w ^ (lo64 * 0x5C)) // \
		mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
		mask := mq | mb | mc

		if checkHTML {
			mask |= hasZeroByte(w ^ (lo64 * 0x3C))
			mask |= hasZeroByte(w ^ (lo64 * 0x3E))
			mask |= hasZeroByte(w ^ (lo64 * 0x26))
		}

		if needRuneScan && w&hi64 != 0 {
			nonASCIIoff := firstMarkedByteIndex(w & hi64)

			// Handle ASCII special char before the non-ASCII byte if present.
			if mask != 0 {
				asciiOff := firstMarkedByteIndex(mask)
				if asciiOff < nonASCIIoff {
					pos := i + asciiOff
					if pos > start {
						buf = append(buf, s[start:pos]...)
					}
					buf = appendEscapeByte(buf, s[pos])
					i = pos + 1
					start = i
					continue
				}
			}

			pos := i + nonASCIIoff
			if pos > start {
				buf = append(buf, s[start:pos]...)
				start = pos
			}

			// Lazy flush: advance j past valid runes, only flush+replace on bad ones.
			j := pos
			for j < n && s[j] >= 0x80 {
				r, size := utf8.DecodeRuneInString(s[j:])
				if r == utf8.RuneError && size <= 1 && checkUTF8 {
					if j > start {
						buf = append(buf, s[start:j]...)
					}
					buf = append(buf, '\\', 'u', 'f', 'f', 'f', 'd')
					j += size
					if size == 0 {
						j++
					}
					start = j
					continue
				}
				if checkUTF8 && r >= 0xD800 && r <= 0xDFFF {
					if j > start {
						buf = append(buf, s[start:j]...)
					}
					buf = append(buf, '\\', 'u', 'f', 'f', 'f', 'd')
					j += size
					start = j
					continue
				}
				if checkLineTerms && (r == 0x2028 || r == 0x2029) {
					if j > start {
						buf = append(buf, s[start:j]...)
					}
					buf = appendUnicodeEscape(buf, r)
					j += size
					start = j
					continue
				}
				j += size
			}
			i = j
			continue
		}

		if mask == 0 {
			i += 8
			continue
		}

		off := firstMarkedByteIndex(mask)
		pos := i + off

		if pos > start {
			buf = append(buf, s[start:pos]...)
		}

		buf = appendEscapeByte(buf, s[pos])
		i = pos + 1
		start = i
	}

	// Scalar tail
	for i < n {
		c := s[i]

		if c < 0x80 {
			if needsEscape[c] || (checkHTML && (c == '<' || c == '>' || c == '&')) {
				if i > start {
					buf = append(buf, s[start:i]...)
				}
				buf = appendEscapeByte(buf, c)
				i++
				start = i
				continue
			}
			i++
			continue
		}

		if !needRuneScan {
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])

		if r == utf8.RuneError && size <= 1 {
			if checkUTF8 {
				if i > start {
					buf = append(buf, s[start:i]...)
				}
				buf = append(buf, '\\', 'u', 'f', 'f', 'f', 'd')
				i += size
				if size == 0 {
					i++
				}
				start = i
				continue
			}
		}

		if checkUTF8 && r >= 0xD800 && r <= 0xDFFF {
			if i > start {
				buf = append(buf, s[start:i]...)
			}
			buf = append(buf, '\\', 'u', 'f', 'f', 'f', 'd')
			i += size
			start = i
			continue
		}

		if checkLineTerms && (r == 0x2028 || r == 0x2029) {
			if i > start {
				buf = append(buf, s[start:i]...)
			}
			buf = appendUnicodeEscape(buf, r)
			i += size
			start = i
			continue
		}

		i += size
	}

	if start < n {
		buf = append(buf, s[start:]...)
	}

	return append(buf, '"')
}

// appendEscapeByte emits the JSON escape for a single byte.
func appendEscapeByte(buf []byte, c byte) []byte {
	if escapeHasReplacement[c] {
		r := escapeReplacement[c]
		return append(buf, r[0], r[1])
	}
	return append(buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0F])
}

// appendUnicodeEscape emits \uXXXX (or a surrogate pair for non-BMP runes).
func appendUnicodeEscape(buf []byte, r rune) []byte {
	if r <= 0xFFFF {
		return append(buf,
			'\\', 'u',
			hexDigits[(r>>12)&0xF],
			hexDigits[(r>>8)&0xF],
			hexDigits[(r>>4)&0xF],
			hexDigits[r&0xF],
		)
	}
	r -= 0x10000
	hi := 0xD800 + (r>>10)&0x3FF
	lo := 0xDC00 + r&0x3FF
	buf = append(buf,
		'\\', 'u',
		hexDigits[(hi>>12)&0xF],
		hexDigits[(hi>>8)&0xF],
		hexDigits[(hi>>4)&0xF],
		hexDigits[hi&0xF],
	)
	return append(buf,
		'\\', 'u',
		hexDigits[(lo>>12)&0xF],
		hexDigits[(lo>>8)&0xF],
		hexDigits[(lo>>4)&0xF],
		hexDigits[lo&0xF],
	)
}
