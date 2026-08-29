package bind

import (
	"testing"

	"github.com/velox-io/json/vbind"
)

// Phase 1 regression: sibling variant + kindof on the same host. Before the
// ensure_pushed (depth, poly_idx, kind) fix, a kindof field appearing between
// the vdisc and the variant field in JSON order would land its independent push
// on top of the stack, and the variant field's ensure_pushed (depth-only match)
// would wrongly share the kindof entry, corrupting the close-path state machine.
//
// This file exercises that exact ordering plus the symmetric orderings to
// guard against regressions.

type soUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type soProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

// soHost carries a sibling variant (Type+Data) AND an independent kindof field
// (Meta). The three poly axes coexist at the same depth.
type soHost struct {
	Type string `json:"type"`                      // vdisc for sibling variant
	Data any    `json:"data" vjson:"variant=type"` // sibling variant
	Meta any    `json:"meta" vjson:"kindof"`       // kindof (independent)
}

func init() {
	vbind.DefineVariantCases[soHost, struct {
		_ soUser    `case:"user"`
		_ soProduct `case:"product"`
	}]()
	vbind.DefineKindofCases[soHost, struct {
		bool   bool
		object soUser
		array  []soUser
	}]()
}

func TestSiblingKindofCoexistence_KindofBetweenDiscAndVariant(t *testing.T) {
	// kindof field "meta" appears BETWEEN vdisc "type" and variant "data".
	// This is the ordering that exposed the depth-only ensure_pushed bug.
	src := `{"type":"user","meta":{"name":"Bob","role":"editor"},"data":{"name":"Alice","role":"admin"}}`
	var h soHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" {
		t.Errorf("Type = %q, want %q", h.Type, "user")
	}
	u, ok := h.Data.(soUser)
	if !ok {
		t.Fatalf("Data = %T, want soUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	m, ok := h.Meta.(soUser)
	if !ok {
		t.Fatalf("Meta = %T, want soUser", h.Meta)
	}
	if m.Name != "Bob" || m.Role != "editor" {
		t.Errorf("Meta = %+v, want {Bob editor}", m)
	}
}

func TestSiblingKindofCoexistence_VariantBeforeKindofBeforeDisc(t *testing.T) {
	// Fully out-of-order: variant, then kindof, then vdisc.
	src := `{"data":{"name":"Alice","role":"admin"},"meta":[{"name":"Bob","role":"editor"}],"type":"user"}`
	var h soHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" {
		t.Errorf("Type = %q, want %q", h.Type, "user")
	}
	u, ok := h.Data.(soUser)
	if !ok {
		t.Fatalf("Data = %T, want soUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	ms, ok := h.Meta.([]soUser)
	if !ok {
		t.Fatalf("Meta = %T, want []soUser", h.Meta)
	}
	if len(ms) != 1 || ms[0].Name != "Bob" || ms[0].Role != "editor" {
		t.Errorf("Meta = %+v, want [{{Bob editor}}]", ms)
	}
}

func TestSiblingKindofCoexistence_KindofScalarAndVariant(t *testing.T) {
	// Scalar kindof case (bool) + variant, disc first.
	src := `{"type":"product","meta":true,"data":{"title":"Widget","price":99}}`
	var h soHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "product" {
		t.Errorf("Type = %q, want %q", h.Type, "product")
	}
	p, ok := h.Data.(soProduct)
	if !ok {
		t.Fatalf("Data = %T, want soProduct", h.Data)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("Data = %+v, want {Widget 99}", p)
	}
	b, ok := h.Meta.(bool)
	if !ok {
		t.Fatalf("Meta = %T, want bool", h.Meta)
	}
	if !b {
		t.Errorf("Meta = %v, want true", b)
	}
}

func TestSiblingKindofCoexistence_UnknownDisc(t *testing.T) {
	// Unknown disc on the sibling variant; kindof should still bind correctly.
	src := `{"type":"unknown","meta":42,"data":{"name":"Alice","role":"admin"}}`
	var h soHost
	if err := Unmarshal([]byte(src), &h); err == nil {
		t.Fatalf("Unmarshal: want error for unknown discriminator, got nil (Data=%v Meta=%v)", h.Data, h.Meta)
	}
}
