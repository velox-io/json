package vjson

import (
	"github.com/velox-io/json/vbind"
)

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
