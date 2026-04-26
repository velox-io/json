package venc

import (
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

type EncodeFn func(es *encodeState, ptr unsafe.Pointer) error

type EncTypeInfo struct {
	*typ.UniType // embedded shared descriptor

	TypeFlags typ.TypeFlag // cached from Hooks for fast bit-test

	Ext unsafe.Pointer // *EncStructInfo / *EncSliceInfo / ... for container kinds

	// Encode is the compile-time bound encode function for this type.
	// Set by bindEncodeFn after all container edges are wired.
	Encode EncodeFn

	HintBytes    int          // static output size estimate
	AdaptiveHint atomic.Int64 // observed max output size (updated after each encode)

	// SizeFn predicts JSON output size by scanning runtime data (lengths, nil-ness).
	SizeFn func(ptr unsafe.Pointer) int

	bp atomic.Pointer[blueprintCache] // lazily compiled blueprint
}

func (t *EncTypeInfo) ResolveStruct() *EncStructInfo {
	return (*EncStructInfo)(t.Ext)
}

func (t *EncTypeInfo) ResolveSlice() *EncSliceInfo {
	return (*EncSliceInfo)(t.Ext)
}

func (t *EncTypeInfo) ResolveArray() *EncArrayInfo {
	return (*EncArrayInfo)(t.Ext)
}

func (t *EncTypeInfo) ResolveMap() *EncMapInfo {
	return (*EncMapInfo)(t.Ext)
}

func (t *EncTypeInfo) ResolvePointer() *EncPointerInfo {
	return (*EncPointerInfo)(t.Ext)
}

func (t *EncTypeInfo) getBlueprint() *Blueprint {
	cache := t.bpCache()
	if cache == nil {
		return nil
	}
	cache.once.Do(func() {
		cache.blueprint = compileBlueprint(t)
	})
	return cache.blueprint
}

func (t *EncTypeInfo) bpCache() *blueprintCache {
	if p := t.bp.Load(); p != nil {
		return p
	}
	p := &blueprintCache{}
	if t.bp.CompareAndSwap(nil, p) {
		return p
	}
	return t.bp.Load()
}

type blueprintCache struct {
	once      sync.Once
	blueprint *Blueprint
}

type EncFieldInfo struct {
	Type *EncTypeInfo // field's type descriptor

	TagFlags typ.TagFlag // omitempty, quoted, etc.
	Offset   uintptr     // field offset in struct
	JSONName string

	KeyBytes       []byte // compact `"name":`
	KeyBytesIndent []byte // indented `"name": `
	IsZeroFn       func(ptr unsafe.Pointer) bool
}

type EncStructInfo struct {
	Fields []EncFieldInfo
}

type EncSliceInfo struct {
	ElemType *EncTypeInfo
	ElemSize uintptr
}

type EncArrayInfo struct {
	ElemType *EncTypeInfo
	ElemSize uintptr
	ArrayLen int
}

type EncPointerInfo struct {
	ElemType *EncTypeInfo
}

type EncMapInfo struct {
	ValType *EncTypeInfo
	KeyType *EncTypeInfo

	MapKind     typ.MapVariant
	MapRType    unsafe.Pointer
	IsStringKey bool
	SlotSize    uintptr // Swiss Map slot size; 0 if unknown
}
