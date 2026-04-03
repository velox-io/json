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

// Fast cache: direct-mapped, sits in front of encTypeCache (sync.Map).
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

// EncTypeInfoOf returns the cached encode descriptor.
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
	// Recursive struct fields may need a second pass after all shells are filled.
	for _, beti := range building {
		if beti.Kind == typ.KindStruct {
			fixupStructFields((*EncStructInfo)(beti.Ext), building)
		}
	}
	for bt, beti := range building {
		encTypeCache.LoadOrStore(bt, beti)
	}
	return eti
}

// buildEncRec resolves recursive types through the goroutine-local build map.
func buildEncRec(t reflect.Type, building map[reflect.Type]*EncTypeInfo) *EncTypeInfo {
	if v, ok := encTypeCache.Load(t); ok {
		return v.(*EncTypeInfo)
	}
	if eti, ok := building[t]; ok {
		return eti
	}
	ut := typ.UniTypeOf(t)
	eti := newEncTypeInfoFromUT(ut)
	building[t] = eti
	fillContainerExt(eti, ut, building)
	bindEncodeFn(eti)
	bindSizeFn(eti)
	eti.HintBytes = computeHintBytes(eti, 0)
	return eti
}

// newEncTypeInfoFromUT copies the shared type descriptor into encode form.
func newEncTypeInfoFromUT(ut *typ.UniType) *EncTypeInfo {
	eti := &EncTypeInfo{
		Kind: ut.Kind,
		Size: ut.Size,
		Type: ut.Type,
	}
	if h := ut.Hooks; h != nil {
		eti.MarshalFn = h.MarshalFn
		eti.TextMarshalFn = h.TextMarshalFn
		if h.MarshalFn != nil {
			eti.TypeFlags |= typ.TypeFlagHasMarshalFn
		}
		if h.TextMarshalFn != nil {
			eti.TypeFlags |= typ.TypeFlagHasTextMarshalFn
		}
	}
	return eti
}

// fillContainerExt populates container-specific encode metadata.
func fillContainerExt(eti *EncTypeInfo, ut *typ.UniType, building map[reflect.Type]*EncTypeInfo) {
	switch info := ut.Ext.(type) {
	case *typ.StructTypeInfo:
		eti.Ext = unsafe.Pointer(compileStructInfo(ut.Type, info, building))
	case *typ.SliceTypeInfo:
		eti.Ext = unsafe.Pointer(compileSliceInfo(ut.Type, info, building))
	case *typ.ArrayTypeInfo:
		eti.Ext = unsafe.Pointer(compileArrayInfo(ut.Type, info, building))
	case *typ.MapTypeInfo:
		eti.Ext = unsafe.Pointer(compileMapInfo(ut.Type, info, building))
	case *typ.PointerTypeInfo:
		eti.Ext = unsafe.Pointer(compilePointerInfo(info, building))
	}
}

// fixupStructFields patches recursive field metadata after the build pass.
func fixupStructFields(si *EncStructInfo, building map[reflect.Type]*EncTypeInfo) {
	if si == nil {
		return
	}
	for i := range si.Fields {
		fi := &si.Fields[i]
		if fi.Ext == nil && isContainerKind(fi.Kind) {
			if eti, ok := building[fi.Type]; ok {
				fi.Ext = eti.Ext
			} else if v, ok := encTypeCache.Load(fi.Type); ok {
				fi.Ext = v.(*EncTypeInfo).Ext
			}
		}
		if fi.EncodeFn == nil {
			bindEncodeFn(fi)
		}
	}
	buildStructEncodeSteps(si)
}

func isContainerKind(k typ.ElemTypeKind) bool {
	switch k {
	case typ.KindStruct, typ.KindSlice, typ.KindArray, typ.KindMap, typ.KindPointer:
		return true
	}
	return false
}

func compileStructInfo(t reflect.Type, info *typ.StructTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncStructInfo {
	si := &EncStructInfo{Type: t}
	si.Fields = make([]EncTypeInfo, len(info.Fields))

	for i, sf := range info.Fields {
		elemETI := buildEncRec(sf.FieldType.Type, building)
		fi := &si.Fields[i]
		fi.Kind = elemETI.Kind
		fi.TypeFlags = elemETI.TypeFlags
		fi.Size = elemETI.Size
		fi.Offset = sf.Offset
		fi.JSONName = sf.JSONName
		fi.Type = sf.FieldType.Type
		fi.Ext = elemETI.Ext
		fi.MarshalFn = elemETI.MarshalFn
		fi.TextMarshalFn = elemETI.TextMarshalFn
		fi.KeyBytes = sf.KeyBytes
		fi.KeyBytesIndent = sf.KeyBytesIndent
		fi.IsZeroFn = sf.IsZeroFn
		fi.TagFlags = sf.TagFlags
		fi.SizeFn = elemETI.SizeFn
		fi.HintBytes = elemETI.HintBytes
	}

	return si
}

func compileSliceInfo(t reflect.Type, info *typ.SliceTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncSliceInfo {
	elemETI := buildEncRec(info.ElemType.Type, building)
	return &EncSliceInfo{
		ElemTI:     elemETI,
		SliceType:  t,
		ElemType:   info.ElemType.Type,
		ElemSize:   info.ElemType.Size,
		ElemHasPtr: info.ElemHasPtr,
		ElemRType:  gort.TypePtr(info.ElemType.Type),
	}
}

func compileArrayInfo(t reflect.Type, info *typ.ArrayTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncArrayInfo {
	elemETI := buildEncRec(info.ElemType.Type, building)
	return &EncArrayInfo{
		ElemTI:     elemETI,
		ArrayType:  t,
		ElemType:   info.ElemType.Type,
		ElemSize:   info.ElemType.Size,
		ArrayLen:   info.ArrayLen,
		ElemHasPtr: info.ElemHasPtr,
		ElemRType:  gort.TypePtr(info.ElemType.Type),
	}
}

func compileMapInfo(t reflect.Type, info *typ.MapTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncMapInfo {
	valETI := buildEncRec(info.ValType.Type, building)
	keyETI := buildEncRec(info.KeyType.Type, building)
	mi := &EncMapInfo{
		ValTI:       valETI,
		KeyTI:       keyETI,
		MapType:     t,
		KeyType:     info.KeyType.Type,
		ValType:     info.ValType.Type,
		ValSize:     info.ValType.Size,
		KeySize:     info.KeyType.Size,
		MapKind:     info.MapKind,
		MapRType:    gort.TypePtr(t),
		KeyRType:    gort.TypePtr(info.KeyType.Type),
		ValRType:    gort.TypePtr(info.ValType.Type),
		IsStringKey: info.IsStringKey,
		ValHasPtr:   info.ValHasPtr,
	}
	// Generic string-key iteration needs the probed Swiss Map slot size.
	if slotSize, ok := probeSwissMapSlotSize(t, info.ValType.Size); ok {
		mi.SlotSize = slotSize
	}
	return mi
}

func compilePointerInfo(info *typ.PointerTypeInfo, building map[reflect.Type]*EncTypeInfo) *EncPointerInfo {
	elemETI := buildEncRec(info.ElemType.Type, building)
	return &EncPointerInfo{
		ElemTI:     elemETI,
		ElemType:   info.ElemType.Type,
		ElemSize:   info.ElemType.Size,
		ElemHasPtr: info.ElemHasPtr,
		ElemRType:  gort.TypePtr(info.ElemType.Type),
	}
}

var _ unsafe.Pointer
