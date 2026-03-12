package vjson

import (
	"io"
	"reflect"
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

	var ptr unsafe.Pointer
	var elemType reflect.Type
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return enc.write([]byte("null\n"))
		}
		ptr = rv.UnsafePointer()
		elemType = rv.Elem().Type()
	} else {
		tmp := reflect.New(rv.Type())
		tmp.Elem().Set(rv)
		ptr = tmp.UnsafePointer()
		elemType = rv.Type()
	}

	ti := GetCodec(elemType)

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

// EncodeValue is a generic convenience wrapper around [Encoder.Encode].
func EncodeValue[T any](enc *Encoder, v *T) error {
	return enc.Encode(v)
}
