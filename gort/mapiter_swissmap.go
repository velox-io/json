//go:build goexperiment.swissmap || go1.26

package gort

import (
	"reflect"
	"unsafe"
)

func init() {
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("gort: unexpected slice memory layout")
	}

	// Verify MapsIter buffer fits maps.Iter. If too small, Init/Next
	// will corrupt the stack.
	m := map[string]string{"__gort_init_check__": "ok"}
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	if MapsIterKey(&it) == nil {
		panic("gort: MapsIter size mismatch")
	}
}

// MapsIter is a stack-allocatable buffer matching maps.Iter (96 bytes).
// Uses uintptr to prevent GC from misinterpreting internal fields as pointers.
type MapsIter struct {
	buf [12]uintptr
}

func MapsIterKey(it *MapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[0]))
}

func MapsIterElem(it *MapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[1]))
}

func MapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *MapsIter) {
	_mapsIterInit(unsafe.Pointer(it), t, m)
	_mapsIterNext(unsafe.Pointer(it))
}

func MapsIterNext(it *MapsIter) {
	_mapsIterNext(unsafe.Pointer(it))
}

//go:linkname _mapsIterInit internal/runtime/maps.(*Iter).Init
//go:noescape
func _mapsIterInit(it unsafe.Pointer, typ unsafe.Pointer, m unsafe.Pointer)

//go:linkname _mapsIterNext internal/runtime/maps.(*Iter).Next
//go:noescape
func _mapsIterNext(it unsafe.Pointer)

type GoMapIterator struct {
	Key  unsafe.Pointer
	Elem unsafe.Pointer
	Typ  unsafe.Pointer
	It   unsafe.Pointer
}

//go:linkname Mapiterinit runtime.mapiterinit
func Mapiterinit(t unsafe.Pointer, m unsafe.Pointer, it *GoMapIterator)

//go:linkname Mapiternext runtime.mapiternext
func Mapiternext(it *GoMapIterator)
