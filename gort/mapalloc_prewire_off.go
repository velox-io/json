//go:build vj_nomapprewire

package gort

// mapPrewireDisabled is set by the vj_nomapprewire build tag to force the
// simplest map init path: PlanMapSlots returns MapAllocUnit-only (GroupOff=0),
// InitMapSlots writes dirPtr=nil, and the runtime allocates the group via
// growToSmall on the first assign. Debugging fallback when the composite
// prewire path (synthesized {MapAllocUnit, Group} type + dirPtr/ctrl wiring)
// is suspected of causing subtle corruption; flipping the tag isolates
// whether the prewire is the source.
const mapPrewireDisabled = true
