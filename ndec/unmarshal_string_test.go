package ndec

import (
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
)

type stringBox struct {
	S string `json:"s"`
}

type stringTwoEsc struct {
	A string `json:"a"`
	B string `json:"b"`
}

type stringEscapedKey struct {
	V string `json:"withakey"` // exercises escaped object-key lookup
}

func TestStringEscape(t *testing.T) {
	cases := []string{
		`{"s":"a\nb"}`,
		`{"s":"\""}`,
		`{"s":"\\"}`,
		`{"s":"a\tb"}`,
		`{"s":"x\/y"}`,
		`{"s":"\b\f\r"}`,
		`{"s":"prefix\nmiddle\ttail"}`,
		`{"s":"é"}`, // é (2-byte UTF-8)
		`{"s":"中文"}`,
		`{"s":"😀"}`, // 😀 (surrogate pair → 4-byte UTF-8)
		`{"s":"mixed\nplus中"}`,
		`{"s":"AB"}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want stringBox
			if err := Unmarshal([]byte(in), &got); err != nil {
				t.Fatalf("ndec.Unmarshal: %v", err)
			}
			if err := json.Unmarshal([]byte(in), &want); err != nil {
				t.Fatalf("encoding/json.Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("parity drift:\n ndec   = %q\n stdlib = %q", got.S, want.S)
			}
		})
	}
}

// Scratch is pre-sized for the whole input, so decoding one escaped field must
// not invalidate a previously written string alias.
func TestStringTwoEscapeFields(t *testing.T) {
	in := `{"a":"first\\value","b":"second\nvalue"}`
	var got, want stringTwoEsc
	runParity(t, in, &got, &want)
}

// Keep one case that exercises the plain-string and escaped-string paths in the same object.
func TestStringEscapeAndPlain(t *testing.T) {
	type Mixed struct {
		Plain string `json:"plain"`
		Esc   string `json:"esc"`
	}
	in := `{"plain":"verbatim","esc":"line1\nline2"}`
	var got, want Mixed
	runParity(t, in, &got, &want)
}

func TestStringUnicode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"s":"A"}`, "A"},
		{"{\"s\":\"\\u00ff\"}", "ÿ"},
		{"{\"s\":\"\\u4e2d\\u6587\"}", "中文"},
		{"{\"s\":\"\\u00e9\"}", "é"},
		{"{\"s\":\"\\u00E9\"}", "é"},
		{"{\"s\":\"\\u0000\"}", "\x00"},
		{"{\"s\":\"\\u0007\"}", "\x07"},
		{"{\"s\":\"\\u001F\"}", "\x1F"},
		{"{\"s\":\"\\uD83D\\uDE00\"}", "\U0001F600"},
		{"{\"s\":\"\\uD83C\\uDF89\"}", "\U0001F389"},
		{"{\"s\":\"\\uD83D\"}", "�"},
		{"{\"s\":\"\\uDC00\"}", "�"},
		{"{\"s\":\"\\\"\"}", `"`},
		{"{\"s\":\"\\\\\"}", `\`},
		{"{\"s\":\"\\/\"}", `/`},
		{"{\"s\":\"\\b\\f\\n\\r\\t\"}", "\b\f\n\r\t"},
		{"{\"s\":\"a\\nb\\u00ff\"}", "a\nbÿ"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			var got, want stringBox
			runParity(t, c.input, &got, &want)
			if got.S != c.want {
				t.Fatalf("ndec %q got %q, want %q", c.input, got.S, c.want)
			}
		})
	}
}

func TestStringEscapedKeyLookup(t *testing.T) {
	// The escaped key must normalize to the struct tag before lookup happens.
	input := "{\"with\\u0061key\":\"hello\"}"
	var got stringEscapedKey
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("ndec.Unmarshal(%q): %v", input, err)
	}
	if got.V != "hello" {
		t.Fatalf("ndec %q got V=%q, want %q", input, got.V, "hello")
	}
}

// Decoding an escaped key must not advance scratch state in a way that breaks
// the escaped value decoded immediately after it.
func TestStringEscapedKeyThenValue(t *testing.T) {
	input := "{\"with\\u0061key\":\"a\\nb\\u4e2d\"}"
	var got stringEscapedKey
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("ndec.Unmarshal(%q): %v", input, err)
	}
	want := "a\nb中"
	if got.V != want {
		t.Fatalf("ndec got V=%q, want %q", got.V, want)
	}
}

// Direct string writes may alias the input buffer, so the backing bytes must
// stay alive across GC.
func TestStringAliasingGC(t *testing.T) {
	// Use a heap-backed payload so a stale alias would show up after GC.
	big := make([]byte, 0, 64)
	big = append(big, '{')
	big = append(big, '"', 'b', '"', ':', '"')
	for range 32 {
		big = append(big, 'x')
	}
	big = append(big, '"', '}')

	var got structFlat
	if err := Unmarshal(big, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	runtime.GC()
	runtime.GC()

	if len(got.B) != 32 {
		t.Fatalf("len(B) = %d, want 32", len(got.B))
	}
	for i := range 32 {
		if got.B[i] != 'x' {
			t.Fatalf("B[%d] = %c, want x; B = %q", i, got.B[i], got.B)
		}
	}
}
