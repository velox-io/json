package benchmark

import (
	"bytes"
	jsontext "encoding/json/jsontext"
	"testing"

	vjson "github.com/velox-io/json"
)

// Indent benchmarks compare vjson.Indent against the jsonv2 equivalent:
// jsontext.AppendFormat with PreserveRawStrings, WithIndentPrefix and
// WithIndent. PreserveRawStrings is required for std Indent semantics:
// strings pass through verbatim and numbers stay raw. Default AppendFormat
// options would rewrite strings to their shortest encoding, which
// vjson.Indent never does.
//
// Inputs are the compact corpus documents: indenting whitespace-free JSON
// is the canonical pretty-print direction.

// =============================================================================
// Parity: both paths must produce byte-identical indented output
// =============================================================================

func Test_Indent_Parity_JSONv2(t *testing.T) {
	datasets := []struct {
		name string
		src  []byte
	}{
		{"KubePods", LoadPodsCompactJSON()},
		{"EscapeHeavy", LoadEscapeHeavyCompactJSON()},
	}
	for _, ds := range datasets {
		var want bytes.Buffer
		if err := vjson.Indent(&want, ds.src, "", "  "); err != nil {
			t.Fatalf("%s: vjson: %v", ds.name, err)
		}
		got, err := jsontext.AppendFormat(nil, ds.src,
			jsontext.PreserveRawStrings(true),
			jsontext.WithIndentPrefix(""),
			jsontext.WithIndent("  "),
		)
		if err != nil {
			t.Fatalf("%s: jsontext: %v", ds.name, err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("%s: output mismatch:\n vjson   %d bytes\n jsontext %d bytes", ds.name, want.Len(), len(got))
		}
	}
}

// =============================================================================
// KubePods
// =============================================================================

func Benchmark_Indent_KubePods_Velox(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		if err := vjson.Indent(&buf, data, "", "  "); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Indent_KubePods_JSONv2(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	var out []byte
	for b.Loop() {
		var err error
		out, err = jsontext.AppendFormat(out[:0], data,
			jsontext.PreserveRawStrings(true),
			jsontext.WithIndentPrefix(""),
			jsontext.WithIndent("  "),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy
// =============================================================================

func Benchmark_Indent_EscapeHeavy_Velox(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		if err := vjson.Indent(&buf, data, "", "  "); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Indent_EscapeHeavy_JSONv2(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	var out []byte
	for b.Loop() {
		var err error
		out, err = jsontext.AppendFormat(out[:0], data,
			jsontext.PreserveRawStrings(true),
			jsontext.WithIndentPrefix(""),
			jsontext.WithIndent("  "),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}
