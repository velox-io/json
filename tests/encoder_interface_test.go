package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// helper: encode with vjson.Encoder.Encode(any) and return trimmed output.
func vjsonEncode(v any) (string, error) {
	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// helper: encode with encoding/json.Encoder and return trimmed output.
func stdEncode(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// TestEncoder_StructFieldAndInterfaceRefSameType tests Encoder.Encode with
// a struct that has both concrete and any fields holding the same type.

func TestEncoder_StructFieldAndInterfaceRefSameType(t *testing.T) {
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
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestEncoder_StructWithMapStringAny tests Encoder.Encode with map[string]any.

func TestEncoder_StructWithMapStringAny(t *testing.T) {
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
			},
		}},
		{"nested_map", WithMap{
			Label: "nested",
			Meta: map[string]any{
				"inner": map[string]any{"x": float64(1)},
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)

			// Map key order is non-deterministic; compare via round-trip.
			var gotVal, wantVal any
			json.Unmarshal([]byte(got), &gotVal)
			json.Unmarshal([]byte(want), &wantVal)
			if !reflect.DeepEqual(gotVal, wantVal) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestEncoder_NonEmptyInterface tests Encoder.Encode with non-empty interface fields.

func TestEncoder_NonEmptyInterface(t *testing.T) {
	type S struct {
		Label string       `json:"label"`
		Name  fmt.Stringer `json:"name"`
		Extra fmt.Stringer `json:"extra,omitempty"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Label: "test", Name: nil}},
		{"value", S{Label: "test", Name: testStringer{"alice"}}},
		{"pointer", S{Label: "test", Name: &testStringer{"bob"}}},
		{"omitempty_nil", S{Label: "test", Name: testStringer{"x"}, Extra: nil}},
		{"omitempty_present", S{Label: "test", Name: testStringer{"x"}, Extra: testStringer{"y"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestEncoder_PlainNonEmptyInterface tests Encoder.Encode with plain non-empty
// interface types (no Stringer, no marshaler).

func TestEncoder_PlainNonEmptyInterface(t *testing.T) {
	type S struct {
		Label string `json:"label"`
		Pet   Animal `json:"pet"`
		Extra Animal `json:"extra,omitempty"`
	}

	dog := Dog{Name: "Rex", Breed: "Labrador"}
	cat := Cat{Name: "Whiskers", Color: "orange", Lives: 9}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Label: "test", Pet: nil}},
		{"dog_value", S{Label: "test", Pet: dog}},
		{"dog_pointer", S{Label: "test", Pet: &dog}},
		{"cat_value", S{Label: "test", Pet: cat}},
		{"cat_pointer", S{Label: "test", Pet: &cat}},
		{"omitempty_nil", S{Label: "test", Pet: dog, Extra: nil}},
		{"omitempty_present", S{Label: "test", Pet: dog, Extra: cat}},
		{"both_pointers", S{Label: "test", Pet: &dog, Extra: &cat}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestEncoder_CustomInterfaceWithMarshalJSON tests Encoder.Encode with values
// stored in non-empty interfaces that implement json.Marshaler.

func TestEncoder_CustomInterfaceWithMarshalJSON(t *testing.T) {
	var foo MyIface = barWithCustomMarshal{Name: "hello", Score: 100}

	got, err := vjsonEncode(foo)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := stdEncode(foo)
	if got != want {
		t.Errorf("mismatch:\n  vjson: %s\n  std:   %s", got, want)
	}

	// Pointer-to-value variant.
	bar := barWithCustomMarshal{Name: "world", Score: 200}
	var foo2 MyIface = &bar

	got2, err := vjsonEncode(foo2)
	if err != nil {
		t.Fatal(err)
	}
	want2, _ := stdEncode(foo2)
	if got2 != want2 {
		t.Errorf("pointer mismatch:\n  vjson: %s\n  std:   %s", got2, want2)
	}
}

// TestEncoder_CustomInterfaceWithoutMarshalJSON tests Encoder.Encode with a
// non-empty interface value that does NOT implement json.Marshaler.

func TestEncoder_CustomInterfaceWithoutMarshalJSON(t *testing.T) {
	var foo MyIface = barPlain{Name: "hello", Score: 100}

	got, err := vjsonEncode(foo)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := stdEncode(foo)
	if got != want {
		t.Errorf("mismatch:\n  vjson: %s\n  std:   %s", got, want)
	}
}

// TestEncoder_CustomInterfaceInStructField tests Encoder.Encode when non-empty
// interface fields are embedded in a struct.

func TestEncoder_CustomInterfaceInStructField(t *testing.T) {
	type Wrapper struct {
		Value MyIface `json:"value"`
	}

	cases := []struct {
		name string
		val  Wrapper
	}{
		{"with_marshal_json", Wrapper{Value: barWithCustomMarshal{Name: "a", Score: 1}}},
		{"without_marshal_json", Wrapper{Value: barPlain{Name: "b", Score: 2}}},
		{"nil_interface", Wrapper{Value: nil}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(&tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(&tc.val)
			if got != want {
				t.Errorf("mismatch:\n  vjson: %s\n  std:   %s", got, want)
			}
		})
	}
}

// TestEncoder_MultipleInterfaceTypes tests Encoder.Encode with multiple
// different interface field types in one struct.

func TestEncoder_MultipleInterfaceTypes(t *testing.T) {
	type S struct {
		Label   string       `json:"label"`
		Pet     Animal       `json:"pet"`
		Display fmt.Stringer `json:"display"`
		Wild    any          `json:"wild"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"all_nil", S{Label: "test"}},
		{"all_set", S{
			Label:   "mixed",
			Pet:     Dog{Name: "Rex", Breed: "Labrador"},
			Display: testStringer{"hello"},
			Wild:    Cat{Name: "Mimi", Color: "black", Lives: 7},
		}},
		{"all_pointers", S{
			Label:   "ptrs",
			Pet:     &Dog{Name: "Rex", Breed: "Labrador"},
			Display: &testStringer{"hello"},
			Wild:    &Cat{Name: "Mimi", Color: "black", Lives: 7},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  vjson: %s\n  std:   %s", got, want)
			}
		})
	}
}

// TestEncodeValue_StructFieldAndInterfaceRefSameType tests EncodeValue[T] with
// the same cases as the Encode(any) test above.

// ptrOnlyMarshaler implements json.Marshaler only on the pointer receiver.
// This means *ptrOnlyMarshaler has MarshalJSON but ptrOnlyMarshaler does not.
type ptrOnlyMarshaler struct {
	Name string
}

func (p *ptrOnlyMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{"ptr_only":"` + p.Name + `"}`), nil
}

// TestEncoder_PointerReceiverMarshalJSON verifies that when *T implements
// json.Marshaler (but T does not), passing *T to Encoder.Encode correctly
// invokes the custom MarshalJSON method.
func TestEncoder_PointerReceiverMarshalJSON(t *testing.T) {
	v := &ptrOnlyMarshaler{Name: "test"}

	got, err := vjsonEncode(v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := stdEncode(v)
	if got != want {
		t.Errorf("Encode mismatch:\n  vjson: %s\n  std:   %s", got, want)
	}
}

// TestEncodeValue_PointerReceiverMarshalJSON verifies the same for EncodeValue[T].
func TestEncodeValue_PointerReceiverMarshalJSON(t *testing.T) {
	v := &ptrOnlyMarshaler{Name: "test"}

	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)
	if err := vjson.EncodeValue(enc, v); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	want, _ := stdEncode(v)
	if got != want {
		t.Errorf("EncodeValue mismatch:\n  vjson: %s\n  std:   %s", got, want)
	}
}

func TestEncodeValue_StructFieldAndInterfaceRefSameType(t *testing.T) {
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
			var buf bytes.Buffer
			enc := vjson.NewEncoder(&buf)
			if err := vjson.EncodeValue(enc, &tc.val); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSpace(buf.String())
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestEncodeValue_NilPointer tests EncodeValue[T] with a nil pointer.

func TestEncodeValue_NilPointer(t *testing.T) {
	type Foo struct {
		A int    `json:"a"`
		B string `json:"b"`
	}

	var p *Foo

	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)
	if err := vjson.EncodeValue(enc, p); err != nil {
		t.Fatal(err)
	}

	got := strings.TrimSpace(buf.String())
	if got != "null" {
		t.Errorf("got %q, want %q", got, "null")
	}
}

// TestEncodeValue_NonPointerValue tests EncodeValue[T] with value types.

func TestEncodeValue_NonPointerValue(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*vjson.Encoder) error
		want string
	}{
		{"int", func(enc *vjson.Encoder) error { return vjson.EncodeValue(enc, 42) }, "42"},
		{"string", func(enc *vjson.Encoder) error { return vjson.EncodeValue(enc, "hello") }, `"hello"`},
		{"bool", func(enc *vjson.Encoder) error { return vjson.EncodeValue(enc, true) }, "true"},
		{"float", func(enc *vjson.Encoder) error { return vjson.EncodeValue(enc, 3.14) }, "3.14"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			enc := vjson.NewEncoder(&buf)
			if err := tc.fn(enc); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSpace(buf.String())
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEncoder_NilValues tests Encoder.Encode with various nil values.

func TestEncoder_NilValues(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"nil_any", nil},
		{"nil_ptr", (*int)(nil)},
		{"nil_slice", ([]int)(nil)},
		{"nil_map", (map[string]int)(nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjsonEncode(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := stdEncode(tc.val)
			if got != want {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}
