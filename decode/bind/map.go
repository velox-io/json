package bind

import (
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

func mapRegionAt(bufBase unsafe.Pointer, off uint32) *ndec.BindMapRegionHeader {
	return (*ndec.BindMapRegionHeader)(unsafe.Add(bufBase, uintptr(off)))
}

func frameAt(frames *ndec.BindFrame, i int32) *ndec.BindFrame {
	return (*ndec.BindFrame)(unsafe.Add(
		unsafe.Pointer(frames), uintptr(i)*unsafe.Sizeof(ndec.BindFrame{})))
}

type inprogMove struct {
	oldAddr unsafe.Pointer
	newAddr unsafe.Pointer
	stride  uintptr
}

type mapRegionMove struct {
	oldOff    uint32
	newOff    uint32
	mapRegion *ndec.BindMapRegionHeader // pointer at new location (after memmove)
}

// mapDrainScratch backs drainAllMapSlots. One instance per Parser is safe:
// FLUSH handling is synchronous (deferred records drain first, stream
// recursion enters through BindYieldInput), and the key hooks a drain runs see
// only key bytes, never this Parser, so drains never nest.
type mapDrainScratch struct {
	offsets     []uint32 // region offsets, ascending
	liveOffs    []uint32 // live region offsets, ascending
	moves       []inprogMove
	regionMoves []mapRegionMove
}

// drainAllMapSlots drains complete KV entries to each map's *hmap and compacts
// in-prog entries so the C machine can keep staging after a FLUSH.
//
// The drain walks linearly:
//
//  1. Drains every region's complete entries (entries [0, EntryCount)) to its *hmap.
//  2. Compacts live regions toward the buffer front, carrying each region's
//     header + single in-prog entry (if any); resets EntryCount/NextEntryOff and
//     the buffer's Used high-water.
//  3. Writes the new region pointers back into frames[d] (FrameMapRegion) for
//     live maps whose region moved during compaction.
//  4. Fixes up frames[].Dst, other regions' ParentSlot, and Core.CurDst that
//     point inside a moved in-prog entry.
//
// A key that fails conversion drops its entry; the failure goes to p.failed.
func drainAllMapSlots(p *Parser, m *ndec.BindMachine) {
	if m.Alloc.MapBufUsed == 0 {
		return
	}
	bufBase := unsafe.Pointer(m.Alloc.MapBuf)
	frames := ndec.FramesBase(m)
	depth := m.Core.Depth
	typeMetaBase := unsafe.Pointer(m.Ctx.TypeMeta)
	typeMetaStride := unsafe.Sizeof(ndec.BindTypeMeta{})
	s := &p.mapDrain
	s.offsets = s.offsets[:0]
	s.liveOffs = s.liveOffs[:0]
	s.moves = s.moves[:0]
	s.regionMoves = s.regionMoves[:0]

	// Collect all region offsets by walking the buffer linearly.
	// Regions are contiguous in [0, MapBufUsed); the walk reads each header's stride to compute the next region's offset.
	for off := uint32(0); off < m.Alloc.MapBufUsed; {
		r := mapRegionAt(bufBase, off)
		s.offsets = append(s.offsets, off)
		off += uint32(ndec.BindMapRegionHeaderSize) + uint32(ndec.BindMapRegionSlots)*r.Stride
	}
	if len(s.offsets) == 0 {
		return
	}

	// Live region offsets from frames[0..depth]. A frame is a live map iff
	// Kind == KindMap and FrameMapRegion() != nil. The linear region walk
	// produced ascending offsets, so filtering preserves ascending order.
	maxLiveD := depth
	if maxLiveD <= 0 {
		maxLiveD = -1
	}
	for _, off := range s.offsets {
		r := mapRegionAt(bufBase, off)
		for d := int32(0); d <= maxLiveD; d++ {
			f := frameAt(frames, d)
			if f.Kind == uint8(vbind.KindMap) && f.FrameMapRegion() == r {
				s.liveOffs = append(s.liveOffs, off)
				break
			}
		}
	}

	// Step 1: drain complete entries for every region.
	for _, off := range s.offsets {
		mapRegion := mapRegionAt(bufBase, off)
		meta := (*ndec.BindTypeMeta)(unsafe.Add(typeMetaBase, uintptr(mapRegion.TypeIdx)*typeMetaStride))
		info := (*vbind.MapDrainInfo)(meta.MapMeta().DrainInfo)
		stride := uintptr(mapRegion.Stride)
		if mapRegion.EntryCount > 0 {
			entriesBase := unsafe.Add(unsafe.Pointer(mapRegion), ndec.BindMapRegionHeaderSize)
			mapHdr := mapRegion.Hmap
			if err := drainKVSlots(mapHdr, entriesBase, int(mapRegion.EntryCount), info, stride, uintptr(ndec.BindMapValOff),
				&p.mapKeys); err != nil {
				p.failed.noteKey(err)
			}
		}
	}

	// No live map frames means no in-prog entries and no compaction targets:
	// every region is closed and fully drained, so resetting the cursor is
	// exactly what compaction would produce.
	if len(s.liveOffs) == 0 {
		m.Alloc.MapBufUsed = 0
		return
	}

	// Step 2: compaction of live regions toward the buffer front. A moved
	// in-prog entry's byte range is recorded for the fixup pass.
	var writePos uint32 // byte offset in buffer
	for _, oldOff := range s.liveOffs {
		oldMapRegion := mapRegionAt(bufBase, oldOff)
		stride := uintptr(oldMapRegion.Stride)
		hasInprog := oldMapRegion.NextEntryOff > oldMapRegion.EntryCount*oldMapRegion.Stride
		newOff := writePos
		// Move the region header to the new position. The old location is
		// either beyond writePos (freed, not read) or overwritten by a later
		// region's memmove; no explicit zeroing needed.
		if newOff != oldOff {
			newMapRegion := mapRegionAt(bufBase, newOff)
			gort.Memmove(unsafe.Pointer(newMapRegion), unsafe.Pointer(oldMapRegion), ndec.BindMapRegionHeaderSize)
		}
		newMapRegion := mapRegionAt(bufBase, newOff)
		// Move the in-prog entry (if any) to the new region's first entry slot.
		if hasInprog {
			oldEntryOff := ndec.BindMapRegionHeaderSize + oldMapRegion.NextEntryOff - oldMapRegion.Stride
			newEntryOff := ndec.BindMapRegionHeaderSize
			oldEntry := unsafe.Add(unsafe.Pointer(oldMapRegion), uintptr(oldEntryOff))
			newEntry := unsafe.Add(unsafe.Pointer(newMapRegion), uintptr(newEntryOff))
			if oldEntry != newEntry {
				gort.Memmove(newEntry, oldEntry, stride)
				s.moves = append(s.moves, inprogMove{oldAddr: oldEntry, newAddr: newEntry, stride: stride})
			}
			newMapRegion.NextEntryOff = newMapRegion.Stride
		} else {
			newMapRegion.NextEntryOff = 0
		}
		newMapRegion.EntryCount = 0
		s.regionMoves = append(s.regionMoves, mapRegionMove{oldOff: oldOff, newOff: newOff, mapRegion: newMapRegion})
		writePos += ndec.BindMapRegionHeaderSize + ndec.BindMapRegionSlots*uint32(stride)
	}
	m.Alloc.MapBufUsed = writePos

	// Step 3: write the new region pointers back into frames[d] (FrameMapRegion)
	// for live maps whose region moved during compaction.
	for d := int32(0); d <= maxLiveD; d++ {
		f := frameAt(frames, d)
		if f.Kind != uint8(vbind.KindMap) {
			continue
		}
		oldMapRegion := f.FrameMapRegion()
		if oldMapRegion == nil {
			continue
		}
		oldOff := uint32(uintptr(unsafe.Pointer(oldMapRegion)) - uintptr(bufBase))
		for _, rm := range s.regionMoves {
			if rm.oldOff == oldOff {
				f.SetFrameMapRegion(rm.mapRegion)
				break
			}
		}
	}

	// Step 4: fixup pointers that referenced a moved in-prog entry. A parent
	// map's ParentSlot, a struct/slice frame's Dst, and Core.CurDst may point
	// into the Value area of an entry that just moved.
	for k := range s.moves {
		mv := &s.moves[k]
		if mv.oldAddr == mv.newAddr {
			continue
		}
		delta := uintptr(mv.newAddr) - uintptr(mv.oldAddr)
		oldStart := uintptr(mv.oldAddr)
		oldEnd := oldStart + mv.stride
		for d := int32(0); d <= depth; d++ {
			f := frameAt(frames, d)
			if dst := uintptr(f.Dst); dst >= oldStart && dst < oldEnd {
				f.Dst = unsafe.Add(f.Dst, delta)
			}
		}
		// Fixup live regions' ParentSlot (nested map whose parent entry moved).
		for _, rm := range s.regionMoves {
			if ps := uintptr(rm.mapRegion.ParentSlot); ps >= oldStart && ps < oldEnd {
				rm.mapRegion.ParentSlot = unsafe.Add(rm.mapRegion.ParentSlot, delta)
			}
		}
		if curDst := uintptr(unsafe.Pointer(m.Core.CurDst)); curDst >= oldStart && curDst < oldEnd {
			m.Core.CurDst = (*byte)(unsafe.Add(unsafe.Pointer(m.Core.CurDst), delta))
		}
	}
}

// drainMapSlotsOnAbort publishes the complete entries a walk error stranded,
// so every map that already closed keeps its contents. It mirrors the Go
// engine's error exits, which close a non-deferred map's region as the error
// unwinds. Deferred-valued regions stay unpublished: their entries point at
// intermediate slots the never-run hooks would have filled, and the Go engine
// drops those on error too. Key conversion failures are dropped; the walk
// error that caused the abort keeps precedence.
func drainMapSlotsOnAbort(p *Parser, m *ndec.BindMachine) {
	if m.Alloc.MapBufUsed == 0 {
		return
	}
	bufBase := unsafe.Pointer(m.Alloc.MapBuf)
	typeMetaBase := unsafe.Pointer(m.Ctx.TypeMeta)
	typeMetaStride := unsafe.Sizeof(ndec.BindTypeMeta{})
	for off := uint32(0); off < m.Alloc.MapBufUsed; {
		r := mapRegionAt(bufBase, off)
		off += uint32(ndec.BindMapRegionHeaderSize) + uint32(ndec.BindMapRegionSlots)*r.Stride
		if r.EntryCount == 0 {
			continue
		}
		meta := (*ndec.BindTypeMeta)(unsafe.Add(typeMetaBase, uintptr(r.TypeIdx)*typeMetaStride))
		info := (*vbind.MapDrainInfo)(meta.MapMeta().DrainInfo)
		if info.ValIsDeferred {
			continue
		}
		entriesBase := unsafe.Add(unsafe.Pointer(r), ndec.BindMapRegionHeaderSize)
		_ = drainKVSlots(r.Hmap, entriesBase, int(r.EntryCount), info, uintptr(r.Stride), uintptr(ndec.BindMapValOff),
			&p.mapKeys)
	}
}

// drainKVSlots writes count staged KV entries into the runtime map, keys
// converting through ks. An entry whose key fails conversion is dropped, and
// the first failure is returned once the rest have landed.
func drainKVSlots(mapHdr, entriesBase unsafe.Pointer, count int, info *vbind.MapDrainInfo, stride, valueOff uintptr,
	ks *vbind.KeyScratch) error {
	keyKind := info.KeyKind
	valSize := uintptr(info.ValSize)
	mapRType := info.MapRType
	valIsDeferred := info.ValIsDeferred

	gort.MapPresize(mapRType, count, mapHdr)

	if keyKind == vbind.KindString {
		// A V that Go stores behind a pointer must go through the generic
		// mapassign, which allocates the element and returns its storage;
		// mapassign_faststr returns the pointer slot instead, and writing V there
		// overwrites the pointer. Decided per map type at build time
		// (MapDrainInfo.ValIndirect), so this is one predictable branch.
		if info.ValIndirect {
			for i := range count {
				slot := unsafe.Add(entriesBase, uintptr(i)*stride)
				valSlot := unsafe.Add(slot, valueOff)
				var valSrc unsafe.Pointer
				if valIsDeferred {
					valSrc = *(*unsafe.Pointer)(valSlot)
				} else {
					valSrc = valSlot
				}
				// The generic call takes the key by address; the staged entry
				// already holds a string header, so pass the slot itself.
				elemInMap := gort.MapAssign(mapRType, mapHdr, slot)
				copyMapValue(elemInMap, valSrc, valSize)
			}
			return nil
		}
		for i := range count {
			slot := unsafe.Add(entriesBase, uintptr(i)*stride)
			key := *(*string)(slot)
			valSlot := unsafe.Add(slot, valueOff)
			var valSrc unsafe.Pointer
			if valIsDeferred {
				valSrc = *(*unsafe.Pointer)(valSlot) // dereference intermediate slot pointer
			} else {
				valSrc = valSlot // inline value
			}
			elemInMap := gort.MapAssignFastStr(mapRType, mapHdr, key)
			copyMapValue(elemInMap, valSrc, valSize)
		}
		return nil
	}

	var first error
	for i := range count {
		slot := unsafe.Add(entriesBase, uintptr(i)*stride)
		elemInMap, err := info.AssignKey(mapHdr, *(*string)(slot), ks)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		valSlot := unsafe.Add(slot, valueOff)
		var valSrc unsafe.Pointer
		if valIsDeferred {
			valSrc = *(*unsafe.Pointer)(valSlot)
		} else {
			valSrc = valSlot
		}
		copyMapValue(elemInMap, valSrc, valSize)
	}
	return first
}

// copyMapValue fills storage returned by mapassign. These raw stores bypass the
// write barrier; allocator retention roots referenced backings through the drain,
// and Release publishes them with a barriered clear before dropping those roots.
func copyMapValue(dst, src unsafe.Pointer, valSize uintptr) {
	if valSize > 8 {
		gort.Memmove(dst, src, valSize)
	} else {
		switch valSize {
		case 1:
			*(*uint8)(dst) = *(*uint8)(src)
		case 2:
			*(*uint16)(dst) = *(*uint16)(src)
		case 4:
			*(*uint32)(dst) = *(*uint32)(src)
		case 8:
			*(*uint64)(dst) = *(*uint64)(src)
		}
	}
}
