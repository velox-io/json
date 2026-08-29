package vbind

import (
	"reflect"
	"testing"

	"github.com/velox-io/json/typ"
)

type recBatchNode struct {
	Name string
	Kids []recBatchNode
}

type flatNode struct {
	Tags   []string
	Nums   []int
	Nested [][]int
}

func sliceSlots(tt *TypeTree) []SlotTemplate {
	var out []SlotTemplate
	for i := range tt.Types {
		if tt.Types[i].Kind != KindSlice {
			continue
		}
		ci := int(tt.TypeMeta[i].SliceMeta().AllocClass)
		if ci >= 0 && ci < len(tt.Slots) {
			out = append(out, tt.Slots[ci])
		}
	}
	return out
}

func TestBuild_RecursiveSliceFlaggedRecBatch(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[recBatchNode]()))
	if err != nil {
		t.Fatalf("Build recBatchNode: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) == 0 {
		t.Fatal("expected at least one slice slot")
	}
	for i, sc := range slots {
		if sc.Mode != slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want slotRecBatch", i, sc.Mode)
		}
	}
}

func TestBuild_NonRecursiveSliceNotFlagged(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[flatNode]()))
	if err != nil {
		t.Fatalf("Build flatNode: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) == 0 {
		t.Fatal("expected at least one slice slot")
	}
	for i, sc := range slots {
		if sc.Mode == slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want non-RecBatch", i, sc.Mode)
		}
	}
}

// Recursion crosses both the slice backing and pointee backing boundaries.
type ptrRecursiveNode struct {
	Name string
	Kids []*ptrRecursiveNode
}

func TestBuild_PointerRecursiveSliceFlaggedRecBatch(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[ptrRecursiveNode]()))
	if err != nil {
		t.Fatalf("Build ptrRecursiveNode: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) == 0 {
		t.Fatal("expected at least one slice slot")
	}
	for i, sc := range slots {
		if sc.Mode != slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want slotRecBatch", i, sc.Mode)
		}
	}
}

// The cycle reaches the slice backing through two pointee boundaries.
type indirectA struct {
	B *indirectB
}
type indirectB struct {
	Items []*indirectA
}

func TestBuild_IndirectRecursiveSliceFlaggedRecBatch(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[indirectA]()))
	if err != nil {
		t.Fatalf("Build indirectA: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) == 0 {
		t.Fatal("expected at least one slice slot")
	}
	for i, sc := range slots {
		if sc.Mode != slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want slotRecBatch", i, sc.Mode)
		}
	}
}

// Each slice backing reaches itself only through the other pointee type.
type mutualA struct {
	E  *mutualE
	Es []*mutualE
}
type mutualE struct {
	A  *mutualA
	As []*mutualA
}

func TestBuild_MutualRecursiveSliceFlaggedRecBatch(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[mutualA]()))
	if err != nil {
		t.Fatalf("Build mutualA: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) < 2 {
		t.Fatalf("expected at least two slice slots (one per mutual type), got %d", len(slots))
	}
	for i, sc := range slots {
		if sc.Mode != slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want slotRecBatch", i, sc.Mode)
		}
	}
}

// The pointee cannot reach the slice backing, so the pointer boundary alone
// must not create an SCC.
type ptrFlatNode struct {
	Kids []*flatChild
}
type flatChild struct {
	Name string
}

func TestBuild_PointerSliceNonRecursiveNotFlagged(t *testing.T) {
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[ptrFlatNode]()))
	if err != nil {
		t.Fatalf("Build ptrFlatNode: %v", err)
	}
	slots := sliceSlots(tt)
	if len(slots) == 0 {
		t.Fatal("expected at least one slice slot")
	}
	for i, sc := range slots {
		if sc.Mode == slotRecBatch {
			t.Errorf("slice slot %d Mode=%d, want non-RecBatch", i, sc.Mode)
		}
	}
}
