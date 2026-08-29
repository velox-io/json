package bind

import (
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// A value.Value names one element inside a shared tape, not a tape of its own.
// Three coordinates locate it and UnmarshalValue needs all three: base is what
// the tape's indices resolve against, the root is where the element starts, and
// the extent is where it stops. Only base was being passed. The walk therefore
// started at word 0 of the region and ran to the end of it, which is the right
// answer for exactly one Value per parse (the one whose element IS the region's
// first word) and wrong for every other.
//
// It failed in two distinguishable ways, and the quiet one is worse:
//
//   - root ignored: the walk binds whatever sits at the region's origin. For a
//     sub-element that is the enclosing document, whose keys the target does not
//     declare, so they are dropped as unknown and the target comes back zeroed
//     with a nil error.
//   - extent ignored: the walk runs past the element into its siblings and
//     reports them as trailing data, so a caller sees a syntax error on input
//     that is not malformed.
//
// These cases pin each coordinate by constructing a Value the old code could
// only get wrong, then asserting on content that identifies which element was
// actually read.

// The most ordinary way to obtain a Value: parse a document and reach into it.
// Every such Value shares the document's base and differs only in root, so this
// is what the missing root coordinate broke.
func TestUnmarshalValue_SubElementRoot(t *testing.T) {
	root, err := dom.Parse([]byte(`{"a":{"n":1},"b":{"n":2},"c":{"n":3}}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	type nn struct {
		N int `json:"n"`
	}
	// Distinct values per key, so a walk that read the wrong element is caught by
	// the value rather than only by an error. The old code returned N=0 with a nil
	// error for all three.
	for i, k := range []string{"a", "b", "c"} {
		var out nn
		if err := UnmarshalValue(root.Get(k), &out); err != nil {
			t.Fatalf("%s: UnmarshalValue: %v", k, err)
		}
		if out.N != i+1 {
			t.Errorf("%s: N = %d, want %d (bound the wrong element)", k, out.N, i+1)
		}
	}
}

// An array element, which carries no key at all. Included because the root
// coordinate is the only thing that distinguishes these Values: they are
// otherwise identical, so a walk that ignores it cannot even accidentally be
// right for one of them.
func TestUnmarshalValue_ArrayElementRoot(t *testing.T) {
	arr, err := dom.Parse([]byte(`[{"n":10},{"n":20},{"n":30}]`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	type nn struct {
		N int `json:"n"`
	}
	for i := range 3 {
		var out nn
		if err := UnmarshalValue(arr.Index(i), &out); err != nil {
			t.Fatalf("Index(%d): UnmarshalValue: %v", i, err)
		}
		if want := (i + 1) * 10; out.N != want {
			t.Errorf("Index(%d): N = %d, want %d", i, out.N, want)
		}
	}
}

// A scalar element, where the extent is one word and every following word is a
// sibling. This is the sharpest test of the extent coordinate: the old code ran
// to the end of the region and so saw the entire rest of the document as
// trailing data.
func TestUnmarshalValue_ScalarExtent(t *testing.T) {
	root, err := dom.Parse([]byte(`{"s":"hello","n":42,"b":true,"tail":{"x":1}}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var s string
	if err := UnmarshalValue(root.Get("s"), &s); err != nil {
		t.Fatalf("string: %v", err)
	}
	if s != "hello" {
		t.Errorf("s = %q, want hello", s)
	}
	var n int
	if err := UnmarshalValue(root.Get("n"), &n); err != nil {
		t.Fatalf("int: %v", err)
	}
	if n != 42 {
		t.Errorf("n = %d, want 42", n)
	}
	var b bool
	if err := UnmarshalValue(root.Get("b"), &b); err != nil {
		t.Fatalf("bool: %v", err)
	}
	if !b {
		t.Error("b = false, want true")
	}
}

// The last element of a document, whose extent ends exactly at the region's end.
// Its neighbor above (a middle element) is the one the extent bound protects, so
// asserting both in one test states that the fix is a bound and not an offset:
// the same code must handle "stops early" and "stops at the end".
func TestUnmarshalValue_MiddleAndLastElement(t *testing.T) {
	root, err := dom.Parse([]byte(`{"mid":{"n":1},"last":{"n":2}}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	type nn struct {
		N int `json:"n"`
	}
	var mid, last nn
	if err := UnmarshalValue(root.Get("mid"), &mid); err != nil {
		t.Fatalf("mid: %v", err)
	}
	if err := UnmarshalValue(root.Get("last"), &last); err != nil {
		t.Fatalf("last: %v", err)
	}
	if mid.N != 1 || last.N != 2 {
		t.Errorf("mid.N = %d, last.N = %d, want 1 and 2", mid.N, last.N)
	}
}

// The root Value itself, which is the one case the old code got right (its
// element IS the region's first word and its extent IS the region's end). Kept so
// a future change that starts the walk somewhere other than the root cannot pass
// the cases above by construction.
func TestUnmarshalValue_RootUnchanged(t *testing.T) {
	root, err := dom.Parse([]byte(`{"n":7,"m":8}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var out struct {
		N int `json:"n"`
		M int `json:"m"`
	}
	if err := UnmarshalValue(root, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.N != 7 || out.M != 8 {
		t.Errorf("out = %+v, want N=7 M=8", out)
	}
}

// A merged tape, where the two coordinates fail together and visibly. An
// embedded variant plus a reserve-unknown puts two logical tapes in one arena:
// tape A holds the case content, tape B the unknown keys, and B's base sits past
// A. Binding B while positioned at A reads A's keys, so the target comes back
// holding the case's fields instead of the unknown ones. That is a wrong answer
// rather than a zeroed one, which is why this shape is worth its own case.

type extentMergedHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type extentMergedCase struct {
	Name string `json:"name"`
}

func init() {
	vbind.DefineVariantCases[extentMergedHost, struct {
		_ extentMergedCase `case:"c1"`
	}]()
}

func TestUnmarshalValue_MergedTapeView(t *testing.T) {
	var h extentMergedHost
	if err := Unmarshal([]byte(`{"kind":"c1","name":"bob","x":1,"y":{"z":2}}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// The Go API is the reference: it reads the same Value correctly, so any
	// disagreement below is UnmarshalValue's alone.
	if got := h.Rest.String(); got != `{"x":1,"y":{"z":2}}` {
		t.Fatalf("Rest = %s, want the unknown keys only", got)
	}

	// Declaring "name" as well as "x" is what makes the failure legible: it
	// belongs to tape A, so binding it non-zero proves the walk read the other
	// view rather than merely losing its place.
	var out struct {
		Name string      `json:"name"`
		X    int         `json:"x"`
		Y    value.Value `json:"y"`
	}
	if err := UnmarshalValue(h.Rest, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.Name != "" {
		t.Errorf("Name = %q, want empty: it is tape A's key and this Value is tape B", out.Name)
	}
	if out.X != 1 {
		t.Errorf("X = %d, want 1", out.X)
	}
	if got := out.Y.String(); got != `{"z":2}` {
		t.Errorf("Y = %s, want {\"z\":2}", got)
	}
}

// The same shape through the pooled path repeatedly. The arena cursor advances
// between parses, so a coordinate that happened to be zero on a cold arena stops
// being zero here.
func TestUnmarshalValue_MergedTapeViewRepeated(t *testing.T) {
	src := []byte(`{"kind":"c1","name":"bob","x":1,"y":{"z":2}}`)
	for i := range 8 {
		var h extentMergedHost
		if err := Unmarshal(src, &h); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		var out struct {
			Name string `json:"name"`
			X    int    `json:"x"`
		}
		if err := UnmarshalValue(h.Rest, &out); err != nil {
			t.Fatalf("iter %d: UnmarshalValue: %v", i, err)
		}
		if out.Name != "" || out.X != 1 {
			t.Fatalf("iter %d: Name=%q X=%d, want empty and 1", i, out.Name, out.X)
		}
	}
}

// Nesting the fix on itself: bind a sub-element, and one of the target's fields
// is a value.Value that must then bind correctly in turn. The inner Value's
// coordinates come from the walk rather than from Go, so this reaches a
// combination the cases above do not.
func TestUnmarshalValue_NestedValueField(t *testing.T) {
	root, err := dom.Parse([]byte(`{"skip":{"q":0},"want":{"n":5,"deep":{"k":"v"}}}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var mid struct {
		N    int         `json:"n"`
		Deep value.Value `json:"deep"`
	}
	if err := UnmarshalValue(root.Get("want"), &mid); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if mid.N != 5 {
		t.Errorf("N = %d, want 5", mid.N)
	}
	var inner struct {
		K string `json:"k"`
	}
	if err := UnmarshalValue(mid.Deep, &inner); err != nil {
		t.Fatalf("inner UnmarshalValue: %v", err)
	}
	if inner.K != "v" {
		t.Errorf("K = %q, want v", inner.K)
	}
}
