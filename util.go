package vjson

import (
	"math/bits"
	"unsafe"
)

// firstMarkedByteIndex returns the byte index of the first marked byte
// in a SWAR bitmask (where marked bytes have their high bit set).
// arm64 is always little-endian.
func firstMarkedByteIndex(mask uint64) int {
	return bits.TrailingZeros64(mask) >> 3
}

// unsafeString converts a byte slice to a string without copying.
// The caller must ensure the byte slice is not modified during the
// lifetime of the returned string.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
