package vjson

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Test types — cover strings (zero-copy + arena), pointer fields (batch alloc),
// slices, maps, and nested structs.
// ---------------------------------------------------------------------------

type SafetyItem struct {
	Name  string            `json:"name"`
	Value float64           `json:"value"`
	Tags  []string          `json:"tags"`
	Inner *SafetyInner      `json:"inner"`
	Meta  map[string]string `json:"meta"`
}

type SafetyInner struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// safetyInput returns a JSON byte slice and expected SafetyItem for index i.
// Escaped strings force the arena path; plain strings exercise zero-copy.
func safetyInput(i int) ([]byte, SafetyItem) {
	// Use escaped strings (\n, \t) to exercise the arena unescape path,
	// and plain strings to exercise the zero-copy path.
	json := fmt.Sprintf(
		`{"name":"item-%d\nok","value":%d.5,"tags":["a%d","b%d"],"inner":{"id":%d,"label":"lbl%d"},"meta":{"k%d":"v%d"}}`,
		i, i, i, i, i, i, i, i,
	)
	want := SafetyItem{
		Name:  fmt.Sprintf("item-%d\nok", i),
		Value: float64(i) + 0.5,
		Tags:  []string{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)},
		Inner: &SafetyInner{ID: int64(i), Label: fmt.Sprintf("lbl%d", i)},
		Meta:  map[string]string{fmt.Sprintf("k%d", i): fmt.Sprintf("v%d", i)},
	}
	return []byte(json), want
}

func verifySafetyItem(t *testing.T, prefix string, got, want SafetyItem) {
	t.Helper()
	if got.Name != want.Name {
		t.Errorf("%s Name = %q, want %q", prefix, got.Name, want.Name)
	}
	if got.Value != want.Value {
		t.Errorf("%s Value = %v, want %v", prefix, got.Value, want.Value)
	}
	if len(got.Tags) != len(want.Tags) {
		t.Errorf("%s Tags len = %d, want %d", prefix, len(got.Tags), len(want.Tags))
	} else {
		for j := range want.Tags {
			if got.Tags[j] != want.Tags[j] {
				t.Errorf("%s Tags[%d] = %q, want %q", prefix, j, got.Tags[j], want.Tags[j])
			}
		}
	}
	if got.Inner == nil {
		t.Errorf("%s Inner is nil", prefix)
	} else {
		if got.Inner.ID != want.Inner.ID {
			t.Errorf("%s Inner.ID = %d, want %d", prefix, got.Inner.ID, want.Inner.ID)
		}
		if got.Inner.Label != want.Inner.Label {
			t.Errorf("%s Inner.Label = %q, want %q", prefix, got.Inner.Label, want.Inner.Label)
		}
	}
	for k, wv := range want.Meta {
		if gv, ok := got.Meta[k]; !ok || gv != wv {
			t.Errorf("%s Meta[%q] = %q, want %q", prefix, k, gv, wv)
		}
	}
}

// ---------------------------------------------------------------------------
// 1. TestConcurrentUnmarshal — pool data race detection
// ---------------------------------------------------------------------------

func TestConcurrentUnmarshal(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	numG := procs * 4
	itersPerG := 500

	var wg sync.WaitGroup
	errs := make(chan error, numG)

	for g := 0; g < numG; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < itersPerG; i++ {
				idx := gid*itersPerG + i
				data, want := safetyInput(idx)
				var got SafetyItem
				if err := Unmarshal(data, &got); err != nil {
					errs <- fmt.Errorf("g%d/i%d: Unmarshal error: %w", gid, i, err)
					return
				}
				if got.Name != want.Name || got.Value != want.Value {
					errs <- fmt.Errorf("g%d/i%d: mismatch Name=%q Value=%v", gid, i, got.Name, got.Value)
					return
				}
				if got.Inner == nil || got.Inner.ID != want.Inner.ID {
					errs <- fmt.Errorf("g%d/i%d: Inner mismatch", gid, i)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// 2. TestConcurrentUnmarshal_GCStress — GC during concurrent parsing
// ---------------------------------------------------------------------------

func TestConcurrentUnmarshal_GCStress(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	numG := procs * 4
	itersPerG := 200

	// Dedicated goroutine hammering GC.
	stop := make(chan struct{})
	go func() {
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
	errs := make(chan error, numG)

	for g := 0; g < numG; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < itersPerG; i++ {
				idx := gid*itersPerG + i
				data, want := safetyInput(idx)
				var got SafetyItem
				if err := Unmarshal(data, &got); err != nil {
					errs <- fmt.Errorf("g%d/i%d: %w", gid, i, err)
					return
				}
				// Verify after parse — GC may have run between parse and check.
				if got.Name != want.Name {
					errs <- fmt.Errorf("g%d/i%d: Name=%q want=%q", gid, i, got.Name, want.Name)
					return
				}
				if got.Inner == nil || got.Inner.Label != want.Inner.Label {
					errs <- fmt.Errorf("g%d/i%d: Inner mismatch", gid, i)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// 3. TestArenaStringsSurviveGC — arena-backed strings must survive GC
// ---------------------------------------------------------------------------

func TestArenaStringsSurviveGC(t *testing.T) {
	// Escaped strings force arena allocation in unescapeSinglePass.
	inputs := []struct {
		json string
		want string
	}{
		{`{"name":"hello\nworld"}`, "hello\nworld"},
		{`{"name":"tab\there"}`, "tab\there"},
		{`{"name":"quote\"inside"}`, "quote\"inside"},
		{`{"name":"slash\\path"}`, "slash\\path"},
		{`{"name":"unicode\u0041bc"}`, "unicodeAbc"},
	}

	type S struct {
		Name string `json:"name"`
	}

	results := make([]S, len(inputs))
	for i, in := range inputs {
		if err := Unmarshal([]byte(in.json), &results[i]); err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
	}

	// Hammer GC — if arena memory is incorrectly collected, strings will corrupt.
	for i := 0; i < 10; i++ {
		runtime.GC()
	}

	for i, in := range inputs {
		if results[i].Name != in.want {
			t.Errorf("after GC: input %d Name = %q, want %q", i, results[i].Name, in.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. TestZeroCopyStringsSurviveGC — zero-copy strings reference input buffer
// ---------------------------------------------------------------------------

func TestZeroCopyStringsSurviveGC(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}

	// Plain strings (no escapes) take the zero-copy path via unsafe.String.
	// The contract says the caller must keep the input buffer alive.
	input := []byte(`{"name":"plaintext"}`)

	var got S
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}

	// Keep input alive (don't zero it) — verify string survives GC.
	for i := 0; i < 10; i++ {
		runtime.GC()
	}

	if got.Name != "plaintext" {
		t.Errorf("after GC: Name = %q, want %q", got.Name, "plaintext")
	}
	runtime.KeepAlive(input)
}

// ---------------------------------------------------------------------------
// 5. TestPointerFieldAllocation_GCStress — batch allocator under GC
// ---------------------------------------------------------------------------

func TestPointerFieldAllocation_GCStress(t *testing.T) {
	const N = 1000

	type Item struct {
		Inner *SafetyInner `json:"inner"`
	}

	results := make([]Item, N)
	for i := 0; i < N; i++ {
		data := []byte(fmt.Sprintf(`{"inner":{"id":%d,"label":"L%d"}}`, i, i))
		if err := Unmarshal(data, &results[i]); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}

		if i%100 == 0 {
			runtime.GC()
		}
	}

	// Final GC pass.
	runtime.GC()
	runtime.GC()

	for i := 0; i < N; i++ {
		if results[i].Inner == nil {
			t.Fatalf("iter %d: Inner is nil after GC", i)
		}
		if results[i].Inner.ID != int64(i) {
			t.Errorf("iter %d: Inner.ID = %d, want %d", i, results[i].Inner.ID, i)
		}
		wantLabel := fmt.Sprintf("L%d", i)
		if results[i].Inner.Label != wantLabel {
			t.Errorf("iter %d: Inner.Label = %q, want %q", i, results[i].Inner.Label, wantLabel)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. TestConcurrentUnmarshal_DiverseTypes — pool reuse across type paths
// ---------------------------------------------------------------------------

func TestConcurrentUnmarshal_DiverseTypes(t *testing.T) {
	const iters = 200

	var wg sync.WaitGroup
	errs := make(chan error, 5)

	// Goroutine 1: struct target
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			data, want := safetyInput(i)
			var got SafetyItem
			if err := Unmarshal(data, &got); err != nil {
				errs <- fmt.Errorf("struct: %w", err)
				return
			}
			if got.Name != want.Name {
				errs <- fmt.Errorf("struct: Name=%q want=%q", got.Name, want.Name)
				return
			}
		}
	}()

	// Goroutine 2: any target
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			data := []byte(fmt.Sprintf(`{"key":"val-%d\nesc"}`, i))
			var got any
			if err := Unmarshal(data, &got); err != nil {
				errs <- fmt.Errorf("any: %w", err)
				return
			}
			m, ok := got.(map[string]any)
			if !ok {
				errs <- fmt.Errorf("any: not a map")
				return
			}
			want := fmt.Sprintf("val-%d\nesc", i)
			if m["key"] != want {
				errs <- fmt.Errorf("any: key=%q want=%q", m["key"], want)
				return
			}
		}
	}()

	// Goroutine 3: map[string]string target
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			data := []byte(fmt.Sprintf(`{"a":"x%d","b":"y%d\ttab"}`, i, i))
			var got map[string]string
			if err := Unmarshal(data, &got); err != nil {
				errs <- fmt.Errorf("map: %w", err)
				return
			}
			wantA := fmt.Sprintf("x%d", i)
			wantB := fmt.Sprintf("y%d\ttab", i)
			if got["a"] != wantA || got["b"] != wantB {
				errs <- fmt.Errorf("map: a=%q b=%q", got["a"], got["b"])
				return
			}
		}
	}()

	// Goroutine 4: []any target
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			data := []byte(fmt.Sprintf(`[%d,"s%d\n",true]`, i, i))
			var got []any
			if err := Unmarshal(data, &got); err != nil {
				errs <- fmt.Errorf("slice: %w", err)
				return
			}
			if len(got) != 3 {
				errs <- fmt.Errorf("slice: len=%d", len(got))
				return
			}
		}
	}()

	// Goroutine 5: nested slice of structs
	wg.Add(1)
	go func() {
		defer wg.Done()
		type List struct {
			Items []SafetyInner `json:"items"`
		}
		for i := 0; i < iters; i++ {
			data := []byte(fmt.Sprintf(`{"items":[{"id":%d,"label":"L%d"},{"id":%d,"label":"M%d"}]}`, i, i, i+1, i+1))
			var got List
			if err := Unmarshal(data, &got); err != nil {
				errs <- fmt.Errorf("nested: %w", err)
				return
			}
			if len(got.Items) != 2 || got.Items[0].ID != int64(i) {
				errs <- fmt.Errorf("nested: items mismatch at %d", i)
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// 7. TestPoolReuse_ArenaIntegrity — sequential arena reuse stress
// ---------------------------------------------------------------------------

func TestPoolReuse_ArenaIntegrity(t *testing.T) {
	type S struct {
		A string `json:"a"`
		B string `json:"b"`
	}

	const N = 100
	results := make([]S, N)

	for i := 0; i < N; i++ {
		// Escaped strings force arena allocation.
		data := []byte(fmt.Sprintf(
			`{"a":"alpha-%d\nnewline","b":"beta-%d\ttab"}`, i, i,
		))
		if err := Unmarshal(data, &results[i]); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}

	// After all parses, all previous results must still be intact.
	// The pool has been returning and re-issuing parsers throughout.
	for i := 0; i < N; i++ {
		wantA := fmt.Sprintf("alpha-%d\nnewline", i)
		wantB := fmt.Sprintf("beta-%d\ttab", i)
		if results[i].A != wantA {
			t.Errorf("iter %d: A = %q, want %q", i, results[i].A, wantA)
		}
		if results[i].B != wantB {
			t.Errorf("iter %d: B = %q, want %q", i, results[i].B, wantB)
		}
	}
}
