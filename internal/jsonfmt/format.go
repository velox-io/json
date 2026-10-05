// Package jsonfmt implements the `format` option of the `json` struct tag as
// encoding/json/v2 defines it: resolving an option value against a field's
// type into the representation it selects, and the codecs that representation
// needs beyond velox's default type mapping.
//
// An option applies to the field's own value. Pointers forward it to the value
// they point at; slices, arrays, and maps do not forward it to their elements.
package jsonfmt

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// Kind names the representation a format option selects.
type Kind uint8

const (
	_ Kind = iota

	NilAsNull  // "emitnull": a nil slice or map encodes as null
	NilAsEmpty // "emitempty": a nil slice or map encodes as [] or {}

	Base64    // "base64": bytes as a string, RFC 4648 section 4
	Base64URL // "base64url": bytes as a string, RFC 4648 section 5
	Base32    // "base32": bytes as a string, RFC 4648 section 6
	Base32Hex // "base32hex": bytes as a string, RFC 4648 section 7
	Base16    // "base16" or "hex": bytes as a string, RFC 4648 section 8
	ByteArray // "array": bytes as an array of numbers

	NonFinite // "nonfinite": a float also takes NaN and ±Inf, as strings

	TimeRFC3339 // "RFC3339" or "RFC3339Nano"
	TimeLayout  // any other time.Format layout, by constant name or literally
	TimeUnix    // "unix", "unixmilli", "unixmicro", "unixnano"

	DurationUnits   // "units": time.Duration.String, e.g. "1h30m0s"
	DurationISO8601 // "iso8601": e.g. "PT1H30M"
	DurationNumber  // "sec", "milli", "micro", "nano"
)

// Format is a format option resolved against a field's type.
type Format struct {
	Kind Kind

	// Quoted wraps a numeric representation in a JSON string, as the
	// `,string` option does for a plain number.
	Quoted bool

	// Ptrs counts the pointers between the field and the formatted value. A
	// nil pointer on the way is null in both directions.
	Ptrs int

	field reflect.Type // the field's declared type
	value reflect.Type // the formatted type: field with Ptrs pointers removed

	// layout is the time layout of TimeRFC3339 and TimeLayout.
	layout string

	// unit is the nanoseconds per unit of a TimeUnix or DurationNumber number:
	// 1e9 for "unix" and "sec", 1 for "unixnano" and "nano".
	unit uint64
}

var (
	timeType     = reflect.TypeFor[time.Time]()
	durationType = reflect.TypeFor[time.Duration]()
)

// Resolve resolves the format option value name against a field of type
// field, whose `,string` option is quoted. It returns the Format each
// direction applies, nil where that direction keeps the field's default
// handling.
//
// methods reports whether a type marshals or unmarshals through methods of its
// own (or velox's built-in handling of a special type). As in
// encoding/json/v2, methods take precedence over the option, except on
// time.Time and time.Duration, whose formats are built in. unmarshal is also
// nil for a format that only changes encoding.
//
// An option that no direction can apply to the type is an error.
func Resolve(field reflect.Type, name string, quoted bool, methods func(reflect.Type) (marshal, unmarshal bool)) (marshal, unmarshal *Format, err error) {
	f := &Format{field: field, value: field}
	for f.value.Kind() == reflect.Pointer {
		f.value = f.value.Elem()
		f.Ptrs++
	}
	// As in encoding/json, `,string` reaches through at most one pointer.
	f.Quoted = quoted && f.Ptrs < 2

	var viaMarshal, viaUnmarshal bool
	if f.value != timeType && f.value != durationType {
		viaMarshal, viaUnmarshal = methods(f.value)
	}
	if viaMarshal && viaUnmarshal {
		return nil, nil, nil
	}
	if !f.resolve(name) {
		return nil, nil, fmt.Errorf("format %q does not apply to type %s; see https://github.com/velox-io/json#format for the formats that each type supports", name, f.value)
	}
	// "emitnull" is how encoding/json already writes a nil slice or map, so
	// it leaves encoding on the default (native) path.
	if !viaMarshal && f.Kind != NilAsNull {
		marshal = f
	}
	if !viaUnmarshal && f.changesDecoding(methods) {
		unmarshal = f
	}
	return marshal, unmarshal, nil
}

func (f *Format) resolve(name string) bool {
	switch t := f.value; {
	case name == "":
		return false
	case t == timeType:
		return f.resolveTime(name)
	case t == durationType:
		return f.resolveDuration(name)
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem().Kind() == reflect.Uint8:
		return f.resolveBytes(name)
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Map:
		return f.resolveNil(name)
	case (t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64) && name == "nonfinite":
		f.Kind = NonFinite
		return true
	}
	return false
}

func (f *Format) resolveNil(name string) bool {
	switch name {
	case "emitnull":
		f.Kind = NilAsNull
	case "emitempty":
		f.Kind = NilAsEmpty
	default:
		return false
	}
	return true
}

func (f *Format) resolveBytes(name string) bool {
	switch name {
	case "base64":
		f.Kind = Base64
	case "base64url":
		f.Kind = Base64URL
	case "base32":
		f.Kind = Base32
	case "base32hex":
		f.Kind = Base32Hex
	case "base16", "hex":
		f.Kind = Base16
	case "array":
		f.Kind = ByteArray
	default:
		return false
	}
	return true
}

func (f *Format) resolveDuration(name string) bool {
	switch name {
	case "units":
		f.Kind = DurationUnits
	case "iso8601":
		f.Kind = DurationISO8601
	case "sec":
		f.Kind, f.unit = DurationNumber, 1e9
	case "milli":
		f.Kind, f.unit = DurationNumber, 1e6
	case "micro":
		f.Kind, f.unit = DurationNumber, 1e3
	case "nano":
		f.Kind, f.unit = DurationNumber, 1
	default:
		return false
	}
	return true
}

// timeLayouts maps the time package's layout constants, other than the
// RFC 3339 pair, to their layouts.
var timeLayouts = map[string]string{
	"ANSIC":      time.ANSIC,
	"UnixDate":   time.UnixDate,
	"RubyDate":   time.RubyDate,
	"RFC822":     time.RFC822,
	"RFC822Z":    time.RFC822Z,
	"RFC850":     time.RFC850,
	"RFC1123":    time.RFC1123,
	"RFC1123Z":   time.RFC1123Z,
	"Kitchen":    time.Kitchen,
	"Stamp":      time.Stamp,
	"StampMilli": time.StampMilli,
	"StampMicro": time.StampMicro,
	"StampNano":  time.StampNano,
	"DateTime":   time.DateTime,
	"DateOnly":   time.DateOnly,
	"TimeOnly":   time.TimeOnly,
}

func (f *Format) resolveTime(name string) bool {
	// The time package's layout constants all start with an ASCII letter, so
	// any other start is a literal layout.
	if c := name[0]; (c < 'a' || 'z' < c) && (c < 'A' || 'Z' < c) {
		f.Kind, f.layout = TimeLayout, name
		return true
	}
	switch name {
	case "RFC3339":
		f.Kind, f.layout = TimeRFC3339, time.RFC3339
	case "RFC3339Nano":
		f.Kind, f.layout = TimeRFC3339, time.RFC3339Nano
	case "unix":
		f.Kind, f.unit = TimeUnix, 1e9
	case "unixmilli":
		f.Kind, f.unit = TimeUnix, 1e6
	case "unixmicro":
		f.Kind, f.unit = TimeUnix, 1e3
	case "unixnano":
		f.Kind, f.unit = TimeUnix, 1
	default:
		if layout, ok := timeLayouts[name]; ok {
			f.Kind, f.layout = TimeLayout, layout
			return true
		}
		// A bare identifier is reserved for constants the time package may add.
		if strings.TrimFunc(name, IsLetterOrDigit) == "" {
			return false
		}
		f.Kind, f.layout = TimeLayout, name
	}
	return true
}

// IsLetterOrDigit reports whether r may continue a Go identifier, the
// grammar of an unquoted `format` value and of a reserved layout name.
func IsLetterOrDigit(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// changesDecoding reports whether f decodes differently from the default.
func (f *Format) changesDecoding(methods func(reflect.Type) (marshal, unmarshal bool)) bool {
	switch f.Kind {
	case NilAsNull, NilAsEmpty:
		return false
	case ByteArray:
		// velox already decodes a byte array, and a slice of bytes that
		// unmarshal themselves, from a JSON array only. A plain byte slice
		// would also take a base64 string, which this format refuses.
		if f.value.Kind() == reflect.Array {
			return false
		}
		_, elemUnmarshals := methods(f.value.Elem())
		return !elemUnmarshals
	}
	return true
}

// InString reports whether a time or duration representation is a JSON
// string: a textual one, or a numeric one under `,string`.
func (f *Format) InString() bool {
	return f.Quoted || (f.Kind != TimeUnix && f.Kind != DurationNumber)
}
