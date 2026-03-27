package tests

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
)

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

// Rule 2: Same-depth same-name fields cancel each other.

type cancelA struct {
	X string // JSON key "X" — no tag, relies on field name
}
type cancelB struct {
	X string // JSON key "X" — same key as cancelA.X to trigger cancellation
}
type cancelOuter struct {
	cancelA
	cancelB
	Y string `json:"y"`
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

// Three-way cancellation at same depth.
type cancelC struct {
	Z int `json:"z"`
}
type cancelTriple struct {
	cancelA
	cancelB
	cancelC
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

// Rule 3: Embedded non-struct named types are promoted.

type MyString string

type embedNamedNonStruct struct {
	MyString
	Extra int `json:"extra"`
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

type MyInt int

type embedNamedInt struct {
	MyInt
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

// Rule 4: Exported fields from unexported embedded structs are promoted.

type unexportedEmbed struct {
	Visible string `json:"visible"`
	hidden  string //nolint
}

type outerWithUnexportedEmbed struct {
	unexportedEmbed
	Other int `json:"other"`
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
