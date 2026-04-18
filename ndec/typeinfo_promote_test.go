package ndec

import (
	"reflect"
	"testing"
)

type embedA struct{ X, Y int }
type embedB struct{ Z string }

type embedPTR struct {
	*embedA
	embedB
}

type embedConflict1 struct {
	Name int `json:"Name"`
}

type embedConflict2 struct {
	Name string
}

type embedConflictSame struct {
	embedConflict1
	embedConflict2
}

type embedConflictMultiTag1 struct {
	X int `json:"x"`
}

type embedConflictMultiTag2 struct {
	X string `json:"x"`
}

type embedConflictMultiTag struct {
	embedConflictMultiTag1
	embedConflictMultiTag2 // intentional duplicate json tag for conflict testing
}

type embedCyclicA struct {
	*embedCyclicB
	X int
}

type embedCyclicB struct {
	*embedCyclicA
	Y int
}

type embedUnexportedNamed struct {
	_ embedA // unexported named field (unused, retained for reflect-based field testing)
	Y int
}

func flatFieldNames(flats []flatField) []string {
	names := make([]string, len(flats))
	for i, ff := range flats {
		names[i] = ff.name
	}
	return names
}

func TestTypeFields_BasicEmbedding(t *testing.T) {
	// Outer struct embedding two inner structs
	type Outer struct {
		embedA
		embedB
		W bool
	}
	flats := typeFields(reflect.TypeFor[Outer]())
	names := flatFieldNames(flats)
	// Promoted fields are discovered breadth first, so direct fields sort ahead
	// of fields coming from embedded structs.
	expectNames := []string{"W", "X", "Y", "Z"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Basic embedding:\n  got:  %v\n  want: %v", names, expectNames)
	}

	for _, ff := range flats {
		if ff.needsPtrChain() {
			t.Fatalf("field %q should not need ptr chain", ff.name)
		}
		t.Logf("field %q: offset=%d, path=%d steps, leafType=%v",
			ff.name, ff.accumulatedOffset(), len(ff.path), ff.leafType)
	}
}

func TestTypeFields_NonEmbedded(t *testing.T) {
	// Regular struct with no embedding
	type Simple struct {
		A int
		B string `json:"b_field"`
		C bool   `json:"-"`
	}
	flats := typeFields(reflect.TypeFor[Simple]())
	names := flatFieldNames(flats)
	// C is skipped (json:"-")
	expectNames := []string{"A", "b_field"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Non-embedded:\n  got:  %v\n  want: %v", names, expectNames)
	}

	// Verify tag flags
	for _, ff := range flats {
		if ff.name == "b_field" && !ff.nameFromTag {
			t.Fatal("b_field should have nameFromTag=true")
		}
		if ff.name == "A" && ff.nameFromTag {
			t.Fatal("A should have nameFromTag=false")
		}
	}
}

func TestTypeFields_MultiLevelEmbedding(t *testing.T) {
	type Mid struct{ embedA }
	type Top struct {
		Mid
		QQ float64
	}
	flats := typeFields(reflect.TypeFor[Top]())
	names := flatFieldNames(flats)
	expectNames := []string{"QQ", "X", "Y"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Multi-level embedding:\n  got:  %v\n  want: %v", names, expectNames)
	}
	// X and Y each have path length 3 (Top→Mid→embedA→X)
	for _, ff := range flats {
		if ff.name == "X" || ff.name == "Y" {
			if len(ff.path) != 3 {
				t.Fatalf("field %q path length=%d, want 3", ff.name, len(ff.path))
			}
		}
	}
}

func TestTypeFields_NamedTagStopsPromotion(t *testing.T) {
	// Anonymous field with explicit json tag → NOT promoted
	type Outer struct {
		Embed  embedA `json:"data"` // named tag
		embedB        // no tag → promoted
		Plain  int
	}
	flats := typeFields(reflect.TypeFor[Outer]())
	names := flatFieldNames(flats)
	// embedB is promoted: Z appears; Embed is named: "data" appears
	expectNames := []string{"data", "Plain", "Z"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Named tag:\n  got:  %v\n  want: %v", names, expectNames)
	}
}

func TestTypeFields_DepthConflictResolution(t *testing.T) {
	// embedA has X at depth 1; Outer also has X at depth 0.
	// Shallower depth (Outer.X at depth 0) wins.
	type Outer struct {
		embedA
		X float64 // shadows embedA.X
	}
	flats := typeFields(reflect.TypeFor[Outer]())
	names := flatFieldNames(flats)
	expectNames := []string{"X", "Y"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Depth conflict:\n  got:  %v\n  want: %v", names, expectNames)
	}
	// X should be the float64 one (depth 0), not int (from embedA, depth 1)
	for _, ff := range flats {
		if ff.name == "X" {
			if ff.leafType != reflect.TypeFor[float64]() {
				t.Fatalf("X type = %v, want float64", ff.leafType)
			}
		}
	}
}

func TestTypeFields_SameDepthTagWins(t *testing.T) {
	// embedConflict1.X has tag "Name", embedConflict2.Name has implicit "Name".
	// At same depth, the one with explicit tag wins.
	flats := typeFields(reflect.TypeFor[embedConflictSame]())
	names := flatFieldNames(flats)
	expectNames := []string{"Name"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Same depth tag wins:\n  got:  %v\n  want: %v", names, expectNames)
	}
	// The winner should be embedConflict1.Name (with tag "Name", type int)
	for _, ff := range flats {
		if ff.name == "Name" {
			if ff.leafType != reflect.TypeFor[int]() {
				t.Fatalf("Name type = %v, want int (tag wins over implicit)", ff.leafType)
			}
			if !ff.nameFromTag {
				t.Fatal("Name should have nameFromTag=true")
			}
		}
	}
}

func TestTypeFields_SameDepthConflictDrop(t *testing.T) {
	// embedConflictMultiTag1.X and embedConflictMultiTag2.X both have
	// explicit tags named "x". At same depth with multiple explicit
	// tags → conflict → both dropped.
	flats := typeFields(reflect.TypeFor[embedConflictMultiTag]())
	names := flatFieldNames(flats)
	// Both "x" fields should be dropped → empty list
	expectNames := []string{}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Same depth conflict drop:\n  got:  %v\n  want: %v (all dropped)", names, expectNames)
	}
}

func TestTypeFields_CyclicEmbedding(t *testing.T) {
	// embedCyclicA → *embedCyclicB → *embedCyclicA
	// typeFields should detect the cycle and not infinite loop.
	// Pointer embeddings are not promoted, so the cycle is still bounded and only
	// the direct field X remains visible here.
	flats := typeFields(reflect.TypeFor[embedCyclicA]())
	names := flatFieldNames(flats)
	expectNames := []string{"X"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Cyclic embedding:\n  got:  %v\n  want: %v", names, expectNames)
	}
}

func TestTypeFields_UnexportedAnonymousStruct(t *testing.T) {
	// unexportedInner is an unexported type with exported fields.
	// When embedded as an anonymous field, its exported fields
	// should still be promoted (stdlib behavior).
	type unexportedInner struct{ X, Y int }
	type Outer struct {
		unexportedInner // anonymous, unexported type
		Z               int
	}
	flats := typeFields(reflect.TypeFor[Outer]())
	names := flatFieldNames(flats)
	// Z is depth 0, X and Y are depth 1 (promoted from unexportedInner)
	expectNames := []string{"Z", "X", "Y"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Unexported anonymous struct:\n  got:  %v\n  want: %v", names, expectNames)
	}
}

func TestTypeFields_UnexportedNamedField(t *testing.T) {
	// embedUnexportedNamed has a named unexported field 'inner embedA'.
	// Named unexported fields are skipped (not exported, not anonymous).
	flats := typeFields(reflect.TypeFor[embedUnexportedNamed]())
	names := flatFieldNames(flats)
	expectNames := []string{"Y"}
	if !reflect.DeepEqual(names, expectNames) {
		t.Fatalf("Unexported named:\n  got:  %v\n  want: %v", names, expectNames)
	}
}

func TestTypeFields_PTRPromotionDeferred(t *testing.T) {
	// Pointer embeddings stay unpromoted, while the non-pointer embedded struct
	// still contributes its promoted fields.
	type Outer struct {
		embedPTR
		Extra bool
	}
	flats := typeFields(reflect.TypeFor[Outer]())
	names := flatFieldNames(flats)
	// embedB is promoted (non-ptr). embedPTR is promoted as a struct
	// (it's anonymous struct, not ptr-to-struct).
	// embedPTR itself is now treated as a regular struct since *embedA
	// is inside it and won't be promoted.
	t.Logf("PTR embedded fields: %v", names)
	// Basic check: should not panic/loop, and should produce some fields
	if len(names) == 0 {
		t.Fatal("Expected some fields from PTR embedded struct")
	}
}

func TestParseJSONTagFull_Basic(t *testing.T) {
	type testStruct struct {
		NoTag   int
		Named   string `json:"renamed"`
		Dash    bool   `json:"-"`
		Omitted int    `json:",omitempty"`
		Quoted  string `json:"q,string"`
		Empty   int    `json:""`
	}
	ty := reflect.TypeFor[testStruct]()

	check := func(idx int, name string, hasName, dash bool, flags uint8) {
		f := ty.Field(idx)
		jt := parseJSONTagFull(f)
		if jt.name != name {
			t.Errorf("field %d name: got %q, want %q", idx, jt.name, name)
		}
		if jt.hasName != hasName {
			t.Errorf("field %d hasName: got %v, want %v", idx, jt.hasName, hasName)
		}
		if jt.dash != dash {
			t.Errorf("field %d dash: got %v, want %v", idx, jt.dash, dash)
		}
		if jt.flags != flags {
			t.Errorf("field %d flags: got %v, want %v", idx, jt.flags, flags)
		}
	}

	check(0, "", false, false, 0)            // NoTag: no json tag
	check(1, "renamed", true, false, 0)      // Named: json:"renamed"
	check(2, "-", false, true, 0)            // Dash: json:"-"
	check(3, "", false, false, bffOmitEmpty) // Omitted: json:",omitempty" → hasName=false (name empty before comma)
	check(4, "q", true, false, bffQuoted)    // Quoted: json:"q,string"
	check(5, "", true, false, 0)             // Empty: json:""
}
