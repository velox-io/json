// Package rtcache provides fixed-capacity atomic caches keyed by rtype pointer.
// A key hashes to one group via Fibonacci hashing (the golden ratio multiplied
// by 2^64) and may occupy any way within that group.
//
// Two families share that geometry. Table and Cache map an rtype to one value,
// for descriptors built once per type. ObjPool parks several warm objects per
// rtype, as a floor under a sync.Pool whose entries GC would otherwise drop on
// its own schedule.
package rtcache

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// GoldenRatio is the Fibonacci hashing multiplier: floor(2^64 / phi).
// Multiplying a 64-bit key by it and keeping the high bits yields a uniformly
// distributed group index for power-of-two group counts.
const GoldenRatio = 0x9e3779b97f4a7c15

const (
	// Groups is the number of independently indexed groups. Must be a power of
	// two so the high-bit shift in Index covers the full range.
	Groups = 64

	// Ways is the number of entries per group. Two keys landing in the same
	// group coexist rather than evicting each other, up to this many.
	Ways = 8

	// Capacity is the number of keys the table can hold when they distribute
	// perfectly. Real capacity is lower because groups fill unevenly.
	Capacity = Groups * Ways
)

const (
	groupShift = 64 - 6 // 6 = log2(Groups)
	// Way selection for eviction reads hash bits below those used for the
	// group, so a full group does not always discard the same way.
	wayShift = groupShift - 3 // 3 = log2(Ways)
)

// CacheLinePadSize is the stride that separates independently written words.
// 128 is not this machine's line size but a value that every widespread line
// size divides, so a 128-byte stride lands two words on distinct lines whether
// the hardware uses 64 or 128. Matches sync.Pool's choice for the same reason.
const CacheLinePadSize = 128

// Entry pairs an rtype pointer key with a cached value of type V.
//
// An Entry is immutable once published. That is what lets a way be a single
// atomic word: key and value move together in one store, so no reader can
// observe a key paired with another key's value. Splitting them into two atomic
// words would remove the allocation in Set but admit exactly that tear when two
// writers interleave stores for different keys, and a mismatched type descriptor
// is silent memory corruption rather than a cache miss.
type Entry[V any] struct {
	Key uintptr
	Val V
}

// group holds Ways entry pointers and occupies its own cache line stride.
//
// Padding cannot align a group to a line: Go's linker caps data alignment at 32
// bytes, so a Table global starts at an arbitrary line offset. What the padding
// does guarantee holds under any base alignment: consecutive groups sit exactly
// CacheLinePadSize apart, so no line holds live words of two different groups. A
// store therefore only invalidates lines belonging to the storing group, whose
// ways are true sharers anyway. Padding bytes are never read or written.
type group[V any] struct {
	ways [Ways]atomic.Pointer[Entry[V]]
	_    [CacheLinePadSize - unsafe.Sizeof([Ways]atomic.Pointer[Entry[V]]{})%CacheLinePadSize]byte
}

// Table is a set-associative atomic cache keyed by rtype pointer. The zero value
// is ready to use. Concurrent readers and writers race without corruption; a
// racing pair of Sets resolves to one winner and the loser's key simply misses
// later. Callers own slow-path deduplication, such as a sync.Map fallback, when
// build idempotency matters.
//
// Associativity is the point of the geometry, not capacity. Under direct mapping
// two hot keys sharing a slot thrash permanently: each Get finds the other's key,
// misses, and stores its own back, turning a read-only steady state into one
// store and one Entry allocation per call. Groups slots make that certain in a
// process with more than a handful of types, since by the birthday bound 16 keys
// over 32 slots collide with probability 0.99. With Ways entries per group both
// keys stay resident and both Gets hit, so a warmed table performs no stores at
// all. Thrash only resumes once a single group holds more than Ways live keys.
type Table[V any] [Groups]group[V]

// Index returns the group index in [0, Groups) for rtp via Fibonacci hashing.
func Index(rtp uintptr) uintptr {
	return (rtp * GoldenRatio) >> groupShift
}

// Get returns the cached value for rtp. The bool result is false when rtp is
// absent from its group.
func (t *Table[V]) Get(rtp uintptr) (V, bool) {
	g := &t[Index(rtp)]
	// Occupied ways form a prefix: Set always claims the leftmost empty way and
	// no way ever reverts to nil, so the first nil ends the search. A group is
	// usually near-empty, which keeps this well short of Ways iterations.
	for i := range g.ways {
		e := g.ways[i].Load()
		if e == nil {
			break
		}
		if e.Key == rtp {
			return e.Val, true
		}
	}
	var zero V
	return zero, false
}

// Set stores v as the cached value for rtp, replacing any existing entry for
// rtp. When rtp is absent it claims the leftmost empty way; when the group is
// full it evicts a hash-selected way. Intended for publish after build.
func (t *Table[V]) Set(rtp uintptr, v V) {
	g := &t[Index(rtp)]
	e := &Entry[V]{Key: rtp, Val: v}
	for i := range g.ways {
		if cur := g.ways[i].Load(); cur == nil || cur.Key == rtp {
			g.ways[i].Store(e)
			return
		}
	}
	g.ways[(rtp*GoldenRatio)>>wayShift&(Ways-1)].Store(e)
}

// Cache combines a fast atomic Table with a sync.Map slow path. The slow map
// deduplicates concurrent builds for the same key: the first publisher wins
// under LoadOrStore, later racers observe the published value.
//
// The zero value is ready to use. Use Cache when callers need idempotent build
// semantics across goroutines; use Table directly when slow path is owned
// elsewhere (such as a downstream package).
//
// slow needs no explicit pad: Table's trailing group padding already puts it a
// full CacheLinePadSize past the last live fast word, so sync.Map's internal
// stores to mu and misses cannot invalidate a group.
type Cache[V any] struct {
	fast Table[V]
	slow sync.Map // uintptr -> V
}

// Get returns the cached value for rtp without invoking a builder. The bool
// result is false when neither the fast nor slow tier has an entry for rtp.
// A slow hit is promoted into the fast table so subsequent calls hit directly.
//
// Use Get when a build callback needs to read prior publications, such as a
// recursive type builder that shares subtrees across roots.
func (c *Cache[V]) Get(rtp uintptr) (V, bool) {
	if v, ok := c.fast.Get(rtp); ok {
		return v, true
	}
	if v, ok := c.slow.Load(rtp); ok {
		vv := v.(V)
		c.fast.Set(rtp, vv)
		return vv, true
	}
	var zero V
	return zero, false
}

// Publish stores v as the cached value for rtp using LoadOrStore semantics.
// Returns the value now cached for rtp, which equals v unless a racing
// publisher won the slot. Use Publish when a build callback constructs multiple
// related entries (such as a recursive type builder publishing every subtree).
func (c *Cache[V]) Publish(rtp uintptr, v V) V {
	actual, _ := c.slow.LoadOrStore(rtp, v)
	v = actual.(V)
	c.fast.Set(rtp, v)
	return v
}

// GetOrBuild returns the cached value for rtp, invoking build on miss. On a
// slow miss, build runs exactly once per racing cohort under LoadOrStore. If
// build returns an error, no value is cached and the next caller will retry.
func (c *Cache[V]) GetOrBuild(rtp uintptr, build func() (V, error)) (V, error) {
	if v, ok := c.Get(rtp); ok {
		return v, nil
	}
	v, err := build()
	if err != nil {
		var zero V
		return zero, err
	}
	return c.Publish(rtp, v), nil
}
