package dom

import (
	"errors"
	"fmt"
	"testing"
	"unsafe"

	"github.com/velox-io/json/decode"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
)

// --- helpers ---

// mustParseTape parses and returns the tape view (value.Value) for tests that
// assert on tape structure directly (TagAt, StringAt, Int64At, etc.).
func mustParseTape(t *testing.T, src string) value.Value {
	t.Helper()
	p := parserPool.Get().(*Parser)
	defer parserPool.Put(p)
	v, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return v
}

func mustParseTapeZC(t *testing.T, src string) value.Value {
	t.Helper()
	p := parserPool.Get().(*Parser)
	defer parserPool.Put(p)
	padded := Pad([]byte(src))
	v, err := p.ParsePadded(padded, WithZeroCopy())
	if err != nil {
		t.Fatalf("ParsePadded(%q) zero-copy: %v", src, err)
	}
	return v
}

// contentRoot returns the tape index of the root element. With TAPE_ROOT
// removed, the root element lives at slot 0.
func contentRoot() int { return 0 }

func descriptor(v *value.Value) *valueabi.Descriptor {
	desc := valueabi.Load(unsafe.Pointer(v))
	return &desc
}

// --- Parse errors ---

func TestParseEmptyInput(t *testing.T) {
	_, err := Parse([]byte{})
	if err != decode.ErrEmptyInput {
		t.Fatalf("got %v, want ErrEmptyInput", err)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	cases := []string{
		`{`,
		`[`,
		`{"a"}`,
		`"unclosed`,
		`tru`,
		`nul`,
		`fals`,
		`[1,]`,
		`{"a":}`,
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q", c), func(t *testing.T) {
			_, err := Parse([]byte(c))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if _, ok := err.(*decode.SyntaxError); !ok {
				t.Fatalf("expected *SyntaxError, got %T: %v", err, err)
			}
		})
	}
}

// TestParseScanStrictness verifies that lax mode preserves raw string bytes and
// WithStrictScan rejects invalid UTF-8 and unescaped control bytes.
func TestParseScanStrictness(t *testing.T) {
	reject := map[string]string{
		"lone continuation":  "[\"a\x80b\"]",
		"overlong NUL":       "[\"\xc0\x80\"]",
		"lone surrogate":     "[\"\xed\xa0\x80\"]",
		"0xff":               "[\"\xff\"]",
		"truncated lead":     "[\"\xc2\"]",
		"past U+10FFFF":      "[\"\xf4\x90\x80\x80\"]",
		"5-byte lead":        "[\"\xf8\x80\x80\x80\x80\"]",
		"raw control byte":   "[\"a\x01b\"]",
		"buried in long run": "[\"" + str100('A') + "\xc0\x80" + str100('B') + "\"]",
	}
	for name, src := range reject {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src), WithStrictScan()); err == nil {
				t.Fatalf("Parse(%q, WithStrictScan) accepted; want rejection", src)
			}
			// Inside a string the lax scan passes the raw bytes through.
			v, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("lax Parse(%q) rejected: %v", src, err)
			}
			elem := v.Index(0)
			if got, ok := elem.Str(); !ok || got != string(src[2:len(src)-2]) {
				t.Fatalf("lax Parse(%q) string = %q (ok=%v); want the raw bytes preserved", src, got, ok)
			}
		})
	}

	// Outside a string an invalid byte is a malformed token the tape builder
	// rejects regardless of scan strictness.
	for _, opts := range [][]ParseOption{nil, {WithStrictScan()}} {
		if _, err := Parse([]byte("[\xff]"), opts...); err == nil {
			t.Fatalf("Parse([\\xff], opts=%v) accepted; want rejection", opts)
		}
	}

	// Negative control: strict rejection is about the byte sequence, not about
	// merely containing non-ASCII or being long.
	accept := map[string]string{
		"2-byte":            "[\"\xc2\xa9\"]",
		"3-byte CJK":        "[\"\xe4\xb8\xad\"]",
		"4-byte emoji":      "[\"\xf0\x9f\x98\x80\"]",
		"max codepoint":     "[\"\xf4\x8f\xbf\xbf\"]",
		"multibyte in long": "[\"" + str100('A') + "\xe4\xb8\xad" + str100('B') + "\"]",
	}
	for name, src := range accept {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src), WithStrictScan()); err != nil {
				t.Fatalf("Parse(%q, WithStrictScan) rejected valid UTF-8: %v", src, err)
			}
		})
	}
}

func str100(c byte) string {
	b := make([]byte, 100)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// --- Value.TagAt ---

func TestValueTagAt(t *testing.T) {
	v := mustParseTape(t, `{"s":"hello","n":42,"b":true,"f":false,"null":null,"pi":3.14}`)

	// Index 0 is the root element (TagObjBeg, no ROOT wrapper).
	if tag := descriptor(&v).TagAt(contentRoot()); tag != valueabi.TagObjBeg {
		t.Fatalf("TagAt(0)=%c, want %c (TagObjBeg)", tag, valueabi.TagObjBeg)
	}

	// Walk keys and verify value tags.
	cur := contentRoot() + 1 // first key inside object
	for tag := descriptor(&v).TagAt(cur); tag == valueabi.TagString || tag == valueabi.TagStrFree; tag = descriptor(&v).TagAt(cur) {
		key := descriptor(&v).StringAt(cur)
		valIdx := cur + 1
		valTag := descriptor(&v).TagAt(valIdx)

		switch string(key) {
		case "s":
			if valTag != valueabi.TagStrFree {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagStrFree)
			}
		case "n":
			if valTag != valueabi.TagInt64 {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagInt64)
			}
		case "b":
			if valTag != valueabi.TagTrue {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagTrue)
			}
		case "f":
			if valTag != valueabi.TagFalse {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagFalse)
			}
		case "null":
			if valTag != valueabi.TagNull {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagNull)
			}
		case "pi":
			if valTag != valueabi.TagDouble {
				t.Errorf("key %q: tag=%c, want %c", key, valTag, valueabi.TagDouble)
			}
		}
		cur = descriptor(&v).Skip(valIdx)
	}

	if tag := descriptor(&v).TagAt(cur); tag != valueabi.TagObjEnd {
		t.Fatalf("tail: TagAt(%d)=%c, want %c", cur, tag, valueabi.TagObjEnd)
	}
}

// --- Value.Skip ---

func TestValueSkipRootScalar(t *testing.T) {
	v := mustParseTape(t, `null`)
	// Root null is a single-word tag at slot 0; Skip(0) = 1.
	if got := descriptor(&v).Skip(0); got != 1 {
		t.Errorf("Skip(0) = %d, want 1 (past null tag)", got)
	}
	if got := descriptor(&v).Skip(0); got != len(descriptor(&v).Doc.Tape) {
		t.Errorf("Skip(0) = %d, want %d (tape length)", got, len(descriptor(&v).Doc.Tape))
	}
}

func TestValueSkipScalars(t *testing.T) {
	type tc struct {
		json string
		skip int // expected Skip value from content root
	}
	cases := []tc{
		{`null`, 1},
		{`true`, 1},
		{`false`, 1},
	}
	for _, c := range cases {
		t.Run(c.json, func(t *testing.T) {
			v := mustParseTape(t, c.json)
			idx := contentRoot()
			if got := descriptor(&v).Skip(idx); got != c.skip {
				t.Errorf("Skip(%d) = %d, want %d", idx, got, c.skip)
			}
		})
	}
}

func TestValueSkipNumbers(t *testing.T) {
	type tc struct {
		json string
		skip int
	}
	cases := []tc{
		{`42`, 2},   // tag + value
		{`-7`, 2},   // tag + value
		{`3.14`, 2}, // tag + value
	}
	for _, c := range cases {
		t.Run(c.json, func(t *testing.T) {
			v := mustParseTape(t, c.json)
			idx := contentRoot() // TagInt64 / TagDouble
			if got := descriptor(&v).Skip(idx); got != idx+2 {
				t.Errorf("Skip(%d) = %d, want %d", idx, got, idx+2)
			}
		})
	}
}

func TestValueSkipContainers(t *testing.T) {
	// Empty array: [ArrBeg, ArrEnd] -> Skip(0) = 2.
	v := mustParseTape(t, `[]`)
	if got := descriptor(&v).Skip(contentRoot()); got != 2 {
		t.Errorf("empty array Skip(0)=%d, want 2", got)
	}

	// Empty object.
	v = mustParseTape(t, `{}`)
	if got := descriptor(&v).Skip(contentRoot()); got != 2 {
		t.Errorf("empty object Skip(0)=%d, want 2", got)
	}

	// Non-empty array: [ArrBeg, Int64, 0, Int64, 1, Int64, 2, ArrEnd]
	// Skip(ArrBeg) = 8 = tape length.
	v = mustParseTape(t, `[0,1,2]`)
	arrIdx := contentRoot()
	if got := descriptor(&v).Skip(arrIdx); got != 8 {
		t.Errorf("[0,1,2] Skip(0)=%d, want 8", got)
	}
	// Skip past first element (Int64 at 1).
	if got := descriptor(&v).Skip(arrIdx + 1); got != arrIdx+3 {
		t.Errorf("[0,1,2] Skip(first num)=%d, want %d", got, arrIdx+3)
	}

	// Nested: [[],{"a":1}] -> 9 tape words.
	v = mustParseTape(t, `[[],{"a":1}]`)
	// Skip(nested arr) covers full tape.
	if got := descriptor(&v).Skip(contentRoot()); got != len(descriptor(&v).Doc.Tape) {
		t.Errorf("nested Skip(0)=%d, want %d", got, len(descriptor(&v).Doc.Tape))
	}
}

// --- Value.ContainerCount ---

func TestValueContainerCount(t *testing.T) {
	type tc struct {
		json  string
		count int
	}
	cases := []tc{
		{`[]`, 0},
		{`{}`, 0},
		{`[1]`, 1},
		{`[1,2,3,4,5]`, 5},
		{`{"a":1}`, 1},
		{`{"a":1,"b":2,"c":3}`, 3},
	}
	for _, c := range cases {
		t.Run(c.json, func(t *testing.T) {
			v := mustParseTape(t, c.json)
			if got := descriptor(&v).ContainerCount(contentRoot()); got != c.count {
				t.Errorf("ContainerCount(%d) = %d, want %d", contentRoot(), got, c.count)
			}
		})
	}
}

// --- Value.StringAt (StrModeCopy) ---

func TestValueStringAtCopy(t *testing.T) {
	v := mustParseTape(t, `"hello world"`)
	idx := contentRoot()
	if got := string(descriptor(&v).StringAt(idx)); got != "hello world" {
		t.Errorf("StringAt = %q, want %q", got, "hello world")
	}
}

func TestValueStringAtEscape(t *testing.T) {
	type tc struct {
		json string
		want string
	}
	cases := []tc{
		{`"a\nb"`, "a\nb"},
		{`"a\tb"`, "a\tb"},
		{`"a\"b"`, `a"b`},
		{`"a\\b"`, `a\b`},
		{`"a\/b"`, "a/b"},
		{`"\u0041"`, "A"},
		{`"\u00e9"`, "\u00e9"},
	}
	for _, c := range cases {
		t.Run(c.json, func(t *testing.T) {
			v := mustParseTape(t, c.json)
			idx := contentRoot()
			got := string(descriptor(&v).StringAt(idx))
			if got != c.want {
				t.Errorf("StringAt = %q, want %q", got, c.want)
			}
		})
	}
}

func TestValueStringAtObjectKeys(t *testing.T) {
	v := mustParseTape(t, `{"foo":"bar","baz":1}`)
	objIdx := contentRoot()
	keys := []string{"foo", "baz"}
	vals := []string{"bar", ""}
	cur := objIdx + 1 // first key
	for i, want := range keys {
		if tag := descriptor(&v).TagAt(cur); tag != valueabi.TagStrFree {
			t.Fatalf("pos %d: tag=%c, want %c", cur, tag, valueabi.TagStrFree)
		}
		if got := string(descriptor(&v).StringAt(cur)); got != want {
			t.Errorf("key[%d] = %q, want %q", i, got, want)
		}
		valIdx := cur + 1
		if i < len(vals) && vals[i] != "" {
			if got := string(descriptor(&v).StringAt(valIdx)); got != vals[i] {
				t.Errorf("val[%d] = %q, want %q", i, got, vals[i])
			}
		}
		cur = descriptor(&v).Skip(valIdx)
	}
}

// --- Value.RawStringAt (StrModeZeroCopy) ---

// TestParseRejectsZeroCopyWithoutPadded pins that zero-copy is a
// ParsePadded-only contract: Parse copies src into the Parser's reusable
// buffer, so zero-copy payloads would be corrupted by the next parse.
func TestParseRejectsZeroCopyWithoutPadded(t *testing.T) {
	p := NewParser()
	if _, err := p.Parse([]byte(`{"a":"b"}`), WithZeroCopy()); !errors.Is(err, ErrZeroCopyNeedsPadded) {
		t.Errorf("Parser.Parse: got %v, want ErrZeroCopyNeedsPadded", err)
	}
	if _, err := Parse([]byte(`{"a":"b"}`), WithZeroCopy()); !errors.Is(err, ErrZeroCopyNeedsPadded) {
		t.Errorf("Parse: got %v, want ErrZeroCopyNeedsPadded", err)
	}
}

func TestParseCopyModePublishesNoSrc(t *testing.T) {
	v, err := Parse([]byte(`{"a":"b","e":"x\ny"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if descriptor(&v).Doc.Src != nil {
		t.Error("copy-mode doc publishes Src; copy-mode tapes carry no raw strings")
	}
	if descriptor(&v).Doc.ZeroCopy {
		t.Error("copy-mode doc marked ZeroCopy")
	}
}

func TestParsePaddedZeroCopyFlagsDoc(t *testing.T) {
	v, err := ParsePadded(Pad([]byte(`{"a":"b"}`)), WithZeroCopy())
	if err != nil {
		t.Fatalf("ParsePadded: %v", err)
	}
	if !descriptor(&v).Doc.ZeroCopy {
		t.Error("zero-copy doc missing ZeroCopy flag")
	}
	if descriptor(&v).Doc.Src == nil {
		t.Error("zero-copy doc missing Src")
	}
	sv := v.Get("a")
	if s, ok := sv.Str(); !ok || s != "b" {
		t.Errorf("Str = %q, %v; want b, true", s, ok)
	}
}

func TestValueRawStringAt(t *testing.T) {
	v := mustParseTapeZC(t, `"raw zero-copy string"`)
	idx := contentRoot()
	// Zero-copy plain strings are tagged TagStrRaw, not TagString.
	tag := descriptor(&v).TagAt(idx)
	if tag != valueabi.TagStrRaw {
		t.Fatalf("expected TagStrRaw in zero-copy mode, got %c", tag)
	}
	if got := string(descriptor(&v).RawStringAt(idx)); got != "raw zero-copy string" {
		t.Errorf("RawStringAt = %q, want %q", got, "raw zero-copy string")
	}
}

func TestValueRawStringAliasing(t *testing.T) {
	src := `"alias test"`
	v := mustParseTapeZC(t, src)
	idx := contentRoot()
	raw := descriptor(&v).RawStringAt(idx)
	if len(raw) == 0 || len(descriptor(&v).Doc.Src) == 0 {
		t.Fatal("empty raw or Src")
	}
	rawPtr := uintptr(unsafe.Pointer(&raw[0]))
	srcBase := uintptr(unsafe.Pointer(&descriptor(&v).Doc.Src[0]))
	srcEnd := srcBase + uintptr(len(descriptor(&v).Doc.Src))
	if rawPtr < srcBase || rawPtr >= srcEnd {
		t.Error("RawStringAt does not alias Src")
	}
}

// --- Value.ScalarStringAt ---

func TestValueScalarStringAt(t *testing.T) {
	// Zero-copy mode: plain strings are TagStrRaw, escaped are TagString.
	v := mustParseTapeZC(t, `{"plain":"hello","esc":"a\nb"}`)
	root := contentRoot()

	// key "plain" -> TagStrRaw
	keyIdx := root + 1
	if tag := descriptor(&v).TagAt(keyIdx); tag != valueabi.TagStrRaw {
		t.Fatalf("key 'plain' tag: %c, want %c", tag, valueabi.TagStrRaw)
	}
	if got := string(descriptor(&v).ScalarStringAt(keyIdx)); got != "plain" {
		t.Errorf("ScalarStringAt(key plain) = %q, want 'plain'", got)
	}

	// value "hello" -> TagStrRaw
	valIdx := descriptor(&v).Skip(keyIdx)
	if tag := descriptor(&v).TagAt(valIdx); tag != valueabi.TagStrRaw {
		t.Fatalf("value 'hello' tag: %c, want %c", tag, valueabi.TagStrRaw)
	}
	if got := string(descriptor(&v).ScalarStringAt(valIdx)); got != "hello" {
		t.Errorf("ScalarStringAt(val hello) = %q, want 'hello'", got)
	}

	// key "esc" -> TagStrRaw (plain text, no escapes)
	keyIdx2 := descriptor(&v).Skip(valIdx)
	if tag := descriptor(&v).TagAt(keyIdx2); tag != valueabi.TagStrRaw {
		t.Fatalf("key 'esc' tag: %c, want %c", tag, valueabi.TagStrRaw)
	}
	if got := string(descriptor(&v).ScalarStringAt(keyIdx2)); got != "esc" {
		t.Errorf("ScalarStringAt(key esc) = %q, want 'esc'", got)
	}
	// value "a\nb" -> TagString (escaped)
	valIdx2 := descriptor(&v).Skip(keyIdx2)
	if tag := descriptor(&v).TagAt(valIdx2); tag != valueabi.TagString {
		t.Fatalf("value 'a\\nb' tag: %c, want %c", tag, valueabi.TagString)
	}
	if got := string(descriptor(&v).ScalarStringAt(valIdx2)); got != "a\nb" {
		t.Errorf("ScalarStringAt(val esc) = %q, want 'a\\nb'", got)
	}

	// Copy mode: the escape-free body is an arena copy tagged TagStrFree.
	v2 := mustParseTape(t, `"copy mode"`)
	idx := contentRoot()
	if tag := descriptor(&v2).TagAt(idx); tag != valueabi.TagStrFree {
		t.Fatalf("copy mode tag: %c, want %c", tag, valueabi.TagStrFree)
	}
	if got := string(descriptor(&v2).ScalarStringAt(idx)); got != "copy mode" {
		t.Errorf("ScalarStringAt(copy) = %q, want 'copy mode'", got)
	}
}

// --- Value number decoding ---

func TestValueInt64At(t *testing.T) {
	v := mustParseTape(t, `{"pos":42,"neg":-7,"zero":0}`)
	// obj at 0, key "pos" at 1, val TagInt64 at 2.
	if got := descriptor(&v).Int64At(2); got != 42 {
		t.Errorf("42: %d", got)
	}
	cur := descriptor(&v).Skip(descriptor(&v).Skip(1)) // skip key "pos" then val "42"
	if got := descriptor(&v).Int64At(cur + 1); got != -7 {
		t.Errorf("-7: %d", got)
	}
	cur = descriptor(&v).Skip(descriptor(&v).Skip(cur)) // skip key "neg" then val "-7"
	if got := descriptor(&v).Int64At(cur + 1); got != 0 {
		t.Errorf("0: %d", got)
	}
}

func TestValueUint64At(t *testing.T) {
	v := mustParseTape(t, `18446744073709551615`)
	idx := contentRoot() // TagUint64
	if tag := descriptor(&v).TagAt(idx); tag != valueabi.TagUint64 {
		t.Fatalf("tag: %c, want %c", tag, valueabi.TagUint64)
	}
	if got := descriptor(&v).Uint64At(idx); got != 18446744073709551615 {
		t.Errorf("got %d, want 18446744073709551615", got)
	}
}

func TestValueDoubleAt(t *testing.T) {
	v := mustParseTape(t, `{"pi":3.14159,"neg":-2.5,"sci":1e10}`)
	// obj at 0, key "pi" at 1, val Double at 2.
	if got := descriptor(&v).DoubleAt(2); got != 3.14159 {
		t.Errorf("3.14159: %f", got)
	}
	cur := descriptor(&v).Skip(descriptor(&v).Skip(1))
	if got := descriptor(&v).DoubleAt(cur + 1); got != -2.5 {
		t.Errorf("-2.5: %f", got)
	}
	cur = descriptor(&v).Skip(descriptor(&v).Skip(cur))
	if got := descriptor(&v).DoubleAt(cur + 1); got != 1e10 {
		t.Errorf("1e10: %f", got)
	}
}

// --- Parser reuse ---

func TestParserReuse(t *testing.T) {
	docs := []string{
		`{"a":1,"b":"hello"}`,
		`[true,false,null,42]`,
		`{"nested":{"deep":{"x":9.9}}}`,
	}
	for _, src := range docs {
		t.Run(fmt.Sprintf("%q", src), func(t *testing.T) {
			p := parserPool.Get().(*Parser)
			defer parserPool.Put(p)
			v, err := p.Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			_ = descriptor(&v).Doc.Tape
			_ = descriptor(&v).Doc.StrArena
		})
	}
}

// --- Tape round-trip ---

func TestValueRoundTrip(t *testing.T) {
	src := `{"name":"alice","age":30,"active":true,"scores":[9.5,8.2],"addr":{"city":"NYC","zip":10001}}`
	v := mustParseTape(t, src)

	var out []byte
	var walk func(idx int)
	walk = func(idx int) {
		switch descriptor(&v).TagAt(idx) {
		case valueabi.TagObjBeg:
			out = append(out, '{')
			n := descriptor(&v).ContainerCount(idx)
			cur := idx + 1
			for i := range n {
				if i > 0 {
					out = append(out, ',')
				}
				key := descriptor(&v).ScalarStringAt(cur)
				out = append(out, '"')
				out = append(out, key...)
				out = append(out, '"')
				out = append(out, ':')
				cur = descriptor(&v).Skip(cur) // skip key to value
				walk(cur)                      // walk the value
				cur = descriptor(&v).Skip(cur) // advance past value to next key
			}
			out = append(out, '}')
		case valueabi.TagArrBeg:
			out = append(out, '[')
			n := descriptor(&v).ContainerCount(idx)
			cur := idx + 1
			for i := range n {
				if i > 0 {
					out = append(out, ',')
				}
				walk(cur)
				cur = descriptor(&v).Skip(cur)
			}
			out = append(out, ']')
		case valueabi.TagString, valueabi.TagStrRaw, valueabi.TagStrFree:
			out = append(out, '"')
			out = append(out, descriptor(&v).ScalarStringAt(idx)...)
			out = append(out, '"')
		case valueabi.TagInt64:
			out = fmt.Appendf(out, "%d", descriptor(&v).Int64At(idx))
		case valueabi.TagUint64:
			out = fmt.Appendf(out, "%d", descriptor(&v).Uint64At(idx))
		case valueabi.TagDouble:
			out = fmt.Appendf(out, "%g", descriptor(&v).DoubleAt(idx))
		case valueabi.TagTrue:
			out = append(out, "true"...)
		case valueabi.TagFalse:
			out = append(out, "false"...)
		case valueabi.TagNull:
			out = append(out, "null"...)
		}
	}

	// Start at the root element (slot 0, no wrapper).
	walk(0)

	// Re-parse the reconstructed JSON.
	v2 := mustParseTape(t, string(out))

	t.Logf("reconstructed JSON: %s", string(out))
	t.Logf("original tape: %d words, reconstructed tape: %d words", len(descriptor(&v).Doc.Tape), len(descriptor(&v2).Doc.Tape))

	if descriptor(&v).TagAt(0) != descriptor(&v2).TagAt(0) {
		t.Fatal("root tag mismatch")
	}
	for i := 0; i < len(descriptor(&v).Doc.Tape) && i < len(descriptor(&v2).Doc.Tape); i++ {
		if descriptor(&v).TagAt(i) != descriptor(&v2).TagAt(i) {
			t.Errorf("tag at [%d]: %c vs %c", i, descriptor(&v).TagAt(i), descriptor(&v2).TagAt(i))
		}
	}
	if len(descriptor(&v).Doc.Tape) != len(descriptor(&v2).Doc.Tape) {
		t.Errorf("tape length mismatch: %d vs %d", len(descriptor(&v).Doc.Tape), len(descriptor(&v2).Doc.Tape))
	}
}

// --- Navigation smoke tests on Parse (Value) ---

func TestParseNavigation(t *testing.T) {
	d, err := Parse([]byte(`{"id":"u-1","n":7,"tags":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Type(); got != value.KindObject {
		t.Errorf("Type() = %v, want object", got)
	}
	if n := d.Len(); n != 3 {
		t.Errorf("Len() = %d, want 3", n)
	}
	if got, ok := d.GetString("id"); !ok || got != "u-1" {
		t.Errorf(`GetString("id") = %q, %v; want "u-1"`, got, ok)
	}
	if got, ok := d.GetInt("n"); !ok || got != 7 {
		t.Errorf(`GetInt("n") = %d, %v; want 7`, got, ok)
	}
	tags := d.Get("tags")
	if got := tags.Type(); got != value.KindArray {
		t.Errorf("tags Type() = %v, want array", got)
	}
	var collected []string
	tags.ForEachElem(func(_ int, e Value) bool {
		s, _ := e.Str()
		collected = append(collected, s)
		return true
	})
	if len(collected) != 2 || collected[0] != "a" || collected[1] != "b" {
		t.Errorf("tags = %v, want [a b]", collected)
	}
}
