//go:build goexperiment.swissmap || go1.26

package gort

import (
	"reflect"
	"runtime"
	"sync"
	"unsafe"
)

// compositeEntry caches the synthesized composite type, the byte offset of the
// group field within it, and the unit stride, keyed by the map's GroupType
// pointer (unique per K/V). A nil typ marks a K/V whose synthesis failed so
// PlanMapSlots falls back to the lazy path.
type compositeEntry struct {
	typ      reflect.Type
	groupOff uintptr
	stride   uintptr
}

var compositeCache sync.Map

// compositeMapType returns a reflect.StructOf composite of {MapAllocUnit, Group}
// for the given map *abi.Type, plus the byte offset of the group field and the
// unit stride (== composite type size). Returns (nil, 0, 0, false) when the
// layout is unavailable or synthesis fails; PlanMapSlots falls back to lazy.
//
// The group is embedded as its real type (layout.GroupType) rather than
// resynthesized from K/V: the runtime's own group type already carries the
// correct internal layout (ctrl word, interleaved vs split slots, padding), so
// the composite inherits it verbatim with no second source of truth.
func compositeMapType(rtype unsafe.Pointer) (typ reflect.Type, groupOff, stride uintptr, ok bool) {
	layout := ReadMapLayout(rtype)
	if layout.GroupType == nil || layout.GroupSize == 0 {
		return nil, 0, 0, false
	}
	key := uintptr(layout.GroupType)
	if v, loaded := compositeCache.Load(key); loaded {
		e := v.(compositeEntry)
		if e.typ == nil {
			return nil, 0, 0, false
		}
		return e.typ, e.groupOff, e.stride, true
	}
	groupRT := TypeFromRType(layout.GroupType)
	comp, synthOK := safeStructOf([]reflect.StructField{
		{Name: "M", Type: reflect.TypeFor[MapAllocUnit]()},
		{Name: "G", Type: groupRT},
	})
	if !synthOK {
		compositeCache.LoadOrStore(key, compositeEntry{nil, 0, 0})
		return nil, 0, 0, false
	}
	entry := compositeEntry{typ: comp, groupOff: comp.Field(1).Offset, stride: comp.Size()}
	actual, _ := compositeCache.LoadOrStore(key, entry)
	if actual.(compositeEntry).typ == nil {
		return nil, 0, 0, false
	}
	return actual.(compositeEntry).typ, actual.(compositeEntry).groupOff, actual.(compositeEntry).stride, true
}

// safeStructOf wraps reflect.StructOf with panic recovery so a synthesis failure
// (reflect refusing a field type, alignment edge case) degrades to the lazy
// fallback instead of crashing the process.
func safeStructOf(fields []reflect.StructField) (t reflect.Type, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			t = nil
			ok = false
		}
	}()
	t = reflect.StructOf(fields)
	ok = true
	return
}

// verifyCompositeMerge confirms that a Map allocated inside a composite
// {MapAllocUnit, Group} block, with dirPtr prewired to the in-block group,
// survives a full assign/readback/GC cycle. This is the load-bearing assumption
// of the composite path: dirPtr is an interior pointer into the same allocation
// that holds the Map, so the GC must resolve it to the unit base and scan the
// whole unit (Map fields plus the embedded group) as one object. If the layout
// or interior-pointer handling doesn't match what the runtime expects, the
// round-trip fails and compositeMergeOK stays false (PlanMapSlots falls back).
//
// The probe allocates the inner block itself (it runs before vbind exists) and
// inlines the same init logic as InitMapSlots for a single unit.
func verifyCompositeMerge() bool {
	mt := TypePtr(reflect.TypeFor[map[string]any]())
	comp, groupOff, stride, ok := compositeMapType(mt)
	if !ok {
		return false
	}
	inner := UnsafeNewArray(TypePtr(comp), 1)
	plan := MapSlotPlan{InnerType: TypePtr(comp), GroupOff: groupOff, Stride: stride}
	parentRType := TypePtr(reflect.TypeFor[unsafe.Pointer]())
	block := UnsafeNewArray(parentRType, 1)
	InitMapSlots(block, inner, nil, unsafe.Sizeof(unsafe.Pointer(nil)), plan, 1)
	unit := *(*unsafe.Pointer)(block)

	// Point a map[string]any header at our composite-allocated hmap. The map
	// type is carried statically; only the hmap storage is ours.
	var m map[string]any
	*(*unsafe.Pointer)(unsafe.Pointer(&m)) = unit
	m["__gort_merge_check__"] = 1
	v, got := m["__gort_merge_check__"]
	if !got || v != 1 {
		return false
	}
	if *(*uint64)(unit) != 1 { // Map.used == 1
		return false
	}
	runtime.GC()
	v, got = m["__gort_merge_check__"]
	return got && v == 1
}
