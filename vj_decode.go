package vjson

import (
	"io"

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

// WithZeroCopy selects the string backing for decode entries whose input is
// caller bytes. Unmarshal and UnmarshalPadded alias escape-free strings into
// that input by default; WithZeroCopy(false) selects the copying parse, and
// WithZeroCopy(true) is an explicit demand that entries whose input cannot
// alias reject with ErrZeroCopyUnsupported. On ParsePadded the option is an
// opt-in: WithZeroCopy(true) makes the returned Value navigation-only.
func WithZeroCopy(enabled bool) Option { return option.WithZeroCopy(enabled) }

// PaddingSize is the minimum number of 0x20 padding bytes a buffer must
// carry past its length to be usable with UnmarshalPadded / ParsePadded.
// Callers that manage their own buffer without calling Pad must reserve at
// least this many trailing bytes.
const PaddingSize = ndec.BindScanPad

// Unmarshal parses JSON data into v.
//
// Escape-free strings alias data's backing by default: the input is scanned
// through an internal padded copy, and each aliased span rebases into the
// caller-owned original. The caller preserves data's bytes while any decoded
// value remains reachable. WithZeroCopy(false) selects the copying parse.
// Trees carrying value.Value or poly fields stay arena-backed under the
// default and are rejected with ErrZeroCopyTypedTree when zero-copy is
// demanded explicitly.
func Unmarshal[T any](data []byte, v T, opts ...Option) (err error) {
	err = bind.Unmarshal(data, v, opts...)
	return
}

// UnmarshalValue binds a pre-built value.Value (tape) into v. The Value's
// tape is walked by the native tape-bind sub-routine, so every kind is
// supported (struct/slice/array/map/pointer/any/scalar, plus nested
// variant/kindof and value.Value fields). See decode/bind.UnmarshalValue.
// Values parsed with WithZeroCopy(true) are rejected with ErrZeroCopyValue.
func UnmarshalValue[T any](v Value, out T, opts ...Option) (err error) {
	err = bind.UnmarshalValue(v, out, opts...)
	return
}

// ErrZeroCopyValue aliases bind.ErrZeroCopyValue: UnmarshalValue rejects
// navigation-only Values whose document was parsed with WithZeroCopy(true).
var ErrZeroCopyValue = bind.ErrZeroCopyValue

// ErrZeroCopyUnsupported aliases option.ErrZeroCopyUnsupported: entries whose
// input relocates across windows or is not caller bytes reject an explicit
// WithZeroCopy(true) demand.
var ErrZeroCopyUnsupported = option.ErrZeroCopyUnsupported

// ErrZeroCopyTypedTree aliases bind.ErrZeroCopyTypedTree: Unmarshal and
// UnmarshalPadded reject an explicit WithZeroCopy(true) demand on trees
// carrying value.Value or poly fields.
var ErrZeroCopyTypedTree = bind.ErrZeroCopyTypedTree

// Pad returns a buffer holding data followed by PaddingSize bytes of 0x20
// scan sentinel, suitable for UnmarshalPadded and ParsePadded.
func Pad(data []byte) []byte { return bind.Pad(data) }

// UnmarshalPadded parses JSON data into v using a caller-padded buffer.
// paddedData must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it.
//
// Escape-free strings alias paddedData by default: decoded values keep its
// backing reachable, and the caller preserves its bytes while any decoded
// value remains reachable. Escaped strings still decode through the internal
// string arena, and WithZeroCopy(false) selects the copying parse. Trees
// carrying value.Value or poly fields stay arena-backed under the default
// and are rejected with ErrZeroCopyTypedTree when zero-copy is demanded
// explicitly.
func UnmarshalPadded[T any](paddedData []byte, v T, opts ...Option) (err error) {
	err = bind.UnmarshalPadded(paddedData, v, opts...)
	return
}

// DecoderOption configures a [Decoder].
type DecoderOption = bind.DecoderOption

// Decoder reads and decodes JSON values from an input stream.
type Decoder = bind.Decoder

// NewDecoder creates a Decoder that reads from r.
func NewDecoder(r io.Reader, opts ...DecoderOption) *Decoder {
	return bind.NewDecoder(r, opts...)
}

// WithBufferSize sets the initial window size (default 128 KB); the window
// still grows by doubling when a single value exceeds it.
func WithBufferSize(size int) DecoderOption { return bind.WithBufferSize(size) }

// WithSkipErrors enables skip-on-error recovery for NDJSON streams.
func WithSkipErrors(fn func(err error) bool) DecoderOption { return bind.WithSkipErrors(fn) }

// WithExpectedSize hints the expected value size; it raises the initial
// window to fit one value without regrowth.
func WithExpectedSize(size int) DecoderOption { return bind.WithExpectedSize(size) }

// DecodeValue is a generic convenience wrapper around [Decoder.Decode].
func DecodeValue[T any](d *Decoder, v *T) error {
	return bind.DecodeValue(d, v)
}
