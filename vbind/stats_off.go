//go:build !vbindstats

package vbind

// The default build preserves the instrumentation API while constant false
// checks and empty hooks let the compiler eliminate instrumentation call sites.
type OffsetSnapshot struct{}

func SetStats(on bool) {}

func StatsEnabled() bool { return false }

func ResetStats() {}

func FormatStats() string { return "(stats disabled; rebuild with -tags vbindstats)" }

func StatsSnapshot() (esz []uint32, bump, growCalls, waste, growBytes []uint64) {
	return nil, nil, nil, nil, nil
}

func FinalBatchSnapshot() []uint32 { return nil }

func IsSliceSnapshot() []bool { return nil }

func IsMapSnapshot() []bool { return nil }

func RefreshFinalBatch(a *Allocator) {}

func (a *Allocator) ensureStatsSlots() {}

func (a *Allocator) statsBump(ci uint32, n uint32) {}

func (a *Allocator) statsSliceGrow(sc *SlotClass)      {}
func (a *Allocator) statsNewBlock(ci uint32)           {}
func (a *Allocator) statsRecBatchRefill(sc *SlotClass) {}
func (a *Allocator) statsRecBatchBypass(sc *SlotClass) {}

func (a *Allocator) statsGrowRecord(ci, oldBatch, oldOffset, newBatch uint32) {}

func (a *Allocator) SnapshotOffsets() OffsetSnapshot { return OffsetSnapshot{} }

func (a *Allocator) ConsumedSince(s OffsetSnapshot) []uint32 { return nil }

func YieldCounts() (sliceGrow, newBlock, recBatchRefill, recBatchBypass []uint64) {
	return nil, nil, nil, nil
}

func LiveSlotState(a *Allocator) (muBlock, cap, limit, offset []uint32) {
	return nil, nil, nil, nil
}
