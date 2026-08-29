package bind

import (
	"errors"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

func syncMapBuf(alloc *vbind.Allocator, allocABI *ndec.BindAllocator) {
	allocABI.MapBuf = (*byte)(unsafe.SliceData(alloc.MapBuf))
	allocABI.MapBufUsed = 0
	allocABI.MapBufCap = uint32(cap(alloc.MapBuf))
}

func (p *Parser) serveFlushMap(m *ndec.BindMachine) error {
	return drainAllMapSlots(m)
}

func mapRegionAt(bufBase unsafe.Pointer, off uint32) *ndec.BindMapRegionHeader {
	return (*ndec.BindMapRegionHeader)(unsafe.Add(bufBase, uintptr(off)))
}

func frameAt(frames *ndec.BindFrame, i int32) *ndec.BindFrame {
	return (*ndec.BindFrame)(unsafe.Add(
		unsafe.Pointer(frames), uintptr(i)*unsafe.Sizeof(ndec.BindFrame{})))
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
func drainAllMapSlots(m *ndec.BindMachine) error {
	if m.Alloc.MapBufUsed == 0 {
		return nil
	}
	bufBase := unsafe.Pointer(m.Alloc.MapBuf)
	frames := ndec.FramesBase(m)
	depth := m.Core.Depth
	typeMetaBase := unsafe.Pointer(m.Ctx.TypeMeta)
	typeMetaStride := unsafe.Sizeof(ndec.BindTypeMeta{})

	// Collect all region offsets by walking the buffer linearly.
	// Regions are contiguous in [0, MapBufUsed); the walk reads each header's stride to compute the next region's offset.
	offsets := make([]uint32, 0, ndec.BindMaxDepth+1)
	for off := uint32(0); off < m.Alloc.MapBufUsed; {
		r := mapRegionAt(bufBase, off)
		offsets = append(offsets, off)
		off += uint32(ndec.BindMapRegionHeaderSize) + uint32(ndec.BindMapRegionSlots)*r.Stride
	}
	if len(offsets) == 0 {
		return nil
	}

	// Live region set from frames[0..maxLiveD] A frame is a live map iff Kind == KindMap and FrameMapRegion() != nil.
	maxLiveD := depth
	if maxLiveD <= 0 {
		maxLiveD = -1
	}
	liveDepth := make(map[*ndec.BindMapRegionHeader]int32, maxLiveD+1)
	for d := int32(0); d <= maxLiveD; d++ {
		f := frameAt(frames, d)
		if f.Kind == uint8(vbind.KindMap) {
			if mapRegion := f.FrameMapRegion(); mapRegion != nil {
				liveDepth[mapRegion] = d
			}
		}
	}

	// Step 1: drain complete entries for every region.
	for _, off := range offsets {
		mapRegion := mapRegionAt(bufBase, off)
		meta := (*ndec.BindTypeMeta)(unsafe.Add(typeMetaBase, uintptr(mapRegion.TypeIdx)*typeMetaStride))
		info := (*vbind.MapDrainInfo)(meta.MapMeta().DrainInfo)
		stride := uintptr(mapRegion.Stride)
		if mapRegion.EntryCount > 0 {
			entriesBase := unsafe.Add(unsafe.Pointer(mapRegion), ndec.BindMapRegionHeaderSize)
			mapHdr := mapRegion.Hmap
			if err := drainKVSlots(mapHdr, entriesBase, int(mapRegion.EntryCount), info, stride, uintptr(ndec.BindMapValOff)); err != nil {
				return err
			}
		}
	}

	// Step 2: compaction of live regions toward the buffer front. A moved
	// in-prog entry's byte range is recorded for the fixup pass.
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
	var moves []inprogMove
	var mapRegionMoves []mapRegionMove

	// Collect live region offsets. The linear walk already produced offsets in
	// ascending order, so liveOffs is ascending without an explicit sort.
	liveOffs := make([]uint32, 0, len(liveDepth))
	for _, off := range offsets {
		r := mapRegionAt(bufBase, off)
		if _, ok := liveDepth[r]; ok {
			liveOffs = append(liveOffs, off)
		}
	}

	var writePos uint32 // byte offset in buffer
	for _, oldOff := range liveOffs {
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
				moves = append(moves, inprogMove{oldAddr: oldEntry, newAddr: newEntry, stride: stride})
			}
			newMapRegion.NextEntryOff = newMapRegion.Stride
		} else {
			newMapRegion.NextEntryOff = 0
		}
		newMapRegion.EntryCount = 0
		mapRegionMoves = append(mapRegionMoves, mapRegionMove{oldOff: oldOff, newOff: newOff, mapRegion: newMapRegion})
		writePos += ndec.BindMapRegionHeaderSize + ndec.BindMapRegionSlots*uint32(stride)
	}
	m.Alloc.MapBufUsed = writePos

	// Step 3: write the new region pointers back into frames[d] (FrameMapRegion)
	// for live maps whose region moved during compaction.
	relocated := make(map[uint32]uint32, len(mapRegionMoves))
	for _, rm := range mapRegionMoves {
		relocated[rm.oldOff] = rm.newOff
	}
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
		newOff, ok := relocated[oldOff]
		if !ok {
			continue // should not happen for live regions
		}
		newMapRegion := mapRegionAt(bufBase, newOff)
		f.SetFrameMapRegion(newMapRegion)
	}

	// Step 4: fixup pointers that referenced a moved in-prog entry. A parent
	// map's ParentSlot, a struct/slice frame's Dst, and Core.CurDst may point
	// into the Value area of an entry that just moved.
	for k := range moves {
		mv := &moves[k]
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
		for _, rm := range mapRegionMoves {
			if ps := uintptr(rm.mapRegion.ParentSlot); ps >= oldStart && ps < oldEnd {
				rm.mapRegion.ParentSlot = unsafe.Add(rm.mapRegion.ParentSlot, delta)
			}
		}
		if curDst := uintptr(unsafe.Pointer(m.Core.CurDst)); curDst >= oldStart && curDst < oldEnd {
			m.Core.CurDst = (*byte)(unsafe.Add(unsafe.Pointer(m.Core.CurDst), delta))
		}
	}

	return nil
}

// drainKVSlots writes count staged KV entries into the runtime map.
func drainKVSlots(mapHdr, entriesBase unsafe.Pointer, count int, info *vbind.MapDrainInfo, stride, valueOff uintptr) error {
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

	var keyBuf [8]byte
	for i := range count {
		slot := unsafe.Add(entriesBase, uintptr(i)*stride)
		key := *(*string)(slot)
		if err := encodeIntKey(&keyBuf, keyKind, key); err != nil {
			return err
		}
		valSlot := unsafe.Add(slot, valueOff)
		var valSrc unsafe.Pointer
		if valIsDeferred {
			valSrc = *(*unsafe.Pointer)(valSlot)
		} else {
			valSrc = valSlot
		}
		elemInMap := gort.MapAssign(mapRType, mapHdr, unsafe.Pointer(&keyBuf[0]))
		copyMapValue(elemInMap, valSrc, valSize)
	}
	return nil
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

func encodeIntKey(buf *[8]byte, keyKind vbind.Kind, keyStr string) error {
	switch keyKind {
	case vbind.KindInt, vbind.KindInt8, vbind.KindInt16, vbind.KindInt32, vbind.KindInt64:
		bits := 64
		switch keyKind {
		case vbind.KindInt8:
			bits = 8
		case vbind.KindInt16:
			bits = 16
		case vbind.KindInt32:
			bits = 32
		}
		v, err := strconv.ParseInt(keyStr, 10, bits)
		if err != nil {
			return errors.New("bind: map int key " + keyStr + ": " + err.Error())
		}
		switch keyKind {
		case vbind.KindInt8:
			*(*int8)(unsafe.Pointer(&buf[0])) = int8(v)
		case vbind.KindInt16:
			*(*int16)(unsafe.Pointer(&buf[0])) = int16(v)
		case vbind.KindInt32:
			*(*int32)(unsafe.Pointer(&buf[0])) = int32(v)
		default:
			*(*int64)(unsafe.Pointer(&buf[0])) = v
		}
		return nil
	case vbind.KindUint, vbind.KindUint8, vbind.KindUint16, vbind.KindUint32, vbind.KindUint64:
		bits := 64
		switch keyKind {
		case vbind.KindUint8:
			bits = 8
		case vbind.KindUint16:
			bits = 16
		case vbind.KindUint32:
			bits = 32
		}
		v, err := strconv.ParseUint(keyStr, 10, bits)
		if err != nil {
			return errors.New("bind: map uint key " + keyStr + ": " + err.Error())
		}
		switch keyKind {
		case vbind.KindUint8:
			*(*uint8)(unsafe.Pointer(&buf[0])) = uint8(v)
		case vbind.KindUint16:
			*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(v)
		case vbind.KindUint32:
			*(*uint32)(unsafe.Pointer(&buf[0])) = uint32(v)
		default:
			*(*uint64)(unsafe.Pointer(&buf[0])) = v
		}
		return nil
	default:
		return errors.New("bind: unsupported map key kind")
	}
}
