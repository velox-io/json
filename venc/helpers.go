package venc

import (
	"math/bits"
	"reflect"
	"time"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// SliceHeader matches the internal layout of a Go slice.
type SliceHeader = gort.SliceHeader

// SWAR constants for byte-level searches within a uint64.
const (
	lo64 uint64 = 0x0101010101010101 // every byte low bit
	hi64 uint64 = 0x8080808080808080 // every byte high bit
)

// hasZeroByte returns a mask with the high bit set for each zero byte in x.
//
// Classic null-byte detection (Mycroft's trick, see Hacker's Delight S6-1):
// for each byte b in x, (b - 0x01) borrows from the high bit when b == 0,
// and (^b & 0x80) is true when b < 0x80. Their conjunction is true only when
// b == 0. To search for a specific byte c, callers XOR the word with
// broadcast(c) first, turning c-bytes into zero-bytes.
func hasZeroByte(x uint64) uint64 {
	return (x - lo64) & ^x & hi64
}

func firstMarkedByteIndex(mask uint64) int {
	return bits.TrailingZeros64(mask) >> 3
}

// unsafeString converts a []byte to string without copying.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// rtypePtr extracts the *abi.Type from a reflect.Type interface value.
func rtypePtr(t reflect.Type) unsafe.Pointer {
	return gort.TypePtr(t)
}

func maplen(m unsafe.Pointer) int {
	return gort.MapLen(m)
}

var timeType = reflect.TypeFor[time.Time]()
