package bind

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/velox-io/json/vbind"
)

// TestShapeForCanonical verifies that shapeFor canonicalizes by type while
// NewParser still returns independent Parser instances.
func TestShapeForCanonical(t *testing.T) {
	type X struct {
		V int
	}
	a, err := shapeFor(reflect.TypeFor[X]())
	if err != nil {
		t.Fatalf("shapeFor: %v", err)
	}
	b, err := shapeFor(reflect.TypeFor[X]())
	if err != nil {
		t.Fatalf("shapeFor: %v", err)
	}
	if a != b {
		t.Errorf("shapeFor not canonical: %p vs %p", a, b)
	}

	// Parser instances are distinct but share the immutable shape.
	p1, err := NewParser[X]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	p2, err := NewParser[X]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if p1 == p2 {
		t.Error("NewParser should return distinct *Parser instances")
	}
	if p1.shape != p2.shape {
		t.Errorf("Parsers should share the same *shape: %p vs %p", p1.shape, p2.shape)
	}
}

// TestParserUnmarshalDirect verifies the caller held Parser path that bypasses
// the package Unmarshal pool.
func TestParserUnmarshalDirect(t *testing.T) {
	type X struct {
		N int    `json:"n"`
		S string `json:"s"`
	}
	p, err := NewParser[X]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var x X
	if err := p.Unmarshal([]byte(`{"n":42,"s":"ok"}`), &x); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if x.N != 42 || x.S != "ok" {
		t.Errorf("got %+v, want {N:42 S:ok}", x)
	}
}

// TestShapeRootPtrPrecomputed verifies that pointer root metadata is computed
// once in buildShape so the hot path does not inspect the root TypeInfo.
func TestShapeRootPtrPrecomputed(t *testing.T) {
	type Foo struct {
		V int
	}
	p, err := NewParser[*Foo]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	// if !p.rootIsPtr {
	// 	t.Error("rootIsPtr should be true for *T root")
	// }
	// RootType keeps the PTR type; document_start unwraps the chain in C.
	rootIdx := p.ctxTemplate.RootType
	if rootIdx >= uint32(len(p.tt.Types)) {
		t.Errorf("ctxTemplate.RootType %d out of range (Types len=%d)", rootIdx, len(p.tt.Types))
	}
	// RootType is the *Foo PTR type; document_start peels it in C.
	if p.tt.Types[rootIdx].Kind != vbind.KindPointer {
		t.Errorf("ctxTemplate.RootType points to kind %d, want KindPointer", p.tt.Types[rootIdx].Kind)
	}
}

// TestParserReuseAcrossCalls verifies that one Parser can carry reusable state
// across repeated parses.
func TestParserReuseAcrossCalls(t *testing.T) {
	type X struct {
		N int `json:"n"`
	}
	p, err := NewParser[X]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	for i := range 5 {
		var x X
		if err := p.Unmarshal([]byte(`{"n":42}`), &x); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if x.N != 42 {
			t.Errorf("call %d: x.N = %d, want 42", i, x.N)
		}
	}
}

// TestRootDblPtrScalar exercises the root_scalar PTR chain: a **int root
// with a JSON number value. The Go driver (rootIsPtr) allocates the outermost
// *int, and the C root_scalar while loop unwraps the remaining **int layer
// before the number write.
func TestRootDblPtrScalar(t *testing.T) {
	var got, want **int
	errV := Unmarshal([]byte(`42`), &got)
	errJ := json.Unmarshal([]byte(`42`), &want)
	if (errV == nil) != (errJ == nil) {
		t.Fatalf("error mismatch: ndec=%v stdlib=%v", errV, errJ)
	}
	if errV != nil {
		t.Fatalf("ndec.Unmarshal: %v", errV)
	}
	// Drill both pointer layers and compare the final int.
	if got == nil || *got == nil {
		t.Fatalf("ndec: got=%v, want non-nil chain", got)
	}
	if want == nil || *want == nil {
		t.Fatalf("stdlib: want=%v, expected non-nil chain", want)
	}
	if **got != **want {
		t.Errorf("value mismatch: ndec=%d stdlib=%d", **got, **want)
	}
}
