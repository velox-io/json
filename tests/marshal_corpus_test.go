package tests

// An encoding corpus run against encoding/json. Each case encodes one value
// through every encode entry point, typed and through an any, under the
// options that select encoding/json's escaping and float format: Marshal,
// MarshalIndent, AppendMarshal onto a non-empty prefix, and an Encoder. The
// make test matrix runs it under the native encoder and the Go fallback
// (vj_noencvm).

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"

	vjson "github.com/velox-io/json"
)

var stdCompat = vjson.Join(vjson.EscapeHTML(true), vjson.EscapeLineTerms(true), vjson.AllowInvalidUTF8(false), vjson.FloatExpAuto(true))

// marshalForm selects how a case's output is compared. Map entries encode
// in iteration order, so a map with several keys compares by decoded value,
// with its indented form checked for canonical layout. A json.Marshaler's
// output and a RawMessage are written verbatim, so their cases skip the
// indented comparison.
type marshalForm int

const (
	formExact marshalForm = iota
	formUnordered
	formVerbatim
)

type marshalCase struct {
	name string
	run  func(t *testing.T)
}

// mc builds a case encoding v as T and as an any holding it.
func mc[T any](name string, v T) marshalCase { return mcForm(name, v, formExact) }

func mcForm[T any](name string, v T, form marshalForm) marshalCase {
	return marshalCase{name, func(t *testing.T) {
		t.Helper()
		assertMarshalParity[T](t, name, v, form)
		assertMarshalParity[any](t, name+"/any", v, form)
	}}
}

func assertMarshalParity[T any](t *testing.T, name string, v T, form marshalForm) {
	t.Helper()
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: encoding/json: %v", name, err)
	}
	same := func(got, want []byte) bool {
		if form != formUnordered {
			return bytes.Equal(got, want)
		}
		var g, w any
		return json.Unmarshal(got, &g) == nil && json.Unmarshal(want, &w) == nil && reflect.DeepEqual(g, w)
	}
	check := func(leg string, got []byte, err error, want []byte) {
		t.Helper()
		if err != nil {
			t.Errorf("%s/%s: %v", name, leg, err)
			return
		}
		if !same(got, want) {
			t.Errorf("%s/%s:\n got %s\nwant %s", name, leg, got, want)
		}
	}
	got, err := vjson.Marshal(v, stdCompat)
	check("Marshal", got, err, want)

	got, err = vjson.AppendMarshal([]byte("prefix:"), v, stdCompat)
	if err == nil && !bytes.HasPrefix(got, []byte("prefix:")) {
		t.Errorf("%s/AppendMarshal: lost the prefix: %s", name, got)
	}
	check("AppendMarshal", bytes.TrimPrefix(got, []byte("prefix:")), err, want)

	var buf bytes.Buffer
	err = vjson.NewEncoder(&buf, stdCompat).Encode(v)
	if err == nil && !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("%s/Encoder: no trailing newline: %q", name, buf.Bytes())
	}
	check("Encoder", bytes.TrimSuffix(buf.Bytes(), []byte("\n")), err, want)

	if form == formVerbatim {
		return
	}
	got, err = vjson.MarshalIndent(v, " ", "\t", stdCompat)
	if err != nil {
		t.Errorf("%s/MarshalIndent: %v", name, err)
		return
	}
	// An indented document is the indentation of its compact form, so
	// checking that, plus the compact form's value, pins the layout without
	// depending on key order.
	var compact, ind bytes.Buffer
	if err := json.Compact(&compact, got); err != nil {
		t.Errorf("%s/MarshalIndent: invalid output %s: %v", name, got, err)
		return
	}
	if err := json.Indent(&ind, compact.Bytes(), " ", "\t"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ind.Bytes()) {
		t.Errorf("%s/MarshalIndent: layout\n got %s\nwant %s", name, got, ind.Bytes())
	}
	check("MarshalIndent", compact.Bytes(), nil, want)
}

type mcQuoted struct {
	B bool    `json:"b,string"`
	I int8    `json:"i,string"`
	J int16   `json:"j,string"`
	K int32   `json:"k,string"`
	L int64   `json:"l,string"`
	U uint    `json:"u,string"`
	V uint8   `json:"v,string"`
	W uint16  `json:"w,string"`
	X uint32  `json:"x,string"`
	Y uint64  `json:"y,string"`
	F float32 `json:"f,string"`
	G float64 `json:"g,string"`
	S string  `json:"s,string"`
	P *int    `json:"p,string"`
}

type mcMarshaler struct{ N int }

func (m mcMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"m"`), nil }

type mcText struct{ S string }

func (m mcText) MarshalText() ([]byte, error) { return []byte("t:" + m.S), nil }

type mcMethodsQuoted struct {
	M mcMarshaler `json:"m,string"` //nolint:staticcheck // methods ignore the option
	T mcText      `json:"t,string"` //nolint:staticcheck // methods ignore the option
}

// mcStatus, mcName and mcCount are int, string and int64 kinds that encode
// through value-receiver methods.
type mcStatus int

func (s mcStatus) MarshalJSON() ([]byte, error) {
	return []byte(`"s` + strconv.Itoa(int(s)) + `"`), nil
}

type mcName string

func (n mcName) MarshalText() ([]byte, error) { return []byte("n:" + string(n)), nil }

type mcCount int64

func (c mcCount) MarshalJSON() ([]byte, error) { return []byte(`"c` + strconv.Itoa(int(c)) + `"`), nil }

type mcHookedMaps struct {
	S map[string]mcStatus
	N map[string]mcName
	C map[string]mcCount
}

type mcBytes struct {
	Nil   []byte
	Empty []byte
	Data  []byte
	Arr   [4]byte
	I8    [2]int8
	Any   any
}

func TestMarshalCorpus_Scalars(t *testing.T) {
	for _, c := range []marshalCase{
		mc("bool", true),
		mc("int", int(-5)),
		mc("int8", int8(math.MinInt8)),
		mc("int16", int16(math.MinInt16)),
		mc("int32", int32(math.MinInt32)),
		mc("int64", int64(math.MinInt64)),
		mc("uint", uint(7)),
		mc("uint8", uint8(math.MaxUint8)),
		mc("uint16", uint16(math.MaxUint16)),
		mc("uint32", uint32(math.MaxUint32)),
		mc("uint64", uint64(math.MaxUint64)),
		mc("float32", float32(0.5)),
		mc("float32 exponent", float32(1e-7)),
		mc("float64", 1.5e3),
		mc("float64 exponent", 1e21),
		mc("float64 small", 1e-7),
		mc("negative zero", math.Copysign(0, -1)),
		mc("string", "a\t\"<>&\u2028\u00e9\x01"),
		mc("number", json.Number("12.5e3")),
		mcForm("raw message", json.RawMessage(`{"k":[1,2]}`), formVerbatim),
		mc("raw message scalar", json.RawMessage(`"r"`)),
	} {
		c.run(t)
	}
}

func TestMarshalCorpus_Composites(t *testing.T) {
	n := 9
	quoted := mcQuoted{
		B: true, I: math.MinInt8, J: math.MinInt16, K: math.MinInt32, L: math.MinInt64,
		U: 7, V: math.MaxUint8, W: math.MaxUint16, X: math.MaxUint32, Y: math.MaxUint64,
		F: 0.5, G: 1.5e3, S: "h\"i", P: &n,
	}
	for _, c := range []marshalCase{
		mc("quoted kinds", quoted),
		mc("quoted zero", mcQuoted{}),
		mc("methods ignore string", mcMethodsQuoted{T: mcText{S: "x"}}),
		mc("byte shapes", mcBytes{Empty: []byte{}, Data: []byte("abc"), Arr: [4]byte{1}, I8: [2]int8{-1, 2}, Any: [2]byte{3, 4}}),
		mcForm("any map", map[string]any{"b": "s", "a": 1.0, "c": nil, "d": []any{true, map[string]any{}}}, formUnordered),
		mc("any slice", []any{1.0, "x", nil, []byte("hi"), json.Number("7")}),
		mcForm("int keys", map[int]string{10: "a", -1: "b", 2: "c"}, formUnordered),
		mc("int key", map[int8]string{-128: "a"}),
		mcForm("uint8 keys", map[uint8]int{255: 1, 0: 2}, formUnordered),
		mcForm("text keys", map[mcText]int{{S: "b"}: 1, {S: "a"}: 2}, formUnordered),
		mc("text key", map[mcText]int{{S: "<&>"}: 1}),
		mc("nested pointers", &struct{ P **int }{P: func() **int { p := &n; return &p }()}),
	} {
		c.run(t)
	}
}

// mcRef and mcRefs hold a single pointer, so an interface stores them in its
// data word rather than behind a pointer.
type mcRef struct{ P *int }

type mcRefs [1]*int

type mcAnyHost struct{ I any }

// An interface payload encodes the same whether the interface stores it
// behind a pointer or in its data word.
func TestMarshalCorpus_InterfacePayloads(t *testing.T) {
	n := 7
	for _, c := range []marshalCase{
		mc("single-pointer struct", mcRef{P: &n}),
		mc("single-pointer array", mcRefs{&n}),
		mc("single-pointer struct in field", mcAnyHost{I: mcRef{P: &n}}),
		mc("single-pointer array in field", mcAnyHost{I: mcRefs{&n}}),
		mc("nil single-pointer struct in field", mcAnyHost{I: mcRef{}}),
		mc("single-pointer struct in slice", []any{mcRef{P: &n}, mcRefs{&n}, mcRef{}}),
		mc("single-pointer struct in map", map[string]any{"k": mcRef{P: &n}}),
		mc("map in field", mcAnyHost{I: map[string]int{"a": 1}}),
	} {
		c.run(t)
	}
}

type mcPair struct {
	A int
	B string
}

// An interface payload that is a pointer encodes its pointee, null when nil,
// however deep the chain and whichever receiver the pointee's method has.
func TestMarshalCorpus_PointerInterfacePayloads(t *testing.T) {
	n := 7
	pair := &mcPair{A: 1, B: "x"}
	var nilPair *mcPair
	for _, c := range []marshalCase{
		mc("pointer to struct", mcAnyHost{I: pair}),
		mc("nil pointer", mcAnyHost{I: nilPair}),
		mc("pointer to int", mcAnyHost{I: &n}),
		mc("pointer chain", mcAnyHost{I: &pair}),
		mc("pointer chain inner nil", mcAnyHost{I: &nilPair}),
		mc("pointer to data-word struct", mcAnyHost{I: &mcRef{P: &n}}),
		mc("pointer to value-receiver hook", mcAnyHost{I: &mcMarshaler{N: 1}}),
		mcForm("pointer-receiver hook", mcAnyHost{I: &ptrOnlyMarshaler{Name: "p"}}, formVerbatim),
		mc("pointers in slice field", mcAnySliceHost{S: []any{pair, nilPair, &mcPair{A: 2}, &n}}),
		mc("pointers in slice", []any{pair, nilPair, &n}),
		mc("pointer in map", map[string]any{"k": pair}),
	} {
		c.run(t)
	}
}

// mcShape is a non-empty interface; its implementations cover a boxed
// struct, a pointer receiver, a primitive kind, a data-word struct, and a
// marshal method.
type mcShape interface{ Area() int }

type mcSquare struct{ S int }

func (s mcSquare) Area() int { return s.S * s.S }

type mcCircle struct{ R int }

func (c *mcCircle) Area() int { return 3 * c.R * c.R }

type mcLength int

func (l mcLength) Area() int { return 0 }

type mcRefShape struct{ P *int }

func (mcRefShape) Area() int { return 0 }

type mcHookShape struct{ N int }

func (mcHookShape) Area() int                    { return 0 }
func (mcHookShape) MarshalJSON() ([]byte, error) { return []byte(`"hook"`), nil }

// mcBlank is a non-empty interface whose method set includes IsZero, so
// omitzero asks the dynamic value.
type mcBlank interface{ IsZero() bool }

type mcBlankVal struct{ N int }

func (b mcBlankVal) IsZero() bool { return b.N == 0 }

type mcShapeHost struct {
	S mcShape
	T mcShape `json:"t,omitempty"`
	U mcShape `json:"u,omitzero"`
	Z mcBlank `json:"z,omitzero"`
	P *mcShape
}

type mcShapes struct {
	L []mcShape
	M map[string]mcShape
}

// A non-empty interface encodes its dynamic value exactly as any does.
func TestMarshalCorpus_NonEmptyInterfacePayloads(t *testing.T) {
	n := 7
	var nilCircle *mcCircle
	var shape mcShape = mcSquare{S: 2}
	for _, c := range []marshalCase{
		mc("nil", mcShapeHost{}),
		mc("boxed struct", mcShapeHost{S: mcSquare{S: 2}, T: mcSquare{S: 3}, U: mcSquare{}}),
		mc("pointer", mcShapeHost{S: &mcCircle{R: 1}}),
		mc("typed nil pointer", mcShapeHost{S: nilCircle, T: nilCircle, U: nilCircle}),
		mc("primitive kind", mcShapeHost{S: mcLength(5)}),
		mc("data-word struct", mcShapeHost{S: mcRefShape{P: &n}, T: mcRefShape{}}),
		mc("marshal method", mcShapeHost{S: mcHookShape{N: 1}}),
		mc("omitzero method zero", mcShapeHost{Z: mcBlankVal{}}),
		mc("omitzero method non-zero", mcShapeHost{Z: mcBlankVal{N: 1}}),
		mc("pointer to interface", mcShapeHost{P: &shape}),
		mc("elements", mcShapes{
			L: []mcShape{mcSquare{S: 1}, nil, &mcCircle{R: 2}, mcLength(3), mcRefShape{P: &n}, mcHookShape{}},
			M: map[string]mcShape{"k": mcSquare{S: 4}},
		}),
		mc("slice", []mcShape{mcSquare{S: 1}, nilCircle, mcLength(2)}),
	} {
		c.run(t)
	}
}

// mcRefHook is stored in an interface's data word and encodes through a
// value-receiver method.
type mcRefHook struct{ P *int }

func (r mcRefHook) MarshalJSON() ([]byte, error) { return []byte(strconv.Itoa(*r.P)), nil }

type mcAnySliceHost struct{ S []any }

// An interface payload whose type has a marshal method encodes through it,
// whatever its kind and however the interface stores it.
func TestMarshalCorpus_HookedInterfacePayloads(t *testing.T) {
	n := 7
	for _, c := range []marshalCase{
		mc("hooked int in field", mcAnyHost{I: mcStatus(1)}),
		mc("hooked string in field", mcAnyHost{I: mcName("x")}),
		mc("hooked int64 in field", mcAnyHost{I: mcCount(2)}),
		mc("hooked struct in field", mcAnyHost{I: mcMarshaler{N: 1}}),
		mc("hooked data-word struct in field", mcAnyHost{I: mcRefHook{P: &n}}),
		mc("hooked values in slice field", mcAnySliceHost{S: []any{mcStatus(3), mcName("y"), mcCount(4), mcRefHook{P: &n}}}),
		mc("hooked values in slice", []any{mcStatus(5), mcName("z")}),
	} {
		c.run(t)
	}
}

// A map value whose type has a marshal method encodes through it, whatever
// its kind.
func TestMarshalCorpus_HookedMapValues(t *testing.T) {
	for _, c := range []marshalCase{
		mc("hooked map values", mcHookedMaps{
			S: map[string]mcStatus{"a": 1},
			N: map[string]mcName{"b": "x"},
			C: map[string]mcCount{"c": 2},
		}),
		mc("status map", map[string]mcStatus{"a": 1}),
		mc("name map", map[string]mcName{"b": "x"}),
		mc("count map", map[string]mcCount{"c": 2}),
	} {
		c.run(t)
	}
}
