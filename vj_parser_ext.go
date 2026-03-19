package vjson

// vj_parser_ext.go contains cold-path parser functions that handle
// special field tags (`,string`), custom unmarshalers (json.Unmarshaler,
// encoding.TextUnmarshaler), and non-string map keys.
//
// These functions are separated from vj_parser.go to reduce icache
// pressure on the hot unmarshal path. See docs/perf-regression-analysis.md.

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

// bindScanArrayFn sets TypeInfo.ScanArrayFn for element types that have
// specialized array-scan paths (int*, uint*, float64), bypassing the
// generic scanValue/scanNumber dispatch per element.
func bindScanArrayFn(ti *TypeInfo) {
	switch ti.Kind {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		elemKind := ti.Kind
		elemType := ti.Ext.Type
		ti.ScanArrayFn = func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error) {
			return scanArrayInt(src, idx, arrayLen, elemSize, elemKind, elemType, ptr)
		}
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		elemKind := ti.Kind
		elemType := ti.Ext.Type
		ti.ScanArrayFn = func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error) {
			return scanArrayUint(src, idx, arrayLen, elemSize, elemKind, elemType, ptr)
		}
	case KindFloat64:
		ti.ScanArrayFn = scanArrayFloat64
	}
}

// scanValueSpecial handles fields with non-zero unmarshal flags (for example
// `,string`, RawMessage, Number, copy-string, or custom unmarshal hooks).
// Called only when ti.UFlags != 0, keeping scanValue lean.
func (sc *Parser) scanValueSpecial(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	// Native json.RawMessage: skipValue + copy — no interface dispatch.
	if ti.Flags&tiFlagRawMessage != 0 {
		endIdx, err := skipValue(src, idx)
		if err != nil {
			return endIdx, err
		}
		raw := make([]byte, endIdx-idx)
		copy(raw, src[idx:endIdx])
		*(*[]byte)(ptr) = raw
		return endIdx, nil
	}
	// Native json.Number field: capture the raw number text as a string.
	if ti.Flags&tiFlagNumber != 0 {
		return sc.scanNumberToString(src, idx, ptr)
	}
	if ti.Flags&tiFlagHasUnmarshalFn != 0 {
		endIdx, err := skipValue(src, idx)
		if err != nil {
			return endIdx, err
		}
		return endIdx, ti.Ext.UnmarshalFn(ptr, src[idx:endIdx])
	}
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch sliceAt(src, idx) {
	case '"':
		if ti.Flags&tiFlagHasTextUnmarshalFn != 0 {
			newIdx, s, err := sc.scanString(src, idx)
			if err != nil {
				return newIdx, err
			}
			return newIdx, ti.Ext.TextUnmarshalFn(ptr, []byte(s))
		}
		if ti.Flags&tiFlagQuoted != 0 {
			return sc.scanQuotedValue(src, idx, ti, ptr)
		}
		if ti.Kind == KindSlice {
			return sc.scanStringToSlice(src, idx, ti, ptr)
		}
		if ti.Kind == KindArray {
			return sc.scanStringToArray(src, idx, ti, ptr)
		}
		return sc.scanStringValue(src, idx, ti, ptr)
	case '{':
		switch ti.Kind {
		case KindStruct:
			return sc.scanStruct(src, idx, ti.resolveCodec().(*StructCodec), ptr)
		case KindMap:
			return sc.scanMap(src, idx, ti.resolveCodec().(*MapCodec), ptr)
		case KindAny:
			newIdx, m, err := sc.scanMapAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = m
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("object", ti.Ext.Type, idx)
		}
	case '[':
		switch ti.Kind {
		case KindSlice:
			return sc.scanSlice(src, idx, ti.resolveCodec().(*SliceCodec), ptr)
		case KindArray:
			return sc.scanArray(src, idx, ti.resolveCodec().(*ArrayCodec), ptr)
		case KindAny:
			newIdx, arr, err := sc.scanSliceAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = arr
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("array", ti.Ext.Type, idx)
		}
	case 't':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32True {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case KindBool:
			*(*bool)(ptr) = true
		case KindAny:
			*(*any)(ptr) = true
		default:
			return idx + 4, unmarshalBoolTypeError(ti, idx+4)
		}
		return idx + 4, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx+1])) != litU32Alse {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case KindBool:
			*(*bool)(ptr) = false
		case KindAny:
			*(*any)(ptr) = false
		default:
			return idx + 5, unmarshalBoolTypeError(ti, idx+5)
		}
		return idx + 5, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case KindPointer:
			*(*unsafe.Pointer)(ptr) = nil
		case KindSlice:
			*(*SliceHeader)(ptr) = SliceHeader{}
		case KindMap:
			nullifyMap(ti, ptr)
		case KindAny:
			*(*any)(ptr) = nil
		default:
			// Primitive value types (string, bool, int, uint, float) and structs:
			// null leaves the existing value unchanged, matching encoding/json.
		}
		return idx + 4, nil
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		// if (src[idx] >= '0' && src[idx] <= '9') || src[idx] == '-' {
		return sc.scanNumber(src, idx, ti, ptr)
		// }
	default:
		return idx, newSyntaxError(fmt.Sprintf("vjson: unexpected character %q at offset %d", src[idx], idx), idx)
	}
}

// scanQuotedValue handles the `,string` tag: the JSON value is a string that
// contains a number, bool, or another string. E.g. "123" → int(123).
//
// Note: unlike encoding/json, we do not reject bare (unquoted) values for
// `,string` fields — those are handled by the normal scanNumber/scanTrue/scanFalse
// paths in scanValue. This is intentionally more lenient.
func (sc *Parser) scanQuotedValue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	newIdx, inner, err := sc.scanString(src, idx)
	if err != nil {
		return newIdx, err
	}

	switch ti.Kind {
	case KindBool:
		switch inner {
		case "true":
			*(*bool)(ptr) = true
		case "false":
			*(*bool)(ptr) = false
		default:
			return newIdx, newSyntaxError(fmt.Sprintf("vjson: cannot unmarshal string %q into bool", inner), newIdx)
		}

	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		v, parseErr := strconv.ParseInt(inner, 10, 64)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into integer: %v", inner, parseErr), newIdx, parseErr)
		}
		if !intFitsKind(v, ti.Kind) {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Ext.Type, newIdx)
		}
		writeIntValue(ptr, ti.Kind, v)

	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		if len(inner) > 0 && inner[0] == '-' {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Ext.Type, newIdx)
		}
		v, parseErr := strconv.ParseUint(inner, 10, 64)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into unsigned integer: %v", inner, parseErr), newIdx, parseErr)
		}
		if !uintFitsKind(v, ti.Kind) {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Ext.Type, newIdx)
		}
		writeUintValue(ptr, ti.Kind, v)

	case KindFloat32:
		v, parseErr := strconv.ParseFloat(inner, 32)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into float32: %v", inner, parseErr), newIdx, parseErr)
		}
		*(*float32)(ptr) = float32(v)

	case KindFloat64:
		innerBytes := unsafe.Slice(unsafe.StringData(inner), len(inner))
		_, v, parseErr := scanFloat64(innerBytes, 0)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into float64: %v", inner, parseErr), newIdx, parseErr)
		}
		*(*float64)(ptr) = v

	case KindString:
		// Double-encoded string: JSON "\"hello\"" → inner is `"hello"` → need to unquote.
		if len(inner) >= 2 && inner[0] == '"' && inner[len(inner)-1] == '"' {
			_, innerStr, scanErr := sc.scanString([]byte(inner), 0)
			if scanErr != nil {
				return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal quoted string: %v", scanErr), newIdx, scanErr)
			}
			if ti.Flags&tiFlagCopyString != 0 {
				innerStr = strings.Clone(innerStr)
			}
			*(*string)(ptr) = innerStr
		} else {
			if sc.copyString || (ti.Flags&tiFlagCopyString != 0) {
				inner = strings.Clone(inner)
			}
			*(*string)(ptr) = inner
		}

	default:
		return newIdx, newUnmarshalTypeError("string", ti.Ext.Type, newIdx)
	}

	return newIdx, nil
}

// scanPointerQuoted handles pointer fields with the `,string` tag.
func (sc *Parser) scanPointerQuoted(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	pDec := ti.resolveCodec().(*PointerCodec)

	idx = skipWS(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	if src[idx] == 'n' {
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		*(*unsafe.Pointer)(ptr) = nil
		return idx + 4, nil
	}

	// Reuse existing allocation if non-nil (matches encoding/json behavior).
	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		if pDec.ElemHasPtr {
			elemPtr = sc.ptrAlloc(pDec.ElemRType, pDec.ElemSize)
		} else {
			backing := make([]byte, pDec.ElemSize)
			// elemPtr = unsafe.Pointer(&backing[0])
			elemPtr = slicePtr(backing)
		}
	}

	// Shallow copy to propagate Quoted flag to the element.
	elemTI := *pDec.ElemTI
	elemTI.Flags |= tiFlagQuoted
	elemTI.UFlags |= tiFlagQuoted
	newIdx, err := sc.scanValue(src, idx, &elemTI, elemPtr)
	if err != nil {
		return newIdx, err
	}

	*(*unsafe.Pointer)(ptr) = elemPtr
	return newIdx, nil
}

// resolveMapKey parses a JSON string key and writes the typed key value into keyPtr.
// keyPtr must point to zeroed memory of at least keyType.Size() bytes.
// Used by scanMap to avoid reflect.Value allocation for non-string-key maps.
func resolveMapKey(keyStr string, keyType reflect.Type, keyTI *TypeInfo, keyPtr unsafe.Pointer) error {
	if keyTI.Flags&tiFlagHasTextUnmarshalFn != 0 {
		return keyTI.Ext.TextUnmarshalFn(keyPtr, []byte(keyStr))
	}
	switch keyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(keyStr, 10, int(keyType.Size()*8))
		if err != nil {
			return newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid map key %q: %v", keyStr, err), 0, err)
		}
		switch keyType.Size() {
		case 1:
			*(*int8)(keyPtr) = int8(n)
		case 2:
			*(*int16)(keyPtr) = int16(n)
		case 4:
			*(*int32)(keyPtr) = int32(n)
		case 8:
			*(*int64)(keyPtr) = n
		}
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(keyStr, 10, int(keyType.Size()*8))
		if err != nil {
			return newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid map key %q: %v", keyStr, err), 0, err)
		}
		switch keyType.Size() {
		case 1:
			*(*uint8)(keyPtr) = uint8(n)
		case 2:
			*(*uint16)(keyPtr) = uint16(n)
		case 4:
			*(*uint32)(keyPtr) = uint32(n)
		case 8:
			*(*uint64)(keyPtr) = n
		}
		return nil
	case reflect.String:
		*(*string)(keyPtr) = keyStr
		return nil
	}
	return newSyntaxError(fmt.Sprintf("vjson: unsupported map key type: %v", keyType), 0)
}

// scanNumberToString handles a json.Number field: stores the raw number text
// as a string. Also accepts quoted strings ("123") and null.
func (sc *Parser) scanNumberToString(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch src[idx] {
	case '"':
		// Quoted number: "123" → json.Number("123")
		newIdx, s, err := sc.scanString(src, idx)
		if err != nil {
			return newIdx, err
		}
		*(*string)(ptr) = s
		return newIdx, nil
	case 'n':
		// null → empty json.Number (zero value)
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		*(*string)(ptr) = ""
		return idx + 4, nil
	default:
		// Bare number
		end, _, numErr := scanNumberSpan(src, idx)
		if numErr != nil {
			return end, numErr
		}
		*(*string)(ptr) = string(src[idx:end])
		return end, nil
	}
}

// scanStruct is the cold-path struct decoder, used only when scanValueSpecial
// dispatches to it (e.g. a struct type that also implements TextUnmarshaler
// but receives a JSON object instead of a string). The hot-path struct
// decoding is inlined directly in scanValue (vj_parser.go).
func (sc *Parser) scanStruct(src []byte, idx int, dec *StructCodec, base unsafe.Pointer) (int, error) {
	idx++
	idx = skipWSLong(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, nil
	}

	var firstErr error

	for {
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		var keyBytes []byte
		var err error
		idx, keyBytes, err = sc.scanStringKey(src, idx)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		fi := dec.LookupFieldBytes(keyBytes)
		if fi == nil {
			// Unknown field — skip value
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		} else {
			savedIdx := idx
			fieldPtr := unsafe.Add(base, fi.Offset)
			idx, err = sc.scanValue(src, idx, fi, fieldPtr)
			if err != nil {
				var ute *UnmarshalTypeError
				if !errors.As(err, &ute) {
					return idx, err // syntax error → abort
				}
				// Type mismatch: skip the value and continue.
				if idx == savedIdx {
					// Object/array mismatch: scanValue didn't consume the value.
					idx, err = skipValue(src, idx)
					if err != nil {
						return idx, err
					}
				}
				if firstErr == nil {
					firstErr = ute
				}
			}
		}

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, firstErr
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, firstErr
			}
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in object, got %q", src[idx]), idx)
	}
}
