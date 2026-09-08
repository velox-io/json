//go:build goexperiment.swissmap || go1.26

package gort

import (
	"reflect"
	"strconv"
	"unsafe"
)

// Build-tag gate (shared with mapiter_swissmap.go and maptype_old.go):
// goexperiment.swissmap is set by default on Go 1.24+, because swiss tables
// became the default map implementation in 1.24 and remain registered as a
// default experiment. That clause already covers 1.24/1.25/1.26/1.27; the
// `|| go1.26` fallback is a forward-compat hedge for if swissmap later
// graduates out of the experiment registry. Pre-1.24, or
// GOEXPERIMENT=noswissmap, falls back to mapiter_noswissmap.go.
//
// The abi.MapType layout split is orthogonal to this gate: maptype.go targets
// the 1.27 restructure (KeysOff/KeyStride/ElemsOff/ElemStride), maptype_old.go
// targets pre-1.27 (1.24/1.25/1.26 share the SlotSize/ElemOff layout). The
// verify* functions below are the runtime guard: if any offset is wrong for the
// running toolchain, the flags stay false and callers fall back.
// verifySwissMapLargeLayout additionally guards the large-map directory and
// table traversal plus the swissmap.h group constants.

// SwissMapLayoutOK indicates whether the runtime's Swiss Map memory layout
// matches what our C code expects. False triggers the Go fallback path.
var SwissMapLayoutOK bool

var SwissMapStrIntLayoutOK bool

var SwissMapStrInt64LayoutOK bool

// SwissMapLargeLayoutOK indicates whether the runtime's large-map structure
// (directory of table pointers, table field offsets, group array stride)
// matches the C traversal in native/encvm/impl/swissmap.h. False forces every
// native map opcode onto the Go fallback; map prewiring only depends on the
// small-map flags above and stays enabled.
var SwissMapLargeLayoutOK bool

// SwissMapSplitGroup is true for KKKKVVVV group layout (GOEXPERIMENT=mapsplitgroup).
var SwissMapSplitGroup bool

func init() {
	SwissMapLayoutOK = verifySwissMapLayout()
	SwissMapStrIntLayoutOK = verifySwissMapStrIntLayout()
	SwissMapStrInt64LayoutOK = verifySwissMapStrInt64Layout()

	if SwissMapLayoutOK {
		layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]string]()))
		SwissMapSplitGroup = layout.KeyStride == 16 && layout.ElemsOff > 24
	}

	SwissMapLargeLayoutOK = verifySwissMapLargeLayout()

	// Prewiring must clear the layout gate: it depends on dirPtr offset, the
	// ctrl-empty encoding, and the group's *abi.Type all matching the runtime.
	// The vj_nomapprewire tag skips both probes so smallMapPrewireOK and
	// compositeMergeOK stay false, forcing PlanMapSlots onto the lazy
	// growToSmall path. The vj_nocompositemap tag skips only verifyCompositeMerge
	// so PlanMapSlots falls back to the two-block prewire path (separate
	// MapAllocUnit + group blocks, no reflect.StructOf synthesis).
	if !mapPrewireDisabled {
		smallMapPrewireOK = SwissMapLayoutOK && verifySmallMapPrewire()

		// Merging builds on prewire: it requires the same group layout plus a
		// reflect.StructOf composite whose Map+group embedding works under GC
		// (dirPtr is an interior pointer into the same unit). If either the probe
		// fails or vj_nocompositemap is set, PlanMapSlots falls back to two-block.
		if !compositeMergeDisabled {
			compositeMergeOK = smallMapPrewireOK && verifyCompositeMerge()
		}
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

// verifySwissMapLargeLayout replicates the C large-map traversal (swissmap.h:
// directory -> tables -> groups -> slots, advancing the directory index by
// 1<<(globalDepth-localDepth) per table) against a map big enough to hold a
// split directory. It pins the GoSwissMap and GoSwissTable field offsets plus
// the per-variant group constants the C hardcodes. A future runtime that
// restructures the directory or table fails these checks and the native map
// opcodes fall back; a radical overhaul may instead fault here at init, which
// still beats silently encoding garbage.
func verifySwissMapLargeLayout() bool {
	if !SwissMapLayoutOK || unsafe.Sizeof(uintptr(0)) != 8 {
		return false
	}
	if !largeGroupMatchesC(ReadMapLayout(TypePtr(reflect.TypeFor[map[string]string]())), SwissMapSplitGroup, 32, 16, 264) ||
		!largeGroupMatchesC(ReadMapLayout(TypePtr(reflect.TypeFor[map[string]int]())), SwissMapSplitGroup, 24, 8, 200) ||
		!largeGroupMatchesC(ReadMapLayout(TypePtr(reflect.TypeFor[map[string]int64]())), SwissMapSplitGroup, 24, 8, 200) {
		return false
	}

	const n = 2000 // past two table splits: directory with globalDepth >= 1
	m := make(map[string]string)
	for i := range n {
		k := "k" + strconv.Itoa(i)
		m[k] = k
	}

	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	if *(*uint64)(mp) != n { // GoSwissMap.used
		return false
	}
	dirLen := *(*int64)(unsafe.Add(mp, 24))           // GoSwissMap.dir_len
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))  // GoSwissMap.dir_ptr
	globalDepth := int(*(*uint8)(unsafe.Add(mp, 32))) // GoSwissMap.global_depth
	if dirPtr == nil || dirLen <= 1 || globalDepth < 1 || globalDepth > 30 || dirLen != int64(1)<<uint(globalDepth) {
		return false
	}

	layout := ReadMapLayout(TypePtr(reflect.TypeFor[map[string]string]()))
	seen := make([]bool, n)
	found := 0
	di := int64(0)
	for di < dirLen {
		tab := *(*unsafe.Pointer)(unsafe.Add(dirPtr, uintptr(di)*8))
		if tab == nil || uintptr(tab)&7 != 0 {
			return false
		}
		tabIndex := *(*int64)(unsafe.Add(tab, 8))             // GoSwissTable.index
		localDepth := int(*(*uint8)(unsafe.Add(tab, 6)))      // GoSwissTable.local_depth
		groupsData := *(*unsafe.Pointer)(unsafe.Add(tab, 16)) // GoSwissTable.groups_data
		numGroups := *(*uint64)(unsafe.Add(tab, 24)) + 1      // GoSwissTable.groups_mask
		if tabIndex != di || localDepth > globalDepth || groupsData == nil || uintptr(groupsData)&7 != 0 ||
			numGroups > 128 || numGroups&(numGroups-1) != 0 {
			return false
		}

		for g := uintptr(0); g < uintptr(numGroups); g++ {
			group := unsafe.Add(groupsData, g*layout.GroupSize)
			ctrls := *(*uint64)(group) // ctrl word at group head
			for si := uintptr(0); si < 8; si++ {
				if byte(ctrls>>(si*8))&0x80 != 0 { // empty or deleted
					continue
				}
				k := *(*string)(unsafe.Add(group, layout.KeysOff+si*layout.KeyStride))
				if len(k) < 2 || k[0] != 'k' {
					return false
				}
				idx, err := strconv.Atoi(k[1:])
				if err != nil || idx < 0 || idx >= n || seen[idx] {
					return false
				}
				v := *(*string)(unsafe.Add(group, layout.ElemsOff+si*layout.ElemStride))
				if v != k {
					return false
				}
				seen[idx] = true
				found++
			}
		}
		di += int64(1) << uint(globalDepth-localDepth)
	}
	return found == n
}

// largeGroupMatchesC pins the group constants native/encvm/impl/swissmap.h
// hardcodes for one map variant: the 8-byte ctrl word at the group head
// (KeysOff), key stride, elems offset, elem stride, and group size, for both
// interleaved and split layouts. ilSlot is the interleaved slot size and
// splitElem the split elem stride.
func largeGroupMatchesC(l SwissMapLayout, split bool, ilSlot, splitElem, groupSize uintptr) bool {
	if l.KeysOff != 8 { // SWISS_CTRL_SIZE
		return false
	}
	if split {
		// SWISS_SPLIT_KEY_STRIDE and SWISS_SPLIT_ELEMS_OFF: string keys
		return l.KeyStride == 16 && l.ElemsOff == 136 && l.ElemStride == splitElem && l.GroupSize == groupSize
	}
	// Interleaved: elem sits right after the 16-byte string key in each slot
	return l.KeyStride == ilSlot && l.ElemStride == ilSlot && l.ElemsOff == 24 && l.GroupSize == groupSize
}

// verifySmallMapPrewire confirms that prewiring a small map's dirPtr to a
// batch-allocated, ctrl-initialized group yields a usable map without going
// through growToSmall. This is the load-bearing assumption of InitMapSlots's
// prewire path: if dirPtr offset, the ctrl-empty encoding, or the group layout
// doesn't match the runtime, the round-trip fails and InitMapSlots falls back
// to lazy growToSmall (smallMapPrewireOK stays false).
func verifySmallMapPrewire() bool {
	mt := TypePtr(reflect.TypeFor[map[string]any]())
	layout := ReadMapLayout(mt)
	if layout.GroupType == nil || layout.GroupSize == 0 {
		return false
	}

	// One group, zeroed by newarray. Set the control word to all-empty.
	gptr := UnsafeNewArray(layout.GroupType, 1)
	*(*uint64)(gptr) = mapCtrlEmpty

	// make(map[string]any) yields a small map with dirPtr=nil (or a compiler
	// stack-allocated group); we overwrite dirPtr only. dirLen stays 0 so the
	// runtime takes the small-map path on assign.
	m := make(map[string]any)
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	*(*unsafe.Pointer)(unsafe.Add(mp, mapDirPtrOff)) = gptr

	// Assign + read back via the normal runtime path (putSlotSmall on the
	// prewired group). A layout mismatch corrupts or panics here.
	m["__gort_prewire_check__"] = 1
	v, ok := m["__gort_prewire_check__"]
	if !ok || v != 1 {
		return false
	}
	if len(m) != 1 {
		return false
	}
	// used (Map offset 0) must be 1: putSlotSmall increments it on insert.
	if *(*uint64)(mp) != 1 {
		return false
	}
	return true
}

// ProbeSwissMapSlotSize probes the layout for a map[string]V. The returned
// slotSize has dual semantics:
//   - interleaved (KVKVKVKV): actual slot size (key+elem+padding)
//   - split (KKKKVVVV): elem stride (size of a single elem, aligned)
//
// It declines a map whose element Go stores behind a pointer, because there is no
// inline element for a stride to describe. It also requires the large-map
// layout guard: consumers stride over both small and large maps in C.
func ProbeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	if !SwissMapLayoutOK || !SwissMapLargeLayoutOK {
		return 0, false
	}
	if mapType.Key().Kind() != reflect.String {
		return 0, false
	}
	// Above the element limit a slot holds a *V, so the stride the runtime reports
	// is the pointer's, and a consumer stepping by it reads that pointer as if it
	// were the value. Declining keeps such maps on the caller's own iteration,
	// which dereferences properly.
	//
	// This must also come before the writes below: they clear valSize bytes
	// through the address faststr returned, which for an indirect element is the
	// pointer slot, so the clear would run past it and over the group.
	if MapValueIsIndirect(valSize) {
		return 0, false
	}

	mt := TypePtr(mapType)
	layout := ReadMapLayout(mt)

	mp := MakeMap(mt, 2, nil)
	valPtr1 := MapAssignFastStr(mt, mp, "__gort_probe_1__")
	for i := range valSize {
		*(*byte)(unsafe.Add(valPtr1, i)) = 0
	}
	valPtr2 := MapAssignFastStr(mt, mp, "__gort_probe_2__")
	for i := range valSize {
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
