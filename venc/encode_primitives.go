package venc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/internal/jsonfmt"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/value"
)

type UnsupportedTypeError = jerr.UnsupportedTypeError
type UnsupportedShapeError = jerr.UnsupportedShapeError
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

func (es *encodeState) encodeQuotedString(s string) {
	inner := appendEscapedString(nil, s, escapeFlags(es.flags))
	es.buf = appendEscapedString(es.buf, unsafeString(inner), escapeFlags(es.flags))
}

// fieldValueFn returns the encoder a field of type ti runs in place of
// ti.Encode when its tag options change its representation: the `format`
// encoder, or the `,string` quoting. It returns nil for a field its type
// encodes as is.
func fieldValueFn(ti *EncTypeInfo, tagFlags typ.TagFlag, format *jsonfmt.Format) EncodeFn {
	switch {
	case format != nil:
		return formatEncodeFn(format, ti)
	case tagFlags&EncTagFlagQuoted != 0:
		return quotedEncodeFn(ti)
	}
	return nil
}

// encodeFieldValue writes a struct field's value with valueFn, its
// fieldValueFn encoder, or by its type when that is nil. Every Go-side field
// emission goes through here so the fallback, interpreter, and unfold paths
// agree with the compiled one. ti.Encode is read per call: it is bound only
// once its type build completes.
func (es *encodeState) encodeFieldValue(ti *EncTypeInfo, valueFn EncodeFn, ptr unsafe.Pointer) error {
	if valueFn == nil {
		valueFn = ti.Encode
	}
	return valueFn(es, ptr)
}

// quotedEncodeFn returns the `,string` encoder of a quotable kind, which
// writes the value inside a JSON string, or nil for a kind `,string` leaves
// alone. The kind is settled here, once per field, so a quoted field costs a
// single call per value. A type that marshals itself ignores `,string`, as
// in encoding/json: its JSON or text method still runs.
func quotedEncodeFn(ti *EncTypeInfo) EncodeFn {
	if ti.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
		return nil
	}
	switch ti.Kind {
	case typ.KindBool:
		return fnEncodeQuotedBool
	case typ.KindInt:
		return fnEncodeQuotedInt
	case typ.KindInt8:
		return fnEncodeQuotedInt8
	case typ.KindInt16:
		return fnEncodeQuotedInt16
	case typ.KindInt32:
		return fnEncodeQuotedInt32
	case typ.KindInt64:
		return fnEncodeQuotedInt64
	case typ.KindUint:
		return fnEncodeQuotedUint
	case typ.KindUint8:
		return fnEncodeQuotedUint8
	case typ.KindUint16:
		return fnEncodeQuotedUint16
	case typ.KindUint32:
		return fnEncodeQuotedUint32
	case typ.KindUint64:
		return fnEncodeQuotedUint64
	case typ.KindFloat32:
		return fnEncodeQuotedFloat32
	case typ.KindFloat64:
		return fnEncodeQuotedFloat64
	case typ.KindString:
		return fnEncodeQuotedString
	case typ.KindPointer:
		// The pointee is resolved per call: a pointer type may still be under
		// construction while the struct holding it is built.
		return func(es *encodeState, ptr unsafe.Pointer) error {
			elemPtr := *(*unsafe.Pointer)(ptr)
			if elemPtr == nil {
				es.buf = append(es.buf, litNull...)
				return nil
			}
			elem := ti.ResolvePointer().ElemType
			if fn := quotedEncodeFn(elem); fn != nil {
				return fn(es, elemPtr)
			}
			return elem.Encode(es, elemPtr)
		}
	}
	return nil
}

func fnEncodeQuotedBool(es *encodeState, ptr unsafe.Pointer) error {
	if *(*bool)(ptr) {
		es.buf = append(es.buf, `"true"`...)
	} else {
		es.buf = append(es.buf, `"false"`...)
	}
	return nil
}

func fnEncodeQuotedInt(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedInt64(int64(*(*int)(ptr)))
	return nil
}

func fnEncodeQuotedInt8(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedInt64(int64(*(*int8)(ptr)))
	return nil
}

func fnEncodeQuotedInt16(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedInt64(int64(*(*int16)(ptr)))
	return nil
}

func fnEncodeQuotedInt32(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedInt64(int64(*(*int32)(ptr)))
	return nil
}

func fnEncodeQuotedInt64(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedInt64(*(*int64)(ptr))
	return nil
}

func fnEncodeQuotedUint(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedUint64(uint64(*(*uint)(ptr)))
	return nil
}

func fnEncodeQuotedUint8(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedUint64(uint64(*(*uint8)(ptr)))
	return nil
}

func fnEncodeQuotedUint16(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedUint64(uint64(*(*uint16)(ptr)))
	return nil
}

func fnEncodeQuotedUint32(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedUint64(uint64(*(*uint32)(ptr)))
	return nil
}

func fnEncodeQuotedUint64(es *encodeState, ptr unsafe.Pointer) error {
	es.appendQuotedUint64(*(*uint64)(ptr))
	return nil
}

func fnEncodeQuotedFloat32(es *encodeState, ptr unsafe.Pointer) error {
	f := float64(*(*float32)(ptr))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	es.buf = append(es.buf, '"')
	es.appendJSONFloat32(f)
	es.buf = append(es.buf, '"')
	return nil
}

func fnEncodeQuotedFloat64(es *encodeState, ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	es.buf = append(es.buf, '"')
	es.appendJSONFloat64(f)
	es.buf = append(es.buf, '"')
	return nil
}

func fnEncodeQuotedString(es *encodeState, ptr unsafe.Pointer) error {
	es.encodeQuotedString(*(*string)(ptr))
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
		if err := mi.ValType.Encode(es, elemPtr); err != nil {
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
	case value.Value:
		return es.appendTapeValue(&val)
	default:
		return es.encodeAnyReflect(v)
	}
	return nil
}

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

	// Reached through a pointer: encode in place. Otherwise the value is not
	// addressable and must be copied into an addressable slot.
	if rv.CanAddr() {
		return ti.Encode(es, rv.Addr().UnsafePointer())
	}
	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return ti.Encode(es, tmp.UnsafePointer())
}
