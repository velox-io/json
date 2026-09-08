//go:build vjgcstress

// GC and async-preemption stress for the C encoder VM, run by the
// encode leg of scripts/gc-stress.sh. Under this build the venc entry
// point also forces a collection before every VM exec, so each test
// stacks its own pressure on top of that deterministic window.
package preemptstress

import (
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "github.com/velox-io/json"
)

// TestPreemptStress_GC hammers every VM mode with concurrent
// collection: each variant drives one encoder specialization, so mark
// and preemption arrive inside each compiled path.
func TestPreemptStress_GC(t *testing.T) {
	variants := []struct {
		name    string
		marshal func(any) ([]byte, error)
	}{
		{"marshal", func(v any) ([]byte, error) { return json.Marshal(v) }},
		{"marshalIndent", func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }},
		{"marshalEscapeHTML", func(v any) ([]byte, error) {
			return json.Marshal(v, json.WithEscapeHTML())
		}},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			runGCHammer(t, v.marshal)
		})
	}
}

// runGCHammer runs marshal workers alongside a dedicated runtime.GC
// driver and allocation pressure, so concurrent mark and preemption
// arrive while the VM is active on many goroutine stacks at once.
func runGCHammer(t *testing.T, marshal func(any) ([]byte, error)) {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	const (
		numWorkers = 32
		duration   = 5 * time.Second
	)

	payload := BuildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
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
				if _, err := marshal(payload); err != nil {
					t.Errorf("marshal: %v", err)
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

	t.Logf("%d marshal ops in %v across %d workers", opCount.Load(), duration, numWorkers)
}

// TestPreemptStress_StackGrowth pairs marshal workers with deep
// recursion in other goroutines, forcing stack growth and relocation
// while the VM runs, so a stale stack address inside VM state faults.
func TestPreemptStress_StackGrowth(t *testing.T) {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)

	const (
		numWorkers = 16
		duration   = 5 * time.Second
	)

	payload := BuildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
	)

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

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			runtime.Gosched()
		}
	}()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := json.Marshal(payload); err != nil {
					t.Errorf("marshal: %v", err)
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

	t.Logf("%d marshal ops under stack growth pressure", opCount.Load())
}

// deepRecursion burns stack to trigger goroutine stack growth.
//
//go:noinline
func deepRecursion(n int) int {
	if n <= 0 {
		runtime.Gosched()
		return 1
	}
	var pad [64]byte
	pad[0] = byte(n)
	return deepRecursion(n-1) + int(pad[0])
}

// TestPreemptStress_Signal targets async preemption directly: 64
// workers in tight marshal loops under SetGCPercent(5), so preemption
// signals fire at maximum frequency and land inside active C frames.
func TestPreemptStress_Signal(t *testing.T) {
	old := debug.SetGCPercent(5)
	defer debug.SetGCPercent(old)

	const (
		numWorkers = 64
		duration   = 5 * time.Second
	)

	payload := BuildLargePayload()

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		opCount atomic.Int64
	)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := json.Marshal(payload); err != nil {
					t.Errorf("marshal: %v", err)
					stop.Store(true)
					return
				}
				opCount.Add(1)
			}
		}()
	}

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

	t.Logf("%d marshal ops under aggressive preemption", opCount.Load())
}
