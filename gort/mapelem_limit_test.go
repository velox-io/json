//go:build goexperiment.swissmap || go1.26

package gort

import (
	"reflect"
	"testing"
	"unsafe"
)

// Go stores a map element behind a pointer once its type exceeds
// abi.MapMaxElemBytes, so the slot holds a *V rather than the V. A stride cannot
// describe such an element, and the two things this package offers for stepping
// over slots directly, the layout probe and MapAssignFastStr, are both invalid
// for it. These tests pin the boundary at its source, where the size is the only
// input, so a consumer does not have to reproduce the rule to stay correct.

// TestProbeSwissMapSlotSizeDeclinesIndirectElem pins that the probe refuses a map
// whose element is stored indirectly.
//
// Two things go wrong if it does not. The stride it would report is the pointer's,
// so a consumer stepping by it renders or reads the *V as if it were the V; and
// the probe's own zeroing writes valSize bytes through the address the assignment
// returned, which for an indirect element is an 8-byte pointer slot, so the clear
// runs past it and over the group.
func TestProbeSwissMapSlotSizeDeclinesIndirectElem(t *testing.T) {
	if !SwissMapLayoutOK {
		t.Skip("swiss map layout unavailable")
	}
	type overLimit struct{ P [MapMaxElemBytes + 8]byte }
	// Exactly at the limit: still inline, still probed.
	type atLimit struct{ P [MapMaxElemBytes]byte }

	if _, ok := ProbeSwissMapSlotSize(reflect.TypeOf(map[string]overLimit{}),
		reflect.TypeOf(overLimit{}).Size()); ok {
		t.Errorf("probe accepted a map whose element Go stores behind a pointer; the stride it reports describes the pointer, not the element")
	}
	slotSize, ok := ProbeSwissMapSlotSize(reflect.TypeOf(map[string]atLimit{}),
		reflect.TypeOf(atLimit{}).Size())
	if !ok {
		t.Fatal("probe declined an element exactly at the limit; it is still stored inline, so the fast path must remain available")
	}
	if slotSize == 0 {
		t.Error("probe reported ok with a zero stride")
	}
}

// TestMapValueIsIndirect pins the boundary itself, which callers store in their
// own metadata to decide between MapAssign and MapAssignFastStr.
func TestMapValueIsIndirect(t *testing.T) {
	for _, tc := range []struct {
		size uintptr
		want bool
	}{
		{1, false},
		{MapMaxElemBytes - 1, false},
		{MapMaxElemBytes, false}, // at the limit Go still stores inline
		{MapMaxElemBytes + 1, true},
		{MapMaxElemBytes * 4, true},
	} {
		if got := MapValueIsIndirect(tc.size); got != tc.want {
			t.Errorf("MapValueIsIndirect(%d) = %v, want %v", tc.size, got, tc.want)
		}
	}
}

// TestMapAssignFastStrMatchesGenericAtLimit checks that the two assignment entry
// points agree for an element right at the limit, which is the largest size the
// faststr path may still be used for. It is the counterpart to the decline test:
// together they say the gate is placed at the exact size where faststr stops
// being able to reach the element.
func TestMapAssignFastStrMatchesGenericAtLimit(t *testing.T) {
	type atLimit struct {
		N   int64
		Pad [MapMaxElemBytes - 8]byte
	}
	if MapValueIsIndirect(reflect.TypeOf(atLimit{}).Size()) {
		t.Fatalf("atLimit is %d bytes; the test type must sit exactly at the inline limit",
			reflect.TypeOf(atLimit{}).Size())
	}
	mapType := reflect.TypeOf(map[string]atLimit{})
	rt := TypePtr(mapType)

	viaFast := make(map[string]atLimit)
	p := MapAssignFastStr(rt, reflect.ValueOf(viaFast).UnsafePointer(), "k")
	(*atLimit)(p).N = 7

	viaGeneric := make(map[string]atLimit)
	key := "k"
	q := MapAssign(rt, reflect.ValueOf(viaGeneric).UnsafePointer(), unsafe.Pointer(&key))
	(*atLimit)(q).N = 7

	if viaFast["k"].N != 7 {
		t.Errorf("faststr assignment published N = %d, want 7", viaFast["k"].N)
	}
	if viaGeneric["k"].N != 7 {
		t.Errorf("generic assignment published N = %d, want 7", viaGeneric["k"].N)
	}
}
