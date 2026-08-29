package tests

// Adversarial cases for tape construction, found by differential fuzzing of the
// tape builder against two independent oracles: a strict RFC 8259 validator
// (does the parser accept exactly the valid documents?) and a hand-written tape
// walker (does the produced tape satisfy its own structural invariants?).
//
// The walker below deliberately avoids valueabi.Descriptor.Skip and
// value.Value's Len / ForEachKey. Those are the reader half of the same design,
// so a walker written from them agrees with the producer by construction and
// can only find disagreements the design already anticipated. Reading doc.Tape
// directly is what lets a shared misunderstanding surface.
//
// Two groups of tests live here:
//
//   - FINDING_* are reproductions of defects. They assert the behavior the
//     format's own rules call for, so they FAIL until the defect is fixed.
//   - PIN_* are invariants that hold today and are cheap to break. They pass,
//     and exist so a future change to the seam machinery has to keep them.
//
// The DOM path also rejects out-of-range numbers under JSON_DOM_STR_INSITU,
// which no Go entry point can reach (decode/dom.StrModeInsitu is commented out),
// so that one is reproduced in C only. See the note on FINDING_ZeroCopy below.

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// ---------------------------------------------------------------------------
// independent tape walker
// ---------------------------------------------------------------------------

const tapeSeamMask = 0x7FFFFFFF

type tapeWalk struct {
	tape  []uint64
	base  int
	tidx  int
	shift int // seam view shift, already masked out of the descriptor mode
	arena []byte
	src   []byte
	hops  int
}

func newTapeWalk(v *value.Value) (*tapeWalk, error) {
	desc := valueabi.Load(unsafe.Pointer(v))
	doc := desc.Doc
	if doc == nil || len(doc.Tape) == 0 {
		return nil, fmt.Errorf("Value has no published tape")
	}
	return &tapeWalk{
		tape: doc.Tape, base: int(desc.Base), tidx: int(desc.Tidx),
		shift: int(desc.Mode & valueabi.ViewShiftMask),
		arena: doc.StrArena, src: doc.Src,
	}, nil
}

func (w *tapeWalk) word(i int) (uint64, error) {
	if w.base+i < 0 || w.base+i >= len(w.tape) {
		return 0, fmt.Errorf("index %d (base %d) out of range, len(tape)=%d", i, w.base, len(w.tape))
	}
	return w.tape[w.base+i], nil
}

// skipSeams follows this view's half of each seam. It reports a zero distance
// instead of flooring it to 1: the floor in the production walkers is a liveness
// guard, and a test that inherits it cannot see the condition it guards against.
func (w *tapeWalk) skipSeams(i int) (int, error) {
	for {
		x, err := w.word(i)
		if err != nil {
			return 0, err
		}
		if int64(x) >= 0 {
			return i, nil
		}
		d := int((x >> uint(w.shift)) & tapeSeamMask)
		if d == 0 {
			return 0, fmt.Errorf("seam at %d carries distance 0 for view shift %d (word %#016x)", i, w.shift, x)
		}
		if w.hops++; w.hops > 1<<22 {
			return 0, fmt.Errorf("seam chain near index %d does not terminate", i)
		}
		i += d
	}
}

func (w *tapeWalk) str(i int) (string, error) {
	x, err := w.word(i)
	if err != nil {
		return "", err
	}
	tag := byte(x >> 56)
	off, n := int(x&0xFFFFFFFF), int((x>>32)&0xFFFFFF)
	var buf []byte
	switch tag {
	case '"', 'D', 'S':
		buf = w.arena
	case 'R':
		buf = w.src
	default:
		return "", fmt.Errorf("tape[%d] tag %q is not a string", i, tag)
	}
	if off < 0 || off+n > len(buf) {
		return "", fmt.Errorf("tape[%d] tag %q: range [%d,%d) past buffer len %d", i, tag, off, off+n, len(buf))
	}
	return string(buf[off : off+n]), nil
}

// valueEnd is one past the value at i, which must already be a real value word.
func (w *tapeWalk) valueEnd(i int) (int, error) {
	x, err := w.word(i)
	if err != nil {
		return 0, err
	}
	if int64(x) < 0 {
		return 0, fmt.Errorf("valueEnd landed on a seam word at %d", i)
	}
	switch tag := byte(x >> 56); tag {
	case '{', '[':
		wantEnd := byte('}')
		if tag == '[' {
			wantEnd = ']'
		}
		close := int(x & 0xFFFFFFFF)
		if close <= i {
			return 0, fmt.Errorf("container at %d: close index %d does not point forward", i, close)
		}
		cw, err := w.word(close)
		if err != nil {
			return 0, fmt.Errorf("container at %d: %w", i, err)
		}
		if got := byte(cw >> 56); got != wantEnd {
			return 0, fmt.Errorf("container at %d: paired word %d has tag %q, want %q", i, close, got, wantEnd)
		}
		if back := int(cw & 0xFFFFFFFF); back != i {
			return 0, fmt.Errorf("close word %d names open %d, but it belongs to %d", close, back, i)
		}
		return close + 1, nil
	case 'l', 'u', 'd':
		return i + 2, nil // tag word plus one value word, which is never inspected
	case '"', 'R', 'D', 'S', 't', 'f', 'n':
		return i + 1, nil
	default:
		return 0, fmt.Errorf("tape[%d]: unknown tag %#02x (word %#016x)", i, tag, x)
	}
}

func (w *tapeWalk) skipValue(i int) (int, error) {
	i, err := w.skipSeams(i)
	if err != nil {
		return 0, err
	}
	e, err := w.valueEnd(i)
	if err != nil {
		return 0, err
	}
	return w.skipSeams(e)
}

// walkObject returns the keys of the object v addresses, in document order, plus
// the entry count its root word advertises. The two are computed by different
// means on purpose: keys come from threading the seam chain, count from the
// 24 bits packed into the open word. A view whose chain and count disagree reads
// as a different object depending on which one the caller trusts.
func walkObject(v *value.Value) (keys []string, count int, err error) {
	w, err := newTapeWalk(v)
	if err != nil {
		return nil, 0, err
	}
	root, err := w.skipSeams(w.tidx)
	if err != nil {
		return nil, 0, err
	}
	rw, err := w.word(root)
	if err != nil {
		return nil, 0, err
	}
	if tag := byte(rw >> 56); tag != '{' {
		return nil, 0, fmt.Errorf("root word at %d has tag %q, want '{'", root, tag)
	}
	close := int(rw & 0xFFFFFFFF)
	count = int((rw >> 32) & 0xFFFFFF)
	if close <= root {
		return nil, 0, fmt.Errorf("root at %d: close %d does not point forward", root, close)
	}
	cur, err := w.skipSeams(root + 1)
	if err != nil {
		return nil, 0, err
	}
	for cur < close {
		k, kerr := w.str(cur)
		if kerr != nil {
			return nil, 0, fmt.Errorf("member key at %d: %w", cur, kerr)
		}
		keys = append(keys, k)
		if cur, err = w.skipValue(cur + 1); err != nil {
			return nil, 0, err
		}
	}
	if cur != close {
		return nil, 0, fmt.Errorf("walk overran the close: landed at %d, close is %d", cur, close)
	}
	return keys, count, nil
}

// checkView asserts that the count field, the seam chain and the caller's
// expectation all name the same set of entries.
func checkView(t *testing.T, label string, v *value.Value, wantKeys []string) {
	t.Helper()
	keys, count, err := walkObject(v)
	if err != nil {
		t.Errorf("%s: independent walk: %v", label, err)
		return
	}
	if count != len(keys) {
		t.Errorf("%s: count field says %d entries, the seam chain threads %d (%v)", label, count, len(keys), keys)
	}
	if got := v.Len(); got != len(keys) {
		t.Errorf("%s: Len() = %d, independent walk found %d", label, got, len(keys))
	}
	if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("%s: keys = %v, want %v", label, keys, wantKeys)
	}
}

// ---------------------------------------------------------------------------
// types under test
// ---------------------------------------------------------------------------

// A host with both an embedded variant and a reserve-unknown: two consumers for
// one merged tape, served as two seam views over the same words.
type advDualHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type advDualCase struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// Same, plus a declared value.Value field. Binding that field writes a tape at
// the arena tail, which lands between two merged-tape entries and forces the
// standing seam to be widened across the gap.
type advGapHost struct {
	Kind string      `json:"kind"`
	Blob value.Value `json:"blob"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

// A sink with no variant beside it: one consumer, so the merged tape is single
// view and the unknowns are read through view A in place.
type advSinkHost struct {
	Name string      `json:"name"`
	Rest value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[advDualHost, struct {
		_ advDualCase `case:"c1"`
	}]()
	vbind.DefineVariantCases[advGapHost, struct {
		_ advDualCase `case:"c1"`
	}]()
}

// ---------------------------------------------------------------------------
// PIN: the 24-bit length field rejects rather than truncating
// ---------------------------------------------------------------------------

// A tape word packs a string as (offset, length) with 24 bits for the length, so
// a body at or past 2^24 has no representation. Every site that produces such a
// word now refuses one instead of storing length mod 2^24: the check is folded
// into the malformed-escape branch each site already had, by testing the decoded
// length as unsigned (-1 and >2^24 fail the same compare).
//
// Truncation was the defect: it also broke the WINDOW terminator invariant that
// key lookup relies on, since str_arena[off+len] is supposed to be '"' and at a
// truncated len it is a byte of the body.
//
// What is NOT asserted here is that such a document is invalid outright. The
// bound belongs to the tape, so it binds the targets that go through a tape and
// no others: a Go string field is written from the source and holds all 16 MiB,
// while a value.Value field fails. That is the same rule the number side already
// follows, where 1e309 reaches a value.Value or json.Number and only a float64
// target fails, "and it fails where it is bound" (number.h). Legality of the
// document and expressiveness of the destination are separate questions.
func TestPIN_StringPast24BitsRejectsNotTruncates(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~100 MiB")
	}
	const lim = 1 << 24
	for _, n := range []int{lim - 1, lim, lim + 1} {
		// An escape-free body and one carrying an escape take different decode
		// paths (the scan's fast exit vs. its continuation); both must agree.
		for _, body := range []string{
			strings.Repeat("x", n),
			`\n` + strings.Repeat("x", n-1),
		} {
			src := []byte(`{"s":"` + body + `"}`)

			var taped struct {
				S value.Value `json:"s"`
			}
			err := vjson.Unmarshal(src, &taped)

			if n >= lim {
				if err == nil {
					got, _ := taped.S.Str()
					t.Errorf("n=%d: an unrepresentable length was accepted and reads back as %d "+
						"bytes (n mod 2^24 = %d): the tape word silently truncated", n, len(got), n%lim)
				}
				continue
			}
			if err != nil {
				t.Errorf("n=%d: a representable length was rejected: %v", n, err)
				continue
			}
			got, ok := taped.S.Str()
			if !ok {
				t.Errorf("n=%d: no string on the tape", n)
				continue
			}
			if len(got) != n {
				t.Errorf("n=%d: read %d bytes", n, len(got))
			}
		}
	}
}

// The DOM path, both string modes, at the same boundary. Separate from the bind
// path above because it reaches dom_visit_string's own two branches rather than
// bind_emit_string_copy.
func TestPIN_DomStringPast24BitsRejects(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~100 MiB")
	}
	const lim = 1 << 24
	modes := []struct {
		name string
		opts []dom.ParseOption
	}{
		{"copy", nil},
		{"zero-copy", []dom.ParseOption{dom.WithZeroCopy()}},
	}
	for _, n := range []int{lim - 1, lim} {
		for _, m := range modes {
			for _, body := range []string{
				strings.Repeat("x", n),
				`\n` + strings.Repeat("x", n-1),
			} {
				src := []byte(`{"s":"` + body + `"}`)
				v, err := dom.ParsePadded(dom.Pad(src), m.opts...)
				if n >= lim {
					if err == nil {
						sv := v.Get("s")
						got, _ := sv.Str()
						t.Errorf("%s n=%d: accepted, reads back %d bytes: truncated",
							m.name, n, len(got))
					}
					continue
				}
				if err != nil {
					t.Errorf("%s n=%d: rejected a representable length: %v", m.name, n, err)
					continue
				}
				sv := v.Get("s")
				if got, _ := sv.Str(); len(got) != n {
					t.Errorf("%s n=%d: read %d bytes", m.name, n, len(got))
				}
			}
		}
	}
}

// An object key past the same limit is rejected too, by ndec_str_parse_zc_scan's
// guard on the bind path. Pinned as the sibling of the value-side bound: both
// halves of a member now answer to the same limit.
func TestPIN_ObjectKeyPast24BitsIsRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~100 MiB")
	}
	const n = 1 << 24
	var host advSinkHost
	src := []byte(`{"name":"bob","` + strings.Repeat("x", n) + `":1}`)
	err := vjson.Unmarshal(src, &host)
	if err == nil {
		keys, _, werr := walkObject(&host.Rest)
		if werr != nil {
			t.Fatalf("walk: %v", werr)
		}
		for _, k := range keys {
			if len(k) != n {
				t.Errorf("a %d-byte key was accepted and reads back as %d bytes", n, len(k))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// PIN: zero-copy accepts every document the default mode accepts
// ---------------------------------------------------------------------------

// str_arena used to be sized per mode: the default mode got a bound computed
// from srcLen, while zero copy got a 4 KiB seed and was expected to grow on
// demand. It could not. The Go DOM entry point (ndec_dom_parse_counted)
// installs no allocator, so the grow call could only ever fail, which made
// acceptance a function of how much arena-bound text happened to precede a
// token rather than of the document being well formed. Two kinds of text land
// there under zero copy: escape-bearing string bodies, and numbers no binary
// form represents faithfully (TAPE_NUM_RAW).
//
// The fix sizes str_arena once, mode-independently, for the whole document, and
// removes the grow path entirely. Zero copy writes strictly less than the
// default mode does, so one bound covers both, and the mode now decides only
// where bytes land, never whether the parse can finish.
func TestPIN_ZeroCopyAcceptsWhateverDefaultAccepts(t *testing.T) {
	// Kept number text: 1e309 is out of double's range, so each element is
	// stored as its source text.
	t.Run("number text", func(t *testing.T) {
		for _, n := range []int{1, 256, 1024, 4096, 16384} {
			src := []byte("[" + strings.TrimSuffix(strings.Repeat("1e309,", n), ",") + "]")
			if _, err := dom.Parse(src); err != nil {
				t.Errorf("n=%d: default mode rejected a valid document: %v", n, err)
			}
			if _, err := dom.ParsePadded(dom.Pad(src), dom.WithZeroCopy()); err != nil {
				t.Errorf("n=%d (%d bytes of kept number text): zero-copy rejected a document the "+
					"default mode accepts: %v", n, n*5, err)
			}
		}
	})

	// Escaped string bodies: the only other writer into str_arena under zero
	// copy, and the one the old 4 KiB seed hit first.
	t.Run("escaped strings", func(t *testing.T) {
		for _, n := range []int{1, 100, 1000, 20000} {
			src := []byte("[" + strings.TrimSuffix(strings.Repeat(`"a\nb",`, n), ",") + "]")
			if _, err := dom.Parse(src); err != nil {
				t.Errorf("n=%d: default mode rejected a valid document: %v", n, err)
			}
			v, err := dom.ParsePadded(dom.Pad(src), dom.WithZeroCopy())
			if err != nil {
				t.Errorf("n=%d: zero-copy rejected a document the default mode accepts: %v", n, err)
				continue
			}
			if got := v.Len(); got != n {
				t.Errorf("n=%d: zero-copy tape reports %d elements", n, got)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// PIN: the two seam views stay disjoint and self-consistent
// ---------------------------------------------------------------------------

// Every arrangement of a discriminator, case fields and unknown fields up to
// width four. Interleaving is the point: a view that leapt a whole run of
// entries at once, rather than chaining one drop per entry, passes any test whose
// keys are grouped by owner.
func TestPIN_SeamViewsAgreeWithTheirCountField(t *testing.T) {
	type tok struct {
		json, key string
		kind      byte // 'd' discriminator, 'c' case field, 'u' unknown
	}
	toks := []tok{
		{`"kind":"c1"`, "kind", 'd'},
		{`"name":"bob"`, "name", 'c'},
		{`"age":7`, "age", 'c'},
		{`"u1":1`, "u1", 'u'},
		{`"u2":-1`, "u2", 'u'},
		{`"u3":{"deep":[1,2,3]}`, "u3", 'u'},
		{`"u4":-2.0`, "u4", 'u'},
	}

	shapes := 0
	var walk func(cur, left []int)
	walk = func(cur, left []int) {
		if len(cur) > 0 {
			var parts []string
			var wantUnknown, allNames []string
			hasDisc, wantName, wantAge := false, "", 0
			for _, i := range cur {
				parts = append(parts, toks[i].json)
				if toks[i].kind != 'd' {
					allNames = append(allNames, toks[i].key)
				}
				switch toks[i].kind {
				case 'd':
					hasDisc = true
				case 'u':
					wantUnknown = append(wantUnknown, toks[i].key)
				case 'c':
					if toks[i].key == "name" {
						wantName = "bob"
					} else {
						wantAge = 7
					}
				}
			}
			src := "{" + strings.Join(parts, ",") + "}"
			var h advDualHost
			if err := vjson.Unmarshal([]byte(src), &h); err != nil {
				t.Errorf("%s: Unmarshal: %v", src, err)
			} else {
				want := wantUnknown
				if hasDisc {
					c, ok := h.Case.(advDualCase)
					if !ok {
						t.Errorf("%s: Case = %T, want advDualCase", src, h.Case)
					} else if c.Name != wantName || c.Age != wantAge {
						t.Errorf("%s: case = %+v, want {Name:%q Age:%d}", src, c, wantName, wantAge)
					}
				} else {
					// No discriminator selects no case, so the case's own field
					// names are leftover and the sink collects them too.
					want = allNames
				}
				checkView(t, src, &h.Rest, want)
			}
			shapes++
		}
		if len(cur) >= 4 {
			return
		}
		for j := range left {
			next := append(append([]int{}, left[:j]...), left[j+1:]...)
			walk(append(cur, left[j]), next)
		}
	}
	all := make([]int, len(toks))
	for i := range all {
		all[i] = i
	}
	walk(nil, all)
	t.Logf("checked %d generated shapes", shapes)
}

// ---------------------------------------------------------------------------
// PIN: a number's value word is never read as a seam
// ---------------------------------------------------------------------------

// The seam marker is the top bit, and a number's value word has no spare bits,
// so every negative int64 and negative double reads as a seam if anything
// inspects one. The encoding is only sound because no walk does; core/tape.h
// records that the invariant was violated once and that the symptom was a hang.
//
// These payloads make a misread loud rather than subtle. -2.0 is
// 0xC000000000000000, whose view A distance is 0 (a hang, absent the liveness
// floor) and whose view B distance is 0x18000000 words (straight off the tape).
// INT64_MIN is 0x8000000000000000: both distances 0.
func TestPIN_NegativeNumbersAreNotMistakenForSeams(t *testing.T) {
	payloads := []string{
		"-1",                   // 0xFFFFFFFFFFFFFFFF: both distance fields all ones
		"-2.0",                 // 0xC000000000000000: view A distance 0
		"-4.0",                 // view A distance 0
		"-9223372036854775808", // INT64_MIN: both distances 0
		"-1.0", "-0.0",         //
		"-1e-300", "-1e300", // exponent extremes with the sign bit set
		"-9007199254740993",       // just past the float64 integer range
		"-1.7976931348623157e308", // largest finite negative double
	}
	shapes := []string{
		`{"kind":"c1","name":"bob","u1":%[1]s,"u2":2}`,
		`{"kind":"c1","u1":%[1]s,"name":"bob","age":7}`,
		`{"u1":%[1]s,"kind":"c1","name":"bob"}`,
		`{"kind":"c1","name":"bob","u1":%[1]s}`,
		`{"kind":"c1","u1":%[1]s,"u2":%[1]s,"u3":%[1]s,"name":"bob"}`,
	}
	for _, p := range payloads {
		for _, shape := range shapes {
			src := fmt.Sprintf(shape, p)
			var h advDualHost
			if err := vjson.Unmarshal([]byte(src), &h); err != nil {
				t.Errorf("%s: Unmarshal: %v", src, err)
				continue
			}
			keys, count, err := walkObject(&h.Rest)
			if err != nil {
				t.Errorf("%s: walk: %v", src, err)
				continue
			}
			if count != len(keys) {
				t.Errorf("%s: count field %d, seam chain %d (%v)", src, count, len(keys), keys)
			}
			// Re-serializing runs the reader's own walk over the same words.
			if got := h.Rest.String(); !strings.HasPrefix(got, "{") {
				t.Errorf("%s: sink re-serialized as %q", src, got)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// PIN: both views step across a gap another writer punched
// ---------------------------------------------------------------------------

func TestPIN_SeamsSpanGapsFromOtherWriters(t *testing.T) {
	cases := []struct {
		src      string
		wantKeys []string
		wantBlob string
	}{
		{`{"u1":1,"blob":{"z":9},"u2":2,"kind":"c1","name":"bob"}`, []string{"u1", "u2"}, `{"z":9}`},
		{`{"blob":[1,2,3],"u1":1,"kind":"c1","u2":2,"name":"bob","u3":3}`, []string{"u1", "u2", "u3"}, `[1,2,3]`},
		{`{"kind":"c1","u1":1,"blob":{"a":{"b":[1,{"c":2}]}},"u2":2,"name":"bob"}`, []string{"u1", "u2"}, `{"a":{"b":[1,{"c":2}]}}`},
		{`{"u1":1,"blob":1,"u2":2,"blob":2,"u3":3,"kind":"c1"}`, []string{"u1", "u2", "u3"}, `2`},
		{`{"blob":{},"u1":1,"kind":"c1","name":"bob"}`, []string{"u1"}, `{}`},
	}
	for _, c := range cases {
		var h advGapHost
		if err := vjson.Unmarshal([]byte(c.src), &h); err != nil {
			t.Errorf("%s: Unmarshal: %v", c.src, err)
			continue
		}
		checkView(t, c.src, &h.Rest, c.wantKeys)
		if got := h.Blob.String(); got != c.wantBlob {
			t.Errorf("%s: Blob = %s, want %s", c.src, got, c.wantBlob)
		}
	}
}

// ---------------------------------------------------------------------------
// PIN: a view every entry left still reads as an empty object
// ---------------------------------------------------------------------------

func TestPIN_EmptyViewReadsAsEmptyObject(t *testing.T) {
	cases := []struct {
		src      string
		wantKeys []string
	}{
		{`{}`, nil},
		{`{"kind":"c1"}`, nil},
		{`{"kind":"c1","name":"bob","age":7}`, nil}, // view B empty: all case content
		{`{"u1":1}`, []string{"u1"}},                // no discriminator, so all leftover
		{`{"kind":"c1","u1":1}`, []string{"u1"}},    // view A holds only the dropped discriminator
	}
	for _, c := range cases {
		var h advDualHost
		if err := vjson.Unmarshal([]byte(c.src), &h); err != nil {
			t.Errorf("%s: Unmarshal: %v", c.src, err)
			continue
		}
		if got := h.Rest.Type(); got != value.KindObject {
			t.Errorf("%s: sink Type = %v, want KindObject", c.src, got)
			continue
		}
		checkView(t, c.src, &h.Rest, c.wantKeys)
	}
}

// ---------------------------------------------------------------------------
// PIN: the tape arena budget the merged tape actually needs
// ---------------------------------------------------------------------------

// EnsureTapeArena guarantees srcLen+3 words per parse, doubled to 2*srcLen+3
// when the root type has a split tape (an inline variant beside a
// reserve-unknown). Nothing on the C side checks tape_used against
// tape_arena_cap, so those two numbers are the whole safety argument, and the
// rationale written at EnsureTapeArena counts only plain value.Value tapes:
// "the worst-case cumulative tape_used across all Value fields in one parse is
// srcLen+1".
//
// A merged tape spends more than that. Measured below:
//
//	single view, empty         3 words per 3 source bytes   ratio 1.000
//	single view, one unknown   7 words per 7 source bytes   ratio 1.000
//	dual view, empty           5 words per 3 source bytes   ratio 1.667
//
// Both fit, but not by the margin the comment implies: the single-view shapes
// clear srcLen+3 by exactly 3 words at any length, so one extra word per entry
// on that path overflows a Go-owned buffer with no bounds check in between. This
// test measures the ratio so a change that pushes it past the guarantee fails
// here rather than corrupting the heap.
func TestPIN_MergedTapeWordBudget(t *testing.T) {
	repeat := func(unit string, n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(unit+",", n), ",") + "]"
	}
	const n = 2000
	for _, c := range []struct {
		name  string
		src   string
		dual  bool
		parse func(src []byte) (*value.Value, error)
	}{
		{"single_view_empty", repeat(`{}`, n), false, nil},
		{"single_view_one_unknown", repeat(`{"":0}`, n), false, nil},
		{"single_view_long_unknown", repeat(`{"a":1}`, n), false, nil},
		{"dual_view_empty", repeat(`{}`, n), true, nil},
		{"dual_view_one_unknown", repeat(`{"a":1}`, n), true, nil},
		{"dual_view_disc_only", repeat(`{"kind":"c1"}`, n), true, nil},
	} {
		var doc *valueabi.Doc
		if c.dual {
			var v []advDualHost
			if err := vjson.Unmarshal([]byte(c.src), &v); err != nil {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			doc = valueabi.Load(unsafe.Pointer(&v[0].Rest)).Doc
		} else {
			var v []advSinkHost
			if err := vjson.Unmarshal([]byte(c.src), &v); err != nil {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			doc = valueabi.Load(unsafe.Pointer(&v[0].Rest)).Doc
		}
		if doc == nil {
			t.Errorf("%s: no published doc", c.name)
			continue
		}
		used, srcLen := len(doc.Tape), len(c.src)
		budget := srcLen + 3
		if c.dual {
			budget = 2*srcLen + 3
		}
		t.Logf("%-24s srcLen=%-7d tape_used=%-7d words/byte=%.3f budget=%-7d headroom=%d",
			c.name, srcLen, used, float64(used)/float64(srcLen), budget, budget-used)
		if used > budget {
			t.Errorf("%s: needs %d tape words for %d source bytes, past the %d-word arena "+
				"guarantee: C writes past the Go-owned arena with no bounds check",
				c.name, used, srcLen, budget)
		}
	}
}

// The arena cursor is monotonic across parses (CommitTapeArena advances the
// slice, EnsureTapeArena regrows only once the remainder drops below the
// guarantee), so the amortization block is slack a pooled parser eats. Running
// the worst-ratio shape through one repeatedly is what exercises the guarantee
// itself rather than the slack.
func TestPIN_MergedTapeBudgetUnderArenaChurn(t *testing.T) {
	const n = 2000
	src := []byte("[" + strings.TrimSuffix(strings.Repeat("{},", n), ",") + "]")
	for iter := range 200 {
		var v []advDualHost
		if err := vjson.Unmarshal(src, &v); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if len(v) != n {
			t.Fatalf("iter %d: %d elements, want %d", iter, len(v), n)
		}
		for i := range v {
			if v[i].Rest.Type() != value.KindObject || v[i].Rest.Len() != 0 {
				t.Fatalf("iter %d element %d: sink is %v with Len %d, want an empty object",
					iter, i, v[i].Rest.Type(), v[i].Rest.Len())
			}
		}
	}
}
