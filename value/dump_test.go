package value

import (
	"regexp"
	"strings"
	"testing"

	"github.com/velox-io/json/internal/valueabi"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes ANSI escape sequences so assertions can target structure.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// firstPayloadRowCells returns the trimmed contents of the first payload row
// in out. A payload row is a │-delimited line immediately followed by a
// bottom border (└). Returns nil if no payload row is found. Used to assert on
// the rendered payload of a specific tape word without relying on a "=" prefix.
func firstPayloadRowCells(out string) []string {
	lines := strings.Split(out, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "\u2502") || !strings.HasPrefix(lines[i+1], "\u2514") {
			continue
		}
		parts := strings.Split(lines[i], "\u2502")
		// parts[0] is "" (before the leading │); drop it and the trailing "".
		if len(parts) < 3 {
			return nil
		}
		cells := parts[1 : len(parts)-1]
		trimmed := make([]string, len(cells))
		for j, c := range cells {
			trimmed[j] = strings.TrimSpace(c)
		}
		return trimmed
	}
	return nil
}

// cellAt returns cells[i] or "" if i is out of range.
func cellAt(cells []string, i int) string {
	if i >= 0 && i < len(cells) {
		return cells[i]
	}
	return ""
}

// distinctSegments counts the distinct non-negative segment ids in segOf.
func distinctSegments(segOf []int) int {
	seen := map[int]struct{}{}
	for _, id := range segOf {
		if id >= 0 {
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func TestDiagram_EmptyValue(t *testing.T) {
	var v Value
	if got := v.TapeDiagram(); got != "" {
		t.Errorf("zero Value TapeDiagram() = %q, want empty", got)
	}
}

func TestDiagram_Contiguous(t *testing.T) {
	v := makeTapeValue()
	out := stripANSI(v.TapeDiagram())

	// Tag chars appear in the tag row.
	for _, want := range []string{"{", "\"", "l", "]", "}"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagram missing tag %q in:\n%s", want, out)
		}
	}
	// Number pair renders the bare value in the payload row (Int64 word 4's
	// value is 7, stored at word 5). No "=" prefix on the value cell.
	cells := firstPayloadRowCells(out)
	if got := cellAt(cells, 5); got != "7" {
		t.Errorf("word 5 payload = %q, want 7 in:\n%s", got, out)
	}
	// Container payloads render as "<close><count>" with each field in its
	// own color. After stripANSI the digits concatenate: obj close=15 count=5
	// -> "155", arr close=12 count=2 -> "122".
	if !strings.Contains(out, "155") {
		t.Errorf("diagram missing obj payload 155 (close=15 count=5) in:\n%s", out)
	}
	if !strings.Contains(out, "122") {
		t.Errorf("diagram missing arr payload 122 (close=12 count=2) in:\n%s", out)
	}
	// Decoded strings appear in payload cells.
	if !strings.Contains(out, "id") || !strings.Contains(out, "abc") {
		t.Errorf("diagram missing decoded string payloads in:\n%s", out)
	}
	// No seam in a contiguous tape.
	if strings.Contains(out, "\u2192") {
		t.Errorf("contiguous tape should have no jump marker in:\n%s", out)
	}
	// base==tidx (both 0) renders the combined marker.
	if !strings.Contains(out, "\u25c6") {
		t.Errorf("diagram missing base=tidx marker \u25c6 in:\n%s", out)
	}
	// Exactly one logical segment (contiguous).
	segOf := computeSegments(&v, v.desc.Doc.Tape)
	if got := distinctSegments(segOf); got != 1 {
		t.Errorf("contiguous tape has %d segments, want 1", got)
	}
	// Header reports tape length and coordinates.
	if !strings.Contains(out, "tape: 16 words") {
		t.Errorf("diagram header missing word count in:\n%s", out)
	}
	if !strings.Contains(out, "base=0") || !strings.Contains(out, "tidx=0") || !strings.Contains(out, "end=16") {
		t.Errorf("diagram header missing coordinates in:\n%s", out)
	}
}

func TestDiagram_JumpTape(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagStr    = uint64('"') << 56
		tagInt64  = uint64('l') << 56
		tagNull   = uint64('n') << 56
		tagTrue   = uint64('t') << 56
		tagSeam   = uint64(1) << 63
	)
	strPack := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }
	// Both views advance the same distance: this tape has one consumer, so the
	// distinction the two distance fields exist for does not arise here.
	seam := func(d uint64) uint64 { return tagSeam | d | (d << 31) }

	// A seam-threaded object with one member before the seam and one after:
	//   0 {        obj, close=10, count=2
	//   1 "k1"     key (str off=0 len=2)
	//   2 l        int tag
	//   3 7        value (seg 0)
	//   4 J(3)     seam, both views advance 3 -> target 7 (seg 0; words 5,6 are gap)
	//   5 n        gap (foreign atom)
	//   6 t        gap (foreign atom)
	//   7 "k2"    key (str off=2 len=2) [new segment]
	//   8 l        int tag
	//   9 9        value (seg 1)
	//  10 }        obj end
	strArena, off := testStringArena("k1", "k2")
	tape := []uint64{
		tagObjBeg | 10 | (2 << 32), // 0
		strPack(off[0], 2),         // 1: "k1"
		tagInt64,                   // 2
		7,                          // 3
		seam(3),                    // 4: seam to 7
		tagNull,                    // 5: gap
		tagTrue,                    // 6: gap
		strPack(off[1], 2),         // 7: "k2"
		tagInt64,                   // 8
		9,                          // 9
		tagObjEnd,                  // 10
	}
	v := testValue(&valueabi.Doc{Tape: tape, StrArena: strArena})

	segOf := computeSegments(&v, tape)
	if got := distinctSegments(segOf); got != 2 {
		t.Errorf("jump tape has %d segments, want 2 (segOf=%v)", got, segOf)
	}
	if segOf[5] != -1 || segOf[6] != -1 {
		t.Errorf("gap words 5,6 should be dim (-1), got segOf[5]=%d segOf[6]=%d", segOf[5], segOf[6])
	}
	if segOf[7] != 1 {
		t.Errorf("jump target word 7 should start segment 1, got %d", segOf[7])
	}

	out := stripANSI(v.TapeDiagram())
	if !strings.Contains(out, "J") {
		t.Errorf("diagram missing J tag in:\n%s", out)
	}
	if !strings.Contains(out, "\u21923") { // →3
		t.Errorf("diagram missing jump distance \u21923 in:\n%s", out)
	}
	// Both number pairs render their bare values (word 3=7, word 9=9).
	cells := firstPayloadRowCells(out)
	if got := cellAt(cells, 3); got != "7" {
		t.Errorf("word 3 payload = %q, want 7 in:\n%s", got, out)
	}
	if got := cellAt(cells, 9); got != "9" {
		t.Errorf("word 9 payload = %q, want 9 in:\n%s", got, out)
	}
	// base==tidx at word 0 -> combined marker.
	if !strings.Contains(out, "\u25c6") {
		t.Errorf("diagram missing base=tidx marker in:\n%s", out)
	}
	// Header reports the tape length.
	if !strings.Contains(out, "tape: 11 words") {
		t.Errorf("diagram header missing word count in:\n%s", out)
	}
}

func TestDiagram_ScalarRoot(t *testing.T) {
	const tagInt64 = uint64('l') << 56
	tape := []uint64{tagInt64, 42}
	v := testValue(&valueabi.Doc{Tape: tape})

	out := stripANSI(v.TapeDiagram())
	// Scalar root renders the bare value 42 in the payload row (no "=" prefix).
	cells := firstPayloadRowCells(out)
	if got := cellAt(cells, 1); got != "42" {
		t.Errorf("word 1 payload = %q, want 42 in:\n%s", got, out)
	}
	if !strings.Contains(out, "tape: 2 words") {
		t.Errorf("diagram header missing word count in:\n%s", out)
	}
	// Value word renders a blank tag cell, never an unknown "?" tag.
	if strings.Contains(out, "?") {
		t.Errorf("value word should not render as unknown tag in:\n%s", out)
	}
}

func TestDiagram_StringSanitization(t *testing.T) {
	// A string containing control, ESC, and multi-byte UTF-8 must collapse to
	// ASCII-safe content so it cannot corrupt grid alignment.
	const tagStr = uint64('"') << 56
	payload := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }
	body := "a\x1b\xc2\x80z" // 'a', ESC, U+0080 (2 bytes), 'z'
	arena, _ := testStringArena(body)
	tape := []uint64{payload(0, uint32(len(body)))}
	v := testValue(&valueabi.Doc{Tape: tape, StrArena: arena})

	got := sanitizeASCII(body)
	// Each non-ASCII/control rune becomes a single '?'; the printable ASCII
	// letters survive.
	if got != "a??z" {
		t.Errorf("sanitizeASCII = %q, want %q", got, "a??z")
	}
	// The diagram must render without the raw bytes leaking through (no ESC,
	// no multi-byte sequence) and must still carry the safe letters.
	out := stripANSI(v.TapeDiagram())
	if strings.Contains(out, "\x1b") {
		t.Errorf("diagram leaked ESC byte into output:\n%s", out)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "z") {
		t.Errorf("diagram dropped safe ASCII letters in:\n%s", out)
	}
}

// countCellsPerRow returns the cell count of every │-delimited line (tag
// rows and payload rows alike). Cells are separated by │, so the cell count
// is │ occurrences minus 1.
func countCellsPerRow(t *testing.T, out string) []int {
	t.Helper()
	out = stripANSI(out)
	var counts []int
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "\u2502") {
			continue
		}
		counts = append(counts, strings.Count(line, "\u2502")-1)
	}
	return counts
}

func TestDiagram_KeyValueMarking(t *testing.T) {
	// Object with mixed value types: string, int, string. After the non-string
	// value (int), the key/value state machine must still flip back to key.
	// Regression: a prior bug left keyNext unchanged after non-string values,
	// so "type" (a key) was mis-marked as a value (which would have been "v"
	// before that suffix was dropped; now a mis-marked key looks like a bare
	// `"` indistinguishable from a value string).
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagStr    = uint64('"') << 56
		tagInt64  = uint64('l') << 56
	)
	strPack := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }
	// {"a":1,"type":"user"}
	strArena, off := testStringArena("a", "type", "user")
	tape := []uint64{
		tagObjBeg | 6 | (2 << 32), // 0: obj close=6 count=2
		strPack(off[0], 1),        // 1: "a" (key)
		tagInt64,                  // 2: int tag
		1,                         // 3: value
		strPack(off[1], 4),        // 4: "type" (key)
		strPack(off[2], 4),        // 5: "user" (value)
		tagObjEnd,                 // 6: }
	}
	v := testValue(&valueabi.Doc{Tape: tape, StrArena: strArena})
	out := stripANSI(v.TapeDiagramCols(32))

	// Find the tag row (the one carrying "k markers).
	lines := strings.Split(out, "\n")
	var tagRow string
	for _, l := range lines {
		if strings.Contains(l, "\u2502") && strings.Contains(l, "\"k") {
			tagRow = l
			break
		}
	}
	if tagRow == "" {
		t.Fatalf("no tag row with \"k found in:\n%s", out)
	}
	// "a" at word 1 is a key (marked "k). "type" at word 4 must also be a key
	// (marked "k), NOT a bare " (which is what a value string renders as).
	cells := strings.Split(tagRow, "\u2502")
	// cells[0] is leading empty, cells[1] is word 0, cells[5] is word 4 (type).
	if len(cells) < 6 {
		t.Fatalf("tag row has too few cells: %d", len(cells))
	}
	typeCell := strings.TrimSpace(cells[5])
	if !strings.Contains(typeCell, "\"k") {
		t.Errorf("word 4 (type) should be marked \"k (key), got %q in:\n%s", typeCell, tagRow)
	}
}

// TestDiagram_GapPairSkipsState is a regression test for the key/value state
// machine desyncing on gap words. When a seam leaps over a gap that contains an
// orphaned number pair (the variant bind widened the seam over its key), the pair
// must not flip the state. Without this, the first real key after the seam is
// mis-marked as a value, and its value is mis-marked as a key.
//
// Tape layout (mirrors the merged-tape structure the variant bind produces):
//
//	0 {        obj, close=8, count=2
//	1 "k1"     key
//	2 "v1"     value
//	3 J(→6)    seam, both views advance 3; gap = words 4,5
//	4 l        gap: orphaned number (its key was leapt over)
//	5 99       gap: number value
//	6 "k2"     key (post-jump, must stay "k)
//	7 "v2"     value (must stay bare ")
//	8 }        obj end
func TestDiagram_GapPairSkipsState(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagStr    = uint64('"') << 56
		tagInt64  = uint64('l') << 56
		tagSeam   = uint64(1) << 63
	)
	strPack := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }
	seam := func(d uint64) uint64 { return tagSeam | d | (d << 31) }
	strArena, off := testStringArena("k1", "v1", "k2", "v2")
	tape := []uint64{
		tagObjBeg | 8 | (2 << 32), // 0: obj close=8 count=2
		strPack(off[0], 2),        // 1: "k1" (key)
		strPack(off[1], 2),        // 2: "v1" (value)
		seam(3),                   // 3: seam to 6; gap = 4,5
		tagInt64,                  // 4: gap orphaned number tag
		99,                        // 5: gap number value
		strPack(off[2], 2),        // 6: "k2" (key, post-jump)
		strPack(off[3], 2),        // 7: "v2" (value)
		tagObjEnd,                 // 8: }
	}
	v := testValue(&valueabi.Doc{Tape: tape, StrArena: strArena})

	// Gap words 4,5 must be marked gap.
	gap := computeGaps(tape, valueabi.SeamViewA)
	if !gap[4] || !gap[5] {
		t.Errorf("gap[4,5] = [%v,%v], want [true,true]", gap[4], gap[5])
	}
	if gap[6] {
		t.Errorf("gap[6] = true, want false (the seam target is on-path)")
	}

	out := stripANSI(v.TapeDiagramCols(32))
	lines := strings.Split(out, "\n")
	var tagRow string
	for _, l := range lines {
		if strings.Contains(l, "\u2502") && strings.Contains(l, "\"k") {
			tagRow = l
			break
		}
	}
	if tagRow == "" {
		t.Fatalf("no tag row with \"k found in:\n%s", out)
	}
	cells := strings.Split(tagRow, "\u2502")
	if len(cells) < 8 {
		t.Fatalf("tag row has too few cells: %d in:\n%s", len(cells), tagRow)
	}
	// cells[1]=word0, cells[2]=word1(k1), cells[3]=word2(v1),
	// cells[4]=word3(J), cells[5]=word4(l gap), cells[6]=word5(99 gap),
	// cells[7]=word6(k2 key), cells[8]=word7(v2 value).
	k2Cell := strings.TrimSpace(cells[7])
	v2Cell := strings.TrimSpace(cells[8])
	if !strings.Contains(k2Cell, "\"k") {
		t.Errorf("word 6 (k2) post-jump should be key \"k, got %q in:\n%s", k2Cell, tagRow)
	}
	if strings.Contains(v2Cell, "\"k") {
		t.Errorf("word 7 (v2) should be bare \" (value), got %q in:\n%s", v2Cell, tagRow)
	}
}

func TestDiagram_ColsAdapts(t *testing.T) {
	v := makeTapeValue() // 16 words
	// Narrow: cols=4 (the minimum). The 16-word tape + number pairing should
	// wrap into several short rows, each ≤ 4 cells.
	narrow := stripANSI(v.TapeDiagramCols(4))
	for _, n := range countCellsPerRow(t, narrow) {
		if n > 4 {
			t.Errorf("cols=4 produced a row with %d cells, want ≤ 4", n)
		}
	}
	if !strings.Contains(narrow, "cols=4") {
		t.Errorf("header should report cols=4:\n%s", narrow)
	}

	// Wide: cols=32 (the default). The 16-word tape fits in one row.
	// Cells = 16: 14 single-word units + 1 number pair (tag+value = 2 cells).
	wide := stripANSI(v.TapeDiagramCols(32))
	rows := countCellsPerRow(t, wide)
	if len(rows) < 1 {
		t.Fatalf("cols=32 produced no tag rows")
	}
	if rows[0] != 16 {
		t.Errorf("cols=32 first row has %d cells, want 16", rows[0])
	}
	if !strings.Contains(wide, "cols=32") {
		t.Errorf("header should report cols=32:\n%s", wide)
	}
}

func TestDiagram_ColsClamps(t *testing.T) {
	v := makeTapeValue()
	// Below minimum: clamp to diagramMinCols (4).
	out := stripANSI(v.TapeDiagramCols(1))
	if !strings.Contains(out, "cols=4") {
		t.Errorf("cols=1 should clamp to cols=4:\n%s", out)
	}
	// Above maximum: clamp to diagramMaxCols (64).
	out = stripANSI(v.TapeDiagramCols(10000))
	if !strings.Contains(out, "cols=64") {
		t.Errorf("cols=10000 should clamp to cols=64:\n%s", out)
	}
}
