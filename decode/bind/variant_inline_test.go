package bind

import (
	"errors"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Inline-tag variant (§6.6) tests. The variant field carries
// `vjson:"variant=<discName>,inline"`; case type fields unfold into the host
// struct. The C struct-open path intercepts and tapes the whole struct via
// vd_dispatch; Go rebinds once with HostTypeIdx so walkStruct dispatches host
// fields and the post-walk loop writes the eface at the variant field's offset.

// --- case types ---

type inlUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type inlProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

type inlAdmin struct {
	Level int `json:"level"`
}

// --- hosts ---

// inlHost: inline variant only. Data is virtual (,inline); case fields
// (name/role, title/price, level) unfold into the host JSON object.
type inlHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
		_ inlAdmin   `case:"admin"`
	}]()
}

// inlSiblingHost: inline variant + sibling variant on the same host
// (prohibited before Phase 1; now allowed). Both variant fields share one
// disc ("type") and one case table (one DefineVariantCases call covers both).
type inlSiblingHost struct {
	Type    string `json:"type"`
	Data    any    `json:",embed" vjson:"variant=type"`
	Payload any    `json:"payload" vjson:"variant=type"` // sibling variant, same disc
}

func init() {
	vbind.DefineVariantCases[inlSiblingHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
}

// inlDualDiscHost: internal + sibling variant with INDEPENDENT discriminators
// on the same host. inlSiblingHost above shares one disc between the two axes;
// this host uses two separate disc fields (Type for the inline axis,
// Source for the sibling axis). The sibling axis (Source/Meta) is host-level
// and unrelated to the inline case axis (Type/Data): Meta is a real JSON
// member whose case is selected by Source, while Data's case fields unfold
// into the host selected by Type. The two variant fields still share one
// case table (one DefineVariantCases call covers both; the variantRegistry
// maps host→descriptor one-to-one), so the case TYPE SET is the same for
// both axes; the two discs independently select which case from that shared
// set applies to their respective variant field.
type inlDualDiscHost struct {
	Type   string `json:"type"`                        // inline disc
	Source string `json:"source"`                      // sibling disc
	Meta   any    `json:"meta" vjson:"variant=source"` // sibling variant (own disc)
	Data   any    `json:",embed" vjson:"variant=type"` // inline variant (own disc)
}

func init() {
	vbind.DefineVariantCases[inlDualDiscHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
}

// inlDualDiscDistinctHost: internal + sibling variant with INDEPENDENT
// discriminators AND independent case sets. inlDualDiscHost above shares one
// case set between the two axes (one DefineVariantCases call covers both, via
// the "" fallback slot); this host registers a SEPARATE case set per disc
// via DefineVariantCasesAt, so the inline axis (Type → Data) and the sibling
// axis (Source → Meta) select from DIFFERENT target-type sets.
//
//   - inline axis (disc "type"): {user → inlUser, product → inlProduct}
//   - sibling  axis (disc "source"): {github → inlGithubSource, gitlab → inlGitlabSource}
//
// This is the form that "Source/Meta is host-level and unrelated to the
// inline case" expresses in full: not just an independent disc field, but
// an independent case-type universe.
type inlGithubSource struct {
	Repo string `json:"repo"`
}

type inlGitlabSource struct {
	Project int `json:"project"`
}

type inlDualDiscDistinctHost struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Meta   any    `json:"meta" vjson:"variant=source"` // sibling, own case set
	Data   any    `json:",embed" vjson:"variant=type"` // inline, own case set
}

func init() {
	vbind.DefineVariantCasesAt[inlDualDiscDistinctHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]("Data")
	vbind.DefineVariantCasesAt[inlDualDiscDistinctHost, struct {
		_ inlGithubSource `case:"github"`
		_ inlGitlabSource `case:"gitlab"`
	}]("Meta")
}

// inlKindofHost: inline variant + kindof on the same host.
type inlKindofHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
	Meta any    `json:"meta" vjson:"kindof"`
}

func init() {
	vbind.DefineVariantCases[inlKindofHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
	vbind.DefineKindofCases[inlKindofHost, struct {
		bool   bool
		object inlUser
	}]()
}

// --- basic tests: disc first / middle / last ---

func TestInlineVariant_DiscFirst(t *testing.T) {
	src := `{"type":"user","name":"Alice","role":"admin"}`
	var h inlHost
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
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestInlineVariant_DiscMiddle(t *testing.T) {
	src := `{"name":"Alice","type":"user","role":"admin"}`
	var h inlHost
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
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestInlineVariant_DiscLast(t *testing.T) {
	src := `{"name":"Alice","role":"admin","type":"user"}`
	var h inlHost
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
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestInlineVariant_ProductCase(t *testing.T) {
	src := `{"type":"product","title":"Widget","price":99}`
	var h inlHost
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
}

// --- error cases: missing disc / unknown disc ---

// TestInlineVariant_AbsentDiscSelectsNothing: no discriminator key in the input
// selects no case, and that is not an error. An embedded variant is a layout for
// the selected case's fields, not a requirement that a case be selected, so an
// absent discriminator leaves Data nil exactly as a sibling variant leaves its
// field nil when its payload key is absent. Host fields still bind.
//
// The keys here are the CASE's fields, not the host's, so they are leftover once
// no case is selected. inlHost declares no sink, so they are dropped.
func TestInlineVariant_AbsentDiscSelectsNothing(t *testing.T) {
	src := `{"name":"Alice","role":"admin"}`
	var h inlHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data != nil {
		t.Errorf("Data = %v, want nil: no discriminator names no case", h.Data)
	}
	if h.Type != "" {
		t.Errorf("Type = %q, want empty", h.Type)
	}
}

// TestInlineVariant_EmptyDiscErrors pins the other half of the split. A
// discriminator key that IS present must name a case: "" is a caller naming
// something unresolvable, not a caller declining to choose, so it reports where
// an absent key stays quiet. This boundary is what keeps
// TestInlineVariant_AbsentDiscSelectsNothing from being a blanket "missing disc
// is fine".
func TestInlineVariant_EmptyDiscErrors(t *testing.T) {
	src := `{"type":"","name":"Alice"}`
	var h inlHost
	err := Unmarshal([]byte(src), &h)
	if err == nil {
		t.Fatalf("Unmarshal: want error for empty discriminator, got nil (Data=%v)", h.Data)
	}
	if !strings.Contains(err.Error(), "missing discriminator") {
		t.Errorf("err = %q, want it to name the missing discriminator", err)
	}
}

func TestInlineVariant_UnknownDisc(t *testing.T) {
	src := `{"type":"unknown","name":"Alice"}`
	var h inlHost
	if err := Unmarshal([]byte(src), &h); err == nil {
		t.Fatalf("Unmarshal: want error for unknown discriminator, got nil (Data=%v)", h.Data)
	}
}

func TestInlineVariant_NullDisc(t *testing.T) {
	// JSON null for the disc field: the string field stays "" (encoding/json
	// behavior for null into a non-pointer scalar), which the post-walk loop
	// treats as missing discriminator.
	src := `{"type":null,"name":"Alice"}`
	var h inlHost
	if err := Unmarshal([]byte(src), &h); err == nil {
		t.Fatalf("Unmarshal: want error for null discriminator, got nil (Data=%v)", h.Data)
	}
}

// --- nested: inline variant host as a struct field (BIND_DESCEND_STRUCT) ---

type inlNestedStructHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type inlNestedStructOuter struct {
	Label string              `json:"label"`
	Inner inlNestedStructHost `json:"inner"`
}

func init() {
	vbind.DefineVariantCases[inlNestedStructHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
}

func TestInlineVariant_NestedStructField(t *testing.T) {
	// Inline variant host as a nested struct field. This exercises the
	// BIND_DESCEND_STRUCT intercept (not the root entry). The C state machine
	// intercepts at the "inner" field's struct open, tapes the whole inner
	// object, and the C-side tape-bind sub-routine rebinds it.
	src := `{"label":"outer","inner":{"type":"user","name":"Alice","role":"admin"}}`
	var o inlNestedStructOuter
	if err := Unmarshal([]byte(src), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if o.Label != "outer" {
		t.Errorf("Label = %q, want %q", o.Label, "outer")
	}
	if o.Inner.Type != "user" {
		t.Errorf("Inner.Type = %q, want %q", o.Inner.Type, "user")
	}
	u, ok := o.Inner.Data.(inlUser)
	if !ok {
		t.Fatalf("Inner.Data = %T, want inlUser", o.Inner.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Inner.Data = %+v, want {Alice admin}", u)
	}
}

// --- nested: inline variant host in a slice ---

func TestInlineVariant_InSlice(t *testing.T) {
	src := `[{"type":"user","name":"Alice","role":"admin"},{"type":"product","title":"Widget","price":99}]`
	var hs []inlNestedStructHost
	if err := Unmarshal([]byte(src), &hs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("len = %d, want 2", len(hs))
	}
	u, ok := hs[0].Data.(inlUser)
	if !ok {
		t.Fatalf("hs[0].Data = %T, want inlUser", hs[0].Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("hs[0].Data = %+v, want {Alice admin}", u)
	}
	p, ok := hs[1].Data.(inlProduct)
	if !ok {
		t.Fatalf("hs[1].Data = %T, want inlProduct", hs[1].Data)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("hs[1].Data = %+v, want {Widget 99}", p)
	}
}

// --- empty object / only host fields ---

func TestInlineVariant_EmptyObject(t *testing.T) {
	// `{}` names no case, which selects nothing rather than failing: an empty
	// object is a struct with zero fields, and the close owes what any other
	// close of this type owes. With no discriminator that is nothing.
	src := `{}`
	var h inlHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data != nil {
		t.Errorf("Data = %v, want nil", h.Data)
	}
}

func TestInlineVariant_OnlyDisc(t *testing.T) {
	// Only the discriminator, no case fields. The case is selected, case
	// struct is zero-valued, eface is written.
	src := `{"type":"user"}`
	var h inlHost
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
	if u.Name != "" || u.Role != "" {
		t.Errorf("Data = %+v, want zero inlUser", u)
	}
}

// --- internal + sibling on same host (解禁) ---

func TestInlineVariant_WithSibling(t *testing.T) {
	// inline variant (case fields unfold into host) + sibling variant
	// (Payload is a real JSON member). Both share the same disc "type".
	src := `{"type":"user","name":"Alice","role":"admin","payload":{"name":"Bob","role":"editor"}}`
	var h inlSiblingHost
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
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	p, ok := h.Payload.(inlUser)
	if !ok {
		t.Fatalf("Payload = %T, want inlUser", h.Payload)
	}
	if p.Name != "Bob" || p.Role != "editor" {
		t.Errorf("Payload = %+v, want {Bob editor}", p)
	}
}

// --- internal + sibling on same host, INDEPENDENT discs ---

func TestInlineVariant_WithSibling_IndependentDiscs(t *testing.T) {
	// Two independent polymorphic axes on the same host:
	//   - inline axis: Type → Data (case fields unfold into host)
	//   - sibling axis : Source → Meta (Meta is a real JSON member)
	// Each disc picks its case independently from the shared case set.
	// Here Type="user" unfolds {name,role} from inlUser, while Source="product"
	// selects inlProduct for Meta: the two axes pick DIFFERENT cases,
	// proving they are not coupled.
	src := `{"type":"user","source":"product","meta":{"title":"Widget","price":99},"name":"Alice","role":"admin"}`
	var h inlDualDiscHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" {
		t.Errorf("Type = %q, want %q", h.Type, "user")
	}
	if h.Source != "product" {
		t.Errorf("Source = %q, want %q", h.Source, "product")
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	p, ok := h.Meta.(inlProduct)
	if !ok {
		t.Fatalf("Meta = %T, want inlProduct", h.Meta)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("Meta = %+v, want {Widget 99}", p)
	}
}

func TestInlineVariant_WithSibling_IndependentDiscs_SameCase(t *testing.T) {
	// Both discs pick the SAME case ("user"). Data unfolds {name,role} from
	// inlUser; Meta is a sibling inlUser payload. Confirms the two axes don't
	// interfere when they happen to select the same case type.
	src := `{"type":"user","source":"user","meta":{"name":"Bob","role":"editor"},"name":"Alice","role":"admin"}`
	var h inlDualDiscHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" || h.Source != "user" {
		t.Errorf("Type=%q Source=%q, want both %q", h.Type, h.Source, "user")
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	m, ok := h.Meta.(inlUser)
	if !ok {
		t.Fatalf("Meta = %T, want inlUser", h.Meta)
	}
	if m.Name != "Bob" || m.Role != "editor" {
		t.Errorf("Meta = %+v, want {Bob editor}", m)
	}
}

func TestInlineVariant_WithSibling_IndependentDiscs_OutOfOrder(t *testing.T) {
	// Fully out-of-order: sibling payload, then sibling disc, then internal
	// case fields, then inline disc. Exercises the poly_stack entry
	// matching (depth, poly_idx, kind) across two independent axes at the
	// same depth, where the internal entry (kind=2) and sibling entry (kind=0)
	// must not be confused for each other.
	src := `{"meta":{"title":"Widget","price":99},"source":"product","name":"Alice","role":"admin","type":"user"}`
	var h inlDualDiscHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" || h.Source != "product" {
		t.Errorf("Type=%q Source=%q", h.Type, h.Source)
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	p, ok := h.Meta.(inlProduct)
	if !ok {
		t.Fatalf("Meta = %T, want inlProduct", h.Meta)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("Meta = %+v, want {Widget 99}", p)
	}
}

func TestInlineVariant_WithSibling_IndependentDiscs_UnknownSiblingDisc(t *testing.T) {
	// Unknown value on the sibling disc; the inline axis should still bind.
	// The sibling variant cold path reports the unknown-disc error at rebind.
	src := `{"type":"user","source":"unknown","meta":{"title":"Widget","price":99},"name":"Alice","role":"admin"}`
	var h inlDualDiscHost
	if err := Unmarshal([]byte(src), &h); err == nil {
		t.Fatalf("Unmarshal: want error for unknown sibling disc, got nil (Data=%v Meta=%v)", h.Data, h.Meta)
	}
}

// --- internal + sibling with INDEPENDENT case sets (DefineVariantCasesAt) ---

func TestInlineVariant_WithSibling_IndependentCaseSets(t *testing.T) {
	// The two axes have DIFFERENT case-type universes:
	//   type="user"    → Data is inlUser    (internal, unfolds name/role)
	//   source="github" → Meta is inlGithubSource (sibling, real JSON member)
	// Proving the case sets are independent: inlGithubSource could never be a
	// valid Data case, and inlUser could never be a valid Meta case.
	src := `{"type":"user","source":"github","meta":{"repo":"velox-io/json"},"name":"Alice","role":"admin"}`
	var h inlDualDiscDistinctHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" {
		t.Errorf("Type = %q, want %q", h.Type, "user")
	}
	if h.Source != "github" {
		t.Errorf("Source = %q, want %q", h.Source, "github")
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	g, ok := h.Meta.(inlGithubSource)
	if !ok {
		t.Fatalf("Meta = %T, want inlGithubSource", h.Meta)
	}
	if g.Repo != "velox-io/json" {
		t.Errorf("Meta = %+v, want {velox-io/json}", g)
	}
}

func TestInlineVariant_WithSibling_IndependentCaseSets_OtherCombo(t *testing.T) {
	// The complementary combo: type="product" → inlProduct, source="gitlab"
	// → inlGitlabSource. Confirms both axes' case sets are independently
	// addressed.
	src := `{"type":"product","source":"gitlab","meta":{"project":42},"title":"Widget","price":99}`
	var h inlDualDiscDistinctHost
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
	l, ok := h.Meta.(inlGitlabSource)
	if !ok {
		t.Fatalf("Meta = %T, want inlGitlabSource", h.Meta)
	}
	if l.Project != 42 {
		t.Errorf("Meta = %+v, want {42}", l)
	}
}

func TestInlineVariant_WithSibling_IndependentCaseSets_OutOfOrder(t *testing.T) {
	// Out-of-order: sibling payload, inline case fields, sibling disc,
	// inline disc. Confirms poly_stack entry matching across two axes
	// with disjoint case sets.
	src := `{"meta":{"repo":"velox-io/json"},"name":"Alice","role":"admin","source":"github","type":"user"}`
	var h inlDualDiscDistinctHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "user" || h.Source != "github" {
		t.Errorf("Type=%q Source=%q", h.Type, h.Source)
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	g, ok := h.Meta.(inlGithubSource)
	if !ok {
		t.Fatalf("Meta = %T, want inlGithubSource", h.Meta)
	}
	if g.Repo != "velox-io/json" {
		t.Errorf("Meta = %+v, want {velox-io/json}", g)
	}
}

func TestInlineVariant_WithSibling_IndependentCaseSets_CrossDiscUnknown(t *testing.T) {
	// A case value valid on ONE axis but not the other must be rejected on
	// the wrong axis. "user" is a valid type-disc value but not a source-disc
	// value (the source case set is {github, gitlab}); the sibling axis must
	// reject it without affecting the inline axis.
	src := `{"type":"user","source":"user","meta":{"name":"Alice"},"name":"Alice","role":"admin"}`
	var h inlDualDiscDistinctHost
	if err := Unmarshal([]byte(src), &h); err == nil {
		t.Fatalf("Unmarshal: want error for source=user (valid on type axis, invalid on source axis), got nil (Data=%v Meta=%v)", h.Data, h.Meta)
	}
}

// --- internal + kindof on same host ---

func TestInlineVariant_WithKindof(t *testing.T) {
	src := `{"type":"user","name":"Alice","role":"admin","meta":{"name":"Bob","role":"editor"}}`
	var h inlKindofHost
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
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
	m, ok := h.Meta.(inlUser)
	if !ok {
		t.Fatalf("Meta = %T, want inlUser", h.Meta)
	}
	if m.Name != "Bob" || m.Role != "editor" {
		t.Errorf("Meta = %+v, want {Bob editor}", m)
	}
}

func TestInlineVariant_WithKindof_ScalarMeta(t *testing.T) {
	src := `{"type":"product","title":"Widget","price":99,"meta":true}`
	var h inlKindofHost
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
	b, ok := h.Meta.(bool)
	if !ok {
		t.Fatalf("Meta = %T, want bool", h.Meta)
	}
	if !b {
		t.Errorf("Meta = %v, want true", b)
	}
}

// --- nested: inline case containing a sibling variant ---

type inlNestedHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

// inlCaseWithSibling is a case type that itself contains a sibling variant.
// The inline variant's case can have its own (sibling) variant; only
// nested *internal* variants are rejected (diamond).
type inlCaseWithSibling struct {
	SubType string `json:"subType"`
	SubData any    `json:"subData" vjson:"variant=subType"`
}

func init() {
	vbind.DefineVariantCases[inlNestedHost, struct {
		_ inlCaseWithSibling `case:"nested"`
	}]()
	// Register the sibling variant descriptor for inlCaseWithSibling.
	vbind.DefineVariantCases[inlCaseWithSibling, struct {
		_ inlUser `case:"user"`
	}]()
}

func TestInlineVariant_CaseWithSiblingVariant(t *testing.T) {
	src := `{"type":"nested","subType":"user","subData":{"name":"Alice","role":"admin"}}`
	var h inlNestedHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "nested" {
		t.Errorf("Type = %q, want %q", h.Type, "nested")
	}
	c, ok := h.Data.(inlCaseWithSibling)
	if !ok {
		t.Fatalf("Data = %T, want inlCaseWithSibling", h.Data)
	}
	if c.SubType != "user" {
		t.Errorf("SubType = %q, want %q", c.SubType, "user")
	}
	u, ok := c.SubData.(inlUser)
	if !ok {
		t.Fatalf("SubData = %T, want inlUser", c.SubData)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("SubData = %+v, want {Alice admin}", u)
	}
}

func TestInlineVariant_CaseWithSiblingVariant_OutOfOrder(t *testing.T) {
	// Out-of-order: sibling payload, sibling disc, inline disc. The inline
	// axis tapes the whole host and rebinds via post-walk, so host field
	// order is irrelevant to it. The coverage is the sibling axis inside
	// the case: subData arrives before subType, forcing the sibling cold
	// path (tape+walker). poly_stack must match the outer inline entry
	// (host depth, kind=2) vs the inner sibling entry (case depth, kind=0).
	src := `{"subData":{"name":"Alice","role":"admin"},"subType":"user","type":"nested"}`
	var h inlNestedHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "nested" {
		t.Errorf("Type = %q, want %q", h.Type, "nested")
	}
	c, ok := h.Data.(inlCaseWithSibling)
	if !ok {
		t.Fatalf("Data = %T, want inlCaseWithSibling", h.Data)
	}
	if c.SubType != "user" {
		t.Errorf("SubType = %q, want %q", c.SubType, "user")
	}
	u, ok := c.SubData.(inlUser)
	if !ok {
		t.Fatalf("SubData = %T, want inlUser", c.SubData)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("SubData = %+v, want {Alice admin}", u)
	}
}

// --- nested: inline case containing a kindof ---

type inlCaseWithKindof struct {
	Meta any `json:"meta" vjson:"kindof"`
}

type inlNestedKindofHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlNestedKindofHost, struct {
		_ inlCaseWithKindof `case:"withkindof"`
	}]()
	vbind.DefineKindofCases[inlCaseWithKindof, struct {
		bool   bool
		object inlUser
	}]()
}

func TestInlineVariant_CaseWithKindof(t *testing.T) {
	src := `{"type":"withkindof","meta":{"name":"Bob","role":"editor"}}`
	var h inlNestedKindofHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Data.(inlCaseWithKindof)
	if !ok {
		t.Fatalf("Data = %T, want inlCaseWithKindof", h.Data)
	}
	m, ok := c.Meta.(inlUser)
	if !ok {
		t.Fatalf("Meta = %T, want inlUser", c.Meta)
	}
	if m.Name != "Bob" || m.Role != "editor" {
		t.Errorf("Meta = %+v, want {Bob editor}", m)
	}
}

// --- inline case with pointer-to-struct containing a Value field ---

// inlValueHolder is the innermost struct: its Raw field captures arbitrary
// JSON as a tape-backed value.Value.
type inlValueHolder struct {
	Raw value.Value `json:"raw"`
}

// inlCasePtrToValue is an inline-variant case whose Detail is a pointer to
// inlValueHolder. During the C-side tape-bind walk this forces a pointer
// slot allocation for Detail, then a VALUE_ALLOC yield to carve the tape
// sub-slice backing the pointee's Raw Value field.
type inlCasePtrToValue struct {
	Label  string          `json:"label"`
	Detail *inlValueHolder `json:"detail"`
}

type inlHostPtrValueCase struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlHostPtrValueCase, struct {
		_ inlCasePtrToValue `case:"ptrval"`
	}]()
}

func TestInlineVariant_CaseWithPointerToValue_OutOfOrder(t *testing.T) {
	// Out-of-order: case fields (label, detail) arrive before the disc (type),
	// forcing the inline cold path. The tape-bind sub-routine walks the taped
	// host struct as inlCasePtrToValue: it allocates a pointer slot for Detail,
	// descends into the pointee, and yields VALUE_ALLOC to carve the tape
	// backing for the Raw Value field. The Value captures the nested object.
	src := `{"label":"entry","detail":{"raw":{"nested":{"deep":true}}},"type":"ptrval"}`
	var h inlHostPtrValueCase
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "ptrval" {
		t.Errorf("Type = %q, want %q", h.Type, "ptrval")
	}
	c, ok := h.Data.(inlCasePtrToValue)
	if !ok {
		t.Fatalf("Data = %T, want inlCasePtrToValue", h.Data)
	}
	if c.Label != "entry" {
		t.Errorf("Label = %q, want %q", c.Label, "entry")
	}
	if c.Detail == nil {
		t.Fatal("Detail is nil, want *inlValueHolder")
	}
	nested := c.Detail.Raw.Get("nested")
	if !nested.Exists() {
		t.Fatalf("Raw.Get(\"nested\") missing; Raw = %s", c.Detail.Raw.String())
	}
	deep, ok := nested.GetBool("deep")
	if !ok || !deep {
		t.Errorf("nested.deep = %v, %v; want true", deep, ok)
	}
}

func TestInlineVariant_CaseWithPointerToValue_DiscFirst(t *testing.T) {
	// Disc first: fast path (no tape, inline dispatch). Same case type, same
	// pointer + Value structure, but the disc is seen before the case fields so
	// the inline fast path binds directly. Confirms both paths produce the same
	// result for pointer-to-struct-with-Value case types.
	src := `{"type":"ptrval","label":"entry","detail":{"raw":{"deep":false,"n":7}}}`
	var h inlHostPtrValueCase
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Type != "ptrval" {
		t.Errorf("Type = %q, want %q", h.Type, "ptrval")
	}
	c, ok := h.Data.(inlCasePtrToValue)
	if !ok {
		t.Fatalf("Data = %T, want inlCasePtrToValue", h.Data)
	}
	if c.Label != "entry" {
		t.Errorf("Label = %q, want %q", c.Label, "entry")
	}
	if c.Detail == nil {
		t.Fatal("Detail is nil, want *inlValueHolder")
	}
	deep, ok := c.Detail.Raw.GetBool("deep")
	if !ok || deep {
		t.Errorf("raw.deep = %v, %v; want false", deep, ok)
	}
	n, ok := c.Detail.Raw.GetInt("n")
	if !ok || n != 7 {
		t.Errorf("raw.n = %d, %v; want 7", n, ok)
	}
}

// --- diamond rejection: case type that itself hosts an inline variant ---

type inlDiamondCase struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"` // case hosts its own inline variant
}

type inlDiamondHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	// Register inlDiamondCase first so its inline variant is built. When
	// inlDiamondHost tries to use inlDiamondCase as a case, the build should
	// reject it (diamond).
	vbind.DefineVariantCases[inlDiamondCase, struct {
		_ inlUser `case:"user"`
	}]()
	vbind.DefineVariantCases[inlDiamondHost, struct {
		_ inlDiamondCase `case:"diamond"`
	}]()
}

func TestInlineVariant_DiamondRejection(t *testing.T) {
	var h inlDiamondHost
	err := Unmarshal([]byte(`{"type":"diamond","type":"user","name":"A"}`), &h)
	if err == nil {
		t.Fatalf("Unmarshal: want error for diamond (case hosts inline variant), got nil")
	}
	// The error should mention diamond or inline variant.
	msg := err.Error()
	if !strings.Contains(msg, "diamond") && !strings.Contains(msg, "inline") {
		t.Errorf("error %q does not mention diamond/internal", msg)
	}
}

// --- pointer case rejection ---

type inlPointerCaseHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlPointerCaseHost, struct {
		_ *inlUser `case:"ptr"` // pointer case: must be rejected
	}]()
}

func TestInlineVariant_PointerCaseRejection(t *testing.T) {
	var h inlPointerCaseHost
	err := Unmarshal([]byte(`{"type":"ptr","name":"A"}`), &h)
	if err == nil {
		t.Fatalf("Unmarshal: want error for pointer case type, got nil")
	}
	if !strings.Contains(err.Error(), "struct") {
		t.Errorf("error %q does not mention struct requirement", err.Error())
	}
}

// --- iface field type (user-defined interface, not `any`/eface) ---
//
// When the inline variant field is declared as a user-defined interface,
// the build computes the itab for each (case type, interface) pair and stores
// it in caseRType. walkVariantField writes the itab to word 0 (iface layout
// {*itab, data}) instead of *_type (eface layout). The user can type-assert
// and call methods on the result directly.

type inlEventKind interface {
	KindName() string
}

func (inlUser) KindName() string    { return "user" }
func (inlProduct) KindName() string { return "product" }

type inlIfaceHost struct {
	Type string       `json:"type"`
	Data inlEventKind `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlIfaceHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
	}]()
}

func TestInlineVariant_IfaceField(t *testing.T) {
	src := `{"type":"user","name":"Alice","role":"admin"}`
	var h inlIfaceHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data == nil {
		t.Fatal("Data is nil")
	}
	if h.Data.KindName() != "user" {
		t.Errorf("KindName() = %q, want %q", h.Data.KindName(), "user")
	}
	u, ok := h.Data.(inlUser)
	if !ok {
		t.Fatalf("Data = %T, want inlUser", h.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestInlineVariant_IfaceField_ProductCase(t *testing.T) {
	src := `{"title":"Widget","price":99,"type":"product"}`
	var h inlIfaceHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Data == nil {
		t.Fatal("Data is nil")
	}
	if h.Data.KindName() != "product" {
		t.Errorf("KindName() = %q, want %q", h.Data.KindName(), "product")
	}
	p, ok := h.Data.(inlProduct)
	if !ok {
		t.Fatalf("Data = %T, want inlProduct", h.Data)
	}
	if p.Title != "Widget" || p.Price != 99 {
		t.Errorf("Data = %+v, want {Widget 99}", p)
	}
}

// --- strict mode (WithDisallowUnknownFields) + inline variant ---
//
// Ported from examples/unmarshal/poly/main.go's Permission example. The host
// mirrors Permission: a discriminator (Type) plus a virtual inline variant
// field (User) whose case fields unfold into the host JSON object.
//
// Strict mode reaches the unfolded case fields: the merged-tape pass classifies
// every member against the host's field table and the selected case's, so a key
// that neither declares is a genuine unknown field and is rejected here rather
// than silently dropped.

type inlPermission struct {
	Type string `json:"type"`
	User any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlPermission, struct {
		user inlUser
	}]()
}

func TestInlineVariant_StrictMode_PermissionExample(t *testing.T) {
	// "is_admin" is declared by neither inlPermission nor inlUser.
	src := `{"type":"user", "name": "bob", "is_admin": true}`
	var perm inlPermission
	err := Unmarshal([]byte(src), &perm, WithDisallowUnknownFields())
	var typeErr *UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Unmarshal error = %v, want *UnmarshalTypeError", err)
	}
	if typeErr.Value != "unknown_field" {
		t.Errorf("Value = %q, want %q", typeErr.Value, "unknown_field")
	}

	// Without strict mode the same input binds and the unknown member is dropped.
	perm = inlPermission{}
	if err := Unmarshal([]byte(src), &perm); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if perm.Type != "user" {
		t.Errorf("Type = %q, want %q", perm.Type, "user")
	}
	u, ok := perm.User.(inlUser)
	if !ok {
		t.Fatalf("User = %T, want inlUser", perm.User)
	}
	if u.Name != "bob" {
		t.Errorf("User.Name = %q, want %q", u.Name, "bob")
	}
	if u.Role != "" {
		t.Errorf("User.Role = %q, want %q (JSON supplied no role)", u.Role, "")
	}
}

// TestInlineVariant_UnmarshalValue verifies the tape-bind cold-start inline
// intercept (t_document_start) dispatches a struct case via UnmarshalValue,
// not just value.Value cases. Pins that the intercept is general: it runs
// pass-1 (walk as host) → pass-2 (walk as case type) → eface write for any
// inline-variant host, covering both disc-first and out-of-order field order.
func TestInlineVariant_UnmarshalValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"disc-first", `{"type":"user","name":"Alice","role":"admin"}`},
		{"out-of-order", `{"name":"Alice","role":"admin","type":"user"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var h inlHost
			if err := UnmarshalValue(val, &h); err != nil {
				t.Fatalf("UnmarshalValue: %v", err)
			}
			if h.Type != "user" {
				t.Errorf("Type = %q, want %q", h.Type, "user")
			}
			u, ok := h.Data.(inlUser)
			if !ok {
				t.Fatalf("Data = %T, want inlUser", h.Data)
			}
			if u.Name != "Alice" || u.Role != "admin" {
				t.Errorf("Data = %+v, want {Alice admin}", u)
			}
		})
	}
}

// TestInlineVariant_NestedViaUnmarshalValue verifies a NESTED inline-variant
// host (a field whose type is itself an inline-variant host) dispatches its
// variant via UnmarshalValue. The tape-bind struct-field intercept
// (t_object_field_value BIND_KIND_STRUCT case) detects the inline-variant host
// child type, runs pass-1/pass-2 on the field's tape sub-span (saving the outer
// struct walk's hot locals to nested_walk_save), and restores at completion to
// continue the wrapper's field walk. Both Unmarshal (JSON path, intercept in
// BIND_DESCEND_STRUCT) and UnmarshalValue (tape-bind, intercept in t_field_value
// struct case) must produce the same result.
func TestInlineVariant_NestedViaUnmarshalValue(t *testing.T) {
	type wrapper struct {
		Label string  `json:"label"`
		Inner inlHost `json:"inner"`
	}
	src := `{"label":"wrap","inner":{"type":"user","name":"Alice","role":"admin"}}`

	// JSON path control.
	var w wrapper
	if err := Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if u, ok := w.Inner.Data.(inlUser); !ok || u.Name != "Alice" || u.Role != "admin" {
		t.Fatalf("Unmarshal nested inline: Inner.Data = %T %+v, want inlUser{Alice admin}", w.Inner.Data, w.Inner.Data)
	}

	// tape-bind path: nested inline now dispatched (struct-field intercept).
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var wv wrapper
	if err := UnmarshalValue(val, &wv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if wv.Label != "wrap" {
		t.Errorf("Label = %q, want %q", wv.Label, "wrap")
	}
	if wv.Inner.Type != "user" {
		t.Errorf("Inner.Type = %q, want %q", wv.Inner.Type, "user")
	}
	u, ok := wv.Inner.Data.(inlUser)
	if !ok {
		t.Fatalf("UnmarshalValue nested inline: Inner.Data = %T, want inlUser", wv.Inner.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Inner.Data = %+v, want {Alice admin}", u)
	}
}
