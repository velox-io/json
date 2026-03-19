package vjson

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"unsafe"
)

// Two-layer dispatch design:
//   Hot path — bindEncodeFn pre-binds EncodeFn per TypeInfo; encodeValue calls it directly.
//   Cold path — encodeValueSlow handles dynamically-obtained TypeInfo (e.g. encodeAnyReflect),

// Pre-computed string representations for 0-999.
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

// Primitive encoding

func (m *Marshaler) appendInt64(v int64) error {
	if v >= 0 && v < 1000 {
		m.buf = append(m.buf, smallInts[v]...)
		return nil
	}
	m.buf = strconv.AppendInt(m.buf, v, 10)
	return nil
}

func (m *Marshaler) appendUint64(v uint64) error {
	if v < 1000 {
		m.buf = append(m.buf, smallInts[v]...)
		return nil
	}
	m.buf = strconv.AppendUint(m.buf, v, 10)
	return nil
}

func (m *Marshaler) appendQuotedInt64(v int64) {
	m.buf = append(m.buf, '"')
	m.buf = strconv.AppendInt(m.buf, v, 10)
	m.buf = append(m.buf, '"')
}

func (m *Marshaler) appendQuotedUint64(v uint64) {
	m.buf = append(m.buf, '"')
	m.buf = strconv.AppendUint(m.buf, v, 10)
	m.buf = append(m.buf, '"')
}

// appendJSONFloat64 appends a float64. When vjEncFloatExpAuto is set, uses
// encoding/json format: scientific notation for |f| < 1e-6 or |f| >= 1e21.
// Otherwise uses fixed-point notation.
func (m *Marshaler) appendJSONFloat64(f float64) {
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

// appendJSONFloat32 appends a float32. When vjEncFloatExpAuto is set, uses
// encoding/json format: scientific notation for |f| < 1e-6 or |f| >= 1e21.
// Otherwise uses fixed-point notation.
func (m *Marshaler) appendJSONFloat32(f float64) {
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

func (m *Marshaler) encodeFloat32(ptr unsafe.Pointer) error {
	f := float64(*(*float32)(ptr))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.appendJSONFloat32(f)
	return nil
}

func (m *Marshaler) encodeFloat64(ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.appendJSONFloat64(f)
	return nil
}

func (m *Marshaler) encodeString(s string) {
	m.buf = appendEscapedString(m.buf, s, escapeFlags(m.flags))
}

// encodeQuotedString double-encodes a string: Go "hello" → JSON "\"hello\"".
func (m *Marshaler) encodeQuotedString(s string) {
	inner := appendEscapedString(nil, s, escapeFlags(m.flags))
	m.buf = appendEscapedString(m.buf, unsafeString(inner), escapeFlags(m.flags))
}

// EncodeFn wrappers — package-level functions satisfying the EncodeFn signature,
// assigned to TypeInfo.EncodeFn by bindEncodeFn during codec construction.
// encodeValue calls them directly via function pointer (hot path).

func encodeBool(m *Marshaler, ptr unsafe.Pointer) error {
	if *(*bool)(ptr) {
		m.buf = append(m.buf, litTrue...)
	} else {
		m.buf = append(m.buf, litFalse...)
	}
	return nil
}

func encodeInt(m *Marshaler, ptr unsafe.Pointer) error     { return m.appendInt64(int64(*(*int)(ptr))) }
func encodeInt8(m *Marshaler, ptr unsafe.Pointer) error    { return m.appendInt64(int64(*(*int8)(ptr))) }
func encodeInt16(m *Marshaler, ptr unsafe.Pointer) error   { return m.appendInt64(int64(*(*int16)(ptr))) }
func encodeInt32(m *Marshaler, ptr unsafe.Pointer) error   { return m.appendInt64(int64(*(*int32)(ptr))) }
func encodeInt64Fn(m *Marshaler, ptr unsafe.Pointer) error { return m.appendInt64(*(*int64)(ptr)) }

func encodeUint(m *Marshaler, ptr unsafe.Pointer) error {
	return m.appendUint64(uint64(*(*uint)(ptr)))
}
func encodeUint8(m *Marshaler, ptr unsafe.Pointer) error {
	return m.appendUint64(uint64(*(*uint8)(ptr)))
}
func encodeUint16(m *Marshaler, ptr unsafe.Pointer) error {
	return m.appendUint64(uint64(*(*uint16)(ptr)))
}
func encodeUint32(m *Marshaler, ptr unsafe.Pointer) error {
	return m.appendUint64(uint64(*(*uint32)(ptr)))
}
func encodeUint64Fn(m *Marshaler, ptr unsafe.Pointer) error {
	return m.appendUint64(*(*uint64)(ptr))
}

func encodeFloat32Value(m *Marshaler, ptr unsafe.Pointer) error { return m.encodeFloat32(ptr) }
func encodeFloat64Value(m *Marshaler, ptr unsafe.Pointer) error { return m.encodeFloat64(ptr) }

func encodeStringValue(m *Marshaler, ptr unsafe.Pointer) error {
	m.encodeString(*(*string)(ptr))
	return nil
}

func encodeRawMessageFn(m *Marshaler, ptr unsafe.Pointer) error {
	raw := *(*[]byte)(ptr)
	if len(raw) == 0 {
		m.buf = append(m.buf, litNull...)
	} else {
		m.buf = append(m.buf, raw...)
	}
	return nil
}

func encodeNumberFn(m *Marshaler, ptr unsafe.Pointer) error {
	s := *(*string)(ptr)
	if s == "" {
		m.buf = append(m.buf, '0')
	} else {
		m.buf = append(m.buf, s...)
	}
	return nil
}

func encodeAnyValue(m *Marshaler, ptr unsafe.Pointer) error { return m.encodeAny(*(*any)(ptr)) }

// Closure builders for composite types.

func makeEncodeStruct(dec *StructCodec) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		return m.encodeStruct(dec, ptr)
	}
}

func makeEncodeSlice(dec *SliceCodec) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		return m.encodeSlice(dec, ptr)
	}
}

func makeEncodeArray(dec *ArrayCodec) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		return m.encodeArray(dec, ptr)
	}
}

func makeEncodePointer(dec *PointerCodec) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		return m.encodePointer(dec, ptr)
	}
}

func makeEncodeMap(dec *MapCodec) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		return m.encodeMap(dec, ptr)
	}
}

// Closure builders for custom marshalers.

func makeEncodeMarshalJSON(marshalFn func(ptr unsafe.Pointer) ([]byte, error)) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		data, err := marshalFn(ptr)
		if err != nil {
			return err
		}
		m.buf = append(m.buf, data...)
		return nil
	}
}

func makeEncodeTextMarshal(textMarshalFn func(ptr unsafe.Pointer) ([]byte, error)) func(m *Marshaler, ptr unsafe.Pointer) error {
	return func(m *Marshaler, ptr unsafe.Pointer) error {
		text, err := textMarshalFn(ptr)
		if err != nil {
			return err
		}
		m.encodeString(string(text))
		return nil
	}
}

// encodeValueSlow is the cold-path fallback for dynamically-obtained TypeInfo
// (e.g. encodeAnyReflect) where EncodeFn was not pre-bound at codec construction.
// It delegates to the same per-Kind EncodeFn wrappers used by the hot path,
// so all actual encoding logic appears only once.
func (m *Marshaler) encodeValueSlow(ti *TypeInfo, ptr unsafe.Pointer) error {
	if ti.Flags&tiFlagHasMarshalFn != 0 {
		data, err := ti.Ext.MarshalFn(ptr)
		if err != nil {
			return err
		}
		m.buf = append(m.buf, data...)
		return nil
	}
	if ti.Flags&tiFlagHasTextMarshalFn != 0 {
		text, err := ti.Ext.TextMarshalFn(ptr)
		if err != nil {
			return err
		}
		m.encodeString(string(text))
		return nil
	}
	switch ti.Kind {
	case KindBool:
		return encodeBool(m, ptr)
	case KindInt:
		return encodeInt(m, ptr)
	case KindInt8:
		return encodeInt8(m, ptr)
	case KindInt16:
		return encodeInt16(m, ptr)
	case KindInt32:
		return encodeInt32(m, ptr)
	case KindInt64:
		return encodeInt64Fn(m, ptr)
	case KindUint:
		return encodeUint(m, ptr)
	case KindUint8:
		return encodeUint8(m, ptr)
	case KindUint16:
		return encodeUint16(m, ptr)
	case KindUint32:
		return encodeUint32(m, ptr)
	case KindUint64:
		return encodeUint64Fn(m, ptr)
	case KindFloat32:
		return encodeFloat32Value(m, ptr)
	case KindFloat64:
		return encodeFloat64Value(m, ptr)
	case KindString:
		return encodeStringValue(m, ptr)
	case KindStruct:
		return m.encodeStruct(ti.resolveCodec().(*StructCodec), ptr)
	case KindSlice:
		return m.encodeSlice(ti.resolveCodec().(*SliceCodec), ptr)
	case KindArray:
		return m.encodeArray(ti.resolveCodec().(*ArrayCodec), ptr)
	case KindPointer:
		return m.encodePointer(ti.resolveCodec().(*PointerCodec), ptr)
	case KindMap:
		return m.encodeMap(ti.resolveCodec().(*MapCodec), ptr)
	case KindRawMessage:
		return encodeRawMessageFn(m, ptr)
	case KindNumber:
		return encodeNumberFn(m, ptr)
	case KindAny:
		return m.encodeAny(*(*any)(ptr))
	case KindIface:
		return m.encodeIface(ti, ptr)
	default:
		return &UnsupportedTypeError{Type: ti.Ext.Type}
	}
}

// encodeValueQuoted encodes a value wrapped in a JSON string (for `,string` tag).
func (m *Marshaler) encodeValueQuoted(ti *TypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case KindBool:
		if *(*bool)(ptr) {
			m.buf = append(m.buf, `"true"`...)
		} else {
			m.buf = append(m.buf, `"false"`...)
		}
	case KindInt:
		m.appendQuotedInt64(int64(*(*int)(ptr)))
	case KindInt8:
		m.appendQuotedInt64(int64(*(*int8)(ptr)))
	case KindInt16:
		m.appendQuotedInt64(int64(*(*int16)(ptr)))
	case KindInt32:
		m.appendQuotedInt64(int64(*(*int32)(ptr)))
	case KindInt64:
		m.appendQuotedInt64(*(*int64)(ptr))
	case KindUint:
		m.appendQuotedUint64(uint64(*(*uint)(ptr)))
	case KindUint8:
		m.appendQuotedUint64(uint64(*(*uint8)(ptr)))
	case KindUint16:
		m.appendQuotedUint64(uint64(*(*uint16)(ptr)))
	case KindUint32:
		m.appendQuotedUint64(uint64(*(*uint32)(ptr)))
	case KindUint64:
		m.appendQuotedUint64(*(*uint64)(ptr))
	case KindFloat32:
		f := float64(*(*float32)(ptr))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = append(m.buf, '"')
		m.appendJSONFloat32(f)
		m.buf = append(m.buf, '"')
	case KindFloat64:
		f := *(*float64)(ptr)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = append(m.buf, '"')
		m.appendJSONFloat64(f)
		m.buf = append(m.buf, '"')
	case KindString:
		m.encodeQuotedString(*(*string)(ptr))
	case KindPointer:
		dec := ti.resolveCodec().(*PointerCodec)
		elemPtr := *(*unsafe.Pointer)(ptr)
		if elemPtr == nil {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		return m.encodeValueQuoted(dec.ElemTI, elemPtr)
	default:
		return m.encodeValue(ti, ptr)
	}
	return nil
}

func (m *Marshaler) appendNewlineIndent() {
	m.buf = append(m.buf, '\n')
	m.buf = append(m.buf, m.prefix...)
	for range m.indentDepth {
		m.buf = append(m.buf, m.indent...)
	}
}

// Struct encoding (Go path)

// encodeStructGo encodes a struct using the pure-Go encoder.
// This is the fallback path for structs with features
// not supported by the C engine (omitempty, custom marshalers, etc.).
func (m *Marshaler) encodeStructGo(dec *StructCodec, base unsafe.Pointer) error {
	m.buf = append(m.buf, '{')
	first := true
	for _, step := range dec.EncodeSteps {
		var err error
		first, err = step(m, base, first)
		if err != nil {
			return err
		}
	}
	m.buf = append(m.buf, '}')
	return nil
}

func (m *Marshaler) encodeStructIndent(dec *StructCodec, base unsafe.Pointer) error {
	m.buf = append(m.buf, '{')
	first := true
	m.indentDepth++

	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldPtr := unsafe.Add(base, fi.Offset)

		if fi.Flags&tiFlagOmitEmpty != 0 && fi.Ext.IsZeroFn != nil && fi.Ext.IsZeroFn(fieldPtr) {
			continue
		}

		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		m.appendNewlineIndent()
		m.buf = append(m.buf, fi.Ext.KeyBytesIndent...)

		if fi.Flags&tiFlagQuoted != 0 {
			if err := m.encodeValueQuoted(fi, fieldPtr); err != nil {
				return err
			}
		} else if err := m.encodeValue(fi, fieldPtr); err != nil {
			return err
		}
	}

	m.indentDepth--
	if !first {
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

// Slice / Array encoding (Go path)

// encodeSliceGo is the pure-Go fallback for slice encoding.
func (m *Marshaler) encodeSliceGo(dec *SliceCodec, ptr unsafe.Pointer) error {
	sh := (*SliceHeader)(ptr)

	if sh.Len == 0 {
		m.buf = append(m.buf, litArr...)
		return nil
	}

	m.buf = append(m.buf, '[')
	elemSize := dec.ElemSize

	if m.indent != "" {
		m.indentDepth++
	}

	for i := range sh.Len {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if m.indent != "" {
			m.appendNewlineIndent()
		}

		elemPtr := unsafe.Add(sh.Data, uintptr(i)*elemSize)
		if err := m.encodeValue(dec.ElemTI, elemPtr); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, ']')
	return nil
}

func (m *Marshaler) encodeByteSlice(sh *SliceHeader) error {
	data := unsafe.Slice((*byte)(sh.Data), sh.Len)
	m.buf = append(m.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(m.buf)
	m.buf = append(m.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(m.buf[start:], data)
	m.buf = append(m.buf, '"')
	return nil
}

// encodeArrayGo is the pure-Go fallback for fixed-size array encoding.
func (m *Marshaler) encodeArrayGo(dec *ArrayCodec, ptr unsafe.Pointer) error {
	if dec.ArrayLen == 0 {
		m.buf = append(m.buf, litArr...)
		return nil
	}

	m.buf = append(m.buf, '[')
	elemSize := dec.ElemSize

	if m.indent != "" {
		m.indentDepth++
	}

	for i := range dec.ArrayLen {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if m.indent != "" {
			m.appendNewlineIndent()
		}

		elemPtr := unsafe.Add(ptr, uintptr(i)*elemSize)
		if err := m.encodeValue(dec.ElemTI, elemPtr); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.indentDepth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, ']')
	return nil
}

// encodeByteArray encodes [N]byte as a base64 JSON string.
func (m *Marshaler) encodeByteArray(dec *ArrayCodec, ptr unsafe.Pointer) error {
	data := unsafe.Slice((*byte)(ptr), dec.ArrayLen)
	m.buf = append(m.buf, '"')
	encodedLen := base64.StdEncoding.EncodedLen(len(data))
	start := len(m.buf)
	m.buf = append(m.buf, make([]byte, encodedLen)...)
	base64.StdEncoding.Encode(m.buf[start:], data)
	m.buf = append(m.buf, '"')
	return nil
}

// Pointer encoding

func (m *Marshaler) encodePointer(dec *PointerCodec, ptr unsafe.Pointer) error {
	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	return m.encodeValue(dec.ElemTI, elemPtr)
}

// Map encoding (Go path)

func (m *Marshaler) encodeMapStringString(ptr unsafe.Pointer) error {
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

// encodeMapKey encodes a non-string map key directly into m.buf as a quoted
// JSON string, avoiding the intermediate string allocation that
// resolveMapKeyPtr + encodeString would incur for integer keys.
func (m *Marshaler) encodeMapKey(keyPtr unsafe.Pointer, keyTI *TypeInfo, keyType reflect.Type) error {
	if keyTI.Flags&tiFlagHasTextMarshalFn != 0 {
		text, err := keyTI.Ext.TextMarshalFn(keyPtr)
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

// readIntN reads a signed integer of the given byte width from ptr.
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

// readUintN reads an unsigned integer of the given byte width from ptr.
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

func (m *Marshaler) encodeMapGeneric(dec *MapCodec, ptr unsafe.Pointer) error {
	// ptr is a pointer to the map variable, which itself is a pointer to the map header.
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
	mapsIterInit(dec.mapRType, mp, &it)
	for mapsIterKey(&it) != nil {
		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
		}

		keyPtr := mapsIterKey(&it)
		if dec.isStringKey {
			m.encodeString(*(*string)(keyPtr))
		} else if err := m.encodeMapKey(keyPtr, dec.KeyTI, dec.KeyType); err != nil {
			return err
		}
		if m.indent != "" {
			m.buf = append(m.buf, ':', ' ')
		} else {
			m.buf = append(m.buf, ':')
		}

		elemPtr := mapsIterElem(&it)
		if err := m.encodeValue(dec.ValTI, elemPtr); err != nil {
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

// any / interface encoding

// encodeIface encodes a non-empty interface field (e.g. fmt.Stringer).
// It extracts the dynamic value via reflect and marshals it, matching
// encoding/json behavior.
func (m *Marshaler) encodeIface(ti *TypeInfo, ptr unsafe.Pointer) error {
	rv := reflect.NewAt(ti.Ext.Type, ptr).Elem()
	if rv.IsNil() {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	// Unwrap the interface to get the dynamic value, then marshal via encodeAnyReflect.
	return m.encodeAnyReflect(rv.Elem().Interface())
}

// encodeAny encodes an arbitrary Go value stored in an interface{}.
// Hot types (string, float64, bool, []any, map[string]any) are handled
// inline; numeric types, []byte, json.Number follow; everything else
// falls back to encodeAnyReflect.
func (m *Marshaler) encodeAny(v any) error {
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

// encodeAnySlice encodes a []any as a JSON array.
// Hot types are inlined for icache locality; cold types fall through to encodeAny.
func (m *Marshaler) encodeAnySlice(arr []any) error {
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

		// Inline fast-path for the most common JSON value types.
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

// encodeAnyMap encodes a map[string]any as a JSON object.
// Hot types are inlined for icache locality; cold types fall through to encodeAny.
func (m *Marshaler) encodeAnyMap(mp map[string]any) error {
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

		// Inline fast-path for the most common JSON value types.
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

// encodeAnyReflect is the reflect fallback for non-standard any values.
func (m *Marshaler) encodeAnyReflect(v any) error {
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

	ti := getCodec(rv.Type())

	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return m.encodeValue(ti, tmp.UnsafePointer())
}
