package ndec

import (
	"fmt"
	"unsafe"

	"github.com/velox-io/json/gort"
)

func writePtrElemRaw(elemPtr unsafe.Pointer, elemKind bindKind, raw []byte) error {
	switch elemKind {
	case bkString:
		h := (*goStringHeader)(elemPtr)
		if len(raw) > 0 {
			h.data = unsafe.Pointer(&raw[0])
		}
		h.len = uintptr(len(raw))
	case bkBool:
		if len(raw) == 4 && raw[0] == 't' {
			*(*uint8)(elemPtr) = 1
		} else if len(raw) == 5 && raw[0] == 'f' {
			*(*uint8)(elemPtr) = 0
		} else {
			return fmt.Errorf("ndec: ptr-bool got unexpected raw %q", raw)
		}
	default:
		if err := writeNumberFallback(elemPtr, elemKind, raw); err != nil {
			return err
		}
	}
	return nil
}

// allocPtrChain allocates a chain of pointer layers starting from bt.
// Returns (topSlot, leafBT, leafSlot) where leaf is the first non-PTR type.
// For a simple *int: topSlot = leafSlot, leafBT = int typeinfo.
// For **int: topSlot is *&int, leafSlot is &int, leafBT = int typeinfo.
func allocPtrChain(bt *typeInfo) (topSlot, leafSlot unsafe.Pointer, leafBT *typeInfo) {
	topSlot = gort.UnsafeNew(bt.rtypePtr())
	curSlot := topSlot
	curBT := bt
	for curBT.kind() == bkPtr {
		inner := curBT.elemTypeInfo()
		next := gort.UnsafeNew(inner.rtypePtr())
		*(*unsafe.Pointer)(curSlot) = next
		curSlot = next
		curBT = inner
	}
	leafBT = curBT
	leafSlot = curSlot
	return
}

// handleBeginPtrRoot serves both root pointer cases: sp==0 receives raw
// scalar bytes from root_scalar (no push), sp==1 receives container flags
// from begin_object/begin_array (parser already pushed child slot).
func (d *driverState) handleBeginPtrRoot(root *ndecFrame) error {
	rootBT := (*typeInfo)(root.BindType)
	if rootBT.kind() != bkPtr {
		return fmt.Errorf("ndec: root ptr yield on non-ptr root kind %d", rootBT.kind())
	}

	// Root scalar PTR: sp==0 (no STACK_PUSH, scalar hook).
	// Use raw token bytes to decide allocation.
	if d.ctx.Sp == 0 {
		raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)
		if len(raw) == 4 && raw[0] == 'n' && raw[1] == 'u' && raw[2] == 'l' && raw[3] == 'l' {
			*(*unsafe.Pointer)(root.BindDst) = nil
			return nil
		}
		topSlot, leafSlot, leafBT := allocPtrChain(rootBT)
		*(*unsafe.Pointer)(root.BindDst) = topSlot
		return writePtrElemRaw(leafSlot, leafBT.kind(), raw)
	}

	// Root container PTR: sp==1, parser pushed child frame at frames[1].
	// Allocate the ptr chain and rewrite root frame from PTR to the leaf
	// container kind so subsequent root close behavior is consistent.
	// The actual container state lives at frames[1] (child).
	topSlot, leafSlot, leafBT := allocPtrChain(rootBT)
	*(*unsafe.Pointer)(root.BindDst) = topSlot

	switch leafBT.kind() {
	case bkStruct:
		root.BindType = unsafe.Pointer(&leafBT.base)
		root.BindDst = leafSlot
		root.BindContainerKind = uint8(bkStruct)
		root.BindPendingFieldIdx = -1
		// Fill child STRUCT frame at frames[1] (already pushed by parser).
		child := &d.ctx.Frames[1]
		child.BindType = unsafe.Pointer(&leafBT.base)
		child.BindDst = leafSlot
		child.BindContainerKind = uint8(bkStruct)
		child.BindPendingFieldIdx = -1
	case bkSlice:
		sh := (*goSliceHeader)(leafSlot)
		sh.data = leafBT.emptySliceData()
		sh.len = 0
		sh.cap = 0
		root.BindType = unsafe.Pointer(&leafBT.base)
		root.BindDst = nil // lazy alloc
		root.setBindArrayIndex(0)
		root.BindArrayCap = 0
		root.BindContainerKind = uint8(bkSlice)
		root.BindSliceHdr = unsafe.Pointer(sh)
		// Fill child SLICE frame at frames[1].
		child := &d.ctx.Frames[1]
		child.BindType = unsafe.Pointer(&leafBT.base)
		child.BindDst = nil
		child.setBindArrayIndex(0)
		child.BindArrayCap = 0
		child.BindContainerKind = uint8(bkSlice)
		child.BindSliceHdr = unsafe.Pointer(sh)
	case bkMap:
		mapHeader := gort.MakeMap(leafBT.rtypePtr(), 0, nil)
		*(*unsafe.Pointer)(leafSlot) = mapHeader
		// Rewrite root as MAP and bootstrap child MAP frame at frames[1].
		root.BindType = unsafe.Pointer(&leafBT.base)
		root.BindContainerKind = uint8(bkMap)
		child := &d.ctx.Frames[1]
		return d.bootstrapRootMap(leafBT, mapHeader, child)
	default:
		return fmt.Errorf("ndec: root ptr leaf kind %d unsupported", leafBT.kind())
	}
	return nil
}

// handleBeginPtrYield handles *T fields.
func (d *driverState) handleBeginPtrYield() error {
	// Root PTR: frames[0].kind == PTR (pre-filled root binding by driver).
	//   sp==0: root scalar PTR (yield in ndec_root_scalar, no push)
	//   sp==1: root container PTR (reactor pushed child, then yielded)
	rootFrame := &d.ctx.Frames[0]
	if rootFrame.BindContainerKind == uint8(bkPtr) && d.ctx.Sp <= 1 {
		return d.handleBeginPtrRoot(rootFrame)
	}

	if d.userData.YieldFlags == yfPtrToStruct {
		return d.handleBeginPtrStruct()
	}
	if d.userData.YieldFlags == yfPtrToSlice {
		return d.handleBeginPtrSlice()
	}
	if d.userData.YieldFlags == yfPtrToMap {
		return d.handleBeginPtrMap()
	}

	frame, fi, err := d.pendingStructField("ptr yield")
	if err != nil {
		return err
	}
	if fi.Kind != uint8(bkPtr) {
		return fmt.Errorf("ndec: ptr yield on non-ptr kind %d", fi.Kind)
	}
	elemBT := (*typeInfo)(fi.Type)
	if elemBT.rtypePtr() == nil {
		return fmt.Errorf("ndec: ptr elem has no rtype")
	}

	// **T / ***T ...: elem itself is PTR, requires a chain of allocs
	// down to the concrete leaf type. A single alloc (rtypeOf(*int))
	// would try to write raw bytes as *int, and writeNumberFallback
	// has no branch for bkPtr ("unsupported kind").
	//
	// Algorithm: record each alloc'd slot in order, alloc + link
	// layer by layer, then write raw into the leaf slot. This also
	// makes null a leaf no-op (stdlib-equivalent: **T on null
	// allocates nothing, field stays nil).
	raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)
	isNull := len(raw) == 4 && raw[0] == 'n' && raw[1] == 'u' && raw[2] == 'l' && raw[3] == 'l'

	if isNull {
		// stdlib on *T / **T / ... with null: field set to nil directly,
		// no allocation. No distinction between intermediate layers (top nil
		// expresses "no value"). The entire ptr chain is not allocated.
		*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(frame.BindDst), uintptr(fi.Offset))) = nil
		frame.BindPendingFieldIdx = -1
		return nil
	}

	// Multi-ptr chain descent: allocate top layer first, each subsequent
	// layer writes a new alloc into the previous slot. elemBT points to the
	// first elem's typeinfo beyond the field. If it's still PTR, keep
	// unwrapping inward.
	topSlot := gort.UnsafeNew(elemBT.rtypePtr())
	curSlot := topSlot
	curBT := elemBT
	for curBT.kind() == bkPtr {
		inner := curBT.elemTypeInfo()
		if inner == nil || inner.rtypePtr() == nil {
			return fmt.Errorf("ndec: multi-ptr elem missing inner type")
		}
		next := gort.UnsafeNew(inner.rtypePtr())
		*(*unsafe.Pointer)(curSlot) = next
		curSlot = next
		curBT = inner
	}

	if err := writePtrElemRaw(curSlot, curBT.kind(), raw); err != nil {
		return err
	}

	*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(frame.BindDst), uintptr(fi.Offset))) = topSlot
	frame.BindPendingFieldIdx = -1
	return nil
}

// handleBeginPtrMapValueYield handles map[string]*T values.
func (d *driverState) handleBeginPtrMapValueYield() error {
	if d.userData.YieldFlags == yfMapValuePtrStruct {
		// Parser has already pushed: child slot is at frames[sp];
		// parent MAP frame is at frames[sp-1].
		if d.ctx.Sp < 1 {
			return fmt.Errorf("ndec: map ptr-struct yield without parent frame (sp=%d)", d.ctx.Sp)
		}
		parent := &d.ctx.Frames[d.ctx.Sp-1]
		if parent.BindContainerKind != uint8(bkMap) {
			return fmt.Errorf("ndec: map ptr-struct yield on non-map parent")
		}
		mapBT, err := mapBuiltTypeOfFrame(parent)
		if err != nil {
			return err
		}
		elemVal := mapBT.elemTypeInfo()
		if elemVal == nil || elemVal.rtypePtr() == nil {
			return fmt.Errorf("ndec: map ptr-struct elem has no rtype")
		}
		elemPtr := gort.UnsafeNew(elemVal.rtypePtr())
		*(*unsafe.Pointer)(mapValueSlotPtr(parent, mapBT, parent.bindKvCount())) = elemPtr
		child := &d.ctx.Frames[d.ctx.Sp]
		child.BindType = unsafe.Pointer(&elemVal.base)
		child.BindDst = elemPtr
		child.BindPendingFieldIdx = -1
		child.BindContainerKind = uint8(bkStruct)
		// Phase already set by parser STACK_PUSH; no Sp increment needed.
		return nil
	}
	// Map ptr scalar value yield (non-struct): the yield fires from a
	// MAP-frame scalar hook, before any push happens. sp points at the
	// MAP frame itself (no child to fill).
	if d.ctx.Sp < 0 {
		return fmt.Errorf("ndec: map ptr value yield without active frame")
	}
	frame := &d.ctx.Frames[d.ctx.Sp]
	if frame.BindContainerKind != uint8(bkMap) {
		return fmt.Errorf("ndec: map ptr value yield on non-map frame (kind=%d)", frame.BindContainerKind)
	}
	mapBT, err := mapBuiltTypeOfFrame(frame)
	if err != nil {
		return err
	}
	elemVal := mapBT.elemTypeInfo()
	if elemVal == nil || elemVal.rtypePtr() == nil {
		return fmt.Errorf("ndec: map ptr value elem has no rtype")
	}
	elemPtr := gort.UnsafeNew(elemVal.rtypePtr())
	raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)
	if err := writePtrElemRaw(elemPtr, bindKind(elemVal.base.Kind), raw); err != nil {
		return err
	}
	*(*unsafe.Pointer)(mapValueSlotPtr(frame, mapBT, frame.bindKvCount())) = elemPtr
	frame.setBindKvCount(frame.bindKvCount() + 1)
	if frame.bindKvCount() >= frame.BindArrayCap {
		return d.flushMapFrame(frame, false)
	}
	return nil
}

// handleBeginPtrStruct handles *<struct> fields: alloc + fill child frame.
func (d *driverState) handleBeginPtrStruct() error {
	// Parser has already pushed: child slot is at frames[sp];
	// parent STRUCT frame is at frames[sp-1].
	if d.ctx.Sp < 1 {
		return fmt.Errorf("ndec: ptr-struct yield without parent frame (sp=%d)", d.ctx.Sp)
	}
	parent := &d.ctx.Frames[d.ctx.Sp-1]
	if parent.BindContainerKind != uint8(bkStruct) || parent.BindPendingFieldIdx < 0 {
		return fmt.Errorf("ndec: ptr-struct yield on invalid parent")
	}
	parentTI := (*bindTypeInfo)(parent.BindType)
	if parent.BindPendingFieldIdx >= int32(parentTI.FieldCount) {
		return fmt.Errorf("ndec: ptr-struct pending field idx out of range")
	}
	fieldsBase := (*bindFieldInfo)(parentTI.Fields)
	fi := &unsafe.Slice(fieldsBase, parentTI.FieldCount)[parent.BindPendingFieldIdx]
	if fi.Kind != uint8(bkPtr) {
		return fmt.Errorf("ndec: ptr-struct yield on non-ptr field kind %d", fi.Kind)
	}
	elemBT := (*typeInfo)(fi.Type)
	if elemBT == nil || elemBT.rtypePtr() == nil {
		return fmt.Errorf("ndec: ptr-struct elem has no rtype")
	}

	elemPtr := gort.UnsafeNew(elemBT.rtypePtr())
	*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(parent.BindDst), uintptr(fi.Offset))) = elemPtr
	parent.BindPendingFieldIdx = -1

	child := &d.ctx.Frames[d.ctx.Sp]
	child.BindType = unsafe.Pointer(&elemBT.base)
	child.BindDst = elemPtr
	child.BindPendingFieldIdx = -1
	child.BindContainerKind = uint8(bkStruct)
	// Phase already set by parser STACK_PUSH.
	return nil
}

// handleBeginPtrSlice handles *[]T fields (PTR to SLICE container).
func (d *driverState) handleBeginPtrSlice() error {
	// Parser already pushed: child slot is at frames[sp]; parent
	// STRUCT frame is at frames[sp-1].
	if d.ctx.Sp < 1 {
		return fmt.Errorf("ndec: ptr-slice yield without parent")
	}
	parent := &d.ctx.Frames[d.ctx.Sp-1]
	if parent.BindContainerKind != uint8(bkStruct) || parent.BindPendingFieldIdx < 0 {
		return fmt.Errorf("ndec: ptr-slice yield on invalid parent")
	}
	parentTI := (*bindTypeInfo)(parent.BindType)
	fieldsBase := (*bindFieldInfo)(parentTI.Fields)
	fi := &unsafe.Slice(fieldsBase, parentTI.FieldCount)[parent.BindPendingFieldIdx]
	if fi.Kind != uint8(bkPtr) {
		return fmt.Errorf("ndec: ptr-slice yield on non-ptr field")
	}
	// Snapshot parent_field_idx for the child SLICE frame, used by error path.
	parentFieldIdx := parent.BindPendingFieldIdx

	// elem is the SLICE container typeinfo (*T → elem=[]T)
	sliceTI := (*typeInfo)(fi.Type)

	// Alloc slice header and write ptr to struct field
	sh := (*goSliceHeader)(gort.UnsafeNew(sliceTI.rtypePtr()))
	sh.data = sliceTI.emptySliceData()
	sh.len = 0
	sh.cap = 0
	*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(parent.BindDst), uintptr(fi.Offset))) = unsafe.Pointer(sh)
	parent.BindPendingFieldIdx = -1

	// Fill child SLICE frame at frames[sp] (already pushed by parser).
	child := &d.ctx.Frames[d.ctx.Sp]
	child.BindType = unsafe.Pointer(&sliceTI.base)
	child.BindDst = nil // lazy alloc
	child.setBindArrayIndex(0)
	child.BindArrayCap = 0
	child.BindContainerKind = uint8(bkSlice)
	child.ParentFieldIdx = parentFieldIdx
	child.BindSliceHdr = unsafe.Pointer(sh)
	// Phase already set by parser STACK_PUSH; no Sp increment needed.
	return nil
}

// handleBeginPtrMap handles *map[K]V fields.
func (d *driverState) handleBeginPtrMap() error {
	// Parser has already pushed: child slot at frames[sp];
	// parent STRUCT frame at frames[sp-1].
	if d.ctx.Sp < 1 {
		return fmt.Errorf("ndec: ptr-map yield without parent")
	}
	parent := &d.ctx.Frames[d.ctx.Sp-1]
	if parent.BindContainerKind != uint8(bkStruct) || parent.BindPendingFieldIdx < 0 {
		return fmt.Errorf("ndec: ptr-map yield on invalid parent")
	}
	parentTI := (*bindTypeInfo)(parent.BindType)
	fieldsBase := (*bindFieldInfo)(parentTI.Fields)
	fi := &unsafe.Slice(fieldsBase, parentTI.FieldCount)[parent.BindPendingFieldIdx]
	if fi.Kind != uint8(bkPtr) {
		return fmt.Errorf("ndec: ptr-map yield on non-ptr field")
	}
	parentFieldIdx := parent.BindPendingFieldIdx

	mapTI := (*typeInfo)(fi.Type)
	mapHeader := gort.MakeMap(mapTI.rtypePtr(), 0, nil)
	slot := gort.UnsafeNew(mapTI.rtypePtr())
	*(*unsafe.Pointer)(slot) = mapHeader
	*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(parent.BindDst), uintptr(fi.Offset))) = slot
	parent.BindPendingFieldIdx = -1

	// Fill child MAP frame at frames[sp] (already pushed by parser).
	need := int(mapTI.kvSlotSize()) * mapKVBufCount
	base := d.reserveMapKVBuf(need)
	child := &d.ctx.Frames[d.ctx.Sp]
	initMapFrame(child, mapTI, mapHeader, base, mapKVBufCount, parentFieldIdx)
	return nil
}
