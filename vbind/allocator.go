package vbind

import (
	"errors"
	"unsafe"

	"github.com/velox-io/json/gort"
)

const (
	mapBufHeadroom = 32
	mapBufMinBytes = 4 << 10 // 4 KiB
	mapBufMaxBytes = 1 << 20 // 1 MiB
)

const SlotBatchMax = 128

const slotBlockFloor = SlotBatchMax >> 2

// The initial EWMA block balances cold-type waste against immediate regrowth.
const slotBlockInitial = SlotBatchMax >> 1

// Recursive trees use smaller blocks to limit cross-parse backing chains.
const defaultSlotBatchRecursive = 32

// Each recursive group detaches its slot backings every slotDetachK Releases.
// The generation cadence bounds cross-parse backing chains without sweeping rows.
const slotDetachK = 3

// Native SIMD stores may overshoot the final decoded string. This tail keeps
// those stores in bounds outside the per-string hot path. It also absorbs the
// '\0' reparse sentinel stored past textual numbers; only a final token can lack
// a trailing source separator, so the sentinel overhang is at most one byte.
const strArenaTail = 64

// A fresh string arena covers several worst-case parses. Each parse consumes
// at most srcLen bytes, so a buffer of strArenaAmortize*srcLen+strArenaTail
// serves strArenaAmortize parses per allocation. A larger factor would
// amortize mallocgc further but hold more memory live when actual string
// content is small; 3 keeps steady-state live memory near srcLen per parse.
const strArenaAmortize = 3

// A fresh tape arena reserves several caller-computed bounds to amortize growth.
const tapeAmortize = 3

func mapBufCapFor(minBytes uint32) int {
	return max(min(max(int(minBytes)*mapBufHeadroom, mapBufMinBytes), mapBufMaxBytes), int(minBytes))
}

// Allocator is single-owner mutable parse state. Reuse across parses requires
// exclusive access to every method.
type Allocator struct {
	Tree  *TypeTree
	Slots []SlotClass
	// DeferredDrain stages 24 byte UnmarshalRecord entries written by native
	// parsing and consumed by Go before Release. Only map values that require
	// deferred binding use a scannable slot instead of the noscan map buffer, so
	// their deferred heap writes remain visible to GC.
	DeferredDrain []byte

	MapBuf []byte
	// retained roots displaced backings until drains finish and stages fresh
	// backings for publication by a barriered release clear.
	retained []unsafe.Pointer

	// tapeHighWater is the largest completed native scan bound recorded through
	// NoteTapeBound. TapeHighWater exposes it as the next parse's sizing hint.
	tapeHighWater int

	// live holds backings native may keep carving from after a release point.
	live []unsafe.Pointer

	// groups contains only recursive slot classes. Release advances each group
	// generation and detaches the whole group on its cadence without sweeping rows.
	groups []recGroup

	// slotBatchMax is both the block ceiling and the standalone bypass threshold.
	// Recursive trees default to a smaller value to limit retained backing chains.
	slotBatchMax uint32

	// StrArena is a monotonic cross-parse bump view. CommitStrArena advances the
	// view; published Value document string-arena slices root older backings directly.
	StrArena []byte

	// TapeArena is a monotonic cross-parse bump view carved by native code.
	// Published Value document tape slices keep displaced backings reachable.
	TapeArena []uint64
}

// A recursive group detaches as one generation unit. Typed overlay pointers
// avoid scanning non-recursive slots or switching on mode during Release.
type recGroup struct {
	gen    uint32
	bumps  []*RecBumpSlotClass
	batchs []*RecBatchSlotClass
}

// The parent block is a scannable array of map pointers, so each wired parent
// keeps its inner allocation reachable. On the two-block path, scannable dirPtr
// fields in the inner units also keep the group block reachable.
func initMapSlotBlock(rtype, block unsafe.Pointer, esz uintptr, batch int) {
	plan := gort.PlanMapSlots(rtype)
	inner := gort.UnsafeNewArray(plan.InnerType, batch)
	var groupBlock unsafe.Pointer
	if plan.GroupOff == 0 && plan.GroupType != nil {
		groupBlock = gort.UnsafeNewArray(plan.GroupType, batch)
	}
	gort.InitMapSlots(block, inner, groupBlock, esz, plan, batch)
}

// Map slots are initialized as ready map pointers. Their scannable parent block
// roots the inner allocations without a separate retained handle.
func newBumpSlotClass(tpl SlotTemplate, initial uint32) BumpSlotClass {
	slotCount := tpl.Batch
	b := BumpSlotClass{
		RType:    tpl.RType,
		ElemSize: tpl.ElemSize,
		Mode:     slotBump,
		Flags:    tpl.Flags,
		Cap:      slotCount,
	}
	if tpl.IsStream {
		// An empty bump range makes native yield before the first element so Go can
		// install the fixed batch buffer. Cap carries the requested batch size.
		return b
	}
	b.Block = gort.UnsafeNewArray(tpl.RType, int(slotCount))
	b.Limit = slotCount * tpl.ElemSize
	if tpl.Flags&SlotIsMap != 0 {
		initMapSlotBlock(tpl.RType, b.Block, uintptr(tpl.ElemSize), int(slotCount))
	} else {
		// Only slice growth consumes the EWMA seed.
		b.MuBlock = initial
	}
	return b
}

// Recursive bump slots use Offset and Limit but reserve the shared overlay's
// trailing state for the group generation instead of EWMA data.
func newRecBumpSlotClass(tpl SlotTemplate) RecBumpSlotClass {
	slotCount := tpl.Batch
	r := RecBumpSlotClass{
		Block:    gort.UnsafeNewArray(tpl.RType, int(slotCount)),
		RType:    tpl.RType,
		ElemSize: tpl.ElemSize,
		Mode:     slotRecBump,
		Flags:    tpl.Flags,
		Offset:   0,
		Limit:    slotCount * tpl.ElemSize,
		Group:    tpl.Group,
	}
	if tpl.Flags&SlotIsMap != 0 {
		initMapSlotBlock(tpl.RType, r.Block, uintptr(tpl.ElemSize), int(slotCount))
	}
	return r
}

// Clearing the limits forces the next native access to yield for a fresh block.
// Published maps keep their inner allocations reachable after detachment.
func (r *RecBumpSlotClass) reset() {
	r.Block = nil
	r.Offset = 0
	r.Limit = 0
}

// AllocOption is applied after recursion-aware defaults are selected.
type AllocOption func(*Allocator)

// WithSlotBatchMax sets the block ceiling and bypass threshold; values
// outside [1, SlotBatchMax] fall back to SlotBatchMax.
func WithSlotBatchMax(n uint32) AllocOption {
	return func(a *Allocator) {
		if n == 0 || n > SlotBatchMax {
			n = SlotBatchMax
		}
		a.slotBatchMax = n
	}
}

func NewAllocator(tt *TypeTree, opts ...AllocOption) *Allocator {
	a := &Allocator{Tree: tt}

	// Smaller recursive batches limit the cross-parse backing chain.
	if tt.GroupCount > 0 {
		a.slotBatchMax = defaultSlotBatchRecursive
	} else {
		a.slotBatchMax = SlotBatchMax
	}
	for _, o := range opts {
		o(a)
	}

	a.DeferredDrain = make([]byte, 16*24)

	a.MapBuf = make([]byte, mapBufCapFor(tt.MapBufMinBytes))

	// Slot group IDs are one-based; the slice index is group ID minus one.
	a.groups = make([]recGroup, tt.GroupCount)

	initial := a.slotBatchMax >> 1
	a.Slots = make([]SlotClass, len(tt.Slots))
	for i := range tt.Slots {
		tpl := tt.Slots[i]
		// Bump, RecBump, and RecBatch share one 48 byte C ABI overlay.
		switch tpl.Mode {
		case slotRecBatch:
			r := (*RecBatchSlotClass)(unsafe.Pointer(&a.Slots[i]))
			*r = newRecBatchSlotClass(tpl)
			a.groups[tpl.Group-1].batchs = append(a.groups[tpl.Group-1].batchs, r)
		case slotRecBump:
			r := (*RecBumpSlotClass)(unsafe.Pointer(&a.Slots[i]))
			*r = newRecBumpSlotClass(tpl)
			a.groups[tpl.Group-1].bumps = append(a.groups[tpl.Group-1].bumps, r)
		default:
			*(*BumpSlotClass)(unsafe.Pointer(&a.Slots[i])) = newBumpSlotClass(tpl, initial)
		}
		// Release clears each fresh backing through a barriered pointer store after
		// native may have published interior pointers from it.
		if b := a.Slots[i].Block; b != nil {
			a.retained = append(a.retained, b)
		}
	}
	a.ensureStatsSlots()
	return a
}

// Retain stages a backing until a release clears the entry through Go's pointer
// deletion barrier. A newly installed backing needs that later shade because
// native publishes interior pointers outside Go's write barriers. A displaced
// backing needs immediate GC-visible reachability until drains copy pointers
// out of noscan staging buffers.
func (a *Allocator) Retain(ptr unsafe.Pointer) {
	a.retained = append(a.retained, ptr)
}

// Release requires native drains to have finished with temporary backings. Its
// barriered clear publishes native writes into staged backings, then StageLive
// republishes the backings retained for later parses.
func (a *Allocator) Release() {
	for i := range a.retained {
		a.retained[i] = nil
	}
	a.retained = a.retained[:0]

	// Recursive maps and slices can link sibling backings across parses. Each SCC
	// detaches as one generation on a fixed cadence to bound that chain.
	for i := range a.groups {
		g := &a.groups[i]
		g.gen++
		if g.gen%slotDetachK == 0 {
			for _, r := range g.bumps {
				r.reset()
			}
			for _, r := range g.batchs {
				r.reset()
			}
		}
	}

	a.StageLive()
}

// RetainMark records the current retention height so a later ReleaseScoped can
// drop everything staged above it.
func (a *Allocator) RetainMark() int {
	return len(a.retained)
}

// RetainedCount reports backings staged for release or continued native use.
func (a *Allocator) RetainedCount() int {
	return len(a.retained) + len(a.live)
}

// ReleaseScoped publishes and drops backings staged since mark while a stream
// continues parsing. Callers must first drain every staging buffer that can hold
// pointers into those backings. StageLive republishes backings that native may
// continue writing so a later release can publish those subsequent writes.
func (a *Allocator) ReleaseScoped(mark int) {
	if mark < 0 || mark > len(a.retained) {
		return
	}
	for i := mark; i < len(a.retained); i++ {
		a.retained[i] = nil
	}
	a.retained = a.retained[:mark]
	a.StageLive()
}

// StageLive republishes every backing native may still carve from through Go
// pointer stores. This covers carried-over blocks whose spare capacity lets the
// parse continue using the existing allocation. The set includes current slot
// blocks, RecBatch row backings, and both arenas.
func (a *Allocator) StageLive() {
	live := a.live[:0]
	for i := range a.Slots {
		sc := &a.Slots[i]
		if sc.Mode == slotRecBatch {
			// The matrix is plain data reachable from Block; what native
			// carves from are the row backings.
			m := sc.RecBatch().matrix()
			for r := range m.Rows {
				if b := m.Rows[r].Base; b != nil {
					live = append(live, b)
				}
			}
			continue
		}
		if sc.Block != nil {
			live = append(live, sc.Block)
		}
	}
	// Native interns strings and tape words into these arenas by raw cursor,
	// and bound values alias them.
	if a.StrArena != nil {
		live = append(live, unsafe.Pointer(unsafe.SliceData(a.StrArena)))
	}
	if a.TapeArena != nil {
		live = append(live, unsafe.Pointer(unsafe.SliceData(a.TapeArena)))
	}
	for i := len(live); i < len(a.live); i++ {
		a.live[i] = nil
	}
	a.live = live
}

// Carve returns a pointer into a typed slot backing. The result stays
// GC-visible through that backing until its owning result is released.
func (a *Allocator) Carve(slotClassIdx int32) (unsafe.Pointer, error) {
	sc := &a.Slots[slotClassIdx]
	if sc.Offset >= sc.Limit {
		a.installBlock(sc, a.slotBatchMax)
	}
	slot := unsafe.Add(unsafe.Pointer(sc.Block), uintptr(sc.Offset))
	sc.Offset += sc.ElemSize
	return slot, nil
}

// CarveSlice takes n contiguous elements. RecBatch slots and requests above
// the block ceiling use standalone typed arrays; other requests use bump space.
func (a *Allocator) CarveSlice(slotClassIdx int32, n int) (unsafe.Pointer, error) {
	sc := &a.Slots[slotClassIdx]
	if sc.Mode == slotRecBatch {
		bk := gort.UnsafeNewArray(sc.RType, n)
		a.retained = append(a.retained, bk)
		return bk, nil
	}
	elemSize := uintptr(sc.ElemSize)
	needBytes := uintptr(n) * elemSize
	if uintptr(sc.Offset)+needBytes <= uintptr(sc.Limit) {
		slot := unsafe.Add(unsafe.Pointer(sc.Block), uintptr(sc.Offset))
		sc.Offset += uint32(needBytes)
		return slot, nil
	}
	if n > int(a.slotBatchMax) {
		bk := gort.UnsafeNewArray(sc.RType, n)
		a.retained = append(a.retained, bk)
		return bk, nil
	}
	a.installBlock(sc, a.slotBatchMax)
	slot := unsafe.Add(unsafe.Pointer(sc.Block), uintptr(sc.Offset))
	sc.Offset += uint32(needBytes)
	return slot, nil
}

// installBlock installs a caller-sized bump backing and resets the cursor. The
// displaced backing remains rooted until drains finish. The fresh backing stays
// staged until a release publishes native interior-pointer stores into it.
func (a *Allocator) installBlock(sc *SlotClass, n uint32) {
	if sc.Block != nil {
		a.retained = append(a.retained, sc.Block)
	}
	sc.Block = gort.UnsafeNewArray(sc.RType, int(n))
	a.retained = append(a.retained, sc.Block)
	sc.Offset = 0
	sc.Limit = n * sc.ElemSize
	if sc.ElemSize == 0 {
		// A saturated limit makes the stationary cursor model Go's shared address
		// semantics for zero-sized values while keeping BLOCK_FULL false.
		sc.Limit = ^uint32(0)
	}
	sc.Len = 0
	sc.Cap = n

	if sc.Flags&SlotIsMap != 0 {
		// Scannable parent map pointers root every initialized inner unit.
		initMapSlotBlock(sc.RType, sc.Block, uintptr(sc.ElemSize), int(n))
	}
}

// ServeNewBlock installs an allocator-ceiling backing for BLOCK_FULL. The native
// ABI reserves need for alternative sizing policies.
func (a *Allocator) ServeNewBlock(ci, need uint32) error {
	if int(ci) >= len(a.Slots) {
		return errors.New("vbind: BLOCK_FULL class idx out of range")
	}
	a.statsNewBlock(ci)
	a.installBlock(&a.Slots[ci], a.slotBatchMax)
	return nil
}

// ServeSliceGrow handles growth for a non-recursive bump slice. Requests above
// slotBatchMax bypass the shared block and EWMA. Smaller requests take a whole
// EWMA-sized block; close returns the unused tail to sibling slices.
func (a *Allocator) ServeSliceGrow(sc *SlotClass, hdr *gort.SliceHeader) error {
	a.statsSliceGrow(sc)

	floor := a.slotBatchMax >> 2
	needCap := uint32(max(hdr.Len*2, int(floor)))

	if needCap > a.slotBatchMax {
		data := gort.UnsafeNewArray(sc.RType, int(needCap))
		a.retained = append(a.retained, data)
		if hdr.Data == nil {
			hdr.Data = data
			hdr.Cap = int(needCap)
			hdr.Len = 0
		} else {
			if hdr.Len > 0 {
				gort.Memmove(data, hdr.Data, uintptr(hdr.Len)*uintptr(sc.ElemSize))
			}
			hdr.Data = data
			hdr.Cap = int(needCap)
		}
		return nil
	}

	// The template block is sized by Batch, far below real usage, so training
	// on its first grow would feed EWMA a tiny sample and let beta=0.5 pull
	// MuBlock into a stable low fixed point. Gate training on reaching the
	// floor capacity, i.e. after one real EWMA-sized block has run.
	if sc.Cap >= floor {
		const beta = 0.5
		sample := sc.Len + uint32(hdr.Len)
		sc.Aux = uint32(beta*float64(sample) + (1-beta)*float64(sc.Aux))
	}

	blockCap := min(uint32(max(int(sc.Aux), int(needCap))), a.slotBatchMax)

	a.installBlock(sc, blockCap)
	data := sc.Block
	// Reserving the whole block prevents sibling allocations until close returns
	// the unused tail.
	sc.Offset = sc.Limit

	if hdr.Data == nil {
		hdr.Data = data
		hdr.Cap = int(blockCap)
		hdr.Len = 0
	} else {
		if hdr.Len > 0 {
			// Source and destination are typed backings that remain GC-reachable
			// throughout the raw copy.
			gort.Memmove(data, hdr.Data, uintptr(hdr.Len)*uintptr(sc.ElemSize))
		}
		hdr.Data = data
		hdr.Cap = int(blockCap)
	}
	return nil
}

// EnsureStrArena preserves its monotonic cursor across parses. Published Value
// documents keep displaced backings reachable; retained stages a fresh backing
// for publication at the next release.
func (a *Allocator) EnsureStrArena(srcLen int) {
	need := srcLen + strArenaTail
	if cap(a.StrArena) >= need {
		return
	}
	newCap := max(strArenaAmortize*srcLen+strArenaTail, need)
	a.StrArena = gort.MakeDirtyBytes(int(newCap), int(newCap))
	a.retained = append(a.retained, unsafe.Pointer(unsafe.SliceData(a.StrArena)))
}

// CommitStrArena advances past bytes written by one parse. Successful parses
// publish this region; failed parses seal it so destination pointers already
// written by that parse cannot enter the next parse's generation.
func (a *Allocator) CommitStrArena(used int) {
	a.StrArena = a.StrArena[used:]
}

// CommitTapeArena advances by native TapeUsed and must run only after success.
// An error leaves the view unchanged so the next parse may overwrite partial data.
func (a *Allocator) CommitTapeArena(used int) {
	a.TapeArena = a.TapeArena[used:]
}

// EnsureTapeArena reserves the caller-computed tape-word bound while preserving
// the monotonic cursor across parses. The caller must provide a valid bound
// because native writes rely on the reserved capacity. Published Value documents
// keep displaced backings reachable, and retained stages the fresh backing for
// publication at the next release.
func (a *Allocator) EnsureTapeArena(words int) {
	if cap(a.TapeArena) >= words {
		return
	}
	newCap := max(tapeAmortize*words, words)
	a.TapeArena = make([]uint64, newCap)
	a.retained = append(a.retained, unsafe.Pointer(unsafe.SliceData(a.TapeArena)))
}

// TapeHighWater reports the largest completed native scan bound recorded by
// NoteTapeBound. The native scan recomputes the required bound when this sizing
// hint is insufficient.
func (a *Allocator) TapeHighWater() int { return a.tapeHighWater }

// Footprint reports resident scratch capacity that contributes to pooled parser
// size. It includes both arenas and the fixed map and drain buffers. SlotClass
// backings use fixed geometry, while oversized standalone arrays leave with
// retained at Release.
func (a *Allocator) Footprint() int {
	return cap(a.StrArena) + cap(a.TapeArena)*8 + cap(a.MapBuf) + cap(a.DeferredDrain)
}

// NoteTapeBound records the largest completed native scan bound. The bound is
// the capacity required before walking begins, while tape_used records only the
// words produced. CommitTapeArena advances the independent success-only cursor.
func (a *Allocator) NoteTapeBound(words int) {
	if words > a.tapeHighWater {
		a.tapeHighWater = words
	}
}
