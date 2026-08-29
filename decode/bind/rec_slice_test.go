package bind

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// recSliceNode is a recursive slice type: []recSliceNode's element-backing
// slot self-loops (recSliceNode contains []recSliceNode), so it is a single-
// member self-loop SCC group routed to RecBatch (RecBatch). This fills the
// dispatch-matrix cell: array_begin/value/continue × recursive slice ×
// RecBatch, exercised across detach boundaries.
type recSliceNode struct {
	V int            `json:"v"`
	C []recSliceNode `json:"c"`
}

// TestRecursiveSliceAcrossDetach parses a recursive-slice document many times
// crossing the slotDetachK cadence, verifying the RecBatch RecBatch path stays
// correct after each detach (fresh RecBatchMatrix installed on Release).
func TestRecursiveSliceAcrossDetach(t *testing.T) {
	data := []byte(`{"v":0,"c":[{"v":1,"c":[{"v":2,"c":[]}]},{"v":3,"c":[]}]}`)

	// 30 iterations crosses slotDetachK=8 three times (detach at 8, 16, 24).
	for i := range 30 {
		var got recSliceNode
		if err := Unmarshal(data, &got); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		if got.V != 0 || len(got.C) != 2 {
			t.Fatalf("iter %d: root V=%d len(C)=%d", i, got.V, len(got.C))
		}
		if got.C[0].V != 1 || len(got.C[0].C) != 1 || got.C[0].C[0].V != 2 {
			t.Fatalf("iter %d: C[0] wrong: %+v", i, got.C[0])
		}
		if got.C[1].V != 3 || len(got.C[1].C) != 0 {
			t.Fatalf("iter %d: C[1] wrong: %+v", i, got.C[1])
		}
	}
}

// TestRecursiveSlicePreallocatedEmpty is a regression test for a panic when the
// destination is a pre-allocated empty recursive slice ([]T{}). The slice header
// has Data != nil but Cap == 0, which violates array_begin's invariant that
// Data != nil implies a valid Cap (set by SLICE_GROW resume or a pooled Parser).
// array_begin skips init, leaving Cap == 0, so the first element triggers grow
// with next_cap = cap * 2 = 0. RecBatch grows inline (no floor like the bump
// path's ServeSliceGrow), so recbatch_row_idx(__builtin_ctz(0)) returned 32,
// indexing past the 8-row matrix. The fix floors next_cap to 1.
//
// Both the root slice ([]recSliceNode{}) and a recursive field
// (recSliceNode{}.C = []recSliceNode{}) are covered. nil slices and non-
// recursive slices never hit the bug; they are included as guards.
func TestRecursiveSlicePreallocatedEmpty(t *testing.T) {
	// Root []recSliceNode{}: pre-allocated empty, exercises the grow path on
	// the first element with Data != nil and Cap == 0.
	t.Run("root-prealloc-empty", func(t *testing.T) {
		got := []recSliceNode{}
		if err := Unmarshal([]byte(`[{"v":1,"c":[{"v":2,"c":[]}]}]`), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		var want []recSliceNode
		if err := json.Unmarshal([]byte(`[{"v":1,"c":[{"v":2,"c":[]}]}]`), &want); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mismatch\n  got  =%+v\n  want =%+v", got, want)
		}
	})

	// Field C []recSliceNode: pre-allocated empty slice in a struct field,
	// same Data != nil / Cap == 0 condition reached via object field dispatch.
	t.Run("field-prealloc-empty", func(t *testing.T) {
		got := recSliceNode{C: []recSliceNode{}}
		if err := Unmarshal([]byte(`{"v":1,"c":[{"v":2,"c":[]}]}`), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		var want recSliceNode
		if err := json.Unmarshal([]byte(`{"v":1,"c":[{"v":2,"c":[]}]}`), &want); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mismatch\n  got  =%+v\n  want =%+v", got, want)
		}
	})

	// Root []any{}: pre-allocated empty any slice. []any is a RecBatch slot
	// (element backing self-loops via the universal any type). Same Cap == 0
	// grow condition. Tape-bind rejects root any at entry, so compare against
	// encoding/json directly.
	t.Run("root-any-prealloc-empty", func(t *testing.T) {
		got := []any{}
		if err := Unmarshal([]byte(`["str", 1, true, null, {"k":"v"}, [1,2]]`), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		var want []any
		if err := json.Unmarshal([]byte(`["str", 1, true, null, {"k":"v"}, [1,2]]`), &want); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if !anyEqual(got, want) {
			t.Errorf("mismatch\n  got  =%+v\n  want =%+v", got, want)
		}
	})

	// Guards: nil and non-recursive empty slices were never affected; include
	// them so a future regression in either direction is caught.
	t.Run("root-nil", func(t *testing.T) {
		var got []recSliceNode
		if err := Unmarshal([]byte(`[{"v":1,"c":[]}]`), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(got) != 1 || got[0].V != 1 || len(got[0].C) != 0 {
			t.Errorf("got = %+v", got)
		}
	})
	t.Run("non-recursive-prealloc-empty", func(t *testing.T) {
		got := []int{}
		if err := Unmarshal([]byte(`[1,2,3]`), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, []int{1, 2, 3}) {
			t.Errorf("got = %+v", got)
		}
	})
}

// recursiveMapNode is a recursive map type: map[string]*recursiveMapNode's
// value pointee slot self-loops, so the *recursiveMapNode pointee slot is a
// single-member self-loop SCC group routed to RecBump. Fills the dispatch-
// matrix cell: map_open/drain × recursive map value × RecBump, across detach.
type recursiveMapNode struct {
	V int                          `json:"v"`
	M map[string]*recursiveMapNode `json:"m"`
}

// TestRecursiveMapAcrossDetach parses a recursive-map document many times
// crossing the detach cadence, verifying the RecBump map path stays correct
// after each detach (bump Block=nil forces BLOCK_FULL + InitMapSlots).
func TestRecursiveMapAcrossDetach(t *testing.T) {
	// {"v":0,"m":{"a":{"v":1,"m":{"b":{"v":2,"m":{}}}},"c":{"v":3,"m":{}}}}
	var b strings.Builder
	b.WriteString(`{"v":0,"m":{"a":{"v":1,"m":{"b":{"v":2,"m":{}}}},"c":{"v":3,"m":{}}}}`)
	data := []byte(b.String())

	verify := func(t *testing.T, n *recursiveMapNode, v int, childKeys map[int]string) {
		t.Helper()
		if n == nil {
			t.Fatalf("V=%d: nil node", v)
		}
		if n.V != v {
			t.Fatalf("V=%d want %d", n.V, v)
		}
		if len(n.M) != len(childKeys) {
			t.Fatalf("V=%d: len(M)=%d want %d", v, len(n.M), len(childKeys))
		}
		for cv, key := range childKeys {
			c := n.M[key]
			if c == nil {
				t.Fatalf("V=%d: child %q nil", v, key)
			}
			if c.V != cv {
				t.Fatalf("V=%d: child %q V=%d want %d", v, key, c.V, cv)
			}
		}
	}

	for i := range 30 {
		var got recursiveMapNode
		if err := Unmarshal(data, &got); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		verify(t, &got, 0, map[int]string{1: "a", 3: "c"})
		verify(t, got.M["a"], 1, map[int]string{2: "b"})
		verify(t, got.M["c"], 3, map[int]string{})
		if got.M["a"].M["b"].V != 2 || len(got.M["a"].M["b"].M) != 0 {
			t.Fatalf("iter %d: nested b wrong: %+v", i, got.M["a"].M["b"])
		}
	}
}
