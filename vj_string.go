package vjson

import (
	"unsafe"
)

const (
	lo64 = uint64(0x0101010101010101)
	hi64 = uint64(0x8080808080808080)
)

// hasZeroByte returns a mask with the high bit set for each zero byte.
// Returns 0 if no byte in x is zero.
func hasZeroByte(x uint64) uint64 {
	return (x - lo64) & ^x & hi64
}

// findStructuralChar scans src starting at idx for JSON structural characters:
// '{', '}', '[', ']', '"'. Returns (foundIdx, foundChar).
// If end of buffer reached, returns (len(src), 0).
//
// NOTE: The Go compiler does not inline this function (cost 185 > budget 80),
// so skipContainer inlines the SWAR loop in its hot path.
//
//nolint:unused // kept for reference; not used in hot paths
func findStructuralChar(src []byte, idx int) (int, byte) {
	n := len(src)

	for idx+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[idx]))

		m1 := hasZeroByte(w ^ (lo64 * '{')) // 0x7B
		m2 := hasZeroByte(w ^ (lo64 * '}')) // 0x7D
		m3 := hasZeroByte(w ^ (lo64 * '[')) // 0x5B
		m4 := hasZeroByte(w ^ (lo64 * ']')) // 0x5D
		m5 := hasZeroByte(w ^ (lo64 * '"')) // 0x22

		combined := m1 | m2 | m3 | m4 | m5
		if combined != 0 {
			off := firstMarkedByteIndex(combined)
			return idx + off, src[idx+off]
		}
		idx += 8
	}

	for i := idx; i < n; i++ {
		c := src[i]
		switch c {
		case '{', '}', '[', ']', '"':
			return i, c
		}
	}
	return n, 0
}

// findQuoteOrBackslash scans src starting at idx for '"', '\\', or control chars (< 0x20).
// Returns (foundIdx, foundChar). If end of buffer reached, returns (len(src), 0).
//
// Uses SWAR to process 8 bytes at a time, falling back to byte-at-a-time for tails.
//
// NOTE: The Go compiler does not inline this function (cost 143 > budget 80),
// so hot-path callers (scanStringBytes, scanStringAny, skipString) manually
// inline the SWAR logic for performance.
func findQuoteOrBackslash(src []byte, idx int) (int, byte) {
	n := len(src)

	// Process 8 bytes at a time.
	for idx+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[idx]))

		// Check for '"' (0x22)
		mq := hasZeroByte(w ^ (lo64 * 0x22))

		// Check for '\\' (0x5C)
		mb := hasZeroByte(w ^ (lo64 * 0x5C))

		// Check for control chars (byte < 0x20):
		// bit trick: (w-0x20) & ^w & hi64 flags bytes with value < 0x20.
		mc := (w - lo64*0x20) & ^w & hi64

		combined := mq | mb | mc
		if combined != 0 {
			off := firstMarkedByteIndex(combined)
			return idx + off, src[idx+off]
		}
		idx += 8
	}

	// Handle remaining bytes
	for i := idx; i < n; i++ {
		c := src[i]
		if c == '"' || c == '\\' || c < 0x20 {
			return i, c
		}
	}
	return n, 0
}
