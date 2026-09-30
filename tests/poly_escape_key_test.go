package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
)

// Escaped keys decode through the transient unescape buffer a parse reuses
// for every transient string. A poly host stores deferred keys beyond the
// walk, so a stored key must survive the next escaped key's decode: two
// escaped case keys, an escaped key after an escaped discriminator, an
// escaped unknown key after an escaped case key, and escaped keys on a
// kindof host all pin the stored key against that reuse.

func TestPolyInlineEscapedKeys(t *testing.T) {
	cases := []struct {
		in   string
		want polyUser
	}{
		{`{"type":"user","i\u0064":1,"na\u006de":"ann"}`, polyUser{ID: 1, Name: "ann"}},
		{`{"ty\u0070e":"user","na\u006de":"ann"}`, polyUser{Name: "ann"}},
		{`{"na\u006de":"ann","type":"user"}`, polyUser{Name: "ann"}},
		{`{"type":"user","na\u006de":"ann","ju\u006ek":1}`, polyUser{Name: "ann"}},
	}
	for _, tc := range cases {
		var env polyInlineHost
		if err := vjson.Unmarshal([]byte(tc.in), &env); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if env.Type != "user" {
			t.Errorf("%s: Type = %q, want user", tc.in, env.Type)
		}
		got, ok := env.Data.(polyUser)
		if !ok {
			t.Fatalf("%s: Data is %T (%+v), want polyUser", tc.in, env.Data, env.Data)
		}
		if got != tc.want {
			t.Errorf("%s: Data = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// The kindof field key is stored with the deferred member until the value's
// kind selects the case, so an escaped kindof key followed by another
// escaped key must still resolve the field.
func TestPolyKindofEscapedKeys(t *testing.T) {
	var env polyKindofEnvelope
	const in = `{"da\u0074a":1,"x\u0079":2}`
	if err := vjson.Unmarshal([]byte(in), &env); err != nil {
		t.Fatalf("Unmarshal(%s): %v", in, err)
	}
	if got, ok := env.Data.(float64); !ok || got != 1 {
		t.Errorf("%s: Data = %#v, want 1", in, env.Data)
	}
}

// A reserve-unknown sink collects the members the host does not declare and
// re-serializes them with their keys decoded.
func TestReserveUnknownEscapedKeys(t *testing.T) {
	var h reserveUnknownHost
	const in = `{"name":"a","o\u006ee":1,"tw\u006f":2,"tail":7}`
	if err := vjson.Unmarshal([]byte(in), &h); err != nil {
		t.Fatalf("Unmarshal(%s): %v", in, err)
	}
	if h.Name != "a" || h.Tail != 7 {
		t.Fatalf("%s: declared fields not bound: %+v", in, h)
	}
	rest, err := vjson.Marshal(h.Rest)
	if err != nil {
		t.Fatalf("Marshal(Rest): %v", err)
	}
	const want = `{"one":1,"two":2}`
	if string(rest) != want {
		t.Errorf("%s: Rest = %s, want %s", in, rest, want)
	}
}
