package decoder

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/vdec"
)

func getTypeInfo(t reflect.Type) *vdec.DecTypeInfo {
	return vdec.DecTypeInfoOf(t)
}

//go:linkname memmove runtime.memmove
func memmove(to unsafe.Pointer, from unsafe.Pointer, n uintptr)

// ---- sliceHeader helpers ----

var sliceHeaderRType = func() unsafe.Pointer {
	t := reflect.TypeFor[gort.SliceHeader]()
	return gort.TypePtr(t)
}()

func writeSliceHeader(dst unsafe.Pointer, data unsafe.Pointer, length, capacity int) {
	src := gort.SliceHeader{Data: data, Len: length, Cap: capacity}
	gort.TypedMemmove(sliceHeaderRType, dst, unsafe.Pointer(&src))
}
