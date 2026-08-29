package bind

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/dom"
)

type diffFlat struct {
	Name string  `json:"name"`
	N    int     `json:"n"`
	F    float64 `json:"f"`
	B    bool    `json:"b"`
	U    uint    `json:"u"`
	I8   int8    `json:"i8"`
	I64  int64   `json:"i64"`
}

// TestDiffFlatStruct compares nbind with encoding/json for the simplest struct
// shape that still exercises scalar fields, strings, and shape construction,
// across both bind paths via parity3.
func TestDiffFlatStruct(t *testing.T) {
	cases := []string{
		`{"name":"hello","n":42,"f":3.14,"b":true,"u":7,"i8":-5,"i64":9000000000000}`,
		`{"name":"","n":0,"f":0,"b":false,"u":0,"i8":0,"i64":0}`,
		`{"name":"with \"quotes\"","n":-128,"f":-0.5,"u":4294967295,"i8":127,"i64":-9223372036854775808}`,
		`{"name":"unicode→←","n":2147483647,"f":1e10,"b":true}`,
		`{"name":"tab\tand\nnl","n":1,"f":2.5,"b":false}`,
	}
	for i, in := range cases {
		parity3[diffFlat](t, fmt.Sprintf("case%d", i), in)
	}
}

// TestDiffRootScalar validates scalar roots without container state. Pointer
// root null handling is covered by pointer root tests because it needs the
// driver to skip the handoff store. Runs each scalar through encoding/json,
// Unmarshal (JSON bind), and UnmarshalValue (tape-bind); all three must agree.
func TestDiffRootScalar(t *testing.T) {
	cases := []struct {
		in  string
		out any
	}{
		{"42", new(int)},
		{"-128", new(int8)},
		{"3.14", new(float64)},
		{"true", new(bool)},
		{"false", new(bool)},
		{"\"hello\"", new(string)},
		{"\"with\\tescape\"", new(string)},
	}
	for i, c := range cases {
		// Fresh targets keep json, nbind, and nval writes independent.
		want := reflect.New(reflect.TypeOf(c.out).Elem()).Interface()
		got := reflect.New(reflect.TypeOf(c.out).Elem()).Interface()
		val, errP := dom.Parse([]byte(c.in))
		if errP != nil {
			t.Fatalf("case %d (%s): dom.Parse: %v", i, c.in, errP)
		}
		vv := reflect.New(reflect.TypeOf(c.out).Elem()).Interface()
		errJ := json.Unmarshal([]byte(c.in), want)
		errV := Unmarshal([]byte(c.in), got)
		errVV := UnmarshalValue(val, vv)
		if (errJ == nil) != (errV == nil) || (errV == nil) != (errVV == nil) {
			t.Errorf("case %d (%s): error mismatch json=%v nbind=%v nval=%v", i, c.in, errJ, errV, errVV)
			continue
		}
		if errJ != nil {
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("case %d (%s): json vs nbind mismatch json=%v nbind=%v", i, c.in, want, got)
		}
		if !reflect.DeepEqual(got, vv) {
			t.Errorf("case %d (%s): nbind vs nval (tape-bind) mismatch nbind=%v nval=%v", i, c.in, got, vv)
		}
	}
}
