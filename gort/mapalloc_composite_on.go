//go:build !vj_nocompositemap

package gort

// compositeMergeDisabled is false by default: the composite {MapAllocUnit, Group}
// path is active when compositeMergeOK (smallMapPrewireOK + verifyCompositeMerge).
// The vj_nocompositemap tag flips this to true to drop synthesis and fall back
// to the two-block prewire path.
const compositeMergeDisabled = false
