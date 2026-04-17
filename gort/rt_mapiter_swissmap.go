//go:build goexperiment.swissmap || go1.26

package gort

import (
	"reflect"
	"unsafe"
)

// SwissMapLayoutOK indicates whether the runtime's Swiss Map memory layout
// matches what our C code expects. False triggers the Go fallback path.
var SwissMapLayoutOK bool

// SwissMapStrIntLayoutOK is the same as SwissMapLayoutOK but for map[string]int.
var SwissMapStrIntLayoutOK bool

// SwissMapStrInt64LayoutOK is the same as SwissMapLayoutOK but for map[string]int64.
var SwissMapStrInt64LayoutOK bool

// SwissMapSplitGroup is true for KKKKVVVV group layout (GOEXPERIMENT=mapsplitgroup).
var SwissMapSplitGroup bool

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

	// Verify Swiss Map memory layout for C-native map iteration.
	SwissMapLayoutOK = verifySwissMapLayout()
	SwissMapStrIntLayoutOK = verifySwissMapStrIntLayout()
	SwissMapStrInt64LayoutOK = verifySwissMapStrInt64Layout()

	// Detect split vs interleaved group layout.
	if SwissMapLayoutOK {
		layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]string]()))
		SwissMapSplitGroup = layout.KeyStride == 16 && layout.ElemsOff > 24
	}
}

// verifySwissMapLayout checks that the runtime's Swiss Map group offsets
// match what our C code assumes, using the universal addressing formula:
//
//	key(i)  = group + layout.KeysOff  + i * layout.KeyStride
//	elem(i) = group + layout.ElemsOff + i * layout.ElemStride
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

	layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]string]()))

	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		keyPtr := unsafe.Add(dirPtr, layout.KeysOff+uintptr(i)*layout.KeyStride)
		elemPtr := unsafe.Add(dirPtr, layout.ElemsOff+uintptr(i)*layout.ElemStride)
		key := *(*string)(keyPtr)
		elem := *(*string)(elemPtr)
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

	layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]int]()))

	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		keyPtr := unsafe.Add(dirPtr, layout.KeysOff+uintptr(i)*layout.KeyStride)
		elemPtr := unsafe.Add(dirPtr, layout.ElemsOff+uintptr(i)*layout.ElemStride)
		key := *(*string)(keyPtr)
		elem := *(*int)(elemPtr)
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

	layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]int64]()))

	ctrls := *(*uint64)(dirPtr)
	found := 0
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 != 0 {
			continue
		}
		keyPtr := unsafe.Add(dirPtr, layout.KeysOff+uintptr(i)*layout.KeyStride)
		elemPtr := unsafe.Add(dirPtr, layout.ElemsOff+uintptr(i)*layout.ElemStride)
		key := *(*string)(keyPtr)
		elem := *(*int64)(elemPtr)
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

// ProbeSwissMapSlotSize probes the layout for a map[string]V.
// The returned slotSize has dual semantics:
//   - interleaved (KVKVKVKV): actual slot size (key+elem+padding)
//   - split (KKKKVVVV): elem stride (size of a single elem, aligned)
func ProbeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	if !SwissMapLayoutOK {
		return 0, false
	}
	if mapType.Key().Kind() != reflect.String {
		return 0, false
	}

	mt := TypePtr(mapType)
	layout := ReadMapLayout(mt)

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
		keyPtr := unsafe.Add(dirPtr, layout.KeysOff+uintptr(i)*layout.KeyStride)
		key := *(*string)(keyPtr)
		if key != "__gort_probe_1__" && key != "__gort_probe_2__" {
			return 0, false
		}
		found++
	}
	if found != 2 {
		return 0, false
	}

	if SwissMapSplitGroup {
		return layout.ElemStride, true
	}
	return layout.KeyStride, true
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

// GoMapIterator mirrors runtime.linknameIter (swissmap only, 32 bytes).
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
