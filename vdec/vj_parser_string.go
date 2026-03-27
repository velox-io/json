package vdec

import (
	"fmt"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// unescapeSinglePass scans src from firstEscIdx for the closing '"', unescaping
// as it goes. src[start:firstEscIdx] is the prefix before the first backslash.
// Returns (endIdx past closing quote, decoded []byte, error).
// Decoded bytes land in arena (zero-copy), scratch→arena/heap, or heap overflow.
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
		buf = sc.scratchBuf[:]
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
			return i, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", i), i)
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
				return i, nil, newSyntaxError(fmt.Sprintf("vjson: invalid escape '\\%c' in string at offset %d", next, i), i)
			}
			// \uXXXX path — may write up to 4 bytes
			grow(4)
			var unescErr error
			i, pos, unescErr = unescapeSequence(src, n, i, buf, pos)
			if unescErr != nil {
				return i, nil, unescErr
			}
		} else {
			return i, nil, errUnexpectedEOF
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
					return i, nil, newSyntaxError(fmt.Sprintf("vjson: invalid escape '\\%c' in string at offset %d", next, i), i)
				}
				grow(4)
				var unescErr error
				i, pos, unescErr = unescapeSequence(src, n, i, buf, pos)
				if unescErr != nil {
					return i, nil, unescErr
				}
			} else {
				return i, nil, errUnexpectedEOF
			}
			continue
		}
		if c < 0x20 {
			return i, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", i), i)
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
func (sc *Parser) processEscapedString(src []byte, start, firstEscIdx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	endIdx, result, err := sc.unescapeSinglePass(src, start, firstEscIdx)
	if err != nil {
		return endIdx, err
	}
	needCopy := sc.copyString
	var s string
	if needCopy {
		s = string(result)
	} else {
		s = unsafe.String(unsafe.SliceData(result), len(result))
	}
	switch ti.Kind {
	case typ.KindString:
		*(*string)(ptr) = s
	case typ.KindAny:
		*(*any)(ptr) = s
	default:
		return endIdx, newUnmarshalTypeError("string", ti.Type, endIdx)
	}
	return endIdx, nil
}
