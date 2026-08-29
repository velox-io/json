package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// A host with both an embedded variant and a reserve-unknown has two consumers for
// one merged tape wanting different subsets of it. They are served as two seam
// views over the same physical words: view A is the case content, view B the
// unknown fields, and the seam in front of each entry carries a distance per view,
// so widening one half removes the entry from that view alone.
//
// What this replaced was a copy: the unknowns were duplicated onto a second tape
// because a seam held one distance and could serve only one view. Measured on the
// commit before the change, same inputs:
//
//	{"kind":"c1","name":"bob"}                     11 -> 11 words
//	...,"u1":1}                                    18 -> 15
//	...,"u1":1,"u2":2}                             25 -> 19
//	...,"u1":1,"u2":2,"u3":{"deep":[1,2,3]}}       50 -> 32
//
// The saving grows with the unknowns' size, since the copy scaled with the subtree
// while the header does not.
//
// These cases pin the behavior the two views must have. The sharp ones are the
// negatives: each view must NOT see what belongs to the other, which is the failure
// mode a copy could not produce and a shared-word encoding can.

type dualViewHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type dualViewCase struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func init() {
	vbind.DefineVariantCases[dualViewHost, struct {
		_ dualViewCase `case:"c1"`
	}]()
}

// The two views over one tape, asserted together. Interleaving case fields with
// unknown ones matters: a view that leapt a whole run at once rather than chaining
// per entry would pass a test where each side's keys were contiguous.
func TestDualView_ViewsSeeDisjointSubsets(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantName string
		wantAge  int
		wantRest string
	}{
		{
			"interleaved",
			`{"u1":1,"kind":"c1","name":"bob","u2":2,"age":7,"u3":3}`,
			"bob", 7, `{"u1":1,"u2":2,"u3":3}`,
		},
		{
			"case_fields_first",
			`{"kind":"c1","name":"bob","age":7,"u1":1,"u2":2}`,
			"bob", 7, `{"u1":1,"u2":2}`,
		},
		{
			"unknowns_first",
			`{"u1":1,"u2":2,"kind":"c1","name":"bob","age":7}`,
			"bob", 7, `{"u1":1,"u2":2}`,
		},
		{
			// Consecutive drops from one view must chain rather than needing a
			// single wide distance.
			"consecutive_unknowns",
			`{"kind":"c1","name":"bob","age":7,"u1":1,"u2":2,"u3":3,"u4":4}`,
			"bob", 7, `{"u1":1,"u2":2,"u3":3,"u4":4}`,
		},
		{
			"consecutive_case_fields",
			`{"u1":1,"kind":"c1","name":"bob","age":7,"u2":2}`,
			"bob", 7, `{"u1":1,"u2":2}`,
		},
		{
			// No unknowns: view B keeps nothing and must read as an empty object,
			// not as invalid. There is no separate empty tape any more; a fully
			// dropped view walks from its lead-in straight to the shared close.
			"no_unknowns",
			`{"kind":"c1","name":"bob","age":7}`,
			"bob", 7, `{}`,
		},
		{
			// The mirror: view A keeps nothing but the case still binds, from
			// fields that are simply absent.
			"no_case_fields",
			`{"kind":"c1","u1":1,"u2":2}`,
			"", 0, `{"u1":1,"u2":2}`,
		},
		{
			// A container as an unknown value: view B must carry the whole subtree,
			// and its inner container indices must still resolve, which is why both
			// views share one base.
			"container_unknown",
			`{"kind":"c1","name":"bob","u1":{"a":[1,2],"b":{"c":3}}}`,
			"bob", 0, `{"u1":{"a":[1,2],"b":{"c":3}}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h dualViewHost
			if err := Unmarshal([]byte(c.src), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			cc, ok := h.Case.(dualViewCase)
			if !ok {
				t.Fatalf("Case = %T, want dualViewCase", h.Case)
			}
			if cc.Name != c.wantName || cc.Age != c.wantAge {
				t.Errorf("case = %+v, want Name=%q Age=%d", cc, c.wantName, c.wantAge)
			}
			if got := h.Rest.String(); got != c.wantRest {
				t.Errorf("Rest = %s, want %s", got, c.wantRest)
			}
			// The count in view B's own root word, independent of view A's. Len
			// reads that field directly while ForEachKey follows seams, so the two
			// are separate derivations and a mismatch means one view's bookkeeping
			// leaked into the other. Counted by walking rather than by parsing the
			// expected string, which would have to know about nesting.
			var walked int
			h.Rest.ForEachKey(func(string, value.Value) bool { walked++; return true })
			if got := h.Rest.Len(); got != walked {
				t.Errorf("Rest.Len = %d but the walk visited %d (view B's count must be its own)", got, walked)
			}
		})
	}
}

// Len against an actual walk of the same view. The count lives in a field the
// producer stamps while the walk follows seams, so the two are independent
// derivations and a mismatch means one view's bookkeeping leaked into the other.
func TestDualView_SinkLenMatchesWalk(t *testing.T) {
	var h dualViewHost
	src := `{"u1":1,"kind":"c1","name":"bob","u2":2,"age":7,"u3":{"n":3}}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var walked int
	h.Rest.ForEachKey(func(string, value.Value) bool { walked++; return true })
	if got := h.Rest.Len(); got != walked {
		t.Errorf("Len = %d but the walk visited %d entries", got, walked)
	}
	if walked != 3 {
		t.Errorf("walk visited %d entries, want 3", walked)
	}
}

// The sink handed back to UnmarshalValue, which is the only path that carries a
// non-default view into the native walker. Until the view became an input the
// walker read the sink through view A and returned the case's fields, so this is
// the case that pins the plumbing rather than the encoding.
func TestDualView_SinkThroughUnmarshalValue(t *testing.T) {
	var h dualViewHost
	if err := Unmarshal([]byte(`{"kind":"c1","name":"bob","age":7,"u1":1,"u2":{"z":2}}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Declaring "name" and "age" as well is what makes a view mix-up legible: they
	// belong to view A, so binding either non-zero means the wrong view was read.
	var out struct {
		Name string      `json:"name"`
		Age  int         `json:"age"`
		U1   int         `json:"u1"`
		U2   value.Value `json:"u2"`
	}
	if err := UnmarshalValue(h.Rest, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.Name != "" || out.Age != 0 {
		t.Errorf("Name=%q Age=%d, want empty: both are view A's and this Value is view B", out.Name, out.Age)
	}
	if out.U1 != 1 {
		t.Errorf("U1 = %d, want 1", out.U1)
	}
	if got := out.U2.String(); got != `{"z":2}` {
		t.Errorf("U2 = %s, want {\"z\":2}", got)
	}
}

// A view switch mid-walk: the sink (view B) is bound into a type that ITSELF has an
// embedded variant plus a sink, so phase2 descends into a case while the enclosing
// walk reads view B. Verified to reach that state (the outer Value's mode is
// SeamViewB and the inner case does bind).
//
// The honest scope: this does NOT currently guard the rebind stack's saved mode.
// Deleting the restore leaves the suite green, because phase2's classification loop
// names TAPE_VIEW_A explicitly rather than reading the machine field, being a walk
// over the tape it is itself building. Measured, not assumed. The save is kept
// because the invariant is "switching the input saves the old one", which the next
// reader of m->tape_view_mode inside a descent would depend on; this case is what
// will exercise it then.

type dualViewInner struct {
	IKind string      `json:"ikind"`
	ICase any         `json:",embed" vjson:"variant=ikind"`
	IRest value.Value `json:",embed"`
}

type dualViewInnerCase struct {
	Label string `json:"label"`
}

func init() {
	vbind.DefineVariantCases[dualViewInner, struct {
		_ dualViewInnerCase `case:"d1"`
	}]()
}

func TestDualView_NestedDescentSwitchesView(t *testing.T) {
	// The outer parse puts ikind/label/deep into the sink, since the outer case
	// declares none of them.
	var h dualViewHost
	src := `{"kind":"c1","name":"bob","ikind":"d1","label":"L","deep":9}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := h.Rest.String(); got != `{"ikind":"d1","label":"L","deep":9}` {
		t.Fatalf("Rest = %s, want the three non-case keys", got)
	}

	// Binding that view-B Value into a type whose own phase2 descends into a case
	// read through view A.
	var inner dualViewInner
	if err := UnmarshalValue(h.Rest, &inner); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	ic, ok := inner.ICase.(dualViewInnerCase)
	if !ok {
		t.Fatalf("ICase = %T, want dualViewInnerCase", inner.ICase)
	}
	if ic.Label != "L" {
		t.Errorf("Label = %q, want L", ic.Label)
	}
	if got := inner.IRest.String(); got != `{"deep":9}` {
		t.Errorf("IRest = %s, want {\"deep\":9}", got)
	}
}

// The same shapes through the pooled path repeatedly. The arena cursor advances
// between parses, so a coordinate or a view that happened to be right on a cold
// arena stops being right here.
func TestDualView_Repeated(t *testing.T) {
	src := []byte(`{"u1":1,"kind":"c1","name":"bob","u2":2,"age":7}`)
	for i := range 16 {
		var h dualViewHost
		if err := Unmarshal(src, &h); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		cc, _ := h.Case.(dualViewCase)
		if cc.Name != "bob" || cc.Age != 7 {
			t.Fatalf("iter %d: case = %+v, want bob/7", i, cc)
		}
		if got := h.Rest.String(); got != `{"u1":1,"u2":2}` {
			t.Fatalf("iter %d: Rest = %s", i, got)
		}
	}
}
