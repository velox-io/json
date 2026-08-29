package venc

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
)

// tapeWalkParse parses src into a Value through the dom path; the test corpus
// stays inside what every producer can build.
func tapeWalkParse(t *testing.T, src string) value.Value {
	t.Helper()
	v, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse(%s): %v", src, err)
	}
	return v
}

// TestTapeWalkValueParity walks the corpus through the direct es.buf walk and
// compares against value.Value.MarshalJSON (the serializer the walk replaces).
// Corpus doubles avoid the exponent range where the encoder's float policy
// deliberately differs from strconv's 'g' format.
func TestTapeWalkValueParity(t *testing.T) {
	corpus := []string{
		`null`, `true`, `false`, `0`, `-1`, `42`, `3.5`, `-0.25`,
		`""`, `"s"`, `"a\"b"`, `"back\\slash"`, `"tab\tnewline\n"`,
		`"ctrlhex\u0001\u001f"`, `"emoji 😀 snow ☃"`, `"quote\"end"`,
		`[]`, `[1]`, `[1,2,3]`, `["a","b"]`, `[null,true,false]`,
		`[[1],[2,[3]]]`, `[{"k":1},[2]]`,
		`{}`, `{"a":1}`, `{"a":1,"b":"c"}`, `{"nested":{"deep":[1,{"x":null}]}}`,
		`{"empty_arr":[],"empty_obj":{}}`,
		`{"n":123456789012345678901234567890}`,
		`{"u":18446744073709551615}`,
		`{"neg":-9223372036854775808}`,
		`{"f":0.1}`,
	}
	for _, src := range corpus {
		v := tapeWalkParse(t, src)
		es := acquireEncodeState()
		if err := es.appendTapeValue(&v); err != nil {
			t.Fatalf("appendTapeValue(%s): %v", src, err)
		}
		want, err := v.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(%s): %v", src, err)
		}
		if string(es.buf) != string(want) {
			t.Errorf("walk(%s) = %s, want %s", src, es.buf, want)
		}
		releaseEncodeState(es)
	}
}

// TestTapeWalkValueIndent pins the indented form, including the empty
// container shapes (no inner newlines).
func TestTapeWalkValueIndent(t *testing.T) {
	v := tapeWalkParse(t, `{"a":[1,{"b":true}],"e":{},"z":[]}`)
	es := acquireEncodeState()
	es.indentString = "  "
	es.indentDepth = 0
	if err := es.appendTapeValue(&v); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": [\n    1,\n    {\n      \"b\": true\n    }\n  ],\n  \"e\": {},\n  \"z\": []\n}"
	if string(es.buf) != want {
		t.Errorf("indent walk:\ngot  %q\nwant %q", es.buf, want)
	}
	releaseEncodeState(es)
}

// TestTapeWalkSpread covers member-mode emission: no braces, comma state
// driven by the caller's latch, empty leaves the latch untouched.
func TestTapeWalkSpread(t *testing.T) {
	cases := []struct {
		src    string
		first0 bool
		want   string
	}{
		{`{"a":1}`, false, `,"a":1`},
		{`{"a":1}`, true, `"a":1`},
		{`{"a":1,"b":[2]}`, false, `,"a":1,"b":[2]`},
		{`{}`, false, ``},
		{`{}`, true, ``},
	}
	for _, c := range cases {
		v := tapeWalkParse(t, c.src)
		es := acquireEncodeState()
		first := c.first0
		if err := es.appendTapeSpread(&v, &first); err != nil {
			t.Fatalf("appendTapeSpread(%s): %v", c.src, err)
		}
		if string(es.buf) != c.want {
			t.Errorf("spread(%s, first=%v) = %q, want %q", c.src, c.first0, es.buf, c.want)
		}
		releaseEncodeState(es)
	}
}

// A zero Value spreads nothing and reports no error.
func TestTapeWalkSpreadZero(t *testing.T) {
	var v value.Value
	es := acquireEncodeState()
	first := true
	if err := es.appendTapeSpread(&v, &first); err != nil {
		t.Fatal(err)
	}
	if len(es.buf) != 0 || !first {
		t.Errorf("zero spread wrote %q, first=%v", es.buf, first)
	}
	releaseEncodeState(es)
}

// A non-object Value cannot spread; the error is deterministic.
func TestTapeWalkSpreadNonObject(t *testing.T) {
	v := tapeWalkParse(t, `[1]`)
	es := acquireEncodeState()
	first := true
	if err := es.appendTapeSpread(&v, &first); err == nil {
		t.Fatalf("spread of array: want error, wrote %q", es.buf)
	} else if !strings.Contains(err.Error(), "non-object") {
		t.Errorf("error = %v, want non-object mention", err)
	}
	releaseEncodeState(es)
}

// The walk must not apply optional escape modes: a Value holds pre-parsed
// JSON and its MarshalJSON output is never re-escaped, while sibling string
// fields honor the encoder mode.
func TestTapeWalkEscapeModeIndependent(t *testing.T) {
	v := tapeWalkParse(t, `{"h":"<b>&amp;</b>"}`)
	es := acquireEncodeState()
	es.flags = uint32(escapeStdCompat)
	if err := es.appendTapeValue(&v); err != nil {
		t.Fatal(err)
	}
	if string(es.buf) != `{"h":"<b>&amp;</b>"}` {
		t.Errorf("HTML-escape leaked into tape walk: %s", es.buf)
	}
	releaseEncodeState(es)
}
