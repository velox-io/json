package vbind

import (
	"sync"

	"github.com/velox-io/json/native/vlib"
	"github.com/velox-io/json/typ"
)

// Each shared struct type has one immutable lookup entry. The process-wide
// cache also keeps every blob reachable for native metadata.
var structLookupCache sync.Map

// Fieldless structs use a NONE sentinel so native code can perform lookup
// unconditionally. Package storage gives its address a stable process lifetime.
var emptyLookupSentinel [4]byte

type lookupEntry struct {
	blob []byte
}

// getStructLookup builds each struct's process-wide lookup once. The cache owns
// the blob backing used by native metadata.
func getStructLookup(si *typ.StructTypeInfo) ([]byte, error) {
	if v, ok := structLookupCache.Load(si); ok {
		return v.(*lookupEntry).blob, nil
	}
	blob, err := buildStructLookup(si)
	if err != nil {
		return nil, err
	}
	e := &lookupEntry{blob: blob}
	actual, _ := structLookupCache.LoadOrStore(si, e)
	return actual.(*lookupEntry).blob, nil
}

// buildStructLookup preserves Fields order in the lookup result, so each index
// is relative to the struct's first TypeTree field.
func buildStructLookup(si *typ.StructTypeInfo) ([]byte, error) {
	n := len(si.Fields)
	if n == 0 {
		return nil, nil
	}
	keys := make([]string, n)
	for i := range si.Fields {
		keys[i] = si.Fields[i].JSONName
	}
	blob, rc := vlib.Build(keys, vlib.TiersAll)
	if rc <= 0 {
		return nil, &fieldLookupError{si: si, code: rc}
	}
	return blob, nil
}

type fieldLookupError struct {
	si   *typ.StructTypeInfo
	code int32
}

func (e *fieldLookupError) Error() string {
	return "vbind: cannot build field lookup for struct (lookup build failed)"
}
