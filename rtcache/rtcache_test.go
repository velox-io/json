package rtcache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestIndex_Range(t *testing.T) {
	for i := range 10000 {
		rtp := uintptr(0x40000 + i*0x10)
		idx := Index(rtp)
		if idx >= Size {
			t.Fatalf("Index(%#x) = %d, out of range [0, %d)", rtp, idx, Size)
		}
	}
}

func TestIndex_Distribution(t *testing.T) {
	// Linear rtype-like pointers should still spread across the table thanks
	// to Fibonacci hashing. Demand > 80% slot usage from 10000 probes.
	const n = 10000
	var used [Size]bool
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
	if ratio := float64(count) / Size; ratio < 0.8 {
		t.Fatalf("slot usage %.0f%% < 80%% after %d probes", ratio*100, n)
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

	// find two distinct keys that hash to the same slot, then verify that
	// publishing the second evicts the first.
	k1 := uintptr(0x1000)
	k2 := uintptr(0x1001)
	for k2 < 1<<20 {
		if Index(k1) == Index(k2) {
			break
		}
		k2++
	}
	if Index(k1) != Index(k2) {
		t.Skip("no same-slot pair found in search range")
	}
	c.Set(k1, 1)
	c.Set(k2, 2)
	if _, ok := c.Get(k1); ok {
		t.Fatal("expected miss for k1 after k2 overwrote its slot")
	}
	if v, ok := c.Get(k2); !ok || v != 2 {
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
