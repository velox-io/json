package venc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/typ"
)

type UnsupportedTypeError = jerr.UnsupportedTypeError
type UnsupportedValueError = jerr.UnsupportedValueError

var smallInts [1000]string

func init() {
	for i := range smallInts {
		smallInts[i] = strconv.Itoa(i)
	}
}

var (
	litTrue  = []byte("true")
	litFalse = []byte("false")
	litNull  = []byte("null")
	litEmpty = []byte("{}")
	litArr   = []byte("[]")
)

func (es *encodeState) appendInt64(v int64) {
	if v >= 0 && v < 1000 {
		es.buf = append(es.buf, smallInts[v]...)
		return
	}
	es.buf = strconv.AppendInt(es.buf, v, 10)
}

func (es *encodeState) appendUint64(v uint64) {
	if v < 1000 {
		es.buf = append(es.buf, smallInts[v]...)
		return
	}
	es.buf = strconv.AppendUint(es.buf, v, 10)
}

func (es *encodeState) appendQuotedInt64(v int64) {
	es.buf = append(es.buf, '"')
	es.buf = strconv.AppendInt(es.buf, v, 10)
	es.buf = append(es.buf, '"')
}

func (es *encodeState) appendQuotedUint64(v uint64) {
	es.buf = append(es.buf, '"')
	es.buf = strconv.AppendUint(es.buf, v, 10)
	es.buf = append(es.buf, '"')
}

// appendJSONFloat64 matches encoding/json float formatting when requested.
func (es *encodeState) appendJSONFloat64(f float64) {
	if es.flags&EncFloatExpAuto != 0 {
		abs := math.Abs(f)
		if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			es.buf = strconv.AppendFloat(es.buf, f, 'e', -1, 64)
			n := len(es.buf)
			if n >= 4 && es.buf[n-4] == 'e' && es.buf[n-3] == '-' && es.buf[n-2] == '0' {
				es.buf[n-2] = es.buf[n-1]
				es.buf = es.buf[:n-1]
			}
			return
		}
	}
	es.buf = strconv.AppendFloat(es.buf, f, 'f', -1, 64)
}

// appendJSONFloat32 matches encoding/json float formatting when requested.
func (es *encodeState) appendJSONFloat32(f float64) {
	if es.flags&EncFloatExpAuto != 0 {
		abs := float32(math.Abs(f))
		if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			es.buf = strconv.AppendFloat(es.buf, f, 'e', -1, 32)
			n := len(es.buf)
			if n >= 4 && es.buf[n-4] == 'e' && es.buf[n-3] == '-' && es.buf[n-2] == '0' {
				es.buf[n-2] = es.buf[n-1]
				es.buf = es.buf[:n-1]
			}
			return
		}
	}
	es.buf = strconv.AppendFloat(es.buf, f, 'f', -1, 32)
}

func (es *encodeState) encodeFloat32(ptr unsafe.Pointer) error {
	f := float64(*(*float32)(ptr))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	es.appendJSONFloat32(f)
	return nil
}

func (es *encodeState) encodeFloat64(ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	es.appendJSONFloat64(f)
	return nil
}

func (es *encodeState) encodeString(s string) {
	es.buf = appendEscapedString(es.buf, s, escapeFlags(es.flags))
}

// encodeQuotedString applies the `,string` double-encoding rule.
func (es *encodeState) encodeQuotedString(s string) {
	inner := appendEscapedString(nil, s, escapeFlags(es.flags))
	es.buf = appendEscapedString(es.buf, unsafeString(inner), escapeFlags(es.flags))
}

// encodeValueQuoted implements the `,string` field option.
func (es *encodeState) encodeValueQuoted(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case typ.KindBool:
		if *(*bool)(ptr) {
			es.buf = append(es.buf, `"true"`...)
		} else {
			es.buf = append(es.buf, `"false"`...)
		}
	case typ.KindInt:
		es.appendQuotedInt64(int64(*(*int)(ptr)))
	case typ.KindInt8:
		es.appendQuotedInt64(int64(*(*int8)(ptr)))
	case typ.KindInt16:
		es.appendQuotedInt64(int64(*(*int16)(ptr)))
	case typ.KindInt32:
		es.appendQuotedInt64(int64(*(*int32)(ptr)))
	case typ.KindInt64:
		es.appendQuotedInt64(*(*int64)(ptr))
	case typ.KindUint:
		es.appendQuotedUint64(uint64(*(*uint)(ptr)))
	case typ.KindUint8:
		es.appendQuotedUint64(uint64(*(*uint8)(ptr)))
	case typ.KindUint16:
		es.appendQuotedUint64(uint64(*(*uint16)(ptr)))
	case typ.KindUint32:
		es.appendQuotedUint64(uint64(*(*uint32)(ptr)))
	case typ.KindUint64:
		es.appendQuotedUint64(*(*uint64)(ptr))
	case typ.KindFloat32:
		f := float64(*(*float32)(ptr))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		es.buf = append(es.buf, '"')
		es.appendJSONFloat32(f)
		es.buf = append(es.buf, '"')
	case typ.KindFloat64:
		f := *(*float64)(ptr)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		es.buf = append(es.buf, '"')
		es.appendJSONFloat64(f)
		es.buf = append(es.buf, '"')
	case typ.KindString:
		es.encodeQuotedString(*(*string)(ptr))
	case typ.KindPointer:
		pi := ti.ResolvePointer()
		elemPtr := *(*unsafe.Pointer)(ptr)
		if elemPtr == nil {
			es.buf = append(es.buf, litNull...)
			return nil
		}
		return es.encodeValueQuoted(pi.ElemType, elemPtr)
	default:
		return es.encodeTop(ti, ptr)
	}
	return nil
}

func (es *encodeState) appendNewlineIndent() {
	es.buf = append(es.buf, '\n')
	es.buf = append(es.buf, es.indentPrefix...)
	for range es.indentDepth {
		es.buf = append(es.buf, es.indentString...)
	}
}

func (es *encodeState) encodeByteSlice(sh *SliceHeader) error {
	data := unsafe.Slice((*byte)(sh.Data), sh.Len)
	es.buf = append(es.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(es.buf)
	es.buf = append(es.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(es.buf[start:], data)
	es.buf = append(es.buf, '"')
	return nil
}

// encodeByteArray writes [N]byte as base64.
func (es *encodeState) encodeByteArray(ai *EncArrayInfo, ptr unsafe.Pointer) error {
	data := unsafe.Slice((*byte)(ptr), ai.ArrayLen)
	es.buf = append(es.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(es.buf)
	es.buf = append(es.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(es.buf[start:], data)
	es.buf = append(es.buf, '"')
	return nil
}

func (es *encodeState) encodeMapStringString(ptr unsafe.Pointer) error {
	mp := *(*map[string]string)(ptr)
	if mp == nil {
		es.buf = append(es.buf, litNull...)
		return nil
	}
	if len(mp) == 0 {
		es.buf = append(es.buf, litEmpty...)
		return nil
	}

	es.buf = append(es.buf, '{')
	first := true

	if es.indentString != "" {
		es.indentDepth++
	}

	for k, v := range mp {
		if !first {
			es.buf = append(es.buf, ',')
		}
		first = false

		if es.indentString != "" {
			es.appendNewlineIndent()
		}

		es.encodeString(k)
		if es.indentString != "" {
			es.buf = append(es.buf, ':', ' ')
		} else {
			es.buf = append(es.buf, ':')
		}
		es.encodeString(v)
	}

	if es.indentString != "" {
		es.indentDepth--
		es.appendNewlineIndent()
	}

	es.buf = append(es.buf, '}')
	return nil
}

// encodeMapKey writes non-string map keys without building an intermediate string.
func (es *encodeState) encodeMapKey(keyPtr unsafe.Pointer, keyTI *EncTypeInfo, keyType reflect.Type) error {
	if keyTI.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		text, err := keyTI.Hooks.TextMarshalFn(keyPtr)
		if err != nil {
			return err
		}
		es.encodeString(string(text))
		return nil
	}
	switch keyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		es.appendQuotedInt64(readIntN(keyPtr, keyType.Size()))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		es.appendQuotedUint64(readUintN(keyPtr, keyType.Size()))
		return nil
	}
	return &UnsupportedTypeError{Type: keyType}
}

func readIntN(ptr unsafe.Pointer, size uintptr) int64 {
	switch size {
	case 1:
		return int64(*(*int8)(ptr))
	case 2:
		return int64(*(*int16)(ptr))
	case 4:
		return int64(*(*int32)(ptr))
	default:
		return *(*int64)(ptr)
	}
}

func readUintN(ptr unsafe.Pointer, size uintptr) uint64 {
	switch size {
	case 1:
		return uint64(*(*uint8)(ptr))
	case 2:
		return uint64(*(*uint16)(ptr))
	case 4:
		return uint64(*(*uint32)(ptr))
	default:
		return *(*uint64)(ptr)
	}
}

func (es *encodeState) encodeMapGeneric(mi *EncMapInfo, ptr unsafe.Pointer) error {
	// ptr points to the map variable; the runtime header pointer is one level deeper.
	mp := *(*unsafe.Pointer)(ptr)
	if mp == nil {
		es.buf = append(es.buf, litNull...)
		return nil
	}
	n := maplen(mp)
	if n == 0 {
		es.buf = append(es.buf, litEmpty...)
		return nil
	}

	es.buf = append(es.buf, '{')
	first := true

	if es.indentString != "" {
		es.indentDepth++
	}

	var it mapsIter
	mapsIterInit(mi.MapRType, mp, &it)
	for mapsIterKey(&it) != nil {
		if !first {
			es.buf = append(es.buf, ',')
		}
		first = false

		if es.indentString != "" {
			es.appendNewlineIndent()
		}

		keyPtr := mapsIterKey(&it)
		if mi.IsStringKey {
			es.encodeString(*(*string)(keyPtr))
		} else if err := es.encodeMapKey(keyPtr, mi.KeyType, mi.KeyType.Type); err != nil {
			return err
		}
		if es.indentString != "" {
			es.buf = append(es.buf, ':', ' ')
		} else {
			es.buf = append(es.buf, ':')
		}

		elemPtr := mapsIterElem(&it)
		if err := es.encodeTop(mi.ValType, elemPtr); err != nil {
			return err
		}
		mapsIterNext(&it)
	}

	if es.indentString != "" {
		es.indentDepth--
		es.appendNewlineIndent()
	}

	es.buf = append(es.buf, '}')
	return nil
}

// encodeAny handles the common interface{} fast paths before reflecting.
func (es *encodeState) encodeAny(v any) error {
	if v == nil {
		es.buf = append(es.buf, litNull...)
		return nil
	}

	switch val := v.(type) {
	case string:
		es.encodeString(val)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
		}
		es.appendJSONFloat64(val)
	case bool:
		if val {
			es.buf = append(es.buf, litTrue...)
		} else {
			es.buf = append(es.buf, litFalse...)
		}
	case []any:
		return es.encodeAnySlice(val)
	case map[string]any:
		return es.encodeAnyMap(val)
	case int:
		es.appendInt64(int64(val))
	case int8:
		es.appendInt64(int64(val))
	case int16:
		es.appendInt64(int64(val))
	case int32:
		es.appendInt64(int64(val))
	case int64:
		es.appendInt64(val)
	case uint:
		es.appendUint64(uint64(val))
	case uint8:
		es.appendUint64(uint64(val))
	case uint16:
		es.appendUint64(uint64(val))
	case uint32:
		es.appendUint64(uint64(val))
	case uint64:
		es.appendUint64(val)
	case float32:
		f := float64(val)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		es.appendJSONFloat32(f)
	case []byte:
		if val == nil {
			es.buf = append(es.buf, litNull...)
		} else {
			es.buf = append(es.buf, '"')
			encodedLen := base64.StdEncoding.EncodedLen(len(val))
			start := len(es.buf)
			es.buf = append(es.buf, make([]byte, encodedLen)...)
			base64.StdEncoding.Encode(es.buf[start:], val)
			es.buf = append(es.buf, '"')
		}
	case json.Number:
		s := string(val)
		if s == "" {
			es.buf = append(es.buf, '0')
		} else {
			es.buf = append(es.buf, s...)
		}
	default:
		return es.encodeAnyReflect(v)
	}
	return nil
}

// encodeAnySlice keeps the common interface{} element types inline.
func (es *encodeState) encodeAnySlice(arr []any) error {
	if arr == nil {
		es.buf = append(es.buf, litNull...)
		return nil
	}
	if len(arr) == 0 {
		es.buf = append(es.buf, litArr...)
		return nil
	}

	es.buf = append(es.buf, '[')

	if es.indentString != "" {
		es.indentDepth++
	}

	for i, v := range arr {
		if i > 0 {
			es.buf = append(es.buf, ',')
		}
		if es.indentString != "" {
			es.appendNewlineIndent()
		}

		switch val := v.(type) {
		case string:
			es.encodeString(val)
		case float64:
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
			}
			es.appendJSONFloat64(val)
		case bool:
			if val {
				es.buf = append(es.buf, litTrue...)
			} else {
				es.buf = append(es.buf, litFalse...)
			}
		case nil:
			es.buf = append(es.buf, litNull...)
		case []any:
			if err := es.encodeAnySlice(val); err != nil {
				return err
			}
		case map[string]any:
			if err := es.encodeAnyMap(val); err != nil {
				return err
			}
		default:
			if err := es.encodeAny(v); err != nil {
				return err
			}
		}
	}

	if es.indentString != "" {
		es.indentDepth--
		es.appendNewlineIndent()
	}

	es.buf = append(es.buf, ']')
	return nil
}

// encodeAnyMap keeps the common interface{} value types inline.
func (es *encodeState) encodeAnyMap(mp map[string]any) error {
	if mp == nil {
		es.buf = append(es.buf, litNull...)
		return nil
	}
	if len(mp) == 0 {
		es.buf = append(es.buf, litEmpty...)
		return nil
	}

	es.buf = append(es.buf, '{')
	first := true

	if es.indentString != "" {
		es.indentDepth++
	}

	for k, v := range mp {
		if !first {
			es.buf = append(es.buf, ',')
		}
		first = false

		if es.indentString != "" {
			es.appendNewlineIndent()
		}

		es.encodeString(k)
		if es.indentString != "" {
			es.buf = append(es.buf, ':', ' ')
		} else {
			es.buf = append(es.buf, ':')
		}

		switch val := v.(type) {
		case string:
			es.encodeString(val)
		case float64:
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
			}
			es.appendJSONFloat64(val)
		case bool:
			if val {
				es.buf = append(es.buf, litTrue...)
			} else {
				es.buf = append(es.buf, litFalse...)
			}
		case nil:
			es.buf = append(es.buf, litNull...)
		case []any:
			if err := es.encodeAnySlice(val); err != nil {
				return err
			}
		case map[string]any:
			if err := es.encodeAnyMap(val); err != nil {
				return err
			}
		default:
			if err := es.encodeAny(v); err != nil {
				return err
			}
		}
	}

	if es.indentString != "" {
		es.indentDepth--
		es.appendNewlineIndent()
	}

	es.buf = append(es.buf, '}')
	return nil
}

func (es *encodeState) encodeAnyReflect(v any) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		es.buf = append(es.buf, litNull...)
		return nil
	}

	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			es.buf = append(es.buf, litNull...)
			return nil
		}
		rv = rv.Elem()
	}

	ti := EncTypeInfoOf(rv.Type())

	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return es.encodeTop(ti, tmp.UnsafePointer())
}
