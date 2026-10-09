package tests

// Decode hooks run against encoding/json. A json.Unmarshaler sees the raw
// span of its value, a TextUnmarshaler sees the decoded string body,
// RawMessage keeps the span, and []byte decodes base64. The cases go through
// Unmarshal and the chunked Decoder; the tape walk does not run hooks.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// hkSpan records the span its UnmarshalJSON receives.
type hkSpan struct{ Span string }

func (h *hkSpan) UnmarshalJSON(b []byte) error {
	h.Span = string(b)
	return nil
}

// hkText records the body its UnmarshalText receives.
type hkText struct{ Body string }

func (h *hkText) UnmarshalText(b []byte) error {
	h.Body = string(b)
	return nil
}

// hkKey is a TextUnmarshaler map key.
type hkKey struct{ K string }

func (h *hkKey) UnmarshalText(b []byte) error {
	h.K = "key:" + string(b)
	return nil
}

type hkHost struct {
	U  hkSpan            `json:"u"`
	P  *hkSpan           `json:"p"`
	T  hkText            `json:"t"`
	TP *hkText           `json:"tp"`
	M  map[string]hkSpan `json:"m"`
	S  []*hkSpan         `json:"s"`
	TS []hkText          `json:"ts"`
	R  json.RawMessage   `json:"r"`
	B  []byte            `json:"b"`
	K  map[hkKey]int     `json:"k"`
	N  int               `json:"n"`
}

type hkIface struct {
	I fmt.Stringer `json:"i"`
	N int          `json:"n"`
}

// spanList builds an array of n distinct objects, enough to grow a slice's
// backing while its elements' hooks are pending.
func spanList(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"i":%d}`, i)
	}
	b.WriteByte(']')
	return b.String()
}

func TestUnmarshalHooks(t *testing.T) {
	cases := []corpusCase{
		{"unmarshaler spans", `{"u":{"a":[1,2]},"p":"x","m":{"a":1,"b":[2],"c":"s"},"n":1}`, newOf[hkHost](), false},
		{"unmarshaler span keeps escapes", `{"u":"a\u00e9\n","p":[ 1 , {"k" : null} ]}`, newOf[hkHost](), false},
		{"unmarshaler null", `{"u":null,"p":null,"m":{"a":null}}`, newOf[hkHost](), false},
		{"unmarshaler slice growth", `{"s":` + spanList(40) + `,"n":2}`, newOf[hkHost](), false},
		{"unmarshaler slice null elements", `{"s":[null,{"a":1},null]}`, newOf[hkHost](), false},
		{"text body decoded", `{"t":"a\tb\u00e9\ud83d\ude00","tp":"p","ts":["x","y\"z"]}`, newOf[hkHost](), false},
		{"text null", `{"t":null,"tp":null,"ts":[null,"a"]}`, newOf[hkHost](), false},
		{"text number", `{"t":1,"n":3}`, newOf[hkHost](), false},
		{"text bool", `{"tp":true,"n":3}`, newOf[hkHost](), false},
		{"text object", `{"t":{"a":1},"n":3}`, newOf[hkHost](), false},
		{"text element mismatch", `{"ts":["a",2,"c"],"n":3}`, newOf[hkHost](), false},
		{"text map keys", `{"k":{"a":1,"b\u00e9":2}}`, newOf[hkHost](), false},
		{"raw message and bytes", `{"r":{"k":[1, 2]},"b":"YWJj"}`, newOf[hkHost](), false},
		{"raw message scalars", `{"r":"s","b":""}`, newOf[hkHost](), false},
		{"raw message null", `{"r":null,"b":null}`, newOf[hkHost](), false},
		{"bytes from number", `{"b":2,"n":3}`, newOf[hkHost](), false},
		{"bytes from array", `{"b":[1,2,3]}`, newOf[hkHost](), false},
		{"unmarshaler root", `{"a":1}`, newOf[hkSpan](), false},
		{"unmarshaler root null", `null`, newOf[hkSpan](), false},
		{"text root", `"r"`, newOf[hkText](), false},
		{"text root number", `1`, newOf[hkText](), false},
		{"unmarshaler span truncated", `{"u":{"a":`, newOf[hkHost](), false},
		{"raw message truncated", `{"r":[1`, newOf[hkHost](), false},
		{"raw message root truncated", `{"a":[`, newOf[json.RawMessage](), false},
		{"iface null", `{"i":null,"n":1}`, newOf[hkIface](), false},
		{"iface value", `{"i":[1,2],"n":1}`, newOf[hkIface](), false},
		{"iface string", `{"i":"s","n":1}`, newOf[hkIface](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// A raw span reaches its hook, or its RawMessage, only as a complete JSON
// value: a malformed one fails the decode as a syntax error, as with
// encoding/json.
func TestUnmarshalHooks_MalformedSpan(t *testing.T) {
	cases := []corpusCase{
		{"trailing comma in array", `{"u":[1,]}`, newOf[hkHost](), false},
		{"trailing comma in object", `{"u":{"a":1,}}`, newOf[hkHost](), false},
		{"missing comma", `{"u":[1 2]}`, newOf[hkHost](), false},
		{"missing colon", `{"p":{"a" 1}}`, newOf[hkHost](), false},
		{"bare key", `{"p":{a:1}}`, newOf[hkHost](), false},
		{"mismatched close", `{"u":[1}`, newOf[hkHost](), false},
		{"broken literal inside", `{"u":[tru]}`, newOf[hkHost](), false},
		{"broken literal", `{"u":tru}`, newOf[hkHost](), false},
		{"leading zero", `{"u":01}`, newOf[hkHost](), false},
		{"bare minus", `{"p":-}`, newOf[hkHost](), false},
		{"bad escape", `{"u":"\x"}`, newOf[hkHost](), false},
		{"bad escape inside", `{"u":{"a":"\q"}}`, newOf[hkHost](), false},
		{"raw message trailing comma", `{"r":[1,],"n":1}`, newOf[hkHost](), false},
		{"raw message broken literal", `{"r":nul,"n":1}`, newOf[hkHost](), false},
		{"map value", `{"m":{"a":[1,]}}`, newOf[hkHost](), false},
		{"slice element", `{"s":[{"a":1,}]}`, newOf[hkHost](), false},
		{"unmarshaler root", `[1,]`, newOf[hkSpan](), false},
		{"raw message root", `{"a":[1,]}`, newOf[json.RawMessage](), false},
		{"raw message element missing", `[1,]`, newOf[[]json.RawMessage](), false},
		{"raw message map value", `{"a":{"b" 1}}`, newOf[map[string]json.RawMessage](), false},
		{"deep valid span", `{"u":` + strings.Repeat("[", 200) + strings.Repeat("]", 200) + `}`, newOf[hkHost](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// The lenient skip opt counts brackets over a raw span as it does over a
// skipped value; a well-formed span decodes the same under it.
func TestUnmarshalHooks_SkipLenient(t *testing.T) {
	for _, in := range []string{
		`{"u":{"a":[1,2]},"p":"x","m":{"a":1,"b":[2]},"r":{"k":[1, 2]},"n":1}`,
		`{"s":` + spanList(40) + `,"r":"s"}`,
	} {
		var std, got hkHost
		if err := json.Unmarshal([]byte(in), &std); err != nil {
			t.Fatal(err)
		}
		if err := vjson.Unmarshal([]byte(in), &got, vjson.SkipLenient(true)); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual(got, std) {
			t.Errorf("%s: decoded %s, encoding/json %s", in, dbgAny(got), dbgAny(std))
		}
	}
}

var errHook = errors.New("hook failure")

type hkFail struct{}

func (*hkFail) UnmarshalJSON([]byte) error { return errHook }

type hkTextFail struct{}

func (*hkTextFail) UnmarshalText([]byte) error { return errHook }

type hkFailHost struct {
	A int        `json:"a"`
	U hkFail     `json:"u"`
	T hkTextFail `json:"t"`
	B int        `json:"b"`
}

// A hook's error reaches the caller as the hook returned it, on every entry
// point, as it does with encoding/json.
func TestUnmarshalHooks_ErrorIdentity(t *testing.T) {
	for _, in := range []string{
		`{"a":1,"u":{"x":[1]},"b":2}`,
		`{"a":1,"t":"s","b":2}`,
		`{"u":1,"t":"s"}`,
		`[{"u":1}]`,
	} {
		var std []hkFailHost
		var host hkFailHost
		target, stdTarget := any(&host), any(&host)
		if in[0] == '[' {
			target, stdTarget = new([]hkFailHost), &std
		}
		if err := json.Unmarshal([]byte(in), stdTarget); err != errHook {
			t.Fatalf("%s: encoding/json returned %v", in, err)
		}
		if err := vjson.Unmarshal([]byte(in), target); err != errHook {
			t.Errorf("%s: Unmarshal returned %v, want the hook's error", in, err)
		}
		for _, chunk := range []int{1, len(in) + 1} {
			err := vjson.NewDecoder(&chunkReader{data: []byte(in), size: chunk}).Decode(target)
			if err != errHook {
				t.Errorf("%s: Decoder(chunk=%d) returned %v, want the hook's error", in, chunk, err)
			}
		}
	}
}
