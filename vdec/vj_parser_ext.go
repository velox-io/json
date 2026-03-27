package vdec

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// scanValueSpecial handles type-level slow paths kept out of scanValue.
func (sc *Parser) scanValueSpecial(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	if ti.TypeFlags&typ.TypeFlagRawMessage != 0 {
		endIdx, err := skipValue(src, idx)
		if err != nil {
			return endIdx, err
		}
		raw := make([]byte, endIdx-idx)
		copy(raw, src[idx:endIdx])
		*(*[]byte)(ptr) = raw
		return endIdx, nil
	}
	if ti.TypeFlags&typ.TypeFlagNumber != 0 {
		return sc.scanNumberToString(src, idx, ptr)
	}
	if ti.TypeFlags&typ.TypeFlagHasUnmarshalFn != 0 {
		endIdx, err := skipValue(src, idx)
		if err != nil {
			return endIdx, err
		}
		return endIdx, ti.UnmarshalFn(ptr, src[idx:endIdx])
	}
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch sliceAt(src, idx) {
	case '"':
		if ti.TypeFlags&typ.TypeFlagHasTextUnmarshalFn != 0 {
			newIdx, s, err := sc.scanString(src, idx)
			if err != nil {
				return newIdx, err
			}
			return newIdx, ti.TextUnmarshalFn(ptr, []byte(s))
		}
		if ti.Kind == typ.KindSlice {
			return sc.scanStringToSlice(src, idx, ti, ptr)
		}
		if ti.Kind == typ.KindArray {
			return sc.scanStringToArray(src, idx, ti, ptr)
		}
		return sc.scanStringValue(src, idx, ti, ptr)
	case '{':
		switch ti.Kind {
		case typ.KindStruct:
			return sc.scanStruct(src, idx, ti.ResolveStruct(), ptr)
		case typ.KindMap:
			return sc.scanMap(src, idx, ti, ptr)
		case typ.KindAny:
			newIdx, m, err := sc.scanMapAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = m
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("object", ti.Type, idx)
		}
	case '[':
		switch ti.Kind {
		case typ.KindSlice:
			return sc.scanSlice(src, idx, ti.ResolveSlice(), ptr)
		case typ.KindArray:
			return sc.scanArray(src, idx, ti.ResolveArray(), ptr)
		case typ.KindAny:
			newIdx, arr, err := sc.scanSliceAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = arr
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("array", ti.Type, idx)
		}
	case 't':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32True {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case typ.KindBool:
			*(*bool)(ptr) = true
		case typ.KindAny:
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
		case typ.KindBool:
			*(*bool)(ptr) = false
		case typ.KindAny:
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
		case typ.KindPointer:
			*(*unsafe.Pointer)(ptr) = nil
		case typ.KindSlice:
			*(*SliceHeader)(ptr) = SliceHeader{}
		case typ.KindMap:
			nullifyMap(ti, ptr)
		case typ.KindAny:
			*(*any)(ptr) = nil
		default:
			// Primitive values and structs keep their existing value on null.
		}
		return idx + 4, nil
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		return sc.scanNumber(src, idx, ti, ptr)
	default:
		return idx, newSyntaxError(fmt.Sprintf("vjson: unexpected character %q at offset %d", src[idx], idx), idx)
	}
}

// scanFieldTagged handles `,string` and `,copy` field tags.
func (sc *Parser) scanFieldTagged(src []byte, idx int, fi *DecFieldInfo, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	copyStr := fi.TagFlags&typ.TagFlagCopyString != 0

	if fi.TagFlags&typ.TagFlagQuoted != 0 {
		// `,string` still preserves encoding/json's null handling.
		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == 'n' {
			if idx+4 > len(src) {
				return idx, errUnexpectedEOF
			}
			if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
				return idx, invalidLiteralError(idx)
			}
			switch fi.Kind {
			case typ.KindPointer:
				*(*unsafe.Pointer)(ptr) = nil
			case typ.KindSlice:
				*(*SliceHeader)(ptr) = SliceHeader{}
			case typ.KindMap:
				nullifyMap(ti, ptr)
			}
			return idx + 4, nil
		}
		if fi.Kind == typ.KindPointer {
			return sc.scanPointerQuoted(src, idx, ti, copyStr, ptr)
		}
		return sc.scanQuotedValue(src, idx, ti, copyStr, ptr)
	}
	// `,copy` forces every nested string path to allocate.
	prev := sc.copyString
	sc.copyString = true
	newIdx, err := sc.scanValue(src, idx, ti, ptr)
	sc.copyString = prev
	return newIdx, err
}

// scanQuotedValue decodes the payload of a `,string` field.
// Bare values still go through the normal scanValue path.
func (sc *Parser) scanQuotedValue(src []byte, idx int, ti *DecTypeInfo, copyStr bool, ptr unsafe.Pointer) (int, error) {
	newIdx, inner, err := sc.scanString(src, idx)
	if err != nil {
		return newIdx, err
	}

	switch ti.Kind {
	case typ.KindBool:
		switch inner {
		case "true":
			*(*bool)(ptr) = true
		case "false":
			*(*bool)(ptr) = false
		default:
			return newIdx, newSyntaxError(fmt.Sprintf("vjson: cannot unmarshal string %q into bool", inner), newIdx)
		}

	case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64:
		v, parseErr := strconv.ParseInt(inner, 10, 64)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into integer: %v", inner, parseErr), newIdx, parseErr)
		}
		if !intFitsKind(v, ti.Kind) {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Type, newIdx)
		}
		writeIntValue(ptr, ti.Kind, v)

	case typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
		if len(inner) > 0 && inner[0] == '-' {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Type, newIdx)
		}
		v, parseErr := strconv.ParseUint(inner, 10, 64)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into unsigned integer: %v", inner, parseErr), newIdx, parseErr)
		}
		if !uintFitsKind(v, ti.Kind) {
			return newIdx, newUnmarshalTypeError("number "+inner, ti.Type, newIdx)
		}
		writeUintValue(ptr, ti.Kind, v)

	case typ.KindFloat32:
		v, parseErr := strconv.ParseFloat(inner, 32)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into float32: %v", inner, parseErr), newIdx, parseErr)
		}
		*(*float32)(ptr) = float32(v)

	case typ.KindFloat64:
		innerBytes := unsafe.Slice(unsafe.StringData(inner), len(inner))
		_, v, parseErr := scanFloat64(innerBytes, 0)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into float64: %v", inner, parseErr), newIdx, parseErr)
		}
		*(*float64)(ptr) = v

	case typ.KindString:
		// `,string` on strings produces a second layer of JSON quoting.
		if len(inner) >= 2 && inner[0] == '"' && inner[len(inner)-1] == '"' {
			_, innerStr, scanErr := sc.scanString([]byte(inner), 0)
			if scanErr != nil {
				return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal quoted string: %v", scanErr), newIdx, scanErr)
			}
			if copyStr {
				innerStr = strings.Clone(innerStr)
			}
			*(*string)(ptr) = innerStr
		} else {
			if sc.copyString || copyStr {
				inner = strings.Clone(inner)
			}
			*(*string)(ptr) = inner
		}

	default:
		return newIdx, newUnmarshalTypeError("string", ti.Type, newIdx)
	}

	return newIdx, nil
}

// scanPointerQuoted applies `,string` after pointer allocation/reuse.
func (sc *Parser) scanPointerQuoted(src []byte, idx int, ti *DecTypeInfo, copyStr bool, ptr unsafe.Pointer) (int, error) {
	pDec := ti.ResolvePointer()

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

	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		if pDec.ElemHasPtr {
			elemPtr = sc.ptrAlloc(pDec.ElemRType, pDec.ElemSize)
		} else {
			backing := make([]byte, pDec.ElemSize)
			elemPtr = slicePtr(backing)
		}
	}

	newIdx, err := sc.scanQuotedValue(src, idx, pDec.ElemTI, copyStr, elemPtr)
	if err != nil {
		return newIdx, err
	}

	*(*unsafe.Pointer)(ptr) = elemPtr
	return newIdx, nil
}

// resolveMapKey parses a JSON object key into typed map-key storage.
func resolveMapKey(keyStr string, keyType reflect.Type, keyTI *DecTypeInfo, keyPtr unsafe.Pointer) error {
	if keyTI.TypeFlags&typ.TypeFlagHasTextUnmarshalFn != 0 {
		return keyTI.TextUnmarshalFn(keyPtr, []byte(keyStr))
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

// scanNumberToString stores the raw text for json.Number.
func (sc *Parser) scanNumberToString(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch src[idx] {
	case '"':
		newIdx, s, err := sc.scanString(src, idx)
		if err != nil {
			return newIdx, err
		}
		*(*string)(ptr) = s
		return newIdx, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		*(*string)(ptr) = ""
		return idx + 4, nil
	default:
		end, _, numErr := scanNumberSpan(src, idx)
		if numErr != nil {
			return end, numErr
		}
		*(*string)(ptr) = string(src[idx:end])
		return end, nil
	}
}

// scanStruct is the cold-path struct decoder used by scanValueSpecial.
func (sc *Parser) scanStruct(src []byte, idx int, dec *DecStructInfo, base unsafe.Pointer) (int, error) {
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
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		} else {
			savedIdx := idx
			fieldPtr := unsafe.Add(base, fi.Offset)
			ti := fi.TypeInfo
			if fi.TagFlags&DecTagFlagSpecial != 0 {
				idx, err = sc.scanFieldTagged(src, idx, fi, ti, fieldPtr)
			} else {
				idx, err = sc.scanValue(src, idx, ti, fieldPtr)
			}
			if err != nil {
				var ute *UnmarshalTypeError
				if !errors.As(err, &ute) {
					return idx, err // syntax error → abort
				}
				if idx == savedIdx {
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
