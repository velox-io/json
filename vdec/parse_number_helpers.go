package vdec

import (
	"math"
	"unsafe"

	"github.com/velox-io/json/typ"
)

func intFitsKind(v int64, kind typ.ElemTypeKind) bool {
	switch kind {
	case typ.KindInt8:
		return v >= math.MinInt8 && v <= math.MaxInt8
	case typ.KindInt16:
		return v >= math.MinInt16 && v <= math.MaxInt16
	case typ.KindInt32:
		return v >= math.MinInt32 && v <= math.MaxInt32
	default:
		return true
	}
}

func uintFitsKind(v uint64, kind typ.ElemTypeKind) bool {
	switch kind {
	case typ.KindUint8:
		return v <= math.MaxUint8
	case typ.KindUint16:
		return v <= math.MaxUint16
	case typ.KindUint32:
		return v <= math.MaxUint32
	default:
		return true
	}
}

func writeIntValue(ptr unsafe.Pointer, kind typ.ElemTypeKind, v int64) {
	switch kind {
	case typ.KindInt:
		*(*int)(ptr) = int(v)
	case typ.KindInt8:
		*(*int8)(ptr) = int8(v)
	case typ.KindInt16:
		*(*int16)(ptr) = int16(v)
	case typ.KindInt32:
		*(*int32)(ptr) = int32(v)
	case typ.KindInt64:
		*(*int64)(ptr) = v
	}
}

func writeUintValue(ptr unsafe.Pointer, kind typ.ElemTypeKind, v uint64) {
	switch kind {
	case typ.KindUint:
		*(*uint)(ptr) = uint(v)
	case typ.KindUint8:
		*(*uint8)(ptr) = uint8(v)
	case typ.KindUint16:
		*(*uint16)(ptr) = uint16(v)
	case typ.KindUint32:
		*(*uint32)(ptr) = uint32(v)
	case typ.KindUint64:
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
