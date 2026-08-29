package tests

import (
	"encoding/json"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

// `json:",embed"` promotes a named field's content into its host. Go embedding
// already promotes; this option extends the same layout to a field that has a
// Go name.
//
// The expectations below are reference outputs for the same types, checked
// against go1.28-devel. They are written out rather than compared against an
// external package because this module still targets an older Go, so that import
// is unavailable. encoding/json v1 cannot be the baseline here: it does not
// know the option and silently emits the field under its Go name, which is
// exactly the outcome this feature exists to replace.

type embInner struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// embNamedHost promotes a field that is not Go-embedded.
type embNamedHost struct {
	Mix  embInner `json:",embed"`
	Kind string   `json:"kind"`
}

// embMid promotes into itself, so embTransitiveHost promotes through two levels.
type embMid struct {
	Deep embInner `json:",embed"`
}

type embTransitiveHost struct {
	M    embMid `json:",embed"`
	Kind string `json:"kind"`
}

// embPrecedenceHost has a shallow field with the same JSON name as a promoted
// one, which the shallow field must win by depth.
type embPrecedenceHost struct {
	Mix  embInner `json:",embed"`
	Name string   `json:"name"`
}

// embCollideHost promotes the same names from two fields at one depth, so both
// cancel exactly as two same-name Go embeds do.
type embCollideHost struct {
	A embInner `json:",embed"`
	B embInner `json:",embed"`
}

func TestEmbedOption_NamedFieldPromotes(t *testing.T) {
	in := []byte(`{"name":"bob","age":3,"kind":"k"}`)

	var v embNamedHost
	if err := vjson.Unmarshal(in, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Mix.Name != "bob" || v.Mix.Age != 3 || v.Kind != "k" {
		t.Errorf("decoded %+v; want the promoted keys to land in Mix", v)
	}

	out, err := vjson.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"name":"bob","age":3,"kind":"k"}`
	if string(out) != want {
		t.Errorf("marshal = %s, want %s", out, want)
	}
}

func TestEmbedOption_Transitive(t *testing.T) {
	var v embTransitiveHost
	if err := vjson.Unmarshal([]byte(`{"name":"deep","age":7,"kind":"k"}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.M.Deep.Name != "deep" || v.M.Deep.Age != 7 {
		t.Errorf("decoded %+v; want promotion through both levels", v)
	}
	out, _ := vjson.Marshal(v)
	const want = `{"name":"deep","age":7,"kind":"k"}`
	if string(out) != want {
		t.Errorf("marshal = %s, want %s", out, want)
	}
}

// A promoted name loses to a shallower one, and two promoted copies of a name at
// the same depth cancel. Both rules already governed Go embedding, so the point
// here is that promoting by tag enters the same name bookkeeping rather than a
// parallel one.
func TestEmbedOption_NamePrecedence(t *testing.T) {
	var p embPrecedenceHost
	if err := vjson.Unmarshal([]byte(`{"name":"shallow","age":7}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Name != "shallow" {
		t.Errorf("Name = %q; want the depth-0 field to win", p.Name)
	}
	if p.Mix.Name != "" {
		t.Errorf("Mix.Name = %q; want the promoted field to be shadowed", p.Mix.Name)
	}
	if p.Mix.Age != 7 {
		t.Errorf("Mix.Age = %d; want 7, since only the colliding name is shadowed", p.Mix.Age)
	}

	var c embCollideHost
	if err := vjson.Unmarshal([]byte(`{"name":"x","age":1}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.A.Name != "" || c.B.Name != "" || c.A.Age != 0 || c.B.Age != 0 {
		t.Errorf("decoded A=%+v B=%+v; want every same-depth duplicate canceled", c.A, c.B)
	}
	out, _ := vjson.Marshal(c)
	if string(out) != `{}` {
		t.Errorf("marshal = %s, want {} since all names canceled", out)
	}
}

// A struct reached through a pointer cannot be promoted: promotion is offset
// arithmetic, and a pointer hop breaks the base+offset identity. velox refuses
// it rather than compute an address that does not name the field.
type embPtrHost struct {
	Mix  *embInner `json:",embed"`
	Kind string    `json:"kind"`
}

// Embedding needs content to promote, so a scalar cannot carry the option.
type embScalarHost struct {
	N int `json:",embed"`
}

// An explicit name contradicts a layout defined by having no name.
type embNamedConflict struct {
	Mix embInner `json:"mix,embed"`
}

func TestEmbedOption_RefusedShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		dst  any
		want string
	}{
		{"pointer", new(embPtrHost), "cannot be embedded"},
		{"scalar", new(embScalarHost), "cannot be embedded"},
		{"explicit name", new(embNamedConflict), "explicit JSON name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := vjson.Unmarshal([]byte(`{"name":"x","kind":"k"}`), tc.dst)
			if err == nil {
				t.Fatalf("decode succeeded; want a build error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("decode error = %v; want it to contain %q", err, tc.want)
			}
			if _, err := vjson.Marshal(tc.dst); err == nil {
				t.Errorf("encode succeeded; a shape refused for decode must not encode either")
			}
		})
	}
}

// A value.Value carrying the option reserves unknown keys: a struct has no
// members of its own to promote, so embedding a Value collects the unmatched
// keys instead. velox answers the same layout question ("this field holds
// content that is not its own member") for a type whose content is the keys the
// host did not declare.
type embReserveHost struct {
	Name string      `json:"name"`
	Rest value.Value `json:",embed"`
}

func TestEmbedOption_ValueReservesUnknown(t *testing.T) {
	var v embReserveHost
	if err := vjson.Unmarshal([]byte(`{"name":"bob","x":1,"y":"z"}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Name != "bob" {
		t.Errorf("Name = %q, want bob", v.Name)
	}
	if !v.Rest.Exists() {
		t.Fatal("Rest holds nothing; want the undeclared keys reserved into it")
	}
	for _, key := range []string{"x", "y"} {
		got := v.Rest.Get(key)
		if !got.Exists() {
			t.Errorf("Rest is missing key %q", key)
		}
	}
	// The field occupies no JSON name of its own, so it must not surface under
	// its Go name the way an untagged field would.
	if self := v.Rest.Get("Rest"); self.Exists() {
		t.Error("Rest contains a key named after itself; the field must occupy no name")
	}
}

// The retired spellings must fail loudly. Silence would be worst here: a struct
// written against the old vocabulary would keep compiling and quietly stop
// reserving keys, which is data loss with no diagnostic.
func TestEmbedOption_RetiredSpellingsRejected(t *testing.T) {
	type retiredUnknown struct {
		Name string      `json:"name"`
		Rest value.Value `json:"-" vjson:"unknown"`
	}
	type retiredInline struct {
		Type string `json:"type"`
		Data any    `vjson:"variant=type,inline"`
	}

	for _, tc := range []struct {
		name string
		dst  any
		want string
	}{
		{"unknown", new(retiredUnknown), "now spelled"},
		{"inline", new(retiredInline), "unrecognized vjson option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := vjson.Unmarshal([]byte(`{"name":"n","type":"t"}`), tc.dst)
			if err == nil {
				t.Fatalf("decode succeeded; want a build error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("decode error = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

// A type reached by two paths at one depth promotes an ambiguous selector, so
// every name it carries must cancel. Go's own selector rules say the same, and
// stdlib is the baseline: this case used to resolve to whichever path the
// traversal reached first.
type ambigLeaf struct {
	Name string `json:"name"`
}

type ambigLeftWrap struct{ ambigLeaf }
type ambigRightWrap struct{ ambigLeaf }

type ambigHost struct {
	ambigLeftWrap
	ambigRightWrap
	Kind string `json:"kind"`
}

func TestEmbed_SameDepthTwoPathsCancels(t *testing.T) {
	in := []byte(`{"name":"n","kind":"k"}`)

	var std, vj ambigHost
	if err := json.Unmarshal(in, &std); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}
	if err := vjson.Unmarshal(in, &vj); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}
	if vj != std {
		t.Errorf("decode diverges from encoding/json\n  stdlib: %+v\n  vjson:  %+v", std, vj)
	}
	if vj.ambigLeftWrap.ambigLeaf.Name != "" || vj.ambigRightWrap.ambigLeaf.Name != "" {
		t.Error("an ambiguous promoted name was filled in; both paths must cancel")
	}

	stdRaw, vjRaw := encodeWithBoth(t, ambigHost{Kind: "k"})
	assertJSONEqual(t, "ambiguous same-depth promotion", stdRaw, vjRaw)
	if strings.Contains(vjRaw, "name") {
		t.Errorf("marshal = %s; want no ambiguous name emitted", vjRaw)
	}
}

// Polymorphic dispatch locates the discriminator and stores the chosen case's
// eface relative to the host base. A field promoted across an embedded pointer
// has neither, so the combination is refused rather than dispatched against the
// wrong base. This is narrower than the promotion support itself: plain promoted
// fields across a pointer work.
type embPolyInner struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}
type embPolyHost struct {
	*embPolyInner
	Y int `json:"y"`
}

type embKindofInner struct {
	Data any `json:"data" vjson:"kindof"`
}
type embKindofHost struct {
	*embKindofInner
	Y int `json:"y"`
}

type embReserveInner struct {
	Rest value.Value `json:",embed"`
}
type embReserveHostPtr struct {
	*embReserveInner
	Y int `json:"y"`
}

func TestEmbedOption_PolyAcrossPointerRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		dst  any
		want string
	}{
		{"variant", new(embPolyHost), "promoted across an embedded pointer"},
		{"kindof", new(embKindofHost), "promoted across an embedded pointer"},
		{"reserve-unknown", new(embReserveHostPtr), "promoted across an embedded pointer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := vjson.Unmarshal([]byte(`{"type":"t","y":1}`), tc.dst)
			if err == nil {
				t.Fatalf("decode succeeded; want a build error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}
