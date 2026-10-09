package venc

import (
	"math/bits"
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
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

type ifaceVerdictPtr struct {
	A int `json:"a"`
}

type ifaceVerdictCase struct {
	B int `json:"b"`
}

type ifaceVerdictAnyHost struct {
	V any `json:"v"`
}

type ifaceVerdictUnfoldHost struct {
	Kind string `json:"kind"`
	Obj  any    `json:",embed" vjson:"variant=kind"`
}

// An entry's OP_INTERFACE verdict decides whether a payload stays native:
// no tag and no Blueprint sends every payload of the type to Go. A pointer
// payload switches into its root Blueprint, and an entry an unfold created
// carries the verdict as well as the body, so the type stays native when it
// later fills an ordinary interface field.
func TestIfaceEntryNativeVerdict(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	entry := func(t reflect.Type) *VjIfaceCacheEntry {
		return loadIfaceCacheSnapshot().lookup(rtypePtr(t))
	}

	if _, err := Marshal(ifaceVerdictAnyHost{V: &ifaceVerdictPtr{A: 1}}); err != nil {
		t.Fatal(err)
	}
	if e := entry(reflect.TypeFor[*ifaceVerdictPtr]()); e == nil || e.OpsPtr == nil {
		t.Errorf("pointer payload entry = %+v, want a Blueprint", e)
	}

	if _, err := Marshal(ifaceVerdictUnfoldHost{Kind: "k", Obj: ifaceVerdictCase{B: 2}}); err != nil {
		t.Fatal(err)
	}
	if e := entry(reflect.TypeFor[ifaceVerdictCase]()); e == nil || e.OpsPtr == nil || e.BodyOpsPtr == nil {
		t.Errorf("unfolded case entry = %+v, want both a Blueprint and a body", e)
	}
	got, err := Marshal(ifaceVerdictAnyHost{V: ifaceVerdictCase{B: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":{"b":2}}` {
		t.Errorf("unfolded case in an interface field: got %s", got)
	}
}

type ifaceVerdictShape interface{ Name() string }

type ifaceVerdictSquare struct {
	S int `json:"s"`
}

func (ifaceVerdictSquare) Name() string { return "square" }

type ifaceVerdictCircle struct {
	R int `json:"r"`
}

func (ifaceVerdictCircle) Name() string { return "circle" }

type ifaceVerdictShapeHost struct {
	S ifaceVerdictShape   `json:"s"`
	L []ifaceVerdictShape `json:"l"`
}

// A non-empty interface field or element dispatches through OP_INTERFACE,
// which resolves the concrete type from the itab and publishes its entry:
// the payload stays native like an any's.
func TestNonEmptyInterfaceNativeVerdict(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	h := ifaceVerdictShapeHost{S: ifaceVerdictSquare{S: 1}, L: []ifaceVerdictShape{ifaceVerdictCircle{R: 2}}}
	got, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"s":{"s":1},"l":[{"r":2}]}` {
		t.Errorf("got %s", got)
	}
	for _, rt := range []reflect.Type{reflect.TypeFor[ifaceVerdictSquare](), reflect.TypeFor[ifaceVerdictCircle]()} {
		if e := loadIfaceCacheSnapshot().lookup(rtypePtr(rt)); e == nil || e.OpsPtr == nil {
			t.Errorf("%v entry = %+v, want a Blueprint", rt, e)
		}
	}
}

// ifaceVerdictRef holds a single pointer, so an interface stores it in its
// data word.
type ifaceVerdictRef struct {
	P *int `json:"p"`
}

// A case the interface stores in its data word unfolds natively from the
// word itself, whose nil is the field's value rather than a missing case:
// even a first sight with a nil word compiles the body.
func TestUnfoldDataWordCaseNative(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	got, err := Marshal(ifaceVerdictUnfoldHost{Kind: "ref", Obj: ifaceVerdictRef{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"ref","p":null}`; string(got) != want {
		t.Errorf("nil word: got %s, want %s", got, want)
	}
	e := loadIfaceCacheSnapshot().lookup(rtypePtr(reflect.TypeFor[ifaceVerdictRef]()))
	if e == nil || e.BodyOpsPtr == nil || e.Flags&ifaceFlagBodyIndirect == 0 {
		t.Errorf("data-word case entry = %+v, want a body read at the word", e)
	}
	x := 7
	got, err = Marshal([]ifaceVerdictUnfoldHost{
		{Kind: "ref", Obj: ifaceVerdictRef{P: &x}},
		{Kind: "ref", Obj: ifaceVerdictRef{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"kind":"ref","p":7},{"kind":"ref","p":null}]`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
