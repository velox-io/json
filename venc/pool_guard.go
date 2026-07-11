//go:build !race

package venc

// poolGuard is a no-op in production builds; the pooling-correctness checks it
// enforces live only in race builds (see pool_guard_race.go). Zero size, zero
// cost on the Marshal/Encoder hot path.
type poolGuard struct{}

func (poolGuard) acquire() {}
func (poolGuard) release() {}
