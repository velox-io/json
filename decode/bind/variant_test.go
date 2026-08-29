package bind

import (
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/vbind"
)

// Variant test types. Each envelope has a Type discriminator (vdisc) and a
// Data field (variant) whose concrete type is selected by Type's value.

type variantUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type variantProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

// variantEnvelopeSibling uses sibling-tag layout: Data is a real JSON member.
type variantEnvelopeSibling struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

// variantEnvelopeMethod uses the method-form descriptor.
type variantEnvelopeMethod struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func (variantEnvelopeMethod) JSONVariantCases(struct {
	user    variantUser
	product variantProduct
}) {
}

func init() {
	// Registry-form descriptor for variantEnvelopeSibling.
	vbind.DefineVariantCases[variantEnvelopeSibling, struct {
		_ variantUser    `case:"user"`
		_ variantProduct `case:"product"`
	}]()
}

// TestVariantSibling_DiscriminatorFirst verifies the common case: the
// discriminator appears before the variant value in the JSON object. The
// uniform buffer+rebind path still applies (variant field is buffered and
// rebound at object_close), but the discriminator is already in the Go struct
// when the rebind runs.
func TestVariantSibling_DiscriminatorFirst(t *testing.T) {
	src := `{"type":"user","data":{"name":"Alice","role":"admin"}}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Type != "user" {
		t.Errorf("Type = %q, want %q", env.Type, "user")
	}
	u, ok := env.Data.(variantUser)
	if !ok {
		t.Fatalf("Data = %T, want variantUser", env.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

// TestVariantSibling_VariantFirst verifies out-of-order: the variant value
// appears before the discriminator. The variant is buffered, the discriminator
// is parsed later, and the rebind at object_close uses the now-known value.
func TestVariantSibling_VariantFirst(t *testing.T) {
	src := `{"data":{"name":"Alice","role":"admin"},"type":"user"}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Type != "user" {
		t.Errorf("Type = %q, want %q", env.Type, "user")
	}
	u, ok := env.Data.(variantUser)
	if !ok {
		t.Fatalf("Data = %T, want variantUser", env.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

// TestVariantSibling_Product verifies a different case value selects a
// different concrete type.
func TestVariantSibling_Product(t *testing.T) {
	src := `{"type":"product","data":{"title":"Widget","price":99}}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pr, ok := env.Data.(variantProduct)
	if !ok {
		t.Fatalf("Data = %T, want variantProduct", env.Data)
	}
	if pr.Title != "Widget" || pr.Price != 99 {
		t.Errorf("Data = %+v, want {Widget 99}", pr)
	}
}

// TestVariantSibling_MethodForm verifies the method-form descriptor
// (JSONVariantCases) is discovered via reflection and dispatches correctly.
func TestVariantSibling_MethodForm(t *testing.T) {
	src := `{"type":"user","data":{"name":"Bob","role":"user"}}`
	var env variantEnvelopeMethod
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(variantUser)
	if !ok {
		t.Fatalf("Data = %T, want variantUser", env.Data)
	}
	if u.Name != "Bob" || u.Role != "user" {
		t.Errorf("Data = %+v, want {Bob user}", u)
	}
}

// TestVariantSibling_UnknownDiscriminator verifies an unknown discriminator
// value yields an error.
func TestVariantSibling_UnknownDiscriminator(t *testing.T) {
	src := `{"type":"unknown","data":{"name":"Alice"}}`
	var env variantEnvelopeSibling
	err := Unmarshal([]byte(src), &env)
	if err == nil {
		t.Fatal("expected error for unknown discriminator, got nil")
	}
}

// TestVariantSibling_MissingDiscriminator verifies a missing discriminator
// field yields an error.
func TestVariantSibling_MissingDiscriminator(t *testing.T) {
	src := `{"data":{"name":"Alice","role":"admin"}}`
	var env variantEnvelopeSibling
	err := Unmarshal([]byte(src), &env)
	if err == nil {
		t.Fatal("expected error for missing discriminator, got nil")
	}
}

// variantEnvelopeOuter is a nested variant host: its Data can be another
// variant envelope or a direct variantUser.
type variantEnvelopeOuter struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[variantEnvelopeOuter, struct {
		_ variantEnvelopeSibling `case:"wrap"`
		_ variantUser            `case:"direct"`
	}]()
}

// TestVariantSibling_NestedVariant verifies a variant whose selected type is
// itself a variant-bearing struct (nested envelopes). The inner variant
// triggers a nested tape-bind sub-routine walk which recurses.
func TestVariantSibling_NestedVariant(t *testing.T) {
	// Outer envelope: type=wrap, data=inner envelope.
	// Inner envelope: type=user, data=User.
	src := `{"type":"wrap","data":{"type":"user","data":{"name":"Carol","role":"owner"}}}`
	var env variantEnvelopeOuter
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	inner, ok := env.Data.(variantEnvelopeSibling)
	if !ok {
		t.Fatalf("outer Data = %T, want variantEnvelopeSibling", env.Data)
	}
	if inner.Type != "user" {
		t.Errorf("inner Type = %q, want %q", inner.Type, "user")
	}
	u, ok := inner.Data.(variantUser)
	if !ok {
		t.Fatalf("inner Data = %T, want variantUser", inner.Data)
	}
	if u.Name != "Carol" {
		t.Errorf("inner Data.Name = %q, want %q", u.Name, "Carol")
	}
}

// TestVariantSibling_DirectCase verifies the "direct" case in the outer
// envelope (non-envelope target type).
func TestVariantSibling_DirectCase(t *testing.T) {
	src := `{"type":"direct","data":{"name":"Dave","role":"guest"}}`
	var env variantEnvelopeOuter
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(variantUser)
	if !ok {
		t.Fatalf("Data = %T, want variantUser", env.Data)
	}
	if u.Name != "Dave" || u.Role != "guest" {
		t.Errorf("Data = %+v, want {Dave guest}", u)
	}
}

// TestVariantSibling_RepeatedParse verifies the Parser is reusable across
// multiple parses (the poly_stack and stash reset cleanly).
func TestVariantSibling_RepeatedParse(t *testing.T) {
	parser, err := NewParser[variantEnvelopeSibling]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for i := range 5 {
		var env variantEnvelopeSibling
		src := `{"type":"user","data":{"name":"Eve","role":"admin"}}`
		if err := parser.Unmarshal([]byte(src), &env); err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
		if env.Type != "user" {
			t.Errorf("parse %d: Type = %q", i, env.Type)
		}
		if u, ok := env.Data.(variantUser); !ok || u.Name != "Eve" {
			t.Errorf("parse %d: Data = %+v", i, env.Data)
		}
	}
}

// TestVariantSibling_TypeAssertion confirms the bound value's dynamic type
// matches the descriptor's declared type (eface boxing correctness).
func TestVariantSibling_TypeAssertion(t *testing.T) {
	src := `{"type":"user","data":{"name":"Frank","role":"dev"}}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// The eface should hold a variantUser (value type, not pointer).
	if reflect.TypeOf(env.Data) != reflect.TypeFor[variantUser]() {
		t.Errorf("Data type = %T, want variantUser (value)", env.Data)
	}
}

// TestVariantSibling_LongDiscriminatorTruncated verifies a long/unknown
// discriminator value yields an error whose message is bounded, not the
// full user input. Without truncation a malicious or pathological value
// could inflate the error string unboundedly.
func TestVariantSibling_LongDiscriminatorTruncated(t *testing.T) {
	long := strings.Repeat("x", 200)
	src := `{"type":"` + long + `","data":{"name":"Alice"}}`
	var env variantEnvelopeSibling
	err := Unmarshal([]byte(src), &env)
	if err == nil {
		t.Fatal("expected error for long unknown discriminator, got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, long) {
		t.Errorf("error message echoes full long discriminator (%d bytes); should truncate", len(long))
	}
	if !strings.Contains(msg, "(truncated)") {
		t.Errorf("error message missing truncation marker: %q", msg)
	}
}

// --- non-struct variant case types ---
//
// The unified walker (value.UnmarshalValueInto) handles all Kinds, so variant
// case types are no longer limited to structs. These tests exercise slice and
// map-bearing case types through the parser path (Unmarshal), including the
// out-of-order case (data before vdisc) which triggers the cold-path tape-bind.
//
// Pure map case types are not tested here: the variant builder registers case
// slots via registerSlotClass (not registerMapSlotClass), so the *hmap is not
// pre-wired. A struct with a map field works because collect routes the field
// through registerMapSlotClass. Pointer case types are rejected by the builder.

type variantSliceCase struct {
	Items []int    `json:"items"`
	Tags  []string `json:"tags"`
}

type variantMapStructCase struct {
	Counts map[string]int `json:"counts"`
	Label  string         `json:"label"`
}

type variantEnvelopeNonStruct struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[variantEnvelopeNonStruct, struct {
		_ []int                `case:"ints"`
		_ variantSliceCase     `case:"slicestruct"`
		_ variantMapStructCase `case:"mapstruct"`
	}]()
}

// variantEnvelopePtrMap exercises pure map and pointer case types: a
// map[string]int case (KindMap at the case root) and a *variantUser case
// (KindPointer at the case root). The unified walker handles both; the
// pointer case was previously rejected by the builder.
type variantEnvelopePtrMap struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[variantEnvelopePtrMap, struct {
		_ map[string]int `case:"counts"`
		_ *variantUser   `case:"ptruser"`
	}]()
}

// TestVariantNonStruct_SliceCase verifies a pure []int case type through the
// parser path. The case slot holds the slice header; walkSlice carves the
// element backing via CarveSlice.
func TestVariantNonStruct_SliceCase(t *testing.T) {
	src := `{"type":"ints","data":[1,2,3]}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ints, ok := env.Data.([]int)
	if !ok {
		t.Fatalf("Data = %T, want []int", env.Data)
	}
	if len(ints) != 3 || ints[0] != 1 || ints[2] != 3 {
		t.Errorf("Data = %+v, want [1 2 3]", ints)
	}
}

// TestVariantNonStruct_SliceCaseVariantFirst triggers the rebind path
// (out-of-order: data before vdisc) with a pure []int case type.
func TestVariantNonStruct_SliceCaseVariantFirst(t *testing.T) {
	src := `{"data":[1,2,3],"type":"ints"}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ints, ok := env.Data.([]int)
	if !ok {
		t.Fatalf("Data = %T, want []int", env.Data)
	}
	if len(ints) != 3 || ints[0] != 1 || ints[2] != 3 {
		t.Errorf("Data = %+v, want [1 2 3]", ints)
	}
}

// TestVariantNonStruct_SliceStructCase verifies a struct case with slice
// fields through the parser path.
func TestVariantNonStruct_SliceStructCase(t *testing.T) {
	src := `{"type":"slicestruct","data":{"items":[1,2,3],"tags":["a","b"]}}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	s, ok := env.Data.(variantSliceCase)
	if !ok {
		t.Fatalf("Data = %T, want variantSliceCase", env.Data)
	}
	if len(s.Items) != 3 || s.Items[0] != 1 || s.Items[2] != 3 {
		t.Errorf("Items = %+v, want [1 2 3]", s.Items)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "a" || s.Tags[1] != "b" {
		t.Errorf("Tags = %+v, want [a b]", s.Tags)
	}
}

// TestVariantNonStruct_SliceStructCaseVariantFirst triggers the rebind path
// with a struct case containing slice fields.
func TestVariantNonStruct_SliceStructCaseVariantFirst(t *testing.T) {
	src := `{"data":{"items":[1,2,3],"tags":["a","b"]},"type":"slicestruct"}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	s, ok := env.Data.(variantSliceCase)
	if !ok {
		t.Fatalf("Data = %T, want variantSliceCase", env.Data)
	}
	if len(s.Items) != 3 || s.Items[2] != 3 {
		t.Errorf("Items = %+v, want [1 2 3]", s.Items)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "a" {
		t.Errorf("Tags = %+v, want [a b]", s.Tags)
	}
}

// TestVariantNonStruct_MapStructCase verifies a struct case with a map field
// through the parser path. The map field is pre-wired via registerMapSlotClass
// during collect, so the *hmap is ready for SetMapIndex.
func TestVariantNonStruct_MapStructCase(t *testing.T) {
	src := `{"type":"mapstruct","data":{"counts":{"a":1,"b":2},"label":"hi"}}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, ok := env.Data.(variantMapStructCase)
	if !ok {
		t.Fatalf("Data = %T, want variantMapStructCase", env.Data)
	}
	if m.Label != "hi" {
		t.Errorf("Label = %q, want %q", m.Label, "hi")
	}
	if len(m.Counts) != 2 || m.Counts["a"] != 1 || m.Counts["b"] != 2 {
		t.Errorf("Counts = %+v, want {a:1 b:2}", m.Counts)
	}
}

// TestVariantNonStruct_MapStructCaseVariantFirst triggers the rebind path
// with a struct case containing a map field.
func TestVariantNonStruct_MapStructCaseVariantFirst(t *testing.T) {
	src := `{"data":{"counts":{"a":1,"b":2},"label":"hi"},"type":"mapstruct"}`
	var env variantEnvelopeNonStruct
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, ok := env.Data.(variantMapStructCase)
	if !ok {
		t.Fatalf("Data = %T, want variantMapStructCase", env.Data)
	}
	if len(m.Counts) != 2 || m.Counts["a"] != 1 || m.Counts["b"] != 2 {
		t.Errorf("Counts = %+v, want {a:1 b:2}", m.Counts)
	}
	if m.Label != "hi" {
		t.Errorf("Label = %q, want %q", m.Label, "hi")
	}
}

// TestVariantNonStruct_PureMapCase verifies a pure map[string]int case type
// through the parser path. collect routes the map type through
// registerMapSlotClass, so the case slot's *hmap is pre-wired.
func TestVariantNonStruct_PureMapCase(t *testing.T) {
	src := `{"type":"counts","data":{"a":1,"b":2,"c":3}}`
	var env variantEnvelopePtrMap
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, ok := env.Data.(map[string]int)
	if !ok {
		t.Fatalf("Data = %T, want map[string]int", env.Data)
	}
	if n := len(m); n != 3 {
		t.Fatalf("len = %d, want 3", n)
	}
	if m["a"] != 1 || m["b"] != 2 || m["c"] != 3 {
		t.Errorf("Data = {a:%d b:%d c:%d}, want {a:1 b:2 c:3}", m["a"], m["b"], m["c"])
	}
}

// TestVariantNonStruct_PureMapCaseVariantFirst triggers the rebind path with
// a pure map case type.
func TestVariantNonStruct_PureMapCaseVariantFirst(t *testing.T) {
	src := `{"data":{"a":1,"b":2},"type":"counts"}`
	var env variantEnvelopePtrMap
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, ok := env.Data.(map[string]int)
	if !ok {
		t.Fatalf("Data = %T, want map[string]int", env.Data)
	}
	if len(m) != 2 || m["a"] != 1 || m["b"] != 2 {
		t.Errorf("Data = %+v, want {a:1 b:2}", m)
	}
}

// TestVariantNonStruct_PointerCase verifies a *variantUser pointer case type
// through both the JSON bind path (Unmarshal) and the tape-bind sub-routine
// (UnmarshalValue via roundTrip). The case slot holds the *variantUser;
// walkPointer carves the pointee and writes the pointer into the case slot.
func TestVariantNonStruct_PointerCase(t *testing.T) {
	src := `{"type":"ptruser","data":{"name":"Grace","role":"admin"}}`
	roundTrip[variantEnvelopePtrMap](t, src)
	var env variantEnvelopePtrMap
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(*variantUser)
	if !ok || u == nil || u.Name != "Grace" || u.Role != "admin" {
		t.Fatalf("Data = %+v, want *variantUser{Grace admin}", env.Data)
	}
}

// TestVariantNonStruct_PointerCaseVariantFirst triggers the rebind path with
// a pointer case type (data-before-disc, out-of-order variant dispatch).
func TestVariantNonStruct_PointerCaseVariantFirst(t *testing.T) {
	src := `{"data":{"name":"Heidi","role":"dev"},"type":"ptruser"}`
	roundTrip[variantEnvelopePtrMap](t, src)
	var env variantEnvelopePtrMap
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(*variantUser)
	if !ok || u == nil || u.Name != "Heidi" || u.Role != "dev" {
		t.Fatalf("Data = %+v, want *variantUser{Heidi dev}", env.Data)
	}
}

// --- recursive variant: case type IS the host type (parser path) ---

type recurVariantTreeBind struct {
	Type     string                 `json:"type"`
	Data     any                    `json:"data" vjson:"variant=type"`
	Children []recurVariantTreeBind `json:"children"`
}

type recurVariantLeafBind struct {
	Value int `json:"value"`
}

func init() {
	vbind.DefineVariantCases[recurVariantTreeBind, struct {
		_ recurVariantTreeBind `case:"tree"`
		_ recurVariantLeafBind `case:"leaf"`
	}]()
}

// TestVariantRecursive_Rebind exercises a recursive variant (case type is
// the host type) through the parser path with out-of-order keys, triggering
// the cold-path tape-bind sub-routine at each nesting level.
func TestVariantRecursive_Rebind(t *testing.T) {
	// Out-of-order: "data" before "type" at every level → rebind path.
	src := `{"data":{"data":{"data":{"value":42},"type":"leaf","children":[]},"type":"tree","children":[]},"type":"tree","children":[]}`
	var got recurVariantTreeBind
	if err := Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != "tree" {
		t.Errorf("outer Type = %q, want %q", got.Type, "tree")
	}
	mid, ok := got.Data.(recurVariantTreeBind)
	if !ok {
		t.Fatalf("outer Data = %T, want recurVariantTreeBind", got.Data)
	}
	inner, ok := mid.Data.(recurVariantTreeBind)
	if !ok {
		t.Fatalf("mid Data = %T, want recurVariantTreeBind", mid.Data)
	}
	if inner.Type != "leaf" {
		t.Errorf("inner Type = %q, want %q", inner.Type, "leaf")
	}
	leaf, ok := inner.Data.(recurVariantLeafBind)
	if !ok {
		t.Fatalf("inner Data = %T, want recurVariantLeafBind", inner.Data)
	}
	if leaf.Value != 42 {
		t.Errorf("leaf Value = %d, want 42", leaf.Value)
	}
}

// --- fast path tests (discriminator-first inline dispatch) ---
//
// The fast path triggers when the vdisc field appears before the variant
// field in the JSON object. The C state machine marks disc_seen on the
// poly_stack at the vdisc hit, and the variant field hit dispatches the
// concrete case type inline (no tape, no Go walker rebind). These tests
// exercise fast-path-specific scenarios not covered by the tests above.

// TestVariantFastPath_NullValue verifies a null variant value with
// discriminator-first leaves the eface at nil (nil any), matching the cold
// path's walkVariantField null handling. The fast path handles null before
// case lookup (no case slot is carved).
func TestVariantFastPath_NullValue(t *testing.T) {
	src := `{"type":"user","data":null}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Type != "user" {
		t.Errorf("Type = %q, want %q", env.Type, "user")
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil (null variant)", env.Data)
	}
}

// TestVariantFastPath_NullValueVariantFirst verifies null variant value with
// variant-first (cold path) also leaves the eface at nil.
func TestVariantFastPath_NullValueVariantFirst(t *testing.T) {
	src := `{"data":null,"type":"user"}`
	var env variantEnvelopeSibling
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil (null variant)", env.Data)
	}
}

// variantEnvelopeScalar exercises scalar case types (string, int) through the
// fast path. Scalar cases dispatch inline via BIND_DISPATCH_STRING /
// BIND_WRITE_NUMBER, writing directly to the case slot.
type variantEnvelopeScalar struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[variantEnvelopeScalar, struct {
		_ string `case:"name"`
		_ int    `case:"count"`
	}]()
}

// TestVariantFastPath_ScalarStringCase verifies a string case type through
// the fast path (disc-first). The case slot holds a Go string header;
// eface.data points to the slot (value kind).
func TestVariantFastPath_ScalarStringCase(t *testing.T) {
	src := `{"type":"name","data":"Alice"}`
	var env variantEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	s, ok := env.Data.(string)
	if !ok {
		t.Fatalf("Data = %T, want string", env.Data)
	}
	if s != "Alice" {
		t.Errorf("Data = %q, want %q", s, "Alice")
	}
}

// TestVariantFastPath_ScalarIntCase verifies an int case type through the
// fast path (disc-first).
func TestVariantFastPath_ScalarIntCase(t *testing.T) {
	src := `{"type":"count","data":42}`
	var env variantEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	n, ok := env.Data.(int)
	if !ok {
		t.Fatalf("Data = %T, want int", env.Data)
	}
	if n != 42 {
		t.Errorf("Data = %d, want 42", n)
	}
}

// TestVariantFastPath_ScalarStringCaseVariantFirst verifies a string case
// type through the cold path (variant-first) for symmetry with the fast
// path test.
func TestVariantFastPath_ScalarStringCaseVariantFirst(t *testing.T) {
	src := `{"data":"Bob","type":"name"}`
	var env variantEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	s, ok := env.Data.(string)
	if !ok {
		t.Fatalf("Data = %T, want string", env.Data)
	}
	if s != "Bob" {
		t.Errorf("Data = %q, want %q", s, "Bob")
	}
}

// TestVariantFastPath_RecursiveDiscFirst exercises a recursive variant
// (case type is the host type) with discriminator-first at every level.
// Each level takes the fast path (inline dispatch); the poly_stack
// pushes/pops at each struct close.
func TestVariantFastPath_RecursiveDiscFirst(t *testing.T) {
	// Disc-first at every level → fast path at every level.
	src := `{"type":"tree","data":{"type":"tree","data":{"type":"leaf","data":{"value":99},"children":[]},"children":[]},"children":[]}`
	var got recurVariantTreeBind
	if err := Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != "tree" {
		t.Errorf("outer Type = %q, want %q", got.Type, "tree")
	}
	mid, ok := got.Data.(recurVariantTreeBind)
	if !ok {
		t.Fatalf("outer Data = %T, want recurVariantTreeBind", got.Data)
	}
	inner, ok := mid.Data.(recurVariantTreeBind)
	if !ok {
		t.Fatalf("mid Data = %T, want recurVariantTreeBind", mid.Data)
	}
	if inner.Type != "leaf" {
		t.Errorf("inner Type = %q, want %q", inner.Type, "leaf")
	}
	leaf, ok := inner.Data.(recurVariantLeafBind)
	if !ok {
		t.Fatalf("inner Data = %T, want recurVariantLeafBind", inner.Data)
	}
	if leaf.Value != 99 {
		t.Errorf("leaf Value = %d, want 99", leaf.Value)
	}
}

// TestVariantFastPath_MixedOrdering exercises a mix of disc-first (fast path)
// and variant-first (cold path) within one parse: the outer envelope is
// disc-first, the inner is variant-first. This verifies the two paths
// interleave correctly on the poly_stack.
func TestVariantFastPath_MixedOrdering(t *testing.T) {
	// Outer: disc-first (fast path). Inner: variant-first (cold path).
	src := `{"type":"wrap","data":{"data":{"name":"Ivan","role":"dev"},"type":"user"}}`
	var env variantEnvelopeOuter
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	inner, ok := env.Data.(variantEnvelopeSibling)
	if !ok {
		t.Fatalf("outer Data = %T, want variantEnvelopeSibling", env.Data)
	}
	if inner.Type != "user" {
		t.Errorf("inner Type = %q, want %q", inner.Type, "user")
	}
	u, ok := inner.Data.(variantUser)
	if !ok {
		t.Fatalf("inner Data = %T, want variantUser", inner.Data)
	}
	if u.Name != "Ivan" || u.Role != "dev" {
		t.Errorf("inner Data = %+v, want {Ivan dev}", u)
	}
}

// TestVariantFastPath_RepeatedParse verifies the parser is reusable across
// multiple disc-first parses (fast path state resets cleanly).
func TestVariantFastPath_RepeatedParse(t *testing.T) {
	parser, err := NewParser[variantEnvelopeSibling]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for i := range 10 {
		var env variantEnvelopeSibling
		src := `{"type":"product","data":{"title":"Widget","price":` + itoa(i) + `}}`
		if err := parser.Unmarshal([]byte(src), &env); err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
		pr, ok := env.Data.(variantProduct)
		if !ok {
			t.Fatalf("parse %d: Data = %T, want variantProduct", i, env.Data)
		}
		if pr.Price != i {
			t.Errorf("parse %d: Price = %d, want %d", i, pr.Price, i)
		}
	}
}

// itoa is a minimal int-to-string helper to avoid importing strconv in the
// hot test loop.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// --- iface variant field tests (user-defined interface, not `any`) ---
//
// When the variant field is declared as a user-defined interface (e.g.
// EventData), the build computes the itab for each (case type, interface)
// pair and stores it in caseRType. The runtime writes the itab to word 0
// (iface layout {*itab, data}) instead of *_type (eface layout). The user
// can type-assert and call methods on the result directly.

type variantEventKind interface {
	KindName() string
}

func (variantUser) KindName() string    { return "user" }
func (variantProduct) KindName() string { return "product" }

type variantEnvelopeIface struct {
	Type string           `json:"type"`
	Data variantEventKind `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[variantEnvelopeIface, struct {
		_ variantUser    `case:"user"`
		_ variantProduct `case:"product"`
	}]()
}

// TestVariantIface_DiscriminatorFirst verifies an iface variant field works
// through the fast path (disc-first). The result can be type-asserted to the
// interface and methods can be called.
func TestVariantIface_DiscriminatorFirst(t *testing.T) {
	src := `{"type":"user","data":{"name":"Alice","role":"admin"}}`
	var env variantEnvelopeIface
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data == nil {
		t.Fatal("Data is nil")
	}
	if env.Data.KindName() != "user" {
		t.Errorf("KindName() = %q, want %q", env.Data.KindName(), "user")
	}
	u, ok := env.Data.(variantUser)
	if !ok {
		t.Fatalf("Data = %T, want variantUser", env.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

// TestVariantIface_VariantFirst verifies an iface variant field works
// through the cold path (variant-first, tape + Go walker rebind).
func TestVariantIface_VariantFirst(t *testing.T) {
	src := `{"data":{"title":"Widget","price":99},"type":"product"}`
	var env variantEnvelopeIface
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data == nil {
		t.Fatal("Data is nil")
	}
	if env.Data.KindName() != "product" {
		t.Errorf("KindName() = %q, want %q", env.Data.KindName(), "product")
	}
	pr, ok := env.Data.(variantProduct)
	if !ok {
		t.Fatalf("Data = %T, want variantProduct", env.Data)
	}
	if pr.Title != "Widget" || pr.Price != 99 {
		t.Errorf("Data = %+v, want {Widget 99}", pr)
	}
}

// TestVariantIface_NullValue verifies a null variant value with an iface
// field leaves Data at nil (nil interface).
func TestVariantIface_NullValue(t *testing.T) {
	src := `{"type":"user","data":null}`
	var env variantEnvelopeIface
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
}
