package vbind

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/vlib"
	"github.com/velox-io/json/typ"
)

func TestParseVariantDescriptor_NamedFields(t *testing.T) {
	desc := reflect.TypeFor[struct {
		user    testVariantUser
		product testVariantProduct
	}]()
	pd, err := parseVariantDescriptor(desc)
	if err != nil {
		t.Fatalf("parseVariantDescriptor: %v", err)
	}
	cases := pd.Cases
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	want := map[string]string{"user": "testVariantUser", "product": "testVariantProduct"}
	got := map[string]string{}
	for _, c := range cases {
		got[c.Value] = c.Target.Name()
	}
	for v, w := range want {
		if g, ok := got[v]; !ok || g != w {
			t.Errorf("case %q: got %q, want %q", v, g, w)
		}
	}
}

func TestParseVariantDescriptor_BlankFields(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	pd, err := parseVariantDescriptor(desc)
	if err != nil {
		t.Fatalf("parseVariantDescriptor: %v", err)
	}
	cases := pd.Cases
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	want := map[string]bool{"user": true, "product": true}
	for _, c := range cases {
		if !want[c.Value] {
			t.Errorf("unexpected case %q", c.Value)
		}
	}
}

func TestParseVariantDescriptor_EmptyCaseRejected(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:""`
	}]()
	if _, err := parseVariantDescriptor(desc); err == nil {
		t.Fatal("expected error for empty case value, got nil")
	}
}

func TestParseVariantDescriptor_Duplicate(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"dup"`
		_ testVariantUser `case:"dup"`
	}]()
	if _, err := parseVariantDescriptor(desc); err == nil {
		t.Fatal("expected error for duplicate case value, got nil")
	}
}

func TestParseVariantDescriptor_NamedFieldWithCaseTag(t *testing.T) {
	desc := reflect.TypeFor[struct {
		user testVariantUser `case:"foo"`
	}]()
	if _, err := parseVariantDescriptor(desc); err == nil {
		t.Fatal("expected error for named field with case tag, got nil")
	}
}

// TestParseVariantDescriptor_BlankWithoutCaseIsDefault: a blank field with no
// `case:` tag is the default case. It carries no discriminator value, so it stays
// out of Cases (and out of the lookup blob built from them) and is reported
// through Default instead.
func TestParseVariantDescriptor_BlankWithoutCaseIsDefault(t *testing.T) {
	desc := reflect.TypeFor[struct {
		user testVariantUser
		_    testVariantProduct
	}]()
	pd, err := parseVariantDescriptor(desc)
	if err != nil {
		t.Fatalf("parseVariantDescriptor: %v", err)
	}
	if len(pd.Cases) != 1 {
		t.Fatalf("got %d cases, want 1 (the default is not a case)", len(pd.Cases))
	}
	if pd.Cases[0].Value != "user" {
		t.Errorf("Cases[0].Value = %q, want %q", pd.Cases[0].Value, "user")
	}
	if pd.Default != reflect.TypeFor[testVariantProduct]() {
		t.Errorf("Default = %v, want testVariantProduct", pd.Default)
	}
}

// A default alone is a complete descriptor: every value resolves to it, so
// nothing is missing.
func TestParseVariantDescriptor_DefaultOnly(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ testVariantUser
	}]()
	pd, err := parseVariantDescriptor(desc)
	if err != nil {
		t.Fatalf("parseVariantDescriptor: %v", err)
	}
	if len(pd.Cases) != 0 {
		t.Errorf("got %d cases, want 0", len(pd.Cases))
	}
	if pd.Default != reflect.TypeFor[testVariantUser]() {
		t.Errorf("Default = %v, want testVariantUser", pd.Default)
	}
}

// Two defaults have no tie-breaker, so the descriptor is rejected rather than
// letting one of them silently win.
func TestParseVariantDescriptor_TwoDefaultsRejected(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ testVariantUser
		_ testVariantProduct
	}]()
	_, err := parseVariantDescriptor(desc)
	if err == nil {
		t.Fatal("expected error for two default cases, got nil")
	}
	if !strings.Contains(err.Error(), "two default cases") {
		t.Errorf("err = %q, want it to name the duplicate default", err)
	}
}

// An empty descriptor stays rejected: allowing an untagged blank must not make
// "no entries at all" look complete.
func TestParseVariantDescriptor_EmptyRejected(t *testing.T) {
	desc := reflect.TypeFor[struct{}]()
	if _, err := parseVariantDescriptor(desc); err == nil {
		t.Fatal("expected error for descriptor with no entries, got nil")
	}
}

func TestBuildVariantCaseLookup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping blob build in short mode")
	}
	cases := []variantCase{
		{Value: "user", Target: reflect.TypeFor[testVariantUser]()},
		{Value: "product", Target: reflect.TypeFor[testVariantProduct]()},
	}
	blob, err := buildVariantCaseLookup(cases)
	if err != nil {
		t.Fatalf("buildVariantCaseLookup: %v", err)
	}
	if len(blob) == 0 {
		t.Skip("vlib not available; skipping blob round-trip")
	}
}

func TestAttachVariantsForStruct_RegistryForm(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Variants) != 1 {
		t.Fatalf("got %d variant tables, want 1", len(tt.Variants))
	}
	v := &tt.Variants[0]
	if v.CaseCount != 2 {
		t.Errorf("CaseCount = %d, want 2", v.CaseCount)
	}
	expectedDiscOff := uint32(host.Field(0).Offset)
	if v.DiscFieldOff != expectedDiscOff {
		t.Errorf("DiscFieldOff = %d, want %d", v.DiscFieldOff, expectedDiscOff)
	}
	for i, want := range []string{"user", "product"} {
		got := *(*uint16)(unsafe.Add(v.caseTypeIdxData, uintptr(i)*2))
		if got == 0 {
			t.Errorf("case %d (%q) TypeIdx = 0, want non-zero", i, want)
		}
		_ = want
	}
	rootIdx := tt.Root
	rootType := &tt.Types[rootIdx]
	firstFieldIdx := rootType.StructFirstFieldIndex(&tt.Fields[0])
	vdiscField := &tt.Fields[firstFieldIdx]
	variantField := &tt.Fields[firstFieldIdx+1]
	if !FieldIsDiscriminator(vdiscField) {
		t.Errorf("vdisc field missing TagVDisc flag (required for fast path)")
	}
	if !FieldHasVariant(variantField) {
		t.Errorf("variant field missing TagVariant flag")
	}
	vidx := FieldVariantIdx(variantField)
	if int(vidx) != 0 {
		t.Errorf("VariantIdx = %d, want 0", vidx)
	}
}

func TestAttachVariantsForStruct_ConcurrentBuild(t *testing.T) {
	if !vlib.Available {
		t.Skip("vlib not available on this platform")
	}
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	const workers = 32
	type result struct {
		tree *TypeTree
		err  error
	}
	results := make(chan result, workers)
	for range workers {
		go func() {
			tree, err := Build(typ.UniTypeOf(host))
			results <- result{tree: tree, err: err}
		}()
	}
	trees := make([]*TypeTree, 0, workers)
	for range workers {
		r := <-results
		if r.err != nil {
			t.Fatalf("Build: %v", r.err)
		}
		trees = append(trees, r.tree)
	}
	runtime.GC()
	for i, tree := range trees {
		if got := LookupFind(tree.Variants[0].Lookup, "user"); got != 0 {
			t.Errorf("tree %d lookup = %d, want 0", i, got)
		}
	}
}

func TestAttachVariantsForStruct_MethodForm(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeMethod]()
	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Variants) != 1 {
		t.Fatalf("got %d variant tables, want 1", len(tt.Variants))
	}
	if tt.Variants[0].CaseCount != 2 {
		t.Errorf("CaseCount = %d, want 2", tt.Variants[0].CaseCount)
	}
}

func TestAttachVariantsForStruct_MissingDescriptor(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeNoDesc]()
	_, err := Build(typ.UniTypeOf(host))
	if err == nil {
		t.Fatal("expected error for missing descriptor, got nil")
	}
}

func TestAttachVariantsForStruct_BothSources(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeMethod]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)
	if _, err := Build(typ.UniTypeOf(host)); err == nil {
		t.Fatal("expected error for both descriptor sources, got nil")
	}
}

func TestAttachVariantsForStruct_PointerTarget(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopePtrTarget]()
	desc := reflect.TypeFor[struct {
		_ *testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)
	if _, err := Build(typ.UniTypeOf(host)); err != nil {
		t.Fatalf("pointer target should be accepted, got error: %v", err)
	}
}

// Several sibling variants on one host build, including two that name the same
// discriminator. Nothing about a sibling is recorded per host, so they do not
// contend: each field's flags carry its own table index, and each table its own
// discriminator offset.
func TestAttachVariantsForStruct_MultipleSiblings(t *testing.T) {
	host := reflect.TypeFor[testVariantMultiSibling]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// One table per variant field, even where two share a discriminator: the case
	// set is per field, so sharing a discriminator does not share a table.
	if len(tt.Variants) != 3 {
		t.Fatalf("got %d variant tables, want 3 (one per variant field)", len(tt.Variants))
	}
	rootType := &tt.Types[tt.Root]
	firstFieldIdx := rootType.StructFirstFieldIndex(&tt.Fields[0])
	// Field order: Kind, A, B, Cate, C.
	seen := map[uint16]bool{}
	for _, fi := range []int{1, 2, 4} {
		f := &tt.Fields[firstFieldIdx+uint32(fi)]
		if !FieldHasVariant(f) {
			t.Fatalf("field %d missing TagVariant", fi)
		}
		idx := FieldVariantIdx(f)
		if seen[idx] {
			t.Errorf("field %d reuses variant table %d; each sibling needs its own", fi, idx)
		}
		seen[idx] = true
		if got := tt.Variants[idx].CaseCount; got != 2 {
			t.Errorf("table %d CaseCount = %d, want 2", idx, got)
		}
	}
	// A and B share "kind"; C uses "cate". Dispatch locates the discriminator
	// through the table, so each table must carry the right offset.
	wantKindOff := uint32(host.Field(0).Offset)
	wantCateOff := uint32(host.Field(3).Offset)
	gotOffs := map[uint32]int{}
	for idx := range seen {
		gotOffs[tt.Variants[idx].DiscFieldOff]++
	}
	if gotOffs[wantKindOff] != 2 {
		t.Errorf("%d tables point at the \"kind\" offset %d, want 2", gotOffs[wantKindOff], wantKindOff)
	}
	if gotOffs[wantCateOff] != 1 {
		t.Errorf("%d tables point at the \"cate\" offset %d, want 1", gotOffs[wantCateOff], wantCateOff)
	}
	// No inline variant here, so the host must not be routed through the merged tape.
	if got := tt.TypeMeta[tt.Root].StructMeta().InlineVariantIdx; got != 0xFFFF {
		t.Errorf("InlineVariantIdx = %d, want 0xFFFF (no embedded variant declared)", got)
	}
}

// The embedded variant stays capped at one. Unlike a sibling it has per host
// state: InlineVariantIdx is a single slot and the struct-close split classifies
// keys against one case field set.
func TestAttachVariantsForStruct_TwoEmbeddedRejected(t *testing.T) {
	host := reflect.TypeFor[testVariantTwoInline]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	_, err := Build(typ.UniTypeOf(host))
	if err == nil {
		t.Fatal("expected error for two embedded variant fields, got nil")
	}
	if !strings.Contains(err.Error(), "multiple embedded variant fields") {
		t.Errorf("err = %q, want it to name the embedded variant limit", err)
	}
}

func TestAttachVariantsForStruct_UserDefinedIface(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeIface]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)
	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("user-defined interface variant field should be accepted: %v", err)
	}
	if len(tt.Variants) != 1 {
		t.Fatalf("got %d variant tables, want 1", len(tt.Variants))
	}
}

func TestAttachVariantsForStruct_IfaceNotImplemented(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeIfaceBad]()
	desc := reflect.TypeFor[struct {
		_ testVariantNotImpl `case:"bad"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)
	_, err := Build(typ.UniTypeOf(host))
	if err == nil {
		t.Fatal("expected error for case type not implementing interface, got nil")
	}
}

func TestDefineVariantCases_ConflictingPanics(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	// A different descriptor for the same host+disc is a genuine conflict.
	conflict := reflect.TypeFor[struct {
		_ testVariantProduct `case:"product"`
	}]()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registerVariant did not panic on conflicting descriptor")
		}
		got := fmt.Sprintf("%v", r)
		if !strings.Contains(got, "conflicting variant case definitions") {
			t.Errorf("panic = %q, want substring %q", got, "conflicting variant case definitions")
		}
	}()
	registerVariant(host, conflict, "")
}

func TestDefineVariantCases_Idempotent(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelopeNoDesc]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	// Re-registering the same descriptor must be a no-op so tests calling
	// DefineVariantCases inside the test body survive -count=N.
	registerVariant(host, desc, "")
	registerVariant(host, desc, "")

	variantRegistryMu.RLock()
	got := variantRegistry[host][""]
	variantRegistryMu.RUnlock()
	if got != desc {
		t.Errorf("registry desc = %v, want %v (idempotent re-registration should not replace)", got, desc)
	}
}

// The empty field name is the fallback slot's key, so reaching it through the per
// field API would silently overwrite the fallback instead of defining a field.
func TestDefineVariantCasesAt_EmptyFieldNamePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("DefineVariantCasesAt did not panic on empty fieldName")
		}
		got := fmt.Sprintf("%v", r)
		if !strings.Contains(got, "empty fieldName") {
			t.Errorf("panic = %q, want substring %q", got, "empty fieldName")
		}
	}()
	DefineVariantCasesAt[testVariantEnvelope, struct {
		_ testVariantUser `case:"user"`
	}]("")
}

func TestFieldVariantIdxPacking(t *testing.T) {
	var f BindField
	f.Flags = PackVariantFieldFlags(0x1234, 0)
	if !FieldHasVariant(&f) {
		t.Error("FieldHasVariant false after PackVariantFieldFlags")
	}
	if got := FieldVariantIdx(&f); got != 0x1234 {
		t.Errorf("FieldVariantIdx = %#x, want 0x1234", got)
	}
}

type testVariantUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type testVariantProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

type testVariantEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

type testVariantEnvelopeMethod struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func (testVariantEnvelopeMethod) JSONVariantCases(struct {
	user    testVariantUser
	product testVariantProduct
}) {
}

type testVariantEnvelopeNoDesc struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

type testVariantEnvelopePtrTarget struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

// Three sibling variants; A and B deliberately share the "kind" discriminator.
type testVariantMultiSibling struct {
	Kind string `json:"kind"`
	A    any    `json:"a" vjson:"variant=kind"`
	B    any    `json:"b" vjson:"variant=kind"`
	Cate string `json:"cate"`
	C    any    `json:"c" vjson:"variant=cate"`
}

type testVariantTwoInline struct {
	Kind string `json:"kind"`
	A    any    `json:",embed" vjson:"variant=kind"`
	B    any    `json:",embed" vjson:"variant=kind"`
}

type testVariantEventData interface {
	Kind() string
}

func (testVariantUser) Kind() string    { return "user" }
func (testVariantProduct) Kind() string { return "product" }

type testVariantEnvelopeIface struct {
	Type string               `json:"type"`
	Data testVariantEventData `json:"data" vjson:"variant=type"`
}

type testVariantNotImpl struct {
	Value int `json:"value"`
}

type testVariantEnvelopeIfaceBad struct {
	Type string               `json:"type"`
	Data testVariantEventData `json:"data" vjson:"variant=type"`
}

func unregisterVariant(host reflect.Type) {
	variantRegistryMu.Lock()
	defer variantRegistryMu.Unlock()
	delete(variantRegistry, host)
}

// findStreamField is tested at the build stage. The variant case rejection
// of stream-typed case targets is exercised end-to-end in stream_test (which
// can import both vbind and the stream package). Here we only verify the
// helper terminates on self-referential types and reports no false positives
// on plain structs.
func TestFindStreamField_NoFalsePositives(t *testing.T) {
	if _, ok := findStreamField(reflect.TypeFor[struct{ X int }](), nil); ok {
		t.Fatal("plain struct reported as containing stream field")
	}
	pt := reflect.TypeFor[*struct{ X int }]()
	if _, ok := findStreamField(pt, nil); ok {
		t.Fatal("pointer to plain struct reported as containing stream field")
	}
	type selfRef struct {
		Next *selfRef
	}
	if _, ok := findStreamField(reflect.TypeFor[selfRef](), nil); ok {
		t.Fatal("self-referential struct reported as containing stream field")
	}
}

// TestAttachVariantsForStruct_DefaultCase pins how a default case lands in the
// built table: it occupies a real case slot (so it has a TypeIdx, an rtype and a
// slot class like any hit) but is absent from the lookup blob, since a lookup
// miss is what selects it.
func TestAttachVariantsForStruct_DefaultCase(t *testing.T) {
	if !vlib.Available {
		t.Skip("vlib not available on this platform")
	}
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
		_ testVariantProduct
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	v := &tt.Variants[0]
	// Two entries: the named case and the default appended after it.
	if v.CaseCount != 2 {
		t.Fatalf("CaseCount = %d, want 2 (named case + default)", v.CaseCount)
	}
	if v.DefaultCaseIdx != 1 {
		t.Fatalf("DefaultCaseIdx = %d, want 1 (default is appended last)", v.DefaultCaseIdx)
	}
	// The default must carry real metadata, not a zeroed slot.
	if got := v.CaseTypeIdx(int(v.DefaultCaseIdx)); got == 0 {
		t.Error("default case TypeIdx = 0, want a collected type")
	}
	if v.CaseRType(int(v.DefaultCaseIdx)) == nil {
		t.Error("default case RType = nil, want the concrete type pointer")
	}
	// "user" resolves through the blob; the default's slot is not addressable by
	// any string, so an unmatched key must miss rather than land on it.
	if got := LookupFind(v.Lookup, "user"); got != 0 {
		t.Errorf("lookup(user) = %d, want 0", got)
	}
	if got := LookupFind(v.Lookup, "nosuch"); got >= 0 {
		t.Errorf("lookup(nosuch) = %d, want a miss: the default must not be keyed by a string", got)
	}
}

// A descriptor with no default leaves the sentinel in place, so native reports an
// unmatched value instead of silently binding case 0.
func TestAttachVariantsForStruct_NoDefaultSentinel(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser    `case:"user"`
		_ testVariantProduct `case:"product"`
	}]()
	registerVariant(host, desc, "")
	defer unregisterVariant(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := tt.Variants[0].DefaultCaseIdx; got != variantNoDefaultCase {
		t.Errorf("DefaultCaseIdx = %#x, want %#x (no default declared)", got, variantNoDefaultCase)
	}
}

// A DefineVariantCasesAt entry naming no variant field is a typo. Silence is the
// worst outcome: the field would fall back to the host-wide set and decode into
// types the author did not intend, so the build fails and names the alternatives.
func TestDefineVariantCasesAt_UnknownFieldRejected(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	// The fallback keeps the real field satisfied, so the only fault is the stray
	// entry, not a missing descriptor.
	registerVariant(host, desc, "")
	registerVariant(host, desc, "Dat") // the field is "Data"
	defer unregisterVariant(host)

	_, err := Build(typ.UniTypeOf(host))
	if err == nil {
		t.Fatal("expected error for a field name matching no variant field, got nil")
	}
	if !strings.Contains(err.Error(), `"Dat"`) {
		t.Errorf("err = %q, want it to quote the offending name", err)
	}
	if !strings.Contains(err.Error(), "Data") {
		t.Errorf("err = %q, want it to list the real variant fields", err)
	}
}

// The JSON name is the mistake this check most often catches: the sibling's JSON
// name differs from its Go name, and only the Go name is a key.
func TestDefineVariantCasesAt_JSONNameRejected(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "")
	registerVariant(host, desc, "data") // JSON name; the Go name is "Data"
	defer unregisterVariant(host)

	_, err := Build(typ.UniTypeOf(host))
	if err == nil {
		t.Fatal("expected error for a JSON name used as the field key, got nil")
	}
	if !strings.Contains(err.Error(), "Go field name") {
		t.Errorf("err = %q, want it to point at the Go/JSON name confusion", err)
	}
}

// A host with a field specific entry and no fallback still resolves, so the check
// must not require the fallback to be present.
func TestDefineVariantCasesAt_FieldOnlyNoFallback(t *testing.T) {
	host := reflect.TypeFor[testVariantEnvelope]()
	desc := reflect.TypeFor[struct {
		_ testVariantUser `case:"user"`
	}]()
	registerVariant(host, desc, "Data")
	defer unregisterVariant(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Variants) != 1 || tt.Variants[0].CaseCount != 1 {
		t.Errorf("got %d tables (first CaseCount=%d), want 1 table with 1 case", len(tt.Variants), tt.Variants[0].CaseCount)
	}
}
