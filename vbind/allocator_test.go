package vbind

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/gort"
)

type pointee struct {
	X int
}

type elemKind struct {
	A, B int
}

func slotBytes(sc SlotClass, n uint32) uint32 { return n * sc.ElemSize }

func makeTreeForBatch(t *testing.T) *TypeTree {
	t.Helper()
	pty := gort.TypePtr(reflect.TypeFor[pointee]())
	return &TypeTree{
		Slots: []SlotTemplate{{
			Batch:    4,
			ElemSize: uint32(unsafe.Sizeof(pointee{})),
			RType:    pty,
		}},
	}
}

func makeTreeForGrow(t *testing.T) *TypeTree {
	t.Helper()
	ety := gort.TypePtr(reflect.TypeFor[elemKind]())
	bt := BindType{Kind: KindSlice}
	bt.Slice().ChildSize = uint32(unsafe.Sizeof(elemKind{}))
	meta := TypeMeta{Size: uint32(unsafe.Sizeof(elemKind{}))}
	sm := meta.SliceMeta()
	sm.ElemRType = ety
	sm.AllocClass = 0
	return &TypeTree{
		Types:    []BindType{bt},
		TypeMeta: []TypeMeta{meta},
		Slots: []SlotTemplate{{
			Batch:    4,
			ElemSize: uint32(unsafe.Sizeof(elemKind{})),
			RType:    ety,
		}},
	}
}

func TestNewAllocatorPrewarmSlots(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	if len(a.Slots) != 1 {
		t.Fatalf("Slots len = %d, want 1", len(a.Slots))
	}
	if a.Slots[0].Block == nil {
		t.Error("Slots[0].Block should be non-nil after prewarm")
	}
	if a.Slots[0].Offset != 0 {
		t.Errorf("Slots[0].Offset = %d, want 0", a.Slots[0].Offset)
	}
	if a.Slots[0].Limit != slotBytes(a.Slots[0], 4) {
		t.Errorf("Slots[0].Limit = %d, want %d (4 elems)", a.Slots[0].Limit, slotBytes(a.Slots[0], 4))
	}
	if a.Slots[0].Aux != slotBlockInitial {
		t.Errorf("Slots[0].Aux = %d, want %d (bump slice initial)", a.Slots[0].Aux, slotBlockInitial)
	}
	if len(a.MapBuf) == 0 {
		t.Error("MapBuf should be allocated")
	}
}

func TestServeNewBlockHandsOff(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	originalBlock := a.Slots[0].Block
	a.Slots[0].Offset = a.Slots[0].Limit

	if err := a.ServeNewBlock(0, 0); err != nil {
		t.Fatalf("ServeNewBlock err: %v", err)
	}
	if a.Slots[0].Block == originalBlock {
		t.Error("Block should be replaced after ServeNewBlock")
	}
	if a.Slots[0].Block == nil {
		t.Error("Block should be non-nil after ServeNewBlock")
	}
	if a.Slots[0].Offset != 0 {
		t.Errorf("Offset = %d, want 0", a.Slots[0].Offset)
	}
}

func TestServeNewBlockOOB(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	err := a.ServeNewBlock(99, 0)
	if err == nil {
		t.Fatal("ServeNewBlock(99) should return error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error msg should mention out of range, got: %v", err)
	}
}

func TestServeSliceGrowFirstAlloc(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	var hdr gort.SliceHeader
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("ServeSliceGrow err: %v", err)
	}
	if hdr.Data == nil {
		t.Error("hdr.Data should be non-nil after first grow")
	}
	const wantCap = slotBlockInitial
	if hdr.Cap != wantCap {
		t.Errorf("Cap = %d, want %d (cold-start: MuBlock=%d, floor=%d)", hdr.Cap, wantCap, slotBlockInitial, slotBlockFloor)
	}
	if hdr.Len != 0 {
		t.Errorf("Len = %d, want 0", hdr.Len)
	}
	if sc.Aux != wantCap {
		t.Errorf("MuBlock = %d, want %d (cold-start skips EWMA update)", sc.Aux, wantCap)
	}
	if sc.Offset != sc.Limit {
		t.Errorf("sc.Offset = %d, want %d (= sc.Limit, slice owns whole block)", sc.Offset, sc.Limit)
	}
}

func TestServeSliceGrowGeometric(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	var hdr gort.SliceHeader
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("init err: %v", err)
	}
	hdr.Len = int(slotBlockInitial)
	elems := (*[slotBlockInitial]elemKind)(hdr.Data)
	elems[0] = elemKind{A: 11, B: 22}
	elems[slotBlockInitial-1] = elemKind{A: 33, B: 44}

	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("grow err: %v", err)
	}
	const wantCap = SlotBatchMax
	if hdr.Cap != wantCap {
		t.Errorf("Cap = %d, want %d (max(MuBlock=%d, floor=%d, hdr.Len*2=%d), capped at SlotBatchMax)",
			hdr.Cap, wantCap, slotBlockInitial, slotBlockFloor, slotBlockInitial*2)
	}
	if hdr.Len != int(slotBlockInitial) {
		t.Errorf("Len = %d, want %d (preserved across grow)", hdr.Len, int(slotBlockInitial))
	}
	got := (*[SlotBatchMax]elemKind)(hdr.Data)
	if got[0] != (elemKind{A: 11, B: 22}) {
		t.Errorf("elems[0] not copied: %+v", got[0])
	}
	if got[slotBlockInitial-1] != (elemKind{A: 33, B: 44}) {
		t.Errorf("elems[slotBlockInitial-1] not copied: %+v", got[slotBlockInitial-1])
	}
}

func TestServeSliceGrowEWMA(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	// The small template block is below the EWMA training floor.
	var hdr gort.SliceHeader
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("cold-start err: %v", err)
	}
	if sc.Aux != slotBlockInitial {
		t.Fatalf("cold-start MuBlock = %d, want %d (EWMA skipped)", sc.Aux, slotBlockInitial)
	}

	sc.Len = 100
	hdr = gort.SliceHeader{}
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("err: %v", err)
	}
	const wantMu1 = uint32(0.5*100 + 0.5*float64(slotBlockInitial))
	if sc.Aux != wantMu1 {
		t.Errorf("MuBlock = %d, want %d (0.5*100+0.5*slotBlockInitial)", sc.Aux, wantMu1)
	}
	const wantCap1 = wantMu1
	if hdr.Cap != int(wantCap1) {
		t.Errorf("Cap = %d, want %d (max(MuBlock=%d, floor=%d), not capped below SlotBatchMax=%d)",
			hdr.Cap, wantCap1, wantMu1, slotBlockFloor, SlotBatchMax)
	}
	if sc.Len != 0 {
		t.Errorf("sc.Len = %d, want 0 (reset by allocBlock)", sc.Len)
	}
}

func TestServeSliceGrowSlotBatchMaxCeiling(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	sc.Aux = SlotBatchMax * 4
	var hdr gort.SliceHeader
	hdr.Len = 0
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hdr.Cap != SlotBatchMax {
		t.Errorf("Cap = %d, want %d (capped at SlotBatchMax on block path)", hdr.Cap, SlotBatchMax)
	}
}

func TestServeSliceGrowBypass(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	originalBlock := sc.Block
	originalOffset := sc.Offset
	originalMuBlock := sc.Aux

	var hdr gort.SliceHeader
	hdr.Len = SlotBatchMax
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("err: %v", err)
	}
	wantCap := 2 * SlotBatchMax
	if hdr.Cap != wantCap {
		t.Errorf("Cap = %d, want %d (standalone: needCap=hdr.Len*2)", hdr.Cap, wantCap)
	}
	if sc.Block != originalBlock {
		t.Error("standalone should not swap sc.Block")
	}
	if sc.Offset != originalOffset {
		t.Errorf("standalone moved Offset from %d to %d", originalOffset, sc.Offset)
	}
	if sc.Aux != originalMuBlock {
		t.Errorf("standalone updated MuBlock from %d to %d (should not affect EWMA)", originalMuBlock, sc.Aux)
	}
}

func makeTreeForRecBatchGrow(t *testing.T) *TypeTree {
	t.Helper()
	ety := gort.TypePtr(reflect.TypeFor[elemKind]())
	bt := BindType{Kind: KindSlice}
	bt.Slice().ChildSize = uint32(unsafe.Sizeof(elemKind{}))
	meta := TypeMeta{Size: uint32(unsafe.Sizeof(elemKind{}))}
	sm := meta.SliceMeta()
	sm.ElemRType = ety
	sm.AllocClass = 0
	return &TypeTree{
		Types:    []BindType{bt},
		TypeMeta: []TypeMeta{meta},
		Slots: []SlotTemplate{{
			Batch:    4,
			ElemSize: uint32(unsafe.Sizeof(elemKind{})),
			RType:    ety,
			Mode:     slotRecBatch,
			Group:    1,
		}},
		GroupCount: 1,
	}
}

func TestServeSliceGrow_RecBatchPath(t *testing.T) {
	tt := makeTreeForRecBatchGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	if sc.Mode != slotRecBatch {
		t.Fatal("Slots[0].Flags should have slotRecBatch mode set")
	}
	mat := sc.RecBatch().matrix()

	var hdr gort.SliceHeader
	if err := sc.RecBatch().ServeRefill(a, 0, &hdr); err != nil {
		t.Fatalf("init err: %v", err)
	}
	if hdr.Data == nil {
		t.Fatal("hdr.Data should be non-nil")
	}
	if hdr.Cap != 1 {
		t.Errorf("Cap = %d, want 1 (matrix row 0)", hdr.Cap)
	}
	row0Slots := recBatchRowSlots(0)
	if mat.Rows[0].FreeCount != row0Slots-1 {
		t.Errorf("row 0 FreeCount = %d, want %d", mat.Rows[0].FreeCount, row0Slots-1)
	}

	hdr.Len = 1
	elems := (*[1]elemKind)(hdr.Data)
	elems[0] = elemKind{A: 11, B: 22}
	oldData := hdr.Data

	if err := sc.RecBatch().ServeRefill(a, 1, &hdr); err != nil {
		t.Fatalf("grow err: %v", err)
	}
	if hdr.Cap != 2 {
		t.Errorf("Cap = %d, want 2 (matrix row 1)", hdr.Cap)
	}
	if hdr.Len != 1 {
		t.Errorf("Len = %d, want 1 (preserved across grow)", hdr.Len)
	}
	got := (*[2]elemKind)(hdr.Data)
	if got[0] != (elemKind{A: 11, B: 22}) {
		t.Errorf("element not copied: got[0]=%+v", got[0])
	}

	// Growing into another row must not make the old backing reusable.
	for ri := range mat.Rows {
		for i := range uint32(recBatchRowSlots(uint32(ri))) {
			isFree := mat.Rows[ri].Bitmap&(1<<i) != 0
			if isFree && sc.RecBatch().slotAddr(uint32(ri), uint32(i)) == oldData {
				t.Errorf("old backing recycled as free backing in row %d slot %d", ri, i)
			}
		}
	}
}

func TestServeSliceGrowNonRecBatch(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	if sc.Mode == slotRecBatch {
		t.Fatal("Slots[0].Flags should not have slotRecBatch mode set")
	}
	var hdr gort.SliceHeader
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("ServeSliceGrow err: %v", err)
	}
	if hdr.Cap != int(slotBlockInitial) {
		t.Errorf("Cap = %d, want %d (bump path, cold-start MuBlock=slotBlockInitial)", hdr.Cap, slotBlockInitial)
	}
}

func TestSlotClassRetentionChain(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	p0 := a.Slots[0].Block
	*(*pointee)(p0) = pointee{X: 0x1234}

	a.Slots[0].Offset = a.Slots[0].Limit
	if err := a.ServeNewBlock(0, 0); err != nil {
		t.Fatalf("ServeNewBlock err: %v", err)
	}
	// Every backing native may carve from is staged, the new ones and the
	// displaced ones alike, so assert membership rather than an exact count.
	if !retainedHas(a, p0) {
		t.Fatalf("displaced block %p not in retained", p0)
	}

	// Repeated GC exercises the temporary typed root. KeepAlive prevents the
	// allocator itself from becoming dead before the retained block is inspected.
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(a)

	val := (*pointee)(p0)
	if val.X != 0x1234 {
		t.Errorf("after GC, sentinel = %#x, want 0x1234 (retained chain must root the block)", val.X)
	}

	a.Release()
	if len(a.retained) != 0 {
		t.Errorf("after Release, len = %d, want 0", len(a.retained))
	}
}

func TestServeNewBlockMapSlot_GC(t *testing.T) {
	mt := gort.TypePtr(reflect.TypeFor[map[string]int]())
	tt := &TypeTree{
		Slots: []SlotTemplate{{
			Batch:    4,
			ElemSize: uint32(reflect.TypeFor[map[string]int]().Size()),
			RType:    mt,
			Flags:    SlotIsMap,
		}},
	}
	a := NewAllocator(tt)
	sc := &a.Slots[0]

	// Reproduce native map-header extraction from sc.Block plus sc.Offset.
	takeMap := func() map[string]int {
		mp := *(*unsafe.Pointer)(unsafe.Add(sc.Block, sc.Offset))
		sc.Offset += sc.ElemSize
		var m map[string]int
		*(*unsafe.Pointer)(unsafe.Pointer(&m)) = mp
		return m
	}

	m0 := takeMap()
	m1 := takeMap()
	m0["a"] = 11
	m1["b"] = 22

	oldBlock := sc.Block
	sc.Offset = sc.Limit
	if err := a.ServeNewBlock(0, 0); err != nil {
		t.Fatalf("ServeNewBlock err: %v", err)
	}

	// The scannable parent block is staged; its map pointers keep the inner units
	// reachable without separate roots.
	if !retainedHas(a, oldBlock) {
		t.Fatalf("displaced block %p not in retained", oldBlock)
	}

	// Published map headers and retained parent pointers independently root the
	// inner units during forced GC.
	runtime.GC()
	runtime.GC()
	if v := m0["a"]; v != 11 {
		t.Errorf("m0[a] = %d after GC, want 11 (old block map corrupted)", v)
	}
	if v := m1["b"]; v != 22 {
		t.Errorf("m1[b] = %d after GC, want 22 (old block map corrupted)", v)
	}

	m2 := takeMap()
	m2["c"] = 33
	runtime.GC()
	if v := m2["c"]; v != 33 {
		t.Errorf("m2[c] = %d after GC, want 33 (new block map corrupted)", v)
	}
}

func TestReleaseRetainedNoopWhenEmpty(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)
	a.Release()
	if len(a.retained) != 0 {
		t.Errorf("len(retained) = %d, want 0", len(a.retained))
	}
}

// TestStagingCoversDisplacedBlock pins that a block leaving sc.Block is staged.
// It is the case that needs a root rather than only a shade: after the swap the
// allocator no longer references it, and native may have published pointers into
// it that live only in the noscan map buffer, which roots nothing until drain.
//
// Both installBlock entries are covered, the BLOCK_FULL yield and the slice grow,
// because they are one mechanism and a regression in either loses the root.
func TestStagingCoversDisplacedBlock(t *testing.T) {
	t.Run("block full", func(t *testing.T) {
		a := NewAllocator(makeTreeForBatch(t))
		a.Release() // drop the construction-time staging
		b0 := a.Slots[0].Block

		a.Slots[0].Offset = a.Slots[0].Limit
		if err := a.ServeNewBlock(0, 0); err != nil {
			t.Fatalf("ServeNewBlock: %v", err)
		}
		if !retainedHas(a, b0) {
			t.Errorf("displaced block %p not staged", b0)
		}
		if !retainedHas(a, a.Slots[0].Block) {
			t.Errorf("new block %p not staged", a.Slots[0].Block)
		}
	})

	t.Run("slice grow", func(t *testing.T) {
		a := NewAllocator(makeTreeForGrow(t))
		a.Release()
		sc := &a.Slots[0]
		b0 := sc.Block

		var hdr gort.SliceHeader
		if err := a.ServeSliceGrow(sc, &hdr); err != nil {
			t.Fatalf("ServeSliceGrow: %v", err)
		}
		if !retainedHas(a, b0) {
			t.Errorf("displaced block %p not staged", b0)
		}
		if !retainedHas(a, sc.Block) {
			t.Errorf("new block %p not staged", sc.Block)
		}
	})
}

func TestServeStrArenaFirstAllocSizesToAmortize(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	const srcLen = 1000
	a.EnsureStrArena(srcLen)
	buf := a.StrArena

	wantCap := strArenaAmortize*srcLen + strArenaTail
	if cap(buf) != wantCap {
		t.Errorf("first alloc cap = %d, want %d (strArenaAmortize*srcLen+strArenaTail)", cap(buf), wantCap)
	}
}

func TestServeStrArenaReusesAcrossParses(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	const srcLen = 1000
	a.EnsureStrArena(srcLen)
	buf1 := a.StrArena
	base := unsafe.Pointer(unsafe.SliceData(buf1))

	a.CommitStrArena(srcLen)

	a.EnsureStrArena(srcLen)
	buf2 := a.StrArena
	if unsafe.Pointer(unsafe.SliceData(buf2)) != unsafe.Add(base, srcLen) {
		t.Errorf("parse 2 view start = %p, want %p (bump cursor advanced by srcLen)",
			unsafe.SliceData(buf2), unsafe.Add(base, srcLen))
	}
	if uintptr(cap(buf2)) != strArenaAmortize*srcLen+strArenaTail-srcLen {
		t.Errorf("parse 2 cap = %d, want %d (residual after parse 1)",
			cap(buf2), strArenaAmortize*srcLen+strArenaTail-srcLen)
	}

	for i := 2; i < strArenaAmortize; i++ {
		a.CommitStrArena(srcLen)
		a.EnsureStrArena(srcLen)
		bufN := a.StrArena
		if uintptr(cap(bufN)) < srcLen+strArenaTail {
			t.Errorf("parse %d cap = %d, want >= %d (must fit one more parse)", i, cap(bufN), srcLen+strArenaTail)
		}
	}
	a.CommitStrArena(srcLen)
	a.EnsureStrArena(srcLen)
	bufNext := a.StrArena
	if uintptr(cap(bufNext)) != strArenaAmortize*srcLen+strArenaTail {
		t.Errorf("after exhaustion, grow should reset cap to %d, got %d",
			strArenaAmortize*srcLen+strArenaTail, cap(bufNext))
	}
}

func TestServeStrArenaGrowDropsAllocatorRef(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	a.EnsureStrArena(100)
	buf1 := a.StrArena
	oldData := unsafe.Pointer(unsafe.SliceData(buf1))
	growSrcLen := strArenaAmortize * 100
	a.CommitStrArena(100)

	a.EnsureStrArena(growSrcLen)
	if unsafe.Pointer(unsafe.SliceData(a.StrArena)) == oldData {
		t.Error("grow should install a new backing, not return the old one")
	}
	// The published Value slice header is the sole root after arena replacement.
	if unsafe.Pointer(unsafe.SliceData(buf1)) != oldData {
		t.Errorf("buf1 data = %p, want %p (Value alias must still reference old backing)",
			unsafe.SliceData(buf1), oldData)
	}
}

func TestServeStrArenaGrowsWhenSrcLenExceedsBacking(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	a.EnsureStrArena(100)
	a.CommitStrArena(100)

	a.EnsureStrArena(5000)
	buf := a.StrArena
	if uintptr(cap(buf)) != strArenaAmortize*5000+strArenaTail {
		t.Errorf("after grow for srcLen=5000, cap = %d, want %d",
			cap(buf), strArenaAmortize*5000+strArenaTail)
	}
}

func TestCommitStrArenaAdvancesCursor(t *testing.T) {
	tt := makeTreeForBatch(t)
	a := NewAllocator(tt)

	a.EnsureStrArena(1000)
	buf1 := a.StrArena
	buf1[0] = 0xAB

	a.CommitStrArena(400)
	a.EnsureStrArena(1000)
	buf2 := a.StrArena
	if &buf2[0] != &buf1[400] {
		t.Errorf("parse 2 view start = %p, want %p (cursor advanced by 400)", &buf2[0], &buf1[400])
	}
	if buf1[0] != 0xAB {
		t.Errorf("sentinel byte = %#x, want 0xAB (prior region must not be overwritten)", buf1[0])
	}
}

func makeTreeForGrowRecursive(t *testing.T) *TypeTree {
	t.Helper()
	tt := makeTreeForGrow(t)
	tt.GroupCount = 1
	return tt
}

func TestNewAllocatorSmartDefaultNonRecursive(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt)
	if a.slotBatchMax != SlotBatchMax {
		t.Errorf("non-recursive slotBatchMax = %d, want %d (SlotBatchMax)", a.slotBatchMax, SlotBatchMax)
	}
}

func TestNewAllocatorSmartDefaultRecursive(t *testing.T) {
	tt := makeTreeForRecBatchGrow(t)
	a := NewAllocator(tt)
	if a.slotBatchMax != defaultSlotBatchRecursive {
		t.Errorf("recursive slotBatchMax = %d, want %d (defaultSlotBatchRecursive)", a.slotBatchMax, defaultSlotBatchRecursive)
	}
}

func TestWithSlotBatchMaxOverride(t *testing.T) {
	t.Run("non-recursive", func(t *testing.T) {
		tt := makeTreeForGrow(t)
		a := NewAllocator(tt, WithSlotBatchMax(64))
		if a.slotBatchMax != 64 {
			t.Errorf("slotBatchMax = %d, want 64 (overridden)", a.slotBatchMax)
		}
	})
	t.Run("recursive", func(t *testing.T) {
		tt := makeTreeForGrowRecursive(t)
		a := NewAllocator(tt, WithSlotBatchMax(64))
		if a.slotBatchMax != 64 {
			t.Errorf("slotBatchMax = %d, want 64 (override beats smart default)", a.slotBatchMax)
		}
	})
}

func TestWithSlotBatchMaxClamped(t *testing.T) {
	tt := makeTreeForGrow(t)
	a := NewAllocator(tt, WithSlotBatchMax(0))
	if a.slotBatchMax != SlotBatchMax {
		t.Errorf("slotBatchMax(0) = %d, want %d (0 clamps to ceiling)", a.slotBatchMax, SlotBatchMax)
	}
	a = NewAllocator(tt, WithSlotBatchMax(SlotBatchMax*2))
	if a.slotBatchMax != SlotBatchMax {
		t.Errorf("slotBatchMax(2x) = %d, want %d (oversize clamps to ceiling)", a.slotBatchMax, SlotBatchMax)
	}
}

func TestServeSliceGrowRecursiveTreeUsesSmallerBatch(t *testing.T) {
	tt := makeTreeForGrowRecursive(t)
	a := NewAllocator(tt)
	if a.slotBatchMax != defaultSlotBatchRecursive {
		t.Fatalf("slotBatchMax = %d, want %d", a.slotBatchMax, defaultSlotBatchRecursive)
	}
	sc := &a.Slots[0]

	sc.Aux = defaultSlotBatchRecursive * 4
	var hdr gort.SliceHeader
	hdr.Len = 0
	if err := a.ServeSliceGrow(sc, &hdr); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hdr.Cap != int(defaultSlotBatchRecursive) {
		t.Errorf("Cap = %d, want %d (capped at recursive slotBatchMax, not SlotBatchMax=%d)",
			hdr.Cap, defaultSlotBatchRecursive, SlotBatchMax)
	}
}

// retainedHas reports whether ptr is staged in retained. Staging covers every
// backing native may carve from, so tests assert membership, not an exact count.
func retainedHas(a *Allocator, ptr unsafe.Pointer) bool {
	for _, p := range a.retained {
		if p == ptr {
			return true
		}
	}
	return false
}

// TestReleaseScopedKeepsLiveBackingsStaged pins the invariant that makes a
// mid-parse release safe: a backing native is still carving from must remain
// staged, so the next barrier shades it again.
//
// This is asserted structurally rather than through a GC stress test because the
// hazard is a lost write barrier, which only manifests if a cycle happens to be
// marking during the release window. That window is far too narrow to hit
// reliably, so a passing stress run proves nothing; the staging set, on the
// other hand, is directly observable.
func TestReleaseScopedKeepsLiveBackingsStaged(t *testing.T) {
	a := NewAllocator(makeTreeForBatch(t))
	sc := &a.Slots[0]

	// Carve past the initial block so a replaced backing exists to be dropped,
	// and a fresh one is live.
	for range int(sc.Cap) + 1 {
		if _, err := a.Carve(0); err != nil {
			t.Fatalf("Carve: %v", err)
		}
	}
	a.EnsureStrArena(1024)

	mark := a.RetainMark()
	// Exhaust the live block so the release has a replaced backing to drop.
	for range int(a.Slots[0].Cap) + 1 {
		if _, err := a.Carve(0); err != nil {
			t.Fatalf("Carve: %v", err)
		}
	}
	before := len(a.retained)
	if before <= mark {
		t.Fatalf("nothing staged above mark: len=%d mark=%d", before, mark)
	}

	a.ReleaseScoped(mark)

	if len(a.retained) != mark {
		t.Errorf("retained = %d, want truncated to mark %d", len(a.retained), mark)
	}

	staged := make(map[unsafe.Pointer]bool, len(a.retained)+len(a.live))
	for _, p := range a.retained {
		staged[p] = true
	}
	for _, p := range a.live {
		staged[p] = true
	}
	if b := a.Slots[0].Block; b == nil || !staged[b] {
		t.Error("the block native still carves from is no longer staged; its next unbarriered write would go unshaded")
	}
	if arena := unsafe.Pointer(unsafe.SliceData(a.StrArena)); !staged[arena] {
		t.Error("the string arena native still interns into is no longer staged")
	}

	// Repeated releases must not accumulate: that is what makes a stream of
	// sibling scopes bounded rather than merely bounded per scope.
	first := len(a.retained) + len(a.live)
	for range 50 {
		a.ReleaseScoped(mark)
	}
	if got := len(a.retained) + len(a.live); got != first {
		t.Errorf("staging grew over 50 releases: %d then %d", first, got)
	}

	// Release drops retained outright but restages live: the shade for what it
	// drops is fused into the rebuild, so the set that comes out is the one the
	// next parse needs, not an empty one.
	a.Release()
	if len(a.retained) != 0 {
		t.Errorf("after Release: retained=%d, want 0", len(a.retained))
	}
	restaged := make(map[unsafe.Pointer]bool, len(a.live))
	for _, p := range a.live {
		restaged[p] = true
	}
	if b := a.Slots[0].Block; b == nil || !restaged[b] {
		t.Error("after Release: the block native still carves from is not staged")
	}
	if arena := unsafe.Pointer(unsafe.SliceData(a.StrArena)); !restaged[arena] {
		t.Error("after Release: the string arena native still interns into is not staged")
	}
}
