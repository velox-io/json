package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
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

func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	for _, o := range opts {
		o(es)
	}

	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		rtp := *(*uintptr)(unsafe.Add(unsafe.Pointer(&rt), unsafe.Sizeof(uintptr(0))))
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr != nil {
			ti := encElemTypeInfoOf(rtp, rt)

			hint := encodingSizeHint(ti, elemPtr)
			if hint > cap(es.buf) {
				es.buf = gort.MakeDirtyBytes(0, max(encBufInitSize, hint))
			}

			err := es.encodeTop(ti, elemPtr)

			if err != nil {
				releaseEncodeState(es)
				return nil, err
			}

			if n := int64(len(es.buf)); n > ti.AdaptiveHint.Load() {
				ti.AdaptiveHint.Store(n + n/20) // +5% headroom to avoid BufFull on next call
			}

			return es.finalize(), nil
		}
	}

	// Slow path: non-pointer T or nil pointer — v escapes here, which is fine.
	return marshalSlow(es, v)
}

func marshalSlow[T any](m *encodeState, v T) ([]byte, error) {
	ti, ptr := encodingTarget(v)

	hint := encodingSizeHint(ti, ptr)
	if hint > cap(m.buf) {
		m.buf = gort.MakeDirtyBytes(0, max(encBufInitSize, hint))
	}

	if err := m.encodeTop(ti, ptr); err != nil {
		releaseEncodeState(m)
		return nil, err
	}

	if n := int64(len(m.buf)); n > ti.AdaptiveHint.Load() {
		ti.AdaptiveHint.Store(n + n/20) // +5% headroom to avoid BufFull on next call
	}

	return m.finalize(), nil
}

func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	for _, o := range opts {
		o(es)
	}

	es.indentPrefix = prefix
	es.indentString = indent
	es.nativeIndent = encvm.Available && isSimpleIndent(prefix, indent) > 0

	ti, ptr := encodingTarget(v)
	if err := es.encodeTop(ti, ptr); err != nil {
		releaseEncodeState(es)
		return nil, err
	}

	return es.finalize(), nil
}

func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	for _, o := range opts {
		o(es)
	}

	es.buf = dst

	ti, ptr := encodingTarget(v)
	if err := es.encodeTop(ti, ptr); err != nil {
		es.buf = nil // detach caller's buffer before pooling
		releaseEncodeState(es)
		return dst, err
	}

	result := es.buf
	es.buf = nil // detach caller's buffer before pooling
	releaseEncodeState(es)
	return result, nil
}
