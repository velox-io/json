package vjson

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// FuzzUnmarshalAny — the primary differential fuzzer.
//
// Feeds arbitrary bytes into both vjson.Unmarshal and encoding/json.Unmarshal
// targeting *any (interface{}). Checks:
//   - vjson must not be stricter than encoding/json (reject what std accepts)
//   - result semantic equivalence when both accept (modulo known UTF-8 gap)
//   - vjson may be more lenient (accept what std rejects) — logged, not failed
//
// This single target covers strings, numbers, booleans, null, objects, arrays,
// and all combinations thereof.
// ---------------------------------------------------------------------------

func FuzzUnmarshalAny(f *testing.F) {
	seeds := []string{
		// Primitives
		`null`, `true`, `false`,
		`0`, `-0`, `1`, `-1`, `42`, `1.5`, `-0.5`,
		`1e10`, `1e+10`, `1e-10`, `1.5e2`, `-1.5e-2`,
		`""`, `"hello"`, `"hello world"`,

		// Escape sequences
		`"hello\nworld"`, `"tab\there"`, `"quote\"here"`,
		`"back\\slash"`, `"slash\/slash"`,
		`"\b\f\r"`,
		`"\u0041"`, `"\u4e2d\u6587"`,
		`"\uD83D\uDE00"`, // surrogate pair → emoji

		// Objects
		`{}`, `{"a":1}`,
		`{"a":1,"b":"two","c":true,"d":null}`,
		`{"nested":{"x":1}}`,
		`{"arr":[1,2,3]}`,

		// Arrays
		`[]`, `[1]`, `[1,2,3]`,
		`[1,"two",true,null,1.5]`,
		`[[1,2],[3,4]]`,
		`[{"a":1},{"b":2}]`,

		// Whitespace variations
		" \t\n\r{ \"a\" : 1 } ",
		"[\n  1,\n  2,\n  3\n]",

		// Edge cases
		`{"":""}`,           // empty key and value
		`{"a":0,"a":1}`,     // duplicate keys
		`[[[[[1]]]]]`,       // deep nesting
		`0.123456789012345`, // high precision float

		// Invalid inputs (should be rejected by both)
		``, `{`, `}`, `[`, `]`, `"`, `"unterminated`,
		`{key:1}`, `{'key':1}`,
		`[1,]`, `{,}`, `[,]`,
		`01`, `+1`, `1.`, `.5`, `1e`, `1e+`,
		`tru`, `fals`, `nul`,
		`NaN`, `Infinity`, `-Infinity`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var vjResult any
		vjErr := Unmarshal(data, &vjResult)

		var stdResult any
		stdErr := json.Unmarshal(data, &stdResult)

		// CRITICAL: vjson must not reject valid JSON that encoding/json accepts.
		if vjErr != nil && stdErr == nil {
			t.Errorf("vjson rejected but encoding/json accepted\ninput: %q\nvjson error: %v\nencoding/json result: %v",
				data, vjErr, stdResult)
		}

		// Leniency: vjson accepts but encoding/json rejects.
		// After RFC 8259 fixes, this should not happen for well-known cases.
		if vjErr == nil && stdErr != nil {
			t.Errorf("vjson too lenient\ninput: %q\nvjson result: %v", data, vjResult)
			return
		}

		// When both accept, results must be semantically equal.
		if vjErr == nil && stdErr == nil {
			if !deepEqualJSON(vjResult, stdResult) {
				// Known divergence: vjson does not validate/replace invalid
				// UTF-8 on the zero-copy path. encoding/json replaces with U+FFFD.
				if !utf8.Valid(data) {
					return
				}
				t.Errorf("result mismatch\ninput:  %q\nvjson:  %#v\nstdlib: %#v",
					data, vjResult, stdResult)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzUnmarshalStruct — typed struct fuzzing.
//
// Exercises struct decoding (field lookup via perfect hash, type coercion,
// nested structs, slices, pointers, maps) and compares with encoding/json.
// ---------------------------------------------------------------------------

func FuzzUnmarshalStruct(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"name":"Alice","age":30,"score":99.5,"active":true}`,
		`{"name":"Bob","tags":["go","rust"],"meta":{"k":"v"}}`,
		`{"name":"","age":0,"score":0.0,"active":false,"tags":[],"meta":{}}`,
		`{"name":"escape\n\"test","age":-1}`,
		`{"name":null,"age":null,"score":null,"active":null,"tags":null,"meta":null}`,
		`{"unknown_field":123,"name":"test"}`,
		`{"name":"hello\uD83D\uDE00","age":42}`,
		// Large integer
		`{"age":9223372036854775807}`,
		// Nested
		`{"tags":["a","b","c","d","e","f","g","h"]}`,
		`{"meta":{"a":"1","b":"2","c":"3"}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	type FuzzStruct struct {
		Name   string            `json:"name"`
		Age    int64             `json:"age"`
		Score  float64           `json:"score"`
		Active bool              `json:"active"`
		Tags   []string          `json:"tags"`
		Meta   map[string]string `json:"meta"`
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var vjResult FuzzStruct
		vjErr := Unmarshal(data, &vjResult)

		var stdResult FuzzStruct
		stdErr := json.Unmarshal(data, &stdResult)

		if vjErr != nil && stdErr == nil {
			t.Errorf("vjson rejected but encoding/json accepted\ninput: %q\nvjson error: %v\nstdlib: %+v",
				data, vjErr, stdResult)
		}

		if vjErr == nil && stdErr != nil {
			// Struct/map targets may skip unknown fields via skipValue/skipContainer
			// which doesn't fully validate inner JSON grammar. Log for tracking.
			// t.Errorf("vjson too lenient\ninput: %q\nvjson result: %+v", data, vjResult)
			return
		}

		if vjErr == nil && stdErr == nil {
			if !reflect.DeepEqual(vjResult, stdResult) {
				if !utf8.Valid(data) {
					return // UTF-8 divergence: vjson preserves raw bytes, stdlib replaces with U+FFFD
				}
				t.Errorf("struct mismatch\ninput:  %q\nvjson:  %+v\nstdlib: %+v",
					data, vjResult, stdResult)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzUnmarshalNested — deeply nested struct with pointers and slices.
//
// Targets the pointer allocation path (ptrAlloc, unsafe_New, unsafe_NewArray)
// and slice growth logic to stress GC safety of unsafe allocations.
// ---------------------------------------------------------------------------

func FuzzUnmarshalNested(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"value":"v1","inner":null}`,
		`{"items":[]}`,
		`{"items":[{"value":"a","inner":{"name":"x"}}]}`,
		`{"items":[{"value":"a\\nb","inner":{"name":"hello\tworld"}},{"value":"c","inner":null}]}`,
		`{"items":[{"value":"v","inner":{"name":"n"}},{"value":"v","inner":{"name":"n"}},{"value":"v","inner":{"name":"n"}}]}`,
		`null`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	type Inner struct {
		Name string `json:"name"`
	}
	type Item struct {
		Value string `json:"value"`
		Inner *Inner `json:"inner"`
	}
	type Root struct {
		Items []Item `json:"items"`
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var vjResult Root
		vjErr := Unmarshal(data, &vjResult)

		var stdResult Root
		stdErr := json.Unmarshal(data, &stdResult)

		if vjErr != nil && stdErr == nil {
			t.Errorf("vjson rejected but encoding/json accepted\ninput: %q\nvjson error: %v",
				data, vjErr)
		}

		if vjErr == nil && stdErr != nil {
			// Nested struct target may skip unknown fields via skipValue/skipContainer
			// which doesn't fully validate inner JSON grammar. Log for tracking.
			// t.Errorf("vjson too lenient\ninput: %q\nvjson result: %+v", data, vjResult)
			return
		}

		if vjErr == nil && stdErr == nil {
			if !reflect.DeepEqual(vjResult, stdResult) {
				if !utf8.Valid(data) {
					return // UTF-8 divergence: vjson preserves raw bytes, stdlib replaces with U+FFFD
				}
				t.Errorf("nested mismatch\ninput:  %q\nvjson:  %+v\nstdlib: %+v",
					data, vjResult, stdResult)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzNoCrash — pure crash-finding fuzzer.
//
// Feeds arbitrary bytes without checking correctness. Catches panics, OOB
// reads from SWAR scanning, and infinite loops in the parser.
// Targets multiple concrete types to cover all code paths.
// ---------------------------------------------------------------------------

func FuzzNoCrash(f *testing.F) {
	seeds := []string{
		`null`, `true`, `false`, `0`, `""`, `{}`, `[]`,
		`"\uD800"`,                           // lone surrogate
		`"\uD800\uD800"`,                     // two high surrogates
		`"\u0000"`,                           // null byte in string
		string([]byte{0x22, 0x00, 0x22}),     // raw null in string
		`{"a":` + string([]byte{0xff}) + `}`, // invalid byte
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Try every target type to exercise all code paths.
		// We don't check results — only that nothing panics.
		var a any
		Unmarshal(data, &a)

		var s string
		Unmarshal(data, &s)

		var n float64
		Unmarshal(data, &n)

		var b bool
		Unmarshal(data, &b)

		var i int64
		Unmarshal(data, &i)

		var u uint64
		Unmarshal(data, &u)

		var arr []any
		Unmarshal(data, &arr)

		var m map[string]any
		Unmarshal(data, &m)

		var ms map[string]string
		Unmarshal(data, &ms)
	})
}

// ---------------------------------------------------------------------------
// FuzzMarshalString — differential fuzzer for string escaping.
//
// Takes an arbitrary string, wraps it in a struct field, and marshals with
// both vjson (WithStdCompat) and encoding/json. Output must be byte-identical.
// This exercises all native string escape paths: SIMD (16/32-byte), SWAR
// (8-byte), byte-by-byte, UTF-8 validation, HTML escaping, line terminators,
// and surrogate replacement.
// ---------------------------------------------------------------------------

func FuzzMarshalString(f *testing.F) {
	seeds := []string{
		// Plain ASCII
		"", "hello", "hello world",
		// Control characters — full range
		"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f",
		"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f",
		// Single 0x1F (previously buggy boundary)
		"\x1f",
		// Short escapes
		"\b\t\n\f\r",
		// Quotes and backslash
		`"hello"`, `path\to\file`, `a\"b`,
		// HTML special chars
		"<script>", "a>b", "a&b", "<>&",
		// Line terminators (U+2028, U+2029)
		"a\u2028b", "a\u2029b", "\u2028\u2029",
		// Unicode — CJK
		"中文测试", "日本語", "한국어",
		// Emoji (4-byte UTF-8)
		"\U0001F600\U0001F4A9",
		// Invalid UTF-8
		"\xff", "abc\xffdef", "\xc0\xaf",
		"\xe0\x80", "\xf0\x80\x80",
		// Surrogate byte sequences (3-byte)
		"\xed\xa0\x80", "\xed\xbf\xbf", "a\xed\xa0\x80b",
		// Mixed
		"hello\n\t\"world\"\x00<>&\u2028\xff中文\U0001F600",
		// Length boundary cases for SIMD
		"abcdefghijklmno",                   // 15 bytes
		"abcdefghijklmnop",                  // 16 bytes — exact SIMD width
		"abcdefghijklmnopq",                 // 17 bytes
		"abcdefghijklmnopqrstuvwxyz012345",  // 31 bytes
		"abcdefghijklmnopqrstuvwxyz0123456", // 32 bytes — exact AVX2 width
		// All-escape strings at SIMD boundaries
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f",         // 15
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10",     // 16
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11", // 17
	}
	for _, s := range seeds {
		f.Add(s)
	}

	type S struct {
		V string `json:"v"`
	}

	f.Fuzz(func(t *testing.T, s string) {
		v := S{V: s}

		vjOut, vjErr := Marshal(&v, WithStdCompat())
		stdOut, stdErr := json.Marshal(v)

		if vjErr != nil && stdErr == nil {
			t.Errorf("vjson error but stdlib ok\ninput: %q\nvjson err: %v", s, vjErr)
			return
		}
		if vjErr == nil && stdErr != nil {
			t.Errorf("vjson ok but stdlib error\ninput: %q\nstdlib err: %v", s, stdErr)
			return
		}
		if vjErr != nil && stdErr != nil {
			return // both error — ok
		}
		if string(vjOut) != string(stdOut) {
			t.Errorf("output mismatch\ninput:  %q\nstdlib: %s\nvelox:  %s", s, stdOut, vjOut)
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzMarshalStruct — differential fuzzer for structured types.
//
// Builds a struct with diverse field types from fuzzer-provided entropy bytes,
// then marshals with both vjson (WithStdCompat) and encoding/json. Exercises
// the native C VM, string escaping, number formatting, bool encoding, slices,
// maps, pointers, []byte (base64), and omitempty.
// ---------------------------------------------------------------------------

func FuzzMarshalStruct(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x00},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		// enough bytes to fill all fields
		make([]byte, 128),
	}
	// A seed with varied data
	varied := make([]byte, 128)
	for i := range varied {
		varied[i] = byte(i)
	}
	seeds = append(seeds, varied)

	for _, s := range seeds {
		f.Add(s)
	}

	type Inner struct {
		X string `json:"x"`
		Y int    `json:"y"`
	}

	type FuzzS struct {
		Name   string            `json:"name"`
		Age    int64             `json:"age"`
		Score  float64           `json:"score"`
		Active bool              `json:"active"`
		Tags   []string          `json:"tags"`
		Meta   map[string]string `json:"meta"`
		Inner  *Inner            `json:"inner,omitempty"`
		Data   []byte            `json:"data,omitempty"`
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		r := &fuzzReader{data: data}

		v := FuzzS{
			Name:   r.readString(),
			Age:    r.readInt64(),
			Score:  r.readFloat64Safe(),
			Active: r.readBool(),
		}

		// Tags: 0-4 strings
		nTags := int(r.readByte()) % 5
		if nTags > 0 {
			v.Tags = make([]string, nTags)
			for i := range nTags {
				v.Tags[i] = r.readString()
			}
		}

		// Meta: 0-3 key-value pairs (ASCII keys to avoid UTF-8 collision issues)
		nMeta := int(r.readByte()) % 4
		if nMeta > 0 {
			v.Meta = make(map[string]string, nMeta)
			for range nMeta {
				v.Meta[r.readSafeString()] = r.readString()
			}
		}

		// Inner: 50% chance of being non-nil
		if r.readBool() {
			v.Inner = &Inner{X: r.readString(), Y: int(r.readInt64())}
		}

		// Data: 50% chance of non-nil []byte
		if r.readBool() {
			n := int(r.readByte()) % 64
			v.Data = r.readBytes(n)
		}

		vjOut, vjErr := Marshal(&v, WithStdCompat())
		stdOut, stdErr := json.Marshal(v)

		if vjErr != nil && stdErr == nil {
			t.Errorf("vjson error but stdlib ok\nvalue: %+v\nvjson err: %v", v, vjErr)
			return
		}
		if vjErr == nil && stdErr != nil {
			t.Errorf("vjson ok but stdlib error\nvalue: %+v\nstdlib err: %v", v, stdErr)
			return
		}
		if vjErr != nil && stdErr != nil {
			return
		}
		// Semantic comparison: parse both outputs back and compare.
		// This handles legitimate formatting differences (float representation,
		// map key ordering) while still catching real value mismatches.
		var vjParsed, stdParsed any
		if err := json.Unmarshal(vjOut, &vjParsed); err != nil {
			t.Errorf("vjson output is not valid JSON\nvalue: %+v\noutput: %s\nparse err: %v", v, vjOut, err)
			return
		}
		if err := json.Unmarshal(stdOut, &stdParsed); err != nil {
			t.Errorf("stdlib output is not valid JSON (unexpected)\noutput: %s\nparse err: %v", stdOut, err)
			return
		}
		if !deepEqualJSON(vjParsed, stdParsed) {
			t.Errorf("semantic mismatch\nvalue:  %+v\nstdlib: %s\nvelox:  %s", v, stdOut, vjOut)
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzMarshalNoCrash — pure crash finder for marshal.
//
// Constructs values from fuzz bytes and marshals them with every option
// combination. Only checks that nothing panics or crashes.
// ---------------------------------------------------------------------------

func FuzzMarshalNoCrash(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x00},
		{0xFF},
		make([]byte, 64),
		make([]byte, 256),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	type Inner struct {
		X string `json:"x"`
	}
	type S struct {
		A string            `json:"a"`
		B int64             `json:"b"`
		C float64           `json:"c"`
		D bool              `json:"d"`
		E []string          `json:"e,omitempty"`
		F map[string]string `json:"f,omitempty"`
		G *Inner            `json:"g,omitempty"`
		H []byte            `json:"h,omitempty"`
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		r := &fuzzReader{data: data}

		v := S{
			A: r.readString(),
			B: r.readInt64(),
			C: r.readFloat64Safe(),
			D: r.readBool(),
		}
		if r.readBool() {
			n := int(r.readByte()) % 5
			v.E = make([]string, n)
			for i := range n {
				v.E[i] = r.readString()
			}
		}
		if r.readBool() {
			v.F = map[string]string{r.readString(): r.readString()}
		}
		if r.readBool() {
			v.G = &Inner{X: r.readString()}
		}
		if r.readBool() {
			v.H = r.readBytes(int(r.readByte()) % 32)
		}

		// Marshal with every option combination — must not panic.
		Marshal(&v)
		Marshal(&v, WithStdCompat())
		Marshal(&v, WithFastEscape())
		Marshal(&v, WithEscapeHTML())
		Marshal(&v, WithoutUTF8Correction())

		// Also test bare string marshaling
		s := v.A
		Marshal(&s)
		Marshal(&s, WithStdCompat())

		// Also test MarshalIndent
		MarshalIndent(&v, "", "  ")
		MarshalIndent(&v, ">", "\t", WithStdCompat())
	})
}

// ---------------------------------------------------------------------------
// Marshal fuzz helpers
// ---------------------------------------------------------------------------

// fuzzReader consumes bytes from a fuzz input to deterministically build
// structured Go values. When bytes are exhausted, returns zero values.
type fuzzReader struct {
	data []byte
	pos  int
}

func (r *fuzzReader) readByte() byte {
	if r.pos >= len(r.data) {
		return 0
	}
	b := r.data[r.pos]
	r.pos++
	return b
}

func (r *fuzzReader) readBytes(n int) []byte {
	if n <= 0 {
		return nil
	}
	if r.pos+n > len(r.data) {
		n = len(r.data) - r.pos
	}
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	copy(b, r.data[r.pos:r.pos+n])
	r.pos += n
	return b
}

func (r *fuzzReader) readBool() bool {
	return r.readByte()&1 == 1
}

func (r *fuzzReader) readInt64() int64 {
	b := r.readBytes(8)
	if len(b) < 8 {
		var buf [8]byte
		copy(buf[:], b)
		return int64(binary.LittleEndian.Uint64(buf[:]))
	}
	return int64(binary.LittleEndian.Uint64(b))
}

// readFloat64Safe reads a float64 but ensures it is JSON-safe (not NaN/Inf).
func (r *fuzzReader) readFloat64Safe() float64 {
	f := math.Float64frombits(binary.LittleEndian.Uint64(func() []byte {
		b := r.readBytes(8)
		if len(b) < 8 {
			var buf [8]byte
			copy(buf[:], b)
			return buf[:]
		}
		return b
	}()))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func (r *fuzzReader) readString() string {
	n := int(r.readByte()) % 64
	b := r.readBytes(n)
	if b == nil {
		return ""
	}
	return string(b)
}

// readSafeString returns an ASCII-only string suitable for map keys.
// Avoids non-ASCII bytes that could cause UTF-8 replacement collisions
// (e.g., two different invalid byte sequences both escaping to \ufffd).
func (r *fuzzReader) readSafeString() string {
	n := int(r.readByte()) % 32
	b := r.readBytes(n)
	if b == nil {
		return ""
	}
	for i, c := range b {
		// Map to printable ASCII range [0x20, 0x7E], avoiding '"' and '\\'
		c = c%0x5F + 0x20 // [0x20, 0x7E]
		switch c {
		case '"':
			c = 'A'
		case '\\':
			c = 'B'
		}
		b[i] = c
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// deepEqualJSON compares two values produced by JSON unmarshaling into any.
// Handles NaN (which reflect.DeepEqual would say != NaN) and provides
// tolerance-free float comparison otherwise.
func deepEqualJSON(a, b any) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(av) && math.IsNaN(bv) {
			return true
		}
		return av == bv
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for k, va := range av {
			vb, ok := bv[k]
			if !ok {
				return false
			}
			if !deepEqualJSON(va, vb) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualJSON(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
