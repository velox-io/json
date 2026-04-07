package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// MarshalOption configures encoding behavior.
type MarshalOption func(*encodeState)

// WithEscapeHTML enables HTML escaping.
func WithEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeHTML) }
}

// WithoutEscapeHTML disables HTML escaping.
func WithoutEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML) }
}

// WithEscapeLineTerms escapes U+2028 and U+2029.
func WithEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeLineTerms) }
}

// WithoutEscapeLineTerms leaves U+2028 and U+2029 unescaped.
func WithoutEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeLineTerms) }
}

// WithUTF8Correction replaces invalid UTF-8 with \ufffd.
func WithUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeInvalidUTF8) }
}

// WithoutUTF8Correction preserves invalid UTF-8 bytes.
func WithoutUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeInvalidUTF8) }
}

// WithStdCompat matches encoding/json escaping and float formatting.
func WithStdCompat() MarshalOption {
	return func(es *encodeState) {
		es.flags = uint32(escapeStdCompat) | EncFloatExpAuto
	}
}

// WithFloatExpAuto matches encoding/json scientific-notation thresholds.
func WithFloatExpAuto() MarshalOption {
	return func(es *encodeState) { es.flags |= EncFloatExpAuto }
}

// WithFastEscape leaves only the mandatory JSON escapes enabled.
func WithFastEscape() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML | escapeLineTerms | escapeInvalidUTF8) }
}

// WithBufSize sets the initial encoder buffer capacity.
func WithBufSize(n int) MarshalOption {
	return func(es *encodeState) {
		if n > 0 {
			es.bufHint = n
		}
	}
}

// Marshal serializes v to JSON.
//
// Pointer T: handled inline — v is 8 bytes, dereference without &v so v
// stays on the stack (zero allocs).
// Value T:   dispatches to marshalSlow in a separate function so that its
// &v does not poison the pointer path's escape analysis.
func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		// Pointer fast path — inline to avoid extra call overhead on the hot path.
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr == nil {
			return []byte("null"), nil
		}

		es := acquireEncodeState()
		defer releaseEncodeState(es)
		for _, o := range opts {
			o(es)
		}

		rtp := uintptr(gort.TypePtr(rt))
		ti := encElemTypeInfoOf(rtp, rt)

		return es.marshalWith(ti, elemPtr)
	}
	return marshalSlow(v, rt, opts)
}

// marshalSlow is a thin shim that takes &v and forwards to marshalSlowPtr.
// It must stay small enough to be inlined, so that the compiler folds it
// into Marshal — v lives on Marshal's stack frame and is never copied again.
func marshalSlow[T any](v T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	return marshalSlowPtr(&v, rt, opts)
}

// marshalSlowPtr does the real work for value-typed T. It receives *T so
// the (potentially large) value is not copied a second time.
func marshalSlowPtr[T any](v *T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	for _, o := range opts {
		o(es)
	}

	ti := EncTypeInfoOf(rt)
	return es.marshalWith(ti, unsafe.Pointer(v))
}

// marshalWith is the shared tail for Marshal: sets up the buffer, runs the
// encoder, updates the adaptive hint, and splits the result out of es.buf.
func (es *encodeState) marshalWith(ti *EncTypeInfo, ptr unsafe.Pointer) ([]byte, error) {
	hint := int(ti.AdaptiveHint.Load())
	if hint == 0 {
		hint = encodingSizeHint(ti, ptr)
		ti.AdaptiveHint.Store(int64(hint))
	}

	// When the user requested a smaller buffer (WithBufSize) and the pool
	// buffer is larger than needed, replace it with a right-sized one.
	if es.bufHint > 0 && cap(es.buf) > es.bufHint && hint <= es.bufHint {
		es.buf = gort.MakeDirtyBytes(0, es.bufHint)
	}
	es.growBuf(hint)

	if err := es.encodeTop(ti, ptr); err != nil {
		return nil, err
	}

	n := len(es.buf)
	adapted := n + n/32 // +3% headroom for VM pessimistic checks
	if int64(adapted) > ti.AdaptiveHint.Load() {
		ti.AdaptiveHint.Store(int64(adapted))
	}

	result := es.buf[:n]
	es.buf = es.buf[n:]
	return result, nil
}

func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	return Marshal(v, append(opts, withIndent(prefix, indent))...)
}

// withIndent returns an internal MarshalOption that configures indentation.
func withIndent(prefix, indent string) MarshalOption {
	return func(es *encodeState) {
		es.indentPrefix = prefix
		es.indentString = indent
		es.nativeIndent = es.nativeIndent && isSimpleIndent(prefix, indent) > 0
	}
}

// AppendMarshal appends the JSON encoding of v to dst.
//
// Same pointer/value split as Marshal — pointer path inline, value path in
// appendMarshalSlow to isolate &v escape.
func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Pointer {
		return appendMarshalSlow(dst, v, rt, opts)
	}

	// Pointer fast path.
	elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
	if elemPtr == nil {
		return append(dst, "null"...), nil
	}

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	for _, o := range opts {
		o(es)
	}

	es.buf = dst
	rtp := uintptr(gort.TypePtr(rt))
	ti := encElemTypeInfoOf(rtp, rt)

	if err := es.encodeTop(ti, elemPtr); err != nil {
		es.buf = nil
		return dst, err
	}

	result := es.buf
	es.buf = nil
	return result, nil
}

func appendMarshalSlow[T any](dst []byte, v T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	return appendMarshalSlowPtr(dst, &v, rt, opts)
}

func appendMarshalSlowPtr[T any](dst []byte, v *T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	for _, o := range opts {
		o(es)
	}

	es.buf = dst
	ti := EncTypeInfoOf(rt)

	if err := es.encodeTop(ti, unsafe.Pointer(v)); err != nil {
		es.buf = nil
		return dst, err
	}

	result := es.buf
	es.buf = nil
	return result, nil
}
