//go:build !goexperiment.swissmap

package vjson

import (
	"reflect"
	"unsafe"
)

// SwissMapLayoutOK is always false when Swiss Tables are disabled.
// Map encoding falls back to the generic Go-based map iteration (OP_MAP_BEGIN/END).
var SwissMapLayoutOK = false

// SwissMapStrIntLayoutOK is always false when Swiss Tables are disabled.
var SwissMapStrIntLayoutOK = false

// SwissMapStrInt64LayoutOK is always false when Swiss Tables are disabled.
var SwissMapStrInt64LayoutOK = false

func init() {
	// Verify our sliceHeader layout assumption matches the Go runtime.
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("vjson: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Skip mapsIter verification and Swiss Map layout checks — they are
	// not available when Swiss Tables are disabled (GOEXPERIMENT=noswissmap).
	// The mapsIter API is provided via GoMapIterator-based shim below.
}

// mapsIter wraps GoMapIterator to provide the same API surface as the
// swissmap variant. This avoids the direct linkname to internal/runtime/maps
// which is not available when Swiss Tables are disabled.
type mapsIter struct {
	it GoMapIterator
}

// mapsIterKey returns the current key pointer from the iterator.
// nil indicates end of iteration.
func mapsIterKey(it *mapsIter) unsafe.Pointer {
	return it.it.Key
}

// mapsIterElem returns the current elem pointer from the iterator.
// nil indicates end of iteration.
func mapsIterElem(it *mapsIter) unsafe.Pointer {
	return it.it.Elem
}

// mapsIterInit initializes the iterator and advances to the first entry.
// After return, mapsIterKey(it) is the first key or nil if the map is empty.
func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	mapiterinit(t, m, &it.it)
}

// mapsIterNext advances the iterator to the next entry.
// After return, mapsIterKey(it) is the next key or nil if done.
func mapsIterNext(it *mapsIter) {
	mapiternext(&it.it)
}

// probeSwissMapSlotSize always returns (0, false) when Swiss Tables are disabled.
func probeSwissMapSlotSize(_ reflect.Type, _ uintptr) (slotSize uintptr, ok bool) {
	return 0, false
}
