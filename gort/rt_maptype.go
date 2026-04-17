//go:build go1.27

package gort

import "unsafe"

// SwissMapLayout describes the group-internal addressing for Swiss Map key/elem access.
// Works for both interleaved (KVKVKVKV) and split (KKKKVVVV) layouts via the formula:
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

// ReadMapLayout reads the Swiss Map layout parameters from abi.MapType.
//
// Go 1.27+ abi.MapType layout (after abi.Type at +48):
//
//	+80:  GroupSize  uintptr
//	+88:  KeysOff   uintptr
//	+96:  KeyStride uintptr
//	+104: ElemsOff  uintptr
//	+112: ElemStride uintptr
func ReadMapLayout(mt unsafe.Pointer) SwissMapLayout {
	return SwissMapLayout{
		KeysOff:    *(*uintptr)(unsafe.Add(mt, 88)),
		KeyStride:  *(*uintptr)(unsafe.Add(mt, 96)),
		ElemsOff:   *(*uintptr)(unsafe.Add(mt, 104)),
		ElemStride: *(*uintptr)(unsafe.Add(mt, 112)),
		GroupSize:  *(*uintptr)(unsafe.Add(mt, 80)),
	}
}
