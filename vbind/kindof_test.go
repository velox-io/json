package vbind

import (
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/typ"
)

func TestParseKindofDescriptor_Valid(t *testing.T) {
	desc := reflect.TypeFor[struct {
		bool   bool
		number float64
		string string
		array  []int
		object testVariantUser
	}]()
	cases, err := parseKindofDescriptor(desc)
	if err != nil {
		t.Fatalf("parseKindofDescriptor: %v", err)
	}
	if len(cases) != 5 {
		t.Fatalf("got %d cases, want 5", len(cases))
	}
	wantKind := map[string]int8{"bool": 0, "number": 1, "string": 2, "array": 3, "object": 4}
	seen := map[string]bool{}
	for _, c := range cases {
		name := kindofKindNames[c.KindIdx]
		seen[name] = true
		if want, ok := wantKind[name]; !ok || c.KindIdx != want {
			t.Errorf("case %q: KindIdx = %d, want %d", name, c.KindIdx, want)
		}
	}
	if len(seen) != 5 {
		t.Errorf("saw %d distinct kinds, want 5", len(seen))
	}
}

func TestParseKindofDescriptor_Partial(t *testing.T) {
	desc := reflect.TypeFor[struct {
		bool   bool
		object testVariantUser
	}]()
	cases, err := parseKindofDescriptor(desc)
	if err != nil {
		t.Fatalf("parseKindofDescriptor: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
}

func TestParseKindofDescriptor_BlankRejected(t *testing.T) {
	desc := reflect.TypeFor[struct {
		_ bool
	}]()
	if _, err := parseKindofDescriptor(desc); err == nil {
		t.Fatal("expected error for blank field, got nil")
	}
}

func TestParseKindofDescriptor_CaseTagRejected(t *testing.T) {
	desc := reflect.TypeFor[struct {
		bool bool `case:"foo"`
	}]()
	if _, err := parseKindofDescriptor(desc); err == nil {
		t.Fatal("expected error for case tag, got nil")
	}
}

func TestParseKindofDescriptor_AnonymousRejected(t *testing.T) {
	desc := reflect.TypeFor[struct {
		testVariantUser
	}]()
	if _, err := parseKindofDescriptor(desc); err == nil {
		t.Fatal("expected error for anonymous embedded field, got nil")
	}
}

func TestParseKindofDescriptor_DuplicateKind(t *testing.T) {
	// Go forbids duplicate field names, so a descriptor cannot name one kind twice.
	t.Skip("kindof descriptor field names are unique by Go struct semantics; duplicate-kind check is structural")
}

func TestParseKindofDescriptor_UnknownKind(t *testing.T) {
	desc := reflect.TypeFor[struct {
		integer int
	}]()
	if _, err := parseKindofDescriptor(desc); err == nil {
		t.Fatal("expected error for unknown kind name, got nil")
	}
}

func TestParseKindofDescriptor_Empty(t *testing.T) {
	desc := reflect.TypeFor[struct{}]()
	if _, err := parseKindofDescriptor(desc); err == nil {
		t.Fatal("expected error for empty descriptor, got nil")
	}
}

func TestParseKindofDescriptor_NotStruct(t *testing.T) {
	if _, err := parseKindofDescriptor(reflect.TypeFor[int]()); err == nil {
		t.Fatal("expected error for non-struct descriptor, got nil")
	}
}

func TestAttachKindofsForStruct_RegistryForm(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelope]()
	desc := reflect.TypeFor[struct {
		bool   bool
		object testVariantUser
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Polys) != 1 {
		t.Fatalf("got %d poly tables, want 1", len(tt.Polys))
	}
	o := &tt.Polys[0]
	if o.CaseCount != polyKindCount {
		t.Errorf("CaseCount = %d, want %d (one slot per JSON kind)", o.CaseCount, polyKindCount)
	}
	if o.CaseRType(0) == nil {
		t.Error("CaseRType[bool] = nil, want the registered case rtype")
	}
	if o.CaseRType(4) == nil {
		t.Error("CaseRType[object] = nil, want the registered case rtype")
	}
	if o.CaseRType(1) != nil {
		t.Error("CaseRType[number] != nil, want nil for an unregistered kind")
	}
	rootIdx := tt.Root
	rootType := &tt.Types[rootIdx]
	firstFieldIdx := rootType.StructFirstFieldIndex(&tt.Fields[0])
	kindofField := &tt.Fields[firstFieldIdx]
	if !FieldHasKindof(kindofField) {
		t.Errorf("kindof field missing TagKindof flag")
	}
	oidx := FieldPolyIdx(kindofField)
	if int(oidx) != 0 {
		t.Errorf("PolyIdx = %d, want 0", oidx)
	}
	for _, kind := range [2]int{0, 4} {
		if got := o.CaseTypeIdx(kind); got == 0 {
			t.Errorf("CaseTypeIdx[%d] = 0, want non-zero", kind)
		}
	}
}

func TestAttachKindofsForStruct_MethodForm(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeMethod]()
	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Polys) != 1 {
		t.Fatalf("got %d poly tables, want 1", len(tt.Polys))
	}
	if got := tt.Polys[0].CaseCount; got != polyKindCount {
		t.Errorf("CaseCount = %d, want %d", got, polyKindCount)
	}
}

func TestAttachKindofsForStruct_MissingDescriptor(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeNoDesc]()
	if _, err := Build(typ.UniTypeOf(host)); err == nil {
		t.Fatal("expected error for missing descriptor, got nil")
	}
}

func TestAttachKindofsForStruct_BothSources(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeMethod]()
	desc := reflect.TypeFor[struct {
		bool bool
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)
	if _, err := Build(typ.UniTypeOf(host)); err == nil {
		t.Fatal("expected error for both descriptor sources, got nil")
	}
}

func TestAttachKindofsForStruct_PointerTarget(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopePtrTarget]()
	desc := reflect.TypeFor[struct {
		object *testVariantUser
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)
	if _, err := Build(typ.UniTypeOf(host)); err != nil {
		t.Fatalf("pointer target should be accepted, got error: %v", err)
	}
}

func TestAttachKindofsForStruct_UserDefinedIface(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeIface]()
	desc := reflect.TypeFor[struct {
		object testVariantUser
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)
	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("user-defined interface kindof field should be accepted: %v", err)
	}
	if len(tt.Polys) != 1 {
		t.Fatalf("got %d poly tables, want 1", len(tt.Polys))
	}
}

func TestAttachKindofsForStruct_IfaceNotImplemented(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeIfaceBad]()
	desc := reflect.TypeFor[struct {
		object testVariantNotImpl
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)
	if _, err := Build(typ.UniTypeOf(host)); err == nil {
		t.Fatal("expected error for case type not implementing interface, got nil")
	}
}

func TestAttachKindofsForStruct_VariantAndKindofConflict(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeConflict]()
	if _, err := Build(typ.UniTypeOf(host)); err == nil {
		t.Fatal("expected error for field with both variant and kindof tags, got nil")
	}
}

func TestFieldPolyIdxPacking_Kindof(t *testing.T) {
	var f BindField
	f.Flags = PackKindofFieldFlags(0x1234, 0)
	if !FieldHasKindof(&f) {
		t.Error("FieldHasKindof false after PackKindofFieldFlags")
	}
	if got := FieldPolyIdx(&f); got != 0x1234 {
		t.Errorf("FieldPolyIdx = %#x, want 0x1234", got)
	}
}

func TestAttachKindofsForStruct_MultipleFields(t *testing.T) {
	host := reflect.TypeFor[testKindofEnvelopeDual]()
	desc := reflect.TypeFor[struct {
		bool   bool
		number float64
		string string
		array  []testVariantUser
		object testVariantUser
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)

	tt, err := Build(typ.UniTypeOf(host))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tt.Polys) != 2 {
		t.Fatalf("got %d poly tables, want 2 (one per kindof field)", len(tt.Polys))
	}
	rootIdx := tt.Root
	rootType := &tt.Types[rootIdx]
	firstFieldIdx := rootType.StructFirstFieldIndex(&tt.Fields[0])
	for i := 0; i < 2; i++ {
		f := &tt.Fields[firstFieldIdx+uint32(i)]
		if !FieldHasKindof(f) {
			t.Errorf("field %d missing TagKindof flag", i)
		}
		oidx := FieldPolyIdx(f)
		if int(oidx) != i {
			t.Errorf("field %d PolyIdx = %d, want %d", i, oidx, i)
		}
	}
}

type testKindofEnvelope struct {
	Data any `json:"data" vjson:"kindof"`
}

type testKindofEnvelopeMethod struct {
	Data any `json:"data" vjson:"kindof"`
}

func (testKindofEnvelopeMethod) JSONKindofCases(struct {
	bool   bool
	object testVariantUser
}) {
}

type testKindofEnvelopeNoDesc struct {
	Data any `json:"data" vjson:"kindof"`
}

type testKindofEnvelopePtrTarget struct {
	Data any `json:"data" vjson:"kindof"`
}

type testKindofEnvelopeIface struct {
	Data testVariantEventData `json:"data" vjson:"kindof"`
}

type testKindofEnvelopeIfaceBad struct {
	Data testVariantEventData `json:"data" vjson:"kindof"`
}

type testKindofEnvelopeConflict struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type,kindof"`
}

type testKindofEnvelopeDual struct {
	Primary   any `json:"primary" vjson:"kindof"`
	Secondary any `json:"secondary" vjson:"kindof"`
}

func unregisterKindof(host reflect.Type) {
	kindofRegistry.Delete(host)
}

func TestDefineKindofCases_ConflictingPanics(t *testing.T) {
	// The process global registry requires a host type unique to this test.
	type conflictHost struct {
		Data any `json:"data" vjson:"kindof"`
	}
	host := reflect.TypeFor[conflictHost]()
	desc := reflect.TypeFor[struct {
		bool   bool
		object testVariantUser
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)

	// A different descriptor for the same host is a genuine conflict.
	conflict := reflect.TypeFor[struct {
		bool   bool
		object testVariantProduct
	}]()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registerKindof did not panic on conflicting descriptor")
		}
		msg, ok := r.(error)
		if !ok {
			t.Fatalf("panic value is not error: %T", r)
		}
		if !strings.Contains(msg.Error(), "conflicting kindof case definitions") {
			t.Errorf("panic message = %q, want substring %q", msg.Error(), "conflicting kindof case definitions")
		}
	}()
	registerKindof(host, conflict)
}

func TestDefineKindofCases_Idempotent(t *testing.T) {
	type idemHost struct {
		Data any `json:"data" vjson:"kindof"`
	}
	host := reflect.TypeFor[idemHost]()
	desc := reflect.TypeFor[struct {
		bool bool
	}]()
	registerKindof(host, desc)
	defer unregisterKindof(host)

	// Re-registering the same descriptor must be a no-op so tests calling
	// DefineKindofCases inside the test body survive -count=N.
	registerKindof(host, desc)
	registerKindof(host, desc)

	got, ok := kindofRegistry.Load(host)
	if !ok {
		t.Fatal("kindofRegistry has no entry after idempotent re-registration")
	}
	if got.(reflect.Type) != desc {
		t.Errorf("registry desc = %v, want %v (idempotent re-registration should not replace)", got, desc)
	}
}

func TestDefineKindofCases_FirstCallSucceeds(t *testing.T) {
	type freshHost struct {
		Data any `json:"data" vjson:"kindof"`
	}
	host := reflect.TypeFor[freshHost]()
	desc := reflect.TypeFor[struct {
		bool bool
	}]()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("first registerKindof panicked: %v", r)
		}
		unregisterKindof(host)
	}()
	registerKindof(host, desc)

	got, ok := kindofRegistry.Load(host)
	if !ok {
		t.Fatal("kindofRegistry has no entry after registerKindof")
	}
	if got != desc {
		t.Errorf("registry descriptor = %v, want %v", got, desc)
	}
}
