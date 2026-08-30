package bind

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

// A host may declare any number of sibling variants. Nothing about a sibling is
// recorded per host: its table index rides in its own field flags, the table
// carries its own discriminator offset, and the eface goes at the field's own
// offset. So two siblings never contend for state, and the only ordering hazard
// is a discriminator arriving after its payload, which each field defers
// independently through the merged tape.
//
// The embedded variant is the one that stays single, and for a reason that shows
// up here: it has per host state (InlineVariantIdx, disc_seen) and the
// struct-close split classifies each key against one case field set.

type msUser struct {
	Name string `json:"name"`
}

type msProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

// Three siblings, three discriminators, three independent case sets.
type msTriple struct {
	Kind  string `json:"kind"`
	SVar1 any    `json:"svar1" vjson:"variant=kind"`
	Cate  string `json:"cate"`
	SVar2 any    `json:"svar2" vjson:"variant=cate"`
	Zone  string `json:"zone"`
	SVar3 any    `json:"svar3" vjson:"variant=zone"`
}

type msNestedHost struct {
	Inner msTriple `json:"inner"`
}

// Two siblings sharing ONE discriminator field. The vdisc field record is stamped
// twice, once per table, and the second stamp finds TagVDisc already set. That is
// harmless precisely because sibling dispatch reads the discriminator through its
// own table's DiscFieldOff, never through the idx in this field record.
type msShared struct {
	Kind string `json:"kind"`
	A    any    `json:"a" vjson:"variant=kind"`
	B    any    `json:"b" vjson:"variant=kind"`
}

// A sibling alongside the single embedded variant, to pin that lifting the
// sibling cap did not disturb the coexistence rule.
type msInlinePlusSiblings struct {
	Type  string `json:"type"`
	Inl   any    `json:",embed" vjson:"variant=type"`
	Kind  string `json:"kind"`
	SVar1 any    `json:"svar1" vjson:"variant=kind"`
	Cate  string `json:"cate"`
	SVar2 any    `json:"svar2" vjson:"variant=cate"`
}

func init() {
	// One shared case set for every variant field on the host, which is what the
	// fallback slot is for. Per field sets are exercised in
	// variant_percase_test.go.
	vbind.DefineVariantCases[msTriple, struct {
		user    msUser
		product msProduct
	}]()
	vbind.DefineVariantCases[msShared, struct {
		user    msUser
		product msProduct
	}]()
	vbind.DefineVariantCases[msInlinePlusSiblings, struct {
		user    msUser
		product msProduct
	}]()
}

func TestMultiSibling_ThreeIndependentDiscs(t *testing.T) {
	cases := []struct {
		name                string
		src                 string
		want1, want2, want3 any // nil means the field must stay nil
	}{
		{
			"disc before payload",
			`{"kind":"user","svar1":{"name":"A"},"cate":"product","svar2":{"title":"T"},"zone":"user","svar3":{"name":"C"}}`,
			msUser{Name: "A"}, msProduct{Title: "T"}, msUser{Name: "C"},
		},
		{
			// Every payload precedes every discriminator, so all three defer to the
			// merged tape and all three resolve at struct close.
			"all discs after all payloads",
			`{"svar1":{"name":"A"},"svar2":{"title":"T"},"svar3":{"name":"C"},"kind":"user","cate":"product","zone":"user"}`,
			msUser{Name: "A"}, msProduct{Title: "T"}, msUser{Name: "C"},
		},
		{
			// One resolves at its field site, the others defer. Mixed resolution is
			// where a shared per host slot would have shown up.
			"interleaved",
			`{"kind":"user","svar2":{"title":"T"},"cate":"product","svar1":{"name":"A"},"zone":"user","svar3":{"name":"C"}}`,
			msUser{Name: "A"}, msProduct{Title: "T"}, msUser{Name: "C"},
		},
		{
			"only the middle one present",
			`{"cate":"product","svar2":{"title":"only"}}`,
			nil, msProduct{Title: "only"}, nil,
		},
		{
			"no variant keys at all",
			`{}`,
			nil, nil, nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h msTriple
			if err := Unmarshal([]byte(c.src), &h); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.src, err)
			}
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var hv msTriple
			if err := UnmarshalValue(val, &hv); err != nil {
				t.Fatalf("UnmarshalValue(%s): %v", c.src, err)
			}
			for what, got := range map[string]msTriple{"Unmarshal": h, "UnmarshalValue": hv} {
				for i, pair := range [][2]any{
					{got.SVar1, c.want1}, {got.SVar2, c.want2}, {got.SVar3, c.want3},
				} {
					if pair[1] == nil {
						if pair[0] != nil {
							t.Errorf("%s: SVar%d = %#v, want nil", what, i+1, pair[0])
						}
						continue
					}
					if pair[0] != pair[1] {
						t.Errorf("%s: SVar%d = %#v, want %#v", what, i+1, pair[0], pair[1])
					}
				}
			}
		})
	}
}

func msMiddleVariantIdx(t *testing.T, root reflect.Type) uint16 {
	t.Helper()
	tt, err := vbind.TypeTreeOf(root)
	if err != nil {
		t.Fatalf("TypeTreeOf: %v", err)
	}
	middle, _ := reflect.TypeFor[msTriple]().FieldByName("SVar2")
	disc, _ := reflect.TypeFor[msTriple]().FieldByName("Cate")
	var matches []uint16
	for i := range tt.Fields {
		field := &tt.Fields[i]
		if field.Offset != uint32(middle.Offset) || field.Flags&uint32(vbind.TagVariant) == 0 {
			continue
		}
		idx := vbind.FieldPolyIdx(field)
		if int(idx) < len(tt.Polys) && tt.Polys[idx].DiscFieldOff == uint32(disc.Offset) {
			matches = append(matches, idx)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("middle sibling variant matches = %v, want one table for Cate", matches)
	}
	if matches[0] == 0 {
		t.Fatal("middle sibling variant index = 0, want a nonzero table index")
	}
	return matches[0]
}

func TestMultiSibling_UnknownMiddleDiscError(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		root    reflect.Type
		newDest func() any
	}{
		{"root", `{"svar2":{"title":"T"},"cate":"nosuch"}`, reflect.TypeFor[msTriple](), func() any { return new(msTriple) }},
		{"nested", `{"inner":{"svar2":{"title":"T"},"cate":"nosuch"}}`, reflect.TypeFor[msNestedHost](), func() any { return new(msNestedHost) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantIdx := msMiddleVariantIdx(t, c.root)
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			paths := []struct {
				name string
				run  func(any) error
			}{
				{"Unmarshal", func(dst any) error { return Unmarshal([]byte(c.src), dst) }},
				{"UnmarshalValue", func(dst any) error { return UnmarshalValue(val, dst) }},
			}
			for _, path := range paths {
				t.Run(path.name, func(t *testing.T) {
					err := path.run(c.newDest())
					var variantErr *VariantError
					if !errors.As(err, &variantErr) {
						t.Fatalf("error = %v, want *VariantError", err)
					}
					if variantErr.VariantIdx != wantIdx {
						t.Errorf("VariantIdx = %d, want %d", variantErr.VariantIdx, wantIdx)
					}
					if variantErr.Pos != 0 {
						t.Errorf("Pos = %d, want 0", variantErr.Pos)
					}
					if variantErr.Host != reflect.TypeFor[msTriple]().String() {
						t.Errorf("Host = %q, want %q", variantErr.Host, reflect.TypeFor[msTriple]().String())
					}
					if !strings.Contains(variantErr.Message, "nosuch") {
						t.Errorf("Message = %q, want actual discriminator", variantErr.Message)
					}
				})
			}
		})
	}
}

// Two siblings may name one discriminator, which then selects the same case type
// for both. This is the shape that made the old vdisc stamping ambiguous.
func TestMultiSibling_SharedDiscriminator(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any
	}{
		{"disc first", `{"kind":"user","a":{"name":"A"},"b":{"name":"B"}}`, msUser{}},
		{"disc last", `{"a":{"name":"A"},"b":{"name":"B"},"kind":"user"}`, msUser{}},
		{"disc between", `{"a":{"name":"A"},"kind":"user","b":{"name":"B"}}`, msUser{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h msShared
			if err := Unmarshal([]byte(c.src), &h); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.src, err)
			}
			a, aOK := h.A.(msUser)
			b, bOK := h.B.(msUser)
			if !aOK || !bOK {
				t.Fatalf("A = %#v, B = %#v, want both msUser", h.A, h.B)
			}
			if a.Name != "A" {
				t.Errorf("A.Name = %q, want %q", a.Name, "A")
			}
			if b.Name != "B" {
				t.Errorf("B.Name = %q, want %q", b.Name, "B")
			}
		})
	}
}

// The embedded variant still unfolds into the host object while two siblings bind
// their own members, all with distinct discriminators.
func TestMultiSibling_WithEmbeddedVariant(t *testing.T) {
	const src = `{"kind":"product","svar1":{"title":"S1"},"name":"inlined","type":"user","cate":"user","svar2":{"name":"S2"}}`
	var h msInlinePlusSiblings
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, ok := h.Inl.(msUser); !ok || got.Name != "inlined" {
		t.Errorf("Inl = %#v, want msUser{Name:\"inlined\"}", h.Inl)
	}
	if got, ok := h.SVar1.(msProduct); !ok || got.Title != "S1" {
		t.Errorf("SVar1 = %#v, want msProduct{Title:\"S1\"}", h.SVar1)
	}
	if got, ok := h.SVar2.(msUser); !ok || got.Name != "S2" {
		t.Errorf("SVar2 = %#v, want msUser{Name:\"S2\"}", h.SVar2)
	}
}
