//go:build vj_enginediff

package bind

import (
	"testing"
)

// Failed-parse destinations, native versus the pure-Go engine. The engines
// agree on error identity (covered by the differential suites); this checks
// what a failing parse leaves behind, which the suites compare only on
// success. Documents without deferred records (hooks, raw spans) so the
// comparison does not depend on drain timing.

func ilEngineRun[T any](goCore bool, in string, opts []UnmarshalOption) ilOutcome {
	prev := forceGoCore
	forceGoCore = goCore
	defer func() { forceGoCore = prev }()
	p, _ := NewParser[T]()
	return ilRun[T](p, in, opts)
}

func ilEngineDiff[T any](t *testing.T, good []string) {
	t.Helper()
	var inputs []string
	for _, g := range good {
		inputs = append(inputs, ilTypeMutants(g)...)
		inputs = append(inputs, ilSweepMutants(g)...)
	}
	seen := map[string]bool{}
	fails := 0
	for i, in := range inputs {
		opts := ilOptSets[i%len(ilOptSets)]
		n := ilEngineRun[T](false, in, opts)
		g := ilEngineRun[T](true, in, opts)
		if ilSameStrict(n, g) {
			continue
		}
		key := n.err + "|" + g.err
		if n.err == g.err {
			key = "dst|" + n.err[:min(12, len(n.err))]
		}
		if seen[key] || fails >= 10 {
			continue
		}
		seen[key] = true
		fails++
		t.Errorf("native vs Go engine\n  in: %q (opts#%d)\n  native: err=%s\n  go:     err=%s\n  %s", in, i%len(ilOptSets), n.err, g.err, ilDiff(n, g))
	}
}

func ilSameStrict(a, b ilOutcome) bool {
	return a.err == b.err && a.json == b.json
}

func TestInterleaveEngineDiffFailedDestination(t *testing.T) {
	needNativeForDiff(t)
	ilEngineDiff[ilPlain](t, ilPlainGood)
}
