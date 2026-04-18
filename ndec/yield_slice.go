// GROW_SLICE / GROW_SLICE_STRUCT yield handling: backing array growth and
// element write.

package ndec

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/gort"
)

const initialSliceCap = 4

func (d *driverState) allocBacking(elemBT *typeInfo, capN int) unsafe.Pointer {
	if elemBT.elemHasPtr() {
		return gort.UnsafeNewArray(elemBT.rtypePtr(), capN)
	}
	need := capN * int(elemBT.base.Size)
	if d.slab != nil {
		off := len(d.slab)
		if cap(d.slab)-off >= need {
			d.slab = d.slab[: off+need : cap(d.slab)]
			return unsafe.Pointer(&d.slab[off])
		}
	}
	bytes := make([]byte, need)
	return unsafe.Pointer(unsafe.SliceData(bytes))
}

func (d *driverState) handleGrowSliceYield() error {
	frame, err := d.currentSliceFrame("grow_slice")
	if err != nil {
		return err
	}

	sliceBT := (*typeInfo)(frame.BindType)
	elemBT := sliceBT.elemTypeInfo()
	elemSize := uintptr(elemBT.base.Size)
	elemKind := bindKind(elemBT.base.Kind)
	allocPtr := d.userData.YieldFlags&yfGrowAllocPtr != 0
	isNull := d.userData.YieldFlags&yfGrowNull != 0
	idx := frame.bindArrayIndex()

	needGrow := idx >= frame.BindArrayCap
	var newBacking unsafe.Pointer

	if needGrow {
		newCap := frame.BindArrayCap * 2
		if newCap == 0 {
			hint := uint32(atomic.LoadInt32(&sliceBT.base.CapHint))
			newCap = hint
			if newCap < initialSliceCap {
				newCap = initialSliceCap
			}
		}
		newBacking = d.allocBacking(elemBT, int(newCap))
		if frame.BindArrayCap > 0 {
			if elemBT.elemHasPtr() || allocPtr {
				gort.TypedSliceCopy(elemBT.rtypePtr(),
					newBacking, int(frame.BindArrayCap),
					frame.BindDst, int(frame.BindArrayCap))
			} else {
				oldBytes := unsafe.Slice((*byte)(frame.BindDst), uintptr(frame.BindArrayCap)*elemSize)
				newBytes := unsafe.Slice((*byte)(newBacking), uintptr(frame.BindArrayCap)*elemSize)
				copy(newBytes, oldBytes)
			}
		}
		frame.BindDst = newBacking
		frame.BindArrayCap = newCap

		if frame.BindSliceHdr != nil {
			hdr := (*goSliceHeader)(frame.BindSliceHdr)
			hdr.data = newBacking
			hdr.cap = int(newCap)
		}
	} else {
		newBacking = frame.BindDst
	}
	dst := unsafe.Add(newBacking, uintptr(idx)*elemSize)
	raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)

	if allocPtr {
		if isNull {
			*(*unsafe.Pointer)(dst) = nil
		} else {
			pointeeBT := elemBT.elemTypeInfo() // unwrap PTR to get pointee type
			ptr := gort.UnsafeNew(pointeeBT.rtypePtr())
			pointeeKind := bindKind(pointeeBT.base.Kind)
			switch pointeeKind {
			case bkString:
				h := (*goStringHeader)(ptr)
				h.data = unsafe.Pointer(&raw[0])
				h.len = uintptr(len(raw))
			case bkBool:
				if len(raw) == 4 && raw[0] == 't' {
					*(*uint8)(ptr) = 1
				} else {
					*(*uint8)(ptr) = 0
				}
			default:
				if err := writeNumberFallback(ptr, pointeeKind, raw); err != nil {
					return err
				}
			}
			*(*unsafe.Pointer)(dst) = ptr
		}
		frame.setBindArrayIndex(idx + 1)
		return nil
	}

	if isNull {
		switch elemKind {
		case bkString:
			h := (*goStringHeader)(dst)
			h.data = nil
			h.len = 0
		case bkBool, bkInt8, bkUint8:
			*(*uint8)(dst) = 0
		case bkInt16, bkUint16:
			*(*uint16)(dst) = 0
		case bkInt32, bkUint32, bkFloat32:
			*(*uint32)(dst) = 0
		default:
			*(*uint64)(dst) = 0
		}
		frame.setBindArrayIndex(idx + 1)
		return nil
	}

	if err := writeSliceElem(dst, elemKind, raw, d); err != nil {
		return err
	}

	frame.setBindArrayIndex(idx + 1)
	return nil
}

func (d *driverState) handleGrowSliceStructYield() error {
	if d.ctx.Depth < 2 {
		return fmt.Errorf("ndec: grow_slice_struct without parent SLICE frame (depth=%d)", d.ctx.Depth)
	}
	sliceFrame := &d.ctx.Frames[d.ctx.Depth-2]
	if sliceFrame.BindContainerKind != uint8(bkSlice) {
		return fmt.Errorf("ndec: grow_slice_struct on non-slice parent (kind=%d)",
			sliceFrame.BindContainerKind)
	}

	sliceBT := (*typeInfo)(sliceFrame.BindType)
	elemBT := sliceBT.elemTypeInfo()
	elemSize := uintptr(elemBT.base.Size)

	oldCap := sliceFrame.BindArrayCap
	var newCap uint32
	if oldCap == 0 {
		hint := uint32(atomic.LoadInt32(&sliceBT.base.CapHint))
		newCap = hint
		if newCap < initialSliceCap {
			newCap = initialSliceCap
		}
	} else {
		newCap = oldCap * 2
	}
	newBacking := d.allocBacking(elemBT, int(newCap))
	if oldCap > 0 {
		if elemBT.elemHasPtr() {
			gort.TypedSliceCopy(elemBT.rtypePtr(),
				newBacking, int(oldCap),
				sliceFrame.BindDst, int(oldCap))
		} else {
			oldBytes := unsafe.Slice((*byte)(sliceFrame.BindDst), uintptr(oldCap)*elemSize)
			newBytes := unsafe.Slice((*byte)(newBacking), uintptr(oldCap)*elemSize)
			copy(newBytes, oldBytes)
		}
	}
	sliceFrame.BindDst = newBacking
	sliceFrame.BindArrayCap = newCap

	if sliceFrame.BindSliceHdr != nil {
		hdr := (*goSliceHeader)(sliceFrame.BindSliceHdr)
		hdr.data = newBacking
		hdr.cap = int(newCap)
	}

	idx := sliceFrame.bindArrayIndex()
	child := &d.ctx.Frames[d.ctx.Depth-1]
	elemDst := unsafe.Add(newBacking, uintptr(idx)*elemSize)

	// Container kind depends on elem type
	elemContainerKind := bindKind(sliceBT.base.ElemKind)
	switch elemContainerKind {
	case bkStruct:
		child.BindType = unsafe.Pointer(&elemBT.base)
		child.BindDst = elemDst
		child.BindPendingFieldIdx = -1
		child.BindContainerKind = uint8(bkStruct)
		// SLICE<struct> child STRUCT frame: errCtx only reads ParentFieldIdx
		// on child SLICE/MAP with STRUCT parent paths. STRUCT children do not
		// read or write it.
	case bkSlice:
		// Nested [][]T: write inner slice header at elem slot
		innerTI := elemBT
		sh := (*goSliceHeader)(elemDst)
		sh.data = innerTI.emptySliceData()
		sh.len = 0
		sh.cap = 0
		child.BindType = unsafe.Pointer(&innerTI.base)
		child.BindDst = nil
		child.setBindArrayIndex(0)
		child.BindArrayCap = 0
		child.BindContainerKind = uint8(bkSlice)
		// Inner SLICE parent is not a STRUCT, so ParentFieldIdx is not read or written.
		child.BindSliceHdr = elemDst
	case bkMap:
		// []map[K]V: just grow outer backing, child MAP frame filled by BEGIN_MAP yield
		child.BindContainerKind = uint8(bkMap)
		child.BindSliceHdr = elemDst
	default:
		return fmt.Errorf("ndec: grow_slice_struct with unsupported elem kind %d", elemContainerKind)
	}
	return nil
}

// handleGrowSlicePtrStruct handles []*Struct begin_object.
func (d *driverState) handleGrowSlicePtrStruct() error {
	if d.ctx.Depth < 2 {
		return fmt.Errorf("ndec: grow_slice_ptr_struct without parent (depth=%d)", d.ctx.Depth)
	}
	sliceFrame := &d.ctx.Frames[d.ctx.Depth-2]
	if sliceFrame.BindContainerKind != uint8(bkSlice) {
		return fmt.Errorf("ndec: grow_slice_ptr_struct on non-slice parent")
	}

	sliceBT := (*typeInfo)(sliceFrame.BindType)
	elemBT := sliceBT.elemTypeInfo()
	pointeeBT := elemBT.elemTypeInfo()

	elemSize := uintptr(elemBT.base.Size)
	idx := sliceFrame.bindArrayIndex()

	if idx >= sliceFrame.BindArrayCap {
		if err := d.growOuterBacking(sliceFrame, sliceBT); err != nil {
			return err
		}
		sliceFrame = &d.ctx.Frames[d.ctx.Depth-2]
	}

	structPtr := gort.UnsafeNew(pointeeBT.rtypePtr())
	dst := unsafe.Add(unsafe.Pointer(sliceFrame.BindDst), uintptr(idx)*elemSize)
	*(*unsafe.Pointer)(dst) = structPtr

	child := &d.ctx.Frames[d.ctx.Depth-1]
	child.BindType = unsafe.Pointer(&pointeeBT.base)
	child.BindDst = structPtr
	child.BindPendingFieldIdx = -1
	child.BindContainerKind = uint8(bkStruct)
	return nil
}

func (d *driverState) growOuterBacking(sliceFrame *ndecFrame, sliceBT *typeInfo) error {
	elemBT := sliceBT.elemTypeInfo()
	elemSize := uintptr(elemBT.base.Size)

	oldCap := sliceFrame.BindArrayCap
	var newCap uint32
	if oldCap == 0 {
		newCap = uint32(atomic.LoadInt32(&sliceBT.base.CapHint))
		if newCap < initialSliceCap {
			newCap = initialSliceCap
		}
	} else {
		newCap = oldCap * 2
	}
	newBacking := d.allocBacking(elemBT, int(newCap))
	if oldCap > 0 {
		if elemBT.elemHasPtr() {
			gort.TypedSliceCopy(elemBT.rtypePtr(),
				newBacking, int(oldCap),
				sliceFrame.BindDst, int(oldCap))
		} else {
			oldBytes := unsafe.Slice((*byte)(sliceFrame.BindDst), uintptr(oldCap)*elemSize)
			newBytes := unsafe.Slice((*byte)(newBacking), uintptr(oldCap)*elemSize)
			copy(newBytes, oldBytes)
		}
	}
	sliceFrame.BindDst = newBacking
	sliceFrame.BindArrayCap = newCap

	if sliceFrame.BindSliceHdr != nil {
		hdr := (*goSliceHeader)(sliceFrame.BindSliceHdr)
		hdr.data = newBacking
		hdr.cap = int(newCap)
	}
	return nil
}
