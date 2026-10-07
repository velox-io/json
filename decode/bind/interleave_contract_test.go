package bind

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/velox-io/json/internal/gdec"
)

// Findings pinned as tests. Each one compares against encoding/json or against
// the Decoder's own promise (skip a failing line, keep the stream usable).

// A malformed token at an element site must stay a syntax error even when
// the destination element type would also reject it. A mismatched container
// aborts the parse unread, so a malformed token inside it is out of scope.
func TestInterleaveElementSiteSyntaxClass(t *testing.T) {
	cases := []string{
		`{"X":[t,2,3]}`, `{"X":[,2]}`, `{"X":[1,,3]}`, `{"X":[1,2,]}`, `{"X":[:]}`,
		`{"X":[1,}]}`, `{"X":[e]}`, `{"X":[.]}`, `{"X":["\q"]}`, `{"X":[nul]}`,
	}
	for _, in := range cases {
		var std, vj struct{ X []int }
		stdErr := json.Unmarshal([]byte(in), &std)
		var se *json.SyntaxError
		if !errors.As(stdErr, &se) {
			t.Fatalf("%s: stdlib did not report a syntax error: %v", in, stdErr)
		}
		p, _ := NewParser[struct{ X []int }]()
		err := p.Unmarshal([]byte(in), &vj)
		var vse *SyntaxError
		if !errors.As(err, &vse) {
			t.Errorf("%s: want syntax error like encoding/json, got %s", in, ilDescribeErr(err))
		}
	}
}

// json.Number must reject a JSON string that is not a valid number, as
// encoding/json does, rather than store text that cannot be marshaled back.
func TestInterleaveNumberRejectsInvalidString(t *testing.T) {
	for _, in := range []string{`{"N":"x"}`, `{"N":""}`, `{"N":"1x"}`, `{"N":"--1"}`, `{"N":"0x10"}`, `{"N":" 1"}`} {
		var std, vj struct{ N json.Number }
		stdErr := json.Unmarshal([]byte(in), &std)
		if stdErr == nil {
			t.Fatalf("%s: stdlib accepted", in)
		}
		p, _ := NewParser[struct{ N json.Number }]()
		if err := p.Unmarshal([]byte(in), &vj); err == nil {
			t.Errorf("%s: accepted, stored json.Number(%q)", in, vj.N)
		}
	}
}

func ilDecodeSeq(data string, rd io.Reader, bufSize int) []string {
	opts := []DecoderOption{WithSkipErrors(func(error) bool { return true })}
	if bufSize > 0 {
		opts = append(opts, WithBufferSize(bufSize))
	}
	d := NewDecoder(rd, opts...)
	var out []string
	for i := 0; i < 32; i++ {
		var v feedInner
		err := d.Decode(&v)
		switch {
		case err == io.EOF:
			return append(out, "EOF")
		case err != nil:
			out = append(out, "ERR")
		default:
			out = append(out, "ok")
		}
	}
	return out
}

// Skipping a failing line must not swallow the failure when it is the last
// line of the stream: io.EOF means a clean end, so the caller would never
// learn that the final line was bad.
func TestInterleaveDecoderSkipReportsLastLineError(t *testing.T) {
	for _, in := range []string{
		"{\"a\":1}\n{\"a\":\"s\"}",
		"{\"a\":1}\n{\"a\":1,}",
		"{\"a\":1}\n7",
		"{\"a\":\"s\"}",
	} {
		for _, chunk := range []int{1, 4096} {
			got := ilDecodeSeq(in, &chunkReader{data: []byte(in), chunk: chunk}, 0)
			errs := 0
			for _, g := range got {
				if g == "ERR" {
					errs++
				}
			}
			if errs != 1 {
				t.Errorf("%q chunk=%d: want exactly one reported error, got %v", in, chunk, got)
			}
		}
	}
}

// A line that ends mid-value makes the Decoder read into the next line before
// the error shows. The skip must resume at the line after the FAILED line, so
// the following good lines survive, whatever the reader chunking and window.
func TestInterleaveDecoderSkipResyncIndependentOfWindow(t *testing.T) {
	in := "{\"a\":1}\n{\"a\":2,\n{\"a\":3}\n{\"a\":4}\n"
	var ref []string
	for _, chunk := range []int{0, 1, 3, 7, 16} {
		for _, bs := range []int{0, 8, 16, 64} {
			var rd io.Reader = bytes.NewReader([]byte(in))
			if chunk > 0 {
				rd = &chunkReader{data: []byte(in), chunk: chunk}
			}
			got := ilDecodeSeq(in, rd, bs)
			if ref == nil {
				ref = got
				continue
			}
			if strings.Join(got, " ") != strings.Join(ref, " ") {
				t.Errorf("chunk=%d buf=%d: %v differs from chunk=0 buf=0: %v", chunk, bs, got, ref)
			}
		}
	}
	// The stable outcome must also keep the good line after the failed one.
	if want := "ok ERR ok ok EOF"; strings.Join(ref, " ") != want {
		t.Errorf("resync outcome %v, want %s", ref, want)
	}
}

// One failing value must surface as one error: with a window alignment where
// the failing line is the last in the stream, the same value is reported twice.
func TestInterleaveDecoderSkipNoPhantomError(t *testing.T) {
	for _, bad := range []string{`7`, `"s"`, `[1]`, `{"a":"x"}`, `{"a":1,}`, `tru`} {
		for _, bs := range []int{8, 16, 33, 64, 100, 128} {
			for n := 0; n < 40; n++ {
				in := strings.Repeat("{\"a\":1,\"b\":\"xxxxxxxxxxxxxxxx\"}\n", n%7+1) + "{\"a\":2}\n" +
					strings.Repeat(" ", n) + bad + "\n"
				got := ilDecodeSeq(in, bytes.NewReader([]byte(in)), bs)
				errs := 0
				for _, g := range got {
					if g == "ERR" {
						errs++
					}
				}
				if errs != 1 {
					t.Fatalf("bad line %q buf=%d pad=%d: one failing line reported %d errors: %v", bad, bs, n, errs, got)
				}
			}
		}
	}
}

// After a field-site type mismatch the walk completes (encoding/json "completes
// the unmarshaling as best it can"). Maps that were fully parsed before and
// after the failing field must hold their entries; the native binder drains its
// staged map slots only on the success path, so they come back non-nil and
// empty while the pure-Go engine and encoding/json fill them.
func TestInterleaveSoftMismatchKeepsParsedMaps(t *testing.T) {
	type dst struct {
		A   int
		M   map[string]int
		MS  map[string]ilInner
		Any any
		AM  map[string]any
		B   map[string][]int
	}
	for _, in := range []string{
		`{"A":"x","M":{"a":1,"b":2},"MS":{"k":{"B":1}},"Any":{"z":1},"AM":{"p":[1]},"B":{"q":[1,2]}}`,
		`{"M":{"a":1,"b":2},"MS":{"k":{"B":1}},"Any":{"z":1},"AM":{"p":[1]},"B":{"q":[1,2]},"A":"x"}`,
		`{"M":{"a":1,"b":2},"A":"x","MS":{"k":{"B":1}},"Any":{"z":1},"AM":{"p":[1]},"B":{"q":[1,2]}}`,
	} {
		var std, vj dst
		if err := json.Unmarshal([]byte(in), &std); err == nil {
			t.Fatalf("stdlib accepted %s", in)
		}
		p, _ := NewParser[dst]()
		if err := p.Unmarshal([]byte(in), &vj); err == nil {
			t.Fatalf("vjson accepted %s", in)
		}
		want, _ := json.Marshal(std)
		got, _ := json.Marshal(vj)
		if string(want) != string(got) {
			t.Errorf("destination after field mismatch differs\n  in:  %s\n  std: %s\n  vj:  %s", in, want, got)
		}
	}
}

// A root-level scalar that mismatches the target consumes exactly its own value
// and leaves the stream usable. The outcome of the lines around it must not
// depend on where the reader's chunk boundaries fall.
func TestInterleaveDecoderRootMismatchChunkIndependent(t *testing.T) {
	scripts := [][]string{
		{"[1,2,3]", "12", "[4]"},
		{"[1,2,3]", "12", "[4]", "[5]"},
		{"[1,2,3]", `"s"`, "[4]"},
		{"[1,2,3]", "true", "[4]"},
		{"[1,2,3]", "{}", "[4]"},
		{"[1,2,3]", "null", "[4]"},
	}
	run := func(script []string, chunk int, skip bool) []string {
		data := []byte(strings.Join(script, "\n") + "\n")
		var rd io.Reader = bytes.NewReader(data)
		if chunk > 0 {
			rd = &chunkReader{data: data, chunk: chunk}
		}
		var opts []DecoderOption
		if skip {
			opts = append(opts, WithSkipErrors(func(error) bool { return true }))
		}
		d := NewDecoder(rd, opts...)
		var out []string
		for range script {
			var v []int
			if err := d.Decode(&v); err != nil {
				out = append(out, "ERR")
			} else {
				js, _ := json.Marshal(v)
				out = append(out, string(js))
			}
		}
		return out
	}
	for _, script := range scripts {
		for _, skip := range []bool{true, false} {
			want := run(script, 0, skip)
			for chunk := 1; chunk <= 40; chunk++ {
				if got := run(script, chunk, skip); strings.Join(got, " ") != strings.Join(want, " ") {
					t.Errorf("script %q skip=%v chunk=%d: got %v, whole-buffer read gives %v", script, skip, chunk, got, want)
					break
				}
			}
		}
	}
}

// Every syntax error names where the document breaks, through the
// contiguous and the streaming driver alike. The structural scan's verdict
// and a missing colon used to report offset 0 wherever the defect was.
func TestInterleaveSyntaxErrorNamesPosition(t *testing.T) {
	bad := 0
	for _, g := range ilPlainGood {
		for _, m := range ilMutants(g) {
			var se *json.SyntaxError
			if !errors.As(json.Unmarshal([]byte(m), new(ilPlain)), &se) {
				continue
			}
			p, _ := NewParser[ilPlain]()
			cerr := p.Unmarshal([]byte(m), new(ilPlain))
			derr := NewDecoder(&chunkReader{data: []byte(m), chunk: 7}).Decode(new(ilPlain))
			for _, e := range []struct {
				name string
				err  error
			}{{"Unmarshal", cerr}, {"Decoder", derr}} {
				var vse *SyntaxError
				if !errors.As(e.err, &vse) {
					continue
				}
				// The streaming driver stages a base64 body off the source, so
				// its decode failure has no document offset to name.
				if e.name == "Decoder" && errors.As(e.err, new(base64.CorruptInputError)) {
					continue
				}
				// Offset 0 is a position when the defect is the token there or
				// the byte right after it.
				first, _ := gdec.TokenEnd([]byte(m), 0)
				if vse.Offset == 0 && se.Offset > int64(first)+1 && bad < 8 {
					bad++
					t.Errorf("%s %q: offset 0 (%v), encoding/json stops at %d (%v)", e.name, m, vse, se.Offset, se)
				}
				if vse.Offset > int64(len(m)) && bad < 8 {
					bad++
					t.Errorf("%s %q: offset %d past the input", e.name, m, vse.Offset)
				}
			}
		}
	}
}
