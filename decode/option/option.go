// Package option defines decode options shared by bind, dom, and package vjson.
// Each decoder applies the Config fields relevant to its execution path.
package option

import "errors"

// ErrZeroCopyUnsupported reports that zero-copy strings require a caller-owned
// input buffer. Entries whose input relocates across windows or is not caller
// bytes reject an explicit WithZeroCopy(true) demand with this error.
var ErrZeroCopyUnsupported = errors.New("vjson: WithZeroCopy(true) requires a caller-owned input buffer")

// ZeroCopyMode selects how escape-free decoded strings are backed.
type ZeroCopyMode uint8

const (
	// ZeroCopyAuto lets each entry apply its input model's default: Unmarshal
	// and UnmarshalPadded alias the caller-owned input, while every entry
	// whose input relocates across windows or is not caller bytes copies.
	ZeroCopyAuto ZeroCopyMode = iota
	// ZeroCopyOn demands aliasing into the caller-owned input.
	ZeroCopyOn
	// ZeroCopyOff demands the copying parse.
	ZeroCopyOff
)

// Config holds the resolved option state for one decode call.
type Config struct {
	// ZeroCopy selects the string backing for the call. The zero value
	// defers to each entry's input-model default; WithZeroCopy resolves it
	// to an explicit demand or disable.
	ZeroCopy ZeroCopyMode

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

// WithZeroCopy selects the string backing. Unmarshal and UnmarshalPadded
// alias escape-free strings into the caller-owned input by default:
// WithZeroCopy(false) selects the copying parse, and WithZeroCopy(true) is an
// explicit demand that entries whose input cannot alias reject with
// ErrZeroCopyUnsupported.
func WithZeroCopy(enabled bool) Option {
	return func(c Config) Config {
		if enabled {
			c.ZeroCopy = ZeroCopyOn
		} else {
			c.ZeroCopy = ZeroCopyOff
		}
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
