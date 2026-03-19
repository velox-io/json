package vjson

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// TestMarshal_StructFieldAndInterfaceRefSameType
//
// Struct A has a concrete field of type B and an interface field (any) that
// dynamically holds a B instance. Both should marshal identically to
// encoding/json.
// ---------------------------------------------------------------------------

func TestMarshal_StructFieldAndInterfaceRefSameType(t *testing.T) {
	type B struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	type A struct {
		Direct B   `json:"direct"`
		Iface  any `json:"iface"`
	}

	b := B{Name: "alice", Age: 30}

	cases := []struct {
		name string
		val  A
	}{
		{"value", A{Direct: b, Iface: b}},
		{"pointer", A{Direct: b, Iface: &b}},
		{"nil_iface", A{Direct: b, Iface: nil}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Marshal(&tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMarshal_StructWithMapStringAny
//
// Struct with a map[string]any field containing various dynamic value types.
// ---------------------------------------------------------------------------

func TestMarshal_StructWithMapStringAny(t *testing.T) {
	type B struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	type WithMap struct {
		Label string         `json:"label"`
		Meta  map[string]any `json:"meta"`
	}

	cases := []struct {
		name string
		val  WithMap
	}{
		{"nil_map", WithMap{Label: "test", Meta: nil}},
		{"empty_map", WithMap{Label: "test", Meta: map[string]any{}}},
		{"primitives", WithMap{
			Label: "prim",
			Meta: map[string]any{
				"str":  "hello",
				"num":  float64(42),
				"bool": true,
				"null": nil,
				"neg":  float64(-3.14),
				"zero": float64(0),
			},
		}},
		{"nested_map", WithMap{
			Label: "nested",
			Meta: map[string]any{
				"inner": map[string]any{
					"x": float64(1),
					"y": float64(2),
				},
			},
		}},
		{"slice_value", WithMap{
			Label: "slice",
			Meta: map[string]any{
				"items": []any{float64(1), "two", nil, true},
			},
		}},
		{"struct_value", WithMap{
			Label: "struct",
			Meta: map[string]any{
				"person": B{Name: "bob", Age: 25},
			},
		}},
		{"struct_pointer_value", WithMap{
			Label: "ptr",
			Meta: map[string]any{
				"person": &B{Name: "carol", Age: 35},
			},
		}},
		{"mixed", WithMap{
			Label: "mixed",
			Meta: map[string]any{
				"name":    "test",
				"count":   float64(99),
				"enabled": false,
				"tags":    []any{"a", "b"},
				"nested":  map[string]any{"k": "v"},
				"person":  B{Name: "dave", Age: 40},
				"nothing": nil,
			},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Marshal(&tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)

			// Use JSON round-trip comparison since map key order is non-deterministic.
			var gotVal, wantVal any
			if err := json.Unmarshal(got, &gotVal); err != nil {
				t.Fatalf("unmarshal got: %v\nJSON: %s", err, got)
			}
			if err := json.Unmarshal(want, &wantVal); err != nil {
				t.Fatalf("unmarshal want: %v\nJSON: %s", err, want)
			}
			if !reflect.DeepEqual(gotVal, wantVal) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}
