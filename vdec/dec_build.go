package vdec

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

var decTypeCache sync.Map

// DecTypeInfoOf returns the cached decode descriptor.
func DecTypeInfoOf(t reflect.Type) *DecTypeInfo {
	if v, ok := decTypeCache.Load(t); ok {
		return v.(*DecTypeInfo)
	}
	return buildDecTypeInfo(t)
}

func buildDecTypeInfo(t reflect.Type) *DecTypeInfo {
	ut := typ.UniTypeOf(t)
	dti := newDecTypeInfoFromUT(ut)

	if ut.Kind == typ.KindStruct {
		// Struct shells stay goroutine-local until recursive fields are wired.
		building := map[reflect.Type]*DecTypeInfo{t: dti}
		fillStructExt(dti, ut.Ext.(*typ.StructTypeInfo), building)
	} else {
		fillContainerExt(dti, ut)
	}
	if actual, loaded := decTypeCache.LoadOrStore(t, dti); loaded {
		return actual.(*DecTypeInfo)
	}
	return dti
}

func fillStructExt(dti *DecTypeInfo, info *typ.StructTypeInfo, building map[reflect.Type]*DecTypeInfo) {
	dti.Ext = unsafe.Pointer(compileStructInfo(info, building))
}

func newDecTypeInfoFromUT(ut *typ.UniType) *DecTypeInfo {
	dti := &DecTypeInfo{
		Kind:    ut.Kind,
		Size:    ut.Size,
		Type:    ut.Type,
		TypePtr: gort.TypePtr(ut.Type),
	}
	if h := ut.Hooks; h != nil {
		dti.UnmarshalFn = h.UnmarshalFn
		dti.TextUnmarshalFn = h.TextUnmarshalFn
		if h.UnmarshalFn != nil {
			dti.TypeFlags |= typ.TypeFlagHasUnmarshalFn
		}
		if h.TextUnmarshalFn != nil {
			dti.TypeFlags |= typ.TypeFlagHasTextUnmarshalFn
		}
	}
	if ut.Kind == typ.KindRawMessage {
		dti.TypeFlags |= typ.TypeFlagRawMessage
	}
	if ut.Kind == typ.KindNumber {
		dti.TypeFlags |= typ.TypeFlagNumber
	}
	return dti
}

func fillContainerExt(dti *DecTypeInfo, ut *typ.UniType) {
	switch info := ut.Ext.(type) {
	case *typ.SliceTypeInfo:
		dti.Ext = unsafe.Pointer(compileSliceInfo(info))
	case *typ.ArrayTypeInfo:
		dti.Ext = unsafe.Pointer(compileArrayInfo(info))
	case *typ.MapTypeInfo:
		dti.Ext = unsafe.Pointer(compileMapInfo(info))
	case *typ.PointerTypeInfo:
		dti.Ext = unsafe.Pointer(compilePointerInfo(info))
	}
}

func compileStructInfo(info *typ.StructTypeInfo, building map[reflect.Type]*DecTypeInfo) *DecStructInfo {
	si := &DecStructInfo{}
	si.Fields = make([]DecFieldInfo, len(info.Fields))

	for i, sf := range info.Fields {
		elemDTI := resolveFieldType(sf.FieldType, building)
		fi := &si.Fields[i]
		fi.Kind = elemDTI.Kind
		fi.TagFlags = sf.TagFlags
		fi.Offset = sf.Offset
		fi.JSONName = sf.JSONName
		fi.TypeInfo = elemDTI
	}
	buildDecLookup(si)
	return si
}

func resolveFieldType(fieldUT *typ.UniType, building map[reflect.Type]*DecTypeInfo) *DecTypeInfo {
	t := fieldUT.Type
	if v, ok := decTypeCache.Load(t); ok {
		return v.(*DecTypeInfo)
	}
	if fieldUT.Kind == typ.KindStruct {
		if dti, ok := building[t]; ok {
			return dti
		}
		dti := newDecTypeInfoFromUT(fieldUT)
		building[t] = dti
		fillStructExt(dti, fieldUT.Ext.(*typ.StructTypeInfo), building)
		return dti
	}
	dti := newDecTypeInfoFromUT(fieldUT)
	fillContainerExtRec(dti, fieldUT, building)
	return dti
}

func fillContainerExtRec(dti *DecTypeInfo, ut *typ.UniType, building map[reflect.Type]*DecTypeInfo) {
	switch info := ut.Ext.(type) {
	case *typ.SliceTypeInfo:
		elemDTI := resolveFieldType(info.ElemType, building)
		dti.Ext = unsafe.Pointer(&DecSliceInfo{
			ElemTI:         elemDTI,
			ElemSize:       info.ElemType.Size,
			ElemHasPtr:     info.ElemHasPtr,
			ElemRType:      gort.TypePtr(info.ElemType.Type),
			EmptySliceData: info.EmptySliceData,
			EmaAlpha:       2,
		})
	case *typ.ArrayTypeInfo:
		elemDTI := resolveFieldType(info.ElemType, building)
		ai := &DecArrayInfo{
			ElemTI:     elemDTI,
			ElemSize:   info.ElemType.Size,
			ArrayLen:   info.ArrayLen,
			ElemHasPtr: info.ElemHasPtr,
			ElemRType:  gort.TypePtr(info.ElemType.Type),
		}
		bindScanArrayFn(ai, elemDTI)
		dti.Ext = unsafe.Pointer(ai)
	case *typ.MapTypeInfo:
		valDTI := resolveFieldType(info.ValType, building)
		keyDTI := resolveFieldType(info.KeyType, building)
		dti.Ext = unsafe.Pointer(buildDecMapInfo(info, valDTI, keyDTI))
	case *typ.PointerTypeInfo:
		elemDTI := resolveFieldType(info.ElemType, building)
		dti.Ext = unsafe.Pointer(&DecPointerInfo{
			ElemTI:     elemDTI,
			ElemSize:   info.ElemType.Size,
			ElemHasPtr: info.ElemHasPtr,
			ElemRType:  gort.TypePtr(info.ElemType.Type),
		})
	}
}

func compileSliceInfo(info *typ.SliceTypeInfo) *DecSliceInfo {
	elemDTI := buildDecTypeInfo(info.ElemType.Type)
	return &DecSliceInfo{
		ElemTI:         elemDTI,
		ElemSize:       info.ElemType.Size,
		ElemHasPtr:     info.ElemHasPtr,
		ElemRType:      gort.TypePtr(info.ElemType.Type),
		EmptySliceData: info.EmptySliceData,
		EmaAlpha:       2,
	}
}

func compileArrayInfo(info *typ.ArrayTypeInfo) *DecArrayInfo {
	elemDTI := buildDecTypeInfo(info.ElemType.Type)
	ai := &DecArrayInfo{
		ElemTI:     elemDTI,
		ElemSize:   info.ElemType.Size,
		ArrayLen:   info.ArrayLen,
		ElemHasPtr: info.ElemHasPtr,
		ElemRType:  gort.TypePtr(info.ElemType.Type),
	}
	bindScanArrayFn(ai, elemDTI)
	return ai
}

func compileMapInfo(info *typ.MapTypeInfo) *DecMapInfo {
	valDTI := buildDecTypeInfo(info.ValType.Type)
	keyDTI := buildDecTypeInfo(info.KeyType.Type)
	return buildDecMapInfo(info, valDTI, keyDTI)
}

func buildDecMapInfo(info *typ.MapTypeInfo, valDTI, keyDTI *DecTypeInfo) *DecMapInfo {
	mi := &DecMapInfo{
		ValTI:       valDTI,
		KeyTI:       keyDTI,
		ValSize:     info.ValType.Size,
		KeySize:     info.KeyType.Size,
		KeyType:     info.KeyType.Type,
		ValType:     info.ValType.Type,
		KeyRType:    gort.TypePtr(info.KeyType.Type),
		ValRType:    gort.TypePtr(info.ValType.Type),
		IsStringKey: info.IsStringKey,
		ValHasPtr:   info.ValHasPtr,
		SlotSize:    info.SlotSize,
	}
	if info.IsStringKey {
		switch valDTI.Kind {
		case typ.KindString:
			mi.ScanMapFn = (*Parser).scanMapStringString
		case typ.KindInt:
			mi.ScanMapFn = (*Parser).scanMapStringInt
		case typ.KindInt64:
			mi.ScanMapFn = (*Parser).scanMapStringInt64
		}
	}
	return mi
}

func compilePointerInfo(info *typ.PointerTypeInfo) *DecPointerInfo {
	elemDTI := buildDecTypeInfo(info.ElemType.Type)
	return &DecPointerInfo{
		ElemTI:     elemDTI,
		ElemSize:   info.ElemType.Size,
		ElemHasPtr: info.ElemHasPtr,
		ElemRType:  gort.TypePtr(info.ElemType.Type),
	}
}

func bindScanArrayFn(ai *DecArrayInfo, elemDTI *DecTypeInfo) {
	switch elemDTI.Kind {
	case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64:
		elemKind := elemDTI.Kind
		elemType := elemDTI.Type
		ai.ScanArrayFn = func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error) {
			return scanArrayInt(src, idx, arrayLen, elemSize, elemKind, elemType, ptr)
		}
	case typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
		elemKind := elemDTI.Kind
		elemType := elemDTI.Type
		ai.ScanArrayFn = func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error) {
			return scanArrayUint(src, idx, arrayLen, elemSize, elemKind, elemType, ptr)
		}
	case typ.KindFloat64:
		ai.ScanArrayFn = scanArrayFloat64
	}
}
