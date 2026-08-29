package dom

import (
	"fmt"
	"strings"
	"testing"
)

// parseSized parses src through a fresh Parser with the requested string mode.
func parseSized(t *testing.T, src string, zc bool) (Value, *Parser) {
	t.Helper()
	p := NewParser()
	var v Value
	var err error
	if zc {
		v, err = p.ParsePadded(Pad([]byte(src)), WithZeroCopy())
	} else {
		v, err = p.Parse([]byte(src))
	}
	if err != nil {
		t.Fatalf("parse (zc=%v): %v", zc, err)
	}
	return v, p
}

// sizingCorpus mixes the shapes where the counted bound is tightest (bare
// scalars, dense integers, empty containers) with the shapes where it is
// loosest (long strings, escapes).
var sizingCorpus = []string{
	`1`,
	`true`,
	`null`,
	`-1.5e300`,
	`"s"`,
	`{}`,
	`[]`,
	`[1]`,
	`[1,2,3]`,
	`{"a":1}`,
	`{"a":1,"b":2,"c":3}`,
	`[1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1]`,
	`[true,false,null,true,false,null]`,
	`{"a":{"b":{"c":[1,2,3,{"d":null}]}}}`,
	`["aa","bb","cc","dd"]`,
	`[{},{},{},{},{},{},{},{}]`,
	`[[[[[1]]]]]`,
	`{"key":"a string long enough that tape words are far cheaper than source bytes"}`,
	`"a\nb\tc\\d\"e\f\/g"`,
	`18446744073709551615`,
	`{"nested":{"deep":{"x":9.9}}}`,
}

// TestSizingBoundCoversCorpus checks the counted bound against the tape the
// build actually wrote across the corpus in both string modes: the regression
// guard for an understated bound, which would let C write past the Go-owned
// arena.
func TestSizingBoundCoversCorpus(t *testing.T) {
	for _, src := range sizingCorpus {
		for _, zc := range []bool{false, true} {
			name := fmt.Sprintf("zc=%v/%s", zc, src)
			if len(name) > 64 {
				name = name[:64]
			}
			t.Run(name, func(t *testing.T) {
				_, p := parseSized(t, src, zc)
				if need, used := int(p.domCtx.TapeNeed), int(p.domCtx.TapeLen); need < used {
					t.Fatalf("counted bound %d understates the %d-word tape: C writes past a Go-owned arena",
						need, used)
				}
			})
		}
	}
}

// TestSizingBoundDominance stresses the counted bound on adversarial inputs at
// scale, where an off-by-one in the per-plane charging would surface.
func TestSizingBoundDominance(t *testing.T) {
	repeat := func(n int, s string) string { return strings.Repeat(s, n) }
	longRun := strings.Repeat("x", 1000)
	cases := []string{
		"[" + strings.TrimSuffix(repeat(1000, "1,"), ",") + "]",
		repeat(100, "[") + "1" + repeat(100, "]"),
		"[" + strings.TrimSuffix(repeat(500, "{},"), ",") + "]",
		"[" + strings.TrimSuffix(repeat(300, `"a\nb",`), ",") + "]",
		`{"key":"` + longRun + `"}`,
		`"` + longRun + `"`,
		`["` + longRun + `"]`,
	}
	for _, src := range cases {
		_, p := parseSized(t, src, false)
		need, used := int(p.domCtx.TapeNeed), int(p.domCtx.TapeLen)
		if need < used {
			t.Fatalf("counted bound %d understates the %d-word tape (srcLen=%d)", need, used, len(src))
		}
		t.Logf("srcLen=%d tapeLen=%d bound=%d ratio=%.3f", len(src), used, need, float64(used)/float64(len(src)))
	}
}

// TestSizingErrors checks that failures surface through the counted flow,
// including the ones the scan phase reports before any tape word is written.
// The invalid-UTF-8 case rides WithStrictScan: the lax default passes raw
// bytes inside strings through, so only the strict scan rejects it.
func TestSizingErrors(t *testing.T) {
	cases := []string{
		`{`,
		`[`,
		`{"a"}`,
		`"unclosed`,
		`tru`,
		`nul`,
		`[1,]`,
		`{"a":}`,
	}
	for _, src := range cases {
		t.Run(fmt.Sprintf("%q", src), func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Fatal("Parse accepted invalid input")
			}
		})
	}
	if _, err := Parse([]byte("[\"a\x80b\"]"), WithStrictScan()); err == nil {
		t.Fatal("Parse accepted invalid UTF-8 under WithStrictScan")
	}
}

// TestSizingArenaGrowthReuse parses repeatedly on one Parser until the tape
// arena must grow mid-stream, then verifies Values carved from the old backing
// still read correctly: the growth path installs a fresh backing while prior
// docs keep aliasing the old one.
func TestSizingArenaGrowthReuse(t *testing.T) {
	p := NewParser()
	src := `{"name":"` + strings.Repeat("alice", 40) + `","scores":[9.5,8.2,7.0]}`
	var held []Value
	for i := 0; i < 12; i++ {
		v, err := p.Parse([]byte(src))
		if err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
		held = append(held, v)
	}
	for i, v := range held {
		want := strings.Repeat("alice", 40)
		nv := v.Get("name")
		if got, ok := nv.Str(); !ok || got != want {
			t.Fatalf("value %d: name = %q (ok=%v), want %d chars", i, got, ok, len(want))
		}
		sv := v.Get("scores")
		if n := sv.Len(); n != 3 {
			t.Fatalf("value %d: scores len = %d, want 3", i, n)
		}
	}
}

// TestSizingLongStringWins pins the payoff case: on a string-heavy document
// the counted reservation is a fraction of the srcLen worst case.
func TestSizingLongStringWins(t *testing.T) {
	src := `{"key":"` + strings.Repeat("x", 4096) + `"}`
	_, p := parseSized(t, src, false)
	need, worst := int(p.domCtx.TapeNeed), len(src)+3
	if need >= worst {
		t.Fatalf("counted need %d should beat the srcLen worst case %d on a string-heavy document", need, worst)
	}
	t.Logf("srcLen+3=%d counted need=%d (%.1f%% of worst case)", worst, need, 100*float64(need)/float64(worst))
}
