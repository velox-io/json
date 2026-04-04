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

// vjEncFloatExpAuto matches encoding/json scientific-notation thresholds.
const vjEncFloatExpAuto uint32 = 1 << 3

// smallInts caches decimal strings for 0..999.
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

func (m *marshaler) appendInt64(v int64) error {
	if v >= 0 && v < 1000 {
		m.buf = append(m.buf, smallInts[v]...)
		return nil
	}
	m.buf = strconv.AppendInt(m.buf, v, 10)
	return nil
}

func (m *marshaler) appendUint64(v uint64) error {
	if v < 1000 {
		m.buf = append(m.buf, smallInts[v]...)
		return nil
	}
	m.buf = strconv.AppendUint(m.buf, v, 10)
	return nil
}

func (m *marshaler) appendQuotedInt64(v int64) {
	m.buf = append(m.buf, '"')
	m.buf = strconv.AppendInt(m.buf, v, 10)
	m.buf = append(m.buf, '"')
}

func (m *marshaler) appendQuotedUint64(v uint64) {
	m.buf = append(m.buf, '"')
	m.buf = strconv.AppendUint(m.buf, v, 10)
	m.buf = append(m.buf, '"')
}

// appendJSONFloat64 matches encoding/json float formatting when requested.
func (m *marshaler) appendJSONFloat64(f float64) {
	if m.flags&vjEncFloatExpAuto != 0 {
		abs := math.Abs(f)
		if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			m.buf = strconv.AppendFloat(m.buf, f, 'e', -1, 64)
			n := len(m.buf)
			if n >= 4 && m.buf[n-4] == 'e' && m.buf[n-3] == '-' && m.buf[n-2] == '0' {
				m.buf[n-2] = m.buf[n-1]
				m.buf = m.buf[:n-1]
			}
			return
		}
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 64)
}

// appendJSONFloat32 matches encoding/json float formatting when requested.
func (m *marshaler) appendJSONFloat32(f float64) {
	if m.flags&vjEncFloatExpAuto != 0 {
		abs := float32(math.Abs(f))
		if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			m.buf = strconv.AppendFloat(m.buf, f, 'e', -1, 32)
			n := len(m.buf)
			if n >= 4 && m.buf[n-4] == 'e' && m.buf[n-3] == '-' && m.buf[n-2] == '0' {
				m.buf[n-2] = m.buf[n-1]
				m.buf = m.buf[:n-1]
			}
			return
		}
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
}

func (m *marshaler) encodeFloat32(ptr unsafe.Pointer) error {
	f := float64(*(*float32)(ptr))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.appendJSONFloat32(f)
	return nil
}

func (m *marshaler) encodeFloat64(ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.appendJSONFloat64(f)
	return nil
}

func (m *marshaler) encodeString(s string) {
	m.buf = appendEscapedString(m.buf, s, escapeFlags(m.flags))
}

// encodeQuotedString applies the `,string` double-encoding rule.
func (m *marshaler) encodeQuotedString(s string) {
	inner := appendEscapedString(nil, s, escapeFlags(m.flags))
	m.buf = appendEscapedString(m.buf, unsafeString(inner), escapeFlags(m.flags))
}

// encodeValueQuoted implements the `,string` field option.
func (m *marshaler) encodeValueQuoted(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case typ.KindBool:
		if *(*bool)(ptr) {
			m.buf = append(m.buf, `"true"`...)
		} else {
			m.buf = append(m.buf, `"false"`...)
		}
	case typ.KindInt:
		m.appendQuotedInt64(int64(*(*int)(ptr)))
	case typ.KindInt8:
		m.appendQuotedInt64(int64(*(*int8)(ptr)))
	case typ.KindInt16:
		m.appendQuotedInt64(int64(*(*int16)(ptr)))
	case typ.KindInt32:
		m.appendQuotedInt64(int64(*(*int32)(ptr)))
	case typ.KindInt64:
		m.appendQuotedInt64(*(*int64)(ptr))
	case typ.KindUint:
		m.appendQuotedUint64(uint64(*(*uint)(ptr)))
	case typ.KindUint8:
		m.appendQuotedUint64(uint64(*(*uint8)(ptr)))
	case typ.KindUint16:
		m.appendQuotedUint64(uint64(*(*uint16)(ptr)))
	case typ.KindUint32:
		m.appendQuotedUint64(uint64(*(*uint32)(ptr)))
	case typ.KindUint64:
		m.appendQuotedUint64(*(*uint64)(ptr))
	case typ.KindFloat32:
		f := float64(*(*float32)(ptr))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = append(m.buf, '"')
		m.appendJSONFloat32(f)
		m.buf = append(m.buf, '"')
	case typ.KindFloat64:
		f := *(*float64)(ptr)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = append(m.buf, '"')
		m.appendJSONFloat64(f)
		m.buf = append(m.buf, '"')
	case typ.KindString:
		m.encodeQuotedString(*(*string)(ptr))
	case typ.KindPointer:
		pi := ti.ResolvePointer()
		elemPtr := *(*unsafe.Pointer)(ptr)
		if elemPtr == nil {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		return m.encodeValueQuoted(pi.ElemTI, elemPtr)
	default:
		return m.encodeTop(ti, ptr)
	}
	return nil
}

func (m *marshaler) appendNewlineIndent() {
	m.buf = append(m.buf, '\n')
	m.buf = append(m.buf, m.prefix...)
	for range m.indentDepth {
		m.buf = append(m.buf, m.indent...)
	}
}

func (m *marshaler) encodeByteSlice(sh *SliceHeader) error {
	data := unsafe.Slice((*byte)(sh.Data), sh.Len)
	m.buf = append(m.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(m.buf)
	m.buf = append(m.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(m.buf[start:], data)
	m.buf = append(m.buf, '"')
	return nil
}

// encodeByteArray writes [N]byte as base64.
func (m *marshaler) encodeByteArray(ai *EncArrayInfo, ptr unsafe.Pointer) error {
	data := unsafe.Slice((*byte)(ptr), ai.ArrayLen)
	m.buf = append(m.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(m.buf)
	m.buf = append(m.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(m.buf[start:], data)
	m.buf = append(m.buf, '"')
	return nil
}

func (m *marshaler) encodeMapStringString(ptr unsafe.Pointer) error {
	mp := *(*map[string]string)(ptr)
	if mp == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	if len(mp) == 0 {
		m.buf = append(m.buf, litEmpty...)
		return nil
	}

	m.buf = append(m.buf, '{')
	first := true

	if m.indent != "" {
		m.indentDepth++
	}

	for k, v := range mp {
		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
		}

		m.encodeString(k)
		if m.indent != "" {
			m.buf = append(m.buf, ':', ' ')
		} else {
			m.buf = append(m.buf, ':')
		}
		m.encodeString(v)
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

// encodeMapKey writes non-string map keys without building an intermediate string.
func (m *marshaler) encodeMapKey(keyPtr unsafe.Pointer, keyTI *EncTypeInfo, keyType reflect.Type) error {
	if keyTI.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		text, err := keyTI.UT.Hooks.TextMarshalFn(keyPtr)
		if err != nil {
			return err
		}
		m.encodeString(string(text))
		return nil
	}
	switch keyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		m.appendQuotedInt64(readIntN(keyPtr, keyType.Size()))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		m.appendQuotedUint64(readUintN(keyPtr, keyType.Size()))
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

func (m *marshaler) encodeMapGeneric(mi *EncMapInfo, ptr unsafe.Pointer) error {
	// ptr points to the map variable; the runtime header pointer is one level deeper.
	mp := *(*unsafe.Pointer)(ptr)
	if mp == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	n := maplen(mp)
	if n == 0 {
		m.buf = append(m.buf, litEmpty...)
		return nil
	}

	m.buf = append(m.buf, '{')
	first := true

	if m.indent != "" {
		m.indentDepth++
	}

	var it mapsIter
	mapsIterInit(mi.MapRType, mp, &it)
	for mapsIterKey(&it) != nil {
		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
		}

		keyPtr := mapsIterKey(&it)
		if mi.IsStringKey {
			m.encodeString(*(*string)(keyPtr))
		} else if err := m.encodeMapKey(keyPtr, mi.KeyTI, mi.KeyType); err != nil {
			return err
		}
		if m.indent != "" {
			m.buf = append(m.buf, ':', ' ')
		} else {
			m.buf = append(m.buf, ':')
		}

		elemPtr := mapsIterElem(&it)
		if err := m.encodeTop(mi.ValTI, elemPtr); err != nil {
			return err
		}
		mapsIterNext(&it)
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

// encodeAny handles the common interface{} fast paths before reflecting.
func (m *marshaler) encodeAny(v any) error {
	if v == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}

	switch val := v.(type) {
	case string:
		m.encodeString(val)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
		}
		m.appendJSONFloat64(val)
	case bool:
		if val {
			m.buf = append(m.buf, litTrue...)
		} else {
			m.buf = append(m.buf, litFalse...)
		}
	case []any:
		return m.encodeAnySlice(val)
	case map[string]any:
		return m.encodeAnyMap(val)
	case int:
		_ = m.appendInt64(int64(val))
	case int8:
		_ = m.appendInt64(int64(val))
	case int16:
		_ = m.appendInt64(int64(val))
	case int32:
		_ = m.appendInt64(int64(val))
	case int64:
		_ = m.appendInt64(val)
	case uint:
		_ = m.appendUint64(uint64(val))
	case uint8:
		_ = m.appendUint64(uint64(val))
	case uint16:
		_ = m.appendUint64(uint64(val))
	case uint32:
		_ = m.appendUint64(uint64(val))
	case uint64:
		_ = m.appendUint64(val)
	case float32:
		f := float64(val)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.appendJSONFloat32(f)
	case []byte:
		if val == nil {
			m.buf = append(m.buf, litNull...)
		} else {
			m.buf = append(m.buf, '"')
			encodedLen := base64.StdEncoding.EncodedLen(len(val))
			start := len(m.buf)
			m.buf = append(m.buf, make([]byte, encodedLen)...)
			base64.StdEncoding.Encode(m.buf[start:], val)
			m.buf = append(m.buf, '"')
		}
	case json.Number:
		s := string(val)
		if s == "" {
			m.buf = append(m.buf, '0')
		} else {
			m.buf = append(m.buf, s...)
		}
	default:
		return m.encodeAnyReflect(v)
	}
	return nil
}

// encodeAnySlice keeps the common interface{} element types inline.
func (m *marshaler) encodeAnySlice(arr []any) error {
	if arr == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	if len(arr) == 0 {
		m.buf = append(m.buf, litArr...)
		return nil
	}

	m.buf = append(m.buf, '[')

	if m.indent != "" {
		m.indentDepth++
	}

	for i, v := range arr {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if m.indent != "" {
			m.appendNewlineIndent()
		}

		switch val := v.(type) {
		case string:
			m.encodeString(val)
		case float64:
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
			}
			m.appendJSONFloat64(val)
		case bool:
			if val {
				m.buf = append(m.buf, litTrue...)
			} else {
				m.buf = append(m.buf, litFalse...)
			}
		case nil:
			m.buf = append(m.buf, litNull...)
		case []any:
			if err := m.encodeAnySlice(val); err != nil {
				return err
			}
		case map[string]any:
			if err := m.encodeAnyMap(val); err != nil {
				return err
			}
		default:
			if err := m.encodeAny(v); err != nil {
				return err
			}
		}
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, ']')
	return nil
}

// encodeAnyMap keeps the common interface{} value types inline.
func (m *marshaler) encodeAnyMap(mp map[string]any) error {
	if mp == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	if len(mp) == 0 {
		m.buf = append(m.buf, litEmpty...)
		return nil
	}

	m.buf = append(m.buf, '{')
	first := true

	if m.indent != "" {
		m.indentDepth++
	}

	for k, v := range mp {
		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
		}

		m.encodeString(k)
		if m.indent != "" {
			m.buf = append(m.buf, ':', ' ')
		} else {
			m.buf = append(m.buf, ':')
		}

		switch val := v.(type) {
		case string:
			m.encodeString(val)
		case float64:
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return &UnsupportedValueError{Str: strconv.FormatFloat(val, 'g', -1, 64)}
			}
			m.appendJSONFloat64(val)
		case bool:
			if val {
				m.buf = append(m.buf, litTrue...)
			} else {
				m.buf = append(m.buf, litFalse...)
			}
		case nil:
			m.buf = append(m.buf, litNull...)
		case []any:
			if err := m.encodeAnySlice(val); err != nil {
				return err
			}
		case map[string]any:
			if err := m.encodeAnyMap(val); err != nil {
				return err
			}
		default:
			if err := m.encodeAny(v); err != nil {
				return err
			}
		}
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

func (m *marshaler) encodeAnyReflect(v any) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		m.buf = append(m.buf, litNull...)
		return nil
	}

	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		rv = rv.Elem()
	}

	ti := EncTypeInfoOf(rv.Type())

	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return m.encodeTop(ti, tmp.UnsafePointer())
}


// computeHintBytes returns a one-time static output-size estimate.
func computeHintBytes(ti *EncTypeInfo, depth int) int {
	if depth > 8 {
		return 32 // cap recursive hint growth
	}
	if ti.TypeFlags&EncTypeFlagHasMarshalFn != 0 || ti.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		return 64
	}
	switch ti.Kind {
	case typ.KindBool:
		return 5
	case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64:
		return 12
	case typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
		return 12
	case typ.KindFloat32, typ.KindFloat64:
		return 20
	case typ.KindString:
		return 32
	case typ.KindStruct:
		si := ti.ResolveStruct()
		if si == nil {
			return 64
		}
		n := 2
		for i := range si.Fields {
			fi := &si.Fields[i]
			n += len(fi.KeyBytes) + 1
			n += computeHintBytes(fi, depth+1)
		}
		return n
	case typ.KindSlice:
		si := ti.ResolveSlice()
		if si == nil {
			return 64
		}
		elemHint := computeHintBytes(si.ElemTI, depth+1)
		return 2 + 4*(elemHint+1)
	case typ.KindPointer:
		pi := ti.ResolvePointer()
		if pi == nil {
			return 64
		}
		return computeHintBytes(pi.ElemTI, depth+1)
	case typ.KindMap:
		return 128
	case typ.KindRawMessage:
		return 64
	case typ.KindNumber:
		return 12
	case typ.KindAny:
		return 64
	default:
		return 32
	}
}
