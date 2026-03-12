package vjson

import (
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync"
	"unsafe"
)

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

const (
	marshalBufInitSize = 4096
	marshalBufMaxPool  = 64 * 1024
)

// MarshalOption configures encoding behavior.
type MarshalOption func(*Marshaler)

// WithEscapeHTML enables escaping of <, >, & in JSON strings.
func WithEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags |= escapeHTML }
}

// WithNoEscapeHTML disables escaping of <, >, &.
func WithNoEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags &^= escapeHTML }
}

// WithStdCompat enables full encoding/json compatibility.
func WithStdCompat() MarshalOption {
	return func(m *Marshaler) { m.flags = escapeStdCompat }
}

// Marshaler is a pooled JSON encoder.
type Marshaler struct {
	buf    []byte
	indent string
	prefix string
	depth  int
	flags  escapeFlags
}

var marshalerPool = sync.Pool{
	New: func() any {
		return &Marshaler{
			buf: make([]byte, 0, marshalBufInitSize),
		}
	},
}

func init() {
	marshalerPool.Put(&Marshaler{buf: make([]byte, 0, marshalBufInitSize)})
}

func getMarshaler() *Marshaler {
	m := marshalerPool.Get().(*Marshaler)
	m.buf = m.buf[:0]
	m.indent = ""
	m.prefix = ""
	m.depth = 0
	m.flags = escapeDefault
	return m
}

func putMarshaler(m *Marshaler) {
	if cap(m.buf) <= marshalBufMaxPool {
		marshalerPool.Put(m)
	}
}

// estimateDataSize pre-scans data to estimate JSON output size.
func estimateDataSize(ti *TypeInfo, ptr unsafe.Pointer) int {
	switch ti.Kind {
	case KindBool:
		return 5 // "false"
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		return 20 // max int64 is 19 digits + sign
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		return 20
	case KindFloat32, KindFloat64:
		return 24
	case KindString:
		s := *(*string)(ptr)
		return len(s) + 2 + len(s)/8 + 1
	case KindStruct:
		return estimateStructDataSize(ti.Decoder.(*ReflectStructDecoder), ptr)
	case KindSlice:
		return estimateSliceDataSize(ti.Decoder.(*ReflectSliceDecoder), ptr)
	case KindPointer:
		dec := ti.Decoder.(*ReflectPointerDecoder)
		elemPtr := *(*unsafe.Pointer)(ptr)
		if elemPtr == nil {
			return 4 // "null"
		}
		return estimateDataSize(dec.ElemTI, elemPtr)
	case KindMap:
		return estimateMapDataSize(ti.Decoder.(*ReflectMapDecoder), ptr)
	case KindAny:
		v := *(*any)(ptr)
		if v == nil {
			return 4
		}
		return 64
	default:
		return 32
	}
}

func estimateStructDataSize(dec *ReflectStructDecoder, base unsafe.Pointer) int {
	n := 2 // { }
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldPtr := unsafe.Add(base, fi.Offset)

		if fi.OmitEmpty && fi.IsZeroFn != nil && fi.IsZeroFn(fieldPtr) {
			continue
		}

		n += len(fi.KeyBytes)
		n += estimateDataSize(fi, fieldPtr)
		n++ // comma
	}
	return n
}

func estimateSliceDataSize(dec *ReflectSliceDecoder, ptr unsafe.Pointer) int {
	sh := (*SliceHeader)(ptr)
	if sh.Data == nil || sh.Len == 0 {
		return 2 // []
	}

	if dec.ElemTI.Kind == KindUint8 && dec.ElemSize == 1 {
		return int(sh.Len)*4/3 + 4 + 2
	}

	n := 2 // [ ]
	elemSize := dec.ElemSize
	count := int(sh.Len)

	switch dec.ElemTI.Kind {
	case KindBool:
		n += count * 6 // "false,"
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		n += count * 12
	case KindFloat32, KindFloat64:
		n += count * 16
	case KindString:
		// Walk each string to get actual lengths
		for i := range count {
			elemPtr := unsafe.Add(sh.Data, uintptr(i)*elemSize)
			s := *(*string)(elemPtr)
			n += len(s) + 2 + len(s)/8 + 2 // quotes + escape margin + comma
		}
	default:
		// Complex elements: scan each one
		for i := range count {
			elemPtr := unsafe.Add(sh.Data, uintptr(i)*elemSize)
			n += estimateDataSize(dec.ElemTI, elemPtr) + 1 // +1 for comma
		}
	}
	return n
}

func estimateMapDataSize(dec *ReflectMapDecoder, ptr unsafe.Pointer) int {
	mapPtr := reflect.NewAt(dec.MapType, ptr)
	mapVal := mapPtr.Elem()
	if mapVal.IsNil() {
		return 4 // null
	}
	l := mapVal.Len()
	if l == 0 {
		return 2 // {}
	}
	return 2 + l*48
}

// Marshal returns the compact JSON encoding of *v.
func Marshal[T any](v *T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	ti := GetDecoder(reflect.TypeFor[T]())

	hint := estimateDataSize(ti, unsafe.Pointer(v))
	if hint > cap(m.buf) {
		m.buf = make([]byte, 0, hint)
	}

	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		putMarshaler(m)
		return nil, err
	}

	return m.finalize(), nil
}

// MarshalIndent returns the indented JSON encoding of *v.
func MarshalIndent[T any](v *T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	m.prefix = prefix
	m.indent = indent

	ti := GetDecoder(reflect.TypeFor[T]())
	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		putMarshaler(m)
		return nil, err
	}

	return m.finalize(), nil
}

// AppendMarshal appends the compact JSON encoding of *v to dst.
func AppendMarshal[T any](dst []byte, v *T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}
	defer putMarshaler(m)

	m.buf = dst

	ti := GetDecoder(reflect.TypeFor[T]())
	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		return dst, err
	}

	return m.buf, nil
}

// finalize returns the encoded JSON as a standalone byte slice.
// For poolable buffers, it copies the data out so the Marshaler's buf can be
// reused by the pool without the caller's reference aliasing it.
func (m *Marshaler) finalize() []byte {
	if cap(m.buf) <= marshalBufMaxPool {
		result := make([]byte, len(m.buf))
		copy(result, m.buf)
		putMarshaler(m)
		return result
	}
	result := m.buf[:len(m.buf):len(m.buf)]
	m.buf = nil
	putMarshaler(m)
	return result
}

func (m *Marshaler) appendNewlineIndent() {
	m.buf = append(m.buf, '\n')
	m.buf = append(m.buf, m.prefix...)
	for range m.depth {
		m.buf = append(m.buf, m.indent...)
	}
}

func (m *Marshaler) encodeValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case KindBool:
		if *(*bool)(ptr) {
			m.buf = append(m.buf, litTrue...)
		} else {
			m.buf = append(m.buf, litFalse...)
		}
		return nil

	case KindInt:
		return m.appendInt64(int64(*(*int)(ptr)))
	case KindInt8:
		return m.appendInt64(int64(*(*int8)(ptr)))
	case KindInt16:
		return m.appendInt64(int64(*(*int16)(ptr)))
	case KindInt32:
		return m.appendInt64(int64(*(*int32)(ptr)))
	case KindInt64:
		return m.appendInt64(*(*int64)(ptr))

	case KindUint:
		return m.appendUint64(uint64(*(*uint)(ptr)))
	case KindUint8:
		return m.appendUint64(uint64(*(*uint8)(ptr)))
	case KindUint16:
		return m.appendUint64(uint64(*(*uint16)(ptr)))
	case KindUint32:
		return m.appendUint64(uint64(*(*uint32)(ptr)))
	case KindUint64:
		return m.appendUint64(*(*uint64)(ptr))

	case KindFloat32:
		return m.encodeFloat32(ptr)
	case KindFloat64:
		return m.encodeFloat64(ptr)

	case KindString:
		m.encodeString(*(*string)(ptr))
		return nil

	case KindStruct:
		return m.encodeStruct(ti.Decoder.(*ReflectStructDecoder), ptr)
	case KindSlice:
		return m.encodeSlice(ti.Decoder.(*ReflectSliceDecoder), ptr)
	case KindPointer:
		return m.encodePointer(ti.Decoder.(*ReflectPointerDecoder), ptr)
	case KindMap:
		return m.encodeMap(ti.Decoder.(*ReflectMapDecoder), ptr)
	case KindAny:
		return m.encodeAny(ptr)

	default:
		return fmt.Errorf("vjson: unsupported type kind %v for marshal", ti.Kind)
	}
}

func (m *Marshaler) encodeString(s string) {
	m.buf = appendEscapedString(m.buf, s, m.flags)
}

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

func (m *Marshaler) encodeFloat32(ptr unsafe.Pointer) error {
	f := float64(*(*float32)(ptr))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("vjson: unsupported float value: %v", f)
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
	return nil
}

func (m *Marshaler) encodeFloat64(ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("vjson: unsupported float value: %v", f)
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 64)
	return nil
}

func (m *Marshaler) encodeStruct(dec *ReflectStructDecoder, base unsafe.Pointer) error {
	m.buf = append(m.buf, '{')
	first := true

	if m.indent != "" {
		m.depth++
	}

	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldPtr := unsafe.Add(base, fi.Offset)

		if fi.OmitEmpty && fi.IsZeroFn != nil && fi.IsZeroFn(fieldPtr) {
			continue
		}

		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
			m.buf = append(m.buf, fi.KeyBytesIndent...)
		} else {
			m.buf = append(m.buf, fi.KeyBytes...)
		}

		if err := m.encodeValue(fi, fieldPtr); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.depth--
		if !first {
			m.appendNewlineIndent()
		}
	}

	m.buf = append(m.buf, '}')
	return nil
}

func (m *Marshaler) encodeSlice(dec *ReflectSliceDecoder, ptr unsafe.Pointer) error {
	sh := (*SliceHeader)(ptr)

	if sh.Data == nil || sh.Len == 0 {
		m.buf = append(m.buf, litArr...)
		return nil
	}

	// []byte → base64
	if dec.ElemTI.Kind == KindUint8 && dec.ElemSize == 1 {
		return m.encodeByteSlice(sh)
	}

	m.buf = append(m.buf, '[')
	elemSize := dec.ElemSize

	if m.indent != "" {
		m.depth++
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
		m.depth--
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

func (m *Marshaler) encodePointer(dec *ReflectPointerDecoder, ptr unsafe.Pointer) error {
	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	return m.encodeValue(dec.ElemTI, elemPtr)
}

func (m *Marshaler) encodeMap(dec *ReflectMapDecoder, ptr unsafe.Pointer) error {
	if dec.ValIsString {
		return m.encodeMapStringString(ptr)
	}
	return m.encodeMapGeneric(dec, ptr)
}

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
		m.depth++
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
		m.depth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

func (m *Marshaler) encodeMapGeneric(dec *ReflectMapDecoder, ptr unsafe.Pointer) error {
	mapPtr := reflect.NewAt(dec.MapType, ptr)
	mapVal := mapPtr.Elem()
	if mapVal.IsNil() {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	if mapVal.Len() == 0 {
		m.buf = append(m.buf, litEmpty...)
		return nil
	}

	m.buf = append(m.buf, '{')
	first := true

	if m.indent != "" {
		m.depth++
	}

	iter := mapVal.MapRange()
	for iter.Next() {
		if !first {
			m.buf = append(m.buf, ',')
		}
		first = false

		if m.indent != "" {
			m.appendNewlineIndent()
		}

		key := iter.Key().String()
		m.encodeString(key)
		if m.indent != "" {
			m.buf = append(m.buf, ':', ' ')
		} else {
			m.buf = append(m.buf, ':')
		}

		val := iter.Value()
		valPtr := reflect.New(dec.ValType)
		valPtr.Elem().Set(val)
		if err := m.encodeValue(dec.ValTI, valPtr.UnsafePointer()); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.depth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

func (m *Marshaler) encodeAny(ptr unsafe.Pointer) error {
	v := *(*any)(ptr)
	if v == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}

	switch val := v.(type) {
	case bool:
		if val {
			m.buf = append(m.buf, litTrue...)
		} else {
			m.buf = append(m.buf, litFalse...)
		}
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
			return fmt.Errorf("vjson: unsupported float value: %v", f)
		}
		m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return fmt.Errorf("vjson: unsupported float value: %v", val)
		}
		m.buf = strconv.AppendFloat(m.buf, val, 'f', -1, 64)
	case string:
		m.encodeString(val)
	case []byte:
		m.buf = append(m.buf, '"')
		encodedLen := base64.StdEncoding.EncodedLen(len(val))
		start := len(m.buf)
		m.buf = append(m.buf, make([]byte, encodedLen)...)
		base64.StdEncoding.Encode(m.buf[start:], val)
		m.buf = append(m.buf, '"')
	case []any:
		return m.encodeAnySlice(val)
	case map[string]any:
		return m.encodeAnyMap(val)
	default:
		return m.encodeAnyReflect(v)
	}
	return nil
}

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
		m.depth++
	}

	for i, v := range arr {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if m.indent != "" {
			m.appendNewlineIndent()
		}

		vv := v
		if err := m.encodeAny(unsafe.Pointer(&vv)); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.depth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, ']')
	return nil
}

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
		m.depth++
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

		vv := v
		if err := m.encodeAny(unsafe.Pointer(&vv)); err != nil {
			return err
		}
	}

	if m.indent != "" {
		m.depth--
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

	ti := GetDecoder(rv.Type())

	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return m.encodeValue(ti, tmp.UnsafePointer())
}
