package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// A map value that can receive heap pointers cannot be staged in the noscan KV
// buffer, so the buffer holds only a pointer to scannable intermediate storage.
// That indirection is a contract between the binder and the drain, recorded as
// MapDrainInfo.ValIsDeferred: when it is set, drainKVSlots reads the KV slot as a
// pointer. Both binder paths must honor it, or the drain dereferences whatever the
// binder happened to write inline, and tape words get read as an address.
//
// KindValue triggers it directly; so does any aggregate whose field tree merely
// reaches one, which is the case worth pinning because nothing about such a struct
// looks deferred at the map's own dispatch site.

// mapIndirectKinds is the matrix below, expressed once.
//
// The indirection is decided per map value type by two predicates that must
// agree: the drain's MapDrainInfo.ValIsDeferred (does the KV slot hold a
// pointer?) and the binder's BIND_FLAG_CONTAINS_DEFERRED (should I redirect
// this value to scannable storage?). Neither is derivable from the other at
// runtime, so the only way to hold them together is to drive every value shape
// that reaches a deferred type and require both binder paths to agree with each
// other and with the input.
//
// A shape whose predicates disagree does not error. The binder writes the value
// inline, the drain dereferences it anyway, and the map publishes whatever those
// bytes happen to be: a Value aliasing tape words as a pointer, or a slice
// header whose length is a tape word. So every case asserts published content,
// and asserts a container's length before indexing it, since a corrupt length is one
// of the observed symptoms, and traversing it would take down the whole package
// instead of failing this test.

// mapIndirectElem is a struct that reaches a Value only through a field, so
// nothing at the map's own dispatch site marks it as needing the indirection.
type mapIndirectElem struct {
	V value.Value `json:"v"`
}

// mapIndirectCase is one map value shape. bind fills the destination from src
// and returns a normalized description of what got published; the two paths must
// produce the same description, and it must equal want.
type mapIndirectCase struct {
	name string
	src  string
	want string
	// typ is the map type under test, so the contract check can read both
	// predicates without binding anything.
	typ reflect.Type
	// tapeUnsupported marks a shape UnmarshalValue rejects up front by design
	// (deferred kinds it cannot reconstruct). Its JSON leg still runs.
	tapeUnsupported bool
	bind            func(t *testing.T, src []byte, viaTape bool) (string, error)
}

// describeValue renders a Value as a short, comparable string. It never indexes
// past a length it has not checked.
func describeValue(v value.Value) string {
	if !v.Valid() {
		return "invalid"
	}
	if v.Type() != value.KindObject {
		return "kind=" + v.Type().String()
	}
	a := v.Get("a")
	n, ok := a.Int()
	if !ok {
		return fmt.Sprintf("len=%d a=missing", v.Len())
	}
	return fmt.Sprintf("len=%d a=%d", v.Len(), n)
}

func mapIndirectCases() []mapIndirectCase {
	// unmarshalInto runs whichever path the leg asks for.
	into := func(t *testing.T, src []byte, viaTape bool, dst any) error {
		t.Helper()
		if !viaTape {
			return Unmarshal(src, dst)
		}
		val, err := dom.Parse(src)
		if err != nil {
			t.Fatalf("dom.Parse(%s): %v", src, err)
		}
		return UnmarshalValue(val, dst)
	}
	return []mapIndirectCase{
		{
			// Baseline: no deferred type anywhere, so no indirection. Present so a
			// fix that over-applies the redirect shows up as a failure here.
			name: "int",
			typ:  reflect.TypeOf(map[string]int{}),
			src:  `{"k":7}`,
			want: "7",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]int
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				return fmt.Sprint(m["k"]), nil
			},
		},
		{
			name: "Value",
			typ:  reflect.TypeOf(map[string]value.Value{}),
			src:  `{"k":{"a":7}}`,
			want: "len=1 a=7",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]value.Value
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				return describeValue(m["k"]), nil
			},
		},
		{
			name: "struct reaching Value",
			typ:  reflect.TypeOf(map[string]mapIndirectElem{}),
			src:  `{"k":{"v":{"a":7}}}`,
			want: "len=1 a=7",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]mapIndirectElem
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				e := m["k"]
				return describeValue(e.V), nil
			},
		},
		{
			// ARRAY is marked like STRUCT, so a one-element array is the control
			// that says the aggregate marking works at all. Arrays of two or more
			// are a separate defect, pinned by TestMapValueArrayElement below.
			name: "array of Value",
			typ:  reflect.TypeOf(map[string][1]value.Value{}),
			src:  `{"k":[{"a":7}]}`,
			want: "[len=1 a=7]",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string][1]value.Value
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				arr := m["k"]
				return fmt.Sprintf("[%s]", describeValue(arr[0])), nil
			},
		},
		{
			// SLICE value. Its KV slot is a 24-byte header, not a pointer, so the
			// drain dereferencing it yields a header built from unrelated bytes;
			// the observed symptom is a length far larger than the input's.
			name: "slice of Value",
			typ:  reflect.TypeOf(map[string][]value.Value{}),
			src:  `{"k":[{"a":7}]}`,
			want: "len=1 [len=1 a=7]",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string][]value.Value
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				s := m["k"]
				if len(s) != 1 {
					// Do not index: the length itself is the corruption.
					return fmt.Sprintf("len=%d", len(s)), nil
				}
				return fmt.Sprintf("len=1 [%s]", describeValue(s[0])), nil
			},
		},
		{
			name: "slice of struct reaching Value",
			typ:  reflect.TypeOf(map[string][]mapIndirectElem{}),
			src:  `{"k":[{"v":{"a":7}}]}`,
			want: "len=1 [len=1 a=7]",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string][]mapIndirectElem
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				s := m["k"]
				if len(s) != 1 {
					return fmt.Sprintf("len=%d", len(s)), nil
				}
				return fmt.Sprintf("len=1 [%s]", describeValue(s[0].V)), nil
			},
		},
		{
			// MAP value. Its KV slot is an *hmap, which the drain reads as a
			// pointer to storage holding one; the observed symptom is an empty
			// inner map.
			name: "map of Value",
			typ:  reflect.TypeOf(map[string]map[string]value.Value{}),
			src:  `{"k":{"inner":{"a":7}}}`,
			want: "len=1 [len=1 a=7]",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]map[string]value.Value
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				inner := m["k"]
				if len(inner) != 1 {
					return fmt.Sprintf("len=%d", len(inner)), nil
				}
				return fmt.Sprintf("len=1 [%s]", describeValue(inner["inner"])), nil
			},
		},
		{
			// Nested map of a non-deferred value: the outer map needs no
			// indirection either. Pins that the bug is about reaching a deferred
			// type, not about nesting.
			name: "map of int",
			typ:  reflect.TypeOf(map[string]map[string]int{}),
			src:  `{"k":{"inner":7}}`,
			want: "len=1 [7]",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]map[string]int
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				inner := m["k"]
				if len(inner) != 1 {
					return fmt.Sprintf("len=%d", len(inner)), nil
				}
				return fmt.Sprintf("len=1 [%d]", inner["inner"]), nil
			},
		},
		{
			// PTR is deliberately exempt: its inline value is already a pointer to
			// scannable pointee storage. Present so a fix cannot "solve" the matrix
			// by redirecting everything.
			name: "pointer to Value",
			typ:  reflect.TypeOf(map[string]*value.Value{}),
			src:  `{"k":{"a":7}}`,
			want: "len=1 a=7",
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]*value.Value
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				p := m["k"]
				if p == nil {
					return "nil", nil
				}
				return describeValue(*p), nil
			},
		},
		{
			name:            "RawMessage",
			typ:             reflect.TypeOf(map[string]json.RawMessage{}),
			src:             `{"k":{"a":7}}`,
			want:            `{"a":7}`,
			tapeUnsupported: true,
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string]json.RawMessage
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				return string(m["k"]), nil
			},
		},
		{
			name:            "slice of RawMessage",
			typ:             reflect.TypeOf(map[string][]json.RawMessage{}),
			src:             `{"k":[{"a":7}]}`,
			want:            `len=1 [{"a":7}]`,
			tapeUnsupported: true,
			bind: func(t *testing.T, src []byte, viaTape bool) (string, error) {
				var m map[string][]json.RawMessage
				if err := into(t, src, viaTape, &m); err != nil {
					return "", err
				}
				s := m["k"]
				if len(s) != 1 {
					return fmt.Sprintf("len=%d", len(s)), nil
				}
				return fmt.Sprintf("len=1 [%s]", s[0]), nil
			},
		},
	}
}

// TestMapValueIndirection_Matrix is the shape × path matrix. Both paths must
// publish exactly what the input said, for every value shape that reaches a
// deferred type and for the non-deferred and exempt shapes that must keep
// working unchanged.
func TestMapValueIndirection_Matrix(t *testing.T) {
	for _, c := range mapIndirectCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				got, err := c.bind(t, []byte(c.src), false)
				if err != nil {
					t.Fatalf("Unmarshal(%s): %v", c.src, err)
				}
				if got != c.want {
					t.Errorf("Unmarshal(%s) published %s, want %s", c.src, got, c.want)
				}
			})
			t.Run("tape", func(t *testing.T) {
				got, err := c.bind(t, []byte(c.src), true)
				if c.tapeUnsupported {
					// Rejected up front by design. Requiring the rejection (rather
					// than skipping) keeps this honest: if tape-bind ever learns the
					// shape, this fails and the case moves into the matrix proper.
					var unsupported *TapeBindUnsupportedError
					if !errors.As(err, &unsupported) {
						t.Fatalf("UnmarshalValue(%s) err = %v, want TapeBindUnsupportedError", c.src, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("UnmarshalValue(%s): %v", c.src, err)
				}
				if got != c.want {
					t.Errorf("UnmarshalValue(%s) published %s, want %s", c.src, got, c.want)
				}
			})
		})
	}
}

// TestMapValueIndirection_MatrixContractAgrees checks the two predicates against
// each other directly, without binding anything.
//
// The matrix above catches a disagreement only where a shape is exercised and
// the corruption happens to be observable. This reads both sides for every map
// type in each shape's tree and requires them to match, so a future value shape
// that reaches a deferred type is caught by construction rather than by someone
// remembering to add it above.
func TestMapValueIndirection_MatrixContractAgrees(t *testing.T) {
	for _, c := range mapIndirectCases() {
		t.Run(c.name, func(t *testing.T) {
			p, err := NewParserForType(c.typ)
			if err != nil {
				t.Fatalf("NewParserForType(%s): %v", c.typ, err)
			}
			base := &p.tt.Types[0]
			for i := range p.tt.Types {
				if p.tt.Types[i].Kind != vbind.KindMap {
					continue
				}
				di := (*vbind.MapDrainInfo)(p.tt.TypeMeta[i].MapMeta().DrainInfo)
				valIdx := p.tt.Types[i].ChildIndex(base)
				valType := &p.tt.Types[valIdx]
				// The drain reads the slot as a pointer exactly when ValIsDeferred.
				// The binder redirects exactly when it sees KindValue, a deferred
				// kind, or CONTAINS_DEFERRED. PTR is exempt on both sides: its
				// inline value already points at scannable pointee storage.
				binderRedirects := valType.Kind == vbind.KindValue ||
					valType.Kind == vbind.KindUnmarshaler ||
					valType.Kind == vbind.KindTextUnmarshaler ||
					valType.Kind == vbind.KindRawMessage ||
					valType.HasContainsDeferred()
				if valType.Kind == vbind.KindPointer {
					binderRedirects = false
				}
				if di.ValIsDeferred != binderRedirects {
					verb := "does not redirect"
					if binderRedirects {
						verb = "redirects"
					}
					t.Errorf("map value kind %d: drain ValIsDeferred=%v but the binder %s; the two must agree or the drain reads bytes the binder never wrote as a pointer",
						int(valType.Kind), di.ValIsDeferred, verb)
				}
			}
		})
	}
}

// TestMapValueArrayElement pins the array-element case that the size boundary
// made unreachable: a [2]Value map value is 160 bytes, so Go stores it behind a
// pointer, and until the drain stopped assigning such elements through
// mapassign_faststr its published Values aliased unrelated memory.
//
// The single-element form worked all along ([1]Value is 80 bytes, inline), which
// is what made this look like an array-walk defect rather than a size one.
func TestMapValueArrayElement(t *testing.T) {
	const src = `{"k":[{"a":7},{"a":8}]}`
	var m map[string][2]value.Value
	if err := Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	arr := m["k"]
	if got, want := describeValue(arr[0]), "len=1 a=7"; got != want {
		t.Errorf("arr[0] = %s, want %s", got, want)
	}
	if got, want := describeValue(arr[1]), "len=1 a=8"; got != want {
		t.Errorf("arr[1] = %s, want %s", got, want)
	}
}
