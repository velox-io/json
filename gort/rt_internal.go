package gort

import (
	"reflect"
	"unsafe"
	_ "unsafe" // required for go:linkname
)

//go:linkname UnsafeNew reflect.unsafe_New
//go:noescape
func UnsafeNew(typ unsafe.Pointer) unsafe.Pointer //nolint:revive

//go:linkname UnsafeNewArray reflect.unsafe_NewArray
//go:noescape
func UnsafeNewArray(typ unsafe.Pointer, n int) unsafe.Pointer //nolint:revive

//go:linkname TypedSliceCopy runtime.typedslicecopy
//go:nosplit
func TypedSliceCopy(typ unsafe.Pointer, dstPtr unsafe.Pointer, dstLen int, srcPtr unsafe.Pointer, srcLen int) int

//go:linkname MakeMap runtime.makemap
func MakeMap(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer

// runtimeRand mirrors runtime.rand: per-m chacha8, nosplit, explicitly exposed
// for linkname. Used by mapSeed to seed batch-allocated maps without going
// through MakeMap/NewMap (which re-seed per map).
//
//go:linkname runtimeRand runtime.rand
func runtimeRand() uint64

// mapSeed returns a random uintptr for Map.seed. One call per InitMapSlots
// batch replaces N per-map NewMap re-seeds.
func mapSeed() uintptr {
	return uintptr(runtimeRand())
}

// map_presize is runtime.makemap with m != nil (reuse path).
// MapPresize wraps it; do not call this directly.
//
//go:linkname map_presize runtime.makemap
func map_presize(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer //nolint:revive

// MapPresize preallocates buckets on an empty map for hint entries.
// No-op when m == nil, hint <= 8, or the map is non-empty (safety net
// against orphaning existing entries). The hint<=8 guard is intentional:
// map_presize for small maps costs as much as the natural smallmap growth
// it would avoid (profiled: makemap overhead matches growToSmall savings).
// The *hmap pointer is unchanged; only the internal directory grows.
//
// Note: for hint 9-16 (a single full region), presize sizes the directory for
// ~hint entries in one alloc, avoiding the incremental growTable copies that
// growToDir+fill would incur. Skipping it is a net loss, so prewired maps still
// go through here (the re-seed + discarded prewired group are cheaper than the
// extra growTable allocs).
func MapPresize(t unsafe.Pointer, hint int, m unsafe.Pointer) {
	if m == nil || hint <= 8 || MapLen(m) > 0 {
		return
	}
	map_presize(t, hint, m)
}

//go:linkname MapAssign runtime.mapassign
func MapAssign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

// MapMaxElemBytes mirrors abi.MapMaxElemBytes: above this size Go stores a map
// element indirectly, so the slot holds a *V and the runtime allocates the V.
// It is the gate on MapAssignFastStr, which cannot perform that allocation.
const MapMaxElemBytes = 128

// MapValueIsIndirect reports whether a map with this element size stores its
// elements behind a pointer, which is exactly when MapAssignFastStr must not be
// used. Callers that assign map values by string key should evaluate this once
// per map type at build time and keep the answer in their own metadata rather
// than asking here on the hot path.
func MapValueIsIndirect(elemSize uintptr) bool { return elemSize > MapMaxElemBytes }

// MapAssignFastStr is valid only for a map whose element Go stores inline, i.e.
// one where MapValueIsIndirect(sizeof(V)) is false.
//
// Above that size the slot holds a *V that the runtime allocates on assignment.
// runtime.mapassign does that and returns the element storage; this faststr
// variant does neither, so it hands back the pointer slot itself and a caller
// writing the value there lands on top of the pointer. Nothing reports an error:
// the map publishes an element whose words are whatever was written, so a string
// or slice header ends up addressing arbitrary memory, and the table's own
// bookkeeping can be overwritten too (observed: len() over-reporting, lookups
// probing forever).
//
// The compiler never emits this call above the limit (walk.mapfast falls back to
// mapslow) and reflect gates on the same size, so callers here must gate as well.
// Use MapAssign for indirect elements.
//
//go:linkname MapAssignFastStr runtime.mapassign_faststr
func MapAssignFastStr(t unsafe.Pointer, m unsafe.Pointer, key string) unsafe.Pointer //nolint:revive

//go:linkname MapLen reflect.maplen
//go:noescape
func MapLen(m unsafe.Pointer) int

//go:linkname TypedMemmove runtime.typedmemmove
func TypedMemmove(typ unsafe.Pointer, dst unsafe.Pointer, src unsafe.Pointer)

// Memmove is a direct wrapper of runtime.memmove. Prefer it over TypedMemmove
// only when both src and dst hold no GC pointers (noscan data): it skips the
// ptrmask scan and write barrier that TypedMemmove performs.
//
//go:linkname Memmove runtime.memmove
//go:noescape
func Memmove(dst unsafe.Pointer, src unsafe.Pointer, n uintptr)

// MemclrHasPointers zeroes n bytes at ptr, honoring GC pointer semantics
// (drops any pointers in the range so the GC no longer traces them). Used to
// clear typed, GC-scannable buffers (e.g. drained map entry slots) so stale
// pointers into since-freed memory are never scanned.
//
//go:linkname MemclrHasPointers runtime.memclrHasPointers
//go:noescape
func MemclrHasPointers(ptr unsafe.Pointer, n uintptr)

// MemclrNoHeapPointers zeroes n bytes at ptr without GC pointer semantics.
// Use for noscan memory (e.g. a []byte map buffer) where the range holds no
// GC pointers the runtime must trace. Cheaper than MemclrHasPointers because
// it skips the write barrier.
//
//go:linkname MemclrNoHeapPointers runtime.memclrNoHeapPointers
//go:noescape
func MemclrNoHeapPointers(ptr unsafe.Pointer, n uintptr)

//go:linkname MallocGC runtime.mallocgc
func MallocGC(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

type GoIface struct {
	Tab  unsafe.Pointer
	Data unsafe.Pointer
}

func ExtractItab(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return (*GoIface)(ifacePtr).Tab
}

func TypePtr(t reflect.Type) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(&t), unsafe.Sizeof(uintptr(0))))
}

// reflectTypeItab is the itab shared by all reflect.Type interface values.
// reflect.Type is an interface; every reflect.Type returned by the runtime
// has the same itab (the one for *reflect.rtype implementing reflect.Type).
// We extract it once from a known type and reuse it to construct fresh
// reflect.Type values from raw rtype pointers without going through reflect.
var reflectTypeItab = extractReflectTypeItab()

func extractReflectTypeItab() unsafe.Pointer {
	t := reflect.TypeFor[int]()
	// reflect.Type is an interface: layout is {itab, data}. The data word
	// is the rtype pointer.
	return *(*unsafe.Pointer)(unsafe.Pointer(&t))
}

// TypeFromRType constructs a reflect.Type for the given rtype pointer without
// calling into reflect. It works by reusing the shared reflect.Type itab and
// substituting the rtype as the interface's data word.
//
// The returned reflect.Type is valid for the lifetime of the program (rtype
// pointers are immutable runtime structures).
func TypeFromRType(rtype unsafe.Pointer) reflect.Type {
	// reflect.Type layout: {itab, data}. We assemble it via GoIface and
	// then reinterpret as reflect.Type.
	hdr := GoIface{Tab: reflectTypeItab, Data: rtype}
	return *(*reflect.Type)(unsafe.Pointer(&hdr))
}

// EfaceRType returns the rtype of an empty interface (any) value. ifacePtr
// points at the `any` variable. Returns nil for a nil interface.
//
// eface layout: {_type, data}. _type IS the rtype.
func EfaceRType(ifacePtr unsafe.Pointer) unsafe.Pointer {
	return *(*unsafe.Pointer)(ifacePtr)
}

// IfaceConcreteRType returns the rtype of a non-empty interface value.
// ifacePtr points at the interface variable. Returns nil for a nil interface.
//
// runtime.itab layout: { *interfacetype, *_type, ... }. The rtype is at
// offset 8 (one pointer width).
func IfaceConcreteRType(ifacePtr unsafe.Pointer) unsafe.Pointer {
	itab := *(*unsafe.Pointer)(ifacePtr)
	if itab == nil {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Add(itab, unsafe.Sizeof(uintptr(0))))
}

type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// StringHeader mirrors the runtime string layout {Data, Len}. Used by unsafe
// paths that read or write Go strings via raw pointer casts. Like SliceHeader,
// it depends on the runtime layout staying stable.
type StringHeader struct {
	Data unsafe.Pointer
	Len  uintptr
}

// MakeDirtyBytes allocates a []byte without zeroing. Caller MUST overwrite
// every byte before reading. Safe because bytes have no pointers for GC.
func MakeDirtyBytes(len, cap int) []byte {
	var b []byte
	p := MallocGC(uintptr(cap), nil, false)
	sh := (*SliceHeader)(unsafe.Pointer(&b))
	sh.Data = p
	sh.Len = len
	sh.Cap = cap
	return b
}
