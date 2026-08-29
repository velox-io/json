package venc

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/value"
)

func domUnmarshal(src string, out any) error {
	return bind.Unmarshal([]byte(src), out)
}

func parseValue(t *testing.T, src string) value.Value {
	t.Helper()
	v, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse(%s): %v", src, err)
	}
	return v
}

// TestValueFieldNativeGolden pins the native tape-walk output for a Value
// field through the full VM path (compact and indent).
func TestValueFieldNativeGolden(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		A int         `json:"a"`
		D value.Value `json:"d"`
		B string      `json:"b"`
	}
	v := parseValue(t, `{"nested":[1,2,{"k":"v","esc":"a\"b"}],"n":null,"f":false}`)

	got, err := Marshal(S{A: 1, D: v, B: "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1,"d":{"nested":[1,2,{"k":"v","esc":"a\"b"}],"n":null,"f":false},"b":"x"}`
	if string(got) != want {
		t.Errorf("compact: got %s, want %s", got, want)
	}

	got, err = MarshalIndent(S{A: 1, D: v, B: "x"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = "{\n  \"a\": 1,\n  \"d\": {\n    \"nested\": [\n      1,\n      2,\n      {\n        \"k\": \"v\",\n        \"esc\": \"a\\\"b\"\n      }\n    ],\n    \"n\": null,\n    \"f\": false\n  },\n  \"b\": \"x\"\n}"
	if string(got) != want {
		t.Errorf("indent:\ngot  %q\nwant %q", got, want)
	}
}

// TestValueInAnyNative covers a boxed value.Value reached through an
// interface{} field: the pre-warmed cache entry routes OP_INTERFACE into
// the walk.
func TestValueInAnyNative(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		D any `json:"d"`
	}
	v := parseValue(t, `{"k":[1,"two",true,null]}`)
	got, err := Marshal(S{D: v})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"d":{"k":[1,"two",true,null]}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestValuePositionsNative covers keyless positions: slice element, map
// value, pointer target.
func TestValuePositionsNative(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	v1 := parseValue(t, `[1,2]`)
	v2 := parseValue(t, `"s"`)

	got, err := Marshal([]value.Value{v1, v2})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[[1,2],"s"]` {
		t.Errorf("slice: got %s", got)
	}

	mv := parseValue(t, `3`)
	got, err = Marshal(map[string]value.Value{"k": mv})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"k":3}` {
		t.Errorf("map: got %s", got)
	}

	got, err = Marshal(&v1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,2]` {
		t.Errorf("pointer: got %s", got)
	}
}

// TestValueFieldBufFullSweep forces window-full exits inside the tape walk
// at every small buffer size and pins the final output: the frame-resume
// protocol must neither duplicate nor drop elements.
func TestValueFieldBufFullSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		D value.Value `json:"d"`
	}
	src := `{"users":[{"id":1,"name":"a\"b"},{"id":2,"name":"plain"},{"deep":[[1,[2,[3]]]]}],"total":3,"ok":true,"note":"tail"}`
	v := parseValue(t, src)
	want, err := Marshal(S{D: v})
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != `{"d":`+src+`}` {
		t.Fatalf("baseline: got %s", want)
	}
	for n := 8; n <= len(want)+16; n++ {
		got, err := Marshal(S{D: v}, WithBufSize(n))
		if err != nil {
			t.Fatalf("bufsize %d: %v", n, err)
		}
		if string(got) != string(want) {
			t.Fatalf("bufsize %d: got %s, want %s", n, got, want)
		}
	}
}

// TestValueIndentBufFullSweep repeats the sweep in indent mode, where the
// walk must restore indent depth across window-full exits.
func TestValueIndentBufFullSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		D value.Value `json:"d"`
	}
	v := parseValue(t, `{"a":[1,{"b":"c"}],"d":{}}`)
	want, err := MarshalIndent(S{D: v}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for n := 8; n <= len(want)+16; n++ {
		got, err := MarshalIndent(S{D: v}, "", "  ", WithBufSize(n))
		if err != nil {
			t.Fatalf("bufsize %d: %v", n, err)
		}
		if string(got) != string(want) {
			t.Fatalf("bufsize %d:\ngot  %q\nwant %q", n, got, want)
		}
	}
}

// TestValueDeepNestingFallback pins that values nested deeper than the
// walk's native bounds fall back to the Go walk with identical bytes.
func TestValueDeepNestingFallback(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	deep := strings.Repeat("[", 20) + "1" + strings.Repeat("]", 20)
	v := parseValue(t, deep)
	type S struct {
		D value.Value `json:"d"`
	}
	got, err := Marshal(S{D: v})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"d":`+deep+`}` {
		t.Errorf("got %s", got)
	}

	// Deep enough to exceed even Go's recursion comfort is not this test's
	// concern; 20 levels exercises the fallback path only.
	shallow := strings.Repeat("[", 3) + "1" + strings.Repeat("]", 3)
	v2 := parseValue(t, shallow)
	got, err = Marshal(S{D: v2})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"d":`+shallow+`}` {
		t.Errorf("shallow: got %s", got)
	}
}

// TestValueEscapeModeNative pins that the walk never applies optional
// escape modes, while a sibling string field honors them.
func TestValueEscapeModeNative(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		D value.Value `json:"d"`
		E string      `json:"e"`
	}
	v := parseValue(t, `{"h":"<b>&"}`)
	got, err := Marshal(S{D: v, E: "<b>&"}, WithStdCompat())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"d":{"h":"<b>&"},"e":"\u003cb\u003e\u0026"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestValueNativeVsGoWalk parity: the native walk and the Go walk (the
// interp implementation) must agree byte for byte over a broad corpus.
func TestValueNativeVsGoWalk(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	corpus := []string{
		`null`, `true`, `0`, `-42`, `3.5`, `""`, `"plain"`, `"esc\"\\\"\n"`,
		`[]`, `{}`, `[1,[2,[3,{"k":"v"}]]]`,
		`{"a":1,"b":[true,false,null],"c":{"d":{"e":"f"}}}`,
		`{"num":123456789012345678901234567890}`,
		`{"u":18446744073709551615,"i":-9223372036854775808}`,
	}
	type S struct {
		D value.Value `json:"d"`
	}
	for _, src := range corpus {
		v := parseValue(t, src)
		native, err := Marshal(S{D: v})
		if err != nil {
			t.Fatalf("Marshal(%s): %v", src, err)
		}
		es := acquireEncodeState()
		if err := es.appendTapeValue(&v); err != nil {
			t.Fatalf("go walk(%s): %v", src, err)
		}
		if string(native) != `{"d":`+string(es.buf)+`}` {
			t.Errorf("native %s != go %s for %s", native, es.buf, src)
		}
		releaseEncodeState(es)
	}
}

// TestValueSpreadBufFullSweep forces window-full exits inside a spread at
// every small buffer size: the first-member latch protocol must survive
// resume without duplicating members or dropping the comma state.
func TestValueSpreadBufFullSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		Head int         `json:"head"`
		Rest value.Value `json:",embed"`
		Tail string      `json:"tail"`
	}
	input := `{"head":1,"zzz":{"k":[2,3]},"qqq":"deep enough text","tail":"t"}`
	var s S
	if err := domUnmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	want, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for n := 8; n <= len(want)+16; n++ {
		got, err := Marshal(s, WithBufSize(n))
		if err != nil {
			t.Fatalf("bufsize %d: %v", n, err)
		}
		if string(got) != string(want) {
			t.Fatalf("bufsize %d: got %s, want %s", n, got, want)
		}
	}
}

// A spread nested deeper than the native bounds falls back to Go with
// identical bytes.
func TestValueSpreadDeepFallback(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type S struct {
		Rest value.Value `json:",embed"`
	}
	deep := strings.Repeat("[", 20) + "1" + strings.Repeat("]", 20)
	input := `{"d":` + deep + `}`
	var s S
	if err := domUnmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"d":`+deep+`}` {
		t.Errorf("got %s", got)
	}
}
