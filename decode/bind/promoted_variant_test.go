package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// A variant/kindof descriptor is a property of the field's tag, so it belongs to
// the type that writes that tag. Promotion moves the field into a host's key
// space, but it does not move the tag, and so must not move where the descriptor
// is looked up. Registering on every type that happens to embed the declaring
// type would be busywork the author cannot even enumerate.
//
// The method form (JSONVariantCases) never had this problem: Go promotes methods
// alongside the field. Only the registry form, keyed on reflect.Type, could
// diverge, and the two forms are documented as equivalent in docs/manual/poly.md.
//
// Note what does NOT follow declaration: the discriminator. It is resolved by
// JSON name against the flattened field set, so an outer field shadowing the
// declaring type's discriminator legitimately becomes the discriminator. See
// TestPromotedVariant_ShadowedDiscriminator.

type promoInner struct {
	Name string      `json:"name"`
	Rest value.Value `json:",embed"`
	Obj  any         `json:",embed" vjson:"variant=name"`
}

type promoBob struct {
	Greet string `json:"greet"`
}

type promoCarl struct {
	Title string `json:"title"`
}

// promoHost promotes promoInner's variant field. Nothing is registered for
// promoHost itself.
type promoHost struct {
	promoInner
	Extra string `json:"extra"`
}

// promoShadowHost additionally declares "name", shadowing the declaring type's
// discriminator field.
type promoShadowHost struct {
	promoInner
	Name string `json:"name"`
}

// promoPadHost puts the shadowing field at an offset far from promoInner.Name, so
// a dispatch that read the discriminator by the declaring type's offset instead of
// by name would read the wrong bytes.
type promoPadHost struct {
	promoInner
	Pad  [7]int64 `json:"pad"`
	Name string   `json:"name"`
}

func init() {
	vbind.DefineVariantCases[promoInner, struct {
		bob  promoBob
		carl promoCarl
	}]()
}

// TestPromotedVariant_RegistryFollowsDeclaringType is the core case: the host
// registered nothing, and the build must find the descriptor on the type that
// declares the field.
func TestPromotedVariant_RegistryFollowsDeclaringType(t *testing.T) {
	var h promoHost
	if err := Unmarshal([]byte(`{"name":"bob","greet":"hello","a":1}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Name != "bob" {
		t.Errorf("Name = %q, want %q", h.Name, "bob")
	}
	b, ok := h.Obj.(promoBob)
	if !ok {
		t.Fatalf("Obj = %T, want promoBob", h.Obj)
	}
	if b.Greet != "hello" {
		t.Errorf("Greet = %q, want %q", b.Greet, "hello")
	}
	if h.Rest.Len() != 1 {
		t.Errorf("Rest.Len = %d, want 1 (a)", h.Rest.Len())
	}
}

// TestPromotedVariant_SelectsPerDiscriminator proves the discriminator is really
// read rather than a single case being hardwired: a different value picks a
// different case type.
func TestPromotedVariant_SelectsPerDiscriminator(t *testing.T) {
	var h promoHost
	if err := Unmarshal([]byte(`{"name":"carl","title":"T"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Obj.(promoCarl)
	if !ok {
		t.Fatalf("Obj = %T, want promoCarl", h.Obj)
	}
	if c.Title != "T" {
		t.Errorf("Title = %q, want %q", c.Title, "T")
	}
}

// TestPromotedVariant_ShadowedDiscriminator pins the deliberate asymmetry. The
// descriptor follows the declaring type, but the discriminator follows the
// flattened key space: "name" resolves to the outer field, and the declaring
// type's own Name stays zero because no JSON key reaches it any more. One key,
// one owner.
func TestPromotedVariant_ShadowedDiscriminator(t *testing.T) {
	var h promoShadowHost
	if err := Unmarshal([]byte(`{"name":"carl","title":"T"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Name != "carl" {
		t.Errorf("outer Name = %q, want %q", h.Name, "carl")
	}
	if h.promoInner.Name != "" {
		t.Errorf("shadowed inner Name = %q, want empty; the outer field owns the key", h.promoInner.Name)
	}
	if _, ok := h.Obj.(promoCarl); !ok {
		t.Fatalf("Obj = %T, want promoCarl; the shadowing field is the discriminator", h.Obj)
	}
}

// TestPromotedVariant_ShadowedDiscriminatorAtDistantOffset is the same shape with
// the shadowing field moved far away in memory. Resolution is by JSON name, so
// the offset must not matter; reading by the declaring type's offset would land in
// the padding array.
func TestPromotedVariant_ShadowedDiscriminatorAtDistantOffset(t *testing.T) {
	var h promoPadHost
	if err := Unmarshal([]byte(`{"name":"bob","greet":"hi"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Name != "bob" {
		t.Errorf("outer Name = %q, want %q", h.Name, "bob")
	}
	b, ok := h.Obj.(promoBob)
	if !ok {
		t.Fatalf("Obj = %T, want promoBob", h.Obj)
	}
	if b.Greet != "hi" {
		t.Errorf("Greet = %q, want %q", b.Greet, "hi")
	}
}

// TestPromotedVariant_UnknownDiscriminatorStillErrors verifies the promoted field
// keeps its error behavior: finding the descriptor by declaring type must not
// make dispatch lenient.
func TestPromotedVariant_UnknownDiscriminatorStillErrors(t *testing.T) {
	var h promoHost
	err := Unmarshal([]byte(`{"name":"nope"}`), &h)
	if err == nil {
		t.Fatal("Unmarshal: want an error for an unregistered discriminator value, got nil")
	}
}

// --- kindof, same rule ---

type promoKindofInner struct {
	Data any `json:"data" vjson:"kindof"`
}

type promoKindofHost struct {
	promoKindofInner
	Extra string `json:"extra"`
}

func init() {
	vbind.DefineKindofCases[promoKindofInner, struct {
		object promoBob
		array  []promoBob
	}]()
}

func TestPromotedKindof_RegistryFollowsDeclaringType(t *testing.T) {
	var h promoKindofHost
	if err := Unmarshal([]byte(`{"data":{"greet":"hello"},"extra":"e"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := h.Data.(promoBob); !ok {
		t.Fatalf("Data = %T, want promoBob", h.Data)
	}

	var h2 promoKindofHost
	if err := Unmarshal([]byte(`{"data":[{"greet":"a"}]}`), &h2); err != nil {
		t.Fatalf("Unmarshal array: %v", err)
	}
	arr, ok := h2.Data.([]promoBob)
	if !ok {
		t.Fatalf("Data = %T, want []promoBob", h2.Data)
	}
	if len(arr) != 1 || arr[0].Greet != "a" {
		t.Errorf("Data = %+v, want [{a}]", arr)
	}
}

// --- the host may still register for a field it inherited ---

type promoOverrideInner struct {
	Kind string `json:"kind"`
	Obj  any    `json:"obj" vjson:"variant=kind"`
}

type promoOverrideHost struct {
	promoOverrideInner
}

func init() {
	// Registered on the promoting host, not on the declaring type. Looking up only
	// the declaring type would miss it, so both are consulted.
	vbind.DefineVariantCases[promoOverrideHost, struct {
		bob promoBob
	}]()
}

func TestPromotedVariant_HostMayRegisterForInheritedField(t *testing.T) {
	var h promoOverrideHost
	if err := Unmarshal([]byte(`{"kind":"bob","obj":{"greet":"hello"}}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	b, ok := h.Obj.(promoBob)
	if !ok {
		t.Fatalf("Obj = %T, want promoBob", h.Obj)
	}
	if b.Greet != "hello" {
		t.Errorf("Greet = %q, want %q", b.Greet, "hello")
	}
}
