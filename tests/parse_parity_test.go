package tests

// Parity pins against encoding/json for inputs whose error presence, decoded
// value, or stream behavior once diverged. assertSameDecode compares
// Unmarshal results; assertSameStream runs the same contract through Decoder
// over a chunked reader so tokens and escapes can straddle read boundaries at
// every alignment.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// chunkReader serves data in fixed-size chunks so escapes and tokens can be
// made to straddle arbitrary internal buffer boundaries, and so Read may
// return fewer bytes than the caller asked for.
type chunkReader struct {
	data []byte
	pos  int
	size int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.size
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	copy(p[:n], r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

// dbgAny renders a decode destination for failure messages, following
// pointers so new(any) destinations print their content instead of an address.
func dbgAny(v any) string {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	return fmt.Sprintf("%+v", rv.Interface())
}

// assertSameDecode pins that vjson and encoding/json agree on error presence
// and, on success, on the decoded value.
func assertSameDecode(t *testing.T, name string, data []byte, mk func() any) {
	t.Helper()
	stdV, vjV := mk(), mk()
	stdErr := json.Unmarshal(data, stdV)
	vjErr := vjson.Unmarshal(data, vjV)
	if (stdErr == nil) != (vjErr == nil) {
		t.Errorf("%s: error divergence: std=%v vjson=%v", name, stdErr, vjErr)
		return
	}
	if stdErr != nil {
		return
	}
	if !reflect.DeepEqual(stdV, vjV) {
		t.Errorf("%s: value divergence:\n  std:   %+v\n  vjson: %+v", name, dbgAny(stdV), dbgAny(vjV))
	}
}

// assertSameStream pins the same contract through Decoder over a chunked reader.
func assertSameStream(t *testing.T, name string, data []byte, chunk int, mk func() any) {
	t.Helper()
	stdV, vjV := mk(), mk()
	stdErr := json.NewDecoder(&chunkReader{data: data, size: chunk}).Decode(stdV)
	vjErr := vjson.NewDecoder(&chunkReader{data: data, size: chunk}).Decode(vjV)
	if (stdErr == nil) != (vjErr == nil) {
		t.Errorf("%s (chunk=%d): error divergence: std=%v vjson=%v", name, chunk, stdErr, vjErr)
		return
	}
	if stdErr != nil {
		return
	}
	if !reflect.DeepEqual(stdV, vjV) {
		t.Errorf("%s (chunk=%d): value divergence:\n  std:   %+v\n  vjson: %+v", name, chunk, dbgAny(stdV), dbgAny(vjV))
	}
}

// ---------------------------------------------------------------------------
// Strings and escapes
// ---------------------------------------------------------------------------

// Struct tags holding non-ASCII characters match their JSON keys.
func TestUnmarshal_NonASCIITagKey(t *testing.T) {
	type doc struct {
		Msg string `json:"Сообщение"`
	}
	assertSameDecode(t, "non-ascii tag key", []byte(`{"Сообщение":"Текст"}`), func() any {
		return new(doc)
	})
}

// A truncated \u escape errors instead of producing a wrong value.
func TestUnmarshal_InvalidUnicodeEscape(t *testing.T) {
	assertSameDecode(t, "bare \\u escape", []byte(`["\u","A"]`), func() any {
		return new([]string)
	})
	assertSameDecode(t, "short \\u escape", []byte(`["\u00"]`), func() any {
		return new([]string)
	})
}

// A value ending in an escaped backslash is valid JSON and decodes; an
// invalid escape control errors.
func TestUnmarshal_EscapedBackslashAtValueEnd(t *testing.T) {
	data := []byte(`{"c":"\\"}`)
	if !json.Valid(data) {
		t.Fatal("precondition: std says document is valid")
	}
	if vjson.Valid(data) != json.Valid(data) {
		t.Errorf("escaped backslash: Valid divergence: std=%v vjson=%v", json.Valid(data), vjson.Valid(data))
	}
	assertSameDecode(t, "escaped backslash", data, func() any {
		return new(map[string]json.RawMessage)
	})
	assertSameDecode(t, "invalid escape control", []byte(`{"c":"\\a"}`), func() any {
		return new(map[string]json.RawMessage)
	})
}

// Escape sequences straddling a read boundary survive every chunk alignment.
// The value ends with an escaped quote, an escaped backslash and a unicode
// escape near the 512 byte mark.
func TestDecoder_EscapeAcrossChunkBoundary(t *testing.T) {
	type doc struct {
		Data string `json:"data"`
	}
	data := []byte(`{"data":"` + strings.Repeat("-", 500) + `\"` + `\\` + `\u55f7` + `"}`)
	assertSameDecode(t, "escape boundary direct", data, func() any { return new(doc) })
	for _, chunk := range []int{1, 7, 64, 128, 505, 509, 510, 511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			assertSameStream(t, "escape boundary", data, chunk, func() any { return new(doc) })
		})
	}
}

// A unicode escape at the very end of a long string near a read boundary
// decodes through the stream decoder.
func TestDecoder_UnicodeEscapeNearBufferEnd(t *testing.T) {
	data := []byte(`{"嗷嗷":["` + strings.Repeat("a", 400) + `\u55f7"]}`)
	assertSameDecode(t, "unicode escape end direct", data, func() any { return new(any) })
	for _, chunk := range []int{1, 512, 1024, 4096} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			assertSameStream(t, "unicode escape end", data, chunk, func() any { return new(any) })
		})
	}
}

// Invalid UTF-8 bytes inside strings pass through verbatim in values and map
// keys alike, and WithStrictScan rejects them. This is a deliberate divergence
// from encoding/json, which replaces each invalid byte with U+FFFD. Raw bytes
// are assembled by concatenation, not backticks, so they stay real 0xe2
// bytes.
func TestUnmarshal_InvalidUTF8RawPassthrough(t *testing.T) {
	data := []byte("\"" + strings.Repeat("0", 1000) + strings.Repeat("\xe2", 300) + "00\"")
	want := strings.Repeat("0", 1000) + strings.Repeat("\xe2", 300) + "00"

	var got any
	if err := vjson.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid utf8 direct: %v", err)
	}
	if s, ok := got.(string); !ok || s != want {
		t.Fatalf("invalid utf8 direct: got %q (string=%v); want the raw bytes preserved", got, ok)
	}

	var m map[string]int
	if err := vjson.Unmarshal([]byte("{\"k\xff\":1}"), &m); err != nil {
		t.Fatalf("invalid utf8 map key: %v", err)
	}
	if _, ok := m["k\xff"]; !ok {
		t.Fatalf("invalid utf8 map key: keys %v; want the raw key preserved", m)
	}

	// Valid accepts malformed UTF-8 in strings exactly as encoding/json does.
	if vjson.Valid(data) != json.Valid(data) {
		t.Errorf("invalid utf8: Valid divergence: std=%v vjson=%v", json.Valid(data), vjson.Valid(data))
	}

	// Streaming preserves the raw bytes at every chunk alignment.
	for _, chunk := range []int{1, 1024} {
		var sv any
		if err := vjson.NewDecoder(&chunkReader{data: data, size: chunk}).Decode(&sv); err != nil {
			t.Fatalf("invalid utf8 stream chunk=%d: %v", chunk, err)
		}
		if s, ok := sv.(string); !ok || s != want {
			t.Fatalf("invalid utf8 stream chunk=%d: got %q; want the raw bytes preserved", chunk, sv)
		}
	}
	var sm map[string]int
	if err := vjson.NewDecoder(&chunkReader{data: []byte("{\"k\xff\":1}"), size: 1}).Decode(&sm); err != nil {
		t.Fatalf("invalid utf8 map key stream: %v", err)
	}
	if _, ok := sm["k\xff"]; !ok {
		t.Fatalf("invalid utf8 map key stream: keys %v; want the raw key preserved", sm)
	}

	// Strict scan rejects the same input.
	if err := vjson.Unmarshal(data, vjson.WithStrictScan()); err == nil {
		t.Fatal("invalid utf8 strict: accepted; want rejection")
	}
}

// A ,string payload containing escapes unquotes before conversion.
func TestUnmarshal_StringTagEscapedPayload(t *testing.T) {
	type doc struct {
		F0 string `json:"S,string"`
	}
	assertSameDecode(t, "string tag escapes", []byte(`{"S":"\"\u0026\""}`), func() any { return new(doc) })
	assertSameDecode(t, "string tag quoted text", []byte(`{"S":"\"abc\""}`), func() any { return new(doc) })
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

// Trailing garbage after a number is rejected at every nesting depth.
func TestUnmarshal_TrailingGarbageAfterNumber(t *testing.T) {
	for _, in := range []string{"1e2e3", "[1e2e3]", `{"a":1e2e3}`} {
		assertSameDecode(t, "trailing garbage "+in, []byte(in), func() any { return new(any) })
	}
}

// Float64 literals decode to the identical bit pattern.
func TestUnmarshal_Float64Precision(t *testing.T) {
	type doc struct {
		Test float64 `json:"test"`
	}
	data := []byte(`{"test":0.6667}`)
	assertSameDecode(t, "float64 map", data, func() any { return new(map[string]any) })
	assertSameDecode(t, "float64 struct", data, func() any { return new(doc) })
	assertSameDecode(t, "float64 scalar", []byte(`0.6667`), func() any { return new(float64) })
}

// Float32 fields parse at float32 precision directly rather than rounding a
// float64 result.
func TestUnmarshal_Float32Precision(t *testing.T) {
	type doc struct {
		F float32 `json:"f"`
	}
	data := []byte(`7.328900098800659`)
	assertSameDecode(t, "float32 scalar", data, func() any { return new(float32) })
	assertSameDecode(t, "float32 struct", []byte(`{"f":7.328900098800659}`), func() any {
		return new(doc)
	})
	assertSameDecode(t, "float32 map", []byte(`{"f":7.328900098800659}`), func() any {
		return new(map[string]float32)
	})

	var std, vj float32
	if err := json.Unmarshal(data, &std); err != nil {
		t.Fatal(err)
	}
	if err := vjson.Unmarshal(data, &vj); err != nil {
		t.Fatal(err)
	}
	if math.Float32bits(std) != math.Float32bits(vj) {
		t.Errorf("float32 bits: divergence: std=%08x vjson=%08x", math.Float32bits(std), math.Float32bits(vj))
	}
}

// A quoted number under ,string follows strconv.ParseFloat's grammar:
// leading zeros, a bare trailing dot, and range overflow match
// encoding/json on every supported toolchain. A leading plus sign and a
// bare leading dot are accepted by encoding/json only since go1.27, so
// vjson's acceptance is pinned directly and parity is asserted only when
// the running std also accepts them.
//
// Known remaining divergence inside strconv.ParseFloat's grammar: hex floats
// ("0x1p-2") and digit separators ("1_0") are not parsed by the native
// backend.
func TestUnmarshal_StringTagFloatGrammar(t *testing.T) {
	type doc struct {
		Q float64 `json:",string"`
	}
	assertSameDecode(t, "string float leading zeros", []byte(`{"Q":"00010"}`), func() any { return new(doc) })
	assertSameDecode(t, "string float plain", []byte(`{"Q":"10"}`), func() any { return new(doc) })
	assertSameDecode(t, "string float trailing dot", []byte(`{"Q":"5."}`), func() any { return new(doc) })
	assertSameDecode(t, "string float range overflow", []byte(`{"Q":"1e1000"}`), func() any { return new(doc) })
	assertSameDecode(t, "string float inner space", []byte(`{"Q":" 10"}`), func() any { return new(doc) })

	var plus, frac doc
	if err := vjson.Unmarshal([]byte(`{"Q":"+10"}`), &plus); err != nil || plus.Q != 10 {
		t.Errorf("string float plus sign: vjson got=%v err=%v", plus.Q, err)
	}
	if err := vjson.Unmarshal([]byte(`{"Q":".5"}`), &frac); err != nil || frac.Q != 0.5 {
		t.Errorf("string float leading fraction: vjson got=%v err=%v", frac.Q, err)
	}
	if stdLaxStringFloat() {
		assertSameDecode(t, "string float plus sign", []byte(`{"Q":"+10"}`), func() any { return new(doc) })
		assertSameDecode(t, "string float leading fraction", []byte(`{"Q":".5"}`), func() any { return new(doc) })
	}
}

// stdLaxStringFloat reports whether the running encoding/json accepts the
// ParseFloat lax forms (leading plus sign, bare leading dot) for quoted
// numbers, which arrived in go1.27.
func stdLaxStringFloat() bool {
	var d struct {
		Q float64 `json:",string"`
	}
	return json.Unmarshal([]byte(`{"Q":"+10"}`), &d) == nil
}

// ---------------------------------------------------------------------------
// Valid
// ---------------------------------------------------------------------------

// Valid accepts a large, deeply nested document held verbatim in testdata.
func TestValid_LargeNestedDocument(t *testing.T) {
	data, err := os.ReadFile("testdata/valid_large.json")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	stdValid := json.Valid(data)
	if !stdValid {
		t.Fatal("precondition: std says document is valid")
	}
	if got := vjson.Valid(data); got != stdValid {
		t.Errorf("large document: Valid divergence: std=%v vjson=%v", stdValid, got)
	}
}

// Valid agrees with encoding/json over the simdjson example corpus when a
// local checkout exists.
func TestValid_RealWorldCorpus(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	dir := filepath.Join(home, "Data", "projects", "simdjson.git", "jsonexamples")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	files2, _ := filepath.Glob(filepath.Join(dir, "small", "*.json"))
	files = append(files, files2...)
	if len(files) == 0 {
		t.Skip("simdjson jsonexamples not available locally")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			stdValid := json.Valid(data)
			vjValid := vjson.Valid(data)
			if vjValid != stdValid {
				t.Errorf("corpus %s: Valid divergence: std=%v vjson=%v", filepath.Base(path), stdValid, vjValid)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Field binding
// ---------------------------------------------------------------------------

// A []byte field accepts base64 text ending in a \n escape, as encoding/json
// does.
func TestUnmarshal_ByteSliceBase64NewlineEscape(t *testing.T) {
	type doc struct {
		Name    string `json:"name"`
		PayLoad []byte `json:"pay_load"`
	}
	assertSameDecode(t, "base64 newline escape", []byte(`{"name":"hah","pay_load":"LWItFQcIBQwvDA==\n"}`), func() any {
		return new(doc)
	})
}

// An outer RawMessage field shadowing an embedded struct field with the same
// tag populates the outer field.
func TestUnmarshal_ShadowedEmbeddedRawMessage(t *testing.T) {
	type message struct {
		App     string          `json:"app"`
		Payload json.RawMessage `json:"payload"`
	}
	type shadow struct {
		Payload json.RawMessage `json:"payload"`
		message
	}
	assertSameDecode(t, "shadowed rawmessage", []byte(`{"app":"payment","payload":{"amount":100,"cur":"EUR"}}`), func() any {
		return new(shadow)
	})
}

// A map of structs containing nested untagged structs decodes fully.
func TestUnmarshal_NestedUntaggedStructMap(t *testing.T) {
	type inner struct {
		F0, F1, F2, F3, F4, F5, F6, F7, F8, F9 string
	}
	type middle struct {
		F0 string
		F1 inner
	}
	type outer struct {
		F0 string `json:"m"`
		F1 middle
	}
	data := []byte(`{
  "a": {},
  "b": {},
  "c": {},
  "d": {},
  "e": {},
  "f": {},
  "g": {},
  "h": {
    "m": "1"
  },
  "i": {}
}`)
	assertSameDecode(t, "nested untagged map", data, func() any {
		return new(map[string]outer)
	})
}

// ---------------------------------------------------------------------------
// Interface dispatch
// ---------------------------------------------------------------------------

// An interface-typed field holding a concrete json.Unmarshaler dispatches to
// the custom unmarshaler.
type unmarshalerIface interface {
	UnmarshalJSON(b []byte) error
}

type unmarshalerImpl struct {
	a string
}

func (im *unmarshalerImpl) UnmarshalJSON(b []byte) error {
	im.a = string(b)
	return nil
}

type unmarshalerWrap struct {
	F unmarshalerIface `json:"F"`
}

func TestUnmarshal_UnmarshalerBehindInterface(t *testing.T) {
	data := []byte(`{"F":"xx"}`)

	stdWrap := unmarshalerWrap{F: &unmarshalerImpl{}}
	stdErr := json.Unmarshal(data, &stdWrap)
	vjWrap := unmarshalerWrap{F: &unmarshalerImpl{}}
	// A panic during unmarshal is itself a divergence, so trap it and keep
	// the rest of the suite running.
	vjErr := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		return vjson.Unmarshal(data, &vjWrap)
	}()
	if (stdErr == nil) != (vjErr == nil) {
		t.Fatalf("unmarshaler behind interface: error divergence: std=%v vjson=%v", stdErr, vjErr)
	}
	if stdErr != nil {
		t.Fatal("unmarshaler behind interface: precondition: std decodes successfully")
	}
	if vjErr != nil {
		t.Fatalf("unmarshaler behind interface: vjson failed: %v", vjErr)
	}
	stdA := stdWrap.F.(*unmarshalerImpl).a
	vjA := vjWrap.F.(*unmarshalerImpl).a
	if stdA != vjA {
		t.Errorf("unmarshaler behind interface: payload divergence: std=%q vjson=%q", stdA, vjA)
	}
	if want := `"xx"`; vjA != want {
		t.Errorf("unmarshaler behind interface: unmarshaler saw %q, want %q", vjA, want)
	}
}

// Decoding through a pointer to an interface that already holds a concrete
// value. std rejects the object because the interface holds a non-pointer
// value that implements neither Unmarshaler nor pointer indirection, so this
// is a pure differential pin: vjson must agree.
type indexNameDoc interface {
	IndexName() string
}

type indexNameProperty struct {
	Name string `json:"name"`
}

func (indexNameProperty) IndexName() string { return "properties" }

func TestUnmarshal_PointerToNonEmptyInterface(t *testing.T) {
	data := []byte(`{"name":"Test Property to remove"}`)
	stdDoc, vjDoc := indexNameDoc(indexNameProperty{}), indexNameDoc(indexNameProperty{})
	stdErr := json.Unmarshal(data, &stdDoc)
	vjErr := vjson.Unmarshal(data, &vjDoc)
	if (stdErr == nil) != (vjErr == nil) {
		t.Errorf("pointer to interface: error divergence: std=%v vjson=%v", stdErr, vjErr)
		return
	}
	if stdErr != nil {
		return
	}
	if !reflect.DeepEqual(stdDoc, vjDoc) {
		t.Errorf("pointer to interface: value divergence:\n  std:   %+v\n  vjson: %+v", stdDoc, vjDoc)
	}
}

// ---------------------------------------------------------------------------
// Repeated decode
// ---------------------------------------------------------------------------

// The input buffer is never mutated, and nested RawMessage values re-decode
// on repeated calls.
func TestUnmarshal_RepeatedDecodeInputImmutable(t *testing.T) {
	data := []byte(`[{"Body":{"List":[{"nodeType":"Call"},{"nodeType":"Call"}],"nodeKind":"Block","nodeType":"Statement"},"nodeType":"Function"},{"Body":{"List":[],"nodeKind":"Block","nodeType":"Statement"},"nodeType":"Function"}]`)
	orig := bytes.Clone(data)

	var vjTop []json.RawMessage
	if err := vjson.Unmarshal(data, &vjTop); err != nil {
		t.Fatalf("input immutable: vjson top-level decode: %v", err)
	}
	if !bytes.Equal(data, orig) {
		t.Errorf("input immutable: vjson mutated the input buffer")
	}

	var stdTop []json.RawMessage
	if err := json.Unmarshal(data, &stdTop); err != nil {
		t.Fatalf("input immutable: std top-level decode: %v", err)
	}
	if !reflect.DeepEqual(stdTop, vjTop) {
		t.Errorf("input immutable: RawMessage divergence:\n  std:   %s\n  vjson: %s", stdTop, vjTop)
	}

	// Re-decode nested levels through both decoders and compare the leaves.
	walk := func(dec func([]byte, any) error) []string {
		var elems []json.RawMessage
		if err := dec(data, &elems); err != nil {
			t.Fatalf("input immutable: top walk: %v", err)
		}
		var kinds []string
		for _, elem := range elems {
			var bodyMap map[string]json.RawMessage
			if err := dec(elem, &bodyMap); err != nil {
				t.Fatalf("input immutable: elem walk: %v", err)
			}
			var body map[string]json.RawMessage
			if err := dec(bodyMap["Body"], &body); err != nil {
				t.Fatalf("input immutable: body walk: %v", err)
			}
			var list []json.RawMessage
			if err := dec(body["List"], &list); err != nil {
				t.Fatalf("input immutable: list walk: %v", err)
			}
			for _, item := range list {
				var node map[string]json.RawMessage
				if err := dec(item, &node); err != nil {
					t.Fatalf("input immutable: item walk: %v", err)
				}
				kinds = append(kinds, string(node["nodeType"]))
			}
		}
		return kinds
	}
	stdKinds := walk(func(b []byte, v any) error { return json.Unmarshal(b, v) })
	vjKinds := walk(func(b []byte, v any) error { return vjson.Unmarshal(b, v) })
	if !reflect.DeepEqual(stdKinds, vjKinds) {
		t.Errorf("input immutable: nested walk divergence:\n  std:   %q\n  vjson: %q", stdKinds, vjKinds)
	}
}

// Successive Unmarshal calls into the same non-empty []*T keep working. The
// payloads shrink on every step, which is what makes the slice-reuse path
// interesting. Time fields are trimmed since vjson does not decode them.
func TestUnmarshal_RepeatedDecodePointerSliceShrink(t *testing.T) {
	type user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		FullName  string `json:"full_name"`
		Email     string `json:"email"`
		Active    bool   `json:"active"`
		Followers int    `json:"followers_count"`
	}
	type label struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	type milestone struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
	}
	type pullRequest struct {
		HasMerged bool `json:"merged"`
	}
	type repo struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	type issue struct {
		ID        int64        `json:"id"`
		URL       string       `json:"url"`
		Number    int64        `json:"number"`
		User      *user        `json:"user"`
		Labels    []*label     `json:"labels"`
		Milestone *milestone   `json:"milestone"`
		Assignees []*user      `json:"assignees"`
		State     string       `json:"state"`
		Comments  int          `json:"comments"`
		PullReq   *pullRequest `json:"pull_request"`
		Repo      *repo        `json:"repository"`
	}

	user1 := `{"id":1,"login":"user1","full_name":"User One","email":"user1@example.com","active":false,"followers_count":0}`
	user2 := `{"id":2,"login":"user2","full_name":"   \u0026lt; Ur Tw \u0026gt;\u0026lt;  ","email":"user2@noreply.example.org","active":true,"followers_count":2}`
	first := `[
{"id":6,"url":"http://localhost:3003/issues/1","number":1,"user":` + user1 + `,"labels":[],"milestone":null,"assignees":[` + user2 + `,{"id":3,"login":"user3","full_name":"User Three","email":"user3@example.com","active":true,"followers_count":5}],"state":"open","comments":0,"pull_request":null,"repository":{"id":3,"name":"repo3"}},
{"id":7,"url":"http://localhost:3003/issues/2","number":2,"user":` + user2 + `,"labels":[{"id":1,"name":"label1","color":"abcdef"},{"id":4,"name":"orglabel4","color":"000000"}],"milestone":{"id":1,"title":"milestone1","state":"open"},"assignees":null,"state":"closed","comments":2,"pull_request":{"merged":true},"repository":{"id":1,"name":"repo1"}},
{"id":10,"url":"http://localhost:3003/issues/3","number":1,"user":{"id":-1,"login":"Ghost","full_name":"","email":"","active":false,"followers_count":0},"labels":[{"id":2,"name":"label2","color":"000000"}],"milestone":{"id":3,"title":"milestone3","state":"closed"},"assignees":null,"state":"open","comments":0,"pull_request":{"merged":false},"repository":null}
]`
	second := `[
{"id":5,"url":"http://localhost:3003/issues/4","number":4,"user":` + user2 + `,"labels":[{"id":2,"name":"label2","color":"000000"}],"milestone":null,"assignee":null,"assignees":null,"state":"closed","comments":0,"pull_request":null,"repository":{"id":1,"name":"repo1"}},
{"id":4,"url":"http://localhost:3003/issues/5","number":1,"user":` + user2 + `,"labels":[],"milestone":null,"assignees":null,"state":"closed","comments":0,"pull_request":null,"repository":{"id":2,"name":"repo2"}}
]`
	final := `[
{"id":6,"url":"http://localhost:3003/issues/1","number":1,"user":` + user1 + `,"labels":[],"milestone":null,"assignees":[` + user2 + `],"state":"open","comments":0,"pull_request":null,"repository":{"id":3,"name":"repo3"}}
]`

	var stdPS, vjPS []*issue
	for i, doc := range []string{first, second, final} {
		if err := json.Unmarshal([]byte(doc), &stdPS); err != nil {
			t.Fatalf("pointer slice shrink: std step %d: %v", i, err)
		}
		if err := vjson.Unmarshal([]byte(doc), &vjPS); err != nil {
			t.Fatalf("pointer slice shrink: vjson step %d: %v", i, err)
		}
		if !reflect.DeepEqual(stdPS, vjPS) {
			t.Errorf("pointer slice shrink: step %d divergence:\n  std:   %+v\n  vjson: %+v", i, stdPS, vjPS)
		}
	}
}

// ---------------------------------------------------------------------------
// Decoder over chunked readers
// ---------------------------------------------------------------------------

// Bare tokens (true/false/null) and mixed elements crossing a read boundary
// decode whole.
func TestDecoder_BareTokenAcrossChunkBoundary(t *testing.T) {
	data := []byte(strings.Repeat(" ", 4090) + `[true,false,null,-1.5e10,"x"]`)
	for _, chunk := range []int{1, 4, 4090, 4091, 4092, 4093, 4094, 4095, 4096, 4097, 8192} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			assertSameStream(t, "bare token boundary", data, chunk, func() any { return new(any) })
		})
	}
}

// After a Decode error inside an NDJSON stream the decoder keeps making
// progress: the number element fails, the object that follows decodes.
func TestDecoder_ErrorRecovery(t *testing.T) {
	type valDoc struct {
		Val int `json:"val"`
	}
	data := "12\n{\"val\" : 2}\n"

	run := func(newDec func(io.Reader) interface{ Decode(any) error }) (successes, failures int, vals []int) {
		dec := newDec(strings.NewReader(data))
		for i := 0; i < 16; i++ {
			var p valDoc
			err := dec.Decode(&p)
			if err == io.EOF {
				return
			}
			if err != nil {
				failures++
				continue
			}
			successes++
			vals = append(vals, p.Val)
		}
		t.Fatal("error recovery: decoder made no progress, possible infinite loop")
		return
	}

	stdS, stdF, stdVals := run(func(r io.Reader) interface{ Decode(any) error } { return json.NewDecoder(r) })
	vjS, vjF, vjVals := run(func(r io.Reader) interface{ Decode(any) error } { return vjson.NewDecoder(r) })
	if stdS != vjS || stdF != vjF || !reflect.DeepEqual(stdVals, vjVals) {
		t.Errorf("error recovery: stream divergence: std=(succ %d, fail %d, vals %v) vjson=(succ %d, fail %d, vals %v)",
			stdS, stdF, stdVals, vjS, vjF, vjVals)
	}
	if vjS != 1 || len(vjVals) != 1 || vjVals[0] != 2 {
		t.Errorf("error recovery: expected the object element to decode after the failing number, got vals %v", vjVals)
	}
}

// A Decoder over a length-limited reader never accepts a truncated document.
func TestDecoder_TruncatedDocumentErrors(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&sb, `"k%d":"value%d",`, i, i)
	}
	sb.WriteString(`"last":"end"}`)
	big := []byte(sb.String())

	run := func(newDec func(io.Reader) interface{ Decode(any) error }) error {
		r := io.LimitReader(bytes.NewReader(big), int64(len(big)-10))
		return newDec(r).Decode(new(map[string]string))
	}
	stdErr := run(func(r io.Reader) interface{ Decode(any) error } { return json.NewDecoder(r) })
	vjErr := run(func(r io.Reader) interface{ Decode(any) error } { return vjson.NewDecoder(r) })
	if stdErr == nil {
		t.Fatal("truncated stream: precondition: std rejects the truncated document")
	}
	if vjErr == nil {
		t.Errorf("truncated stream: vjson accepted a truncated document")
	}
}

// An integer map key straddling a read boundary decodes whole. Padding
// sweeps the key across the 4096 byte mark; chunk sweeps cover other
// alignments.
func TestDecoder_IntegerMapKeyAcrossChunkBoundary(t *testing.T) {
	mk := func() any { return new(map[int64]map[string]any) }
	assertSameDecode(t, "int map key unpadded", []byte(`{"123":{}}`), mk)
	for pad := 4088; pad <= 4100; pad++ {
		data := []byte(strings.Repeat(" ", pad) + `{"123":{}}`)
		assertSameDecode(t, fmt.Sprintf("int map key pad=%d", pad), data, mk)
	}
	for _, chunk := range []int{1, 1024, 2048, 4095, 4096, 4097} {
		data := []byte(strings.Repeat(" ", 4093) + `{"123":{}}`)
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			assertSameStream(t, "int map key", data, chunk, mk)
		})
	}
}

// ---------------------------------------------------------------------------
// Depth
// ---------------------------------------------------------------------------

// Value nesting is capped at 255 on both backends, tighter than
// encoding/json's 10000. Within the cap both decoders agree; beyond it
// vjson fails loudly with a depth error instead of exhausting the Go stack.
func TestUnmarshal_DeepNestingBounded(t *testing.T) {
	ok := []byte(`{"a":` + strings.Repeat("[", 250) + strings.Repeat("]", 250) + `}`)
	assertSameDecode(t, "depth within limit", ok, func() any { return new(any) })

	deep := []byte(`{"a":` + strings.Repeat("[", 500) + strings.Repeat("]", 500) + `}`)
	var vjDeep any
	err := vjson.Unmarshal(deep, &vjDeep)
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("depth beyond limit: want a depth error, got %v", err)
	}

	bad := []byte(`{"a":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`)
	var stdV, vjV any
	stdErr := json.Unmarshal(bad, &stdV)
	vjErr := vjson.Unmarshal(bad, &vjV)
	if stdErr == nil {
		t.Fatal("deep nesting: precondition: std rejects the over-deep document")
	}
	if vjErr == nil {
		t.Errorf("deep nesting: vjson accepted an over-deep document")
	}
}
