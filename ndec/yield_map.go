// MAP yields stage key/value pairs into a fixed KV buffer. When that buffer
// fills, or when end_object closes the map, Go flushes the staged slots into
// the destination map. BEGIN_MAP only prepares the child frame and its buffer;
// the map itself is still allocated lazily on the first flush.

package ndec

import (
	"errors"
	"fmt"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// yfMapClosing: FLUSH_MAP yield flag set by the C-side end_object hook
// on MAP frame pop. The driver, after flushing, must also advance the
// parent frame and release the KV buffer sub-region.
const yfMapClosing uint8 = 1

// Cold-path sentinel errors. In practice the native parser never hits these
// conditions; they guard against violated C/Go ABI invariants (mismatched
// type tables, depth overflow, lost pending fields). Using errors.New instead
// of fmt.Errorf keeps yield handlers short, helping the compiler inline the
// hot paths.
var (
	errMapBindTypeNil          = errors.New("ndec: map bind type is nil")
	errMapBindTypeKindMismatch = errors.New("ndec: bind type kind is not map")
	errFrameKindNotMap         = errors.New("ndec: frame kind is not map")

	errBeginMapNoParent          = errors.New("ndec: begin_map without parent frame")
	errBeginMapInvalidStruct     = errors.New("ndec: begin_map on invalid struct parent")
	errBeginMapPendingFieldRange = errors.New("ndec: begin_map pending field idx out of range")
	errBeginMapNonMapField       = errors.New("ndec: begin_map on non-map field kind")
	errBeginMapMapElemKind       = errors.New("ndec: begin_map on map parent with non-map elem kind")
	errBeginMapBadParentKind     = errors.New("ndec: begin_map on unsupported parent kind")
	errBeginMapMapBTNotBuilt     = errors.New("ndec: begin_map mapBT not properly built")

	errFlushMapValueRType        = errors.New("ndec: flush_map value rtype missing")
	errFlushMapLostStructPending = errors.New("ndec: flush_map first alloc lost struct pending")
	errFlushMapPendingFieldRange = errors.New("ndec: flush_map first alloc pending field idx out of range")
	errFlushMapBadParentKind     = errors.New("ndec: flush_map first alloc on unsupported parent kind")

	errFlushMapClosingDepth  = errors.New("ndec: flush_map closing depth out of range")
	errFlushMapNoActiveFrame = errors.New("ndec: flush_map without active frame")
	errFlushMapNonMapFrame   = errors.New("ndec: flush_map on non-map frame")
)

func mapBuiltTypeFromBindType(p unsafe.Pointer) (*typeInfo, error) {
	if p == nil {
		return nil, errMapBindTypeNil
	}
	bt := (*typeInfo)(p)
	if bt.base.Kind != uint8(bkMap) {
		return nil, errMapBindTypeKindMismatch
	}
	return bt, nil
}

func mapBuiltTypeOfFrame(frame *ndecFrame) (*typeInfo, error) {
	if frame.BindContainerKind != uint8(bkMap) {
		return nil, errFrameKindNotMap
	}
	return mapBuiltTypeFromBindType(frame.BindType)
}

// rebaseLiveMapFrameBases: when kvBuf grows, relocate all frame pointers
// that reference the old backing. Two kinds of pointers are affected:
//
//  1. MAP frame BindSliceHdr — the frame's KV buffer sub-region start
//  2. STRUCT frame BindDst — when the parent is MAP<struct>, the struct
//     child frame's dst points into the outer KV slot's value area, which
//     physically lives in d.kvBuf
//
// Missing type 2 would leave struct child frames and their subtrees with
// BindDst pointing to GC'd old backing. Lazy alloc / inline struct field
// writes would land in the old memory, while outer flush would read zeros
// from the new backing.
//
// SLICE frame BindSliceHdr does not need rebase: SLICE hdrs point to Go
// slice headers in live structs, not inside kvBuf. SLICE frame BindDst is
// the backing array base, also not in kvBuf.
func (d *driverState) rebaseLiveMapFrameBases(oldStart unsafe.Pointer, oldLen int, newStart unsafe.Pointer) {
	if oldStart == nil || newStart == nil || oldLen == 0 || d.ctx.Sp < 0 {
		return
	}
	oldBase := uintptr(oldStart)
	oldEnd := oldBase + uintptr(oldLen)
	for i := int32(0); i <= d.ctx.Sp; i++ {
		frame := &d.ctx.Frames[i]
		switch frame.BindContainerKind {
		case uint8(bkMap):
			if frame.BindSliceHdr == nil {
				continue
			}
			ptr := uintptr(frame.BindSliceHdr)
			if ptr < oldBase || ptr >= oldEnd {
				continue
			}
			frame.BindSliceHdr = unsafe.Add(newStart, ptr-oldBase)
		case uint8(bkStruct):
			if frame.BindDst == nil {
				continue
			}
			ptr := uintptr(frame.BindDst)
			if ptr < oldBase || ptr >= oldEnd {
				continue
			}
			frame.BindDst = unsafe.Add(newStart, ptr-oldBase)
		}
	}
}

func (d *driverState) reserveMapKVBuf(need int) unsafe.Pointer {
	off := len(d.kvBuf)
	if cap(d.kvBuf)-off < need {
		var oldStart unsafe.Pointer
		if cap(d.kvBuf) > 0 {
			full := d.kvBuf[:cap(d.kvBuf)]
			oldStart = unsafe.Pointer(unsafe.SliceData(full))
		}
		oldLen := len(d.kvBuf)
		newBuf := make([]byte, off+need, (off+need)*2)
		copy(newBuf, d.kvBuf)
		newStart := unsafe.Pointer(unsafe.SliceData(newBuf))
		d.rebaseLiveMapFrameBases(oldStart, oldLen, newStart)
		d.kvBuf = newBuf
	} else {
		d.kvBuf = d.kvBuf[:off+need]
	}
	// Sync cursor: BEGIN_MAP fast path reads buffer bounds via userData.KvBuf*;
	// after grow / sub-region carving, base/len/cap must be realigned.
	d.syncKvBufCursor()
	return unsafe.Add(unsafe.Pointer(unsafe.SliceData(d.kvBuf)), off)
}

func initMapFrame(child *ndecFrame, mapBT *typeInfo, mapHeader, base unsafe.Pointer, kvBufCount int, parentFieldIdx int32) {
	child.BindType = unsafe.Pointer(&mapBT.base)
	child.BindDst = mapHeader
	child.setBindKvCount(0)
	child.BindArrayCap = uint32(kvBufCount)
	child.BindContainerKind = uint8(bkMap)
	child.ParentFieldIdx = parentFieldIdx
	child.BindSliceHdr = base
	child.Phase = uint32(phaseObjectFieldOrEnd)
	child.Data = 0
}

// bootstrapRootMap rewrites frames[0] as a MAP frame after map header
// has been allocated and written to the user's *map[K]V slot.
// Used for both root MAP and root PTR-to-MAP paths.
func (d *driverState) bootstrapRootMap(mapBT *typeInfo, mapHeader unsafe.Pointer, root *ndecFrame) error {
	need := int(mapBT.kvSlotSize()) * mapKVBufCount
	base := d.reserveMapKVBuf(need)

	root.BindDst = mapHeader
	root.setBindKvCount(0)
	root.BindArrayCap = uint32(mapKVBufCount)
	root.BindContainerKind = uint8(bkMap)
	root.BindSliceHdr = base
	return nil
}

func (d *driverState) handleBeginMapYield() error {
	// Root MAP: sp==1 (reactor pushed child), frames[0] is the root
	// binding (pre-filled by driver). Allocate the map header + kvBuf
	// sub-region, then rewrite frames[1] as a MAP frame.
	if d.ctx.Sp == 1 && d.ctx.Frames[0].BindContainerKind == uint8(bkMap) {
		root := &d.ctx.Frames[0]
		mapBT := (*typeInfo)(root.BindType)
		if mapBT == nil || mapBT.rtypePtr() == nil {
			return errMapBindTypeNil
		}
		if mapBT.base.Kind != uint8(bkMap) {
			return errMapBindTypeKindMismatch
		}
		mapHeader := gort.MakeMap(mapBT.rtypePtr(), 0, nil)
		*(*unsafe.Pointer)(root.BindDst) = mapHeader
		// Child frame at frames[1] was already pushed by reactor.
		child := &d.ctx.Frames[1]
		return d.bootstrapRootMap(mapBT, mapHeader, child)
	}

	if d.ctx.Sp < 1 {
		return errBeginMapNoParent
	}
	parent := &d.ctx.Frames[d.ctx.Sp-1]
	var mapBT *typeInfo
	parentFieldIdx := int32(-1)
	switch parent.BindContainerKind {
	case uint8(bkStruct):
		if parent.BindPendingFieldIdx < 0 {
			return errBeginMapInvalidStruct
		}
		parentTI := (*bindTypeInfo)(parent.BindType)
		if parent.BindPendingFieldIdx >= int32(parentTI.FieldCount) {
			return errBeginMapPendingFieldRange
		}
		parentFieldIdx = parent.BindPendingFieldIdx
		fieldsBase := (*bindFieldInfo)(parentTI.Fields)
		fi := &unsafe.Slice(fieldsBase, parentTI.FieldCount)[parent.BindPendingFieldIdx]
		if fi.Kind == uint8(bkPtr) && fi.Type != nil {
			// *map[K]V: fi->type points to map typeinfo directly
			mapBT = (*typeInfo)(fi.Type)
			if mapBT.base.Kind != uint8(bkMap) {
				return errBeginMapNonMapField
			}
		} else if fi.Kind != uint8(bkMap) {
			return errBeginMapNonMapField
		}
		if mapBT == nil {
			var err error
			mapBT, err = mapBuiltTypeFromBindType(fi.Type)
			if err != nil {
				return err
			}
		}
	case uint8(bkMap):
		parentBT, err := mapBuiltTypeOfFrame(parent)
		if err != nil {
			return err
		}
		if bindKind(parentBT.base.ElemKind) != bkMap || parentBT.elemTypeInfo() == nil {
			return errBeginMapMapElemKind
		}
		mapBT = parentBT.elemTypeInfo()
	case uint8(bkSlice):
		// []map[K]V: outer SLICE frame, elem is MAP, get via bind_type
		sliceBT := (*typeInfo)(parent.BindType)
		if bindKind(sliceBT.base.ElemKind) != bkMap || sliceBT.elemTypeInfo() == nil {
			return errBeginMapMapElemKind
		}
		mapBT = sliceBT.elemTypeInfo()
		// grow outer backing if needed (lazy alloc)
		if parent.bindArrayIndex() >= parent.BindArrayCap {
			_ = d.handleGrowSliceStructYield()
		}
	default:
		return errBeginMapBadParentKind
	}
	if mapBT == nil || mapBT.rtypePtr() == nil || mapBT.elemTypeInfo() == nil {
		return errBeginMapMapBTNotBuilt
	}
	kvBufCount := mapKVBufCount
	need := int(mapBT.kvSlotSize()) * kvBufCount
	base := d.reserveMapKVBuf(need)
	child := &d.ctx.Frames[d.ctx.Sp]
	initMapFrame(child, mapBT, nil, base, kvBufCount, parentFieldIdx)
	// Stack already pushed by reactor; no Sp increment needed.
	return nil
}

func mapKVSlot(frame *ndecFrame, mapBT *typeInfo, idx uint32) unsafe.Pointer {
	return unsafe.Add(frame.BindSliceHdr, uintptr(idx)*uintptr(mapBT.kvSlotSize()))
}

func mapValueSlotPtr(frame *ndecFrame, mapBT *typeInfo, idx uint32) unsafe.Pointer {
	return unsafe.Add(mapKVSlot(frame, mapBT, idx), 16)
}

// parentMapFrame returns the MAP frame's physical predecessor in ctx.Frames.
// frames[0] is the root frame, so frame-1 is always a valid real parent
// (STRUCT or outer MAP) unless frame itself is the root frame.
func parentMapFrame(frame *ndecFrame) *ndecFrame {
	return (*ndecFrame)(unsafe.Add(unsafe.Pointer(frame), -int(unsafe.Sizeof(ndecFrame{}))))
}

// lazyAllocMapHeader allocates a map header on first flush, using hint as
// the makemap hint, then writes the parent slot and consumes the parent
// STRUCT pending. Before call: frame.BindDst == nil. After call: frame.BindDst
// points to the new map header.
func (d *driverState) lazyAllocMapHeader(frame *ndecFrame, bt *typeInfo, hint uint32) error {
	mapHeader := gort.MakeMap(bt.rtypePtr(), int(hint), nil)
	frame.BindDst = mapHeader

	parent := parentMapFrame(frame)
	switch parent.BindContainerKind {
	case uint8(bkStruct):
		if parent.BindPendingFieldIdx < 0 {
			return errFlushMapLostStructPending
		}
		parentTI := (*bindTypeInfo)(parent.BindType)
		if parent.BindPendingFieldIdx >= int32(parentTI.FieldCount) {
			return errFlushMapPendingFieldRange
		}
		fieldsBase := (*bindFieldInfo)(parentTI.Fields)
		fi := &unsafe.Slice(fieldsBase, parentTI.FieldCount)[parent.BindPendingFieldIdx]
		fieldPtr := unsafe.Add(unsafe.Pointer(parent.BindDst), uintptr(fi.Offset))
		// *map[K]V: alloc an intermediate map cell, store header into it,
		// then point the struct field at the cell. Direct assignment of
		// *hmap into the *map field would make `*M` deref the runtime hmap
		// memory (count int, etc.) as if it were a map header, crashing on flush.
		if fi.Kind == uint8(bkPtr) {
			slot := gort.UnsafeNew(bt.rtypePtr())
			*(*unsafe.Pointer)(slot) = mapHeader
			*(*unsafe.Pointer)(fieldPtr) = slot
		} else {
			*(*unsafe.Pointer)(fieldPtr) = mapHeader
		}
		parent.BindPendingFieldIdx = -1
	case uint8(bkMap):
		parentBT, err := mapBuiltTypeOfFrame(parent)
		if err != nil {
			return err
		}
		*(*unsafe.Pointer)(mapValueSlotPtr(parent, parentBT, parent.bindKvCount())) = mapHeader
	case uint8(bkSlice):
		// []map[K]V: parent is outer SLICE frame, write map header to current elem slot
		*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(parent.BindDst),
			uintptr(parent.bindArrayIndex())*uintptr(((*typeInfo)(parent.BindType)).base.ElemSize))) = mapHeader
	default:
		return errFlushMapBadParentKind
	}
	return nil
}

func (d *driverState) flushMapFrame(frame *ndecFrame, closing bool) error {
	bt, err := mapBuiltTypeOfFrame(frame)
	if err != nil {
		return err
	}
	count := frame.bindKvCount()

	// Lazy alloc: first entry (BindDst==nil) estimates makemap hint from count.
	if frame.BindDst == nil {
		hint := count
		if !closing {
			hint = count * 4
		}
		if err := d.lazyAllocMapHeader(frame, bt, hint); err != nil {
			return err
		}
	}

	if count > 0 {
		mapHeader := frame.BindDst
		mapRType := bt.rtypePtr()
		elemRType := bt.mapValueRType()
		if elemRType == nil {
			elemRType = bt.elemTypeInfo().rtypePtr()
		}
		if elemRType == nil {
			return errFlushMapValueRType
		}
		base := frame.BindSliceHdr
		slotSize := uintptr(bt.kvSlotSize())
		keyKind := bt.mapKeyKind()
		valSize := uintptr(bt.base.ElemSize)
		hasPtr := bt.mapValueHasPtr()

		// fast path: keyKind == bkString. mapassign_faststr takes a
		// string directly, avoiding ParseInt + 8B stack write.
		if keyKind == bkString {
			if hasPtr {
				for i := range count {
					slot := unsafe.Add(base, uintptr(i)*slotSize)
					keyHdr := (*goStringHeader)(slot)
					keyStr := unsafe.String((*byte)(keyHdr.data), int(keyHdr.len))
					elemSlotInMap := gort.MapAssignFastStr(mapRType, mapHeader, keyStr)
					gort.TypedMemmove(elemRType, elemSlotInMap, unsafe.Add(slot, 16))
				}
			} else {
				for i := range count {
					slot := unsafe.Add(base, uintptr(i)*slotSize)
					keyHdr := (*goStringHeader)(slot)
					keyStr := unsafe.String((*byte)(keyHdr.data), int(keyHdr.len))
					elemSlotInMap := gort.MapAssignFastStr(mapRType, mapHeader, keyStr)
					copyMapValue(elemSlotInMap, unsafe.Add(slot, 16), valSize)
				}
			}
		} else {
			// int / uint key: ParseInt -> 8B stack buffer -> MapAssign(&buf).
			// Cannot pass GoStringHeader.data directly: stdlib parsing
			// requires converting the string to int; key comparison uses
			// hash(int_bytes), not string.
			var keyBuf [8]byte
			for i := range count {
				slot := unsafe.Add(base, uintptr(i)*slotSize)
				keyHdr := (*goStringHeader)(slot)
				keyStr := unsafe.String((*byte)(keyHdr.data), int(keyHdr.len))
				if err := encodeIntKey(&keyBuf, keyKind, keyStr); err != nil {
					return err
				}
				elemSlotInMap := gort.MapAssign(mapRType, mapHeader, unsafe.Pointer(&keyBuf[0]))
				if hasPtr {
					gort.TypedMemmove(elemRType, elemSlotInMap, unsafe.Add(slot, 16))
				} else {
					copyMapValue(elemSlotInMap, unsafe.Add(slot, 16), valSize)
				}
			}
		}
	}
	if closing {
		d.shrinkKvBufTo(frame.BindSliceHdr)
		// Pop: reactor delegated pop to driver. Parent is at Sp-1.
		d.ctx.Sp--
		if d.ctx.Sp >= 0 {
			parent := &d.ctx.Frames[d.ctx.Sp]
			switch parent.BindContainerKind {
			case uint8(bkStruct):
				parent.BindPendingFieldIdx = -1
			case uint8(bkSlice):
				parent.setBindArrayIndex(parent.bindArrayIndex() + 1)
			case uint8(bkMap):
				parent.setBindKvCount(parent.bindKvCount() + 1)
				if parent.bindKvCount() >= parent.BindArrayCap {
					return d.flushMapFrame(parent, false)
				}
			}
		}
	} else {
		frame.setBindKvCount(0)
	}
	return nil
}

// handleFlushMapYield: KV buffer full or end_object triggered. Driver
// iterates kv_count slots to bulk mapassign + typedmemmove.
func (d *driverState) handleFlushMapYield() error {
	closing := d.userData.YieldFlags == yfMapClosing
	var frame *ndecFrame
	if closing {
		// Parser hasn't POP'd yet in the new model; the closing MAP frame
		// is at frames[sp]. The reactor yields before pop.
		if d.ctx.Sp >= ndecMaxDepth {
			return errFlushMapClosingDepth
		}
		frame = &d.ctx.Frames[d.ctx.Sp]
	} else {
		if d.ctx.Sp < 0 {
			return errFlushMapNoActiveFrame
		}
		frame = &d.ctx.Frames[d.ctx.Sp]
	}
	if frame.BindContainerKind != uint8(bkMap) {
		return errFlushMapNonMapFrame
	}
	return d.flushMapFrame(frame, closing)
}

// shrinkKvBufTo shrinks d.kvBuf to before base (reclaiming the sub-region
// when closing a MAP frame). base must be a pointer within d.kvBuf's
// underlying array; 0 / out-of-bounds base is a no-op.
func (d *driverState) shrinkKvBufTo(base unsafe.Pointer) {
	if len(d.kvBuf) == 0 || base == nil {
		return
	}
	bufStart := unsafe.Pointer(unsafe.SliceData(d.kvBuf))
	off := uintptr(base) - uintptr(bufStart)
	if off > uintptr(len(d.kvBuf)) {
		return // safety net
	}
	d.kvBuf = d.kvBuf[:off]
	d.syncKvBufCursor()
}

// copyMapValue writes the KV slot value area (16B into the slot) to the
// runtime mapassign destination slot. Uses word store instead of memmove
// when valSize <= 8.
func copyMapValue(dst, src unsafe.Pointer, valSize uintptr) {
	switch valSize {
	case 1:
		*(*uint8)(dst) = *(*uint8)(src)
	case 2:
		*(*uint16)(dst) = *(*uint16)(src)
	case 4:
		*(*uint32)(dst) = *(*uint32)(src)
	case 8:
		*(*uint64)(dst) = *(*uint64)(src)
	default:
		gort.Memmove(dst, src, valSize)
	}
}

// encodeIntKey parses a JSON string key into int/uint and writes it to the
// low bytes of an 8B stack buffer (host endian). MapAssign uses buf's base
// address as the key pointer (runtime reads keyType.size bytes). Matches
// stdlib's literalStore behavior.
func encodeIntKey(buf *[8]byte, keyKind bindKind, keyStr string) error {
	switch keyKind {
	case bkInt, bkInt8, bkInt16, bkInt32, bkInt64:
		bits := 64
		switch keyKind {
		case bkInt8:
			bits = 8
		case bkInt16:
			bits = 16
		case bkInt32:
			bits = 32
		}
		v, err := strconv.ParseInt(keyStr, 10, bits)
		if err != nil {
			return fmt.Errorf("ndec: map int key %q: %w", keyStr, err)
		}
		switch keyKind {
		case bkInt8:
			*(*int8)(unsafe.Pointer(&buf[0])) = int8(v)
		case bkInt16:
			*(*int16)(unsafe.Pointer(&buf[0])) = int16(v)
		case bkInt32:
			*(*int32)(unsafe.Pointer(&buf[0])) = int32(v)
		default: // bkInt, bkInt64 (8B in 64-bit Go)
			*(*int64)(unsafe.Pointer(&buf[0])) = v
		}
		return nil
	case bkUint, bkUint8, bkUint16, bkUint32, bkUint64:
		bits := 64
		switch keyKind {
		case bkUint8:
			bits = 8
		case bkUint16:
			bits = 16
		case bkUint32:
			bits = 32
		}
		v, err := strconv.ParseUint(keyStr, 10, bits)
		if err != nil {
			return fmt.Errorf("ndec: map uint key %q: %w", keyStr, err)
		}
		switch keyKind {
		case bkUint8:
			*(*uint8)(unsafe.Pointer(&buf[0])) = uint8(v)
		case bkUint16:
			*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(v)
		case bkUint32:
			*(*uint32)(unsafe.Pointer(&buf[0])) = uint32(v)
		default: // bkUint, bkUint64
			*(*uint64)(unsafe.Pointer(&buf[0])) = v
		}
		return nil
	default:
		return fmt.Errorf("ndec: map key kind %d not supported", keyKind)
	}
}
