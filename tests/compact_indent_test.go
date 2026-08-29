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

func TestFmtCorpusRoundTrip(t *testing.T) {
	gz, err := os.ReadFile(filepath.Join("benchmark", "corpus", "testdata", "canada_geometry.json.gz"))
	if err != nil {
		t.Skipf("corpus unavailable: %v", err)
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
