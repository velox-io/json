package vdec

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// scanStringToSlice handles a JSON string token targeting a typ.KindSlice field.
// Only []byte (elem = uint8, size = 1) is supported — base64-decode the string.
// All other slice types return an error.
func (sc *Parser) scanStringToSlice(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	sDec := ti.ResolveSlice()
	if sDec.ElemTI.Kind != typ.KindUint8 || sDec.ElemSize != 1 {
		return idx, newUnmarshalTypeError("string", ti.Type, idx)
	}

	newIdx, raw, err := sc.scanStringKey(src, idx)
	if err != nil {
		return newIdx, err
	}

	if len(raw) == 0 {
		// "" → empty (non-nil) byte slice
		sh := (*SliceHeader)(ptr)
		sh.Data = sDec.EmptySliceData
		sh.Len = 0
		sh.Cap = 0
		return newIdx, nil
	}

	dbuf := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))
	n, err := base64.StdEncoding.Decode(dbuf, raw)
	if err != nil {
		return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid base64 in []byte field: %v", err), newIdx, err)
	}
	dbuf = dbuf[:n]

	sh := (*SliceHeader)(ptr)
	sh.Data = unsafe.Pointer(&dbuf[0])
	sh.Len = n
	sh.Cap = n
	return newIdx, nil
}

// scanStringToArray handles a JSON string token targeting a typ.KindArray field.
// Only [N]byte (elem = uint8, size = 1) is supported — base64-decode the string.
// Decoded bytes are copied into the fixed-size array; if shorter, remaining bytes
// are zeroed; if longer, excess is silently truncated.
func (sc *Parser) scanStringToArray(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	aDec := ti.ResolveArray()
	if aDec.ElemTI.Kind != typ.KindUint8 || aDec.ElemSize != 1 {
		return idx, newUnmarshalTypeError("string", ti.Type, idx)
	}

	newIdx, raw, err := sc.scanStringKey(src, idx)
	if err != nil {
		return newIdx, err
	}

	dst := unsafe.Slice((*byte)(ptr), aDec.ArrayLen)

	if len(raw) == 0 {
		// "" → zero the array
		clear(dst)
		return newIdx, nil
	}

	dbuf := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))
	n, err := base64.StdEncoding.Decode(dbuf, raw)
	if err != nil {
		return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid base64 in [%d]byte field: %v", aDec.ArrayLen, err), newIdx, err)
	}

	copied := copy(dst, dbuf[:n])
	if copied < aDec.ArrayLen {
		clear(dst[copied:])
	}
	return newIdx, nil
}
