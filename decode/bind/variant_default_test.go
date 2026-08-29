package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

// A blank descriptor field with no `case:` tag is the default case: a
// discriminator VALUE that matches no case resolves to it instead of reporting.
//
// It answers only "this value does not resolve". A discriminator key absent from
// the input is the other question ("no value was given") and keeps its own
// behavior, selecting nothing. The two are separate because a host that declares
// a default still wants `{}` to mean "nothing chosen", not "the fallback".

type defUser struct {
	Name string `json:"name"`
}

type defFallback struct {
	Raw string `json:"raw"`
}

// sibling variant with a default
type defSibHost struct {
	Kind string `json:"kind"`
	Data any    `json:"data" vjson:"variant=kind"`
}

// inline variant with a default
type defInlHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[defSibHost, struct {
		_ defUser `case:"user"`
		_ defFallback
	}]()
	vbind.DefineVariantCases[defInlHost, struct {
		_ defUser `case:"user"`
		_ defFallback
	}]()
}

func TestVariantDefault_Sibling(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any // nil means "Data must be nil"
	}{
		{"named case wins", `{"kind":"user","data":{"name":"A"}}`, defUser{Name: "A"}},
		{"unknown value falls back", `{"kind":"nosuch","data":{"raw":"r"}}`, defFallback{Raw: "r"}},
		// "" is a value the caller wrote that resolves to nothing, so the default
		// takes it; without a default this is the "missing discriminator" error.
		{"empty value falls back", `{"kind":"","data":{"raw":"r"}}`, defFallback{Raw: "r"}},
		// No discriminator key and no payload key: nothing was chosen.
		{"absent key selects nothing", `{}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h defSibHost
			if err := Unmarshal([]byte(c.src), &h); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.src, err)
			}
			if c.want == nil {
				if h.Data != nil {
					t.Errorf("Data = %v, want nil", h.Data)
				}
				return
			}
			if h.Data != c.want {
				t.Errorf("Data = %#v, want %#v", h.Data, c.want)
			}
		})
	}
}

// The payload key present with no discriminator key stays an error even with a
// default declared: the default resolves values, and here no value was given.
func TestVariantDefault_SiblingPayloadWithoutDiscStillErrors(t *testing.T) {
	for _, preset := range []string{"", "user"} {
		h := defSibHost{Kind: preset}
		err := Unmarshal([]byte(`{"data":{"raw":"r"}}`), &h)
		if err == nil {
			t.Fatalf("preset=%q: Unmarshal: want error, got nil (Data=%v)", preset, h.Data)
		}
		if !strings.Contains(err.Error(), "missing discriminator") {
			t.Errorf("preset=%q: err = %q, want it to name the missing discriminator", preset, err)
		}
	}
}

func TestVariantDefault_Inline(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any
	}{
		{"named case wins", `{"type":"user","name":"A"}`, defUser{Name: "A"}},
		{"unknown value falls back", `{"type":"nosuch","raw":"r"}`, defFallback{Raw: "r"}},
		{"empty value falls back", `{"type":"","raw":"r"}`, defFallback{Raw: "r"}},
		// Inline has no payload key of its own, so an absent discriminator is the
		// only "nothing given" shape here.
		{"absent key selects nothing", `{"raw":"r"}`, nil},
		{"empty object selects nothing", `{}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Both paths must agree: the JSON walk resolves at struct close, and
			// tape-bind re-derives the case from the merged tape.
			var h defInlHost
			if err := Unmarshal([]byte(c.src), &h); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.src, err)
			}
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse(%s): %v", c.src, err)
			}
			var hv defInlHost
			if err := UnmarshalValue(val, &hv); err != nil {
				t.Fatalf("UnmarshalValue(%s): %v", c.src, err)
			}
			for what, got := range map[string]any{"Unmarshal": h.Data, "UnmarshalValue": hv.Data} {
				if c.want == nil {
					if got != nil {
						t.Errorf("%s: Data = %v, want nil", what, got)
					}
					continue
				}
				if got != c.want {
					t.Errorf("%s: Data = %#v, want %#v", what, got, c.want)
				}
			}
		})
	}
}

// reuseHost is used to pin what a REUSED destination does. The discriminator is
// an ordinary Go field, so a caller can hand back a struct that already holds a
// value from an earlier parse or from plain assignment.
type reuseHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type reuseUser struct {
	Name string `json:"name"`
}

type reuseProduct struct {
	Title string `json:"title"`
}

func init() {
	vbind.DefineVariantCases[reuseHost, struct {
		_ reuseUser    `case:"user"`
		_ reuseProduct `case:"product"`
	}]()
}

// TestVariantInline_ReusedDestinationHonorsInputDisc: the discriminator the INPUT
// carries must win over whatever the destination already held.
//
// The regression this guards: the merged-tape scan used to treat "host field is
// non-NULL" as "already bound and no rescan needed". A reused destination makes
// that false, so the scan was skipped and the discriminator on the tape was never
// read. The stale case bound instead, and silently: a `{"type":"product"}` input
// produced a User with no error at all.
func TestVariantInline_ReusedDestinationHonorsInputDisc(t *testing.T) {
	steps := []struct {
		src  string
		want any
	}{
		{`{"type":"user","name":"A"}`, reuseUser{Name: "A"}},
		// The input names a different case than the one already in the destination.
		{`{"type":"product","title":"W"}`, reuseProduct{Title: "W"}},
		// And back again, so a single stale value cannot pass by luck.
		{`{"type":"user","name":"C"}`, reuseUser{Name: "C"}},
	}
	var h reuseHost // deliberately reused across iterations
	for _, s := range steps {
		if err := Unmarshal([]byte(s.src), &h); err != nil {
			t.Fatalf("Unmarshal(%s): %v", s.src, err)
		}
		if h.Data != s.want {
			t.Fatalf("Unmarshal(%s): Data = %#v, want %#v", s.src, h.Data, s.want)
		}
	}
}

// The same, with the discriminator set by hand rather than by a previous parse.
func TestVariantInline_PresetDiscDoesNotOverrideInput(t *testing.T) {
	h := reuseHost{Type: "user"}
	if err := Unmarshal([]byte(`{"type":"product","title":"W"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if want := (reuseProduct{Title: "W"}); h.Data != want {
		t.Errorf("Data = %#v, want %#v: the input's discriminator must win", h.Data, want)
	}
	if h.Type != "product" {
		t.Errorf("Type = %q, want %q", h.Type, "product")
	}
}

func TestVariantSibling_PresetDiscDoesNotOverrideInput(t *testing.T) {
	for _, path := range []struct {
		name string
		run  func(string, *variantEnvelopeSibling) error
	}{
		{"Unmarshal", func(src string, dst *variantEnvelopeSibling) error {
			return Unmarshal([]byte(src), dst)
		}},
		{"UnmarshalValue", func(src string, dst *variantEnvelopeSibling) error {
			v, err := dom.Parse([]byte(src))
			if err != nil {
				return err
			}
			return UnmarshalValue(v, dst)
		}},
	} {
		t.Run(path.name, func(t *testing.T) {
			h := variantEnvelopeSibling{Type: "user"}
			src := `{"data":{"title":"W","price":9},"type":"product"}`
			if err := path.run(src, &h); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, ok := h.Data.(variantProduct); !ok {
				t.Fatalf("Data = %T, want variantProduct", h.Data)
			}
			if h.Type != "product" {
				t.Fatalf("Type = %q, want product", h.Type)
			}
		})
	}
}

func TestVariantSibling_FailedParseDoesNotAuthorizeStaleDisc(t *testing.T) {
	type host struct {
		Type string `json:"type"`
		Note string `json:"note"`
		Data any    `json:"data" vjson:"variant=type"`
	}
	vbind.DefineVariantCases[host, struct {
		_ variantUser `case:"user"`
	}]()
	p, err := NewParser[host]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var dst host
	if perr := p.Unmarshal([]byte(`{"type":"user","data":`), &dst); perr == nil {
		t.Fatal("failed parse returned nil")
	}
	dst = host{}
	err = p.Unmarshal([]byte(`{"note":"user","data":{"name":"A"}}`), &dst)
	if err == nil || !strings.Contains(err.Error(), "missing discriminator") {
		t.Fatalf("second parse error = %v, want missing discriminator", err)
	}
}

func TestUnmarshalValueDoesNotMutateLaterValueArena(t *testing.T) {
	p := dom.NewParser()
	first, err := p.Parse([]byte(`{"type":"user","data":{"name":"A"}}`))
	if err != nil {
		t.Fatalf("first Parse: %v", err)
	}
	later, err := p.Parse([]byte(`{"type":"product","data":{"title":"W","price":9}}`))
	if err != nil {
		t.Fatalf("later Parse: %v", err)
	}
	wantLater := later.String()
	var out variantEnvelopeSibling
	if err := UnmarshalValue(first, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if got := later.String(); got != wantLater {
		t.Fatalf("later Value mutated: got %s, want %s", got, wantLater)
	}
}

// A fresh destination is unaffected by the idempotence change: an absent
// discriminator still selects nothing.
func TestVariantInline_FreshDestinationAbsentDisc(t *testing.T) {
	for _, src := range []string{`{}`, `{"name":"B"}`} {
		var h reuseHost
		if err := Unmarshal([]byte(src), &h); err != nil {
			t.Fatalf("Unmarshal(%s): %v", src, err)
		}
		if h.Data != nil {
			t.Errorf("Unmarshal(%s): Data = %v, want nil", src, h.Data)
		}
	}
}
