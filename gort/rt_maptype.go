//go:build go1.27

package gort

import "unsafe"

// SwissMapLayout describes group-internal key/elem addressing.
//
//	key(i)  = group + KeysOff  + i * KeyStride
//	elem(i) = group + ElemsOff + i * ElemStride
type SwissMapLayout struct {
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	GroupSize  uintptr
}

// ReadMapLayout reads layout parameters from abi.MapType.
//
// Go 1.27+ abi.MapType (after abi.Type +48):
//
//	+80: GroupSize   +88: KeysOff   +96: KeyStride
//	+104: ElemsOff   +112: ElemStride
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	return SwissMapLayout{
		KeysOff:    *(*uintptr)(unsafe.Add(mt, 88)),
		KeyStride:  *(*uintptr)(unsafe.Add(mt, 96)),
		ElemsOff:   *(*uintptr)(unsafe.Add(mt, 104)),
		ElemStride: *(*uintptr)(unsafe.Add(mt, 112)),
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
