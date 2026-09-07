package vjson

import (
	"encoding/json"

	"github.com/velox-io/json/vbind"
	"github.com/velox-io/json/vcopy"
)

type Number = json.Number
type RawMessage = json.RawMessage
type Marshaler = json.Marshaler
type Unmarshaler = json.Unmarshaler

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

// DefineVariantCases defines the fallback variant case set for T.
// See vbind.DefineVariantCases for the full contract.
func DefineVariantCases[T any, D any]() { vbind.DefineVariantCases[T, D]() }

// DefineVariantCasesAt defines the variant case set for one variant field of T,
// named by its Go field name. See vbind.DefineVariantCasesAt for the full contract.
func DefineVariantCasesAt[T any, D any](fieldName string) {
	vbind.DefineVariantCasesAt[T, D](fieldName)
}

// DefineKindofCases defines the JSON kind case set for T.
// See vbind.DefineKindofCases for the full contract.
func DefineKindofCases[T any, D any]() { vbind.DefineKindofCases[T, D]() }
