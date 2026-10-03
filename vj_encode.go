package vjson

import (
	"io"

	"github.com/velox-io/json/venc"
	"github.com/velox-io/json/vopt"
)

// MarshalOption aliases Options, retained for code written against the
// encode-specific name.
type MarshalOption = Options

// EscapeHTML controls escaping of <, >, and & in encoded strings. Default
// false: escaping for an HTML embedding context is the embedder's concern,
// not a JSON encoder's.
func EscapeHTML(v bool) Options { return vopt.EscapeHTML(v) }

// EscapeLineTerms controls escaping of U+2028 and U+2029. Default false.
func EscapeLineTerms(v bool) Options { return vopt.EscapeLineTerms(v) }

// FloatExpAuto selects scientific notation for floats with |f| < 1e-6 or
// |f| >= 1e21. Default false: floats always use fixed-point notation.
func FloatExpAuto(v bool) Options { return vopt.FloatExpAuto(v) }

// BufSize fixes the starting size of the encoder's working buffer and opts the
// call out of the zero-copy return: the result is copied into a tight-fit
// allocation so the pooled buffer keeps its capacity.
func BufSize(n int) Options { return vopt.BufSize(n) }

// Marshal returns the compact JSON encoding of v.
func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	return venc.Marshal(v, opts...)
}

// MarshalIndent returns the indented JSON encoding of v.
func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	return venc.MarshalIndent(v, prefix, indent, opts...)
}

// AppendMarshal appends the compact JSON encoding of v to dst.
func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	return venc.AppendMarshal(dst, v, opts...)
}

// Encoder writes JSON values to an output stream.
// Each Encode call writes one JSON value followed by a newline.
type Encoder = venc.Encoder

// NewEncoder creates an Encoder that writes to w. opts carry the same option
// set as Marshal; options that do not apply to encoding are ignored.
func NewEncoder(w io.Writer, opts ...Options) *Encoder {
	return venc.NewEncoder(w, opts...)
}

// EncodeValue is a generic, zero-allocation alternative to [Encoder.Encode].
func EncodeValue[T any](enc *Encoder, v T) error {
	return venc.EncodeValue(enc, v)
}
