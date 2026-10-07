package bind

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"testing"
)

// Retention: every destination a Parser has filled, successfully or not,
// belongs to the caller from the moment the call returns. Later calls on the
// same Parser carve from the same slot blocks and string arena, so a failing
// call that reclaims or re-exposes memory it should not (open-slice sealing,
// string arena commit on failure, retained backing links) corrupts results
// that are still in use. Each destination is snapshotted when its call
// returns and re-checked after every later call.

type ilRetNested struct {
	A  []int
	B  [][]string
	C  []ilInner
	D  map[string][]int
	E  []map[string]string
	F  [][]ilInner
	P  []*ilInner
	S  string
	SS []string
}

var ilRetGood = []string{
	`{"A":[1,2,3,4,5],"B":[["a","b"],["c"],[]],"C":[{"B":1,"C":"x","D":[1.5]},{"B":2}],"D":{"k":[1,2],"j":[3]},"E":[{"a":"b"},{}],"F":[[{"B":1}],[{"B":2},{"B":3}]],"P":[{"B":7},null],"S":"str","SS":["u","v","w"]}`,
	`{"A":[9,8,7],"B":[["zz"]],"C":[{"C":"only"}],"S":"second","SS":["s1"]}`,
	`{"F":[[{"B":1,"C":"deep","D":[1,2,3]}]],"A":[],"B":[[]],"D":{}}`,
}

type ilRetEntry struct {
	dst  *ilRetNested
	snap string
	src  string
}

func ilRetCheck(t *testing.T, kept []ilRetEntry, step string) bool {
	t.Helper()
	for i, e := range kept {
		js, _ := json.Marshal(e.dst)
		if string(js) != e.snap {
			t.Errorf("retained destination #%d (from input %q) changed after %s\n  was: %.300s\n  now: %.300s", i, e.src, step, e.snap, js)
			return false
		}
	}
	return true
}

func TestInterleaveRetainedResultsSurviveFailures(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var inputs []string
	for _, g := range ilRetGood {
		inputs = append(inputs, ilSweepMutants(g)...)
		inputs = append(inputs, ilTypeMutants(g)...)
	}
	for _, mode := range []string{"all-retained", "success-only"} {
		p, _ := NewParser[ilRetNested]()
		var kept []ilRetEntry
		for i, in := range inputs {
			dst := new(ilRetNested)
			err := p.Unmarshal([]byte(in), dst)
			if mode == "success-only" && err != nil {
				// Failed destinations are dropped, but the later calls still
				// run on the state this failure left behind.
			} else {
				js, _ := json.Marshal(dst)
				kept = append(kept, ilRetEntry{dst, string(js), in})
				if len(kept) > 24 {
					kept = kept[1:]
				}
			}
			// A good document after each bad one, retained.
			g := ilRetGood[i%len(ilRetGood)]
			gd := new(ilRetNested)
			if gErr := p.Unmarshal([]byte(g), gd); gErr != nil {
				t.Fatalf("good document failed after %q: %v", in, gErr)
			}
			js, _ := json.Marshal(gd)
			kept = append(kept, ilRetEntry{gd, string(js), g})
			if len(kept) > 24 {
				kept = kept[1:]
			}
			if i%16 == 0 {
				runtime.GC()
			}
			if !ilRetCheck(t, kept, fmt.Sprintf("mode=%s step %d (failing input %q err=%v)", mode, i, in, ilDescribeErr(err))) {
				return
			}
		}
	}
}
