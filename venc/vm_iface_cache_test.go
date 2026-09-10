package venc

import (
	"math/bits"
	"testing"
	"unsafe"
)

// fakeTypePtrs returns n distinct 8-aligned pointers that mimic rtype
// addresses (never nil, >=8-byte aligned).
func fakeTypePtrs(n int) []unsafe.Pointer {
	backing := make([]int64, n)
	ptrs := make([]unsafe.Pointer, n)
	for i := range ptrs {
		ptrs[i] = unsafe.Pointer(&backing[i])
	}
	return ptrs
}

func checkIfaceTableInvariants(t *testing.T, s *ifaceCacheSnapshot, wantCount int) {
	t.Helper()
	if s.count != wantCount {
		t.Fatalf("count = %d, want %d", s.count, wantCount)
	}
	if len(s.slots) < 32 || len(s.slots)&(len(s.slots)-1) != 0 {
		t.Fatalf("slots capacity %d is not a power of two >= 32", len(s.slots))
	}
	if bits.Len64(uint64(len(s.slots)-1)) != 64-int(s.shift) {
		t.Fatalf("shift %d inconsistent with capacity %d", s.shift, len(s.slots))
	}
	if s.count*2 > len(s.slots) {
		t.Fatalf("load factor invariant violated: %d entries in %d slots", s.count, len(s.slots))
	}
	live := 0
	for i := range s.slots {
		if s.slots[i].TypePtr != nil {
			live++
		}
	}
	if live != s.count {
		t.Fatalf("live slots %d != count %d", live, s.count)
	}
}

func TestIfaceCacheTableBuildAndFind(t *testing.T) {
	ptrs := fakeTypePtrs(500)
	entries := make([]VjIfaceCacheEntry, len(ptrs))
	for i, p := range ptrs {
		entries[i] = VjIfaceCacheEntry{TypePtr: p, Tag: uint8(opString)}
	}

	snap := buildIfaceTable(entries)
	checkIfaceTableInvariants(t, snap, len(entries))

	for i, p := range ptrs {
		e := snap.lookup(p)
		if e == nil {
			t.Fatalf("type %d (%p) not found after build", i, p)
		}
		if e.Tag != uint8(opString) {
			t.Fatalf("type %d: tag = %d, want %d", i, e.Tag, uint8(opString))
		}
	}

	absent := fakeTypePtrs(64)
	for _, p := range absent {
		if snap.lookup(p) != nil {
			t.Fatalf("absent type %p found", p)
		}
	}
}

func TestIfaceCacheTableInsertGrowth(t *testing.T) {
	ptrs := fakeTypePtrs(1000)
	snap := &ifaceCacheSnapshot{}
	for i, p := range ptrs {
		if snap.lookup(p) != nil {
			t.Fatalf("type %d (%p) found before insert", i, p)
		}
		snap = snap.withEntry(VjIfaceCacheEntry{TypePtr: p})
	}

	checkIfaceTableInvariants(t, snap, len(ptrs))

	for i, p := range ptrs {
		e := snap.lookup(p)
		if e == nil {
			t.Fatalf("type %d (%p) lost after growth", i, p)
		}
	}
}

func TestIfaceCacheTableBodyAttachKeepsSlots(t *testing.T) {
	ptrs := fakeTypePtrs(40)
	snap := &ifaceCacheSnapshot{}
	for _, p := range ptrs {
		snap = snap.withEntry(VjIfaceCacheEntry{TypePtr: p, Tag: uint8(opInt)})
	}
	before := make([]VjIfaceCacheEntry, len(snap.slots))
	copy(before, snap.slots)

	target := ptrs[7]
	// BodyOpsPtr is only compared here, never dereferenced, but it still has
	// to be a real aligned address: a fabricated uintptr trips vet and the
	// checkptr instrumentation under -race and -asan.
	var bodyOpsBacking uintptr
	bodyOps := unsafe.Pointer(&bodyOpsBacking)
	idx := snap.find(target)
	if idx < 0 {
		t.Fatal("target type not found")
	}

	slots := make([]VjIfaceCacheEntry, len(snap.slots))
	copy(slots, snap.slots)
	slots[idx].BodyOpsPtr = bodyOps
	next := &ifaceCacheSnapshot{slots: slots, shift: snap.shift, count: snap.count}

	for i := range before {
		if i == idx {
			if next.slots[i].BodyOpsPtr != bodyOps {
				t.Fatal("body ops not attached at target slot")
			}
			continue
		}
		if next.slots[i] != before[i] {
			t.Fatalf("slot %d changed during body attach", i)
		}
	}
	if next.lookup(target).BodyOpsPtr != bodyOps {
		t.Fatal("lookup does not see attached body ops")
	}
}
