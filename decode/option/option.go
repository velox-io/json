// Package option defines decode options shared by bind, dom, and package vjson.
// Each decoder applies the Config fields relevant to its execution path.
package option

// Config holds the resolved option state for one decode call.
type Config struct {
	// ZeroCopy lets escape-free DOM strings alias the ParsePadded source. The
	// resulting Doc roots the source. Callers preserve its bytes and backing
	// allocation while any derived Value remains reachable. Other decode paths
	// keep strings arena-backed.
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

// WithZeroCopy arms the zero-copy string path. Honored by dom.ParsePadded
// only; dom.Parse rejects it, and zero-copy Values cannot be bound with
// UnmarshalValue.
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
