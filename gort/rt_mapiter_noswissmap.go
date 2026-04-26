//go:build !goexperiment.swissmap && !go1.26

package gort

import (
	"reflect"
	"unsafe"
)

var SwissMapLayoutOK = false

var SwissMapStrIntLayoutOK = false

var SwissMapStrInt64LayoutOK = false

func init() {
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("gort: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Verify MapsIter buffer fits runtime.hiter (104 bytes on 64-bit).
	m := map[string]string{"__gort_init_check__": "ok"}
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	if MapsIterKey(&it) == nil {
		panic("gort: MapsIter (noswissmap) init check failed — hiter layout may have changed")
	}
}

// MapsIter is a stack-allocatable buffer matching runtime.hiter (noswissmap).
// runtime asserts: sizeof(hiter) == 8 + 12*PtrSize = 104 on 64-bit.
// hiter.key at offset 0, hiter.elem at offset 8.
type MapsIter struct {
	buf [14]uintptr // 112 bytes >= 104
}

func MapsIterKey(it *MapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[0]))
}

func MapsIterElem(it *MapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[1]))
}

// MapsIterInit initializes the iterator and advances to the first entry.
// noswissmap's mapiterinit calls mapiternext internally, so the iterator
// is already at the first entry on return.
func MapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *MapsIter) {
	noswissMapiterinit(t, m, unsafe.Pointer(it))
}

func MapsIterNext(it *MapsIter) {
	noswissMapiternext(unsafe.Pointer(it))
}

//go:linkname noswissMapiterinit runtime.mapiterinit
func noswissMapiterinit(t unsafe.Pointer, m unsafe.Pointer, it unsafe.Pointer) //nolint:revive

//go:linkname noswissMapiternext runtime.mapiternext
func noswissMapiternext(it unsafe.Pointer) //nolint:revive

func ProbeSwissMapSlotSize(_ reflect.Type, _ uintptr) (slotSize uintptr, ok bool) {
	return 0, false
}
