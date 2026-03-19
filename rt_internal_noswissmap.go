//go:build !goexperiment.swissmap && !go1.26

package vjson

import (
	"reflect"
	"unsafe"
)

// SwissMapLayoutOK is always false when Swiss Tables are disabled.
var SwissMapLayoutOK = false

// SwissMapStrIntLayoutOK is always false when Swiss Tables are disabled.
var SwissMapStrIntLayoutOK = false

// SwissMapStrInt64LayoutOK is always false when Swiss Tables are disabled.
var SwissMapStrInt64LayoutOK = false

func init() {
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("vjson: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Verify mapsIter buffer fits runtime.hiter (104 bytes on 64-bit).
	m := map[string]string{"__vjson_init_check__": "ok"}
	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	if mapsIterKey(&it) == nil {
		panic("vjson: mapsIter (noswissmap) init check failed — hiter layout may have changed")
	}
}

// mapsIter is a stack-allocatable buffer matching runtime.hiter (noswissmap).
// runtime asserts: sizeof(hiter) == 8 + 12*PtrSize = 104 on 64-bit.
// hiter.key at offset 0, hiter.elem at offset 8.
type mapsIter struct {
	buf [14]uintptr // 112 bytes >= 104
}

func mapsIterKey(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[0]))
}

func mapsIterElem(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[1]))
}

// mapsIterInit initializes the iterator and advances to the first entry.
// noswissmap's mapiterinit calls mapiternext internally, so the iterator
// is already at the first entry on return.
func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	noswissMapiterinit(t, m, unsafe.Pointer(it))
}

func mapsIterNext(it *mapsIter) {
	noswissMapiternext(unsafe.Pointer(it))
}

// Linknames to the old (noswissmap) map iteration runtime.
// Takes unsafe.Pointer because our mapsIter buffer matches hiter layout.
//
//go:linkname noswissMapiterinit runtime.mapiterinit
func noswissMapiterinit(t unsafe.Pointer, m unsafe.Pointer, it unsafe.Pointer) //nolint:revive

//go:linkname noswissMapiternext runtime.mapiternext
func noswissMapiternext(it unsafe.Pointer) //nolint:revive

func probeSwissMapSlotSize(_ reflect.Type, _ uintptr) (slotSize uintptr, ok bool) {
	return 0, false
}
