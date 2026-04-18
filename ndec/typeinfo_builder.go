// Type info builder registry: maps Go reflect.Types to native typeinfo.

package ndec

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// buildCtx carries shared state through the builder chain.
type buildCtx struct {
	cache    *sync.Map             // reflect.Type -> *typeInfo
	visiting map[reflect.Type]bool // recursion detection
	budget   *budgetAccumulator
}

type budgetAccumulator struct {
}

// kindBuilder is implemented by each kind.
type kindBuilder interface {
	canBuild(t reflect.Type) bool
	build(t reflect.Type, ctx *buildCtx) (*typeInfo, error)
}

// kindBuilders are ordered by priority: special traits (Marshaler, etc.)
// take precedence over concrete kinds. The order determines canBuild
// matching priority.
var kindBuilders = []kindBuilder{
	structBuilder{},
	arrayBuilder{},
	sliceBuilder{},
	mapBuilder{},
	ptrBuilder{},
	scalarBuilder{},
}

// buildAny is the top-level entry point of the builder registry.
func buildAny(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	if v, ok := ctx.cache.Load(t); ok {
		return v.(*typeInfo), nil
	}
	for _, b := range kindBuilders {
		if b.canBuild(t) {
			ti, err := b.build(t, ctx)
			if err != nil {
				return nil, err
			}
			actual, _ := ctx.cache.LoadOrStore(t, ti)
			return actual.(*typeInfo), nil
		}
	}
	return nil, fmt.Errorf("ndec: unsupported type %s", t)
}

type structBuilder struct{}

func (structBuilder) canBuild(t reflect.Type) bool { return t.Kind() == reflect.Struct }

func (structBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	body := &structBody{rt: t, rtypePtr: ti.rtypePtrData}
	ti.base.Kind = uint8(bkStruct)
	ti.base.Size = uint32(t.Size())
	ti.body = unsafe.Pointer(body)

	flats := typeFields(t)
	for _, ff := range flats {
		fi, child, err := buildFieldInfoFromFlat(ff, ctx)
		if err != nil {
			return nil, fmt.Errorf("ndec: field %s in %s: %w", ff.name, t.Name(), err)
		}
		body.fields = append(body.fields, fi)
		body.fieldNames = append(body.fieldNames, ff.name)
		body.fieldOriginalPaths = append(body.fieldOriginalPaths, ff.originalPath)
		if child != nil {
			body.children = append(body.children, child)
		}
	}

	if len(body.fields) > int(^uint16(0)) {
		return nil, fmt.Errorf("ndec: field count %d in %s exceeds uint16", len(body.fields), t.Name())
	}

	ti.base.FieldCount = uint16(len(body.fields))
	if len(body.fields) > 0 {
		ti.base.Fields = unsafe.Pointer(unsafe.SliceData(body.fields))
	}

	body.lookup = buildLookup(body.fieldNames)
	ti.base.Lookup = unsafe.Pointer(&body.lookup.abi)

	for _, c := range body.children {
		body.noscanSliceBudget += c.noscanSliceBudget()
		switch c.kind() {
		case bkStruct, bkMap:
			body.mapKVBufBudget += c.mapKVBufBudget()
		}
	}

	return ti, nil
}

type scalarBuilder struct{}

func (scalarBuilder) canBuild(t reflect.Type) bool { return scalarKindOf(t.Kind()) != bkInvalid }

func (scalarBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	k := scalarKindOf(t.Kind())
	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	ti.base.Kind = uint8(k)
	ti.base.Size = uint32(t.Size())
	return ti, nil
}

type ptrBuilder struct{}

func (ptrBuilder) canBuild(t reflect.Type) bool { return t.Kind() == reflect.Pointer }

func (ptrBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	elemT := t.Elem()
	elem, err := buildAny(elemT, ctx)
	if err != nil {
		return nil, fmt.Errorf("ndec: ptr to %s: %w", elemT.Kind(), err)
	}

	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	body := &ptrBody{rt: t, rtypePtr: ti.rtypePtrData, elem: elem}
	ti.base.Kind = uint8(bkPtr)
	ti.base.Size = uint32(t.Size())
	ti.base.ElemKind = uint8(elem.kind())
	ti.base.ElemSize = uint32(elemT.Size())
	ti.base.ElemType = unsafe.Pointer(&elem.base)
	ti.body = unsafe.Pointer(body)
	return ti, nil
}

type arrayBuilder struct{}

func (arrayBuilder) canBuild(t reflect.Type) bool { return t.Kind() == reflect.Array }

func (arrayBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	elemT := t.Elem()
	elem, err := buildAny(elemT, ctx)
	if err != nil {
		return nil, fmt.Errorf("ndec: array of %s: %w", elemT.Kind(), err)
	}

	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	ti.base.Kind = uint8(bkFixedArray)
	ti.base.Size = uint32(t.Size())
	ti.base.ElemKind = uint8(elem.kind())
	ti.base.ElemSize = uint32(elemT.Size())
	ti.base.FixedCount = uint32(t.Len())
	ti.base.ElemType = unsafe.Pointer(&elem.base)
	// Array elements are inline, no separate body needed (C reads from base)
	ti.body = unsafe.Pointer(elem) // keep elem alive via body pointer
	return ti, nil
}

type sliceBuilder struct{}

func (sliceBuilder) canBuild(t reflect.Type) bool { return t.Kind() == reflect.Slice }

func (sliceBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	elemT := t.Elem()
	elem, err := buildAny(elemT, ctx)
	if err != nil {
		return nil, fmt.Errorf("ndec: slice of %s: %w", elemT.Kind(), err)
	}

	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	body := &sliceBody{
		rt:             t,
		rtypePtr:       ti.rtypePtrData,
		elem:           elem,
		elemHasPtr:     typ.TypeContainsPointer(elemT),
		emptySliceData: unsafe.Pointer(reflect.MakeSlice(t, 0, 0).Pointer()),
	}
	ti.base.Kind = uint8(bkSlice)
	ti.base.Size = uint32(t.Size())
	ti.base.EmptySliceData = body.emptySliceData

	elemKind := elem.kind()
	ti.base.ElemKind = uint8(elemKind)
	ti.base.ElemSize = uint32(elemT.Size())
	ti.base.ElemType = unsafe.Pointer(&elem.base)
	ti.body = unsafe.Pointer(body)

	// noscanSliceBudget: only SLICE<noscan non-struct elem> contributes.
	if !body.elemHasPtr && elemT.Kind() != reflect.Struct {
		body.noscanSliceBudget = initialSliceCap * int(elemT.Size())
	}

	return ti, nil
}

type mapBuilder struct{}

func (mapBuilder) canBuild(t reflect.Type) bool { return t.Kind() == reflect.Map }

func (mapBuilder) build(t reflect.Type, ctx *buildCtx) (*typeInfo, error) {
	keyT := t.Key()
	valT := t.Elem()

	// stdlib JSON map keys accept string and all integer types
	// (encoding/json/decode.go genericDecoder.literalStore: JSON string
	// keys are strconv.ParseInt / ParseUint'd then SetInt / SetUint'd).
	// Flush selects the fast path by keyKind:
	//   string: MapAssignFastStr passes string directly
	//   intN  : parse + 8B stack buffer + MapAssign(&buf)
	keyKind := scalarKindOf(keyT.Kind())
	switch keyKind {
	case bkString,
		bkInt, bkInt8, bkInt16, bkInt32, bkInt64,
		bkUint, bkUint8, bkUint16, bkUint32, bkUint64:
	default:
		return nil, fmt.Errorf("map[%s]V key type not supported (need string / intN / uintN)", keyT.Kind())
	}

	// For pointer values (map[string]*T), elem points to the pointee, not
	// the ptr wrapper (consistent with buildFieldInfoV2 pointer field handling).
	typeToBuild := valT
	if valT.Kind() == reflect.Pointer {
		typeToBuild = valT.Elem()
	}

	valElem, err := buildAny(typeToBuild, ctx)
	if err != nil {
		return nil, fmt.Errorf("ndec: map value %s: %w", valT.Kind(), err)
	}

	valElemKind := valElem.kind()
	if valT.Kind() == reflect.Pointer {
		valElemKind = bkPtr
	}
	valSize := uint32(valT.Size())

	ti := &typeInfo{rtypePtrData: rtypeOf(t)}
	body := &mapBody{
		rt:             t,
		rtypePtr:       ti.rtypePtrData,
		elem:           valElem,
		elemHasPtr:     typ.TypeContainsPointer(valT),
		mapValueRType:  rtypeOf(valT),
		mapValueHasPtr: typ.TypeContainsPointer(valT),
		keyKind:        keyKind,
		keySize:        uint8(keyT.Size()),
	}
	body.kvSlotSize = 16 + ((valSize + 7) &^ 7)
	body.mapKVBufBudget = int(body.kvSlotSize) * mapKVBufCount

	ti.base.Kind = uint8(bkMap)
	ti.base.Size = uint32(t.Size())
	ti.base.ElemKind = uint8(valElemKind)
	ti.base.ElemSize = valSize
	ti.base.ElemType = unsafe.Pointer(&valElem.base)
	ti.body = unsafe.Pointer(body)

	return ti, nil
}

// buildFieldInfoFromFlat converts a flatField (produced by the typeFields
// algorithm) into a bindFieldInfo. The flatField's path describes the chain
// from root struct to leaf field (may contain multiple layers of non-ptr
// embedded struct offsets).
//
// Non-PTR path: accumulate all path offsets as the field offset; ABI unchanged.
// PTR path: set bffNeedsPtrChain flag; offset stores the ptr slot offset
// (full PTR path handling deferred, see typeinfo_promote.go).
func buildFieldInfoFromFlat(ff flatField, ctx *buildCtx) (bindFieldInfo, *typeInfo, error) {
	if len(ff.name) > int(^uint16(0)) {
		return bindFieldInfo{}, nil, fmt.Errorf("JSON name length exceeds uint16")
	}

	fieldType := ff.leafType

	// Pointer fields: fi.Type points to elem typeinfo (same as
	// buildFieldInfoV2, no ptrBuilder wrapper).
	if fieldType.Kind() == reflect.Pointer {
		elemT := fieldType.Elem()
		elem, err := buildAny(elemT, ctx)
		if err != nil {
			return bindFieldInfo{}, nil, fmt.Errorf("ndec: ptr to %s: %w", elemT.Kind(), err)
		}
		nameData := unsafe.StringData(ff.name)
		off := ff.accumulatedOffset()
		if off > uintptr(^uint32(0)) {
			return bindFieldInfo{}, nil, fmt.Errorf("field offset exceeds uint32")
		}
		return bindFieldInfo{
			Kind:     uint8(bkPtr),
			TagFlags: ff.tagFlags,
			NameLen:  uint16(len(ff.name)),
			Offset:   uint32(off),
			Name:     unsafe.Pointer(nameData),
			Type:     unsafe.Pointer(&elem.base),
		}, elem, nil
	}

	child, err := buildAny(fieldType, ctx)
	if err != nil {
		return bindFieldInfo{}, nil, err
	}

	kind := child.kind()
	var typePtr unsafe.Pointer
	if kind != bkBool && kind != bkInt && kind != bkInt8 && kind != bkInt16 && kind != bkInt32 && kind != bkInt64 &&
		kind != bkUint && kind != bkUint8 && kind != bkUint16 && kind != bkUint32 && kind != bkUint64 &&
		kind != bkFloat32 && kind != bkFloat64 && kind != bkString {
		typePtr = unsafe.Pointer(&child.base)
	}

	off := ff.accumulatedOffset()
	if off > uintptr(^uint32(0)) {
		return bindFieldInfo{}, nil, fmt.Errorf("field offset exceeds uint32")
	}
	nameData := unsafe.StringData(ff.name)
	return bindFieldInfo{
		Kind:     uint8(kind),
		TagFlags: ff.tagFlags,
		NameLen:  uint16(len(ff.name)),
		Offset:   uint32(off),
		Name:     unsafe.Pointer(nameData),
		Type:     typePtr,
	}, child, nil
}

var typeCache sync.Map // map[reflect.Type]*typeInfo

func bindTypeInfoOf(t reflect.Type) (*typeInfo, error) {
	if v, ok := typeCache.Load(t); ok {
		return v.(*typeInfo), nil
	}
	ctx := &buildCtx{
		cache:    &typeCache,
		visiting: make(map[reflect.Type]bool),
		budget:   &budgetAccumulator{},
	}
	ti, err := buildAny(t, ctx)
	if err != nil {
		return nil, err
	}
	return ti, nil
}
