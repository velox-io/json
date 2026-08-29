package bind

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/dom"
)

// Probe tests covering ***T in every container context: slice elem, struct
// field, fixed-array elem, map value. Not exhaustive, just enough to confirm
// the PTR chain works across all four dispatch sites. Struct/map/root cases run
// through parity3 (encoding/json + JSON bind + tape-bind); slice cases use
// runProbeNormalizeSlice (nil-vs-empty tolerant) extended with a tape-bind leg.

type triplePtrHolder struct {
	V ***int32 `json:"v"`
}

func TestProbeTriplePtrStructField(t *testing.T) {
	cases := []string{`{"v":42}`, `{"v":null}`, `{}`, `{"v":0}`}
	for i, in := range cases {
		parity3[triplePtrHolder](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbeTriplePtrSliceElem(t *testing.T) {
	cases := []string{`[1,2,3]`, `[]`, `[null,42]`}
	for _, in := range cases {
		runProbeNormalizeSlice[***int32](t, in)
	}
}

type fixedArrTriplePtr struct {
	Arr [2]***int32 `json:"arr"`
}

func TestProbeTriplePtrFixedArray(t *testing.T) {
	cases := []string{`{"arr":[1,2]}`, `{"arr":[0,0]}`, `{"arr":[null,42]}`}
	for i, in := range cases {
		parity3[fixedArrTriplePtr](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbeTriplePtrMapValue(t *testing.T) {
	cases := []string{`{"a":1,"b":2}`, `{}`, `{"a":null,"b":42}`}
	for i, in := range cases {
		parity3[map[string]***int32](t, fmt.Sprintf("case%d", i), in)
	}
}

// Deeper chains (4 layers) to stress the loop.

type rootStruct struct {
	V int32 `json:"v"`
}

// Root pointer chains whose ULTIMATE type is a scalar already work (the C
// root_scalar path resolves the remaining PTR layers). These guard that.
func TestProbeDoublePtrRootScalar(t *testing.T) {
	for i, in := range []string{`42`, `null`, `0`, `-7`} {
		parity3[**int32](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbeTriplePtrRootScalar(t *testing.T) {
	for i, in := range []string{`42`, `null`, `0`, `-7`} {
		parity3[***int32](t, fmt.Sprintf("case%d", i), in)
	}
}

// Root pointer chains whose ULTIMATE type is a CONTAINER (struct/{). Historically
// the Go driver peeled only one PTR layer; now resolved, so these pass parity3
// across both bind paths.
func TestProbeDoublePtrRootStruct(t *testing.T) {
	for i, in := range []string{`{"v":7}`, `{"v":0}`, `null`, `{}`} {
		parity3[**rootStruct](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbeTriplePtrRootStruct(t *testing.T) {
	for i, in := range []string{`{"v":7}`, `{"v":0}`, `null`, `{}`} {
		parity3[***rootStruct](t, fmt.Sprintf("case%d", i), in)
	}
}

// Root pointer to map: the user-facing root type is *map[string]T, so the
// Unmarshaler receives **map[string]T. JSON null must leave the pointer nil;
// {} or a populated object must allocate the map and point at it. The any
// variant uses runAnyDiff so nested []any nil-vs-empty divergence is tolerated.
func TestProbePtrRootMapStringString(t *testing.T) {
	for i, in := range []string{`{"a":"b","c":"d"}`, `{}`, `null`} {
		parity3[*map[string]string](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbePtrRootMapStringInt(t *testing.T) {
	for i, in := range []string{`{"a":1,"b":-2}`, `{}`, `null`} {
		parity3[*map[string]int](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbePtrRootMapStringStruct(t *testing.T) {
	for i, in := range []string{`{"a":{"v":1},"b":{"v":2}}`, `{}`, `null`} {
		parity3[*map[string]rootStruct](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbePtrRootMapStringAny(t *testing.T) {
	// Root **map[string]any with complex any values. Direct nested any is
	// supported by tape-bind via t_any_value, so this runs through parity3
	// (encoding/json + Unmarshal + UnmarshalValue).
	cases := []string{
		`{"a":1,"b":"two","c":true,"d":null}`,
		`{}`,
		`null`,
		`{"nested":{"x":1,"y":2}}`,
		`{"arr":[1,2,3]}`,
		`{"deep":{"arr":[{"k":"v"}]}}`,
		`{"f":1.5,"i":42,"s":"str","b":false}`,
	}
	for i, in := range cases {
		parity3[*map[string]any](t, fmt.Sprintf("case%d", i), in)
	}
}

type quadPtrField struct {
	V ****int32 `json:"v"`
}

func TestProbeQuadPtrStructField(t *testing.T) {
	cases := []string{`{"v":7}`, `{"v":null}`, `{}`}
	for i, in := range cases {
		parity3[quadPtrField](t, fmt.Sprintf("case%d", i), in)
	}
}

func TestProbeQuadPtrSliceElem(t *testing.T) {
	cases := []string{`[1,2,3]`, `[null]`, `[]`}
	for _, in := range cases {
		runProbeNormalizeSlice[****int32](t, in)
	}
}

// runProbeNormalizeSlice runs in through encoding/json, Unmarshal, and
// UnmarshalValue (tape-bind), comparing the two nbind paths element-by-element
// with nil-vs-empty tolerance (encoding/json represents JSON [] as nil; nbind as
// an empty slice). The encoding/json leg asserts error-presence parity only.
func runProbeNormalizeSlice[T any](t *testing.T, in string) {
	t.Helper()
	var gj, vb, vv []T
	errJ := json.Unmarshal([]byte(in), &gj)
	errV := Unmarshal([]byte(in), &vb)
	val, errP := dom.Parse([]byte(in))
	if errP != nil {
		t.Fatalf("input=%s: dom.Parse: %v", in, errP)
	}
	errVV := UnmarshalValue(val, &vv)
	if (errJ == nil) != (errV == nil) || (errV == nil) != (errVV == nil) {
		t.Errorf("input=%s: error mismatch json=%v nbind=%v nval=%v", in, errJ, errV, errVV)
		return
	}
	if errV != nil {
		return
	}
	// nbind vs nval (tape-bind): both nil or both empty → equal.
	if len(vb) == 0 && len(vv) == 0 {
		return
	}
	if len(vb) != len(vv) {
		t.Errorf("input=%s: nbind vs nval len mismatch nbind=%d nval=%d", in, len(vb), len(vv))
		return
	}
	for i := range vb {
		if !reflect.DeepEqual(vb[i], vv[i]) {
			t.Errorf("input=%s: elem %d nbind=%+v nval=%+v", in, i, vb[i], vv[i])
		}
	}
}
