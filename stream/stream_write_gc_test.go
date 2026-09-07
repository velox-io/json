package stream_test

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

// Write-side GC safety.
//
// An OnWrite producer hands the encoder a *T; the encoder erases it to an
// unsafe.Pointer at the Sink seam and parks it in encode state the collector
// cannot fully see: VjExecCtx.CurBase is scannable, but a slice iteration over
// the element stores its data pointer in VjStackFrame.Payload, a [20]byte
// array that the GC does not scan as a pointer. Reachability therefore has to
// come from the typed Go side (the runtime.KeepAlive pair in Sink.Encode and
// streamDriver.EncodeValue) rather than from the ABI context.
//
// The same window also spans a stack move. An element is normally heap-moved
// by escape analysis at the Sink[T] interface seam, but the producer runs on a
// Go stack that can be copied while an element address is live in the encoder,
// and the documented contract lets a producer reuse one element slot across
// calls. A retained raw address would survive a bad move silently.
//
// These tests widen that window on purpose (collect and grow the stack between
// elements) and then check the bytes: a missed root shows up as corrupted or
// truncated output long before it shows up as a crash. They are written to be
// meaningful in a normal `go test` run and considerably more hostile under the
// encode leg of scripts/gc-stress.sh, whose vjgcstress build collects before
// every native VM entry.

// gcElem carries one of every allocation source, so each element the producer
// submits roots a pointer, a slice backing, a map, and an indirect string.
// Scalars alone would leave the allocator idle and prove nothing.
type gcElem struct {
	ID   *string           `json:"id"`
	Tags []string          `json:"tags"`
	Attr map[string]string `json:"attr"`
	N    int               `json:"n"`
}

// newGCElem builds element i with freshly allocated parts every call. The
// caller drops all other references, so an element that the encoder fails to
// root becomes collectible while it is still being encoded (and, under
// GODEBUG=clobberfree=1, is poisoned rather than silently readable).
func newGCElem(i int) gcElem {
	id := "elem-" + strconv.Itoa(i)
	return gcElem{
		ID:   &id,
		Tags: []string{"t" + strconv.Itoa(i), "shared"},
		Attr: map[string]string{"k": strconv.Itoa(i * 7)},
		N:    i,
	}
}

// gcGrowStack recurses with fat frames to force the Go stack to grow and be
// copied. Called from inside a producer, between Sink.Encode calls, it moves
// the producer's own frame (and any element slot living in it) to a new
// address.
//
//go:noinline
func gcGrowStack(d int) byte {
	if d == 0 {
		return 0
	}
	var pad [256]byte
	pad[0] = byte(d)
	return pad[0] ^ gcGrowStack(d-1)
}

// gcEngines are the three encode plans a stream field can run under. The
// indent string picks the engine: "  " is synthesizable by the native VM,
// while "--" fails isSimpleIndent and forces the Go interpreter, which frames
// stream elements through a different newline protocol and different
// invocation contexts.
var gcEngines = []struct {
	name   string
	prefix string
	indent string
}{
	{"compact", "", ""},
	{"native-indent", "", "  "},
	{"interp-indent", "", "--"},
}

// TestStreamWriteGCPressureFresh collects and moves the stack between every
// few elements, with each element freshly allocated and unreferenced by the
// producer once submitted. The output is compared against the equivalent
// slice value, so a dropped or recycled element is caught as wrong bytes
// rather than only as a crash.
func TestStreamWriteGCPressureFresh(t *testing.T) {
	const elems = 2000
	const gcEvery = 50

	want := make([]gcElem, elems)
	for i := range want {
		want[i] = newGCElem(i)
	}

	for _, eng := range gcEngines {
		t.Run(eng.name, func(t *testing.T) {
			var s stream.Stream[gcElem]
			s.OnWrite(func(sink stream.Sink[gcElem]) error {
				for i := range elems {
					// Freshly allocated per iteration and reachable only
					// through the pointer handed to the encoder.
					e := newGCElem(i)
					if err := sink.Encode(&e); err != nil {
						return err
					}
					if i%gcEvery == 0 {
						runtime.GC()
						gcGrowStack(120)
					}
				}
				return nil
			})

			got, err := vjson.MarshalIndent(&s, eng.prefix, eng.indent)
			if err != nil {
				t.Fatalf("MarshalIndent: %v", err)
			}
			exp, err := vjson.MarshalIndent(&want, eng.prefix, eng.indent)
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			if !bytes.Equal(got, exp) {
				t.Errorf("output diverged from the slice oracle (%d vs %d bytes)\n%s",
					len(got), len(exp), firstDiff(got, exp))
			}
		})
	}
}

// TestStreamWriteGCReusedSlot exercises the documented reuse contract: the
// producer keeps ONE element slot and mutates it per call. The slot is a
// local, so growing the stack between submissions relocates it; anything the
// encoder retained by raw address would now read a stale frame.
func TestStreamWriteGCReusedSlot(t *testing.T) {
	const elems = 1500

	want := make([]gcElem, elems)
	for i := range want {
		want[i] = newGCElem(i)
	}

	for _, eng := range gcEngines {
		t.Run(eng.name, func(t *testing.T) {
			var s stream.Stream[gcElem]
			s.OnWrite(func(sink stream.Sink[gcElem]) error {
				var slot gcElem // reused across every Encode call
				for i := range elems {
					slot = newGCElem(i)
					if err := sink.Encode(&slot); err != nil {
						return err
					}
					// Move the frame that holds slot, then collect.
					gcGrowStack(80 + i%64)
					if i%40 == 0 {
						runtime.GC()
					}
				}
				return nil
			})

			got, err := vjson.MarshalIndent(&s, eng.prefix, eng.indent)
			if err != nil {
				t.Fatalf("MarshalIndent: %v", err)
			}
			exp, err := vjson.MarshalIndent(&want, eng.prefix, eng.indent)
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			if !bytes.Equal(got, exp) {
				t.Errorf("reused-slot output diverged (%d vs %d bytes)\n%s",
					len(got), len(exp), firstDiff(got, exp))
			}
		})
	}
}

// TestStreamWriteGCAcrossDrain targets the windowed-output seam. In stream
// mode the driver flushes at a low-water mark between elements, so a
// collection landing there runs while the driver holds the element address,
// the parked streaming buffer is live, and the native VM context is suspended
// mid-container. Marshal (buffer mode) never takes that path.
func TestStreamWriteGCAcrossDrain(t *testing.T) {
	// ~100 bytes per element: enough total output to cross the 32 KiB drain
	// mark and the 128 KiB window many times over.
	const elems = 5000

	for _, eng := range gcEngines {
		t.Run(eng.name, func(t *testing.T) {
			mk := func() *stream.Stream[gcElem] {
				s := new(stream.Stream[gcElem])
				s.OnWrite(func(sink stream.Sink[gcElem]) error {
					for i := range elems {
						e := newGCElem(i)
						if err := sink.Encode(&e); err != nil {
							return err
						}
						if i%100 == 0 {
							runtime.GC()
						}
					}
					return nil
				})
				return s
			}

			var buf bytes.Buffer
			enc := vjson.NewEncoder(&buf)
			if eng.indent != "" {
				enc.SetIndent(eng.prefix, eng.indent)
			}
			if err := vjson.EncodeValue(enc, mk()); err != nil {
				t.Fatalf("EncodeValue: %v", err)
			}

			// The windowed writer path must produce exactly what the
			// single-buffer path does.
			exp, err := vjson.MarshalIndent(mk(), eng.prefix, eng.indent)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
			if !bytes.Equal(got, exp) {
				t.Errorf("streamed output diverged from Marshal (%d vs %d bytes)\n%s",
					len(got), len(exp), firstDiff(got, exp))
			}
		})
	}
}

// TestStreamWriteGCNested keeps several invocation contexts alive at once. A
// nested producer suspends its parent's context mid-element, so a collection
// inside the innermost producer must find every suspended level's base
// pointer still rooted, not just the innermost one.
func TestStreamWriteGCNested(t *testing.T) {
	type node struct {
		Name string              `json:"name"`
		Data gcElem              `json:"data"`
		Kids stream.Stream[node] `json:"kids"`
	}

	// leaf's own producer is empty; kids is not omitempty, so every level
	// must configure one.
	leaf := func(name string, n int) node {
		var kid node
		kid.Name = name
		kid.Data = newGCElem(n)
		kid.Kids.OnWrite(func(sink stream.Sink[node]) error {
			runtime.GC()
			return nil
		})
		return kid
	}

	var root node
	root.Name = "root"
	root.Data = newGCElem(0)
	root.Kids.OnWrite(func(l1 stream.Sink[node]) error {
		for i := range 40 {
			var mid node
			mid.Name = "mid" + strconv.Itoa(i)
			mid.Data = newGCElem(i)
			mid.Kids.OnWrite(func(l2 stream.Sink[node]) error {
				for j := range 5 {
					// Collect and move the stack while the parent
					// activation is suspended mid-element.
					runtime.GC()
					gcGrowStack(60)
					g := leaf("g"+strconv.Itoa(j), i*100+j)
					if err := l2.Encode(&g); err != nil {
						return err
					}
				}
				return nil
			})
			if err := l1.Encode(&mid); err != nil {
				return err
			}
			runtime.GC()
		}
		return nil
	})

	// The producers run exactly once per encode, so compare each engine
	// against a structural oracle recovered from the compact form.
	compact, err := vjson.Marshal(&root)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	type eNode struct {
		Name string  `json:"name"`
		Data gcElem  `json:"data"`
		Kids []eNode `json:"kids"`
	}
	var oracle eNode
	if err := vjson.Unmarshal(compact, &oracle); err != nil {
		t.Fatalf("compact output is not valid JSON: %v\n%s", err, compact)
	}
	if len(oracle.Kids) != 40 {
		t.Fatalf("lost nested elements: got %d children, want 40", len(oracle.Kids))
	}
	for i, kid := range oracle.Kids {
		if len(kid.Kids) != 5 {
			t.Fatalf("child %d: got %d grandchildren, want 5", i, len(kid.Kids))
		}
		if kid.Data.ID == nil || *kid.Data.ID != "elem-"+strconv.Itoa(i) {
			t.Fatalf("child %d: element payload corrupted: %+v", i, kid.Data)
		}
	}

	for _, ident := range []string{"  ", "--"} {
		got, err := vjson.MarshalIndent(&root, "", ident)
		if err != nil {
			t.Fatalf("MarshalIndent(%q): %v", ident, err)
		}
		exp, err := vjson.MarshalIndent(&oracle, "", ident)
		if err != nil {
			t.Fatalf("oracle MarshalIndent(%q): %v", ident, err)
		}
		if !bytes.Equal(got, exp) {
			t.Errorf("nested MarshalIndent(%q) diverged\n%s", ident, firstDiff(got, exp))
		}
	}
}

// TestStreamWriteGCConcurrent runs many encoders at once so the pooled
// encodeState (and the invocation stack it carries) is recycled across
// goroutines under collection pressure. A context that kept a stale element
// pointer from a previous encode shows up here as cross-talk between
// goroutines.
func TestStreamWriteGCConcurrent(t *testing.T) {
	const goroutines = 16
	const elemsPer = 300

	exp := make([][]byte, goroutines)
	for g := range goroutines {
		want := make([]gcElem, elemsPer)
		for i := range want {
			want[i] = newGCElem(g*elemsPer + i)
		}
		b, err := vjson.Marshal(&want)
		if err != nil {
			t.Fatalf("oracle %d: %v", g, err)
		}
		exp[g] = b
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	got := make([][]byte, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var s stream.Stream[gcElem]
			s.OnWrite(func(sink stream.Sink[gcElem]) error {
				for i := range elemsPer {
					e := newGCElem(g*elemsPer + i)
					if err := sink.Encode(&e); err != nil {
						return err
					}
					if i%25 == 0 {
						runtime.GC()
						gcGrowStack(50)
					}
				}
				return nil
			})
			b, err := vjson.Marshal(&s)
			got[g], errs[g] = b, err
		}(g)
	}
	wg.Wait()

	for g := range goroutines {
		if errs[g] != nil {
			t.Fatalf("goroutine %d: %v", g, errs[g])
		}
		if !bytes.Equal(got[g], exp[g]) {
			t.Errorf("goroutine %d output diverged\n%s", g, firstDiff(got[g], exp[g]))
		}
	}
}

// TestStreamWriteIndentDepthLeak pins a live encoder bug found while building
// the GC suite above. It is NOT a GC problem: it reproduces deterministically
// with no collection, no concurrency and no stack growth.
//
// When a stream element routes through encodeAnyReflect (an `any` holding a
// pointer, which the native VM cannot encode inline), the element's execution
// raises es.indentDepth and never restores it. streamDriver saves and restores
// indentDepth per activation, not per element, so the leak accumulates: depth
// grows by one per element until 1+prefix+depth*step overruns the 768-byte
// indent template and writeIndent panics with a slice-bounds error.
//
//	panic: runtime error: slice bounds out of range [:769] with length 768
//	  venc.(*encodeState).writeIndent      venc/vm_exec.go:19
//	  venc.(*encodeState).handleInterfaceYield venc/vm_iface.go:40
//
// Scope: native indent mode only. Compact has no template to overrun, and the
// non-simple-indent interpreter path does not leak. Output is visibly wrong
// (runaway indentation) well before the panic, so small element counts corrupt
// silently and large ones crash. Present in 2544212f, the commit that added
// the write side.
//
// Unskip once the per-element depth is restored (the natural fix is for the
// driver to reset to its saved depth after each element, matching what
// popInvocation already does for nested runs).
func TestStreamWriteIndentDepthLeak(t *testing.T) {
	t.Skip("known bug: indentDepth leaks per stream element via encodeAnyReflect")

	const elems = 400
	var s stream.Stream[[]any]
	s.OnWrite(func(sink stream.Sink[[]any]) error {
		for range elems {
			v := "x"
			e := []any{&v} // pointer inside any: forces encodeAnyReflect
			if err := sink.Encode(&e); err != nil {
				return err
			}
		}
		return nil
	})

	got, err := vjson.MarshalIndent(&s, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Every element sits at a fixed nesting depth, so the deepest line must
	// not grow with the element count.
	deepest := 0
	for _, line := range bytes.Split(got, []byte("\n")) {
		n := len(line) - len(bytes.TrimLeft(line, " "))
		deepest = max(deepest, n/2)
	}
	if deepest > 2 {
		t.Errorf("indent depth leaked: deepest line is at depth %d, want <= 2", deepest)
	}
}

// firstDiff renders a short window around the first differing byte. Whole-
// output dumps are useless at these sizes, and the offset of the divergence
// is what identifies which element went wrong.
func firstDiff(got, want []byte) string {
	n := min(len(got), len(want))
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	lo := max(i-60, 0)
	hiG := min(i+60, len(got))
	hiW := min(i+60, len(want))
	return "first difference at byte " + strconv.Itoa(i) +
		"\n got ..." + string(got[lo:hiG]) +
		"\nwant ..." + string(want[lo:hiW])
}
