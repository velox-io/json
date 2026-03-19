package vjson

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

// Tests for mapsIter (works in both swissmap and noswissmap modes)

func TestMapsIterStringString(t *testing.T) {
	m := map[string]string{
		"hello": "world",
		"foo":   "bar",
		"key":   "value",
	}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	// Verify maplen
	n := maplen(mp)
	if n != 3 {
		t.Fatalf("maplen: got %d, want 3", n)
	}

	// Iterate and collect all k-v pairs
	var it mapsIter
	mapsIterInit(mt, mp, &it)

	got := make(map[string]string)
	count := 0
	for mapsIterKey(&it) != nil {
		k := readGoString(mapsIterKey(&it))
		v := readGoString(mapsIterElem(&it))
		got[k] = v
		count++
		mapsIterNext(&it)
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

	mt := rtypePtr(reflect.TypeFor[map[string]int]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := maplen(mp); n != 3 {
		t.Fatalf("maplen: got %d, want 3", n)
	}

	got := make(map[string]int)
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	for mapsIterKey(&it) != nil {
		k := readGoString(mapsIterKey(&it))
		v := *(*int)(mapsIterElem(&it))
		got[k] = v
		mapsIterNext(&it)
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

	mt := rtypePtr(reflect.TypeFor[map[int]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := maplen(mp); n != 3 {
		t.Fatalf("maplen: got %d, want 3", n)
	}

	got := make(map[int]string)
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	for mapsIterKey(&it) != nil {
		k := *(*int)(mapsIterKey(&it))
		v := readGoString(mapsIterElem(&it))
		got[k] = v
		mapsIterNext(&it)
	}

	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%d] = %q, want %q", k, got[k], want)
		}
	}
}

func TestMapsIterEmpty(t *testing.T) {
	m := map[string]string{}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := maplen(mp); n != 0 {
		t.Fatalf("maplen: got %d, want 0", n)
	}

	var it mapsIter
	mapsIterInit(mt, mp, &it)
	if mapsIterKey(&it) != nil {
		t.Fatal("expected nil key for empty map")
	}
}

func TestMapsIterNil(t *testing.T) {
	var m map[string]string // nil map

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := maplen(mp); n != 0 {
		t.Fatalf("maplen(nil): got %d, want 0", n)
	}

	var it mapsIter
	mapsIterInit(mt, mp, &it)
	if mapsIterKey(&it) != nil {
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

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	if n := maplen(mp); n != 100 {
		t.Fatalf("maplen: got %d, want 100", n)
	}

	got := make(map[string]string)
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	for mapsIterKey(&it) != nil {
		k := readGoString(mapsIterKey(&it))
		v := readGoString(mapsIterElem(&it))
		got[k] = v
		mapsIterNext(&it)
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
