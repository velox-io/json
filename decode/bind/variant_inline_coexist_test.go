package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Tests for an inline variant coexisting with a reserve-unknown on the
// same host. These are the same struct close, and the interesting property is
// that the two consumers want disjoint halves of one merged tape:
//
//   - the inline case wants the members it declares (they unfold into it);
//   - the reserve-unknown wants everything neither the host nor the case declares.
//
// phase1 cannot split them at the field site, because telling a case member from
// an unknown key requires the case, which requires the discriminator, which may
// arrive last. So every undecidable member goes onto one merged tape and the
// struct-close pass splits it, keyed on the host field table plus the selected
// case's. Interleaving in the JSON is therefore the case worth pinning: it forces
// the split to work without moving anything.

type coexistHost struct {
	Type string      `json:"type"`
	Data any         `json:",embed" vjson:"variant=type"`
	Exts value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[coexistHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
}

// TestCoexistInlineVariantReserveUnknown_Interleaved is the core case: case
// members and unknown keys alternate, and the discriminator sits in the middle.
// Every member before "type" was taped without knowing which half it belonged to.
func TestCoexistInlineVariantReserveUnknown_Interleaved(t *testing.T) {
	src := `{"name":"bob","ext1":1,"type":"user","role":"admin","ext2":{"deep":true}}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" {
		t.Errorf("Type = %q, want %q", h.Type, "user")
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "bob" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {bob admin}", u)
	}
	if h.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject", h.Exts.Type())
	}
	if h.Exts.Len() != 2 {
		t.Fatalf("Exts.Len = %d, want 2 (ext1, ext2)", h.Exts.Len())
	}
	ext1 := h.Exts.Get("ext1")
	if n, ok := ext1.Int(); !ok || n != 1 {
		t.Errorf("Exts.ext1 = %d (ok=%v), want 1", n, ok)
	}
	// A container copied into the reserve-unknown's tape carries paired indices
	// relative to the merged tape it was written on; reading it back proves the
	// copy rebased them onto the reserve-unknown's own base.
	ext2 := h.Exts.Get("ext2")
	if ext2.Type() != value.KindObject {
		t.Fatalf("Exts.ext2.Type = %v, want KindObject", ext2.Type())
	}
	deep := ext2.Get("deep")
	if b, ok := deep.Bool(); !ok || !b {
		t.Errorf("Exts.ext2.deep = %v (ok=%v), want true", b, ok)
	}
}

// TestCoexistInlineVariantReserveUnknown_DiscFirst is the ordering where the
// discriminator arrives before anything undecidable. The case is known while the
// members are still being read, but they are taped anyway: the split is driven by
// the tape, not by whether it could have been avoided.
func TestCoexistInlineVariantReserveUnknown_DiscFirst(t *testing.T) {
	src := `{"type":"product","title":"Widget","price":99,"vendor":"acme"}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	p, ok := h.Data.(inlProduct)
	if !ok {
		t.Fatalf("Data = %T, want inlProduct", h.Data)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("Data = %+v, want {Widget 99}", p)
	}
	if h.Exts.Len() != 1 {
		t.Fatalf("Exts.Len = %d, want 1 (vendor)", h.Exts.Len())
	}
	vendor := h.Exts.Get("vendor")
	if s, ok := vendor.Str(); !ok || s != "acme" {
		t.Errorf("Exts.vendor = %q (ok=%v), want %q", s, ok, "acme")
	}
}

// TestCoexistInlineVariantReserveUnknown_DiscLast puts the discriminator after
// every member, so nothing can be classified until the close.
func TestCoexistInlineVariantReserveUnknown_DiscLast(t *testing.T) {
	src := `{"name":"carol","role":"dev","extra":[1,2,3],"type":"user"}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "carol" || u.Role != "dev" {
		t.Errorf("Data = %+v, want {carol dev}", u)
	}
	if h.Exts.Len() != 1 {
		t.Fatalf("Exts.Len = %d, want 1 (extra)", h.Exts.Len())
	}
	extra := h.Exts.Get("extra")
	if extra.Type() != value.KindArray || extra.Len() != 3 {
		t.Errorf("Exts.extra = %v len %d, want array of 3", extra.Type(), extra.Len())
	}
}

// TestCoexistInlineVariantReserveUnknown_NoUnknown verifies the reserve-unknown
// reads as an empty object rather than as invalid when the case claims every
// member. The empty tape has to be the compact two-word form: readers derive an
// empty container's close from its count and ignore the paired index.
func TestCoexistInlineVariantReserveUnknown_NoUnknown(t *testing.T) {
	src := `{"type":"user","name":"dave","role":"ops"}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if u, ok := h.Data.(inlUser); !ok || u.Name != "dave" {
		t.Fatalf("Data = %#v, want inlUser{dave ops}", h.Data)
	}
	if h.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject (empty, not invalid)", h.Exts.Type())
	}
	if h.Exts.Len() != 0 {
		t.Errorf("Exts.Len = %d, want 0", h.Exts.Len())
	}
}

// TestCoexistInlineVariantReserveUnknown_AllUnknown is the mirror: the case
// declares nothing present, so every member lands in the reserve-unknown.
func TestCoexistInlineVariantReserveUnknown_AllUnknown(t *testing.T) {
	src := `{"type":"user","a":1,"b":2,"c":3}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := h.Data.(inlUser); !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if h.Exts.Len() != 3 {
		t.Errorf("Exts.Len = %d, want 3", h.Exts.Len())
	}
}

// TestCoexistInlineVariantReserveUnknown_AbsentDisc: with no discriminator no
// case is selected, so nothing is case content and the sink collects everything.
// This is the shape where the two features interact most: the A/B split is keyed
// on the selected case's field table, and here there is no selected case, so the
// keys that WOULD have been the case's (name, role) become leftover and land in
// Exts alongside the genuine unknowns.
func TestCoexistInlineVariantReserveUnknown_AbsentDisc(t *testing.T) {
	src := `{"name":"bob","ext1":1,"role":"admin"}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data != nil {
		t.Errorf("Data = %v, want nil: no discriminator names no case", h.Data)
	}
	if h.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject", h.Exts.Type())
	}
	if h.Exts.Len() != 3 {
		t.Fatalf("Exts.Len = %d, want 3 (name, ext1, role all leftover)", h.Exts.Len())
	}
	for k, want := range map[string]string{"name": "bob", "role": "admin"} {
		v := h.Exts.Get(k)
		if got, ok := v.Str(); !ok || got != want {
			t.Errorf("Exts.%s = %q (ok=%v), want %q", k, got, ok, want)
		}
	}
	ext1 := h.Exts.Get("ext1")
	if n, ok := ext1.Int(); !ok || n != 1 {
		t.Errorf("Exts.ext1 = %d (ok=%v), want 1", n, ok)
	}
}

// TestCoexistInlineVariantReserveUnknown_AbsentDiscEmptyObject: `{}` with both
// features present must publish an empty sink Value and a nil eface, not error.
func TestCoexistInlineVariantReserveUnknown_AbsentDiscEmptyObject(t *testing.T) {
	var h coexistHost
	if err := Unmarshal([]byte(`{}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data != nil {
		t.Errorf("Data = %v, want nil", h.Data)
	}
	if !h.Exts.Valid() {
		t.Fatal("Exts invalid; want a valid empty object")
	}
	if h.Exts.Type() != value.KindObject || h.Exts.Len() != 0 {
		t.Errorf("Exts = %v len %d, want empty object", h.Exts.Type(), h.Exts.Len())
	}
}

// TestCoexistInlineVariantReserveUnknown_StrictMode verifies strict mode stays
// inert here: with a reserve-unknown present, an unmatched key is collected, not
// rejected. The reject only applies when nothing volunteered to carry it.
func TestCoexistInlineVariantReserveUnknown_StrictMode(t *testing.T) {
	src := `{"type":"user","name":"eve","surprise":true}`
	var h coexistHost
	if err := Unmarshal([]byte(src), &h, WithDisallowUnknownFields()); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Exts.Len() != 1 {
		t.Errorf("Exts.Len = %d, want 1 (surprise collected, not rejected)", h.Exts.Len())
	}
}

// TestCoexistInlineVariantReserveUnknown_Repeated reuses one Parser so the shared
// tape arena and slot-class cursors advance across parses. A merged tape whose
// bookkeeping assumed a zero base, or a seam left unstitched, drifts into a
// failure only after the cursors have moved.
func TestCoexistInlineVariantReserveUnknown_Repeated(t *testing.T) {
	p, err := NewParser[coexistHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte(`{"name":"bob","ext1":{"x":1},"type":"user","role":"admin","ext2":[1,2]}`)
	for i := range 8 {
		var h coexistHost
		if err := p.Unmarshal(src, &h); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		u, ok := h.Data.(inlUser)
		if !ok {
			t.Fatalf("iter %d: Data = %T, want inlUser", i, h.Data)
		}
		if u.Name != "bob" || u.Role != "admin" {
			t.Fatalf("iter %d: Data = %+v, want {bob admin}", i, u)
		}
		if h.Exts.Len() != 2 {
			t.Fatalf("iter %d: Exts.Len = %d, want 2", i, h.Exts.Len())
		}
		ext1 := h.Exts.Get("ext1")
		x := ext1.Get("x")
		if n, ok := x.Int(); !ok || n != 1 {
			t.Fatalf("iter %d: Exts.ext1.x = %d (ok=%v), want 1", i, n, ok)
		}
	}
}

// TestCoexistInlineVariantReserveUnknown_ShapeFlags pins that this pair, and not
// either feature alone, is what asks for the split-tape arena.
func TestCoexistInlineVariantReserveUnknown_ShapeFlags(t *testing.T) {
	p, err := NewParser[coexistHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if !p.tt.HasSplitTape {
		t.Error("HasSplitTape = false; want true")
	}
	if !p.tt.HasPolyField {
		t.Error("HasPolyField = false; want true")
	}
}

// --- jump tape → typed object via the inline case descent ---
//
// phase2 excises unknowns out of tape A into tape B, leaving seams in
// A where the excisions happened. The inline case descent then walks A as the
// case type at phase2_finish, threading those seams via the same tape-bind
// walk that UnmarshalValue runs. The robustness question is whether that walk
// still binds correctly when the case type itself has a value.Value field: it
// must skip seams between entries to reach the field, then alias the field's
// sub-span. A regression that read a seam word as content, or miscounted after
// a jump, would corrupt the value.Value field's coordinates while leaving
// scalar neighbors apparently fine.
//
// The companion jump_tape_unmarshal_value_test.go covers the other consumer
// of a seam-threaded tape: a reserve-unknown Value (produced with seam
// seams by the sibling-variant configuration) handed to the public
// UnmarshalValue API.

// inlUserMeta is an inline case that declares its own value.Value field. The
// case descent into jump-linked tape A must skip the seams over excised
// unknowns to reach Meta and alias its sub-span.
type inlUserMeta struct {
	Name string      `json:"name"`
	Role string      `json:"role"`
	Meta value.Value `json:"meta"`
}

type coexistMetaHost struct {
	Type string      `json:"type"`
	Data any         `json:",embed" vjson:"variant=type"`
	Exts value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[coexistMetaHost, struct {
		_ inlUserMeta `case:"user"`
	}]()
}

// TestCoexistInlineVariant_JumpTapeCaseWithValueField interleaves case members,
// the discriminator, a case-declared value.Value field, and unknowns. phase2
// excises the unknowns into tape B (seams in A) and the case descent
// walks the jump-linked A as inlUserMeta: it skips seams over the excised
// unknowns, skips the discriminator (unknown to the case), binds Name/Role, and
// aliases Meta's sub-span. The value.Value field proves the tape-bind walk
// recovers a correct sub-span from a non-contiguous tape.
func TestCoexistInlineVariant_JumpTapeCaseWithValueField(t *testing.T) {
	src := `{"name":"bob","ext1":1,"type":"user","role":"admin","meta":{"k":1},"ext2":2}`
	var h coexistMetaHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := h.Data.(inlUserMeta)
	if !ok {
		t.Fatalf("Data = %T, want inlUserMeta", h.Data)
	}
	if u.Name != "bob" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {bob admin ...}", u)
	}
	if u.Meta.Type() != value.KindObject {
		t.Fatalf("Meta.Type = %v, want KindObject", u.Meta.Type())
	}
	if u.Meta.Len() != 1 {
		t.Fatalf("Meta.Len = %d, want 1", u.Meta.Len())
	}
	k := u.Meta.Get("k")
	if n, ok := k.Int(); !ok || n != 1 {
		t.Errorf("Meta.k = %d (ok=%v), want 1", n, ok)
	}
	// The reserve-unknown collected the two excised unknowns.
	if h.Exts.Len() != 2 {
		t.Fatalf("Exts.Len = %d, want 2 (ext1, ext2)", h.Exts.Len())
	}
	ext1 := h.Exts.Get("ext1")
	if n, ok := ext1.Int(); !ok || n != 1 {
		t.Errorf("Exts.ext1 = %d (ok=%v), want 1", n, ok)
	}
	ext2 := h.Exts.Get("ext2")
	if n, ok := ext2.Int(); !ok || n != 2 {
		t.Errorf("Exts.ext2 = %d (ok=%v), want 2", n, ok)
	}
}
