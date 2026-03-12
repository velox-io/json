package vjson

import (
	"math"
	"unsafe"
)

// intFitsKind checks whether v fits in the target signed integer kind.
func intFitsKind(v int64, kind ElemTypeKind) bool {
	switch kind {
	case KindInt8:
		return v >= math.MinInt8 && v <= math.MaxInt8
	case KindInt16:
		return v >= math.MinInt16 && v <= math.MaxInt16
	case KindInt32:
		return v >= math.MinInt32 && v <= math.MaxInt32
	default: // kindInt, KindInt64
		return true
	}
}

// uintFitsKind checks whether v fits in the target unsigned integer kind.
func uintFitsKind(v uint64, kind ElemTypeKind) bool {
	switch kind {
	case KindUint8:
		return v <= math.MaxUint8
	case KindUint16:
		return v <= math.MaxUint16
	case KindUint32:
		return v <= math.MaxUint32
	default: // kindUint, KindUint64
		return true
	}
}

// writeIntValue writes a signed integer to ptr per target kind.
func writeIntValue(ptr unsafe.Pointer, kind ElemTypeKind, v int64) {
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

// writeUintValue writes an unsigned integer to ptr per target kind.
func writeUintValue(ptr unsafe.Pointer, kind ElemTypeKind, v uint64) {
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

// internedFloats is an array of pre-boxed float64 values (0-255) to reduce
// allocations for common small numbers in the any-path.
var internedFloats = func() [256]any {
	var arr [256]any
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}()
