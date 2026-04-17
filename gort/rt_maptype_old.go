//go:build (goexperiment.swissmap || go1.26) && !go1.27

package gort

import "unsafe"

// SwissMapLayout describes the group-internal addressing for Swiss Map key/elem access.
type SwissMapLayout struct {
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	GroupSize  uintptr
}

// ReadMapLayout reads the Swiss Map layout parameters from abi.MapType.
//
// Go 1.26 abi.MapType layout (after abi.Type at +48):
//
//	+80: GroupSize uintptr
//	+88: SlotSize  uintptr
//	+96: ElemOff   uintptr
//
// The interleaved layout uses a single slot stride:
//
//	key(i)  = group + 8 + i * SlotSize
//	elem(i) = group + 8 + ElemOff + i * SlotSize
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	slotSize := *(*uintptr)(unsafe.Add(mt, 88))
	elemOff := *(*uintptr)(unsafe.Add(mt, 96))
	return SwissMapLayout{
		KeysOff:    8, // ctrl word = 8 bytes
		KeyStride:  slotSize,
		ElemsOff:   8 + elemOff,
		ElemStride: slotSize,
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
