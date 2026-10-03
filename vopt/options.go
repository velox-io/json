// Package vopt defines the single option set shared by encoding and decoding.
//
// An Options value carries a presence mask and a value mask, so an option that
// was never named is distinguishable from one explicitly set to false. Entry
// points take a variadic list and resolve it with Join: later values override
// earlier ones, and options that do not apply to an operation are ignored.
package vopt

import "errors"

// ErrZeroCopyUnsupported reports that zero-copy strings require a caller-owned
// input buffer. Entries whose input relocates across windows or is not caller
// bytes reject an explicit ZeroCopy(true) demand with this error.
var ErrZeroCopyUnsupported = errors.New("vjson: ZeroCopy(true) requires a caller-owned input buffer")

// Flag identifies one boolean option.
type Flag uint32

const (
	// FlagAllowInvalidUTF8 governs invalid UTF-8 handling on both sides.
	// Decoding rejects invalid sequences when false; encoding replaces them
	// with U+FFFD when false. Default true: bytes pass through verbatim.
	FlagAllowInvalidUTF8 Flag = 1 << iota

	// FlagEscapeHTML escapes <, >, and & in encoded strings. Encode only.
	FlagEscapeHTML
	// FlagEscapeLineTerms escapes U+2028 and U+2029. Encode only.
	FlagEscapeLineTerms
	// FlagFloatExpAuto selects scientific notation for |f| < 1e-6 or
	// |f| >= 1e21. Encode only.
	FlagFloatExpAuto

	// FlagUseNumber decodes numbers bound to any as json.Number rather than
	// float64. Decode only.
	FlagUseNumber
	// FlagRejectUnknownMembers fails a decode that meets a JSON object member
	// with no matching Go struct field. Decode only.
	FlagRejectUnknownMembers
	// FlagZeroCopy demands or disables aliasing decoded strings into the
	// caller's input buffer. Unset leaves each entry its input-model default.
	// Decode only.
	FlagZeroCopy
	// FlagSkipLenient counts brackets over a skipped value without validating
	// that value's scalar or the comma order inside its containers.
	// Decode only.
	FlagSkipLenient

	// flagIndent marks indentPrefix/indentStep as carried. Encode only.
	flagIndent
	// flagBufSize marks bufSize as carried. Encode only.
	flagBufSize
)

// Options is one option or a set of them. The zero value names nothing and
// leaves every default in place.
type Options struct {
	set Flag // which options this value names
	val Flag // the value of each named option

	bufSize      int
	indentPrefix string
	indentStep   string
}

// boolOpt builds a single-entry Options for f.
func boolOpt(f Flag, v bool) Options {
	o := Options{set: f}
	if v {
		o.val = f
	}
	return o
}

// AllowInvalidUTF8 controls invalid UTF-8 handling. When false, decoding
// rejects invalid byte sequences and unescaped C0 control bytes, and encoding
// replaces invalid bytes with U+FFFD. Default true: bytes pass through
// verbatim, which is the fastest path and keeps the decoder from rewriting its
// input. The JSON syntax stays valid either way.
func AllowInvalidUTF8(v bool) Options { return boolOpt(FlagAllowInvalidUTF8, v) }

// EscapeHTML controls escaping of <, >, and & in encoded strings. Default
// false: escaping for an HTML embedding context is the embedder's concern,
// not a JSON encoder's.
func EscapeHTML(v bool) Options { return boolOpt(FlagEscapeHTML, v) }

// EscapeLineTerms controls escaping of U+2028 and U+2029. Default false.
func EscapeLineTerms(v bool) Options { return boolOpt(FlagEscapeLineTerms, v) }

// FloatExpAuto selects scientific notation for floats with |f| < 1e-6 or
// |f| >= 1e21. Default false: floats always use fixed-point notation.
func FloatExpAuto(v bool) Options { return boolOpt(FlagFloatExpAuto, v) }

// UseNumber decodes numbers bound to any as json.Number rather than float64,
// preserving the literal text and its full precision.
func UseNumber(v bool) Options { return boolOpt(FlagUseNumber, v) }

// RejectUnknownMembers fails a decode that meets a JSON object member with no
// matching Go struct field.
func RejectUnknownMembers(v bool) Options { return boolOpt(FlagRejectUnknownMembers, v) }

// ZeroCopy selects the backing for escape-free decoded strings. Left unset,
// each entry applies its input-model default: entries over caller-owned bytes
// alias that buffer, and entries whose input relocates across windows copy.
// ZeroCopy(false) selects the copying parse. ZeroCopy(true) is a demand that
// entries unable to alias reject with ErrZeroCopyUnsupported.
func ZeroCopy(v bool) Options { return boolOpt(FlagZeroCopy, v) }

// SkipLenient selects how a decode passes over the values it does not bind.
// Lenient counts brackets and trusts the rest, so a malformed token or comma
// inside a skipped region goes unreported. See vjson.SkipLenient for the sites
// it governs.
func SkipLenient(v bool) Options { return boolOpt(FlagSkipLenient, v) }

// Indent sets the per-line prefix and the per-depth indentation step.
func Indent(prefix, step string) Options {
	return Options{set: flagIndent, val: flagIndent, indentPrefix: prefix, indentStep: step}
}

// BufSize fixes the starting size of the encoder's working buffer and opts the
// call out of the zero-copy return: the result is copied into a tight-fit
// allocation so the pooled buffer keeps its capacity.
func BufSize(n int) Options {
	return Options{set: flagBufSize, val: flagBufSize, bufSize: n}
}

// Join merges opts left to right. A later value overrides an earlier one for
// the options it names, and leaves the rest untouched.
func Join(opts ...Options) Options {
	var dst Options
	for _, o := range opts {
		dst.set |= o.set
		dst.val = dst.val&^o.set | o.val
		if o.set&flagIndent != 0 {
			dst.indentPrefix, dst.indentStep = o.indentPrefix, o.indentStep
		}
		if o.set&flagBufSize != 0 {
			dst.bufSize = o.bufSize
		}
	}
	return dst
}

// Has reports whether f was explicitly named.
func (o Options) Has(f Flag) bool { return o.set&f != 0 }

// Enabled reports whether f was named and set to true.
func (o Options) Enabled(f Flag) bool { return o.val&f != 0 }

// Get returns the value of f and whether it was named.
func (o Options) Get(f Flag) (value, ok bool) { return o.val&f != 0, o.set&f != 0 }

// Indent returns the carried indentation and whether any was set.
func (o Options) Indent() (prefix, step string, ok bool) {
	return o.indentPrefix, o.indentStep, o.set&flagIndent != 0
}

// BufSize returns the carried starting buffer size, or 0 if none was set.
func (o Options) BufSize() int { return o.bufSize }
