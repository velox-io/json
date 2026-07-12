//go:build vjstress

package venc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stress tests driven from the caller's perspective. Each test mixes real API
// entry points (Marshal, Encoder.Encode, AppendMarshal, MarshalIndent) with
// real io.Writer behaviors and lets deadlocks and memory bloat surface on
// their own. Time-bounded so a hang fails the test instead of hanging CI.

// payloads

type stressCustomMarshaler struct{ N int }

func (s stressCustomMarshaler) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"custom":%d}`, s.N), nil
}

// stressPayloads is a pool of shapes a caller would actually serialize. Mixed
// kinds force the encoder through struct, slice, map, interface, pointer,
// []byte and custom-marshaler paths in the same run.
func stressPayloads() []any {
	bigStr := strings.Repeat("x", 30000)
	inner := stackTestSimple{A: 7, B: "p", C: true}
	ptrVal := 99
	wide := make(map[string]any, 64)
	for i := range 64 {
		wide[fmt.Sprintf("k%02d", i)] = i
	}
	return []any{
		stackTestSimple{A: 42, B: "hello", C: true},
		stackTestNested{Name: "n", Inner: stackTestSimple{A: 1, B: "in", C: false}, Value: 3.14},
		stackTestDeep{},
		stackTestSlice{Items: []stackTestSimple{{A: 1, B: "a", C: true}, {A: 2, B: "b", C: false}}, Tags: []string{"x", "y"}},
		stackTestInterface{Name: "i", Value: map[string]any{"k": "v", "n": float64(42)}, Extra: []any{"a", nil, true}},
		stackTestPointer{Name: "p", Inner: &inner, Ptr: &ptrVal},
		stackTestComplex{ID: 1, Name: "c", Meta: map[string]any{"k": "v"}, Iface: "s", Tags: []string{"t"}, Flags: map[string]string{"d": "true"}},
		wide,
		map[string]string{"big": bigStr},
		map[string]any{"bytes": []byte(bigStr)},
		stressCustomMarshaler{N: 123},
		[]any{1, "two", 3.0, true, nil},
	}
}

// writers

// slowWriter sleeps per Write to apply backpressure, then accepts the bytes.
type slowWriter struct {
	mu sync.Mutex
	d  time.Duration
	n  int
}

func (w *slowWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.n++
	w.mu.Unlock()
	time.Sleep(w.d)
	return len(p), nil
}

// randomErrWriter returns a random subset of writes as errors. Exercises the
// streaming error path under load without a fixed failure point.
type randomErrWriter struct {
	mu    sync.Mutex
	p     float64
	calls int
	errs  int
}

func (w *randomErrWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.calls++
	if rand.Float64() < w.p {
		w.errs++
		w.mu.Unlock()
		return 0, errors.New("random write error")
	}
	w.mu.Unlock()
	return len(p), nil
}

// flakyWriter succeeds for the first okCalls writes, then returns err. Models
// a writer that fails mid-stream after a healthy prefix.
type flakyWriter struct {
	mu      sync.Mutex
	okCalls int
	calls   int
	err     error
	out     []byte
}

func (w *flakyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls <= w.okCalls {
		w.out = append(w.out, p...)
		return len(p), nil
	}
	return 0, w.err
}

// harness helpers

// waitOrTimeout returns true if wg completed before timeout, false otherwise.
// On false it dumps all goroutine stacks so a deadlock is diagnosable.
func waitOrTimeout(t *testing.T, wg *sync.WaitGroup, timeout time.Duration, msg string) bool {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		buf := make([]byte, 64*1024)
		n := runtime.Stack(buf, true)
		t.Fatalf("%s: timed out after %v\ngoroutine dump:\n%s", msg, timeout, buf[:n])
		return false
	}
}

// goroutineLeakCheck compares live goroutine count before and after, allowing
// a small slack for runtime/test-framework goroutines. Fails on growth.
func goroutineLeakCheck(t *testing.T, before int, label string) {
	t.Helper()
	// Give workers a moment to exit.
	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		runtime.Gosched()
		after = runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("%s: goroutine leak: before=%d after=%d", label, before, after)
}

// tests

// TestStress_MixedAPI_NoDeadlock runs many concurrent workers each randomly
// picking one of the four Marshal-family entry points and a random payload.
// Any internal deadlock or shared-state corruption surfaces as a timeout.
func TestStress_MixedAPI_NoDeadlock(t *testing.T) {
	payloads := stressPayloads()
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	const workers = 32
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), uint64(time.Now().UnixNano())))
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				v := payloads[rng.IntN(len(payloads))]
				switch id % 4 {
				case 0:
					if _, err := Marshal(v); err != nil {
						errCount.Add(1)
					}
				case 1:
					var buf bytes.Buffer
					enc := NewEncoder(&buf)
					if err := enc.Encode(v); err != nil {
						errCount.Add(1)
					}
				case 2:
					dst := make([]byte, 0, 64)
					if _, err := AppendMarshal(dst, v); err != nil {
						errCount.Add(1)
					}
				case 3:
					if _, err := MarshalIndent(v, "", "  "); err != nil {
						errCount.Add(1)
					}
				}
			}
		}(w)
	}

	if !waitOrTimeout(t, &wg, 10*time.Second, "MixedAPI deadlock") {
		return
	}
	goroutineLeakCheck(t, before, "MixedAPI")
	// Errors are expected only for unsupported types; none of our payloads
	// should produce one, so any error signals a real bug.
	if n := errCount.Load(); n != 0 {
		t.Errorf("MixedAPI: %d unexpected errors", n)
	}
}

// TestStress_SlowBlockingWriter_NoDeadlock drives Encoder.Encode of a large
// payload through writers that apply backpressure or fail randomly. The
// streaming BUF_FULL path must make progress under backpressure, not spin.
func TestStress_SlowBlockingWriter_NoDeadlock(t *testing.T) {
	big := strings.Repeat("x", 200*1024) // 200 KiB, forces multiple flushes
	v := map[string]any{"data": big}

	cases := []struct {
		name string
		make func() io.Writer
	}{
		{"slow1ms", func() io.Writer { return &slowWriter{d: time.Millisecond} }},
		{"slow100us", func() io.Writer { return &slowWriter{d: 100 * time.Microsecond} }},
		{"randomErr10pct", func() io.Writer { return &randomErrWriter{p: 0.10} }},
		{"randomErr1pct", func() io.Writer { return &randomErrWriter{p: 0.01} }},
	}

	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	const perCase = 8
	for ci, c := range cases {
		for i := range perCase {
			wg.Add(1)
			go func(caseIdx int, name string, mk func() io.Writer, seed int) {
				defer wg.Done()
				enc := NewEncoder(mk())
				// We do not care whether Encode succeeds (random error writers
				// will fail); we care that it terminates.
				_ = enc.Encode(v)
			}(ci, c.name, c.make, i)
		}
	}

	waitOrTimeout(t, &wg, 15*time.Second, "SlowBlockingWriter deadlock")
	goroutineLeakCheck(t, before, "SlowBlockingWriter")
}

// TestStress_EncoderErrorReuse verifies the sticky-error contract from a
// caller's view: after a write error, continued Encode calls must return the
// same error promptly, must not deadlock, and must not spin consuming CPU.
func TestStress_EncoderErrorReuse(t *testing.T) {
	sentinel := errors.New("flaky writer boom")
	w := &flakyWriter{okCalls: 2, err: sentinel}
	enc := NewEncoder(w)

	v := stackTestNested{Name: "x", Inner: stackTestSimple{A: 1, B: "y", C: true}, Value: 1.5}

	// First two encodes succeed.
	for i := range 2 {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("encode %d: unexpected error %v", i, err)
		}
	}
	// Third encode hits the writer error.
	if err := enc.Encode(v); err == nil {
		t.Fatal("encode 3: expected error, got nil")
	} else if !errors.Is(err, sentinel) {
		t.Fatalf("encode 3: expected sentinel, got %v", err)
	}

	before := runtime.NumGoroutine()
	// Continue hammering: every call must return the sticky error instantly.
	start := time.Now()
	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 200
	// NOTE: one Encoder is single-owner per the public contract; we serialize
	// the calls through a channel to model a caller that reuses one Encoder
	// from a worker pool rather than sharing it concurrently.
	jobs := make(chan int, workers*perWorker)
	for w := range workers {
		for i := range perWorker {
			jobs <- w*perWorker + i
		}
	}
	close(jobs)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				if err := enc.Encode(v); !errors.Is(err, sentinel) {
					t.Errorf("post-error encode: expected sentinel, got %v", err)
					return
				}
			}
		}()
	}
	if !waitOrTimeout(t, &wg, 5*time.Second, "EncoderErrorReuse deadlock") {
		return
	}
	elapsed := time.Since(start)
	goroutineLeakCheck(t, before, "EncoderErrorReuse")

	// 1600 sticky-early-return calls should be near-instant. A spin or hang
	// would blow past this budget.
	if elapsed > 500*time.Millisecond {
		t.Errorf("sticky-err path took %v for 1600 calls, expected < 500ms", elapsed)
	}
	// The writer must not have been called again after the first error.
	if w.calls > 3 {
		t.Errorf("writer called %d times after error, expected <= 3", w.calls)
	}
}

// TestStress_MarshalReturnPin_Heap verifies that Marshal's pool buffer
// management does not leak: after the caller drops all references to the
// returned slices, GC must reclaim them and HeapInuse must return to baseline.
//
// The default path returns a sub-slice of a pooled 32 KiB buffer, and
// WithBufSize opts into a tight copy. We exercise both paths interleaved, hold
// all results, record the peak, then release and force GC. The assertion is on
// the post-release HeapInuse, not on the peak: pool reuse means the default
// path's pin footprint is dominated by how many distinct backing arrays got
// promoted (a function of payload size and N), not by N itself, so a strict
// default-vs-tight ratio would be brittle. What matters for correctness is
// that nothing stays pinned after the caller lets go.
func TestStress_MarshalReturnPin_Heap(t *testing.T) {
	v := stackTestSimple{A: 1, B: "small", C: true}
	const n = 1000

	// Baseline: force GC twice so the runtime settles before sampling.
	runtime.GC()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	// Phase 1: interleave default and WithBufSize paths, holding every result.
	hold := make([][]byte, 0, n*2)
	for range n {
		b1, err := Marshal(v)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		hold = append(hold, b1)

		b2, err := Marshal(v, WithBufSize(64))
		if err != nil {
			t.Fatalf("Marshal WithBufSize: %v", err)
		}
		hold = append(hold, b2)
	}

	// Peak: GC first so HeapInuse reflects only what hold pins (plus other
	// roots), not transient garbage from the in-flight Marshal loop. This
	// matches the baseline/after sampling discipline.
	runtime.GC()
	runtime.GC()
	var peak runtime.MemStats
	runtime.ReadMemStats(&peak)
	t.Logf("peak HeapInuse: %d KiB (held %d slices)", peak.HeapInuse>>10, len(hold))

	// Loose upper bound on the peak. Current behavior pins only the pool's
	// resident backing arrays (a few hundred KiB for 2000 small results); if
	// someone changes the default path to allocate a fresh 32 KiB buffer per
	// call instead of pool reuse, peak would jump to ~31 MiB and trip this.
	const peakCeiling = 10 << 20 // 10 MiB
	if peak.HeapInuse > peakCeiling {
		t.Errorf("peak HeapInuse %d KiB exceeded ceiling %d MiB (pool reuse broken?)",
			peak.HeapInuse>>10, peakCeiling>>20)
	}

	// Phase 2: drop all references and force GC. Nothing the pool handed out
	// should remain reachable from the caller, so HeapInuse must fall back
	// near baseline. The 1 MiB slack covers runtime/test-framework noise and
	// any type-info caches populated during the run.
	hold = nil
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	delta := int64(after.HeapInuse) - int64(baseline.HeapInuse)
	t.Logf("HeapInuse: baseline=%d KiB, after=%d KiB, delta=%d KiB",
		baseline.HeapInuse>>10, after.HeapInuse>>10, delta>>10)

	if delta > 1<<20 {
		t.Errorf("heap did not return to baseline after release: delta=%d KiB (expected < 1 MiB)", delta>>10)
	}
}

// TestStress_LongRun_SteadyHeap runs a sustained mixed-API workload and samples
// HeapAlloc after GC at regular intervals. A leak shows up as monotonic growth
// across samples; a healthy pool stays bounded. Skipped under -short.
func TestStress_LongRun_SteadyHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-run stress under -short")
	}
	payloads := stressPayloads()

	const window = 5 * time.Second
	const sampleEvery = 1 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()

	var wg sync.WaitGroup
	const workers = 16
	stop := atomic.Bool{}
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), 99))
			for {
				if stop.Load() {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				v := payloads[rng.IntN(len(payloads))]
				switch rng.IntN(4) {
				case 0:
					_, _ = Marshal(v)
				case 1:
					var buf bytes.Buffer
					_ = NewEncoder(&buf).Encode(v)
				case 2:
					_, _ = AppendMarshal(make([]byte, 0, 64), v)
				case 3:
					_, _ = MarshalIndent(v, "", "  ")
				}
			}
		}(w)
	}

	// Sample HeapAlloc after GC every sampleEvery until the window closes.
	type sample struct {
		t  time.Duration
		mb uint64
	}
	var samples []sample
	sampleTick := time.NewTicker(sampleEvery)
	defer sampleTick.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			goto done
		case <-sampleTick.C:
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			samples = append(samples, sample{t: time.Since(start), mb: ms.HeapAlloc >> 20})
		}
	}
done:
	stop.Store(true)
	if !waitOrTimeout(t, &wg, 3*time.Second, "LongRun shutdown") {
		return
	}

	if len(samples) < 2 {
		t.Fatalf("only %d samples collected", len(samples))
	}
	first := samples[0].mb
	last := samples[len(samples)-1].mb
	t.Logf("HeapAlloc samples (MiB): %v", samples)

	// Allow fluctuation, but the final GC'd sample must not be far above the
	// first. A 2x ceiling catches steady leaks while tolerating normal churn.
	if last > first*2 && last > first+4 {
		t.Errorf("heap grew under sustained load: first=%d MiB last=%d MiB", first, last)
	}
}

// TestStress_ConcurrentMixedWriters_NoDeadlock combines the two pressure
// axes: many goroutines, each using a different writer behavior, interleaved
// with plain Marshal calls. Surfaces any cross-path interaction between the
// pooled Marshal buffer and the streaming Encoder buffer.
func TestStress_ConcurrentMixedWriters_NoDeadlock(t *testing.T) {
	payloads := stressPayloads()
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	const workers = 24
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), 7))
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				v := payloads[rng.IntN(len(payloads))]

				switch id % 5 {
				case 0:
					// Plain Marshal, no held reference.
					_, _ = Marshal(v)
				case 1:
					// Encoder into a buffered sink.
					var buf bytes.Buffer
					_ = NewEncoder(&buf).Encode(v)
				case 2:
					// Encoder under backpressure.
					_ = NewEncoder(&slowWriter{d: 200 * time.Microsecond}).Encode(v)
				case 3:
					// Encoder that may fail mid-stream.
					_ = NewEncoder(&flakyWriter{okCalls: rng.IntN(5), err: errors.New("boom")}).Encode(v)
				case 4:
					// AppendMarshal, then drop the result.
					_, _ = AppendMarshal(make([]byte, 0, 128), v, WithStdCompat())
				}
			}
		}(w)
	}

	waitOrTimeout(t, &wg, 8*time.Second, "ConcurrentMixedWriters deadlock")
	goroutineLeakCheck(t, before, "ConcurrentMixedWriters")
}

// TestStress_OutputCorrectnessUnderConcurrency is a sanity check that
// concurrent stress does not silently corrupt output. Each worker marshals a
// fixed payload and compares against a reference computed once. A shared-state
// bug (e.g. pooled buffer bleed) would surface as a mismatch.
func TestStress_OutputCorrectnessUnderConcurrency(t *testing.T) {
	v := stackTestComplex{
		ID: 1, Name: "cc",
		Nested: stackTestNested{Name: "n", Inner: stackTestSimple{A: 2, B: "n", C: true}, Value: 2.0},
		Items:  []stackTestSimple{{A: 10, B: "i1", C: true}, {A: 20, B: "i2", C: false}},
		Meta:   map[string]any{"k1": "v1", "k2": float64(42)},
		Iface:  []any{"hi", float64(3.14), nil},
		Tags:   []string{"a", "b"},
		Flags:  map[string]string{"d": "true"},
	}
	ref, err := Marshal(v)
	if err != nil {
		t.Fatalf("reference Marshal: %v", err)
	}
	// Semantic reference (map key order is not stable).
	var refVal any
	if err := json.Unmarshal(ref, &refVal); err != nil {
		t.Fatalf("reference unmarshal: %v", err)
	}

	const workers = 16
	const perWorker = 500
	var wg sync.WaitGroup
	errCh := make(chan string, workers)
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range perWorker {
				got, err := Marshal(v)
				if err != nil {
					errCh <- fmt.Sprintf("w%d: %v", id, err)
					return
				}
				// Fast path: byte-exact.
				if bytes.Equal(got, ref) {
					continue
				}
				// Slow path: semantic compare (map ordering).
				var gotVal any
				if err := json.Unmarshal(got, &gotVal); err != nil {
					errCh <- fmt.Sprintf("w%d: invalid JSON %q", id, got)
					return
				}
				if fmt.Sprintf("%v", gotVal) != fmt.Sprintf("%v", refVal) {
					errCh <- fmt.Sprintf("w%d: semantic mismatch got=%s", id, got)
					return
				}
			}
		}(w)
	}
	if !waitOrTimeout(t, &wg, 10*time.Second, "Correctness deadlock") {
		return
	}
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

// TestStress_MarshalReturnPin_PerCallPromotion verifies that each Marshal
// call, when forced to allocate a fresh encodeState, pins a distinct 32 KiB
// backing array that survives until the caller drops the returned slice.
//
// In a serial loop without intervention, sync.Pool reuse makes every returned
// slice alias the same backing array (this is what TestStress_MarshalReturnPin_Heap
// observes: peak ~= baseline). To exercise the per-call promotion path, we
// force a pool eviction (two GCs sync.Pool victim-cache handshake) before
// each Marshal so the next acquire must New a fresh encodeState with its own
// 32 KiB backing array. The caller holds every returned slice, so each pins
// a distinct backing array; peak HeapInuse must grow in proportion to N and
// return to baseline once the caller lets go.
//
// The per-iteration double-GC binds to sync.Pool's victim-cache implementation
// detail. It is a regression net for the pool's New function and the
// marshalWith zero-copy return contract, not a usage-driven stress test.
func TestStress_MarshalReturnPin_PerCallPromotion(t *testing.T) {
	v := stackTestSimple{A: 1, B: "small", C: true}

	// n must be large enough that n * encBufInitSize clearly exceeds GC
	// noise (~1 MiB). 64 * 32 KiB = 2 MiB.
	const n = 64

	runtime.GC()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	// Phase 1: between each Marshal, force a pool eviction (two GCs cover the
	// sync.Pool main -> victim -> drop handshake) so the next Marshal must New
	// a fresh encodeState. Each returned slice now aliases a distinct 32 KiB
	// backing array.
	held := make([][]byte, n)
	for i := range held {
		runtime.GC()
		runtime.GC()
		b, err := Marshal(v)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		held[i] = b
	}

	runtime.GC()
	runtime.GC()
	var peak runtime.MemStats
	runtime.ReadMemStats(&peak)

	// Each held slice pins a distinct 32 KiB backing array. Allow slack for
	// GC timing but require clear growth proportional to n.
	expectedPin := int64(n) * encBufInitSize
	peakDelta := int64(peak.HeapInuse) - int64(baseline.HeapInuse)
	t.Logf("peak HeapInuse: %d KiB, baseline=%d KiB, delta=%d KiB (expected ~%d KiB)",
		peak.HeapInuse>>10, baseline.HeapInuse>>10, peakDelta>>10, expectedPin>>10)

	if peakDelta < expectedPin/2 {
		t.Errorf("peak delta only %d KiB, expected >= %d KiB (pool not creating a fresh 32 KiB buffer per Marshal?)",
			peakDelta>>10, (expectedPin/2)>>10)
	}
	if peakDelta > expectedPin*3 {
		t.Errorf("peak delta %d KiB exceeds 3x expected %d KiB (unexpected allocation source?)",
			peakDelta>>10, (expectedPin*3)>>10)
	}

	// Phase 2: release all references and force GC. The pinned backing arrays
	// should be reclaimed and HeapInuse should return near baseline.
	for i := range held {
		held[i] = nil
	}
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	afterDelta := int64(after.HeapInuse) - int64(baseline.HeapInuse)
	t.Logf("after HeapInuse: %d KiB, delta=%d KiB", after.HeapInuse>>10, afterDelta>>10)

	if afterDelta > 1<<20 {
		t.Errorf("heap did not return to baseline after release: delta=%d KiB (expected < 1 MiB)", afterDelta>>10)
	}
}

