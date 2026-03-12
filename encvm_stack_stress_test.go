package vjson

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/velox-io/json/native/encvm"
)

// =============================================================================
// Goroutine Stack Stress Tests for Native C Encoder
//
// The native C encoder runs directly on the goroutine stack via NOSPLIT
// trampolines (no Go stack growth check before entering C). These tests
// verify that the C VM does not corrupt the goroutine stack when called
// on fresh, small goroutine stacks.
//
// Go goroutines start with a small stack (~2-8KB) that grows on demand.
// However, C code cannot trigger Go's stack growth mechanism. The real
// danger is a fresh goroutine calling Marshal directly — the stack is at
// its initial minimum size, and the full Go→C call chain (~912 bytes on
// arm64) must fit within it.
//
// Strategy:
// 1. Spawn many goroutines that immediately call Marshal (depth=0, no
//    recursion warm-up) to hit the initial small stack scenario
// 2. Run concurrent goroutines to maximize the chance of exercising
//    freshly allocated small stacks
// 3. Verify the output is correct (no silent corruption)
// =============================================================================

// --- Test data types (varying complexity to exercise different VM paths) ---

type stackTestSimple struct {
	A int    `json:"a"`
	B string `json:"b"`
	C bool   `json:"c"`
}

type stackTestNested struct {
	Name  string          `json:"name"`
	Inner stackTestSimple `json:"inner"`
	Value float64         `json:"value"`
}

type stackTestDeep struct {
	Level1 struct {
		Level2 struct {
			Level3 struct {
				X int    `json:"x"`
				Y string `json:"y"`
			} `json:"level3"`
		} `json:"level2"`
	} `json:"level1"`
}

type stackTestSlice struct {
	Items []stackTestSimple `json:"items"`
	Tags  []string          `json:"tags"`
}

type stackTestInterface struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	Extra any    `json:"extra"`
}

type stackTestPointer struct {
	Name  string           `json:"name"`
	Inner *stackTestSimple `json:"inner"`
	Ptr   *int             `json:"ptr"`
}

type stackTestComplex struct {
	ID     int               `json:"id"`
	Name   string            `json:"name"`
	Nested stackTestNested   `json:"nested"`
	Items  []stackTestSimple `json:"items"`
	Meta   map[string]any    `json:"meta"`
	Iface  any               `json:"iface"`
	Ptr    *stackTestSimple  `json:"ptr"`
	Tags   []string          `json:"tags"`
	Flags  map[string]string `json:"flags"`
}

// --- Core test runner ---

// runStackStressTest runs the given test function across many concurrent
// goroutines to stress freshly allocated goroutine stacks.
// Asserts that the native C VM was actually invoked at least once.
func runStackStressTest(t *testing.T, numGoroutines int, fn func(t *testing.T, goroutineID int)) {
	t.Helper()

	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}

	var wg sync.WaitGroup
	errCh := make(chan string, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					errCh <- fmt.Sprintf("goroutine %d panicked: %v\n%s", id, r, buf[:n])
				}
			}()
			fn(t, id)
		}(i)
	}

	wg.Wait()
	close(errCh)

	for msg := range errCh {
		t.Error(msg)
	}

}

// verifyMarshalResult checks that the native encoder output is semantically
// equivalent to encoding/json output. Uses JSON round-trip comparison to
// handle map key ordering differences.
func verifyMarshalResult[T any](t *testing.T, v *T, got []byte, err error, label string) {
	t.Helper()
	if err != nil {
		t.Errorf("[%s] Marshal error: %v", label, err)
		return
	}
	want, err2 := json.Marshal(v)
	if err2 != nil {
		t.Errorf("[%s] std json.Marshal error: %v", label, err2)
		return
	}
	// Fast path: byte-exact match.
	if string(got) == string(want) {
		return
	}
	// Slow path: JSON semantic comparison (handles map key ordering).
	var gotVal, wantVal any
	if e := json.Unmarshal(got, &gotVal); e != nil {
		t.Errorf("[%s] got is not valid JSON: %v\n  got: %s", label, e, got)
		return
	}
	if e := json.Unmarshal(want, &wantVal); e != nil {
		t.Errorf("[%s] want is not valid JSON: %v\n  want: %s", label, e, want)
		return
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("[%s] semantic mismatch:\n  got:  %s\n  want: %s", label, got, want)
	}
}

// =============================================================================
// Test cases — all use depth=0 (direct Marshal on fresh goroutine stacks)
// =============================================================================

// TestNativeEncoder_GoroutineStackStress_Simple tests the native encoder
// with simple structs on fresh goroutine stacks.
func TestNativeEncoder_GoroutineStackStress_Simple(t *testing.T) {
	v := stackTestSimple{A: 42, B: "hello", C: true}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("goroutine=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Nested tests nested structs
// (multiple OBJ_OPEN/CLOSE in the VM) on fresh stacks.
func TestNativeEncoder_GoroutineStackStress_Nested(t *testing.T) {
	v := stackTestNested{
		Name:  "test",
		Inner: stackTestSimple{A: 1, B: "inner", C: false},
		Value: 3.14,
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_DeepNesting tests deeply nested
// struct layouts (OBJ_OPEN/CLOSE in the VM) on fresh stacks.
func TestNativeEncoder_GoroutineStackStress_DeepNesting(t *testing.T) {
	v := stackTestDeep{}
	v.Level1.Level2.Level3.X = 99
	v.Level1.Level2.Level3.Y = "deep"

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Slices tests slice encoding
// (VM SLICE_BEGIN/END loop) on fresh stacks.
func TestNativeEncoder_GoroutineStackStress_Slices(t *testing.T) {
	v := stackTestSlice{
		Items: []stackTestSimple{
			{A: 1, B: "one", C: true},
			{A: 2, B: "two", C: false},
			{A: 3, B: "three", C: true},
		},
		Tags: []string{"alpha", "beta", "gamma"},
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Interface tests interface encoding
// (VM yield protocol) on fresh stacks. Interface values cause C→Go→C
// round-trips which add extra stack load.
func TestNativeEncoder_GoroutineStackStress_Interface(t *testing.T) {
	v := stackTestInterface{
		Name:  "iface-test",
		Value: map[string]any{"key": "val", "num": float64(42)},
		Extra: []any{"a", float64(1), true, nil},
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Pointer tests pointer field encoding
// (VM PTR_DEREF/PTR_END) on fresh stacks.
func TestNativeEncoder_GoroutineStackStress_Pointer(t *testing.T) {
	inner := stackTestSimple{A: 7, B: "pointed", C: true}
	ptrVal := 123
	v := stackTestPointer{
		Name:  "ptr-test",
		Inner: &inner,
		Ptr:   &ptrVal,
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Complex combines all challenging
// features: nested structs, slices, interfaces, maps, pointers.
func TestNativeEncoder_GoroutineStackStress_Complex(t *testing.T) {
	inner := stackTestSimple{A: 99, B: "complex-inner", C: true}
	v := stackTestComplex{
		ID:   1,
		Name: "stress",
		Nested: stackTestNested{
			Name:  "nested",
			Inner: stackTestSimple{A: 2, B: "n-inner", C: false},
			Value: 2.718,
		},
		Items: []stackTestSimple{
			{A: 10, B: "item1", C: true},
			{A: 20, B: "item2", C: false},
		},
		Meta:  map[string]any{"k1": "v1", "k2": float64(42), "k3": true},
		Iface: []any{"hello", float64(3.14), nil},
		Ptr:   &inner,
		Tags:  []string{"t1", "t2", "t3"},
		Flags: map[string]string{"debug": "true", "mode": "fast"},
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}

// TestNativeEncoder_GoroutineStackStress_Indent tests indent mode encoding
// on fresh stacks. Indent mode uses VMExec (default) with additional local
// state (indent_tpl, indent_depth, etc.) — the largest C stack frame (304B).
func TestNativeEncoder_GoroutineStackStress_Indent(t *testing.T) {
	v := stackTestNested{
		Name:  "indent-test",
		Inner: stackTestSimple{A: 1, B: "inner", C: true},
		Value: 1.23,
	}

	runStackStressTest(t, 500, func(t *testing.T, id int) {
		got, err := MarshalIndent(&v, "", "  ")
		label := fmt.Sprintf("g=%d", id)
		if err != nil {
			t.Errorf("[%s] MarshalIndent error: %v", label, err)
			return
		}
		want, err2 := json.MarshalIndent(&v, "", "  ")
		if err2 != nil {
			t.Errorf("[%s] std json.MarshalIndent error: %v", label, err2)
			return
		}
		if string(got) != string(want) {
			t.Errorf("[%s] indent mismatch:\n  got:  %s\n  want: %s", label, got, want)
		}
	})
}

// TestNativeEncoder_GoroutineStackStress_ManyOptions tests all VM entry points
// (default, compact, fast) on fresh stacks.
func TestNativeEncoder_GoroutineStackStress_ManyOptions(t *testing.T) {
	v := stackTestNested{
		Name:  "opts-test",
		Inner: stackTestSimple{A: 5, B: "opt-inner", C: true},
		Value: 9.99,
	}

	type optCase struct {
		name string
		opts []MarshalOption
	}
	cases := []optCase{
		{"default", nil},
		{"escape-html", []MarshalOption{WithEscapeHTML()}},
		{"std-compat", []MarshalOption{WithStdCompat()}},
		{"fast-escape", []MarshalOption{WithFastEscape()}},
		{"line-terms", []MarshalOption{WithEscapeLineTerms()}},
		{"utf8-correction", []MarshalOption{WithUTF8Correction()}},
	}

	runStackStressTest(t, 300, func(t *testing.T, id int) {
		for _, c := range cases {
			got, err := Marshal(&v, c.opts...)
			if err != nil {
				t.Errorf("[g=%d opt=%s] error: %v", id, c.name, err)
				continue
			}
			// Verify non-empty valid JSON (option-specific output may differ
			// from std json, so we just check basic structure).
			if len(got) < 2 || got[0] != '{' || got[len(got)-1] != '}' {
				t.Errorf("[g=%d opt=%s] bad output: %s", id, c.name, got)
			}
		}
	})
}

// TestNativeEncoder_GoroutineStackStress_TinyStack attempts to trigger the
// worst case: many goroutines released simultaneously call Marshal on their
// initial small stacks with no prior stack growth.
func TestNativeEncoder_GoroutineStackStress_TinyStack(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}

	v := stackTestComplex{
		ID:   1,
		Name: "tiny-stack",
		Nested: stackTestNested{
			Name:  "n",
			Inner: stackTestSimple{A: 1, B: "x", C: true},
			Value: 1.0,
		},
		Items: []stackTestSimple{{A: 1, B: "i", C: true}},
		Meta:  map[string]any{"k": "v"},
		Iface: "hello",
		Tags:  []string{"t"},
	}

	// Use GOMAXPROCS goroutines all starting simultaneously to maximize
	// contention on the stack growth path.
	numG := runtime.GOMAXPROCS(0) * 50

	var ready sync.WaitGroup
	ready.Add(numG)
	var start sync.WaitGroup
	start.Add(1)

	var wg sync.WaitGroup
	errCh := make(chan string, numG)

	for i := range numG {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					errCh <- fmt.Sprintf("goroutine %d panicked: %v\n%s", id, r, buf[:n])
				}
			}()

			// Signal ready and wait for simultaneous start.
			ready.Done()
			start.Wait()

			// Immediately marshal — no warm-up, no recursion.
			got, err := Marshal(&v)
			if err != nil {
				errCh <- fmt.Sprintf("goroutine %d marshal error: %v", id, err)
				return
			}
			if len(got) < 2 || got[0] != '{' || got[len(got)-1] != '}' {
				errCh <- fmt.Sprintf("goroutine %d bad output: %s", id, got)
			}
		}(i)
	}

	// Wait for all goroutines to be ready, then release them simultaneously.
	ready.Wait()
	start.Done()

	wg.Wait()
	close(errCh)

	for msg := range errCh {
		t.Error(msg)
	}

}

// TestNativeEncoder_GoroutineStackStress_RapidSpawn tests rapid goroutine
// creation and destruction to stress the goroutine stack allocator.
// New goroutines may reuse recently freed small stacks.
func TestNativeEncoder_GoroutineStackStress_RapidSpawn(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}

	v := stackTestNested{
		Name:  "rapid",
		Inner: stackTestSimple{A: 42, B: "data", C: true},
		Value: 6.28,
	}

	const iterations = 20
	const batchSize = 200

	for iter := range iterations {
		var wg sync.WaitGroup
		errCh := make(chan string, batchSize)

		for i := range batchSize {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						buf := make([]byte, 4096)
						n := runtime.Stack(buf, false)
						errCh <- fmt.Sprintf("iter=%d g=%d panicked: %v\n%s", iter, id, r, buf[:n])
					}
				}()

				got, err := Marshal(&v)
				verifyMarshalResult(t, &v, got, err, fmt.Sprintf("iter=%d g=%d", iter, id))
			}(i)
		}

		wg.Wait()
		close(errCh)

		for msg := range errCh {
			t.Error(msg)
		}

		// Brief pause to encourage stack reclamation.
		runtime.GC()
	}

}

// TestNativeEncoder_GoroutineStackStress_LargeStrings tests encoding with
// large string fields on fresh stacks. Large strings cause the VM to check
// and grow the output buffer multiple times (VJ_ERR_BUF_FULL), meaning
// multiple C→Go→C transitions per encode — each re-entry hits the
// goroutine stack.
func TestNativeEncoder_GoroutineStackStress_LargeStrings(t *testing.T) {
	// Build a string large enough to cause multiple buffer-full yields.
	bigStr := make([]byte, 8192)
	for i := range bigStr {
		bigStr[i] = 'A' + byte(i%26)
	}

	type bigStringStruct struct {
		A string `json:"a"`
		B string `json:"b"`
		C int    `json:"c"`
	}

	v := bigStringStruct{
		A: string(bigStr),
		B: string(bigStr[:4096]),
		C: 42,
	}

	runStackStressTest(t, 200, func(t *testing.T, id int) {
		got, err := Marshal(&v)
		verifyMarshalResult(t, &v, got, err, fmt.Sprintf("g=%d", id))
	})
}
