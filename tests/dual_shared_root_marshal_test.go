package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Encoding of an escaping inline Value: the projection a dual shared root
// publishes for a value.Value case. Its descriptor carries view A plus the
// CountAtClose flag, so both the Go serializer and the native encvm walker
// must mask the mode down to a seam shift and emit exactly the inline
// projection's members.

type marshalDualHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type marshalDualCase struct {
	Name string `json:"name"`
}

func init() {
	vbind.DefineVariantCases[marshalDualHost, struct {
		_ marshalDualCase `case:"c1"`
		_ value.Value     `case:"raw"`
	}]()
}

func escapingInlineValue(t *testing.T) value.Value {
	t.Helper()
	var h marshalDualHost
	src := `{"kind":"raw","a":1,"b":{"c":[1,2]}}`
	if err := vjson.Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	vv, ok := h.Case.(value.Value)
	if !ok {
		t.Fatalf("Case = %T, want value.Value", h.Case)
	}
	return vv
}

// Ordinary Value encoding: the field carries its own key and the walker emits
// the inline projection under it.
func TestDualSharedRoot_MarshalEscapingInlineValue(t *testing.T) {
	vv := escapingInlineValue(t)
	var wrap struct {
		Blob value.Value `json:"blob"`
	}
	wrap.Blob = vv
	out, err := vjson.Marshal(wrap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"blob":{"kind":"raw","a":1,"b":{"c":[1,2]}}}`
	if string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}

// Spread encoding: a reserve-unknown field (`,embed`) spreads the Value's
// members into the host object with no key of their own.
func TestDualSharedRoot_MarshalEscapingInlineValueSpread(t *testing.T) {
	vv := escapingInlineValue(t)
	var host struct {
		Prefix string      `json:"prefix"`
		Blob   value.Value `json:",embed"`
	}
	host.Prefix = "p"
	host.Blob = vv
	out, err := vjson.Marshal(host)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"prefix":"p","kind":"raw","a":1,"b":{"c":[1,2]}}`
	if string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}

// The reserve projection of the same dual tape encodes its own, disjoint set
// of members through the same walkers, now reading view B.
func TestDualSharedRoot_MarshalReserveProjection(t *testing.T) {
	var h marshalDualHost
	src := `{"kind":"c1","name":"bob","u1":1,"u2":[true]}`
	if err := vjson.Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var wrap struct {
		Rest value.Value `json:"rest"`
	}
	wrap.Rest = h.Rest
	out, err := vjson.Marshal(wrap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"rest":{"u1":1,"u2":[true]}}`
	if string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}
