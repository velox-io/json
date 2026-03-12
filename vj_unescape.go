package vjson

import (
	"fmt"
	"unicode/utf8"
	"unsafe"
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
	// Found backslash
	if i+1 >= n {
		return i, pos, fmt.Errorf("vjson: trailing backslash in string at offset %d", i)
	}

	next := data[i+1]
	if next == 'u' {
		// Unicode escape \uXXXX
		// Need exactly 4 hex digits after \u
		if i+5 < n {
			// Validate all 4 characters are hex
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
		// Invalid or incomplete unicode escape
		return i, pos, fmt.Errorf("vjson: invalid unicode escape in string at offset %d", i)
	}

	// Lookup table for common escapes
	if unescaped := escapeTable[next]; unescaped != 0 {
		dst[pos] = unescaped
		return i + 2, pos + 1, nil
	}

	// Unknown escape — RFC 8259 only allows " \\ / b f n r t uXXXX
	return i, pos, fmt.Errorf("vjson: invalid escape '\\%c' in string at offset %d", next, i)
}

// unescapeSinglePass scans src from firstEscIdx for the closing '"', unescaping
// escape sequences as it goes. src[start:firstEscIdx] is the prefix before the
// first backslash (copied verbatim).
// Returns (endIdx past closing quote, decoded []byte, error).
// The returned []byte is backed by arena, scratch, or heap — see done: label.
func (sc *Parser) unescapeSinglePass(src []byte, start, firstEscIdx int) (int, []byte, error) {
	n := len(src)

	// Choose initial decode buffer
	arenaRemaining := len(sc.arenaData) - sc.arenaOff
	var buf []byte
	decodingInArena := false
	if arenaRemaining >= scratchBufSize {
		buf = sc.arenaData[sc.arenaOff:]
		decodingInArena = true
	} else {
		buf = sc.buf[:]
	}
	overflowed := false

	// Copy the prefix before the first escape (no escapes, verbatim copy)
	prefixLen := firstEscIdx - start
	if prefixLen > len(buf) {
		// Prefix alone exceeds buffer — grow
		newSize := len(buf) * 2
		for newSize < prefixLen {
			newSize *= 2
		}
		buf = make([]byte, newSize)
		decodingInArena = false
		overflowed = true
	}
	copy(buf[:prefixLen], src[start:firstEscIdx])
	pos := prefixLen // write position in buf

	// grow ensures buf has room for at least `need` more bytes at pos.
	grow := func(need int) {
		if pos+need <= len(buf) {
			return
		}
		newSize := len(buf) * 2
		for newSize < pos+need {
			newSize *= 2
		}
		newBuf := make([]byte, newSize)
		copy(newBuf[:pos], buf[:pos])
		buf = newBuf
		decodingInArena = false
		overflowed = true
	}

	// Single-pass decode loop
	i := firstEscIdx
	for i+8 <= n {
		// Ensure room for 8-byte SWAR copy (fast path)
		if pos+8 > len(buf) {
			grow(8)
		}

		w := *(*uint64)(unsafe.Pointer(&src[i]))

		// SWAR: check for '"' (0x22), '\\' (0x5C), or control chars (< 0x20)
		mq := hasZeroByte(w ^ (lo64 * 0x22))
		mb := hasZeroByte(w ^ (lo64 * 0x5C))
		mc := (w - lo64*0x20) & ^w & hi64 // < 0x20
		combined := mq | mb | mc

		if combined == 0 {
			// No special char — copy 8 bytes directly.
			// Unaligned store is safe on amd64/arm64 (all SWAR accesses in this
			// codebase assume unaligned read/write support from the target arch).
			*(*uint64)(unsafe.Pointer(&buf[pos])) = w
			pos += 8
			i += 8
			continue
		}

		// Found quote, backslash, or control char — determine which and where
		off := firstMarkedByteIndex(combined)
		c := src[i+off]

		// Copy bytes before the found character (up to 7 bytes)
		// Ensure room: off bytes prefix + up to 4 bytes for escape result
		grow(off + 4)
		j := 0
		for j+4 <= off {
			*(*uint32)(unsafe.Pointer(&buf[pos+j])) = *(*uint32)(unsafe.Pointer(&src[i+j]))
			j += 4
		}
		for j < off {
			buf[pos+j] = src[i+j]
			j++
		}
		pos += off
		i += off

		if c == '"' {
			goto done
		}

		if c < 0x20 {
			return i, nil, fmt.Errorf("vjson: control character in string at offset %d", i)
		}

		// c == '\\': process escape inline
		if i+1 < n {
			next := src[i+1]
			if next != 'u' {
				if esc := escapeTable[next]; esc != 0 {
					buf[pos] = esc
					pos++
					i += 2
					continue
				}
				// Unknown escape — RFC 8259 only allows " \\ / b f n r t uXXXX
				return i, nil, fmt.Errorf("vjson: invalid escape '\\%c' in string at offset %d", next, i)
			}
			// \uXXXX path — may write up to 4 bytes
			grow(4)
			var unescErr error
			i, pos, unescErr = unescapeSequence(src, n, i, buf, pos)
			if unescErr != nil {
				return i, nil, unescErr
			}
		} else {
			return i, nil, fmt.Errorf("vjson: trailing backslash in string at offset %d", i)
		}
	}

	// Tail: < 8 bytes remaining, byte-by-byte
	for i < n {
		c := src[i]
		if c == '"' {
			goto done
		}
		if c == '\\' {
			if i+1 < n {
				next := src[i+1]
				if next != 'u' {
					grow(2)
					if esc := escapeTable[next]; esc != 0 {
						buf[pos] = esc
						pos++
						i += 2
						continue
					}
					// Unknown escape — RFC 8259 only allows " \\ / b f n r t uXXXX
					return i, nil, fmt.Errorf("vjson: invalid escape '\\%c' in string at offset %d", next, i)
				}
				grow(4)
				var unescErr error
				i, pos, unescErr = unescapeSequence(src, n, i, buf, pos)
				if unescErr != nil {
					return i, nil, unescErr
				}
			} else {
				return i, nil, fmt.Errorf("vjson: trailing backslash in string at offset %d", i)
			}
			continue
		}
		if c < 0x20 {
			return i, nil, fmt.Errorf("vjson: control character in string at offset %d", i)
		}
		grow(1)
		buf[pos] = c
		pos++
		i++
	}

	return i, nil, errUnexpectedEOF

done:
	// Finalize the decoded result
	var result []byte
	if decodingInArena {
		// Case 1: decoded directly into arena — zero copy
		result = sc.arenaData[sc.arenaOff : sc.arenaOff+pos]
		sc.arenaOff += pos
	} else if !overflowed {
		// Case 2: decoded into scratch buf — must copy to persistent storage
		if pos <= arenaInlineMax {
			dst := sc.arenaAlloc(pos)
			copy(dst, buf[:pos])
			result = dst
		} else {
			dst := make([]byte, pos)
			copy(dst, buf[:pos])
			result = dst
		}
	} else {
		// Case 3: overflowed into heap buffer — use directly
		result = buf[:pos]
	}
	return i + 1, result, nil
}

// processEscapedString handles strings with escape sequences.
// Thin wrapper around unescapeSinglePass that assigns the result to the target field.
func (sc *Parser) processEscapedString(src []byte, start, firstEscIdx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	endIdx, result, err := sc.unescapeSinglePass(src, start, firstEscIdx)
	if err != nil {
		return endIdx, err
	}
	s := unsafe.String(unsafe.SliceData(result), len(result))
	switch ti.Kind {
	case KindString:
		*(*string)(ptr) = s
	case KindAny:
		*(*any)(ptr) = s
	default:
		return endIdx, fmt.Errorf("vjson: cannot assign string to %v field", ti.Kind)
	}
	return endIdx, nil
}
