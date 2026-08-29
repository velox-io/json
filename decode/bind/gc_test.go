package bind

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// TestGCSurvival forces GC after binding a pointer chain. It protects the
// invariant that allocator typed batches keep every native written pointee
// reachable until the final Go handoff makes the graph visible to the caller.
func TestGCSurvival(t *testing.T) {
	type node struct {
		Name  string `json:"name"`
		Child *node  `json:"child"`
	}
	data := []byte(`{"name":"root","child":{"name":"mid","child":{"name":"leaf"}}}`)

	var got node
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	runtime.GC()
	runtime.GC()

	if got.Name != "root" {
		t.Fatalf("root name = %q", got.Name)
	}
	if got.Child == nil || got.Child.Name != "mid" {
		t.Fatalf("mid lost after GC: %+v", got.Child)
	}
	if got.Child.Child == nil || got.Child.Child.Name != "leaf" {
		t.Fatalf("leaf lost after GC")
	}
}

// TestRootPtrHandoffGC verifies the *T root handoff: the pointee is
// allocated via the root slot class during parse, then the single
// barrier store on the Go side writes its address into the user's *T
// variable. GC must not collect it.
func TestRootPtrHandoffGC(t *testing.T) {
	type payload struct {
		V int    `json:"v"`
		S string `json:"s"`
	}
	data := []byte(`{"v":42,"s":"hello"}`)

	var got *payload
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got == nil {
		t.Fatalf("root *T handoff left nil pointer")
	}

	runtime.GC()
	runtime.GC()

	if got.V != 42 {
		t.Fatalf("V = %d, want 42", got.V)
	}
	if got.S != "hello" {
		t.Fatalf("S = %q, want hello", got.S)
	}
}

// TestStringLifecycleAcrossPooledParse verifies that strings from an earlier
// pooled parse remain valid after later parses reuse the Parser. Result string
// headers may point inside the old string arena, so those buffers must stay
// reachable through the decoded value.
func TestStringLifecycleAcrossPooledParse(t *testing.T) {
	type rec struct {
		Plain   string `json:"plain"`
		Escaped string `json:"escaped"`
	}

	var first rec
	if err := Unmarshal([]byte(`{"plain":"hello-world","escaped":"a\tb\nc"}`), &first); err != nil {
		t.Fatal(err)
	}
	wantPlain, wantEscaped := "hello-world", "a\tb\nc"
	if first.Plain != wantPlain || first.Escaped != wantEscaped {
		t.Fatalf("initial parse wrong: %+v", first)
	}

	for range 200 {
		var other rec
		_ = Unmarshal([]byte(`{"plain":"XXXXXXXXXXXXXXXXXXXX","escaped":"zzz\nzzz\tzzz"}`), &other)
	}
	runtime.GC()
	runtime.GC()

	if first.Plain != wantPlain {
		t.Errorf("Plain corrupted after pooled reuse: got %q want %q", first.Plain, wantPlain)
	}
	if first.Escaped != wantEscaped {
		t.Errorf("Escaped corrupted after pooled reuse: got %q want %q", first.Escaped, wantEscaped)
	}
}

// TestBatchReuseAcrossParse verifies that allocator slot residue can survive
// across parses of the same root type without corrupting decoded results.
func TestBatchReuseAcrossParse(t *testing.T) {
	type box struct {
		P *int `json:"p"`
	}
	for i := range 50 {
		var b box
		if err := Unmarshal([]byte(`{"p":12345}`), &b); err != nil {
			t.Fatal(err)
		}
		if b.P == nil || *b.P != 12345 {
			t.Fatalf("iter %d: got %v", i, b.P)
		}
	}
}

// TestConcurrentMarkBoxedAnyGC guards the publication barrier in
// Allocator.Release (shadeCarvedBackings).
//
// Native writes eface.data for a boxed any element straight into the result
// graph with no Go write barrier. If the mark phase already blackened the
// destination, that edge stays invisible for the rest of the cycle, and the
// only other reference to the box's backing dies when the Parser returns to
// the pool. The backing is then swept while the caller still points at it,
// surfacing as "found pointer to free object".
//
// Only element kinds that box onto the heap can regress: null writes a nil
// data word and bool points at a static, so both stay safe with no barrier.
// A slot class whose block serves a whole parse without growing is never
// staged in retained, which is why small inputs are the ones at risk.
//
// GOGC=1 plus a GC spinner keeps a mark phase running for most of the parse.
// Run under -race for extra scheduling interleavings, and under
// GODEBUG=gccheckmark=1 to catch a missed mark before any sweep can.
func TestConcurrentMarkBoxedAnyGC(t *testing.T) {
	const goroutines = 12
	const iters = 400

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

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			// Hold every result so a swept backing stays reachable and is
			// caught by the next cycle instead of going unnoticed.
			kept := make([]any, iters)
			for i := range iters {
				n := gid*iters + i
				// float64 and string box onto the heap; bool and null do not.
				data := fmt.Appendf(nil, `[%d,"s%d\n",true,null]`, n, n)
				var got []any
				if err := Unmarshal(data, &got); err != nil {
					t.Errorf("g%d/i%d: %v", gid, i, err)
					return
				}
				if len(got) != 4 {
					t.Errorf("g%d/i%d: len = %d, want 4", gid, i, len(got))
					return
				}
				if f, ok := got[0].(float64); !ok || f != float64(n) {
					t.Errorf("g%d/i%d: elem0 = %#v, want float64(%d)", gid, i, got[0], n)
					return
				}
				if s, ok := got[1].(string); !ok || s != fmt.Sprintf("s%d\n", n) {
					t.Errorf("g%d/i%d: elem1 = %#v", gid, i, got[1])
					return
				}
				kept[i] = got
			}
			runtime.KeepAlive(kept)
		}(g)
	}
	wg.Wait()
	close(stop)
	spinner.Wait()
}
