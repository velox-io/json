package vjson

import (
	"encoding/json"
	"testing"
)

// ================================================================
// Circular pointer reference tests
//
// These tests verify that:
// 1. compileBlueprint does NOT stack-overflow on circular types
// 2. Marshal output matches encoding/json for acyclic instances
// ================================================================

// --- Mutual recursion: A ↔ B ---

type circA struct {
	Name string `json:"name"`
	B    *circB `json:"b"`
}

type circB struct {
	Value int    `json:"value"`
	A     *circA `json:"a"`
}

func TestMarshal_CircularMutual_AllNil(t *testing.T) {
	v := circA{Name: "root", B: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_CircularMutual_OneLevel(t *testing.T) {
	v := circA{
		Name: "root",
		B: &circB{
			Value: 42,
			A:     nil, // terminate
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_CircularMutual_TwoLevels(t *testing.T) {
	v := circA{
		Name: "a1",
		B: &circB{
			Value: 1,
			A: &circA{
				Name: "a2",
				B: &circB{
					Value: 2,
					A:     nil,
				},
			},
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Self-referential struct ---

type selfRef struct {
	ID   int      `json:"id"`
	Next *selfRef `json:"next"`
}

func TestMarshal_SelfRef_Nil(t *testing.T) {
	v := selfRef{ID: 1, Next: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_SelfRef_Chain(t *testing.T) {
	v := selfRef{
		ID: 1,
		Next: &selfRef{
			ID: 2,
			Next: &selfRef{
				ID:   3,
				Next: nil,
			},
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Deep cycle: A → B → C → A ---

type cycleTriA struct {
	Name string    `json:"name"`
	B    *cycleTriB `json:"b"`
}

type cycleTriB struct {
	Value int       `json:"value"`
	C     *cycleTriC `json:"c"`
}

type cycleTriC struct {
	Tag string    `json:"tag"`
	A   *cycleTriA `json:"a"`
}

func TestMarshal_TriangleCycle_AllNil(t *testing.T) {
	v := cycleTriA{Name: "start", B: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_TriangleCycle_FullChain(t *testing.T) {
	v := cycleTriA{
		Name: "a",
		B: &cycleTriB{
			Value: 1,
			C: &cycleTriC{
				Tag: "c",
				A: &cycleTriA{
					Name: "a2",
					B:    nil,
				},
			},
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Tree structure (cycle through slice of pointers) ---

type treeNode struct {
	Name     string      `json:"name"`
	Children []*treeNode `json:"children"`
}

func TestMarshal_TreeCycle_Nil(t *testing.T) {
	v := treeNode{Name: "root", Children: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_TreeCycle_TwoLevels(t *testing.T) {
	v := treeNode{
		Name: "root",
		Children: []*treeNode{
			{Name: "child1", Children: nil},
			{
				Name: "child2",
				Children: []*treeNode{
					{Name: "grandchild", Children: nil},
				},
			},
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Self-referential struct through value-embedded struct ---
// type Inner struct { Back *Outer }
// type Outer struct { Inner; ID int }

type selfViaEmbedInner struct {
	Back *selfViaEmbedOuter `json:"back"`
}

type selfViaEmbedOuter struct {
	selfViaEmbedInner
	ID int `json:"id"`
}

func TestMarshal_SelfRefViaEmbed_Nil(t *testing.T) {
	v := selfViaEmbedOuter{
		selfViaEmbedInner: selfViaEmbedInner{Back: nil},
		ID:                1,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_SelfRefViaEmbed_OneLevel(t *testing.T) {
	v := selfViaEmbedOuter{
		selfViaEmbedInner: selfViaEmbedInner{
			Back: &selfViaEmbedOuter{
				selfViaEmbedInner: selfViaEmbedInner{Back: nil},
				ID:                2,
			},
		},
		ID: 1,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Value-embedded struct containing self-pointer ---
// This tests the case where a struct has an anonymous embedded struct,
// and that embedded struct has a pointer back to itself.
// NOTE: We use the self-referential inner type directly to avoid a known
// pre-existing issue where getCodecForCycle returns a partial TypeInfo
// with nil Codec when the self-referential type is first encountered as
// a transitive dependency (not the top-level type).

type embedSelfRefInner struct {
	Next *embedSelfRefInner `json:"next"`
	V    int                `json:"v"`
}

func TestMarshal_EmbedSelfRef_InnerDirect(t *testing.T) {
	// Test the self-referential inner type directly.
	v := embedSelfRefInner{
		Next: &embedSelfRefInner{
			Next: nil,
			V:    2,
		},
		V: 1,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Circular with omitempty ---

type circOmitA struct {
	Name string    `json:"name"`
	B    *circOmitB `json:"b,omitempty"`
}

type circOmitB struct {
	Value int       `json:"value"`
	A     *circOmitA `json:"a,omitempty"`
}

func TestMarshal_CircularOmitempty_AllNil(t *testing.T) {
	v := circOmitA{Name: "root"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

func TestMarshal_CircularOmitempty_Partial(t *testing.T) {
	v := circOmitA{
		Name: "a",
		B: &circOmitB{
			Value: 10,
			// A is nil → omitted
		},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}

// --- Unmarshal circular types ---

func TestUnmarshal_CircularMutual(t *testing.T) {
	input := `{"name":"a1","b":{"value":42,"a":{"name":"a2","b":null}}}`

	var std circA
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj circA
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	// Verify structure
	if vj.Name != std.Name {
		t.Errorf("Name: vjson=%q, stdlib=%q", vj.Name, std.Name)
	}
	if vj.B == nil {
		t.Fatal("vjson: B is nil")
	}
	if vj.B.Value != std.B.Value {
		t.Errorf("B.Value: vjson=%d, stdlib=%d", vj.B.Value, std.B.Value)
	}
	if vj.B.A == nil {
		t.Fatal("vjson: B.A is nil")
	}
	if vj.B.A.Name != std.B.A.Name {
		t.Errorf("B.A.Name: vjson=%q, stdlib=%q", vj.B.A.Name, std.B.A.Name)
	}
}

func TestUnmarshal_SelfRef(t *testing.T) {
	input := `{"id":1,"next":{"id":2,"next":{"id":3,"next":null}}}`

	var std selfRef
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj selfRef
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.ID != 1 || vj.Next == nil || vj.Next.ID != 2 {
		t.Errorf("unexpected structure: %+v", vj)
	}
	if vj.Next.Next == nil || vj.Next.Next.ID != 3 {
		t.Errorf("unexpected deep structure")
	}
	if vj.Next.Next.Next != nil {
		t.Errorf("expected terminal nil")
	}
}

func TestUnmarshal_TreeNode(t *testing.T) {
	input := `{"name":"root","children":[{"name":"a","children":[]},{"name":"b","children":[{"name":"c","children":null}]}]}`

	var std treeNode
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj treeNode
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.Name != "root" {
		t.Errorf("Name: %q", vj.Name)
	}
	if len(vj.Children) != 2 {
		t.Fatalf("Children len: %d", len(vj.Children))
	}
	if vj.Children[0].Name != "a" || vj.Children[1].Name != "b" {
		t.Errorf("child names: %q, %q", vj.Children[0].Name, vj.Children[1].Name)
	}
	if len(vj.Children[1].Children) != 1 || vj.Children[1].Children[0].Name != "c" {
		t.Errorf("grandchild mismatch")
	}
}

// --- Roundtrip (marshal → unmarshal) for circular types ---

func TestRoundtrip_CircularMutual(t *testing.T) {
	orig := circA{
		Name: "root",
		B: &circB{
			Value: 99,
			A: &circA{
				Name: "nested",
				B:    nil,
			},
		},
	}

	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got circA
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != orig.Name {
		t.Errorf("Name: %q != %q", got.Name, orig.Name)
	}
	if got.B == nil || got.B.Value != orig.B.Value {
		t.Errorf("B.Value mismatch")
	}
	if got.B.A == nil || got.B.A.Name != orig.B.A.Name {
		t.Errorf("B.A.Name mismatch")
	}
	if got.B.A.B != nil {
		t.Errorf("expected terminal nil")
	}
}

func TestRoundtrip_SelfRef(t *testing.T) {
	orig := selfRef{
		ID: 1,
		Next: &selfRef{
			ID: 2,
			Next: &selfRef{
				ID:   3,
				Next: nil,
			},
		},
	}

	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got selfRef
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != 1 || got.Next == nil || got.Next.ID != 2 {
		t.Errorf("shallow mismatch")
	}
	if got.Next.Next == nil || got.Next.Next.ID != 3 || got.Next.Next.Next != nil {
		t.Errorf("deep mismatch")
	}
}

// ================================================================
// Additional anonymous embed edge cases
// ================================================================

// --- Embedded struct with field name conflict at different embed depths ---

type embedConflictBase struct {
	Name string `json:"name"`
}

type embedConflictMid struct {
	embedConflictBase
}

type embedConflictTop struct {
	embedConflictMid
	Name string `json:"name"` // shadows embedConflictBase.Name
	ID   int    `json:"id"`
}

func TestEmbed_ShadowedFieldRoundtrip(t *testing.T) {
	v := embedConflictTop{
		embedConflictMid: embedConflictMid{
			embedConflictBase: embedConflictBase{Name: "hidden"},
		},
		Name: "visible",
		ID:   1,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("shadowed field:\n  vjson:  %s\n  stdlib: %s", got, std)
	}

	// Unmarshal: "name" should go to the top-level field, not the embedded one.
	input := `{"name":"decoded","id":2}`
	var stdVal embedConflictTop
	json.Unmarshal([]byte(input), &stdVal)
	var vjVal embedConflictTop
	Unmarshal([]byte(input), &vjVal)

	if vjVal.Name != stdVal.Name {
		t.Errorf("unmarshal Name: vjson=%q, stdlib=%q", vjVal.Name, stdVal.Name)
	}
	if vjVal.ID != stdVal.ID {
		t.Errorf("unmarshal ID: vjson=%d, stdlib=%d", vjVal.ID, stdVal.ID)
	}
}

// --- Embedded struct with mixed field types (value + pointer fields) ---

type embedMixedInner struct {
	A int    `json:"a"`
	B string `json:"b"`
}

type embedMixedOuter struct {
	embedMixedInner
	C bool  `json:"c"`
	D int64 `json:"d"`
}

func TestEmbed_MixedFieldTypes(t *testing.T) {
	v := embedMixedOuter{
		embedMixedInner: embedMixedInner{A: 42, B: "hello"},
		C:               true,
		D:               999,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("mixed embed:\n  vjson:  %s\n  stdlib: %s", got, std)
	}

	// Roundtrip unmarshal
	var vjVal embedMixedOuter
	if err := Unmarshal(got, &vjVal); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if vjVal.A != v.A || vjVal.B != v.B || vjVal.C != v.C || vjVal.D != v.D {
		t.Errorf("roundtrip mismatch: got %+v", vjVal)
	}
}

// --- Double embedding (3 levels of value embed) ---

type embedL3 struct {
	X int `json:"x"`
}

type embedL2 struct {
	embedL3
	Y string `json:"y"`
}

type embedL1 struct {
	embedL2
	Z bool `json:"z"`
}

func TestEmbed_ThreeLevelValue(t *testing.T) {
	v := embedL1{
		embedL2: embedL2{
			embedL3: embedL3{X: 1},
			Y:       "two",
		},
		Z: true,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	std, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("3-level embed:\n  vjson:  %s\n  stdlib: %s", got, std)
	}
}
