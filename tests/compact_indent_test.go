package tests

import (
	"bytes"
	"compress/gzip"
	stdjson "encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

var fmtDocs = []string{
	`{"a": 1, "b": [1, 2, 3], "c": {"d": "x"}, "e": {}}`,
	`  [ 1.5e10 , -0.25 , true , false , null ]  `,
	`{"nested":{"deep":[[["ü\ud83d\ude00"]] ]}}`,
	`[[1,[2,3]],{"k":[null,true]}]`,
	`"top-level string"`,
	`123`,
	`{}`,
	`[]`,
}

func stdCompact(t *testing.T, src string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := stdjson.Compact(&buf, []byte(src)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func stdIndent(t *testing.T, src, prefix, indent string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := stdjson.Indent(&buf, []byte(src), prefix, indent); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func vCompact(t *testing.T, src string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := vjson.Compact(&buf, []byte(src)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func vIndent(t *testing.T, src, prefix, indent string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := vjson.Indent(&buf, []byte(src), prefix, indent); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestCompact(t *testing.T) {
	for _, d := range fmtDocs {
		if got, want := vCompact(t, d), stdCompact(t, d); got != want {
			t.Errorf("Compact(%q)\n got %q\nwant %q", d, got, want)
		}
	}
}

func TestCompactAppend(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("prefix:")
	if err := vjson.Compact(&buf, []byte(`{"a": 1}`)); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "prefix:"+stdCompact(t, `{"a": 1}`); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIndent(t *testing.T) {
	for _, d := range fmtDocs {
		for _, prefix := range []string{"", "PRE ", "\t"} {
			for _, indent := range []string{"  ", "\t", ""} {
				if got, want := vIndent(t, d, prefix, indent), stdIndent(t, d, prefix, indent); got != want {
					t.Errorf("Indent(%q,%q,%q)\n got %q\nwant %q", d, prefix, indent, got, want)
				}
			}
		}
	}
}

func TestFmtRoundTrip(t *testing.T) {
	for _, d := range fmtDocs {
		pretty := vIndent(t, d, "", "  ")
		if back := vCompact(t, pretty); back != stdCompact(t, d) {
			t.Errorf("round trip of %q gave %q", d, back)
		}
	}
}

func TestFmtDeepNestingRetry(t *testing.T) {
	// Pretty output grows quadratically with depth, forcing the
	// size-query retry path.
	deep := strings.Repeat("[", 200) + "1" + strings.Repeat("]", 200)
	if got, want := vIndent(t, deep, "", "  "), stdIndent(t, deep, "", "  "); got != want {
		t.Error("deep nesting mismatch")
	}
}

func TestFmtErrors(t *testing.T) {
	for _, bad := range []string{`{"a":`, `[1,2`, `tru`, `{}x`, ``, `{"a" 1}`} {
		var buf bytes.Buffer
		if err := vjson.Compact(&buf, []byte(bad)); err == nil {
			t.Errorf("Compact(%q) succeeded, want error", bad)
		} else if _, ok := err.(*vjson.SyntaxError); !ok {
			t.Errorf("Compact(%q): got %T, want *SyntaxError", bad, err)
		}
	}
}

// A document with a raw control byte is rejected but must not poison the
// pooled native formatter state for the next call: the SAX context
// reinitializes every scan-state field between calls.
func TestFmtControlResidue(t *testing.T) {
	var bad bytes.Buffer
	if err := vjson.Compact(&bad, []byte("[\"\x1f\"]")); err == nil {
		t.Fatal("Compact with a raw control byte: want error")
	}
	var ok bytes.Buffer
	if err := vjson.Compact(&ok, []byte(`{"a":1,"b":[1,2,3]}`)); err != nil {
		t.Fatalf("Compact on clean input after a rejected document: %v", err)
	}
	var ind bytes.Buffer
	if err := vjson.Indent(&ind, []byte(`{"a":1}`), "", "  "); err != nil {
		t.Fatalf("Indent on clean input after a rejected document: %v", err)
	}
}

// fmtTokenCases are documents whose structure is well formed but whose
// number or string tokens break the grammar, which the reformatter must
// reject token by token as encoding/json does.
func fmtTokenCases() []string {
	docs := []string{
		`01`, `-`, `1.`, `.5`, `1e`, `1e+`, `-01`, `1.2.3`, `[01]`, `{"a":-}`, `[1.e5]`,
		`"\q"`, `"\u00"`, `"\uZZZZ"`, `"\u12"`, `"\"`, `["\x"]`, `{"\q":1}`, `{"a\u0":1}`,
		"\"a\x01b\"", "[\"\x1f\"]", "{\"k\x00\":1}", "\"\t\"",
	}
	// A raw control byte or a bad escape at every offset of strings that
	// span word and scanner chunk boundaries.
	for _, n := range []int{7, 8, 9, 63, 64, 65, 130} {
		for _, at := range []int{0, n / 2, n - 1} {
			body := []byte(strings.Repeat("x", n))
			body[at] = 0x01
			docs = append(docs, `{"k":"`+string(body)+`"}`, `["`+string(body)+`"]`)
			body[at] = 'x'
			esc := string(body[:at]) + `\q` + string(body[at:])
			docs = append(docs, `{"k":"`+esc+`"}`, `{"`+esc+`":1}`)
		}
	}
	return docs
}

// Compact and Indent reject exactly what encoding/json rejects, as a
// *SyntaxError, and agree with Valid.
func TestFmtTokenGrammar(t *testing.T) {
	valid := []string{
		`0`, `-0`, `1.5e-3`, `1E+9`, `"\u00e9\ud83d\ude00\/\b\f\n\r\t\"\\"`, "\"\xff\"",
		`{"a\nb":"c\u0041"}`, `[` + strings.Repeat(`"\\"`+",", 20) + `0]`,
	}
	for _, doc := range append(valid, fmtTokenCases()...) {
		want := stdjson.Valid([]byte(doc))
		if got := vjson.Valid([]byte(doc)); got != want {
			t.Errorf("Valid(%q) = %v, encoding/json %v", doc, got, want)
		}
		for name, run := range map[string]func(*bytes.Buffer) error{
			"Compact": func(b *bytes.Buffer) error { return vjson.Compact(b, []byte(doc)) },
			"Indent":  func(b *bytes.Buffer) error { return vjson.Indent(b, []byte(doc), "", "  ") },
		} {
			var buf bytes.Buffer
			err := run(&buf)
			if (err == nil) != want {
				t.Errorf("%s(%q) error = %v, encoding/json valid = %v", name, doc, err, want)
				continue
			}
			if err != nil {
				if _, ok := err.(*vjson.SyntaxError); !ok {
					t.Errorf("%s(%q): got %T, want *SyntaxError", name, doc, err)
				}
			}
		}
	}
}

func TestFmtCorpusRoundTrip(t *testing.T) {
	gz, err := os.ReadFile(filepath.Join("..", "benchmark", "corpus", "testdata", "canada_geometry.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	src, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatal(err)
	}

	compact := vCompact(t, string(src))
	if want := stdCompact(t, string(src)); compact != want {
		t.Error("Compact(canada_geometry) mismatch")
	}
	pretty := vIndent(t, compact, "", "  ")
	if back := vCompact(t, pretty); back != compact {
		t.Error("round trip mismatch")
	}
}
