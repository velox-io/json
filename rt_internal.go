package vjson

import (
	"reflect"
	"unsafe"
	_ "unsafe" // required for go:linkname
)

// Low-level runtime allocation and copy functions, accessed via go:linkname
// to bypass reflect's validation overhead (type assertions, kind checks,
// mustBeAssignable, reflect.Value construction, etc.).
//
// All three targets are explicitly marked as stable linkname targets in the
// Go runtime with "Do not remove or change the type signature" comments
// (see go.dev/issue/67401).

//go:linkname unsafe_New reflect.unsafe_New
//go:noescape
func unsafe_New(typ unsafe.Pointer) unsafe.Pointer //nolint:revive

//go:linkname unsafe_NewArray reflect.unsafe_NewArray
//go:noescape
func unsafe_NewArray(typ unsafe.Pointer, n int) unsafe.Pointer //nolint:revive

//go:linkname typedslicecopy runtime.typedslicecopy
//go:nosplit
func typedslicecopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int

//go:linkname makemap runtime.makemap
func makemap(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign runtime.mapassign
func mapassign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign_faststr runtime.mapassign_faststr
func mapassign_faststr(t unsafe.Pointer, m unsafe.Pointer, key string) unsafe.Pointer //nolint:revive

//go:linkname typedmemmove runtime.typedmemmove
func typedmemmove(typ unsafe.Pointer, dst unsafe.Pointer, src unsafe.Pointer)

// goIface matches the runtime iface layout: {itab, data}.
// Used to construct interface values with a cached itab, avoiding
// the mallocgc boxing that reflect.Value.Interface() triggers for
// value types larger than a pointer (e.g. time.Time = 24 bytes).
type goIface struct {
	tab  unsafe.Pointer
	data unsafe.Pointer
}

// extractItab extracts the cached *itab from an interface pointer.
// ifacePtr must point to a non-empty interface value (e.g. *json.Marshaler).
// The interface must have layout {itab, data} (iface, not eface).
func extractItab(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return (*goIface)(ifacePtr).tab
}

// rtypePtr extracts the *abi.Type pointer from a reflect.Type interface value.
//
// reflect.Type is an interface {itab, data} where data points to an *rtype,
// and rtype is struct{t abi.Type}. Since abi.Type is the first (and only)
// field, the data pointer IS the *abi.Type pointer.
func rtypePtr(t reflect.Type) unsafe.Pointer {
	// An interface value in memory is {itab *itab, data unsafe.Pointer}.
	// We extract the data word (index 1), which is the *rtype == *abi.Type.
	iface := *(*[2]unsafe.Pointer)(unsafe.Pointer(&t))
	return iface[1]
}

// SliceHeader matches the internal layout of a Go slice.
type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

//go:linkname mallocgc runtime.mallocgc
func mallocgc(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

// makeDirtyBytes allocates a []byte of the given length and capacity without
// zeroing the memory. The caller MUST overwrite every byte before reading it.
//
// This is safe for byte slices because bytes contain no pointers — the GC
// never scans their contents. The returned slice is GC-managed (allocated
// via mallocgc), so it does not require manual freeing.
func makeDirtyBytes(len, cap int) []byte {
	var b []byte
	p := mallocgc(uintptr(cap), nil, false)
	sh := (*SliceHeader)(unsafe.Pointer(&b))
	sh.Data = p
	sh.Len = len
	sh.Cap = cap
	return b
}

// SwissMapLayoutOK indicates whether the Go runtime's Swiss Map memory layout
// matches what our C code expects. If false, map[string]string encoding
// falls back to the generic Go-based map iteration (OP_MAP_BEGIN/END).
//
// Set by verifySwissMapLayout during init. May be false if:
//   - The Go runtime's Swiss Map implementation has changed
//   - This is a new Go version with different map internals
//
// When false, map[string]string is still correctly encoded, just slower.
var SwissMapLayoutOK bool

// SwissMapStrIntLayoutOK indicates whether the Go runtime's Swiss Map layout
// for map[string]int matches what our C code expects.
var SwissMapStrIntLayoutOK bool

// SwissMapStrInt64LayoutOK indicates whether the Go runtime's Swiss Map layout
// for map[string]int64 matches what our C code expects.
var SwissMapStrInt64LayoutOK bool

func init() {
	// Verify our sliceHeader layout assumption matches the Go runtime.
	s := make([]byte, 1, 2)
	sh := (*SliceHeader)(unsafe.Pointer(&s))
	if sh.Len != 1 || sh.Cap != 2 || sh.Data == nil {
		panic("vjson: unexpected slice memory layout — sliceHeader assumption violated")
	}

	// Verify mapsIter buffer is large enough for maps.Iter by doing a
	// round-trip: init an iterator on a 1-element map and check it works.
	// If mapsIterSize is too small, the Init/Next calls will corrupt the
	// stack — this test catches that during startup.
	//
	// This MUST succeed — there is no fallback for broken maps.Iter layout.
	m := map[string]string{"__vjson_init_check__": "ok"}
	mt := rtypePtr(reflect.TypeFor[map[string]string]())
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))
	var it mapsIter
	mapsIterInit(mt, mp, &it)
	if mapsIterKey(&it) == nil {
		panic("vjson: mapsIter size mismatch — maps.Iter layout may have changed")
	}

	// Verify Swiss Map memory layout for C-native map iteration.
	// C code (OP_MAP_STR_STR/OP_MAP_STR_INT/OP_MAP_STR_INT64) directly reads
	// these structs — any layout change in the Go runtime would silently
	// corrupt output.
	//
	// Unlike the mapsIter check above, this is NOT fatal — we can fall back
	// to Go-based map iteration (OP_MAP_BEGIN/END) if the layout doesn't match.
	SwissMapLayoutOK = verifySwissMapLayout()
	SwissMapStrIntLayoutOK = verifySwissMapStrIntLayout()
	SwissMapStrInt64LayoutOK = verifySwissMapStrInt64Layout()
}

// verifySwissMapLayout checks that the Go runtime's Swiss Map struct offsets
// match what our C code assumes (GoSwissMap/GoSwissTable in types.h).
// Returns true if the layout matches, false otherwise.
//
// When this returns false, map[string]string encoding will fall back to
// the generic Go-based map iteration path (slower but correct).
func verifySwissMapLayout() bool {
	m := map[string]string{"a": "b"}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m)) // *maps.Map

	// Map.used at offset 0 should be 1
	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}

	// Map.dirLen at offset 24 should be 0 (small map with 1 entry)
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}

	// Map.dirPtr at offset 16 is the group pointer (small map)
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	// Group: ctrl bytes at offset 0, slots at offset 8.
	// Find the full slot (ctrl byte with bit 7 clear).
	ctrls := *(*uint64)(dirPtr)
	foundSlot := -1
	for i := range 8 {
		ctrl := byte(ctrls >> (i * 8))
		if ctrl&0x80 == 0 { // full slot
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		return false
	}

	// Read key GoString from slot: group + 8 + slot*32
	const slotSize = 32
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	// Read elem GoString from slot: group + 8 + slot*32 + 16
	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*string)(elemPtr)
	return elem == "b"
}

// verifySwissMapStrIntLayout checks that map[string]int Swiss Map layout
// matches what our C code assumes (slot_size=24, elem_off=16, group_size=200).
func verifySwissMapStrIntLayout() bool {
	m := map[string]int{"a": 42}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	// Map.used at offset 0 should be 1
	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}

	// Map.dirLen at offset 24 should be 0 (small map)
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}

	// Map.dirPtr at offset 16 is the group pointer (small map)
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	// Find the full slot
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

	// Read key GoString from slot: group + 8 + slot*24
	const slotSize = 24
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	// Read elem int from slot: group + 8 + slot*24 + 16
	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*int)(elemPtr)
	return elem == 42
}

// verifySwissMapStrInt64Layout checks that map[string]int64 Swiss Map layout
// matches what our C code assumes (slot_size=24, elem_off=16, group_size=200).
func verifySwissMapStrInt64Layout() bool {
	m := map[string]int64{"a": 42}
	mp := *(*unsafe.Pointer)(unsafe.Pointer(&m))

	// Map.used at offset 0 should be 1
	used := *(*uint64)(mp)
	if used != 1 {
		return false
	}

	// Map.dirLen at offset 24 should be 0 (small map)
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return false
	}

	// Map.dirPtr at offset 16 is the group pointer (small map)
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return false
	}

	// Find the full slot
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

	// Read key GoString from slot: group + 8 + slot*24
	const slotSize = 24
	const elemOff = 16
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize)
	key := *(*string)(keyPtr)
	if key != "a" {
		return false
	}

	// Read elem int64 from slot: group + 8 + slot*24 + 16
	elemPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*slotSize+elemOff)
	elem := *(*int64)(elemPtr)
	return elem == 42
}

// probeSwissMapSlotSize determines the slot size for a map[string]V type
// by creating a test map with one entry and inspecting the Swiss Map memory layout.
// Returns the slot size and true if successful, or 0 and false if the layout
// doesn't match expectations (e.g. different Go runtime version).
//
// Requires SwissMapLayoutOK as a precondition (GoSwissMap/GoSwissTable structs OK).
func probeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	if !SwissMapLayoutOK {
		return 0, false
	}
	if mapType.Key().Kind() != reflect.String {
		return 0, false
	}

	// Expected slot size: GoString (16 bytes) + value size, aligned to 8 bytes.
	expectedSlotSize := (16 + valSize + 7) &^ 7

	// Create a 1-element map using the runtime's makemap + mapassign_faststr.
	// This avoids reflect.Value.Pointer() issues.
	mt := rtypePtr(mapType)
	mp := makemap(mt, 1, nil)
	valPtr := mapassign_faststr(mt, mp, "__vjson_probe__")
	// Zero-initialize the value (mapassign returns uninitialized memory).
	for i := uintptr(0); i < valSize; i++ {
		*(*byte)(unsafe.Add(valPtr, i)) = 0
	}

	// Verify Map.used == 1
	used := *(*uint64)(mp)
	if used != 1 {
		return 0, false
	}

	// Verify Map.dirLen == 0 (small map with 1 entry)
	dirLen := *(*int64)(unsafe.Add(mp, 24))
	if dirLen != 0 {
		return 0, false
	}

	// Map.dirPtr at offset 16 is the group pointer (small map)
	dirPtr := *(*unsafe.Pointer)(unsafe.Add(mp, 16))
	if dirPtr == nil {
		return 0, false
	}

	// Find the full slot (ctrl byte with bit 7 clear)
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

	// Verify key at expected position: group + 8 + slot * expectedSlotSize
	keyPtr := unsafe.Add(dirPtr, 8+uintptr(foundSlot)*expectedSlotSize)
	key := *(*string)(keyPtr)
	if key != "__vjson_probe__" {
		return 0, false
	}

	return expectedSlotSize, true
}

// ---- Low-level map iteration via go:linkname ----
//
// We bypass both reflect.MapRange (which allocates reflect.Values per entry)
// and the runtime's linknameIter shim (which heap-allocates a maps.Iter via
// new(maps.Iter) on every mapiterinit call).
//
// Instead, we linkname directly to the Swiss Map iterator methods:
//   - internal/runtime/maps.(*Iter).Init
//   - internal/runtime/maps.(*Iter).Next
//
// and use a stack-allocated buffer matching the maps.Iter layout.
//
// maps.Iter layout (internal/runtime/maps/table.go):
//
//	type Iter struct {
//	    key         unsafe.Pointer  // offset 0  — current key ptr (nil = end)
//	    elem        unsafe.Pointer  // offset 8  — current elem ptr
//	    typ         *abi.MapType    // offset 16
//	    m           *Map            // offset 24
//	    entryOffset uint64          // offset 32
//	    dirOffset   uint64          // offset 40
//	    clearSeq    uint64          // offset 48
//	    globalDepth uint8           // offset 56
//	    // 7 bytes padding
//	    dirIdx      int             // offset 64
//	    tab         *table          // offset 72
//	    group       groupReference  // offset 80 (1 pointer = 8 bytes)
//	    entryIdx    uint64          // offset 88
//	}   // total: 96 bytes on 64-bit
//
// After Init + Next, key/elem at offsets 0/8 point directly into the
// SwissTable slots — for inline types (size <= 128 bytes), these are
// direct pointers (e.g. for map[string]string, key points to a
// GoString{ptr,len} in the slot).

// mapsIterSize is the size of internal/runtime/maps.Iter on 64-bit platforms.
// This MUST match unsafe.Sizeof(maps.Iter{}). Verified at init time.
const mapsIterSize = 96 //nolint:unused

// mapsIter is an opaque, stack-allocatable buffer matching maps.Iter.
// Uses uintptr (not unsafe.Pointer) to prevent the GC stack scanner from
// treating internal integer fields (entryOffset, dirIdx, etc.) as pointers,
// which would cause "bad pointer in frame" panics during stack copying.
// This is safe because Go's GC is non-moving, and the map itself (kept alive
// by the caller) retains all referenced objects.
// Zero value is a valid pre-Init state.
type mapsIter struct {
	buf [12]uintptr // 96 bytes, matches maps.Iter size
}

// mapsIterKey returns the current key pointer from the iterator.
// nil indicates end of iteration. Must be called after Init+Next.
func mapsIterKey(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[0]))
}

// mapsIterElem returns the current elem pointer from the iterator.
// nil indicates end of iteration. Must be called after Init+Next.
func mapsIterElem(it *mapsIter) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&it.buf[1]))
}

// mapsIterInit initializes a stack-allocated maps.Iter and advances to
// the first entry. After return, mapsIterKey(it) is the first key or
// nil if the map is empty.
//
// Parameters:
//   - t: *abi.MapType, obtainable via rtypePtr(reflect.TypeFor[map[K]V]())
//   - m: the map header pointer (*maps.Map)
//   - it: pointer to a zeroed mapsIter buffer
func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	_mapsIterInit(unsafe.Pointer(it), t, m)
	_mapsIterNext(unsafe.Pointer(it))
}

// mapsIterNext advances the iterator to the next entry.
// After return, mapsIterKey(it) is the next key or nil if done.
func mapsIterNext(it *mapsIter) {
	_mapsIterNext(unsafe.Pointer(it))
}

// _mapsIterInit is the direct linkname to maps.(*Iter).Init.
// Signature: func(it *maps.Iter, typ *abi.MapType, m *maps.Map)
//
//go:linkname _mapsIterInit internal/runtime/maps.(*Iter).Init
//go:noescape
func _mapsIterInit(it unsafe.Pointer, typ unsafe.Pointer, m unsafe.Pointer)

// _mapsIterNext is the direct linkname to maps.(*Iter).Next.
// Signature: func(it *maps.Iter)
//
//go:linkname _mapsIterNext internal/runtime/maps.(*Iter).Next
//go:noescape
func _mapsIterNext(it unsafe.Pointer)

// ---- Legacy shim-based iteration (kept for reference/fallback) ----

// GoMapIterator mirrors runtime.linknameIter. The runtime's mapiterinit
// heap-allocates a maps.Iter internally (new(maps.Iter)). Prefer the
// stack-based mapsIter above for hot paths.
type GoMapIterator struct {
	Key  unsafe.Pointer // current key ptr (nil = end of iteration)
	Elem unsafe.Pointer // current elem ptr
	Typ  unsafe.Pointer // *abi.MapType (opaque, set by runtime)
	It   unsafe.Pointer // *maps.Iter (opaque, allocated by runtime)
}

//go:linkname mapiterinit runtime.mapiterinit
func mapiterinit(t unsafe.Pointer, m unsafe.Pointer, it *GoMapIterator)

//go:linkname mapiternext runtime.mapiternext
func mapiternext(it *GoMapIterator)

// maplen returns the number of entries in the map.
// m is the map header pointer (same as mapiterinit's m parameter).
//
//go:linkname maplen reflect.maplen
func maplen(m unsafe.Pointer) int
