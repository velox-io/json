package bind

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// buildAnyDoc renders a map[string]any document whose values are nested
// containers (arrays-of-arrays, objects-with-arrays), so each parse exercises
// the any SCC group: []any element backings, map[string]any hmap headers, and
// the *hmap dirPtrs that pin them. This is the shape that triggered the v1
// cross-parse live-heap avalanche (126MB -> 9.3GB) before detach.
func buildAnyDoc(n int) []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":`)
		if i%2 == 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`,[`)
			b.WriteString(strconv.Itoa(i * 2))
			b.WriteString(`,[`)
			b.WriteString(strconv.Itoa(i * 3))
			b.WriteString(`]]]`)
		} else {
			b.WriteString(`{"n":`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`,"a":[`)
			b.WriteString(strconv.Itoa(i))
			b.WriteByte(',')
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(`],"o":{"x":`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`}}`)
		}
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// TestAnyLeakBoundedAcrossParses verifies the v2 detach cadence keeps live
// heap bounded across many parses of an any-heavy document. Without detach,
// the SlotClass pool pins *hmap dirPtrs and []any backings across parses,
// extending the backing dependency chain without bound; with detach, every
// slotDetachK parses the group's backing is dropped, bounding the chain.
func TestAnyLeakBoundedAcrossParses(t *testing.T) {
	data := buildAnyDoc(256)

	run := func(n int) uint64 {
		for range n {
			var v any
			if err := Unmarshal(data, &v); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
		}
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	run(64) // warmup: prime allocator + cross several detach boundaries
	mid := run(1024)
	end := run(4096)

	// Steady state: the second batch must not exceed the first by more than a
	// small tolerance. The v1 leak grew live heap into the GBs across parses;
	// detach bounds it to ~slotDetachK parses' worth of backings.
	if end > mid+50*1024*1024 {
		t.Errorf("live heap unbounded across parses: mid=%d end=%d (delta=%d)", mid, end, end-mid)
	}
}

// TestRecursiveStructLeakBounded covers a directly self-recursive pointer
// type (*listNode via Next field): the pointee slot is a single-member
// self-loop SCC group, detached on the same K cadence. Live heap must stay
// bounded.
func TestRecursiveStructLeakBounded(t *testing.T) {
	type listNode struct {
		V    int       `json:"v"`
		Next *listNode `json:"next"`
	}
	// 64-deep linked list: {"v":0,"next":{"v":1,"next":...{"v":63,"next":null}...}}
	var b strings.Builder
	for i := range 64 {
		b.WriteString(`{"v":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"next":`)
	}
	b.WriteString(`null`)
	b.WriteString(strings.Repeat(`}`, 64))
	data := []byte(b.String())

	run := func(n int) uint64 {
		for range n {
			var got *listNode
			if err := Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
		}
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	run(32)
	mid := run(512)
	end := run(2048)
	if end > mid+20*1024*1024 {
		t.Errorf("recursive-struct live heap unbounded: mid=%d end=%d (delta=%d)", mid, end, end-mid)
	}
}
