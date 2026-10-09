package tests

import (
	"encoding/json"
	"reflect"
	"testing"

	vjson "github.com/velox-io/json"
)

// assertSameValueDecode pins that UnmarshalValue over a parsed doc binds what
// encoding/json binds from the text, on error presence and on the value.
func assertSameValueDecode(t *testing.T, name, doc string, mk func() any) {
	t.Helper()
	v, err := vjson.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("%s: Parse: %v", name, err)
	}
	got, want := mk(), mk()
	vjErr := vjson.UnmarshalValue(v, got)
	stdErr := json.Unmarshal([]byte(doc), want)
	if (vjErr == nil) != (stdErr == nil) {
		t.Errorf("%s: error divergence: std=%v vjson=%v", name, stdErr, vjErr)
		return
	}
	if stdErr == nil && !reflect.DeepEqual(got, want) {
		t.Errorf("%s: value divergence:\n  std:   %s\n  vjson: %s", name, dbgAny(want), dbgAny(got))
	}
}

type valueMergeLeaf struct{ A, B int }

type valueMergeHost struct {
	In  valueMergeLeaf
	P   *valueMergeLeaf
	Arr [2]valueMergeLeaf
	M   map[string]valueMergeLeaf
}

// UnmarshalValue merges an object into the struct already in place, as
// Unmarshal does: a field the object omits keeps its value, whether the
// struct is a field, a pointee, or a fixed array element. A map value is a
// fresh entry either way.
func TestUnmarshalValue_MergesIntoStructs(t *testing.T) {
	mk := func() any {
		return &valueMergeHost{
			In:  valueMergeLeaf{1, 1},
			P:   &valueMergeLeaf{2, 2},
			Arr: [2]valueMergeLeaf{{3, 3}, {4, 4}},
			M:   map[string]valueMergeLeaf{"k": {5, 5}},
		}
	}
	assertSameValueDecode(t, "merge",
		`{"In":{"A":9},"P":{"A":9},"Arr":[{"A":9},{}],"M":{"k":{"A":9}}}`, mk)
	assertSameValueDecode(t, "empty objects", `{"In":{},"P":{},"Arr":[{},{}]}`, mk)
}
