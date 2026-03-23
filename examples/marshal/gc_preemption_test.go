package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "github.com/velox-io/json"
)

// TestVMExecGCPreemption verifies that the C VM (running via nosplit trampoline
// on the goroutine stack) does not crash when Go's GC / async preemption fires.
//
// Strategy:
//   - Spawn many goroutines all doing Marshal concurrently, so the VM is active
//     on many goroutine stacks simultaneously.
//   - Spawn a dedicated goroutine that aggressively triggers GC (runtime.GC)
//     and forces stack scans.
//   - Use a large, deeply-nested struct so that the VM runs for a long time
//     per call, maximizing the window where preemption / GC stack scanning
//     could hit the C frame.
//
// If the runtime crashes (SIGSEGV, "unexpected return pc", "unknown pc", etc.),
// the test fails.
func TestVMExecGCPreemption(t *testing.T) {
	// Reduce GC threshold so the collector fires more often.
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	// Use all available CPUs to maximize scheduling contention.
	prevProcs := runtime.GOMAXPROCS(runtime.NumCPU())
	defer runtime.GOMAXPROCS(prevProcs)

	const (
		numWorkers = 32
		duration   = 5 * time.Second
	)

	// Build a large test payload that exercises the VM for a long time:
	// many nested structs, slices, maps, interface fields.
	payload := buildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
		errOnce sync.Once
		testErr error
	)

	// --- GC hammer goroutine ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			// Also Gosched to encourage preemption on worker goroutines.
			runtime.Gosched()
		}
	}()

	// --- Allocation pressure goroutines ---
	// Create garbage to keep the GC busy scanning stacks.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sink [][]byte
			for !stop.Load() {
				// Allocate and immediately discard to pressure GC.
				sink = append(sink, make([]byte, 1024))
				if len(sink) > 1000 {
					sink = sink[:0]
				}
				runtime.Gosched()
			}
			_ = sink
		}()
	}

	// --- Marshal worker goroutines ---
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := json.Marshal(payload)
				if err != nil {
					errOnce.Do(func() { testErr = err })
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

	// Let the test run for the specified duration.
	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	if testErr != nil {
		t.Fatalf("Marshal returned error under GC pressure: %v", testErr)
	}

	ops := opCount.Load()
	t.Logf("Completed %d marshal ops in %v (%d ops/sec) with %d workers — no crash",
		ops, duration, ops/int64(duration.Seconds()), numWorkers)
}

// TestVMExecGCPreemptionIndent is the same test but uses MarshalIndent,
// which exercises the "full" VM mode (indent + escape flags).
func TestVMExecGCPreemptionIndent(t *testing.T) {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	prevProcs := runtime.GOMAXPROCS(runtime.NumCPU())
	defer runtime.GOMAXPROCS(prevProcs)

	const (
		numWorkers = 32
		duration   = 5 * time.Second
	)

	payload := buildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
		errOnce sync.Once
		testErr error
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			runtime.Gosched()
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sink [][]byte
			for !stop.Load() {
				sink = append(sink, make([]byte, 1024))
				if len(sink) > 1000 {
					sink = sink[:0]
				}
				runtime.Gosched()
			}
			_ = sink
		}()
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					errOnce.Do(func() { testErr = err })
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	if testErr != nil {
		t.Fatalf("MarshalIndent returned error under GC pressure: %v", testErr)
	}

	ops := opCount.Load()
	t.Logf("Completed %d MarshalIndent ops in %v (%d ops/sec) with %d workers — no crash",
		ops, duration, ops/int64(duration.Seconds()), numWorkers)
}

// TestVMExecGCPreemptionWithHTMLEscape exercises the "compact" VM mode
// (escape flags enabled, no indent).
func TestVMExecGCPreemptionWithHTMLEscape(t *testing.T) {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	prevProcs := runtime.GOMAXPROCS(runtime.NumCPU())
	defer runtime.GOMAXPROCS(prevProcs)

	const (
		numWorkers = 32
		duration   = 5 * time.Second
	)

	payload := buildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
		errOnce sync.Once
		testErr error
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			runtime.Gosched()
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sink [][]byte
			for !stop.Load() {
				sink = append(sink, make([]byte, 1024))
				if len(sink) > 1000 {
					sink = sink[:0]
				}
				runtime.Gosched()
			}
			_ = sink
		}()
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := json.Marshal(payload, json.WithEscapeHTML())
				if err != nil {
					errOnce.Do(func() { testErr = err })
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	if testErr != nil {
		t.Fatalf("Marshal(EscapeHTML) returned error under GC pressure: %v", testErr)
	}

	ops := opCount.Load()
	t.Logf("Completed %d Marshal(EscapeHTML) ops in %v (%d ops/sec) with %d workers — no crash",
		ops, duration, ops/int64(duration.Seconds()), numWorkers)
}

// TestVMExecStackGrowth checks that stack growth (triggered by deep recursion
// in other goroutines) doesn't corrupt the C VM's view of the stack.
func TestVMExecStackGrowth(t *testing.T) {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	const (
		numWorkers = 16
		duration   = 5 * time.Second
	)

	payload := buildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
		errOnce sync.Once
		testErr error
	)

	// Stack growth goroutines: deep recursion forces stack relocation.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				deepRecursion(200)
				runtime.Gosched()
			}
		}()
	}

	// GC hammer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			runtime.Gosched()
		}
	}()

	// Marshal workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := json.Marshal(payload)
				if err != nil {
					errOnce.Do(func() { testErr = err })
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	if testErr != nil {
		t.Fatalf("Marshal returned error during stack growth stress: %v", testErr)
	}

	t.Logf("Completed %d marshal ops under stack growth pressure — no crash", opCount.Load())
}

// deepRecursion burns stack space to trigger goroutine stack growth.
//
//go:noinline
func deepRecursion(n int) int {
	if n <= 0 {
		runtime.Gosched()
		return 1
	}
	// Allocate some stack space in each frame.
	var pad [64]byte
	pad[0] = byte(n)
	return deepRecursion(n-1) + int(pad[0])
}

// --- Payload construction ---

// LargePayload is a deeply nested struct designed to keep the C VM busy
// for a long time per Marshal call.
type LargePayload struct {
	Users    [64]User       `json:"users"`
	Index    map[string]int `json:"index"`
	Metadata map[string]any `json:"metadata"`
}

func buildLargePayload() LargePayload {
	var p LargePayload

	// Fill 64 copies of the test user — the VM iterates the full struct
	// array without yielding to Go (unless it hits interface/map fields).
	base := NewTestUser()
	for i := range p.Users {
		u := base
		u.Name = fmt.Sprintf("user_%d", i)
		u.Age = 20 + i
		u.Nickname = fmt.Sprintf("nick_%d", i)
		p.Users[i] = u
	}

	// Large string-keyed map to exercise MAP_STR_ITER in C.
	p.Index = make(map[string]int, 200)
	for i := 0; i < 200; i++ {
		p.Index[fmt.Sprintf("key_%04d", i)] = i
	}

	// map[string]any with nested values to trigger interface cache lookups
	// and on-the-fly Blueprint compilation.
	p.Metadata = map[string]any{
		"string_val": "hello world",
		"int_val":    42,
		"float_val":  3.14159,
		"bool_val":   true,
		"null_val":   nil,
		"nested_map": map[string]any{
			"a": "value_a",
			"b": 123,
			"c": map[string]any{
				"deep": true,
			},
		},
		"slice_val": []any{"x", "y", "z", 1, 2, 3},
	}

	return p
}

// TestVMExecPreemptionSignal specifically targets async preemption (SIGURG)
// by running with GOFLAGS=-race-like scheduling and checking for panics.
func TestVMExecPreemptionSignal(t *testing.T) {
	if os.Getenv("GOGC") == "" {
		// Run sub-process with aggressive GC if not already set.
		t.Setenv("GOGC", "5")
	}

	old := debug.SetGCPercent(5)
	defer debug.SetGCPercent(old)

	const (
		numWorkers = 64
		duration   = 5 * time.Second
	)

	payload := buildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
	)

	// Maximize preemption opportunities: each goroutine runs a tight
	// Marshal loop. With GOGC=5 and 64 workers, the runtime will
	// attempt async preemption very frequently.
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := json.Marshal(payload)
				if err != nil {
					t.Errorf("Marshal error: %v", err)
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

	// GC hammer with high frequency
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
		}
	}()

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	t.Logf("Completed %d marshal ops with aggressive preemption — no crash", opCount.Load())
}
