package value

import (
	"testing"

	"github.com/velox-io/json/internal/valueabi"
)

// Seam encoding, asserted directly on hand-built words.
//
// This level is necessary because the behavioral tests cannot reach it. A seam
// on a single-consumer tape holds the same distance in both fields, so a reader
// that consults the wrong one still lands in the right place: swapping view A and
// view B in skipSeams passes the entire suite. Verified, not assumed.
//
// What follows therefore states the encoding as an encoding: which bits hold
// which view, that a reserved seam is inert, and that the two views can disagree.
// The last is the property the whole design rests on, and until a producer emits
// a divergent seam these cases are the only thing that holds it.

const (
	seamTagWord = valueabi.SeamBit
	objBegWord  = uint64('{') << 56
	objEndWord  = uint64('}') << 56
	strTagWord  = uint64('"') << 56
)

func mkSeam(distA, distB uint64) uint64 {
	return seamTagWord | distA | (distB << valueabi.SeamBits)
}

// The two distances occupy disjoint fields and neither leaks into the other.
// Chosen so that a shift by the wrong amount, or a mask of the wrong width, gives
// an answer distinguishable from the right one.
func TestSeam_FieldsAreIndependent(t *testing.T) {
	cases := []struct{ a, b uint64 }{
		{1, 1},   // a reserved seam
		{1, 2},   // view B leaps one entry view A keeps
		{2, 1},   // and the reverse
		{5, 9},   // both widened, unequal
		{1, 100}, // a large gap in one view only
		{valueabi.SeamMask, 1},
		{1, valueabi.SeamMask},
		{valueabi.SeamMask, valueabi.SeamMask}, // both at the field's maximum
	}
	for _, c := range cases {
		w := mkSeam(c.a, c.b)
		if got := (w >> valueabi.SeamViewA) & valueabi.SeamMask; got != c.a {
			t.Errorf("seam(%d,%d): view A = %d, want %d", c.a, c.b, got, c.a)
		}
		if got := (w >> valueabi.SeamViewB) & valueabi.SeamMask; got != c.b {
			t.Errorf("seam(%d,%d): view B = %d, want %d", c.a, c.b, got, c.b)
		}
		if !valueabi.IsSeam(w) {
			t.Errorf("seam(%d,%d): word %#x does not read as a seam", c.a, c.b, w)
		}
	}
}

// The field width, stated as literals rather than in terms of valueabi.SeamBits/valueabi.SeamMask.
//
// Every other case here derives its expectations from those two constants, so
// narrowing them narrows the tests in step and nothing fails. Verified: changing
// valueabi.SeamMask to 27 bits left the whole suite green. The width is also an ABI
// agreement with TAPE_SEAM_BITS in core/tape.h, which cannot see these constants,
// so it has to be pinned to a number.
func TestSeam_FieldWidthIsThirtyOneBits(t *testing.T) {
	if valueabi.SeamBits != 31 {
		t.Errorf("valueabi.SeamBits = %d, want 31 (must equal TAPE_SEAM_BITS in core/tape.h)", valueabi.SeamBits)
	}
	if valueabi.SeamMask != 0x7FFFFFFF {
		t.Errorf("valueabi.SeamMask = %#x, want 0x7FFFFFFF (must equal TAPE_SEAM_MASK in core/tape.h)", valueabi.SeamMask)
	}
	if valueabi.SeamViewA != 0 || valueabi.SeamViewB != 31 {
		t.Errorf("views at %d and %d, want 0 and 31 (must equal TAPE_VIEW_A / TAPE_VIEW_B)", valueabi.SeamViewA, valueabi.SeamViewB)
	}
	if valueabi.ViewShiftMask != 0x1F {
		t.Errorf("valueabi.ViewShiftMask = %#x, want 0x1F (must equal TAPE_VIEW_SHIFT_MASK in core/tape.h and VJ_TVIEW_SHIFT_MASK in encvm)", valueabi.ViewShiftMask)
	}
	// The count-location flag must sit above the shift mask; a bit inside it
	// would leak into a seam shift. Its exact position is an ABI agreement with
	// TAPE_MODE_COUNT_AT_CLOSE in core/tape.h, so it is pinned to a number.
	if valueabi.ModeCountAtClose != 1<<8 {
		t.Errorf("valueabi.ModeCountAtClose = %#x, want 1<<8 (must equal TAPE_MODE_COUNT_AT_CLOSE in core/tape.h)", valueabi.ModeCountAtClose)
	}
	if valueabi.ModeInlineDualRoot != valueabi.SeamViewA|valueabi.ModeCountAtClose ||
		valueabi.ModeReserveDualRoot != valueabi.SeamViewB {
		t.Errorf("dual-root modes = %#x / %#x, want view A|CountAtClose and view B", valueabi.ModeInlineDualRoot, valueabi.ModeReserveDualRoot)
	}
	if valueabi.SeamBit != uint64(1)<<63 {
		t.Errorf("valueabi.SeamBit = %#x, want 1<<63 (must equal TAPE_SEAM_BIT in core/tape.h)", valueabi.SeamBit)
	}

	// The two fields plus the marker fill the word exactly: 31 + 31 + 1 = 64. A
	// maximal seam must therefore leave the marker set, which a wider field would
	// not.
	w := mkSeam(0x7FFFFFFF, 0x7FFFFFFF)
	if !valueabi.IsSeam(w) {
		t.Errorf("word %#x stopped reading as a seam: the distance fields overflowed the marker bit", w)
	}
	// An out-of-range distance DOES corrupt the neighboring field: one bit past
	// view A is view B's low bit. Nothing in the layout can prevent that, which is
	// exactly why the write sites range-check before packing rather than relying on
	// truncation being harmless. Asserted so the requirement is stated where the
	// encoding is, not left implicit in the writers.
	if got := (mkSeam(1<<31, 0) >> valueabi.SeamViewB) & 0x7FFFFFFF; got != 1 {
		t.Errorf("view B = %d, want 1: an over-range view A distance must be shown to corrupt view B", got)
	}
}

// skipSeams follows the view its Value names, and only that view. A tape whose
// seams disagree is walked into two different sequences by two Values that differ
// in nothing but shift.
//
// Layout, the shape a merged tape has: a seam precedes each entry, and an entry
// is a key word plus a value word.
//
//	0 {        close=8
//	1 J        A advances 1 (to the entry), B advances 3 (to the NEXT seam)
//	2 "a"      key of the entry only view A keeps
//	3 "x"      its value
//	4 J        both advance 1
//	5 "b"      key of the entry both views keep
//	6 "y"      its value
//	7 J        trailing seam, both advance 1
//	8 }
//
// View A sees both entries; view B leaps the first. Nothing physical differs.
//
// A widened distance targets the following SEAM, not the following key, which is
// what a drop naturally produces: the seam after the dropped entry is already
// there and is the next place a decision can be recorded. So a leap lands on a
// seam and the walk takes one more step, and consecutive drops chain (see
// TestSeam_ChainFollowedPerView) instead of needing one wide distance.
func TestSeam_ViewsDivergeOnOneTape(t *testing.T) {
	str := func(off, n uint32) uint64 { return strTagWord | uint64(off) | (uint64(n) << 32) }
	arena, off := testStringArena("a", "b", "x", "y")
	tape := []uint64{
		objBegWord | 8 | (2 << 32), // 0
		mkSeam(1, 3),               // 1: A -> 2, B -> 4
		str(off[0], 1),             // 2: "a"
		str(off[2], 1),             // 3: "x"
		mkSeam(1, 1),               // 4
		str(off[1], 1),             // 5: "b"
		str(off[3], 1),             // 6: "y"
		mkSeam(1, 1),               // 7
		objEndWord,                 // 8
	}
	doc := &valueabi.Doc{Tape: tape, StrArena: arena}

	// Same doc, same base, same root: only the lens differs.
	viewA := testValueAt(doc, 0, 0, int32(len(tape)), valueabi.SeamViewA)
	viewB := testValueAt(doc, 0, 0, int32(len(tape)), valueabi.SeamViewB)

	if got := viewA.desc.SkipSeams(1); got != 2 {
		t.Errorf("view A skipSeams(1) = %d, want 2 (it keeps the first member)", got)
	}
	// 5, not 4: the distance targets the seam at 4, which then advances one more.
	if got := viewB.desc.SkipSeams(1); got != 5 {
		t.Errorf("view B skipSeams(1) = %d, want 5 (it leaps the first entry)", got)
	}

	// The same divergence through the public walk, which is what a consumer sees.
	var gotA, gotB []string
	viewA.ForEachKey(func(k string, _ Value) bool { gotA = append(gotA, k); return true })
	viewB.ForEachKey(func(k string, _ Value) bool { gotB = append(gotB, k); return true })
	if len(gotA) != 2 || gotA[0] != "a" || gotA[1] != "b" {
		t.Errorf("view A walked %v, want [a b]", gotA)
	}
	if len(gotB) != 1 || gotB[0] != "b" {
		t.Errorf("view B walked %v, want [b]", gotB)
	}
}

// A chain of seams, which is what consecutive drops produce: each leaps to the
// next rather than one leaping the whole run. The walk must follow the chain to
// its end, and a chain in one view must not perturb the other.
func TestSeam_ChainFollowedPerView(t *testing.T) {
	str := func(off, n uint32) uint64 { return strTagWord | uint64(off) | (uint64(n) << 32) }
	// Three entries, each preceded by a seam. View B leaps the first two by
	// chaining seam 1 -> seam 4 -> seam 7.
	//
	//	0 {  1 J  2 "a" 3 "x"  4 J  5 "b" 6 "y"  7 J  8 "c" 9 "z"  10 J  11 }
	arena, off := testStringArena("a", "b", "c", "x", "y", "z")
	tape := []uint64{
		objBegWord | 11 | (3 << 32), // 0
		mkSeam(1, 3),                // 1: B -> 4
		str(off[0], 1),              // 2: "a"
		str(off[3], 1),              // 3: "x"
		mkSeam(1, 3),                // 4: B -> 7
		str(off[1], 1),              // 5: "b"
		str(off[4], 1),              // 6: "y"
		mkSeam(1, 1),                // 7
		str(off[2], 1),              // 8: "c"
		str(off[5], 1),              // 9: "z"
		mkSeam(1, 1),                // 10
		objEndWord,                  // 11
	}
	doc := &valueabi.Doc{Tape: tape, StrArena: arena}
	viewA := testValueAt(doc, 0, 0, int32(len(tape)), valueabi.SeamViewA)
	viewB := testValueAt(doc, 0, 0, int32(len(tape)), valueabi.SeamViewB)

	if got := viewA.desc.SkipSeams(1); got != 2 {
		t.Errorf("view A skipSeams(1) = %d, want 2", got)
	}
	// One call, not one per link: skipSeams loops until it reaches a value word.
	if got := viewB.desc.SkipSeams(1); got != 8 {
		t.Errorf("view B skipSeams(1) = %d, want 8 (the chain must be followed to its end)", got)
	}
	// Through the public walk, so the chain is checked as a sequence and not only
	// as one landing position.
	var keys []string
	viewB.ForEachKey(func(k string, _ Value) bool { keys = append(keys, k); return true })
	if len(keys) != 1 || keys[0] != "c" {
		t.Errorf("view B walked %v, want [c]", keys)
	}
}

// A reserved seam advances exactly one word in either view, so a tape carrying
// only reserved seams reads as if it had none. This is what replaced the separate
// no-op tag: a reserved slot and a widened one are the same word shape, which is
// why the read path needs no branch between them.
func TestSeam_ReservedIsInert(t *testing.T) {
	str := func(off, n uint32) uint64 { return strTagWord | uint64(off) | (uint64(n) << 32) }
	reserved := mkSeam(1, 1)
	arena, off := testStringArena("a", "b", "x", "y")
	tape := []uint64{
		objBegWord | 8 | (2 << 32), // 0
		reserved,                   // 1
		str(off[0], 1),             // 2: "a"
		str(off[2], 1),             // 3: "x"
		reserved,                   // 4
		str(off[1], 1),             // 5: "b"
		str(off[3], 1),             // 6: "y"
		reserved,                   // 7
		objEndWord,                 // 8
	}
	doc := &valueabi.Doc{Tape: tape, StrArena: arena}
	for _, shift := range []int32{valueabi.SeamViewA, valueabi.SeamViewB} {
		v := testValueAt(doc, 0, 0, int32(len(tape)), shift)
		var keys []string
		v.ForEachKey(func(k string, _ Value) bool { keys = append(keys, k); return true })
		if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
			t.Errorf("shift=%d walked %v, want [a b]: a reserved seam must be inert", shift, keys)
		}
	}
}

// Distances are relative to the seam word, not indices into the tape. That is what
// lets a span be copied to a different offset without rewriting its seams, so it
// is asserted as its own property: the same words at two different bases must
// walk to the same members.
func TestSeam_DistancesAreRelative(t *testing.T) {
	str := func(off, n uint32) uint64 { return strTagWord | uint64(off) | (uint64(n) << 32) }
	arena, off := testStringArena("a", "b", "x", "y")
	span := []uint64{
		objBegWord | 8 | (2 << 32), // +0
		mkSeam(1, 3),               // +1: A -> +2, B -> +4
		str(off[0], 1),             // +2 "a"
		str(off[2], 1),             // +3 "x"
		mkSeam(1, 1),               // +4
		str(off[1], 1),             // +5 "b"
		str(off[3], 1),             // +6 "y"
		mkSeam(1, 1),               // +7
		objEndWord,                 // +8
	}
	for _, base := range []int32{0, 3, 17} {
		tape := make([]uint64, int(base)+len(span))
		copy(tape[base:], span)
		doc := &valueabi.Doc{Tape: tape, StrArena: arena}
		for _, tc := range []struct {
			shift int32
			want  []string
		}{
			{valueabi.SeamViewA, []string{"a", "b"}},
			{valueabi.SeamViewB, []string{"b"}},
		} {
			v := testValueAt(doc, base, 0, int32(len(span)), tc.shift)
			var keys []string
			v.ForEachKey(func(k string, _ Value) bool { keys = append(keys, k); return true })
			if len(keys) != len(tc.want) {
				t.Errorf("base=%d shift=%d walked %v, want %v", base, tc.shift, keys, tc.want)
				continue
			}
			for i := range keys {
				if keys[i] != tc.want[i] {
					t.Errorf("base=%d shift=%d walked %v, want %v", base, tc.shift, keys, tc.want)
					break
				}
			}
		}
	}
}
