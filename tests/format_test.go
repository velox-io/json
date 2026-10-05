package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/native/encvm"
)

func TestFormat_Marshal(t *testing.T) {
	for _, tc := range formatMarshalCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("Marshal\n got  %s\n want %s", got, tc.want)
			}
			// Decoding the encoding and encoding again reproduces it, which
			// also holds for lossy representations such as DateOnly.
			nv := reflect.New(reflect.TypeOf(tc.v).Elem()).Interface()
			if err = vjson.Unmarshal([]byte(tc.want), nv); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			again, err := vjson.Marshal(nv)
			if err != nil || string(again) != tc.want {
				t.Fatalf("round trip\n got  %s (err %v)\n want %s", again, err, tc.want)
			}
		})
	}
}

func TestFormat_Unmarshal(t *testing.T) {
	for _, tc := range formatDecodeCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := vjson.Unmarshal([]byte(tc.in), tc.into)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("Unmarshal(%s): want an error", tc.in)
				}
				var ute *vjson.UnmarshalTypeError
				if !errors.As(err, &ute) {
					t.Fatalf("Unmarshal(%s): error %T %v is not an UnmarshalTypeError", tc.in, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.in, err)
			}
			got, err := vjson.Marshal(tc.into)
			if err != nil || string(got) != tc.want {
				t.Fatalf("decoded value re-encodes as\n %s (err %v)\nwant\n %s", got, err, tc.want)
			}
		})
	}
}

func TestFormat_UnmarshalErrorCause(t *testing.T) {
	var v fmtDecTime
	err := vjson.Unmarshal([]byte(`{"layout":"2021-12-25"}`), &v)
	var ute *vjson.UnmarshalTypeError
	if !errors.As(err, &ute) || ute.Value != "string" || ute.Type != reflect.TypeFor[time.Time]() {
		t.Fatalf("layout mismatch: got %#v", err)
	}
	var pe *time.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("layout mismatch: cause %v is not a *time.ParseError", err)
	}
	var stdErr *json.UnmarshalTypeError
	if !errors.As(err, &stdErr) || stdErr.Type != ute.Type {
		t.Fatalf("layout mismatch: no encoding/json bridge: %v", err)
	}

	err = vjson.Unmarshal([]byte(`{"unix":1e3}`), &v)
	if !errors.Is(err, strconv.ErrSyntax) {
		t.Fatalf("unix exponent: got %v, want strconv.ErrSyntax in the chain", err)
	}

	var b fmtDecBytes
	err = vjson.Unmarshal([]byte(`{"b64":"A==="}`), &b)
	var corrupt base64.CorruptInputError
	if !errors.As(err, &corrupt) {
		t.Fatalf("bad base64: got %v, want a base64.CorruptInputError cause", err)
	}
	if !strings.Contains(err.Error(), "[]uint8") {
		t.Fatalf("bad base64: message %q does not name the type", err)
	}
}

type (
	fmtBadNotLast struct {
		T time.Time `json:"t,format:unix,omitempty"` //nolint:staticcheck // deliberately malformed
	}
	fmtBadNoValue struct {
		T time.Time `json:"t,format"` //nolint:staticcheck // deliberately malformed
	}
	fmtBadEmpty struct {
		T time.Time `json:"t,format:"` //nolint:staticcheck // deliberately malformed
	}
	fmtBadEmptyQuote struct {
		T time.Time `json:"t,format:''"`
	}
	fmtBadUnquoted struct {
		T time.Time `json:"t,format:2006-01-02"` //nolint:staticcheck // deliberately malformed
	}
	fmtBadUnclosed struct {
		T time.Time `json:"t,format:'2006"` //nolint:staticcheck // deliberately malformed
	}
	fmtBadMutant struct {
		T time.Time `json:"t,Format:unix"` //nolint:staticcheck // deliberate mutant
	}
	fmtBadInt struct {
		I int `json:"i,format:hex"`
	}
	fmtBadNilPtr struct {
		I *int `json:"i,omitempty,format:hex"`
	}
	fmtBadStruct struct {
		S struct{ A int } `json:"s,format:emitnull"`
	}
	fmtBadElems struct {
		T []time.Time `json:"t,format:unix"`
	}
	fmtBadBytesNil struct {
		B []byte `json:"b,format:emitnull"`
	}
	fmtBadIface struct {
		A any `json:"a,format:unix"`
	}
	fmtBadReserved struct {
		T time.Time `json:"t,format:Foo"`
	}
	fmtBadDuration struct {
		D time.Duration `json:"d,format:unix"`
	}
	fmtBadEmbedded struct {
		fmtBytes `json:",format:hex"`
	}
)

func TestFormat_TagRejected(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{&fmtBadNotLast{}, "is not last; move it to the end of the tag"},
		{&fmtBadNoValue{}, "without a value; add one"},
		{&fmtBadEmpty{}, "empty value; add one"},
		{&fmtBadEmptyQuote{}, "empty value; add one"},
		{&fmtBadUnquoted{}, "invalid value"},
		{&fmtBadUnclosed{}, "invalid quoted value"},
		{&fmtBadMutant{}, "specify `format` instead"},
		{&fmtBadInt{}, `format "hex" does not apply to type int`},
		// encoding/json/v2 would fail only on a non-nil value; velox rejects
		// the type up front, like its other tag mistakes.
		{&fmtBadNilPtr{}, `format "hex" does not apply to type int`},
		{&fmtBadStruct{}, "does not apply to type struct"},
		// A format applies to the field's own value, never to elements.
		{&fmtBadElems{}, "does not apply to type []time.Time"},
		{&fmtBadBytesNil{}, `format "emitnull" does not apply to type []uint8`},
		{&fmtBadIface{}, "does not apply to type interface {}"},
		{&fmtBadReserved{}, `format "Foo" does not apply to type time.Time`},
		{&fmtBadDuration{}, `format "unix" does not apply to type time.Duration`},
		{&fmtBadEmbedded{}, "embedded field options besides `embed`"},
	}
	for _, tc := range cases {
		name := reflect.TypeOf(tc.v).Elem().Name()
		_, err := vjson.Marshal(tc.v)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Marshal error %v, want one containing %q", name, err, tc.want)
		}
		err = vjson.Unmarshal([]byte(`{}`), tc.v)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Unmarshal error %v, want one containing %q", name, err, tc.want)
		}
	}
}

type fmtLayouts struct {
	Comma time.Time `json:"comma,format:'2006,01,02'"`
	Quote time.Time `json:"quote,format:'\"2006\"'"`
	HTML  time.Time `json:"html,format:'<2006>&'"`
}

func TestFormat_QuotedLayout(t *testing.T) {
	ts := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	v := fmtLayouts{ts, ts, ts}
	for _, tc := range []struct {
		opts []vjson.MarshalOption
		want string
	}{
		{nil, `{"comma":"2020,01,02","quote":"\"2020\"","html":"<2020>&"}`},
		{[]vjson.MarshalOption{vjson.WithEscapeHTML()}, `{"comma":"2020,01,02","quote":"\"2020\"","html":"\u003c2020\u003e\u0026"}`},
	} {
		got, err := vjson.Marshal(&v, tc.opts...)
		if err != nil || string(got) != tc.want {
			t.Fatalf("Marshal = %s (err %v), want %s", got, err, tc.want)
		}
		var back fmtLayouts
		if err := vjson.Unmarshal(got, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", got, err)
		}
		if want := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC); !back.Comma.Equal(want) {
			t.Fatalf("comma layout decoded %v, want %v", back.Comma, want)
		}
	}
}

func TestFormat_UnrepresentableTime(t *testing.T) {
	v := fmtTime{RFC3339: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
	_, err := vjson.Marshal(&v)
	var uve *vjson.UnsupportedValueError
	if !errors.As(err, &uve) || !strings.Contains(err.Error(), "has no RFC 3339 form: year outside of range") {
		t.Fatalf("year 10000 as RFC3339: got %v", err)
	}
}

type fmtIndent struct {
	Arr   []byte        `json:"arr,format:array"`
	Empty []byte        `json:"empty,format:array"`
	S     []int         `json:"s,format:emitempty"`
	D     time.Duration `json:"d,format:units"`
}

func TestFormat_Indent(t *testing.T) {
	got, err := vjson.MarshalIndent(&fmtIndent{Arr: []byte{1, 2}, Empty: []byte{}, D: time.Second}, "", "  ")
	want := "{\n  \"arr\": [\n    1,\n    2\n  ],\n  \"empty\": [],\n  \"s\": [],\n  \"d\": \"1s\"\n}"
	if err != nil || string(got) != want {
		t.Fatalf("MarshalIndent =\n%s\n(err %v), want\n%s", got, err, want)
	}
}

// A formatted field must behave the same wherever its struct sits: as a slice
// element, a map value (decoded through scannable staging), behind pointers,
// promoted through an embedded pointer, and in every decode driver.
type fmtLeaf struct {
	T time.Time     `json:"t,format:unixmilli"`
	D time.Duration `json:"d,format:iso8601"`
	B []byte        `json:"b,format:hex"`
	A []byte        `json:"a,format:array"`
	F float64       `json:"f,format:nonfinite"`
}

type fmtPromoted struct {
	P time.Time `json:"p,format:DateOnly"`
}

type fmtHost struct {
	*fmtPromoted
	Leaf  fmtLeaf
	Slice []fmtLeaf
	Map   map[string]fmtLeaf
	Ptrs  map[string]*fmtLeaf
	Arr   [2]fmtLeaf
}

func fmtLeafDoc(i int) string {
	return `{"t":` + strconv.Itoa(1577934245600+i) + `,"d":"PT` + strconv.Itoa(i) + `M","b":"0` + strconv.Itoa(i) + `ff","a":[` + strconv.Itoa(i) + `],"f":"-Infinity"}`
}

func TestFormat_Contexts(t *testing.T) {
	doc := `{"p":"2021-05-06","Leaf":` + fmtLeafDoc(1) +
		`,"Slice":[` + fmtLeafDoc(2) + `,` + fmtLeafDoc(3) + `]` +
		`,"Map":{"k":` + fmtLeafDoc(4) + `},"Ptrs":{"p":` + fmtLeafDoc(5) + `}` +
		`,"Arr":[` + fmtLeafDoc(6) + `,` + fmtLeafDoc(7) + `]}`

	var h fmtHost
	if err := vjson.Unmarshal([]byte(doc), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, err := vjson.Marshal(&h); err != nil || string(got) != doc {
		t.Fatalf("round trip\n got  %s (err %v)\n want %s", got, err, doc)
	}
	if want := time.UnixMilli(1577934245605).UTC(); !h.Ptrs["p"].T.Equal(want) {
		t.Fatalf("map pointer value decoded %v, want %v", h.Ptrs["p"].T, want)
	}

	// The streaming decoder hands values that cross a window edge to the
	// format decoder from its scratch copy; tiny windows force every edge.
	for _, size := range []int{1, 7, 64} {
		var hs fmtHost
		dec := vjson.NewDecoder(strings.NewReader(doc), vjson.WithBufferSize(size))
		if err := dec.Decode(&hs); err != nil {
			t.Fatalf("Decoder(size %d): %v", size, err)
		}
		if got, _ := vjson.Marshal(&hs); string(got) != doc {
			t.Fatalf("Decoder(size %d) round trip\n got  %s\n want %s", size, got, doc)
		}
	}

	// Formatted values own their bytes even when decoding borrows the input.
	in := []byte(`{"Leaf":` + fmtLeafDoc(8) + `}`)
	want := append([]byte(nil), in...)
	var hz fmtHost
	if err := vjson.Unmarshal(in, &hz, vjson.WithZeroCopy(true)); err != nil {
		t.Fatalf("Unmarshal zero-copy: %v", err)
	}
	copy(in, bytes.Repeat([]byte{'x'}, len(in)))
	if got, _ := vjson.Marshal(&hz.Leaf); !bytes.Contains(want, got) {
		t.Fatalf("zero-copy value changed with its input: %s", got)
	}
}

// Methods win per direction: a type that only marshals itself keeps its
// `format` for decoding, and one that only unmarshals itself keeps it for
// encoding.
type fmtTextOut float64

func (f fmtTextOut) MarshalText() ([]byte, error) {
	return []byte("x" + strconv.FormatFloat(float64(f), 'g', -1, 64)), nil
}

type fmtTextIn float64

func (f *fmtTextIn) UnmarshalText([]byte) error { *f = 42; return nil }

type fmtOneWay struct {
	Out fmtTextOut `json:"out,format:nonfinite"`
	In  fmtTextIn  `json:"in,format:nonfinite"`
}

func TestFormat_MethodsWinPerDirection(t *testing.T) {
	got, err := vjson.Marshal(&fmtOneWay{Out: fmtTextOut(math.NaN()), In: fmtTextIn(math.Inf(1))})
	if want := `{"out":"xNaN","in":"Infinity"}`; err != nil || string(got) != want {
		t.Fatalf("Marshal = %s (err %v), want %s", got, err, want)
	}
	var v fmtOneWay
	if err := vjson.Unmarshal([]byte(`{"out":"-Infinity","in":"x"}`), &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !math.IsInf(float64(v.Out), -1) || v.In != 42 {
		t.Fatalf("Unmarshal = %+v, want {Out:-Inf In:42}", v)
	}
}

// An inline variant placed past the native offset limit unfolds its case in
// Go. That path must apply field tags exactly as the compiled one does; it
// used to drop `,string`.
type fmtVariantCase struct {
	N int       `json:"n,string"`
	T time.Time `json:"t,format:unix"`
}

type fmtVariantHot struct {
	Kind   string `json:"kind"`
	Object any    `json:",embed" vjson:"variant=kind"`
}

type fmtVariantCold struct {
	Pad    [70000]byte `json:"-"`
	Kind   string      `json:"kind"`
	Object any         `json:",embed" vjson:"variant=kind"`
}

func init() {
	vjson.DefineVariantCases[fmtVariantHot, struct {
		_ fmtVariantCase `case:"c"`
	}]()
	vjson.DefineVariantCases[fmtVariantCold, struct {
		_ fmtVariantCase `case:"c"`
	}]()
}

func TestFormat_InlineVariantTags(t *testing.T) {
	if !encvm.Available {
		t.Skip("the interpreter cannot yet unfold an inline case that holds a Go-encoded field")
	}
	c := fmtVariantCase{N: 5, T: time.Unix(1577934245, 0)}
	want := `{"kind":"c","n":"5","t":1577934245}`
	for name, v := range map[string]any{
		"compiled": &fmtVariantHot{Kind: "c", Object: c},
		"go":       &fmtVariantCold{Kind: "c", Object: c},
	} {
		if got, err := vjson.Marshal(v); err != nil || string(got) != want {
			t.Errorf("%s unfold: Marshal = %s (err %v), want %s", name, got, err, want)
		}
	}
}

func TestFormat_UnsupportedFormatPointsToDocs(t *testing.T) {
	_, err := vjson.Marshal(&fmtBadInt{})
	if err == nil || !strings.Contains(err.Error(), "https://github.com/velox-io/json#format") {
		t.Fatalf("got %v, want a pointer to the format documentation", err)
	}
}

// Under `,string`, a quoted null through a pointer clears the pointer, as
// encoding/json does, rather than allocating or keeping the pointee.
type fmtQuotedNullPtr struct {
	F *float64 `json:"f,string,format:nonfinite"`
}

func TestFormat_QuotedNullClearsPointer(t *testing.T) {
	for _, start := range []*float64{nil, ptrTo(3.0)} {
		v := fmtQuotedNullPtr{F: start}
		if err := vjson.Unmarshal([]byte(`{"f":"null"}`), &v); err != nil || v.F != nil {
			t.Fatalf("Unmarshal(\"null\") from %v: F = %v (err %v), want nil", start, v.F, err)
		}
	}
}

// `,string` leaves a type that marshals itself alone, as encoding/json does,
// on the compiled path and on the Go unfold path of an inline variant case.
type fmtStringMarshaler int

func (fmtStringMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"m"`), nil }

type fmtStringCase struct {
	M fmtStringMarshaler  `json:"m,string"`
	P *fmtStringMarshaler `json:"p,string"`
}

type fmtStringCold struct {
	Pad    [70000]byte `json:"-"`
	Kind   string      `json:"kind"`
	Object any         `json:",embed" vjson:"variant=kind"`
}

func init() {
	vjson.DefineVariantCases[fmtStringCold, struct {
		_ fmtStringCase `case:"c"`
	}]()
}

func TestFormat_StringTagLeavesMarshalers(t *testing.T) {
	m := fmtStringMarshaler(2)
	c := fmtStringCase{M: 1, P: &m}
	if got, err := vjson.Marshal(&c); err != nil || string(got) != `{"m":"m","p":"m"}` {
		t.Fatalf("Marshal = %s (err %v), want %s", got, err, `{"m":"m","p":"m"}`)
	}
	if !encvm.Available {
		t.Skip("the interpreter cannot yet unfold an inline case that holds a Go-encoded field")
	}
	want := `{"kind":"c","m":"m","p":"m"}`
	if got, err := vjson.Marshal(&fmtStringCold{Kind: "c", Object: c}); err != nil || string(got) != want {
		t.Fatalf("unfold: Marshal = %s (err %v), want %s", got, err, want)
	}
}
