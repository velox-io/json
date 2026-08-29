package bind

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/velox-io/json/vbind"
)

// Variant case types that contain an `any` field. The tape-bind sub-routine
// (cold rebind for sibling variants, inline variants, and kindof pointer
// cases) walks these case structs field-by-field; when it reaches the any
// field it must route through the any_value path (carve slot, box eface) the
// same way the main bind path does, rather than rejecting the tag with
// BIND_ERR_UNSUPPORTED_TAG.
//
// The control case is kindof with a NON-pointer object case: that dispatches
// inline (main bind path) which already supports any, so it must pass today.

// anyCaseMeta: direct any field inside a case struct.
type anyCaseMeta struct {
	Name string `json:"name"`
	Meta any    `json:"meta"`
}

// anyCaseTags: []any field inside a case struct. The tape-bind SLICE walk
// descends into elements; the element type is any, so t_array_value hits the
// BIND_IS_ANY gate.
type anyCaseTags struct {
	Name string `json:"name"`
	Tags []any  `json:"tags"`
}

// anyCaseAttrs: map[string]any field inside a case struct. The tape-bind MAP
// walk descends into values; the value type is any, so t_map_value hits the
// BIND_IS_ANY gate.
type anyCaseAttrs struct {
	Name  string         `json:"name"`
	Attrs map[string]any `json:"attrs"`
}

// anyCaseNested: a case struct that nests another struct carrying an any
// field. The tape-bind STRUCT walk descends into the nested struct; its any
// field then hits the gate at t_object_field_value.
type anyCaseNested struct {
	Label string      `json:"label"`
	Inner anyCaseMeta `json:"inner"`
}

// --- sibling variant envelope ---

type anyEnvelopeSibling struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[anyEnvelopeSibling, struct {
		_ anyCaseMeta   `case:"meta"`
		_ anyCaseTags   `case:"tags"`
		_ anyCaseAttrs  `case:"attrs"`
		_ anyCaseNested `case:"nested"`
	}]()
}

// --- inline variant envelope ---

type anyEnvelopeInline struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[anyEnvelopeInline, struct {
		_ anyCaseMeta `case:"meta"`
		_ anyCaseTags `case:"tags"`
	}]()
}

// --- kindof envelopes ---
//
// Pointer object case (cold-kind) falls back to tape + tape-bind rebind, so
// the any field inside the pointee hits the gate. Non-pointer object case
// dispatches inline (main bind path) and supports any today; it is the
// control that must pass.

type anyEnvelopeKindofPointer struct {
	Data any `json:"data" vjson:"kindof"`
}

type anyEnvelopeKindofObject struct {
	Data any `json:"data" vjson:"kindof"`
}

func init() {
	vbind.DefineKindofCases[anyEnvelopeKindofPointer, struct {
		object *anyCaseMeta
	}]()
	vbind.DefineKindofCases[anyEnvelopeKindofObject, struct {
		object anyCaseMeta
	}]()
}

// anyBoxedAs returns the value encoding/json produces for subJSON, used as the
// reference for how the bind any_value path should box the same JSON.
func anyBoxedAs(t *testing.T, subJSON string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(subJSON), &v); err != nil {
		t.Fatalf("stdlib unmarshal %s: %v", subJSON, err)
	}
	return v
}

// TestVariantSibling_AnyField exercises a sibling-variant case struct with a
// direct any field, across every JSON value kind and both discriminator/variant
// orderings. Both orderings buffer+rebind via the tape-bind sub-routine, so
// both exercise the any-inside-case gap.
func TestVariantSibling_AnyField(t *testing.T) {
	kinds := []struct {
		name     string
		metaJSON string
	}{
		{"string", `"hello"`},
		{"number", `42`},
		{"bool", `true`},
		{"null", `null`},
		{"array", `[1,2,3]`},
		{"object", `{"role":"admin","since":2021}`},
	}
	orders := []struct {
		name  string
		first bool // true = discriminator first
	}{
		{"discFirst", true},
		{"variantFirst", false},
	}
	for _, ord := range orders {
		for _, k := range kinds {
			name := fmt.Sprintf("%s/%s", ord.name, k.name)
			t.Run(name, func(t *testing.T) {
				var src string
				if ord.first {
					src = `{"type":"meta","data":{"name":"Alice","meta":` + k.metaJSON + `}}`
				} else {
					src = `{"data":{"name":"Alice","meta":` + k.metaJSON + `},"type":"meta"}`
				}
				var env anyEnvelopeSibling
				if err := Unmarshal([]byte(src), &env); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				got, ok := env.Data.(anyCaseMeta)
				if !ok {
					t.Fatalf("Data = %T, want anyCaseMeta", env.Data)
				}
				if got.Name != "Alice" {
					t.Errorf("Name = %q, want Alice", got.Name)
				}
				want := anyBoxedAs(t, k.metaJSON)
				if !anyEqual(got.Meta, want) {
					t.Errorf("Meta = %#v, want %#v", got.Meta, want)
				}
			})
		}
	}
}

// TestVariantSibling_AnySliceField: []any field inside a case struct. A typed
// []any field is a recursive slice and uses BIND_SLOT_RECBATCH for its backing;
// t_array_value grows it inline via recbatch_alloc/recbatch_free (mirroring the
// main bind path), so the matrix is not corrupted across parses.
func TestVariantSibling_AnySliceField(t *testing.T) {
	tagsJSON := `[1,"two",true,null,{"k":"v"}]`
	for _, ord := range []struct {
		name  string
		first bool
	}{{"discFirst", true}, {"variantFirst", false}} {
		t.Run(ord.name, func(t *testing.T) {
			var src string
			if ord.first {
				src = `{"type":"tags","data":{"name":"Alice","tags":` + tagsJSON + `}}`
			} else {
				src = `{"data":{"name":"Alice","tags":` + tagsJSON + `},"type":"tags"}`
			}
			var env anyEnvelopeSibling
			if err := Unmarshal([]byte(src), &env); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got, ok := env.Data.(anyCaseTags)
			if !ok {
				t.Fatalf("Data = %T, want anyCaseTags", env.Data)
			}
			if got.Name != "Alice" {
				t.Errorf("Name = %q, want Alice", got.Name)
			}
			want := anyBoxedAs(t, tagsJSON).([]any)
			if !anyEqual(got.Tags, want) {
				t.Errorf("Tags = %#v, want %#v", got.Tags, want)
			}
		})
	}
}

// TestVariantSibling_AnyMapField: map[string]any field inside a case struct.
func TestVariantSibling_AnyMapField(t *testing.T) {
	attrsJSON := `{"a":1,"b":"x","c":[true,false],"d":{"n":7}}`
	for _, ord := range []struct {
		name  string
		first bool
	}{{"discFirst", true}, {"variantFirst", false}} {
		t.Run(ord.name, func(t *testing.T) {
			var src string
			if ord.first {
				src = `{"type":"attrs","data":{"name":"Alice","attrs":` + attrsJSON + `}}`
			} else {
				src = `{"data":{"name":"Alice","attrs":` + attrsJSON + `},"type":"attrs"}`
			}
			var env anyEnvelopeSibling
			if err := Unmarshal([]byte(src), &env); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got, ok := env.Data.(anyCaseAttrs)
			if !ok {
				t.Fatalf("Data = %T, want anyCaseAttrs", env.Data)
			}
			if got.Name != "Alice" {
				t.Errorf("Name = %q, want Alice", got.Name)
			}
			want := anyBoxedAs(t, attrsJSON).(map[string]any)
			if !anyEqual(got.Attrs, want) {
				t.Errorf("Attrs = %#v, want %#v", got.Attrs, want)
			}
		})
	}
}

// TestVariantSibling_AnyNestedField: case struct nesting another struct that
// carries an any field. The tape-bind STRUCT walk must descend into the
// nested struct and bind its any field.
func TestVariantSibling_AnyNestedField(t *testing.T) {
	metaJSON := `{"role":"admin","since":2021}`
	for _, ord := range []struct {
		name  string
		first bool
	}{{"discFirst", true}, {"variantFirst", false}} {
		t.Run(ord.name, func(t *testing.T) {
			var src string
			if ord.first {
				src = `{"type":"nested","data":{"label":"L","inner":{"name":"Alice","meta":` + metaJSON + `}}}`
			} else {
				src = `{"data":{"label":"L","inner":{"name":"Alice","meta":` + metaJSON + `}},"type":"nested"}`
			}
			var env anyEnvelopeSibling
			if err := Unmarshal([]byte(src), &env); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got, ok := env.Data.(anyCaseNested)
			if !ok {
				t.Fatalf("Data = %T, want anyCaseNested", env.Data)
			}
			if got.Label != "L" || got.Inner.Name != "Alice" {
				t.Errorf("Label/Name = %q/%q, want L/Alice", got.Label, got.Inner.Name)
			}
			want := anyBoxedAs(t, metaJSON)
			if !anyEqual(got.Inner.Meta, want) {
				t.Errorf("Inner.Meta = %#v, want %#v", got.Inner.Meta, want)
			}
		})
	}
}

// TestInlineVariant_AnyField: inline-variant case struct with a direct any
// field. Inline pass-2 re-enters tape-bind as the case type, so the any field
// hits the same gate.
func TestInlineVariant_AnyField(t *testing.T) {
	metaJSON := `{"role":"admin","since":2021}`
	src := `{"type":"meta","name":"Alice","meta":` + metaJSON + `}`
	var env anyEnvelopeInline
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := env.Data.(anyCaseMeta)
	if !ok {
		t.Fatalf("Data = %T, want anyCaseMeta", env.Data)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", got.Name)
	}
	want := anyBoxedAs(t, metaJSON)
	if !anyEqual(got.Meta, want) {
		t.Errorf("Meta = %#v, want %#v", got.Meta, want)
	}
}

// TestKindofPointer_AnyField: kindof with a pointer object case (cold-kind)
// falls back to tape + tape-bind rebind. The any field inside the pointee
// hits the gate.
func TestKindofPointer_AnyField(t *testing.T) {
	metaJSON := `{"role":"admin","since":2021}`
	src := `{"data":{"name":"Alice","meta":` + metaJSON + `}}`
	var env anyEnvelopeKindofPointer
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := env.Data.(*anyCaseMeta)
	if !ok {
		t.Fatalf("Data = %T, want *anyCaseMeta", env.Data)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", got.Name)
	}
	want := anyBoxedAs(t, metaJSON)
	if !anyEqual(got.Meta, want) {
		t.Errorf("Meta = %#v, want %#v", got.Meta, want)
	}
}

// TestKindofObject_AnyField is the CONTROL: kindof with a non-pointer object
// case dispatches inline (main bind path), which already supports any. This
// must pass today, confirming the gap is specific to the tape-bind sub-routine.
func TestKindofObject_AnyField(t *testing.T) {
	metaJSON := `{"role":"admin","since":2021}`
	src := `{"data":{"name":"Alice","meta":` + metaJSON + `}}`
	var env anyEnvelopeKindofObject
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := env.Data.(anyCaseMeta)
	if !ok {
		t.Fatalf("Data = %T, want anyCaseMeta", env.Data)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", got.Name)
	}
	want := anyBoxedAs(t, metaJSON)
	if !anyEqual(got.Meta, want) {
		t.Errorf("Meta = %#v, want %#v", got.Meta, want)
	}
}
