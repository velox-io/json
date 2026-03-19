//go:build goexperiment.swissmap || go1.26

package vjson

import (
	"reflect"
	"testing"
	"unsafe"
)

// Benchmarks: stack-based mapsIter vs legacy GoMapIterator (shim).
// Only meaningful with Swiss Tables (both paths identical under noswissmap).

func BenchmarkMapsIterDirect(b *testing.B) {
	m := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		k := "key_" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		m[k] = "val_" + k
	}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var it mapsIter
		mapsIterInit(mt, mp, &it)
		for mapsIterKey(&it) != nil {
			_ = mapsIterKey(&it)
			_ = mapsIterElem(&it)
			mapsIterNext(&it)
		}
	}
}

func BenchmarkMapsIterLegacyShim(b *testing.B) {
	m := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		k := "key_" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		m[k] = "val_" + k
	}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var it GoMapIterator
		mapiterinit(mt, mp, &it)
		for it.Key != nil {
			_ = it.Key
			_ = it.Elem
			mapiternext(&it)
		}
	}
}

// Tests for legacy GoMapIterator (swissmap-only linknameIter shim)

func TestLegacyMapIterStringString(t *testing.T) {
	m := map[string]string{
		"hello": "world",
		"foo":   "bar",
		"key":   "value",
	}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	got := make(map[string]string)
	var it GoMapIterator
	mapiterinit(mt, mp, &it)
	for it.Key != nil {
		k := readGoString(it.Key)
		v := readGoString(it.Elem)
		got[k] = v
		mapiternext(&it)
	}

	if len(got) != 3 {
		t.Fatalf("iteration count: got %d, want 3", len(got))
	}
	for k, want := range m {
		if got[k] != want {
			t.Errorf("m[%q] = %q, want %q", k, got[k], want)
		}
	}
}

func TestLegacyMapIterEmpty(t *testing.T) {
	m := map[string]string{}

	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := mapHeaderPtr(unsafe.Pointer(&m))

	var it GoMapIterator
	mapiterinit(mt, mp, &it)
	if it.Key != nil {
		t.Fatal("expected nil Key for empty map")
	}
}
