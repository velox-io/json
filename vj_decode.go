package vjson

import (
	"io"

	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vopt"
)

// Options is the single option set accepted by every entry point: Marshal,
// MarshalIndent, AppendMarshal, Unmarshal, UnmarshalPadded, Parse, and
// ParsePadded. Options that do not apply to a given operation are ignored.
// Later values override earlier ones.
type Options = vopt.Options

// Option aliases Options, retained for code written against the singular name.
type Option = Options

// UnmarshalOption aliases Options, retained for source compatibility with
// code written against the bind-specific type.
type UnmarshalOption = Options

// Join merges opts into one Options. A later value overrides an earlier one
// for the options it names and leaves the rest untouched, so a joined set can
// be stored once and still be overridden per call.
func Join(opts ...Options) Options { return vopt.Join(opts...) }

// AllowInvalidUTF8 controls invalid UTF-8 handling on both sides. When false,
// decoding rejects invalid byte sequences and unescaped C0 control bytes, and
// encoding replaces invalid bytes with U+FFFD. Default true: bytes pass
// through verbatim, so the decoder never rewrites its input and the encoder
// takes its fastest string path. The JSON syntax stays valid either way.
func AllowInvalidUTF8(v bool) Options { return vopt.AllowInvalidUTF8(v) }

// UseNumber decodes numbers bound to any as json.Number rather than float64,
// preserving the literal text and its full precision.
func UseNumber(v bool) Options { return vopt.UseNumber(v) }

// RejectUnknownMembers fails a decode that meets a JSON object member with no
// matching Go struct field.
func RejectUnknownMembers(v bool) Options { return vopt.RejectUnknownMembers(v) }

// ZeroCopy selects the backing for escape-free decoded strings. Left unset,
// each entry applies its input-model default: Unmarshal and UnmarshalPadded
// alias the caller-owned input, while entries whose input relocates across
// windows copy. ZeroCopy(false) selects the copying parse. ZeroCopy(true) is a
// demand that entries unable to alias reject with ErrZeroCopyUnsupported. On
// ParsePadded it is an opt-in that makes the returned Value navigation-only.
func ZeroCopy(v bool) Options { return vopt.ZeroCopy(v) }

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
// value remains reachable. ZeroCopy(false) selects the copying parse.
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
// Values parsed with ZeroCopy(true) are rejected with ErrZeroCopyValue.
func UnmarshalValue[T any](v Value, out T, opts ...Option) (err error) {
	err = bind.UnmarshalValue(v, out, opts...)
	return
}

// ErrZeroCopyValue aliases bind.ErrZeroCopyValue: UnmarshalValue rejects
// navigation-only Values whose document was parsed with ZeroCopy(true).
var ErrZeroCopyValue = bind.ErrZeroCopyValue

// ErrZeroCopyUnsupported aliases vopt.ErrZeroCopyUnsupported: entries whose
// input relocates across windows or is not caller bytes reject an explicit
// ZeroCopy(true) demand.
var ErrZeroCopyUnsupported = vopt.ErrZeroCopyUnsupported

// ErrZeroCopyTypedTree aliases bind.ErrZeroCopyTypedTree: Unmarshal and
// UnmarshalPadded reject an explicit ZeroCopy(true) demand on trees
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
// string arena, and ZeroCopy(false) selects the copying parse. Trees
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
