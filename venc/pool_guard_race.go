//go:build race

package venc

import "sync/atomic"

// poolGuard catches the sync.Pool handing one encodeState to two goroutines at
// once (which would alias es.buf and produce interleaved/corrupt output), and
// double-release / double-Put (which is what seeds that aliasing: a single
// object Put into the pool twice can then be Get by two goroutines). Active in
// race builds only, so the atomic never touches the production hot path.
type poolGuard struct {
	inUse atomic.Bool
}

func (g *poolGuard) acquire() {
	if !g.inUse.CompareAndSwap(false, true) {
		panic("venc: encodeState acquired while already in use (concurrent pool aliasing)")
	}
}

func (g *poolGuard) release() {
	if !g.inUse.CompareAndSwap(true, false) {
		panic("venc: encodeState released while not in use (double release / double Put)")
	}
}
