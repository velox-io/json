package value

import (
	"testing"

	"github.com/velox-io/json/internal/valueabi"
)

// The paired index on a container's open word is what lets a reader step over a
// whole subtree in one load. Behavioral tests cannot guard it: the merged-tape
// walk re-synchronizes on seam words, so an off-by-one in the jump is absorbed
// and every result still comes out right. The failure is silent by construction.
//
// So the guard has to assert the encoding itself, on tapes built by hand where
// the correct answer is known independently of the code that computes it.
//
// These tests are also the fixture for changing the encoding: they state what a
// container word means, so a re-encoding either keeps them passing or has to
// restate them deliberately.

// buildObj lays out {"a":<inner>,"z":1} where inner is supplied as words, and
// returns the tape plus the index of the inner value. Nothing here derives an
// index from the code under test.
func buildObj(inner []uint64) (tape []uint64, innerIdx int, closeIdx int) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagStr    = uint64('"') << 56
		tagInt64  = uint64('l') << 56
	)
	strPack := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }

	// 0:open 1:"a" 2..:inner  then "z" 1  then close
	innerIdx = 2
	body := []uint64{strPack(0, 1)}
	body = append(body, inner...)
	body = append(body, strPack(2, 1), tagInt64, 1)
	closeIdx = 1 + len(body)

	tape = append([]uint64{tagObjBeg | uint64(closeIdx) | (2 << 32)}, body...)
	tape = append(tape, tagObjEnd)
	return tape, innerIdx, closeIdx
}

func tapeValue(tape []uint64) Value {
	arena, _ := testStringArena("a", "z")
	return testValue(&valueabi.Doc{Tape: tape, StrArena: arena})
}

// Skip over a container must land exactly one word past its close, derived from
// the paired index rather than by walking.
func TestPairedIndex_SkipLandsPastClose(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagArrBeg = uint64('[') << 56
		tagArrEnd = uint64(']') << 56
		tagInt64  = uint64('l') << 56
	)
	cases := []struct {
		name  string
		inner []uint64
	}{
		// An empty container is the tightest case: its close is adjacent, so one
		// stray word is already past it.
		{"empty object", []uint64{tagObjBeg | 3 | (0 << 32), tagObjEnd}},
		{"empty array", []uint64{tagArrBeg | 3 | (0 << 32), tagArrEnd}},
		// A one-element container: close is two words on.
		{"one element", []uint64{tagArrBeg | 5 | (1 << 32), tagInt64, 9, tagArrEnd}},
		// Nested: the OUTER paired index must be used, and the inner close must
		// not be mistaken for it.
		{"nested", []uint64{tagArrBeg | 7 | (1 << 32), tagArrBeg | 6 | (1 << 32), tagInt64, 9, tagArrEnd, tagArrEnd}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tape, innerIdx, _ := buildObj(c.inner)
			v := tapeValue(tape)
			// The word after the inner value is the "z" key. Skip must land on it.
			wantNext := innerIdx + len(c.inner)
			if got := v.desc.Skip(innerIdx); got != wantNext {
				t.Errorf("Skip(%d) = %d, want %d (must land on the next key)", innerIdx, got, wantNext)
			}
			// ContainerEnd must name the inner container's own close.
			wantEnd := innerIdx + len(c.inner) - 1
			if got := v.desc.ContainerEnd(innerIdx); got != wantEnd {
				t.Errorf("ContainerEnd(%d) = %d, want %d", innerIdx, got, wantEnd)
			}
			if tag := v.desc.TagAt(wantEnd); tag != valueabi.TagObjEnd && tag != valueabi.TagArrEnd {
				t.Errorf("word at ContainerEnd is tag %q, want a close tag", tag)
			}
		})
	}
}

// Navigation past a container is where a wrong paired index shows up as data
// corruption rather than a slowdown: the key AFTER the container is only
// reachable by stepping over it correctly.
func TestPairedIndex_KeyAfterContainerReachable(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagInt64  = uint64('l') << 56
	)
	// A deep chain, so an off-by-one lands inside it rather than past it.
	inner := []uint64{}
	const depth = 8
	for i := range depth {
		inner = append(inner, tagObjBeg|uint64(2+2*depth-i)|(0<<32))
	}
	for range depth {
		inner = append(inner, tagObjEnd)
	}
	// Restamp each open with its true close now that the layout is known.
	for i := range depth {
		closeAt := 2 + len(inner) - 1 - i
		inner[i] = tagObjBeg | uint64(closeAt) | (1 << 32)
	}
	inner[depth-1] = tagObjBeg | uint64(2+depth) | (0 << 32) // innermost is empty: close is the next word

	tape, innerIdx, _ := buildObj(inner)
	v := tapeValue(tape)

	z := v.Get("z")
	if !z.Valid() {
		t.Fatalf(`Get("z") invalid: a wrong paired index lost the key after the container`)
	}
	if n, ok := z.Int(); !ok || n != 1 {
		t.Errorf("z = %d (ok=%v), want 1", n, ok)
	}
	if got := v.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}
	_ = innerIdx
}

// Len (read off the count) and the walk (which steps entry by entry) derive the
// same fact by different routes. Disagreement means one of the two encodings on
// the open word is wrong, which is exactly what a botched re-encoding produces.
func TestPairedIndex_CountAgreesWithWalk(t *testing.T) {
	const (
		tagArrBeg = uint64('[') << 56
		tagArrEnd = uint64(']') << 56
		tagInt64  = uint64('l') << 56
	)
	inner := []uint64{tagArrBeg | (3 << 32)} // close patched below, once the layout is known
	for i := range 3 {
		inner = append(inner, tagInt64, uint64(i))
	}
	inner = append(inner, tagArrEnd)
	// close sits at innerIdx + len(inner) - 1, and innerIdx is 2.
	inner[0] = tagArrBeg | uint64(2+len(inner)-1) | (3 << 32)

	tape, innerIdx, _ := buildObj(inner)
	v := tapeValue(tape)
	arr := v.Get("a")
	if !arr.Valid() {
		t.Fatal(`Get("a") invalid`)
	}
	var walked int
	arr.ForEachElem(func(int, Value) bool { walked++; return true })
	if got := arr.Len(); got != walked {
		t.Errorf("Len = %d but walk visited %d", got, walked)
	}
	if walked != 3 {
		t.Errorf("walk visited %d elements, want 3", walked)
	}
	_ = innerIdx
}
