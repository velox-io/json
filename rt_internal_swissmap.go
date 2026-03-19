//go:build goexperiment.swissmap || go1.26

package vjson

import (
	"reflect"
	"unsafe"
)

// SwissMapLayoutOK indicates whether the runtime's Swiss Map memory layout
// matches what our C code expects. When false, map encoding falls back to
// the Go-based iteration path (slower but correct).
var SwissMapLayoutOK bool

// SwissMapStrIntLayoutOK — same as SwissMapLayoutOK but for map[string]int.
var SwissMapStrIntLayoutOK bool

// SwissMapStrInt64LayoutOK — same as SwissMapLayoutOK but for map[string]int64.
var SwissMapStrInt64LayoutOK bool

func init() {
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("vjson: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Verify mapsIter buffer fits maps.Iter. If too small, Init/Next
	// will corrupt the stack — catch it now.
	m := map[string]string{"__vjson_init_check__": "ok"}
	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	if mapsIterKey(&it) == nil {
		panic("vjson: mapsIter size mismatch — maps.Iter layout may have changed")
	}

	// Verify Swiss Map memory layout for C-native map iteration.
	// Not fatal — falls back to Go-based iteration if mismatched.
	SwissMapLayoutOK = verifySwissMapLayout()
	SwissMapStrIntLayoutOK = verifySwissMapStrIntLayout()
	SwissMapStrInt64LayoutOK = verifySwissMapStrInt64Layout()
}

// verifySwissMapLayout checks that the runtime's Swiss Map struct offsets
// match what our C code assumes (GoSwissMap/GoSwissTable in types.h).
func verifySwissMapLayout() bool {
	m := map[string]string{"a": "b"}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	// Find the full slot (ctrl byte with bit 7 clear).
	ctrls := *(*uint64)(dirPtr)
	foundSlot := -1
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 == 0 {
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		return false
	}

	const slotSize = 32
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*string)(elemPtr)
	return elem == "b"
}

func verifySwissMapStrIntLayout() bool {
	m := map[string]int{"a": 42}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	ctrls := *(*uint64)(dirPtr)
	foundSlot := -1
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 == 0 {
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		return false
	}

	const slotSize = 24
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*int)(elemPtr)
	return elem == 42
}

func verifySwissMapStrInt64Layout() bool {
	m := map[string]int64{"a": 42}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	ctrls := *(*uint64)(dirPtr)
	foundSlot := -1
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 == 0 {
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		return false
	}

	const slotSize = 24
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*int64)(elemPtr)
	return elem == 42
}

// probeSwissMapSlotSize determines the slot size for a map[string]V by
// creating a 1-element map and inspecting the Swiss Map memory layout.
// Requires SwissMapLayoutOK as a precondition.
func probeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	if !SwissMapLayoutOK {
		return 0, false
	}
	if mapType.Key().Kind() != reflect.String {
		return 0, false
	}

	expectedSlotSize := (16 + valSize + 7) &^ 7

	mt := rtypePtr(mapType)
	mp := makemap(mt, 1, nil)
	valPtr := mapassign_faststr(mt, mp, "__vjson_probe__")
	for i := uintptr(0); i < valSize; i++ {
		*(*byte)(unsafe.Add(valPtr, i)) = 0
	}

	used := *(*uint64)(mp)
	if used != 1 {
		return 0, false
	}
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return 0, false
	}
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return 0, false
	}

	ctrls := *(*uint64)(dirPtr)
	foundSlot := -1
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 == 0 {
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		return 0, false
	}

	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*expectedSlotSize)
	key := *(*string)(keyPtr)
	if key != "__vjson_probe__" {
		return 0, false
	}

	return expectedSlotSize, true
}

// Stack-based map iteration via direct linkname to maps.Iter.Init/Next,
// bypassing both reflect.MapRange (allocates per entry) and the runtime's
// linknameIter shim (heap-allocates maps.Iter on every mapiterinit).
//
// maps.Iter layout (96 bytes on 64-bit):
//
//	key  unsafe.Pointer  // offset 0
//	elem unsafe.Pointer  // offset 8
//	...internal fields... // offsets 16–88

// mapsIter is an opaque, stack-allocatable buffer matching maps.Iter.
// Uses uintptr (not unsafe.Pointer) to prevent the GC from misinterpreting
// internal integer fields as pointers ("bad pointer in frame" on stack copy).
type mapsIter struct {
	buf [12]uintptr // 96 bytes
}

func mapsIterKey(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[0]))
}

func mapsIterElem(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[1]))
}

func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	_mapsIterInit(unsafe.Pointer(it), t, m)
	_mapsIterNext(unsafe.Pointer(it))
}

func mapsIterNext(it *mapsIter) {
	_mapsIterNext(unsafe.Pointer(it))
}

//go:linkname _mapsIterInit internal/runtime/maps.(*Iter).Init
//go:noescape
func _mapsIterInit(it unsafe.Pointer, typ unsafe.Pointer, m unsafe.Pointer)

//go:linkname _mapsIterNext internal/runtime/maps.(*Iter).Next
//go:noescape
func _mapsIterNext(it unsafe.Pointer)

// GoMapIterator mirrors runtime.linknameIter (swissmap only, 32 bytes).
// In noswissmap mode, runtime.mapiterinit expects the much larger hiter
// struct (104 bytes), so this type must NOT be used there.
type GoMapIterator struct {
	Key  unsafe.Pointer
	Elem unsafe.Pointer
	Typ  unsafe.Pointer
	It   unsafe.Pointer
}

//go:linkname mapiterinit runtime.mapiterinit
func mapiterinit(t unsafe.Pointer, m unsafe.Pointer, it *GoMapIterator)

//go:linkname mapiternext runtime.mapiternext
func mapiternext(it *GoMapIterator)
