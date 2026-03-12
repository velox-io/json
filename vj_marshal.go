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

	"github.com/velox-io/json/native/encvm"
)

// Pre-computed string representations for 0-999.
var smallInts [1000]string

func init() {
	for i := range smallInts {
		smallInts[i] = strconv.Itoa(i)
	}
	// Pre-warm pool so the first Marshal call avoids allocation.
	marshalerPool.Put(&Marshaler{buf: make([]byte, 0, marshalBufInitSize)})
}

var (
	litTrue  = []byte("true")
	litFalse = []byte("false")
	litNull  = []byte("null")
	litEmpty = []byte("{}")
	litArr   = []byte("[]")
)

const (
	// marshalBufInitSize is the default capacity for pool-created buffers.
	marshalBufInitSize = 32 * 1024
	// marshalBufPoolLimit is the maximum buffer capacity kept in the pool.
	// Buffers that grow beyond this during encoding are discarded (nil-ed)
	// when the Marshaler is returned to the pool, preventing large one-off
	// payloads from inflating steady-state pool memory.
	marshalBufPoolLimit = 1024 * 1024
)

// MarshalOption configures encoding behavior.
type MarshalOption func(*Marshaler)

// WithEscapeHTML enables escaping of <, >, & in strings.
func WithEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags |= escapeHTML }
}

// WithoutEscapeHTML disables escaping of <, >, &.
func WithoutEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags &^= escapeHTML }
}

// WithEscapeLineTerms enables escaping of U+2028 and U+2029 line terminators in strings.
func WithEscapeLineTerms() MarshalOption {
	return func(m *Marshaler) { m.flags |= escapeLineTerms }
}

// WithoutEscapeLineTerms disables escaping of U+2028 and U+2029.
func WithoutEscapeLineTerms() MarshalOption {
	return func(m *Marshaler) { m.flags &^= escapeLineTerms }
}

// WithUTF8Correction enables replacing invalid UTF-8 with \ufffd in strings.
func WithUTF8Correction() MarshalOption {
	return func(m *Marshaler) { m.flags |= escapeInvalidUTF8 }
}

// WithoutUTF8Correction disables replacing invalid UTF-8 in strings.
func WithoutUTF8Correction() MarshalOption {
	return func(m *Marshaler) { m.flags &^= escapeInvalidUTF8 }
}

// WithStdCompat enables full encoding/json compatibility.
func WithStdCompat() MarshalOption {
	return func(m *Marshaler) {
		m.flags = escapeStdCompat
		m.encFlags |= vjEncFloatExpAuto
	}
}

// WithFloatExpAuto enables encoding/json-compatible scientific notation
// for floats with |f| < 1e-6 or |f| >= 1e21 (e.g. 1e-7, 1e+21).
// By default, floats are always formatted in fixed-point notation.
func WithFloatExpAuto() MarshalOption {
	return func(m *Marshaler) { m.encFlags |= vjEncFloatExpAuto }
}

// WithFastEscape disables all string-level escape features
// (UTF-8 validation, line terminator escaping, HTML escaping).
// Only mandatory JSON escapes (control chars, '"', '\\') are performed.
// This enables the fastest string encoding path in the native encoder.
func WithFastEscape() MarshalOption {
	return func(m *Marshaler) { m.flags &^= escapeHTML | escapeLineTerms | escapeInvalidUTF8 }
}

// Marshaler is a pooled JSON encoder.
type Marshaler struct {
	buf      []byte
	indent   string
	prefix   string
	depth    int
	flags    escapeFlags
	encFlags uint32    // extra VjEncFlags bits (float format, etc.) ORed with flags for VM
	vmCtx    VjExecCtx // reusable C VM context (avoids per-call stack zeroing of 1448 bytes)
	inVM     bool      // true while execVM is active; prevents re-entrant VM calls

	// indentTpl holds the precomputed "\n" + prefix + indent×MAX_DEPTH template
	// for the C VM indent path. Only used when isSimpleIndent returns true.
	// Pointer to pool-allocated array; nil in compact mode (zero overhead).
	indentTpl *[1 + 255 + maxStackDepth*8]byte

	// Dynamic instruction expansion buffers for map[string]string VM encoding.
	// Reused across calls to avoid per-map allocation.
	dynOps  []VjOpStep      // dynamic instruction buffer
	dynData []mapStrStrPair // contiguous k-v pair data for opMapStrKV

	// flushFn enables streaming mode: flush accumulated data through
	// this callback instead of growing the buffer indefinitely.
	// Used by Encoder for bounded O(bufCap) memory.
	flushFn func([]byte) error
}

var marshalerPool = sync.Pool{
	New: func() any {
		return &Marshaler{
			buf: make([]byte, 0, marshalBufInitSize),
		}
	},
}

var indentTplPool = sync.Pool{
	New: func() any {
		return new([1 + 255 + maxStackDepth*8]byte)
	},
}

func getMarshaler() *Marshaler {
	m := marshalerPool.Get().(*Marshaler)
	m.buf = m.buf[:0]
	m.indent = ""
	m.prefix = ""
	m.depth = 0
	m.flags = 0
	m.encFlags = 0
	m.flushFn = nil
	initMarshalerVMCtx(m)
	m.setupVMTrace()
	return m
}

func putMarshaler(m *Marshaler) {
	if cap(m.buf) > marshalBufPoolLimit {
		m.buf = nil // discard oversized buffer, let GC reclaim
	}
	if m.indentTpl != nil {
		indentTplPool.Put(m.indentTpl)
		m.indentTpl = nil
	}
	m.flushFn = nil      // clear closure reference before pooling
	marshalerPool.Put(m) // always recycle the struct (vmCtx is 1448 bytes)
}

// flush writes all buffered data through flushFn and resets the buffer.
// Returns nil if flushFn is not set or the buffer is empty.
func (m *Marshaler) flush() error {
	if m.flushFn == nil || len(m.buf) == 0 {
		return nil
	}
	err := m.flushFn(m.buf)
	m.buf = m.buf[:0]
	return err
}

// Per-Kind encode functions (package-level) for EncodeFn dispatch.

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
		if ti.Codec == nil {
			return // cycle: Codec not yet built; EncodeFn will be bound later
		}
		ti.EncodeFn = makeEncodeStruct(ti.Codec.(*StructCodec))
	case KindSlice:
		if ti.Codec == nil {
			return
		}
		ti.EncodeFn = makeEncodeSlice(ti.Codec.(*SliceCodec))
	case KindArray:
		if ti.Codec == nil {
			return
		}
		ti.EncodeFn = makeEncodeArray(ti.Codec.(*ArrayCodec))
	case KindPointer:
		if ti.Codec == nil {
			return
		}
		ti.EncodeFn = makeEncodePointer(ti.Codec.(*PointerCodec))
	case KindMap:
		if ti.Codec == nil {
			return
		}
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
			if encodeFn != nil {
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
			} else {
				// EncodeFn nil due to cycle — resolve at runtime via encodeValue.
				fiRef := fi
				steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
					ptr := unsafe.Add(base, offset)
					if isZeroFn(ptr) {
						return first, nil
					}
					if !first {
						m.buf = append(m.buf, ',')
					}
					m.buf = append(m.buf, keyBytes...)
					return false, m.encodeValue(fiRef, ptr)
				}
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
			if encodeFn != nil {
				steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
					if !first {
						m.buf = append(m.buf, ',')
					}
					m.buf = append(m.buf, keyBytes...)
					return false, encodeFn(m, unsafe.Add(base, offset))
				}
			} else {
				// EncodeFn nil due to cycle — resolve at runtime via encodeValue.
				fiRef := fi
				steps[i] = func(m *Marshaler, base unsafe.Pointer, first bool) (bool, error) {
					if !first {
						m.buf = append(m.buf, ',')
					}
					m.buf = append(m.buf, keyBytes...)
					return false, m.encodeValue(fiRef, unsafe.Add(base, offset))
				}
			}
		}
	}
	dec.EncodeSteps = steps
}

// computeHintBytes returns a static byte-count estimate for the JSON encoding
// of ti, computed once at codec construction time. Conservative for
// data-dependent types; underestimates are safe (buffer grows via append).
func computeHintBytes(ti *TypeInfo, depth int) int {
	if depth > 8 {
		return 32 // prevent unbounded recursion for deeply nested/recursive types
	}
	if ti.Flags&tiFlagHasMarshalFn != 0 || ti.Flags&tiFlagHasTextMarshalFn != 0 {
		return 64
	}
	// During codec construction, Codec may be nil for types involved in
	// pointer cycles (getCodecForCycle returns a partially-initialized TypeInfo).
	// Return a conservative estimate in that case.
	if ti.Codec == nil {
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

	m.buf = dst

	ti := GetCodec(reflect.TypeFor[T]())
	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		m.buf = nil // detach caller's buffer before pooling
		putMarshaler(m)
		return dst, err
	}

	result := m.buf
	m.buf = nil // detach caller's buffer before pooling
	putMarshaler(m)
	return result, nil
}

// finalize copies the result to a new slice and recycles the Marshaler.
func (m *Marshaler) finalize() []byte {
	n := len(m.buf)
	result := makeDirtyBytes(n, n)
	copy(result, m.buf)
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

// isSimpleIndent returns the indent step (bytes per level) if the indent
// string is a uniform repetition of spaces or tabs and prefix fits in uint8.
// Returns 0 if the indent is not suitable for the native VM fast path.
func isSimpleIndent(prefix, indent string) int {
	if len(prefix) > 255 || len(indent) == 0 || len(indent) > 8 {
		return 0
	}
	ch := indent[0]
	if ch != ' ' && ch != '\t' {
		return 0
	}
	for i := 1; i < len(indent); i++ {
		if indent[i] != ch {
			return 0
		}
	}
	return len(indent)
}

// buildIndentTpl fills m.indentTpl with "\n" + prefix + indent×MAX_DEPTH.
// Allocates the template array from indentTplPool on first use.
func (m *Marshaler) buildIndentTpl(prefix, indent string) {
	if m.indentTpl == nil {
		m.indentTpl = indentTplPool.Get().(*[1 + 255 + maxStackDepth*8]byte)
	}
	m.indentTpl[0] = '\n'
	off := 1
	off += copy(m.indentTpl[off:], prefix)
	for range maxStackDepth {
		off += copy(m.indentTpl[off:], indent)
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

// appendJSONFloat64 appends a float64. When vjEncFloatExpAuto is set, uses
// encoding/json format: scientific notation for |f| < 1e-6 or |f| >= 1e21.
// Otherwise uses fixed-point notation.
func (m *Marshaler) appendJSONFloat64(f float64) {
	if m.encFlags&vjEncFloatExpAuto != 0 {
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
	if m.encFlags&vjEncFloatExpAuto != 0 {
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

func (m *Marshaler) encodeStruct(dec *StructCodec, base unsafe.Pointer) error {
	// Indent mode: check if we can use the native VM fast path.
	// Simple indents (uniform spaces/tabs, no prefix) can be handled
	// by the C VM; exotic indents fall through to the Go path.
	if m.indent != "" {
		if m.inVM || !encvm.Available || isSimpleIndent(m.prefix, m.indent) == 0 {
			return m.encodeStructIndent(dec, base)
		}
		return m.encodeStructNative(dec, base)
	}

	// If we're inside a VM yield handler (e.g. cycle-detected struct fallback),
	// use the Go path to avoid re-entrant execVM calls that would corrupt the
	// single shared VjExecCtx.
	if m.inVM {
		return m.encodeStructGo(dec, base)
	}

	// Native VM path: flat linear instruction stream with yield protocol.
	// This handles ALL struct types (including those with custom marshalers,
	// maps, slices, etc.) by yielding to Go for unsupported fields.
	if encvm.Available {
		return m.encodeStructNative(dec, base)
	}

	// Go fallback: no native encoder available on this platform.
	return m.encodeStructGo(dec, base)
}

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

	if !m.inVM && encvm.Available {
		if m.indent == "" || isSimpleIndent(m.prefix, m.indent) > 0 {
			return m.encodeSliceNative(dec, ptr)
		}
	}

	return m.encodeSliceGo(dec, ptr)
}

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

func (m *Marshaler) encodeArray(dec *ArrayCodec, ptr unsafe.Pointer) error {
	// [N]byte → base64
	if dec.ElemTI.Kind == KindUint8 && dec.ElemSize == 1 {
		return m.encodeByteArray(dec, ptr)
	}

	if !m.inVM && encvm.Available {
		if m.indent == "" || isSimpleIndent(m.prefix, m.indent) > 0 {
			return m.encodeArrayNative(dec, ptr)
		}
	}

	return m.encodeArrayGo(dec, ptr)
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
		m.depth++
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
		m.depth--
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

// encodeAny encodes an any-typed value from a pointer to the interface{} eface.
// Thin wrapper over encodeAnyVal; exists to satisfy the EncodeFn / encodeValueSlow
// signature that requires unsafe.Pointer.
func (m *Marshaler) encodeAny(ptr unsafe.Pointer) error {
	return m.encodeAnyVal(*(*any)(ptr))
}

// encodeAnyVal encodes an arbitrary Go value stored in an interface{}.
// Hot types (string, float64, bool, []any, map[string]any) are handled
// inline; numeric types, []byte, json.Number follow; everything else
// falls back to encodeAnyReflect.
func (m *Marshaler) encodeAnyVal(v any) error {
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
// Hot types are inlined for icache locality; cold types fall through to encodeAnyVal.
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
			if err := m.encodeAnyVal(v); err != nil {
				return err
			}
		}
	}

	if m.indent != "" {
		m.depth--
		m.appendNewlineIndent()
	}

	m.buf = append(m.buf, ']')
	return nil
}

// encodeAnyMap encodes a map[string]any as a JSON object.
// Hot types are inlined for icache locality; cold types fall through to encodeAnyVal.
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

		// Inline fast-path for the most common JSON value types.
		// Avoids function call overhead for hot cases (string, float64, etc.).
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
			if err := m.encodeAnyVal(v); err != nil {
				return err
			}
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
