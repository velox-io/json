package bind

import (
	"fmt"
	"strings"
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// phase2_finish is re-entrant: an inline variant whose case needs a value slot
// yields BLOCK_FULL and the driver resumes at BIND_PHASE_TAPE_BIND_CLOSE_DRAIN_RETRY,
// which re-enters at the label's top. Everything above the yield therefore runs
// twice and must be idempotent.
//
// The reserve-unknown publish sits above it. Closing tape B is a one-shot append
// (an END word plus a re-stamped count on the open word), so running it twice
// left the published Value naming one entry more than B holds, and the reader
// walked into whatever followed. The symptom was a phantom trailing key.
//
// Reaching the retry needs the case slot to be exhausted, which only happens
// once earlier parses on the same Parser have drawn the class down, so a single
// Unmarshal cannot show it. These tests reuse one Parser, which is also what
// package-level Unmarshal does through the pool.

type reentUser struct {
	Name string `json:"name"`
}

// Embedded variant + reserve-unknown: the pair that makes B a separate tape,
// which is what gets closed twice.
type reentHost struct {
	Type   string      `json:"type"`
	Data   any         `json:",embed" vjson:"variant=type"`
	Others value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[reentHost, struct{ user reentUser }]()
}

// assertNoPhantomKey walks the published Value and reports any key the input did
// not contain. A double close shows up as an extra entry the walk reads past the
// real end, which surfaces as an empty key rather than a read fault, so the
// assertion has to enumerate rather than just compare Len.
func assertNoPhantomKey(t *testing.T, tag string, v value.Value, want []string) {
	t.Helper()
	var got []string
	v.ForEachKey(func(k string, _ value.Value) bool {
		got = append(got, k)
		return true
	})
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: keys = %q, want %q (value = %s)", tag, got, want, v.String())
	}
}

// One Parser, many parses. The case slot runs dry partway through, and from
// that parse on every close takes the retry path.
func TestPhase2Finish_ReentryDoesNotRepublishSink(t *testing.T) {
	p, err := NewParser[reentHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	// Unknown values of assorted shapes: an empty container is the tightest case
	// (its close sits adjacent to its open, so one stray word is already past it),
	// and nesting one inside another checks the walk did not simply stop early.
	srcs := []string{
		`{"type":"user","name":"A","u1":{}}`,
		`{"type":"user","name":"A","u1":{},"u2":7}`,
		`{"type":"user","name":"A","u1":{"a":{}},"u2":7}`,
		`{"type":"user","name":"A","u1":{"a":[]},"u2":7}`,
		`{"type":"user","name":"A","u1":[[]],"u2":7}`,
		`{"type":"user","name":"A","u1":[{}],"u2":7}`,
		`{"type":"user","name":"A","u1":{"a":{"b":{}}},"u2":7}`,
		`{"type":"user","name":"A","u1":[],"u2":[],"u3":{}}`,
	}
	wants := [][]string{
		{"u1"},
		{"u1", "u2"},
		{"u1", "u2"},
		{"u1", "u2"},
		{"u1", "u2"},
		{"u1", "u2"},
		{"u1", "u2"},
		{"u1", "u2", "u3"},
	}
	// Several rounds so the exhaustion point is crossed regardless of the class's
	// initial capacity.
	for round := range 4 {
		for i, src := range srcs {
			var h reentHost
			if err := p.Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("round %d %s: %v", round, src, err)
			}
			if got, ok := h.Data.(reentUser); !ok || got.Name != "A" {
				t.Errorf("round %d %s: Data = %#v, want reentUser{Name:\"A\"}", round, src, h.Data)
			}
			assertNoPhantomKey(t, fmt.Sprintf("round %d %s", round, src), h.Others, wants[i])
		}
	}
}

// The same shape driven through package-level Unmarshal, which borrows from the
// shared pool. This is the configuration a caller actually hits.
func TestPhase2Finish_ReentryViaPool(t *testing.T) {
	for round := range 6 {
		for _, src := range []string{
			`{"type":"user","name":"A","u1":[[]],"u2":7}`,
			`{"type":"user","name":"A","u1":{},"u2":{}}`,
			`{"type":"user","name":"A","u1":{"deep":{"deeper":[]}},"u2":1}`,
		} {
			var h reentHost
			if err := Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("round %d %s: %v", round, src, err)
			}
			var n int
			h.Others.ForEachKey(func(k string, _ value.Value) bool {
				if k == "" {
					t.Errorf("round %d %s: phantom empty key in %s", round, src, h.Others.String())
				}
				n++
				return true
			})
			if n != 2 {
				t.Errorf("round %d %s: %d keys, want 2 (value = %s)", round, src, n, h.Others.String())
			}
		}
	}
}

// Len is derived from the count stamped on the open word, while ForEachKey walks
// the entries. A double close moved the two apart, so agreement between them is
// the assertion that pins the count rather than the walk.
func TestPhase2Finish_SinkLenMatchesWalk(t *testing.T) {
	p, err := NewParser[reentHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for round := range 4 {
		for _, src := range []string{
			`{"type":"user","name":"A","u1":[[]],"u2":7}`,
			`{"type":"user","name":"A","u1":{"a":[]},"u2":7,"u3":{}}`,
		} {
			var h reentHost
			if err := p.Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			var walked int
			h.Others.ForEachKey(func(string, value.Value) bool { walked++; return true })
			if got := h.Others.Len(); got != walked {
				t.Errorf("round %d %s: Len = %d but walk visited %d (value = %s)",
					round, src, got, walked, h.Others.String())
			}
		}
	}
}
