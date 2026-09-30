package gbind

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/vbind"
)

// The memo is a pure cache, so outcome tests cannot tell a memo hit from a
// blob hit. This reaches under the hood: a mispredicting bind records its
// resolution in the struct's TypeMeta row, and the row must index the
// binder's memo in bounds, the layout the native Parser's memo shares.

type memoProbe struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestKeyMemoRowPlumbing(t *testing.T) {
	tt, err := vbind.Build(typ.UniTypeOf(reflect.TypeFor[memoProbe]()))
	if err != nil {
		t.Fatalf("vbind.Build: %v", err)
	}
	pl := NewPlan(tt)
	if pl.memoLen != tt.KeyMemoLen {
		t.Fatalf("plan memoLen %d, TypeTree KeyMemoLen %d", pl.memoLen, tt.KeyMemoLen)
	}
	var kt *keyTable
	for _, k := range pl.keys {
		if k != nil {
			kt = k
			break
		}
	}
	if kt == nil {
		t.Fatal("no key table in the plan")
	}
	row := int(kt.memoRow)
	if row == 0 {
		t.Fatal("struct has no memo row")
	}

	st := &State{}
	a := vbind.NewAllocator(tt)
	bind := func(doc string, dst *memoProbe) {
		t.Helper()
		if _, err := Bind(pl, nil, &Input{Src: []byte(doc), State: st, Alloc: a}, unsafe.Pointer(dst)); err != nil {
			t.Fatalf("Bind(%s): %v", doc, err)
		}
	}

	// The first member, b, resolves against prediction a, so word 0 takes
	// b. The cursor then sits on the sentinel, where the second member, a,
	// fails and records against it: word 1, the b prediction, never fails.
	var v memoProbe
	bind(`{"b":1,"a":2}`, &v)
	if v != (memoProbe{A: 2, B: 1}) {
		t.Fatalf("bound %+v", v)
	}
	if got := st.c.memo; len(got) != pl.memoLen {
		t.Fatalf("memo len %d, plan memoLen %d", len(got), pl.memoLen)
	} else if w := got[row : row+3]; w[0] != 2 || w[1] != 0 || w[2] != 1 {
		t.Fatalf("memo row %d holds %v, want (2, 0, 1): b against a, none against b, a against the sentinel", row, w)
	}

	// A second bind through the same State reads the memo the first wrote.
	bind(`{"b":9}`, &v)
	if v.B != 9 {
		t.Fatalf("bound %+v", v)
	}
}

// The one-past sentinel matches no key: its n is negative where every
// name's is not, so the empty key, which the sentinel's zero key would
// answer, reaches the blob's miss from every matcher.
func TestKeyTableSentinelUnmatchable(t *testing.T) {
	tt, err := vbind.Build(typ.UniTypeOf(reflect.TypeFor[memoProbe]()))
	if err != nil {
		t.Fatalf("vbind.Build: %v", err)
	}
	pl := NewPlan(tt)
	var kt *keyTable
	for _, k := range pl.keys {
		if k != nil {
			kt = k
			break
		}
	}
	last := len(kt.byIdx) - 1
	if got := kt.byIdx[last].n; got != -1 {
		t.Fatalf("sentinel n = %d, want -1", got)
	}

	c := &binder{memo: make([]byte, pl.memoLen)}
	for _, next := range []int{0, 1, last} {
		if idx := c.matchString(kt, "", next); idx != -1 {
			t.Errorf("matchString(%q, next %d) = %d, want -1", "", next, idx)
		}
		if idx := c.matchString(kt, "a", next); idx != 0 {
			t.Errorf("matchString(%q, next %d) = %d, want 0", "a", next, idx)
		}
	}
}
