package bind

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

// any_value diff tests. Compare bind.Unmarshal against encoding/json for
// types containing any / interface{} / []any / map[string]any. The native
// state machine boxes JSON values into eface {type_ptr, data_ptr} via the
// BindAnyMeta metadata block; this file exercises each JSON value kind and
// each container context (struct field, slice elem, map value, root).
//
// runAnyDiff runs a 3-leg comparison (encoding/json + JSON bind + tape-bind via
// UnmarshalValue) so the tape-bind any-boxing path is covered by the same
// corpus. runAnyDiff2 (2-leg) is retained for *any / root-any types, which
// tape-bind rejects at UnmarshalValue entry with TapeBindUnsupportedError
// (TypeTree.TapeBindUnsupported flag, computed at Build time).

type anyField struct {
	V any `json:"v"`
}

type ptrAnyField struct {
	V *any `json:"v"`
}

// runAnyDiff unmarshals `in` into encoding/json, Unmarshal (JSON bind), and
// UnmarshalValue (tape-bind) targets, then compares with anyEqual (a
// reflect.DeepEqual variant that treats nil and empty slices as equal:
// encoding/json represents JSON [] as []T{} while nbind represents it as nil,
// matching the typed-slice behavior). All three legs must agree on error
// presence and, on success, produce anyEqual values.
func runAnyDiff[T any](t *testing.T, in string, dest func() *T) {
	t.Helper()
	jDest := dest()
	vDest := dest()
	errJ := json.Unmarshal([]byte(in), jDest)
	errV := Unmarshal([]byte(in), vDest)
	val, errP := dom.Parse([]byte(in))
	var vvDest *T
	var errVV error
	if errP != nil {
		errVV = errP
	} else {
		vvDest = dest()
		errVV = UnmarshalValue(val, vvDest)
	}
	if (errJ == nil) != (errV == nil) || (errV == nil) != (errVV == nil) {
		t.Errorf("input=%s: error mismatch json=%v nbind=%v nval=%v", in, errJ, errV, errVV)
		return
	}
	if errJ != nil {
		return
	}
	if !anyEqual(jDest, vDest) {
		t.Errorf("input=%s: json vs nbind mismatch\n  json =%+v\n  nbind=%+v", in, jDest, vDest)
	}
	if !anyEqual(vDest, vvDest) {
		t.Errorf("input=%s: nbind vs nval (tape-bind) mismatch\n  nbind=%+v\n  nval =%+v", in, vDest, vvDest)
	}
}

// runAnyDiff2 is the 2-leg (encoding/json + JSON bind) variant for types the
// tape-bind path rejects at entry (root any/iface, *any/*iface field). The
// JSON bind path supports these via its cold-path fallback; tape-bind returns
// TapeBindUnsupportedError without entering the C walk.
func runAnyDiff2[T any](t *testing.T, in string, dest func() *T) {
	t.Helper()
	jDest := dest()
	vDest := dest()
	errJ := json.Unmarshal([]byte(in), jDest)
	errV := Unmarshal([]byte(in), vDest)
	if (errJ == nil) != (errV == nil) {
		t.Errorf("input=%s: error mismatch json=%v nbind=%v", in, errJ, errV)
		return
	}
	if errJ != nil {
		return
	}
	if !anyEqual(jDest, vDest) {
		t.Errorf("input=%s: mismatch\n  json =%+v\n  nbind=%+v", in, jDest, vDest)
	}
}

// anyEqual reports whether two values are deep-equal, treating nil and empty
// slices as equal. Walks through any / []T / map[K]V / struct recursively so
// the nil-vs-empty divergence on nested []any does not cause spurious failures.
func anyEqual(a, b any) bool {
	return anyEqualValue(reflect.ValueOf(a), reflect.ValueOf(b))
}

func anyEqualValue(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Kind() != b.Kind() {
		// Allow nil-interface vs nil-typed-ptr to compare unequal (encoding/json
		// and nbind both leave a nil any field nil for JSON null).
		return false
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() && b.IsNil()
		}
		return anyEqualValue(a.Elem(), b.Elem())
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() && b.IsNil()
		}
		return anyEqualValue(a.Elem(), b.Elem())
	case reflect.Slice:
		if a.Len() == 0 && b.Len() == 0 {
			return true
		}
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !anyEqualValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		iter := a.MapRange()
		for iter.Next() {
			bv := b.MapIndex(iter.Key())
			if !bv.IsValid() {
				return false
			}
			if !anyEqualValue(iter.Value(), bv) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !anyEqualValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	case reflect.String:
		return a.String() == b.String()
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}

func TestDiffAnyScalar(t *testing.T) {
	cases := []string{
		`{"v":42}`,
		`{"v":3.14}`,
		`{"v":"hello"}`,
		`{"v":true}`,
		`{"v":false}`,
		`{"v":null}`,
		`{"v":""}`,
		`{"v":0}`,
		`{"v":-1.5}`,
		`{"v":"中文"}`,
		`{"v":"with\"escape"}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *anyField { return new(anyField) })
	}
}

func TestDiffAnySliceField(t *testing.T) {
	type sliceAnyField struct {
		Vs []any `json:"vs"`
	}
	cases := []string{
		`{"vs":[1, "two", true, null]}`,
		`{"vs":[]}`,
		`{"vs":null}`,
		`{}`,
		`{"vs":[[1,2],[3,4]]}`,
		`{"vs":[{"a":1},{"b":2}]}`,
		`{"vs":[1.5,2.5,3.5]}`,
		`{"vs":["a","b","c"]}`,
		`{"vs":[true,false,true]}`,
		`{"vs":[null,null,null]}`,
		`{"vs":[[1,"a"],[2,"b"],[3,"c"]]}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *sliceAnyField { return new(sliceAnyField) })
	}
}

func TestDiffAnyMapField(t *testing.T) {
	type mapAnyField struct {
		M map[string]any `json:"m"`
	}
	cases := []string{
		`{"m":{"a":1,"b":"two","c":true,"d":null}}`,
		`{"m":{}}`,
		`{"m":null}`,
		`{}`,
		`{"m":{"nested":{"x":1,"y":2}}}`,
		`{"m":{"arr":[1,2,3]}}`,
		`{"m":{"deep":{"arr":[{"k":"v"}]}}}`,
		`{"m":{"f":1.5,"i":42,"s":"str","b":false}}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *mapAnyField { return new(mapAnyField) })
	}
}

// TestDiffPtrAnyMapField exercises *map[string]any. The pointer layer adds two
// behaviors absent from the bare map case: JSON null and a missing field must
// both leave M as a nil pointer, while {} must allocate an empty map and point
// at it. runAnyDiff's Pointer branch unwraps the layer before comparing.
func TestDiffPtrAnyMapField(t *testing.T) {
	type ptrMapAnyField struct {
		M *map[string]any `json:"m"`
	}
	cases := []string{
		`{"m":{"a":1,"b":"two","c":true,"d":null}}`,
		`{"m":{}}`,
		`{"m":null}`,
		`{}`,
		`{"m":{"nested":{"x":1,"y":2}}}`,
		`{"m":{"arr":[1,2,3]}}`,
		`{"m":{"deep":{"arr":[{"k":"v"}]}}}`,
		`{"m":{"f":1.5,"i":42,"s":"str","b":false}}`,
		`{"m":{"a":null,"b":null}}`,
		`{"m":{"arr":[]}}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *ptrMapAnyField { return new(ptrMapAnyField) })
	}
}

func TestDiffAnyRoot(t *testing.T) {
	// Root *any: tape-bind rejects at UnmarshalValue entry with
	// TapeBindUnsupportedError (root any after PTR unwrap). The JSON bind
	// path supports it, so this stays on 2-leg runAnyDiff2.
	cases := []string{
		`42`,
		`3.14`,
		`"hello"`,
		`true`,
		`false`,
		`null`,
		`""`,
		`-1.5`,
		`[1,2,3]`,
		`[]`,
		`{"a":1,"b":"two"}`,
		`{}`,
		`[[1,2],[3,4]]`,
		`{"nested":{"deep":{"value":42}}}`,
		`["a","b","c"]`,
		`{"arr":[1,"two",true,null]}`,
	}
	for _, in := range cases {
		runAnyDiff2(t, in, func() *any { return new(any) })
	}
}

func TestDiffPtrAny(t *testing.T) {
	// *any field: tape-bind rejects at UnmarshalValue entry with
	// TapeBindUnsupportedError (*any/*iface field). The JSON bind path
	// supports it, so this stays on 2-leg runAnyDiff2.
	cases := []string{
		`{"v":42}`,
		`{"v":"str"}`,
		`{"v":null}`,
		`{"v":true}`,
		`{"v":[1,2,3]}`,
		`{"v":{"k":"v"}}`,
		`{}`,
		`{"v":3.14}`,
	}
	for _, in := range cases {
		runAnyDiff2(t, in, func() *ptrAnyField { return new(ptrAnyField) })
	}
}

// TestUnmarshalValueUnsupportedAny verifies that UnmarshalValue rejects
// types containing positions the tape-bind sub-routine cannot walk, returning
// TapeBindUnsupportedError with a non-empty path and reason. The JSON bind
// path (Unmarshal) handles these via its cold-path fallback.
func TestUnmarshalValueUnsupportedAny(t *testing.T) {
	cases := []struct {
		name string
		in   string
		dest func() any
	}{
		{"root-any", `42`, func() any { return new(any) }},
		{"root-any-obj", `{"a":1}`, func() any { return new(any) }},
		{"ptr-any-field", `{"v":1}`, func() any { return new(ptrAnyField) }},
		{"ptr-any-field-null", `{"v":null}`, func() any { return new(ptrAnyField) }},
	}
	for _, c := range cases {
		val, err := dom.Parse([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: dom.Parse: %v", c.name, err)
		}
		err = UnmarshalValue(val, c.dest())
		var uerr *TapeBindUnsupportedError
		if !errors.As(err, &uerr) {
			t.Errorf("%s: want TapeBindUnsupportedError, got %T: %v", c.name, err, err)
			continue
		}
		if uerr.Pos.Path == "" || uerr.Pos.Reason == "" {
			t.Errorf("%s: empty path or reason: %+v", c.name, uerr.Pos)
		}
	}
}

func TestDiffNestedAny(t *testing.T) {
	// Deeply interleaved []any / map[string]any with mixed scalar and
	// container values at every level.
	type nested struct {
		Root map[string]any `json:"root"`
	}
	cases := []string{
		`{"root":{"a":[1,2,{"x":3}],"b":{"c":[true,null,{"d":"e"}]}}}`,
		`{"root":{"list":[{"k1":"v1"},{"k2":"v2"}],"scalar":42}}`,
		`{"root":{"matrix":[[1,2],[3,4],[[5,6],[7,8]]]}}`,
		`{"root":{"deep":{"deep":{"deep":{"deep":{"v":1}}}}}}`,
		`{"root":{"empty_arr":[],"empty_obj":{},"nil":null}}`,
		`{"root":{"mixed":[1,"a",true,null,[1,2],{"k":"v"}]}}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *nested { return new(nested) })
	}
}

func TestDiffAnyArrayElem(t *testing.T) {
	// []any as the root, exercising array_value's ANY dispatch.
	cases := []string{
		`[1,2,3]`,
		`[]`,
		`[null]`,
		`["a","b"]`,
		`[1,"two",3.0,true,null]`,
		`[[1,2],[3,4]]`,
		`[{"a":1},{"b":2}]`,
		`[1,[2,"a"],{"b":true},null,3.14]`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *[]any { return new([]any) })
	}
}

func TestDiffAnyObjectFieldMixed(t *testing.T) {
	// Struct with multiple any fields of differing JSON value kinds in
	// one parse to exercise any_value across the four dispatch sites
	// in a single document.
	type mixed struct {
		A any `json:"a"`
		B any `json:"b"`
		C any `json:"c"`
		D any `json:"d"`
	}
	cases := []string{
		`{"a":1,"b":"two","c":true,"d":null}`,
		`{"a":[1,2],"b":{"x":3},"c":"str","d":false}`,
		`{"a":null,"b":null,"c":null,"d":null}`,
		`{"a":{"nested":{"arr":[1,2,3]}},"b":[true,false],"c":42,"d":"end"}`,
	}
	for _, in := range cases {
		runAnyDiff(t, in, func() *mixed { return new(mixed) })
	}
}

// TestAnyStringSlotNotRecBatch verifies the SlotClass that boxes JSON strings
// when decoding into any does not use the RecBatch carve. any_value stores a
// JSON string as a 16B Go string header carved from the BindAnyMeta
// string_slot_class; eface.data points at that header. The slot is registered
// as a primitive leaf, so SCC analysis leaves it in group 0 (slotBump) rather
// than promoting it to slotRecBatch, which is reserved for recursive slice
// element backings. Native any_value carves the slot with a linear
// block+offset+=elem_size cursor that is only valid for bump-style slots, so a
// RecBatch classification here would corrupt the carve.
//
// Each case builds the canonical TypeTree the pooled Parser uses and
// materializes an Allocator from it (as newParserFromShape does), then drives a
// real Unmarshal through the any path to confirm the scenario is live.
func TestAnyStringSlotNotRecBatch(t *testing.T) {
	cases := []struct {
		name string
		root reflect.Type
		in   string
		dest func() any
	}{
		{"struct-any-field", reflect.TypeFor[anyField](), `{"v":"str"}`, func() any { return new(anyField) }},
		{"map-string-any", reflect.TypeFor[map[string]any](), `{"k":"str"}`, func() any { v := map[string]any{}; return &v }},
		{"slice-any", reflect.TypeFor[[]any](), `["str"]`, func() any { return new([]any) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Unmarshal([]byte(c.in), c.dest()); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			tt, err := vbind.TypeTreeOf(c.root)
			if err != nil {
				t.Fatalf("TypeTreeOf: %v", err)
			}
			if len(tt.AnyMetas) == 0 {
				t.Fatalf("tree reached no any type; AnyTypeIdx=%d", tt.AnyTypeIdx)
			}
			am := tt.Types[tt.AnyTypeIdx].AnyMeta(tt.AnyMetas)
			alloc := vbind.NewAllocator(tt)
			sc := &alloc.Slots[am.StringSlotClass]
			if sc.IsRecBatch() {
				t.Errorf("string slot class (idx %d) is RecBatch; want Bump/RecBump", am.StringSlotClass)
			}
		})
	}
}
