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

// map_presize is runtime.makemap with m != nil (reuse path).
// MapPresize wraps it; do not call this directly.
//
//go:linkname map_presize runtime.makemap
func map_presize(t unsafe.Pointer, hint int, m unsafe.Pointer) unsafe.Pointer //nolint:revive

// MapPresize preallocates buckets on an empty map for hint entries.
// No-op when m == nil, hint <= 8 (MapGroupSlots, small map path), or
// m is non-empty (MapLen > 0). The *hmap pointer is unchanged; only
// the internal directory grows. Caller should ensure m is empty, but
// the MapLen check is a safety net against misuse that would orphan
// existing entries.
func MapPresize(t unsafe.Pointer, hint int, m unsafe.Pointer) {
	if m == nil || hint <= 8 || MapLen(m) > 0 {
		return
	}
	map_presize(t, hint, m)
}

//go:linkname MapAssign runtime.mapassign
func MapAssign(t unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

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
// clear typed, GC-scanned buffers (e.g. drained map entry slots) so stale
// pointers into since-freed memory are never scanned.
//
//go:linkname MemclrHasPointers runtime.memclrHasPointers
//go:noescape
func MemclrHasPointers(ptr unsafe.Pointer, n uintptr)

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
