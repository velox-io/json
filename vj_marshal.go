package vjson

import "github.com/velox-io/json/venc"

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

// WithUTF8Correction enables replacing invalid UTF-8 with \ufffd in strings.
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
