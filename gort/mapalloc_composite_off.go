//go:build vj_nocompositemap

package gort

// compositeMergeDisabled is set by the vj_nocompositemap build tag to drop the
// composite {MapAllocUnit, Group} path (no reflect.StructOf synthesis). When
// set, maplayout init skips verifyCompositeMerge so compositeMergeOK stays
// false; PlanMapSlots falls back to the two-block prewire path (separate
// MapAllocUnit + group blocks, dirPtr prewired to the group block) as long as
// smallMapPrewireOK holds. Use to isolate whether folding Map+group into one
// allocation (dirPtr as an interior pointer into the same unit) is the source
// of a bug, independent of prewire logic. vj_nomapprewire is the stronger
// switch that also disables prewire (lazy only).
const compositeMergeDisabled = true
