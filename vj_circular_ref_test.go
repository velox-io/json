package vjson

import (
	"encoding/json"
	"testing"
)

// Circular pointer reference tests
//
// These tests verify that:
// 1. compileBlueprint does NOT stack-overflow on circular types
// 2. Marshal output matches encoding/json for acyclic instances

// Mutual recursion: A ↔ B

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

// Self-referential struct

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

// Deep cycle: A → B → C → A

type cycleTriA struct {
	Name string     `json:"name"`
	B    *cycleTriB `json:"b"`
}

type cycleTriB struct {
	Value int        `json:"value"`
	C     *cycleTriC `json:"c"`
}

type cycleTriC struct {
	Tag string     `json:"tag"`
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

// Tree structure (cycle through slice of pointers)

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

// Self-referential struct through value-embedded struct
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

// Value-embedded struct containing self-pointer
// This tests the case where a struct has an anonymous embedded struct,
// and that embedded struct has a pointer back to itself.

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

// Self-referential type as indirect dependency via anonymous embedding
//
// Reproduces the getCodecForCycle nil Codec copy bug.
//
// When the self-referential type (Node) is embedded anonymously into Wrap,
// and Wrap is the top-level type being built, the codec construction order
// causes Node's *Node field to receive a nil Codec:
//
//   GetCodec(Wrap)
//     BuildStructCodec(Wrap)
//       CollectStructFields(Wrap):
//         BFS expands anonymous Node → processes Node's fields:
//           field Next *Node → getCodecForCycle(*Node)
//             → not cached, sync build *Node:
//               BuildPointerCodec(*Node):
//                 getCodecForCycle(Node)
//                   → not cached, sync build Node:
//                     BuildStructCodec(Node)
//                       CollectStructFields(Node):
//                         field Next *Node → getCodecForCycle(*Node)
//                           → already cached (in-progress!)
//                           → returns ti, Codec = nil !!
//                         fi.Codec = cached.Codec = nil  ← BUG
//
// In contrast, when Node is the top-level type (TestMarshal_EmbedSelfRef_InnerDirect),
// Node's codecEntry enters the cache first, so *Node can be fully built
// before the value copy happens — no bug.
//
// IMPORTANT: These types must be unique and NOT shared with any other test
// (e.g. TestMarshal_EmbedSelfRef_InnerDirect). The global codecCache is
// shared across all tests; if another test builds the inner type first as
// a top-level type, the cache entry will already be complete and this test
// will silently pass without exercising the bug path.

type indirectSelfRefNode struct {
	Next *indirectSelfRefNode `json:"next"`
	V    int                  `json:"v"`
}

type indirectSelfRefWrap struct {
	indirectSelfRefNode        // anonymous embed of self-referential struct
	Tag                 string `json:"tag"`
}

func TestRoundtrip_IndirectSelfRef(t *testing.T) {
	orig := indirectSelfRefWrap{
		indirectSelfRefNode: indirectSelfRefNode{
			Next: &indirectSelfRefNode{Next: nil, V: 2},
			V:    1,
		},
		Tag: "ok",
	}

	// Marshal — before fix: panic (nil Codec type-asserted as *PointerCodec)
	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	std, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("stdlib Marshal: %v", err)
	}
	if string(data) != string(std) {
		t.Errorf("marshal mismatch:\n  vjson:  %s\n  stdlib: %s", data, std)
	}

	// Unmarshal round-trip
	var got indirectSelfRefWrap
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Tag != orig.Tag {
		t.Errorf("Tag: %q != %q", got.Tag, orig.Tag)
	}
	if got.V != orig.V {
		t.Errorf("V: %d != %d", got.V, orig.V)
	}
	if got.Next == nil || got.Next.V != orig.Next.V {
		t.Errorf("Next.V mismatch")
	}
	if got.Next.Next != nil {
		t.Errorf("expected terminal nil")
	}
}

// Circular with omitempty

type circOmitA struct {
	Name string     `json:"name"`
	B    *circOmitB `json:"b,omitempty"`
}

type circOmitB struct {
	Value int        `json:"value"`
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

// Unmarshal circular types

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

// Roundtrip (marshal → unmarshal) for circular types

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

// Additional anonymous embed edge cases

// Embedded struct with field name conflict at different embed depths

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

// Embedded struct with mixed field types (value + pointer fields)

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

// Double embedding (3 levels of value embed)

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

// Transitive circular reference tests
//
// These verify that resolveCodec() correctly handles the case where a
// self-referential type is first encountered as a *transitive dependency*
// (not the top-level marshal/unmarshal target). In this scenario,
// getCodecForCycle returns a partial TypeInfo with nil Codec, which is
// value-copied into StructCodec.Fields[]. resolveCodec() must lazily
// resolve it from the codec cache at usage time.
//
// Each test group uses unique types (not shared with other tests) so
// that codec construction order is deterministic regardless of which
// test runs first — the outer wrapper type is always the entry point.

// Scenario 1: Wrapper embeds a self-referential struct by value
// Construction: Wrapper → Inner (via value embed) → *Inner (cycle)
// Inner's Codec is nil when Wrapper's Fields[] is built.

type transInner1 struct {
	Next *transInner1 `json:"next"`
	V    int          `json:"v"`
}

type transWrapper1 struct {
	transInner1
	Label string `json:"label"`
}

func TestMarshal_TransitiveSelfRef_ViaEmbed(t *testing.T) {
	v := transWrapper1{
		transInner1: transInner1{
			Next: &transInner1{
				Next: nil,
				V:    2,
			},
			V: 1,
		},
		Label: "wrap",
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

func TestMarshal_TransitiveSelfRef_ViaEmbed_NilChain(t *testing.T) {
	v := transWrapper1{
		transInner1: transInner1{Next: nil, V: 0},
		Label:       "empty",
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

func TestUnmarshal_TransitiveSelfRef_ViaEmbed(t *testing.T) {
	input := `{"next":{"next":null,"v":2},"v":1,"label":"wrap"}`

	var std transWrapper1
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj transWrapper1
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.Label != std.Label {
		t.Errorf("Label: vjson=%q, stdlib=%q", vj.Label, std.Label)
	}
	if vj.V != std.V {
		t.Errorf("V: vjson=%d, stdlib=%d", vj.V, std.V)
	}
	if vj.Next == nil {
		t.Fatal("vjson: Next is nil")
	}
	if vj.Next.V != std.Next.V {
		t.Errorf("Next.V: vjson=%d, stdlib=%d", vj.Next.V, std.Next.V)
	}
	if vj.Next.Next != nil {
		t.Errorf("expected terminal nil, got %+v", vj.Next.Next)
	}
}

func TestRoundtrip_TransitiveSelfRef_ViaEmbed(t *testing.T) {
	orig := transWrapper1{
		transInner1: transInner1{
			Next: &transInner1{
				Next: &transInner1{Next: nil, V: 3},
				V:    2,
			},
			V: 1,
		},
		Label: "deep",
	}

	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got transWrapper1
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Label != orig.Label || got.V != orig.V {
		t.Errorf("top-level mismatch: got %+v", got)
	}
	if got.Next == nil || got.Next.V != 2 {
		t.Fatal("level-2 mismatch")
	}
	if got.Next.Next == nil || got.Next.Next.V != 3 {
		t.Fatal("level-3 mismatch")
	}
	if got.Next.Next.Next != nil {
		t.Errorf("expected terminal nil")
	}
}

// Scenario 2: Wrapper has a named field (not embed) of self-referential type
// Construction: Wrapper → Inner (named field) → *Inner (cycle)

type transInner2 struct {
	Child *transInner2 `json:"child"`
	Name  string       `json:"name"`
}

type transWrapper2 struct {
	ID    int         `json:"id"`
	Inner transInner2 `json:"inner"`
}

func TestMarshal_TransitiveSelfRef_NamedField(t *testing.T) {
	v := transWrapper2{
		ID: 1,
		Inner: transInner2{
			Name: "root",
			Child: &transInner2{
				Name:  "child",
				Child: nil,
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

func TestUnmarshal_TransitiveSelfRef_NamedField(t *testing.T) {
	input := `{"id":1,"inner":{"child":{"child":null,"name":"child"},"name":"root"}}`

	var std transWrapper2
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj transWrapper2
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.ID != std.ID || vj.Inner.Name != std.Inner.Name {
		t.Errorf("top-level: vjson=%+v, stdlib=%+v", vj, std)
	}
	if vj.Inner.Child == nil || vj.Inner.Child.Name != std.Inner.Child.Name {
		t.Errorf("child: vjson=%+v", vj.Inner.Child)
	}
}

func TestRoundtrip_TransitiveSelfRef_NamedField(t *testing.T) {
	orig := transWrapper2{
		ID: 42,
		Inner: transInner2{
			Name: "a",
			Child: &transInner2{
				Name:  "b",
				Child: &transInner2{Name: "c", Child: nil},
			},
		},
	}

	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got transWrapper2
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != orig.ID {
		t.Errorf("ID: %d != %d", got.ID, orig.ID)
	}
	if got.Inner.Name != "a" || got.Inner.Child == nil {
		t.Fatal("level-1 mismatch")
	}
	if got.Inner.Child.Name != "b" || got.Inner.Child.Child == nil {
		t.Fatal("level-2 mismatch")
	}
	if got.Inner.Child.Child.Name != "c" || got.Inner.Child.Child.Child != nil {
		t.Fatal("level-3 mismatch")
	}
}

// Scenario 3: Transitive mutual recursion through a third type
// Construction: Wrapper → NodeA → *NodeB → *NodeA (cycle)
// None of NodeA, NodeB, *NodeA, *NodeB have been seen before Wrapper.

type transNodeA3 struct {
	Name string       `json:"name"`
	B    *transNodeB3 `json:"b"`
}

type transNodeB3 struct {
	Value int          `json:"value"`
	A     *transNodeA3 `json:"a"`
}

type transWrapper3 struct {
	Tag  string      `json:"tag"`
	Node transNodeA3 `json:"node"`
}

func TestMarshal_TransitiveMutualRecursion(t *testing.T) {
	v := transWrapper3{
		Tag: "test",
		Node: transNodeA3{
			Name: "a1",
			B: &transNodeB3{
				Value: 10,
				A: &transNodeA3{
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

func TestUnmarshal_TransitiveMutualRecursion(t *testing.T) {
	input := `{"tag":"test","node":{"name":"a1","b":{"value":10,"a":{"name":"a2","b":null}}}}`

	var std transWrapper3
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj transWrapper3
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.Tag != std.Tag {
		t.Errorf("Tag: vjson=%q, stdlib=%q", vj.Tag, std.Tag)
	}
	if vj.Node.Name != std.Node.Name {
		t.Errorf("Node.Name: vjson=%q, stdlib=%q", vj.Node.Name, std.Node.Name)
	}
	if vj.Node.B == nil || vj.Node.B.Value != std.Node.B.Value {
		t.Errorf("Node.B.Value mismatch")
	}
	if vj.Node.B.A == nil || vj.Node.B.A.Name != std.Node.B.A.Name {
		t.Errorf("Node.B.A.Name mismatch")
	}
}

func TestRoundtrip_TransitiveMutualRecursion(t *testing.T) {
	orig := transWrapper3{
		Tag: "round",
		Node: transNodeA3{
			Name: "n1",
			B: &transNodeB3{
				Value: 100,
				A: &transNodeA3{
					Name: "n2",
					B: &transNodeB3{
						Value: 200,
						A:     nil,
					},
				},
			},
		},
	}

	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got transWrapper3
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Tag != orig.Tag || got.Node.Name != orig.Node.Name {
		t.Errorf("top-level mismatch")
	}
	if got.Node.B == nil || got.Node.B.Value != 100 {
		t.Fatal("B mismatch")
	}
	if got.Node.B.A == nil || got.Node.B.A.Name != "n2" {
		t.Fatal("B.A mismatch")
	}
	if got.Node.B.A.B == nil || got.Node.B.A.B.Value != 200 {
		t.Fatal("B.A.B mismatch")
	}
	if got.Node.B.A.B.A != nil {
		t.Errorf("expected terminal nil")
	}
}

// Scenario 4: Slice of self-referential struct as transitive dep
// Construction: Wrapper → []Inner → Inner → *Inner (cycle)

type transInner4 struct {
	ID   int          `json:"id"`
	Next *transInner4 `json:"next"`
}

type transWrapper4 struct {
	Items []transInner4 `json:"items"`
	Total int           `json:"total"`
}

func TestMarshal_TransitiveSelfRef_ViaSlice(t *testing.T) {
	v := transWrapper4{
		Total: 2,
		Items: []transInner4{
			{ID: 1, Next: &transInner4{ID: 11, Next: nil}},
			{ID: 2, Next: nil},
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

func TestUnmarshal_TransitiveSelfRef_ViaSlice(t *testing.T) {
	input := `{"items":[{"id":1,"next":{"id":11,"next":null}},{"id":2,"next":null}],"total":2}`

	var std transWrapper4
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj transWrapper4
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if vj.Total != std.Total || len(vj.Items) != len(std.Items) {
		t.Fatalf("top-level: vjson=%+v, stdlib=%+v", vj, std)
	}
	if vj.Items[0].ID != 1 || vj.Items[0].Next == nil || vj.Items[0].Next.ID != 11 {
		t.Errorf("item[0] mismatch: %+v", vj.Items[0])
	}
	if vj.Items[1].ID != 2 || vj.Items[1].Next != nil {
		t.Errorf("item[1] mismatch: %+v", vj.Items[1])
	}
}

// Scenario 5: Map value is a self-referential struct (transitive)
// Construction: Wrapper → map[string]Inner → Inner → *Inner (cycle)

type transInner5 struct {
	Data string       `json:"data"`
	Ref  *transInner5 `json:"ref"`
}

type transWrapper5 struct {
	Entries map[string]transInner5 `json:"entries"`
}

func TestMarshal_TransitiveSelfRef_ViaMap(t *testing.T) {
	v := transWrapper5{
		Entries: map[string]transInner5{
			"x": {Data: "hello", Ref: &transInner5{Data: "world", Ref: nil}},
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

func TestUnmarshal_TransitiveSelfRef_ViaMap(t *testing.T) {
	input := `{"entries":{"x":{"data":"hello","ref":{"data":"world","ref":null}}}}`

	var std transWrapper5
	if err := json.Unmarshal([]byte(input), &std); err != nil {
		t.Fatalf("stdlib: %v", err)
	}

	var vj transWrapper5
	if err := Unmarshal([]byte(input), &vj); err != nil {
		t.Fatalf("vjson: %v", err)
	}

	if len(vj.Entries) != 1 {
		t.Fatalf("entries len: %d", len(vj.Entries))
	}
	x := vj.Entries["x"]
	if x.Data != "hello" || x.Ref == nil || x.Ref.Data != "world" || x.Ref.Ref != nil {
		t.Errorf("mismatch: %+v", x)
	}
}

// Scenario 6: MarshalIndent path for transitive self-ref
// Exercises the encodeStructIndent → encodeValue → encodeValueSlow path
// where field-level TypeInfo.Codec may be nil.

func TestMarshalIndent_TransitiveSelfRef(t *testing.T) {
	v := transWrapper2{
		ID: 1,
		Inner: transInner2{
			Name: "a",
			Child: &transInner2{
				Name:  "b",
				Child: nil,
			},
		},
	}
	got, err := MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	std, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("stdlib error: %v", err)
	}
	if string(got) != string(std) {
		t.Errorf("indent mismatch:\n  vjson:\n%s\n  stdlib:\n%s", got, std)
	}
}

// Indirect recursion: Root → A → []B → A
// A is not the root type, and the cycle goes through a different struct B.

type indirectCycleA struct {
	Name  string           `json:"name"`
	Items []indirectCycleB `json:"items"`
}

type indirectCycleB struct {
	Value int             `json:"value"`
	Back  *indirectCycleA `json:"back"`
}

type indirectCycleRoot struct {
	ID int             `json:"id"`
	A  *indirectCycleA `json:"a"`
}

func TestMarshal_IndirectCycle_Nil(t *testing.T) {
	v := indirectCycleRoot{ID: 1, A: nil}
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

func TestMarshal_IndirectCycle_TwoLevels(t *testing.T) {
	v := indirectCycleRoot{
		ID: 1,
		A: &indirectCycleA{
			Name: "top",
			Items: []indirectCycleB{
				{Value: 10, Back: &indirectCycleA{
					Name: "nested",
					Items: []indirectCycleB{
						{Value: 20, Back: nil},
					},
				}},
				{Value: 30, Back: nil},
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

func TestRoundtrip_IndirectCycle(t *testing.T) {
	orig := indirectCycleRoot{
		ID: 1,
		A: &indirectCycleA{
			Name: "top",
			Items: []indirectCycleB{
				{Value: 10, Back: &indirectCycleA{
					Name:  "nested",
					Items: nil,
				}},
			},
		},
	}
	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var got indirectCycleRoot
	err = Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	data2, err := Marshal(&got)
	if err != nil {
		t.Fatalf("re-Marshal error: %v", err)
	}
	if string(data) != string(data2) {
		t.Errorf("roundtrip mismatch:\n  first:  %s\n  second: %s", data, data2)
	}
}

// Mutual recursion chain: Root → A → B → C → A
// Three-way cycle where each struct references the next via pointer.

type mutualChainA struct {
	Name string        `json:"name"`
	B    *mutualChainB `json:"b"`
}

type mutualChainB struct {
	Value int           `json:"value"`
	C     *mutualChainC `json:"c"`
}

type mutualChainC struct {
	Tag string        `json:"tag"`
	A   *mutualChainA `json:"a"`
}

type mutualChainRoot struct {
	ID int           `json:"id"`
	A  *mutualChainA `json:"a"`
}

func TestMarshal_MutualChain_Nil(t *testing.T) {
	v := mutualChainRoot{ID: 1, A: nil}
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

func TestMarshal_MutualChain_FullLoop(t *testing.T) {
	v := mutualChainRoot{
		ID: 1,
		A: &mutualChainA{
			Name: "a1",
			B: &mutualChainB{
				Value: 42,
				C: &mutualChainC{
					Tag: "c1",
					A: &mutualChainA{
						Name: "a2",
						B: &mutualChainB{
							Value: 99,
							C:     nil,
						},
					},
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

func TestMarshal_MutualChain_PartialNil(t *testing.T) {
	v := mutualChainRoot{
		ID: 1,
		A: &mutualChainA{
			Name: "a1",
			B: &mutualChainB{
				Value: 10,
				C: &mutualChainC{
					Tag: "leaf",
					A:   nil,
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

func TestRoundtrip_MutualChain(t *testing.T) {
	orig := mutualChainRoot{
		ID: 1,
		A: &mutualChainA{
			Name: "a1",
			B: &mutualChainB{
				Value: 42,
				C: &mutualChainC{
					Tag: "c1",
					A: &mutualChainA{
						Name: "a2",
						B:    nil,
					},
				},
			},
		},
	}
	data, err := Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var got mutualChainRoot
	err = Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	data2, err := Marshal(&got)
	if err != nil {
		t.Fatalf("re-Marshal error: %v", err)
	}
	if string(data) != string(data2) {
		t.Errorf("roundtrip mismatch:\n  first:  %s\n  second: %s", data, data2)
	}
}
