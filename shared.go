package vjson

import (
	"errors"
	"unsafe"
)

var (
	errEmptyInput    = errors.New("vjson: empty input")
	errUnexpectedEOF = errors.New("vjson: unexpected end of input")
	errSyntax        = errors.New("vjson: syntax error")
	errNotPointer    = errors.New("vjson: v must be a non-nil pointer")
)

// UnsafeString converts a byte slice to a string without copying.
// The caller must ensure the byte slice is not modified during the
// lifetime of the returned string.
func UnsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// SliceHeader matches the internal layout of a Go slice.
type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// hexToRune parses exactly 4 hex digits into a rune without allocation.
func hexToRune(hex []byte) rune {
	var r rune
	for _, c := range hex[:4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

func WriteIntValue(ptr unsafe.Pointer, kind ElemTypeKind, v int64) {
	switch kind {
	case KindInt:
		*(*int)(ptr) = int(v)
	case KindInt8:
		*(*int8)(ptr) = int8(v)
	case KindInt16:
		*(*int16)(ptr) = int16(v)
	case KindInt32:
		*(*int32)(ptr) = int32(v)
	case KindInt64:
		*(*int64)(ptr) = v
	}
}

func WriteUintValue(ptr unsafe.Pointer, kind ElemTypeKind, v uint64) {
	switch kind {
	case KindUint:
		*(*uint)(ptr) = uint(v)
	case KindUint8:
		*(*uint8)(ptr) = uint8(v)
	case KindUint16:
		*(*uint16)(ptr) = uint16(v)
	case KindUint32:
		*(*uint32)(ptr) = uint32(v)
	case KindUint64:
		*(*uint64)(ptr) = v
	}
}

// InternedFloats holds pre-boxed float64 values for 0-255 to avoid
// heap allocation when returning small integers as any (interface{}).
var InternedFloats = func() [256]any {
	var arr [256]any
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}()
