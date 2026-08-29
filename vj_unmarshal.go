//go:build !vdec

package vjson

import (
	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/native/ndec"
)

// Option is the functional-option type accepted by Unmarshal,
// UnmarshalPadded, Parse, and ParsePadded. An Option value can be passed
// to any of them; options that don't apply to a given decoder are ignored.
type Option = option.Option

// UnmarshalOption aliases Option, retained for source compatibility with
// code written against the bind-specific type.
type UnmarshalOption = Option

// WithUseNumber aliases option.WithUseNumber.
func WithUseNumber() Option { return option.WithUseNumber() }

// WithDisallowUnknownFields aliases option.WithDisallowUnknownFields.
func WithDisallowUnknownFields() Option { return option.WithDisallowUnknownFields() }

// WithStrictScan validates raw UTF-8 and rejects unescaped C0 control bytes
// in JSON strings during the native root scan.
func WithStrictScan() Option { return option.WithStrictScan() }

// PaddingSize is the minimum number of 0x20 padding bytes a buffer must
// carry past its length to be usable with UnmarshalPadded / ParsePadded.
// Callers that manage their own buffer without calling Pad must reserve at
// least this many trailing bytes.
const PaddingSize = ndec.BindScanPad

// Unmarshal parses JSON data into v.
func Unmarshal[T any](data []byte, v T, opts ...Option) (err error) {
	err = bind.Unmarshal(data, v, opts...)
	return
}

// UnmarshalValue binds a pre-built value.Value (tape) into v. The Value's
// tape is walked by the native tape-bind sub-routine, so every kind is
// supported (struct/slice/array/map/pointer/any/scalar, plus nested
// variant/kindof and value.Value fields). See decode/bind.UnmarshalValue.
// Values parsed with WithZeroCopy are rejected with ErrZeroCopyValue.
func UnmarshalValue[T any](v Value, out T, opts ...Option) (err error) {
	err = bind.UnmarshalValue(v, out, opts...)
	return
}

// ErrZeroCopyValue aliases bind.ErrZeroCopyValue: UnmarshalValue rejects
// navigation-only Values whose document was parsed with WithZeroCopy.
var ErrZeroCopyValue = bind.ErrZeroCopyValue

// Pad returns a buffer holding data followed by PaddingSize bytes of 0x20
// scan sentinel, suitable for UnmarshalPadded and ParsePadded.
func Pad(data []byte) []byte { return bind.Pad(data) }

// UnmarshalPadded parses JSON data into v using a caller-padded buffer.
// paddedData must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it.
//
// UnmarshalPadded is the entry point for future zero-copy string
// optimizations. The current implementation still copies through an internal
// buffer; the contract is in place so callers can opt in early.
func UnmarshalPadded[T any](paddedData []byte, v T, opts ...Option) (err error) {
	err = bind.UnmarshalPadded(paddedData, v, opts...)
	return
}
