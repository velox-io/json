// Frame access and validation helpers shared across yield handlers.
//
// All helpers treat protocol violations (missing frame, wrong container kind,
// out-of-range indices) as fatal Unmarshal errors.
//
// Returned frame and fi references point directly into ctx.Frames array
// elements and are safe for callers to mutate.

package ndec

import (
	"fmt"
	"unsafe"
)

// pendingStructField returns the current STRUCT frame's pending field and
// its metadata.
//
// Failure semantics (all are reactor protocol violations that abort
// Unmarshal immediately):
//   - depth == 0: yield arrived with no active frame (unexpected)
//   - frame is not STRUCT: yield path / frame type mismatch
//   - BindPendingFieldIdx < 0: object_field did not fire before this yield
//   - BindPendingFieldIdx out of range: typeinfo / frame data inconsistency
//
// The returned frame and fi are direct pointers into ctx.Frames; callers
// may mutate them.
func (d *driverState) pendingStructField(label string) (*ndecFrame, *bindFieldInfo, error) {
	if d.ctx.Depth == 0 {
		return nil, nil, fmt.Errorf("ndec: %s without active frame", label)
	}
	frame := &d.ctx.Frames[d.ctx.Depth-1]
	if frame.BindContainerKind != uint8(bkStruct) {
		return nil, nil, fmt.Errorf("ndec: %s on non-struct frame (kind=%d)", label, frame.BindContainerKind)
	}
	if frame.BindPendingFieldIdx < 0 {
		return nil, nil, fmt.Errorf("ndec: %s without pending field", label)
	}
	ti := (*bindTypeInfo)(frame.BindType)
	if frame.BindPendingFieldIdx >= int32(ti.FieldCount) {
		return nil, nil, fmt.Errorf("ndec: %s pending field idx %d out of range %d",
			label, frame.BindPendingFieldIdx, ti.FieldCount)
	}
	fieldsBase := (*bindFieldInfo)(ti.Fields)
	fieldsSlice := unsafe.Slice(fieldsBase, ti.FieldCount)
	fi := &fieldsSlice[frame.BindPendingFieldIdx]
	return frame, fi, nil
}

// currentSliceFrame returns the current SLICE frame.
//
// Same failure semantics as pendingStructField, but only validates that the
// frame exists and is a SLICE frame. BindPendingFieldIdx is not read (SLICE
// frames have no pending_field concept).
func (d *driverState) currentSliceFrame(label string) (*ndecFrame, error) {
	if d.ctx.Depth == 0 {
		return nil, fmt.Errorf("ndec: %s without active frame", label)
	}
	frame := &d.ctx.Frames[d.ctx.Depth-1]
	if frame.BindContainerKind != uint8(bkSlice) {
		return nil, fmt.Errorf("ndec: %s on non-slice frame (kind=%d)", label, frame.BindContainerKind)
	}
	return frame, nil
}
