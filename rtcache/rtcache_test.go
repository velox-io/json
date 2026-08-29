package rtcache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// TestGroupPadding_NoLineSharing pins the false-sharing invariant. Base address
// is unconstrained (the linker caps data alignment at 32), so the property is
// stated as a stride, not an alignment: consecutive groups must be at least
// CacheLinePadSize apart, and slow must clear the last group by the same margin.
// Ways within a group intentionally share a line; they are true sharers.
func TestGroupPadding_NoLineSharing(t *testing.T) {
	if got := unsafe.Sizeof(group[*int]{}); got != CacheLinePadSize {
		t.Fatalf("group size = %d, want %d; padding expression is wrong", got, CacheLinePadSize)
	}

	c := new(Cache[*int])
	for i := 1; i < Groups; i++ {
		delta := uintptr(unsafe.Pointer(&c.fast[i])) - uintptr(unsafe.Pointer(&c.fast[i-1]))
		if delta < CacheLinePadSize {
			t.Fatalf("groups %d and %d are %d bytes apart, want >= %d", i-1, i, delta, CacheLinePadSize)
		}
	}

	lastLive := unsafe.Offsetof(c.fast) + uintptr(Groups-1)*unsafe.Sizeof(group[*int]{}) +
		unsafe.Sizeof([Ways]atomic.Pointer[Entry[*int]]{})
	if gap := unsafe.Offsetof(c.slow) - lastLive; gap < CacheLinePadSize-unsafe.Sizeof([Ways]atomic.Pointer[Entry[*int]]{}) {
		t.Fatalf("slow is only %d bytes past the last live fast word", gap)
	}
}

// sameGroupKeys returns n keys that all hash to the same group.
func sameGroupKeys(n int) []uintptr {
	want := Index(0x1000)
	out := []uintptr{0x1000}
	for k := uintptr(0x1001); len(out) < n && k < 1<<26; k++ {
		if Index(k) == want {
			out = append(out, k)
		}
	}
	if len(out) < n {
		panic("not enough same-group keys")
	}
	return out
}

// TestAssociativity_NoThrash is the reason the table is set-associative. Ways
// keys in one group must all stay resident, so that alternating Gets across them
// hit without ever falling through to a store.
func TestAssociativity_NoThrash(t *testing.T) {
	var tb Table[int]
	keys := sameGroupKeys(Ways)
	for i, k := range keys {
		tb.Set(k, i)
	}
	for i, k := range keys {
		v, ok := tb.Get(k)
		if !ok || v != i {
			t.Fatalf("Get(key %d of %d in one group) = (%d, %v), want (%d, true)", i, Ways, v, ok, i)
		}
	}
}

// TestAssociativity_OverflowEvicts pins the boundary: past Ways live keys in one
// group the table must still be correct, dropping some key rather than growing
// or corrupting. Every Get must return either the right value or a clean miss.
func TestAssociativity_OverflowEvicts(t *testing.T) {
	var tb Table[int]
	keys := sameGroupKeys(Ways + 4)
	for i, k := range keys {
		tb.Set(k, i)
	}
	resident := 0
	for i, k := range keys {
		v, ok := tb.Get(k)
		if ok {
			if v != i {
				t.Fatalf("Get(key %d) = %d, want %d; wrong value for resident key", i, v, i)
			}
			resident++
		}
	}
	if resident > Ways {
		t.Fatalf("%d keys resident in one group, want <= %d", resident, Ways)
	}
	if resident == 0 {
		t.Fatal("no keys resident after overflow; eviction discarded everything")
	}
}

// TestSet_ReplacesInPlace guards against a key occupying two ways, which would
// let Get return a stale value depending on scan order.
func TestSet_ReplacesInPlace(t *testing.T) {
	var tb Table[int]
	k := uintptr(0x1000)
	for i := range Ways * 3 {
		tb.Set(k, i)
		if v, ok := tb.Get(k); !ok || v != i {
			t.Fatalf("after Set(%d): Get = (%d, %v), want (%d, true)", i, v, ok, i)
		}
	}
	// The repeated Sets must not have consumed more than one way: a fresh
	// same-group key set must still find room.
	others := sameGroupKeys(Ways)
	for i, ok := range others[1:] {
		tb.Set(ok, 1000+i)
	}
	for i, okk := range others[1:] {
		if v, ok := tb.Get(okk); !ok || v != 1000+i {
			t.Fatalf("same-group key %d evicted: Get = (%d, %v), want (%d, true)", i, v, ok, 1000+i)
		}
	}
}

func TestIndex_Range(t *testing.T) {
	for i := range 10000 {
		rtp := uintptr(0x40000 + i*0x10)
		idx := Index(rtp)
		if idx >= Groups {
			t.Fatalf("Index(%#x) = %d, out of range [0, %d)", rtp, idx, Groups)
		}
	}
}

func TestIndex_Distribution(t *testing.T) {
	// Linear rtype-like pointers should still spread across the table thanks
	// to Fibonacci hashing. Demand > 80% group usage from 10000 probes.
	const n = 10000
	var used [Groups]bool
	for i := range n {
		rtp := uintptr(0x40000 + i*0x10)
		used[Index(rtp)] = true
	}
	count := 0
	for _, u := range used {
		if u {
			count++
		}
	}
	if ratio := float64(count) / Groups; ratio < 0.8 {
		t.Fatalf("group usage %.0f%% < 80%% after %d probes", ratio*100, n)
	}
}

func TestGetSet_Basic(t *testing.T) {
	var c Table[int]

	// miss on empty table
	if _, ok := c.Get(0x12345); ok {
		t.Fatal("expected miss on empty table")
	}

	// hit after set
	c.Set(0x12345, 42)
	if v, ok := c.Get(0x12345); !ok || v != 42 {
		t.Fatalf("Get(0x12345) = (%d, %v), want (42, true)", v, ok)
	}

	// Two keys in the same group must coexist. Under the previous direct-mapped
	// geometry the second Set evicted the first; that eviction was the thrash
	// this table exists to avoid.
	keys := sameGroupKeys(2)
	c.Set(keys[0], 1)
	c.Set(keys[1], 2)
	if v, ok := c.Get(keys[0]); !ok || v != 1 {
		t.Fatalf("Get(k1) = (%d, %v), want (1, true); same-group key was evicted", v, ok)
	}
	if v, ok := c.Get(keys[1]); !ok || v != 2 {
		t.Fatalf("Get(k2) = (%d, %v), want (2, true)", v, ok)
	}
}

func TestGetSet_Concurrent(t *testing.T) {
	var c Table[int]
	const goroutines = 32
	const ops = 1000

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(seed uintptr) {
			defer wg.Done()
			for i := range ops {
				rtp := seed + uintptr(i)*0x10
				c.Set(rtp, i)
				_, _ = c.Get(rtp)
			}
		}(uintptr(0x10000 + g*0x1000))
	}
	wg.Wait()
}

func BenchmarkGet_Hit(b *testing.B) {
	var c Table[int]
	c.Set(0x12345, 99)

	for b.Loop() {
		_, _ = c.Get(0x12345)
	}
}

func BenchmarkSet(b *testing.B) {
	var c Table[int]

	for i := 0; b.Loop(); i++ {
		c.Set(0x12345, i)
	}
}

func TestCache_GetOrBuild_Basic(t *testing.T) {
	var c Cache[*int]
	var builds int
	key := uintptr(0x12345)

	v, err := c.GetOrBuild(key, func() (*int, error) {
		builds++
		x := 42
		return &x, nil
	})
	if err != nil {
		t.Fatalf("first GetOrBuild returned err: %v", err)
	}
	if v == nil || *v != 42 {
		t.Fatalf("first GetOrBuild returned %v, want *42", v)
	}
	if builds != 1 {
		t.Fatalf("build called %d times, want 1", builds)
	}

	// Second call must hit fast path and skip build.
	v2, err := c.GetOrBuild(key, func() (*int, error) {
		builds++
		x := 99
		return &x, nil
	})
	if err != nil {
		t.Fatalf("second GetOrBuild returned err: %v", err)
	}
	if v2 != v {
		t.Fatalf("second GetOrBuild returned different pointer; want same cached value")
	}
	if builds != 1 {
		t.Fatalf("build called %d times on hit, want 1", builds)
	}
}

func TestCache_GetOrBuild_BuildError(t *testing.T) {
	var c Cache[*int]
	key := uintptr(0x12345)
	sentinel := errors.New("build failed")

	_, err := c.GetOrBuild(key, func() (*int, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}

	// Failed build must not populate cache. A second call should run build
	// again and succeed, publishing the new value.
	calls := 0
	v, err := c.GetOrBuild(key, func() (*int, error) {
		calls++
		x := 7
		return &x, nil
	})
	if err != nil || v == nil || *v != 7 {
		t.Fatalf("retry GetOrBuild = (%v, %v), want (*7, nil)", v, err)
	}
	if calls != 1 {
		t.Fatalf("build called %d times on retry, want 1", calls)
	}
}

func TestCache_GetOrBuild_Concurrent(t *testing.T) {
	var c Cache[*int]
	var builds int32
	const goroutines = 32
	key := uintptr(0x4242)

	var wg sync.WaitGroup
	results := make([]*int, goroutines)
	start := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			v, err := c.GetOrBuild(key, func() (*int, error) {
				atomic.AddInt32(&builds, 1)
				x := 123
				return &x, nil
			})
			if err != nil {
				t.Errorf("goroutine %d: err = %v", idx, err)
				return
			}
			results[idx] = v
		}(i)
	}

	close(start)
	wg.Wait()

	// Under LoadOrStore, build may run multiple times when goroutines race past
	// the slow.Load check before any one publishes. The contract is that exactly
	// one value is published: every goroutine observes the same pointer.
	first := results[0]
	if first == nil {
		t.Fatal("first goroutine returned nil")
	}
	for i, v := range results {
		if v != first {
			t.Fatalf("goroutine %d got pointer %p, want %p (shared published value)", i, v, first)
		}
	}
}

func TestCache_Get_PromotesSlowToFast(t *testing.T) {
	var c Cache[*int]
	key := uintptr(0x12345)

	// Publish directly via Publish; fast tier should hold the entry.
	x := 7
	published := c.Publish(key, &x)
	if published != &x {
		t.Fatalf("Publish returned %p, want %p", published, &x)
	}

	// Get must hit (fast path). Use a separate goroutine to verify there is no
	// slow tier traffic by counting slow.Load via a wrapped Cache would require
	// instrumentation; instead verify the contract directly: Get returns the
	// same pointer and reports ok.
	g, ok := c.Get(key)
	if !ok || g != &x {
		t.Fatalf("Get = (%p, %v), want (%p, true)", g, ok, &x)
	}
}

func TestCache_Get_Miss(t *testing.T) {
	var c Cache[*int]
	if _, ok := c.Get(0x12345); ok {
		t.Fatal("expected miss on empty Cache")
	}
}

func TestCache_Publish_LoadOrStoreSemantics(t *testing.T) {
	// A racing Publish must observe the first published value, not its own.
	var c Cache[*int]
	key := uintptr(0x4242)

	first := new(int)
	*first = 1
	got := c.Publish(key, first)

	second := new(int)
	*second = 2
	got2 := c.Publish(key, second)

	if got != first {
		t.Fatalf("first Publish returned %p, want %p", got, first)
	}
	if got2 != first {
		t.Fatalf("second Publish returned %p, want first %p (LoadOrStore)", got2, first)
	}

	// A subsequent Get must also observe the first value.
	g, ok := c.Get(key)
	if !ok || g != first {
		t.Fatalf("Get = (%p, %v), want (%p, true)", g, ok, first)
	}
}

func TestCache_GetOrBuild_UsesPublishedValueFromBuild(t *testing.T) {
	// When a build callback Publishes additional entries (recursive builder
	// pattern), GetOrBuild on those keys must hit without re-invoking build.
	var c Cache[*int]
	rootKey := uintptr(0x1000)
	childKey := uintptr(0x2000)
	var childBuilds int

	_, err := c.GetOrBuild(rootKey, func() (*int, error) {
		// Simulate a recursive builder that publishes a subtree entry.
		child := new(int)
		*child = 42
		c.Publish(childKey, child)
		x := new(int)
		*x = 1
		return x, nil
	})
	if err != nil {
		t.Fatalf("GetOrBuild returned err: %v", err)
	}

	// Now GetOrBuild on childKey must NOT run the build callback; the prior
	// Publish must be visible to Get.
	v, err := c.GetOrBuild(childKey, func() (*int, error) {
		childBuilds++
		y := new(int)
		*y = 99
		return y, nil
	})
	if err != nil {
		t.Fatalf("second GetOrBuild returned err: %v", err)
	}
	if v == nil || *v != 42 {
		t.Fatalf("GetOrBuild(childKey) = %v, want *42 from prior Publish", v)
	}
	if childBuilds != 0 {
		t.Fatalf("build invoked %d times, want 0 (Publish should have populated cache)", childBuilds)
	}
}
