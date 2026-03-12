package vjson

import (
	"math/bits"
	"unsafe"
)

const (
	lo64 uint64 = 0x0101010101010101 // 每字节低位
	hi64 uint64 = 0x8080808080808080 // 每字节高位
)

func firstMarkedByteIndex(mask uint64) int {
	return bits.TrailingZeros64(mask) >> 3
}

// hasZeroByte returns a mask with the high bit set for each zero byte in x.
//
// Classic null-byte detection (Mycroft's trick, see Hacker's Delight §6-1):
// for each byte b in x, (b - 0x01) borrows from the high bit when b == 0,
// and (^b & 0x80) is true when b < 0x80. Their conjunction is true only when
// b == 0. To search for a specific byte c, callers XOR the word with
// broadcast(c) first, turning c-bytes into zero-bytes.
func hasZeroByte(x uint64) uint64 {
	return (x - lo64) & ^x & hi64
}

// sliceAt performs an unchecked index into a slice, bypassing bounds checks.
// Callers must guarantee i is within bounds.
//
//go:nosplit
func sliceAt[T any](s []T, i int) T {
	return *(*T)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(s)), unsafe.Sizeof(*new(T))*uintptr(i)))
}

// slicePtr returns an unsafe.Pointer to the first element of s.
//
//go:nosplit
func slicePtr[T any](s []T) unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(s))
}

// slicePtrT returns a typed pointer to the first element of s.
//
//go:nosplit
func slicePtrT[T any](s []T) *T {
	return (*T)(slicePtr(s))
}

// sliceRangeT returns s[start:end] without bounds checks.
//
//go:nosplit
func sliceRangeT[T any](src []T, start, end int) []T {
	base := unsafe.SliceData(src)
	return unsafe.Slice((*T)(unsafe.Add(unsafe.Pointer(base), uintptr(start)*unsafe.Sizeof(*new(T)))), end-start)
}

// unsafeString converts a []byte to string without copying.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}
