package vjson

import (
	"unsafe"
)

const (
	lo64 = uint64(0x0101010101010101)
	hi64 = uint64(0x8080808080808080)
)

// hasZeroByte returns a mask with the high bit set for each zero byte.
func hasZeroByte(x uint64) uint64 {
	return (x - lo64) & ^x & hi64
}

// findStructuralChar scans for JSON structural characters: '{', '}', '[', ']', '"'.
//
//nolint:unused // kept as reference; hot-path callers inline the SWAR loop
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

// findQuoteOrBackslash scans for '"', '\\', or control chars (< 0x20).
// Not used directly in hot paths — callers inline the SWAR logic.
func findQuoteOrBackslash(src []byte, idx int) (int, byte) {
	n := len(src)

	for idx+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[idx]))
		mq := hasZeroByte(w ^ (lo64 * 0x22))
		mb := hasZeroByte(w ^ (lo64 * 0x5C))
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
