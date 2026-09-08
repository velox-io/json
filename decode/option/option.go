// Package option defines decode options shared by bind, dom, and package vjson.
// Each decoder applies the Config fields relevant to its execution path.
package option

import "errors"

// ErrZeroCopyNeedsPadded reports that zero-copy strings require a caller-owned
// padded buffer. Every entry that copies or relocates its input rejects
// WithZeroCopy with this error.
var ErrZeroCopyNeedsPadded = errors.New("vjson: WithZeroCopy requires a padded caller-owned buffer")

// Config holds the resolved option state for one decode call.
type Config struct {
	// ZeroCopy lets escape-free strings alias the padded source in
	// dom.ParsePadded and bind.UnmarshalPadded. The decoded values keep the
	// source's backing reachable; callers preserve its bytes while any
	// decoded value remains reachable. Every other decode path rejects the
	// option with ErrZeroCopyNeedsPadded.
	ZeroCopy bool

	// UseNumber decodes any/interface{} numbers as json.Number instead of
	// float64. Bind only.
	UseNumber bool

	// DisallowUnknownFields fails decoding when a JSON object contains a
	// field with no matching Go struct field. Mirrors encoding/json's
	// Decoder.DisallowUnknownFields. Bind only.
	DisallowUnknown bool

	// StrictScan validates raw UTF-8 and rejects unescaped C0 control bytes
	// during native scanning. Bind and dom use the lax scan by default.
	StrictScan bool

	SkipLenient bool
}

// Option is value-in/value-out (not func(*Config)) so Apply never passes a
// pointer to its local Config into an indirect call: c stays stack-resident
// regardless of how many opts are applied.
type Option func(Config) Config

// WithZeroCopy arms the zero-copy string path. Honored by dom.ParsePadded and
// bind.UnmarshalPadded; escaped strings still decode through the string arena.
// Entries with a different input model reject it with ErrZeroCopyNeedsPadded,
// and bind additionally rejects trees carrying value.Value or poly fields
// with bind.ErrZeroCopyTypedTree.
func WithZeroCopy() Option {
	return func(c Config) Config {
		c.ZeroCopy = true
		return c
	}
}

// WithUseNumber arms json.Number decoding for any/interface{} fields.
func WithUseNumber() Option {
	return func(c Config) Config {
		c.UseNumber = true
		return c
	}
}

// WithDisallowUnknownFields arms the unknown-field rejection check.
func WithDisallowUnknownFields() Option {
	return func(c Config) Config {
		c.DisallowUnknown = true
		return c
	}
}

// WithStrictScan validates raw UTF-8 and rejects unescaped C0 control bytes
// during native bind and DOM scans.
func WithStrictScan() Option {
	return func(c Config) Config {
		c.StrictScan = true
		return c
	}
}

// Apply resolves opts into a Config.
func Apply(opts []Option) Config {
	var c Config
	for _, o := range opts {
		c = o(c)
	}
	return c
}
