package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// The reserve-unknown is a second logical view (view B) over the merged tape's
// words, not a tape copied out of it. Its seams are not guaranteed to be adjacent
// on the arena: the same pass can descend into a poly case between two of them,
// and that descent appends to the shared arena, leaving a gap. So the view's seam
// must be able to leave a gap behind and thread across it.
//
// Unlike the merged tape, view B is never excised from (an entry is dropped from a
// view, its physical words untouched), so it reserves a seam only when a descent
// actually interrupts it rather than after every entry. That makes the
// interleaving below the case worth pinning: it is the only shape where the
// reservation is used, and the shapes around it are where a reservation that was
// made but never needed would show up as a stray word.

// Both halves are needed to exercise view B at all. The inline variant is what
// gives A a second consumer, and only then are the unknowns served by a second
// seam view over the same words instead of being served in place. The sibling variant is what
// interrupts that view: its discriminator arrives after the values, so phase1
// tapes it and phase2 descends into it midway through the walk.
type seamHost struct {
	Kind string      `json:"kind"`
	Poly any         `json:"poly" vjson:"variant=kind"`
	Type string      `json:"type"`
	Inl  any         `json:",embed" vjson:"variant=type"`
	Rest value.Value `json:",embed"`
}

// The case carries a sink of its own, so descending into it appends to the shared
// arena during the host's struct-close pass. That append is the gap: a case that
// only binds plain fields consumes no arena, and the entries would stay adjacent
// whether or not a seam was reserved. (A value.Value field would not do either,
// since tape-bind aliases the source tape instead of emitting.)
type seamCase struct {
	Deep string      `json:"deep"`
	Sink value.Value `json:",embed"`
}

// The inline case declares one key, so "inl" leaves A and everything else is
// leftover for the host's sink.
type seamInlineCase struct {
	Inl string `json:"inl"`
}

func init() {
	vbind.DefineVariantCasesAt[seamHost, struct {
		_ seamCase `case:"c"`
	}]("Poly")
	vbind.DefineVariantCasesAt[seamHost, struct {
		_ seamInlineCase `case:"i"`
	}]("Inl")
}

// The poly value precedes the discriminator, so phase1 cannot bind it and tapes
// it instead. phase2 then descends into it midway through collecting unknowns,
// putting an arena gap between the "a" entry and the "b" entry.
func TestReserveUnknownTape_DescentBetweenUnknowns(t *testing.T) {
	var h seamHost
	src := `{"a":1,"poly":{"deep":"d","cs":8},"b":2,"kind":"c","type":"i","inl":"iv"}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	assertSeamCase(t, h.Poly)
	assertRestKeys(t, h.Rest, map[string]int64{"a": 1, "b": 2})
}

// The descent lands after the last unknown, so its reservation is never consumed.
// The tape must still close cleanly, with no seam left dangling before the close.
func TestReserveUnknownTape_DescentAfterLastUnknown(t *testing.T) {
	var h seamHost
	src := `{"a":1,"b":2,"poly":{"deep":"d","cs":8},"kind":"c","type":"i","inl":"iv"}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	assertSeamCase(t, h.Poly)
	assertRestKeys(t, h.Rest, map[string]int64{"a": 1, "b": 2})
}

// The discriminator arrives first, so the poly field binds at its own field site
// and phase2 never descends. Nothing interrupts the entries, so the view is
// contiguous: this is the shape that pays no seam at all.
func TestReserveUnknownTape_NoDescent(t *testing.T) {
	var h seamHost
	src := `{"kind":"c","type":"i","inl":"iv","a":1,"poly":{"deep":"d","cs":8},"b":2}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	assertSeamCase(t, h.Poly)
	assertRestKeys(t, h.Rest, map[string]int64{"a": 1, "b": 2})
}

// assertSeamCase checks the descended case bound both its plain field and its
// own sink. The sink is the half that forced the descent to append to the arena.
func assertSeamCase(t *testing.T, poly any) {
	t.Helper()
	c, ok := poly.(seamCase)
	if !ok {
		t.Fatalf("Poly = %T, want seamCase", poly)
	}
	if c.Deep != "d" {
		t.Errorf("Poly.Deep = %q, want %q", c.Deep, "d")
	}
	assertRestKeys(t, c.Sink, map[string]int64{"cs": 8})
}

func assertRestKeys(t *testing.T, rest value.Value, want map[string]int64) {
	t.Helper()
	if rest.Type() != value.KindObject {
		t.Fatalf("Rest.Type = %v, want KindObject", rest.Type())
	}
	if rest.Len() != len(want) {
		t.Fatalf("Rest.Len = %d, want %d", rest.Len(), len(want))
	}
	for k, w := range want {
		v := rest.Get(k)
		got, ok := v.Int()
		if !ok || got != w {
			t.Errorf("Rest[%q] = %d (ok=%v), want %d", k, got, ok, w)
		}
	}
}
