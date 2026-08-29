package bind

import (
	"fmt"
	"strings"
	"testing"
)

// A slice takes its backing by borrowing the rest of its SlotClass block, and
// array_begin charges the whole borrowed tail immediately so the cursor is never
// behind the bytes the slice went on to write. These tests are about what
// happens when a parse dies mid-slice: array_close never runs, so nothing hands
// the tail back, and every byte the dead parse touched has to stay out of
// circulation. Charging at close instead of at borrow leaves the cursor at the
// start of the abandoned region, and the next parse carves its elements out of
// memory the dead one already wrote.
//
// Both tests interleave a failing parse with a succeeding one on the same pooled
// Parser, because no single input reaches the state: the dirty bytes are
// produced by the parse that errors and consumed by the parse after it.

// The element struct is deliberately wider than the JSON provides, so leaked
// bytes surface as a field the input never mentioned.
type slotReuseElem struct {
	A string `json:"a"`
	B string `json:"b"`
	C int    `json:"c"`
}

type slotReuseRoot struct {
	Items []slotReuseElem `json:"items"`
}

// TestErroredParseLeavesNoDirtySliceSlots is the observable half: a parse binds
// several struct elements and then hits a syntax error, and the next parse's
// elements omit most fields. Omitted fields must read as zero rather than as
// whatever the dead parse left at that address.
func TestErroredParseLeavesNoDirtySliceSlots(t *testing.T) {
	p, err := NewParser[slotReuseRoot]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	// Fully populated elements, then a syntax error before the closing ']'. The
	// elements ahead of the error are bound, so the abandoned region holds real
	// strings and ints rather than zeros.
	var dirty strings.Builder
	dirty.WriteString(`{"items":[`)
	for i := range 8 {
		if i > 0 {
			dirty.WriteByte(',')
		}
		fmt.Fprintf(&dirty, `{"a":"leak-a-%d","b":"leak-b-%d","c":%d}`, i, i, 1000+i)
	}
	dirty.WriteString(`,{"a":"trailing"` + "\x00")
	bad := dirty.String()

	// Only "a" is present, so B and C must come back zeroed for every element.
	var clean strings.Builder
	clean.WriteString(`{"items":[`)
	for i := range 8 {
		if i > 0 {
			clean.WriteByte(',')
		}
		fmt.Fprintf(&clean, `{"a":"ok-%d"}`, i)
	}
	clean.WriteString(`]}`)
	good := clean.String()

	// Several rounds: the first errored parse may consume a pristine block, and
	// the leak only shows once a later parse lands on the abandoned region.
	for round := range 16 {
		var sink slotReuseRoot
		if err := p.Unmarshal([]byte(bad), &sink); err == nil {
			t.Fatalf("round %d: malformed input parsed without error", round)
		}

		var got slotReuseRoot
		if err := p.Unmarshal([]byte(good), &got); err != nil {
			t.Fatalf("round %d: clean parse: %v", round, err)
		}
		if len(got.Items) != 8 {
			t.Fatalf("round %d: len(Items) = %d, want 8", round, len(got.Items))
		}
		for i, it := range got.Items {
			if want := fmt.Sprintf("ok-%d", i); it.A != want {
				t.Fatalf("round %d: Items[%d].A = %q, want %q", round, i, it.A, want)
			}
			if it.B != "" {
				t.Fatalf("round %d: Items[%d].B = %q, want empty (stale data from the errored parse)",
					round, i, it.B)
			}
			if it.C != 0 {
				t.Fatalf("round %d: Items[%d].C = %d, want 0 (stale data from the errored parse)",
					round, i, it.C)
			}
		}
	}
}

type nestedSliceRoot struct {
	Rows [][]int `json:"rows"`
}

// TestSliceSlotCursorNeverExposesWrittenBytes asserts the invariant itself, not
// a symptom. A rewind is not merely a stale read: the bytes below the cursor may
// already be published, their addresses sitting in a slice header the caller
// still holds, so re-issuing them lets a later parse write into an earlier
// parse's result.
//
// The cursor is allowed to move back, because the error path reclaims the tail a
// dead parse borrowed but never wrote (sealOpenSlices). What it may never do is
// return to a position some slice actually wrote through. So this watches the
// written high-water mark per installed block rather than the raw cursor.
//
// It also pins the two accounting fields together. array_begin takes the backing
// base from Offset but the capacity from Cap - Len, so a cursor moved without its
// matching Len hands out a capacity reaching past the end of the block. That
// produced real out-of-block writes (Offset > Limit) while this was being
// written, which is why the check is here and not left to review.
//
// [][]int is the shape to test on. Its elements are slice headers, and
// array_begin reads an element's header before descending: a non-nil Data is
// taken as a backing installed earlier and written in place. This is also the
// case a per-element memset could never have covered, since the damage is in the
// header the element carries, not in the element's own bytes.
func TestSliceSlotCursorNeverExposesWrittenBytes(t *testing.T) {
	p, err := NewParser[nestedSliceRoot]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	// Per slot, the furthest byte handed out in the currently installed block.
	// Reset when a fresh block appears: the old bytes are retained, not
	// re-issued, so they constrain nothing in the new block.
	type mark struct {
		block   uintptr
		written uint32
	}
	marks := make([]mark, len(p.alloc.Slots))

	// Any Offset observed after a parse is evidence that the bytes below it were
	// handed to a slice, so the high-water mark is the max Offset seen while this
	// block stayed installed. The cursor must never drop below it.
	observe := func(tag string) {
		for i := range p.alloc.Slots {
			sc := &p.alloc.Slots[i]
			if !sc.IsBumpTail() {
				continue
			}
			if sc.Offset > sc.Limit {
				t.Fatalf("%s: slot %d wrote past its block: Offset=%d Limit=%d",
					tag, i, sc.Offset, sc.Limit)
			}
			if sc.Len > sc.Cap {
				t.Fatalf("%s: slot %d Len=%d exceeds Cap=%d; capacity handed out past the block",
					tag, i, sc.Len, sc.Cap)
			}
			// Offset and Len must name the same boundary, or array_begin's
			// Cap-Len capacity disagrees with its Offset-derived base.
			if sc.ElemSize > 0 && sc.Offset != sc.Len*sc.ElemSize {
				t.Fatalf("%s: slot %d accounting split: Offset=%d but Len*ElemSize=%d",
					tag, i, sc.Offset, sc.Len*sc.ElemSize)
			}
			m := &marks[i]
			if m.block != uintptr(sc.Block) {
				*m = mark{block: uintptr(sc.Block)}
			}
			if sc.Offset < m.written {
				t.Fatalf("%s: slot %d cursor dropped to %d, below the %d bytes already handed out",
					tag, i, sc.Offset, m.written)
			}
			m.written = sc.Offset
		}
	}

	// Two rows, so the outer slice fits inside one block with room to spare.
	// That room is the point: a successful parse hands its unused tail back, the
	// next parse borrows that tail and dies inside it, and the parse after that
	// borrows the same bytes again. A slice big enough to force a grow would miss
	// this, because the grow path installs a fresh block and reserves all of it.
	const rowCount = 2

	var dirty strings.Builder
	dirty.WriteString(`{"rows":[`)
	for i := range rowCount {
		if i > 0 {
			dirty.WriteByte(',')
		}
		fmt.Fprintf(&dirty, `[%d,%d,%d,%d,%d,%d,%d,%d]`, i, i+1, i+2, i+3, i+4, i+5, i+6, i+7)
	}
	dirty.WriteString(`,[9,9` + "\x00")
	bad := dirty.String()

	const good = `{"rows":[[7],[8]]}`

	for round := range 16 {
		var sink nestedSliceRoot
		if err := p.Unmarshal([]byte(bad), &sink); err == nil {
			t.Fatalf("round %d: malformed input parsed without error", round)
		}
		observe(fmt.Sprintf("round %d errored parse", round))

		var got nestedSliceRoot
		if err := p.Unmarshal([]byte(good), &got); err != nil {
			t.Fatalf("round %d: clean parse: %v", round, err)
		}
		observe(fmt.Sprintf("round %d parse after error", round))

		if len(got.Rows) != rowCount {
			t.Fatalf("round %d: len(Rows) = %d, want %d", round, len(got.Rows), rowCount)
		}
		for i, row := range got.Rows {
			if want := 7 + i; len(row) != 1 || row[0] != want {
				t.Fatalf("round %d: Rows[%d] = %v, want [%d]", round, i, row, want)
			}
		}
	}
}
