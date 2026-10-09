package tests

import (
	"encoding/json"
	"io"
	"reflect"
	"testing"

	vjson "github.com/velox-io/json"
)

// arrayTailCases decode into destinations already holding data, so an
// element the JSON array does not supply shows whether it was zeroed.
var arrayTailCases = []struct {
	name string
	doc  string
	mk   func() any
}{
	{"empty", `[]`, func() any { return &[3]int{9, 9, 9} }},
	{"short", `[1]`, func() any { return &[3]int{9, 9, 9} }},
	{"exact", `[1,2,3]`, func() any { return &[3]int{9, 9, 9} }},
	{"surplus", `[1,2,3,4,5]`, func() any { return &[3]int{9, 9, 9} }},
	{"strings", `["a"]`, func() any { return &[3]string{"x", "y", "z"} }},
	{"pointers", `[1]`, func() any { return &[3]*int{new(int), new(int), new(int)} }},
	{"slices", `[[1]]`, func() any { return &[2][]int{{7}, {8}} }},
	{"nested", `[[1],[]]`, func() any { return &[2][2]int{{9, 9}, {9, 9}} }},
	{"nested surplus", `[[1,2,3],[4]]`, func() any { return &[2][2]int{{9, 9}, {9, 9}} }},
	{"field", `{"X":[1]}`, func() any { return &struct{ X [3]int }{X: [3]int{9, 9, 9}} }},
	{"empty field", `{"X":[]}`, func() any { return &struct{ X [3]int }{X: [3]int{9, 9, 9}} }},
	{"map value", `{"k":[1]}`, func() any { return &map[string][3]int{"k": {9, 9, 9}} }},
	{"slice element", `[[1],[2,3]]`, func() any { return &[][3]int{{9, 9, 9}, {9, 9, 9}} }},
}

// A fixed array holds exactly the elements its JSON array supplied: a
// shorter JSON array zeroes the rest, as encoding/json does, on every path
// that binds an array.
func TestUnmarshal_FixedArrayZeroesTail(t *testing.T) {
	for _, c := range arrayTailCases {
		t.Run(c.name, func(t *testing.T) {
			assertSameDecode(t, "Unmarshal", []byte(c.doc), c.mk)
			for chunk := 1; chunk <= len(c.doc); chunk++ {
				assertSameStream(t, "Decoder", []byte(c.doc), chunk, c.mk)
			}
		})
	}
}

// UnmarshalValue binds the parsed tape, a separate walk from the JSON one.
func TestUnmarshalValue_FixedArrayZeroesTail(t *testing.T) {
	for _, c := range arrayTailCases {
		t.Run(c.name, func(t *testing.T) {
			v, err := vjson.Parse([]byte(c.doc))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, want := c.mk(), c.mk()
			if err := vjson.UnmarshalValue(v, got); err != nil {
				t.Fatalf("UnmarshalValue: %v", err)
			}
			if err := json.Unmarshal([]byte(c.doc), want); err != nil {
				t.Fatalf("encoding/json: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("UnmarshalValue = %s, encoding/json = %s", dbgAny(got), dbgAny(want))
			}
		})
	}
}

// A Decoder reusing one destination across documents must not carry the
// previous document's elements into a shorter one.
func TestDecoder_FixedArrayReuse(t *testing.T) {
	const docs = `[1,2,3] [4] [] [5,6]`
	want := [][3]int{{1, 2, 3}, {4, 0, 0}, {0, 0, 0}, {5, 6, 0}}
	for chunk := 1; chunk <= len(docs); chunk++ {
		d := vjson.NewDecoder(&chunkReader{data: []byte(docs), size: chunk})
		var dst [3]int
		for i, w := range want {
			if err := d.Decode(&dst); err != nil {
				t.Fatalf("chunk=%d doc %d: %v", chunk, i, err)
			}
			if dst != w {
				t.Fatalf("chunk=%d doc %d = %v, want %v", chunk, i, dst, w)
			}
		}
		if err := d.Decode(&dst); err != io.EOF {
			t.Fatalf("chunk=%d: trailing Decode = %v, want io.EOF", chunk, err)
		}
	}
}
