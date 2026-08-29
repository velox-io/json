package vjson

import "github.com/velox-io/json/vcopy"

// DeepCopy produces a deep copy of v using the precompiled type descriptors
// shared with the marshal/unmarshal path. It is substantially faster than a
// reflect-driven deep copy and avoids the JSON roundtrip entirely.
//
// v's type must be representable by the vjson type system (no chan, func, or
// unsafe.Pointer). Scalars are returned by value; slices, maps, and pointers
// are freshly allocated. Strings are copied to new backing storage.
// Cyclic graphs are not supported; the type graph must be acyclic.
func DeepCopy[T any](v T) (T, error) {
	return vcopy.DeepCopy(v)
}

// CopyInto deep-copies src into *dst using the vjson type system. It is the
// allocation-light variant of DeepCopy: the result is written in place,
// avoiding the final reflect.Value boxing.
func CopyInto[T any](src T, dst *T) error {
	return vcopy.CopyInto(src, dst)
}
