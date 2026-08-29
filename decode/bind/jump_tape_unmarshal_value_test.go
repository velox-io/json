package bind

import (
	"testing"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Tests for a seam-linked tape (one threaded by seam words) handed to the
// public UnmarshalValue API. The reserve-unknown Value is the only tape a
// caller can obtain from Unmarshal that carries seams, and only the sibling
// variant configuration produces them: the poly case descent runs during
// phase2_walk, between two unknown-key copies into tape B, and its arena
// appends leave B's entries non-adjacent. The inline variant cannot expose
// such a Value, because its case descent is deferred to phase2_finish, after B is
// built, so B stays contiguous; and a value.Value inline case claims every
// key, leaving nothing to excise.
//
// The robustness question these tests pin is whether the tape-bind walk
// (the same machinery UnmarshalValue runs) still binds correctly when the
// target type itself has a value.Value field: the walk must skip seams
// between entries to reach the field, then alias the field's sub-span. A
// regression that read a seam word as content, or miscounted after a jump,
// would corrupt the value.Value field's coordinates while leaving scalar
// neighbors apparently fine.

type jumpTapeHost struct {
	Type string      `json:"type"`
	Data any         `json:"data" vjson:"variant=type"`
	Exts value.Value `json:",embed"`
}

type jumpTapeCase struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func init() {
	vbind.DefineVariantCases[jumpTapeHost, struct {
		_ jumpTapeCase `case:"user"`
	}]()
}

// assertTapeHasJump sanity-checks that the document tape carries at least one
// seam word, so the test is actually exercising seam-skipping rather than
// binding a contiguous tape by accident.
func assertTapeHasJump(t *testing.T, v value.Value) {
	t.Helper()
	desc := valueDescriptor(&v)
	if desc.Doc == nil {
		t.Fatal("Value doc = nil")
	}
	snapshot := append([]uint64(nil), desc.Doc.Tape...)
	for _, w := range snapshot {
		if valueabi.IsSeam(w) {
			return
		}
	}
	t.Fatal("no seam word in tape; test is not exercising a seam-linked tape")
}

// TestJumpTapeValue_UnmarshalValueWithValueField is the core public-API test.
// Unmarshal produces Exts as a jump-linked value.Value (the sibling-variant
// reserve-unknown). UnmarshalValue then re-walks that non-contiguous tape to
// bind a target struct whose Ext2 field is itself a value.Value: the walk must
// skip the seam between ext1 and ext2, bind the scalar ext1, and alias ext2's
// sub-span. A seam-skipping regression would either read the jump word as a key
// (syntax error) or advance past ext2 (missing field).
func TestJumpTapeValue_UnmarshalValueWithValueField(t *testing.T) {
	// disc after "data", unknowns on both sides: the poly case descent between
	// the ext1 and ext2 copies into B creates the seam in B.
	src := `{"ext1":1,"data":{"name":"bob","role":"admin"},"type":"user","ext2":{"deep":true}}`
	var h jumpTapeHost
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := h.Data.(jumpTapeCase); !ok {
		t.Fatalf("Data = %T, want jumpTapeCase", h.Data)
	}
	if h.Exts.Type() != value.KindObject || h.Exts.Len() != 2 {
		t.Fatalf("Exts = %v len %d, want object of 2", h.Exts.Type(), h.Exts.Len())
	}
	assertTapeHasJump(t, h.Exts)

	type target struct {
		Ext1 int         `json:"ext1"`
		Ext2 value.Value `json:"ext2"`
	}
	var tgt target
	if err := UnmarshalValue(h.Exts, &tgt); err != nil {
		t.Fatalf("UnmarshalValue(jump tape): %v", err)
	}
	if tgt.Ext1 != 1 {
		t.Errorf("Ext1 = %d, want 1", tgt.Ext1)
	}
	if tgt.Ext2.Type() != value.KindObject {
		t.Fatalf("Ext2.Type = %v, want KindObject", tgt.Ext2.Type())
	}
	deep := tgt.Ext2.Get("deep")
	if b, ok := deep.Bool(); !ok || !b {
		t.Errorf("Ext2.deep = %v (ok=%v), want true", b, ok)
	}
}

// TestJumpTapeValue_Repeated drives the construct-then-bind pair through the
// pooled path repeatedly. A merged-tape whose seam bookkeeping assumed a zero
// base, or a jump payload left unrewritten, drifts into a failure only after
// the arena cursors have moved past the first parse's tail.
func TestJumpTapeValue_Repeated(t *testing.T) {
	src := []byte(`{"ext1":1,"data":{"name":"bob","role":"admin"},"type":"user","ext2":{"deep":true}}`)
	for i := range 8 {
		var h jumpTapeHost
		if err := Unmarshal(src, &h); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		if h.Exts.Len() != 2 {
			t.Fatalf("iter %d: Exts.Len = %d, want 2", i, h.Exts.Len())
		}
		type target struct {
			Ext1 int         `json:"ext1"`
			Ext2 value.Value `json:"ext2"`
		}
		var tgt target
		if err := UnmarshalValue(h.Exts, &tgt); err != nil {
			t.Fatalf("iter %d: UnmarshalValue: %v", i, err)
		}
		if tgt.Ext1 != 1 {
			t.Fatalf("iter %d: Ext1 = %d, want 1", i, tgt.Ext1)
		}
		deep := tgt.Ext2.Get("deep")
		if b, ok := deep.Bool(); !ok || !b {
			t.Fatalf("iter %d: Ext2.deep = %v (ok=%v), want true", i, b, ok)
		}
	}
}
