//go:build vj_enginediff

package bind

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// Failed-parse destinations, native versus the pure-Go engine. The engines
// agree on error identity (covered by the differential suites); this checks
// what a failing parse leaves behind, which the suites compare only on
// success. A failed parse may leave a partial destination, so the
// destinations must agree where the walk completes the way encoding/json
// completes it: a type mismatch that encoding/json records and continues
// past, with one engine reproducing its destination.

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
		if n.err == g.err {
			std, ok := ilStdCompleted[T](in, opts)
			if !ok || (n.json != std && g.json != std) {
				continue
			}
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

// ilStdCompleted returns encoding/json's destination for a well-formed in
// that it rejects with a type mismatch, honoring the options it can mirror.
func ilStdCompleted[T any](in string, opts []UnmarshalOption) (string, bool) {
	if !json.Valid([]byte(in)) {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(in)))
	for i := range ilOptSets {
		if len(opts) == 0 || len(ilOptSets[i]) == 0 || &opts[0] != &ilOptSets[i][0] {
			continue
		}
		switch i {
		case 1:
			dec.UseNumber()
		case 2:
			dec.DisallowUnknownFields()
		}
	}
	v := new(T)
	var ute *json.UnmarshalTypeError
	if err := dec.Decode(v); !errors.As(err, &ute) {
		return "", false
	}
	js, _ := json.Marshal(v)
	return string(js), true
}

func ilSameStrict(a, b ilOutcome) bool {
	return a.err == b.err && a.json == b.json
}

func TestInterleaveEngineDiffFailedDestination(t *testing.T) {
	needNativeForDiff(t)
	ilEngineDiff[ilPlain](t, ilPlainGood)
}
