package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
)

type MarshalOption func(*encodeState)

func WithEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeHTML) }
}

func WithoutEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML) }
}

func WithEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeLineTerms) }
}

func WithoutEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeLineTerms) }
}

func WithUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeInvalidUTF8) }
}

func WithoutUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeInvalidUTF8) }
}

// WithStdCompat matches encoding/json escaping and float formatting.
func WithStdCompat() MarshalOption {
	return func(es *encodeState) {
		es.flags = uint32(escapeStdCompat) | EncFloatExpAuto
	}
}

func WithFloatExpAuto() MarshalOption {
	return func(es *encodeState) { es.flags |= EncFloatExpAuto }
}

func WithFastEscape() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML | escapeLineTerms | escapeInvalidUTF8) }
}

// WithBufSize sets a fixed working buffer size for encoding. If es.buf's
// capacity is less than n, a new buffer of size n is allocated; otherwise the
// existing buffer is kept. After encoding, the result is copied into a
// tight-fit allocation (exactly len(output) bytes) and returned, so es.buf
// retains its full capacity for pool reuse.
func WithBufSize(n int) MarshalOption {
	return func(es *encodeState) {
		if n > 0 {
			es.bufSize = n
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

func marshalSlow[T any](v T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	return marshalSlowPtr(&v, rt, opts)
}

func marshalSlowPtr[T any](v *T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	for _, o := range opts {
		o(es)
	}

	ti := EncTypeInfoOf(rt)
	return es.marshalWith(ti, unsafe.Pointer(v))
}

func (es *encodeState) marshalWith(ti *EncTypeInfo, ptr unsafe.Pointer) ([]byte, error) {
	hint := int(ti.AdaptiveHint.Load())
	if hint == 0 {
		hint = encodingSizeHint(ti, ptr)
		ti.AdaptiveHint.Store(int64(hint))
	}

	if es.bufSize > 0 {
		if cap(es.buf) < es.bufSize {
			es.buf = gort.MakeDirtyBytes(0, es.bufSize)
		}
	} else {
		es.growBuf(hint)
	}

	if err := es.encodeTop(ti, ptr); err != nil {
		return nil, err
	}

	n := len(es.buf)
	adapted := n + n/32 // +3% headroom
	if adapted > hint {
		ti.AdaptiveHint.Store(int64(adapted))
	}

	if es.bufSize > 0 {
		// Tight copy: allocate exactly n bytes for the caller;
		// es.buf keeps its full capacity for pool reuse.
		result := make([]byte, n)
		copy(result, es.buf)
		es.buf = es.buf[:0]
		return result, nil
	}

	result := es.buf[:n:n] // cap=n prevents caller append from corrupting pool buffer
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

func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Pointer {
		return appendMarshalSlow(dst, v, rt, opts)
	}

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
