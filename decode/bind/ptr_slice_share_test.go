package bind

import (
	"bytes"
	"fmt"
	"testing"
)

// sharedChild is the element/pointee type T shared by both a *T field and a
// []T field. sharedClassRoot exercises the SlotClass sharing described in
// vbind/build.go: registerSlot dedups by element UniType, so *T pointees and
// []T backings of the same T draw from one bump arena (sc.Offset). The test
// guards against the two consumers clobbering each other's region.
type sharedChild struct {
	A int     `json:"a"`
	B string  `json:"b"`
	C bool    `json:"c"`
	D float64 `json:"d"`
}

type sharedClassRoot struct {
	P  *sharedChild  `json:"p"`
	S  []sharedChild `json:"s"`
	S2 []sharedChild `json:"s2"`
}

// buildSharedJSON builds a JSON document with a pointer field P and two large
// slices S/S2 of the same element type T. n controls the slice length; passing
// a large n forces the slice backing through several SLICE_GROW yields and the
// standalone bypass path (needCap > SlotBatchMax).
func buildSharedJSON(n int, seed int64) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `{"p":{"a":%d,"b":"ptr","c":true,"d":1.5},"s":[`, seed%1000)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"str-%d-é","c":%t,"d":%g}`, i, i, i%2 == 0, float64(i)+0.25)
	}
	buf.WriteString(`],"s2":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"s2-%d","c":%t,"d":%g}`, i*2, i, i%3 == 0, float64(i)*2)
	}
	buf.WriteString(`]}`)
	return buf.String()
}

// TestDiffPtrAndSliceSameClass validates that a *T field and []T slices of the
// identical element type T decode correctly when they share one SlotClass bump
// arena. The pointer pointee and the slice backings must not clobber each other.
//
// P is parsed before the large slices, so the pointee lands at the front of the
// shared arena and the slices carve their (possibly bypassed) backings after it
// via repeated SLICE_GROW + memmove.
//
// Runs through parity3 so the tape-bind sub-routine (UnmarshalValue) covers the
// same shared SlotClass state; the bypass path's standalone arrays must survive
// the cross-path cursor reset (t_array_continue's off < sc->limit guard).
func TestDiffPtrAndSliceSameClass(t *testing.T) {
	const n = 300 // needCap exceeds SlotBatchMax to exercise the standalone bypass path
	parity3[sharedClassRoot](t, "PtrAndSliceSameClass", buildSharedJSON(n, 1))
}

// TestDiffPtrAndSliceSameClassReversed puts the slices BEFORE the pointer in the
// JSON so the slices carve the arena first (front of block) and the *T pointee
// is bumped later; verifies the pointee survives subsequent slice regrows.
func TestDiffPtrAndSliceSameClassReversed(t *testing.T) {
	const n = 300
	var buf bytes.Buffer
	buf.WriteString(`{"s":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"s%d","c":%t,"d":%g}`, i, i, i%3 == 0, float64(i))
	}
	buf.WriteString(`],"s2":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"s2-%d","c":%t,"d":%g}`, i*2, i, i%2 == 1, float64(i)*2)
	}
	buf.WriteString(`],"p":{"a":999,"b":"tail-ptr","c":false,"d":9.5}}`)
	parity3[sharedClassRoot](t, "PtrAndSliceSameClassReversed", buf.String())
}

// TestDiffPtrAndSliceSameClassStress varies slice length across the bypass
// boundary and interleaves a pointer field, to surface any arena-overlap
// corruption that depends on allocation ordering. Each iteration crosses
// slotBatchMax so the standalone bypass path fires on every parse.
func TestDiffPtrAndSliceSameClassStress(t *testing.T) {
	for i := range 50 {
		n := 100 + (i*37)%400 // vary length to cross the bypass boundary
		parity3[sharedClassRoot](t, "PtrAndSliceSameClassStress",
			buildSharedJSON(n, int64(i+1)))
	}
}

// sharedPtrChild is used for the slice-of-pointers variant, to confirm that
// []*T (pointer class) and *T (pointee class) and []T (value class) keep their
// arenas straight.
type sharedPtrRoot struct {
	P  *sharedChild   `json:"p"`
	S  []sharedChild  `json:"s"`
	Ps []*sharedChild `json:"ps"`
}

// TestDiffPtrSliceAndSlicePtrSameClass combines a *T field (class T), []T fields
// (class T, shared with P), and a []*T field (class *T) so both the shared
// value class and the pointer class are exercised together in one document.
func TestDiffPtrSliceAndSlicePtrSameClass(t *testing.T) {
	const n = 300
	var buf bytes.Buffer
	buf.WriteString(`{"p":{"a":1,"b":"p","c":true,"d":1.0},"s":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"s%d","c":%t,"d":%g}`, i, i, i%2 == 0, float64(i))
	}
	buf.WriteString(`],"ps":[`)
	for i := range 250 {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"ps%d","c":%t,"d":%g}`, i*2, i, i%2 == 1, float64(i)*2)
	}
	buf.WriteString(`]}`)
	parity3[sharedPtrRoot](t, "PtrSliceAndSlicePtrSameClass", buf.String())
}

// nestChild exercises the deep-recursion pattern: a []T whose element type T
// itself carries a *T sub-pointer that allocates in the SAME SlotClass arena.
// As the slice is parsed, each element's sub-pointer bumps the shared frontier
// forward; the slice must then regrow (memmove its own backing to the new
// frontier) while its descendants' pointees stay put at lower offsets. Two such
// slices (A early, B later) plus a *T field all share class T, forcing the
// "early slice grows again after deep descent" ordering.
type nestChild struct {
	ID  int        `json:"id"`
	Sub *nestChild `json:"sub"`
	Tag string     `json:"tag"`
}

type nestRoot struct {
	A []nestChild `json:"a"`
	B []nestChild `json:"b"`
	P *nestChild  `json:"p"`
}

func buildNestJSON(n int, seed int64) string {
	var buf bytes.Buffer
	r := seed
	next := func() int {
		r = r*1103515245 + 12345
		return int(r % 1000)
	}
	var writeChild func(depth int)
	writeChild = func(depth int) {
		fmt.Fprintf(&buf, `{"id":%d,"tag":"t%d"`, next(), next())
		if depth > 0 && next()%2 == 0 {
			buf.WriteString(`,"sub":`)
			writeChild(depth - 1)
		} else {
			buf.WriteString(`,"sub":null`)
		}
		buf.WriteByte('}')
	}
	buf.WriteString(`{"a":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeChild(2)
	}
	buf.WriteString(`],"b":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeChild(2)
	}
	buf.WriteString(`],"p":`)
	writeChild(1)
	buf.WriteByte('}')
	return buf.String()
}

// TestDiffNestedSliceGrowsAfterDescent verifies that a []T slice sharing its
// SlotClass arena with the *T sub-pointers of its own elements (and with other
// slices/fields of T) decodes correctly even as the early slice regrows after
// its descendants have pushed the bump frontier forward.
func TestDiffNestedSliceGrowsAfterDescent(t *testing.T) {
	const n = 300
	for iter := range 20 {
		parity3[nestRoot](t, "NestedSliceGrowsAfterDescent",
			buildNestJSON(n, int64(iter*7+3)))
	}
}
