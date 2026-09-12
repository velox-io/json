//go:build go1.24

package tests

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
)

// The omitzero semantics under test follow encoding/json (Go 1.24+): a field
// is skipped when its IsZero method says so, or when the value is zero by
// reflect rules. Every case that stdlib can express is byte-compared against
// encoding/json; shapes only vjson has (value.Value, stream.Stream) or that
// vjson rejects (embedded fields carrying options) use explicit expectations.

// -----------------------------------------------------------------------------
// Shared method-bearing types (methods cannot live inside test functions)
// -----------------------------------------------------------------------------

type ozBox struct {
	V int
}

func (b ozBox) IsZero() bool { return b.V == 0 }

type ozBoxPtr struct {
	V int
}

func (b *ozBoxPtr) IsZero() bool { return b.V == 0 }

type ozZeroer interface{ IsZero() bool }

type ozNeverZero struct{}

func (ozNeverZero) IsZero() bool { return false }

type ozCntAlways int

func (c ozCntAlways) IsZero() bool { return false }

type ozTags []string

func (tg ozTags) IsZero() bool { return len(tg) == 2 }

type ozMarshaling struct {
	A int
}

func (m ozMarshaling) MarshalJSON() ([]byte, error) { return json.Marshal(m.A) }

// Marshaler fields: the constant payload makes a wrongly-run hook visible,
// since the zero cases below expect the field to be gone entirely.
type ozMPlain struct{ A int }

func (m ozMPlain) MarshalJSON() ([]byte, error) { return []byte(`"marshaled"`), nil }

type ozMZeroer struct{ A int }

func (m ozMZeroer) MarshalJSON() ([]byte, error) { return []byte(`"marshaled"`), nil }
func (m ozMZeroer) IsZero() bool                 { return m.A == 0 }

// -----------------------------------------------------------------------------
// Shared element shapes: tagged fields in leading, trailing, and both-edge
// positions, for the nested and repeated emission tests
// -----------------------------------------------------------------------------

type ozLead struct {
	At time.Time `json:"at,omitzero"`
	N  int       `json:"n"`
}

type ozTail struct {
	N  int       `json:"n"`
	At time.Time `json:"at,omitzero"`
}

type ozBoth struct {
	At time.Time `json:"at,omitzero"`
	N  int       `json:"n"`
	D  time.Time `json:"d,omitzero"`
}

type ozLeaf struct {
	X int `json:"x,omitzero"`
}

// -----------------------------------------------------------------------------
// IsZero method bindings
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_Time(t *testing.T) {
	type ev struct {
		Name string    `json:"name"`
		At   time.Time `json:"at,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, ev{Name: "boot"})
	assertJSONEqual(t, "zero time", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, ev{Name: "boot", At: time.Unix(1, 0)})
	assertJSONEqual(t, "non-zero time", stdRaw, vjRaw)

	// A zero time in a non-UTC location is still method-zero.
	loc := time.FixedZone("X", 3600)
	stdRaw, vjRaw = encodeWithBoth(t, ev{Name: "boot", At: time.Time{}.In(loc)})
	assertJSONEqual(t, "zero time in location", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_ValueReceiver(t *testing.T) {
	type outer struct {
		B ozBox `json:"b,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero value receiver", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{B: ozBox{V: 1}})
	assertJSONEqual(t, "non-zero value receiver", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_PointerReceiver(t *testing.T) {
	type outer struct {
		B ozBoxPtr `json:"b,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero pointer receiver", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{B: ozBoxPtr{V: 1}})
	assertJSONEqual(t, "non-zero pointer receiver", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_PointerField(t *testing.T) {
	type outer struct {
		B *ozBox `json:"b,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil pointer", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{B: new(ozBox)})
	assertJSONEqual(t, "pointer to zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{B: &ozBox{V: 1}})
	assertJSONEqual(t, "pointer to non-zero", stdRaw, vjRaw)
}

// A pointer whose type (or pointee type) has no IsZero method is zero only
// when nil: a non-nil pointer to a zero value is kept, the contrast to the
// method-bearing cases above.
func TestMarshal_OmitZero_PointerToZeroNoMethod(t *testing.T) {
	type plain struct{ A int }
	type outer struct {
		P *int   `json:"p,omitzero"`
		Q *plain `json:"q,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil pointers", stdRaw, vjRaw)

	z := 0
	stdRaw, vjRaw = encodeWithBoth(t, outer{P: &z, Q: &plain{}})
	assertJSONEqual(t, "non-nil pointers to zero", stdRaw, vjRaw)

	v := 1
	stdRaw, vjRaw = encodeWithBoth(t, outer{P: &v, Q: &plain{A: 1}})
	assertJSONEqual(t, "non-nil pointers to non-zero", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_InterfaceField(t *testing.T) {
	type outer struct {
		Z ozZeroer `json:"z,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil interface", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Z: (*ozBox)(nil)})
	assertJSONEqual(t, "typed nil pointer elem", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Z: ozBox{}})
	assertJSONEqual(t, "zero elem", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Z: ozBox{V: 1}})
	assertJSONEqual(t, "non-zero elem", stdRaw, vjRaw)
}

// A method on a named primitive type wins over the kind-level check: the
// field routes through the method even though its ElemTypeKind is a plain
// int.
func TestMarshal_OmitZero_NamedPrimitiveMethod(t *testing.T) {
	type outer struct {
		C ozCntAlways `json:"c,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "always-false IsZero at zero value", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{C: 7})
	assertJSONEqual(t, "always-false IsZero at non-zero value", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_NamedSliceMethod(t *testing.T) {
	type outer struct {
		T ozTags `json:"t,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil named slice", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{T: ozTags{"a"}})
	assertJSONEqual(t, "one-element named slice", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{T: ozTags{"a", "b"}})
	assertJSONEqual(t, "two-element named slice (IsZero)", stdRaw, vjRaw)
}

// -----------------------------------------------------------------------------
// Marshaler fields: the check runs at the Go fallback before the hook
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_MarshalerField(t *testing.T) {
	type plain struct {
		M ozMPlain `json:"m,omitzero"`
	}
	// No IsZero method: the reflect walk omits the zero struct and the hook
	// must not run, or its payload would appear.
	stdRaw, vjRaw := encodeWithBoth(t, plain{})
	assertJSONEqual(t, "zero marshaler field", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, plain{M: ozMPlain{A: 1}})
	assertJSONEqual(t, "non-zero marshaler field", stdRaw, vjRaw)

	type zeroer struct {
		M ozMZeroer `json:"m,omitzero"`
	}
	// The IsZero method decides even though the value also marshals via the
	// hook.
	stdRaw, vjRaw = encodeWithBoth(t, zeroer{})
	assertJSONEqual(t, "zero method marshaler field", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, zeroer{M: ozMZeroer{A: 1}})
	assertJSONEqual(t, "non-zero method marshaler field", stdRaw, vjRaw)
}

// -----------------------------------------------------------------------------
// Pure reflect semantics
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_StructReflect(t *testing.T) {
	type hidden struct {
		A int
		m int
	}
	type outer struct {
		H hidden `json:"h,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "all-zero struct", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{H: hidden{A: 1}})
	assertJSONEqual(t, "exported non-zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{H: hidden{m: 1}})
	assertJSONEqual(t, "unexported non-zero", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_BlankField(t *testing.T) {
	type withBlank struct {
		A int
		_ int
	}
	type outer struct {
		B withBlank `json:"b,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "blank field never blocks zero", stdRaw, vjRaw)
}

// Recursion inside a struct never consults IsZero methods: the inner type's
// method is invisible to the outer field's reflect walk.
func TestMarshal_OmitZero_RecursionIgnoresMethods(t *testing.T) {
	type direct struct {
		N ozNeverZero `json:"n,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, direct{})
	assertJSONEqual(t, "direct field honors method", stdRaw, vjRaw)

	type wrap struct {
		N ozNeverZero
	}
	type nested struct {
		W wrap `json:"w,omitzero"`
	}
	stdRaw, vjRaw = encodeWithBoth(t, nested{})
	assertJSONEqual(t, "nested walk ignores method", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_Array(t *testing.T) {
	type outer struct {
		A [2]int      `json:"a,omitzero"`
		B [0]int      `json:"b,omitzero"`
		C [4]byte     `json:"c,omitzero"`
		D [2]struct{} `json:"d,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "all-zero arrays", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{A: [2]int{0, 1}})
	assertJSONEqual(t, "non-zero array", stdRaw, vjRaw)

	// Non-zero byte arrays encode as base64 (a standing vjson difference from
	// stdlib's number array); only the omission decision is under test here.
	// The other arrays stay zero, so only c survives.
	got, err := vjson.Marshal(outer{C: [4]byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"c":"AQAAAA=="}`; string(got) != want {
		t.Errorf("non-zero byte array: got %s, want %s", got, want)
	}
}

func TestMarshal_OmitZero_ArrayOfTime(t *testing.T) {
	type outer struct {
		T [2]time.Time `json:"t,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero time array", stdRaw, vjRaw)

	// A zero time carrying a location is reflect-non-zero even though the
	// IsZero method would call it zero: the element-wise walk has no methods.
	stdRaw, vjRaw = encodeWithBoth(t, outer{T: [2]time.Time{time.Time{}.In(time.UTC)}})
	assertJSONEqual(t, "located zero time array", stdRaw, vjRaw)
}

// -----------------------------------------------------------------------------
// Tags on fields of nested and repeated structs
// -----------------------------------------------------------------------------

// The tagged fields belong to a struct that is itself a plain field: the
// skip decision runs inside the nested emission.
func TestMarshal_OmitZero_NestedStructFields(t *testing.T) {
	type mid struct {
		Leaf ozLeaf    `json:"leaf"`
		At   time.Time `json:"at,omitzero"`
	}
	type outer struct {
		Mid mid `json:"mid"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero fields in nested struct", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Mid: mid{At: time.Unix(1, 0)}})
	assertJSONEqual(t, "nested struct inner field non-zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Mid: mid{Leaf: ozLeaf{X: 1}, At: time.Unix(1, 0)}})
	assertJSONEqual(t, "nested struct both fields non-zero", stdRaw, vjRaw)

	// An omitzero field whose struct carries its own tagged fields: the
	// outer skip covers the whole nested emission.
	type deep struct {
		L ozLeaf `json:"l,omitzero"`
	}
	stdRaw, vjRaw = encodeWithBoth(t, deep{})
	assertJSONEqual(t, "two-level zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, deep{L: ozLeaf{X: 1}})
	assertJSONEqual(t, "two-level non-zero", stdRaw, vjRaw)
}

// Element structs carry their own omitzero tags: the skip ops re-run for
// every element, with leading, trailing, and both-edge omissions.
func TestMarshal_OmitZero_SliceElements(t *testing.T) {
	type outer struct {
		Items []ozLead `json:"items"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil slice of tagged elements", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Items: []ozLead{{N: 1}, {N: 2}}})
	assertJSONEqual(t, "leading field omitted per element", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Items: []ozLead{
		{At: time.Unix(1, 0), N: 1},
		{N: 2},
		{At: time.Unix(3, 0), N: 3},
	}})
	assertJSONEqual(t, "mixed zero and non-zero elements", stdRaw, vjRaw)

	type tailer struct {
		Items []ozTail `json:"items"`
	}
	stdRaw, vjRaw = encodeWithBoth(t, tailer{Items: []ozTail{{N: 1}, {N: 2, At: time.Unix(1, 0)}}})
	assertJSONEqual(t, "trailing field omitted per element", stdRaw, vjRaw)

	type both struct {
		Items []ozBoth `json:"items"`
	}
	stdRaw, vjRaw = encodeWithBoth(t, both{Items: []ozBoth{
		{At: time.Unix(1, 0), N: 1, D: time.Unix(4, 0)},
		{N: 2},
		{At: time.Unix(2, 0), N: 3},
		{N: 4, D: time.Unix(5, 0)},
	}})
	assertJSONEqual(t, "both edges omitted per element", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_ArrayElements(t *testing.T) {
	type outer struct {
		Arr [2]ozLead `json:"arr"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "all-zero array elements", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{Arr: [2]ozLead{{At: time.Unix(1, 0)}, {N: 2}}})
	assertJSONEqual(t, "mixed array elements", stdRaw, vjRaw)
}

// Single-key maps keep the comparison independent of map iteration order.
func TestMarshal_OmitZero_MapValueFields(t *testing.T) {
	type outer struct {
		M map[string]ozLeaf `json:"m"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil map", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{M: map[string]ozLeaf{"k": {}}})
	assertJSONEqual(t, "zero map value", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{M: map[string]ozLeaf{"k": {X: 1}}})
	assertJSONEqual(t, "non-zero map value", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_NilVsEmpty(t *testing.T) {
	type outer struct {
		S  []string       `json:"s,omitzero"`
		M  map[string]int `json:"m,omitzero"`
		BS []byte         `json:"bs,omitzero"`
		N  json.Number    `json:"n,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "all nil", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{
		S: []string{},
		M: map[string]int{},
	})
	assertJSONEqual(t, "empty but non-nil", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{
		S:  []string{"x"},
		M:  map[string]int{"k": 1},
		BS: []byte("ab"),
		N:  json.Number("0"),
	})
	assertJSONEqual(t, "non-empty", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_RawMessage(t *testing.T) {
	type outer struct {
		R json.RawMessage `json:"r,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil raw message", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{R: json.RawMessage("null")})
	assertJSONEqual(t, "non-nil raw message", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{R: json.RawMessage(`{"k":1}`)})
	assertJSONEqual(t, "object raw message", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_Scalars(t *testing.T) {
	type outer struct {
		I int      `json:"i,omitzero"`
		F float64  `json:"f,omitzero"`
		S string   `json:"s,omitzero"`
		P *int     `json:"p,omitzero"`
		E struct{} `json:"e,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero scalars", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{F: math.Copysign(0, -1)})
	assertJSONEqual(t, "negative zero is zero", stdRaw, vjRaw)

	v := 1
	stdRaw, vjRaw = encodeWithBoth(t, outer{I: 1, F: 1.5, S: "x", P: &v})
	assertJSONEqual(t, "non-zero scalars", stdRaw, vjRaw)
}

// The method lookup is static: `any` has no method set, so a non-nil
// interface holding a zero time.Time is not omitted.
func TestMarshal_OmitZero_AnyHoldsZeroTime(t *testing.T) {
	type outer struct {
		V any `json:"v,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "nil any", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{V: time.Time{}})
	assertJSONEqual(t, "any holding zero time", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_Indent(t *testing.T) {
	type ev struct {
		Name string    `json:"name"`
		At   time.Time `json:"at,omitzero"`
		Tags []string  `json:"tags,omitzero"`
	}
	got, err := vjson.MarshalIndent(ev{Name: "x"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	stdRaw, err := json.MarshalIndent(ev{Name: "x"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(stdRaw) {
		t.Errorf("indent omitzero:\n stdlib: %q\n vjson:  %q", stdRaw, got)
	}
}

// -----------------------------------------------------------------------------
// Interaction with omitempty and ,string
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_BothTags(t *testing.T) {
	type outer struct {
		T ozTags         `json:"t,omitzero,omitempty"`
		S []string       `json:"s,omitzero,omitempty"`
		M map[string]int `json:"m,omitzero,omitempty"`
	}
	// nil: both tags say skip.
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "both tags nil", stdRaw, vjRaw)

	// empty non-nil: omitempty says skip (len==0), omitzero says keep.
	stdRaw, vjRaw = encodeWithBoth(t, outer{S: []string{}, M: map[string]int{}})
	assertJSONEqual(t, "both tags empty", stdRaw, vjRaw)

	// method-zero but non-empty: omitzero says skip, omitempty says keep.
	stdRaw, vjRaw = encodeWithBoth(t, outer{T: ozTags{"a", "b"}})
	assertJSONEqual(t, "both tags method-zero", stdRaw, vjRaw)

	// neither: kept.
	stdRaw, vjRaw = encodeWithBoth(t, outer{S: []string{"x"}})
	assertJSONEqual(t, "both tags non-empty", stdRaw, vjRaw)
}

func TestMarshal_OmitZero_QuotedInt(t *testing.T) {
	type outer struct {
		N int `json:"n,string,omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "quoted zero int", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{N: 5})
	assertJSONEqual(t, "quoted non-zero int", stdRaw, vjRaw)
}

// The empty-name spelling keeps the Go field name and still carries the
// option.
func TestMarshal_OmitZero_EmptyName(t *testing.T) {
	type outer struct {
		At   time.Time `json:",omitzero"`
		Tags []string  `json:",omitzero"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "empty-name zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{At: time.Unix(1, 0), Tags: []string{"x"}})
	assertJSONEqual(t, "empty-name non-zero", stdRaw, vjRaw)
}

// -----------------------------------------------------------------------------
// Promotion across embedding
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_ViaEmbeddedPointer(t *testing.T) {
	type inner struct {
		X int `json:"x,omitzero"`
	}
	type outer struct {
		*inner
		Name string `json:"name"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{Name: "n"})
	assertJSONEqual(t, "nil embedded pointer", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{inner: &inner{}, Name: "n"})
	assertJSONEqual(t, "zero pointee", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{inner: &inner{X: 1}, Name: "n"})
	assertJSONEqual(t, "non-zero pointee", stdRaw, vjRaw)
}

// A tagged field promoted through value embedding keeps its skip decision.
func TestMarshal_OmitZero_ValueEmbedPromotion(t *testing.T) {
	type outer struct {
		ozLeaf
		Name string `json:"name"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{Name: "n"})
	assertJSONEqual(t, "promoted zero", stdRaw, vjRaw)

	stdRaw, vjRaw = encodeWithBoth(t, outer{ozLeaf: ozLeaf{X: 1}, Name: "n"})
	assertJSONEqual(t, "promoted non-zero", stdRaw, vjRaw)
}

// -----------------------------------------------------------------------------
// value.Value and stream.Stream: omitzero does not participate (vjson-only
// expectations; stdlib would reflect-omit a zero Value)
// -----------------------------------------------------------------------------

func TestMarshal_OmitZero_ValueFieldNoOp(t *testing.T) {
	type outer struct {
		V value.Value `json:"v,omitzero"`
	}
	got, err := vjson.Marshal(outer{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":null}`; string(got) != want {
		t.Errorf("zero Value with omitzero: got %s, want %s", got, want)
	}
}

// The contrast field proves the no-op: the same empty producer IS dropped
// under omitempty.
func TestMarshal_OmitZero_StreamFieldNoOp(t *testing.T) {
	type outer struct {
		A stream.Stream[int] `json:"a,omitzero"`
		B stream.Stream[int] `json:"b,omitempty"`
	}
	mk := func(ns ...int) stream.Stream[int] {
		var s stream.Stream[int]
		s.OnWrite(func(sink stream.Sink[int]) error {
			for _, n := range ns {
				v := n
				if err := sink.Encode(&v); err != nil {
					return err
				}
			}
			return nil
		})
		return s
	}
	got, err := vjson.Marshal(outer{A: mk(), B: mk()})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":[]}`; string(got) != want {
		t.Errorf("empty producers: got %s, want %s", got, want)
	}

	got, err = vjson.Marshal(outer{A: mk(1, 2), B: mk(3)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":[1,2],"b":[3]}`; string(got) != want {
		t.Errorf("filled producers: got %s, want %s", got, want)
	}
}

// -----------------------------------------------------------------------------
// Baseline pins: omitempty never elides struct-shaped fields (v1 semantics)
// -----------------------------------------------------------------------------

func TestMarshal_OmitEmpty_StructBaseline(t *testing.T) {
	type plain struct {
		A int `json:"a,omitempty"`
	}
	type outer struct {
		P plain `json:"p,omitempty"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "plain struct omitempty", stdRaw, vjRaw)
	if stdRaw != `{"p":{}}` {
		t.Errorf("stdlib emitted %s, want {\"p\":{}}", stdRaw)
	}
}

func TestMarshal_OmitEmpty_MarshalerStructBaseline(t *testing.T) {
	type outer struct {
		M ozMarshaling `json:"m,omitempty"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "marshaler struct omitempty", stdRaw, vjRaw)
}

func TestMarshal_OmitEmpty_TimeBaseline(t *testing.T) {
	type outer struct {
		At time.Time `json:"at,omitempty"`
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{})
	assertJSONEqual(t, "zero time omitempty", stdRaw, vjRaw)
	if !strings.Contains(stdRaw, "0001-01-01") {
		t.Errorf("stdlib emitted %s, want the zero timestamp", stdRaw)
	}
}

// -----------------------------------------------------------------------------
// Strict tag-option parsing
// -----------------------------------------------------------------------------

type ozMutantOmitZeroCase struct {
	A int `json:"a,omitZero"` //nolint:staticcheck // deliberate mutant
}

type ozMutantAllCaps struct {
	A int `json:"a,OMITZERO"` //nolint:staticcheck // deliberate mutant
}

type ozMutantUnderscore struct {
	A int `json:"a,omit_zero"` //nolint:staticcheck // deliberate mutant
}

type ozMutantTrailing struct {
	A int `json:"a,omitzero_"` //nolint:staticcheck // deliberate mutant
}

type ozMutantOmitEmpty struct {
	A int `json:"a,omitEmpty"` //nolint:staticcheck // deliberate mutant
}

type ozMutantString struct {
	A int `json:"a,String"` //nolint:staticcheck // deliberate mutant
}

type ozMutantEmbed struct {
	A int `json:"a,Embed"` //nolint:staticcheck // deliberate mutant
}

func TestMarshal_TagMutantsRejected(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"omitZero", &ozMutantOmitZeroCase{}},
		{"OMITZERO", &ozMutantAllCaps{}},
		{"omit_zero", &ozMutantUnderscore{}},
		{"omitzero_", &ozMutantTrailing{}},
		{"omitEmpty", &ozMutantOmitEmpty{}},
		{"String", &ozMutantString{}},
		{"Embed", &ozMutantEmbed{}},
	}
	for _, tc := range cases {
		if _, err := vjson.Marshal(tc.v); err == nil {
			t.Errorf("%s: expected marshal error, got success", tc.name)
		} else if !strings.Contains(err.Error(), "specify") {
			t.Errorf("%s: error lacks the canonical spelling hint: %v", tc.name, err)
		}
		if err := vjson.Unmarshal([]byte(`{"A":1}`), tc.v); err == nil {
			t.Errorf("%s: expected unmarshal error, got success", tc.name)
		}
	}
}

func TestMarshal_TagUnknownOptionIgnored(t *testing.T) {
	type outer struct {
		A int `json:"a,banana"` //nolint:staticcheck // unknown option, must stay ignored
	}
	stdRaw, vjRaw := encodeWithBoth(t, outer{A: 1})
	assertJSONEqual(t, "unknown option ignored", stdRaw, vjRaw)
}

func TestMarshal_EmbeddedWithOptionsRejected(t *testing.T) {
	type embed struct {
		Name string `json:"name,omitempty"`
	}
	type anon struct {
		embed `json:",omitempty"`
	}
	if _, err := vjson.Marshal(anon{}); err == nil {
		t.Errorf("anonymous embedded with options: expected marshal error, got success")
	}
	if err := vjson.Unmarshal([]byte(`{"name":"x"}`), &anon{}); err == nil {
		t.Errorf("anonymous embedded with options: expected unmarshal error, got success")
	}

	type named struct {
		Inner embed `json:",embed,omitzero"`
	}
	if _, err := vjson.Marshal(named{}); err == nil {
		t.Errorf("named embed with extra options: expected marshal error, got success")
	}
}

func TestMarshal_EmbeddedEmbedOptionStillPromotes(t *testing.T) {
	type inner struct {
		X int `json:"x"`
	}
	type outer struct {
		inner `json:",embed"`
		Name  string `json:"name"`
	}
	obj := outer{inner: inner{X: 1}, Name: "n"}
	stdRaw, vjRaw := encodeWithBoth(t, obj)
	assertJSONEqual(t, "anonymous embed option promotes", stdRaw, vjRaw)
}
