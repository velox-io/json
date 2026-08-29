package gort

import (
	"reflect"
	"unsafe"
)

// MapAllocUnit is the layout of internal/runtime/maps.Map (48B). UnsafeNewArray
// batch-allocates Map structs as arrays of this type. Only dirPtr (offset 16)
// is a pointer; the other fields are scalars. If a Go upgrade changes Map's
// layout, TestBatchMapAlloc_SizeMatch fails.
type MapAllocUnit struct {
	pad0   [16]byte       // used(8) + seed(8)
	dirPtr unsafe.Pointer // dirPtr
	pad1   [24]byte       // dirLen(8) + flags(4) + pad(4) + clearSeq(8)
}

var _ = [1]struct{}{}[unsafe.Sizeof(MapAllocUnit{})-48]

var mapAllocUnitRType = TypePtr(reflect.TypeFor[MapAllocUnit]())

const (
	// mapDirPtrOff is the offset of Map.dirPtr (internal/runtime/maps.Map).
	// Map layout: used(8) + seed(8) + dirPtr(8) + dirLen(8) + ...
	mapDirPtrOff = 16

	// mapSeedOff is the offset of Map.seed.
	mapSeedOff = 8

	// mapCtrlEmpty is the control word for an empty swiss-table group: 8 slots
	// each set to ctrlEmpty (0x80). Prewired into the first 8 bytes of each
	// batch-allocated group so putSlotSmall sees a valid empty group without
	// going through growToSmall on the first assign.
	mapCtrlEmpty uint64 = 0x8080808080808080
)

// smallMapPrewireOK gates small-map group prewiring (the two-block path). Set
// by the swissmap init() after verifySmallMapPrewire passes; stays false on
// non-swissmap builds or if the runtime layout doesn't match our assumptions.
// It is also a prerequisite for compositeMergeOK: the composite path prewires
// dirPtr to the in-block group, which requires the same layout guarantees.
var smallMapPrewireOK bool

// compositeMergeOK gates the composite {MapAllocUnit, Group} allocation path in
// PlanMapSlots. Set by the swissmap init() after verifyCompositeMerge passes;
// requires smallMapPrewireOK. When false but smallMapPrewireOK is true,
// PlanMapSlots falls back to the two-block prewire path. When both are false,
// it falls back to lazy growToSmall.
var compositeMergeOK bool

// MapSlotPlan describes the inner-block allocation vbind must perform for a map
// slot batch. gort is the SSOT for the inner type: the composite type is cached
// per K/V (keyed by GroupType) in compositeCache, so map[string]string and
// map[string]any share one synthesized type across all TypeTrees/allocators.
//
// Three paths, selected by PlanMapSlots in tier order:
//   - Composite (GroupOff > 0): InnerType is {MapAllocUnit, Group}. One inner
//     alloc. Each unit carries its group in-block; InitMapSlots prewires ctrl +
//     dirPtr to it, so the first assign takes the small-map path with zero alloc.
//   - Two-block (GroupOff == 0, GroupType != nil): InnerType is MapAllocUnit,
//     plus a separate group block of GroupType (GroupSize stride). vbind
//     allocates both; InitMapSlots prewires ctrl on each group slot and dirPtr
//     to it. First assign is zero alloc, at the cost of a second allocation.
//   - Lazy (GroupOff == 0, GroupType == nil): InnerType is MapAllocUnit. dirPtr
//     stays nil; the runtime allocates the group via growToSmall on first assign.
//
// Stride is the byte size of one unit in InnerType; InitMapSlots uses it to
// walk the inner block. vbind allocates InnerType (always) and GroupType (when
// non-nil) via UnsafeNewArray.
type MapSlotPlan struct {
	InnerType unsafe.Pointer // composite (GroupOff>0) or MapAllocUnit
	GroupOff  uintptr        // >0 = composite: group at this offset in InnerType
	GroupType unsafe.Pointer // non-nil + GroupOff==0 = two-block: separate group block
	GroupSize uintptr        // group unit stride (two-block path)
	Stride    uintptr        // InnerType unit stride
}

// PlanMapSlots returns the inner-block plan for a map slot batch. vbind
// allocates `UnsafeNewArray(plan.InnerType, batch)` (plus
// `UnsafeNewArray(plan.GroupType, batch)` when GroupType is set), passes them
// to InitMapSlots. SSOT: the composite type is cached per K/V via compositeCache.
//
// Tier order: composite (default) > two-block prewire > lazy. Build tags
// disable tiers from the top: vj_nocompositemap drops composite (keeps
// two-block prewire); vj_nomapprewire drops both (lazy only). This lets you
// isolate whether a bug lives in reflect.StructOf synthesis, the prewire logic,
// or neither.
func PlanMapSlots(rtype unsafe.Pointer) MapSlotPlan {
	if compositeMergeOK {
		if comp, groupOff, stride, ok := compositeMapType(rtype); ok {
			return MapSlotPlan{InnerType: TypePtr(comp), GroupOff: groupOff, Stride: stride}
		}
	}
	if smallMapPrewireOK {
		layout := ReadMapLayout(rtype)
		if layout.GroupType != nil && layout.GroupSize != 0 {
			return MapSlotPlan{
				InnerType: mapAllocUnitRType,
				Stride:    unsafe.Sizeof(MapAllocUnit{}),
				GroupType: layout.GroupType,
				GroupSize: layout.GroupSize,
			}
		}
	}
	return MapSlotPlan{InnerType: mapAllocUnitRType, Stride: unsafe.Sizeof(MapAllocUnit{})}
}

// InitMapSlots is the explicit, batched version of what the Go compiler emits
// inline for a non-escaping `make(map[K]V)` with hint <= 8 (see go walkMakeMap).
// The compiler stack-allocates the hmap and one group, prewires ctrl=empty and
// dirPtr to the group, writes seed directly, and skips the makemap call entirely:
// for hint <= 8, runtime makemap -> maps.NewMap only writes seed.
// NewMap returns before any directory allocation, and reuses the group if dirPtr
// is already set. InitMapSlots does the same but for N maps that DO escape, held
// by the parser's slot block.
//
// Pure initialization, zero allocation: vbind owns the inner block(s) (allocated
// via PlanMapSlots + UnsafeNewArray) and the parent block (array of *hmap).
// InitMapSlots writes seed (+ ctrl/dirPtr on composite or two-block paths) into
// each unit and publishes each unit's address into block[i]. One mapSeed() call
// covers the whole batch instead of N per-map re-seeds.
//
//   - Composite (plan.GroupOff > 0): dirPtr prewired to the in-block group.
//   - Two-block (groupBlock != nil): dirPtr prewired to groupBlock[i].
//   - Lazy (neither): dirPtr stays nil; runtime grows the group on first assign.
//
// GC: groupBlock (two-block path) stays rooted via inner[i].dirPtr (MapAllocUnit
// is scannable, dirPtr@16 is a GC pointer), so it tracks the inner block's
// lifetime without a separate handle.
func InitMapSlots(block, inner, groupBlock unsafe.Pointer, esz uintptr, plan MapSlotPlan, batch int) {
	seed := mapSeed()
	for i := range batch {
		unit := unsafe.Add(inner, uintptr(i)*plan.Stride)
		*(*[48]byte)(unit) = [48]byte{}
		*(*uintptr)(unsafe.Add(unit, mapSeedOff)) = seed
		if plan.GroupOff > 0 {
			// Composite: group is in-block at GroupOff.
			gptr := unsafe.Add(unit, plan.GroupOff)
			*(*uint64)(gptr) = mapCtrlEmpty
			*(*unsafe.Pointer)(unsafe.Add(unit, mapDirPtrOff)) = gptr
		} else if groupBlock != nil {
			// Two-block: group is a separate block, one slot per map.
			gptr := unsafe.Add(groupBlock, uintptr(i)*plan.GroupSize)
			*(*uint64)(gptr) = mapCtrlEmpty
			*(*unsafe.Pointer)(unsafe.Add(unit, mapDirPtrOff)) = gptr
		}
		*(*unsafe.Pointer)(unsafe.Add(block, uintptr(i)*esz)) = unit
	}
}
