package bind

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
)

// parity3 runs a JSON snippet through three legs: encoding/json,
// nbind.Unmarshal, and dom.Parse + UnmarshalValue, which drives the tape-bind
// sub-routine (the same path variant/kindof cold paths use). All three legs
// must agree on error presence, and on success all three decoded values must
// DeepEqual. This catches divergences between the JSON bind path (Unmarshal)
// and the tape-bind path (UnmarshalValue) that a single-leg test cannot reach.
func parity3[T any](t *testing.T, name, input string) {
	t.Helper()
	var gj, vb, vv T
	errJ := json.Unmarshal([]byte(input), &gj)
	errV := Unmarshal([]byte(input), &vb)
	val, errP := dom.Parse([]byte(input))
	var errVV error
	if errP == nil {
		errVV = UnmarshalValue(val, &vv)
	} else {
		errVV = errP
	}
	if (errJ == nil) != (errV == nil) || (errV == nil) != (errVV == nil) {
		t.Errorf("%s: error mismatch json=%v nbind=%v nval=%v\n  input=%s", name, errJ, errV, errVV, input)
		return
	}
	if errJ != nil {
		return
	}
	if !reflect.DeepEqual(gj, vb) {
		t.Errorf("%s: json vs nbind mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", name, input, gj, vb)
	}
	if !reflect.DeepEqual(vb, vv) {
		t.Errorf("%s: nbind vs nval (tape-bind) mismatch\n  input=%s\n  nbind=%+v\n  nval =%+v", name, input, vb, vv)
	}
}

type mapStrStr struct {
	M map[string]string `json:"m"`
}

type mapStrInt struct {
	M map[string]int `json:"m"`
}

type mapStrFloat struct {
	M map[string]float64 `json:"m"`
}

type mapStrBool struct {
	M map[string]bool `json:"m"`
}

type innerVal struct {
	X int    `json:"x"`
	S string `json:"s"`
}

type mapStrStruct struct {
	M map[string]innerVal `json:"m"`
}

type mapStrPtrInt struct {
	M map[string]*int `json:"m"`
}

type mapStrPtrStruct struct {
	M map[string]*innerVal `json:"m"`
}

type mapStrMapInt struct {
	M map[string]map[string]int `json:"m"`
}

type mapStrMapMapMapInt struct {
	M map[string]map[string]map[string]map[string]int `json:"m"`
}

// structWithNestedMap places a map inside a struct that itself sits as a map
// value. This stresses the DOM flush invariant: when an inner map's map buffer
// fills mid-struct, the outer slot's struct value must NOT be drained as a
// half-written (x filled, m filling, y not yet) value.
type structWithNestedMap struct {
	X int               `json:"x"`
	M map[string]string `json:"m"`
	Y int               `json:"y"`
}

type mapStrStructWithNestedMap struct {
	M map[string]structWithNestedMap `json:"m"`
}

type mapIntStr struct {
	M map[int]string `json:"m"`
}

type mapUint32Str struct {
	M map[uint32]string `json:"m"`
}

type mapInt64Str struct {
	M map[int64]string `json:"m"`
}

type ptrMapStrStr struct {
	M *map[string]string `json:"m"`
}

type ptrMapStrInt struct {
	M *map[string]int `json:"m"`
}

type ptrMapStrStruct struct {
	M *map[string]innerVal `json:"m"`
}

func TestMapStringString_Empty(t *testing.T) {
	parity3[mapStrStr](t, "empty", `{"m":{}}`)
}

func TestMapStringString_Null(t *testing.T) {
	parity3[mapStrStr](t, "null", `{"m":null}`)
}

func TestMapStringString_Single(t *testing.T) {
	parity3[mapStrStr](t, "single", `{"m":{"a":"b"}}`)
}

func TestMapStringString_Many(t *testing.T) {
	parity3[mapStrStr](t, "many", `{"m":{"a":"1","b":"2","c":"3","d":"4","e":"5","f":"6","g":"7","h":"8"}}`)
}

func TestMapStringString_Escape(t *testing.T) {
	parity3[mapStrStr](t, "escape", `{"m":{"ke\"y":"va\tl","uni→é":"esc\u00e9"}}`)
}

func TestMapStringString_FlushBoundary(t *testing.T) {
	// 33 entries forces one non-closing flush at kv_cap=32, then a closing flush.
	var in strings.Builder
	in.WriteString(`{"m":{`)
	for i := range 33 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + string(rune('a'+i)) + `":"v` + string(rune('a'+i)) + `"`)
	}
	in.WriteString(`}}`)
	parity3[mapStrStr](t, "flush33", in.String())
}

func TestMapStringInt(t *testing.T) {
	parity3[mapStrInt](t, "basic", `{"m":{"a":1,"b":2,"c":-3}}`)
}

func TestMapStringInt_FlushBoundary(t *testing.T) {
	var in strings.Builder
	in.WriteString(`{"m":{`)
	for i := range 33 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + string(rune('a'+i)) + `":` + string(rune('0'+i%10)))
	}
	in.WriteString(`}}`)
	parity3[mapStrInt](t, "flush33", in.String())
}

func TestMapStringFloat64(t *testing.T) {
	parity3[mapStrFloat](t, "basic", `{"m":{"pi":3.14,"e":2.718}}`)
}

func TestMapStringBool(t *testing.T) {
	parity3[mapStrBool](t, "basic", `{"m":{"t":true,"f":false}}`)
}

func TestMapStringString_NullValue(t *testing.T) {
	parity3[mapStrStr](t, "nullval", `{"m":{"a":null,"b":"x"}}`)
}

func TestMapStringInt_NullValue(t *testing.T) {
	parity3[mapStrInt](t, "nullval", `{"m":{"a":null,"b":42}}`)
}

func TestMapStringStruct_Single(t *testing.T) {
	parity3[mapStrStruct](t, "single", `{"m":{"k":{"x":1,"s":"hi"}}}`)
}

func TestMapStringStruct_Many(t *testing.T) {
	parity3[mapStrStruct](t, "many", `{"m":{"a":{"x":1,"s":"a"},"b":{"x":2,"s":"b"},"c":{"x":3,"s":"c"}}}`)
}

func TestMapStringStruct_NullValue(t *testing.T) {
	parity3[mapStrStruct](t, "nullval", `{"m":{"a":null,"b":{"x":5,"s":"b"}}}`)
}

func TestMapStringPtrInt_Basic(t *testing.T) {
	parity3[mapStrPtrInt](t, "basic", `{"m":{"a":1,"b":null,"c":3}}`)
}

func TestMapStringPtrInt_FlushBoundary(t *testing.T) {
	var in strings.Builder
	in.WriteString(`{"m":{`)
	for i := range 33 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + string(rune('a'+i)) + `":` + string(rune('0'+i%10)))
	}
	in.WriteString(`}}`)
	parity3[mapStrPtrInt](t, "flush33", in.String())
}

func TestMapStringPtrStruct_Basic(t *testing.T) {
	parity3[mapStrPtrStruct](t, "basic", `{"m":{"a":{"x":1,"s":"hi"},"b":null}}`)
}

func TestMapStringMapInt_Basic(t *testing.T) {
	parity3[mapStrMapInt](t, "basic", `{"m":{"outer":{"a":1,"b":2},"other":{"c":3}}}`)
}

func TestMapStringMapInt_FlushBoundary(t *testing.T) {
	// Inner and outer maps each exceed kv_cap to exercise nested KV rebase.
	var in strings.Builder
	in.WriteString(`{"m":{`)
	for i := range 33 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + string(rune('a'+i)) + `":{"x":` + string(rune('0'+i%10)) + `}`)
	}
	in.WriteString(`}}`)
	parity3[mapStrMapInt](t, "flush33", in.String())
}

func TestMapStringMapMapMapInt_DeepNested(t *testing.T) {
	// 4-level nested map. Exercises the single-slot push model: each level
	// pushes one entry at a time into the shared map buffer, and the pre-sized
	// capacity (from TypeTree's deepest map-only chain estimate) must cover
	// the depth without triggering a grow.
	parity3[mapStrMapMapMapInt](t, "deep", `{"m":{"a":{"b":{"c":{"d":1,"e":2},"f":{"g":3}},"h":{"i":{"j":4}}},"k":{"l":{"m":{"n":5}}}}}`)
}

// TestMapStringStructWithNestedMap_NoFlush exercises the struct-with-nested-map
// value without crossing the map buffer flush threshold: a baseline that must
// always pass regardless of drain timing correctness.
func TestMapStringStructWithNestedMap_NoFlush(t *testing.T) {
	parity3[mapStrStructWithNestedMap](t, "small", `{"m":{"k":{"x":1,"m":{"a":"v1","b":"v2"},"y":2}}}`)
}

// TestMapStringStructWithNestedMap_InnerFlushBoundary reproduces the DOM flush
// invariant bug: when the inner map's map buffer region fills while parsing the
// inner entries of a struct value, a full-buffer drain at that moment would
// walk the outer slot too. The outer slot's struct value is half-written at
// that point (x filled, m filling, y not yet written), so draining it into
// the outer map via mapassign + copyMapValue would publish a half-finished
// struct that subsequent field writes (y=2) cannot repair.
//
// The test forces enough inner entries to cross the map buffer cap and exercises
// exactly the scenario above; the assertion checks that all of x, m, and y
// on the resulting struct are intact.
func TestMapStringStructWithNestedMap_InnerFlushBoundary(t *testing.T) {
	var in strings.Builder
	in.WriteString(`{"m":{"k":{"x":1,"m":{`)
	for i := range 200 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + strconv.Itoa(i) + `":"v` + strconv.Itoa(i) + `"`)
	}
	in.WriteString(`},"y":2}}}`)
	parity3[mapStrStructWithNestedMap](t, "inner-flush", in.String())
}

// TestMapStringStructWithNestedMap_MultipleOuterEntries exercises the same
// flush invariant across multiple outer entries: each outer value is a
// struct with a nested map that fills the map buffer. Confirms that every outer
// entry, not just the first, ends up with all struct fields intact.
func TestMapStringStructWithNestedMap_MultipleOuterEntries(t *testing.T) {
	var in strings.Builder
	in.WriteString(`{"m":{`)
	for i := range 5 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + strconv.Itoa(i) + `":{"x":` + strconv.Itoa(i) + `,"m":{`)
		for j := range 200 {
			if j > 0 {
				in.WriteString(",")
			}
			in.WriteString(`"kk` + strconv.Itoa(j) + `":"vv` + strconv.Itoa(j) + `"`)
		}
		in.WriteString(`},"y":` + strconv.Itoa(i*100) + `}`)
	}
	in.WriteString(`}}`)
	parity3[mapStrStructWithNestedMap](t, "multi-outer", in.String())
}

func TestPtrMapStringString_Basic(t *testing.T) {
	parity3[ptrMapStrStr](t, "basic", `{"m":{"a":"b"}}`)
}

func TestPtrMapStringString_Null(t *testing.T) {
	parity3[ptrMapStrStr](t, "null", `{"m":null}`)
}

func TestPtrMapStringString_Empty(t *testing.T) {
	parity3[ptrMapStrStr](t, "empty", `{"m":{}}`)
}

func TestPtrMapStringString_Missing(t *testing.T) {
	parity3[ptrMapStrStr](t, "missing", `{}`)
}

func TestPtrMapStringInt_Basic(t *testing.T) {
	parity3[ptrMapStrInt](t, "basic", `{"m":{"a":1,"b":2,"c":-3}}`)
}

func TestPtrMapStringInt_Empty(t *testing.T) {
	parity3[ptrMapStrInt](t, "empty", `{"m":{}}`)
}

func TestPtrMapStringInt_Null(t *testing.T) {
	parity3[ptrMapStrInt](t, "null", `{"m":null}`)
}

func TestPtrMapStringInt_Missing(t *testing.T) {
	parity3[ptrMapStrInt](t, "missing", `{}`)
}

func TestPtrMapStringStruct_Basic(t *testing.T) {
	parity3[ptrMapStrStruct](t, "basic", `{"m":{"a":{"x":1,"s":"p"},"b":{"x":2,"s":"q"}}}`)
}

func TestPtrMapStringStruct_Empty(t *testing.T) {
	parity3[ptrMapStrStruct](t, "empty", `{"m":{}}`)
}

func TestPtrMapStringStruct_Null(t *testing.T) {
	parity3[ptrMapStrStruct](t, "null", `{"m":null}`)
}

func TestPtrMapStringStruct_Missing(t *testing.T) {
	parity3[ptrMapStrStruct](t, "missing", `{}`)
}

func TestMapIntKey_Basic(t *testing.T) {
	parity3[mapIntStr](t, "basic", `{"m":{"1":"a","2":"b","42":"answer"}}`)
}

func TestMapInt64Key_Boundary(t *testing.T) {
	parity3[mapInt64Str](t, "boundary", `{"m":{"9223372036854775807":"max","-9223372036854775808":"min"}}`)
}

func TestMapUint32Key(t *testing.T) {
	parity3[mapUint32Str](t, "basic", `{"m":{"0":"zero","4294967295":"max"}}`)
}

func TestMapRoot_StringString(t *testing.T) {
	parity3[map[string]string](t, "root", `{"a":"b","c":"d"}`)
}

func TestMapRoot_Int(t *testing.T) {
	parity3[map[string]int](t, "root", `{"a":1,"b":2}`)
}

func TestMapRoot_Struct(t *testing.T) {
	parity3[map[string]innerVal](t, "root", `{"k":{"x":1,"s":"hi"},"j":{"x":2,"s":"bye"}}`)
}

func TestMapRoot_IntKey(t *testing.T) {
	parity3[map[int]string](t, "root", `{"1":"a","2":"b"}`)
}

func TestMapRoot_FlushBoundary(t *testing.T) {
	var in strings.Builder
	in.WriteString(`{`)
	for i := range 33 {
		if i > 0 {
			in.WriteString(",")
		}
		in.WriteString(`"k` + string(rune('a'+i)) + `":` + string(rune('0'+i%10)))
	}
	in.WriteString(`}`)
	parity3[map[string]int](t, "rootflush", in.String())
}

// TestDiffMapContainerValue covers map_value descending into a container value
// type: map[string][]T (slice) and map[string][N]T (fixed array). These go
// through map_value's BIND_KIND_SLICE/ARRAY branch.
func TestDiffMapContainerValue(t *testing.T) {
	type mapSliceVal struct {
		M map[string][]int `json:"m"`
	}
	type mapArrayVal struct {
		M map[string][2]int `json:"m"`
	}
	t.Run("SliceValue", func(t *testing.T) {
		parity3[mapSliceVal](t, "nonempty", `{"m":{"a":[1,2],"b":[3,4,5]}}`)
		parity3[mapSliceVal](t, "ragged", `{"m":{"a":[1],"b":[2,3,4,5]}}`)
		parity3[mapSliceVal](t, "empty", `{"m":{"a":[],"b":[1]}}`)
	})
	t.Run("ArrayValue", func(t *testing.T) {
		parity3[mapArrayVal](t, "nonempty", `{"m":{"a":[1,2],"b":[3,4]}}`)
		parity3[mapArrayVal](t, "single", `{"m":{"a":[1,2]}}`)
	})
}
