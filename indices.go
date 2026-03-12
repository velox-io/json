package pjson

import "math/bits"

// extractIndices extracts all set bit positions (0..63) from bitmask
// into buf, and returns the number of positions written.
//
// Uses compiler-intrinsic TrailingZeros64 (RBIT+CLZ on arm64) with
// direct array writes to avoid slice append/growslice overhead.
func extractIndices(bitmask uint64, buf *[64]uint32) int {
	n := 0
	for bitmask != 0 {
		buf[n] = uint32(bits.TrailingZeros64(bitmask))
		n++
		bitmask &= bitmask - 1
	}
	return n
}
