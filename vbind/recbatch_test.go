package vbind

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/gort"
)

func newRecBatchSlotForTest(rtype unsafe.Pointer, elemSize uintptr) *SlotClass {
	var sc SlotClass
	*(*RecBatchSlotClass)(unsafe.Pointer(&sc)) = newRecBatchSlotClass(SlotTemplate{
		ElemSize: uint32(elemSize),
		RType:    rtype,
		Mode:     slotRecBatch,
	})
	return &sc
}

func TestRecBatchMatrixInit(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[int]()), unsafe.Sizeof(int(0)))
	mat := sc.RecBatch().matrix()
	if len(mat.Rows) != recBatchRows {
		t.Fatalf("len(Rows) = %d, want %d", len(mat.Rows), recBatchRows)
	}
	for ri := range mat.Rows {
		if got := recBatchRowCap(uint32(ri)); got != uint32(1)<<uint32(ri) {
			t.Errorf("row %d derived Cap = %d, want %d", ri, got, uint32(1)<<uint32(ri))
		}
		if mat.Rows[ri].Base != nil {
			t.Errorf("row %d Base = %v, want nil (no allocation until refill)", ri, mat.Rows[ri].Base)
		}
		if mat.Rows[ri].FreeCount != 0 {
			t.Errorf("row %d FreeCount = %d, want 0 (empty init)", ri, mat.Rows[ri].FreeCount)
		}
		slots := recBatchRowSlots(uint32(ri))
		for i := range slots {
			if mat.Rows[ri].Bitmap&(1<<i) != 0 {
				t.Errorf("row %d slot %d bitmap bit set at init (want used=0)", ri, i)
			}
		}
	}
}

func TestRecBatchTakeFromRowEmptyReturnsFalse(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[int]()), unsafe.Sizeof(int(0)))
	if bk, ok := sc.RecBatch().take(0); ok || bk != nil {
		t.Fatalf("take on empty row = (%v, %v), want (nil, false)", bk, ok)
	}
}

func TestRecBatchRefillAndTake(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[int]()), unsafe.Sizeof(int(0)))
	mat := sc.RecBatch().matrix()

	sc.RecBatch().refillRow(nil, 0)
	slots := recBatchRowSlots(0)
	if mat.Rows[0].Base == nil {
		t.Fatal("Base nil after refill")
	}
	if mat.Rows[0].FreeCount != slots {
		t.Fatalf("FreeCount after refill = %d, want %d", mat.Rows[0].FreeCount, slots)
	}
	for i := range slots {
		if mat.Rows[0].Bitmap&(1<<i) == 0 {
			t.Errorf("slot %d bitmap not free after refill", i)
		}
	}

	bk, ok := sc.RecBatch().take(0)
	if !ok || bk == nil {
		t.Fatalf("take = (%v, %v), want a backing", bk, ok)
	}
	if mat.Rows[0].FreeCount != slots-1 {
		t.Errorf("FreeCount after take = %d, want %d", mat.Rows[0].FreeCount, slots-1)
	}
	found := false
	for i := range slots {
		if sc.RecBatch().slotAddr(0, i) == bk {
			found = true
			if mat.Rows[0].Bitmap&(1<<i) != 0 {
				t.Errorf("slot %d bitmap still free after take (want used)", i)
			}
		}
	}
	if !found {
		t.Fatal("handed-out backing is not a row-0 slot address")
	}
}

func TestRecBatchRefillReplacesBase(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[int]()), unsafe.Sizeof(int(0)))
	mat := sc.RecBatch().matrix()
	sc.RecBatch().refillRow(nil, 2)
	oldBase := mat.Rows[2].Base
	sc.RecBatch().take(2)
	sc.RecBatch().take(2)

	sc.RecBatch().refillRow(nil, 2)
	slots := recBatchRowSlots(2)

	if mat.Rows[2].Base == nil {
		t.Fatal("Base nil after refill")
	}
	if mat.Rows[2].Base == oldBase {
		t.Error("Base not replaced by refill")
	}
	if mat.Rows[2].FreeCount != slots {
		t.Errorf("FreeCount after refill = %d, want %d", mat.Rows[2].FreeCount, slots)
	}
	if mat.Rows[2].Bitmap != recBatchRowSlotMask(2) {
		t.Errorf("Bitmap after refill = %#x, want %#x (all free)", mat.Rows[2].Bitmap, recBatchRowSlotMask(2))
	}
}

func TestServeRecBatchRefillInitAndGrow(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[elemKind]()), unsafe.Sizeof(elemKind{}))
	a := &Allocator{Slots: []SlotClass{*sc}}
	sc = &a.Slots[0]

	var hdr gort.SliceHeader
	if err := sc.RecBatch().ServeRefill(a, 0, &hdr); err != nil {
		t.Fatalf("init ServeRefill: %v", err)
	}
	if hdr.Data == nil {
		t.Fatal("hdr.Data nil after init")
	}
	if hdr.Cap != 1 {
		t.Errorf("init Cap = %d, want 1 (row 0)", hdr.Cap)
	}
	if hdr.Len != 0 {
		t.Errorf("init Len = %d, want 0", hdr.Len)
	}

	hdr.Len = 1
	(*[1]elemKind)(hdr.Data)[0] = elemKind{A: 11, B: 22}

	if err := sc.RecBatch().ServeRefill(a, 1, &hdr); err != nil {
		t.Fatalf("grow ServeRefill: %v", err)
	}
	if hdr.Cap != 2 {
		t.Errorf("grow Cap = %d, want 2 (row 1)", hdr.Cap)
	}
	if hdr.Len != 1 {
		t.Errorf("grow Len = %d, want 1 (preserved)", hdr.Len)
	}
	got := (*[2]elemKind)(hdr.Data)
	if got[0] != (elemKind{A: 11, B: 22}) {
		t.Errorf("element not copied on grow: got[0]=%+v", got[0])
	}
}

func TestServeRecBatchBypassGrow(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[elemKind]()), unsafe.Sizeof(elemKind{}))
	a := &Allocator{Slots: []SlotClass{*sc}}
	sc = &a.Slots[0]

	var hdr gort.SliceHeader
	if err := sc.RecBatch().ServeBypass(a, 256, &hdr); err != nil {
		t.Fatalf("bypass init: %v", err)
	}
	if hdr.Cap != 256 {
		t.Fatalf("bypass init Cap = %d, want 256", hdr.Cap)
	}
	hdr.Len = 3
	(*[256]elemKind)(hdr.Data)[0] = elemKind{A: 1}
	(*[256]elemKind)(hdr.Data)[1] = elemKind{A: 2}
	(*[256]elemKind)(hdr.Data)[2] = elemKind{A: 3}

	if err := sc.RecBatch().ServeBypass(a, 512, &hdr); err != nil {
		t.Fatalf("bypass grow: %v", err)
	}
	if hdr.Cap != 512 {
		t.Errorf("bypass grow Cap = %d, want 512", hdr.Cap)
	}
	if hdr.Len != 3 {
		t.Errorf("bypass grow Len = %d, want 3", hdr.Len)
	}
	got := (*[512]elemKind)(hdr.Data)
	if got[0] != (elemKind{A: 1}) || got[1] != (elemKind{A: 2}) || got[2] != (elemKind{A: 3}) {
		t.Errorf("elements not copied on bypass grow: %+v", got[:3])
	}
}

// TestRecBatchRefillStagesReplacedBase pins that a row base a refill displaces
// is still staged. A noscan map buffer may be the only reference to it until
// drain, so it needs a GC-visible root for the rest of the parse.
func TestRecBatchRefillStagesReplacedBase(t *testing.T) {
	tt := &TypeTree{
		Slots: []SlotTemplate{{
			ElemSize: uint32(unsafe.Sizeof(int(0))),
			RType:    gort.TypePtr(reflect.TypeFor[int]()),
			Mode:     slotRecBatch,
			Group:    1,
		}},
		GroupCount: 1,
	}
	a := NewAllocator(tt)
	sc := &a.Slots[0]
	mat := sc.RecBatch().matrix()

	sc.RecBatch().refillRow(nil, 0)
	oldBase := mat.Rows[0].Base
	slots := recBatchRowSlots(0)
	for i := range slots {
		if _, ok := sc.RecBatch().take(0); !ok {
			t.Fatalf("take(0) slot %d: empty", i)
		}
	}

	sc.RecBatch().refillRow(a, 0)

	if !retainedHas(a, oldBase) {
		t.Errorf("replaced Base %p not in retained", oldBase)
	}
	if !retainedHas(a, mat.Rows[0].Base) {
		t.Errorf("new Base %p not in retained", mat.Rows[0].Base)
	}

	if mat.Rows[0].Base == oldBase {
		t.Error("Base not replaced by refill")
	}
	if mat.Rows[0].Bitmap != recBatchRowSlotMask(0) {
		t.Errorf("Bitmap after refill = %#x, want %#x (all free)", mat.Rows[0].Bitmap, recBatchRowSlotMask(0))
	}

	for ri := 1; ri < recBatchRows; ri++ {
		if mat.Rows[ri].Base != nil {
			t.Errorf("row %d Base changed by row 0 refill", ri)
		}
	}
}

func TestRecBatchMatrixFree(t *testing.T) {
	sc := newRecBatchSlotForTest(gort.TypePtr(reflect.TypeFor[int]()), unsafe.Sizeof(int(0)))
	mat := sc.RecBatch().matrix()

	sc.RecBatch().refillRow(nil, 2)
	bk0, _ := sc.RecBatch().take(2)
	bk1, _ := sc.RecBatch().take(2)

	// Nonzero payloads stand in for stale pointers that free must clear before reuse.
	(*[4]int)(bk0)[0] = 0xdeadbeef
	(*[4]int)(bk1)[0] = 0xcafebabe

	freeBefore := mat.Rows[2].FreeCount
	baseBefore := mat.Rows[2].Base

	sc.RecBatch().free(bk0, 4)

	if mat.Rows[2].FreeCount != freeBefore+1 {
		t.Errorf("FreeCount after free = %d, want %d", mat.Rows[2].FreeCount, freeBefore+1)
	}
	if mat.Rows[2].Base != baseBefore {
		t.Errorf("Base changed after free: %p -> %p", baseBefore, mat.Rows[2].Base)
	}
	foundFreed := false
	slots := recBatchRowSlots(2)
	for i := range slots {
		if sc.RecBatch().slotAddr(2, i) == bk0 {
			foundFreed = true
			if mat.Rows[2].Bitmap&(1<<i) == 0 {
				t.Errorf("freed slot %d still marked used", i)
			}
		}
	}
	if !foundFreed {
		t.Fatal("freed backing is not a row-2 slot address")
	}
	if (*[4]int)(bk0)[0] != 0 {
		t.Errorf("freed backing not zeroed: [0]=%#x", (*[4]int)(bk0)[0])
	}

	foundUsed := false
	for i := range slots {
		if sc.RecBatch().slotAddr(2, i) == bk1 {
			foundUsed = true
			if mat.Rows[2].Bitmap&(1<<i) != 0 {
				t.Errorf("untouched slot %d marked free (should stay used)", i)
			}
			if (*[4]int)(bk1)[0] != 0xcafebabe {
				t.Errorf("untouched backing modified: [0]=%#x", (*[4]int)(bk1)[0])
			}
		}
	}
	if !foundUsed {
		t.Fatal("untouched backing is not a row-2 slot address")
	}

	sc.RecBatch().free(bk1, 0)
	sc.RecBatch().free(bk1, 512)
	if (*[4]int)(bk1)[0] != 0xcafebabe {
		t.Errorf("bk1 modified by out-of-range free: [0]=%#x", (*[4]int)(bk1)[0])
	}
	if mat.Rows[2].FreeCount != freeBefore+1 {
		t.Errorf("FreeCount changed by out-of-range free: %d, want %d", mat.Rows[2].FreeCount, freeBefore+1)
	}

	standalone := gort.UnsafeNewArray(sc.RType, 4)
	sc.RecBatch().free(standalone, 4)
	if mat.Rows[2].FreeCount != freeBefore+1 {
		t.Errorf("FreeCount changed by not-in-matrix free: %d, want %d", mat.Rows[2].FreeCount, freeBefore+1)
	}
}

func TestServeRecBatchBypassRetainsAndFreesMatrix(t *testing.T) {
	tt := &TypeTree{
		Slots: []SlotTemplate{{
			ElemSize: uint32(unsafe.Sizeof(int(0))),
			RType:    gort.TypePtr(reflect.TypeFor[int]()),
			Mode:     slotRecBatch,
			Group:    1,
		}},
		GroupCount: 1,
	}
	a := NewAllocator(tt)
	sc := &a.Slots[0]
	mat := sc.RecBatch().matrix()

	sc.RecBatch().refillRow(nil, 7)
	matrixBk, ok := sc.RecBatch().take(7)
	if !ok {
		t.Fatal("take(7) empty after refill")
	}
	// Nonzero payloads stand in for stale pointers that must be cleared before reuse.
	(*[128]int)(matrixBk)[0] = 0xdeadbeef
	(*[128]int)(matrixBk)[10] = 0xcafebabe

	// Reproduce the native slice header at the yield boundary.
	var hdr gort.SliceHeader
	hdr.Data = matrixBk
	hdr.Cap = 128
	hdr.Len = 5

	retainedBefore := len(a.retained)
	freeBefore := mat.Rows[7].FreeCount

	// Native execution yields before freeing the old matrix slot. Go must root
	// the new typed backing through drain and clear stale pointers before reuse.
	if err := sc.RecBatch().ServeBypass(a, 256, &hdr); err != nil {
		t.Fatalf("ServeBypass: %v", err)
	}

	if got := len(a.retained) - retainedBefore; got != 1 {
		t.Fatalf("retained grew by %d, want 1 (new bypass backing pinned)", got)
	}
	if a.retained[len(a.retained)-1] != hdr.Data {
		t.Errorf("retained backing %p != hdr.Data %p", a.retained[len(a.retained)-1], hdr.Data)
	}
	if hdr.Cap != 256 {
		t.Errorf("hdr.Cap = %d, want 256", hdr.Cap)
	}

	if mat.Rows[7].FreeCount != freeBefore+1 {
		t.Errorf("row 7 FreeCount = %d, want %d (old backing returned to matrix)",
			mat.Rows[7].FreeCount, freeBefore+1)
	}
	if (*[128]int)(matrixBk)[0] != 0 || (*[128]int)(matrixBk)[10] != 0 {
		t.Errorf("old matrix backing not zeroed: [0]=%#x [10]=%#x",
			(*[128]int)(matrixBk)[0], (*[128]int)(matrixBk)[10])
	}
	bk2, ok := sc.RecBatch().take(7)
	if !ok {
		t.Fatal("take(7) empty after free (should have the returned backing)")
	}
	if bk2 != matrixBk {
		t.Errorf("take after free returned %p, want recycled %p", bk2, matrixBk)
	}
}

// anyStringHosts are root types whose bind graph reaches the universal any
// type, exercising the BindAnyMeta string_slot_class registration.
var anyStringHosts = []reflect.Type{
	reflect.TypeFor[map[string]any](),
	reflect.TypeFor[[]any](),
	reflect.TypeFor[struct {
		V any `json:"v"`
	}](),
}

// TestAnyStringSlotNotRecBatch verifies the SlotClass that boxes JSON strings
// during any/iface binding is never classified as RecBatch. The string slot
// holds 16B Go string headers that eface.data points at; it is registered as a
// primitive leaf, so SCC analysis assigns slotBump (group 0, no backing edges),
// never slotRecBatch which is reserved for recursive slice element backings.
// Native any_value carves it with a linear block+offset+=elem_size cursor, a
// carve that is only valid for bump-style slots.
func TestAnyStringSlotNotRecBatch(t *testing.T) {
	for _, root := range anyStringHosts {
		t.Run(root.String(), func(t *testing.T) {
			tt, err := TypeTreeOf(root)
			if err != nil {
				t.Fatalf("TypeTreeOf: %v", err)
			}
			if len(tt.AnyMetas) == 0 {
				t.Fatalf("tree reached no any type; AnyTypeIdx=%d", tt.AnyTypeIdx)
			}
			am := tt.Types[tt.AnyTypeIdx].AnyMeta(tt.AnyMetas)
			sc := &tt.Slots[am.StringSlotClass]
			if sc.Mode == slotRecBatch {
				t.Errorf("string slot class (idx %d) is RecBatch; want Bump/RecBump", am.StringSlotClass)
			}
		})
	}
}
