package vjson

import (
	"io"

	"github.com/velox-io/json/venc"
)

// MarshalOption configures encoding behavior.
type MarshalOption = venc.MarshalOption

// WithEscapeHTML enables escaping of <, >, & in strings.
func WithEscapeHTML() MarshalOption { return venc.WithEscapeHTML() }

// WithoutEscapeHTML disables escaping of <, >, &.
func WithoutEscapeHTML() MarshalOption { return venc.WithoutEscapeHTML() }

// WithEscapeLineTerms enables escaping of U+2028 and U+2029 line terminators in strings.
func WithEscapeLineTerms() MarshalOption { return venc.WithEscapeLineTerms() }

// WithoutEscapeLineTerms disables escaping of U+2028 and U+2029.
func WithoutEscapeLineTerms() MarshalOption { return venc.WithoutEscapeLineTerms() }

// WithUTF8Correction enables replacing invalid UTF-8 with U+FFFD in strings.
// The replacement format (raw U+FFFD bytes on Go 1.27+, \ufffd escape on
// earlier versions) is build-tag selected to match encoding/json.
func WithUTF8Correction() MarshalOption { return venc.WithUTF8Correction() }

// WithoutUTF8Correction disables replacing invalid UTF-8 in strings.
func WithoutUTF8Correction() MarshalOption { return venc.WithoutUTF8Correction() }

// WithStdCompat enables full encoding/json compatibility.
func WithStdCompat() MarshalOption { return venc.WithStdCompat() }

// WithFloatExpAuto enables encoding/json-compatible scientific notation
// for floats with |f| < 1e-6 or |f| >= 1e21 (e.g. 1e-7, 1e+21).
// By default, floats are always formatted in fixed-point notation.
func WithFloatExpAuto() MarshalOption { return venc.WithFloatExpAuto() }

// WithFastEscape disables all string-level escape features
// (UTF-8 validation, line terminator escaping, HTML escaping).
// Only mandatory JSON escapes (control chars, '"', '\\') are performed.
// This enables the fastest string encoding path in the native encoder.
func WithFastEscape() MarshalOption { return venc.WithFastEscape() }

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
