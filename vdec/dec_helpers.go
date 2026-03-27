package vdec

import (
	"fmt"
	"math/bits"
	"unicode/utf8"
	"unsafe"

	"github.com/velox-io/json/gort"
)

type SliceHeader = gort.SliceHeader

const (
	lo64 uint64 = 0x0101010101010101
	hi64 uint64 = 0x8080808080808080
)

func hasZeroByte(x uint64) uint64 {
	return (x - lo64) & ^x & hi64
}

func firstMarkedByteIndex(mask uint64) int {
	return bits.TrailingZeros64(mask) >> 3
}

func sliceAt[T any](s []T, i int) T {
	return *(*T)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(s)), unsafe.Sizeof(*new(T))*uintptr(i)))
}

func slicePtr[T any](s []T) unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(s))
}

func slicePtrT[T any](s []T) *T {
	return (*T)(slicePtr(s))
}

func sliceRangeT[T any](src []T, start, end int) []T {
	base := unsafe.SliceData(src)
	return unsafe.Slice((*T)(unsafe.Add(unsafe.Pointer(base), uintptr(start)*unsafe.Sizeof(*new(T)))), end-start)
}

func unsafe_NewArray(typ unsafe.Pointer, n int) unsafe.Pointer { //nolint:revive
	return gort.UnsafeNewArray(typ, n)
}

func typedslicecopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int {
	return gort.TypedSliceCopy(typ, dstPtr, dstLen, srcPtr, srcLen)
}

func unsafe_New(typ unsafe.Pointer) unsafe.Pointer { //nolint:revive
	return gort.UnsafeNew(typ)
}

func makemap(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer {
	return gort.MakeMap(t, hint, m)
}

func mapassign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer {
	return gort.MapAssign(t, m, key)
}

func mapassign_faststr(t unsafe.Pointer, m unsafe.Pointer, key string) unsafe.Pointer { //nolint:revive
	return gort.MapAssignFastStr(t, m, key)
}

func typedmemmove(typ unsafe.Pointer, dst unsafe.Pointer, src unsafe.Pointer) {
	gort.TypedMemmove(typ, dst, src)
}

func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

var escapeTable = [256]byte{
	'"':  '"',
	'\\': '\\',
	'/':  '/',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
}

func isHexChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) rune {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0')
	case c >= 'a' && c <= 'f':
		return rune(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return rune(c - 'A' + 10)
	}
	return 0
}

func hexToRune(h []byte) rune {
	return hexVal(h[0])<<12 | hexVal(h[1])<<8 | hexVal(h[2])<<4 | hexVal(h[3])
}

func isHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func isLowSurrogate(r rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }
func decodeSurrogatePair(high, low rune) rune {
	return (high-0xD800)*0x400 + (low - 0xDC00) + 0x10000
}

func unescapeSequence(data []byte, n int, i int, dst []byte, pos int) (int, int, error) {
	if i+1 >= n {
		return i, pos, errUnexpectedEOF
	}

	next := data[i+1]
	if next == 'u' {
		if i+5 < n {
			hexChars := data[i+2 : i+6]
			if isHexChar(hexChars[0]) && isHexChar(hexChars[1]) && isHexChar(hexChars[2]) && isHexChar(hexChars[3]) {
				r := hexToRune(hexChars)

				if isHighSurrogate(r) {
					if i+11 < n && data[i+6] == '\\' && data[i+7] == 'u' {
						lowHex := data[i+8 : i+12]
						if isHexChar(lowHex[0]) && isHexChar(lowHex[1]) && isHexChar(lowHex[2]) && isHexChar(lowHex[3]) {
							low := hexToRune(lowHex)
							if isLowSurrogate(low) {
								combined := decodeSurrogatePair(r, low)
								pos += utf8.EncodeRune(dst[pos:], combined)
								return i + 12, pos, nil
							}
						}
					}
					dst[pos] = 0xEF
					dst[pos+1] = 0xBF
					dst[pos+2] = 0xBD
					return i + 6, pos + 3, nil
				}

				if isLowSurrogate(r) {
					dst[pos] = 0xEF
					dst[pos+1] = 0xBF
					dst[pos+2] = 0xBD
					return i + 6, pos + 3, nil
				}

				pos += utf8.EncodeRune(dst[pos:], r)
				return i + 6, pos, nil
			}
		}
		if i+5 >= n {
			return i, pos, errUnexpectedEOF
		}
		return i, pos, newSyntaxError(fmt.Sprintf("vjson: invalid unicode escape in string at offset %d", i), i)
	}

	if unescaped := escapeTable[next]; unescaped != 0 {
		dst[pos] = unescaped
		return i + 2, pos + 1, nil
	}

	return i, pos, newSyntaxError(fmt.Sprintf("vjson: invalid escape '\\%c' in string at offset %d", next, i), i)
}
