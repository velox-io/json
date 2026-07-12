package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/rtcache"
	"github.com/velox-io/json/typ"
)

var (
	encTypeCache rtcache.Cache[*EncTypeInfo] // rtype to EncTypeInfo, shared with recursive builds
	encPtrCache  rtcache.Table[*EncTypeInfo] // rtype(*T) to EncTypeInfo(T) for pointer-unwrap fast path
)

func EncTypeInfoOf(t reflect.Type) *EncTypeInfo {
	rtp := uintptr(gort.TypePtr(t))
	eti, _ := encTypeCache.GetOrBuild(rtp, func() (*EncTypeInfo, error) {
		return buildEncTypeInfo(t), nil
	})
	return eti
}

// encElemTypeInfoOf looks up EncTypeInfo for the element type of a pointer,
// keyed by the pointer's rtype. This avoids reflect.Type.Elem() on the hot path.
func encElemTypeInfoOf(ptrRtp uintptr, rt reflect.Type) *EncTypeInfo {
	if v, ok := encPtrCache.Get(ptrRtp); ok {
		return v
	}
	return encElemTypeInfoSlow(ptrRtp, rt)
}

func encElemTypeInfoSlow(ptrRtp uintptr, rt reflect.Type) *EncTypeInfo {
	eti := EncTypeInfoOf(rt.Elem())
	encPtrCache.Set(ptrRtp, eti)
	return eti
}

func buildEncTypeInfo(t reflect.Type) *EncTypeInfo {
	// Recursive shells stay local until every container edge is wired. The
	// builder reads encTypeCache.Get for cross-root subtree reuse and Publishes
	// every constructed subtree at the end so future roots share it.
	building := make(map[uintptr]*EncTypeInfo)
	eti := buildEncRec(t, building)
	// All types are now fully constructed (Ext wired). Bind encode functions.
	for _, beti := range building {
		bindEncodeFn(beti)
	}
	for rtp, beti := range building {
		encTypeCache.Publish(rtp, beti)
	}
	return eti
}

func buildEncRec(t reflect.Type, building map[uintptr]*EncTypeInfo) *EncTypeInfo {
	rtp := uintptr(gort.TypePtr(t))
	if v, ok := encTypeCache.Get(rtp); ok {
		return v
	}
	if et, ok := building[rtp]; ok {
		return et
	}
	ut := typ.UniTypeOf(t)
	et := newEncTypeFromUT(ut)
	building[rtp] = et
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

func fillContainerExt(et *EncTypeInfo, ut *typ.UniType, building map[uintptr]*EncTypeInfo) {
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

func buildStructInfo(info *typ.StructTypeInfo, building map[uintptr]*EncTypeInfo) *EncStructInfo {
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

func buildSliceInfo(info *typ.SliceTypeInfo, building map[uintptr]*EncTypeInfo) *EncSliceInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncSliceInfo{
		ElemType: elemET,
		ElemSize: info.ElemType.Size,
	}
}

func buildArrayInfo(info *typ.ArrayTypeInfo, building map[uintptr]*EncTypeInfo) *EncArrayInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncArrayInfo{
		ElemType: elemET,
		ElemSize: info.ElemType.Size,
		ArrayLen: info.ArrayLen,
	}
}

func buildMapInfo(t reflect.Type, info *typ.MapTypeInfo, building map[uintptr]*EncTypeInfo) *EncMapInfo {
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

func buildPointerInfo(info *typ.PointerTypeInfo, building map[uintptr]*EncTypeInfo) *EncPointerInfo {
	elemET := buildEncRec(info.ElemType.Type, building)
	return &EncPointerInfo{
		ElemType: elemET,
	}
}

var _ unsafe.Pointer
