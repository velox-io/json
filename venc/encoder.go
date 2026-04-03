package venc

import (
	"io"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// EncoderOption configures an [Encoder].
type EncoderOption func(*Encoder)

// EncoderSetIndent configures pretty-print indentation.
func EncoderSetIndent(prefix, indent string) EncoderOption {
	return func(enc *Encoder) {
		enc.prefix = prefix
		enc.indent = indent
	}
}

// EncoderSetEscapeHTML toggles HTML escaping.
func EncoderSetEscapeHTML(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= uint32(escapeHTML)
		} else {
			enc.flags &^= uint32(escapeHTML)
		}
	}
}

// EncoderSetEscapeLineTerms toggles U+2028/U+2029 escaping.
func EncoderSetEscapeLineTerms(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= uint32(escapeLineTerms)
		} else {
			enc.flags &^= uint32(escapeLineTerms)
		}
	}
}

// EncoderSetFloatExpAuto matches encoding/json float thresholds.
func EncoderSetFloatExpAuto(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= vjEncFloatExpAuto
		} else {
			enc.flags &^= vjEncFloatExpAuto
		}
	}
}

// Encoder writes one JSON value plus a trailing newline per call.
type Encoder struct {
	w      io.Writer
	err    error // sticky write error
	prefix string
	indent string
	flags  uint32 // escapeFlags (bits 0-2) | vjEncFloatExpAuto (bit 3)
}

// NewEncoder builds a streaming encoder for w.
func NewEncoder(w io.Writer, opts ...EncoderOption) *Encoder {
	enc := &Encoder{
		w: w,
	}
	for _, opt := range opts {
		opt(enc)
	}
	return enc
}

// SetIndent updates pretty-print indentation.
func (enc *Encoder) SetIndent(prefix, indent string) {
	enc.prefix = prefix
	enc.indent = indent
}

// SetEscapeHTML toggles HTML escaping.
func (enc *Encoder) SetEscapeHTML(on bool) {
	if on {
		enc.flags |= uint32(escapeHTML)
	} else {
		enc.flags &^= uint32(escapeHTML)
	}
}

// SetEscapeLineTerms toggles U+2028/U+2029 escaping.
func (enc *Encoder) SetEscapeLineTerms(on bool) {
	if on {
		enc.flags |= uint32(escapeLineTerms)
	} else {
		enc.flags &^= uint32(escapeLineTerms)
	}
}

// Encode writes v plus a trailing newline. Write errors stay sticky.
func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return enc.write([]byte("null\n"))
	}

	var ptr unsafe.Pointer
	var elemType reflect.Type
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return enc.write([]byte("null\n"))
		}
		ptr = rv.UnsafePointer()
		elemType = rv.Elem().Type()
	} else {
		elemType = rv.Type()
		// Direct-interface map/chan/func values need one extra indirection.
		efaceData := &(*[2]unsafe.Pointer)(unsafe.Pointer(&v))[1]
		switch rv.Kind() {
		case reflect.Map, reflect.Chan, reflect.Func:
			ptr = unsafe.Pointer(efaceData)
		default:
			ptr = *efaceData
		}
	}

	err := enc.encodePtr(EncTypeInfoOf(elemType), ptr)
	// Keep the interface payload alive while encodePtr uses its raw data pointer.
	runtime.KeepAlive(v)
	return err
}

// EncodeValue avoids interface boxing for generic callers.
func EncodeValue[T any](enc *Encoder, v T) error {
	if enc.err != nil {
		return enc.err
	}
	ti, ptr := marshalTarget(&v)
	return enc.encodePtr(ti, ptr)
}

// encodePtr shares the streaming encode path used by Encode and EncodeValue.
func (enc *Encoder) encodePtr(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	m := getMarshaler()
	m.flags = enc.flags
	m.prefix = enc.prefix
	m.indent = enc.indent
	if enc.indent != "" {
		m.nativeCompat = isSimpleIndent(enc.prefix, enc.indent) > 0
	}

	hint := marshalHint(ti, ptr)
	if hint > cap(m.buf) {
		m.buf = gort.MakeDirtyBytes(0, max(marshalBufInitSize, hint))
	}

	// Stream out completed chunks to keep memory bounded.
	m.flushFn = func(p []byte) error {
		_, err := enc.w.Write(p)
		if err != nil {
			enc.err = err
		}
		return err
	}

	if err := m.encodeTop(ti, ptr); err != nil {
		putMarshaler(m)
		return enc.stickyErr(err)
	}

	m.buf = append(m.buf, '\n')

	if len(m.buf) > 0 {
		if _, err := enc.w.Write(m.buf); err != nil {
			putMarshaler(m)
			return enc.stickyErr(err)
		}
	}

	putMarshaler(m)
	return nil
}

func (enc *Encoder) stickyErr(err error) error {
	if enc.err == nil {
		enc.err = err
	}
	return err
}

func (enc *Encoder) write(p []byte) error {
	_, err := enc.w.Write(p)
	if err != nil {
		enc.err = err
	}
	return err
}
