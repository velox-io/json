// Package rtcache provides a fixed-size atomic cache keyed by rtype pointer.
// Slot selection uses Fibonacci hashing (the golden ratio multiplied by 2^64)
// to spread pointers uniformly across the power-of-two slot table.
package rtcache

import (
	"sync"
	"sync/atomic"
)

// GoldenRatio is the Fibonacci hashing multiplier: floor(2^64 / phi).
// Multiplying a 64-bit key by it and keeping the high bits yields a uniformly
// distributed slot index for power-of-two table sizes.
const GoldenRatio = 0x9e3779b97f4a7c15

// Size is the slot count of a Table. Must be a power of two so the high-bit
// shift in Index covers the full table range.
const Size = 32

const shift = 64 - 5 // 5 = log2(Size)

// Entry pairs an rtype pointer key with a cached value of type V.
type Entry[V any] struct {
	Key uintptr
	Val V
}

// Table is a Size-way atomic cache keyed by rtype pointer. The zero value is
// ready to use. Concurrent readers and writers of the same slot race without
// corruption; the last writer wins. Callers own slow-path deduplication, such
// as a sync.Map fallback, when build idempotency matters.
type Table[V any] [Size]atomic.Pointer[Entry[V]]

// Index returns a slot index in [0, Size) for rtp via Fibonacci hashing.
func Index(rtp uintptr) uintptr {
	return (rtp * GoldenRatio) >> shift
}

// Get returns the cached value for rtp. The bool result is false when the slot
// is empty or holds a different key.
func (t *Table[V]) Get(rtp uintptr) (V, bool) {
	if e := t[Index(rtp)].Load(); e != nil && e.Key == rtp {
		return e.Val, true
	}
	var zero V
	return zero, false
}

// Set stores v as the cached value for rtp. Intended for publish after build.
func (t *Table[V]) Set(rtp uintptr, v V) {
	t[Index(rtp)].Store(&Entry[V]{Key: rtp, Val: v})
}

// Cache combines a fast atomic Table with a sync.Map slow path. The slow map
// deduplicates concurrent builds for the same key: the first publisher wins
// under LoadOrStore, later racers observe the published value.
//
// The zero value is ready to use. Use Cache when callers need idempotent build
// semantics across goroutines; use Table directly when slow path is owned
// elsewhere (such as a downstream package).
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
