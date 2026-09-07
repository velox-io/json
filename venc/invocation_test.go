package venc

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/velox-io/json/native/encvm"
)

// The invocation model regression suite: a Go-side fallback that encodes a
// composite while a parent execution holds stack frames (slice ITER, map
// handoff under a slice, recursive CALL) must run in its own invocation. The
// shared-stack failure mode produces wrong output or crashes, so every case
// compares against encoding/json.

type invInner struct {
	Tags []string `json:"tags"`
	// Matrix forces the nested via-ptr encode to push interpreter ITER frames
	// (a []string field alone compiles to the frameless SEQ_STRING op, which
	// would leave a shared-stack corruption undetected).
	Matrix [][]int `json:"matrix"`
}

// invOuter promotes invInner across an embedded pointer, so the Tags field is
// compiled to a PtrPath fallback whose encode is a nested composite run.
type invOuter struct {
	*invInner
	B []int `json:"b"`
}

type invWrap struct {
	Items []invOuter `json:"items"`
}

type invNode struct {
	*invInner
	Children []invNode `json:"children,omitempty"`
	// Tail is read after the slice iteration, so a corrupted ITER frame
	// (base restored to nil) faults or garbles the tail instead of passing.
	Tail string `json:"tail,omitempty"`
}

type invMapWrap struct {
	M []map[string]invOuter `json:"m"`
	// Tail is read after the slice iteration; see invNode.Tail.
	Tail string `json:"tail,omitempty"`
}

func invOuterFixture() invWrap {
	return invWrap{Items: []invOuter{
		{invInner: &invInner{Tags: []string{"x", "y"}, Matrix: [][]int{{1, 2}, {3}}}, B: []int{1, 2}},
		{invInner: &invInner{Tags: []string{}, Matrix: [][]int{{}}}, B: []int{3}},
	}}
}

// runInvocationEncode encodes v in one engine and returns the output. The
// empty indent selects the compact native VM; a non-simple indent (--) forces
// the interpreter. The value pointer is derived through reflect because the
// runtime stores one-pointer-word structs directly in the interface data word.
func runInvocationEncode(t *testing.T, v any, prefix, indent string) string {
	t.Helper()
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
		rv = rv.Elem()
	}
	box := reflect.New(rt)
	box.Elem().Set(rv)

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	if indent != "" {
		es.indentString = indent
		es.indentPrefix = prefix
		es.indentDepth = 0
		es.useNativeVM = encvm.Available && isSimpleIndent(prefix, indent)
	}
	ti := EncTypeInfoOf(rt)
	if err := es.encodeTop(ti, box.UnsafePointer()); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(es.buf)
}

func assertJSONParity(t *testing.T, v any) {
	t.Helper()
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("std marshal: %v", err)
	}
	got := runInvocationEncode(t, v, "", "")
	if got != string(want) {
		t.Errorf("native compact:\n got %s\nwant %s", got, want)
	}
	gotIndent := runInvocationEncode(t, v, "", "--")
	wantIndent, _ := json.MarshalIndent(v, "", "--")
	if gotIndent != string(wantIndent) {
		t.Errorf("interpreter indent:\n got %s\nwant %s", gotIndent, wantIndent)
	}
}

func TestInvocationSliceFrameFallback(t *testing.T) {
	assertJSONParity(t, invOuterFixture())
}

func TestInvocationMapHandoffUnderSlice(t *testing.T) {
	// One map entry: map iteration order is random, and the assertion compares
	// against encoding/json's sorted order.
	v := invMapWrap{
		M: []map[string]invOuter{
			{"one": {invInner: &invInner{Tags: []string{"a"}, Matrix: [][]int{{1}}}, B: []int{1}}},
			{"one": {invInner: &invInner{Tags: []string{"b"}, Matrix: [][]int{{2}}}, B: []int{2}}},
		},
		Tail: "after-m",
	}
	assertJSONParity(t, v)
}

func TestInvocationCallFrameFallback(t *testing.T) {
	v := invNode{
		invInner: &invInner{Tags: []string{"root"}, Matrix: [][]int{{9}}},
		Children: []invNode{
			{invInner: &invInner{Tags: []string{"c1"}, Matrix: [][]int{{7}}}},
			{invInner: &invInner{Tags: []string{"c2"}, Matrix: [][]int{{8}}}},
		},
		Tail: "after-children",
	}
	assertJSONParity(t, v)
}

func TestInvocationNilEmbeddedPointer(t *testing.T) {
	v := invWrap{Items: []invOuter{{B: []int{9}}}}
	assertJSONParity(t, v)
}

// TestInvocationRootInterpNestedFrames drives the interpreter as the root
// engine (complex indent) with a slice-element fallback: the nested encode
// must use the child invocation's stack, not the running interpreter's.
func TestInvocationRootInterpNestedFrames(t *testing.T) {
	v := invOuterFixture()
	want, err := json.MarshalIndent(v, "", "--")
	if err != nil {
		t.Fatal(err)
	}
	got := runInvocationEncode(t, v, "", "--")
	if got != string(want) {
		t.Errorf("interp root:\n got %s\nwant %s", got, want)
	}
}

// TestInterpElementProtocol pins the interpreter's element-position protocol
// against encoding/json: container elements own their leading newline, and
// keyless container opens join their elements with commas. These shapes
// produced invalid JSON (missing commas) and missing newlines before the
// invocation model work. The bool cases cover the primitive ops, whose
// element-position separators come from interpWriteKey.
func TestInterpElementProtocol(t *testing.T) {
	type leaf struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	type rec struct {
		Name string `json:"name"`
		Next *rec   `json:"next,omitempty"`
		Kids []rec  `json:"kids,omitempty"`
	}
	pp := &leaf{A: 1}
	ppp := &pp

	cases := []struct {
		name string
		v    any
	}{
		{"slice-slice", [][]int{{1}, {2}}},
		{"array-slice", [2][]int{{1}, {2}}},
		{"slice-struct", []leaf{{A: 1}}},
		{"slice-ptr", []*leaf{pp, {A: 2}}},
		{"slice-any", []any{1, "x", nil}},
		{"slice-bool", []bool{true, false, true}},
		{"slice-slice-bool", [][]bool{{true}, {}, {false}}},
		{"field-bools", struct{ B []bool }{B: []bool{true, false}}},
		{"field-ptr", struct{ P *leaf }{P: pp}},
		{"field-pptr", struct{ PP **leaf }{PP: ppp}},
		{"field-slice", struct{ S []leaf }{S: []leaf{{A: 1}}}},
		{"recursive", rec{Name: "r", Next: &rec{Name: "n"}, Kids: []rec{{Name: "k"}}}},
		{"any-field", struct{ X any }{X: leaf{A: 1}}},
		{"any-nested", struct{ X any }{X: []any{leaf{A: 1}, 2.5}}},
		{"empty-containers", struct {
			E []leaf  `json:"e"`
			N [][]int `json:"n"`
			A [0]int  `json:"a"`
			Z []any   `json:"z"`
		}{E: []leaf{}, N: [][]int{{}, {1}}, Z: []any{}}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			assertJSONParity(t, c.v)
		})
	}
}

// TestInvocationDeepNestingBudget asserts the cross-invocation depth limit:
// deeply nested invocations fail with an error instead of exhausting memory.
func TestInvocationDeepNestingBudget(t *testing.T) {
	// Chain composites so each level's fallback encode is one invocation deep.
	type leaf struct {
		Tags []string `json:"tags"`
	}
	type node struct {
		*leaf
		Next any `json:"next,omitempty"`
	}
	// any-typed Next routes through the reflect path, one exec per level.
	depth := 0
	var build func() any
	build = func() any {
		depth++
		if depth > 4 {
			return nil
		}
		return &node{leaf: &leaf{Tags: []string{"t"}}, Next: build()}
	}
	v := build()
	out, err := Marshal(v)
	if err != nil {
		t.Fatalf("shallow chain must encode: %v", err)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(v); err != nil {
		t.Fatal(err)
	}
	if string(out) != want.String()[:len(want.String())-1] {
		t.Errorf("chain output mismatch:\n got %s\nwant %s", out, want.String())
	}
}

// TestEncodeAnyOneWordStruct covers Encoder.Encode(v any) with a struct whose
// single word is a pointer: the runtime stores such values directly in the
// interface data word (no heap box), so the encoder must not treat the data
// word as a pointer to the value.
func TestEncodeAnyOneWordStruct(t *testing.T) {
	type leaf struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	type oneWord struct {
		P *leaf
	}

	v := oneWord{P: &leaf{A: 1}}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	want := `{"P":{"a":1,"b":""}}` + "\n"
	if buf.String() != want {
		t.Errorf("Encode(oneWord):\n got %q\nwant %q", buf.String(), want)
	}

	// Same shape through the typed entry points must agree.
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"P":{"a":1,"b":""}}` {
		t.Errorf("Marshal(oneWord): %s", got)
	}
}
