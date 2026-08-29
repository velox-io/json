package bind

import (
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
)

// roundTrip checks that dom.Parse(src) followed by UnmarshalValue produces the
// same result as a direct Unmarshal(src). The direct Unmarshal is the reference
// semantics; UnmarshalValue must match it for every kind the tape-bind
// sub-routine walks.
func roundTrip[T any](t *testing.T, src string) {
	t.Helper()
	var ref T
	if err := Unmarshal([]byte(src), &ref); err != nil {
		t.Fatalf("Unmarshal ref: %v", err)
	}

	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}

	var got T
	if err := UnmarshalValue(val, &got); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if !reflect.DeepEqual(got, ref) {
		t.Errorf("UnmarshalValue mismatch\ngot:  %+v\nwant: %+v", got, ref)
	}
}

func TestUnmarshalValueScalar(t *testing.T) {
	roundTrip[int](t, "42")
	roundTrip[string](t, `"hello"`)
	roundTrip[bool](t, "true")
	roundTrip[float64](t, "3.14")
}

func TestUnmarshalValueStruct(t *testing.T) {
	roundTrip[variantUser](t, `{"name":"Alice","role":"admin"}`)
}

func TestUnmarshalValueSlice(t *testing.T) {
	roundTrip[[]int](t, `[1,2,3,4,5]`)
	roundTrip[[]string](t, `["a","b","c"]`)
	roundTrip[[]variantUser](t, `[{"name":"Alice","role":"admin"},{"name":"Bob","role":"user"}]`)
}

func TestUnmarshalValueArray(t *testing.T) {
	roundTrip[[3]int](t, `[1,2,3]`)
}

func TestUnmarshalValueMap(t *testing.T) {
	roundTrip[map[string]int](t, `{"a":1,"b":2,"c":3}`)
	roundTrip[map[string]string](t, `{"x":"foo","y":"bar"}`)
}

func TestUnmarshalValueNested(t *testing.T) {
	type nested struct {
		Name  string         `json:"name"`
		Tags  []string       `json:"tags"`
		Meta  map[string]int `json:"meta"`
		Inner *variantUser   `json:"inner"`
	}
	roundTrip[nested](t, `{"name":"x","tags":["a","b"],"meta":{"k":1},"inner":{"name":"Alice","role":"admin"}}`)
}

func TestUnmarshalValueNullPointer(t *testing.T) {
	type withPtr struct {
		Inner *variantUser `json:"inner"`
	}
	roundTrip[withPtr](t, `{"inner":null}`)
}

func TestUnmarshalValueVariantSiblingDiscFirst(t *testing.T) {
	roundTrip[variantEnvelopeSibling](t, `{"type":"user","data":{"name":"Alice","role":"admin"}}`)
	roundTrip[variantEnvelopeSibling](t, `{"type":"product","data":{"title":"Widget","price":99}}`)
}

func TestUnmarshalValueVariantSiblingOutOfOrder(t *testing.T) {
	// Data before vdisc: triggers the out-of-order cold path (state==5 drain).
	roundTrip[variantEnvelopeSibling](t, `{"data":{"name":"Alice","role":"admin"},"type":"user"}`)
}

func TestUnmarshalValueVariantMethod(t *testing.T) {
	roundTrip[variantEnvelopeMethod](t, `{"type":"user","data":{"name":"Bob","role":"user"}}`)
}

func TestUnmarshalValueKindofScalar(t *testing.T) {
	roundTrip[kindofEnvelopeScalar](t, `{"data":true}`)
	roundTrip[kindofEnvelopeScalar](t, `{"data":42}`)
	roundTrip[kindofEnvelopeScalar](t, `{"data":"hello"}`)
}

func TestUnmarshalValueKindofMixed(t *testing.T) {
	roundTrip[kindofEnvelopeMixed](t, `{"data":{"name":"Alice","role":"admin"}}`)
	roundTrip[kindofEnvelopeMixed](t, `{"data":[{"name":"Alice","role":"admin"},{"name":"Bob","role":"user"}]}`)
	roundTrip[kindofEnvelopeMixed](t, `{"data":true}`)
	roundTrip[kindofEnvelopeMixed](t, `{"data":3.14}`)
}

// TestUnmarshalValueValueField verifies that a value.Value field on the target
// type is bound by aliasing the input tape's sub-tree (TAPE_BIND_VALUE yield).
// The nested Value must be navigable: its Tape/StrArena/Src alias the parent.
func TestUnmarshalValueValueField(t *testing.T) {
	type valueHost struct {
		Name  string      `json:"name"`
		Extra value.Value `json:"extra"`
	}
	src := `{"name":"x","extra":{"a":1,"b":[2,3]}}`

	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var got valueHost
	if err := UnmarshalValue(val, &got); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if got.Name != "x" {
		t.Errorf("Name = %q, want %q", got.Name, "x")
	}
	// The nested Value should view the {"a":1,"b":[2,3]} sub-object.
	if got.Extra.Type() != value.KindObject {
		t.Fatalf("Extra.Type = %v, want Object", got.Extra.Type())
	}
	a := got.Extra.Get("a")
	if !a.Valid() {
		t.Errorf("Extra.a missing")
	} else if ai, ok := a.Int(); !ok || ai != 1 {
		t.Errorf("Extra.a = %d (ok=%v), want 1", ai, ok)
	}
	b := got.Extra.Get("b")
	if !b.Valid() {
		t.Errorf("Extra.b missing")
	} else if b.Len() != 2 {
		t.Errorf("Extra.b len = %d, want 2", b.Len())
	} else {
		b0 := b.Index(0)
		if bi, ok := b0.Int(); !ok || bi != 2 {
			t.Errorf("Extra.b[0] = %d (ok=%v), want 2", bi, ok)
		}
	}
}

// TestUnmarshalValueParserReuse verifies the per-Parser entry works and can be
// reused across calls of the same root type.
func TestUnmarshalValueParserReuse(t *testing.T) {
	p, err := NewParser[variantUser]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for _, src := range []string{
		`{"name":"Alice","role":"admin"}`,
		`{"name":"Bob","role":"user"}`,
		`{"name":"Carol","role":"dev"}`,
	} {
		val, err := dom.Parse([]byte(src))
		if err != nil {
			t.Fatalf("dom.Parse(%s): %v", src, err)
		}
		var u variantUser
		if err := p.UnmarshalValue(val, &u); err != nil {
			t.Fatalf("UnmarshalValue(%s): %v", src, err)
		}
		var ref variantUser
		if err := Unmarshal([]byte(src), &ref); err != nil {
			t.Fatalf("Unmarshal ref(%s): %v", src, err)
		}
		if !reflect.DeepEqual(u, ref) {
			t.Errorf("mismatch for %s: got %+v, want %+v", src, u, ref)
		}
	}
}

// TestUnmarshalValueEmptyTape verifies the empty-input guard.
func TestUnmarshalValueEmptyTape(t *testing.T) {
	var v value.Value
	err := UnmarshalValue(v, new(int))
	if err == nil {
		t.Fatal("expected error for empty tape, got nil")
	}
}

// roundTripExpectErr asserts that both Unmarshal and UnmarshalValue reject src
// for type T, with the same outcome (both error). Used to pin coercion
// semantics that must match the JSON bind path (e.g. double tape -> int target).
func roundTripExpectErr[T any](t *testing.T, src string) {
	t.Helper()
	var ref T
	refErr := Unmarshal([]byte(src), &ref)
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse(%s): %v", src, err)
	}
	var got T
	gotErr := UnmarshalValue(val, &got)
	if refErr == nil {
		t.Errorf("src=%s: expected Unmarshal to error, got nil (ref=%+v)", src, ref)
	}
	if gotErr == nil {
		t.Errorf("src=%s: expected UnmarshalValue to error, got nil (got=%+v)", src, got)
	}
}

// TestUnmarshalValueSliceOfMap exercises t_array_value's MAP case (START_OBJECT
// consumption before descending into t_map_open).
func TestUnmarshalValueSliceOfMap(t *testing.T) {
	roundTrip[[]map[string]int](t, `[{"a":1,"b":2},{"c":3}]`)
	roundTrip[[]map[string]string](t, `[{"x":"p"},{"y":"q"}]`)
}

// TestUnmarshalValueMapOfStruct exercises t_map_value's STRUCT case
// (START_OBJECT consumption + empty-object check before bind_push_map).
func TestUnmarshalValueMapOfStruct(t *testing.T) {
	roundTrip[map[string]variantUser](t, `{"x":{"name":"Alice","role":"admin"},"y":{"name":"Bob","role":"user"}}`)
}

// TestUnmarshalValueMapOfMap exercises t_map_value's MAP case (nested map
// value). Previously hit t_unsupported; now mirrors t_array_value's MAP case.
func TestUnmarshalValueMapOfMap(t *testing.T) {
	roundTrip[map[string]map[string]int](t, `{"outer":{"a":1,"b":2},"inner":{"c":3}}`)
	roundTrip[map[string]map[string]string](t, `{"o":{"x":"p"},"i":{"y":"q"}}`)
}

// TestUnmarshalValueEmptyStructInContainer exercises the empty-object
// optimization in t_array_value/t_map_value STRUCT cases: {} skips
// bind_push/bind_pop and leaves the zeroed struct.
func TestUnmarshalValueEmptyStructInContainer(t *testing.T) {
	type opt struct {
		Name string `json:"name,omitempty"`
	}
	roundTrip[[]opt](t, `[{},{"name":"x"}]`)
	roundTrip[[]opt](t, `[{},{}]`)
	roundTrip[map[string]opt](t, `{"a":{},"b":{"name":"x"}}`)
}

// TestUnmarshalValueNumberCoercion pins the cross-format number semantics:
// int tape -> float target promotes (accepted); double tape -> integer target
// is a type mismatch (rejected, matching the JSON bind path and encoding/json).
func TestUnmarshalValueNumberCoercion(t *testing.T) {
	// int tape -> float target: accepted by both paths.
	roundTrip[float64](t, `42`)
	roundTrip[float32](t, `42`)
	roundTrip[float64](t, `-7`)
	// double tape -> float target: accepted by both paths.
	roundTrip[float64](t, `3.14`)
	roundTrip[float32](t, `3.0`)
	// int tape -> int target: accepted by both paths.
	roundTrip[int](t, `42`)
	roundTrip[int64](t, `-7`)
	// double tape -> integer target: rejected by both paths (type mismatch).
	roundTripExpectErr[int](t, `3.0`)
	roundTripExpectErr[int](t, `3.14`)
	roundTripExpectErr[int](t, `3e2`)
	roundTripExpectErr[int64](t, `3.99`)
	roundTripExpectErr[uint](t, `-2.5`)
	roundTripExpectErr[int](t, `1e10`)
}

// TestUnmarshalValueRootMismatch covers root-level shape mismatches: the JSON
// value kind does not match the root target kind. All three legs (encoding/json,
// JSON bind, tape-bind) must report an error, not crash. Previously the tape-bind
// cold-start path skipped to t_object_continue (an enclosing-struct context)
// and corrupted the stack; TAPE_BIND_ROOT_TYPE_MISMATCH_SKIP now routes to
// t_document_end.
func TestUnmarshalValueRootMismatch(t *testing.T) {
	// JSON array into non-slice/array roots.
	roundTripExpectErr[map[string]string](t, `["a","b"]`)
	roundTripExpectErr[map[string]int](t, `[1,2]`)
	roundTripExpectErr[variantUser](t, `[{"name":"x"}]`)
	// JSON object into non-struct/map roots.
	roundTripExpectErr[[]int](t, `{"a":1}`)
	roundTripExpectErr[[]string](t, `{"a":"b"}`)
	roundTripExpectErr[int](t, `{"a":1}`)
	// JSON array into array-of-different-elem is NOT a root mismatch (array
	// into array is shape-compatible; element mismatch is handled per-element).
	roundTripExpectErr[[3]int](t, `["a","b","c"]`)
}
