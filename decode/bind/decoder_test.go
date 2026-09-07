package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/value"
)

// decAll decodes every value from data through a Decoder with the given
// chunk size, returning the values and the first non-EOF error.
func decAll[T any](t *testing.T, data []byte, chunk int, opts ...DecoderOption) ([]T, error) {
	t.Helper()
	d := NewDecoder(&chunkReader{data: data, chunk: chunk}, opts...)
	var out []T
	for {
		var v T
		err := d.Decode(&v)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
}

// splitValues slices a stream of concatenated values using encoding/json so
// the oracle and the decoder see identical value boundaries.
func splitValues(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var out [][]byte
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				return out
			}
			t.Fatalf("splitting stream %q: %v", data, err)
		}
		out = append(out, []byte(raw))
	}
}

// Both values sit in one window: the root close completes without judging
// the remainder, and the next Decode reads it as its value.
func TestDecoderInWindowMultiValue(t *testing.T) {
	for _, stream := range []string{
		`{"a":1,"b":"x","c":0.5}{"a":2,"b":"y","c":1.5}`,
		`{"a":1,"b":"x","c":0.5} {"a":2,"b":"y","c":1.5}`,
		"{\"a\":1,\"b\":\"x\",\"c\":0.5}\n{\"a\":2,\"b\":\"y\",\"c\":1.5}",
		`{}{}`,
	} {
		got, err := decAll[feedInner](t, []byte(stream), 4096)
		if err != nil {
			t.Fatalf("stream %q: %v", stream, err)
		}
		if len(got) != 2 {
			t.Fatalf("stream %q: got %d values, want 2", stream, len(got))
		}
		for i, want := range []feedInner{{A: 1, B: "x", C: 0.5}, {A: 2, B: "y", C: 1.5}} {
			if strings.HasPrefix(stream, "{}") {
				want = feedInner{}
			}
			if got[i] != want {
				t.Fatalf("stream %q value %d: got %+v want %+v", stream, i, got[i], want)
			}
		}
	}
	// Scalar streams: every scalar start is a structural, so each value
	// completes with the next value's first token already in the window.
	for _, tc := range []struct {
		stream string
		want   []any
	}{
		{`1 2 3`, []any{float64(1), float64(2), float64(3)}},
		{`true false null`, []any{true, false, nil}},
		{`"x" "y"`, []any{"x", "y"}},
	} {
		got, err := decAll[any](t, []byte(tc.stream), 4096)
		if err != nil {
			t.Fatalf("stream %q: %v", tc.stream, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("stream %q: got %#v want %#v", tc.stream, got, tc.want)
		}
	}
	// Containers of scalars back to back.
	gotSlice, err := decAll[[]int](t, []byte(`[1,2][3]`), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSlice, [][]int{{1, 2}, {3}}) {
		t.Fatalf("got %v", gotSlice)
	}
}

func TestDecoderConsecutiveValues(t *testing.T) {
	stream := `{"i":1,"f":2.5,"s":"first","q":[1,2,3]}
{"i":2}
{"nested":[{"a":1,"b":"z","c":0.5}],"ss":["日本語","\"quoted\""]}
{"unknown":{"deep":[1,{"x":"y"},2.5,null]}}`
	data := []byte(stream)
	wantVals := splitValues(t, data)
	for _, chunk := range feedChunkSizes {
		got, err := decAll[feedDoc](t, data, chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(got) != len(wantVals) {
			t.Fatalf("chunk=%d: got %d values, want %d", chunk, len(got), len(wantVals))
		}
		for i, raw := range wantVals {
			want, wantErr := feedUnmarshal[feedDoc](t, raw)
			if wantErr != nil {
				t.Fatalf("oracle value %d: %v", i, wantErr)
			}
			if !reflect.DeepEqual(got[i], want) {
				t.Fatalf("chunk=%d value %d: got %+v want %+v", chunk, i, got[i], want)
			}
		}
	}
}

// Each stream leads with a valid value so the failing value decodes second
// and its error offset must be absolute (prefix + contiguous offset). For
// trailing-data docs the Decoder surfaces the error one value later, at the
// trailing value itself, with the same absolute position.
func TestDecoderErrorParity(t *testing.T) {
	prefix := `{"i":1}` + "\n"
	for _, doc := range feedInvalidDocs {
		data := []byte(prefix + doc)
		_, oracleErr := feedUnmarshal[feedDoc](t, []byte(doc))
		if oracleErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, oracleErr)
		for _, chunk := range feedChunkSizes {
			d := NewDecoder(&chunkReader{data: data, chunk: chunk})
			var first feedDoc
			if err := d.Decode(&first); err != nil {
				t.Fatalf("chunk=%d doc=%q: first value: %v", chunk, doc, err)
			}
			var second feedDoc
			err := d.Decode(&second)
			if err == nil {
				// Trailing-data docs: the trailing bytes are the next value.
				err = d.Decode(&second)
			}
			if err == nil || err == io.EOF {
				t.Fatalf("chunk=%d doc=%q: no error surfaced", chunk, doc)
			}
			kind, off := feedErrKind(t, err)
			// Trailing data decodes as the next value, so its error kind and
			// position follow that value's parse instead of the contiguous
			// trailing check.
			if strings.Contains(oracleErr.Error(), "trailing data") {
				continue
			}
			if kind != wantKind {
				t.Fatalf("chunk=%d doc=%q: error kind %q, contiguous %q (%v vs %v)", chunk, doc, kind, wantKind, err, oracleErr)
			}
			if wantKind == "syntax" && wantOff == 0 && off == 0 {
				continue // scan-level errors carry no position
			}
			if off != wantOff+int64(len(prefix)) {
				t.Fatalf("chunk=%d doc=%q: error offset %d, want %d (prefix %d + contiguous %d)",
					chunk, doc, off, wantOff+int64(len(prefix)), len(prefix), wantOff)
			}
		}
	}
}

func TestDecoderMore(t *testing.T) {
	t.Run("reads on demand", func(t *testing.T) {
		for _, chunk := range []int{1, 3, 4096} {
			d := NewDecoder(&chunkReader{data: []byte(`1 2 3`), chunk: chunk})
			for i := 0; i < 3; i++ {
				if !d.More() {
					t.Fatalf("chunk=%d: More=false before value %d", chunk, i)
				}
				var v int
				if err := d.Decode(&v); err != nil {
					t.Fatalf("chunk=%d: %v", chunk, err)
				}
				if v != i+1 {
					t.Fatalf("chunk=%d: got %d want %d", chunk, v, i+1)
				}
			}
			if d.More() {
				t.Fatalf("chunk=%d: More=true after last value", chunk)
			}
		}
	})
	t.Run("empty and whitespace only", func(t *testing.T) {
		for _, data := range []string{"", " ", "\n\t\r\n  "} {
			d := NewDecoder(&chunkReader{data: []byte(data), chunk: 1})
			if d.More() {
				t.Fatalf("data %q: More=true", data)
			}
			var v feedDoc
			if err := d.Decode(&v); err != io.EOF {
				t.Fatalf("data %q: Decode=%v want io.EOF", data, err)
			}
		}
	})
	t.Run("false after sticky error", func(t *testing.T) {
		d := NewDecoder(&chunkReader{data: []byte(`{"a":1}\n{`), chunk: 4096})
		var v feedInner
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		if err := d.Decode(&v); err == nil {
			t.Fatal("second decode unexpectedly succeeded")
		}
		if d.More() {
			t.Fatal("More=true after sticky error")
		}
	})
	t.Run("reader error ends stream quietly", func(t *testing.T) {
		d := NewDecoder(errReader{})
		if d.More() {
			t.Fatal("More=true on reader error")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("boom")
}

type zeroReader struct{ n int }

func (r *zeroReader) Read([]byte) (int, error) {
	r.n++
	if r.n > feedMaxZeroReads {
		return 0, errors.New("not making progress is the reader's job")
	}
	return 0, nil
}

func TestDecoderBuffered(t *testing.T) {
	data := []byte(`{"a":1,"b":"x","c":0.5}
{"a":2,"b":"y","c":1.5}`)
	d := NewDecoder(&chunkReader{data: data, chunk: 4096})
	var first feedInner
	if err := d.Decode(&first); err != nil {
		t.Fatal(err)
	}
	br := d.Buffered()
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	// The value-boundary relocation cuts at the next value's first
	// structural, discarding the whitespace between values.
	if string(got) != `{"a":2,"b":"y","c":1.5}` {
		t.Fatalf("Buffered=%q", got)
	}
	// The bytes.Reader must survive the next Decode's window relocation.
	var second feedInner
	if err := d.Decode(&second); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(br)
	if string(rest) != "" {
		t.Fatalf("buffered reader changed after Decode: %q", rest)
	}
	if second.A != 2 {
		t.Fatalf("second value: %+v", second)
	}
}

func TestDecoderUseNumber(t *testing.T) {
	stream := `1.5 2 3.5`
	d := NewDecoder(&chunkReader{data: []byte(stream), chunk: 1})
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if f, ok := v.(float64); !ok || f != 1.5 {
		t.Fatalf("default: got %#v want float64(1.5)", v)
	}
	d.UseNumber()
	for _, want := range []string{"2", "3.5"} {
		v = nil
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		n, ok := v.(json.Number)
		if !ok || n.String() != want {
			t.Fatalf("UseNumber: got %#v want json.Number(%q)", v, want)
		}
	}
}

func TestDecoderSkipErrors(t *testing.T) {
	skipAll := func(err error) bool { return true }
	t.Run("basic", func(t *testing.T) {
		data := []byte("{\"a\":1}\n{\"a\":\"str\"}\n{\"a\":3}")
		for _, chunk := range []int{1, 7, 4096} {
			d := NewDecoder(&chunkReader{data: data, chunk: chunk}, WithSkipErrors(skipAll))
			var v feedInner
			if err := d.Decode(&v); err != nil || v.A != 1 {
				t.Fatalf("chunk=%d: first=%v %+v", chunk, err, v)
			}
			v = feedInner{}
			bad := d.Decode(&v)
			if bad == nil {
				t.Fatalf("chunk=%d: bad line succeeded", chunk)
			}
			if d.Err() != nil {
				t.Fatalf("chunk=%d: skipped error became sticky: %v", chunk, d.Err())
			}
			v = feedInner{}
			if err := d.Decode(&v); err != nil || v.A != 3 {
				t.Fatalf("chunk=%d: third=%v %+v", chunk, err, v)
			}
		}
	})
	t.Run("all bad", func(t *testing.T) {
		data := []byte("{\n{\"a\":\n[")
		d := NewDecoder(&chunkReader{data: data, chunk: 1}, WithSkipErrors(skipAll))
		for i := 0; i < 3; i++ {
			var v feedInner
			if err := d.Decode(&v); err == nil {
				t.Fatalf("line %d unexpectedly succeeded", i)
			}
		}
		var v feedInner
		if err := d.Decode(&v); err != io.EOF {
			t.Fatalf("after all bad lines: %v want io.EOF", err)
		}
	})
	t.Run("selective", func(t *testing.T) {
		// The third line is truncated, so it fails with a syntax error the
		// predicate does not skip and it sticks. (A lone '[' would be a type
		// error into feedInner, which the predicate skips to EOF.)
		data := []byte("{\"a\":1}\n{\"a\":\"str\"}\n{")
		d := NewDecoder(&chunkReader{data: data, chunk: 4096},
			WithSkipErrors(func(err error) bool {
				var te *UnmarshalTypeError
				return errors.As(err, &te)
			}))
		var v feedInner
		if err := d.Decode(&v); err != nil || v.A != 1 {
			t.Fatalf("first=%v %+v", err, v)
		}
		v = feedInner{}
		if err := d.Decode(&v); err == nil { // type error skipped
			t.Fatal("type error line succeeded")
		}
		v = feedInner{}
		err := d.Decode(&v) // syntax error sticks
		if err == nil {
			t.Fatal("syntax error line succeeded")
		}
		if d.Err() != err {
			t.Fatalf("sticky=%v want %v", d.Err(), err)
		}
	})
	t.Run("no trailing newline", func(t *testing.T) {
		data := []byte("{\"a\":1}\n{\"a\":\"str\"}")
		d := NewDecoder(&chunkReader{data: data, chunk: 1}, WithSkipErrors(skipAll))
		var v feedInner
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		v = feedInner{}
		if err := d.Decode(&v); err == nil {
			t.Fatal("bad line succeeded")
		}
		if d.Err() != nil {
			t.Fatalf("sticky after skip: %v", d.Err())
		}
		v = feedInner{}
		if err := d.Decode(&v); err != io.EOF {
			t.Fatalf("skip to EOF: %v want io.EOF", err)
		}
	})
	t.Run("nil predicate keeps type errors non-sticky", func(t *testing.T) {
		// A type mismatch consumed the failed value, so like encoding/json the
		// stream continues and the next Decode reports io.EOF; only syntax
		// errors stick without a predicate.
		data := []byte("{\"a\":\"str\"}")
		d := NewDecoder(&chunkReader{data: data, chunk: 4096})
		var v feedInner
		first := d.Decode(&v)
		if first == nil {
			t.Fatal("bad line succeeded")
		}
		second := d.Decode(&v)
		if second != io.EOF {
			t.Fatalf("after type error: %v want io.EOF", second)
		}
	})
}

func TestDecoderStickyErrors(t *testing.T) {
	t.Run("syntax", func(t *testing.T) {
		d := NewDecoder(&chunkReader{data: []byte(`{"a":1}\n{`), chunk: 4096})
		var v feedInner
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		first := d.Decode(&v)
		if first == nil {
			t.Fatal("second decode succeeded")
		}
		if second := d.Decode(&v); second != first {
			t.Fatalf("subsequent=%v want same %v", second, first)
		}
	})
	t.Run("reader error", func(t *testing.T) {
		d := NewDecoder(errReader{})
		var v feedInner
		first := d.Decode(&v)
		if first == nil || first.Error() != "boom" {
			t.Fatalf("first=%v", first)
		}
		if second := d.Decode(&v); second != first {
			t.Fatalf("subsequent=%v want same %v", second, first)
		}
	})
	t.Run("zero progress", func(t *testing.T) {
		d := NewDecoder(&zeroReader{})
		var v feedInner
		if err := d.Decode(&v); err == nil {
			t.Fatal("zero-progress reader succeeded")
		}
		if d.Err() == nil {
			t.Fatal("zero-progress error not sticky")
		}
	})
}

func TestDecoderEOF(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		d := NewDecoder(&chunkReader{data: nil, chunk: 1})
		var v feedDoc
		for i := 0; i < 3; i++ {
			if err := d.Decode(&v); err != io.EOF {
				t.Fatalf("decode %d: %v want io.EOF", i, err)
			}
		}
	})
	t.Run("trailing whitespace", func(t *testing.T) {
		d := NewDecoder(&chunkReader{data: []byte(`{"a":1,"b":"x","c":0.5}` + "\n\n  "), chunk: 1})
		var v feedInner
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		if err := d.Decode(&v); err != io.EOF {
			t.Fatalf("after value: %v want io.EOF", err)
		}
	})
}

func TestDecoderTruncated(t *testing.T) {
	t.Run("first value", func(t *testing.T) {
		for _, chunk := range []int{1, 4096} {
			_, err := decAll[feedInner](t, []byte(`{"a":1`), chunk)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("chunk=%d: %v want io.ErrUnexpectedEOF chain", chunk, err)
			}
		}
	})
	t.Run("second value after good first", func(t *testing.T) {
		for _, chunk := range []int{1, 4096} {
			got, err := decAll[feedInner](t, []byte("{\"a\":1}\n{\"a\":"), chunk)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("chunk=%d: %v want io.ErrUnexpectedEOF chain", chunk, err)
			}
			if len(got) != 1 || got[0].A != 1 {
				t.Fatalf("chunk=%d: first value lost: %+v", chunk, got)
			}
		}
	})
	t.Run("withheld tail after good value", func(t *testing.T) {
		// The single-value feed path rejects this in checkTrailing; the
		// Decoder surfaces it when the next Decode tries the tail.
		got, err := decAll[feedInner](t, []byte(`{"a":1} "unclosed`), 4096)
		if err == nil {
			t.Fatalf("unclosed tail accepted: %+v", got)
		}
		if len(got) != 1 || got[0].A != 1 {
			t.Fatalf("first value lost: %+v", got)
		}
	})
}

func TestDecoderOptions(t *testing.T) {
	t.Run("buffer size one", func(t *testing.T) {
		got, err := decAll[feedInner](t, []byte(`{"a":1,"b":"x","c":0.5} {"a":2,"b":"y","c":1.5}`), 64, WithBufferSize(1))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].A != 1 || got[1].A != 2 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("expected size", func(t *testing.T) {
		for _, size := range []int{1, 8, 1 << 20} {
			got, err := decAll[feedInner](t, []byte(`{"a":7} {"a":8}`), 4096, WithExpectedSize(size))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].A != 7 || got[1].A != 8 {
				t.Fatalf("size=%d: got %+v", size, got)
			}
		}
	})
	t.Run("large value small window", func(t *testing.T) {
		big := strings.Repeat("x", 100_000)
		stream := `{"b":"` + big + `"} {"a":1,"b":"y","c":0.5}`
		got, err := decAll[feedInner](t, []byte(stream), 4096)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || len(got[0].B) != len(big) || got[1].A != 1 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestDecoderMixedTypes(t *testing.T) {
	data := []byte(`{"a":1,"b":"x","c":0.5} 7 {"a":2,"b":"y","c":1.5}`)
	d := NewDecoder(&chunkReader{data: data, chunk: 1})
	var s feedInner
	if err := d.Decode(&s); err != nil || s.A != 1 {
		t.Fatalf("struct 1: %v %+v", err, s)
	}
	var n int
	if err := d.Decode(&n); err != nil || n != 7 {
		t.Fatalf("int: %v %d", err, n)
	}
	s = feedInner{}
	if err := d.Decode(&s); err != nil || s.A != 2 {
		t.Fatalf("struct 2: %v %+v", err, s)
	}
	var last int
	if err := d.Decode(&last); err != io.EOF {
		t.Fatalf("end: %v want io.EOF", err)
	}
}

func TestDecoderAbsoluteErrorOffset(t *testing.T) {
	prefix := `{"a":1,"b":"x","c":0.5}` + "\n"
	data := []byte(prefix + `{"a":"str"}`)
	_, oracleErr := feedUnmarshal[feedInner](t, []byte(`{"a":"str"}`))
	var oracleType *UnmarshalTypeError
	if !errors.As(oracleErr, &oracleType) {
		t.Fatalf("oracle: %v want UnmarshalTypeError", oracleErr)
	}
	for _, chunk := range feedChunkSizes {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk})
		var v feedInner
		if err := d.Decode(&v); err != nil {
			t.Fatalf("chunk=%d: first value: %v", chunk, err)
		}
		v = feedInner{}
		err := d.Decode(&v)
		var te *UnmarshalTypeError
		if !errors.As(err, &te) {
			t.Fatalf("chunk=%d: %v want UnmarshalTypeError", chunk, err)
		}
		if want := oracleType.Offset + int64(len(prefix)); te.Offset != want {
			t.Fatalf("chunk=%d: offset %d want %d", chunk, te.Offset, want)
		}
	}
}

func TestDecoderValueRoots(t *testing.T) {
	stream := `{"a":1,"b":[1,2,{"c":"x"}]} [1,2,3] "text" 3.5 null`
	for _, chunk := range feedChunkSizes {
		d := NewDecoder(&chunkReader{data: []byte(stream), chunk: chunk})
		var vals []value.Value
		for {
			var v value.Value
			err := d.Decode(&v)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("chunk=%d: %v", chunk, err)
			}
			vals = append(vals, v)
		}
		if len(vals) != 5 {
			t.Fatalf("chunk=%d: got %d values", chunk, len(vals))
		}
		// Earlier docs stay readable after later values commit their arenas.
		runtime.GC()
		for i, raw := range splitValues(t, []byte(stream)) {
			want, wantErr := feedUnmarshal[value.Value](t, raw)
			if wantErr != nil {
				t.Fatalf("oracle value %d: %v", i, wantErr)
			}
			if got := vals[i].String(); got != want.String() {
				t.Fatalf("chunk=%d value %d: got %s want %s", chunk, i, got, want.String())
			}
		}
	}
}

func TestDecoderPolyRoots(t *testing.T) {
	stream := `{"name":"bob","greet":"hi","big":[1,2,3],"any":{"k":1}}` + "\n" +
		`{"name":"bob","greet":"second"}`
	for _, chunk := range feedChunkSizes {
		got, err := decAll[feedVariantHost](t, []byte(stream), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(got) != 2 {
			t.Fatalf("chunk=%d: got %d values", chunk, len(got))
		}
		for i := range got {
			c, ok := got[i].Data.(feedVariantCase)
			if !ok {
				t.Fatalf("chunk=%d value %d: variant=%T", chunk, i, got[i].Data)
			}
			if i == 0 && (len(c.Big) != 3 || c.Big[2] != 3) {
				t.Fatalf("chunk=%d value %d: case=%+v", chunk, i, c)
			}
		}
	}
}

// TestDecoderPolyOnlyRoots drives a poly-only tree: the machine's ValueDoc
// stays NULL across values because the merged tape is scratch for the case
// walker.
func TestDecoderPolyOnlyRoots(t *testing.T) {
	stream := `{"name":"bob","data":{"n":7}}` + "\n" +
		`{"name":"bob","data":{"n":8}}`
	for _, chunk := range feedChunkSizes {
		got, err := decAll[feedVarFieldHost](t, []byte(stream), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(got) != 2 {
			t.Fatalf("chunk=%d: got %d values", chunk, len(got))
		}
		for i := range got {
			c, ok := got[i].Data.(feedVarFieldCase)
			if !ok {
				t.Fatalf("chunk=%d value %d: variant=%T", chunk, i, got[i].Data)
			}
			if c.N != 7+i {
				t.Fatalf("chunk=%d value %d: case=%+v", chunk, i, c)
			}
		}
	}
}

func TestDecoderDeferredRoots(t *testing.T) {
	stream := `{"a":1} 5 "text" [1,2] null`
	for _, chunk := range feedChunkSizes {
		got, err := decAll[json.RawMessage](t, []byte(stream), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		want := splitValues(t, []byte(stream))
		if len(got) != len(want) {
			t.Fatalf("chunk=%d: got %d values want %d", chunk, len(got), len(want))
		}
		runtime.GC()
		for i := range want {
			if string(got[i]) != string(want[i]) {
				t.Fatalf("chunk=%d value %d: got %q want %q", chunk, i, got[i], want[i])
			}
		}
	}
}

func TestDecoderInvalidUnmarshal(t *testing.T) {
	d := NewDecoder(&chunkReader{data: []byte(`{"a":1}`), chunk: 4096})
	if err := d.Decode(feedInner{}); err == nil {
		t.Fatal("non-pointer accepted")
	}
	var ie *jerr.InvalidUnmarshalError
	if err := d.Decode(123); !errors.As(err, &ie) {
		t.Fatalf("got %v want InvalidUnmarshalError", err)
	}
	var p *feedInner
	if err := d.Decode(p); !errors.As(err, &ie) {
		t.Fatalf("nil pointer: %v want InvalidUnmarshalError", err)
	}
	if d.Err() != nil {
		t.Fatalf("invalid unmarshal became sticky: %v", d.Err())
	}
}

func TestDecoderBufferedChunked(t *testing.T) {
	data := []byte(`{"a":1,"b":"x","c":0.5}` + "\n" + `{"a":2,"b":"y","c":1.5}`)
	for _, chunk := range []int{1, 7, 31, 64} {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk})
		var first feedInner
		if err := d.Decode(&first); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		br := d.Buffered()
		got, err := io.ReadAll(br)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		// Whatever the window holds when the value completes is the
		// unconsumed remainder; the exact prefix depends on chunking, so
		// assert the reader is a prefix of the remaining input.
		rest := data[len(`{"a":1,"b":"x","c":0.5}`)+1:]
		if !strings.HasPrefix(string(rest), string(got)) && len(got) > 0 {
			t.Fatalf("chunk=%d: Buffered=%q not a prefix of %q", chunk, got, rest)
		}
		// The copy must survive the next Decode's window relocation.
		var second feedInner
		if err := d.Decode(&second); err != nil {
			t.Fatalf("chunk=%d: second: %v", chunk, err)
		}
		if second.A != 2 {
			t.Fatalf("chunk=%d: second=%+v", chunk, second)
		}
	}
}

func TestDecoderWhitespaceHeavy(t *testing.T) {
	// A whitespace-only window is discarded whole, so leading whitespace
	// between values never grows the window.
	ws := strings.Repeat(" \t\n\r", 25_000)
	data := []byte(`{"a":1,"b":"x","c":0.5}` + ws + `{"a":2,"b":"y","c":1.5}`)
	for _, chunk := range []int{1, 4096} {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk})
		var v feedInner
		if err := d.Decode(&v); err != nil || v.A != 1 {
			t.Fatalf("chunk=%d: first=%v %+v", chunk, err, v)
		}
		if !d.More() {
			t.Fatalf("chunk=%d: More=false with a value pending", chunk)
		}
		v = feedInner{}
		if err := d.Decode(&v); err != nil || v.A != 2 {
			t.Fatalf("chunk=%d: second=%v %+v", chunk, err, v)
		}
		if d.More() {
			t.Fatalf("chunk=%d: More=true after last value", chunk)
		}
	}
}

func TestDecoderMapRoots(t *testing.T) {
	stream := `{"a":1,"b":2}` + "\n" + `{"a":10,"b":20,"c":30}`
	for _, chunk := range feedChunkSizes {
		got, err := decAll[map[string]int](t, []byte(stream), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if !reflect.DeepEqual(got, []map[string]int{{"a": 1, "b": 2}, {"a": 10, "b": 20, "c": 30}}) {
			t.Fatalf("chunk=%d: got %v", chunk, got)
		}
	}
}

func TestDecoderUnmarshalerOnce(t *testing.T) {
	stream := `{"u":{"p":"one"},"i":1}` + "\n" + `{"u":{"p":"two"},"i":2}`
	for _, chunk := range feedChunkSizes {
		feedUnmCalls = 0
		got, err := decAll[feedRawDoc](t, []byte(stream), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if feedUnmCalls != 2 {
			t.Fatalf("chunk=%d: UnmarshalJSON called %d times, want 2", chunk, feedUnmCalls)
		}
		if got[0].U.Payload != `{"p":"one"}` || got[1].U.Payload != `{"p":"two"}` {
			t.Fatalf("chunk=%d: payloads %q %q", chunk, got[0].U.Payload, got[1].U.Payload)
		}
		if got[0].I != 1 || got[1].I != 2 {
			t.Fatalf("chunk=%d: ints %d %d", chunk, got[0].I, got[1].I)
		}
	}
}

func TestDecoderManyValuesGC(t *testing.T) {
	const n = 200
	var sb strings.Builder
	want := make([]feedInner, 0, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "{\"a\":%d,\"b\":\"v%d\",\"c\":%d.5}\n", i, i, i)
		want = append(want, feedInner{A: i, B: "v" + strconv.Itoa(i), C: float64(i) + 0.5})
	}
	for _, chunk := range []int{1, 13, 4096} {
		d := NewDecoder(&chunkReader{data: []byte(sb.String()), chunk: chunk})
		for i := 0; i < n; i++ {
			if i%20 == 0 {
				runtime.GC()
			}
			var v feedInner
			if err := d.Decode(&v); err != nil {
				t.Fatalf("chunk=%d value %d: %v", chunk, i, err)
			}
			if v != want[i] {
				t.Fatalf("chunk=%d value %d: got %+v want %+v", chunk, i, v, want[i])
			}
		}
		var v feedInner
		if err := d.Decode(&v); err != io.EOF {
			t.Fatalf("chunk=%d: end=%v want io.EOF", chunk, err)
		}
	}
}

func TestDecoderSkipErrorsProgress(t *testing.T) {
	// Blank lines between values: each skip starts at the window head, so a
	// leading newline takes one extra skip round, but the stream must always
	// progress to the good values.
	skipAll := func(err error) bool { return true }
	data := []byte("{\"a\":1}\n\n{\"a\":\"str\"}\n\n{\"a\":3}\n{\"a\":4}")
	for _, chunk := range []int{1, 5, 4096} {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk}, WithSkipErrors(skipAll))
		var got []int
		for {
			var v feedInner
			err := d.Decode(&v)
			if err == io.EOF {
				break
			}
			if err == nil {
				got = append(got, v.A)
			}
			// A failed decode either yields a value or returns a non-EOF
			// error; both advance the stream, and the loop terminates.
			if d.Err() != nil {
				t.Fatalf("chunk=%d: skipped error became sticky: %v", chunk, d.Err())
			}
		}
		if !reflect.DeepEqual(got, []int{1, 3, 4}) {
			t.Fatalf("chunk=%d: got %v want [1 3 4]", chunk, got)
		}
	}
}

func TestDecoderErrNilAfterEOF(t *testing.T) {
	d := NewDecoder(&chunkReader{data: []byte(`{"a":1,"b":"x","c":0.5}`), chunk: 4096})
	var v feedInner
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := d.Decode(&v); err != io.EOF {
			t.Fatalf("decode %d: %v want io.EOF", i, err)
		}
	}
	if d.Err() != nil {
		t.Fatalf("Err=%v after clean EOF, want nil", d.Err())
	}
}
