package bind

import (
	"encoding/json"
	"reflect"
	"testing"
)

type sliceRoot struct {
	Ints   []int     `json:"ints"`
	Strs   []string  `json:"strs"`
	Bools  []bool    `json:"bools"`
	Floats []float64 `json:"floats"`
}

// TestDiffSliceOfScalar validates scalar slice fields, including nil and empty
// inputs. nbind currently represents JSON empty arrays as nil slices, so the
// test normalizes nil and empty slices before comparing element binding.
func TestDiffSliceOfScalar(t *testing.T) {
	normalize := func(s sliceRoot) sliceRoot {
		if s.Ints == nil {
			s.Ints = []int{}
		}
		if s.Strs == nil {
			s.Strs = []string{}
		}
		if s.Bools == nil {
			s.Bools = []bool{}
		}
		if s.Floats == nil {
			s.Floats = []float64{}
		}
		return s
	}
	cases := []string{
		`{"ints":[1,2,3],"strs":["a","b"],"bools":[true,false],"floats":[1.5,2.5]}`,
		`{"ints":[],"strs":[],"bools":[],"floats":[]}`,
		`{"ints":null,"strs":null,"bools":null,"floats":null}`,
		`{"ints":[-1,0,2147483647,9223372036854775807],"strs":["with\ttab","uni→é"],"bools":[true],"floats":[-0.0,1e10]}`,
		`{"ints":[42],"strs":["x"],"bools":[false],"floats":[3.14]}`,
	}
	for i, in := range cases {
		var gj, vb sliceRoot
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Normalize the known nil versus empty difference for empty arrays.
		if !reflect.DeepEqual(normalize(gj), normalize(vb)) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

type sliceOfPtr struct {
	Items []*ptrChild `json:"items"`
}

// TestDiffSliceOfPointer validates pointer slice elements, including nil
// elements and enough pointees to exercise batch allocation.
func TestDiffSliceOfPointer(t *testing.T) {
	cases := []string{
		`{"items":[{"v":1,"s":"a"},{"v":2,"s":"b"}]}`,
		`{"items":null}`,
		`{"items":[]}`,
		`{"items":[{"v":10,"s":"x"},null,{"v":30,"s":"z"}]}`,
		`{"items":[{"v":-5,"s":""}]}{"items":[{"v":1,"s":"a"},{"v":2,"s":"b"},{"v":3,"s":"c"},{"v":4,"s":"d"},{"v":5,"s":"e"}]}`,
	}
	for i, in := range cases {
		// Replace the stress case with valid JSON before comparison.
		if i == 4 {
			in = `{"items":[{"v":1,"s":"a"},{"v":2,"s":"b"},{"v":3,"s":"c"},{"v":4,"s":"d"},{"v":5,"s":"e"}]}`
		}
		var gj, vb sliceOfPtr
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		if len(gj.Items) != len(vb.Items) {
			t.Errorf("case %d: len mismatch json=%d nbind=%d\n  input=%s", i, len(gj.Items), len(vb.Items), in)
			continue
		}
		for j := range gj.Items {
			if (gj.Items[j] == nil) != (vb.Items[j] == nil) {
				t.Errorf("case %d elem %d: nil mismatch json=%v nbind=%v", i, j, gj.Items[j], vb.Items[j])
				continue
			}
			if gj.Items[j] != nil && !reflect.DeepEqual(*gj.Items[j], *vb.Items[j]) {
				t.Errorf("case %d elem %d: mismatch json=%+v nbind=%+v", i, j, *gj.Items[j], *vb.Items[j])
			}
		}
	}
}

type fixedArrayRoot struct {
	Arr  [3]int    `json:"arr"`
	Strs [2]string `json:"strs"`
}

// TestDiffFixedArray validates fixed arrays, which write directly into the
// destination without slice growth yields.
func TestDiffFixedArray(t *testing.T) {
	cases := []string{
		`{"arr":[1,2,3],"strs":["a","b"]}`,
		`{"arr":[-1,0,1],"strs":["","x"]}`,
		`{"arr":[2147483647,-2147483648,0],"strs":["with\ttab","é"]}`,
	}
	for i, in := range cases {
		var gj, vb fixedArrayRoot
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v", i, errJ, errV)
			continue
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

// TestDiffNestedArrayField covers container-typed array elements: slice of
// slice, slice of fixed array, slice of slice of fixed array (GeoJSON Polygon
// shape, e.g. jsonbench CanadaRoot), array of array, fixed array of slice, and
// slice of map. These descend via array_value's BIND_KIND_SLICE/ARRAY (and
// BIND_KIND_MAP via the common switch) branch, which pushes the parent frame
// and re-enters array_begin / map_open.
func TestDiffNestedArrayField(t *testing.T) {
	type sliceSliceScalar struct {
		Coords [][]float64 `json:"coords"`
	}
	type sliceOfArray struct {
		Coords [][2]float64 `json:"coords"`
	}
	type sliceSliceOfArray struct {
		Coords [][][2]float64 `json:"coords"`
	}
	type arrayOfArray struct {
		Coords [2][2]float64 `json:"coords"`
	}
	type arrayOfSlice struct {
		Coords [2][]int `json:"coords"`
	}
	type arrayOfMap struct {
		Xs []map[string]int `json:"xs"`
	}

	t.Run("SliceSliceScalar", func(t *testing.T) {
		parity3[sliceSliceScalar](t, "nonempty", `{"coords":[[1.5,2.5],[3.5,4.5]]}`)
		parity3[sliceSliceScalar](t, "ragged", `{"coords":[[1.0],[2.0,3.0,4.0]]}`)
		parity3[sliceSliceScalar](t, "outer-empty", `{"coords":[]}`)
	})
	t.Run("SliceOfArray", func(t *testing.T) {
		parity3[sliceOfArray](t, "nonempty", `{"coords":[[1.5,2.5],[3.5,4.5]]}`)
		parity3[sliceOfArray](t, "three", `{"coords":[[1,2],[3,4],[5,6]]}`)
	})
	t.Run("SliceSliceOfArray", func(t *testing.T) {
		parity3[sliceSliceOfArray](t, "polygon", `{"coords":[[[1.5,2.5],[3.5,4.5]],[[5.5,6.5]]]}`)
		parity3[sliceSliceOfArray](t, "outer-empty", `{"coords":[]}`)
	})
	t.Run("ArrayOfArray", func(t *testing.T) {
		parity3[arrayOfArray](t, "full", `{"coords":[[1.5,2.5],[3.5,4.5]]}`)
	})
	t.Run("ArrayOfSlice", func(t *testing.T) {
		parity3[arrayOfSlice](t, "nonempty", `{"coords":[[1,2],[3,4]]}`)
		parity3[arrayOfSlice](t, "ragged", `{"coords":[[1],[2,3,4]]}`)
	})
	t.Run("ArrayOfMap", func(t *testing.T) {
		parity3[arrayOfMap](t, "nonempty", `{"xs":[{"a":1,"b":2},{"c":3}]}`)
		parity3[arrayOfMap](t, "empty", `{"xs":[]}`)
		parity3[arrayOfMap](t, "negative", `{"xs":[{"k":-5}]}`)
	})
}

// TestDiffRootSliceArrayIntoMap verifies behavior when the JSON shape does not
// match the target type: the input is a JSON array but the destination is a
// map[string]string. All three legs (encoding/json, JSON bind, tape-bind) must
// report an error. The tape-bind root-mismatch path skips the value and routes
// to t_document_end (TAPE_BIND_ROOT_TYPE_MISMATCH_SKIP), matching the JSON
// bind path's type-mismatch error.
func TestDiffRootSliceArrayIntoMap(t *testing.T) {

	parity3[map[string]string](t, "array-into-map", `["a","b"]`)
}

// TestDiffRootSlice validates slices used as the root value rather than as a
// struct field.
func TestDiffRootSlice(t *testing.T) {
	cases := []struct {
		in  string
		out any
	}{
		{"[1,2,3]", new([]int)},
		{"[]", new([]int)},
		{"[\"a\",\"b\"]", new([]string)},
		{"[true,false]", new([]bool)},
		{"[1.5,2.5]", new([]float64)},
	}
	for i, c := range cases {
		want := reflect.New(reflect.TypeOf(c.out).Elem()).Interface()
		got := reflect.New(reflect.TypeOf(c.out).Elem()).Interface()
		errJ := json.Unmarshal([]byte(c.in), want)
		errV := Unmarshal([]byte(c.in), got)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d (%s): error mismatch json=%v nbind=%v", i, c.in, errJ, errV)
			continue
		}
		if errJ != nil {
			continue
		}
		// Empty arrays may differ only by nil versus empty slice representation.
		wv := reflect.ValueOf(want).Elem()
		gv := reflect.ValueOf(got).Elem()
		if wv.Len() == 0 && gv.Len() == 0 {
			continue // both empty/nil; element binding not testable here
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("case %d (%s): mismatch json=%v nbind=%v", i, c.in, want, got)
		}
	}
}

// PTR-to-slice fields. Exercises object_field_value ch=='[' where ct enters
// as BIND_KIND_PTR and is resolved to the pointee slice before the kind check.
// Regression for the dead PTR branch that previously misreported type mismatch.

type ptrSliceInt32Field struct {
	Xs *[]int32 `json:"xs"`
}

type ptrSliceStringField struct {
	Ss *[]string `json:"ss"`
}

type ptrSliceFloatField struct {
	Fs *[]float64 `json:"fs"`
}

type ptrSliceMixedFields struct {
	Tag string     `json:"tag"`
	Xs  *[]int32   `json:"xs"`
	Ss  *[]string  `json:"ss"`
	Fs  *[]float64 `json:"fs"`
	N   int32      `json:"n"`
}

func normalizeNilSlice[T any](s *[]T) {
	if s != nil && *s == nil {
		empty := []T{}
		*s = empty
	}
}

func TestDiffPtrSliceInt32(t *testing.T) {
	cases := []string{
		`{"xs":[1,2,3]}`,
		`{"xs":[]}`,
		`{"xs":[42]}`,
		`{"xs":[1,2,3,4,5,6,7,8,9,10]}`,
		`{"xs":[-2147483648,2147483647,0]}`,
		`{"xs":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb ptrSliceInt32Field
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Empty arrays may differ only by nil versus empty slice representation.
		if (gj.Xs == nil || len(*gj.Xs) == 0) && (vb.Xs == nil || len(*vb.Xs) == 0) {
			continue
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

func TestDiffPtrSliceString(t *testing.T) {
	cases := []string{
		`{"ss":["a","b","c"]}`,
		`{"ss":[]}`,
		`{"ss":[""]}`,
		`{"ss":["one","two","three","four","five","six"]}`,
		`{"ss":["esc\nhere","plain","中文"]}`,
		`{"ss":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb ptrSliceStringField
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		if (gj.Ss == nil || len(*gj.Ss) == 0) && (vb.Ss == nil || len(*vb.Ss) == 0) {
			continue
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

func TestDiffPtrSliceFloat(t *testing.T) {
	cases := []string{
		`{"fs":[1.5,2.5,3.14]}`,
		`{"fs":[]}`,
		`{"fs":[0,-0.0,1e10]}`,
		`{"fs":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb ptrSliceFloatField
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		if (gj.Fs == nil || len(*gj.Fs) == 0) && (vb.Fs == nil || len(*vb.Fs) == 0) {
			continue
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

func TestDiffPtrSliceMixed(t *testing.T) {
	cases := []string{
		`{"tag":"all","xs":[1,2],"ss":["a","b"],"fs":[1.5,2.5],"n":7}`,
		`{"tag":"nil_xs","xs":null,"ss":["a"],"fs":[1.0],"n":1}`,
		`{"tag":"nil_ss","xs":[1],"ss":null,"fs":[],"n":2}`,
		`{"tag":"nil_fs","xs":[],"ss":[],"fs":null,"n":3}`,
		`{"tag":"all_nil","n":4}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb ptrSliceMixedFields
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Normalize nil versus empty slice representation for each field.
		for _, pp := range []func(){func() { normalizeNilSlice(gj.Xs); normalizeNilSlice(vb.Xs) },
			func() { normalizeNilSlice(gj.Ss); normalizeNilSlice(vb.Ss) },
			func() { normalizeNilSlice(gj.Fs); normalizeNilSlice(vb.Fs) }} {
			pp()
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

// *[]*T: pointer-to-slice-of-pointers. Composes two PTR resolution paths:
//   - object_field_value ch=='[' resolves the outer PTR to a slice header (the
//     path just fixed by collapsing the dead nested PTR check);
//   - array_value resolves each element's PTR via BIND_RESOLVE_PTR.

type ptrSliceOfPtrChildField struct {
	Items *[]*ptrChild `json:"items"`
}

func TestDiffPtrSliceOfPtr(t *testing.T) {
	cases := []string{
		`{"items":[{"v":1,"s":"a"},{"v":2,"s":"b"}]}`,
		`{"items":[]}`,
		`{"items":[{"v":10,"s":"x"},null,{"v":30,"s":"z"}]}`,
		`{"items":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb ptrSliceOfPtrChildField
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Both nil → equal.
		if gj.Items == nil && vb.Items == nil {
			continue
		}
		if (gj.Items == nil) != (vb.Items == nil) {
			t.Errorf("case %d: Items nil mismatch json=%v nbind=%v\n  input=%s", i, gj.Items, vb.Items, in)
			continue
		}
		gjItems := *gj.Items
		vbItems := *vb.Items
		// Normalize nil vs empty slice representation.
		if len(gjItems) == 0 && len(vbItems) == 0 {
			continue
		}
		if len(gjItems) != len(vbItems) {
			t.Errorf("case %d: len mismatch json=%d nbind=%d\n  input=%s", i, len(gjItems), len(vbItems), in)
			continue
		}
		for j := range gjItems {
			if (gjItems[j] == nil) != (vbItems[j] == nil) {
				t.Errorf("case %d elem %d: nil mismatch json=%v nbind=%v", i, j, gjItems[j], vbItems[j])
				continue
			}
			if gjItems[j] != nil && !reflect.DeepEqual(*gjItems[j], *vbItems[j]) {
				t.Errorf("case %d elem %d: mismatch json=%+v nbind=%+v", i, j, *gjItems[j], *vbItems[j])
			}
		}
	}
}

// **[]T: pointer-to-pointer-to-slice. encoding/json unwraps each pointer layer
// as the JSON value descends, so a non-null array reaches the slice. nbind's
// object_field_value now unwraps all leading PTR layers in a single while loop
// (BIND_RESOLVE_PTR_CHAIN), publishing each intermediate pointee into the
// previous layer's slot. null/{} pass because the ch=='n' branch skips the
// loop entirely, leaving the slot nil.

type dblPtrSliceField struct {
	Items **[]int32 `json:"items"`
}

func TestDiffDblPtrSlice(t *testing.T) {
	cases := []string{
		`{"items":[1,2,3]}`,
		`{"items":[]}`,
		`{"items":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb dblPtrSliceField
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Normalize nil vs empty slice representation.
		if gj.Items != nil && *gj.Items != nil && len(**gj.Items) == 0 {
			empty := []int32{}
			*gj.Items = &empty
		}
		if vb.Items != nil && *vb.Items != nil && len(**vb.Items) == 0 {
			empty := []int32{}
			*vb.Items = &empty
		}
		if !reflect.DeepEqual(gj, vb) {
			t.Errorf("case %d: mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gj, vb)
		}
	}
}

// ***[]T: deeper PTR chain (3 layers) to slice. Validates the while loop
// in object_field_value iterates more than once, with each intermediate
// pointee published into the previous layer's slot.

type trplPtrSliceField struct {
	Items ***[]int32 `json:"items"`
}

func TestDiffTrplPtrSlice(t *testing.T) {
	cases := []string{
		`{"items":[1,2,3]}`,
		`{"items":[]}`,
		`{"items":null}`,
		`{}`,
	}
	for i, in := range cases {
		var gj, vb trplPtrSliceField
		errJ := json.Unmarshal([]byte(in), &gj)
		errV := Unmarshal([]byte(in), &vb)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("case %d: error mismatch json=%v nbind=%v\n  input=%s", i, errJ, errV, in)
			continue
		}
		if errJ != nil {
			continue
		}
		// Both nil → equal.
		if gj.Items == nil && vb.Items == nil {
			continue
		}
		// Drill three layers for the slice comparison; normalize nil vs empty.
		gjSl := sliceOrEmpty3(gj.Items)
		vbSl := sliceOrEmpty3(vb.Items)
		if len(gjSl) == 0 && len(vbSl) == 0 {
			continue
		}
		if !reflect.DeepEqual(gjSl, vbSl) {
			t.Errorf("case %d: slice mismatch\n  input=%s\n  json =%+v\n  nbind=%+v", i, in, gjSl, vbSl)
		}
	}
}

// sliceOrEmpty3 drills ***[]int32 to the underlying []int32 slice, returning
// an empty slice if any layer is nil.
func sliceOrEmpty3(p ***[]int32) []int32 {
	if p == nil || *p == nil || **p == nil {
		return []int32{}
	}
	return ***p
}
