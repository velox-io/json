package vjson

import (
	"io"

	"github.com/velox-io/json/venc"
)

// Encoder writes JSON values to an output stream.
// Each Encode call writes one JSON value followed by a newline.
type Encoder = venc.Encoder

// EncoderOption configures an [Encoder].
type EncoderOption = venc.EncoderOption

// NewEncoder creates an Encoder that writes to w.
func NewEncoder(w io.Writer, opts ...EncoderOption) *Encoder {
	return venc.NewEncoder(w, opts...)
}

// EncoderSetIndent sets the indentation prefix and step for a new [Encoder].
func EncoderSetIndent(prefix, indent string) EncoderOption {
	return venc.EncoderSetIndent(prefix, indent)
}

// EncoderSetEscapeHTML enables or disables escaping of <, >, and &.
func EncoderSetEscapeHTML(on bool) EncoderOption {
	return venc.EncoderSetEscapeHTML(on)
}

// EncoderSetEscapeLineTerms enables or disables escaping of U+2028 and U+2029.
func EncoderSetEscapeLineTerms(on bool) EncoderOption {
	return venc.EncoderSetEscapeLineTerms(on)
}

// EncoderSetFloatExpAuto enables encoding/json-compatible scientific notation
// for floats with |f| < 1e-6 or |f| >= 1e21.
func EncoderSetFloatExpAuto(on bool) EncoderOption {
	return venc.EncoderSetFloatExpAuto(on)
}

// EncodeValue is a generic, zero-allocation alternative to [Encoder.Encode].
func EncodeValue[T any](enc *Encoder, v T) error {
	return venc.EncodeValue(enc, v)
}
