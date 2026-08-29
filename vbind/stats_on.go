//go:build vbindstats

package vbind

import (
	"fmt"
	"sync/atomic"
)

// SlotStats stores explicit Go-side events. Reports derive aggregate policy
// metrics on demand; native cursor advances appear only in cursor snapshots.
type SlotStats struct {
	enabled int32

	BumpElems []uint64

	SliceGrow      []uint64
	NewBlock       []uint64
	RecBatchRefill []uint64
	RecBatchBypass []uint64

	grows []growEvent
}

type growEvent struct {
	ci        uint32
	oldBatch  uint32
	oldOffset uint32
	newBatch  uint32
}

var globalStats SlotStats

// SetStats controls process-global instrumentation. While enabled, callers
// must externally serialize instrumented Allocator construction, grow event
// recording, resets, and snapshot reads.
func SetStats(on bool) {
	if on {
		atomic.StoreInt32(&globalStats.enabled, 1)
	} else {
		atomic.StoreInt32(&globalStats.enabled, 0)
	}
}

func StatsEnabled() bool { return atomic.LoadInt32(&globalStats.enabled) != 0 }

func (a *Allocator) ensureStatsSlots() {
	if !StatsEnabled() {
		return
	}
	setElemSizeSnapshot(a.Slots)
	n := len(a.Slots)
	if len(globalStats.BumpElems) < n {
		globalStats.BumpElems = make([]uint64, n)
	}
	if len(globalStats.SliceGrow) < n {
		globalStats.SliceGrow = make([]uint64, n)
	}
	if len(globalStats.NewBlock) < n {
		globalStats.NewBlock = make([]uint64, n)
	}
	if len(globalStats.RecBatchRefill) < n {
		globalStats.RecBatchRefill = make([]uint64, n)
	}
	if len(globalStats.RecBatchBypass) < n {
		globalStats.RecBatchBypass = make([]uint64, n)
	}
	// Report grouping takes slice classes from TypeTree and map classes from
	// SlotIsMap. Unmarked classes are reported as pointer classes.
	classKind := make([]slotKind, len(a.Slots))
	for ti := range a.Tree.Types {
		tm := &a.Tree.TypeMeta[ti]
		if a.Tree.Types[ti].Kind == KindSlice {
			ac := uint32(tm.SliceMeta().AllocClass)
			if int(ac) < len(classKind) {
				classKind[ac] = slotSlice
			}
		}
	}
	for i, s := range a.Slots {
		if s.Flags&SlotIsMap != 0 && i < len(classKind) {
			classKind[i] = slotMap
		}
	}
	globalClassKind = classKind
}

var globalClassKind []slotKind

func (a *Allocator) statsBump(ci uint32, n uint32) {
	if !StatsEnabled() {
		return
	}
	atomic.AddUint64(&globalStats.BumpElems[ci], uint64(n))
}

func (a *Allocator) statsSliceGrow(sc *SlotClass) {
	if !StatsEnabled() {
		return
	}
	ci := int(a.slotIndex(sc))
	if ci >= 0 && ci < len(globalStats.SliceGrow) {
		atomic.AddUint64(&globalStats.SliceGrow[ci], 1)
	}
}

func (a *Allocator) statsNewBlock(ci uint32) {
	if !StatsEnabled() {
		return
	}
	if int(ci) < len(globalStats.NewBlock) {
		atomic.AddUint64(&globalStats.NewBlock[ci], 1)
	}
}

func (a *Allocator) statsRecBatchRefill(sc *SlotClass) {
	if !StatsEnabled() {
		return
	}
	ci := int(a.slotIndex(sc))
	if ci >= 0 && ci < len(globalStats.RecBatchRefill) {
		atomic.AddUint64(&globalStats.RecBatchRefill[ci], 1)
	}
}

func (a *Allocator) statsRecBatchBypass(sc *SlotClass) {
	if !StatsEnabled() {
		return
	}
	ci := int(a.slotIndex(sc))
	if ci >= 0 && ci < len(globalStats.RecBatchBypass) {
		atomic.AddUint64(&globalStats.RecBatchBypass[ci], 1)
	}
}

// A linear scan is acceptable because instrumentation gates every caller.
func (a *Allocator) slotIndex(sc *SlotClass) uint32 {
	for i := range a.Slots {
		if &a.Slots[i] == sc {
			return uint32(i)
		}
	}
	return ^uint32(0)
}

func (a *Allocator) statsGrowRecord(ci, oldBatch, oldOffset, newBatch uint32) {
	if !StatsEnabled() {
		return
	}
	globalStats.grows = append(globalStats.grows, growEvent{
		ci:        ci,
		oldBatch:  oldBatch,
		oldOffset: oldOffset,
		newBatch:  newBatch,
	})
}

// RefreshFinalBatch does not read or publish allocator state.
func RefreshFinalBatch(a *Allocator) {
}

func ResetStats() {
	zero := func(s []uint64) {
		for i := range s {
			s[i] = 0
		}
	}
	zero(globalStats.BumpElems)
	zero(globalStats.SliceGrow)
	zero(globalStats.NewBlock)
	zero(globalStats.RecBatchRefill)
	zero(globalStats.RecBatchBypass)
	globalStats.grows = globalStats.grows[:0]
}

// OffsetSnapshot captures raw SlotClass byte cursors. It is valid only when
// no SlotClass is replaced or reset between capture and consumption. Pair it
// with Allocator.ConsumedSince when consumption must include both Go and native
// cursor movement, because native bumps bypass statsBump.
type OffsetSnapshot struct {
	Offsets []uint32
}

func (a *Allocator) SnapshotOffsets() OffsetSnapshot {
	s := OffsetSnapshot{Offsets: make([]uint32, len(a.Slots))}
	for i := range a.Slots {
		s.Offsets[i] = a.Slots[i].Offset
	}
	return s
}

// ConsumedSince derives per-class consumption from the raw cursor delta on the
// same Allocator that produced s. ElemSize converts byte cursors to slot counts.
func (a *Allocator) ConsumedSince(s OffsetSnapshot) []uint32 {
	out := make([]uint32, len(a.Slots))
	for i := range a.Slots {
		if i < len(s.Offsets) {
			delta := a.Slots[i].Offset - s.Offsets[i]
			if esz := a.Slots[i].ElemSize; esz > 0 {
				out[i] = delta / esz
			} else {
				out[i] = delta
			}
		}
	}
	return out
}

// Aggregates are derived exclusively from the grow event log. finalBatch
// remains zero when a class has no recorded event.
type growAgg struct {
	growCalls  uint64
	wasteSlots uint64
	growBytes  uint64
	finalBatch uint32
}

func computeGrowAgg(esz []uint32) []growAgg {
	out := make([]growAgg, len(esz))
	for _, ev := range globalStats.grows {
		if int(ev.ci) >= len(out) {
			continue
		}
		g := &out[ev.ci]
		g.growCalls++
		g.wasteSlots += uint64(ev.oldBatch - ev.oldOffset)
		if int(ev.ci) < len(esz) {
			g.growBytes += uint64(ev.newBatch) * uint64(esz[ev.ci])
		}
		g.finalBatch = ev.newBatch
	}
	return out
}

// FormatStats combines the published ElemSize snapshot, explicit bump
// counters, and the grow event log. Native bumps are absent from bump metrics.
func FormatStats() string {
	if !StatsEnabled() {
		return "(stats disabled; rebuild with -tags vbindstats)"
	}
	if len(globalStats.BumpElems) == 0 && len(globalStats.grows) == 0 {
		return "(stats empty)"
	}

	esz := inferElemSizes()
	bump := globalStats.BumpElems
	agg := computeGrowAgg(esz)

	for len(bump) < len(esz) {
		bump = append(bump, 0)
	}

	var totalBump, totalGrowCalls, totalWaste, totalGrowBytes, totalBumpBytes uint64
	for i := range esz {
		totalBump += bump[i]
		totalBumpBytes += bump[i] * uint64(esz[i])
		totalGrowCalls += agg[i].growCalls
		totalWaste += agg[i].wasteSlots
		totalGrowBytes += agg[i].growBytes
	}

	out := fmt.Sprintf("per-SlotClass stats (raw facts; derived metrics computed here)\n")
	out += fmt.Sprintf("total grow events: %d\n", len(globalStats.grows))
	out += fmt.Sprintf("%5s %4s %6s %7s %10s %12s %10s %12s %10s\n", "idx", "kind", "esz", "batch", "goBump", "bumpBytes", "growCalls", "growBytes", "wasteSlots")
	out += fmt.Sprintf("%5s %4s %6s %7s %10s %12s %10s %12s %10s\n", "----", "----", "----", "-----", "------", "--------", "--------", "--------", "---------")
	for i := range esz {
		if bump[i] == 0 && agg[i].growCalls == 0 {
			continue
		}
		kind := "ptr"
		if i < len(globalClassKind) {
			switch globalClassKind[i] {
			case slotSlice:
				kind = "sli"
			case slotMap:
				kind = "map"
			}
		}
		bumpBytes := bump[i] * uint64(esz[i])
		out += fmt.Sprintf("%5d %4s %6d %7d %10d %12d %10d %12d %10d\n",
			i, kind, esz[i], agg[i].finalBatch, bump[i], bumpBytes,
			agg[i].growCalls, agg[i].growBytes, agg[i].wasteSlots)
	}
	out += fmt.Sprintf("%5s %4s %6s %7s %10d %12d %10d %12d %10d\n",
		"TOT", "", "", "", totalBump, totalBumpBytes, totalGrowCalls, totalGrowBytes, totalWaste)
	out += fmt.Sprintf("\n")
	out += fmt.Sprintf("derived:\n")
	out += fmt.Sprintf("  goBumpBytes  = %.3f MB (Go-side AllocFromSlot path)\n", float64(totalBumpBytes)/1e6)
	out += fmt.Sprintf("  growBytes    = %.3f MB (sum of new Block sizes)\n", float64(totalGrowBytes)/1e6)
	if totalBump > 0 {
		out += fmt.Sprintf("  waste ratio  = %.2f%% (wasteSlots / goBump)\n", 100*float64(totalWaste)/float64(totalBump))
	}
	if totalBumpBytes > 0 {
		out += fmt.Sprintf("  grow/goBump  = %.2f%%\n", 100*float64(totalGrowBytes)/float64(totalBumpBytes))
	}
	return out
}

// inferElemSizes returns the snapshot published when the most recent
// instrumented Allocator was constructed.
func inferElemSizes() []uint32 {
	return globalElemSize
}

var globalElemSize []uint32

// StatsSnapshot returns class data for the most recently constructed
// instrumented Allocator. esz and bump alias process-global arrays;
// growCalls, waste, and growBytes are freshly derived from the event log.
// Native bumps do not enter the explicit counters.
func StatsSnapshot() (esz []uint32, bump, growCalls, waste, growBytes []uint64) {
	esz = globalElemSize
	bump = globalStats.BumpElems
	agg := computeGrowAgg(esz)
	growCalls = make([]uint64, len(esz))
	waste = make([]uint64, len(esz))
	growBytes = make([]uint64, len(esz))
	for i := range agg {
		growCalls[i] = agg[i].growCalls
		waste[i] = agg[i].wasteSlots
		growBytes[i] = agg[i].growBytes
	}
	return
}

// The global snapshot follows the most recently constructed instrumented
// Allocator. Reports use it to convert slot counts into byte counts.
func setElemSizeSnapshot(s []SlotClass) {
	globalElemSize = make([]uint32, len(s))
	for i, sc := range s {
		globalElemSize[i] = sc.ElemSize
	}
}

// FinalBatchSnapshot returns the final Batch per class, derived from the
// last grow event's newBatch. Classes with no grow event return 0.
func FinalBatchSnapshot() []uint32 {
	out := make([]uint32, len(globalElemSize))
	for _, ev := range globalStats.grows {
		if int(ev.ci) < len(out) {
			out[ev.ci] = ev.newBatch
		}
	}
	return out
}

// IsSliceSnapshot reports slice backing classes for the most recently
// constructed instrumented Allocator.
func IsSliceSnapshot() []bool {
	out := make([]bool, len(globalElemSize))
	for i, k := range globalClassKind {
		if i < len(out) && k == slotSlice {
			out[i] = true
		}
	}
	return out
}

// IsMapSnapshot reports map header classes for the most recently constructed
// instrumented Allocator.
func IsMapSnapshot() []bool {
	out := make([]bool, len(globalElemSize))
	for i, k := range globalClassKind {
		if i < len(out) && k == slotMap {
			out[i] = true
		}
	}
	return out
}

// YieldCounts returns references to process-global counter arrays indexed by
// SlotClass. ResetStats clears them with the explicit bump counters and grow
// event log.
func YieldCounts() (sliceGrow, newBlock, recBatchRefill, recBatchBypass []uint64) {
	return globalStats.SliceGrow, globalStats.NewBlock, globalStats.RecBatchRefill, globalStats.RecBatchBypass
}

// LiveSlotState returns a raw view of four SlotClass ABI words. The result
// names describe bump mode only: Aux is MuBlock in bump mode, while recursive
// mode may interpret it as Group. Cap, Limit, and Offset are also mode specific.
func LiveSlotState(a *Allocator) (muBlock, cap, limit, offset []uint32) {
	n := len(a.Slots)
	muBlock = make([]uint32, n)
	cap = make([]uint32, n)
	limit = make([]uint32, n)
	offset = make([]uint32, n)
	for i := range a.Slots {
		muBlock[i] = a.Slots[i].Aux
		cap[i] = a.Slots[i].Cap
		limit[i] = a.Slots[i].Limit
		offset[i] = a.Slots[i].Offset
	}
	return
}
