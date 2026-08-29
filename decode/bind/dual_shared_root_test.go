package bind

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// The dual shared-root layout: one merged tape with two logical views carries
// exactly one object begin, one shared leading seam, and one object end. The
// begin word's count field holds the reserve projection's member count; the
// close word's high24 holds the inline projection's. Both published Values
// address the shared begin at relative index zero, and a descriptor mode bit
// (CountAtClose) is the only thing telling an escaping inline Value where its
// count lives.
//
// What this replaced was a per-dual-tape header of two extra words: a second
// object begin for view B and view A's hop seam over it. The tests here pin
// the collapsed shape directly on the tape words, because the behavioral suite
// cannot: a reader that entered through the old header still walks both views
// correctly, so only the physical assertions distinguish the layouts.

type dualRootHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type dualRootCase struct {
	Name string `json:"name"`
}

func init() {
	vbind.DefineVariantCases[dualRootHost, struct {
		_ dualRootCase `case:"c1"`
		_ value.Value  `case:"raw"`
	}]()
}

func loadDesc(v *value.Value) valueabi.Descriptor {
	return valueabi.Load(unsafe.Pointer(v))
}

// The physical shape, asserted word by word on the published sink:
//
//	rel 0  '{'  close=N, count=reserve
//	rel 1  seam (the shared leading seam)
//	rel 2  first entry or the close
//	rel N  '}'  open=0, high24=inline count
//
// No second '{' may appear inside the span, and the word after the begin must
// be a seam: in the old layout that slot was A's hop over B's root and the
// slot after it another begin.
func TestDualSharedRoot_PhysicalLayout(t *testing.T) {
	var h dualRootHost
	src := `{"u1":1,"kind":"c1","name":"bob","u2":2}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	desc := loadDesc(&h.Rest)

	if desc.Tidx != 0 {
		t.Fatalf("reserve root Tidx = %d, want 0 (the shared begin)", desc.Tidx)
	}
	begin := desc.WordAt(0)
	if tag := byte(begin >> 56); tag != valueabi.TagObjBeg {
		t.Fatalf("word 0 tag = %q, want '{'", tag)
	}
	if !valueabi.IsSeam(desc.WordAt(1)) {
		t.Fatalf("word 1 is not a seam: %#016x (the shared leading seam)", desc.WordAt(1))
	}
	closeIdx := int(begin & 0xFFFFFFFF)
	close := desc.WordAt(closeIdx)
	if tag := byte(close >> 56); tag != valueabi.TagObjEnd {
		t.Fatalf("close word %d tag = %q, want '}'", closeIdx, tag)
	}
	if open := uint32(close & 0xFFFFFFFF); open != 0 {
		t.Errorf("close names open index %d, want 0 (paired index of the shared begin)", open)
	}

	// Classification: u1 and u2 stay in the reserve view, the discriminator
	// leaves both, "name" stays in the inline view.
	if got := int((begin >> 32) & 0xFFFFFF); got != 2 {
		t.Errorf("begin.high24 = %d, want 2 (reserve count: u1, u2)", got)
	}
	if got := int((close >> 32) & 0xFFFFFF); got != 1 {
		t.Errorf("close.high24 = %d, want 1 (inline count: name)", got)
	}
	if got := h.Rest.Len(); got != 2 {
		t.Errorf("Rest.Len = %d, want 2", got)
	}

	// Exactly one begin inside the published span: no second root, no hop.
	span := int(desc.End)
	for i := 0; i < span; i++ {
		if i == 0 {
			continue
		}
		if tag := byte(desc.WordAt(i) >> 56); tag == valueabi.TagObjBeg {
			t.Errorf("word %d inside the span is a second object begin", i)
		}
	}
}

// Exact word counts for minimal hosts. The empty dual tape is three words
// (begin, leading seam, close) where the retired layout needed five, and each
// entry adds key, value, and trailing seam unchanged.
func TestDualSharedRoot_WordCounts(t *testing.T) {
	cases := []struct {
		src      string
		wantSpan int
		wantRest int
	}{
		{`{}`, 3, 0},
		{`{"kind":"c1"}`, 6, 0},
		{`{"u1":1}`, 7, 1},
	}
	for _, c := range cases {
		var h dualRootHost
		if err := Unmarshal([]byte(c.src), &h); err != nil {
			t.Fatalf("%s: Unmarshal: %v", c.src, err)
		}
		desc := loadDesc(&h.Rest)
		if got := int(desc.End); got != c.wantSpan {
			t.Errorf("%s: dual tape spans %d words, want %d", c.src, got, c.wantSpan)
		}
		if got := h.Rest.Len(); got != c.wantRest {
			t.Errorf("%s: Rest.Len = %d, want %d", c.src, got, c.wantRest)
		}
	}
}

// An escaping inline Value: the selected case is value.Value, a non-struct
// case that claims every entry before discriminator and leftover handling.
// The projection it publishes keeps the complete object (discriminator
// included) while the reserve view is left empty.
func TestDualSharedRoot_EscapingInlineValue(t *testing.T) {
	var h dualRootHost
	src := `{"kind":"raw","a":1,"b":{"c":[1,2]},"d":true}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	vv, ok := h.Case.(value.Value)
	if !ok {
		t.Fatalf("Case = %T, want value.Value", h.Case)
	}
	desc := loadDesc(&vv)

	if desc.Mode != valueabi.ModeInlineDualRoot {
		t.Errorf("escaping Value mode = %#x, want ModeInlineDualRoot (view A | CountAtClose)", desc.Mode)
	}
	if desc.Tidx != 0 {
		t.Errorf("escaping Value Tidx = %d, want 0 (the shared begin)", desc.Tidx)
	}
	if got := vv.Len(); got != 4 {
		t.Errorf("Len = %d, want 4 (kind, a, b, d; read from the close word)", got)
	}

	// The reserve view keeps nothing: the non-struct case claimed every entry.
	if got := h.Rest.Len(); got != 0 {
		t.Errorf("Rest.Len = %d, want 0", got)
	}
	if h.Rest.Type() != value.KindObject {
		t.Errorf("Rest.Type = %v, want KindObject (an empty object, not invalid)", h.Rest.Type())
	}

	// Navigation and serialization see the complete projection.
	kindVal := vv.Get("kind")
	if s, ok := kindVal.Str(); !ok || s != "raw" {
		t.Errorf("Get(kind) = %q, want raw", s)
	}
	aVal := vv.Get("a")
	if n, ok := aVal.Int(); !ok || n != 1 {
		t.Errorf("Get(a) = %d, want 1", n)
	}
	var keys []string
	vv.ForEachKey(func(k string, _ value.Value) bool { keys = append(keys, k); return true })
	if strings.Join(keys, ",") != "kind,a,b,d" {
		t.Errorf("ForEachKey walked %v, want [kind a b d]", keys)
	}
	// A nested child inherits the mode flag, but its Tidx is nonzero so its
	// count comes from its own begin word.
	bVal := vv.Get("b")
	if got := bVal.Len(); got != 1 {
		t.Errorf("nested b.Len = %d, want 1", got)
	}
	cVal := bVal.Get("c")
	if got := cVal.Len(); got != 2 {
		t.Errorf("nested b.c.Len = %d, want 2", got)
	}
	if got := vv.String(); got != src {
		t.Errorf("String = %s, want %s", got, src)
	}

	// The projection survives a round trip through UnmarshalValue, which
	// enters the native walker with the descriptor's mode as root input.
	var out struct {
		A int         `json:"a"`
		B value.Value `json:"b"`
	}
	if err := UnmarshalValue(vv, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.A != 1 {
		t.Errorf("out.A = %d, want 1", out.A)
	}
	if got := out.B.String(); got != `{"c":[1,2]}` {
		t.Errorf("out.B = %s, want {\"c\":[1,2]}", got)
	}
}

// Binding a view-B sink into a type whose own selected case is value.Value:
// the nested walk switches from the outer view-B mode to ModeInlineDualRoot,
// publishes the aliased Value with the active mode from the yield stash, and
// restores the outer mode afterwards so the host's own sink stays view B.
func TestDualSharedRoot_NestedModeSwitchThroughValueCase(t *testing.T) {
	var outer dualRootHost
	src := `{"kind":"c1","name":"bob","deep":{"kind":"raw","a":1,"extra":9}}`
	if err := Unmarshal([]byte(src), &outer); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	deep := outer.Rest.Get("deep")
	if !deep.Exists() {
		t.Fatalf("Rest has no deep member")
	}
	if deep.Type() != value.KindObject {
		t.Fatalf("deep.Type = %v, want KindObject", deep.Type())
	}

	var inner dualRootHost
	if err := UnmarshalValue(deep, &inner); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	vv, ok := inner.Case.(value.Value)
	if !ok {
		t.Fatalf("inner Case = %T, want value.Value", inner.Case)
	}
	innerDesc := loadDesc(&vv)
	if innerDesc.Mode != valueabi.ModeInlineDualRoot {
		t.Errorf("inner escaping mode = %#x, want ModeInlineDualRoot (stashed through the yield)", innerDesc.Mode)
	}
	if got := vv.Len(); got != 3 {
		t.Errorf("inner escaping Len = %d, want 3 (kind, a, extra)", got)
	}
	if got := vv.String(); got != `{"kind":"raw","a":1,"extra":9}` {
		t.Errorf("inner escaping String = %s", got)
	}
	// The nested reserve is empty because the Value case claimed everything.
	if got := inner.Rest.Len(); got != 0 {
		t.Errorf("inner Rest.Len = %d, want 0", got)
	}
}
