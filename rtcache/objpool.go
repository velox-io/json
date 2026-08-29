package rtcache

import "sync/atomic"

// ReserveDepth is the number of warm objects ObjPool keeps per key.
//
// Deliberately small. The reserve is a floor under sync.Pool, not the working
// set: it only has to cover the concurrent misses immediately following a GC
// eviction, and everything past that depth still gets a correct (merely cold)
// object.
//
// At 4 a group holds two keys at full depth, which is why ObjPool reuses the
// Ways geometry unchanged.
const ReserveDepth = 4

// DefaultObjMax is the per-object ceiling for admission into a reserve.
//
// The unit is bytes reported by the pool's size function, not object count: a
// reserve is worth having for objects whose retained scratch scales with the
// workload they have served, and only a byte measure distinguishes those. Past
// this size an object is large enough that rebuilding it is cheap relative to
// the work that needs it, so it is better left to sync.Pool and the GC.
const DefaultObjMax = 64 << 20

// DefaultObjBudget is the default ceiling on total bytes held resident across
// all keys of one pool.
//
// A per-object ceiling cannot bound a reserve: every object may pass it while
// the product with ReserveDepth and the number of live keys does not. Reserved
// objects are unreachable from sync.Pool and so never collected, which makes
// this the only thing standing between a resident floor and an unbounded leak.
const DefaultObjBudget = 256 << 20

// reserved is the value half of a reserve entry: the parked object plus the size
// it was charged for.
//
// The size is stored rather than recomputed on release because the object's own
// size may have changed by then (a parse can grow its arenas), and the budget
// must be credited by exactly what it was debited or it drifts.
type reserved[T any] struct {
	obj  *T
	size int
}

// ObjPool is a fixed-capacity reserve of warm objects keyed by rtype pointer,
// meant to back a sync.Pool's New rather than to replace the pool.
//
// sync.Pool is a cache, not a pool: poolCleanup demotes every primary cache to
// victim and drops every victim at the start of a GC, so an object survives at
// most two cycles no matter how heavily it is used. For a small object a miss
// costs one allocation and the policy is right. For an object holding megabytes
// of scratch, a miss costs that allocation plus whatever the object had learned
// about its workload, and it arrives as a latency spike whose timing is set by
// GC cadence rather than by load. Worse, the garbage from rebuilding feeds the
// next GC, which evicts again.
//
// The division of labor matters: sync.Pool keeps the uncontended fast path
// (a per-P private slot, reachable with no atomic operation) and keeps its
// stop-the-world eviction, which is what lets a process under memory pressure
// give the memory back. Neither is reproducible outside the runtime. This type
// supplies only what sync.Pool lacks, a floor that GC cannot take away: Take
// belongs in a New function and so runs only on a pool miss, and Offer only on
// Put, leaving a pool hit untouched.
//
// Keying by rtype is what makes one global table safe for objects that are not
// interchangeable: a Take only ever returns an object parked under the caller's
// own key, so a per-type structure (and its memory multiplied by the number of
// live types) is unnecessary.
type ObjPool[T any] struct {
	// Reuses Table's group geometry, including its cache-line stride, but not
	// its scan rule. Table treats occupied ways as a prefix and stops at the
	// first nil; Take punches holes, so every way here must be examined.
	groups [Groups]group[reserved[T]]

	size    func(*T) int
	objMax  int
	budget  int64
	charged atomic.Int64

	// disabled turns Offer into a no-op, for tests that need pooled objects to
	// actually be reclaimable. See SetEnabled.
	disabled atomic.Bool
}

// NewObjPool returns a reserve that admits objects of at most objMax bytes and
// holds at most budget bytes resident in total. Non-positive values for either
// select DefaultObjMax and DefaultObjBudget.
//
// size reports an object's retained footprint. It belongs to the caller because
// only the caller knows which of an object's buffers are the large ones; this
// package cannot inspect T.
func NewObjPool[T any](size func(*T) int, objMax int, budget int64) *ObjPool[T] {
	if size == nil {
		panic("rtcache: NewObjPool requires a size function")
	}
	if objMax <= 0 {
		objMax = DefaultObjMax
	}
	if budget <= 0 {
		budget = DefaultObjBudget
	}
	return &ObjPool[T]{size: size, objMax: objMax, budget: budget}
}

// Take removes and returns a warm object parked under rtp, or nil when the
// reserve holds none. Call it from a sync.Pool New function, before building a
// cold object.
func (p *ObjPool[T]) Take(rtp uintptr) *T {
	g := &p.groups[Index(rtp)]
	for i := range g.ways {
		e := g.ways[i].Load()
		if e == nil || e.Key != rtp {
			continue
		}
		// A losing racer leaves the way to the winner and keeps scanning: the
		// entry it saw is now the winner's, and any other way still holds a
		// parked object of its own.
		if g.ways[i].CompareAndSwap(e, nil) {
			p.charged.Add(int64(-e.Val.size))
			return e.Val.obj
		}
	}
	return nil
}

// Offer parks obj under rtp and reports whether the reserve took ownership.
// A false result means the caller still owns obj and should hand it to its
// sync.Pool as usual.
//
// Offer declines rather than evicting. An object already resident has proven it
// gets reused, while the one being offered has not, so displacing the former for
// the latter would trade a warm object for an unknown one and churn the budget
// doing it.
func (p *ObjPool[T]) Offer(rtp uintptr, obj *T) bool {
	if obj == nil || p.disabled.Load() {
		return false
	}
	sz := p.size(obj)
	if sz > p.objMax {
		return false
	}

	g := &p.groups[Index(rtp)]
	// Survey before allocating an entry or touching the budget. In steady state
	// the reserve is full and this is where Offer exits, so the common path does
	// no allocation and leaves the shared counter alone.
	free := -1
	depth := 0
	for i := range g.ways {
		e := g.ways[i].Load()
		if e == nil {
			if free < 0 {
				free = i
			}
			continue
		}
		if e.Key == rtp {
			depth++
		}
	}
	if free < 0 || depth >= ReserveDepth {
		return false
	}

	// Charge before publishing so a concurrent Offer cannot see room that this
	// one is about to consume. An overshoot is still possible between the load
	// and the store below; it is bounded by the number of concurrent Offers and
	// self-corrects on the next one, which is why the budget is a ceiling on
	// intent rather than a hard allocation limit.
	if p.charged.Add(int64(sz)) > p.budget {
		p.charged.Add(int64(-sz))
		return false
	}
	if g.ways[free].CompareAndSwap(nil, &Entry[reserved[T]]{
		Key: rtp,
		Val: reserved[T]{obj: obj, size: sz},
	}) {
		return true
	}
	// Lost the way to a racer. Declining is correct even though another way may
	// be free: Offer's contract lets the caller fall back to sync.Pool, so the
	// object stays warm either way, and retrying would race again.
	p.charged.Add(int64(-sz))
	return false
}

// Resident reports the total bytes currently parked, as charged at Offer time.
// Intended for tests and metrics.
func (p *ObjPool[T]) Resident() int64 { return p.charged.Load() }

// Len reports how many objects are parked under rtp. Intended for tests.
func (p *ObjPool[T]) Len(rtp uintptr) int {
	g := &p.groups[Index(rtp)]
	n := 0
	for i := range g.ways {
		if e := g.ways[i].Load(); e != nil && e.Key == rtp {
			n++
		}
	}
	return n
}

// SetEnabled turns admission on or off. Disabling drops everything currently
// parked, so subsequent Takes miss and the objects become collectable.
//
// This exists for tests whose subject is what happens when a pooled object is
// actually reclaimed: a reserve that keeps objects alive across GC would make
// such a test pass by preventing the condition it means to detect. Disabling is
// not a supported production mode; a caller wanting no floor should not install
// a reserve at all.
func (p *ObjPool[T]) SetEnabled(on bool) {
	p.disabled.Store(!on)
	if on {
		return
	}
	for gi := range p.groups {
		g := &p.groups[gi]
		for i := range g.ways {
			if e := g.ways[i].Load(); e != nil && g.ways[i].CompareAndSwap(e, nil) {
				p.charged.Add(int64(-e.Val.size))
			}
		}
	}
}
