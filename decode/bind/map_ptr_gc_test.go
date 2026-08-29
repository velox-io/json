package bind

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// mapStrSliceInt is a map whose value type is a slice. The slice header stages
// inline in the noscan map buffer, while its backing array is allocated from a
// SlotClass block, so it stresses the same orphaned-block hazard as pointer
// values.
type mapStrSliceInt struct {
	M map[string][]int `json:"m"`
}

// gcHammerParse runs many parses of data into a fresh T while background
// goroutines hammer runtime.GC(), then hands each decoded value to check.
//
// It targets the noscan map buffer invariant for pointer-bearing map values.
// A map value's heap payload (a *T pointee, a slice backing array) is allocated
// from a SlotClass block, but the pointer/header is staged inline in the
// map buffer ([]byte, noscan) until the map drain. Map close does not drain
// (iron rule), so the payload is reachable only through noscan memory from
// map_value until document_end's drain.
//
// When the next value needs a fresh batch, AllocFromSlot -> growBatch drops the
// Allocator's reference to the old block (appending it to the retained chain)
// and calls mallocgc (a GC safepoint). The retained chain keeps the old block
// alive until the drain copies staged pointers into the *hmap; without it, a
// GC completing in that window would reclaim the old block along with every
// payload still staged in the map buffer, and the drain would copy dangling
// pointers into the map.
//
// A post-parse runtime.GC() (as in gc_test.go) cannot see this: the finished
// map roots every payload. Reproduction requires a GC completing mid-parse,
// which the background hammer forces across many iterations.
func gcHammerParse[T any](t *testing.T, data []byte, check func(iter int, got *T)) {
	t.Helper()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				runtime.GC()
			}
		}()
	}
	defer func() {
		stop.Store(true)
		wg.Wait()
	}()

	for iter := range 200 {
		var got T
		if err := Unmarshal(data, &got); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", iter, err)
		}
		check(iter, &got)
		if t.Failed() {
			return
		}
	}
}

// buildMapDoc renders {"m":{"k0":<val(0)>,...,"k<n-1>":<val(n-1)>}}. n large
// enough to overflow the value SlotClass batch many times per parse.
func buildMapDoc(n int, val func(i int) string) []byte {
	var b strings.Builder
	b.WriteString(`{"m":{`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":`)
		b.WriteString(val(i))
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

const mapGCEntries = 4096

// TestMapPtrValueGCDuringParse covers map[string]*int: an *int pointee is
// allocated from a SlotClass block and its address staged inline in the noscan
// map buffer until drain.
func TestMapPtrValueGCDuringParse(t *testing.T) {
	data := buildMapDoc(mapGCEntries, func(i int) string { return strconv.Itoa(i) })
	gcHammerParse(t, data, func(iter int, got *mapStrPtrInt) {
		if len(got.M) != mapGCEntries {
			t.Fatalf("iter %d: len=%d want %d", iter, len(got.M), mapGCEntries)
		}
		for i := range mapGCEntries {
			key := "k" + strconv.Itoa(i)
			p, ok := got.M[key]
			if !ok {
				t.Fatalf("iter %d: missing key %q", iter, key)
			}
			if p == nil {
				t.Fatalf("iter %d: key %q nil pointer (pointee reclaimed?)", iter, key)
			}
			if *p != i {
				t.Fatalf("iter %d: key %q = %d want %d (pointee corrupted by GC)", iter, key, *p, i)
			}
		}
	})
}

// TestMapPtrStructValueGCDuringParse covers map[string]*innerVal: the pointee
// is a struct with a string field, so a reclaimed block corrupts both the int
// and the string body.
func TestMapPtrStructValueGCDuringParse(t *testing.T) {
	data := buildMapDoc(mapGCEntries, func(i int) string {
		return `{"x":` + strconv.Itoa(i) + `,"s":"v` + strconv.Itoa(i) + `"}`
	})
	gcHammerParse(t, data, func(iter int, got *mapStrPtrStruct) {
		if len(got.M) != mapGCEntries {
			t.Fatalf("iter %d: len=%d want %d", iter, len(got.M), mapGCEntries)
		}
		for i := range mapGCEntries {
			key := "k" + strconv.Itoa(i)
			p, ok := got.M[key]
			if !ok {
				t.Fatalf("iter %d: missing key %q", iter, key)
			}
			if p == nil {
				t.Fatalf("iter %d: key %q nil pointer (pointee reclaimed?)", iter, key)
			}
			wantS := "v" + strconv.Itoa(i)
			if p.X != i || p.S != wantS {
				t.Fatalf("iter %d: key %q = {%d %q} want {%d %q} (pointee corrupted by GC)",
					iter, key, p.X, p.S, i, wantS)
			}
		}
	})
}

// TestMapSliceValueGCDuringParse covers map[string][]int: the slice header
// stages inline in the noscan map buffer while its backing array lives in a
// SlotClass block orphaned on the next batch grow.
func TestMapSliceValueGCDuringParse(t *testing.T) {
	data := buildMapDoc(mapGCEntries, func(i int) string {
		return "[" + strconv.Itoa(i) + "," + strconv.Itoa(i+1) + "," + strconv.Itoa(i+2) + "]"
	})
	gcHammerParse(t, data, func(iter int, got *mapStrSliceInt) {
		if len(got.M) != mapGCEntries {
			t.Fatalf("iter %d: len=%d want %d", iter, len(got.M), mapGCEntries)
		}
		for i := range mapGCEntries {
			key := "k" + strconv.Itoa(i)
			s, ok := got.M[key]
			if !ok {
				t.Fatalf("iter %d: missing key %q", iter, key)
			}
			want := []int{i, i + 1, i + 2}
			if len(s) != 3 || s[0] != want[0] || s[1] != want[1] || s[2] != want[2] {
				t.Fatalf("iter %d: key %q = %v want %v (backing corrupted by GC)", iter, key, s, want)
			}
		}
	})
}

// mapNode is a recursively-nested map value type: map[string]*mapNode where the
// pointee itself holds a map[string]*mapNode. All maps share the single map
// buffer, so their regions interleave within it and a mid-parse FLUSH must
// compact live in-prog regions and fix up the pointers the C machine holds
// into them.
type mapNode struct {
	V int                 `json:"v"`
	M map[string]*mapNode `json:"m"`
}

// TestMapRecursiveSameVGCDuringParse targets the intra-buffer interleave +
// in-prog compaction/fixup path with a recursive same-value-type map under the
// GC hammer. This is the case with no prior coverage: nested maps of the SAME V
// share a buffer, so a live outer region and a live inner region coexist in it,
// and a FLUSH relocates the outer in-prog entry (whose Value pointee is still
// being parsed) while the C machine holds a pointer into it.
func TestMapRecursiveSameVGCDuringParse(t *testing.T) {
	// Build a wide+deep recursive doc: at each level, several keys, each value a
	// nested map, a few levels deep. Wide fan-out forces region overflow (FLUSH)
	// while inner maps are live.
	var build func(depth, width int) string
	build = func(depth, width int) string {
		var b strings.Builder
		b.WriteByte('{')
		for i := range width {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"k`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`":{"v":`)
			b.WriteString(strconv.Itoa(depth*100 + i))
			b.WriteString(`,"m":`)
			if depth > 0 {
				b.WriteString(build(depth-1, width))
			} else {
				b.WriteString(`null`)
			}
			b.WriteByte('}')
		}
		b.WriteByte('}')
		return b.String()
	}
	// {"m": <recursive>} wrapped so the root is map[string]*mapNode via a field.
	type root struct {
		M map[string]*mapNode `json:"m"`
	}
	data := []byte(`{"m":` + build(4, 12) + `}`)

	// Verify a decoded node tree matches the builder.
	var verify func(t *testing.T, m map[string]*mapNode, depth, width int)
	verify = func(t *testing.T, m map[string]*mapNode, depth, width int) {
		if len(m) != width {
			t.Fatalf("depth %d: len=%d want %d", depth, len(m), width)
		}
		for i := range width {
			n := m["k"+strconv.Itoa(i)]
			if n == nil {
				t.Fatalf("depth %d: key k%d nil (pointee reclaimed?)", depth, i)
			}
			if n.V != depth*100+i {
				t.Fatalf("depth %d key k%d: V=%d want %d (corrupted by GC)", depth, i, n.V, depth*100+i)
			}
			if depth > 0 {
				verify(t, n.M, depth-1, width)
			} else if n.M != nil {
				t.Fatalf("depth 0 key k%d: M=%v want nil", i, n.M)
			}
		}
	}

	gcHammerParse(t, data, func(iter int, got *root) {
		verify(t, got.M, 4, 12)
	})
}

// umStr allocates a heap string inside UnmarshalJSON. As a non-PTR map value
// it goes through a SlotClass intermediate slot (not the noscan map buffer
// entry); the string it sets must stay rooted through the drain. This proves
// the intermediate-slot path is GC-safe.
type umStr struct {
	S string
}

func (u *umStr) UnmarshalJSON(data []byte) error {
	// Allocate a fresh heap string (not a slice of data) so GC reachability
	// depends on the SlotClass intermediate slot being scannable.
	u.S = strings.Repeat(strings.Trim(string(data), `"`), 1)
	return nil
}

type mapStrUnmarshaler struct {
	M map[string]umStr `json:"m"`
}

// TestMapUnmarshalerValueGCDuringParse exercises a map whose value type
// implements json.Unmarshaler, under the GC hammer. The closure allocates a
// heap string inside UnmarshalJSON; the SlotClass intermediate slot must keep
// it rooted through a mid-parse GC until the drain copies it into the *hmap.
func TestMapUnmarshalerValueGCDuringParse(t *testing.T) {
	data := buildMapDoc(mapGCEntries, func(i int) string {
		return `"val` + strconv.Itoa(i) + `"`
	})
	gcHammerParse(t, data, func(iter int, got *mapStrUnmarshaler) {
		if len(got.M) != mapGCEntries {
			t.Fatalf("iter %d: len=%d want %d", iter, len(got.M), mapGCEntries)
		}
		for i := range mapGCEntries {
			key := "k" + strconv.Itoa(i)
			v, ok := got.M[key]
			if !ok {
				t.Fatalf("iter %d: missing key %q", iter, key)
			}
			want := "val" + strconv.Itoa(i)
			if v.S != want {
				t.Fatalf("iter %d: key %q = %q want %q (unmarshaler string corrupted by GC)", iter, key, v.S, want)
			}
		}
	})
}
