package venc

import (
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/typ"
)

const (
	EncTypeFlagHasMarshalFn     = typ.TypeFlagHasMarshalFn
	EncTypeFlagHasTextMarshalFn = typ.TypeFlagHasTextMarshalFn
	EncTagFlagQuoted            = typ.TagFlagQuoted
	EncTagFlagOmitEmpty         = typ.TagFlagOmitEmpty
)

// EncTypeInfo is the cached encode descriptor for a Go type.
type EncTypeInfo struct {
	UT *typ.UniType // shared type descriptor (nil for struct field copies)

	Kind      typ.ElemTypeKind
	TypeFlags typ.TypeFlag
	TagFlags  typ.TagFlag // only set on struct fields
	_         [1]byte
	Offset    uintptr

	Ext unsafe.Pointer // *EncStructInfo / *EncSliceInfo / ... for container kinds

	JSONName string

	KeyBytes       []byte       // compact `"name":`
	KeyBytesIndent []byte       // indented `"name": `
	HintBytes      int          // static output size hint
	AdaptiveHint   atomic.Int64 // observed max output size (updated after each marshal)
	IsZeroFn       func(ptr unsafe.Pointer) bool

	// SizeFn predicts JSON output size by scanning runtime data (lengths, nil-ness).
	// Returns 0 if prediction is unavailable (interface{}, custom marshal, etc.).
	// Compiled once per type at registration time.
	SizeFn func(ptr unsafe.Pointer) int
}

func (d *EncTypeInfo) ResolveStruct() *EncStructInfo {
	return (*EncStructInfo)(d.Ext)
}

func (d *EncTypeInfo) ResolveSlice() *EncSliceInfo {
	return (*EncSliceInfo)(d.Ext)
}

func (d *EncTypeInfo) ResolveArray() *EncArrayInfo {
	return (*EncArrayInfo)(d.Ext)
}

func (d *EncTypeInfo) ResolveMap() *EncMapInfo {
	return (*EncMapInfo)(d.Ext)
}

func (d *EncTypeInfo) ResolvePointer() *EncPointerInfo {
	return (*EncPointerInfo)(d.Ext)
}

// EncStructInfo describes a struct.
type EncStructInfo struct {
	Type   reflect.Type
	Fields []EncTypeInfo

	vm atomic.Pointer[encvmCache]
}

// vmCache lazily allocates the per-type VM cache.
func (si *EncStructInfo) vmCache() *encvmCache {
	if p := si.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if si.vm.CompareAndSwap(nil, p) {
		return p
	}
	return si.vm.Load()
}

// encvmCache holds lazily compiled VM state.
type encvmCache struct {
	once      sync.Once
	blueprint *Blueprint
}

// EncSliceInfo describes a slice.
type EncSliceInfo struct {
	ElemTI     *EncTypeInfo
	SliceType  reflect.Type
	ElemType   reflect.Type
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer

	vm atomic.Pointer[encvmCache]
}

func (si *EncSliceInfo) vmCache() *encvmCache {
	if p := si.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if si.vm.CompareAndSwap(nil, p) {
		return p
	}
	return si.vm.Load()
}

// EncArrayInfo describes a fixed-size array.
type EncArrayInfo struct {
	ElemTI     *EncTypeInfo
	ArrayType  reflect.Type
	ElemType   reflect.Type
	ElemSize   uintptr
	ArrayLen   int
	ElemHasPtr bool
	ElemRType  unsafe.Pointer

	vm atomic.Pointer[encvmCache]
}

func (ai *EncArrayInfo) vmCache() *encvmCache {
	if p := ai.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if ai.vm.CompareAndSwap(nil, p) {
		return p
	}
	return ai.vm.Load()
}

// EncMapInfo describes a map.
type EncMapInfo struct {
	ValTI   *EncTypeInfo
	KeyTI   *EncTypeInfo
	MapType reflect.Type
	KeyType reflect.Type
	ValType reflect.Type
	ValSize uintptr
	KeySize uintptr

	MapKind     typ.MapVariant
	MapRType    unsafe.Pointer
	KeyRType    unsafe.Pointer
	ValRType    unsafe.Pointer
	IsStringKey bool
	ValHasPtr   bool
	SlotSize    uintptr // Swiss Map slot size; 0 if unknown

	vm atomic.Pointer[encvmCache]
}

func (mi *EncMapInfo) vmCache() *encvmCache {
	if p := mi.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if mi.vm.CompareAndSwap(nil, p) {
		return p
	}
	return mi.vm.Load()
}

// EncPointerInfo describes a pointer.
type EncPointerInfo struct {
	ElemTI     *EncTypeInfo
	ElemType   reflect.Type
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}
