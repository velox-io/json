package vjson

import (
	"io"

	"github.com/velox-io/json/vdec"
)

// DecoderOption configures a [Decoder].
type DecoderOption = vdec.DecoderOption

// Decoder reads and decodes JSON values from an input stream.
type Decoder = vdec.Decoder

// NewDecoder creates a Decoder that reads from r.
func NewDecoder(r io.Reader, opts ...DecoderOption) *Decoder {
	return vdec.NewDecoder(r, opts...)
}

// WithBufferSize sets the initial read buffer size (default 128 KB).
func WithBufferSize(size int) DecoderOption { return vdec.WithBufferSize(size) }

// WithSkipErrors enables skip-on-error recovery for NDJSON streams.
func WithSkipErrors(fn func(err error) bool) DecoderOption { return vdec.WithSkipErrors(fn) }

// DecoderCopyString causes all decoded strings to be heap-copied.
func DecoderCopyString() DecoderOption { return vdec.DecoderCopyString() }

// WithExpectedSize hints the total input size (e.g. HTTP Content-Length).
func WithExpectedSize(size int) DecoderOption { return vdec.WithExpectedSize(size) }

// DecodeValue is a generic convenience wrapper around [Decoder.Decode].
func DecodeValue[T any](d *Decoder, v *T) error {
	return d.Decode(v)
}
