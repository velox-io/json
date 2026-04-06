package tests

import (
	"encoding/json"
	"reflect"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestUnmarshal_StructFieldAndInterfaceRefSameType
//
// Struct A has a concrete field of type B and an interface field (any) that
// dynamically holds decoded data. Both should unmarshal identically to
// encoding/json.

func TestUnmarshal_StructFieldAndInterfaceRefSameType(t *testing.T) {
	type B struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	type A struct {
		Direct B   `json:"direct"`
		Iface  any `json:"iface"`
	}

	cases := []struct {
		name  string
		input string
	}{
		{"object_value", `{"direct":{"name":"alice","age":30},"iface":{"name":"bob","age":25}}`},
		{"null_iface", `{"direct":{"name":"alice","age":30},"iface":null}`},
		{"number_iface", `{"direct":{"name":"alice","age":30},"iface":42}`},
		{"string_iface", `{"direct":{"name":"alice","age":30},"iface":"hello"}`},
		{"bool_iface", `{"direct":{"name":"alice","age":30},"iface":true}`},
		{"array_iface", `{"direct":{"name":"alice","age":30},"iface":[1,2,3]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want A
			if err := vjson.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
			}
		})
	}
}

// TestUnmarshal_StructWithMapStringAny
//
// Struct with a map[string]any field containing various dynamic value types.

func TestUnmarshal_StructWithMapStringAny(t *testing.T) {
	type WithMap struct {
		Label string         `json:"label"`
		Meta  map[string]any `json:"meta"`
	}

	cases := []struct {
		name  string
		input string
	}{
		{"null_map", `{"label":"test","meta":null}`},
		{"empty_map", `{"label":"test","meta":{}}`},
		{"primitives", `{"label":"prim","meta":{"str":"hello","num":42,"bool":true,"null":null,"neg":-3.14,"zero":0}}`},
		{"nested_map", `{"label":"nested","meta":{"inner":{"x":1,"y":2}}}`},
		{"slice_value", `{"label":"slice","meta":{"items":[1,"two",null,true]}}`},
		{"mixed", `{"label":"mixed","meta":{"name":"test","count":99,"enabled":false,"tags":["a","b"],"nested":{"k":"v"},"nothing":null}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want WithMap
			if err := vjson.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
			}
		})
	}
}

// TestUnmarshal_InterfaceFieldTypes
//
// Struct fields with any/interface{} that receive different JSON value types.

func TestUnmarshal_InterfaceFieldTypes(t *testing.T) {
	type S struct {
		A any `json:"a"`
		B any `json:"b"`
		C any `json:"c"`
		D any `json:"d"`
		E any `json:"e"`
		F any `json:"f"`
	}

	input := `{"a":"string","b":123.5,"c":true,"d":false,"e":null,"f":[1,"x",null]}`

	var got, want S
	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

// TestUnmarshal_NestedInterfaceContainers
//
// Deeply nested any fields: map[string]any inside []any inside map[string]any.

func TestUnmarshal_NestedInterfaceContainers(t *testing.T) {
	input := `{
		"level1": {
			"level2": [
				{"key": "val"},
				[1, 2, 3],
				"plain",
				null,
				true
			]
		},
		"top_array": [
			{"a": 1},
			[null, false, "x"],
			42
		]
	}`

	var got, want map[string]any
	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

// TestUnmarshal_TopLevelAny
//
// Unmarshal various JSON values into a bare any (interface{}).

func TestUnmarshal_TopLevelAny(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"object", `{"name":"alice","age":30}`},
		{"array", `[1,"two",null,true,{"k":"v"}]`},
		{"string", `"hello"`},
		{"number", `42.5`},
		{"integer", `100`},
		{"bool_true", `true`},
		{"bool_false", `false`},
		{"null", `null`},
		{"empty_object", `{}`},
		{"empty_array", `[]`},
		{"nested", `{"a":{"b":{"c":[1,2,3]}}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want any
			if err := vjson.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %#v\n  want: %#v", got, want)
			}
		})
	}
}

// TestUnmarshal_InterfaceFieldPreservesConcreteType
//
// When an interface field already holds a concrete typed value, encoding/json
// unmarshals into that type. Verify velox matches.

func TestUnmarshal_InterfaceFieldPreservesConcreteType(t *testing.T) {
	t.Skip("velox decoder does not yet preserve pre-populated concrete types in any fields")

	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		Val any `json:"val"`
	}

	input := `{"val":{"x":42}}`

	// Pre-populate Val with *Inner so the decoder targets that type.
	got := Outer{Val: &Inner{}}
	want := Outer{Val: &Inner{}}

	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v (val type: %T)\n  want: %+v (val type: %T)",
			got, got.Val, want, want.Val)
	}
}

// TestUnmarshal_MapStringAnyWithEscapedKeys
//
// Ensure escaped string keys in map[string]any are decoded correctly.

func TestUnmarshal_MapStringAnyWithEscapedKeys(t *testing.T) {
	input := `{"key\nA":"val1","key\tB":"val2","key\"C":"val3","normal":"val4"}`

	var got, want map[string]any
	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

// TestUnmarshal_SliceOfAny
//
// Unmarshal a JSON array into []any and compare with encoding/json.

func TestUnmarshal_SliceOfAny(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", `[]`},
		{"primitives", `[1, "hello", true, false, null, 3.14]`},
		{"nested_objects", `[{"a":1},{"b":"two"},{"c":null}]`},
		{"nested_arrays", `[[1,2],[3,4],["a","b"]]`},
		{"mixed_deep", `[1,{"k":[true,null,{"x":"y"}]},"end"]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want []any
			if err := vjson.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %#v\n  want: %#v", got, want)
			}
		})
	}
}

// TestUnmarshal_NonEmptyInterface
//
// Struct fields with non-empty interface types. encoding/json cannot unmarshal
// into non-empty interfaces without a pre-populated concrete value, so a nil
// field stays nil and a pre-populated one gets filled.

func TestUnmarshal_NonEmptyInterface(t *testing.T) {
	type S struct {
		Label string       `json:"label"`
		Name  Animal       `json:"name"`
		Extra Animal       `json:"extra,omitempty"`
	}

	// Non-empty interface fields that are nil stay nil after unmarshal
	// (encoding/json can't create concrete types from interface alone).
	input := `{"label":"test","name":null,"extra":null}`

	var got, want S
	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

// TestUnmarshal_NonEmptyInterfacePrePopulated
//
// When a non-empty interface field is pre-populated with a concrete pointer,
// encoding/json unmarshals into that type.

func TestUnmarshal_NonEmptyInterfacePrePopulated(t *testing.T) {
	t.Skip("velox decoder does not yet unmarshal into pre-populated non-empty interface fields")

	type S struct {
		Label string `json:"label"`
		Pet   Animal `json:"pet"`
	}

	input := `{"label":"test","pet":{"name":"Rex","breed":"Labrador"}}`

	got := S{Pet: &Dog{}}
	want := S{Pet: &Dog{}}

	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v (pet type: %T)\n  want: %+v (pet type: %T)",
			got, got.Pet, want, want.Pet)
	}
}

// TestUnmarshal_MultipleInterfaceTypes
//
// A struct with multiple different interface fields, mixing any with
// non-empty interfaces.

func TestUnmarshal_MultipleInterfaceTypes(t *testing.T) {
	t.Skip("velox decoder does not yet unmarshal into pre-populated non-empty interface fields")

	type S struct {
		Label string `json:"label"`
		Pet   Animal `json:"pet"`
		Wild  any    `json:"wild"`
	}

	input := `{"label":"mixed","pet":{"name":"Rex","breed":"Labrador"},"wild":{"color":"orange"}}`

	got := S{Pet: &Dog{}}
	want := S{Pet: &Dog{}}

	if err := vjson.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

// TestUnmarshal_InterfaceOverwrite
//
// Verify that unmarshaling into an any field that already contains a value
// replaces it correctly.

func TestUnmarshal_InterfaceOverwrite(t *testing.T) {
	type S struct {
		Val any `json:"val"`
	}

	cases := []struct {
		name    string
		initial any
		input   string
	}{
		{"string_to_number", "old", `{"val":42}`},
		{"number_to_string", float64(99), `{"val":"new"}`},
		{"object_to_null", map[string]any{"k": "v"}, `{"val":null}`},
		{"null_to_array", nil, `{"val":[1,2,3]}`},
		{"array_to_object", []any{1, 2}, `{"val":{"k":"v"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := S{Val: tc.initial}
			want := S{Val: tc.initial}

			if err := vjson.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %+v (type: %T)\n  want: %+v (type: %T)",
					got.Val, got.Val, want.Val, want.Val)
			}
		})
	}
}
