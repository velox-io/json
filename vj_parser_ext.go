package vjson

// vj_parser_ext.go contains cold-path parser functions that handle
// special field tags (`,string`), custom unmarshalers (json.Unmarshaler,
// encoding.TextUnmarshaler), and non-string map keys.
//
// These functions are separated from vj_parser.go to reduce icache
// pressure on the hot unmarshal path. See docs/perf-regression-analysis.md.

import (
	"fmt"
	"reflect"
	"strconv"
	"unsafe"
)

// scanValueSpecial handles fields with non-zero Flags (Quoted, UnmarshalFn,
// TextUnmarshalFn). Called only when ti.Flags != 0, keeping scanValue lean.
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
	switch src[idx] {
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
		return sc.scanStringValue(src, idx, ti, ptr)
	case '{':
		return sc.scanObjectValue(src, idx, ti, ptr)
	case '[':
		return sc.scanArrayValue(src, idx, ti, ptr)
	case 't':
		return sc.scanTrue(src, idx, ti, ptr)
	case 'f':
		return sc.scanFalse(src, idx, ti, ptr)
	case 'n':
		return sc.scanNull(src, idx, ti, ptr)
	default:
		if (src[idx] >= '0' && src[idx] <= '9') || src[idx] == '-' {
			return sc.scanNumber(src, idx, ti, ptr)
		}
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
		WriteIntValue(ptr, ti.Kind, v)

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
		WriteUintValue(ptr, ti.Kind, v)

	case KindFloat32:
		v, parseErr := strconv.ParseFloat(inner, 32)
		if parseErr != nil {
			return newIdx, newSyntaxErrorWrap(fmt.Sprintf("vjson: cannot unmarshal string %q into float32: %v", inner, parseErr), newIdx, parseErr)
		}
		*(*float32)(ptr) = float32(v)

	case KindFloat64:
		v, parseErr := strconv.ParseFloat(inner, 64)
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
			*(*string)(ptr) = innerStr
		} else {
			// encoding/json also accepts bare unquoted content for string,string fields.
			*(*string)(ptr) = inner
		}

	default:
		return newIdx, newUnmarshalTypeError("string", ti.Ext.Type, newIdx)
	}

	return newIdx, nil
}

// scanPointerQuoted handles pointer fields with the `,string` tag.
func (sc *Parser) scanPointerQuoted(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	pDec := ti.Codec.(*PointerCodec)

	idx = skipWS(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	if src[idx] == 'n' {
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		*(*unsafe.Pointer)(ptr) = nil
		return idx + 4, nil
	}

	var elemPtr unsafe.Pointer
	if pDec.ElemHasPtr {
		elemPtr = sc.ptrAlloc(pDec.ElemRType, pDec.ElemSize)
	} else {
		backing := make([]byte, pDec.ElemSize)
		elemPtr = unsafe.Pointer(&backing[0])
	}

	// Shallow copy to propagate Quoted flag to the element.
	elemTI := *pDec.ElemTI
	elemTI.Flags |= tiFlagQuoted
	newIdx, err := sc.scanValue(src, idx, &elemTI, elemPtr)
	if err != nil {
		return newIdx, err
	}

	*(*unsafe.Pointer)(ptr) = elemPtr
	return newIdx, nil
}

// resolveMapKeyValue converts a JSON string key to a reflect.Value of the given key type.
func resolveMapKeyValue(keyStr string, keyType reflect.Type, keyTI *TypeInfo) (reflect.Value, error) {
	if keyType.Kind() == reflect.String {
		return reflect.ValueOf(keyStr).Convert(keyType), nil
	}
	if keyTI.Flags&tiFlagHasTextUnmarshalFn != 0 {
		kv := reflect.New(keyType)
		if err := keyTI.Ext.TextUnmarshalFn(kv.UnsafePointer(), []byte(keyStr)); err != nil {
			return reflect.Value{}, err
		}
		return kv.Elem(), nil
	}
	switch keyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(keyStr, 10, 64)
		if err != nil {
			return reflect.Value{}, newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid map key %q: %v", keyStr, err), 0, err)
		}
		return reflect.ValueOf(n).Convert(keyType), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(keyStr, 10, 64)
		if err != nil {
			return reflect.Value{}, newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid map key %q: %v", keyStr, err), 0, err)
		}
		return reflect.ValueOf(n).Convert(keyType), nil
	}
	return reflect.Value{}, newSyntaxError(fmt.Sprintf("vjson: unsupported map key type: %v", keyType), 0)
}

// scanNumberToString handles a json.Number field: stores the raw number text
// as a string. Also accepts quoted strings ("123") and null.
func (sc *Parser) scanNumberToString(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch {
	case src[idx] == '"':
		// Quoted number: "123" → json.Number("123")
		newIdx, s, err := sc.scanString(src, idx)
		if err != nil {
			return newIdx, err
		}
		*(*string)(ptr) = s
		return newIdx, nil
	case src[idx] == 'n':
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
