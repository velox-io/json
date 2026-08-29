package gort

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// zeroMapUnit zeroes a MapAllocUnit-sized region. UnsafeNewArray already
// zeroes, but AllocsPerRun reuses the same slot across iterations.
func zeroMapUnit(p unsafe.Pointer) {
	*(*[48]byte)(p) = [48]byte{}
}

// TestBatchMapAlloc_SizeMatch verifies that MapAllocUnit's size is consistent
// with the real Map struct. We can't name Map directly, but we detect a
// mismatch by allocating adjacent MapAllocUnits and confirming maps don't
// collide.
func TestBatchMapAlloc_SizeMatch(t *testing.T) {
	const N = 4
	unitSize := unsafe.Sizeof(MapAllocUnit{})
	t.Logf("MapAllocUnit size = %d", unitSize)

	rtype := TypePtr(reflect.TypeFor[MapAllocUnit]())
	block := UnsafeNewArray(rtype, N)
	if block == nil {
		t.Fatal("UnsafeNewArray returned nil")
	}

	mt := TypePtr(reflect.TypeFor[map[string]int]())

	// Initialize N maps into the block, each at stride MapAllocUnit.
	maps := make([]unsafe.Pointer, N)
	for i := 0; i < N; i++ {
		slot := unsafe.Add(block, uintptr(i)*unitSize)
		zeroMapUnit(slot)
		maps[i] = MakeMap(mt, 0, slot)
		if maps[i] != slot {
			t.Fatalf("map %d: MakeMap returned %p, want %p (m != nil must return m)", i, maps[i], slot)
		}
	}

	// Write distinct values into each map. If MapAllocUnit is too small,
	// adjacent maps' fields would collide and corrupt each other.
	for i := 0; i < N; i++ {
		slot := MapAssignFastStr(mt, maps[i], "k")
		*(*int)(slot) = (i + 1) * 100
	}

	// Read back and verify no cross-contamination.
	for i := 0; i < N; i++ {
		slot := MapAssignFastStr(mt, maps[i], "k")
		want := (i + 1) * 100
		if got := *(*int)(slot); got != want {
			t.Fatalf("map %d: m[k] = %d, want %d (size mismatch corrupting adjacent maps)",
				i, got, want)
		}
	}
	t.Logf("N=%d maps in one block, no cross-contamination", N)
}

// TestBatchMapAlloc_ZeroAlloc verifies MakeMap(t, 0, m) with m != nil performs
// ZERO heap allocations (skips new(Map), only writes seed).
func TestBatchMapAlloc_ZeroAlloc(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())
	rtype := TypePtr(reflect.TypeFor[MapAllocUnit]())

	// Pre-allocate one unit (this allocation is NOT counted in AllocsPerRun).
	block := UnsafeNewArray(rtype, 1)
	slot := block

	allocs := testing.AllocsPerRun(10, func() {
		zeroMapUnit(slot)
		MakeMap(mt, 0, slot)
	})

	if allocs != 0 {
		t.Fatalf("MakeMap(t, 0, m!=nil) allocated %v objects, want 0", allocs)
	}
	t.Logf("MakeMap(t, 0, m!=nil): %.1f allocs (want 0)", allocs)

	// Contrast: MakeMap(t, 0, nil) should allocate 1 (new(Map)).
	allocsNil := testing.AllocsPerRun(10, func() {
		MakeMap(mt, 0, nil)
	})
	t.Logf("MakeMap(t, 0, nil):    %.1f allocs (baseline)", allocsNil)
}

// TestBatchMapAlloc_BatchCount verifies that creating N maps from one
// pre-allocated block costs exactly 1 allocation (the block itself),
// not N allocations (one new(Map) per map).
func TestBatchMapAlloc_BatchCount(t *testing.T) {
	const N = 64
	mt := TypePtr(reflect.TypeFor[map[string]int]())
	rtype := TypePtr(reflect.TypeFor[MapAllocUnit]())
	unitSize := unsafe.Sizeof(MapAllocUnit{})

	allocs := testing.AllocsPerRun(5, func() {
		// One big allocation for N Map structs.
		block := UnsafeNewArray(rtype, N)
		// Initialize all N maps: each should be zero alloc.
		for i := 0; i < N; i++ {
			slot := unsafe.Add(block, uintptr(i)*unitSize)
			zeroMapUnit(slot)
			MakeMap(mt, 0, slot)
		}
	})

	t.Logf("batch create %d maps: %.1f allocs (want 1.0)", N, allocs)
	if allocs > 1.5 {
		t.Fatalf("batch create allocated %.1f objects, want ~1 (the block)", allocs)
	}
}

// TestBatchMapAlloc_Functional verifies maps created from a batch block are
// fully functional: insert, lookup, len, multiple keys, GC survival.
func TestBatchMapAlloc_Functional(t *testing.T) {
	const N = 8
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	rtype := TypePtr(reflect.TypeFor[MapAllocUnit]())
	unitSize := unsafe.Sizeof(MapAllocUnit{})

	block := UnsafeNewArray(rtype, N)
	mps := make([]unsafe.Pointer, N)
	for i := 0; i < N; i++ {
		slot := unsafe.Add(block, uintptr(i)*unitSize)
		zeroMapUnit(slot)
		mps[i] = MakeMap(mt, 0, slot)
	}

	// Insert distinct entries into each map.
	for i := 0; i < N; i++ {
		for j := 0; j < 5; j++ {
			key := "key" + itoa(j)
			val := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			*(*string)(slot) = val
		}
	}

	// Verify lengths.
	for i := 0; i < N; i++ {
		if l := MapLen(mps[i]); l != 5 {
			t.Fatalf("map %d: len=%d, want 5", i, l)
		}
	}

	// Verify values.
	for i := 0; i < N; i++ {
		for j := 0; j < 5; j++ {
			key := "key" + itoa(j)
			want := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			if got := *(*string)(slot); got != want {
				t.Fatalf("map %d [%s] = %q, want %q", i, key, got, want)
			}
		}
	}

	// Force GC and re-verify: dirPtr (the only GC pointer in Map) must survive.
	runtime.GC()
	runtime.GC()
	for i := 0; i < N; i++ {
		for j := 0; j < 5; j++ {
			key := "key" + itoa(j)
			want := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			if got := *(*string)(slot); got != want {
				t.Fatalf("post-GC map %d [%s] = %q, want %q (GC corruption)", i, key, got, want)
			}
		}
	}
	t.Logf("N=%d maps, 5 entries each, survived 2x GC with correct values", N)
}

// TestBatchMapAlloc_RealWorldUsage simulates the actual initMapSlots pattern:
// allocate a batch block, fill with MakeMap, then drain entries into maps.
func TestBatchMapAlloc_RealWorldUsage(t *testing.T) {
	const batch = 32
	mt := TypePtr(reflect.TypeFor[map[string]int]())
	rtype := TypePtr(reflect.TypeFor[MapAllocUnit]())
	unitSize := unsafe.Sizeof(MapAllocUnit{})

	// Batch-allocate Map structs (1 allocation).
	mapStructBlock := UnsafeNewArray(rtype, batch)

	// Consume 6 maps (like KubePods: 3 pods × 2 maps).
	used := make([]unsafe.Pointer, 6)
	for i := 0; i < 6; i++ {
		slot := unsafe.Add(mapStructBlock, uintptr(i)*unitSize)
		zeroMapUnit(slot)
		used[i] = MakeMap(mt, 0, slot)
	}

	// Drain entries into each map (like drainKVSlots).
	for i := 0; i < 6; i++ {
		for j := 0; j < 3; j++ {
			key := "k" + itoa(j)
			slot := MapAssignFastStr(mt, used[i], key)
			*(*int)(slot) = i*10 + j
		}
	}

	// Verify after GC.
	runtime.GC()
	for i := 0; i < 6; i++ {
		if l := MapLen(used[i]); l != 3 {
			t.Fatalf("map %d: len=%d, want 3", i, l)
		}
		for j := 0; j < 3; j++ {
			key := "k" + itoa(j)
			want := i*10 + j
			slot := MapAssignFastStr(mt, used[i], key)
			if got := *(*int)(slot); got != want {
				t.Fatalf("map %d [%s] = %d, want %d", i, key, got, want)
			}
		}
	}
	t.Logf("batch=%d, consumed=6, 3 entries each, all correct after GC", batch)
}

// ptrRType is the rtype for an 8-byte pointer-stride block used as the parent
// SlotClass block when testing InitMapSlots in isolation.
var ptrRType = TypePtr(reflect.TypeFor[unsafe.Pointer]())

const ptrSize = unsafe.Sizeof(unsafe.Pointer(nil))

// initMapSlotsBatch allocates the parent block and inner block(s) (PlanMapSlots
// + UnsafeNewArray, mirroring vbind's allocator), calls InitMapSlots, and
// returns the N *hmap pointers it wrote into the parent block. On the two-block
// path it also allocates the group block.
func initMapSlotsBatch(t *testing.T, mt unsafe.Pointer, n int) []unsafe.Pointer {
	t.Helper()
	block := UnsafeNewArray(ptrRType, n)
	plan := PlanMapSlots(mt)
	inner := UnsafeNewArray(plan.InnerType, n)
	var groupBlock unsafe.Pointer
	if plan.GroupOff == 0 && plan.GroupType != nil {
		groupBlock = UnsafeNewArray(plan.GroupType, n)
	}
	InitMapSlots(block, inner, groupBlock, ptrSize, plan, n)
	out := make([]unsafe.Pointer, n)
	for i := 0; i < n; i++ {
		out[i] = *(*unsafe.Pointer)(unsafe.Add(block, uintptr(i)*ptrSize))
	}
	return out
}

// TestSmallMapPrewire_FirstAssignZeroAlloc is the core claim: with dirPtr
// prewired to a ctrl-empty group, the FIRST mapassign takes the small-map path
// (putSlotSmall) and skips growToSmall, so it allocates nothing. The lazy
// contrast below confirms the same operation costs 1 alloc without prewiring.
func TestSmallMapPrewire_FirstAssignZeroAlloc(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]any]())

	if !smallMapPrewireOK {
		// Lazy baseline: MakeMap(t, 0, slot) leaves dirPtr=nil, so first assign
		// triggers growToSmall. Confirms the cost the composite path removes.
		const N = 128
		block := UnsafeNewArray(mapAllocUnitRType, N)
		lazy := make([]unsafe.Pointer, N)
		for i := 0; i < N; i++ {
			slot := unsafe.Add(block, uintptr(i)*unsafe.Sizeof(MapAllocUnit{}))
			zeroMapUnit(slot)
			lazy[i] = MakeMap(mt, 0, slot)
		}
		idx := 0
		allocs := testing.AllocsPerRun(100, func() {
			MapAssignFastStr(mt, lazy[idx], "k")
			idx++
		})
		t.Logf("prewire disabled: first assign on lazy map = %.1f allocs (growToSmall)", allocs)
		t.Skip("map prewire disabled on this runtime")
	}

	const N = 128
	mps := initMapSlotsBatch(t, mt, N)

	idx := 0
	allocs := testing.AllocsPerRun(100, func() {
		MapAssignFastStr(mt, mps[idx], "k")
		idx++
	})
	t.Logf("prewire enabled: first assign on prewired map = %.1f allocs (want 0)", allocs)
	if allocs > 0.5 {
		t.Fatalf("prewired first assign allocated %.1f objects, want 0 (growToSmall not skipped)", allocs)
	}
}

// TestSmallMapPrewire_GC verifies the group block stays rooted through GC via
// each map's dirPtr (an interior pointer into the batch-allocated group block).
// If the GC did not resolve the interior pointer to the block base, the groups
// would be collected and the maps would corrupt after GC.
func TestSmallMapPrewire_GC(t *testing.T) {
	if !smallMapPrewireOK {
		t.Skip("map prewire disabled on this runtime")
	}
	const N = 8
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mps := initMapSlotsBatch(t, mt, N)

	for i := 0; i < N; i++ {
		for j := 0; j < 5; j++ { // stays within the 8-slot small-map group
			key := "key" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			*(*string)(slot) = "v" + itoa(i) + "_" + itoa(j)
		}
	}

	runtime.GC()
	runtime.GC()
	for i := 0; i < N; i++ {
		if l := MapLen(mps[i]); l != 5 {
			t.Fatalf("map %d: len=%d after GC, want 5 (group block not retained)", i, l)
		}
		for j := 0; j < 5; j++ {
			key := "key" + itoa(j)
			want := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			if got := *(*string)(slot); got != want {
				t.Fatalf("map %d [%s] = %q after GC, want %q (group collected/corrupted)", i, key, got, want)
			}
		}
	}
	t.Logf("N=%d prewired maps, 5 entries each, survived 2x GC", N)
}

// TestSmallMapPrewire_BatchAllocCount verifies the vbind allocator's allocation
// count per map slot batch: 2 (parent + inner) on the composite and lazy paths,
// 3 (parent + mapBlock + groupBlock) on the two-block prewire path. InitMapSlots
// itself is zero alloc.
func TestSmallMapPrewire_BatchAllocCount(t *testing.T) {
	const N = 32
	mt := TypePtr(reflect.TypeFor[map[string]any]())

	allocs := testing.AllocsPerRun(5, func() {
		block := UnsafeNewArray(ptrRType, N)
		plan := PlanMapSlots(mt)
		inner := UnsafeNewArray(plan.InnerType, N)
		var groupBlock unsafe.Pointer
		if plan.GroupOff == 0 && plan.GroupType != nil {
			groupBlock = UnsafeNewArray(plan.GroupType, N)
		}
		InitMapSlots(block, inner, groupBlock, ptrSize, plan, N)
	})

	want := 2.0 // parent + inner (composite or lazy)
	if smallMapPrewireOK && !compositeMergeOK {
		want = 3.0 // + groupBlock (two-block prewire)
	}
	t.Logf("InitMapSlots batch=%d: %.1f allocs (want ~%.0f, prewire=%v, merge=%v)",
		N, allocs, want, smallMapPrewireOK, compositeMergeOK)
	if allocs > want+0.5 || allocs < want-0.5 {
		t.Fatalf("InitMapSlots allocated %.1f objects, want ~%.0f", allocs, want)
	}
}

// TestSmallMapPrewire_Functional exercises prewired maps end-to-end: distinct
// keys per map, cross-map isolation, and len correctness.
func TestSmallMapPrewire_Functional(t *testing.T) {
	if !smallMapPrewireOK {
		t.Skip("map prewire disabled on this runtime")
	}
	const N = 8
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mps := initMapSlotsBatch(t, mt, N)

	for i := 0; i < N; i++ {
		for j := 0; j < 6; j++ {
			key := "key" + itoa(j)
			val := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			*(*string)(slot) = val
		}
	}
	for i := 0; i < N; i++ {
		if l := MapLen(mps[i]); l != 6 {
			t.Fatalf("map %d: len=%d, want 6", i, l)
		}
		for j := 0; j < 6; j++ {
			key := "key" + itoa(j)
			want := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			if got := *(*string)(slot); got != want {
				t.Fatalf("map %d [%s] = %q, want %q", i, key, got, want)
			}
		}
	}
}

func TestSmallMapPrewire_Flag(t *testing.T) {
	t.Logf("smallMapPrewireOK = %v (SwissMapLayoutOK = %v)", smallMapPrewireOK, SwissMapLayoutOK)
}

func TestCompositeMerge_Flag(t *testing.T) {
	t.Logf("compositeMergeOK = %v (smallMapPrewireOK = %v)", compositeMergeOK, smallMapPrewireOK)
}

// TestCompositeMerge_GC exercises the merged single-allocation path: the
// composite block holds each Map and its group in one unit, with dirPtr as an
// interior pointer into that unit. If the GC failed to resolve dirPtr to the
// unit base and scan the embedded group, the group's key/elem storage would be
// unreachable and collected, and the post-GC readback would corrupt.
func TestCompositeMerge_GC(t *testing.T) {
	if !compositeMergeOK {
		t.Skip("composite merge disabled on this runtime")
	}
	const N = 8
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mps := initMapSlotsBatch(t, mt, N)

	for i := 0; i < N; i++ {
		for j := 0; j < 5; j++ { // stays within the 8-slot small-map group
			key := "key" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			*(*string)(slot) = "v" + itoa(i) + "_" + itoa(j)
		}
	}

	runtime.GC()
	runtime.GC()
	for i := 0; i < N; i++ {
		if l := MapLen(mps[i]); l != 5 {
			t.Fatalf("map %d: len=%d after GC, want 5 (composite unit not retained)", i, l)
		}
		for j := 0; j < 5; j++ {
			key := "key" + itoa(j)
			want := "v" + itoa(i) + "_" + itoa(j)
			slot := MapAssignFastStr(mt, mps[i], key)
			if got := *(*string)(slot); got != want {
				t.Fatalf("map %d [%s] = %q after GC, want %q (group slot collected/corrupted)", i, key, got, want)
			}
		}
	}
	t.Logf("N=%d merged maps, 5 entries each, survived 2x GC", N)
}

// TestCompositeMerge_DifferentKV verifies the composite cache synthesizes a
// distinct type per K/V and that several K/V combinations round-trip through
// InitMapSlots without cross-contamination.
func TestCompositeMerge_DifferentKV(t *testing.T) {
	if !compositeMergeOK {
		t.Skip("composite merge disabled on this runtime")
	}
	type kvCase struct {
		name   string
		mt     unsafe.Pointer
		fill   func(mp unsafe.Pointer)
		expect func(mp unsafe.Pointer) bool
	}
	cases := []kvCase{
		{
			name: "str_int64",
			mt:   TypePtr(reflect.TypeFor[map[string]int64]()),
			fill: func(mp unsafe.Pointer) {
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]int64]()), mp, "k")
				*(*int64)(slot) = 777
			},
			expect: func(mp unsafe.Pointer) bool {
				if MapLen(mp) != 1 {
					return false
				}
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]int64]()), mp, "k")
				return *(*int64)(slot) == 777
			},
		},
		{
			name: "str_str",
			mt:   TypePtr(reflect.TypeFor[map[string]string]()),
			fill: func(mp unsafe.Pointer) {
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]string]()), mp, "k")
				*(*string)(slot) = "sv"
			},
			expect: func(mp unsafe.Pointer) bool {
				if MapLen(mp) != 1 {
					return false
				}
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]string]()), mp, "k")
				return *(*string)(slot) == "sv"
			},
		},
		{
			name: "str_any",
			mt:   TypePtr(reflect.TypeFor[map[string]any]()),
			fill: func(mp unsafe.Pointer) {
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]any]()), mp, "k")
				*(*any)(slot) = "av"
			},
			expect: func(mp unsafe.Pointer) bool {
				if MapLen(mp) != 1 {
					return false
				}
				slot := MapAssignFastStr(TypePtr(reflect.TypeFor[map[string]any]()), mp, "k")
				v, ok := (*(*any)(slot)).(string)
				return ok && v == "av"
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mps := initMapSlotsBatch(t, c.mt, 4)
			for i := range mps {
				c.fill(mps[i])
			}
			runtime.GC()
			runtime.GC()
			for i := range mps {
				if !c.expect(mps[i]) {
					t.Fatalf("case %s map %d: readback mismatch after GC", c.name, i)
				}
			}
		})
	}
}
