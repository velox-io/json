package typ

import (
	"reflect"
	"unsafe"
)

// streamTypePredicate identifies stream.Stream[T] field types so that
// buildUniType can route them to KindStream instead of KindStruct.
//
// typ cannot import the stream package (stream imports decode/option which
// would create cycles through downstream consumers), so the stream package
// registers its predicate at init time via RegisterStreamTypePredicate.
//
// The predicate is consulted once per type, inside buildUniType's special-alias
// switch, before the reflect.Struct branch would otherwise recurse into the
// stream field's internal storage.
var streamTypePredicate func(reflect.Type) bool

// RegisterStreamTypePredicate installs the predicate that recognizes
// stream.Stream[T] instantiations. Called from the stream package's init.
// Registering more than once replaces the predicate; the stream package is
// the only intended caller.
func RegisterStreamTypePredicate(fn func(reflect.Type) bool) {
	streamTypePredicate = fn
}

// IsStreamType reports whether t is a stream.Stream[T] instantiation. Returns
// false when no predicate has been registered (e.g. when the stream package
// has not been imported by the build).
func IsStreamType(t reflect.Type) bool {
	if streamTypePredicate == nil {
		return false
	}
	return streamTypePredicate(t)
}

// StreamElementType returns the reflect.Type of T for a stream.Stream[T]
// instantiation, or nil if t is not a stream type. It calls the ElemType()
// method through reflect because reflect has no public API to read generic
// type parameters. Used by buildUniType to construct a synthetic SliceTypeInfo.
func StreamElementType(t reflect.Type) reflect.Type {
	if !IsStreamType(t) {
		return nil
	}
	// reflect.New(t) returns a pointer to a zero Stream[T]; method-by-name
	// dispatches on the pointer receiver (Stream[T] methods are *Stream[T]).
	out := reflect.New(t).MethodByName("ElemType").Call(nil)
	if len(out) != 1 {
		return nil
	}
	rt, _ := out[0].Interface().(reflect.Type)
	return rt
}

// EmptySliceDataFor returns a pointer to a zero-length slice's backing for
// the element type, matching the EmptySliceData convention buildSliceTypeInfo
// uses for real slice types. Used by buildUniType when constructing a synthetic
// SliceTypeInfo for stream.Stream[T].
func EmptySliceDataFor(elemType reflect.Type) unsafe.Pointer {
	emptySlice := reflect.MakeSlice(reflect.SliceOf(elemType), 0, 0)
	return unsafe.Pointer(emptySlice.Pointer())
}
