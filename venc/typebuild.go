package venc

import (
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

var encTypeCache sync.Map

const encFastCacheSize = 32 // must be power of two

type encCacheEntry struct {
	key uintptr // rtype pointer
	val *EncTypeInfo
}

type encFastCache [encFastCacheSize]atomic.Pointer[encCacheEntry]

func encFastCacheIndex(rtp uintptr) uintptr {
	const magic = 0x9e3779b97f4a7c15 // Fibonacci: golden-ratio × 2^64
	return (rtp * magic) >> (64 - 5) // 5 = log2(32)
}

var (
	encCache    encFastCache // rtype → EncTypeInfo for the same type
	encPtrCache encFastCache // rtype(*T) → EncTypeInfo(T) for pointer-unwrap fast path
)

func EncTypeInfoOf(t reflect.Type) *EncTypeInfo {
	rtp := uintptr(gort.TypePtr(t))
	idx := encFastCacheIndex(rtp)
	if p := encCache[idx].Load(); p != nil && p.key == rtp {
		return p.val
	}
	eti := encTypeInfoViaMap(t)
	encCache[idx].Store(&encCacheEntry{key: rtp, val: eti})
	return eti
}

// encElemTypeInfoOf looks up EncTypeInfo for the element type of a pointer,
// keyed by the pointer's rtype. This avoids reflect.Type.Elem() on the hot path.
func encElemTypeInfoOf(ptrRtp uintptr, rt reflect.Type) *EncTypeInfo {
	idx := encFastCacheIndex(ptrRtp)
	if p := encPtrCache[idx].Load(); p != nil && p.key == ptrRtp {
		return p.val
	}
	return encElemTypeInfoSlow(ptrRtp, idx, rt)
}

func encElemTypeInfoSlow(ptrRtp uintptr, idx uintptr, rt reflect.Type) *EncTypeInfo {
	eti := EncTypeInfoOf(rt.Elem())
	encPtrCache[idx].Store(&encCacheEntry{key: ptrRtp, val: eti})
	return eti
}

func encTypeInfoViaMap(t reflect.Type) *EncTypeInfo {
	if v, ok := encTypeCache.Load(t); ok {
		return v.(*EncTypeInfo)
	}
	return buildEncTypeInfo(t)
}

func buildEncTypeInfo(t reflect.Type) *EncTypeInfo {
	// Recursive shells stay local until every container edge is wired.
	building := make(map[reflect.Type]*EncTypeInfo)
	eti := buildEncRec(t, building)
	// All types are now fully constructed (Ext wired). Bind encode functions.
	for _, beti := range building {
		bindEncodeFn(beti)
	}
	for bt, beti := range building {
		encTypeCache.LoadOrStore(bt, beti)
	}
	return eti
}

func buildEncRec(t reflect.Type, building map[reflect.Type]*EncTypeInfo) *EncTypeInfo {
	if v, ok := encTypeCache.Load(t); ok {
		return v.(*EncTypeInfo)
	}
	if et, ok := building[t]; ok {
		return et
	}
	ut := typ.UniTypeOf(t)
	et := newEncTypeFromUT(ut)
	building[t] = et
	fillContainerExt(et, ut, building)
	bindSizeFn(et)
	et.HintBytes = computeHintBytes(et, 0)
	return et
}

func newEncTypeFromUT(ut *typ.UniType) *EncTypeInfo {
	et := &EncTypeInfo{
		UniType: ut,
	}
	if h := ut.Hooks; h != nil {
		if h.MarshalFn != nil {
			et.TypeFlags |= typ.TypeFlagHasMarshalFn
		}
		if h.TextMarshalFn != nil {
			et.TypeFlags |= typ.TypeFlagHasTextMarshalFn
		}
	}
	return et
}

func fillContainerExt(et *EncTypeInfo, ut *typ.UniType, building map[reflect.Type]*EncTypeInfo) {
	switch info := ut.Ext.(type) {
	case *typ.StructTypeInfo:
		et.Ext = unsafe.Pointer(buildStructInfo(info, building))
	case *typ.SliceTypeInfo:
		et.Ext = unsafe.Pointer(buildSliceInfo(info, building))
	case *typ.ArrayTypeInfo:
		et.Ext = unsafe.Pointer(buildArrayInfo(info, building))
	case *typ.MapTypeInfo:
		et.Ext = unsafe.Pointer(buildMapInfo(ut.Type, info, building))
	case *typ.PointerTypeInfo:
		et.Ext = unsafe.Pointer(buildPointerInfo(info, building))
	}
}

func buildStructInfo(info *typ.StructTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncStructInfo {
	si := &EncStructInfo{}
	si.Fields = make([]EncFieldInfo, len(info.Fields))

	for i, sf := range info.Fields {
		elemET := buildEncRec(sf.FieldType.Type, building)
		fi := &si.Fields[i]
		fi.Type = elemET
		fi.Offset = sf.Offset
		fi.JSONName = sf.JSONName
		fi.KeyBytes = sf.KeyBytes
		fi.KeyBytesIndent = sf.KeyBytesIndent
		fi.IsZeroFn = sf.IsZeroFn
		fi.TagFlags = sf.TagFlags
	}

	return si
}

func buildSliceInfo(info *typ.SliceTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncSliceInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncSliceInfo{
		ElemType: elemET,
		ElemSize: info.ElemType.Size,
	}
}

func buildArrayInfo(info *typ.ArrayTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncArrayInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncArrayInfo{
		ElemType: elemET,
		ElemSize: info.ElemType.Size,
		ArrayLen: info.ArrayLen,
	}
}

func buildMapInfo(t reflect.Type, info *typ.MapTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncMapInfo {
	valET := buildEncRec(info.ValType.Type, building)
	keyET := buildEncRec(info.KeyType.Type, building)
	mi := &EncMapInfo{
		ValType:     valET,
		KeyType:     keyET,
		MapKind:     info.MapKind,
		MapRType:    gort.TypePtr(t),
		IsStringKey: info.IsStringKey,
	}
	if slotSize, ok := probeSwissMapSlotSize(t, info.ValType.Size); ok {
		mi.SlotSize = slotSize
	}
	return mi
}

func buildPointerInfo(info *typ.PointerTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncPointerInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncPointerInfo{
		ElemType: elemET,
	}
}

var _ unsafe.Pointer
