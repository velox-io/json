package bind

import (
	"fmt"
	"testing"
)

type ptrChild struct {
	V int    `json:"v"`
	S string `json:"s"`
}

type ptrRoot struct {
	Name  string    `json:"name"`
	Child *ptrChild `json:"child"`
	Num   *int      `json:"num"`
}

// TestDiffPointerFields compares pointer field allocation and null handling
// across encoding/json, the JSON bind path, and the tape-bind path (parity3).
// Covers allocator-backed pointees and the final root handoff path.
func TestDiffPointerFields(t *testing.T) {
	cases := []string{
		`{"name":"root","child":{"v":1,"s":"a"},"num":42}`,
		`{"name":"r","child":null,"num":null}`,
		`{"name":"multi","child":{"v":10,"s":"x"},"num":-7}`,
	}
	for i, in := range cases {
		parity3[ptrRoot](t, fmt.Sprintf("case%d", i), in)
	}
}

type nestedDeep struct {
	A struct {
		B struct {
			C struct {
				D int `json:"d"`
			} `json:"c"`
		} `json:"b"`
	} `json:"a"`
}

// TestDiffNestedStruct validates nested struct frames (depth > 1) across both
// bind paths via parity3.
func TestDiffNestedStruct(t *testing.T) {
	cases := []string{
		`{"a":{"b":{"c":{"d":42}}}}`,
		`{"a":{"b":{"c":{"d":0}}}}`,
		`{"a":{"b":{"c":{"d":-100}}}}`,
	}
	for i, in := range cases {
		parity3[nestedDeep](t, fmt.Sprintf("case%d", i), in)
	}
}

type ptrChain struct {
	Name string    `json:"name"`
	Next *ptrChain `json:"next"`
}

// TestDiffPtrChain exercises recursive pointer chains so TypeTree cycle indexes
// must be resolved correctly. Test inputs are finite (no cycles), so parity3's
// reflect.DeepEqual is safe across both bind paths.
func TestDiffPtrChain(t *testing.T) {
	cases := []string{
		`{"name":"a","next":{"name":"b","next":{"name":"c","next":null}}}`,
		`{"name":"solo","next":null}`,
		`{"name":"deep","next":{"name":"d1","next":{"name":"d2","next":{"name":"d3","next":null}}}}`,
	}
	for i, in := range cases {
		parity3[ptrChain](t, fmt.Sprintf("case%d", i), in)
	}
}

// Multi-layer PTR to scalar fields. Validates that object_field_value's
// while loop unwraps multiple pointer layers before the scalar kind check,
// publishing each intermediate pointee into the previous layer's slot.

type dblPtrScalarField struct {
	S **string `json:"s"`
	I **int    `json:"i"`
}

func TestDiffDblPtrScalar(t *testing.T) {
	cases := []string{
		`{"s":"hello","i":42}`,
		`{"s":null,"i":null}`,
		`{}`,
		`{"s":"","i":0}`, // empty/zero → both ends hold nil chain
	}
	for i, in := range cases {
		parity3[dblPtrScalarField](t, fmt.Sprintf("case%d", i), in)
	}
}
