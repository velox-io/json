//go:build goexperiment.swissmap || go1.26

package gort

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
		panic("gort: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Verify MapsIter buffer fits maps.Iter. If too small, Init/Next
	// will corrupt the stack — catch it now.
	m := map[string]string{"__gort_init_check__": "ok"}
	mt := TypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it MapsIter
	MapsIterInit(mt, mp, &it)
	if MapsIterKey(&it) == nil {
		panic("gort: MapsIter size mismatch — maps.Iter layout may have changed")
	}

	// Verify Swiss Map memory layout for C-native map iteration.
	// Not fatal — falls back to Go-based iteration if mismatched.
	SwissMapLayoutOK = verifySwissMapLayout()
	SwissMapStrIntLayoutOK = verifySwissMapStrIntLayout()
	SwissMapStrInt64LayoutOK = verifySwissMapStrInt64Layout()
}

// verifySwissMapLayout checks that the runtime's Swiss Map struct offsets
// match what our C code assumes (GoSwissMap/GoSwissTable in types.h).
//
// Uses 2 entries so we can verify the inter-slot stride (slotSize) is correct,
// not just that a single slot's key/elem happen to align. This catches layout
// changes like GOEXPERIMENT=mapsplitgroup (KKKKVVVV instead of KVKVKVKV).
func verifySwissMapLayout() bool {
	m := map[string]string{"a": "b", "c": "d"}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 2 {
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

	const slotSize = 32
	const elemOff = 16

	// Find both full slots and verify key+elem at each.
	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		slotBase := unsafe.Add(dirPtr, 8+uintptr(i)*slotSize)
		key := *(*string)(slotBase)
		elem := *(*string)(unsafe.Add(slotBase, elemOff))
		switch key {
		case "a":
			if elem != "b" {
				return false
			}
		case "c":
			if elem != "d" {
				return false
			}
		default:
			return false
		}
		found++
	}
	return found == 2
}

func verifySwissMapStrIntLayout() bool {
	m := map[string]int{"a": 42, "c": 99}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 2 {
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

	const slotSize = 24
	const elemOff = 16

	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		slotBase := unsafe.Add(dirPtr, 8+uintptr(i)*slotSize)
		key := *(*string)(slotBase)
		elem := *(*int)(unsafe.Add(slotBase, elemOff))
		switch key {
		case "a":
			if elem != 42 {
				return false
			}
		case "c":
			if elem != 99 {
				return false
			}
		default:
			return false
		}
		found++
	}
	return found == 2
}

func verifySwissMapStrInt64Layout() bool {
	m := map[string]int64{"a": 42, "c": 99}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	used := *(*uint64)(mp)
	if used != 2 {
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

	const slotSize = 24
	const elemOff = 16

	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		slotBase := unsafe.Add(dirPtr, 8+uintptr(i)*slotSize)
		key := *(*string)(slotBase)
		elem := *(*int64)(unsafe.Add(slotBase, elemOff))
		switch key {
		case "a":
			if elem != 42 {
				return false
			}
		case "c":
			if elem != 99 {
				return false
			}
		default:
			return false
		}
		found++
	}
	return found == 2
}

// ProbeSwissMapSlotSize determines the slot size for a map[string]V by
// creating a 2-element map and verifying that both slots are at the expected
// stride. Requires SwissMapLayoutOK as a precondition.
func ProbeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	if !SwissMapLayoutOK {
		return 0, false
	}
	if mapType.Key().Kind() != reflect.String {
		return 0, false
	}

	expectedSlotSize := (16 + valSize + 7) &^ 7

	mt := TypePtr(mapType)
	mp := MakeMap(mt, 2, nil)
	valPtr1 := MapAssignFastStr(mt, mp, "__gort_probe_1__")
	for i := uintptr(0); i < valSize; i++ {
		*(*byte)(unsafe.Add(valPtr1, i)) = 0
	}
	valPtr2 := MapAssignFastStr(mt, mp, "__gort_probe_2__")
	for i := uintptr(0); i < valSize; i++ {
		*(*byte)(unsafe.Add(valPtr2, i)) = 0
	}

	used := *(*uint64)(mp)
	if used != 2 {
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
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		keyPtr := unsafe.Add(dirPtr, 8+uintptr(i)*expectedSlotSize)
		key := *(*string)(keyPtr)
		if key != "__gort_probe_1__" && key != "__gort_probe_2__" {
			return 0, false
		}
		found++
	}
	if found != 2 {
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

// MapsIter is an opaque, stack-allocatable buffer matching maps.Iter.
// Uses uintptr (not unsafe.Pointer) to prevent the GC from misinterpreting
// internal integer fields as pointers ("bad pointer in frame" on stack copy).
type MapsIter struct {
	buf [12]uintptr // 96 bytes
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

// GoMapIterator mirrors runtime.linknameIter (swissmap only, 32 bytes).
// In noswissmap mode, runtime.mapiterinit expects the much larger hiter
// struct (104 bytes), so this type must NOT be used there.
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
