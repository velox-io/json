//go:build go1.27

package gort

import "unsafe"

// SwissMapLayout is a version-normalized view of the few fields this package
// needs from abi.MapType, the runtime's map-type descriptor. abi.MapType lives
// in internal/abi (unimportable) and its field layout shifts across Go versions,
// so rather than overlaying it, ReadMapLayout reads scattered fields by offset
// and returns this stable shape. Don't cast *abi.MapType to *SwissMapLayout:
// the offsets don't line up, and pre-1.27 runtimes have no KeysOff/KeyStride
// fields at all (maptype_old.go synthesizes them from SlotSize/ElemOff).
//
// Consumers (InitMapSlots, the composite merge, verify probes) write one
// addressing formula that holds across 1.24 through 1.27+:
//
//	key(i)  = group + KeysOff  + i * KeyStride
//	elem(i) = group + ElemsOff + i * ElemStride
//
// GroupType is the group's *abi.Type (abi.MapType.Group). UnsafeNewArray(GroupType, N)
// batch-allocates N groups; because GroupType is the real group type, its key/elem
// pointer slots are traced correctly. GroupSize is the byte size of one group,
// used as the stride when walking a batch.
type SwissMapLayout struct {
	GroupType  unsafe.Pointer
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	GroupSize  uintptr
}

// ReadMapLayout reads the 1.27+ abi.MapType layout. maptype_old.go holds the
// pre-1.27 reader; both return the same SwissMapLayout so consumers don't
// branch on Go version.
//
// 1.27 restructured MapType's (SlotSize, ElemOff) pair into (KeysOff, KeyStride,
// ElemsOff, ElemStride) so one formula covers both interleaved (KVKVKVKV) and
// split (KKKKVVVV) groups. This reader is a direct 1:1 read; the pre-1.27
// reader synthesizes the quadruple from SlotSize/ElemOff.
//
// Go 1.27+ abi.MapType (after abi.Type +48):
//
//	+64: Group (*abi.Type)   +80: GroupSize   +88: KeysOff   +96: KeyStride
//	+104: ElemsOff   +112: ElemStride
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	return SwissMapLayout{
		GroupType:  *(*unsafe.Pointer)(unsafe.Add(mt, 64)),
		KeysOff:    *(*uintptr)(unsafe.Add(mt, 88)),
		KeyStride:  *(*uintptr)(unsafe.Add(mt, 96)),
		ElemsOff:   *(*uintptr)(unsafe.Add(mt, 104)),
		ElemStride: *(*uintptr)(unsafe.Add(mt, 112)),
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
