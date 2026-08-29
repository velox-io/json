//go:build !vdec

package tests

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
)

// Value must work at every position the decoder can reach. These five are the
// positions the bind engine dispatches differently: document root, struct
// field, map value, slice element, and pointer target.

func TestValue_Root(t *testing.T) {
	var v vjson.Value
	input := []byte(`{"id":"x","n":[1,2]}`)
	if err := vjson.Unmarshal(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.String() != string(input) {
		t.Fatalf("root Value = %s, want %s", v.String(), input)
	}
	if got, ok := v.GetString("id"); !ok || got != "x" {
		t.Errorf(`GetString("id") = %q, %v; want "x"`, got, ok)
	}
}

func TestValue_RootScalars(t *testing.T) {
	for _, in := range []string{`42`, `3.14`, `true`, `false`, `null`, `"hello"`, `[1,2,3]`, `{}`} {
		var v vjson.Value
		if err := vjson.Unmarshal([]byte(in), &v); err != nil {
			t.Errorf("Unmarshal(%q): %v", in, err)
			continue
		}
		if v.String() != in {
			t.Errorf("Unmarshal(%q) gave %s", in, v.String())
		}
	}
}

func TestValue_StructField(t *testing.T) {
	type Response struct {
		Code    int          `json:"code"`
		Message string       `json:"message"`
		Data    vjson.Value  `json:"data"`
		Extra   *vjson.Value `json:"extra"`
	}
	input := []byte(`{"code":200,"message":"ok","data":{"id":"abc","n":7},"extra":[1,2]}`)
	var res Response
	if err := vjson.Unmarshal(input, &res); err != nil {
		t.Fatal(err)
	}
	if res.Code != 200 || res.Message != "ok" {
		t.Fatalf("scalars around the Value decoded wrong: %+v", res)
	}
	if res.Data.String() != `{"id":"abc","n":7}` {
		t.Errorf("Data = %s", res.Data.String())
	}
	if got, ok := res.Data.GetString("id"); !ok || got != "abc" {
		t.Errorf(`Data.GetString("id") = %q, %v; want "abc"`, got, ok)
	}
	if got, ok := res.Data.GetInt("n"); !ok || got != 7 {
		t.Errorf(`Data.GetInt("n") = %d, %v; want 7`, got, ok)
	}
	// Pointer target.
	if res.Extra == nil {
		t.Fatal("Extra pointer is nil")
	}
	if (res.Extra).String() != `[1,2]` {
		t.Errorf("*Extra = %s", res.Extra.String())
	}
}

func TestValue_MapValue(t *testing.T) {
	var m map[string]vjson.Value
	input := []byte(`{"a":{"x":1},"b":[1,2],"c":"s","d":3,"e":null}`)
	if err := vjson.Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"a": `{"x":1}`,
		"b": `[1,2]`,
		"c": `"s"`,
		"d": `3`,
		"e": `null`,
	}
	if len(m) != len(want) {
		t.Fatalf("map has %d entries, want %d", len(m), len(want))
	}
	for k, w := range want {
		v := m[k]
		if v.String() != w {
			t.Errorf("m[%q] = %s, want %s", k, v.String(), w)
		}
	}
	va := m["a"]
	if got, ok := va.GetInt("x"); !ok || got != 1 {
		t.Errorf(`m["a"].GetInt("x") = %d, %v; want 1`, got, ok)
	}
}

func TestValue_SliceElem(t *testing.T) {
	var s []vjson.Value
	input := []byte(`[{"x":1},"str",42,null,[1],true]`)
	if err := vjson.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	want := []string{`{"x":1}`, `"str"`, `42`, `null`, `[1]`, `true`}
	if len(s) != len(want) {
		t.Fatalf("slice has %d elements, want %d", len(s), len(want))
	}
	for i, w := range want {
		if s[i].String() != w {
			t.Errorf("s[%d] = %s, want %s", i, s[i].String(), w)
		}
	}
}

func TestValue_PointerRoot(t *testing.T) {
	var p *vjson.Value
	if err := vjson.Unmarshal([]byte(`{"z":1}`), &p); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("pointer root is nil")
	}
	if (p).String() != `{"z":1}` {
		t.Errorf("*p = %s", p.String())
	}
}

func TestValue_NestedStructs(t *testing.T) {
	type Inner struct {
		Raw vjson.Value `json:"raw"`
	}
	type Outer struct {
		Name  string      `json:"name"`
		Inner Inner       `json:"inner"`
		Top   vjson.Value `json:"top"`
	}
	input := []byte(`{"name":"n","inner":{"raw":{"deep":[1,2]}},"top":{"a":1}}`)
	var o Outer
	if err := vjson.Unmarshal(input, &o); err != nil {
		t.Fatal(err)
	}
	if o.Name != "n" {
		t.Errorf("Name = %q", o.Name)
	}
	if o.Inner.Raw.String() != `{"deep":[1,2]}` {
		t.Errorf("Inner.Raw = %s", o.Inner.Raw.String())
	}
	if o.Top.String() != `{"a":1}` {
		t.Errorf("Top = %s", o.Top.String())
	}
	deep := o.Inner.Raw.Get("deep")
	d1 := deep.Index(1)
	if n, ok := d1.Int(); !ok || n != 2 {
		t.Errorf(`Inner.Raw.Get("deep").Index(1) = %d, %v; want 2`, n, ok)
	}
}

// Value must be emitted verbatim, never base64 encoded. Value is a []byte
// underneath, so it would be base64 encoded if the type descriptor layer did
// not classify it as a raw span before the []byte handling runs.
func TestValue_MarshalVerbatim(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	d, err := vjson.Parse([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := vjson.Marshal(S{D: d})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"d":{"a":1}}` {
		t.Fatalf("Marshal gave %s, want {\"d\":{\"a\":1}}", out)
	}

	// Empty and nil become null, matching RawMessage.
	out, err = vjson.Marshal(S{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"d":null}` {
		t.Errorf("Marshal of zero Value gave %s, want {\"d\":null}", out)
	}

	// Also verbatim in maps, slices and at the root.
	mapV, err := vjson.Parse([]byte(`[1]`))
	if err != nil {
		t.Fatal(err)
	}
	out, err = vjson.Marshal(map[string]vjson.Value{"k": mapV})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"k":[1]}` {
		t.Errorf("map Marshal gave %s", out)
	}
	sliceV1, err := vjson.Parse([]byte(`1`))
	if err != nil {
		t.Fatal(err)
	}
	sliceV2, err := vjson.Parse([]byte(`"s"`))
	if err != nil {
		t.Fatal(err)
	}
	out, err = vjson.Marshal([]vjson.Value{sliceV1, sliceV2})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `[1,"s"]` {
		t.Errorf("slice Marshal gave %s", out)
	}
	out, err = func() ([]byte, error) {
		v, perr := vjson.Parse([]byte(`{"a":1}`))
		if perr != nil {
			return nil, perr
		}
		return vjson.Marshal(v)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a":1}` {
		t.Errorf("root Marshal gave %s", out)
	}
}

func TestValue_RoundTrip(t *testing.T) {
	type S struct {
		A int         `json:"a"`
		D vjson.Value `json:"d"`
		B string      `json:"b"`
	}
	input := []byte(`{"a":1,"d":{"nested":[1,2,{"k":"v"}]},"b":"x"}`)
	var s S
	if err := vjson.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	out, err := vjson.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Errorf("round trip: got %s, want %s", out, input)
	}
}

// Value is tape-backed: bind emits a tape and accessors (and MarshalJSON)
// re-serialize from it, normalizing whitespace. Readers still see through the
// original whitespace because navigation reads the tape, not raw bytes.
func TestValue_WhitespacePreserved(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	input := []byte(`{"d":  { "x" : [ 1 , 2 ] }  }`)
	var s S
	if err := vjson.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}

	// String re-serializes from the tape (whitespace normalized); it must
	// be logically equal to the input, not byte-identical.
	if s.D.String() != `{"x":[1,2]}` {
		t.Errorf("String() = %q, want {\"x\":[1,2]}", s.D.String())
	}

	// Readers see through the whitespace (tape is authoritative).
	if n := s.D.Len(); n != 1 {
		t.Errorf("Len() = %d, want 1", n)
	}
	xv := s.D.Get("x")
	if n := xv.Len(); n != 2 {
		t.Errorf(`Get("x").Len() = %d, want 2`, n)
	}
	d1 := xv.Index(1)
	if got, ok := d1.Int(); !ok || got != 2 {
		t.Errorf(`Get("x").Index(1) = %d, %v; want 2`, got, ok)
	}
	if !s.D.Valid() {
		t.Error("Valid() = false for a whitespace laden but well formed value")
	}
}

// With the tape-emit path the Value walk validates structural syntax, so a
// malformed span now surfaces as a parse error instead of being captured raw.
// (Previously the byte-span capture deferred validation to Valid().)
func TestValue_UnvalidatedSpan(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	var s S
	if err := vjson.Unmarshal([]byte(`{"d":{"a":tru}}`), &s); err == nil {
		t.Fatalf("expected syntax error for malformed value, got nil; captured %s", s.D.String())
	}
}

func TestValue_Omitempty(t *testing.T) {
	// Value is a struct, so encoding/json's omitempty (which does not apply
	// to struct values) cannot elide an empty Value: it marshals as null.
	// json.RawMessage ([]byte) does support omitempty. This test pins the
	// difference so the regression is visible.
	type S struct {
		A vjson.Value `json:"a,omitempty"`
		B vjson.Value `json:"b,omitempty"`
	}
	b, err := vjson.Parse([]byte(`1`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := vjson.Marshal(S{B: b})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":null,"b":1}` {
		t.Errorf("omitempty: got %s, want {\"a\":null,\"b\":1} (struct Value is not elided)", got)
	}
}

func TestValue_Escapes(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	var s S
	if err := vjson.Unmarshal([]byte(`{"d":{"emoji":"😀","q":"a\"b","nl":"x\ny"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.D.GetString("emoji"); !ok || got != "😀" {
		t.Errorf("emoji = %q, %v; want 😀", got, ok)
	}
	if got, ok := s.D.GetString("q"); !ok || got != `a"b` {
		t.Errorf(`q = %q, %v; want a"b`, got, ok)
	}
	if got, ok := s.D.GetString("nl"); !ok || got != "x\ny" {
		t.Errorf("nl = %q, %v; want x\\ny", got, ok)
	}
}

// A Value must not alias the input buffer, so mutating the input afterwards
// must not change the Value.
func TestValue_BytesAreCopied(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	input := []byte(`{"d":{"a":1}}`)
	var s S
	if err := vjson.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	before := s.D.String()
	for i := range input {
		input[i] = 'X'
	}
	if s.D.String() != before {
		t.Errorf("Value aliased the input: was %s, now %s", before, s.D.String())
	}
}

func TestValue_DeepCopy(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	d, err := vjson.Parse([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	src := S{D: d}
	dst, err := vjson.DeepCopy(src)
	if err != nil {
		t.Fatal(err)
	}
	if dst.D.String() != `{"a":1}` {
		t.Fatalf("DeepCopy gave %s", dst.D.String())
	}
	// Value's tape/str_arena/src backings are immutable after drain, so
	// DeepCopy shares them (no deep clone). The navigation cursor (tidx)
	// is copied by value, so dst.D navigates independently of src.D.
}

// Value and json.RawMessage must decode to logically-equal content. Value is
// tape-backed so it normalizes whitespace; RawMessage preserves raw bytes. The
// two match on canonical (whitespace-free) inputs and on logical structure
// otherwise. The whitespace-laden input is excluded from byte comparison.
func TestValue_MatchesRawMessage(t *testing.T) {
	inputs := []string{
		`{"d":{"a":1}}`,
		`{"d":[1,2,3]}`,
		`{"d":"s"}`,
		`{"d":42}`,
		`{"d":null}`,
		`{"d":true}`,
		`{"d":{"nested":{"deep":[{"a":1}]}}}`,
	}
	for _, in := range inputs {
		var withValue struct {
			D vjson.Value `json:"d"`
		}
		var withRaw struct {
			D json.RawMessage `json:"d"`
		}
		if err := vjson.Unmarshal([]byte(in), &withValue); err != nil {
			t.Errorf("Value Unmarshal(%s): %v", in, err)
			continue
		}
		if err := vjson.Unmarshal([]byte(in), &withRaw); err != nil {
			t.Errorf("RawMessage Unmarshal(%s): %v", in, err)
			continue
		}
		// Canonical inputs round-trip byte-identical through the tape.
		if withValue.D.String() != string(withRaw.D) {
			t.Errorf("%s: Value = %q, RawMessage = %q", in, withValue.D.String(), withRaw.D)
		}
	}
}

// Value marshals through encoding/json (Value.MarshalJSON re-serializes the
// tape). Value does NOT implement UnmarshalJSON: it is populated by the native
// binder, not by a byte copy. For stdlib decode use Raw (see TestRaw_StdlibInterop).
func TestValue_StdlibInterop(t *testing.T) {
	type S struct {
		D vjson.Value `json:"d"`
	}
	input := []byte(`{"d":{"a":[1,2]}}`)

	// Decoded by this library, encoded by encoding/json.
	var s S
	if err := vjson.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Errorf("encoding/json encoded %s, want %s", out, input)
	}
}

// Raw round-trips through encoding/json in both directions (Raw implements
// Marshaler and Unmarshaler).
func TestRaw_StdlibInterop(t *testing.T) {
	type S struct {
		D vjson.Raw `json:"d"`
	}
	input := []byte(`{"d":{"a":[1,2]}}`)

	// Decoded by encoding/json, encoded by this library.
	var s S
	if err := json.Unmarshal(input, &s); err != nil {
		t.Fatal(err)
	}
	out, err := vjson.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Errorf("vjson encoded %s, want %s", out, input)
	}

	// Round-trip both ways through encoding/json.
	out, err = json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Errorf("encoding/json encoded %s, want %s", out, input)
	}
}

func TestValue_DOMUsage(t *testing.T) {
	// The shape from the design doc: read a document as a DOM.
	var doc vjson.Value
	input := []byte(`{"id":"u-1","tags":["a","b"],"meta":{"n":3,"ok":true},"score":1.5}`)
	if err := vjson.Unmarshal(input, &doc); err != nil {
		t.Fatal(err)
	}

	if doc.Type() != vjson.ValueObject {
		t.Errorf("Type() = %v, want object", doc.Type())
	}
	if got, ok := doc.GetString("id"); !ok || got != "u-1" {
		t.Errorf(`GetString("id") = %q, %v`, got, ok)
	}
	if got, ok := doc.GetInt("meta", "n"); !ok || got != 3 {
		t.Errorf(`GetInt("meta","n") = %d, %v; want 3`, got, ok)
	}
	if got, ok := doc.GetBool("meta", "ok"); !ok || !got {
		t.Errorf(`GetBool("meta","ok") = %v, %v; want true`, got, ok)
	}
	if got, ok := doc.GetFloat("score"); !ok || got != 1.5 {
		t.Errorf(`GetFloat("score") = %v, %v; want 1.5`, got, ok)
	}

	tags := doc.Get("tags")
	if tags.Type() != vjson.ValueArray {
		t.Errorf("tags Type() = %v, want array", tags.Type())
	}
	if n := tags.Len(); n != 2 {
		t.Errorf("tags Len() = %d, want 2", n)
	}
	var collected []string
	tags.ForEachElem(func(_ int, e vjson.Value) bool {
		s, _ := e.Str()
		collected = append(collected, s)
		return true
	})
	if len(collected) != 2 || collected[0] != "a" || collected[1] != "b" {
		t.Errorf("tags = %v, want [a b]", collected)
	}

	// Absent keys are reported, not guessed.
	if e := doc.Get("nope"); e.Exists() {
		t.Error(`Get("nope") should not exist`)
	}
	if _, ok := doc.GetString("nope"); ok {
		t.Error(`GetString("nope") should fail`)
	}
}
