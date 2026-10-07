package tests

import (
	"encoding/json"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// Test types for ,string tag

type StringTagInt struct {
	Count int `json:"count,string"`
}

type StringTagInt64 struct {
	Count int64 `json:"count,string"`
}

type StringTagUint struct {
	Count uint `json:"count,string"`
}

type StringTagBool struct {
	Flag bool `json:"flag,string"`
}

type StringTagFloat64 struct {
	Rate float64 `json:"rate,string"`
}

type StringTagFloat32 struct {
	Rate float32 `json:"rate,string"`
}

type StringTagString struct {
	Name string `json:"name,string"`
}

type StringTagPtr struct {
	Ptr *int `json:"ptr,string"`
}

type StringTagAll struct {
	Count  int     `json:"count,string"`
	Flag   bool    `json:"flag,string"`
	Rate   float64 `json:"rate,string"`
	Name   string  `json:"name,string"`
	Big    int64   `json:"big,string"`
	Small  uint8   `json:"small,string"`
	Normal int     `json:"normal"`
}

// ,string on slice/struct/map should be silently ignored.
type StringTagIgnored struct {
	Items []int          `json:"items,string"` //nolint:staticcheck // intentionally invalid
	Inner StringTagInt   `json:"inner,string"` //nolint:staticcheck // intentionally invalid
	Map   map[string]int `json:"map,string"`   //nolint:staticcheck // intentionally invalid
}

// Marshal tests

func TestMarshal_StringTag_Int(t *testing.T) {
	v := StringTagInt{Count: 123}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":"123"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMarshal_StringTag_NegativeInt(t *testing.T) {
	v := StringTagInt{Count: -42}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":"-42"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMarshal_StringTag_Bool(t *testing.T) {
	v := StringTagBool{Flag: true}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"flag":"true"}` {
		t.Fatalf("got %s", got)
	}

	v.Flag = false
	got, err = vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"flag":"false"}` {
		t.Fatalf("got %s", got)
	}
}

func TestMarshal_StringTag_Float(t *testing.T) {
	v64 := StringTagFloat64{Rate: 1.5}
	got, err := vjson.Marshal(v64)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"rate":"1.5"}` {
		t.Fatalf("got %s", got)
	}

	v32 := StringTagFloat32{Rate: 2.25}
	got, err = vjson.Marshal(v32)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"rate":"2.25"}` {
		t.Fatalf("got %s", got)
	}
}

func TestMarshal_StringTag_String(t *testing.T) {
	v := StringTagString{Name: "hello"}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json double-encodes: Go "hello" → JSON "\"hello\""
	want := `{"name":"\"hello\""}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMarshal_StringTag_Pointer(t *testing.T) {
	n := 42
	v := StringTagPtr{Ptr: &n}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ptr":"42"}` {
		t.Fatalf("got %s", got)
	}

	// nil pointer → null
	v2 := StringTagPtr{}
	got, err = vjson.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ptr":null}` {
		t.Fatalf("got %s", got)
	}
}

// Unmarshal tests

func TestUnmarshal_StringTag_Int(t *testing.T) {
	var v StringTagInt
	if err := vjson.Unmarshal([]byte(`{"count":"123"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Count != 123 {
		t.Fatalf("got %d, want 123", v.Count)
	}
}

func TestUnmarshal_StringTag_NegativeInt(t *testing.T) {
	var v StringTagInt
	if err := vjson.Unmarshal([]byte(`{"count":"-42"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Count != -42 {
		t.Fatalf("got %d, want -42", v.Count)
	}
}

func TestUnmarshal_StringTag_Int64(t *testing.T) {
	var v StringTagInt64
	if err := vjson.Unmarshal([]byte(`{"count":"9007199254740993"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Count != 9007199254740993 {
		t.Fatalf("got %d, want 9007199254740993", v.Count)
	}
}

func TestUnmarshal_StringTag_Uint(t *testing.T) {
	var v StringTagUint
	if err := vjson.Unmarshal([]byte(`{"count":"456"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Count != 456 {
		t.Fatalf("got %d, want 456", v.Count)
	}
}

func TestUnmarshal_StringTag_Bool(t *testing.T) {
	var v StringTagBool
	if err := vjson.Unmarshal([]byte(`{"flag":"true"}`), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Flag {
		t.Fatal("got false, want true")
	}

	if err := vjson.Unmarshal([]byte(`{"flag":"false"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Flag {
		t.Fatal("got true, want false")
	}
}

func TestUnmarshal_StringTag_Float(t *testing.T) {
	var v64 StringTagFloat64
	if err := vjson.Unmarshal([]byte(`{"rate":"1.5"}`), &v64); err != nil {
		t.Fatal(err)
	}
	if v64.Rate != 1.5 {
		t.Fatalf("got %f, want 1.5", v64.Rate)
	}

	var v32 StringTagFloat32
	if err := vjson.Unmarshal([]byte(`{"rate":"2.25"}`), &v32); err != nil {
		t.Fatal(err)
	}
	if v32.Rate != 2.25 {
		t.Fatalf("got %f, want 2.25", v32.Rate)
	}
}

func TestUnmarshal_StringTag_String(t *testing.T) {
	var v StringTagString
	if err := vjson.Unmarshal([]byte(`{"name":"\"hello\""}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello" {
		t.Fatalf("got %q, want %q", v.Name, "hello")
	}
}

func TestUnmarshal_StringTag_Pointer(t *testing.T) {
	var v StringTagPtr
	if err := vjson.Unmarshal([]byte(`{"ptr":"42"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Ptr == nil || *v.Ptr != 42 {
		t.Fatalf("got %v, want *42", v.Ptr)
	}

	var v2 StringTagPtr
	if err := vjson.Unmarshal([]byte(`{"ptr":null}`), &v2); err != nil {
		t.Fatal(err)
	}
	if v2.Ptr != nil {
		t.Fatalf("got %v, want nil", v2.Ptr)
	}
}

func TestUnmarshal_StringTag_Null(t *testing.T) {
	v := StringTagInt{Count: 999}
	if err := vjson.Unmarshal([]byte(`{"count":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Count != 999 {
		t.Fatalf("got %d, want 999 (null should leave value unchanged)", v.Count)
	}

	vb := StringTagBool{Flag: true}
	if err := vjson.Unmarshal([]byte(`{"flag":null}`), &vb); err != nil {
		t.Fatal(err)
	}
	if !vb.Flag {
		t.Fatal("got false, want true (null should leave value unchanged)")
	}
}

// Roundtrip tests

func TestRoundtrip_StringTag(t *testing.T) {
	original := StringTagAll{
		Count: 42, Flag: true, Rate: 3.14, Name: "world",
		Big: 9007199254740993, Small: 255, Normal: 100,
	}

	data, err := vjson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded StringTagAll
	if err := vjson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v\nJSON: %s", err, data)
	}
	if decoded != original {
		t.Fatalf("roundtrip mismatch:\noriginal: %+v\ndecoded:  %+v\nJSON: %s", original, decoded, data)
	}
}

func TestRoundtrip_StringTag_ZeroValues(t *testing.T) {
	original := StringTagAll{}
	data, err := vjson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded StringTagAll
	if err := vjson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v\nJSON: %s", err, data)
	}
	if decoded != original {
		t.Fatalf("roundtrip mismatch:\noriginal: %+v\ndecoded:  %+v", original, decoded)
	}
}

func TestRoundtrip_StringTag_Pointer(t *testing.T) {
	n := 42
	original := StringTagPtr{Ptr: &n}
	data, err := vjson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded StringTagPtr
	if err := vjson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v\nJSON: %s", err, data)
	}
	if decoded.Ptr == nil || *decoded.Ptr != *original.Ptr {
		t.Fatalf("roundtrip mismatch: got %v, want %v", decoded.Ptr, original.Ptr)
	}
}

// Stdlib compatibility

func TestStringTag_StdlibCompat_Marshal(t *testing.T) {
	cases := []struct {
		name string
		v    any // value (for encoding/json) and pointer (for vjson)
	}{
		{"Int", &StringTagInt{42}},
		{"NegInt", &StringTagInt{-99}},
		{"BoolTrue", &StringTagBool{true}},
		{"BoolFalse", &StringTagBool{false}},
		{"Float64", &StringTagFloat64{3.14}},
		{"String", &StringTagString{"hello"}},
		{"StringQuotes", &StringTagString{`he"llo`}},
		{"EmptyString", &StringTagString{""}},
		{"All", &StringTagAll{42, true, 1.5, "test", 1234567890, 7, 99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdOut, _ := json.Marshal(tc.v)
			vjsonOut, vjErr := marshalVjsonAny(tc.v)
			if vjErr != nil {
				t.Fatal(vjErr)
			}
			if string(vjsonOut) != string(stdOut) {
				t.Fatalf("mismatch:\nvjson:  %s\nstdlib: %s", vjsonOut, stdOut)
			}
		})
	}
}

// marshalVjsonAny dispatches to the correct generic Marshal[T].
func marshalVjsonAny(v any) ([]byte, error) {
	switch vt := v.(type) {
	case *StringTagInt:
		return vjson.Marshal(vt)
	case *StringTagBool:
		return vjson.Marshal(vt)
	case *StringTagFloat64:
		return vjson.Marshal(vt)
	case *StringTagString:
		return vjson.Marshal(vt)
	case *StringTagAll:
		return vjson.Marshal(vt)
	default:
		return nil, nil
	}
}

func TestStringTag_StdlibCompat_Unmarshal(t *testing.T) {
	tests := []string{
		`{"count":"42"}`,
		`{"count":"-99"}`,
		`{"count":"0"}`,
	}
	for _, input := range tests {
		var vjsonV StringTagInt
		var stdV StringTagInt
		vjsonErr := vjson.Unmarshal([]byte(input), &vjsonV)
		stdErr := json.Unmarshal([]byte(input), &stdV)
		if (vjsonErr == nil) != (stdErr == nil) {
			t.Fatalf("input %s: vjson err=%v, stdlib err=%v", input, vjsonErr, stdErr)
		}
		if vjsonV != stdV {
			t.Fatalf("input %s: vjson=%+v, stdlib=%+v", input, vjsonV, stdV)
		}
	}
}

// TestStringTag_StdlibCompat_Edges pins the surprising stdlib edges: a bare
// number or bool still binds through `,string`, only the quoted payload must
// parse as the target type, and a string destination demands the double
// encoding `,string` itself produces.
func TestStringTag_StdlibCompat_Edges(t *testing.T) {
	type i64 struct {
		V int64 `json:"v,string"`
	}
	type f64 struct {
		V float64 `json:"v,string"`
	}
	type bl struct {
		V bool `json:"v,string"`
	}
	type str struct {
		V string `json:"v,string"`
	}
	cases := []struct {
		input string
		mk    func() any
	}{
		{`{"v":123}`, func() any { return new(i64) }}, // bare number binds
		{`{"v":"123"}`, func() any { return new(i64) }},
		{`{"v":"1.5"}`, func() any { return new(i64) }}, // payload must parse as int
		{`{"v":"abc"}`, func() any { return new(i64) }},
		{`{"v":"99999999999999999999"}`, func() any { return new(i64) }},
		{`{"v":true}`, func() any { return new(i64) }},
		{`{"v":"1e21"}`, func() any { return new(f64) }},
		{`{"v":1.5}`, func() any { return new(f64) }}, // bare float binds
		{`{"v":"NaN"}`, func() any { return new(f64) }},
		{`{"v":true}`, func() any { return new(bl) }}, // bare bool binds
		{`{"v":"true"}`, func() any { return new(bl) }},
		{`{"v":"yes"}`, func() any { return new(bl) }},
		{`{"v":123}`, func() any { return new(str) }},  // bare number is not double encoded
		{`{"v":"hi"}`, func() any { return new(str) }}, // one layer is not enough
		{`{"v":"\"hi\""}`, func() any { return new(str) }},
	}
	for _, tc := range cases {
		// A quoted NaN binds through strconv.ParseFloat, which encoding/json
		// accepts only since go1.27, so vjson's acceptance is pinned directly
		// and parity is asserted only when the running std accepts it.
		if tc.input == `{"v":"NaN"}` && !stdQuotedNaN() {
			vjV := tc.mk()
			if err := vjson.Unmarshal([]byte(tc.input), vjV); err != nil {
				t.Errorf("input %s: vjson rejected a quoted NaN: %v", tc.input, err)
			}
			continue
		}
		stdV, vjV := tc.mk(), tc.mk()
		stdErr := json.Unmarshal([]byte(tc.input), stdV)
		vjErr := vjson.Unmarshal([]byte(tc.input), vjV)
		if (stdErr == nil) != (vjErr == nil) {
			t.Errorf("input %s: error divergence: std=%v vjson=%v", tc.input, stdErr, vjErr)
			continue
		}
		if stdErr == nil {
			stdStr, vjStr := dbgAny(stdV), dbgAny(vjV)
			if stdStr != vjStr {
				t.Errorf("input %s: value divergence:\n  std:   %s\n  vjson: %s", tc.input, stdStr, vjStr)
			}
		}
	}
}

// stdQuotedNaN reports whether the running encoding/json accepts a quoted
// NaN under ,string, which arrived in go1.27.
func stdQuotedNaN() bool {
	var d struct {
		V float64 `json:"v,string"`
	}
	return json.Unmarshal([]byte(`{"v":"NaN"}`), &d) == nil
}

func TestStringTag_StdlibCompat_Pointer(t *testing.T) {
	n := 42
	v := StringTagPtr{Ptr: &n}
	vjsonOut, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	stdOut, _ := json.Marshal(v)
	if string(vjsonOut) != string(stdOut) {
		t.Fatalf("mismatch:\nvjson:  %s\nstdlib: %s", vjsonOut, stdOut)
	}

	// nil pointer
	v2 := StringTagPtr{}
	vjsonOut, err = vjson.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	stdOut, _ = json.Marshal(v2)
	if string(vjsonOut) != string(stdOut) {
		t.Fatalf("nil ptr mismatch:\nvjson:  %s\nstdlib: %s", vjsonOut, stdOut)
	}
}

// Ignored for non-quotable types

func TestStringTag_IgnoredForComplexTypes(t *testing.T) {
	v := StringTagIgnored{
		Items: []int{1, 2, 3},
		Inner: StringTagInt{Count: 5},
		Map:   map[string]int{"a": 1},
	}

	vjsonOut, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	stdOut, _ := json.Marshal(v)
	if string(vjsonOut) != string(stdOut) {
		t.Fatalf("mismatch:\nvjson:  %s\nstdlib: %s", vjsonOut, stdOut)
	}
}

// A `,string` field that cannot take its value is a field-site mismatch:
// encoding/json reports the type error and still binds every other member,
// before and after it. Each value below is checked through both the
// contiguous and the streaming driver.
func TestStringTag_MismatchCompletesObject(t *testing.T) {
	type host struct {
		By []byte
		M  map[string]int
		Q  int     `json:",string"`
		QB bool    `json:",string"`
		QF float64 `json:",string"`
		QS string  `json:",string"`
		R  int
		T  []string
	}
	vals := []string{`"x"`, `""`, `"1.5"`, `"99999999999999999999"`, `7`, `true`, `[1,2]`, `{"a":1}`, `"\"a\""`, `"yes"`}
	for _, field := range []string{"Q", "QB", "QF", "QS"} {
		for _, v := range vals {
			in := `{"By":"aGk=","M":{"k":1},` + `"` + field + `":` + v + `,"R":1,"T":["t"]}`
			var std host
			stdErr := json.Unmarshal([]byte(in), &std)
			if stdErr == nil {
				continue
			}
			if _, ok := stdErr.(*json.UnmarshalTypeError); !ok {
				continue
			}
			want, _ := json.Marshal(std)
			var got host
			err := vjson.Unmarshal([]byte(in), &got)
			if err == nil {
				t.Errorf("%s: accepted, want %v", in, stdErr)
				continue
			}
			if js, _ := json.Marshal(got); string(js) != string(want) {
				t.Errorf("%s:\n  std %s\n  vj  %s", in, want, js)
			}
			var dec host
			if err := vjson.NewDecoder(strings.NewReader(in)).Decode(&dec); err == nil {
				t.Errorf("%s: Decoder accepted", in)
			} else if js, _ := json.Marshal(dec); string(js) != string(want) {
				t.Errorf("%s: Decoder\n  std %s\n  vj  %s", in, want, js)
			}
		}
	}
}
