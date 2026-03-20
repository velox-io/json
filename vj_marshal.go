package vjson

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

const (
	// marshalBufInitSize is the default capacity for pool-created buffers.
	marshalBufInitSize = 32 * 1024
	// marshalBufPoolLimit is the maximum buffer capacity kept in the pool.
	// When the Marshaler is returned to the pool, if its buffer has grown
	// beyond this limit, the buffer is released (set to nil) so the GC can
	// reclaim it, preventing large one-off payloads from inflating
	// steady-state pool memory.
	marshalBufPoolLimit = 1024 * 1024
)

func init() {
	// Pre-warm pool so the first Marshal call avoids allocation.
	marshalerPool.Put(&Marshaler{buf: make([]byte, 0, marshalBufInitSize)})
}

type Marshaler struct {
	// vmCtx MUST be the first field: VjExecCtx.Stack (at offset 96 within
	// VjExecCtx) requires 32-byte alignment relative to the struct base.
	// Placing vmCtx at offset 0 guarantees this (96 % 32 == 0), regardless
	// of what fields follow. Do not reorder.
	vmCtx VjExecCtx // reusable C VM context (avoids per-call stack zeroing)
	flags uint32    // escapeFlags (bits 0-2) | vjEncFloatExpAuto (bit 3)
	inVM  bool      // true while execVM is active; prevents re-entrant VM calls
	buf   []byte

	indent      string
	prefix      string
	indentDepth int
	// indentTpl holds the precomputed "\n" + prefix + indent×MAX_DEPTH template
	// for the C VM indent path. Only used when isSimpleIndent returns true.
	// Pointer to pool-allocated array; nil in compact mode (zero overhead).
	indentTpl *[1 + 255 + maxIndentDepth*8]byte

	// flushFn enables streaming mode: flush accumulated data through
	// this callback instead of growing the buffer indefinitely.
	// Used by Encoder for bounded O(bufCap) memory.
	flushFn func([]byte) error
}

// Compile-time assertion: vmCtx must be at offset 0 in Marshaler so that
// VjExecCtx.Stack (offset 96) is 32-byte aligned relative to the struct base.
// If this fails, someone reordered the Marshaler fields — fix the layout.
var _ [0]byte = [unsafe.Offsetof(Marshaler{}.vmCtx)]byte{}

var marshalerPool = sync.Pool{
	New: func() any {
		return &Marshaler{
			buf: make([]byte, 0, marshalBufInitSize),
		}
	},
}

var indentTplPool = sync.Pool{
	New: func() any {
		return new([1 + 255 + maxIndentDepth*8]byte)
	},
}

func getMarshaler() *Marshaler {
	m := marshalerPool.Get().(*Marshaler)
	m.buf = m.buf[:0]
	m.indent = ""
	m.prefix = ""
	m.indentDepth = 0
	m.flags = 0
	m.flushFn = nil
	// Zero indent fields on vmCtx so execVM's compact path can skip them.
	// These may be non-zero if the previous use was MarshalIndent.
	m.vmCtx.IndentTpl = nil
	m.vmCtx.IndentStep = 0
	m.vmCtx.IndentPrefixLen = 0
	m.vmCtx.IndentDepth = 0
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
	marshalerPool.Put(m) // always recycle the struct (vmCtx is 2152 bytes)
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

// MarshalOption configures encoding behavior.
type MarshalOption func(*Marshaler)

// WithEscapeHTML enables escaping of <, >, & in strings.
func WithEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags |= uint32(escapeHTML) }
}

// WithoutEscapeHTML disables escaping of <, >, &.
func WithoutEscapeHTML() MarshalOption {
	return func(m *Marshaler) { m.flags &^= uint32(escapeHTML) }
}

// WithEscapeLineTerms enables escaping of U+2028 and U+2029 line terminators in strings.
func WithEscapeLineTerms() MarshalOption {
	return func(m *Marshaler) { m.flags |= uint32(escapeLineTerms) }
}

// WithoutEscapeLineTerms disables escaping of U+2028 and U+2029.
func WithoutEscapeLineTerms() MarshalOption {
	return func(m *Marshaler) { m.flags &^= uint32(escapeLineTerms) }
}

// WithUTF8Correction enables replacing invalid UTF-8 with \ufffd in strings.
func WithUTF8Correction() MarshalOption {
	return func(m *Marshaler) { m.flags |= uint32(escapeInvalidUTF8) }
}

// WithoutUTF8Correction disables replacing invalid UTF-8 in strings.
func WithoutUTF8Correction() MarshalOption {
	return func(m *Marshaler) { m.flags &^= uint32(escapeInvalidUTF8) }
}

// WithStdCompat enables full encoding/json compatibility.
func WithStdCompat() MarshalOption {
	return func(m *Marshaler) {
		m.flags = uint32(escapeStdCompat) | vjEncFloatExpAuto
	}
}

// WithFloatExpAuto enables encoding/json-compatible scientific notation
// for floats with |f| < 1e-6 or |f| >= 1e21 (e.g. 1e-7, 1e+21).
// By default, floats are always formatted in fixed-point notation.
func WithFloatExpAuto() MarshalOption {
	return func(m *Marshaler) { m.flags |= vjEncFloatExpAuto }
}

// WithFastEscape disables all string-level escape features
// (UTF-8 validation, line terminator escaping, HTML escaping).
// Only mandatory JSON escapes (control chars, '"', '\\') are performed.
// This enables the fastest string encoding path in the native encoder.
func WithFastEscape() MarshalOption {
	return func(m *Marshaler) { m.flags &^= uint32(escapeHTML | escapeLineTerms | escapeInvalidUTF8) }
}

// Marshal returns the compact JSON encoding of *v.
//
// Stack-move safety: v is converted to unsafe.Pointer and stored in the
// heap-allocated Marshaler.vmCtx.CurBase for the C VM to read. If v were
// on the goroutine stack, a stack growth during a VM yield-to-Go could
// relocate the stack, leaving CurBase as a dangling pointer.
//
// This is currently safe because encodeValue dispatches through EncodeFn,
// a function value (indirect call). The escape analysis cannot see through
// it, so the compiler conservatively marks v as "leaks to heap", forcing
// the caller to heap-allocate v before entering Marshal. Should EncodeFn
// ever become a direct (devirtualisable) call, this implicit guarantee
// would break — add an explicit forceEscape or runtime.Pinner at that
// point.
func Marshal[T any](v *T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	ti := codecCacheMarshal.getCodec(reflect.TypeFor[T]())

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
// See Marshal for stack-move safety discussion.
func MarshalIndent[T any](v *T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	m.prefix = prefix
	m.indent = indent

	ti := codecCacheMarshal.getCodec(reflect.TypeFor[T]())
	if err := m.encodeValue(ti, unsafe.Pointer(v)); err != nil {
		putMarshaler(m)
		return nil, err
	}

	return m.finalize(), nil
}

// AppendMarshal appends the compact JSON encoding of *v to dst.
// See Marshal for stack-move safety discussion.
func AppendMarshal[T any](dst []byte, v *T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	m.buf = dst

	ti := codecCacheMarshal.getCodec(reflect.TypeFor[T]())
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

// encodeValue dispatches encoding via pre-bound EncodeFn.
// All supported types have EncodeFn set by bindEncodeFn at codec construction time.
// For types involved in indirect cycles, EncodeFn may still be nil because the
// codec was value-copied before the cycle was fully resolved; in that case we
// lazily resolve the codec and bind EncodeFn on first use.
func (m *Marshaler) encodeValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	if fn := ti.EncodeFn; fn != nil {
		return fn(m, ptr)
	}
	// Lazy resolution for cycle-detected types whose Codec was nil
	// at value-copy time (see getCodecForCycle / resolveCodec).
	if ti.resolveCodec() != nil {
		bindEncodeFn(ti)
		if fn := ti.EncodeFn; fn != nil {
			return fn(m, ptr)
		}
	}
	return &UnsupportedTypeError{Type: ti.Ext.Type}
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

	if encvm.Available {
		return m.encodeStructNative(dec, base)
	}

	return m.encodeStructGo(dec, base)
}

func (m *Marshaler) encodeSlice(dec *SliceCodec, ptr unsafe.Pointer) error {
	sh := (*SliceHeader)(ptr)

	if sh.Data == nil {
		m.buf = append(m.buf, litNull...) // nil slice → null
		return nil
	}

	// []byte → base64. Go fast path: avoids blueprint compilation overhead for
	// this top-level entry point. In struct/element contexts the C VM handles
	// []byte natively via OP_BYTE_SLICE.
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

func (m *Marshaler) encodeMap(dec *MapCodec, ptr unsafe.Pointer) error {
	// Try native VM path for string-keyed maps where MAP_STR_ITER can
	// iterate in C. Non-string-key maps always use the Go path.
	if !m.inVM && encvm.Available && dec.isStringKey {
		if m.indent == "" || isSimpleIndent(m.prefix, m.indent) > 0 {
			return m.encodeMapNative(dec, ptr)
		}
	}
	return m.encodeMapFallback(dec, ptr)
}

// encodeMapFallback is the pure-Go map encoding path.
func (m *Marshaler) encodeMapFallback(dec *MapCodec, ptr unsafe.Pointer) error {
	if dec.MapKind == MapVariantStrStr {
		return m.encodeMapStringString(ptr)
	}
	return m.encodeMapGeneric(dec, ptr)
}

// Codec binding
//
// bindEncodeFn assigns EncodeFn on ti based on priority:
//   MarshalFn > TextMarshalFn > Kind-specific.
//
// At codec construction time, every TypeInfo gets a pre-bound EncodeFn
// so that encodeValue can call it directly without runtime dispatch.

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
	case KindIface:
		ti.EncodeFn = makeEncodeIface(ti)
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
		m.indentTpl = indentTplPool.Get().(*[1 + 255 + maxIndentDepth*8]byte)
	}
	m.indentTpl[0] = '\n'
	off := 1
	off += copy(m.indentTpl[off:], prefix)
	for range maxIndentDepth {
		off += copy(m.indentTpl[off:], indent)
	}
}
