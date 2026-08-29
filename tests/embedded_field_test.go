package tests

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	vjson "github.com/velox-io/json"
)

// encodeWithBoth marshals v with both encoding/json and vjson and returns the
// two JSON strings. Every embedded-field assertion below uses stdlib output as
// the ground-truth baseline, so any divergence from encoding/json fails.
func encodeWithBoth(t *testing.T, v any) (stdRaw, vjRaw string) {
	t.Helper()
	sb, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}
	vb, err := vjson.Marshal(v)
	if err != nil {
		t.Fatalf("vjson marshal: %v", err)
	}
	return string(sb), string(vb)
}

// assertJSONEqual fails the test when vjson's output is not byte-for-byte equal
// to encoding/json. Byte-level comparison is the strictest check and matches
// vjson's explicit goal of reproducing stdlib marshaling exactly.
func assertJSONEqual(t *testing.T, name, stdRaw, vjRaw string) {
	t.Helper()
	if stdRaw != vjRaw {
		t.Errorf("%s: vjson diverges from encoding/json\n  stdlib: %s\n  vjson:  %s",
			name, stdRaw, vjRaw)
	}
}

// -----------------------------------------------------------------------------
// Promotion-path types
//
// These exercise the encoder path where an anonymous embedded field has an
// empty name (no tag, json:",inline", or json:",omitempty"): subfields are
// lifted to the current level. Covers depth/shadowing, same-depth cancellation,
// non-struct named types, and unexported-type embeds without a name tag.
// -----------------------------------------------------------------------------

// Rule 1: Shallow fields win over deeper fields with the same name.

type deepInner struct {
	Name string `json:"name"`
}
type deepMiddle struct {
	deepInner
}
type deepOuter struct {
	Name string `json:"name"`
	deepMiddle
}

// Depth-2 vs Depth-1 (no direct field): the depth-1 embedding should win.
type depth2Inner struct {
	Val string `json:"val"`
}
type depth2Middle struct {
	depth2Inner
	Val string `json:"val"` // depth-1 from depth2Outer's perspective
}
type depth2Outer struct {
	depth2Middle // Val at depth-1 (Middle.Val) should shadow depth-2 (Middle.Inner.Val)
}

// Rule 2: Same-depth same-name fields cancel each other.

type cancelA struct {
	X string // JSON key "X" (no tag, relies on field name)
}
type cancelB struct {
	X string // JSON key "X", same key as cancelA.X to trigger cancellation
}
type cancelOuter struct {
	cancelA
	cancelB
	Y string `json:"y"`
}

type cancelC struct {
	Z int `json:"z"`
}
type cancelTriple struct {
	cancelA
	cancelB
	cancelC
}

// Rule 3: Embedded non-struct named types are promoted.

type MyString string

type embedNamedNonStruct struct {
	MyString
	Extra int `json:"extra"`
}

type MyInt int

type embedNamedInt struct {
	MyInt
}

// Rule 4: Exported fields from unexported embedded structs are promoted.

type unexportedEmbed struct {
	Visible string `json:"visible"`
	hidden  string //nolint
}

type outerWithUnexportedEmbed struct {
	unexportedEmbed
	Other int `json:"other"`
}

// Deeper nesting: unexported struct inside another unexported struct.
type innerUnexported struct {
	Deep string `json:"deep"`
}
type middleUnexported struct {
	innerUnexported
	Mid string `json:"mid"`
}
type outerDeepUnexported struct {
	middleUnexported
	Top string `json:"top"`
}

// -----------------------------------------------------------------------------
// Named-path types
//
// These exercise the encoder path where an anonymous embedded field has a
// non-empty name tag (e.g. json:"metadata,omitempty"): the embed is serialized
// as a nested field under that name, NOT promoted. Also covers the json:",inline"
// explicit tag, the empty-name-with-option boundary, and json:"-" exclusion.
// -----------------------------------------------------------------------------

// exportedEmbed is an embedded field with an exported (capitalized) type name,
// standing in for metav1.ObjectMeta in Kubernetes resources.
type exportedEmbed struct {
	Name   string            `json:"name,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// unexportedMetaEmbed embeds a type with a lowercase name. stdlib still serializes
// it through its tag name because anonymous fields skip the IsExported check
// (see encoding/json's fieldsByIndex).
type unexportedMetaEmbed struct {
	Name   string            `json:"name,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// inlineMeta stands in for metav1.TypeMeta, which uses json:",inline".
type inlineMeta struct {
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

// fakeNamespace mirrors the field layout of corev1.Namespace:
//
//	metav1.TypeMeta   `json:",inline"`            -> inlineMeta
//	metav1.ObjectMeta `json:"metadata,omitempty"` -> exportedEmbed
//	Spec / Status
type fakeNamespace struct {
	inlineMeta    `json:",inline"`
	exportedEmbed `json:"metadata,omitempty"`
	Spec          any `json:"spec,omitempty"`
	Status        any `json:"status,omitempty"`
}

// fakeNamespaceUnexported uses the same layout but with a lowercase embedded
// type name, exercising the IsExported bypass path on the named-field path.
type fakeNamespaceUnexported struct {
	inlineMeta          `json:",inline"`
	unexportedMetaEmbed `json:"metadata,omitempty"`
	Spec                any `json:"spec,omitempty"`
	Status              any `json:"status,omitempty"`
}

// -----------------------------------------------------------------------------
// Promotion-path tests (Rules 1-4)
// -----------------------------------------------------------------------------

func TestEmbedRule1_ShallowOverDeep(t *testing.T) {
	// Direct field (depth 0) vs embedded field (depth 1)
	input := `{"name":"deep"}`

	var stdVal deepOuter
	if err := json.Unmarshal([]byte(input), &stdVal); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vjVal deepOuter
	if err := vjson.Unmarshal([]byte(input), &vjVal); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if stdVal.Name != "deep" {
		t.Fatalf("stdlib: expected Name=\"deep\", got %q", stdVal.Name)
	}
	if vjVal.Name != stdVal.Name {
		t.Errorf("Rule 1 (depth-0 vs depth-1): vjson Name=%q, stdlib Name=%q", vjVal.Name, stdVal.Name)
	}

	// Marshal: the shallow field should be output
	stdOut, _ := json.Marshal(stdVal)
	vjOut, _ := vjson.Marshal(vjVal)
	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 1 marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}
}

func TestEmbedRule1_Depth1OverDepth2(t *testing.T) {
	input := `{"val":"hello"}`

	var stdVal depth2Outer
	if err := json.Unmarshal([]byte(input), &stdVal); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vjVal depth2Outer
	if err := vjson.Unmarshal([]byte(input), &vjVal); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	// depth2Middle.Val (depth 1) should shadow depth2Middle.depth2Inner.Val (depth 2)
	if stdVal.Val != "hello" {
		t.Fatalf("stdlib: expected Middle.Val=\"hello\", got %q", stdVal.Val)
	}
	if vjVal.Val != stdVal.Val {
		t.Errorf("Rule 1 (depth-1 vs depth-2): vjson Middle.Val=%q, stdlib=%q",
			vjVal.Val, stdVal.Val)
	}

	// Marshal round-trip
	stdOut, _ := json.Marshal(stdVal)
	vjOut, _ := vjson.Marshal(vjVal)
	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 1 depth-1>depth-2 marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}
}

func TestEmbedRule2_SameDepthCancels(t *testing.T) {
	// Marshal: "x" should NOT appear because cancelA.X and cancelB.X
	// are at the same depth and cancel each other out.
	val := cancelOuter{
		cancelA: cancelA{X: "fromA"},
		cancelB: cancelB{X: "fromB"},
		Y:       "visible",
	}

	stdOut, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}

	vjOut, err := vjson.Marshal(val)
	if err != nil {
		t.Fatalf("vjson marshal: %v", err)
	}

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 2 marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}

	// Unmarshal: "X" should be ignored (no target), "y" should work.
	input := `{"X":"ignored","y":"present"}`

	var stdVal cancelOuter
	if err := json.Unmarshal([]byte(input), &stdVal); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}

	var vjVal cancelOuter
	if err := vjson.Unmarshal([]byte(input), &vjVal); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}

	// x should NOT be decoded into either cancelA.X or cancelB.X
	if stdVal.cancelA.X != "" || stdVal.cancelB.X != "" {
		t.Fatalf("stdlib: expected both X empty, got A=%q B=%q", stdVal.cancelA.X, stdVal.cancelB.X)
	}
	if vjVal.cancelA.X != stdVal.cancelA.X || vjVal.cancelB.X != stdVal.cancelB.X {
		t.Errorf("Rule 2 unmarshal: vjson A.X=%q B.X=%q, stdlib A.X=%q B.X=%q",
			vjVal.cancelA.X, vjVal.cancelB.X, stdVal.cancelA.X, stdVal.cancelB.X)
	}
	if vjVal.Y != "present" {
		t.Errorf("Rule 2 unmarshal: vjson Y=%q, expected \"present\"", vjVal.Y)
	}
}

func TestEmbedRule2_ThreeWayCancels(t *testing.T) {
	// cancelA.X and cancelB.X cancel; cancelC.Z is unique and survives.
	val := cancelTriple{
		cancelA: cancelA{X: "a"},
		cancelB: cancelB{X: "b"},
		cancelC: cancelC{Z: 42},
	}

	stdOut, _ := json.Marshal(val)
	vjOut, _ := vjson.Marshal(val)

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 2 three-way marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}
}

func TestEmbedRule3_NonStructNamedType(t *testing.T) {
	val := embedNamedNonStruct{
		MyString: "hello",
		Extra:    1,
	}

	stdOut, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}

	vjOut, err := vjson.Marshal(val)
	if err != nil {
		t.Fatalf("vjson marshal: %v", err)
	}

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 3 marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}

	// Unmarshal
	input := string(stdOut)
	var stdVal embedNamedNonStruct
	if err := json.Unmarshal([]byte(input), &stdVal); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}

	var vjVal embedNamedNonStruct
	if err := vjson.Unmarshal([]byte(input), &vjVal); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}

	if string(vjVal.MyString) != string(stdVal.MyString) {
		t.Errorf("Rule 3 unmarshal: vjson MyString=%q, stdlib=%q", vjVal.MyString, stdVal.MyString)
	}
}

func TestEmbedRule3_NonStructIntType(t *testing.T) {
	val := embedNamedInt{MyInt: 42}

	stdOut, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}

	vjOut, err := vjson.Marshal(val)
	if err != nil {
		t.Fatalf("vjson marshal: %v", err)
	}

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 3 int marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}

	// Unmarshal
	var stdVal embedNamedInt
	json.Unmarshal(stdOut, &stdVal)

	var vjVal embedNamedInt
	vjson.Unmarshal(stdOut, &vjVal)

	if int(vjVal.MyInt) != int(stdVal.MyInt) {
		t.Errorf("Rule 3 int unmarshal: vjson=%d, stdlib=%d", vjVal.MyInt, stdVal.MyInt)
	}
}

func TestEmbedRule4_UnexportedStructExportedFields(t *testing.T) {
	val := outerWithUnexportedEmbed{
		unexportedEmbed: unexportedEmbed{Visible: "yes"},
		Other:           1,
	}

	stdOut, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}

	vjOut, err := vjson.Marshal(val)
	if err != nil {
		t.Fatalf("vjson marshal: %v", err)
	}

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 4 marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}

	// Unmarshal
	input := `{"visible":"promoted","other":2}`

	var stdVal outerWithUnexportedEmbed
	if err := json.Unmarshal([]byte(input), &stdVal); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}

	var vjVal outerWithUnexportedEmbed
	if err := vjson.Unmarshal([]byte(input), &vjVal); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}

	if vjVal.Visible != stdVal.Visible {
		t.Errorf("Rule 4 unmarshal Visible: vjson=%q, stdlib=%q", vjVal.Visible, stdVal.Visible)
	}
	if vjVal.Other != stdVal.Other {
		t.Errorf("Rule 4 unmarshal Other: vjson=%d, stdlib=%d", vjVal.Other, stdVal.Other)
	}
}

func TestEmbedRule4_DeepUnexportedEmbed(t *testing.T) {
	val := outerDeepUnexported{
		middleUnexported: middleUnexported{
			innerUnexported: innerUnexported{Deep: "d"},
			Mid:             "m",
		},
		Top: "t",
	}

	stdOut, _ := json.Marshal(val)
	vjOut, _ := vjson.Marshal(val)

	if string(vjOut) != string(stdOut) {
		t.Errorf("Rule 4 deep marshal: vjson=%s, stdlib=%s", vjOut, stdOut)
	}

	input := `{"deep":"D","mid":"M","top":"T"}`
	var stdVal outerDeepUnexported
	json.Unmarshal([]byte(input), &stdVal)

	var vjVal outerDeepUnexported
	vjson.Unmarshal([]byte(input), &vjVal)

	if vjVal.Deep != stdVal.Deep || vjVal.Mid != stdVal.Mid || vjVal.Top != stdVal.Top {
		t.Errorf("Rule 4 deep unmarshal: vjson={%q,%q,%q}, stdlib={%q,%q,%q}",
			vjVal.Deep, vjVal.Mid, vjVal.Top,
			stdVal.Deep, stdVal.Mid, stdVal.Top)
	}
}

// -----------------------------------------------------------------------------
// Named-path + boundary tests
// -----------------------------------------------------------------------------

// TestEmbedded_ExportedTaggedNamedField is a regression guard for an embedded
// anonymous field with an exported type name and a json:"metadata,omitempty" tag.
// stdlib treats it as a named field nested under "metadata". vjson previously
// promoted the inner fields to the top level, collapsing the metadata nesting;
// this test pins the nested behavior.
func TestEmbedded_ExportedTaggedNamedField(t *testing.T) {
	obj := fakeNamespace{
		inlineMeta: inlineMeta{Kind: "Namespace", APIVersion: "v1"},
		exportedEmbed: exportedEmbed{
			Name:   "khaos",
			Labels: map[string]string{"app": "registry-proxy"},
		},
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	t.Logf("stdlib: %s", stdRaw)
	t.Logf("vjson:  %s", vjRaw)
	assertJSONEqual(t, "exported tagged embed", stdRaw, vjRaw)

	// Assert the key fields explicitly so a failure points at the exact missing
	// key instead of only a byte-level diff.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(vjRaw), &parsed); err != nil {
		t.Fatalf("parse vjson output: %v", err)
	}
	meta, _ := parsed["metadata"].(map[string]any)
	if meta == nil || meta["name"] != "khaos" {
		t.Errorf("expected metadata.name=khaos, got metadata=%v", parsed["metadata"])
	}
	if parsed["kind"] != "Namespace" {
		t.Errorf("expected inline kind=Namespace, got %v", parsed["kind"])
	}
}

// TestEmbedded_UnexportedTaggedNamedField covers an anonymous embedded field
// with a lowercase type name and a name tag. stdlib lets it through because
// anonymous fields skip IsExported; vjson used to call IsExported on the
// named-field path, dropping the whole metadata object for lowercase types.
func TestEmbedded_UnexportedTaggedNamedField(t *testing.T) {
	obj := fakeNamespaceUnexported{
		inlineMeta: inlineMeta{Kind: "Namespace", APIVersion: "v1"},
		unexportedMetaEmbed: unexportedMetaEmbed{
			Name:   "khaos",
			Labels: map[string]string{"app": "registry-proxy"},
		},
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	t.Logf("stdlib: %s", stdRaw)
	t.Logf("vjson:  %s", vjRaw)
	assertJSONEqual(t, "unexported tagged embed", stdRaw, vjRaw)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(vjRaw), &parsed); err != nil {
		t.Fatalf("parse vjson output: %v", err)
	}
	meta, _ := parsed["metadata"].(map[string]any)
	if meta == nil || meta["name"] != "khaos" {
		t.Errorf("expected metadata.name=khaos, got metadata=%v", parsed["metadata"])
	}
}

// TestEmbedded_InlinePromotion checks json:",inline" embedded fields: their
// subfields are promoted to the top level. This is the inverse of the named
// path, confirming inline promotion still works.
func TestEmbedded_InlinePromotion(t *testing.T) {
	type onlyInline struct {
		inlineMeta `json:",inline"`
		Extra      string `json:"extra,omitempty"`
	}
	obj := onlyInline{
		inlineMeta: inlineMeta{Kind: "Pod", APIVersion: "v1"},
		Extra:      "x",
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	assertJSONEqual(t, "inline promotion", stdRaw, vjRaw)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(vjRaw), &parsed); err != nil {
		t.Fatalf("parse vjson output: %v", err)
	}
	if parsed["kind"] != "Pod" {
		t.Errorf("expected inline kind=Pod at top level, got %v", parsed["kind"])
	}
	if parsed["extra"] != "x" {
		t.Errorf("expected extra=x, got %v", parsed["extra"])
	}
}

// TestEmbedded_EmptyNameTagPromotes checks an anonymous embedded field tagged
// json:",omitempty" (empty name). stdlib treats an empty name as a promotion
// (subfields lifted to the current level) rather than a named field, so only a
// non-empty name should take the named-field path.
func TestEmbedded_EmptyNameTagPromotes(t *testing.T) {
	type emptyNameEmbed struct {
		Name string `json:"name,omitempty"`
	}
	type outer struct {
		emptyNameEmbed `json:",omitempty"`
		Extra          string `json:"extra,omitempty"`
	}
	obj := outer{
		emptyNameEmbed: emptyNameEmbed{Name: "promoted"},
		Extra:          "y",
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	assertJSONEqual(t, "empty-name tag promote", stdRaw, vjRaw)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(vjRaw), &parsed); err != nil {
		t.Fatalf("parse vjson output: %v", err)
	}
	// name must be promoted to the top level, not nested under an empty key.
	if parsed["name"] != "promoted" {
		t.Errorf("expected promoted name=promoted at top level, got %v", parsed["name"])
	}
	if parsed["extra"] != "y" {
		t.Errorf("expected extra=y, got %v", parsed["extra"])
	}
}

// TestEmbedded_DashTagExcludes checks an anonymous embedded field tagged
// json:"-". stdlib excludes the entire embed (it does not promote), which this
// test pins.
func TestEmbedded_DashTagExcludes(t *testing.T) {
	type dashEmbed struct {
		Name string `json:"name,omitempty"`
	}
	type outer struct {
		dashEmbed `json:"-"`
		Extra     string `json:"extra,omitempty"`
	}
	obj := outer{
		dashEmbed: dashEmbed{Name: "should-be-excluded"},
		Extra:     "kept",
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	assertJSONEqual(t, "dash tag exclude", stdRaw, vjRaw)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(vjRaw), &parsed); err != nil {
		t.Fatalf("parse vjson output: %v", err)
	}
	if _, exists := parsed["name"]; exists {
		t.Errorf("json:\"-\" embed should be excluded, but name appeared: %v", parsed["name"])
	}
	if parsed["extra"] != "kept" {
		t.Errorf("expected extra=kept, got %v", parsed["extra"])
	}
}

// -----------------------------------------------------------------------------
// Composite + round-trip
// -----------------------------------------------------------------------------

// TestEmbedded_NestedStructure reproduces a full Kubernetes resource: TypeMeta
// inline + ObjectMeta named + Spec + Status, matching corev1.Namespace. The
// byte-level comparison ensures the rendered output is acceptable to an apiserver.
func TestEmbedded_NestedStructure(t *testing.T) {
	obj := fakeNamespace{
		inlineMeta: inlineMeta{Kind: "Namespace", APIVersion: "v1"},
		exportedEmbed: exportedEmbed{
			Name:   "khaos",
			Labels: map[string]string{"app": "registry-proxy", "tier": "system"},
		},
		Spec:   map[string]any{"finalizers": []string{"kubernetes"}},
		Status: map[string]any{"phase": "Active"},
	}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	t.Logf("stdlib: %s", stdRaw)
	t.Logf("vjson:  %s", vjRaw)
	if !mapsEqual([]byte(vjRaw), []byte(stdRaw)) {
		t.Errorf("nested K8s-like structure: vjson diverges from encoding/json\n  stdlib: %s\n  vjson:  %s",
			stdRaw, vjRaw)
	}
}

// TestEmbedded_RoundTripUnmarshal marshals then unmarshals to confirm the
// decoder also handles embedded fields correctly, not just the encoder.
func TestEmbedded_RoundTripUnmarshal(t *testing.T) {
	original := fakeNamespace{
		inlineMeta: inlineMeta{Kind: "Namespace", APIVersion: "v1"},
		exportedEmbed: exportedEmbed{
			Name:   "round-trip",
			Labels: map[string]string{"k": "v"},
		},
	}
	raw, err := vjson.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded fakeNamespace
	if err := vjson.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Kind != "Namespace" || decoded.APIVersion != "v1" {
		t.Errorf("inline fields lost: kind=%q apiVersion=%q", decoded.Kind, decoded.APIVersion)
	}
	if decoded.Name != "round-trip" {
		t.Errorf("embedded Name lost: %q", decoded.Name)
	}
	if len(decoded.Labels) != 1 || decoded.Labels["k"] != "v" {
		t.Errorf("embedded Labels lost: %v", decoded.Labels)
	}

	// Cross-check against stdlib's round-trip decode of the same bytes.
	var stdDecoded fakeNamespace
	if err := json.Unmarshal(raw, &stdDecoded); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded, stdDecoded) {
		t.Errorf("round-trip diverges from stdlib\n  vjson:  %+v\n  stdlib: %+v",
			decoded, stdDecoded)
	}
}

// -----------------------------------------------------------------------------
// Promotion across an embedded pointer
//
// Promotion is normally offset arithmetic: a promoted child is reached as
// host+embedOffset+childOffset. An embedded pointer breaks that identity, since
// the promoted bytes are not inside the host at all. Such a field carries a hop
// list instead: read the pointer, allocate the pointee if it is nil, then apply
// the remaining offset from there.
//
// encoding/json is the baseline for every case, including a nil pointee (its
// keys are simply absent) and a partially built one (the existing pointee is
// written through, never replaced).
// -----------------------------------------------------------------------------

type PtrEmbedInner struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type ptrEmbedHost struct {
	*PtrEmbedInner
	Greet string `json:"greet"`
}

// TestPtrEmbed_Decode pins that decoding allocates the pointee and lands the
// promoted keys inside it without touching memory past the host. The canary is
// load-bearing: before hop support this decode reported success and wrote the
// promoted fields over whatever followed the host.
func TestPtrEmbed_Decode(t *testing.T) {
	var pad struct {
		Host   ptrEmbedHost
		Canary [4]uint64
	}
	for i := range pad.Canary {
		pad.Canary[i] = 0xD15EA5E
	}

	raw := []byte(`{"name":"bob","age":3,"greet":"hi"}`)
	if err := vjson.Unmarshal(raw, &pad.Host); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, c := range pad.Canary {
		if c != 0xD15EA5E {
			t.Errorf("canary[%d] = %#x; decode wrote past the host", i, c)
		}
	}

	var std ptrEmbedHost
	if err := json.Unmarshal(raw, &std); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}
	if pad.Host.PtrEmbedInner == nil {
		t.Fatal("pointee not allocated; want it created to hold the promoted keys")
	}
	if *pad.Host.PtrEmbedInner != *std.PtrEmbedInner || pad.Host.Greet != std.Greet {
		t.Errorf("decode diverges from stdlib\n  vjson:  %+v greet=%q\n  stdlib: %+v greet=%q",
			*pad.Host.PtrEmbedInner, pad.Host.Greet, *std.PtrEmbedInner, std.Greet)
	}
}

// TestPtrEmbed_Encode pins encode parity across the states the pointer can be
// in. A nil pointee has no storage to read, so its keys are omitted rather than
// written as null, matching encoding/json.
func TestPtrEmbed_Encode(t *testing.T) {
	for _, tc := range []struct {
		name string
		obj  ptrEmbedHost
	}{
		{"nil pointee", ptrEmbedHost{Greet: "hi"}},
		{"filled", ptrEmbedHost{PtrEmbedInner: &PtrEmbedInner{Name: "bob", Age: 3}, Greet: "hi"}},
		{"zero pointee", ptrEmbedHost{PtrEmbedInner: &PtrEmbedInner{}, Greet: "hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdRaw, vjRaw := encodeWithBoth(t, tc.obj)
			assertJSONEqual(t, "promoted across pointer", stdRaw, vjRaw)
		})
	}
}

// TestPtrEmbed_Nested covers the shape reached as a nested field. A nested
// struct is inlined into its parent's blueprint, so its promoted fields resolve
// through a different code path than the root case above.
func TestPtrEmbed_Nested(t *testing.T) {
	type outer struct {
		Inner ptrEmbedHost `json:"inner"`
		Tail  string       `json:"tail"`
	}
	obj := outer{Inner: ptrEmbedHost{PtrEmbedInner: &PtrEmbedInner{Name: "bob"}}, Tail: "t"}
	stdRaw, vjRaw := encodeWithBoth(t, obj)
	assertJSONEqual(t, "nested promoted across pointer", stdRaw, vjRaw)

	raw := []byte(`{"inner":{"name":"bob","age":2},"tail":"t"}`)
	var std, vj outer
	if err := json.Unmarshal(raw, &std); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}
	if err := vjson.Unmarshal(raw, &vj); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}
	if vj.Tail != std.Tail || *vj.Inner.PtrEmbedInner != *std.Inner.PtrEmbedInner {
		t.Errorf("nested decode diverges from stdlib\n  vjson:  %+v\n  stdlib: %+v", vj, std)
	}
}

// TestPtrEmbed_ReusesExistingPointee pins that a pointee the caller supplied is
// written through rather than replaced, so fields the input does not mention keep
// their values. encoding/json behaves the same way.
func TestPtrEmbed_ReusesExistingPointee(t *testing.T) {
	pre := &PtrEmbedInner{Name: "keep", Age: 7}
	host := ptrEmbedHost{PtrEmbedInner: pre}
	if err := vjson.Unmarshal([]byte(`{"age":9}`), &host); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if host.PtrEmbedInner != pre {
		t.Error("pointee was replaced; want the existing allocation written through")
	}
	if host.Name != "keep" {
		t.Errorf("Name = %q; want the untouched field preserved", host.Name)
	}
	if host.Age != 9 {
		t.Errorf("Age = %d; want 9", host.Age)
	}
}

// PtrEmbedExported is exported because stdlib cannot allocate an embedded
// pointer to an unexported type, which would make the named-field comparison
// below panic inside encoding/json rather than test anything.
type PtrEmbedExported struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestPtrEmbed_NamedPointerStillWorks pins that giving the embedded pointer an
// explicit JSON name keeps it an ordinary named field, addressed through the
// pointer rather than promoted, and byte-identical to encoding/json.
func TestPtrEmbed_NamedPointerStillWorks(t *testing.T) {
	type named struct {
		*PtrEmbedExported `json:"inner"`
		Greet             string `json:"greet"`
	}
	obj := named{PtrEmbedExported: &PtrEmbedExported{Name: "bob", Age: 3}, Greet: "hi"}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	assertJSONEqual(t, "named embedded pointer", stdRaw, vjRaw)

	var std, vj named
	if err := json.Unmarshal([]byte(vjRaw), &std); err != nil {
		t.Fatalf("stdlib unmarshal: %v", err)
	}
	if err := vjson.Unmarshal([]byte(vjRaw), &vj); err != nil {
		t.Fatalf("vjson unmarshal: %v", err)
	}
	if !reflect.DeepEqual(std, vj) {
		t.Errorf("decode diverges from stdlib\n  vjson:  %+v\n  stdlib: %+v", vj, std)
	}
}

// TestPtrEmbed_HookPointerStillWorks pins that an embedded pointer whose pointee
// carries JSON hooks is unaffected. Such a type promotes no field at all (the
// hook consumes the whole value and its own fields are unexported), so no hop
// list is built for it.
func TestPtrEmbed_HookPointerStillWorks(t *testing.T) {
	type hostTime struct {
		*time.Time
		Name string `json:"name"`
	}
	tm := time.Unix(0, 0).UTC()
	obj := hostTime{Time: &tm, Name: "bob"}
	stdRaw, vjRaw := encodeWithBoth(t, &obj)
	assertJSONEqual(t, "embedded *time.Time", stdRaw, vjRaw)
}

// PtrEmbedDeepLeaf is promoted two levels up, crossing a pointer on the way.
type PtrEmbedDeepLeaf struct {
	Deep string `json:"deep"`
}

// PtrEmbedDeepMid embeds the leaf by pointer, putting the hop one level below
// the outer host rather than at its own level.
type PtrEmbedDeepMid struct {
	*PtrEmbedDeepLeaf
}

// ptrEmbedDeepHost reaches the pointer hop through a value embed, pinning that
// hops accumulate along the whole promotion path.
type ptrEmbedDeepHost struct {
	PtrEmbedDeepMid
	Top string `json:"top"`
}

// ptrEmbedSubtreeLeaf sits below a pointer hop, embedded by value.
type ptrEmbedSubtreeLeaf struct {
	Leaf string `json:"leaf"`
}

type PtrEmbedSubtreeMid struct {
	ptrEmbedSubtreeLeaf
}

// ptrEmbedSubtreeHost crosses the pointer first and then keeps promoting by
// value, so the offset following the hop spans more than one embed level.
type ptrEmbedSubtreeHost struct {
	*PtrEmbedSubtreeMid
	Top string `json:"top"`
}

// PtrEmbedChainMid and ptrEmbedChainHost form a two-hop chain: reaching Deep
// crosses two pointers, either of which may need allocating.
type PtrEmbedChainMid struct {
	*PtrEmbedDeepLeaf
	Mid int `json:"mid"`
}

type ptrEmbedChainHost struct {
	*PtrEmbedChainMid
	Top string `json:"top"`
}

// TestPtrEmbed_Transitive pins that hops accumulate along the promotion path: a
// hop below the host's own level, a value embed under a hop, and a chain of two
// hops all resolve to the bytes encoding/json produces.
func TestPtrEmbed_Transitive(t *testing.T) {
	t.Run("hop one level down", func(t *testing.T) {
		raw := []byte(`{"deep":"x","top":"t"}`)
		var std, vj ptrEmbedDeepHost
		if err := json.Unmarshal(raw, &std); err != nil {
			t.Fatalf("stdlib unmarshal: %v", err)
		}
		if err := vjson.Unmarshal(raw, &vj); err != nil {
			t.Fatalf("vjson unmarshal: %v", err)
		}
		if vj.Top != std.Top || vj.Deep != std.Deep {
			t.Errorf("decode diverges: vjson deep=%q top=%q, stdlib deep=%q top=%q",
				vj.Deep, vj.Top, std.Deep, std.Top)
		}
	})

	t.Run("value embed under hop", func(t *testing.T) {
		raw := []byte(`{"leaf":"x","top":"t"}`)
		var std, vj ptrEmbedSubtreeHost
		if err := json.Unmarshal(raw, &std); err != nil {
			t.Fatalf("stdlib unmarshal: %v", err)
		}
		if err := vjson.Unmarshal(raw, &vj); err != nil {
			t.Fatalf("vjson unmarshal: %v", err)
		}
		if vj.Top != std.Top || vj.Leaf != std.Leaf {
			t.Errorf("decode diverges: vjson leaf=%q top=%q, stdlib leaf=%q top=%q",
				vj.Leaf, vj.Top, std.Leaf, std.Top)
		}
	})

	t.Run("two-hop chain", func(t *testing.T) {
		raw := []byte(`{"deep":"d","mid":5,"top":"t"}`)
		var std, vj ptrEmbedChainHost
		if err := json.Unmarshal(raw, &std); err != nil {
			t.Fatalf("stdlib unmarshal: %v", err)
		}
		if err := vjson.Unmarshal(raw, &vj); err != nil {
			t.Fatalf("vjson unmarshal: %v", err)
		}
		if vj.Top != std.Top || vj.Mid != std.Mid || vj.Deep != std.Deep {
			t.Errorf("decode diverges\n  vjson:  deep=%q mid=%d top=%q\n  stdlib: deep=%q mid=%d top=%q",
				vj.Deep, vj.Mid, vj.Top, std.Deep, std.Mid, std.Top)
		}

		// Every partial-allocation state: a nil hop omits everything below it.
		for _, tc := range []struct {
			name string
			obj  ptrEmbedChainHost
		}{
			{"both nil", ptrEmbedChainHost{Top: "t"}},
			{"outer only", ptrEmbedChainHost{PtrEmbedChainMid: &PtrEmbedChainMid{Mid: 5}, Top: "t"}},
			{"both set", ptrEmbedChainHost{
				PtrEmbedChainMid: &PtrEmbedChainMid{PtrEmbedDeepLeaf: &PtrEmbedDeepLeaf{Deep: "d"}, Mid: 5},
				Top:              "t",
			}},
		} {
			stdRaw, vjRaw := encodeWithBoth(t, tc.obj)
			assertJSONEqual(t, "chain encode "+tc.name, stdRaw, vjRaw)
		}
	})
}
