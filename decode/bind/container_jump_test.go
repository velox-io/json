package bind

import (
	"fmt"
	"strings"
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// End-to-end coverage for stepping over large and deeply nested unknown values on
// the merged-tape walk, which reaches them through tape_value_end.
//
// These do NOT guard the container jump's arithmetic, and that was measured, not
// assumed: adding one word to the jump in tape_value_end leaves every assertion
// here passing. The merged-tape walk re-synchronizes on the seam that follows each
// entry, so a small drift is absorbed before the next key is read. The encoding
// itself is pinned in value/paired_index_test.go, on hand-built tapes where the
// expected index does not come from the code under test.
//
// What these do cover is the shapes: an unknown value whose subtree is deep, or
// whose siblings are many, still lands in the sink intact and still leaves the
// following keys reachable.

type jmpUser struct {
	Name string `json:"name"`
}

type jmpHost struct {
	Type   string      `json:"type"`
	Data   any         `json:",embed" vjson:"variant=type"`
	Others value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[jmpHost, struct{ user jmpUser }]()
}

// nestedUnknown builds an unknown value nested `depth` levels deep. A jump that
// is off by any amount lands inside this subtree instead of past it, and the walk
// then reads a value word as a key.
func nestedUnknown(depth int) string {
	var sb strings.Builder
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&sb, `{"l%d":`, i)
	}
	sb.WriteString(`"leaf"`)
	sb.WriteString(strings.Repeat("}", depth))
	return sb.String()
}

// The keys AFTER a large unknown value are the assertion: reaching them at all
// means the walk stepped over the subtree by exactly the right distance. A jump
// that overshoots drops them, one that undershoots turns interior words into
// spurious keys.
func TestContainerJump_StepsExactlyPastSubtree(t *testing.T) {
	for _, depth := range []int{1, 2, 5, 20, 60} {
		src := fmt.Sprintf(`{"type":"user","name":"A","big":%s,"after1":1,"after2":2}`, nestedUnknown(depth))
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			var h jmpHost
			if err := Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got, ok := h.Data.(jmpUser); !ok || got.Name != "A" {
				t.Errorf("Data = %#v, want jmpUser{Name:\"A\"}", h.Data)
			}
			var keys []string
			h.Others.ForEachKey(func(k string, _ value.Value) bool {
				keys = append(keys, k)
				return true
			})
			want := []string{"big", "after1", "after2"}
			if strings.Join(keys, ",") != strings.Join(want, ",") {
				t.Fatalf("keys = %q, want %q\n  value = %s", keys, want, h.Others.String())
			}
			// The subtree itself must survive intact, not just be stepped over.
			big := h.Others.Get("big")
			if !big.Valid() {
				t.Fatal(`Get("big") invalid`)
			}
			probe := big
			for i := 0; i < depth; i++ {
				next := probe.Get(fmt.Sprintf("l%d", i))
				if !next.Valid() {
					t.Fatalf("level %d missing: %s", i, big.String())
				}
				probe = next
			}
			if s, _ := probe.Str(); s != "leaf" {
				t.Errorf("leaf = %q, want %q", s, "leaf")
			}
		})
	}
}

// Wide siblings rather than depth: each unknown value is a container the walk
// must step over, so the jump is exercised once per key. An off-by-one on any of
// them shifts every subsequent key.
func TestContainerJump_ManySiblingContainers(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"type":"user","name":"A"`)
	const n = 40
	for i := range n {
		// Alternate shapes so both the object and array arms are covered, and
		// include empties, whose close sits adjacent to their open.
		switch i % 4 {
		case 0:
			fmt.Fprintf(&sb, `,"k%d":{}`, i)
		case 1:
			fmt.Fprintf(&sb, `,"k%d":[]`, i)
		case 2:
			fmt.Fprintf(&sb, `,"k%d":{"a":[1,2,{"b":3}]}`, i)
		case 3:
			fmt.Fprintf(&sb, `,"k%d":[[[]],{"c":{}}]`, i)
		}
	}
	sb.WriteString(`}`)

	var h jmpHost
	if err := Unmarshal([]byte(sb.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var keys []string
	h.Others.ForEachKey(func(k string, _ value.Value) bool {
		keys = append(keys, k)
		return true
	})
	if len(keys) != n {
		t.Fatalf("%d keys, want %d\n  value = %s", len(keys), n, h.Others.String())
	}
	for i := range n {
		if keys[i] != fmt.Sprintf("k%d", i) {
			t.Fatalf("keys[%d] = %q, want %q (a jump landed wrong)\n  value = %s",
				i, keys[i], fmt.Sprintf("k%d", i), h.Others.String())
		}
	}
}

// BenchmarkContainerJump measures the cost of skipping an unknown value as its
// subtree grows. The defensive fallback in tape_value_end walks the subtree word
// by word where the jump does not, so a regression that invalidated the paired
// index turns the depth=200 row superlinear while depth=2 barely moves. Read it
// as a trend between the two rows, not as an absolute.
//
// Run: go test ./decode/bind/ -run XXX -bench ContainerJump -benchtime 15s
func BenchmarkContainerJump(b *testing.B) {
	for _, depth := range []int{2, 200} {
		src := []byte(fmt.Sprintf(`{"type":"user","name":"A","big":%s,"after":1}`, nestedUnknown(depth)))
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				var h jmpHost
				if err := Unmarshal(src, &h); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
