// Package stackstress drives vjson native roundtrips at the edge of
// goroutine stack exhaustion.
//
// Native entry chains run behind split Go wrappers whose prologue
// guarantees a fixed headroom below SP (abi.StackNosplitBase). The
// static stackdepth check pins each C chain inside that budget. This
// package attacks the same invariant at runtime: a recursion consumes
// the goroutine stack to a parameterized depth and the leaf performs
// the roundtrip. Stack growth sizes the new stack to the requesting
// frame, so a leaf reached through consumption always lands at a few
// KB remaining; the Go call envelope takes most of that and the native
// chain runs just above the stack limit. A chain overrun past its
// budget crosses the limit and corrupts adjacent memory or crashes.
//
// The depth parameter mainly drives how many growth cycles the step
// passes through; every leaf lands at the same tight water level.
// Detection is process survival, captured panics, and differential
// verification of each roundtrip result.
package stackstress

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

const (
	// FineStep is the nominal consumption increment between sweep steps.
	FineStep = 128
	// FineMax is the total nominal consumption range covered by one sweep.
	FineMax = 8192
)

var sink atomic.Uint64

// Case is one stress workload. Run performs only the native roundtrip
// and returns either a result or an error. Verify compares the result
// against a reference.
type Case struct {
	Name   string
	Run    func() any
	Verify func(res any) error
}

// AtDepth burns approximately depth bytes of goroutine stack through
// noinline recursion, then invokes leaf at that depth. Coarse 1KB
// frames carry the bulk, fine FineStep frames carry the tail.
func AtDepth(depth int, leaf func()) {
	consumeCoarseAt(depth/1024, func() {
		consumeFineAt((depth%1024)/FineStep, leaf)
	})
}

// RunAtDepth runs fn in a fresh goroutine with approximately depth
// bytes of stack consumed, with panic recovery. It returns the
// recovered panic text together with a stack snapshot, or the empty
// string.
func RunAtDepth(depth int, fn func()) string {
	c := Case{
		Name: "run-at-depth",
		Run: func() any {
			fn()
			return nil
		},
	}
	return runStepGoroutine(c, depth).Panic
}

//go:noinline
func consumeCoarseAt(levels int, rest func()) {
	if levels <= 0 {
		rest()
		return
	}
	var pad [1024]byte
	i := levels & (len(pad) - 1)
	pad[i] = byte(levels)
	sink.Store(uint64(pad[i]))
	consumeCoarseAt(levels-1, rest)
}

//go:noinline
func consumeFineAt(steps int, leaf func()) {
	if steps <= 0 {
		leaf()
		return
	}
	var pad [128]byte
	i := steps & (len(pad) - 1)
	pad[i] = byte(steps)
	sink.Store(uint64(pad[i]))
	consumeFineAt(steps-1, leaf)
}

type stepReport struct {
	Case  string
	Step  int
	Grew  bool
	Panic string
	Res   any
}

// runRecovered executes fn with panic recovery and captures the panic
// together with a stack snapshot.
func runRecovered(fn func() any) (res any, panicText string) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			panicText = fmt.Sprintf("%v\n%s", r, buf[:n])
		}
	}()
	return fn(), ""
}

// runStep executes one workload at the current depth. Anchor samples
// around Run flag steps where the roundtrip itself relocated the stack,
// meaning the growth path carried the native entry.
//
//go:noinline
func runStep(c Case, rep *stepReport) {
	var anchor byte
	a1 := uintptr(unsafe.Pointer(&anchor))
	res, panicText := runRecovered(c.Run)
	a2 := uintptr(unsafe.Pointer(&anchor))
	rep.Grew = a1 != a2
	rep.Panic = panicText
	rep.Res = res
	sink.Store(uint64(anchor))
}

// runStepGoroutine runs one step in a fresh goroutine starting from a
// pristine initial stack.
func runStepGoroutine(c Case, depth int) (rep stepReport) {
	step := depth / FineStep
	rep.Case = c.Name
	rep.Step = step
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				rep.Panic = fmt.Sprintf("step panic: %v\n%s", r, buf[:n])
			}
		}()
		AtDepth(depth, func() {
			runStep(c, &rep)
		})
	}()
	<-done
	return rep
}

// judge turns one step report into failure messages.
func judge(c Case, rep stepReport) []string {
	var msgs []string
	if rep.Panic != "" {
		msgs = append(msgs, fmt.Sprintf("[%s step=%d grew=%v] panic: %s", c.Name, rep.Step, rep.Grew, rep.Panic))
		return msgs
	}
	if err, ok := rep.Res.(error); ok {
		msgs = append(msgs, fmt.Sprintf("[%s step=%d grew=%v] run error: %v", c.Name, rep.Step, rep.Grew, err))
		return msgs
	}
	if c.Verify != nil {
		if err := c.Verify(rep.Res); err != nil {
			msgs = append(msgs, fmt.Sprintf("[%s step=%d grew=%v] %v", c.Name, rep.Step, rep.Grew, err))
		}
	}
	return msgs
}

// Sweep runs c at fine consumptions 0..FineMax in FineStep increments,
// one fresh goroutine per step.
func Sweep(t *testing.T, c Case) {
	t.Helper()
	logFrameSizes(t, c.Name)
	steps := FineMax / FineStep
	grewCount := 0
	for s := 0; s <= steps; s++ {
		rep := runStepGoroutine(c, s*FineStep)
		if rep.Grew {
			grewCount++
		}
		for _, m := range judge(c, rep) {
			t.Error(m)
		}
	}
	t.Logf("[%s] grew=%d/%d steps", c.Name, grewCount, steps+1)
}

// SweepConcurrent drives the steps of all cases from workers concurrent
// goroutines while a disturber goroutine keeps the GC cycling, adding
// stack shrink and relocation pressure between steps.
func SweepConcurrent(t *testing.T, cases []Case, workers int) {
	SweepDuration(t, cases, workers, 0)
}

// SweepDuration drives the steps of all cases from workers concurrent
// goroutines while a disturber goroutine keeps the GC cycling, adding
// stack shrink and relocation pressure between steps. A positive dur
// loops the step list until the deadline so short-lived step goroutines
// keep recycling stacks through the pool; a stomp below a stack limit
// then lands in neighboring live stacks and surfaces as a crash.
func SweepDuration(t *testing.T, cases []Case, workers int, dur time.Duration) {
	t.Helper()
	type task struct{ caseIdx, step int }
	tasks := make([]task, 0, len(cases)*(FineMax/FineStep+1))
	for i := range cases {
		for s := 0; s <= FineMax/FineStep; s++ {
			tasks = append(tasks, task{i, s})
		}
	}
	var next atomic.Int64
	errCh := make(chan string, 1024)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
				runtime.Gosched()
			}
		}
	}()
	var inner sync.WaitGroup
	for range workers {
		inner.Add(1)
		go func() {
			defer inner.Done()
			for {
				idx := int(next.Add(1)) - 1
				if dur == 0 && idx >= len(tasks) {
					return
				}
				select {
				case <-stop:
					return
				default:
				}
				tk := tasks[idx%len(tasks)]
				rep := runStepGoroutine(cases[tk.caseIdx], tk.step*FineStep)
				for _, m := range judge(cases[tk.caseIdx], rep) {
					errCh <- m
				}
			}
		}()
	}
	if dur > 0 {
		time.Sleep(dur)
		close(stop)
	}
	inner.Wait()
	if dur == 0 {
		close(stop)
	}
	wg.Wait()
	close(errCh)
	for m := range errCh {
		t.Error(m)
	}
}

//go:noinline
func fineFrameProbe(level int, addrs *[2]uintptr) {
	var pad [128]byte
	sink.Store(uint64(pad[0]))
	addrs[level] = uintptr(unsafe.Pointer(&pad[0]))
	if level < 1 {
		fineFrameProbe(level+1, addrs)
	}
}

//go:noinline
func coarseFrameProbe(level int, addrs *[2]uintptr) {
	var pad [1024]byte
	sink.Store(uint64(pad[0]))
	addrs[level] = uintptr(unsafe.Pointer(&pad[0]))
	if level < 1 {
		coarseFrameProbe(level+1, addrs)
	}
}

func absDiff(a, b uintptr) uintptr {
	if a > b {
		return a - b
	}
	return b - a
}

// logFrameSizes reports the measured consumption granularity of the two
// recursion helpers, taken from locals one recursion level apart.
func logFrameSizes(t *testing.T, name string) {
	t.Helper()
	var fa [2]uintptr
	fineFrameProbe(0, &fa)
	fine := absDiff(fa[0], fa[1])
	var ca [2]uintptr
	coarseFrameProbe(0, &ca)
	coarse := absDiff(ca[0], ca[1])
	t.Logf("[%s] measured frames: fine=%dB coarse=%dB", name, fine, coarse)
}
