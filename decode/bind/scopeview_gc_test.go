package bind

import (
	"runtime"
	"sync"
	"testing"

	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
)

// Concentrated reproducer for the streaming-feed GC fault that
// TestStreamStressFeedGB hits at low probability after tens of millions of
// items. That test needs ~4 GiB of feed and several -count runs; these drive
// the same path (batch settle -> closeGeneration -> rotate, each rotation
// re-entering mountWindow) under a GC spinner, so a mark phase is almost always
// running when a pooled buffer is replaced.
//
// The fault they guard is the noscan-machine-block rule: a Go-owned buffer
// reachable only from its Parser/feedState field (structural indexes, raw
// scratch, the window) is orphaned the moment that field is reassigned, so
// every machine-held pointer into it must be nil'd first. Otherwise the store
// that republishes the new backing hands the write barrier an old value whose
// backing nothing roots, and a later cycle frees it under the barrier buffer.

// TestStreamScopeRotationGC runs many short scoped-stream feeds under a GC
// spinner. Each SettleBatch closes a generation and may rotate the view pair,
// which is where a stale pointer would reach the write-barrier buffer.
func TestStreamScopeRotationGC(t *testing.T) {
	stop := make(chan struct{})
	var spinner sync.WaitGroup
	spinner.Add(1)
	go func() {
		defer spinner.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		spinner.Wait()
	}()

	p, err := NewParser[stressHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	const rounds = 40
	var total int64
	for r := 0; r < rounds; r++ {
		src := newStressStreamReader(2 << 20)
		var h stressHost
		var count int64
		h.Items.OnRead(func(s stream.Scope[stressElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				e := it.Target()
				count++
				// Read the Value so its doc is actually resolved, then drop
				// it: nothing is retained, so each generation's doc dies as
				// soon as the next one is installed.
				if (count-1)%8 != 0 {
					kv := e.V.Get("k")
					if _, ok := kv.Str(); !ok {
						t.Errorf("round %d item %d: V.k missing", r, count-1)
					}
				}
			}
			return nil
		})
		if err := p.UnmarshalFeed(src, &h); err != nil {
			t.Fatalf("round %d: UnmarshalFeed: %v", r, err)
		}
		wantItems := src.stats().items
		if count != wantItems {
			t.Fatalf("round %d: items=%d want %d", r, count, wantItems)
		}
		total += count
	}
	t.Logf("items=%d", total)
}

// TestStreamScopeRotationHoldGC is the same drive with every 64th element's
// Value retained, so some generations stay pinned while others die. A doc
// reachable only from the noscan machine block would be collected here while
// the retained ones mask the fault.
func TestStreamScopeRotationHoldGC(t *testing.T) {
	stop := make(chan struct{})
	var spinner sync.WaitGroup
	spinner.Add(1)
	go func() {
		defer spinner.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		spinner.Wait()
	}()

	p, err := NewParser[stressHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	var held []value.Value
	for r := 0; r < 20; r++ {
		src := newStressStreamReader(2 << 20)
		var h stressHost
		var count int64
		h.Items.OnRead(func(s stream.Scope[stressElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				e := it.Target()
				count++
				if count%64 == 0 {
					held = append(held, e.V)
				}
			}
			return nil
		})
		if err := p.UnmarshalFeed(src, &h); err != nil {
			t.Fatalf("round %d: UnmarshalFeed: %v", r, err)
		}
	}
	runtime.GC()
	t.Logf("held=%d", len(held))
}
