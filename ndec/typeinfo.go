package ndec

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// rtypeOf returns t's reflect rtype header for use with gort.UnsafeNew.
func rtypeOf(t reflect.Type) unsafe.Pointer {
	return typ.UniTypeOf(t).Ptr
}

// scalarKindOf maps reflect.Kind to NdecBindKind.
func scalarKindOf(k reflect.Kind) bindKind {
	switch k {
	case reflect.Bool:
		return bkBool
	case reflect.Int:
		return bkInt
	case reflect.Int8:
		return bkInt8
	case reflect.Int16:
		return bkInt16
	case reflect.Int32:
		return bkInt32
	case reflect.Int64:
		return bkInt64
	case reflect.Uint:
		return bkUint
	case reflect.Uint8:
		return bkUint8
	case reflect.Uint16:
		return bkUint16
	case reflect.Uint32:
		return bkUint32
	case reflect.Uint64:
		return bkUint64
	case reflect.Float32:
		return bkFloat32
	case reflect.Float64:
		return bkFloat64
	case reflect.String:
		return bkString
	}
	return bkInvalid
}
