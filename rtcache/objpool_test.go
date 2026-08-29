package rtcache

import (
	"sync"
	"sync/atomic"
	"testing"
)

type box struct {
	id   int
	size int
}

func newTestPool(objMax int, budget int64) *ObjPool[box] {
	return NewObjPool(func(b *box) int { return b.size }, objMax, budget)
}

func TestObjPool_TakeEmpty(t *testing.T) {
	p := newTestPool(0, 0)
	if got := p.Take(0x1000); got != nil {
		t.Fatalf("Take on empty pool = %v, want nil", got)
	}
}

func TestObjPool_OfferTakeRoundTrip(t *testing.T) {
	p := newTestPool(0, 0)
	key := uintptr(0x1000)
	b := &box{id: 7, size: 1024}

	if !p.Offer(key, b) {
		t.Fatal("Offer rejected an object that fits both ceilings")
	}
	if got := p.Resident(); got != 1024 {
		t.Fatalf("Resident = %d, want 1024", got)
	}
	got := p.Take(key)
	if got != b {
		t.Fatalf("Take = %v, want the offered object %v", got, b)
	}
	if r := p.Resident(); r != 0 {
		t.Fatalf("Resident = %d after Take, want 0; budget was not credited back", r)
	}
	if again := p.Take(key); again != nil {
		t.Fatalf("second Take = %v, want nil; object was handed out twice", again)
	}
}

// The reserve is a floor, not a working set: it must stop admitting at
// ReserveDepth so one hot key cannot consume the whole table.
func TestObjPool_DepthCap(t *testing.T) {
	p := newTestPool(0, 0)
	key := uintptr(0x1000)
	for i := range ReserveDepth {
		if !p.Offer(key, &box{id: i, size: 8}) {
			t.Fatalf("Offer %d of %d rejected before reaching depth", i, ReserveDepth)
		}
	}
	if p.Offer(key, &box{id: 99, size: 8}) {
		t.Fatalf("Offer accepted past ReserveDepth=%d", ReserveDepth)
	}
	if n := p.Len(key); n != ReserveDepth {
		t.Fatalf("Len = %d, want %d", n, ReserveDepth)
	}
}

// Take must not return an object parked under a different type. Objects here are
// not interchangeable, so a cross-key hit would be a type confusion, and the
// keys used share a group to exercise the matching rather than the hashing.
func TestObjPool_KeyIsolation(t *testing.T) {
	p := newTestPool(0, 0)
	keys := sameGroupKeys(2)
	if Index(keys[0]) != Index(keys[1]) {
		t.Fatal("test setup: keys must share a group")
	}
	mine := &box{id: 1, size: 16}
	if !p.Offer(keys[0], mine) {
		t.Fatal("Offer rejected")
	}
	if got := p.Take(keys[1]); got != nil {
		t.Fatalf("Take(other key) = %v, want nil; group-mate object leaked across keys", got)
	}
	if got := p.Take(keys[0]); got != mine {
		t.Fatalf("Take(own key) = %v, want %v", got, mine)
	}
}

func TestObjPool_RejectsOversizedObject(t *testing.T) {
	p := newTestPool(1024, 0)
	if p.Offer(0x1000, &box{size: 1025}) {
		t.Fatal("Offer accepted an object past objMax")
	}
	if r := p.Resident(); r != 0 {
		t.Fatalf("Resident = %d, want 0; a rejected object was charged", r)
	}
}

// The per-object ceiling cannot bound the reserve, since depth and key count
// multiply it. The budget is what actually stands between a resident floor and
// an unbounded leak.
func TestObjPool_BudgetCap(t *testing.T) {
	p := newTestPool(0, 1000)
	key := uintptr(0x1000)
	if !p.Offer(key, &box{id: 1, size: 600}) {
		t.Fatal("first Offer rejected under budget")
	}
	if p.Offer(key, &box{id: 2, size: 600}) {
		t.Fatal("Offer accepted past budget")
	}
	if r := p.Resident(); r != 600 {
		t.Fatalf("Resident = %d, want 600; the rejected Offer left a charge behind", r)
	}
	// Freeing the first must reopen room for the second.
	p.Take(key)
	if !p.Offer(key, &box{id: 3, size: 600}) {
		t.Fatal("Offer rejected after the budget was credited back")
	}
}

// Take punches holes, so an occupied way can sit past an empty one. Table's scan
// stops at the first nil; this one must not, or a parked object becomes
// unreachable and its charge is never released.
func TestObjPool_TakeScansPastHoles(t *testing.T) {
	p := newTestPool(0, 0)
	key := uintptr(0x1000)
	a, b := &box{id: 1, size: 8}, &box{id: 2, size: 8}
	if !p.Offer(key, a) || !p.Offer(key, b) {
		t.Fatal("Offer rejected")
	}
	// Drain the earlier way, leaving a hole before the later one.
	if got := p.Take(key); got != a {
		t.Fatalf("first Take = %v, want %v", got, a)
	}
	if got := p.Take(key); got != b {
		t.Fatalf("second Take = %v, want %v; scan stopped at the hole", got, b)
	}
	if r := p.Resident(); r != 0 {
		t.Fatalf("Resident = %d, want 0", r)
	}
}

// A pool whose budget is exhausted and then fully drained must return to zero.
// Charge/credit asymmetry would slowly wall off the reserve, and the symptom
// (a floor that quietly stops working) is invisible without this.
func TestObjPool_BudgetReturnsToZero(t *testing.T) {
	p := newTestPool(0, 1<<20)
	keys := sameGroupKeys(2)
	for _, k := range keys {
		for i := range ReserveDepth {
			p.Offer(k, &box{id: i, size: 4096})
		}
	}
	for _, k := range keys {
		for p.Take(k) != nil {
		}
	}
	if r := p.Resident(); r != 0 {
		t.Fatalf("Resident = %d after draining every key, want 0", r)
	}
}

// Concurrent Take must never hand one object to two callers, and the budget must
// balance once the dust settles.
func TestObjPool_ConcurrentTakeIsExclusive(t *testing.T) {
	p := newTestPool(0, 0)
	key := uintptr(0x1000)
	const rounds = 200

	for range rounds {
		var parked []*box
		for i := range ReserveDepth {
			b := &box{id: i, size: 32}
			if p.Offer(key, b) {
				parked = append(parked, b)
			}
		}

		var mu sync.Mutex
		seen := map[*box]int{}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range ReserveDepth * 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if got := p.Take(key); got != nil {
					mu.Lock()
					seen[got]++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()

		for b, n := range seen {
			if n != 1 {
				t.Fatalf("object %v handed out %d times, want 1", b, n)
			}
		}
		if len(seen) != len(parked) {
			t.Fatalf("recovered %d objects, want %d", len(seen), len(parked))
		}
	}
	if r := p.Resident(); r != 0 {
		t.Fatalf("Resident = %d, want 0", r)
	}
}

// Mixed concurrent Offer/Take must leave the budget consistent with what is
// actually parked, which is the accounting invariant the whole ceiling rests on.
func TestObjPool_ConcurrentOfferTakeAccounting(t *testing.T) {
	const size = 64
	p := newTestPool(0, 0)
	keys := sameGroupKeys(3)

	var wg sync.WaitGroup
	var held atomic.Int64
	for g := range 16 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := keys[g%len(keys)]
			for i := range 500 {
				if i%2 == 0 {
					if p.Offer(key, &box{id: i, size: size}) {
						held.Add(1)
					}
				} else if p.Take(key) != nil {
					held.Add(-1)
				}
			}
		}(g)
	}
	wg.Wait()

	var parked int64
	for _, k := range keys {
		parked += int64(p.Len(k))
	}
	if want := parked * size; p.Resident() != want {
		t.Fatalf("Resident = %d, want %d (%d objects parked); charge and contents disagree",
			p.Resident(), want, parked)
	}
}
