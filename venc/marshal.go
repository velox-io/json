package venc

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/typ"
)

const (
	marshalBufInitSize  = 32 * 1024
	marshalBufPoolLimit = 1024 * 1024
)

type marshaler struct {
	// vmCtx must stay first so VjExecCtx.Stack keeps the native-required alignment.
	vmCtx VjExecCtx
	flags uint32 // escape flags | vjEncFloatExpAuto
	inVM  bool   // blocks re-entrant VM entry
	buf   []byte

	indent       string
	prefix       string
	indentDepth  int
	nativeCompat bool                              // true if compact or simple indent pattern (C VM can handle)
	indentTpl    *[1 + 255 + maxIndentDepth*8]byte // "\n" + prefix + indent*maxDepth

	flushFn func([]byte) error // streaming sink for Encoder
}

// vmCtx offset must remain 0 for the native stack alignment rule.
var _ [0]byte = [unsafe.Offsetof(marshaler{}.vmCtx)]byte{}

var marshalerPool = sync.Pool{
	New: func() any {
		return &marshaler{
			buf: gort.MakeDirtyBytes(0, marshalBufInitSize),
		}
	},
}

var indentTplPool = sync.Pool{
	New: func() any {
		return new([1 + 255 + maxIndentDepth*8]byte)
	},
}

func init() {
	marshalerPool.Put(&marshaler{buf: gort.MakeDirtyBytes(0, marshalBufInitSize)})
}

func getMarshaler() *marshaler {
	m := marshalerPool.Get().(*marshaler)
	m.buf = m.buf[:0] // reset buffer (may contain partial output from a prior error path)
	m.indent = ""
	m.prefix = ""
	m.indentDepth = 0
	m.nativeCompat = encvm.Available // compact mode: native OK if C VM linked
	m.flags = 0
	m.flushFn = nil
	// Compact-mode VM entry assumes the indent fields are zeroed.
	m.vmCtx.IndentTpl = nil
	m.vmCtx.IndentStep = 0
	m.vmCtx.IndentPrefixLen = 0
	m.vmCtx.IndentDepth = 0

	m.setupVMTrace()

	return m
}

func putMarshaler(m *marshaler) {
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

// flush writes buffered data through flushFn.
func (m *marshaler) flush() error {
	if m.flushFn == nil || len(m.buf) == 0 {
		return nil
	}
	err := m.flushFn(m.buf)
	m.buf = m.buf[:0]
	return err
}

// MarshalOption configures encoding behavior.
type MarshalOption func(*marshaler)

// WithEscapeHTML enables HTML escaping.
func WithEscapeHTML() MarshalOption {
	return func(m *marshaler) { m.flags |= uint32(escapeHTML) }
}

// WithoutEscapeHTML disables HTML escaping.
func WithoutEscapeHTML() MarshalOption {
	return func(m *marshaler) { m.flags &^= uint32(escapeHTML) }
}

// WithEscapeLineTerms escapes U+2028 and U+2029.
func WithEscapeLineTerms() MarshalOption {
	return func(m *marshaler) { m.flags |= uint32(escapeLineTerms) }
}

// WithoutEscapeLineTerms leaves U+2028 and U+2029 unescaped.
func WithoutEscapeLineTerms() MarshalOption {
	return func(m *marshaler) { m.flags &^= uint32(escapeLineTerms) }
}

// WithUTF8Correction replaces invalid UTF-8 with \ufffd.
func WithUTF8Correction() MarshalOption {
	return func(m *marshaler) { m.flags |= uint32(escapeInvalidUTF8) }
}

// WithoutUTF8Correction preserves invalid UTF-8 bytes.
func WithoutUTF8Correction() MarshalOption {
	return func(m *marshaler) { m.flags &^= uint32(escapeInvalidUTF8) }
}

// WithStdCompat matches encoding/json escaping and float formatting.
func WithStdCompat() MarshalOption {
	return func(m *marshaler) {
		m.flags = uint32(escapeStdCompat) | vjEncFloatExpAuto
	}
}

// WithFloatExpAuto matches encoding/json scientific-notation thresholds.
func WithFloatExpAuto() MarshalOption {
	return func(m *marshaler) { m.flags |= vjEncFloatExpAuto }
}

// WithFastEscape leaves only the mandatory JSON escapes enabled.
func WithFastEscape() MarshalOption {
	return func(m *marshaler) { m.flags &^= uint32(escapeHTML | escapeLineTerms | escapeInvalidUTF8) }
}

func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		rtp := *(*uintptr)(unsafe.Add(unsafe.Pointer(&rt), unsafe.Sizeof(uintptr(0))))
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr != nil {
			ti := encElemTypeInfoOf(rtp, rt)

			hint := marshalHint(ti, elemPtr)
			if hint > cap(m.buf) {
				m.buf = gort.MakeDirtyBytes(0, max(marshalBufInitSize, hint))
			}

			err := m.encodeTop(ti, elemPtr)

			if err != nil {
				putMarshaler(m)
				return nil, err
			}

			if n := int64(len(m.buf)); n > ti.AdaptiveHint.Load() {
				ti.AdaptiveHint.Store(n + n/20) // +5% headroom to avoid BufFull on next call
			}

			return m.finalize(), nil
		}
	}

	// Slow path: non-pointer T or nil pointer — v escapes here, which is fine.
	return marshalSlow(m, v)
}

// marshalSlow handles non-pointer types and nil pointers where &v must escape.
//
//go:noinline
func marshalSlow[T any](m *marshaler, v T) ([]byte, error) {
	ti, ptr := marshalTarget(v)

	hint := marshalHint(ti, ptr)
	if hint > cap(m.buf) {
		m.buf = gort.MakeDirtyBytes(0, max(marshalBufInitSize, hint))
	}

	if err := m.encodeTop(ti, ptr); err != nil {
		putMarshaler(m)
		return nil, err
	}

	if n := int64(len(m.buf)); n > ti.AdaptiveHint.Load() {
		ti.AdaptiveHint.Store(n + n/20) // +5% headroom to avoid BufFull on next call
	}

	return m.finalize(), nil
}

func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	m.prefix = prefix
	m.indent = indent
	m.nativeCompat = encvm.Available && isSimpleIndent(prefix, indent) > 0

	ti, ptr := marshalTarget(v)
	if err := m.encodeTop(ti, ptr); err != nil {
		putMarshaler(m)
		return nil, err
	}

	return m.finalize(), nil
}

func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	m := getMarshaler()
	for _, o := range opts {
		o(m)
	}

	m.buf = dst

	ti, ptr := marshalTarget(v)
	if err := m.encodeTop(ti, ptr); err != nil {
		m.buf = nil // detach caller's buffer before pooling
		putMarshaler(m)
		return dst, err
	}

	result := m.buf
	m.buf = nil // detach caller's buffer before pooling
	putMarshaler(m)
	return result, nil
}

// marshalTarget unwraps one pointer level; nil pointers stay on the pointer codec.
func marshalTarget[T any](v T) (*EncTypeInfo, unsafe.Pointer) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr == nil {
			return EncTypeInfoOf(rt), unsafe.Pointer(&v)
		}
		return EncTypeInfoOf(rt.Elem()), elemPtr
	}
	return EncTypeInfoOf(rt), unsafe.Pointer(&v)
}

// marshalHint returns the best buffer size estimate for the given type and data.
// Priority: AdaptiveHint (historical, most accurate) > SizeFn (data-driven) > HintBytes (static).
func marshalHint(ti *EncTypeInfo, ptr unsafe.Pointer) int {
	// AdaptiveHint is the most accurate — it's based on real output size + headroom.
	if ah := int(ti.AdaptiveHint.Load()); ah > 0 {
		return ah
	}
	// First call: use SizeFn if available for data-driven prediction.
	if fn := ti.SizeFn; fn != nil {
		if predicted := fn(ptr); predicted > 0 {
			return predicted + predicted/20 // +5% headroom
		}
	}
	return ti.HintBytes
}

// finalize detaches the output before pooling the marshaler.
func (m *marshaler) finalize() []byte {
	n := len(m.buf)

	// result := makeDirtyBytes(n, n)
	// copy(result, m.buf)

	result := m.buf[:n:n]
	c := cap(m.buf)
	remain := c - n
	if remain >= marshalBufInitSize/4 {
		m.buf = m.buf[n:n:c]
	} else {
		m.buf = nil
	}

	putMarshaler(m)
	return result
}

// exec runs a compiled Blueprint through the best available VM.
func (m *marshaler) exec(bp *Blueprint, base unsafe.Pointer) error {
	if !m.inVM && m.nativeCompat {
		return m.execNative(bp, base)
	}
	return m.goVM(bp, base)
}

// execNative drives the C VM. Moved from encvm_exec.go for colocation.
func (m *marshaler) execNative(bp *Blueprint, base unsafe.Pointer) error {
	return m.execVM(bp, base)
}

// encodeTop is the universal encode dispatch. It replaces the old EncodeFn
// function-pointer approach with a direct Kind switch.
func (m *marshaler) encodeTop(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	// Custom marshal hooks — must call user code, cannot be compiled to bytecode.
	if ti.TypeFlags&EncTypeFlagHasMarshalFn != 0 {
		data, err := ti.Hooks.MarshalFn(ptr)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			m.buf = append(m.buf, litNull...)
		} else {
			m.buf = append(m.buf, data...)
		}
		return nil
	}
	if ti.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		data, err := ti.Hooks.TextMarshalFn(ptr)
		if err != nil {
			return err
		}
		m.encodeString(unsafeString(data))
		return nil
	}

	switch ti.Kind {
	case typ.KindBool:
		if *(*bool)(ptr) {
			m.buf = append(m.buf, litTrue...)
		} else {
			m.buf = append(m.buf, litFalse...)
		}
	case typ.KindInt:
		m.appendInt64(int64(*(*int)(ptr)))
	case typ.KindInt8:
		m.appendInt64(int64(*(*int8)(ptr)))
	case typ.KindInt16:
		m.appendInt64(int64(*(*int16)(ptr)))
	case typ.KindInt32:
		m.appendInt64(int64(*(*int32)(ptr)))
	case typ.KindInt64:
		m.appendInt64(*(*int64)(ptr))
	case typ.KindUint:
		m.appendUint64(uint64(*(*uint)(ptr)))
	case typ.KindUint8:
		m.appendUint64(uint64(*(*uint8)(ptr)))
	case typ.KindUint16:
		m.appendUint64(uint64(*(*uint16)(ptr)))
	case typ.KindUint32:
		m.appendUint64(uint64(*(*uint32)(ptr)))
	case typ.KindUint64:
		m.appendUint64(*(*uint64)(ptr))
	case typ.KindFloat32:
		return m.encodeFloat32(ptr)
	case typ.KindFloat64:
		return m.encodeFloat64(ptr)
	case typ.KindString:
		m.encodeString(*(*string)(ptr))
	case typ.KindRawMessage:
		raw := *(*[]byte)(ptr)
		if len(raw) == 0 {
			m.buf = append(m.buf, litNull...)
		} else {
			m.buf = append(m.buf, raw...)
		}
	case typ.KindNumber:
		s := *(*string)(ptr)
		if s == "" {
			m.buf = append(m.buf, '0')
		} else {
			m.buf = append(m.buf, s...)
		}
	case typ.KindStruct:
		return m.exec(ti.getBlueprint(), ptr)
	case typ.KindSlice:
		si := ti.ResolveSlice()
		sh := (*SliceHeader)(ptr)
		if sh.Data == nil {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		if si.ElemType.Kind == typ.KindUint8 && si.ElemSize == 1 {
			return m.encodeByteSlice(sh)
		}
		return m.exec(ti.getBlueprint(), ptr)
	case typ.KindArray:
		ai := ti.ResolveArray()
		if ai.ElemType.Kind == typ.KindUint8 && ai.ElemSize == 1 {
			return m.encodeByteArray(ai, ptr)
		}
		return m.exec(ti.getBlueprint(), ptr)
	case typ.KindMap:
		mi := ti.ResolveMap()
		if mi.IsStringKey && !m.inVM && m.nativeCompat {
			return m.exec(ti.getBlueprint(), ptr)
		}
		if mi.MapKind == typ.MapVariantStrStr {
			return m.encodeMapStringString(ptr)
		}
		return m.encodeMapGeneric(mi, ptr)
	case typ.KindPointer:
		pi := ti.ResolvePointer()
		elemPtr := *(*unsafe.Pointer)(ptr)
		if elemPtr == nil {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		return m.encodeTop(pi.ElemType, elemPtr)
	case typ.KindAny:
		return m.encodeAny(*(*any)(ptr))
	case typ.KindIface:
		// Non-empty interface: need reflect to extract concrete value.
		rv := reflect.NewAt(ti.UniType.Type, ptr).Elem()
		if rv.IsNil() {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		return m.encodeAnyReflect(rv.Elem().Interface())
	default:
		return &UnsupportedTypeError{Type: ti.Type}
	}
	return nil
}

// isSimpleIndent reports whether the native VM can synthesize this indent pattern.
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

// buildIndentTpl materializes the VM indent template on first use.
func (m *marshaler) buildIndentTpl(prefix, indent string) {
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
