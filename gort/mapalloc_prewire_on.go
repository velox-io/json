//go:build !vj_nomapprewire

package gort

// mapPrewireDisabled is false by default: PlanMapSlots uses the composite
// {MapAllocUnit, Group} path when compositeMergeOK, prewiring dirPtr and ctrl
// so the first mapassign skips growToSmall (zero alloc). The vj_nomapprewire
// tag flips this to true to force the lazy path.
const mapPrewireDisabled = false
