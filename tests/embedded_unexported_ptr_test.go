package tests

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// A field promoted from an embedded pointer to an unexported struct is
// followed when the pointer is set, on both engines. encoding/json only
// refuses the nil one, because reflect cannot store the allocation into the
// unexported slot; a set pointer needs no store, so the promoted fields are
// ordinary exported slots of the pointee.
//

func TestUnmarshalEmbeddedUnexportedPointerSet(t *testing.T) {
	type (
		embed1 struct{ Q int }
		embed2 struct{ Q int }
		embed3 struct {
			Q int64 `json:",string"`
		}
		S1 struct {
			*embed1
			R int
		}
		S2 struct {
			*embed1
			Q int
		}
		S4 struct {
			*embed1
			embed2
		}
		S5 struct {
			*embed3
			R int
		}
	)

	tests := []struct {
		name string
		in   string
		new  func() any
	}{{
		name: "set pointer is followed",
		in:   `{"R":2,"Q":1}`,
		new:  func() any { return &S1{embed1: &embed1{}} },
	}, {
		name: "absent key leaves set pointer alone",
		in:   `{"R":2}`,
		new:  func() any { return &S1{embed1: &embed1{Q: 5}} },
	}, {
		name: "top level Q wins",
		in:   `{"Q":1}`,
		new:  func() any { return &S2{embed1: &embed1{Q: 5}} },
	}, {
		name: "value embed wins over ptr embed at same depth",
		in:   `{"Q":1}`,
		new:  func() any { return &S4{embed1: &embed1{Q: 5}} },
	}, {
		name: "set pointer with string tag",
		in:   `{"R":2,"Q":"7"}`,
		new:  func() any { return &S5{embed3: &embed3{}} },
	}}

	for _, tt := range tests {
		want := tt.new()
		if err := json.Unmarshal([]byte(tt.in), want); err != nil {
			t.Fatalf("%s: stdlib: %v", tt.name, err)
		}
		got := tt.new()
		if err := vjson.Unmarshal([]byte(tt.in), got); err != nil {
			t.Errorf("%s: vjson: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: value mismatch\n  stdlib: %#+v\n  vjson:  %#+v", tt.name, want, got)
		}
	}

	// The stream decoder path must agree too.
	in := `{"R":2,"Q":1}`
	want := &S1{embed1: &embed1{}}
	if err := json.NewDecoder(strings.NewReader(in)).Decode(want); err != nil {
		t.Fatalf("stdlib decoder: %v", err)
	}
	got := &S1{embed1: &embed1{}}
	if err := vjson.NewDecoder(strings.NewReader(in)).Decode(got); err != nil {
		t.Errorf("vjson decoder: %v", err)
	} else if !reflect.DeepEqual(want, got) {
		t.Errorf("vjson decoder: value mismatch\n  stdlib: %#+v\n  vjson:  %#+v", want, got)
	}
}

// Marshal over embedded pointers must agree too: a nil one (unexported or
// not) drops the promoted fields, a set one emits them.
func TestMarshalEmbeddedPointer(t *testing.T) {
	type embed1 struct{ Q int }
	type exp1 struct{ Q int }
	type U struct {
		*embed1
		R int
	}
	type E struct {
		*exp1
		R int
	}
	for _, tt := range []struct {
		name string
		v    any
	}{
		{"unexported nil pointer", &U{R: 3}},
		{"unexported set pointer", &U{embed1: &embed1{Q: 7}, R: 3}},
		{"exported nil pointer", &E{R: 3}},
		{"exported set pointer", &E{exp1: &exp1{Q: 7}, R: 3}},
	} {
		encodeWithBothCompare(t, tt.name, tt.v)
	}
}

func encodeWithBothCompare(t *testing.T, name string, v any) {
	t.Helper()
	sb, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: stdlib marshal: %v", name, err)
	}
	vb, err := vjson.Marshal(v)
	if err != nil {
		t.Fatalf("%s: vjson marshal: %v", name, err)
	}
	if string(sb) != string(vb) {
		t.Errorf("%s: vjson diverges from encoding/json\n  stdlib: %s\n  vjson:  %s", name, sb, vb)
	}
}

// stdAliasTarget decodes into its own receiver through an alias type via a
// set embedded pointer to the (unexported, local) alias.
type stdAliasTarget struct {
	A int
	B string
}

func (t *stdAliasTarget) UnmarshalJSON(b []byte) error {
	type plain stdAliasTarget
	return json.Unmarshal(b, &struct{ *plain }{(*plain)(t)})
}

type vjAliasTarget struct {
	A int
	B string
}

func (t *vjAliasTarget) UnmarshalJSON(b []byte) error {
	type plain vjAliasTarget
	return vjson.Unmarshal(b, &struct{ *plain }{(*plain)(t)})
}

func TestUnmarshalJSONThroughAliasEmbeddedPtr(t *testing.T) {
	in := []byte(`{"A":42,"B":"x"}`)

	var std stdAliasTarget
	if err := json.Unmarshal(in, &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}
	if std != (stdAliasTarget{A: 42, B: "x"}) {
		t.Errorf("stdlib decoded wrong: %+v", std)
	}

	// The outer vjson bind defers to the stdlib hook.
	var viaStdHook stdAliasTarget
	if err := vjson.Unmarshal(in, &viaStdHook); err != nil {
		t.Errorf("vjson outer, stdlib hook: %v", err)
	} else if viaStdHook != std {
		t.Errorf("vjson + stdlib hook: %+v, want %+v", viaStdHook, std)
	}

	// All vjson: the hook itself runs vjson over the alias wrapper.
	var viaVjHook vjAliasTarget
	if err := vjson.Unmarshal(in, &viaVjHook); err != nil {
		t.Errorf("vjson outer and hook: %v", err)
	} else if viaVjHook.A != std.A || viaVjHook.B != std.B {
		t.Errorf("vjson hook: %+v, want fields of %+v", viaVjHook, std)
	}
}
