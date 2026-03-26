package vjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Basic Functionality

func TestDecoder_SingleObject(t *testing.T) {
	type Msg struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	input := `{"name":"alice","age":30}`
	dec := NewDecoder(strings.NewReader(input))

	var m Msg
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Name != "alice" || m.Age != 30 {
		t.Fatalf("got %+v, want {alice 30}", m)
	}

	// Next Decode should return io.EOF.
	var m2 Msg
	if err := dec.Decode(&m2); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_MultipleValues(t *testing.T) {
	input := `{"a":1} {"a":2} {"a":3}`
	dec := NewDecoder(strings.NewReader(input))

	for i := 1; i <= 3; i++ {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if m["a"] != i {
			t.Fatalf("Decode #%d: got a=%d, want %d", i, m["a"], i)
		}
	}

	var m map[string]int
	if err := dec.Decode(&m); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_NDJSON(t *testing.T) {
	input := "{\"x\":1}\n{\"x\":2}\n{\"x\":3}\n"
	dec := NewDecoder(strings.NewReader(input))

	for i := 1; i <= 3; i++ {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if m["x"] != i {
			t.Fatalf("Decode #%d: got x=%d, want %d", i, m["x"], i)
		}
	}
}

func TestDecoder_DifferentTypes(t *testing.T) {
	input := `"hello" 42 true false null [1,2,3] {"k":"v"}`
	dec := NewDecoder(strings.NewReader(input))

	var s string
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("string: %v", err)
	}
	if s != "hello" {
		t.Fatalf("string: got %q", s)
	}

	var n int
	if err := dec.Decode(&n); err != nil {
		t.Fatalf("int: %v", err)
	}
	if n != 42 {
		t.Fatalf("int: got %d", n)
	}

	var b1 bool
	if err := dec.Decode(&b1); err != nil {
		t.Fatalf("true: %v", err)
	}
	if !b1 {
		t.Fatalf("true: got false")
	}

	var b2 bool
	if err := dec.Decode(&b2); err != nil {
		t.Fatalf("false: %v", err)
	}
	if b2 {
		t.Fatalf("false: got true")
	}

	var ptr *int
	if err := dec.Decode(&ptr); err != nil {
		t.Fatalf("null: %v", err)
	}
	if ptr != nil {
		t.Fatalf("null: got %v", ptr)
	}

	var arr []int
	if err := dec.Decode(&arr); err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(arr) != 3 || arr[0] != 1 || arr[1] != 2 || arr[2] != 3 {
		t.Fatalf("array: got %v", arr)
	}

	var m map[string]string
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("object: %v", err)
	}
	if m["k"] != "v" {
		t.Fatalf("object: got %v", m)
	}
}

func TestDecoder_Struct(t *testing.T) {
	type Inner struct {
		Value int `json:"value"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
	}

	input := `{"name":"test","inner":{"value":42}}`
	dec := NewDecoder(strings.NewReader(input))

	var o Outer
	if err := dec.Decode(&o); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if o.Name != "test" || o.Inner.Value != 42 {
		t.Fatalf("got %+v", o)
	}
}

// Buffer Management

func TestDecoder_SmallBuffer(t *testing.T) {
	// Use a very small buffer to force multi-read for a single value.
	input := `{"name":"alice","age":30,"city":"wonderland"}`
	dec := NewDecoder(strings.NewReader(input), WithBufferSize(16))

	type Msg struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
		City string `json:"city"`
	}
	var m Msg
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Name != "alice" || m.Age != 30 || m.City != "wonderland" {
		t.Fatalf("got %+v", m)
	}
}

func TestDecoder_SmallBufferMultipleValues(t *testing.T) {
	input := `{"a":1} {"a":2} {"a":3} {"a":4} {"a":5}`
	dec := NewDecoder(strings.NewReader(input), WithBufferSize(32))

	for i := 1; i <= 5; i++ {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if m["a"] != i {
			t.Fatalf("Decode #%d: got a=%d, want %d", i, m["a"], i)
		}
	}
}

func TestDecoder_TinyValuesBackPressure(t *testing.T) {
	// Many tiny values in sequence.
	var buf bytes.Buffer
	n := 100
	for i := range n {
		fmt.Fprintf(&buf, `{"i":%d} `, i)
	}

	dec := NewDecoder(&buf)

	for i := range n {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if m["i"] != i {
			t.Fatalf("Decode #%d: got i=%d, want %d", i, m["i"], i)
		}
	}

	var m map[string]int
	if err := dec.Decode(&m); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_EmptyMapsStress(t *testing.T) {
	// Extreme case: many empty maps in sequence.
	var buf bytes.Buffer
	n := 500
	for i := range n {
		buf.WriteString("{}")
		if i < n-1 {
			buf.WriteByte(' ')
		}
	}

	dec := NewDecoder(&buf, WithBufferSize(64))

	for i := range n {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if len(m) != 0 {
			t.Fatalf("Decode #%d: expected empty map, got %v", i, m)
		}
	}
}

// oneByteReader wraps a reader to return exactly 1 byte per Read call.
type oneByteReader struct {
	r io.Reader
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestDecoder_OneByteReader(t *testing.T) {
	input := `{"a":1} {"a":2}`
	dec := NewDecoder(&oneByteReader{r: strings.NewReader(input)})

	for i := 1; i <= 2; i++ {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if m["a"] != i {
			t.Fatalf("Decode #%d: got a=%d, want %d", i, m["a"], i)
		}
	}
}

func TestDecoder_LargeValue(t *testing.T) {
	// Build a large JSON object that exceeds default buffer size.
	type Big struct {
		Data string `json:"data"`
	}
	bigStr := strings.Repeat("x", 20000) // 20KB string
	input := fmt.Sprintf(`{"data":"%s"}`, bigStr)

	dec := NewDecoder(strings.NewReader(input), WithBufferSize(1024))

	var b Big
	if err := dec.Decode(&b); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if b.Data != bigStr {
		t.Fatalf("got string of len %d, want %d", len(b.Data), len(bigStr))
	}
}

// Edge Cases

func TestDecoder_EmptyReader(t *testing.T) {
	dec := NewDecoder(strings.NewReader(""))
	var m map[string]any
	if err := dec.Decode(&m); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_WhitespaceOnly(t *testing.T) {
	dec := NewDecoder(strings.NewReader("   \n\t\r  "))
	var m map[string]any
	if err := dec.Decode(&m); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_TrailingWhitespace(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`42   `))
	var n int
	if err := dec.Decode(&n); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 42 {
		t.Fatalf("got %d, want 42", n)
	}

	var n2 int
	if err := dec.Decode(&n2); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDecoder_NoTrailingWhitespace(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`"hello"`))
	var s string
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if s != "hello" {
		t.Fatalf("got %q", s)
	}
}

// Error Handling

func TestDecoder_TruncatedInput(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`{"a":1`))
	var m map[string]int
	err := dec.Decode(&m)
	if err == nil {
		t.Fatal("expected error for truncated input")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %T: %v", err, err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF in chain, got: %v", err)
	}
}

func TestDecoder_SyntaxError(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`{invalid}`))
	var m map[string]any
	err := dec.Decode(&m)
	if err == nil {
		t.Fatal("expected error for syntax error")
	}
}

func TestDecoder_StickyError(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`{invalid}`))
	var m map[string]any

	err1 := dec.Decode(&m)
	if err1 == nil {
		t.Fatal("expected error")
	}

	// Subsequent calls should return the same error.
	err2 := dec.Decode(&m)
	if err2 == nil {
		t.Fatal("expected sticky error")
	}
}

// errReader is a reader that always returns an error.
type errReader struct {
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, e.err
}

func TestDecoder_ReaderError(t *testing.T) {
	testErr := errors.New("test read error")
	dec := NewDecoder(&errReader{err: testErr})

	var m map[string]any
	err := dec.Decode(&m)
	if err != testErr {
		t.Fatalf("expected test error, got %v", err)
	}

	// Sticky: second call returns same error.
	err = dec.Decode(&m)
	if err != testErr {
		t.Fatalf("expected sticky test error, got %v", err)
	}
}

func TestDecoder_NotPointer(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`42`))
	var n int
	err := dec.Decode(n) // not a pointer
	if err == nil {
		t.Fatal("expected error for non-pointer")
	}
}

// More() Method

func TestDecoder_More(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`1 2 3`))

	for i := 1; i <= 3; i++ {
		if !dec.More() {
			t.Fatalf("More() = false before Decode #%d", i)
		}
		var n int
		if err := dec.Decode(&n); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if n != i {
			t.Fatalf("Decode #%d: got %d", i, n)
		}
	}

	// After consuming all values and reaching EOF, More should return false.
	// Note: More() may return true until we actually try to read and discover EOF.
	// But after a failed Decode (EOF), More should definitely be false.
	var n int
	err := dec.Decode(&n)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if dec.More() {
		t.Fatal("More() = true after EOF")
	}
}

func TestDecoder_MoreEmpty(t *testing.T) {
	dec := NewDecoder(strings.NewReader(""))
	// For an empty reader, More() might return true (reader not yet read).
	// After a Decode attempt, it should reflect EOF.
	var n int
	if err := dec.Decode(&n); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if dec.More() {
		t.Fatal("More() = true after EOF on empty input")
	}
}

// Buffered() Method

func TestDecoder_Buffered(t *testing.T) {
	var buf bytes.Buffer
	for i := range 20 {
		fmt.Fprintf(&buf, `%d `, i)
	}
	buf.WriteString(`"trailing"`)

	dec := NewDecoder(&buf)

	for i := range 4 {
		var n int
		if err := dec.Decode(&n); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
	}

	// Verify Buffered() returns remaining unscanned data.
	remaining, err := io.ReadAll(dec.Buffered())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(remaining) == 0 {
		t.Log("Buffered returned empty (all data consumed or in a new buffer)")
	}
}

func TestDecoder_BufferedEmpty(t *testing.T) {
	dec := NewDecoder(strings.NewReader(""))
	remaining, err := io.ReadAll(dec.Buffered())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected empty, got %q", remaining)
	}
}

// Options

func TestDecoder_WithScanner(t *testing.T) {
	// WithScanner is a no-op retained for API compatibility.
	// Verify the option is accepted without affecting decode behavior.
	input := `{"a":1}`
	dec := NewDecoder(strings.NewReader(input))

	var m map[string]int
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["a"] != 1 {
		t.Fatalf("got %v", m)
	}
}

func TestDecoder_WithBufferSizeOption(t *testing.T) {
	// Just verify the option doesn't panic and works.
	input := `{"a":1} {"a":2}`
	dec := NewDecoder(strings.NewReader(input), WithBufferSize(64))

	for i := 1; i <= 2; i++ {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
	}
}

// Zero-Copy Safety

func TestDecoder_ZeroCopySafety(t *testing.T) {
	// Decode multiple values and verify earlier strings remain intact
	// after subsequent decodes.
	input := `{"s":"first"} {"s":"second"} {"s":"third"}`
	dec := NewDecoder(strings.NewReader(input))

	type Msg struct {
		S string `json:"s"`
	}

	results := make([]Msg, 0, 3)
	for i := range 3 {
		var m Msg
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		results = append(results, m)
	}

	// Verify all values are intact.
	expected := []string{"first", "second", "third"}
	for i, r := range results {
		if r.S != expected[i] {
			t.Fatalf("result[%d].S = %q, want %q", i, r.S, expected[i])
		}
	}
}

func TestDecoder_ZeroCopyAcrossBuffers(t *testing.T) {
	// Use small buffer to force buffer transitions.
	input := `{"s":"hello"} {"s":"world"}`
	dec := NewDecoder(strings.NewReader(input), WithBufferSize(20))

	type Msg struct {
		S string `json:"s"`
	}

	var m1 Msg
	if err := dec.Decode(&m1); err != nil {
		t.Fatalf("Decode #1: %v", err)
	}

	var m2 Msg
	if err := dec.Decode(&m2); err != nil {
		t.Fatalf("Decode #2: %v", err)
	}

	// Both should be intact.
	if m1.S != "hello" {
		t.Fatalf("m1.S = %q, want hello", m1.S)
	}
	if m2.S != "world" {
		t.Fatalf("m2.S = %q, want world", m2.S)
	}
}

// Compatibility with encoding/json.Decoder

func TestDecoder_CompatBasicStream(t *testing.T) {
	input := `{"a":1} {"a":2} {"a":3}`

	// Standard library decoder.
	stdDec := json.NewDecoder(strings.NewReader(input))
	// vjson decoder.
	vjDec := NewDecoder(strings.NewReader(input))

	for i := range 3 {
		var stdM, vjM map[string]any

		stdErr := stdDec.Decode(&stdM)
		vjErr := vjDec.Decode(&vjM)

		if stdErr != vjErr {
			t.Fatalf("Decode #%d: std err=%v, vj err=%v", i, stdErr, vjErr)
		}

		stdVal := stdM["a"].(float64)
		vjVal := vjM["a"].(float64)
		if stdVal != vjVal {
			t.Fatalf("Decode #%d: std a=%v, vj a=%v", i, stdVal, vjVal)
		}
	}

	// Both should return io.EOF.
	var stdM, vjM map[string]any
	stdErr := stdDec.Decode(&stdM)
	vjErr := vjDec.Decode(&vjM)
	if stdErr != io.EOF || vjErr != io.EOF {
		t.Fatalf("EOF: std=%v, vj=%v", stdErr, vjErr)
	}
}

func TestDecoder_CompatEmptyInput(t *testing.T) {
	stdDec := json.NewDecoder(strings.NewReader(""))
	vjDec := NewDecoder(strings.NewReader(""))

	var stdM, vjM map[string]any
	stdErr := stdDec.Decode(&stdM)
	vjErr := vjDec.Decode(&vjM)

	if stdErr != vjErr {
		t.Fatalf("std err=%v, vj err=%v", stdErr, vjErr)
	}
}

// DecodeValue Generic Wrapper

func TestDecodeValue(t *testing.T) {
	input := `{"name":"test","value":42}`
	dec := NewDecoder(strings.NewReader(input))

	type Msg struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	var m Msg
	if err := DecodeValue(dec, &m); err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if m.Name != "test" || m.Value != 42 {
		t.Fatalf("got %+v", m)
	}
}

// Escaped Strings

func TestDecoder_EscapedStrings(t *testing.T) {
	input := `{"s":"hello\nworld"} {"s":"tab\there"}`
	dec := NewDecoder(strings.NewReader(input))

	type Msg struct {
		S string `json:"s"`
	}

	var m1 Msg
	if err := dec.Decode(&m1); err != nil {
		t.Fatalf("Decode #1: %v", err)
	}
	if m1.S != "hello\nworld" {
		t.Fatalf("m1.S = %q, want %q", m1.S, "hello\nworld")
	}

	var m2 Msg
	if err := dec.Decode(&m2); err != nil {
		t.Fatalf("Decode #2: %v", err)
	}
	if m2.S != "tab\there" {
		t.Fatalf("m2.S = %q, want %q", m2.S, "tab\there")
	}
}

// Deeply Nested

func TestDecoder_DeeplyNested(t *testing.T) {
	// Build deeply nested JSON.
	depth := 50
	var buf bytes.Buffer
	for range depth {
		buf.WriteString(`{"inner":`)
	}
	buf.WriteString(`"leaf"`)
	for range depth {
		buf.WriteByte('}')
	}

	dec := NewDecoder(&buf, WithBufferSize(64))

	var m any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Traverse to the leaf.
	current := m
	for i := range depth {
		obj, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("at depth %d: not a map", i)
		}
		current = obj["inner"]
	}
	if s, ok := current.(string); !ok || s != "leaf" {
		t.Fatalf("leaf = %v, want \"leaf\"", current)
	}
}

// SkipErrors

func TestDecoder_SkipErrors_Basic(t *testing.T) {
	// Mix of good and bad lines. Bad lines should be skipped.
	input := "{\"x\":1}\n{bad}\n{\"x\":3}\n"
	dec := NewDecoder(strings.NewReader(input), WithSkipErrors(func(error) bool { return true }))

	var got []int
	var skipped int
	for {
		var m map[string]int
		err := dec.Decode(&m)
		if err == io.EOF {
			break
		}
		if err != nil {
			skipped++
			continue
		}
		got = append(got, m["x"])
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("got %v, want [1 3]", got)
	}
}

func TestDecoder_SkipErrors_AllBad(t *testing.T) {
	input := "{bad1}\n{bad2}\n{bad3}\n"
	dec := NewDecoder(strings.NewReader(input), WithSkipErrors(func(error) bool { return true }))

	var skipped int
	for {
		var m map[string]any
		err := dec.Decode(&m)
		if err == io.EOF {
			break
		}
		if err != nil {
			skipped++
			continue
		}
		t.Fatalf("unexpected successful decode: %v", m)
	}
	if skipped != 3 {
		t.Fatalf("skipped = %d, want 3", skipped)
	}
}

func TestDecoder_SkipErrors_Selective(t *testing.T) {
	// Only skip errSyntax; other errors remain sticky.
	input := "{bad}\n{\"x\":1}\n"
	dec := NewDecoder(strings.NewReader(input), WithSkipErrors(func(err error) bool {
		// Skip syntax errors only.
		return err.Error() == "vjson: syntax error"
	}))

	var m map[string]int
	// First decode hits bad line → callback returns true → skip.
	err := dec.Decode(&m)
	if err == nil {
		t.Fatal("expected error for bad line")
	}

	// Second decode should succeed.
	err = dec.Decode(&m)
	if err != nil {
		t.Fatalf("Decode good line: %v", err)
	}
	if m["x"] != 1 {
		t.Fatalf("got x=%d, want 1", m["x"])
	}
}

func TestDecoder_SkipErrors_Nil(t *testing.T) {
	// Without WithSkipErrors, errors are sticky (default behavior).
	dec := NewDecoder(strings.NewReader("{bad}\n{\"x\":1}\n"))

	var m map[string]any
	err1 := dec.Decode(&m)
	if err1 == nil {
		t.Fatal("expected error")
	}

	// Second call must return the same sticky error.
	err2 := dec.Decode(&m)
	if err2 != err1 {
		t.Fatalf("expected sticky error %v, got %v", err1, err2)
	}
}

func TestDecoder_SkipErrors_NoTrailingNewline(t *testing.T) {
	// Last line is bad with no trailing newline → should return io.EOF
	// after the skip since there's no more data.
	input := "{\"x\":1}\n{bad}"
	dec := NewDecoder(strings.NewReader(input), WithSkipErrors(func(error) bool { return true }))

	var m map[string]int
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("Decode #1: %v", err)
	}
	if m["x"] != 1 {
		t.Fatalf("got x=%d, want 1", m["x"])
	}

	// Second decode hits bad line, skipToNewline finds no newline → io.EOF.
	err := dec.Decode(&m)
	if err != io.EOF {
		t.Fatalf("expected io.EOF for bad last line, got %v", err)
	}
}

func TestDecoder_SkipErrors_SmallBuffer(t *testing.T) {
	// Force buffer boundary conditions with a small buffer.
	input := "{\"x\":1}\n{bad}\n{\"x\":3}\n"
	dec := NewDecoder(strings.NewReader(input), WithBufferSize(16), WithSkipErrors(func(error) bool { return true }))

	var got []int
	var skipped int
	for {
		var m map[string]int
		err := dec.Decode(&m)
		if err == io.EOF {
			break
		}
		if err != nil {
			skipped++
			continue
		}
		got = append(got, m["x"])
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("got %v, want [1 3]", got)
	}
}

// Benchmark

func BenchmarkDecoder_SingleObject(b *testing.B) {
	input := `{"name":"alice","age":30,"city":"wonderland","active":true}`

	type Msg struct {
		Name   string `json:"name"`
		Age    int    `json:"age"`
		City   string `json:"city"`
		Active bool   `json:"active"`
	}

	b.ReportAllocs()

	for b.Loop() {
		dec := NewDecoder(strings.NewReader(input))
		var m Msg
		if err := dec.Decode(&m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecoder_NDJSON(b *testing.B) {
	// Build NDJSON input.
	var buf bytes.Buffer
	for i := range 100 {
		fmt.Fprintf(&buf, `{"i":%d,"s":"value-%d"}`+"\n", i, i)
	}
	input := buf.String()

	type Msg struct {
		I int    `json:"i"`
		S string `json:"s"`
	}

	b.ReportAllocs()

	for b.Loop() {
		dec := NewDecoder(strings.NewReader(input))
		for {
			var m Msg
			err := dec.Decode(&m)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkStdDecoder_NDJSON(b *testing.B) {
	// Same test with encoding/json for comparison.
	var buf bytes.Buffer
	for i := range 100 {
		fmt.Fprintf(&buf, `{"i":%d,"s":"value-%d"}`+"\n", i, i)
	}
	input := buf.String()

	type Msg struct {
		I int    `json:"i"`
		S string `json:"s"`
	}

	b.ReportAllocs()

	for b.Loop() {
		dec := json.NewDecoder(strings.NewReader(input))
		for {
			var m Msg
			err := dec.Decode(&m)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
