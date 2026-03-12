package vjson

import "unsafe"

// wsLUT is a lookup table for JSON whitespace characters.
// Indexed by byte value; non-zero means whitespace.
var wsLUT [256]byte

func init() {
	wsLUT[' '] = 1
	wsLUT['\t'] = 1
	wsLUT['\n'] = 1
	wsLUT['\r'] = 1
}

// skipWS skips whitespace bytes starting at idx.
// Returns index of the next non-whitespace byte (or len(src)).
//
// Used at call sites where whitespace is typically short (0-1 bytes):
// after ':', after value (before ',' / '}' / ']'), and scanPointer entry.
func skipWS(src []byte, idx int) int {
	for idx < len(src) && wsLUT[src[idx]] != 0 {
		idx++
	}
	return idx
}

// skipWSLong skips whitespace bytes starting at idx, optimized for runs
// that are typically 8+ bytes (newline + indentation).
//
// Used at call sites where whitespace is typically long:
// after '{' / '[' (93%+ are 8+ bytes) and after ',' (96%+ are 8+ bytes)
// in pretty-printed JSON.
//
// Strategy: skip the leading newline byte, then SWAR scan 8 bytes at a time
// checking that all bytes are spaces (0x20). This covers the dominant case
// of "\n    " indentation patterns. Falls back to byte-at-a-time for other
// whitespace characters and for the tail.
func skipWSLong(src []byte, idx int) int {
	n := len(src)
	// Quick bail: no whitespace at all (compact JSON fast path)
	if idx >= n || wsLUT[src[idx]] == 0 {
		return idx
	}
	// Skip leading newline/CR if present (the typical "\n   ..." pattern)
	if src[idx] <= '\r' { // \t=0x09, \n=0x0A, \r=0x0D are all < ' '=0x20
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
// using SWAR (SIMD Within A Register) parallel reduction.
// Caller must ensure src[i:i+8] are all ASCII digits and i+8 <= len(src).
// Requires little-endian byte order (always true on amd64/arm64).
//
// Algorithm from simdjson (scalar fallback for parse_eight_digits_unrolled):
//
//	Step 1: mask off high nibbles → each byte is 0-9
//	Step 2: multiply by 2561 (=256*10+1) merges adjacent digit pairs
//	         into 2-digit values in the high byte of each 16-bit lane, shift right 8
//	Step 3: multiply by 6553601 (=65536*100+1) merges adjacent 2-digit pairs
//	         into 4-digit values in the high 16 bits of each 32-bit lane, shift right 16
//	Step 4: multiply by 42949672960001 (=2^32*10000+1) merges the two 4-digit halves
//	         into the final 8-digit value in the upper 32 bits, shift right 32
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
