package gort

import (
	"reflect"
	"sort"
	"testing"
	"unsafe"
)

// goString mirrors the Go string header.
type goString struct {
	Ptr unsafe.Pointer
	Len int
}

func readGoString(p unsafe.Pointer) string {
	gs := (*goString)(p)
	return unsafe.String((*byte)(gs.Ptr), gs.Len)
}

func mapHeaderPtr(mapVarPtr unsafe.Pointer) unsafe.Pointer {
	return *(*unsafe.Pointer)(mapVarPtr)
}

// Tests for MapsIter (works in both swissmap and noswissmap modes)

func TestMapsIterStringString(t *testing.T) {
	m := map[string]string{
		"hello": "world",
		"foo":   "bar",
		"key":   "value",
	}

	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	// Verify MapLen
	n := MapLen(mp)
	if n != 3 {
		t.Fatalf("MapLen: got %d, want 3", n)
	}

	// Iterate and collect all k-v pairs
	var it MapsIter
	MapsIterInit(mt, mp, &it)

	got := make(map[string]string)
	count := 0
	for MapsIterKey(&it) != nil {
		k := readGoString(MapsIterKey(&it))
		v := readGoString(MapsIterElem(&it))
		got[k] = v
		count++
		MapsIterNext(&it)
	}

	if count != 3 {
		t.Fatalf("iteration count: got %d, want 3", count)
	}

	// Sort keys for deterministic check
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if keys[0] != "foo" || keys[1] != "hello" || keys[2] != "key" {
		t.Fatalf("keys: got %v", keys)
	}

	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%q] = %q, want %q", k, got[k], want)
		}
	}
}

func TestMapsIterStringInt(t *testing.T) {
	m := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	mt := TypePtr(reflect.TypeFor[map[string]int]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := MapLen(mp); n != 3 {
		t.Fatalf("MapLen: got %d, want 3", n)
	}

	got := make(map[string]int)
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	for MapsIterKey(&it) != nil {
		k := readGoString(MapsIterKey(&it))
		v := *(*int)(MapsIterElem(&it))
		got[k] = v
		MapsIterNext(&it)
	}

	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%q] = %d, want %d", k, got[k], want)
		}
	}
}

func TestMapsIterIntString(t *testing.T) {
	m := map[int]string{
		1:   "one",
		2:   "two",
		100: "hundred",
	}

	mt := TypePtr(reflect.TypeFor[map[int]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := MapLen(mp); n != 3 {
		t.Fatalf("MapLen: got %d, want 3", n)
	}

	got := make(map[int]string)
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	for MapsIterKey(&it) != nil {
		k := *(*int)(MapsIterKey(&it))
		v := readGoString(MapsIterElem(&it))
		got[k] = v
		MapsIterNext(&it)
	}

	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%d] = %q, want %q", k, got[k], want)
		}
	}
}

func TestMapsIterEmpty(t *testing.T) {
	m := map[string]string{}

	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := MapLen(mp); n != 0 {
		t.Fatalf("MapLen: got %d, want 0", n)
	}

	var it MapsIter
	MapsIterInit(mt, mp, &it)
	if MapsIterKey(&it) != nil {
		t.Fatal("expected nil key for empty map")
	}
}

func TestMapsIterNil(t *testing.T) {
	var m map[string]string // nil map

	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := MapLen(mp); n != 0 {
		t.Fatalf("MapLen(nil): got %d, want 0", n)
	}

	var it MapsIter
	MapsIterInit(mt, mp, &it)
	if MapsIterKey(&it) != nil {
		t.Fatal("expected nil key for nil map")
	}
}

func TestMapsIterLargeMap(t *testing.T) {
	// Test with a map large enough to exercise multiple groups/tables.
	m := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		k := "key_" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		m[k] = "val_" + k
	}

	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := MapLen(mp); n != 100 {
		t.Fatalf("MapLen: got %d, want 100", n)
	}

	got := make(map[string]string)
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	for MapsIterKey(&it) != nil {
		k := readGoString(MapsIterKey(&it))
		v := readGoString(MapsIterElem(&it))
		got[k] = v
		MapsIterNext(&it)
	}

	if len(got) != 100 {
		t.Fatalf("iterated %d entries, want 100", len(got))
	}
	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%q] = %q, want %q", k, got[k], want)
		}
	}
}
