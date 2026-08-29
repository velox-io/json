package bind

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
)

// The element carries one of every allocation source a stream scope can trigger
// while binding a body: a pointer pointee, a slice backing, and a map (whose
// entries stage in the noscan map buffer). Scalars alone would not exercise the
// allocator at all.
type memElem struct {
	ID   string            `json:"id"`
	Ptr  *memInner         `json:"ptr"`
	Tags []string          `json:"tags"`
	Meta map[string]string `json:"meta"`
}

type memInner struct {
	N int `json:"n"`
}

type memHost struct {
	Items stream.Stream[memElem] `json:"items"`
}

func memElemJSON(i int) string {
	return fmt.Sprintf(`{"id":"id-%d","ptr":{"n":%d},"tags":["a%d","b%d"],"meta":{"k%d":"v%d"}}`,
		i, i, i, i, i, i)
}

func memStreamJSON(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(memElemJSON(i))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// TestStreamRetentionBounded is the regression for the growth this scoped
// release exists to remove: retention used to accumulate for the whole parse,
// so a stream of N elements held N elements' worth of backings no matter how
// few the handler kept.
//
// Element count is scaled 100x while the payload size is held roughly fixed
// (element strings are padded to compensate). That separates the two variables:
// the arenas are sized from srcLen and are expected to stay put, so anything
// that tracks element count is the leak.
func TestStreamRetentionBounded(t *testing.T) {
	const payload = 1 << 20

	var counts []int
	var peaks []int
	for _, n := range []int{200, 2000, 20000} {
		pad := strings.Repeat("x", payload/n)
		var b strings.Builder
		b.WriteString(`{"items":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":"%s","ptr":{"n":%d},"tags":["a"],"meta":{"k":"v"}}`, pad, i)
		}
		b.WriteString(`]}`)

		p, err := NewParser[memHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h memHost
		consumed, peak := 0, 0
		h.Items.OnRead(func(s stream.Scope[memElem]) error {
			s.AllowValueReuse()
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				consumed++
				if r := p.RetainedCount(); r > peak {
					peak = r
				}
			}
			return nil
		})
		if err := p.Unmarshal([]byte(b.String()), &h); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if consumed != n {
			t.Fatalf("n=%d: consumed %d elements", n, consumed)
		}
		counts = append(counts, n)
		peaks = append(peaks, peak)
		t.Logf("elements=%-6d peakRetained=%d", n, peak)
	}

	// A 100x growth in element count must not grow retention at all. Allow a
	// small absolute slack for block-boundary timing, but nothing proportional.
	if peaks[len(peaks)-1] > peaks[0]+4 {
		t.Errorf("retention tracks element count: %v for counts %v; want flat", peaks, counts)
	}
}

// TestStreamHeldElementsStayValid pins the Scope contract that scoped release
// must not weaken: without AllowValueReuse, an element the handler keeps stays
// valid. Releasing retention only drops the decoder's references, and a held
// element's own pointer/slice/map fields keep their backings reachable, so the
// values must still read back correctly after Unmarshal returns.
func TestStreamHeldElementsStayValid(t *testing.T) {
	const n = 5000
	const keepEvery = 100

	var h memHost
	var held []*memElem
	var heldIdx []int
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[memElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			if consumed%keepEvery == 0 {
				held = append(held, it.Target())
				heldIdx = append(heldIdx, consumed)
			}
			consumed++
		}
		return nil
	})
	if err := Unmarshal(memStreamJSON(n), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
	if len(held) != (n+keepEvery-1)/keepEvery {
		t.Fatalf("held %d elements", len(held))
	}

	// Read every field group after the parse: the string (str arena), the
	// pointee (pointer slot block), the slice backing, and the map.
	for k, e := range held {
		i := heldIdx[k]
		if got, want := e.ID, fmt.Sprintf("id-%d", i); got != want {
			t.Fatalf("held[%d].ID = %q, want %q", k, got, want)
		}
		if e.Ptr == nil || e.Ptr.N != i {
			t.Fatalf("held[%d].Ptr = %+v, want N=%d", k, e.Ptr, i)
		}
		wantTags := []string{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)}
		if len(e.Tags) != 2 || e.Tags[0] != wantTags[0] || e.Tags[1] != wantTags[1] {
			t.Fatalf("held[%d].Tags = %v, want %v", k, e.Tags, wantTags)
		}
		if got, want := e.Meta[fmt.Sprintf("k%d", i)], fmt.Sprintf("v%d", i); got != want {
			t.Fatalf("held[%d].Meta = %v, want k%d=%q", k, e.Meta, i, want)
		}
	}
}

// The deferred-value drain and the map drain both have to run before retention
// is dropped, because their staging buffers are noscan and so do not root what
// they point at. This element forces both onto the release path.
type memDeferredElem struct {
	Raw  json.RawMessage   `json:"raw"`
	Meta map[string]string `json:"meta"`
}

type memDeferredHost struct {
	Items stream.Stream[memDeferredElem] `json:"items"`
}

func TestStreamReleaseWithDeferredAndMapValues(t *testing.T) {
	const n = 2000
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"raw":{"v":%d},"meta":{"k%d":"v%d"}}`, i, i, i)
	}
	b.WriteString(`]}`)

	var h memDeferredHost
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[memDeferredElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			// Both fields must be readable right here: this is the assertion
			// that SettleBatch drains the deferred records and the map buffer
			// before the handler is handed the batch.
			e := it.Target()
			if want := fmt.Sprintf(`{"v":%d}`, consumed); string(e.Raw) != want {
				return fmt.Errorf("elem %d Raw = %q, want %q", consumed, e.Raw, want)
			}
			key, want := fmt.Sprintf("k%d", consumed), fmt.Sprintf("v%d", consumed)
			if got := e.Meta[key]; got != want {
				return fmt.Errorf("elem %d Meta[%s] = %q, want %q (map = %v)", consumed, key, got, want, e.Meta)
			}
			consumed++
		}
		return nil
	})
	if err := Unmarshal([]byte(b.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
}

// Sibling scopes are the case a per-scope release floor alone does not cover: a
// document with many independent Stream fields activates one scope after
// another, and without a release on scope exit each would leave its last
// batch's retention behind for the parse to accumulate.
type memSibHost struct {
	Items stream.Stream[memElem] `json:"items"`
}

type memSibRoot struct {
	Hosts []memSibHost `json:"hosts"`
}

func TestStreamSiblingScopesRetentionBounded(t *testing.T) {
	var peaks []int
	hostCounts := []int{10, 1000}
	for _, hosts := range hostCounts {
		var b strings.Builder
		b.WriteString(`{"hosts":[`)
		for hh := 0; hh < hosts; hh++ {
			if hh > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"items":[`)
			for i := 0; i < 8; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(memElemJSON(i))
			}
			b.WriteString(`]}`)
		}
		b.WriteString(`]}`)

		p, err := NewParser[memSibRoot]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var r memSibRoot
		r.Hosts = make([]memSibHost, hosts)
		consumed, peak := 0, 0
		for hi := range r.Hosts {
			r.Hosts[hi].Items.OnRead(func(s stream.Scope[memElem]) error {
				s.AllowValueReuse()
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					consumed++
					if x := p.RetainedCount(); x > peak {
						peak = x
					}
				}
				return nil
			})
		}
		if err := p.Unmarshal([]byte(b.String()), &r); err != nil {
			t.Fatalf("hosts=%d: %v", hosts, err)
		}
		if consumed != hosts*8 {
			t.Fatalf("hosts=%d: consumed %d", hosts, consumed)
		}
		peaks = append(peaks, peak)
		t.Logf("siblingScopes=%-5d peakRetained=%d", hosts, peak)
	}
	if peaks[1] > peaks[0]+4 {
		t.Errorf("retention tracks sibling scope count: %v for %v; want flat", peaks, hostCounts)
	}
}

// TestStreamElementSlotIsBufferBase pins the invariant that lets the driver read
// the current element slot from the slice header instead of the noscan
// Core.CurAux: a non-leaf stream's buffer holds one element and the write cursor
// is reset to its base before each one. If that ever stops holding, deriving the
// slot from hdr.Data would silently hand back the wrong address.
type slotProbeInner struct {
	V string `json:"v"`
}

type slotProbeElem struct {
	ID    string                        `json:"id"`
	Inner stream.Stream[slotProbeInner] `json:"inner"`
}

type slotProbeHost struct {
	Items stream.Stream[slotProbeElem] `json:"items"`
}

func TestStreamElementSlotIsBufferBase(t *testing.T) {
	checked := 0
	streamSlotCheck = func(m *ndec.BindMachine, hdr *gort.SliceHeader, elemHasStream bool) {
		if !elemHasStream || m.Core.Phase != ndec.BindPhaseArrayValueBegin {
			return
		}
		checked++
		// Compared as addresses, not converted to pointers: CurAux is a noscan
		// uintptr, and the whole point of the invariant is that the driver never
		// has to turn it back into a pointer.
		if m.Core.CurAux != uintptr(hdr.Data) {
			t.Errorf("non-leaf element slot %#x != buffer base %#x",
				m.Core.CurAux, uintptr(hdr.Data))
		}
	}
	defer func() { streamSlotCheck = nil }()

	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"o%d","inner":[{"v":"a%d"},{"v":"b%d"}]}`, i, i, i)
	}
	b.WriteString(`]}`)

	var h slotProbeHost
	var outer, inner []string
	h.Items.OnRead(func(s stream.Scope[slotProbeElem]) error {
		for it := range s.Iter() {
			it.Target().Inner.OnRead(func(is stream.Scope[slotProbeInner]) error {
				for iit := range is.Iter() {
					if err := iit.Decode(); err != nil {
						return err
					}
					inner = append(inner, iit.Target().V)
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			outer = append(outer, it.Target().ID)
		}
		return nil
	})
	if err := Unmarshal([]byte(b.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(outer) != 20 || len(inner) != 40 {
		t.Fatalf("outer=%d inner=%d, want 20/40", len(outer), len(inner))
	}
	if checked == 0 {
		t.Fatal("slot invariant never observed; the probe did not run")
	}
}

// TestStreamScopedReleaseConcurrentMarkGC is the GC counterpart to the bound
// above. Dropping retention mid-parse is only safe if what it drops was shaded
// first, and if what native still writes into is shaded again afterwards; a
// mistake in either direction is invisible without a mark in flight. So a
// spinner keeps GC cycles running across the release points while handlers hold
// elements whose pointer, slice, map, and deferred fields were all written by
// native without a write barrier.
//
// Failure mode is a runtime fatal ("found pointer to free object"), not a
// t.Error. Run under GODEBUG=gccheckmark=1 to also catch a missed mark before
// the sweep reaches it.
func TestStreamScopedReleaseConcurrentMarkGC(t *testing.T) {
	const goroutines = 8
	// One long parse per goroutine rather than many short ones: a release only
	// races the mark phase if it happens while a cycle is in flight, and a short
	// parse mostly finishes between cycles. A large array makes the parse span
	// many cycles, so the release points land inside them.
	const elems = 60000
	const keepEvery = 200

	stop := make(chan struct{})
	var spinner sync.WaitGroup
	spinner.Add(1)
	go func() {
		defer spinner.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()

	data := memStreamJSON(elems)

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			// Held elements are the detector: each one's pointer, slice, map,
			// and string fields were written by native with no write barrier,
			// and each lives in a backing a release could have dropped early.
			kept := make([]*memElem, 0, elems/keepEvery+1)
			keptIdx := make([]int, 0, elems/keepEvery+1)
			var h memHost
			local := 0
			h.Items.OnRead(func(s stream.Scope[memElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					if local%keepEvery == 0 {
						kept = append(kept, it.Target())
						keptIdx = append(keptIdx, local)
					}
					local++
				}
				return nil
			})
			if err := Unmarshal(data, &h); err != nil {
				t.Errorf("g%d: %v", gid, err)
				return
			}
			if local != elems {
				t.Errorf("g%d: consumed %d, want %d", gid, local, elems)
				return
			}
			// Dereference after the parse, with cycles still running: a
			// wrongly-dropped backing shows up as corrupted content here, or as
			// a runtime fatal ("found pointer to free object") earlier.
			for k, e := range kept {
				i := keptIdx[k]
				if e.ID != fmt.Sprintf("id-%d", i) ||
					e.Ptr == nil || e.Ptr.N != i ||
					len(e.Tags) != 2 || e.Tags[0] != fmt.Sprintf("a%d", i) ||
					e.Meta[fmt.Sprintf("k%d", i)] != fmt.Sprintf("v%d", i) {
					t.Errorf("g%d: element %d corrupted after release: %+v", gid, i, e)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	spinner.Wait()
}
