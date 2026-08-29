package bind

import (
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
)

// A container's open word carries the index of its close, and that has to mean the
// same thing whether the container is empty or not. Nothing downstream can repair
// a disagreement: count == 0 is equally what a container holding no entries
// reports, so a reader cannot tell "empty" from "empty and encoded differently".
//
// It did disagree. Empty containers stored one past their close while non-empty
// ones stored the close, so the Go readers each carried a count == 0 branch to
// compensate and the two C readers, which had no such branch, skipped a word too
// far. Through UnmarshalValue that surfaced as a bogus syntax error: the walk
// stepped past the value's last word and read the next one as trailing data.
//
// These cases pin the uniformity from the outside. Each drives an empty container
// through a reader that skips over it rather than into it.

type emptySkipHost struct {
	A value.Value `json:"a"`
	Z int         `json:"z"`
}

// A Value field followed by another field: binding Z requires stepping over A,
// which is where the empty container's paired index is consumed.
func TestEmptyContainer_SkippedInTapeWalk(t *testing.T) {
	cases := []struct {
		src   string
		wantA string
	}{
		{`{"a":{},"z":7}`, `{}`},
		{`{"a":[],"z":7}`, `[]`},
		{`{"a":{"i":{}},"z":7}`, `{"i":{}}`},
		{`{"a":[[]],"z":7}`, `[[]]`},
		{`{"a":{"i":[]},"z":7}`, `{"i":[]}`},
		// An unknown key between them, so the walk skips two containers in a row.
		{`{"a":{},"b":{},"z":7}`, `{}`},
		{`{"a":[],"b":[],"z":7}`, `[]`},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			// UnmarshalValue runs the tape walk, whose skip is tape_value_end.
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var h emptySkipHost
			if err := UnmarshalValue(val, &h); err != nil {
				t.Fatalf("UnmarshalValue: %v", err)
			}
			if got := h.A.String(); got != c.wantA {
				t.Errorf("A = %s, want %s", got, c.wantA)
			}
			// Z is the assertion that matters: reaching it means the skip landed
			// exactly past A rather than inside or beyond it.
			if h.Z != 7 {
				t.Errorf("Z = %d, want 7 (the skip over A landed wrong)", h.Z)
			}

			// The JSON path must agree; it reaches the field directly rather than
			// by skipping, so this pins the two paths against each other.
			var h2 emptySkipHost
			if err := Unmarshal([]byte(c.src), &h2); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got := h2.A.String(); got != c.wantA {
				t.Errorf("Unmarshal: A = %s, want %s", got, c.wantA)
			}
			if h2.Z != 7 {
				t.Errorf("Unmarshal: Z = %d, want 7", h2.Z)
			}
		})
	}
}

// An empty container reached through the public Value API: ContainerEnd and Skip
// both read the paired index, and Len reads the count. All three must agree that
// the container is empty and ends where it does.
func TestEmptyContainer_ValueAPIAgrees(t *testing.T) {
	val, err := dom.Parse([]byte(`{"e":{},"f":[],"g":1}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	for _, name := range []string{"e", "f"} {
		v := val.Get(name)
		if !v.Valid() {
			t.Fatalf("Get(%q) invalid", name)
		}
		if got := v.Len(); got != 0 {
			t.Errorf("%s: Len = %d, want 0", name, got)
		}
		var n int
		v.ForEachKey(func(string, value.Value) bool { n++; return true })
		v.ForEachElem(func(int, value.Value) bool { n++; return true })
		if n != 0 {
			t.Errorf("%s: walk visited %d members, want 0", name, n)
		}
	}
	// The key after both empties is only reachable if each was stepped over by
	// exactly its own width.
	g := val.Get("g")
	if got, ok := g.Int(); !ok || got != 1 {
		t.Errorf("g = %d (ok=%v), want 1", got, ok)
	}
}
