package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

// A case set belongs to the variant field, not to the discriminator. Two fields
// may read one discriminator and map its values to different types: the
// discriminator supplies the value, and each field resolves it through its own
// table (each variant field builds its own BindVariantTable, carrying its own
// lookup blob and its own disc offset).
//
// That is why the registry is keyed by field. Keying it by discriminator name made
// the shape below inexpressible: both fields say `variant=kind`, so they would
// necessarily receive one descriptor.

type pcUser struct {
	Name string `json:"name"`
}

type pcProduct struct {
	Title string `json:"title"`
}

type pcGithub struct {
	Repo string `json:"repo"`
}

type pcGitlab struct {
	Proj string `json:"proj"`
}

// One discriminator, two fields, two different case sets. Note "user" appears in
// both sets naming a DIFFERENT type, and "zzz" exists only in B's.
type pcDivergent struct {
	Kind string `json:"kind"`
	A    any    `json:"a" vjson:"variant=kind"`
	B    any    `json:"b" vjson:"variant=kind"`
}

// A field specific set on one field, the host fallback on the other.
type pcMixed struct {
	Kind string `json:"kind"`
	A    any    `json:"a" vjson:"variant=kind"`
	B    any    `json:"b" vjson:"variant=kind"`
}

// The embedded variant is keyed by Go field name like any other: it has no JSON
// name to key on, which is why the API takes the Go name.
type pcEmbedded struct {
	Kind string `json:"kind"`
	Inl  any    `json:",embed" vjson:"variant=kind"`
	Sib  any    `json:"sib" vjson:"variant=kind"`
}

func init() {
	vbind.DefineVariantCasesAt[pcDivergent, struct {
		user    pcUser
		product pcProduct
	}]("A")
	vbind.DefineVariantCasesAt[pcDivergent, struct {
		user pcGithub // same value, different target than A's
		zzz  pcGitlab // a value A does not know at all
	}]("B")

	vbind.DefineVariantCases[pcMixed, struct {
		user pcUser
	}]()
	vbind.DefineVariantCasesAt[pcMixed, struct {
		user pcGithub
	}]("B")

	vbind.DefineVariantCasesAt[pcEmbedded, struct {
		user pcUser
	}]("Inl")
	vbind.DefineVariantCasesAt[pcEmbedded, struct {
		user pcGithub
	}]("Sib")
}

// One discriminator value resolves to a different type per field.
func TestPerField_DivergentCaseSets(t *testing.T) {
	// Key order matters: the discriminator may precede both payloads (each field
	// resolves at its own site), follow both (both defer to the merged tape), or
	// sit between them (one of each). All three must agree.
	for _, src := range []string{
		`{"kind":"user","a":{"name":"A"},"b":{"repo":"R"}}`,
		`{"a":{"name":"A"},"b":{"repo":"R"},"kind":"user"}`,
		`{"a":{"name":"A"},"kind":"user","b":{"repo":"R"}}`,
	} {
		t.Run(src, func(t *testing.T) {
			var h pcDivergent
			if err := Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			val, err := dom.Parse([]byte(src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var hv pcDivergent
			if err := UnmarshalValue(val, &hv); err != nil {
				t.Fatalf("UnmarshalValue: %v", err)
			}
			for what, got := range map[string]pcDivergent{"Unmarshal": h, "UnmarshalValue": hv} {
				a, ok := got.A.(pcUser)
				if !ok {
					t.Fatalf("%s: A = %#v, want pcUser", what, got.A)
				}
				b, ok := got.B.(pcGithub)
				if !ok {
					t.Fatalf("%s: B = %#v, want pcGithub (same disc value, B's own set)", what, got.B)
				}
				if a.Name != "A" {
					t.Errorf("%s: A.Name = %q, want %q", what, a.Name, "A")
				}
				if b.Repo != "R" {
					t.Errorf("%s: B.Repo = %q, want %q", what, b.Repo, "R")
				}
			}
		})
	}
}

// A value present in only one field's set: unknown for the other. The sets are
// independent, so "resolvable" is answered per field rather than per host.
func TestPerField_ValueKnownToOneFieldOnly(t *testing.T) {
	var hb pcDivergent
	if err := Unmarshal([]byte(`{"kind":"zzz","b":{"proj":"P"}}`), &hb); err != nil {
		t.Fatalf("kind=zzz with only field b: %v", err)
	}
	if got, ok := hb.B.(pcGitlab); !ok || got.Proj != "P" {
		t.Errorf("B = %#v, want pcGitlab{Proj:\"P\"}", hb.B)
	}

	var ha pcDivergent
	err := Unmarshal([]byte(`{"kind":"zzz","a":{"name":"A"}}`), &ha)
	if err == nil {
		t.Fatalf("kind=zzz with field a: want unknown-disc error, got %#v", ha.A)
	}
	if !strings.Contains(err.Error(), "unknown discriminator") {
		t.Errorf("err = %q, want it to name the unknown discriminator", err)
	}
}

// A field specific definition wins for its field; every other field keeps the
// host fallback.
func TestPerField_FallbackAndOverrideCoexist(t *testing.T) {
	var h pcMixed
	if err := Unmarshal([]byte(`{"kind":"user","a":{"name":"A"},"b":{"repo":"R"}}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, ok := h.A.(pcUser); !ok || got.Name != "A" {
		t.Errorf("A = %#v, want pcUser{Name:\"A\"} from the fallback set", h.A)
	}
	if got, ok := h.B.(pcGithub); !ok || got.Repo != "R" {
		t.Errorf("B = %#v, want pcGithub{Repo:\"R\"} from B's own set", h.B)
	}
}

// The embedded variant is addressable by Go field name, and its case unfolds into
// the host while the sibling binds its own member from a different set.
func TestPerField_EmbeddedAddressedByGoName(t *testing.T) {
	const src = `{"kind":"user","name":"inlined","sib":{"repo":"R"}}`
	var h pcEmbedded
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, ok := h.Inl.(pcUser); !ok || got.Name != "inlined" {
		t.Errorf("Inl = %#v, want pcUser{Name:\"inlined\"}", h.Inl)
	}
	if got, ok := h.Sib.(pcGithub); !ok || got.Repo != "R" {
		t.Errorf("Sib = %#v, want pcGithub{Repo:\"R\"}", h.Sib)
	}
}
