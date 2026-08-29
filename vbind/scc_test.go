package vbind

import (
	"reflect"
	"testing"
)

// Mutual-recursion test types must be package-level: function-local type
// declarations cannot forward-reference a sibling declared later.
type sccTestA struct {
	B []sccTestB
}
type sccTestB struct {
	A []sccTestA
}

func TestSCC_GroupAssignment(t *testing.T) {
	// The pointee backing reaches itself through Next, forming a single member SCC.
	t.Run("SelfRecursivePointer", func(t *testing.T) {
		type sccList struct {
			Next *sccList
		}
		tt, err := TypeTreeOf(reflect.TypeFor[sccList]())
		if err != nil {
			t.Fatal(err)
		}
		if tt.GroupCount != 1 {
			t.Fatalf("GroupCount = %d, want 1 (single self-loop group)", tt.GroupCount)
		}
		var groupSlots int
		for i := range tt.Slots {
			s := &tt.Slots[i]
			if s.Group == 0 {
				continue
			}
			groupSlots++
			if s.Mode == slotRecBatch {
				t.Errorf("slot %d: self-recursive pointer pointee must not be RecBatch", i)
			}
			if s.Group != 1 {
				t.Errorf("slot %d: Group = %d, want 1", i, s.Group)
			}
		}
		if groupSlots != 1 {
			t.Errorf("groupSlots = %d, want 1 (only the *sccList pointee slot)", groupSlots)
		}
	})

	// The two slice backings form one dependency cycle and must share an SCC.
	t.Run("MutualSliceRecursion", func(t *testing.T) {
		tt, err := TypeTreeOf(reflect.TypeFor[sccTestA]())
		if err != nil {
			t.Fatal(err)
		}
		if tt.GroupCount == 0 {
			t.Fatal("GroupCount = 0, want >= 1 for mutual slice recursion")
		}
		var groupID uint32
		var groupSlots, RecBatchInGroup int
		for i := range tt.Slots {
			s := &tt.Slots[i]
			if s.Group == 0 {
				continue
			}
			groupSlots++
			if groupID == 0 {
				groupID = s.Group
			} else if s.Group != groupID {
				t.Errorf("slot %d: Group %d != %d (mutual slices must share one group)", i, s.Group, groupID)
			}
			if s.Mode == slotRecBatch {
				RecBatchInGroup++
			}
		}
		if groupSlots < 2 {
			t.Errorf("groupSlots = %d, want >= 2", groupSlots)
		}
		if RecBatchInGroup < 2 {
			t.Errorf("RecBatchInGroup = %d, want >= 2 (both []sccTestA and []sccTestB are RecBatch)", RecBatchInGroup)
		}
	})

	// The []any header, element backing, and map header cycle through eface.data.
	t.Run("AnyGroup", func(t *testing.T) {
		type sccAnyHolder struct {
			V any
		}
		tt, err := TypeTreeOf(reflect.TypeFor[sccAnyHolder]())
		if err != nil {
			t.Fatal(err)
		}
		if tt.GroupCount != 1 {
			t.Fatalf("GroupCount = %d, want 1 (the any group)", tt.GroupCount)
		}
		var mapGroup, anySliceGroup uint32
		var sawMap, sawAnyRecBatch bool
		for i := range tt.Slots {
			s := &tt.Slots[i]
			if s.Group == 0 {
				continue
			}
			if s.Flags&SlotIsMap != 0 {
				sawMap = true
				mapGroup = s.Group
			}
			if s.Mode == slotRecBatch {
				sawAnyRecBatch = true
				anySliceGroup = s.Group
			}
		}
		if !sawMap {
			t.Error("map[string]any hmap slot should be in the any SCC group")
		}
		if !sawAnyRecBatch {
			t.Error("[]any element backing slot should be RecBatch and in the any group")
		}
		if sawMap && sawAnyRecBatch && mapGroup != anySliceGroup {
			t.Errorf("map[string]any group %d != []any group %d (any must be one SCC group)", mapGroup, anySliceGroup)
		}
	})

	t.Run("NonRecursive", func(t *testing.T) {
		type sccFlat struct {
			X int
			Y []int
			Z *int
		}
		tt, err := TypeTreeOf(reflect.TypeFor[sccFlat]())
		if err != nil {
			t.Fatal(err)
		}
		if tt.GroupCount != 0 {
			t.Errorf("GroupCount = %d, want 0 for non-recursive type", tt.GroupCount)
		}
		for i := range tt.Slots {
			if tt.Slots[i].Group != 0 {
				t.Errorf("slot %d: Group = %d, want 0", i, tt.Slots[i].Group)
			}
			if tt.Slots[i].Mode == slotRecBatch {
				t.Errorf("slot %d: non-recursive type must not be RecBatch", i)
			}
		}
	})
}

// Recursive backings detach every slotDetachK releases to break cross parse
// dependency chains. Nonrecursive bump slots retain their EWMA block.
func TestDetachSCCGroup(t *testing.T) {
	t.Run("BumpRecDetachesOnK", func(t *testing.T) {
		type sccList struct {
			Next *sccList
		}
		tt, err := TypeTreeOf(reflect.TypeFor[sccList]())
		if err != nil {
			t.Fatal(err)
		}
		a := NewAllocator(tt)
		idx := -1
		for i := range tt.Slots {
			if tt.Slots[i].Group != 0 && tt.Slots[i].Mode != slotRecBatch {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatal("no rec bump slot found")
		}
		orig := a.Slots[idx].Block
		if orig == nil {
			t.Fatal("initial bump rec Block nil")
		}
		for i := range slotDetachK - 1 {
			a.Release()
			if a.Slots[idx].Block != orig {
				t.Fatalf("Release %d: rec Block changed before K", i+1)
			}
		}
		a.Release()
		if a.Slots[idx].Block != nil {
			t.Fatalf("after K Releases: rec Block = %v, want nil (detached)", a.Slots[idx].Block)
		}
		if err := a.ServeNewBlock(uint32(idx), 0); err != nil {
			t.Fatalf("ServeNewBlock: %v", err)
		}
		if a.Slots[idx].Block == nil || a.Slots[idx].Block == orig {
			t.Error("after ServeNewBlock: rec Block should be a fresh allocation")
		}
	})

	t.Run("RecBatchRecDetachesOnK", func(t *testing.T) {
		tt, err := TypeTreeOf(reflect.TypeFor[sccTestA]())
		if err != nil {
			t.Fatal(err)
		}
		a := NewAllocator(tt)
		idx := -1
		for i := range tt.Slots {
			if tt.Slots[i].Group != 0 && tt.Slots[i].Mode == slotRecBatch {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatal("no rec RecBatch slot found")
		}
		orig := a.Slots[idx].Block
		if orig == nil {
			t.Fatal("initial RecBatch rec Block nil")
		}
		for i := range slotDetachK - 1 {
			a.Release()
			if a.Slots[idx].Block != orig {
				t.Fatalf("Release %d: RecBatch rec Block changed before K", i+1)
			}
		}
		a.Release()
		if a.Slots[idx].Block == nil {
			t.Fatal("after K Releases: RecBatch rec Block nil (should be fresh matrix)")
		}
		if a.Slots[idx].Block == orig {
			t.Error("after K Releases: RecBatch rec Block should be a fresh matrix, not the original")
		}
	})

	t.Run("NonRecUnchanged", func(t *testing.T) {
		type sccFlat struct {
			Y []int
		}
		tt, err := TypeTreeOf(reflect.TypeFor[sccFlat]())
		if err != nil {
			t.Fatal(err)
		}
		a := NewAllocator(tt)
		idx := -1
		for i := range tt.Slots {
			s := &tt.Slots[i]
			if s.Group == 0 && s.Mode != slotRecBatch && s.Flags&SlotIsMap == 0 {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatal("no non-rec bump slot found")
		}
		orig := a.Slots[idx].Block
		for range slotDetachK + 2 {
			a.Release()
		}
		if a.Slots[idx].Block != orig {
			t.Errorf("non-rec bump Block changed across K+2 Releases: want %v got %v", orig, a.Slots[idx].Block)
		}
	})
}
