//go:build (goexperiment.swissmap || go1.26) && !go1.27

package gort

import "unsafe"

// SwissMapLayout describes group-internal key/elem addressing.
type SwissMapLayout struct {
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	GroupSize  uintptr
}

// ReadMapLayout reads layout parameters from abi.MapType.
//
// Go 1.26 abi.MapType (after abi.Type +48):
//
//	+80: GroupSize   +88: SlotSize   +96: ElemOff
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	slotSize := *(*uintptr)(unsafe.Add(mt, 88))
	elemOff := *(*uintptr)(unsafe.Add(mt, 96))
	return SwissMapLayout{
		KeysOff:    8,
		KeyStride:  slotSize,
		ElemsOff:   8 + elemOff,
		ElemStride: slotSize,
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
