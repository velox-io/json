package venc

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

// escapeStringFlags selects the slow-path string checks. Zero enables the native fast VM.
const escapeStringFlags = escapeHTML | escapeLineTerms | escapeInvalidUTF8
const escapeStdCompat = escapeStringFlags

// EncFloatExpAuto (bit 3) matches encoding/json scientific-notation thresholds.
// Stored alongside escape flags in encodeState.flags.
const EncFloatExpAuto uint32 = 1 << 3

var (
	needsEscape          [256]bool
	escapeReplacement    [256][2]byte
	escapeHasReplacement [256]bool
)

func init() {
	for i := range byte(0x20) {
		needsEscape[i] = true
	}
	needsEscape['"'] = true
	needsEscape['\\'] = true

	for _, e := range [...][3]byte{
		{'"', '\\', '"'},
		{'\\', '\\', '\\'},
		{'\b', '\\', 'b'},
		{'\f', '\\', 'f'},
		{'\n', '\\', 'n'},
		{'\r', '\\', 'r'},
		{'\t', '\\', 't'},
	} {
		escapeReplacement[e[0]] = [2]byte{e[1], e[2]}
		escapeHasReplacement[e[0]] = true
	}
}

const hexDigits = "0123456789abcdef"

// AppendQuotedJSONString is the minimal key-encoding helper used by the root package.
func AppendQuotedJSONString(buf []byte, s string) []byte {
	return appendEscapedString(buf, s, 0)
}

// appendEscapedString quotes a string and falls back to rune decoding only when needed.
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

			// Flush earlier ASCII escapes before entering the non-ASCII rune path.
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

func appendEscapeByte(buf []byte, c byte) []byte {
	if escapeHasReplacement[c] {
		r := escapeReplacement[c]
		return append(buf, r[0], r[1])
	}
	return append(buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0F])
}

// appendUnicodeEscape emits a BMP escape or surrogate pair.
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
