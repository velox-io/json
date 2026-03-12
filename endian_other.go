//go:build !amd64 && !arm64

package vjson

import (
	"math/bits"
	"unsafe"
)

var isLittleEndian = func() bool {
	var x uint16 = 0x0100
	return *(*byte)(unsafe.Pointer(&x)) == 0
}()

// firstMarkedByteIndex returns the byte index of the first marked byte
// in a SWAR bitmask (where marked bytes have their high bit set).
// Falls back to runtime endianness detection on unknown architectures.
func firstMarkedByteIndex(mask uint64) int {
	if isLittleEndian {
		return bits.TrailingZeros64(mask) >> 3
	}
	return bits.LeadingZeros64(mask) >> 3
}
