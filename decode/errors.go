package decode

import (
	"errors"
	"fmt"

	"github.com/velox-io/json/jerr"
)

var (
	// ErrNoNative is returned by DOM parse entry points when the native parser
	// is unavailable on the current platform.
	ErrNoNative = errors.New("decode: native parser not available on this platform")

	// ErrEmptyInput is returned by DOM parse entry points for empty input.
	ErrEmptyInput = errors.New("decode: empty input")

	// ErrUnexpectedEOF is the shared sentinel used by parsers to signal
	// truncated input. Aliases jerr.ErrUnexpectedEOF so callers can compare
	// with either package's variable.
	ErrUnexpectedEOF = jerr.ErrUnexpectedEOF
)

// Shared decoder error aliases preserve one concrete type and the jerr
// errors.As bridge to encoding/json errors.
type (
	SyntaxError           = jerr.SyntaxError
	UnmarshalTypeError    = jerr.UnmarshalTypeError
	InvalidUnmarshalError = jerr.InvalidUnmarshalError
)

// UnknownFieldError reports a JSON field that has no matching Go struct
// field when strict decoding is enabled. There is no stdlib counterpart, so
// this type lives here rather than in jerr.
type UnknownFieldError struct {
	Field  string
	Struct string
	Offset int64
}

func (e *UnknownFieldError) Error() string {
	if e.Struct != "" {
		return fmt.Sprintf("decode: unknown field %q in %s at offset %d", e.Field, e.Struct, e.Offset)
	}
	return fmt.Sprintf("decode: unknown field %q at offset %d", e.Field, e.Offset)
}
