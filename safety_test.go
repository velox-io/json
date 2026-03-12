package vjson

import (
	"encoding/json"
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

//nolint:unused
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

// ---------------------------------------------------------------------------
// 8. TestPointer_PreExistingValue — pointer field already has a value
// ---------------------------------------------------------------------------

// TestPointer_PreExistingValue verifies behavior when a pointer field already
// points to an existing allocation. Unmarshal should reuse the existing
// allocation and fill in-place (matching encoding/json behavior).
func TestPointer_PreExistingValue(t *testing.T) {
	type Inner struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type Outer struct {
		Data *Inner `json:"data"`
	}

	// Pre-allocate an Inner with existing values
	existing := &Inner{ID: 999, Name: "original"}
	s := Outer{Data: existing}

	// Unmarshal new data
	err := Unmarshal([]byte(`{"data":{"id":42,"name":"updated"}}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the new values are set
	if s.Data == nil {
		t.Fatal("Data is nil after unmarshal")
	}
	if s.Data.ID != 42 {
		t.Errorf("Data.ID = %d, want 42", s.Data.ID)
	}
	if s.Data.Name != "updated" {
		t.Errorf("Data.Name = %q, want \"updated\"", s.Data.Name)
	}

	// Must reuse existing allocation (matches encoding/json behavior)
	if s.Data != existing {
		t.Errorf("pointer changed: want reuse of existing allocation")
	}
}

// TestPointer_PreExistingValue_PointerFree tests pointer-free types (e.g., *int)
// with pre-existing allocations — Unmarshal should reuse.
func TestPointer_PreExistingValue_PointerFree(t *testing.T) {
	type S struct {
		V *int `json:"v"`
	}

	existing := new(int)
	*existing = 999
	s := S{V: existing}

	err := Unmarshal([]byte(`{"v":42}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.V == nil {
		t.Fatal("V is nil after unmarshal")
	}
	if *s.V != 42 {
		t.Errorf("*V = %d, want 42", *s.V)
	}

	// Must reuse existing allocation
	if s.V != existing {
		t.Errorf("pointer changed: want reuse of existing allocation")
	}
}

// TestPointer_PreExistingValue_Null verifies that null properly sets pointer to nil
// even when there's a pre-existing allocation.
func TestPointer_PreExistingValue_Null(t *testing.T) {
	type Inner struct {
		ID int `json:"id"`
	}
	type Outer struct {
		Data *Inner `json:"data"`
	}

	existing := &Inner{ID: 999}
	s := Outer{Data: existing}

	err := Unmarshal([]byte(`{"data":null}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Data != nil {
		t.Errorf("Data = %v, want nil", s.Data)
	}
}

// TestPointer_PreExistingValue_GCStress verifies that pre-existing values
// being replaced don't cause GC issues.
func TestPointer_PreExistingValue_GCStress(t *testing.T) {
	type Inner struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
	}
	type Outer struct {
		Data *Inner `json:"data"`
	}

	const N = 1000
	results := make([]Outer, N)

	// Pre-allocate all Inner structs
	for i := 0; i < N; i++ {
		results[i].Data = &Inner{ID: int64(i + 10000), Label: fmt.Sprintf("pre-%d", i)}
	}

	// Unmarshal new data, replacing all pre-existing allocations
	for i := 0; i < N; i++ {
		data := []byte(fmt.Sprintf(`{"data":{"id":%d,"label":"new-%d"}}`, i, i))
		if err := Unmarshal(data, &results[i]); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}

		if i%100 == 0 {
			runtime.GC()
		}
	}

	// Final GC passes
	runtime.GC()
	runtime.GC()

	// Verify all new values are intact
	for i := 0; i < N; i++ {
		if results[i].Data == nil {
			t.Fatalf("iter %d: Data is nil after GC", i)
		}
		if results[i].Data.ID != int64(i) {
			t.Errorf("iter %d: Data.ID = %d, want %d", i, results[i].Data.ID, i)
		}
		wantLabel := fmt.Sprintf("new-%d", i)
		if results[i].Data.Label != wantLabel {
			t.Errorf("iter %d: Data.Label = %q, want %q", i, results[i].Data.Label, wantLabel)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. TestPointer_StdlibCompat — compare behavior with encoding/json
// ---------------------------------------------------------------------------

// TestPointer_StdlibCompat_NewAllocation verifies that when pointer is nil,
// both vjson and encoding/json allocate new memory and produce same result.
func TestPointer_StdlibCompat_NewAllocation(t *testing.T) {
	type Inner struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type Outer struct {
		Data *Inner `json:"data"`
	}

	input := []byte(`{"data":{"id":42,"name":"test"}}`)

	var vjsonResult Outer
	if err := Unmarshal(input, &vjsonResult); err != nil {
		t.Fatalf("vjson error: %v", err)
	}

	var stdlibResult Outer
	if err := json.Unmarshal(input, &stdlibResult); err != nil {
		t.Fatalf("stdlib error: %v", err)
	}

	// Both should produce the same result
	if vjsonResult.Data == nil || stdlibResult.Data == nil {
		t.Fatal("Data is nil")
	}
	if vjsonResult.Data.ID != stdlibResult.Data.ID {
		t.Errorf("ID: vjson %d != stdlib %d", vjsonResult.Data.ID, stdlibResult.Data.ID)
	}
	if vjsonResult.Data.Name != stdlibResult.Data.Name {
		t.Errorf("Name: vjson %q != stdlib %q", vjsonResult.Data.Name, stdlibResult.Data.Name)
	}
}

// TestPointer_StdlibCompat_PreExisting documents the behavioral difference:
// encoding/json reuses existing allocations, vjson should match.
func TestPointer_StdlibCompat_PreExisting(t *testing.T) {
	type Inner struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type Outer struct {
		Data *Inner `json:"data"`
	}

	input := []byte(`{"data":{"id":42,"name":"updated"}}`)

	// Test vjson behavior
	vjsonExisting := &Inner{ID: 999, Name: "original"}
	vjsonOuter := Outer{Data: vjsonExisting}
	if err := Unmarshal(input, &vjsonOuter); err != nil {
		t.Fatalf("vjson error: %v", err)
	}

	// Test stdlib behavior
	stdlibExisting := &Inner{ID: 999, Name: "original"}
	stdlibOuter := Outer{Data: stdlibExisting}
	if err := json.Unmarshal(input, &stdlibOuter); err != nil {
		t.Fatalf("stdlib error: %v", err)
	}

	// Both should have the new values
	if vjsonOuter.Data.ID != 42 || vjsonOuter.Data.Name != "updated" {
		t.Errorf("vjson: unexpected values %+v", vjsonOuter.Data)
	}
	if stdlibOuter.Data.ID != 42 || stdlibOuter.Data.Name != "updated" {
		t.Errorf("stdlib: unexpected values %+v", stdlibOuter.Data)
	}

	// Both should reuse existing allocation (same pointer address)
	if stdlibOuter.Data != stdlibExisting {
		t.Errorf("stdlib did not reuse existing allocation")
	}
	if vjsonOuter.Data != vjsonExisting {
		t.Errorf("vjson did not reuse existing allocation")
	}
}

// TestPointer_NestedPointers tests deeply nested pointer fields.
func TestPointer_NestedPointers(t *testing.T) {
	type Level3 struct {
		Value int `json:"value"`
	}
	type Level2 struct {
		L3 *Level3 `json:"l3"`
	}
	type Level1 struct {
		L2 *Level2 `json:"l2"`
	}
	type Root struct {
		L1 *Level1 `json:"l1"`
	}

	input := []byte(`{"l1":{"l2":{"l3":{"value":42}}}}`)

	var vjsonResult Root
	if err := Unmarshal(input, &vjsonResult); err != nil {
		t.Fatalf("vjson error: %v", err)
	}

	var stdlibResult Root
	if err := json.Unmarshal(input, &stdlibResult); err != nil {
		t.Fatalf("stdlib error: %v", err)
	}

	// Verify deep nesting works
	if vjsonResult.L1 == nil || vjsonResult.L1.L2 == nil || vjsonResult.L1.L2.L3 == nil {
		t.Fatal("vjson: nested pointers are nil")
	}
	if vjsonResult.L1.L2.L3.Value != 42 {
		t.Errorf("vjson: Value = %d, want 42", vjsonResult.L1.L2.L3.Value)
	}

	if stdlibResult.L1 == nil || stdlibResult.L1.L2 == nil || stdlibResult.L1.L2.L3 == nil {
		t.Fatal("stdlib: nested pointers are nil")
	}
	if stdlibResult.L1.L2.L3.Value != 42 {
		t.Errorf("stdlib: Value = %d, want 42", stdlibResult.L1.L2.L3.Value)
	}
}

// TestPointer_NestedPointers_PreExisting tests nested pointers with pre-existing allocations.
func TestPointer_NestedPointers_PreExisting(t *testing.T) {
	type Level2 struct {
		Value int `json:"value"`
	}
	type Level1 struct {
		L2 *Level2 `json:"l2"`
	}
	type Root struct {
		L1 *Level1 `json:"l1"`
	}

	// Pre-allocate all levels
	existing := Root{
		L1: &Level1{
			L2: &Level2{Value: 999},
		},
	}

	input := []byte(`{"l1":{"l2":{"value":42}}}`)

	if err := Unmarshal(input, &existing); err != nil {
		t.Fatalf("error: %v", err)
	}

	if existing.L1 == nil || existing.L1.L2 == nil {
		t.Fatal("nested pointers are nil")
	}
	if existing.L1.L2.Value != 42 {
		t.Errorf("Value = %d, want 42", existing.L1.L2.Value)
	}
}

// TestPointer_NestedPointers_GCStress tests deeply nested pointers under GC pressure.
func TestPointer_NestedPointers_GCStress(t *testing.T) {
	type Level3 struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
	}
	type Level2 struct {
		L3 *Level3 `json:"l3"`
	}
	type Level1 struct {
		L2 *Level2 `json:"l2"`
	}
	type Root struct {
		L1 *Level1 `json:"l1"`
	}

	const N = 500
	results := make([]Root, N)

	for i := 0; i < N; i++ {
		data := []byte(fmt.Sprintf(`{"l1":{"l2":{"l3":{"id":%d,"label":"L%d"}}}}`, i, i))
		if err := Unmarshal(data, &results[i]); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}

		if i%50 == 0 {
			runtime.GC()
		}
	}

	runtime.GC()
	runtime.GC()

	for i := 0; i < N; i++ {
		if results[i].L1 == nil || results[i].L1.L2 == nil || results[i].L1.L2.L3 == nil {
			t.Fatalf("iter %d: nested pointers are nil after GC", i)
		}
		if results[i].L1.L2.L3.ID != int64(i) {
			t.Errorf("iter %d: ID = %d, want %d", i, results[i].L1.L2.L3.ID, i)
		}
		wantLabel := fmt.Sprintf("L%d", i)
		if results[i].L1.L2.L3.Label != wantLabel {
			t.Errorf("iter %d: Label = %q, want %q", i, results[i].L1.L2.L3.Label, wantLabel)
		}
	}
}

// ---------------------------------------------------------------------------
// 12. TestPointer_PointerFreeElem_GCStress — GC stress for the make([]byte)
//     allocation path used by pointer-free element types (*int, *float64, etc.)
//
// The concern: when ElemHasPtr==false, scanPointer allocates via
// make([]byte, size) and stores elemPtr = &backing[0] into the user struct
// field via *(*unsafe.Pointer)(ptr) = elemPtr. After scanPointer returns,
// the only GC-visible reference to the backing array is through the user
// struct's typed pointer field (e.g., *int). If the GC failed to trace
// this pointer, the backing array would be collected prematurely.
// ---------------------------------------------------------------------------

func TestPointer_PointerFreeElem_GCStress(t *testing.T) {
	type S struct {
		A *int     `json:"a"`
		B *float64 `json:"b"`
		C *bool    `json:"c"`
		D *uint64  `json:"d"`
	}

	const N = 2000
	results := make([]S, N)

	for i := 0; i < N; i++ {
		bval := "true"
		if i%2 == 0 {
			bval = "false"
		}
		data := []byte(fmt.Sprintf(`{"a":%d,"b":%d.5,"c":%s,"d":%d}`, i, i, bval, i*10))
		if err := Unmarshal(data, &results[i]); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}

		// Aggressive GC — force collection between parses
		if i%50 == 0 {
			runtime.GC()
		}
	}

	// Multiple final GC passes to shake out any latent issues
	for gc := 0; gc < 5; gc++ {
		runtime.GC()
	}

	for i := 0; i < N; i++ {
		if results[i].A == nil || *results[i].A != i {
			t.Errorf("iter %d: A = %v, want %d", i, results[i].A, i)
		}
		wantB := float64(i) + 0.5
		if results[i].B == nil || *results[i].B != wantB {
			t.Errorf("iter %d: B = %v, want %v", i, results[i].B, wantB)
		}
		wantC := i%2 != 0
		if results[i].C == nil || *results[i].C != wantC {
			t.Errorf("iter %d: C = %v, want %v", i, results[i].C, wantC)
		}
		wantD := uint64(i * 10)
		if results[i].D == nil || *results[i].D != wantD {
			t.Errorf("iter %d: D = %v, want %d", i, results[i].D, wantD)
		}
	}
}

// ---------------------------------------------------------------------------
// 13. TestPointer_PreExistingStackLikeValue — test that Unmarshal handles
//     a pointer field whose existing value was filled in-place.
//
// In normal Go code, &localVar escapes to the heap if stored in a struct
// that itself escapes. We cannot truly have a dangling stack pointer in
// safe Go. What we CAN test is that Unmarshal correctly fills in-place
// when the pointer field already holds a valid heap-allocated value,
// and the old value's content is properly overwritten.
// ---------------------------------------------------------------------------

func TestPointer_PreExistingStackLikeValue(t *testing.T) {
	type S struct {
		V *int `json:"v"`
	}

	// Simulate the pattern: caller sets up a pointer field from a "local" variable.
	// In Go, &localVar escapes to heap when stored in S which escapes via Unmarshal.
	// The test verifies Unmarshal fills in-place correctly.
	for iter := 0; iter < 500; iter++ {
		localVar := 12345 + iter
		s := S{V: &localVar} // &localVar escapes to heap here
		origPtr := s.V

		data := []byte(fmt.Sprintf(`{"v":%d}`, iter))
		if err := Unmarshal(data, &s); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}

		// Must reuse the same allocation (now points to the same address)
		if s.V != origPtr {
			t.Fatalf("iter %d: pointer changed, expected reuse", iter)
		}
		if *s.V != iter {
			t.Fatalf("iter %d: *V = %d, want %d", iter, *s.V, iter)
		}

		if iter%50 == 0 {
			runtime.GC()
		}
	}

	runtime.GC()
	runtime.GC()
}

// ---------------------------------------------------------------------------
// 14. TestPointer_PreExistingReuse_StdlibCompat — verify pointer reuse
//     matches encoding/json for all common pointer-free types.
// ---------------------------------------------------------------------------

func TestPointer_PreExistingReuse_StdlibCompat(t *testing.T) {
	t.Run("*int", func(t *testing.T) {
		type S struct {
			V *int `json:"v"`
		}
		input := []byte(`{"v":42}`)

		vjsonExisting := new(int)
		*vjsonExisting = 0
		vjsonS := S{V: vjsonExisting}
		if err := Unmarshal(input, &vjsonS); err != nil {
			t.Fatal(err)
		}

		stdExisting := new(int)
		*stdExisting = 0
		stdS := S{V: stdExisting}
		if err := json.Unmarshal(input, &stdS); err != nil {
			t.Fatal(err)
		}

		if vjsonS.V != vjsonExisting {
			t.Error("vjson: did not reuse existing *int allocation")
		}
		if stdS.V != stdExisting {
			t.Error("stdlib: did not reuse existing *int allocation")
		}
		if *vjsonS.V != *stdS.V {
			t.Errorf("value mismatch: vjson=%d, stdlib=%d", *vjsonS.V, *stdS.V)
		}
	})

	t.Run("*float64", func(t *testing.T) {
		type S struct {
			V *float64 `json:"v"`
		}
		input := []byte(`{"v":3.14}`)

		vjsonExisting := new(float64)
		vjsonS := S{V: vjsonExisting}
		if err := Unmarshal(input, &vjsonS); err != nil {
			t.Fatal(err)
		}

		stdExisting := new(float64)
		stdS := S{V: stdExisting}
		if err := json.Unmarshal(input, &stdS); err != nil {
			t.Fatal(err)
		}

		if vjsonS.V != vjsonExisting {
			t.Error("vjson: did not reuse existing *float64 allocation")
		}
		if *vjsonS.V != *stdS.V {
			t.Errorf("value mismatch: vjson=%v, stdlib=%v", *vjsonS.V, *stdS.V)
		}
	})

	t.Run("*bool", func(t *testing.T) {
		type S struct {
			V *bool `json:"v"`
		}
		input := []byte(`{"v":true}`)

		vjsonExisting := new(bool)
		vjsonS := S{V: vjsonExisting}
		if err := Unmarshal(input, &vjsonS); err != nil {
			t.Fatal(err)
		}

		stdExisting := new(bool)
		stdS := S{V: stdExisting}
		if err := json.Unmarshal(input, &stdS); err != nil {
			t.Fatal(err)
		}

		if vjsonS.V != vjsonExisting {
			t.Error("vjson: did not reuse existing *bool allocation")
		}
		if *vjsonS.V != *stdS.V {
			t.Errorf("value mismatch: vjson=%v, stdlib=%v", *vjsonS.V, *stdS.V)
		}
	})

	t.Run("*string (pointer-containing)", func(t *testing.T) {
		type S struct {
			V *string `json:"v"`
		}
		input := []byte(`{"v":"hello"}`)

		vjsonExisting := new(string)
		vjsonS := S{V: vjsonExisting}
		if err := Unmarshal(input, &vjsonS); err != nil {
			t.Fatal(err)
		}

		stdExisting := new(string)
		stdS := S{V: stdExisting}
		if err := json.Unmarshal(input, &stdS); err != nil {
			t.Fatal(err)
		}

		if vjsonS.V != vjsonExisting {
			t.Error("vjson: did not reuse existing *string allocation")
		}
		if *vjsonS.V != *stdS.V {
			t.Errorf("value mismatch: vjson=%q, stdlib=%q", *vjsonS.V, *stdS.V)
		}
	})
}
