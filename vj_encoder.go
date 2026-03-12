package vjson

import (
	"io"
	"reflect"
	"runtime"
	"unsafe"
)

type EncoderOption func(*Encoder)

func EncoderSetIndent(prefix, indent string) EncoderOption {
	return func(enc *Encoder) {
		enc.prefix = prefix
		enc.indent = indent
	}
}

func EncoderSetEscapeHTML(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= escapeHTML
		} else {
			enc.flags &^= escapeHTML
		}
	}
}

func EncoderSetEscapeLineTerms(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= escapeLineTerms
		} else {
			enc.flags &^= escapeLineTerms
		}
	}
}

// Encoder writes JSON values to an output stream.
// Each Encode call writes one JSON value followed by a newline.
type Encoder struct {
	w      io.Writer
	err    error // sticky write error
	prefix string
	indent string
	flags  escapeFlags
}

// NewEncoder creates an Encoder that writes to w.
func NewEncoder(w io.Writer, opts ...EncoderOption) *Encoder {
	enc := &Encoder{
		w:     w,
		flags: escapeDefault,
	}
	for _, opt := range opts {
		opt(enc)
	}
	return enc
}

func (enc *Encoder) SetIndent(prefix, indent string) {
	enc.prefix = prefix
	enc.indent = indent
}

func (enc *Encoder) SetEscapeHTML(on bool) {
	if on {
		enc.flags |= escapeHTML
	} else {
		enc.flags &^= escapeHTML
	}
}

func (enc *Encoder) SetEscapeLineTerms(on bool) {
	if on {
		enc.flags |= escapeLineTerms
	} else {
		enc.flags &^= escapeLineTerms
	}
}

// Encode writes the JSON encoding of v followed by a newline.
// Write errors are sticky — once a write fails, subsequent calls
// return the same error without encoding.
func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		// Untyped nil (e.g. enc.Encode(nil)); match encoding/json behavior.
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
		// reflect forbids taking the address of a non-addressable Value.
		// Rather than copying via reflect.New+Set, we extract the data
		// pointer straight from the interface's eface layout {_type, data}.
		ptr = (*[2]unsafe.Pointer)(unsafe.Pointer(&v))[1]
		elemType = rv.Type()
	}

	err := enc.encodePtr(GetCodec(elemType), ptr)
	// Keep v alive so the GC does not collect the eface data pointer
	// while encodePtr is using it.
	runtime.KeepAlive(v)
	return err
}

// EncodeValue is a generic, zero-allocation alternative to [Encoder.Encode].
// Because the type parameter provides compile-time type information and the
// caller passes a typed pointer directly, it avoids interface boxing, reflect
// overhead, and the eface data-pointer extraction needed by Encode.
func EncodeValue[T any](enc *Encoder, v *T) error {
	if enc.err != nil {
		return enc.err
	}
	return enc.encodePtr(GetCodec(reflect.TypeFor[T]()), unsafe.Pointer(v))
}

// encodePtr is the shared encoding core for Encode and EncodeValue.
func (enc *Encoder) encodePtr(ti *TypeInfo, ptr unsafe.Pointer) error {
	m := getMarshaler()
	m.flags = enc.flags
	m.prefix = enc.prefix
	m.indent = enc.indent

	if err := m.encodeValue(ti, ptr); err != nil {
		putMarshaler(m)
		return err
	}

	m.buf = append(m.buf, '\n')

	err := enc.write(m.buf)
	putMarshaler(m)
	return err
}

// write writes p to enc.w, making any error sticky.
func (enc *Encoder) write(p []byte) error {
	_, err := enc.w.Write(p)
	if err != nil {
		enc.err = err
	}
	return err
}
