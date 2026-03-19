//go:build goexperiment.swissmap

package vjson

import (
	"reflect"
	"testing"
	"unsafe"
)

// Benchmark: stack-based mapsIter (direct linkname to maps.Iter) vs legacy
// GoMapIterator (runtime shim with heap allocation). Only meaningful when
// Swiss Tables are enabled, since both paths use GoMapIterator under noswissmap.

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
