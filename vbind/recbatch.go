// RecBatch isolates recursive slice backings by capacity class, bounding the
// cross-parse backing chain between sibling allocations. Each
// RecBatchRow owns its backing, free bitmap, and free count. SlotClass selects
// the matrix through Block and carries element size, mode, flags, and detach
// group. Native takes free slots; Go refills and clears returned slots. Periodic
// group detachment bounds retained generations.

package vbind

import (
	"errors"
	"math/bits"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// These constants are part of the Go/C ABI and must match
// BIND_RECBATCH_ROW_COUNT and BIND_RECBATCH_MAX_CAP in bind_bridge.h.
const (
	recBatchRows   = 8
	RecBatchMaxCap = 128
)

// Small-cap rows hold roughly 64 elements each to reduce refill frequency.
func recBatchRowSlots(rowIdx uint32) uint32 {
	if rowIdx < 4 {
		return 64 >> rowIdx
	}
	return 8
}

// A 64-slot row saturates the bitmap instead of shifting by the word width.
func recBatchRowSlotMask(rowIdx uint32) uint64 {
	slots := recBatchRowSlots(rowIdx)
	if slots >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << slots) - 1
}

// cap must be a power of two in [1, RecBatchMaxCap].
func recBatchRowIdx(cap uint32) uint32 {
	return uint32(bits.TrailingZeros32(cap))
}

func recBatchRowCap(rowIdx uint32) uint32 {
	return uint32(1) << rowIdx
}

// RecBatchRow owns one contiguous slots*cap typed array and its availability
// state. Slot i begins at Base + i*cap*ElemSize. Group detachment replaces the
// matrix as one generation instead of sweeping individual rows.
type RecBatchRow struct {
	Base      unsafe.Pointer // off 0  contiguous slots*cap element array; nil until refill
	Bitmap    uint64         // off 8  inline; bit i: 1=free, 0=used
	FreeCount uint32         // off 16 slots with bitmap bit=1
	_         uint32         // off 20 pad to 24
}

// RecBatchMatrix groups the rows selected by SlotClass.Block. Native mutates row
// availability while Go owns backing replacement and pointer clearing.
type RecBatchMatrix struct {
	Rows [recBatchRows]RecBatchRow // 192B
}

// Compile-time ABI checks: array sizes must match the C struct sizes.
var (
	_ [unsafe.Sizeof(RecBatchRow{})]byte    = [24]byte{}
	_ [unsafe.Sizeof(RecBatchMatrix{})]byte = [192]byte{}
)

// RecBatch uses the same 48 byte SlotClass overlay as both bump modes.
func newRecBatchSlotClass(tpl SlotTemplate) RecBatchSlotClass {
	r := RecBatchSlotClass{
		RType:    tpl.RType,
		ElemSize: tpl.ElemSize,
		Mode:     slotRecBatch,
		Flags:    tpl.Flags,
		Group:    tpl.Group,
	}
	r.reset()
	return r
}

// Native code dereferences Block without a nil check, so detachment installs an
// empty matrix eagerly. Row backings remain lazy until their first refill.
func (r *RecBatchSlotClass) reset() {
	m := &RecBatchMatrix{}
	for i := range m.Rows {
		m.Rows[i] = RecBatchRow{
			Base:      nil,
			Bitmap:    0,
			FreeCount: 0,
		}
	}
	r.Block = unsafe.Pointer(m)
}

func (r *RecBatchSlotClass) matrix() *RecBatchMatrix {
	return (*RecBatchMatrix)(r.Block)
}

// RecBatch returns the recursive batch overlay. The caller must ensure that
// sc.Mode is slotRecBatch.
func (sc *SlotClass) RecBatch() *RecBatchSlotClass {
	return (*RecBatchSlotClass)(unsafe.Pointer(sc))
}

// IsRecBatch reports whether sc uses the RecBatchMatrix carve. Native carve
// writes block+offset+=elem_size directly, which is valid only for bump-style
// slots, so callers that assume a linear cursor must gate on this.
func (sc *SlotClass) IsRecBatch() bool {
	return sc.Mode == slotRecBatch
}

// refillRow stages the displaced base for reachability after it leaves the
// matrix, then stages the fresh base so Release can publish native writes into
// its slots. A nil allocator is used only before native can publish either base.
func (r *RecBatchSlotClass) refillRow(a *Allocator, rowIdx uint32) {
	row := &r.matrix().Rows[rowIdx]
	if a != nil && row.Base != nil {
		a.retained = append(a.retained, row.Base)
	}
	slots := recBatchRowSlots(rowIdx)
	cap := recBatchRowCap(rowIdx)
	row.Base = gort.UnsafeNewArray(r.RType, int(cap)*int(slots))
	if a != nil {
		a.retained = append(a.retained, row.Base)
	}
	row.Bitmap = recBatchRowSlotMask(rowIdx)
	row.FreeCount = slots
}

func (r *RecBatchSlotClass) slotAddr(rowIdx, i uint32) unsafe.Pointer {
	cap := recBatchRowCap(rowIdx)
	row := &r.matrix().Rows[rowIdx]
	return unsafe.Add(row.Base, uintptr(i)*uintptr(cap)*uintptr(r.ElemSize))
}

func (r *RecBatchSlotClass) take(rowIdx uint32) (unsafe.Pointer, bool) {
	row := &r.matrix().Rows[rowIdx]
	if row.Bitmap == 0 {
		return nil, false
	}
	i := uint32(bits.TrailingZeros64(row.Bitmap))
	row.Bitmap &^= 1 << i
	row.FreeCount--
	return r.slotAddr(rowIdx, i), true
}

// free clears every pointer before returning an aligned slot from the current
// row to its bitmap. cap must be a power of two in [1, RecBatchMaxCap].
func (r *RecBatchSlotClass) free(ptr unsafe.Pointer, cap uint32) {
	if cap < 1 || cap > RecBatchMaxCap {
		return
	}
	rowIdx := recBatchRowIdx(cap)
	row := &r.matrix().Rows[rowIdx]
	slotBytes := uintptr(cap) * uintptr(r.ElemSize)
	totalBytes := uintptr(recBatchRowSlots(rowIdx)) * slotBytes
	offset := uintptr(ptr) - uintptr(row.Base)
	if row.Base == nil || offset >= totalBytes || offset%slotBytes != 0 {
		return
	}
	i := uint32(offset / slotBytes)
	row.Bitmap |= 1 << i
	row.FreeCount++
	gort.MemclrHasPointers(ptr, slotBytes)
}

// ServeRefill replaces an empty row and moves hdr into one fresh slot. refillRow
// stages the displaced row for reachability and the fresh row for publication.
func (r *RecBatchSlotClass) ServeRefill(a *Allocator, rowIdx uint32, hdr *gort.SliceHeader) error {
	a.statsRecBatchRefill((*SlotClass)(unsafe.Pointer(r)))
	r.refillRow(a, rowIdx)
	bk, ok := r.take(rowIdx)
	if !ok {
		return errors.New("vbind: RecBatch refill left row empty")
	}
	cap := recBatchRowCap(rowIdx)
	if hdr.Data == nil {
		hdr.Data = bk
		hdr.Cap = int(cap)
		hdr.Len = 0
		return nil
	}
	if hdr.Len > 0 {
		gort.Memmove(bk, hdr.Data, uintptr(hdr.Len)*uintptr(r.ElemSize))
	}
	hdr.Data = bk
	hdr.Cap = int(cap)
	return nil
}

// ServeBypass grows beyond the matrix into a standalone typed backing. The new
// backing is staged for publication. A displaced matrix row remains rooted by
// its matrix, while a displaced standalone backing remains in retained from its
// own installation until Release.
func (r *RecBatchSlotClass) ServeBypass(a *Allocator, nextCap uint32, hdr *gort.SliceHeader) error {
	a.statsRecBatchBypass((*SlotClass)(unsafe.Pointer(r)))
	bk := gort.UnsafeNewArray(r.RType, int(nextCap))
	a.retained = append(a.retained, bk)
	if hdr.Data == nil {
		hdr.Data = bk
		hdr.Cap = int(nextCap)
		hdr.Len = 0
		return nil
	}
	oldData := hdr.Data
	oldCap := uint32(hdr.Cap)
	if hdr.Len > 0 {
		gort.Memmove(bk, hdr.Data, uintptr(hdr.Len)*uintptr(r.ElemSize))
	}
	hdr.Data = bk
	hdr.Cap = int(nextCap)
	// Native execution yields before freeing the old matrix slot and resumes
	// after the grow branch. Go must clear its pointers before returning it to
	// the reusable row. An old standalone backing becomes unreachable instead.
	if oldCap >= 1 && oldCap <= RecBatchMaxCap {
		r.free(oldData, oldCap)
	}
	return nil
}
