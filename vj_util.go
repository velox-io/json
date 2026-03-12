package vjson

import "unsafe"

// UnsafeString converts a byte slice to a string without copying.
// The caller must ensure the byte slice is not modified during the
// lifetime of the returned string.
func UnsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// wsLUT is a lookup table for JSON whitespace characters.
// Indexed by byte value; non-zero means whitespace.
var wsLUT [256]byte

func init() {
	wsLUT[' '] = 1
	wsLUT['\t'] = 1
	wsLUT['\n'] = 1
	wsLUT['\r'] = 1
}

// skipWS skips JSON whitespace (SP, TAB, LF, CR) starting at idx.
func skipWS(src []byte, idx int) int {
	for idx < len(src) && wsLUT[src[idx]] != 0 {
		idx++
	}
	return idx
}

// skipWSLong skips JSON whitespace, optimized for long runs (newline + indentation
// in pretty-printed JSON). SWAR scans 8 bytes at a time for all-spaces (0x20).
func skipWSLong(src []byte, idx int) int {
	n := len(src)
	// Quick bail: no whitespace at all (compact JSON fast path)
	if idx >= n || wsLUT[src[idx]] == 0 {
		return idx
	}
	// Skip leading newline/CR if present (the typical "\n   ..." pattern)
	if src[idx] <= '\r' { // Handle leading control whitespace (\t, \n, \r), including CRLF.
		idx++
		if idx >= n || wsLUT[src[idx]] == 0 {
			return idx
		}
		// After \r there may be \n
		if src[idx] == '\n' {
			idx++
			if idx >= n || wsLUT[src[idx]] == 0 {
				return idx
			}
		}
	}
	// SWAR: scan 8 bytes at a time checking for all-spaces.
	// After the newline, indentation is almost always spaces (0x20).
	// Compare 8 bytes at once against broadcast(0x20).
	const allSpaces = lo64 * 0x20 // 0x2020202020202020
	for idx+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[idx]))
		if w != allSpaces {
			break
		}
		idx += 8
	}
	// Tail: byte-at-a-time for remaining bytes and non-space whitespace
	for idx < n && wsLUT[src[idx]] != 0 {
		idx++
	}
	return idx
}

// parseInt64 parses an integer from src[start:end] without allocation.
// Handles optional leading '-'. No overflow or format validation.
// Uses SWAR 8-digit fast path when the digit span is long enough.
func parseInt64(src []byte, start, end int) int64 {
	if start >= end {
		return 0
	}
	neg := false
	i := start
	if src[i] == '-' {
		neg = true
		i++
	}
	n := int64(parseUint64(src, i, end))
	if neg {
		return -n
	}
	return n
}

// parseEightDigitsSWAR converts exactly 8 ASCII digit bytes into a uint32
// using SWAR reduction (based on simdjson's parse_eight_digits_unrolled).
// Caller must ensure src[i:i+8] are ASCII digits and i+8 <= len(src).
//
// Steps (little-endian):
//  1. & 0x0F… strips ASCII high nibble, converting '0'-'9' → 0-9
//  2. *2561 (=256*10+1) merges adjacent digits into 2-digit values
//  3. *6553601 (=65536*100+1) merges 2-digit pairs into 4-digit values
//  4. *42949672960001 (=2^32*10000+1) merges the two 4-digit halves
func parseEightDigitsSWAR(src []byte, i int) uint32 {
	val := *(*uint64)(unsafe.Pointer(&src[i]))
	val = (val & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
	val = (val & 0x00FF00FF00FF00FF) * 6553601 >> 16
	val = (val & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
	return uint32(val)
}

// parseUint64 parses an unsigned integer from src[start:end] without allocation.
// Uses SWAR 8-digit fast path when the digit span is long enough.
func parseUint64(src []byte, start, end int) uint64 {
	i := start
	nDigits := end - start

	var n uint64
	// SWAR fast path: parse 8 digits at a time
	if nDigits >= 8 {
		n = uint64(parseEightDigitsSWAR(src, i))
		i += 8
		nDigits -= 8
		if nDigits >= 8 {
			n = n*100_000_000 + uint64(parseEightDigitsSWAR(src, i))
			i += 8
		}
	}
	// Tail: Horner's method for remaining digits
	for ; i < end; i++ {
		n = n*10 + uint64(src[i]-'0')
	}
	return n
}

var internedFloats = func() [256]any {
	var arr [256]any
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}()

// WriteIntValue writes an int64 value to the pointer based on the element type kind.
func WriteIntValue(ptr unsafe.Pointer, kind ElemTypeKind, v int64) {
	switch kind {
	case KindInt:
		*(*int)(ptr) = int(v)
	case KindInt8:
		*(*int8)(ptr) = int8(v)
	case KindInt16:
		*(*int16)(ptr) = int16(v)
	case KindInt32:
		*(*int32)(ptr) = int32(v)
	case KindInt64:
		*(*int64)(ptr) = v
	}
}

// WriteUintValue writes a uint64 value to the pointer based on the element type kind.
func WriteUintValue(ptr unsafe.Pointer, kind ElemTypeKind, v uint64) {
	switch kind {
	case KindUint:
		*(*uint)(ptr) = uint(v)
	case KindUint8:
		*(*uint8)(ptr) = uint8(v)
	case KindUint16:
		*(*uint16)(ptr) = uint16(v)
	case KindUint32:
		*(*uint32)(ptr) = uint32(v)
	case KindUint64:
		*(*uint64)(ptr) = v
	}
}
