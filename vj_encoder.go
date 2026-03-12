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
			enc.flags |= uint32(escapeHTML)
		} else {
			enc.flags &^= uint32(escapeHTML)
		}
	}
}

func EncoderSetEscapeLineTerms(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= uint32(escapeLineTerms)
		} else {
			enc.flags &^= uint32(escapeLineTerms)
		}
	}
}

// EncoderSetFloatExpAuto enables encoding/json-compatible scientific notation
// for floats with |f| < 1e-6 or |f| >= 1e21.
func EncoderSetFloatExpAuto(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= vjEncFloatExpAuto
		} else {
			enc.flags &^= vjEncFloatExpAuto
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
	flags  uint32 // escapeFlags (bits 0-2) | vjEncFloatExpAuto (bit 3)
}

// NewEncoder creates an Encoder that writes to w.
func NewEncoder(w io.Writer, opts ...EncoderOption) *Encoder {
	enc := &Encoder{
		w: w,
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
		enc.flags |= uint32(escapeHTML)
	} else {
		enc.flags &^= uint32(escapeHTML)
	}
}

func (enc *Encoder) SetEscapeLineTerms(on bool) {
	if on {
		enc.flags |= uint32(escapeLineTerms)
	} else {
		enc.flags &^= uint32(escapeLineTerms)
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
//
// In streaming mode the marshaler flushes chunks directly to enc.w as
// the buffer fills, keeping memory usage bounded. The final residual
// data (including the trailing newline) is written after encoding
// completes.
func (enc *Encoder) encodePtr(ti *TypeInfo, ptr unsafe.Pointer) error {
	m := getMarshaler()
	m.flags = enc.flags
	m.prefix = enc.prefix
	m.indent = enc.indent

	// Enable streaming flush: instead of growing the buffer to hold
	// the entire output, flush completed chunks to the writer.
	// Write errors are made sticky so subsequent Encode calls fail fast.
	m.flushFn = func(p []byte) error {
		_, err := enc.w.Write(p)
		if err != nil {
			enc.err = err
		}
		return err
	}

	if err := m.encodeValue(ti, ptr); err != nil {
		putMarshaler(m)
		return enc.stickyErr(err)
	}

	m.buf = append(m.buf, '\n')

	// Flush any remaining data in the buffer.
	if len(m.buf) > 0 {
		if _, err := enc.w.Write(m.buf); err != nil {
			putMarshaler(m)
			return enc.stickyErr(err)
		}
	}

	putMarshaler(m)
	return nil
}

// stickyErr records err as the Encoder's sticky error and returns it.
func (enc *Encoder) stickyErr(err error) error {
	if enc.err == nil {
		enc.err = err
	}
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
