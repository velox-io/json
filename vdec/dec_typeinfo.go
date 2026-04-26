package vdec

import (
	"reflect"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// DecTypeFlagSpecial marks types that bypass the hot scanValue path.
const DecTypeFlagSpecial = typ.TypeFlagHasUnmarshalFn | typ.TypeFlagHasTextUnmarshalFn | typ.TypeFlagRawMessage | typ.TypeFlagNumber

// DecTagFlagSpecial marks field tags handled before scanValue.
const DecTagFlagSpecial = typ.TagFlagQuoted | typ.TagFlagCopyString

type DecTypeInfo struct {
	Kind      typ.ElemTypeKind
	TypeFlags typ.TypeFlag
	_         [2]byte
	Size      uintptr
	TypePtr   unsafe.Pointer // rtype for runtime map/slice helpers

	Ext unsafe.Pointer // *DecStructInfo / *DecSliceInfo / ... for container kinds

	Type reflect.Type // for error messages

	// Custom hooks; nil for most types.
	UnmarshalFn     func(ptr unsafe.Pointer, data []byte) error
	TextUnmarshalFn func(ptr unsafe.Pointer, data []byte) error
}

func (d *DecTypeInfo) ResolveStruct() *DecStructInfo {
	return (*DecStructInfo)(d.Ext)
}

func (d *DecTypeInfo) ResolveSlice() *DecSliceInfo {
	return (*DecSliceInfo)(d.Ext)
}

func (d *DecTypeInfo) ResolveArray() *DecArrayInfo {
	return (*DecArrayInfo)(d.Ext)
}

func (d *DecTypeInfo) ResolveMap() *DecMapInfo {
	return (*DecMapInfo)(d.Ext)
}

func (d *DecTypeInfo) ResolvePointer() *DecPointerInfo {
	return (*DecPointerInfo)(d.Ext)
}

type DecFieldInfo struct {
	Kind     typ.ElemTypeKind // copied for hot-path dispatch
	TagFlags typ.TagFlag
	Offset   uintptr
	JSONName string
	TypeInfo *DecTypeInfo
}

type DecStructInfo struct {
	Fields []DecFieldInfo

	Lookup       fieldLookup
	HasMixedCase bool
}

func (si *DecStructInfo) LookupFieldBytes(key []byte) *DecFieldInfo {
	k := unsafe.String(unsafe.SliceData(key), len(key))

	fi := si.Lookup.lookup(si, k)
	if fi != nil {
		return fi
	}
	if !si.HasMixedCase && !hasUpperASCII(key) {
		return nil
	}
	for i := range si.Fields {
		if equalFoldASCII(si.Fields[i].JSONName, k) {
			return &si.Fields[i]
		}
	}
	return nil
}

type DecSliceInfo struct {
	ElemTI         *DecTypeInfo
	ElemSize       uintptr
	ElemHasPtr     bool
	ElemRType      unsafe.Pointer
	EmptySliceData unsafe.Pointer

	CapHint  atomic.Int32
	EmaAlpha int32 // EMA denominator; default 2
}

type ArraySpecialScanner func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error)

type DecArrayInfo struct {
	ElemTI      *DecTypeInfo
	ElemSize    uintptr
	ArrayLen    int
	ElemHasPtr  bool
	ElemRType   unsafe.Pointer
	ScanArrayFn ArraySpecialScanner
}

type DecMapInfo struct {
	ValTI   *DecTypeInfo
	KeyTI   *DecTypeInfo
	ValSize uintptr
	KeySize uintptr

	KeyType reflect.Type
	ValType reflect.Type

	KeyRType    unsafe.Pointer
	ValRType    unsafe.Pointer
	IsStringKey bool
	ValHasPtr   bool
	SlotSize    uintptr

	ScanMapFn func(sc *Parser, src []byte, idx int, ptr unsafe.Pointer) (int, error)
}

type DecPointerInfo struct {
	ElemTI     *DecTypeInfo
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}
