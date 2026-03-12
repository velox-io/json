package vjson

import "math/bits"

// firstMarkedByteIndex returns the byte index of the first marked byte
// in a SWAR bitmask (where marked bytes have their high bit set).
// arm64 is always little-endian.
func firstMarkedByteIndex(mask uint64) int {
	return bits.TrailingZeros64(mask) >> 3
}
