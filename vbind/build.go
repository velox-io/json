// Construction uses indexes while tables may grow, then resolves ABI pointers
// only after every backing slice is frozen.

package vbind

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// Build constructs a TypeTree from a UniType graph when a caller needs an
// uncached shape table. Most users should call TypeTreeOf or TypeTreeFor so
// equal reflect.Type roots share one TypeTree for the whole process.
func Build(root *typ.UniType) (*TypeTree, error) {
	b := &builder{
		seen:         make(map[*typ.UniType]uint32),
		bySlot:       make(map[*typ.UniType]uint32),
		bySliceSlot:  make(map[*typ.UniType]uint32),
		byStreamSlot: make(map[*typ.UniType]uint32),
		byPrimSlot:   make(map[reflect.Type]uint32),
	}
	rootIdx, err := b.collect(root)
	if err != nil {
		return nil, err
	}

	// SCC classification requires the complete backing dependency graph.
	b.markSCCGroups()

	if err := b.attachFieldLookups(); err != nil {
		return nil, err
	}

	b.attachMapDrainInfos()

	mapBufMinBytes := b.sizeMapBuffer()
	if len(b.fields) == 0 {
		b.fields = []BindField{{}}
		b.fieldNames = []string{""}
	}

	b.reapplyFieldFlags()

	tapeBindUnsupported := b.computeTapeBindUnsupported(rootIdx)
	// Reads child and BindField.Type as in-tree indexes, so it must run before
	// resolveChildPointers rewrites both into ABI pointers.
	splitTapeSites := b.countSplitTapeSites(rootIdx)
	tapeBindMayAppendStrings := b.tapeBindMayAppendStrings()

	if err := b.resolveChildPointers(); err != nil {
		return nil, err
	}

	if len(b.types) > 65536 {
		return nil, fmt.Errorf("vbind: type table size %d exceeds uint16 TypeIdx capacity", len(b.types))
	}
	return &TypeTree{
		Types:               b.types,
		Fields:              b.fields,
		FieldNames:          b.fieldNames,
		TypeMeta:            b.typeMeta,
		Slots:               b.slots,
		Root:                rootIdx,
		MapDrainInfo:        b.mapDrainInfo,
		AnyMetas:            b.anyMetas,
		AnyTypeIdx:          b.anyTypeIdx,
		GroupCount:          b.groupCount,
		UnmarshalHooks:      b.unmarshalHooks,
		MapBufMinBytes:      mapBufMinBytes,
		ReflectTypes:        b.reflectTypes,
		Variants:            b.variants,
		VariantCases:        b.variantCases,
		Kindofs:             b.kindofs,
		KindofCases:         b.kindofCases,
		PtrHops:             b.ptrHops,
		TapeBindUnsupported: tapeBindUnsupported,

		HasValueField: b.hasValueField,
		// A variant or kindof table exists exactly when some field binds through a
		// merged tape, so the tables answer this directly.
		HasPolyField:             len(b.variants) > 0 || len(b.kindofs) > 0,
		HasSplitTape:             b.hasSplitTape,
		TapeBindMayAppendStrings: tapeBindMayAppendStrings,
		SplitTapeSites:           splitTapeSites,
	}, nil
}

type builder struct {
	types               []BindType
	typeMeta            []TypeMeta
	reflectTypes        []reflect.Type
	fields              []BindField
	fieldNames          []string // parallel to fields for tape walking
	slots               []SlotTemplate
	slotRecs            []slotRec // parallel to slots for backing dependency analysis
	seen                map[*typ.UniType]uint32
	bySlot              map[*typ.UniType]uint32 // pointer pointee and map header slots
	bySliceSlot         map[*typ.UniType]uint32 // slice backing slots remain independent from pointee slots
	byStreamSlot        map[*typ.UniType]uint32 // Stream[T] slice backings get a distinct SlotClass from []T: stream batch sizing and EWMA must not cross-contaminate with regular slice growth
	byPrimSlot          map[reflect.Type]uint32
	structSites         []structSite
	mapSites            []mapSite
	mapDrainInfo        []MapDrainInfo
	anyMetas            []BindAnyMeta
	anyTypeIdx          uint32
	groupCount          uint32
	unmarshalHooks      []*typ.InterfaceHooks // parallel to types
	containsUnmarshaler []bool                // transitive and parallel to types
	variants            []BindVariantTable
	variantCases        []VariantCaseData // scannable owners of variant case arrays
	kindofs             []BindKindofTable
	kindofCases         []KindofCaseData // scannable owners of kindof case arrays
	ptrHops             [][]BindPtrHop   // scannable owners of per-struct hop arrays

	// Set during collect, published as the TypeTree fields of the same names.
	// hasPolyField is not here: it follows from the variants/kindofs tables.
	hasValueField bool
	hasSplitTape  bool
}

var (
	anyStaticTrue  uint64 = 1
	anyStaticFalse uint64
)

type structSite struct {
	idx uint32
	si  *typ.StructTypeInfo
}

type mapSite struct {
	idx          uint32
	kvStride     uint32
	valIdx       uint32
	keyIdx       uint32
	stride       uint32 // entry_slot stride (16 + sizeof(V) padded to 8)
	valSlotClass int32  // SlotClass for deferred val intermediate; -1 = N/A
	mapRType     unsafe.Pointer
}

// collect must store cross table references as indexes because every target
// slice can still reallocate. resolveChildPointers converts them only after
// collection and all attach passes have frozen the backing arrays.
func (b *builder) collect(ut *typ.UniType) (uint32, error) {
	if idx, ok := b.seen[ut]; ok {
		return idx, nil
	}
	idx := uint32(len(b.types))
	b.seen[ut] = idx
	b.types = append(b.types, BindType{})
	b.typeMeta = append(b.typeMeta, TypeMeta{})
	b.reflectTypes = append(b.reflectTypes, ut.Type)
	b.unmarshalHooks = append(b.unmarshalHooks, nil)
	b.containsUnmarshaler = append(b.containsUnmarshaler, false)

	var info BindType
	var meta TypeMeta
	meta.Size = uint32(ut.Size)
	// typ.ElemTypeKind and vbind.Kind share the same numeric range.
	info.Kind = Kind(ut.Kind)

	info.TypeIdx = uint16(idx)

	if ut.Hooks != nil && ut.Hooks.UnmarshalFn != nil {
		info.Kind = KindUnmarshaler
		info.flags = bindFlagCold
		b.unmarshalHooks[idx] = ut.Hooks
		b.containsUnmarshaler[idx] = true
		b.types[idx] = info
		b.typeMeta[idx] = meta
		return idx, nil
	}
	if ut.Hooks != nil && ut.Hooks.TextUnmarshalFn != nil {
		info.Kind = KindTextUnmarshaler
		info.flags = bindFlagCold
		b.unmarshalHooks[idx] = ut.Hooks
		b.containsUnmarshaler[idx] = true
		b.types[idx] = info
		b.typeMeta[idx] = meta
		return idx, nil
	}

	switch ut.Kind {
	case typ.KindBool,
		typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
		typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
		typ.KindFloat32, typ.KindFloat64,
		typ.KindString,
		typ.KindNumber:
	case typ.KindRawMessage:
		b.containsUnmarshaler[idx] = true
	case typ.KindValue:
		// Value contains heap pointers, so an enclosing map value must use scannable
		// intermediate storage rather than the noscan staging buffer.
		b.containsUnmarshaler[idx] = true
		// Reached only for types in the tree, so this settles TypeTree.HasValueField
		// without a second pass over Types.
		b.hasValueField = true
		// value.Value is reflect.Struct but collected as KindValue, so the struct
		// payload is never initialized by the KindStruct branch. Stamp the
		// inline-variant sentinel here: without it, buildOneVariantTable reads a
		// zero InlineVariantIdx and rejects an inline value.Value case as "hosts an
		// inline variant". Cold-kind case rejection is handled by checkVariantCaseTypes.
		meta.StructMeta().InlineVariantIdx = 0xFFFF
	case typ.KindPointer:
		pi, _ := ut.Ext.(*typ.PointerTypeInfo)
		if pi == nil || pi.ElemType == nil {
			return 0, fmt.Errorf("vbind: pointer ext missing")
		}
		childIdx, err := b.collect(pi.ElemType)
		if err != nil {
			return 0, err
		}
		p := info.Pointer()
		p.AllocClass = int32(b.registerSlotClass(pi.ElemType))
		info.child = uintptr(childIdx)
		meta.PointerMeta().PtrChildSize = uint32(pi.ElemType.Size)
		if b.containsUnmarshaler[childIdx] {
			b.containsUnmarshaler[idx] = true
		}
	case typ.KindStruct:
		si, _ := ut.Ext.(*typ.StructTypeInfo)
		if si == nil {
			return 0, fmt.Errorf("vbind: struct ext missing")
		}
		// The typ layer collects fields without an error channel, so a shape it
		// could not represent arrives as a recorded reject. Fail here rather than
		// bind against offsets known to be wrong.
		if len(si.Rejects) > 0 {
			return 0, fmt.Errorf("vbind: %s", si.Rejects[0])
		}
		// Zero is a valid variant index. Initialize both copies so later copy back
		// cannot turn the 0xFFFF none sentinel into zero. ReserveUnknownFieldOff uses
		// 0xFFFFFFFF for the same reason: zero is a valid field offset.
		meta.StructMeta().InlineVariantIdx = 0xFFFF
		meta.StructMeta().ReserveUnknownFieldOff = 0xFFFFFFFF
		b.typeMeta[idx].StructMeta().InlineVariantIdx = 0xFFFF
		b.typeMeta[idx].StructMeta().ReserveUnknownFieldOff = 0xFFFFFFFF
		// Children must be collected first so this struct's fields remain contiguous.
		fieldTypeIdxs := make([]uint32, len(si.Fields))
		for i := range si.Fields {
			fIdx, err := b.collect(si.Fields[i].FieldType)
			if err != nil {
				return 0, err
			}
			fieldTypeIdxs[i] = fIdx
		}
		fieldsBase := uint32(len(b.fields))
		// Hops for every field promoted across an embedded pointer in this struct,
		// concatenated. A field records only where its run starts; the run ends at
		// the hop carrying the Last bit.
		var hops []BindPtrHop
		for i := range si.Fields {
			f := &si.Fields[i]
			flags := hotFieldFlagsFrom(f.TagFlags) | (uint32(b.types[fieldTypeIdxs[i]].flags) & ^uint32(bindFlagMayPhase2))
			if len(f.PtrPath) > 0 {
				hopStart := len(hops)
				if hopStart > 0xFFFF {
					return 0, fmt.Errorf("vbind: struct %s needs more than %d embedded-pointer hops", ut.Type, 0xFFFF)
				}
				for h := range f.PtrPath {
					hop := &f.PtrPath[h]
					// Each pointee gets a SlotClass so native can allocate one when
					// the pointer is nil, the same way a plain *T field does.
					cls := int32(b.registerSlotClass(hop.PointeeType))
					if h == len(f.PtrPath)-1 {
						cls |= ptrHopLastBit
					}
					hops = append(hops, BindPtrHop{
						SlotOffset: uint32(hop.SlotOffset),
						AllocClass: cls,
					})
				}
				flags |= uint32(TagViaPtr) | (uint32(hopStart) << fieldFlagVariantIdxShift)
			}
			// Type remains an index until freeze. fieldNames is the scannable Go side
			// companion used by tape walking and is not part of BindField's ABI.
			b.fields = append(b.fields, BindField{
				Type: uintptr(fieldTypeIdxs[i]),
				// For a via-ptr field this is relative to the last hop's pointee,
				// not to the struct base.
				Offset: uint32(f.Offset),
				// Native value dispatch tests this word without loading TypeMeta. Do not
				// inherit bindFlagMayPhase2 because it is a struct close path marker.
				Flags: flags,
			})
			b.fieldNames = append(b.fieldNames, f.JSONName)
		}
		if len(hops) > 0 {
			// PtrHops owns the array; the payload keeps only a raw base so it stays
			// noscan. Both meta copies are stamped for the same reason the variant
			// index is: the final whole-record assignment must not drop it.
			b.ptrHops = append(b.ptrHops, hops)
			base := unsafe.Pointer(unsafe.SliceData(hops))
			meta.StructMeta().PtrHops = base
			b.typeMeta[idx].StructMeta().PtrHops = base
		}
		// Inline variants need the host index both in their table and in host metadata.
		if err := b.attachVariantsForStruct(ut, idx, si, fieldsBase); err != nil {
			return 0, err
		}
		// attachVariantsForStruct updates b.typeMeta directly. Copy its value into
		// local meta before the final whole record assignment can overwrite it.
		if ivIdx := b.typeMeta[idx].StructMeta().InlineVariantIdx; ivIdx != 0xFFFF {
			meta.StructMeta().InlineVariantIdx = ivIdx
		}
		if err := b.attachKindofsForStruct(ut, si, fieldsBase); err != nil {
			return 0, err
		}
		// Validate and stamp the reserve-unknown (`json:",embed"`) field. At most one per
		// struct; must be KindValue. Coexistence with an inline variant is fine: the
		// merged-tape pass at struct close classifies every taped entry against the
		// host's field table and the selected case's, so case content and genuinely
		// unknown keys separate even when interleaved.
		reserveUnknownIdx := -1
		for i := range si.Fields {
			if b.fields[fieldsBase+uint32(i)].Flags&uint32(TagReserveUnknown) != 0 {
				if reserveUnknownIdx >= 0 {
					return 0, fmt.Errorf("vbind: struct %s has multiple json:\"+\" reserve-unknown fields; only one is supported", ut.Type)
				}
				reserveUnknownIdx = i
			}
		}
		if reserveUnknownIdx >= 0 {
			reserveUnknownFieldTypeIdx := fieldTypeIdxs[reserveUnknownIdx]
			if b.types[reserveUnknownFieldTypeIdx].Kind != KindValue {
				return 0, fmt.Errorf("vbind: struct %s json:\"+\" reserve-unknown field %s must be value.Value (got kind %d / type %s); the typ layer should have dropped the flag for non-Value kinds",
					ut.Type, si.Fields[reserveUnknownIdx].JSONName, b.types[reserveUnknownFieldTypeIdx].Kind, b.reflectTypes[reserveUnknownFieldTypeIdx])
			}
			// The offset is stamped on struct metadata and applied to cur_dst
			// directly, with no field record to carry hops, so a promoted
			// reserve-unknown would finalize its Value into the host's own bytes.
			if len(si.Fields[reserveUnknownIdx].PtrPath) > 0 {
				return 0, fmt.Errorf("vbind: struct %s reserves unknown keys into a field promoted across an embedded pointer; the reserve-unknown Value is located from the host base, so embed %s by value or give the pointer an explicit JSON name",
					ut.Type, si.Fields[reserveUnknownIdx].PtrPath[0].PointeeType.Type)
			}
			reserveUnknownOff := uint32(si.Fields[reserveUnknownIdx].Offset)
			meta.StructMeta().ReserveUnknownFieldOff = reserveUnknownOff
			b.typeMeta[idx].StructMeta().ReserveUnknownFieldOff = reserveUnknownOff
		}
		// Native close reads this type flag off the register-live cur_type to
		// dismiss an ordinary struct without touching memory, so it is a superset
		// of "needs the merged-tape pass": an inline variant and a reserve-unknown
		// always need it, and a poly field may. Structs that pass this test re-split
		// precisely at the close, which is off the hot path by construction.
		hasPolyField := false
		for i := range si.Fields {
			if b.fields[fieldsBase+uint32(i)].Flags&(uint32(TagVariant)|uint32(TagInlineVariant)|uint32(TagKindof)) != 0 {
				hasPolyField = true
				break
			}
		}
		if hasPolyField || meta.StructMeta().InlineVariantIdx != 0xFFFF ||
			meta.StructMeta().ReserveUnknownFieldOff != 0xFFFFFFFF {
			info.flags |= bindFlagMayPhase2
		}
		// The pair, not either member alone, is what makes one merged tape serve two
		// consumers with different subsets, and so what sizes the tape arena. Both
		// members are already in hand here, so TypeTree.HasSplitTape costs nothing
		// beyond the conjunction.
		if meta.StructMeta().InlineVariantIdx != 0xFFFF &&
			meta.StructMeta().ReserveUnknownFieldOff != 0xFFFFFFFF {
			b.hasSplitTape = true
		}
		s := info.Struct()
		s.FieldCount = uint32(len(si.Fields))
		info.child = uintptr(fieldsBase)

		b.structSites = append(b.structSites, structSite{idx: idx, si: si})
		// A transitive heap writing field requires scannable staging when this struct
		// appears as a map value.
		for i := range fieldTypeIdxs {
			if b.containsUnmarshaler[fieldTypeIdxs[i]] {
				b.containsUnmarshaler[idx] = true
				break
			}
		}
	case typ.KindSlice, typ.KindStream:
		li, _ := ut.Ext.(*typ.SliceTypeInfo)
		if li == nil || li.ElemType == nil {
			return 0, fmt.Errorf("vbind: slice ext missing")
		}
		childIdx, err := b.collect(li.ElemType)
		if err != nil {
			return 0, err
		}
		sl := info.Slice()
		sl.ChildSize = uint32(li.ElemType.Size)
		info.child = uintptr(childIdx)
		sm := meta.SliceMeta()
		sm.ElemRType = li.ElemType.Ptr
		if li.EmptySliceData != nil {
			sm.EmptySliceData = uintptr(li.EmptySliceData)
		}
		// Slice backing demand and pointer pointee demand use independent SlotClasses.
		// Stream[T] backings take a further distinct SlotClass from regular []T so
		// batch sizing and EWMA do not cross-contaminate across pooled parses.
		isStream := typ.IsStreamType(ut.Type)
		var elemHasStream bool
		if isStream {
			// T must be a value type: the slice backing holds T by value and
			// the native binder writes element slots in place. A pointer T
			// would need per-element heap allocation and break the SlotClass
			// value-slot model.
			if li.ElemType.Type.Kind() == reflect.Pointer {
				return 0, fmt.Errorf("vbind: stream.Stream[T] element type must not be a pointer, got %s", li.ElemType.Type)
			}
			// Stream[T] must not recurse through a Stream edge: a T that
			// transitively contains Stream[T] makes the stream type tree
			// cyclic and the per-element yield model non-terminating.
			if streamRecurses(li.ElemType.Type) {
				return 0, fmt.Errorf("vbind: stream.Stream[T] element type %s recursively contains Stream[%s]", li.ElemType.Type, li.ElemType.Type)
			}
			// Stream[T] whose element type tree contains a Stream field: the
			// batch model would bind element bodies (including nested stream
			// fields) before Go can register nested OnRead via Item.Target().
			// Mark the stream type so native array_value yields per-element
			// (BIND_PHASE_ARRAY_VALUE_BEGIN), letting Go register handlers first.
			// Leaf streams (no nested Stream field) keep the batch path.
			_, elemHasStream = findStreamField(li.ElemType.Type, nil)
			if elemHasStream {
				info.flags |= bindFlagElemHasStream
			}
		}
		ac := b.registerSliceSlotClass(li.ElemType, isStream, elemHasStream)
		sm.AllocClass = int32(ac)
		// RecBatch classification must wait for the complete backing dependency graph.
		if b.containsUnmarshaler[childIdx] {
			b.containsUnmarshaler[idx] = true
		}
	case typ.KindArray:
		ai, _ := ut.Ext.(*typ.ArrayTypeInfo)
		if ai == nil || ai.ElemType == nil {
			return 0, fmt.Errorf("vbind: array ext missing")
		}
		childIdx, err := b.collect(ai.ElemType)
		if err != nil {
			return 0, err
		}
		ap := info.Array()
		ap.ChildSize = uint32(ai.ElemType.Size)
		info.child = uintptr(childIdx)
		meta.ArrayMeta().ArrayLen = uint32(ai.ArrayLen)
		if b.containsUnmarshaler[childIdx] {
			b.containsUnmarshaler[idx] = true
		}
	case typ.KindMap:
		mi, _ := ut.Ext.(*typ.MapTypeInfo)
		if mi == nil || mi.KeyType == nil || mi.ValType == nil {
			return 0, fmt.Errorf("vbind: map ext missing")
		}
		keyIdx, err := b.collect(mi.KeyType)
		if err != nil {
			return 0, err
		}
		valIdx, err := b.collect(mi.ValType)
		if err != nil {
			return 0, err
		}

		// A Stream[T] as a map value is rejected: handlers are pre-registered
		// on a specific Stream[T] field instance before Decode, but a map
		// value is dynamically dispatched (the value slot is recycled across
		// entries), so there is no stable instance to register a handler on.
		// The map_value path also memsets the value slot to zero, which would
		// wipe any pre-registered handler pointer. The check covers both a
		// direct Stream[T] value and an indirect one (e.g. a struct value
		// containing a Stream[T] field).
		if path, ok := findStreamField(mi.ValType.Type, nil); ok {
			return 0, fmt.Errorf("vbind: map value type %s contains stream.Stream[T] at %s; stream fields require a pre-registered handler on a specific field instance, which map values cannot provide", mi.ValType.Type, path)
		}

		valSize := uint32(mi.ValType.Size)
		if b.containsUnmarshaler[valIdx] {
			b.containsUnmarshaler[idx] = true
		}
		stride := uint32(16 + ((valSize + 7) &^ 7))
		kvStride := stride
		valIsDeferred := b.mapValueNeedsIndirection(valIdx)
		valSlotClass := int32(-1)
		if valIsDeferred {
			valSlotClass = int32(b.registerSlotClass(mi.ValType))
		}
		mp := info.Map()
		mp.AllocClass = int32(b.registerMapSlotClass(ut))
		info.child = uintptr(valIdx)
		mm := meta.MapMeta()
		mm.KeyType = keyIdx
		mm.Stride = stride
		b.mapSites = append(b.mapSites, mapSite{
			idx:          idx,
			kvStride:     kvStride,
			valIdx:       valIdx,
			keyIdx:       keyIdx,
			stride:       stride,
			valSlotClass: valSlotClass,
			mapRType:     ut.Ptr,
		})
	case typ.KindAny, typ.KindIface:
		// The seen entry must already exist before collecting []any and map[string]any,
		// which recurse back to this universal any type.
		b.anyTypeIdx = idx
		sliceAnyUt := typ.UniTypeOf(reflect.TypeFor[[]any]())
		mapAnyUt := typ.UniTypeOf(reflect.TypeFor[map[string]any]())
		sliceAnyIdx, err := b.collect(sliceAnyUt)
		if err != nil {
			return 0, err
		}
		mapAnyIdx, err := b.collect(mapAnyUt)
		if err != nil {
			return 0, err
		}
		float64Sc := b.registerPrimitiveSlotClass(reflect.TypeFor[float64](), 8)
		stringSc := b.registerPrimitiveSlotClass(reflect.TypeFor[string](), 16)
		sliceSc := b.registerPrimitiveSlotClass(reflect.TypeFor[[]any](), 24)
		mapSc := b.registerMapSlotClass(mapAnyUt)
		b.anyMetas = append(b.anyMetas, BindAnyMeta{
			Float64Type:      gort.TypePtr(reflect.TypeFor[float64]()),
			StringType:       gort.TypePtr(reflect.TypeFor[string]()),
			BoolType:         gort.TypePtr(reflect.TypeFor[bool]()),
			NilType:          nil, // null writes a zero eface
			SliceType:        gort.TypePtr(reflect.TypeFor[[]any]()),
			MapType:          gort.TypePtr(reflect.TypeFor[map[string]any]()),
			StaticTrue:       &anyStaticTrue,
			StaticFalse:      &anyStaticFalse,
			Float64SlotClass: int32(float64Sc),
			StringSlotClass:  int32(stringSc),
			SliceSlotClass:   int32(sliceSc),
			MapSlotClass:     int32(mapSc),
			SliceAnyTypeIdx:  uint16(sliceAnyIdx),
			MapAnyTypeIdx:    uint16(mapAnyIdx),
			NumberType:       gort.TypePtr(reflect.TypeFor[json.Number]()),
		})
		// AnyMetas may still reallocate, so child remains an index until freeze.
		info.child = uintptr(len(b.anyMetas) - 1)
	default:
		return 0, fmt.Errorf("vbind: unsupported kind %d", ut.Kind)
	}
	// Preserve flags already set by kind specific collection. Allocator mode
	// belongs to SlotTemplate and must never enter this byte.
	info.flags |= kindFlags(info.Kind)
	switch info.Kind {
	case KindStruct, KindArray, KindSlice, KindMap:
		// An aggregate that reaches a deferred type has to be redirected to
		// scannable storage when it lands in the noscan map staging buffer. All
		// four are stamped, not just the two with inline storage: a slice header
		// and an *hmap are written into the entry just as directly, and the drain
		// dereferences the entry for any of them (mapValueNeedsIndirection).
		//
		// The flag is read only at the two map-value dispatch sites, so stamping
		// it here costs the ordinary struct-field and element paths nothing.
		if b.containsUnmarshaler[idx] {
			info.flags |= bindFlagContainsDeferred
		}
	}
	b.types[idx] = info
	b.typeMeta[idx] = meta
	return idx, nil
}

// mapValueNeedsIndirection defines both sides of the map staging representation.
// Pointer values already reference scannable pointee storage. Other values that
// can publish heap pointers use a scannable SlotClass, and the noscan KV entry
// carries only that slot address. Binder flags and drain metadata must derive
// from this same predicate.
func (b *builder) mapValueNeedsIndirection(valIdx uint32) bool {
	switch b.types[valIdx].Kind {
	case KindPointer:
		return false
	case KindUnmarshaler, KindTextUnmarshaler, KindRawMessage, KindValue:
		return true
	}
	return b.containsUnmarshaler[valIdx]
}

func kindFlags(k Kind) uint8 {
	switch k {
	case KindPointer, KindValue, KindAny, KindIface,
		KindUnmarshaler, KindTextUnmarshaler, KindRawMessage:
		// Native dispatch treats flags == 0 as permission to skip predispatch work.
		return bindFlagCold
	default:
		return 0
	}
}

// Recursive references may observe a type before its flags are finalized.
// Reapply while Field.Type is still an index, preserving tag bits and the high
// 16 bit table index carried by variant targets, discriminators, and kindof fields.
func (b *builder) reapplyFieldFlags() {
	for i := range b.fields {
		typeIdx := uintptr(b.fields[i].Type)
		// Discriminator fields also carry VariantIdx, so preserve all high bits.
		// TagViaPtr must survive too: dropping it would leave the field's Offset
		// (relative to a pointee) applied to the struct base.
		preserveBits := b.fields[i].Flags & (uint32(TagQuoted) | uint32(TagVariant) | uint32(TagVDisc) | uint32(TagInlineVariant) | uint32(TagKindof) | uint32(TagReserveUnknown) | uint32(TagInlineVDisc) | uint32(TagViaPtr) | 0xFFFF0000)
		// bindFlagMayPhase2 is type local and must not enter the field gate.
		b.fields[i].Flags = preserveBits | (uint32(b.types[typeIdx].flags) & ^uint32(bindFlagMayPhase2))
	}
}

func (b *builder) tapeBindMayAppendStrings() bool {
	for i := range b.fields {
		f := &b.fields[i]
		if f.Flags&uint32(TagVDisc) != 0 {
			return true
		}
		if f.Flags&uint32(TagQuoted) == 0 {
			continue
		}
		typeIdx := uintptr(f.Type)
		for b.types[typeIdx].Kind == KindPointer {
			typeIdx = b.types[typeIdx].child
		}
		if b.types[typeIdx].Kind == KindString {
			return true
		}
	}
	return false
}

// resolveChildPointers publishes direct ABI pointers into frozen backing arrays.
// Appending to types, fields, or anyMetas after this call would invalidate them.
func (b *builder) resolveChildPointers() error {
	if len(b.types) == 0 {
		return nil
	}
	typesBase := unsafe.Pointer(unsafe.SliceData(b.types))
	typesLen := uintptr(len(b.types))
	// Fieldless trees retain one sentinel row so fieldsBase is always addressable.
	fieldsBase := unsafe.Pointer(unsafe.SliceData(b.fields))
	fieldsLen := uintptr(len(b.fields))
	typeSize := unsafe.Sizeof(BindType{})
	fieldSize := unsafe.Sizeof(BindField{})

	for i := range b.types {
		bt := &b.types[i]
		switch bt.Kind {
		case KindPointer, KindSlice, KindStream, KindArray, KindMap:
			childIdx := uintptr(bt.child)
			if childIdx >= typesLen {
				return fmt.Errorf("vbind: pass2: type %d child idx %d out of range (%d)", i, childIdx, typesLen)
			}
			bt.setChild(unsafe.Add(typesBase, childIdx*typeSize))
		case KindStruct:
			firstIdx := uintptr(bt.child)
			// Fieldless structs point at the sentinel row.
			if firstIdx > fieldsLen {
				return fmt.Errorf("vbind: pass2: type %d first_field %d out of range (%d)", i, firstIdx, fieldsLen)
			}
			bt.setChild(unsafe.Add(fieldsBase, firstIdx*fieldSize))
		case KindAny, KindIface:
			amIdx := uintptr(bt.child)
			if amIdx >= uintptr(len(b.anyMetas)) {
				return fmt.Errorf("vbind: pass2: type %d anymeta idx %d out of range (%d)", i, amIdx, len(b.anyMetas))
			}
			bt.setChild(unsafe.Pointer(&b.anyMetas[amIdx]))
		}
	}
	for i := range b.fields {
		f := &b.fields[i]
		typeIdx := uintptr(f.Type)
		if typeIdx >= typesLen {
			return fmt.Errorf("vbind: pass2: field %d type idx %d out of range (%d)", i, typeIdx, typesLen)
		}
		f.setFieldType(unsafe.Add(typesBase, typeIdx*typeSize))
	}
	return nil
}

func (b *builder) registerSlotClass(elem *typ.UniType) uint32 {
	if idx, ok := b.bySlot[elem]; ok {
		return idx
	}
	idx := uint32(len(b.slots))
	b.slots = append(b.slots, SlotTemplate{
		Batch:    4,
		ElemSize: uint32(elem.Size),
		RType:    elem.Ptr,
	})
	b.bySlot[elem] = idx
	b.slotRecs = append(b.slotRecs, slotRec{kind: slotPointer, roots: []*typ.UniType{elem}})
	return idx
}

// Slice, pointer, and stream storage use distinct SlotClasses because they have
// independent sizing and consumption state. Stream Batch is the fixed buffer
// capacity: one for nested-stream elements and the template batch otherwise.
// Recursion mode is assigned after the backing dependency graph is complete.
func (b *builder) registerSliceSlotClass(elem *typ.UniType, isStream bool, elemHasStream bool) uint32 {
	table := b.bySliceSlot
	if isStream {
		table = b.byStreamSlot
	}
	if idx, ok := table[elem]; ok {
		return idx
	}
	tpl := SlotTemplate{
		Batch:    4,
		ElemSize: uint32(elem.Size),
		RType:    elem.Ptr,
	}
	if isStream {
		tpl.IsStream = true
		if elemHasStream {
			tpl.Batch = 1 // non-leaf: single-slot buffer, reused per element
		}
	}
	idx := uint32(len(b.slots))
	b.slots = append(b.slots, tpl)
	table[elem] = idx
	b.slotRecs = append(b.slotRecs, slotRec{kind: slotSlice, roots: []*typ.UniType{elem}})
	return idx
}

// roots describe which typed values a slot backing can own. SCC edges cross the
// first pointer, slice, map, or any backing boundary reachable from each root.
type slotRec struct {
	kind  slotKind
	roots []*typ.UniType
}

type slotKind uint8

const (
	slotPointer slotKind = iota
	slotSlice
	slotMap
	slotPrim
)

// The SCC graph models backing ownership, not merely type recursion. An edge
// A to B means values stored in backing A can retain backing B. A self loop is
// therefore a nontrivial SCC. Recursive slice backings use RecBatch; other SCC
// members use RecBump so the allocator can detach retained cycles as a group.
func (b *builder) markSCCGroups() {
	n := len(b.slots)
	adj := make([][]uint32, n)
	visited := make([]int, len(b.types))
	token := 0
	for i := range n {
		token++
		var edges []uint32
		for _, root := range b.slotRecs[i].roots {
			edges = b.slotEdgesFrom(root, edges, visited, token)
		}
		adj[i] = edges
	}

	// Tarjan SCC, iterative to avoid deep recursion on large type graphs.
	index := make([]int, n)
	low := make([]int, n)
	for i := range index {
		index[i] = -1
	}
	onStack := make([]bool, n)
	var stack []int
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
	}
	var idxCounter, compID int
	type frame struct {
		v int
		i int
	}
	var work []frame
	for s := range n {
		if index[s] != -1 {
			continue
		}
		index[s] = idxCounter
		low[s] = idxCounter
		idxCounter++
		stack = append(stack, s)
		onStack[s] = true
		work = append(work, frame{v: s, i: 0})
		for len(work) > 0 {
			f := &work[len(work)-1]
			if f.i < len(adj[f.v]) {
				w := int(adj[f.v][f.i])
				f.i++
				if index[w] == -1 {
					index[w] = idxCounter
					low[w] = idxCounter
					idxCounter++
					stack = append(stack, w)
					onStack[w] = true
					work = append(work, frame{v: w, i: 0})
				} else if onStack[w] && index[w] < low[f.v] {
					low[f.v] = index[w]
				}
			} else {
				if low[f.v] == index[f.v] {
					for {
						w := stack[len(stack)-1]
						stack = stack[:len(stack)-1]
						onStack[w] = false
						comp[w] = compID
						if w == f.v {
							break
						}
					}
					compID++
				}
				doneV := f.v
				work = work[:len(work)-1]
				if len(work) > 0 {
					p := &work[len(work)-1]
					if low[doneV] < low[p.v] {
						low[p.v] = low[doneV]
					}
				}
			}
		}
	}

	compSize := make([]int, compID)
	for v := range n {
		compSize[comp[v]]++
	}
	// A single-member component is nontrivial iff its slot has a self-loop.
	selfLoop := make([]bool, n)
	for v := range n {
		for _, w := range adj[v] {
			if int(w) == v {
				selfLoop[v] = true
				break
			}
		}
	}
	groupOf := make([]int, compID)
	groupID := 0
	for c := 0; c < compID; c++ {
		nontrivial := compSize[c] > 1
		if !nontrivial {
			for v := range n {
				if comp[v] == c && selfLoop[v] {
					nontrivial = true
					break
				}
			}
		}
		if nontrivial {
			groupID++
			groupOf[c] = groupID
		}
	}
	for v := range n {
		g := uint32(groupOf[comp[v]])
		b.slots[v].Group = g
		if g == 0 {
			b.slots[v].Mode = slotBump
		} else if b.slotRecs[v].kind == slotSlice {
			b.slots[v].Mode = slotRecBatch
		} else {
			b.slots[v].Mode = slotRecBump
		}
	}
	b.groupCount = uint32(groupID)
}

// Structs and arrays do not create backing boundaries, so traversal continues
// through them. Pointer, slice, map, and any record the first owned backing and
// stop that branch. The token isolates cycle detection for each source slot.
func (b *builder) slotEdgesFrom(ut *typ.UniType, out []uint32, visited []int, token int) []uint32 {
	idx, ok := b.seen[ut]
	if !ok || visited[idx] == token {
		return out
	}
	visited[idx] = token
	switch ut.Kind {
	case typ.KindStruct:
		si, _ := ut.Ext.(*typ.StructTypeInfo)
		if si != nil {
			for i := range si.Fields {
				out = b.slotEdgesFrom(si.Fields[i].FieldType, out, visited, token)
			}
		}
	case typ.KindArray:
		ai, _ := ut.Ext.(*typ.ArrayTypeInfo)
		if ai != nil {
			out = b.slotEdgesFrom(ai.ElemType, out, visited, token)
		}
	case typ.KindPointer:
		pi, _ := ut.Ext.(*typ.PointerTypeInfo)
		if pi != nil {
			if s, ok := b.bySlot[pi.ElemType]; ok {
				out = append(out, s)
			}
		}
	case typ.KindSlice:
		li, _ := ut.Ext.(*typ.SliceTypeInfo)
		if li != nil {
			if s, ok := b.bySliceSlot[li.ElemType]; ok {
				out = append(out, s)
			}
		}
	case typ.KindMap:
		if s, ok := b.bySlot[ut]; ok {
			out = append(out, s)
		}
	case typ.KindAny, typ.KindIface:
		// eface.data can retain either container backing, and both recurse to any.
		if len(b.anyMetas) > 0 {
			am := &b.anyMetas[0]
			out = append(out, uint32(am.SliceSlotClass), uint32(am.MapSlotClass))
		}
	}
	return out
}

func (b *builder) registerMapSlotClass(mt *typ.UniType) uint32 {
	if idx, ok := b.bySlot[mt]; ok {
		return idx
	}
	idx := uint32(len(b.slots))
	b.slots = append(b.slots, SlotTemplate{
		Batch:    4,
		ElemSize: uint32(mt.Size), // sizeof(map[K]V) == one word
		RType:    mt.Ptr,          // map type; UnsafeNewArray + MakeMap both use it
		Flags:    SlotIsMap,
	})
	b.bySlot[mt] = idx
	// An hmap backing can retain backings reachable from both keys and values.
	mi, _ := mt.Ext.(*typ.MapTypeInfo)
	var roots []*typ.UniType
	if mi != nil {
		roots = []*typ.UniType{mi.KeyType, mi.ValType}
	}
	b.slotRecs = append(b.slotRecs, slotRec{kind: slotMap, roots: roots})
	return idx
}

func (b *builder) registerPrimitiveSlotClass(rt reflect.Type, size uintptr) uint32 {
	if idx, ok := b.byPrimSlot[rt]; ok {
		return idx
	}
	idx := uint32(len(b.slots))
	b.slots = append(b.slots, SlotTemplate{
		Batch:    4,
		ElemSize: uint32(size),
		RType:    gort.TypePtr(rt),
	})
	b.byPrimSlot[rt] = idx
	// Primitive roots are leaf nodes except []any headers, whose Data field owns
	// the element backing.
	b.slotRecs = append(b.slotRecs, slotRec{kind: slotPrim, roots: []*typ.UniType{typ.UniTypeOf(rt)}})
	return idx
}

// Only tag bits consumed directly by native value dispatch belong in BindField.
func hotFieldFlagsFrom(t typ.TagFlag) uint32 {
	var f uint32
	if t&typ.TagFlagQuoted != 0 {
		f |= uint32(TagQuoted)
	}
	if t&typ.TagFlagReserveUnknown != 0 {
		f |= uint32(TagReserveUnknown)
	}
	return f
}

// Native lookup requires a valid NONE sentinel even for fieldless structs.
func (b *builder) attachFieldLookups() error {
	for _, s := range b.structSites {
		blob, err := getStructLookup(s.si)
		if err != nil {
			return err
		}
		var ptr unsafe.Pointer
		if len(blob) > 0 {
			ptr = unsafe.Pointer(unsafe.SliceData(blob))
		} else {
			ptr = unsafe.Pointer(&emptyLookupSentinel[0])
		}
		b.typeMeta[s.idx].StructMeta().Lookup = ptr
	}
	return nil
}

// Value size and deferred reachability must be final before drain records are
// published through raw pointers.
func (b *builder) attachMapDrainInfos() {
	if len(b.mapSites) == 0 {
		return
	}
	b.mapDrainInfo = make([]MapDrainInfo, len(b.mapSites))
	for i, s := range b.mapSites {
		valMeta := &b.typeMeta[s.valIdx]
		keyType := &b.types[s.keyIdx]
		b.mapDrainInfo[i] = MapDrainInfo{
			MapRType:      s.mapRType,
			KVStride:      s.kvStride,
			KeyKind:       keyType.Kind,
			ValSize:       valMeta.Size,
			ValIsDeferred: b.mapValueNeedsIndirection(s.valIdx),
			ValIndirect:   gort.MapValueIsIndirect(uintptr(valMeta.Size)),
			ValSlotClass:  s.valSlotClass,
		}
		b.typeMeta[s.idx].MapMeta().DrainInfo = unsafe.Pointer(&b.mapDrainInfo[i])
	}
}

// maxMapChainDepth must match native BIND_MAX_DEPTH because each active map
// region corresponds to one level of the native bind frame stack.
const maxMapChainDepth = 255
const regionSlotsPerMap = 16
const regionHeaderSize = 32

// The capacity floor is the greatest total size of map regions simultaneously
// live on one descent path. Each map type M contributes
// regionHeaderSize + regionSlotsPerMap * stride(M) bytes per live region;
// recursive paths are capped by maxMapChainDepth.
func (b *builder) sizeMapBuffer() uint32 {
	if len(b.mapSites) == 0 {
		return 0
	}
	adj := b.buildTypeAdjacency()
	regionSizeOf := make(map[uint32]uint32, len(b.mapSites))
	var maxRegionSize uint32
	for i := range b.mapSites {
		s := &b.mapSites[i]
		rs := regionHeaderSize + regionSlotsPerMap*s.stride
		regionSizeOf[s.idx] = rs
		if rs > maxRegionSize {
			maxRegionSize = rs
		}
	}

	// The largest region bounds every level when a cycle reaches the depth cap.
	byteCap := maxMapChainDepth * maxRegionSize

	longestLabeled := func(weight func(uint32) uint32) uint32 {
		memo := make(map[uint32]uint32, len(b.types))
		onStack := make(map[uint32]bool, len(b.types))
		var dfs func(idx uint32) (uint32, bool) // (sum, capped)
		dfs = func(idx uint32) (uint32, bool) {
			if v, ok := memo[idx]; ok {
				return v, false
			}
			if onStack[idx] {
				return byteCap, true
			}
			onStack[idx] = true
			var best uint32
			capped := false
			for _, c := range adj[idx] {
				d, dc := dfs(c)
				capped = capped || dc
				if d > best {
					best = d
				}
			}
			onStack[idx] = false
			res := min(weight(idx)+best, byteCap)
			if !capped {
				memo[idx] = res
			}
			return res, capped
		}
		var maxAll uint32
		for i := range b.types {
			d, _ := dfs(uint32(i))
			if d > maxAll {
				maxAll = d
			}
		}
		return maxAll
	}

	maxBytes := longestLabeled(func(idx uint32) uint32 {
		if sz, ok := regionSizeOf[idx]; ok {
			return sz
		}
		return 0
	})
	if maxBytes == 0 {
		maxBytes = regionHeaderSize + regionSlotsPerMap*16 // at least one minimal region
	}
	return maxBytes
}

func (b *builder) buildTypeAdjacency() [][]uint32 {
	adj := make([][]uint32, len(b.types))
	for i := range b.types {
		bt := &b.types[i]
		switch bt.Kind {
		case KindPointer, KindSlice, KindStream, KindArray, KindMap:
			adj[i] = append(adj[i], uint32(bt.child))
		case KindStruct:
			first := uint32(bt.child)
			n := bt.Struct().FieldCount
			for k := range n {
				adj[i] = append(adj[i], uint32(b.fields[first+k].Type))
			}
		case KindAny, KindIface:
			am := &b.anyMetas[bt.child]
			adj[i] = append(adj[i], uint32(am.SliceAnyTypeIdx), uint32(am.MapAnyTypeIdx))
		}
	}
	return adj
}

// isTapeBindUnsupportedRootKind mirrors the root gate after pointer unwrapping.
// KindValue binds by whole-tape aliasing, while kinds requiring JSON-path hooks,
// source spelling, dynamic boxing, or stream yields are rejected.
func isTapeBindUnsupportedRootKind(k Kind) bool {
	switch k {
	case KindAny, KindIface, KindUnmarshaler, KindTextUnmarshaler, KindRawMessage, KindNumber, KindStream:
		return true
	}
	return false
}

// tapeBindNestedUnsupportedReason describes capabilities unavailable to the
// nested tape walker. Deferred values require rebuilding source bytes, Number
// requires source spelling across every numeric tag, and Stream requires the
// JSON path's yield driver. Dynamic boxing and Value aliasing are supported.
func tapeBindNestedUnsupportedReason(k Kind) string {
	switch k {
	case KindUnmarshaler, KindTextUnmarshaler, KindRawMessage:
		return "deferred value field unsupported by tape-bind"
	case KindNumber:
		return "json.Number field unsupported by tape-bind (not every numeric tag retains source text)"
	case KindStream:
		return "stream.Stream[T] field unsupported by tape-bind (requires JSON bind path yield/scope-driver)"
	}
	return ""
}

// computeTapeBindUnsupported records the first position outside the tape
// walker's capabilities. Root and nested positions use separate predicates
// because the root supports whole-tape Value aliasing while nested dynamic
// values use the boxing path. Pointer chains are checked at their pointee.
func (b *builder) computeTapeBindUnsupported(rootIdx uint32) *TapeBindUnsupportedPos {
	visited := make([]bool, len(b.types))

	var walk func(typeIdx uint32, isRoot bool, path string) *TapeBindUnsupportedPos
	walk = func(typeIdx uint32, isRoot bool, path string) *TapeBindUnsupportedPos {
		if int(typeIdx) >= len(b.types) || visited[typeIdx] {
			return nil
		}
		visited[typeIdx] = true
		t := b.types[typeIdx]

		if isRoot {
			cur := typeIdx
			for b.types[cur].Kind == KindPointer {
				cur = uint32(b.types[cur].child)
			}
			if isTapeBindUnsupportedRootKind(b.types[cur].Kind) {
				return &TapeBindUnsupportedPos{Path: path, TypeIdx: uint16(cur), Reason: "unsupported root type for tape-bind"}
			}
		}

		if !isRoot && t.Kind == KindPointer {
			childIdx := uint32(t.child)
			ck := b.types[childIdx].Kind
			if ck == KindAny || ck == KindIface {
				return &TapeBindUnsupportedPos{Path: path, TypeIdx: uint16(typeIdx), Reason: "*any / *interface{} field unsupported by tape-bind"}
			}
			return walk(childIdx, false, path)
		}

		// Non-root kind the tape-bind sub-routine cannot walk (deferred value
		// or Number): reject at build time so UnmarshalValue fails fast at
		// entry with a path/reason instead of runtime t_unsupported. Pointer
		// was unwrapped above so *Unmarshaler surfaces here via the pointee.
		if !isRoot {
			if r := tapeBindNestedUnsupportedReason(t.Kind); r != "" {
				return &TapeBindUnsupportedPos{Path: path, TypeIdx: uint16(typeIdx), Reason: r}
			}
		}

		switch t.Kind {
		case KindStruct:
			fieldCount := t.inner
			fieldsBase := uint32(t.child)
			for i := range fieldCount {
				f := b.fields[fieldsBase+i]
				fname := b.fieldNames[fieldsBase+i]
				childPath := path + "." + fname
				if f.Flags&(uint32(TagVariant)|uint32(TagKindof)|uint32(TagInlineVariant)) != 0 {
					if pos := b.checkVariantCaseTypes(f.Flags, childPath); pos != nil {
						return pos
					}
				}
				if pos := walk(uint32(f.Type), false, childPath); pos != nil {
					return pos
				}
			}
		case KindSlice, KindArray:
			if pos := walk(uint32(t.child), false, path+"[]"); pos != nil {
				return pos
			}
		case KindMap:
			if pos := walk(uint32(t.child), false, path+"[V]"); pos != nil {
				return pos
			}
		}
		return nil
	}

	rootName := ""
	if int(rootIdx) < len(b.reflectTypes) && b.reflectTypes[rootIdx] != nil {
		rootName = b.reflectTypes[rootIdx].String()
	}
	return walk(rootIdx, true, rootName)
}

// checkVariantCaseTypes applies the tape walker's case-entry gate, then checks
// each accepted case subtree for nested unsupported positions.
func (b *builder) checkVariantCaseTypes(fieldFlags uint32, path string) *TapeBindUnsupportedPos {
	variantIdx := int(fieldFlags >> 16)
	isKindof := fieldFlags&uint32(TagKindof) != 0
	if isKindof {
		if variantIdx >= len(b.kindofs) {
			return nil
		}
		ot := b.kindofs[variantIdx]
		for k := range 5 {
			caseIdx := int(ot.CaseIdxByKind[k])
			if caseIdx < 0 {
				continue
			}
			caseTypeIdx := ot.CaseTypeIdx(caseIdx)
			ct := b.types[caseTypeIdx]
			if (ct.flags&bindFlagCold != 0) && ct.Kind != KindPointer && ct.Kind != KindValue {
				return &TapeBindUnsupportedPos{Path: path, TypeIdx: caseTypeIdx, Reason: "kindof case has unsupported cold kind for tape-bind"}
			}
			if pos := b.walkVariantCaseIntoType(caseTypeIdx, path+".case"); pos != nil {
				return pos
			}
		}
	} else {
		if variantIdx >= len(b.variants) {
			return nil
		}
		vt := b.variants[variantIdx]
		for caseIdx := range int(vt.CaseCount) {
			caseTypeIdx := vt.CaseTypeIdx(caseIdx)
			ct := b.types[caseTypeIdx]
			if (ct.flags&bindFlagCold != 0) && ct.Kind != KindPointer && ct.Kind != KindValue {
				return &TapeBindUnsupportedPos{Path: path, TypeIdx: caseTypeIdx, Reason: "variant case has unsupported cold kind for tape-bind"}
			}
			if pos := b.walkVariantCaseIntoType(caseTypeIdx, path+".case"); pos != nil {
				return pos
			}
		}
	}
	return nil
}

// walkVariantCaseIntoType checks nested positions after the case-entry gate has
// accepted typeIdx.
func (b *builder) walkVariantCaseIntoType(typeIdx uint16, path string) *TapeBindUnsupportedPos {
	visited := make([]bool, len(b.types))
	var walk func(idx uint32, p string) *TapeBindUnsupportedPos
	walk = func(idx uint32, p string) *TapeBindUnsupportedPos {
		if int(idx) >= len(b.types) || visited[idx] {
			return nil
		}
		visited[idx] = true
		t := b.types[idx]
		if t.Kind == KindPointer {
			childIdx := uint32(t.child)
			ck := b.types[childIdx].Kind
			if ck == KindAny || ck == KindIface {
				return &TapeBindUnsupportedPos{Path: p, TypeIdx: uint16(idx), Reason: "*any / *interface{} field unsupported by tape-bind"}
			}
			return walk(childIdx, p)
		}
		if r := tapeBindNestedUnsupportedReason(t.Kind); r != "" {
			return &TapeBindUnsupportedPos{Path: p, TypeIdx: uint16(idx), Reason: r}
		}
		switch t.Kind {
		case KindStruct:
			fieldCount := t.inner
			fieldsBase := uint32(t.child)
			for i := range fieldCount {
				f := b.fields[fieldsBase+i]
				fname := b.fieldNames[fieldsBase+i]
				if pos := walk(uint32(f.Type), p+"."+fname); pos != nil {
					return pos
				}
			}
		case KindSlice, KindArray:
			return walk(uint32(t.child), p+"[]")
		case KindMap:
			return walk(uint32(t.child), p+"[V]")
		}
		return nil
	}
	return walk(uint32(typeIdx), path)
}

// countSplitTapeSites bounds how many dual-view merged tapes one parse can
// build, or returns SplitTapeSitesUnbounded when the document decides. See
// TypeTree.SplitTapeSites for why the count is what sizes the tape arena.
//
// Two things make it unbounded, and they are different failures:
//
//   - A collection on the path (slice, array, map, stream). Element count is a
//     property of the input, so one field position becomes arbitrarily many tapes.
//     KindArray is included even though its length is fixed: the bound would be a
//     product over nesting depth, and a [1024][1024]T would compute a K larger
//     than any arena it was meant to shrink.
//   - A cycle. Only pointers and the collections above can form one in a Go type
//     graph, so with collections already rejected, a cycle means a pointer chain
//     whose depth the document chooses.
//
// Both are detected on the path rather than globally: the count is over POSITIONS,
// so a type reached twice from two fields is two tapes and must be visited twice.
// That rules out a visited set, which is exactly what would have masked the cycle
// too. onPath carries the recursion stack instead, and the traversal is otherwise
// a plain DFS whose cost is bounded by the tree's field count.
func (b *builder) countSplitTapeSites(rootIdx uint32) int {
	onPath := make([]bool, len(b.types))

	var walk func(typeIdx uint32) int
	walk = func(typeIdx uint32) int {
		if int(typeIdx) >= len(b.types) {
			return 0
		}
		if onPath[typeIdx] {
			return SplitTapeSitesUnbounded // a cycle: depth is the document's choice
		}
		onPath[typeIdx] = true
		defer func() { onPath[typeIdx] = false }()

		t := b.types[typeIdx]
		switch t.Kind {
		case KindSlice, KindArray, KindMap, KindStream:
			// Reaching a dual-view host under any of these makes the count the
			// document's to decide. A collection whose subtree hosts none is not a
			// problem, so recurse and only propagate what is found.
			if walk(uint32(t.child)) != 0 {
				return SplitTapeSitesUnbounded
			}
			return 0
		case KindPointer:
			return walk(uint32(t.child))
		case KindStruct:
			n := 0
			sm := b.typeMeta[typeIdx].StructMeta()
			if sm.ReserveUnknownFieldOff != 0xFFFFFFFF && sm.InlineVariantIdx != 0xFFFF {
				n = 1
			}
			fieldsBase := uint32(t.child)
			for i := range t.inner {
				f := b.fields[fieldsBase+i]
				sub := walk(uint32(f.Type))
				if sub == SplitTapeSitesUnbounded {
					return SplitTapeSitesUnbounded
				}
				n += sub
				// A poly field's case is bound into the host from the same merged
				// tape, so a case that is itself a dual-view host is another site.
				if f.Flags&(uint32(TagVariant)|uint32(TagKindof)|uint32(TagInlineVariant)) != 0 {
					cases, ok := b.countSplitTapeSitesInCases(f.Flags, walk)
					if !ok {
						return SplitTapeSitesUnbounded
					}
					n += cases
				}
			}
			return n
		}
		return 0
	}

	return walk(rootIdx)
}

// countSplitTapeSitesInCases sums the sites reachable through a poly field's case
// types. Reported as (count, ok) so an unbounded case propagates without being
// confused for a count.
func (b *builder) countSplitTapeSitesInCases(fieldFlags uint32, walk func(uint32) int) (int, bool) {
	tableIdx := int(fieldFlags >> 16)
	n := 0
	add := func(caseTypeIdx uint16) bool {
		sub := walk(uint32(caseTypeIdx))
		if sub == SplitTapeSitesUnbounded {
			return false
		}
		n += sub
		return true
	}
	if fieldFlags&uint32(TagKindof) != 0 {
		if tableIdx >= len(b.kindofs) {
			return 0, true
		}
		ot := b.kindofs[tableIdx]
		for k := range 5 {
			if caseIdx := int(ot.CaseIdxByKind[k]); caseIdx >= 0 {
				if !add(ot.CaseTypeIdx(caseIdx)) {
					return 0, false
				}
			}
		}
		return n, true
	}
	if tableIdx >= len(b.variants) {
		return 0, true
	}
	vt := b.variants[tableIdx]
	for caseIdx := range int(vt.CaseCount) {
		if !add(vt.CaseTypeIdx(caseIdx)) {
			return 0, false
		}
	}
	return n, true
}
