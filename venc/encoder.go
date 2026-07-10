package venc

import (
	"io"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
)

type EncoderOption func(*Encoder)

func EncoderSetIndent(prefix, indent string) EncoderOption {
	return func(enc *Encoder) {
		enc.indentPrefix = prefix
		enc.indentString = indent
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

func EncoderSetFloatExpAuto(on bool) EncoderOption {
	return func(enc *Encoder) {
		if on {
			enc.flags |= EncFloatExpAuto
		} else {
			enc.flags &^= EncFloatExpAuto
		}
	}
}

type Encoder struct {
	w            io.Writer
	err          error // sticky write error
	indentPrefix string
	indentString string
	flags        uint32 // escapeFlags (bits 0-2) | vjEncFloatExpAuto (bit 3)
}

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
	enc.indentPrefix = prefix
	enc.indentString = indent
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

func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}

	rt := reflect.TypeOf(v)
	if rt == nil {
		return enc.write([]byte("null\n"))
	}

	var ptr unsafe.Pointer
	var ti *EncTypeInfo
	eface := (*[2]unsafe.Pointer)(unsafe.Pointer(&v))
	data := eface[1]

	if rt.Kind() == reflect.Pointer {
		// data is the pointer value itself. Nil pointer → null.
		if data == nil {
			return enc.write([]byte("null\n"))
		}
		ptr = data
		rtp := uintptr(gort.TypePtr(rt))
		ti = encElemTypeInfoOf(rtp, rt)
	} else {
		ti = EncTypeInfoOf(rt)
		switch rt.Kind() {
		case reflect.Map, reflect.Chan, reflect.Func:
			// Direct-interface types: data IS the value (a pointer-width
			// descriptor). Encoder expects a pointer TO the value, so take &eface[1].
			ptr = unsafe.Pointer(&eface[1])
		default:
			// Indirect types: data is already a pointer to the value.
			ptr = data
		}
	}

	err := enc.encodePtr(ti, ptr)
	runtime.KeepAlive(v)
	return err
}

func EncodeValue[T any](enc *Encoder, v T) error {
	if enc.err != nil {
		return enc.err
	}
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Pointer {
		return encodeValueSlow(enc, v, rt)
	}

	elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
	if elemPtr == nil {
		return enc.write([]byte("null\n"))
	}
	rtp := uintptr(gort.TypePtr(rt))
	ti := encElemTypeInfoOf(rtp, rt)
	return enc.encodePtr(ti, elemPtr)
}

func encodeValueSlow[T any](enc *Encoder, v T, rt reflect.Type) error {
	return encodeValueSlowPtr(enc, &v, rt)
}

func encodeValueSlowPtr[T any](enc *Encoder, v *T, rt reflect.Type) error {
	ti := EncTypeInfoOf(rt)
	return enc.encodePtr(ti, unsafe.Pointer(v))
}

func (enc *Encoder) encodePtr(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	es := acquireEncodeState()
	defer releaseEncodeState(es)

	es.flags = enc.flags
	es.indentPrefix = enc.indentPrefix
	es.indentString = enc.indentString
	if enc.indentString != "" {
		es.nativeIndent = encvm.Available && isSimpleIndent(enc.indentPrefix, enc.indentString) > 0
	}

	hint := encodingSizeHint(ti, ptr)

	// flushFn returns the writer's n so flush() can retain the unwritten
	// tail on a short write (io.Writer may legally return n < len(p),
	// err == nil).
	es.flushFn = func(p []byte) (int, error) {
		n, err := enc.w.Write(p)
		if err != nil {
			enc.err = err
		}
		return n, err
	}

	// Use a dedicated streaming buffer, isolated from Marshal's zero-copy
	// erosion. marshalWith returns es.buf[:n:n] and advances the base via
	// es.buf[n:], shrinking cap on the pooled object; the streaming path
	// never lends its buffer to a caller, so it must not inherit that eroded
	// cap (a too-small cap is what triggered the BUF_FULL storm). Swap
	// es.buf onto streamBuf for the duration of the encode, capturing the
	// (possibly grown) buffer back into es.streamBuf on exit and restoring
	// es.buf so releaseEncodeState returns the marshal buffer untouched.
	marshalBuf := es.buf
	es.buf = es.acquireStreamBuf()
	defer func() {
		es.streamBuf = es.buf
		es.buf = marshalBuf
	}()

	es.growBuf(hint)

	if err := es.encodeTop(ti, ptr); err != nil {
		return enc.stickyErr(err)
	}

	es.buf = append(es.buf, '\n')

	// Write out the trailing bytes in full, accounting for short writes
	// (io.Writer may legally return n < len(p), err == nil).
	if err := writeAll(enc.w, es.buf); err != nil {
		return enc.stickyErr(err)
	}

	return nil
}

// writeAll writes p to w, retrying the unwritten tail on short writes until
// all bytes are consumed or an error occurs.
func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
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
