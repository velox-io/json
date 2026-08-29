package bind

import (
	"unsafe"

	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// sealOpenSlices reclaims the tails that a failed parse borrowed but never
// closed. It is a memory optimization only: correctness comes from array_begin
// charging the whole tail at borrow time, which keeps [Offset, Limit) unwritten
// no matter how the parse ends. Recomputing here just narrows the charge back to
// what was actually written, so the next parse can reuse the rest.
//
// The walk mirrors array_close. A slice frame is open exactly when the parse
// died inside it, so the open ones are the current container plus every SLICE
// on the frame stack. For each, the written region ends at that frame's running
// write pointer, the same value array_close would have committed.
//
// This is the one place allowed to lower a slot cursor, so it carries the burden
// of not lowering it past what a slice wrote, and of keeping Offset and Len
// naming the same boundary. Splitting those two is not a wasted-memory bug:
// array_begin derives the backing base from Offset but the capacity from
// Cap - Len, so a half-applied move hands out a capacity that runs off the end
// of the block. TestSliceSlotCursorNeverExposesWrittenBytes pins both.
func sealOpenSlices(p *Parser, m *ndec.BindMachine) {
	tree := p.alloc.Tree

	// The innermost container is in the live locals rather than in a frame,
	// since bind_push only saves a container when descending past it.
	sealSliceFrame(p, tree, m.Core.CurType.Kind, uint32(m.Core.CurType.TypeIdx),
		unsafe.Pointer(m.Core.CurDst), m.Core.CurAux)

	// Document completion and BindYieldError spill Depth, so the frame stack is
	// current. Frame zero is the root sentinel.
	frames := ndec.FramesBase(m)
	for d := int32(1); d <= m.Core.Depth; d++ {
		f := (*ndec.BindFrame)(unsafe.Add(unsafe.Pointer(frames), uintptr(d)*unsafe.Sizeof(ndec.BindFrame{})))
		sealSliceFrame(p, tree, vbind.Kind(f.Kind), uint32(f.TypeIdx),
			f.Dst, *(*uintptr)(unsafe.Pointer(&f.U[0])))
	}
}

// sealSliceFrame lowers one slice's SlotClass cursor to the end of the region
// that slice actually wrote. kind/typeIdx identify the container, dst is its
// slice header, and aux is its running write pointer.
func sealSliceFrame(p *Parser, tree *vbind.TypeTree, kind vbind.Kind, typeIdx uint32, dst unsafe.Pointer, aux uintptr) {
	// Only a slice borrows a tail. KindArray has caller-owned inline storage,
	// and KindStream's backing is a fixed buffer Go allocates per batch.
	if kind != vbind.KindSlice {
		return
	}
	if dst == nil || aux == 0 || int(typeIdx) >= len(tree.TypeMeta) {
		return
	}
	ci := tree.TypeMeta[typeIdx].SliceMeta().AllocClass
	if ci < 0 || int(ci) >= len(p.alloc.Slots) {
		return
	}
	sc := &p.alloc.Slots[ci]
	// RecBatch backings come from the matrix, not from a bump tail. A detached
	// or not-yet-installed class has no block to reclaim into.
	if !sc.IsBumpTail() {
		return
	}

	// The backing must be inside the block still installed on this class. A
	// slice whose backing predates the current block, or came from the
	// standalone bypass path, must not move this cursor: the same check
	// array_close makes via `off < sc->limit`.
	data := *(*unsafe.Pointer)(dst)
	if data == nil {
		return
	}
	base := uintptr(sc.Block)
	off := uintptr(data) - base
	if uintptr(data) < base || off >= uintptr(sc.Limit) {
		return
	}
	// aux is the next write position, so it is the end of the written region.
	// It must lie within the block and at or past the backing start.
	end := aux - base
	if aux < uintptr(data) || end > uintptr(sc.Limit) {
		return
	}
	// Never raise the cursor: another slice of this class may already have been
	// charged past this point, and only a lower cursor reclaims anything.
	if uint32(end) >= sc.Offset {
		return
	}
	// Offset and Len must name the same boundary: array_begin derives the
	// backing base from Offset but the capacity from Cap - Len, so a
	// half-applied move hands out a capacity running past the block end.
	if sc.ElemSize == 0 || end%uintptr(sc.ElemSize) != 0 {
		return
	}
	sc.Offset = uint32(end)
	sc.Len = uint32(end / uintptr(sc.ElemSize))
}
