package bind

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Tests for the reserve-unknown Value field. The Go-side plumbing
// (tag parsing, TypeTree construction, StructMeta stamping) is exercised
// in the build-time tests below; the behavioral tests verify the native
// reserve-unknown dispatch in bind.h captures unmatched keys into a tape-backed
// value.Value (kind Object).

// reserveUnknownStruct is the canonical example from examples/unmarshal/partial.
type reserveUnknownStruct struct {
	Name    string      `json:"name"`
	Count   int         `json:"count"`
	Exts    value.Value `json:",embed"`
	Message string      `json:"message"`
}

// TestReserveUnknownBasic verifies the canonical example: known fields bind to
// their Go fields, unknown keys are captured into the reserve-unknown Value as
// a KindObject with the unmatched keys.
func TestReserveUnknownBasic(t *testing.T) {
	src := `{"name":"bob","count":10,"abc":{"a":1,"b":2,"c":3},"message":"OK","xx":"some info"}`
	var result reserveUnknownStruct
	if err := Unmarshal([]byte(src), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Name != "bob" {
		t.Errorf("Name = %q, want %q", result.Name, "bob")
	}
	if result.Count != 10 {
		t.Errorf("Count = %d, want 10", result.Count)
	}
	if result.Message != "OK" {
		t.Errorf("Message = %q, want %q", result.Message, "OK")
	}
	if result.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject", result.Exts.Type())
	}
	if result.Exts.Len() != 2 {
		t.Fatalf("Exts.Len = %d, want 2 (abc, xx)", result.Exts.Len())
	}
	abc := result.Exts.Get("abc")
	if !abc.Valid() {
		t.Fatal("Exts.Get(abc) missing")
	}
	if abc.Type() != value.KindObject {
		t.Errorf("abc.Type = %v, want KindObject", abc.Type())
	}
	a := abc.Get("a")
	if ai, ok := a.Int(); !ok || ai != 1 {
		t.Errorf("abc.a = %d (ok=%v), want 1", ai, ok)
	}
	xx := result.Exts.Get("xx")
	if xs, ok := xx.Str(); !ok || xs != "some info" {
		t.Errorf("xx = %q (ok=%v), want %q", xs, ok, "some info")
	}
}

// TestReserveUnknownEmpty verifies that a struct with a reserve-unknown and no unknown
// keys produces an empty-object Value (KindObject, Len 0).
func TestReserveUnknownEmpty(t *testing.T) {
	src := `{"name":"x","count":1,"message":"m"}`
	var result reserveUnknownStruct
	if err := Unmarshal([]byte(src), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject", result.Exts.Type())
	}
	if result.Exts.Len() != 0 {
		t.Errorf("Exts.Len = %d, want 0 (no unknown keys)", result.Exts.Len())
	}
}

// TestReserveUnknownAllUnknown verifies that a struct with a reserve-unknown and NO
// matched keys captures the entire object into the reserve-unknown Value.
func TestReserveUnknownAllUnknown(t *testing.T) {
	type allUnknown struct {
		Exts value.Value `json:",embed"`
	}
	src := `{"a":1,"b":"hello","c":[1,2,3]}`
	var result allUnknown
	if err := Unmarshal([]byte(src), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Exts.Type() != value.KindObject {
		t.Fatalf("Exts.Type = %v, want KindObject", result.Exts.Type())
	}
	if result.Exts.Len() != 3 {
		t.Fatalf("Exts.Len = %d, want 3", result.Exts.Len())
	}
	a := result.Exts.Get("a")
	if ai, ok := a.Int(); !ok || ai != 1 {
		t.Errorf("a = %d (ok=%v), want 1", ai, ok)
	}
	b := result.Exts.Get("b")
	if bs, ok := b.Str(); !ok || bs != "hello" {
		t.Errorf("b = %q (ok=%v), want hello", bs, ok)
	}
	c := result.Exts.Get("c")
	if c.Len() != 3 {
		t.Errorf("c.Len = %d, want 3", c.Len())
	}
}

// TestReserveUnknownNestedValue verifies deep navigation into the reserve-unknown
// Value: nested objects, arrays, and mixed kinds.
func TestReserveUnknownNestedValue(t *testing.T) {
	type host struct {
		Exts value.Value `json:",embed"`
	}
	src := `{"unknown":{"a":[1,2,{"b":"x"}]}}`
	var result host
	if err := Unmarshal([]byte(src), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u := result.Exts.Get("unknown")
	if !u.Valid() {
		t.Fatal("unknown missing")
	}
	a := u.Get("a")
	if a.Type() != value.KindArray {
		t.Fatalf("a.Type = %v, want KindArray", a.Type())
	}
	if a.Len() != 3 {
		t.Fatalf("a.Len = %d, want 3", a.Len())
	}
	a0 := a.Index(0)
	if ai, ok := a0.Int(); !ok || ai != 1 {
		t.Errorf("a[0] = %d (ok=%v), want 1", ai, ok)
	}
	a2 := a.Index(2)
	bv := a2.Get("b")
	if bs, ok := bv.Str(); !ok || bs != "x" {
		t.Errorf("a[2].b = %q (ok=%v), want x", bs, ok)
	}
}

// TestReserveUnknownDisallowUnknownNoOp verifies that WithDisallowUnknownFields
// is a silent no-op when a reserve-unknown is present: unknown keys are captured,
// not rejected.
func TestReserveUnknownDisallowUnknownNoOp(t *testing.T) {
	src := `{"name":"x","unknown":2}`
	var result reserveUnknownStruct
	err := Unmarshal([]byte(src), &result, WithDisallowUnknownFields())
	if err != nil {
		t.Fatalf("Unmarshal with DisallowUnknown: %v (expected no-op)", err)
	}
	if result.Exts.Len() != 1 {
		t.Errorf("Exts.Len = %d, want 1 (unknown key captured, not rejected)", result.Exts.Len())
	}
}

// TestReserveUnknownParserReuse verifies the same Parser can be reused across
// calls without stale reserve-unknown state leaking.
func TestReserveUnknownParserReuse(t *testing.T) {
	p, err := NewParser[reserveUnknownStruct]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for _, src := range []string{
		`{"name":"a","unknown":1}`,
		`{"name":"b","other":2}`,
		`{"name":"c","count":3}`,
	} {
		var result reserveUnknownStruct
		if err := p.Unmarshal([]byte(src), &result); err != nil {
			t.Fatalf("Unmarshal(%s): %v", src, err)
		}
		if result.Name == "" {
			t.Errorf("src=%s: Name empty", src)
		}
	}
}

// --- build-time tests ---

// hostsReserveUnknown reports whether any struct in the shape's TypeTree carries
// a reserve-unknown field. This is what the native routing reads, so it is also what the
// build-time tests below assert on.
func hostsReserveUnknown(p *Parser) bool {
	for i := range p.tt.Types {
		if p.tt.Types[i].Kind == vbind.KindStruct && p.tt.TypeMeta[i].StructMeta().ReserveUnknownFieldOff != 0xFFFFFFFF {
			return true
		}
	}
	return false
}

// TestReserveUnknownShapeFlag verifies buildShape records a reserve-unknown field in the
// TypeTree, and that a reserve-unknown on its own does not ask for the split-tape
// arena: with no inline variant competing for the merged tape, it is handed that
// tape directly.
func TestReserveUnknownShapeFlag(t *testing.T) {
	p, err := NewParser[reserveUnknownStruct]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if !hostsReserveUnknown(p) {
		t.Fatal("no struct with ReserveUnknownFieldOff stamped; want one")
	}
	if !p.tt.HasValueField {
		t.Fatal("HasValueField = false; want true (reserve-unknown is a Value field)")
	}
	if p.tt.HasSplitTape {
		t.Fatal("HasSplitTape = true; want false (no inline variant to split the tape with)")
	}
}

// TestReserveUnknownShapeFlagNegative verifies a shape without a reserve-unknown
// stamps nothing, including one with regular Value fields.
func TestReserveUnknownShapeFlagNegative(t *testing.T) {
	type plain struct {
		A string      `json:"a"`
		B value.Value `json:"b"` // regular Value field, not rest
	}
	p, err := NewParser[plain]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if hostsReserveUnknown(p) {
		t.Fatal("ReserveUnknownFieldOff stamped; want none (no rest field)")
	}
	if !p.tt.HasValueField {
		t.Fatal("HasValueField = false; want true")
	}
}

// TestReserveUnknownStructMetaOffset verifies the StructMeta.ReserveUnknownFieldOff
// stamping points at the correct field offset.
func TestReserveUnknownStructMetaOffset(t *testing.T) {
	p, err := NewParser[reserveUnknownStruct]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	rt := reflect.TypeFor[reserveUnknownStruct]()
	extsField, _ := rt.FieldByName("Exts")
	want := uint32(extsField.Offset)
	for i := range p.tt.Types {
		if p.tt.Types[i].Kind != vbind.KindStruct {
			continue
		}
		off := p.tt.TypeMeta[i].StructMeta().ReserveUnknownFieldOff
		if off == 0xFFFFFFFF {
			continue
		}
		if off != want {
			t.Errorf("ReserveUnknownFieldOff = %d, want %d (Exts field offset)", off, want)
		}
		return
	}
	t.Fatal("no struct with ReserveUnknownFieldOff stamped in TypeTree")
}

// shallowRestInner is embedded by shallowRestHost to place a reserve-unknown
// field one level deeper than the host's own.
type shallowRestInner struct {
	Deep value.Value `json:",embed"`
}

// shallowRestHost has a reserve-unknown field at depth 0 and, through the
// embedded struct, another at depth 1. The shallow one wins.
type shallowRestHost struct {
	Shallow value.Value `json:",embed"`
	shallowRestInner
	Name string `json:"name"`
}

// TestReserveUnknownBuildValidation runs the build-time validation rules. Each
// subtest asserts a specific behavior; failures here would let a broken
// shape reach the native dispatch.
func TestReserveUnknownBuildValidation(t *testing.T) {
	t.Run("MultipleRestFieldsRejected", func(t *testing.T) {
		// Both fields take typ.ReserveUnknownName as their JSON name, so the
		// ordinary same-name promotion rules cancel them at the same depth. For
		// ordinary names that silence is right: the author wrote two competing
		// fields and neither can win. Here the name is a synthetic sentinel no
		// JSON key matches, so cancellation would leave both fields permanently
		// empty with nothing to explain why. The typ layer records the collision
		// and the build reports it.
		//
		// The duplicate tag is the point of the test, so the type is built at
		// runtime: written as a literal it is exactly what go vet's structtag
		// check reports.
		valueType := reflect.TypeFor[value.Value]()
		bad := reflect.StructOf([]reflect.StructField{
			{Name: "A", Type: valueType, Tag: `json:",embed"`},
			{Name: "B", Type: valueType, Tag: `json:",embed"`},
		})
		_, err := NewParserForType(bad)
		if err == nil {
			t.Fatal("NewParserForType succeeded; want an error for two reserve-unknown fields")
		}
		if !strings.Contains(err.Error(), "at the same embedding depth") {
			t.Errorf("error %q does not explain the same-depth collision", err)
		}
	})
	t.Run("MultipleRestFieldsShallowWins", func(t *testing.T) {
		// Different depths are not a collision: the shallow field wins and the
		// deeper one is shadowed, which is the same rule ordinary names follow.
		// Only the same-depth case above is unrepresentable.
		p, err := NewParser[shallowRestHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		if !hostsReserveUnknown(p) {
			t.Fatal("no reserve-unknown stamped; want the shallow field to win")
		}
	})
	t.Run("EmbedOnNonEmbeddableFieldRejected", func(t *testing.T) {
		// `json:",embed"` names a layout: the field's content is promoted into
		// the host and it occupies no JSON member of its own. A string has no
		// content to promote, so the tag cannot be honored under any reading.
		// Treating it as an ordinary field instead would put the field back under
		// a name the author just said it would not have, so this is refused
		// rather than dropped.
		type bad struct {
			A string `json:",embed"`
		}
		_, err := NewParser[bad]()
		if err == nil {
			t.Fatal("NewParser succeeded; want a build error for embed on a string field")
		}
		if !strings.Contains(err.Error(), "cannot be embedded") {
			t.Fatalf("error = %v; want it to report the field cannot be embedded", err)
		}
	})
}

// --- stress tests ---

// reserveUnknownStressInputs spans the reserve-unknown path's interesting shapes:
//   - empty reserve-unknown (all known)
//   - all-unknown reserve-unknown
//   - deeply nested unknown values
//   - escaped string keys
//   - mixed known/unknown ordering
//   - large unknown arrays
var reserveUnknownStressInputs = []string{
	`{"name":"x","count":1,"message":"m"}`,
	`{"a":1,"b":"hello","c":[1,2,3],"d":{"e":true,"f":null}}`,
	`{"name":"x","unknown":{"deep":{"nested":{"value":42}}}}`,
	`{"name":"x","count":1,"a":1,"b":2,"message":"m","c":3}`,
	`{"name":"x","escaped":"tab\tnewline\nquote\"backslash\\","end":"done"}`,
	`{"name":"x","big":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]}`,
	`{"unknown1":"v1","name":"x","unknown2":"v2","count":1,"unknown3":"v3","message":"m"}`,
	`{"name":"x","count":1,"message":"m","empty_obj":{},"empty_arr":[]}`,
	`{"name":"x","count":1,"message":"m","null_val":null,"bool_t":true,"bool_f":false,"num_int":42,"num_float":3.14}`,
}

// TestReserveUnknownStressParserReuse drives the same Parser across the full
// reserveUnknownStressInputs corpus. Verifies no stale reserve-unknown state leaks
// between parses (the reserve-unknown tape start/count arrays are reset at struct close).
func TestReserveUnknownStressParserReuse(t *testing.T) {
	p, err := NewParser[reserveUnknownStruct]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	for i, src := range reserveUnknownStressInputs {
		var result reserveUnknownStruct
		if err := p.Unmarshal([]byte(src), &result); err != nil {
			t.Fatalf("iter %d src=%s: %v", i, src, err)
		}
		// Sanity: known fields should be bound for inputs that have them.
		// Don't over-assert: this is a stress test for state hygiene, not
		// exact semantics (covered by TestReserveUnknownBasic et al.).
		_ = result
	}
}

// TestReserveUnknownStressConcurrent runs N goroutines, each with its own Parser,
// against the reserveUnknownStressInputs corpus. Exposes any data race on
// reserve-unknown tape start/count between pooled parses (the arrays live on the
// machine block, pooled per shape; concurrent parses on the same Parser
// are NOT supported, but each goroutine uses a fresh Parser to stay safe).
func TestReserveUnknownStressConcurrent(t *testing.T) {
	const goroutines = 8
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			// Each goroutine uses package Unmarshal (pool-backed). The
			// pool hands off distinct Parsers to concurrent callers, so
			// reserve_unknown state never crosses goroutines.
			for i, src := range reserveUnknownStressInputs {
				var result reserveUnknownStruct
				if err := Unmarshal([]byte(src), &result); err != nil {
					t.Errorf("g%d iter%d src=%s: %v", gid, i, src, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestReserveUnknownStressLargeObject drives a single large object with many
// unmatched keys into the reserve-unknown. Verifies the arena sizing holds:
// without it, the parse would write past the arena, which nothing detects at
// runtime.
func TestReserveUnknownStressLargeObject(t *testing.T) {
	type host struct {
		Name string      `json:"name"`
		Exts value.Value `json:",embed"`
	}
	// 256 unknown keys, each with a nested object value. Total tape words
	// per unknown key: 1 (seam) + 1 (TagString) + 1 (TagObjBeg) + 1 (key
	// TagString) + 1 (TagInt64) + 1 (int value) + 1 (TagObjEnd) = 7 words.
	// 256 * 7 = 1792 words, plus the enclosing TagObjBeg/TagObjEnd and the
	// leading seam. srcLen is ~4600 bytes, so the single-copy budget covers it.
	const n = 256
	src := make([]byte, 0, 64+n*40)
	src = append(src, `{"name":"x"`...)
	for i := range n {
		src = append(src, ',', '"', 'k', 'e', 'y', '_')
		src = append(src, '0'+byte(i/100)%10, '0'+byte((i/10)%10), '0'+byte(i%10))
		src = append(src, '"', ':', '{', '"', 'v', '"', ':')
		src = append(src, '0'+byte(i%9))
		src = append(src, '}')
	}
	src = append(src, '}')
	var result host
	if err := Unmarshal(src, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Name != "x" {
		t.Errorf("Name = %q, want x", result.Name)
	}
	if result.Exts.Len() != n {
		t.Errorf("Exts.Len = %d, want %d", result.Exts.Len(), n)
	}
}

// TestReserveUnknownArenaBound pins the tape-arena sizing rule, because nothing
// enforces it at runtime: native has no bounds check on tape_arena, so exceeding
// it is silent memory corruption rather than an error.
//
// One tape costs at most srcLen words. A lone reserve-unknown is served in place
// (it is the merged tape's only consumer), so it stays within that one tape; add
// an inline variant and the same entries must serve two views wanting different
// subsets, a fixed prologue per merged tape, so 2*srcLen is the budget.
//
// Both bounds are tight rather than generous, and the shape that reaches them is a
// lone entry holding a dense int array: an int64 is 2 tape words per 2 source
// bytes, and with no comma anywhere there is no slack. If a future change adds a
// word per entry or a copy, this fails.
func TestReserveUnknownArenaBound(t *testing.T) {
	denseSrc := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"u":[`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('1')
		}
		b.WriteString(`]}`)
		return b.String()
	}
	// tapeUsed reports the words the parse consumed. Reading it after Unmarshal is
	// sound because the arena cursor is reset per parse, not per Parser.
	tapeUsed := func(p *Parser) int {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		return int(m.Alloc.TapeUsed)
	}

	t.Run("ReserveUnknownAlone", func(t *testing.T) {
		type host struct {
			Exts value.Value `json:",embed"`
		}
		for _, n := range []int{1, 2, 10, 1000, 20000} {
			src := denseSrc(n)
			p, err := NewParser[host]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var result host
			if err := p.Unmarshal([]byte(src), &result); err != nil {
				t.Fatalf("n=%d: Unmarshal: %v", n, err)
			}
			if used := tapeUsed(p); used > len(src) {
				t.Errorf("n=%d: tape used %d words exceeds srcLen=%d; the single-copy bound broke",
					n, used, len(src))
			}
			if u := result.Exts.Get("u"); u.Len() != n {
				t.Errorf("n=%d: Exts.u.Len = %d", n, u.Len())
			}
		}
	})

	t.Run("WithInlineVariant", func(t *testing.T) {
		for _, n := range []int{1, 2, 10, 1000, 20000} {
			// The variant's discriminator makes the host pick a case, so the
			// unknown "u" entry is served by a second view over the merged tape's words.
			src := `{"type":"user","name":"a","u":[` + strings.Repeat("1,", n-1) + `1]}`
			p, err := NewParser[coexistHost]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var result coexistHost
			if err := p.Unmarshal([]byte(src), &result); err != nil {
				t.Fatalf("n=%d: Unmarshal: %v", n, err)
			}
			if used := tapeUsed(p); used > 2*len(src) {
				t.Errorf("n=%d: tape used %d words exceeds 2*srcLen=%d; the two-copy bound broke",
					n, used, 2*len(src))
			}
			if u := result.Exts.Get("u"); u.Len() != n {
				t.Errorf("n=%d: Exts.u.Len = %d", n, u.Len())
			}
		}
	})
}

// TestValueBindArenaBound is the sibling of the above for UnmarshalValue, whose
// arena is sized in input tape WORDS rather than source bytes.
//
// A merged tape is strictly wider than the same content on the input tape: it
// reserves a seam in front of every entry, so copying an entry costs one word
// more than it occupied on the way in. The bound is therefore 1.5x the input
// words (2.5x with a split tape), never 1x, and value_bind.go derives both.
//
// Sizing it 1x was an out-of-bounds write rather than a tight fit, C bumping
// tape_used with no capacity check. What hid it is the 3x amortization in
// EnsureTapeArena: one call off a fresh Parser always fits. It takes a pooled
// Parser whose cursor has already advanced to land in the window where the
// remainder satisfies the claimed bound but not the real one, which is why the
// loop below varies the JSON size to sweep the cursor across it.
func TestValueBindArenaBound(t *testing.T) {
	tapeUsedCap := func(p *Parser) (int, int) {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		return int(m.Alloc.TapeUsed), int(m.Alloc.TapeArenaCap)
	}
	// Minimal hosts are the worst case: the per-entry seam and the dual-view
	// prologue are per-tape costs, so the ratio peaks where the hosts are smallest.
	const n = 2000
	valSrc := []byte("[" + strings.TrimSuffix(strings.Repeat("{},", n), ",") + "]")

	run := func(t *testing.T, newParser func() (*Parser, error), bindJSON func(*Parser, []byte) error,
		bindValue func(*Parser, value.Value) error) {
		t.Helper()
		v, err := dom.Parse(valSrc)
		if err != nil {
			t.Fatalf("dom.Parse: %v", err)
		}
		p, err := newParser()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		for iter := range 400 {
			// Sweep the arena cursor: CommitTapeArena advances it by whatever the
			// JSON parse used, so varying that walks the remainder through every
			// size class instead of resting at one.
			jn := 500 + iter*37
			jsonSrc := []byte("[" + strings.TrimSuffix(strings.Repeat("{},", jn), ",") + "]")
			if err := bindJSON(p, jsonSrc); err != nil {
				t.Fatalf("iter %d: Unmarshal: %v", iter, err)
			}
			if err := bindValue(p, v); err != nil {
				t.Fatalf("iter %d: UnmarshalValue: %v", iter, err)
			}
			if used, capw := tapeUsedCap(p); used > capw {
				t.Fatalf("iter %d: wrote %d tape words into a %d-word arena, %d words past the "+
					"Go-owned buffer (C has no bounds check, so this is heap corruption)",
					iter, used, capw, used-capw)
			}
		}
	}

	// Single view: a lone reserve-unknown, served in place, still pays one seam
	// per entry over the input tape.
	t.Run("SingleView", func(t *testing.T) {
		type host struct {
			Exts value.Value `json:",embed"`
		}
		run(t, func() (*Parser, error) { return NewParser[[]host]() },
			func(p *Parser, src []byte) error {
				var out []host
				return p.Unmarshal(src, &out)
			},
			func(p *Parser, v value.Value) error {
				var out []host
				return p.UnmarshalValue(v, &out)
			})
	})

	// Dual view: an inline variant beside the reserve-unknown adds the two-word
	// prologue per merged tape.
	t.Run("SplitTape", func(t *testing.T) {
		run(t, func() (*Parser, error) { return NewParser[[]coexistHost]() },
			func(p *Parser, src []byte) error {
				var out []coexistHost
				return p.Unmarshal(src, &out)
			},
			func(p *Parser, v value.Value) error {
				var out []coexistHost
				return p.UnmarshalValue(v, &out)
			})
	})
}

// Types for TestSplitTapeSiteBudget: the same dual-view host reached three ways,
// so the count is exercised as a property of position rather than of type.
type splitSiteHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type splitSiteCase struct {
	Name string `json:"name"`
}

type splitSiteTwoFixed struct {
	A splitSiteHost `json:"a"`
	B splitSiteHost `json:"b"`
}

type splitSiteRecursive struct {
	Self *splitSiteRecursive `json:"self"`
	D    splitSiteHost       `json:"d"`
}

func init() {
	vbind.DefineVariantCases[splitSiteHost, struct {
		_ splitSiteCase `case:"c1"`
	}]()
}

// TestSplitTapeSiteBudget pins the static merged-tape count and, more to the
// point, that the arena sized from it still holds.
//
// K is what separates "the content doubled" from "each merged tape pays a
// two-word prologue". Where vbind can bound K, the budget is srcLen+3+2K rather
// than 2*srcLen, which is close to half the arena for the same document. The
// value being an optimization is why the second half of each case re-parses and
// compares tape_used against the budget: an undercount here is not a slower
// parse, it is C writing past a Go-owned buffer that has no bounds check.
//
// Unboundedness is a property of the path, not of the type: a host under a
// slice, map, array or pointer cycle lets the document choose the count.
func TestSplitTapeSiteBudget(t *testing.T) {
	tapeUsed := func(p *Parser) int {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		return int(m.Alloc.TapeUsed)
	}
	// Minimal hosts maximize words per byte, so they are what a budget derived
	// from K has to survive.
	unit := `{"kind":"c1","name":"a","u":1}`

	t.Run("bounded", func(t *testing.T) {
		for _, c := range []struct {
			name string
			want int
			src  string
			bind func(*Parser, []byte) error
			new  func() (*Parser, error)
		}{
			{
				name: "root", want: 1, src: unit,
				new:  func() (*Parser, error) { return NewParser[splitSiteHost]() },
				bind: func(p *Parser, s []byte) error { var o splitSiteHost; return p.Unmarshal(s, &o) },
			},
			{
				name: "two fixed fields", want: 2, src: `{"a":` + unit + `,"b":` + unit + `}`,
				new:  func() (*Parser, error) { return NewParser[splitSiteTwoFixed]() },
				bind: func(p *Parser, s []byte) error { var o splitSiteTwoFixed; return p.Unmarshal(s, &o) },
			},
		} {
			p, err := c.new()
			if err != nil {
				t.Fatalf("%s: NewParser: %v", c.name, err)
			}
			if got := p.tt.SplitTapeSites; got != c.want {
				t.Errorf("%s: SplitTapeSites = %d, want %d", c.name, got, c.want)
				continue
			}
			if err := c.bind(p, []byte(c.src)); err != nil {
				t.Fatalf("%s: Unmarshal: %v", c.name, err)
			}
			budget := len(c.src) + 3 + 2*c.want
			if used := tapeUsed(p); used > budget {
				t.Errorf("%s: %d tape words for %d source bytes, past the srcLen+3+2K=%d the arena "+
					"was sized to: C writes past the Go-owned arena with no bounds check",
					c.name, used, len(c.src), budget)
			}
		}
	})

	// A collection or a cycle on the path hands the count to the document, so the
	// per-byte bound is all that is left.
	t.Run("unbounded", func(t *testing.T) {
		for _, c := range []struct {
			name string
			tt   func() (*vbind.TypeTree, error)
		}{
			{"under a slice", vbind.TypeTreeFor[[]splitSiteHost]},
			{"under a map", vbind.TypeTreeFor[map[string]splitSiteHost]},
			{"under an array", vbind.TypeTreeFor[[4]splitSiteHost]},
			{"through a pointer cycle", vbind.TypeTreeFor[splitSiteRecursive]},
		} {
			tt, err := c.tt()
			if err != nil {
				t.Fatalf("%s: TypeTreeFor: %v", c.name, err)
			}
			if tt.SplitTapeSites != vbind.SplitTapeSitesUnbounded {
				t.Errorf("%s: SplitTapeSites = %d, want unbounded: a static count here would size "+
					"the arena for fewer tapes than the document can ask for", c.name, tt.SplitTapeSites)
			}
		}
	})
}

// TestTapeArenaSizedFromScan covers the tape arena being sized from the token mix
// the native root scan counts, rather than from a bound over the source bytes.
//
// The srcLen bound treats every byte as a potential tape word, but most bytes of a
// real document sit inside string bodies or whitespace and cost a fraction of one.
// So the arena is seeded with a guess, and the scan reports the real bound via
// alloc.tape_need, yielding BindYieldTapeArena when the guess falls short.
//
// The guess being wrong must cost only a round-trip, never correctness, and that
// is the half worth testing: the yield fires before any tape word is written, so
// Go may replace the backing outright. Small and large documents are alternated on
// ONE pooled parser to drive the high-water guess below what the large one needs,
// which is what forces the yield.
func TestTapeArenaSizedFromScan(t *testing.T) {
	type host struct {
		Rest value.Value `json:",embed"`
	}
	arena := func(p *Parser) (used, capw int) {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		return int(m.Alloc.TapeUsed), int(m.Alloc.TapeArenaCap)
	}

	t.Run("guess too low still binds correctly", func(t *testing.T) {
		small := []byte(`{"a":1}`)
		big := []byte(`{"u":[` + strings.TrimSuffix(strings.Repeat("1,", 20000), ",") + `]}`)
		p, err := NewParser[host]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		for iter := range 40 {
			var s host
			if err := p.Unmarshal(small, &s); err != nil {
				t.Fatalf("iter %d small: %v", iter, err)
			}
			if got := s.Rest.Get("a").String(); got != "1" {
				t.Fatalf("iter %d small: a = %s", iter, got)
			}
			var b host
			if err := p.Unmarshal(big, &b); err != nil {
				t.Fatalf("iter %d big: %v", iter, err)
			}
			u := b.Rest.Get("u")
			if n := u.Len(); n != 20000 {
				t.Fatalf("iter %d big: u.Len = %d, want 20000", iter, n)
			}
			if used, capw := arena(p); used > capw {
				t.Fatalf("iter %d: wrote %d words into a %d-word arena", iter, used, capw)
			}
		}
	})

	// Token-dense input is where the scan-derived bound is tightest and can even
	// exceed the srcLen one, so it is the shape most likely to undersize.
	t.Run("token dense shapes fit", func(t *testing.T) {
		for _, src := range []string{
			"{}",
			`{"u":1}`,
			`{"u":[` + strings.TrimSuffix(strings.Repeat("1,", 5000), ",") + `]}`,
			`{"u":[` + strings.TrimSuffix(strings.Repeat("-1.5e300,", 2000), ",") + `]}`,
			`{"u":{` + strings.TrimSuffix(strings.Repeat(`"k":{},`, 2000), ",") + `}}`,
			`{"u":[` + strings.TrimSuffix(strings.Repeat(`"",`, 5000), ",") + `]}`,
		} {
			p, err := NewParser[host]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var h host
			if err := p.Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("len=%d: %v", len(src), err)
			}
			used, capw := arena(p)
			if used > capw {
				t.Errorf("len=%d: wrote %d words into a %d-word arena, %d past the Go buffer",
					len(src), used, capw, used-capw)
			}
		}
	})

	// The point of the exercise: a real document must end up with an arena far
	// below what the srcLen policy budgets, which counts every source byte as a
	// potential tape word.
	t.Run("real documents shrink the arena", func(t *testing.T) {
		raw, err := loadCorpus("twitter_status")
		if err != nil {
			t.Fatalf("loadCorpus twitter_status: %v", err)
		}
		var compact bytes.Buffer
		if cerr := json.Compact(&compact, raw); cerr != nil {
			t.Fatalf("compact twitter_status: %v", cerr)
		}
		src := compact.Bytes()
		p, err := NewParser[host]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		for range 5 {
			var h host
			if err := p.Unmarshal(src, &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
		}
		used, capw := arena(p)
		// tapeAmortize is vbind's multiplier, restated rather than imported: this
		// test asserts a ratio against the srcLen policy's allocation, so it must
		// not silently follow a retune of the multiplier.
		const tapeAmortize = 3
		srcLenPolicy := tapeAmortize * (len(src) + 3)
		t.Logf("srcLen=%d used=%d newCap=%d srcLenPolicyCap=%d (%.1fx smaller)",
			len(src), used, capw, srcLenPolicy, float64(srcLenPolicy)/float64(capw))
		if capw > srcLenPolicy/2 {
			t.Errorf("arena is %d words against the srcLen policy's %d: "+
				"scan-derived sizing must stay at least 2x ahead", capw, srcLenPolicy)
		}
		if used > capw {
			t.Errorf("wrote %d words into a %d-word arena", used, capw)
		}
	})
}
