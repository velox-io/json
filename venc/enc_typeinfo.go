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

// EncTypeInfo is the per-type encode descriptor (singleton per Go type).
// It holds type-level encoding metadata and a reference to the shared UniType.
type EncTypeInfo struct {
	*typ.UniType // embedded shared descriptor

	TypeFlags typ.TypeFlag // cached from Hooks for fast bit-test

	Ext unsafe.Pointer // *EncStructInfo / *EncSliceInfo / ... for container kinds

	HintBytes    int          // static output size estimate
	AdaptiveHint atomic.Int64 // observed max output size (updated after each marshal)

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

// getBlueprint returns the lazily compiled blueprint for this type.
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

// blueprintCache holds the lazily compiled Blueprint for a type.
type blueprintCache struct {
	once      sync.Once
	blueprint *Blueprint
}

// EncFieldInfo describes one struct field for encoding.
type EncFieldInfo struct {
	Type *EncTypeInfo // field's type descriptor

	TagFlags typ.TagFlag // omitempty, quoted, etc.
	Offset   uintptr     // field offset in struct
	JSONName string

	KeyBytes       []byte // compact `"name":`
	KeyBytesIndent []byte // indented `"name": `
	IsZeroFn       func(ptr unsafe.Pointer) bool
}

// EncStructInfo describes a struct's encoding layout.
type EncStructInfo struct {
	Fields []EncFieldInfo
}

// EncSliceInfo describes a slice.
type EncSliceInfo struct {
	ElemType   *EncTypeInfo
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}

// EncArrayInfo describes a fixed-size array.
type EncArrayInfo struct {
	ElemType   *EncTypeInfo
	ElemSize   uintptr
	ArrayLen   int
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}

// EncMapInfo describes a map.
type EncMapInfo struct {
	ValType *EncTypeInfo
	KeyType *EncTypeInfo

	MapKind     typ.MapVariant
	MapRType    unsafe.Pointer
	KeyRType    unsafe.Pointer
	ValRType    unsafe.Pointer
	IsStringKey bool
	ValHasPtr   bool
	ValSize     uintptr
	KeySize     uintptr
	SlotSize    uintptr // Swiss Map slot size; 0 if unknown
}

// EncPointerInfo describes a pointer.
type EncPointerInfo struct {
	ElemType   *EncTypeInfo
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}
