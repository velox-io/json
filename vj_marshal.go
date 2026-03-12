package vjson

import (
	"encoding/base64"
	"encoding/json"
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
	marshalBufInitSize = 32 * 1024
	marshalBufMaxPool  = 1024 * 1024
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

// ---------------------------------------------------------------------------
// Per-Kind encode functions (package-level) for EncodeFn dispatch.
// ---------------------------------------------------------------------------

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

func encodeAnyValue(m *Marshaler, ptr unsafe.Pointer) error { return m.encodeAny(ptr) }

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

// bindEncodeFn assigns EncodeFn on ti based on priority:
// MarshalFn > TextMarshalFn > Kind-specific.
func bindEncodeFn(ti *TypeInfo) {
	if ti.Flags&tiFlagHasMarshalFn != 0 && ti.Ext != nil && ti.Ext.MarshalFn != nil {
		ti.EncodeFn = makeEncodeMarshalJSON(ti.Ext.MarshalFn)
		return
	}
	if ti.Flags&tiFlagHasTextMarshalFn != 0 && ti.Ext != nil && ti.Ext.TextMarshalFn != nil {
		ti.EncodeFn = makeEncodeTextMarshal(ti.Ext.TextMarshalFn)
		return
	}
	switch ti.Kind {
	case KindBool:
		ti.EncodeFn = encodeBool
	case KindInt:
		ti.EncodeFn = encodeInt
	case KindInt8:
		ti.EncodeFn = encodeInt8
	case KindInt16:
		ti.EncodeFn = encodeInt16
	case KindInt32:
		ti.EncodeFn = encodeInt32
	case KindInt64:
		ti.EncodeFn = encodeInt64Fn
	case KindUint:
		ti.EncodeFn = encodeUint
	case KindUint8:
		ti.EncodeFn = encodeUint8
	case KindUint16:
		ti.EncodeFn = encodeUint16
	case KindUint32:
		ti.EncodeFn = encodeUint32
	case KindUint64:
		ti.EncodeFn = encodeUint64Fn
	case KindFloat32:
		ti.EncodeFn = encodeFloat32Value
	case KindFloat64:
		ti.EncodeFn = encodeFloat64Value
	case KindString:
		ti.EncodeFn = encodeStringValue
	case KindRawMessage:
		ti.EncodeFn = encodeRawMessageFn
	case KindNumber:
		ti.EncodeFn = encodeNumberFn
	case KindAny:
		ti.EncodeFn = encodeAnyValue
	case KindStruct:
		ti.EncodeFn = makeEncodeStruct(ti.Codec.(*StructCodec))
	case KindSlice:
		ti.EncodeFn = makeEncodeSlice(ti.Codec.(*SliceCodec))
	case KindPointer:
		ti.EncodeFn = makeEncodePointer(ti.Codec.(*PointerCodec))
	case KindMap:
		ti.EncodeFn = makeEncodeMap(ti.Codec.(*MapCodec))
	}
}

// bindStructFieldEncodeFns binds EncodeFn on struct field copies
// and builds the EncodeSteps table for compact encoding.
func bindStructFieldEncodeFns(ti *TypeInfo) {
	if ti.Kind != KindStruct || ti.Codec == nil {
		return
	}
	dec := ti.Codec.(*StructCodec)
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		if fi.EncodeFn == nil {
			bindEncodeFn(fi)
		}
	}
	buildStructEncodeSteps(dec)
}

// structEncodeStep encodes one struct field in compact (non-indent) mode.
// first indicates whether no field has been written yet.
// Returns the updated first flag and any error.
type structEncodeStep func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error)

// buildStructEncodeSteps pre-generates a closure per field that captures
// offset, keyBytes, encodeFn, and omitempty logic, eliminating per-field
// branching at encode time.
func buildStructEncodeSteps(dec *StructCodec) {
	steps := make([]structEncodeStep, len(dec.Fields))
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		offset := fi.Offset
		keyBytes := fi.Ext.KeyBytes
		encodeFn := fi.EncodeFn

		hasOmitEmpty := fi.Flags&tiFlagOmitEmpty != 0 && fi.Ext.IsZeroFn != nil
		isQuoted := fi.Flags&tiFlagQuoted != 0

		switch {
		case hasOmitEmpty && isQuoted:
			isZeroFn := fi.Ext.IsZeroFn
			tiCopy := *fi // copy for encodeValueQuoted
			steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
				ptr := unsafe.Add(base, offset)
				if isZeroFn(ptr) {
					return first, nil
				}
				if !first {
					m.buf = append(m.buf, ',')
				}
				m.buf = append(m.buf, keyBytes...)
				return false, m.encodeValueQuoted(&tiCopy, ptr)
			}
		case hasOmitEmpty:
			isZeroFn := fi.Ext.IsZeroFn
			steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
				ptr := unsafe.Add(base, offset)
				if isZeroFn(ptr) {
					return first, nil
				}
				if !first {
					m.buf = append(m.buf, ',')
				}
				m.buf = append(m.buf, keyBytes...)
				return false, encodeFn(m, ptr)
			}
		case isQuoted:
			tiCopy := *fi
			steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
				if !first {
					m.buf = append(m.buf, ',')
				}
				m.buf = append(m.buf, keyBytes...)
				return false, m.encodeValueQuoted(&tiCopy, unsafe.Add(base, offset))
			}
		default:
			steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
				if !first {
					m.buf = append(m.buf, ',')
				}
				m.buf = append(m.buf, keyBytes...)
				return false, encodeFn(m, unsafe.Add(base, offset))
			}
		}
	}
	dec.EncodeSteps = steps
}

// computeHintBytes returns a static byte-count estimate for the JSON encoding
// of the type described by ti. Unlike the old estimateDataSize, this is
// computed ONCE at codec construction time and costs nothing at marshal time.
// For data-dependent types (strings, slices, maps) it uses a conservative
// fixed estimate; underestimates are safe because the buffer grows via append.
func computeHintBytes(ti *TypeInfo, depth int) int {
	if depth > 8 {
		return 32 // prevent unbounded recursion for deeply nested/recursive types
	}
	if ti.Flags&tiFlagHasMarshalFn != 0 || ti.Flags&tiFlagHasTextMarshalFn != 0 {
		return 64
	}
	switch ti.Kind {
	case KindBool:
		return 5
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		return 12
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		return 12
	case KindFloat32, KindFloat64:
		return 20
	case KindString:
		return 32 // typical short string
	case KindStruct:
		dec := ti.Codec.(*StructCodec)
		n := 2 // { }
		for i := range dec.Fields {
			fi := &dec.Fields[i]
			n += len(fi.Ext.KeyBytes) + 1 // key + comma
			n += computeHintBytes(fi, depth+1)
		}
		return n
	case KindSlice:
		dec := ti.Codec.(*SliceCodec)
		// Assume ~4 elements with the element's static hint
		elemHint := computeHintBytes(dec.ElemTI, depth+1)
		return 2 + 4*(elemHint+1) // [ ] + 4*(elem+comma)
	case KindPointer:
		dec := ti.Codec.(*PointerCodec)
		return computeHintBytes(dec.ElemTI, depth+1)
	case KindMap:
		return 128 // conservative fixed estimate
	case KindRawMessage:
		return 64
	case KindNumber:
		return 12
	case KindAny:
		return 64
	default:
		return 32
	}
}

// Marshal returns the compact JSON encoding of *v.
func Marshal[T any](v *T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	ti := GetCodec(reflect.TypeFor[T]())

	if ti.HintBytes > cap(m.buf) {
		m.buf = make([]byte, 0, ti.HintBytes)
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

	ti := GetCodec(reflect.TypeFor[T]())
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

	ti := GetCodec(reflect.TypeFor[T]())
	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		return dst, err
	}

	return m.buf, nil
}

// finalize returns the encoded JSON as a standalone byte slice.
//
// Poolable buffers (cap <= marshalBufMaxPool) are copied out so the
// Marshaler's buf can be reused by the pool without aliasing the caller's
// result. Oversized buffers are detached and given directly to the caller
// to prevent the pool from accumulating large allocations that inflate
// steady-state memory usage.
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
	if fn := ti.EncodeFn; fn != nil {
		return fn(m, ptr)
	}
	return m.encodeValueSlow(ti, ptr)
}

// encodeValueSlow is the fallback for dynamically-obtained TypeInfo (e.g.
// encodeAnyReflect) or recursive types where EncodeFn was not yet bound.
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
		return m.encodeStruct(ti.Codec.(*StructCodec), ptr)
	case KindSlice:
		return m.encodeSlice(ti.Codec.(*SliceCodec), ptr)
	case KindPointer:
		return m.encodePointer(ti.Codec.(*PointerCodec), ptr)
	case KindMap:
		return m.encodeMap(ti.Codec.(*MapCodec), ptr)
	case KindRawMessage:
		raw := *(*[]byte)(ptr)
		if len(raw) == 0 {
			m.buf = append(m.buf, litNull...)
		} else {
			m.buf = append(m.buf, raw...)
		}
		return nil
	case KindNumber:
		s := *(*string)(ptr)
		if s == "" {
			m.buf = append(m.buf, '0')
		} else {
			m.buf = append(m.buf, s...)
		}
		return nil
	case KindAny:
		return m.encodeAny(ptr)

	default:
		return &UnsupportedTypeError{Type: ti.Ext.Type}
	}
}

func (m *Marshaler) encodeString(s string) {
	m.buf = appendEscapedString(m.buf, s, m.flags)
}

// encodeQuotedString double-encodes a string: Go "hello" → JSON "\"hello\"".
func (m *Marshaler) encodeQuotedString(s string) {
	inner := appendEscapedString(nil, s, m.flags)
	m.buf = appendEscapedString(m.buf, UnsafeString(inner), m.flags)
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
		m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
		m.buf = append(m.buf, '"')
	case KindFloat64:
		f := *(*float64)(ptr)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = append(m.buf, '"')
		m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 64)
		m.buf = append(m.buf, '"')
	case KindString:
		m.encodeQuotedString(*(*string)(ptr))
	case KindPointer:
		dec := ti.Codec.(*PointerCodec)
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
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
	return nil
}

func (m *Marshaler) encodeFloat64(ptr unsafe.Pointer) error {
	f := *(*float64)(ptr)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
	}
	m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 64)
	return nil
}

func (m *Marshaler) encodeStruct(dec *StructCodec, base unsafe.Pointer) error {
	if m.indent != "" {
		return m.encodeStructIndent(dec, base)
	}
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
	m.depth++

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

	m.depth--
	if !first {
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, '}')
	return nil
}

func (m *Marshaler) encodeSlice(dec *SliceCodec, ptr unsafe.Pointer) error {
	sh := (*SliceHeader)(ptr)

	if sh.Data == nil {
		m.buf = append(m.buf, litNull...) // nil slice → null
		return nil
	}

	// []byte → base64 (handles empty []byte{} → "")
	if dec.ElemTI.Kind == KindUint8 && dec.ElemSize == 1 {
		return m.encodeByteSlice(sh)
	}

	if sh.Len == 0 {
		m.buf = append(m.buf, litArr...)
		return nil
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

func (m *Marshaler) encodePointer(dec *PointerCodec, ptr unsafe.Pointer) error {
	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		m.buf = append(m.buf, litNull...)
		return nil
	}
	return m.encodeValue(dec.ElemTI, elemPtr)
}

func (m *Marshaler) encodeMap(dec *MapCodec, ptr unsafe.Pointer) error {
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

// resolveMapKey converts a map key to its JSON string representation.
func resolveMapKey(k reflect.Value, keyTI *TypeInfo) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if keyTI.Flags&tiFlagHasTextMarshalFn != 0 {
		tmp := reflect.New(k.Type())
		tmp.Elem().Set(k)
		text, err := keyTI.Ext.TextMarshalFn(tmp.UnsafePointer())
		if err != nil {
			return "", err
		}
		return string(text), nil
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
	return "", &UnsupportedTypeError{Type: k.Type()}
}

func (m *Marshaler) encodeMapGeneric(dec *MapCodec, ptr unsafe.Pointer) error {
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

		key, err := resolveMapKey(iter.Key(), dec.KeyTI)
		if err != nil {
			return err
		}
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
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", f)}
		}
		m.buf = strconv.AppendFloat(m.buf, f, 'f', -1, 32)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return &UnsupportedValueError{Str: fmt.Sprintf("%v", val)}
		}
		m.buf = strconv.AppendFloat(m.buf, val, 'f', -1, 64)
	case string:
		m.encodeString(val)
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
	case []any:
		return m.encodeAnySlice(val)
	case map[string]any:
		return m.encodeAnyMap(val)
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

	ti := GetCodec(rv.Type())

	tmp := reflect.New(rv.Type())
	tmp.Elem().Set(rv)
	return m.encodeValue(ti, tmp.UnsafePointer())
}
