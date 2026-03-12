package pjson

import "math/bits"

// ExtractIndices extracts all set bit positions (0..63) from bitmask,
// appends them as uint32 values to indices, and returns the extended slice.
//
// Uses simdjson-style 8x unrolled tzcnt+blsr loop to reduce loop overhead.
// Each extracted index is a chunk-local offset (0..63).
func ExtractIndices(bitmask uint64, indices []uint32) []uint32 {
	if bitmask == 0 {
		return indices
	}

	cnt := bits.OnesCount64(bitmask)

	// 8x unrolled extraction: process 8 bits per iteration
	for cnt >= 8 {
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
		cnt -= 8
	}

	// Tail: process remaining < 8 bits one by one
	for bitmask != 0 {
		indices = append(indices, uint32(bits.TrailingZeros64(bitmask)))
		bitmask &= bitmask - 1
	}

	return indices
}
