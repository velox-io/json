package tests

import (
	"encoding/json"
	"errors"
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

// A mismatched array element or map value aborts UnmarshalValue with an
// *UnmarshalTypeError naming the type that rejected the value, the same
// type Unmarshal names for the same document.
func TestUnmarshalValue_ElementMismatchNamesElementType(t *testing.T) {
	type field struct {
		B []int `json:"b"`
	}
	for _, c := range []struct {
		name string
		doc  string
		mk   func() any
	}{
		{"slice string", `[1,"x"]`, func() any { return new([]int) }},
		{"slice bool", `[true]`, func() any { return new([]string) }},
		{"slice object", `[{}]`, func() any { return new([]int) }},
		{"slice array", `[[1]]`, func() any { return new([]bool) }},
		{"slice of slices", `[[1,"x"]]`, func() any { return new([][]int) }},
		{"slice of maps", `[{"k":"x"}]`, func() any { return new([]map[string]int) }},
		{"fixed array", `[1,"x"]`, func() any { return new([2]int) }},
		{"map value", `{"k":"x"}`, func() any { return new(map[string]int) }},
		{"map object", `{"k":{}}`, func() any { return new(map[string][]int) }},
		{"map of slices", `{"k":[1,"x"]}`, func() any { return new(map[string][]int) }},
		{"field slice", `{"b":[1,"x"]}`, func() any { return new(field) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := vjson.Parse([]byte(c.doc))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var got, want *json.UnmarshalTypeError
			if err := vjson.UnmarshalValue(v, c.mk()); !errors.As(err, &got) {
				t.Fatalf("UnmarshalValue error = %v, want *UnmarshalTypeError", err)
			}
			if err := vjson.Unmarshal([]byte(c.doc), c.mk()); !errors.As(err, &want) {
				t.Fatalf("Unmarshal error = %v, want *UnmarshalTypeError", err)
			}
			if got.Type != want.Type {
				t.Errorf("UnmarshalValue names %v, Unmarshal names %v", got.Type, want.Type)
			}
		})
	}
}

type valueNumHost struct {
	I   int64   `json:"i"`
	N   int     `json:"n"`
	I8  int8    `json:"i8"`
	U   uint64  `json:"u"`
	F32 float32 `json:"f32"`
	S   string  `json:"s"`
}

// A number its target cannot hold is an *UnmarshalTypeError from
// UnmarshalValue as from Unmarshal, wherever the number sits: an unsigned
// value past the int64 range never wraps into a signed target, and the walk
// after a mismatched field stays on the document's structure.
func TestUnmarshalValue_NumberMismatch(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
		mk   func() any
	}{
		{"int64 past max", `{"i":9223372036854775808}`, newOf[valueNumHost]()},
		{"int past max", `{"n":18446744073709551615}`, newOf[valueNumHost]()},
		{"int8 from uint64 max", `{"i8":18446744073709551615}`, newOf[valueNumHost]()},
		{"int8 overflow", `{"i8":128}`, newOf[valueNumHost]()},
		{"negative unsigned", `{"u":-1}`, newOf[valueNumHost]()},
		{"fraction for int", `{"i":1.5}`, newOf[valueNumHost]()},
		{"float32 overflow", `{"f32":1e50}`, newOf[valueNumHost]()},
		{"mismatch before fields", `{"i8":1.5,"s":"x","i":2,"u":3}`, newOf[valueNumHost]()},
		{"element past int64", `[1,9223372036854775808]`, newOf[[]int64]()},
		{"element from uint64 max", `[18446744073709551615]`, newOf[[]int8]()},
		{"map value past int64", `{"k":9223372036854775808}`, newOf[map[string]int64]()},
		{"root past int64", `9223372036854775808`, newOf[int64]()},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := vjson.Parse([]byte(c.doc))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := c.mk()
			var te, want *json.UnmarshalTypeError
			if err := vjson.UnmarshalValue(v, got); !errors.As(err, &te) {
				t.Fatalf("UnmarshalValue error = %v (%T), bound %s, want *UnmarshalTypeError", err, err, dbgAny(got))
			}
			if err := json.Unmarshal([]byte(c.doc), c.mk()); !errors.As(err, &want) {
				t.Fatalf("encoding/json error = %v, want *UnmarshalTypeError", err)
			}
			if te.Type != want.Type {
				t.Errorf("UnmarshalValue names %v, encoding/json names %v", te.Type, want.Type)
			}
		})
	}
}
