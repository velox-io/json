package bind

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"
)

// arrayOverflowTail puts a fixed-size [2]int array immediately before a
// sentinel field. Struct layout: Arr at offset 0 (16B), Tail at offset 16.
// A JSON array with >2 elements would overflow the inline storage: element 2
// would land at &Arr[2] == &Tail (offset 16). encoding/json truncates at the
// array length; the native binder's array branch (bind.h array_value)
// bounds-checks cur_count against array_len and parses-and-discards extra
// elements via safe_skip_value, matching encoding/json. Regression test for
// that bounds check.
type arrayOverflowTail struct {
	Arr  [2]int
	Tail int
}

func TestArrayOverflowClobbersAdjacentField(t *testing.T) {
	// Assert the layout this test relies on: Tail must sit right after Arr
	// so the first overflow element lands in Tail.
	if off := unsafe.Offsetof(arrayOverflowTail{}.Tail); off != 16 {
		t.Fatalf("unexpected layout: Tail at offset %d, want 16", off)
	}

	cases := []struct {
		name  string
		input string
	}{
		{"exact", `{"Arr":[1,2]}`},       // control: fills exactly, no overflow
		{"under", `{"Arr":[1]}`},         // control: under-fill, Tail stays 0
		{"one_over", `{"Arr":[1,2,99]}`}, // 1 overflow element -> Tail = 99
	}
	for _, c := range cases {
		var gj, vb arrayOverflowTail
		errJ := json.Unmarshal([]byte(c.input), &gj)
		errV := Unmarshal([]byte(c.input), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("%s: error mismatch json=%v nbind=%v", c.name, errJ, errV)
			continue
		}
		if errJ != nil {
			continue
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("%s: mismatch\n  input=%s\n  json =%+v (Tail=%d)\n  nbind=%+v (Tail=%d)",
				c.name, c.input, gj, gj.Tail, vb, vb.Tail)
		}
	}
}

// arrayOverflowPtr places a pointer field after the array. Without the
// bounds check, an overflow writing a non-pointer int value into Ptr would
// corrupt a GC-traced slot, which a GC pass (or checkptr) catches as a bad
// pointer. This is the variant that turned up as
// TestRawMessageGC_MapValue*Array* crashes before the array cur_aux fix.
type arrayOverflowPtr struct {
	Arr [2]int
	P   *int
	Val int
}

func TestArrayOverflowCorruptsPointerField(t *testing.T) {
	if off := unsafe.Offsetof(arrayOverflowPtr{}.P); off != 16 {
		t.Fatalf("unexpected layout: P at offset %d, want 16", off)
	}
	// [1,2,99]: 99 overflows into P (offset 16), a *int slot. stdlib
	// truncates to [1,2] and leaves P nil; the native binder writes 99 into
	// the pointer field.
	input := `{"Arr":[1,2,99],"Val":7}`
	var gj, vb arrayOverflowPtr
	errJ := json.Unmarshal([]byte(input), &gj)
	errV := Unmarshal([]byte(input), &vb)
	if (errJ == nil) != (errV == nil) {
		t.Fatalf("error mismatch json=%v nbind=%v", errJ, errV)
	}
	if errJ != nil {
		return
	}
	// stdlib: Arr=[1,2], P=nil (99 truncated), Val=7.
	// native (buggy): Arr=[1,2], P=(int)(99) corrupted, Val=7.
	if !reflect.DeepEqual(gj, vb) {
		t.Errorf("mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", input, gj, vb)
	}
}
