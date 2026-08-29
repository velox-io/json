//go:build (goexperiment.swissmap || go1.26) && !go1.27

package gort

import "unsafe"

// SwissMapLayout is a version-normalized view of the few fields this package
// needs from abi.MapType, the runtime's map-type descriptor. See maptype.go for
// the full rationale; the key point is this is NOT a memory overlay of
// abi.MapType. ReadMapLayout reads scattered fields by offset and returns this
// stable shape, and the pre-1.27 reader below synthesizes the
// KeysOff/KeyStride/ElemsOff/ElemStride quadruple from (SlotSize, ElemOff) so
// consumers write one addressing formula across 1.24 through 1.27+:
//
//	key(i)  = group + KeysOff  + i * KeyStride
//	elem(i) = group + ElemsOff + i * ElemStride
//
// GroupType is the group's *abi.Type (abi.MapType.Group); UnsafeNewArray with
// it batch-allocates groups whose key/elem pointer slots are traced correctly.
// GroupSize is the byte size of one group (stride for walking a batch).
type SwissMapLayout struct {
	GroupType  unsafe.Pointer
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	GroupSize  uintptr
}

// ReadMapLayout reads the pre-1.27 abi.MapType layout (shared unchanged by Go
// 1.24/1.25/1.26; 1.26 only renamed SwissMapType to MapType, field offsets are
// identical). maptype.go holds the 1.27+ reader; both return the same
// SwissMapLayout so consumers don't branch on Go version.
//
// pre-1.27 has only interleaved (KVKVKVKV) groups, so MapType stores one
// SlotSize and an ElemOff; KeyStride==ElemStride==SlotSize and KeysOff is
// always 8 (after the ctrl word). We synthesize the quadruple the 1.27 reader
// exposes directly.
//
// Go 1.26 abi.MapType (after abi.Type +48):
//
//	+64: Group (*abi.Type)   +80: GroupSize   +88: SlotSize   +96: ElemOff
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	slotSize := *(*uintptr)(unsafe.Add(mt, 88))
	elemOff := *(*uintptr)(unsafe.Add(mt, 96))
	return SwissMapLayout{
		GroupType:  *(*unsafe.Pointer)(unsafe.Add(mt, 64)),
		KeysOff:    8,
		KeyStride:  slotSize,
		ElemsOff:   8 + elemOff,
		ElemStride: slotSize,
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
