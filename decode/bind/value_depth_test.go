package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/value"
)

// TestValueFieldDepthBoundary pins the combined depth invariant
// bind_depth + vd_levels <= 255 that emerged once value_ctn_stack was merged
// into NdecBindCore.frames[]: the vd tape-emit walk reuses frames[depth+1..255]
// above the parent bind frame, so a root struct (bind_depth=1) with a
// value.Value field admits at most 254 nested containers inside the Value.
func TestValueFieldDepthBoundary(t *testing.T) {
	type T struct{ V value.Value }
	makeJSON := func(n int) []byte {
		return []byte(`{"V":` + strings.Repeat("[", n) + `1` + strings.Repeat("]", n) + `}`)
	}

	// 254 nested arrays: bind_depth=1 + vd_levels=254 = 255, fits.
	var ok T
	if err := Unmarshal(makeJSON(254), &ok); err != nil {
		t.Fatalf("depth 254 (at cap): unexpected error: %v", err)
	}

	// 255 nested arrays: vd_levels=255 would need frames[256], rejected.
	var bad T
	err := Unmarshal(makeJSON(255), &bad)
	if err == nil {
		t.Fatal("depth 255 (over cap): expected max-depth error, got nil")
	}
	if !strings.Contains(err.Error(), "max depth exceeded") {
		t.Errorf("depth 255: expected 'max depth exceeded' error, got: %v", err)
	}
}

// TestValueSliceRoot_ManyScalars_TapeArenaInvariants exercises the tape_arena
// carve path that the "skip pre-scan/reserve on vd_dispatch Value path" commit
// (b2e49477) touched. Each element of a []value.Value carves its own dom tape
// (content only, no TAPE_ROOT wrapper) inline from the shared tape_arena, with
// no per-Value bounds check; the arena is sized once at parse entry to
// 4*(srcLen+3) words.
//
// For [1,1,...,1] (N single-byte scalars) each Value tape is 2 words
// ('l' + int64), so cumulative tape_used = 2N. srcLen = 2N+1, so the per-parse
// floor cap is 4*(2N+4) = 8N+16. The cumulative (2N) stays under the cap
// (ratio 2/8 = 0.25) for every N.
//
// This test pins that invariant: parse increasingly large [1,...,1] arrays into
// []value.Value and verify every element matches dom.Parse of "1". A regression
// that lets cumulative tape_used exceed tape_arena_cap surfaces here as either a
// corrupted element String() (silent overrun into adjacent memory) or a crash
// once the write runs past the arena backing.
func TestValueSliceRoot_ManyScalars_TapeArenaInvariants(t *testing.T) {
	wantVal := mustParseDom(t, "1")
	wantStr := (&wantVal).String()
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192} {
		src := []byte("[" + strings.Repeat("1,", n-1) + "1]")
		var vs []value.Value
		if err := Unmarshal(src, &vs); err != nil {
			t.Fatalf("n=%d: Unmarshal: %v", n, err)
		}
		if len(vs) != n {
			t.Fatalf("n=%d: got %d values, want %d", n, len(vs), n)
		}
		for i, v := range vs {
			if got := (&v).String(); got != wantStr {
				t.Fatalf("n=%d: vs[%d].String()=%q want %q (tape_arena overrun corrupts adjacent Values)", n, i, got, wantStr)
			}
		}
	}
}

// TestValueSliceRoot_ManyScalars_ParserReuse runs the same scenario through a
// pooled Parser across many calls. The tape_arena cursor is amortized across
// parses (CommitTapeArena advances the slice; EnsureTapeArena regrows when the
// remaining cap drops below 4*(srcLen+3)). Forcing many regrowths surfaces any
// corruption that only manifests after the arena has been exhausted and regrown
// repeatedly.
func TestValueSliceRoot_ManyScalars_ParserReuse(t *testing.T) {
	p, err := NewParser[[]value.Value]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	const n = 1024
	src := []byte("[" + strings.Repeat("1,", n-1) + "1]")
	wantVal := mustParseDom(t, "1")
	wantStr := (&wantVal).String()
	const iters = 5000
	for iter := range iters {
		var vs []value.Value
		if err := p.Unmarshal(src, &vs); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if len(vs) != n {
			t.Fatalf("iter %d: got %d values, want %d", iter, len(vs), n)
		}
		for i, v := range vs {
			if got := (&v).String(); got != wantStr {
				t.Fatalf("iter %d: vs[%d].String()=%q want %q", iter, i, got, wantStr)
			}
		}
	}
}
