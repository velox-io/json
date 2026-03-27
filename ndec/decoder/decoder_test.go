package decoder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/velox-io/json/native/gsdec"
	"github.com/velox-io/json/native/rsdec"
	"github.com/velox-io/json/ndec"
)

func TestRsdec(t *testing.T) {
	if !rsdec.D.Available {
		t.Skip("rsdec not available")
	}
	runAllTests(t, &rsdec.D)
}

func TestGsdec(t *testing.T) {
	if !gsdec.D.Available {
		t.Skip("gsdec not available")
	}
	runAllTests(t, &gsdec.D)
}

func runAllTests(t *testing.T, drv *ndec.Driver) {
	t.Run("FullBufferFlatStruct", func(t *testing.T) { testFullBufferFlatStruct(t, drv) })
	t.Run("FullBufferNestedStruct", func(t *testing.T) { testFullBufferNestedStruct(t, drv) })
	t.Run("FullBufferIntTypes", func(t *testing.T) { testFullBufferIntTypes(t, drv) })
	t.Run("FullBufferDeepNesting", func(t *testing.T) { testFullBufferDeepNesting(t, drv) })
	t.Run("StreamingFlatIntOnly", func(t *testing.T) { testStreamingFlatIntOnly(t, drv) })
	t.Run("StreamingNestedStruct", func(t *testing.T) { testStreamingNestedStruct(t, drv) })
	t.Run("StreamingDeep3Levels", func(t *testing.T) { testStreamingDeep3Levels(t, drv) })
	t.Run("StreamingWithStrings", func(t *testing.T) { testStreamingWithStrings(t, drv) })
	t.Run("StreamingNestedWithStrings", func(t *testing.T) { testStreamingNestedWithStrings(t, drv) })
	t.Run("StreamingZeroByteReads", func(t *testing.T) { testStreamingZeroByteReads(t, drv) })
	t.Run("StreamingTruncatedInput", func(t *testing.T) { testStreamingTruncatedInput(t, drv) })
	t.Run("StreamingEmptyInput", func(t *testing.T) { testStreamingEmptyInput(t, drv) })
	t.Run("StreamingReadError", func(t *testing.T) { testStreamingReadError(t, drv) })
	t.Run("FullBufferEscapedStrings", func(t *testing.T) { testFullBufferEscapedStrings(t, drv) })
	t.Run("StreamingEscapedStrings", func(t *testing.T) { testStreamingEscapedStrings(t, drv) })
	t.Run("ArenaOverflow", func(t *testing.T) { testArenaOverflow(t, drv) })
	t.Run("StreamingArenaOverflowWithSmallChunks", func(t *testing.T) { testStreamingArenaOverflowWithSmallChunks(t, drv) })
	t.Run("StreamingNestedArenaAndEOF", func(t *testing.T) { testStreamingNestedArenaAndEOF(t, drv) })
	t.Run("FullBufferSlice", func(t *testing.T) { testFullBufferSlice(t, drv) })
	t.Run("SliceEmpty", func(t *testing.T) { testSliceEmpty(t, drv) })
	t.Run("SliceOfStrings", func(t *testing.T) { testSliceOfStrings(t, drv) })
	t.Run("SliceNested", func(t *testing.T) { testSliceNested(t, drv) })
	t.Run("StreamingSlice", func(t *testing.T) { testStreamingSlice(t, drv) })
	t.Run("StreamingSliceWithArena", func(t *testing.T) { testStreamingSliceWithArena(t, drv) })
	t.Run("SliceNestedWithStreaming", func(t *testing.T) { testSliceNestedWithStreaming(t, drv) })
	t.Run("FullBufferArray", func(t *testing.T) { testFullBufferArray(t, drv) })
	t.Run("ArrayEmpty", func(t *testing.T) { testArrayEmpty(t, drv) })
	t.Run("ArrayPartial", func(t *testing.T) { testArrayPartial(t, drv) })
	t.Run("ArrayOverflow", func(t *testing.T) { testArrayOverflow(t, drv) })
	t.Run("ArrayOfStrings", func(t *testing.T) { testArrayOfStrings(t, drv) })
	t.Run("ArrayNested", func(t *testing.T) { testArrayNested(t, drv) })
	t.Run("StreamingArray", func(t *testing.T) { testStreamingArray(t, drv) })
	t.Run("StreamingArrayPartial", func(t *testing.T) { testStreamingArrayPartial(t, drv) })
	t.Run("StreamingArrayOverflow", func(t *testing.T) { testStreamingArrayOverflow(t, drv) })
	t.Run("StreamingArrayOfStrings", func(t *testing.T) { testStreamingArrayOfStrings(t, drv) })
	t.Run("StreamingArrayNested", func(t *testing.T) { testStreamingArrayNested(t, drv) })
	t.Run("FullBufferMapStringInt", func(t *testing.T) { testFullBufferMapStringInt(t, drv) })
	t.Run("FullBufferMapStringString", func(t *testing.T) { testFullBufferMapStringString(t, drv) })
	t.Run("FullBufferMapEmpty", func(t *testing.T) { testFullBufferMapEmpty(t, drv) })
	t.Run("FullBufferMapStringStruct", func(t *testing.T) { testFullBufferMapStringStruct(t, drv) })
	t.Run("StreamingMapStringInt", func(t *testing.T) { testStreamingMapStringInt(t, drv) })
	t.Run("StreamingMapStringString", func(t *testing.T) { testStreamingMapStringString(t, drv) })
	t.Run("StreamingMapStringStruct", func(t *testing.T) { testStreamingMapStringStruct(t, drv) })
	t.Run("StreamingMapEmpty", func(t *testing.T) { testStreamingMapEmpty(t, drv) })
	t.Run("StreamingMapEscapedKeys", func(t *testing.T) { testStreamingMapEscapedKeys(t, drv) })
	t.Run("StreamingComprehensive", func(t *testing.T) { testStreamingComprehensive(t, drv) })
	t.Run("FullBufferPointerBasic", func(t *testing.T) { testFullBufferPointerBasic(t, drv) })
	t.Run("FullBufferPointerNull", func(t *testing.T) { testFullBufferPointerNull(t, drv) })
	t.Run("FullBufferPointerReuse", func(t *testing.T) { testFullBufferPointerReuse(t, drv) })
	t.Run("FullBufferPointerPrimitive", func(t *testing.T) { testFullBufferPointerPrimitive(t, drv) })
	t.Run("StreamingPointer", func(t *testing.T) { testStreamingPointer(t, drv) })
	t.Run("StreamingPointerComprehensive", func(t *testing.T) { testStreamingPointerComprehensive(t, drv) })
	t.Run("StreamingSkipUnknownField", func(t *testing.T) { testStreamingSkipUnknownField(t, drv) })
	t.Run("StreamingSkipLargeValue", func(t *testing.T) { testStreamingSkipLargeValue(t, drv) })
}

// Helpers

// slowReader delivers data one chunk at a time, simulating a slow network.
type slowReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

// itoa for test names (avoid fmt import)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// zeroThenDataReader returns (0, nil) for the first `zeros` calls,
// then delivers data normally in chunks.
type zeroThenDataReader struct {
	data      []byte
	chunk     int
	pos       int
	zeros     int
	zeroCalls int
}

func (r *zeroThenDataReader) Read(p []byte) (int, error) {
	if r.zeroCalls < r.zeros {
		r.zeroCalls++
		return 0, nil // legal per io.Reader contract
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

// errorAfterReader delivers data normally, then returns a custom error.
type errorAfterReader struct {
	data []byte
	pos  int
	err  error
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// countingReader tracks the total bytes delivered across all Read calls.
type countingReader struct {
	data       []byte
	chunk      int
	pos        int
	totalBytes int
	reads      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	r.totalBytes += n
	r.reads++
	return n, nil
}

func repeatStr(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for range n {
		b = append(b, s...)
	}
	return string(b)
}

// Full-buffer tests (no streaming, verify basic correctness)

func testFullBufferFlatStruct(t *testing.T, drv *ndec.Driver) {
	type Person struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	input := `{"name":"Alice","age":30}`
	var got Person
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Name != "Alice" || got.Age != 30 {
		t.Fatalf("got %+v", got)
	}
	t.Logf("OK: %+v", got)
}

func testFullBufferNestedStruct(t *testing.T, drv *ndec.Driver) {
	type Address struct {
		City string `json:"city"`
		Zip  string `json:"zip"`
	}
	type Person struct {
		Name    string  `json:"name"`
		Age     int     `json:"age"`
		Address Address `json:"address"`
	}

	input := `{"name":"Bob","age":25,"address":{"city":"NYC","zip":"10001"}}`
	var got Person
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Name != "Bob" || got.Age != 25 || got.Address.City != "NYC" || got.Address.Zip != "10001" {
		t.Fatalf("got %+v", got)
	}
	t.Logf("OK: %+v", got)
}

func testFullBufferIntTypes(t *testing.T, drv *ndec.Driver) {
	type IntTypes struct {
		I8  int8   `json:"i8"`
		I16 int16  `json:"i16"`
		I32 int32  `json:"i32"`
		I64 int64  `json:"i64"`
		U8  uint8  `json:"u8"`
		U16 uint16 `json:"u16"`
		U32 uint32 `json:"u32"`
		U64 uint64 `json:"u64"`
	}

	input := `{"i8":127,"i16":32767,"i32":2147483647,"i64":9223372036854775807,"u8":255,"u16":65535,"u32":4294967295,"u64":18446744073709551615}`
	var got IntTypes
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.I64 != 9223372036854775807 || got.U64 != 18446744073709551615 {
		t.Fatalf("got %+v", got)
	}
	t.Logf("OK: %+v", got)
}

func testFullBufferDeepNesting(t *testing.T, drv *ndec.Driver) {
	type C struct {
		Z int `json:"z"`
	}
	type B struct {
		Y int `json:"y"`
		C C   `json:"c"`
	}
	type A struct {
		X int `json:"x"`
		B B   `json:"b"`
	}

	input := `{"x":1,"b":{"y":2,"c":{"z":3}}}`
	var got A
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.X != 1 || got.B.Y != 2 || got.B.C.Z != 3 {
		t.Fatalf("got %+v", got)
	}
	t.Logf("OK: %+v", got)
}

// Streaming tests — small chunk sizes force EOF + resume

func testStreamingFlatIntOnly(t *testing.T, drv *ndec.Driver) {
	type Point struct {
		X int64 `json:"x"`
		Y int64 `json:"y"`
	}

	input := []byte(`{"x":12345,"y":67890}`)

	// Test with various chunk sizes that force mid-parse EOF
	for _, chunk := range []int{1, 2, 3, 5, 7, 10, 15, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Point
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.X != 12345 || got.Y != 67890 {
				t.Fatalf("chunk=%d: got %+v", chunk, got)
			}
		})
	}
}

func testStreamingNestedStruct(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type Outer struct {
		X     int   `json:"x"`
		Inner Inner `json:"inner"`
		Y     int   `json:"y"`
	}

	input := []byte(`{"x":1,"inner":{"a":10,"b":20},"y":2}`)

	for _, chunk := range []int{1, 3, 5, 8, 12, 20, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Outer
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.X != 1 || got.Inner.A != 10 || got.Inner.B != 20 || got.Y != 2 {
				t.Fatalf("chunk=%d: got %+v", chunk, got)
			}
		})
	}
}

func testStreamingDeep3Levels(t *testing.T, drv *ndec.Driver) {
	type C struct {
		V int `json:"v"`
	}
	type B struct {
		C C   `json:"c"`
		N int `json:"n"`
	}
	type A struct {
		B B   `json:"b"`
		M int `json:"m"`
	}

	input := []byte(`{"b":{"c":{"v":42},"n":7},"m":99}`)

	for _, chunk := range []int{1, 2, 4, 7, 11, 17, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got A
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.B.C.V != 42 || got.B.N != 7 || got.M != 99 {
				t.Fatalf("chunk=%d: got %+v", chunk, got)
			}
		})
	}
}

func testStreamingWithStrings(t *testing.T, drv *ndec.Driver) {
	type Data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
		City string `json:"city"`
	}

	input := []byte(`{"name":"Alice","age":30,"city":"NYC"}`)

	for _, chunk := range []int{1, 3, 5, 8, 12, 20, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Data
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.Name != "Alice" || got.Age != 30 || got.City != "NYC" {
				t.Fatalf("chunk=%d: got %+v", chunk, got)
			}
		})
	}
}

func testStreamingNestedWithStrings(t *testing.T, drv *ndec.Driver) {
	type Address struct {
		City string `json:"city"`
		Zip  string `json:"zip"`
	}
	type Person struct {
		Name    string  `json:"name"`
		Age     int     `json:"age"`
		Address Address `json:"address"`
	}

	input := []byte(`{"name":"Bob","age":25,"address":{"city":"NYC","zip":"10001"}}`)

	for _, chunk := range []int{1, 3, 5, 8, 13, 21, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Person
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.Name != "Bob" || got.Age != 25 || got.Address.City != "NYC" || got.Address.Zip != "10001" {
				t.Fatalf("chunk=%d: got %+v", chunk, got)
			}
		})
	}
}

// Edge cases: Reader behavior

func testStreamingZeroByteReads(t *testing.T, drv *ndec.Driver) {
	type Point struct {
		X int64 `json:"x"`
		Y int64 `json:"y"`
	}

	input := []byte(`{"x":100,"y":200}`)

	// Reader returns (0,nil) several times before delivering data.
	// Must not cause infinite loop.
	r := &zeroThenDataReader{data: input, chunk: 5, zeros: 10}
	dec := NewWithChunkSize(r, drv, 5)
	var got Point
	err := dec.Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.X != 100 || got.Y != 200 {
		t.Fatalf("got %+v", got)
	}
	t.Logf("OK: %+v (zeroCalls=%d)", got, r.zeroCalls)
}

func testStreamingTruncatedInput(t *testing.T, drv *ndec.Driver) {
	type Point struct {
		X int64 `json:"x"`
		Y int64 `json:"y"`
	}

	// Truncated JSON: missing closing '}'
	input := []byte(`{"x":100,"y":200`)

	r := &slowReader{data: input, chunk: 5}
	dec := NewWithChunkSize(r, drv, 5)
	var got Point
	err := dec.Decode(&got)
	if err == nil {
		t.Fatalf("expected error, got %+v", got)
	}
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
	t.Logf("OK: correctly returned %v", err)
}

func testStreamingEmptyInput(t *testing.T, drv *ndec.Driver) {
	type Point struct {
		X int64 `json:"x"`
	}

	r := &slowReader{data: []byte{}, chunk: 16}
	dec := NewWithChunkSize(r, drv, 16)
	var got Point
	err := dec.Decode(&got)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	t.Logf("OK: correctly returned %v", err)
}

func testStreamingReadError(t *testing.T, drv *ndec.Driver) {
	type Point struct {
		X int64 `json:"x"`
		Y int64 `json:"y"`
	}

	// Deliver partial data, then a hard error.
	customErr := io.ErrClosedPipe
	r := &errorAfterReader{data: []byte(`{"x":1`), err: customErr}
	dec := NewWithChunkSize(r, drv, 4)
	var got Point
	err := dec.Decode(&got)
	if err == nil {
		t.Fatalf("expected error, got %+v", got)
	}
	if err != customErr {
		t.Fatalf("expected %v, got %v", customErr, err)
	}
	t.Logf("OK: correctly propagated %v", err)
}

// Escaped string tests

func testFullBufferEscapedStrings(t *testing.T, drv *ndec.Driver) {
	type Msg struct {
		Text string `json:"text"`
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{"newline", `{"text":"hello\nworld"}`, "hello\nworld"},
		{"tab", `{"text":"hello\tworld"}`, "hello\tworld"},
		{"quote", `{"text":"say \"hi\""}`, `say "hi"`},
		{"backslash", `{"text":"a\\b"}`, `a\b`},
		{"unicode", `{"text":"\u0048\u0069"}`, "Hi"},
		{"mixed", `{"text":"line1\nline2\ttab\\end"}`, "line1\nline2\ttab\\end"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader([]byte(tt.json))
			dec := New(r, drv)
			var got Msg
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got.Text != tt.want {
				t.Errorf("got %q, want %q", got.Text, tt.want)
			}
		})
	}
}

func testStreamingEscapedStrings(t *testing.T, drv *ndec.Driver) {
	type Msg struct {
		Text string `json:"text"`
	}

	input := []byte(`{"text":"hello\nworld"}`)

	for _, chunk := range []int{1, 3, 5, 8, 12, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Msg
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.Text != "hello\nworld" {
				t.Fatalf("chunk=%d: got %q", chunk, got.Text)
			}
		})
	}
}

func testArenaOverflow(t *testing.T, drv *ndec.Driver) {
	type Data struct {
		A string `json:"a"`
		B string `json:"b"`
	}

	// Create two escaped strings that together exceed the default arena.
	// Each string has \n escapes so has_escape=true → goes to arena.
	// We use a small arena decoder to force the overflow.
	bigEsc := `"` + repeatStr("x\\n", 200) + `"` // ~600 raw bytes → ~400 unescaped
	input := []byte(`{"a":` + bigEsc + `,"b":` + bigEsc + `}`)

	// Decoder with default arena — arena overflow handled by yield.
	dec := NewWithChunkSize(bytes.NewReader(input), drv, 4096)
	var got Data
	err := dec.Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Verify both strings are correctly unescaped
	expected := repeatStr("x\n", 200)
	if got.A != expected {
		t.Errorf("A length: got %d, want %d", len(got.A), len(expected))
	}
	if got.B != expected {
		t.Errorf("B length: got %d, want %d", len(got.B), len(expected))
	}
	t.Logf("OK: A len=%d, B len=%d", len(got.A), len(got.B))
}

func testStreamingArenaOverflowWithSmallChunks(t *testing.T, drv *ndec.Driver) {
	type Data struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
		Val  int    `json:"val"`
	}

	// Escaped strings that need arena, combined with small chunks that
	// trigger buffer EOF. Both suspend reasons will interleave.
	// "desc" has escapes → arena; small chunk → EOF mid-field.
	input := []byte(`{"name":"Alice","desc":"line1\nline2\nline3\nline4\nline5","val":42}`)

	for _, chunk := range []int{1, 3, 5, 8, 13, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			// Small chunk to force arena overflow on "desc"
			dec := NewWithChunkSize(r, drv, chunk)
			var got Data
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.Name != "Alice" {
				t.Errorf("chunk=%d: Name=%q, want Alice", chunk, got.Name)
			}
			wantDesc := "line1\nline2\nline3\nline4\nline5"
			if got.Desc != wantDesc {
				t.Errorf("chunk=%d: Desc=%q, want %q", chunk, got.Desc, wantDesc)
			}
			if got.Val != 42 {
				t.Errorf("chunk=%d: Val=%d, want 42", chunk, got.Val)
			}
		})
	}
}

func testStreamingNestedArenaAndEOF(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		Msg string `json:"msg"`
		N   int    `json:"n"`
	}
	type Outer struct {
		Tag   string `json:"tag"`
		Inner Inner  `json:"inner"`
		End   int    `json:"end"`
	}

	// Nested struct with escaped strings at both levels.
	// Small chunks + small arena = both EOF and arena yields at different nesting depths.
	input := []byte(`{"tag":"hello\tworld","inner":{"msg":"a\\b\\c\\d","n":7},"end":99}`)

	for _, chunk := range []int{1, 2, 4, 7, 11, 17, len(input)} {
		t.Run(itoa(chunk), func(t *testing.T) {
			r := &slowReader{data: input, chunk: chunk}
			dec := NewWithChunkSize(r, drv, chunk)
			var got Outer
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("chunk=%d: error: %v", chunk, err)
			}
			if got.Tag != "hello\tworld" {
				t.Errorf("chunk=%d: Tag=%q", chunk, got.Tag)
			}
			if got.Inner.Msg != "a\\b\\c\\d" {
				t.Errorf("chunk=%d: Inner.Msg=%q", chunk, got.Inner.Msg)
			}
			if got.Inner.N != 7 {
				t.Errorf("chunk=%d: Inner.N=%d", chunk, got.Inner.N)
			}
			if got.End != 99 {
				t.Errorf("chunk=%d: End=%d", chunk, got.End)
			}
		})
	}
}

// Array/Slice tests

func testFullBufferSlice(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items []int `json:"items"`
	}

	input := `{"items":[1,2,3,4,5]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.Items) != 5 {
		t.Fatalf("got %d items, expected 5", len(got.Items))
	}
	for i, v := range got.Items {
		if v != i+1 {
			t.Fatalf("Items[%d] = %d, expected %d", i, v, i+1)
		}
	}
	t.Logf("OK: %+v", got)
}

func testSliceEmpty(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items []int `json:"items"`
	}

	input := `{"items":[]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("got len=%d, expected 0", len(got.Items))
	}
	// Empty slices may be nil or zero-capacity — both are valid
	t.Logf("OK: empty slice %v (len=%d, cap=%d)", got.Items, len(got.Items), cap(got.Items))
}

func testSliceOfStrings(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Names []string `json:"names"`
	}

	input := `{"names":["Alice","Bob","Charlie"]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.Names) != 3 {
		t.Fatalf("got %d names, expected 3", len(got.Names))
	}
	expected := []string{"Alice", "Bob", "Charlie"}
	for i, name := range got.Names {
		if name != expected[i] {
			t.Fatalf("Names[%d] = %q, expected %q", i, name, expected[i])
		}
	}
	t.Logf("OK: %+v", got)
}

func testSliceNested(t *testing.T, drv *ndec.Driver) {
	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type Container struct {
		Items []Item `json:"items"`
	}

	input := `{"items":[{"id":1,"name":"first"},{"id":2,"name":"second"}]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d items, expected 2", len(got.Items))
	}
	if got.Items[0].ID != 1 || got.Items[0].Name != "first" {
		t.Fatalf("Items[0] = %+v", got.Items[0])
	}
	if got.Items[1].ID != 2 || got.Items[1].Name != "second" {
		t.Fatalf("Items[1] = %+v", got.Items[1])
	}
	t.Logf("OK: %+v", got)
}

func testStreamingSlice(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items []int `json:"items"`
	}

	input := `{"items":[1,2,3,4,5]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			err := New(reader, drv).Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if len(got.Items) != 5 {
				t.Fatalf("got %d items, expected 5", len(got.Items))
			}
			for i, v := range got.Items {
				if v != i+1 {
					t.Fatalf("Items[%d] = %d, expected %d", i, v, i+1)
				}
			}
		})
	}
}

func testStreamingSliceWithArena(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Names []string `json:"names"`
	}

	input := `{"names":["escape\u0041me","norm\\al","unu\\\\ual"]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			dec := New(reader, drv)
			err := dec.Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if len(got.Names) != 3 {
				t.Fatalf("got %d names, expected 3", len(got.Names))
			}
			// First element has escape sequence \u0041 which becomes 'A'
			if len(got.Names[0]) != 9 || got.Names[0] != "escapeAme" {
				t.Fatalf("Names[0] = %q (len=%d), expected 'escapeAme' (len=9)", got.Names[0], len(got.Names[0]))
			}
		})
	}
}

func testSliceNestedWithStreaming(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	type S struct {
		Items []Inner `json:"items"`
	}
	input := `{"items":[{"x":1,"y":2},{"x":3,"y":4},{"x":5,"y":6}]}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if len(got.Items) != 3 {
				t.Fatalf("chunk=%d got %d items: %v", chunk, len(got.Items), got.Items)
			}
			if got.Items[0].X != 1 || got.Items[0].Y != 2 ||
				got.Items[1].X != 3 || got.Items[1].Y != 4 ||
				got.Items[2].X != 5 || got.Items[2].Y != 6 {
				t.Fatalf("chunk=%d got %v", chunk, got.Items)
			}
		})
	}
}

// Array tests (fixed-length Go [N]T)

func testFullBufferArray(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [5]int `json:"items"`
	}

	input := `{"items":[1,2,3,4,5]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	expected := [5]int{1, 2, 3, 4, 5}
	if got.Items != expected {
		t.Fatalf("got %v, expected %v", got.Items, expected)
	}
	t.Logf("OK: %+v", got)
}

func testArrayEmpty(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [3]int `json:"items"`
	}

	// JSON has empty array, Go array is fixed [3]int
	// Rust should skip overflow elements (but here none), leave zeros unset
	input := `{"items":[]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	expected := [3]int{0, 0, 0} // all zeros (not provided in JSON)
	if got.Items != expected {
		t.Fatalf("got %v, expected %v (all zeros)", got.Items, expected)
	}
	t.Logf("OK: empty array result %v", got.Items)
}

func testArrayPartial(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [5]int `json:"items"`
	}

	// JSON has only 3 elements, array is [5]int
	input := `{"items":[10,20,30]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Items[0] != 10 || got.Items[1] != 20 || got.Items[2] != 30 {
		t.Fatalf("got %v", got.Items)
	}
	if got.Items[3] != 0 || got.Items[4] != 0 {
		t.Fatalf("unfilled elements should be zero: got %v", got.Items)
	}
	t.Logf("OK: partial array %v", got.Items)
}

func testArrayOverflow(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [2]int `json:"items"`
	}

	// JSON has 5 elements, array is only [2]int
	// Rust should skip the overflow elements
	input := `{"items":[1,2,3,4,5]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	expected := [2]int{1, 2} // only first 2
	if got.Items != expected {
		t.Fatalf("got %v, expected %v (overflow skipped)", got.Items, expected)
	}
	t.Logf("OK: array with overflow %v", got.Items)
}

func testArrayOfStrings(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Names [3]string `json:"names"`
	}

	input := `{"names":["Alice","Bob","Charlie"]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	expected := [3]string{"Alice", "Bob", "Charlie"}
	if got.Names != expected {
		t.Fatalf("got %v, expected %v", got.Names, expected)
	}
	t.Logf("OK: %+v", got)
}

func testArrayNested(t *testing.T, drv *ndec.Driver) {
	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type Container struct {
		Items [2]Item `json:"items"`
	}

	input := `{"items":[{"id":1,"name":"first"},{"id":2,"name":"second"}]}`
	var got Container
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("array should have 2 elements")
	}
	if got.Items[0].ID != 1 || got.Items[0].Name != "first" {
		t.Fatalf("Items[0] = %+v", got.Items[0])
	}
	if got.Items[1].ID != 2 || got.Items[1].Name != "second" {
		t.Fatalf("Items[1] = %+v", got.Items[1])
	}
	t.Logf("OK: %+v", got)
}

func testStreamingArray(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [5]int `json:"items"`
	}

	input := `{"items":[1,2,3,4,5]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			err := NewWithChunkSize(reader, drv, chunk).Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			expected := [5]int{1, 2, 3, 4, 5}
			if got.Items != expected {
				t.Fatalf("got %v, expected %v", got.Items, expected)
			}
		})
	}
}

func testStreamingArrayPartial(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [5]int `json:"items"`
	}

	// JSON has 3 elements, array size is 5
	input := `{"items":[10,20,30]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			err := NewWithChunkSize(reader, drv, chunk).Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got.Items[0] != 10 || got.Items[1] != 20 || got.Items[2] != 30 {
				t.Fatalf("got %v", got.Items)
			}
			if got.Items[3] != 0 || got.Items[4] != 0 {
				t.Fatalf("unfilled should be zero: got %v", got.Items)
			}
		})
	}
}

func testStreamingArrayOverflow(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Items [3]int `json:"items"`
	}

	// JSON has 6 elements, array is only [3]int
	input := `{"items":[1,2,3,4,5,6]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			err := NewWithChunkSize(reader, drv, chunk).Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			expected := [3]int{1, 2, 3}
			if got.Items != expected {
				t.Fatalf("got %v, expected %v (overflow skipped)", got.Items, expected)
			}
		})
	}
}

func testStreamingArrayOfStrings(t *testing.T, drv *ndec.Driver) {
	type Container struct {
		Names [3]string `json:"names"`
	}

	input := `{"names":["Alice","Bob","Charlie"]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			var got Container
			reader := &slowReader{data: []byte(input), chunk: chunk}
			err := NewWithChunkSize(reader, drv, chunk).Decode(&got)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			expected := [3]string{"Alice", "Bob", "Charlie"}
			if got.Names != expected {
				t.Fatalf("got %v, expected %v", got.Names, expected)
			}
		})
	}
}

func testStreamingArrayNested(t *testing.T, drv *ndec.Driver) {
	type Item struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	type Container struct {
		Items [3]Item `json:"items"`
	}

	input := `{"items":[{"x":1,"y":2},{"x":3,"y":4},{"x":5,"y":6}]}`

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			reader := &slowReader{data: []byte(input), chunk: chunk}
			dec := NewWithChunkSize(reader, drv, chunk)
			var got Container
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if len(got.Items) != 3 {
				t.Fatalf("chunk=%d: array should have 3 elements", chunk)
			}
			if got.Items[0].X != 1 || got.Items[0].Y != 2 {
				t.Fatalf("chunk=%d: Items[0]=%+v", chunk, got.Items[0])
			}
			if got.Items[1].X != 3 || got.Items[1].Y != 4 {
				t.Fatalf("chunk=%d: Items[1]=%+v", chunk, got.Items[1])
			}
			if got.Items[2].X != 5 || got.Items[2].Y != 6 {
				t.Fatalf("chunk=%d: Items[2]=%+v", chunk, got.Items[2])
			}
		})
	}
}

// Map tests

func testFullBufferMapStringInt(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]int `json:"m"`
	}
	input := `{"m":{"a":1,"b":2,"c":3}}`
	dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: len(input)}, drv, len(input))
	var got S
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.M) != 3 || got.M["a"] != 1 || got.M["b"] != 2 || got.M["c"] != 3 {
		t.Fatalf("got %v, want map[a:1 b:2 c:3]", got.M)
	}
}

func testFullBufferMapStringString(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]string `json:"m"`
	}
	input := `{"m":{"key1":"val1","key2":"val2"}}`
	dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: len(input)}, drv, len(input))
	var got S
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.M) != 2 || got.M["key1"] != "val1" || got.M["key2"] != "val2" {
		t.Fatalf("got %v", got.M)
	}
}

func testFullBufferMapEmpty(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]int `json:"m"`
	}
	input := `{"m":{}}`
	dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: len(input)}, drv, len(input))
	var got S
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.M == nil || len(got.M) != 0 {
		t.Fatalf("got %v, want empty non-nil map", got.M)
	}
}

func testFullBufferMapStringStruct(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int `json:"x"`
	}
	type S struct {
		M map[string]Inner `json:"m"`
	}
	input := `{"m":{"a":{"x":10},"b":{"x":20}}}`
	dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: len(input)}, drv, len(input))
	var got S
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got.M) != 2 || got.M["a"].X != 10 || got.M["b"].X != 20 {
		t.Fatalf("got %v", got.M)
	}
}

func testStreamingMapStringInt(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]int `json:"m"`
	}
	input := `{"m":{"a":1,"b":2,"c":3}}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if len(got.M) != 3 || got.M["a"] != 1 || got.M["b"] != 2 || got.M["c"] != 3 {
				t.Fatalf("chunk=%d got %v", chunk, got.M)
			}
		})
	}
}

func testStreamingMapStringString(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]string `json:"m"`
	}
	input := `{"m":{"key1":"val1","key2":"val2","key3":"val3"}}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if len(got.M) != 3 || got.M["key1"] != "val1" || got.M["key2"] != "val2" || got.M["key3"] != "val3" {
				t.Fatalf("chunk=%d got %v", chunk, got.M)
			}
		})
	}
}

func testStreamingMapStringStruct(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	type S struct {
		M map[string]Inner `json:"m"`
	}
	input := `{"m":{"a":{"x":10,"y":20},"b":{"x":30,"y":40}}}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if len(got.M) != 2 || got.M["a"].X != 10 || got.M["a"].Y != 20 || got.M["b"].X != 30 || got.M["b"].Y != 40 {
				t.Fatalf("chunk=%d got %v", chunk, got.M)
			}
		})
	}
}

func testStreamingMapEmpty(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]int `json:"m"`
	}
	input := `{"m":{}}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if got.M == nil || len(got.M) != 0 {
				t.Fatalf("chunk=%d got %v, want empty non-nil map", chunk, got.M)
			}
		})
	}
}

func testStreamingMapEscapedKeys(t *testing.T, drv *ndec.Driver) {
	type S struct {
		M map[string]int `json:"m"`
	}
	// Keys with escape sequences: \\n becomes literal backslash-n, \\t becomes backslash-t, \" becomes quote
	input := `{"m":{"key\\nA":1,"key\\tB":2,"key\"C":3}}`
	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			dec := NewWithChunkSize(&slowReader{data: []byte(input), chunk: chunk}, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			// \\n in JSON becomes backslash-n in the string
			if len(got.M) != 3 || got.M["key\\nA"] != 1 || got.M["key\\tB"] != 2 || got.M["key\"C"] != 3 {
				t.Fatalf("chunk=%d got %v", chunk, got.M)
			}
		})
	}
}

// Comprehensive streaming test — all types combined

func testStreamingComprehensive(t *testing.T, drv *ndec.Driver) {
	type Coord struct {
		X int `json:"x"`
		Y int `json:"y"`
	}

	type Inner struct {
		Label string `json:"label"`
		Value any    `json:"value"`
	}

	type Comprehensive struct {
		// primitives
		Name    string `json:"name"`
		Age     int    `json:"age"`
		Escaped string `json:"escaped"`

		// slice
		Tags   []string `json:"tags"`
		Coords []Coord  `json:"coords"`

		// array (fixed-length)
		Fixed    [3]Coord `json:"fixed"`
		Overflow [2]int   `json:"overflow"`
		Partial  [5]int   `json:"partial"`

		// map
		Meta      map[string]int   `json:"meta"`
		NestedMap map[string]Coord `json:"nested_map"`
		AnyMap    map[string]any   `json:"any_map"`
		InnerMap  map[string]Inner `json:"inner_map"`

		// any / interface{}
		AnyInt  any `json:"any_int"`
		AnyStr  any `json:"any_str"`
		AnyObj  any `json:"any_obj"`
		AnyArr  any `json:"any_arr"`
		AnyNull any `json:"any_null"`
		AnyBool any `json:"any_bool"`

		// nested struct with any field
		Inner Inner `json:"inner"`
	}

	input := `
{
  "name": "Alice",
  "age": 30,
  "escaped": "line1\nline2\t\"end\"",
  "tags": [ "go", "rust", "json" ],
  "coords": [
    { "x": 1, "y": 2 },
    { "x": 3, "y": 4 }
  ],
  "fixed": [
    { "x": 10, "y": 20 },
    { "x": 30, "y": 40 },
    { "x": 50, "y": 60 }
  ],
  "overflow": [ 100, 200, 300, 400 ],
  "partial": [ 7, 8, 9 ],
  "meta": { "a": 1, "b": 2 },
  "nested_map": {
    "p": { "x": 5, "y": 6 },
    "q": { "x": 7, "y": 8 }
  },
  "any_map": { "k1": 42, "k2": "hello", "k3": true },
  "inner_map": {
    "m1": { "label": "first", "value": 99 },
    "m2": { "label": "second", "value": "txt" }
  },
  "any_int": 12345,
  "any_str": "world",
  "any_obj": {
    "nested_key": "nested_val"
  },
  "any_arr": [ 1, "two", false ],
  "any_null": null,
  "any_bool": true,
  "inner": {
    "label": "inner_label",
    "value": 3.14
  }
}`

	// Use encoding/json as reference implementation
	var want Comprehensive
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("json.Unmarshal reference failed: %v", err)
	}

	inputBytes := []byte(input)

	for chunk := 1; chunk <= len(inputBytes); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			reader := &slowReader{data: inputBytes, chunk: chunk}
			dec := NewWithChunkSize(reader, drv, chunk)
			var got Comprehensive
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("chunk=%d mismatch\ngot:  %+v\nwant: %+v", chunk, got, want)
			}
		})
	}
}

// Pointer tests

func testFullBufferPointerBasic(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner *Inner `json:"inner"`
		Age   int    `json:"age"`
	}

	input := `{"name":"Alice","inner":{"x":10,"y":"hello"},"age":30}`
	var got Outer
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Name != "Alice" || got.Age != 30 {
		t.Fatalf("got %+v", got)
	}
	if got.Inner == nil {
		t.Fatal("Inner is nil")
	}
	if got.Inner.X != 10 || got.Inner.Y != "hello" {
		t.Fatalf("Inner = %+v", got.Inner)
	}
	t.Logf("OK: %+v, Inner=%+v", got, *got.Inner)
}

func testFullBufferPointerNull(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner *Inner `json:"inner"`
	}

	input := `{"name":"Alice","inner":null}`
	var got Outer
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Name != "Alice" {
		t.Fatalf("Name = %q", got.Name)
	}
	if got.Inner != nil {
		t.Fatalf("Inner should be nil, got %+v", got.Inner)
	}
	t.Logf("OK: Inner is nil")
}

func testFullBufferPointerReuse(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		V int `json:"v"`
	}
	type Outer struct {
		P *Inner `json:"p"`
	}

	// Pre-allocate the pointer
	existing := &Inner{V: 999}
	got := Outer{P: existing}
	input := `{"p":{"v":42}}`
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.P == nil {
		t.Fatal("P is nil")
	}
	if got.P.V != 42 {
		t.Fatalf("P.V = %d, want 42", got.P.V)
	}
	// Should reuse the existing allocation
	if got.P != existing {
		t.Fatalf("pointer was not reused: got %p, want %p", got.P, existing)
	}
	t.Logf("OK: reused pointer, V=%d", got.P.V)
}

func testFullBufferPointerPrimitive(t *testing.T, drv *ndec.Driver) {
	type S struct {
		N *int    `json:"n"`
		S *string `json:"s"`
	}

	input := `{"n":42,"s":"hello"}`
	var got S
	err := New(bytes.NewReader([]byte(input)), drv).Decode(&got)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.N == nil || *got.N != 42 {
		t.Fatalf("N = %v", got.N)
	}
	if got.S == nil || *got.S != "hello" {
		t.Fatalf("S = %v", got.S)
	}
	t.Logf("OK: N=%d, S=%q", *got.N, *got.S)
}

func testStreamingPointer(t *testing.T, drv *ndec.Driver) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner *Inner `json:"inner"`
		Nil   *Inner `json:"nil_field"`
		Age   int    `json:"age"`
	}

	input := `{"name":"Bob","inner":{"x":5,"y":"world"},"nil_field":null,"age":25}`

	// Use encoding/json as reference
	var want Outer
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			reader := &slowReader{data: []byte(input), chunk: chunk}
			dec := NewWithChunkSize(reader, drv, chunk)
			var got Outer
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("chunk=%d mismatch\ngot:  %+v\nwant: %+v", chunk, got, want)
			}
		})
	}
}

func testStreamingPointerComprehensive(t *testing.T, drv *ndec.Driver) {
	type Coord struct {
		X int `json:"x"`
		Y int `json:"y"`
	}

	type S struct {
		Name   string   `json:"name"`
		Coord  *Coord   `json:"coord"`
		Nil    *Coord   `json:"nil_coord"`
		Tags   []string `json:"tags"`
		PtrInt *int     `json:"ptr_int"`
		PtrStr *string  `json:"ptr_str"`
	}

	input := `{"name":"test","coord":{"x":1,"y":2},"nil_coord":null,"tags":["a","b"],"ptr_int":42,"ptr_str":"hello"}`

	var want S
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for chunk := 1; chunk <= len(input); chunk++ {
		t.Run("chunk="+itoa(chunk), func(t *testing.T) {
			reader := &slowReader{data: []byte(input), chunk: chunk}
			dec := NewWithChunkSize(reader, drv, chunk)
			var got S
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("chunk=%d error: %v", chunk, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("chunk=%d mismatch\ngot:  %+v\nwant: %+v", chunk, got, want)
			}
		})
	}
}

// Skip (unknown field) tests

// testStreamingSkipUnknownField tests that unknown JSON fields are
// correctly skipped during streaming.
func testStreamingSkipUnknownField(t *testing.T, drv *ndec.Driver) {
	// Target struct only knows "name" and "age".
	// The JSON has "extra" fields of various types that must be skipped.
	type Person struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	tests := []struct {
		name  string
		json  string
		wantN string
		wantA int
	}{
		{
			name:  "skip_string",
			json:  `{"name":"Alice","unknown_str":"this is a long string value that should be skipped","age":30}`,
			wantN: "Alice",
			wantA: 30,
		},
		{
			name:  "skip_object",
			json:  `{"name":"Bob","unknown_obj":{"nested_key":"nested_value","num":123},"age":25}`,
			wantN: "Bob",
			wantA: 25,
		},
		{
			name:  "skip_array",
			json:  `{"name":"Carol","unknown_arr":[1,2,3,"four",{"five":5},[6,7]],"age":35}`,
			wantN: "Carol",
			wantA: 35,
		},
		{
			name:  "skip_multiple",
			json:  `{"skip1":"val1","name":"Dave","skip2":{"a":1},"skip3":[1,2,3],"age":40,"skip4":true,"skip5":null}`,
			wantN: "Dave",
			wantA: 40,
		},
		{
			name:  "skip_deep_nesting",
			json:  `{"name":"Eve","deep":{"a":{"b":{"c":{"d":"deep_value"}}}},"age":45}`,
			wantN: "Eve",
			wantA: 45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes := []byte(tt.json)

			// Full buffer: should always work
			t.Run("full", func(t *testing.T) {
				var got Person
				err := New(bytes.NewReader(inputBytes), drv).Decode(&got)
				if err != nil {
					t.Fatalf("full buffer error: %v", err)
				}
				if got.Name != tt.wantN || got.Age != tt.wantA {
					t.Fatalf("got %+v, want {Name:%s Age:%d}", got, tt.wantN, tt.wantA)
				}
			})

			// Streaming: test all chunk sizes
			for chunk := 1; chunk <= len(inputBytes); chunk++ {
				t.Run("chunk="+itoa(chunk), func(t *testing.T) {
					reader := &slowReader{data: inputBytes, chunk: chunk}
					dec := NewWithChunkSize(reader, drv, chunk)
					var got Person
					if err := dec.Decode(&got); err != nil {
						t.Fatalf("chunk=%d error: %v", chunk, err)
					}
					if got.Name != tt.wantN || got.Age != tt.wantA {
						t.Fatalf("chunk=%d got %+v, want {Name:%s Age:%d}", chunk, got, tt.wantN, tt.wantA)
					}
				})
			}
		})
	}
}

// testStreamingSkipLargeValue verifies that rd_parse_to_skip (streaming)
// correctly handles progressively larger unknown values.
func testStreamingSkipLargeValue(t *testing.T, drv *ndec.Driver) {
	type S struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	chunk := 16
	for _, n := range []int{50, 100, 200, 400} {
		var bigArray string
		{
			bigArray = "["
			for i := 1; i <= n; i++ {
				if i > 1 {
					bigArray += ","
				}
				bigArray += itoa(i)
			}
			bigArray += "]"
		}

		input := `{"name":"Alice","big_unknown":` + bigArray + `,"age":30}`
		inputBytes := []byte(input)

		cr := &countingReader{data: inputBytes, chunk: chunk}
		dec := NewWithChunkSize(cr, drv, chunk)
		var got S
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("n=%d error: %v", n, err)
		}
		if got.Name != "Alice" || got.Age != 30 {
			t.Fatalf("n=%d got %+v", n, got)
		}

		t.Logf("n=%-3d  input=%-5d  big_value=%-5d  reads=%-3d  (chunk=%d)",
			n, len(input), len(bigArray), cr.reads, chunk)
	}

	t.Log("Streaming skip: reads ≈ input/chunk. Each resume processes only new data (O(C) per cycle).")
}

// Ensure unused imports are used
var _ = fmt.Sprintf
var _ = json.Unmarshal
var _ = reflect.DeepEqual
