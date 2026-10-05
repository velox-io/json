package jsonfmt

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strconv"
	"time"
	"unsafe"

	"github.com/velox-io/json/internal/jsonlit"
	"github.com/velox-io/json/jerr"
)

// The strings a NonFinite float writes for its non-finite values.
const (
	nanName    = "NaN"
	posInfName = "Infinity"
	negInfName = "-Infinity"
)

// NonFiniteName returns the string a NonFinite float writes for v, or "" when
// v is finite and encodes as a number.
func NonFiniteName(v float64) string {
	switch {
	case math.IsNaN(v):
		return nanName
	case math.IsInf(v, +1):
		return posInfName
	case math.IsInf(v, -1):
		return negInfName
	}
	return ""
}

// bytesEncoding is the method set *base64.Encoding and *base32.Encoding
// share, which the byte formats need.
type bytesEncoding interface {
	AppendEncode(dst, src []byte) []byte
	AppendDecode(dst, src []byte) ([]byte, error)
	DecodedLen(n int) int
}

// hexEncoding adapts package hex to bytesEncoding.
type hexEncoding struct{}

func (hexEncoding) AppendEncode(dst, src []byte) []byte          { return hex.AppendEncode(dst, src) }
func (hexEncoding) AppendDecode(dst, src []byte) ([]byte, error) { return hex.AppendDecode(dst, src) }
func (hexEncoding) DecodedLen(n int) int                         { return hex.DecodedLen(n) }

// bytesEncoding returns the RFC 4648 encoding of a byte format. base64 and
// base32 skip CR and LF when decoding, as encoding/json does for []byte.
func (f *Format) bytesEncoding() bytesEncoding {
	switch f.Kind {
	case Base64URL:
		return base64.URLEncoding
	case Base32:
		return base32.StdEncoding
	case Base32Hex:
		return base32.HexEncoding
	case Base16:
		return hexEncoding{}
	}
	return base64.StdEncoding
}

// AppendBytes appends the encoding of src in f's byte encoding, without the
// surrounding quotes.
func (f *Format) AppendBytes(b, src []byte) []byte {
	return f.bytesEncoding().AppendEncode(b, src)
}

// AppendTime appends the representation of t, without the quotes of a string
// representation. An RFC 3339 timestamp outside what RFC 3339 can express (a
// year outside [0,9999], or a zone offset hour outside [0,23]) is an error.
func (f *Format) AppendTime(b []byte, t time.Time) ([]byte, error) {
	switch f.Kind {
	case TimeRFC3339:
		return appendTimeRFC3339(b, t, f.layout)
	case TimeLayout:
		return t.AppendFormat(b, f.layout), nil
	default:
		return appendTimeUnix(b, t, 1e9/f.unit), nil
	}
}

// AppendDuration appends the representation of d, without the quotes of a
// string representation.
func (f *Format) AppendDuration(b []byte, d time.Duration) []byte {
	switch f.Kind {
	case DurationUnits:
		return append(b, d.String()...)
	case DurationISO8601:
		return appendDurationISO8601(b, d)
	default:
		return appendDurationBase10(b, d, f.unit)
	}
}

func (f *Format) parseTime(b []byte) (time.Time, error) {
	switch f.Kind {
	case TimeRFC3339:
		return parseTimeRFC3339(b)
	case TimeLayout:
		// string(b) copies: Parse may keep a zone name sliced from its input,
		// and b can alias the caller's buffer.
		return time.Parse(f.layout, string(b))
	default:
		return parseTimeUnix(b, 1e9/f.unit)
	}
}

func (f *Format) parseDuration(b []byte) (time.Duration, error) {
	switch f.Kind {
	case DurationUnits:
		return time.ParseDuration(string(b))
	case DurationISO8601:
		return parseDurationISO8601(b)
	default:
		return parseDurationBase10(b, f.unit)
	}
}

// Decode decodes data, one complete JSON value, into the field at ptr.
//
// Decoding follows encoding/json's merge semantics: null clears a pointer or
// slice and leaves any other value unchanged, and a nil pointer on the way to
// the formatted value is allocated. A value the format cannot represent is
// reported as an *jerr.UnmarshalTypeError.
func (f *Format) Decode(ptr unsafe.Pointer, data []byte) error {
	if len(data) == 0 {
		return f.typeError(data, nil)
	}
	if data[0] == 'n' {
		if f.Ptrs > 0 || f.value.Kind() == reflect.Slice {
			reflect.NewAt(f.field, ptr).Elem().SetZero()
		}
		return nil
	}
	// Under `,string`, a quoted null takes effect on the pointer rather than
	// the value it points at, as in encoding/json.
	if f.Kind == NonFinite && f.Quoted && f.Ptrs > 0 && string(data) == `"null"` {
		reflect.NewAt(f.field, ptr).Elem().SetZero()
		return nil
	}
	for t := f.field; t.Kind() == reflect.Pointer; t = t.Elem() {
		p := (*unsafe.Pointer)(ptr)
		if *p == nil {
			*p = reflect.New(t.Elem()).UnsafePointer()
		}
		ptr = *p
	}

	switch f.Kind {
	case Base64, Base64URL, Base32, Base32Hex, Base16:
		return f.decodeBytes(ptr, data)
	case ByteArray:
		return f.decodeByteArray(ptr, data)
	case NonFinite:
		return f.decodeFloat(ptr, data)
	case TimeRFC3339, TimeLayout, TimeUnix:
		text, err := f.scalarText(data)
		if err != nil {
			return err
		}
		t, err := f.parseTime(text)
		if err != nil {
			return f.typeError(data, err)
		}
		*(*time.Time)(ptr) = t
		return nil
	case DurationUnits, DurationISO8601, DurationNumber:
		text, err := f.scalarText(data)
		if err != nil {
			return err
		}
		d, err := f.parseDuration(text)
		if err != nil {
			return f.typeError(data, err)
		}
		*(*time.Duration)(ptr) = d
		return nil
	}
	return errors.New("jsonfmt: format does not change decoding")
}

// scalarText returns the text of a time or duration representation: the body
// of a string representation, the literal of a numeric one. A JSON value of
// the other kind is a type error.
func (f *Format) scalarText(data []byte) ([]byte, error) {
	if f.InString() {
		return f.stringBody(data)
	}
	if data[0] != '-' && (data[0] < '0' || data[0] > '9') {
		return nil, f.typeError(data, nil)
	}
	return data, nil
}

// stringBody returns the unquoted body of a JSON string; any other JSON value
// is a type error.
func (f *Format) stringBody(data []byte) ([]byte, error) {
	if data[0] != '"' {
		return nil, f.typeError(data, nil)
	}
	body, ok := jsonlit.Unquote(data)
	if !ok {
		return nil, f.typeError(data, errors.New("invalid string literal"))
	}
	return body, nil
}

func (f *Format) decodeBytes(ptr unsafe.Pointer, data []byte) error {
	text, err := f.stringBody(data)
	if err != nil {
		return err
	}
	enc := f.bytesEncoding()
	b, err := enc.AppendDecode(make([]byte, 0, enc.DecodedLen(len(text))), text)
	if err != nil {
		return f.typeError(data, err)
	}
	if f.value.Kind() == reflect.Array {
		// A decoded length that differs from the array's is padded with zeros
		// or truncated, as encoding/json does.
		dst := unsafe.Slice((*byte)(ptr), f.value.Len())
		clear(dst[copy(dst, b):])
		return nil
	}
	*(*[]byte)(ptr) = b
	return nil
}

// decodeByteArray decodes a JSON array of numbers into a byte slice. Like
// velox's decoding of any slice, it reuses the slice's backing array for the
// elements it can hold, and a null element leaves the byte at its index as
// it was.
func (f *Format) decodeByteArray(ptr unsafe.Pointer, data []byte) error {
	if data[0] != '[' {
		return f.typeError(data, nil)
	}
	dst := (*[]byte)(ptr)
	s := (*dst)[:0]
	i := jsonlit.SkipWS(data, 1)
	if i < len(data) && data[i] == ']' {
		if s == nil {
			s = []byte{}
		}
		*dst = s
		return nil
	}
	for i < len(data) {
		n := len(s)
		if n < cap(s) {
			s = s[:n+1]
		} else {
			s = append(s, 0)
		}
		if data[i] == 'n' {
			i += len("null")
		} else {
			end := jsonlit.ScanNumber(data, i)
			if end == jsonlit.Bad {
				return f.typeError(data[i:], nil)
			}
			v, err := strconv.ParseUint(borrowString(data[i:end]), 10, 8)
			if err != nil {
				return f.typeError(data[i:], nil)
			}
			s[n] = byte(v)
			i = end
		}
		i = jsonlit.SkipWS(data, i)
		if i < len(data) && data[i] == ',' {
			i = jsonlit.SkipWS(data, i+1)
			continue
		}
		if i < len(data) && data[i] == ']' {
			*dst = s
			return nil
		}
		break
	}
	return f.typeError(data, errors.New("malformed array"))
}

// decodeFloat decodes a number, or one of the strings NonFiniteName writes.
// Under `,string` a finite value arrives quoted instead, read as
// encoding/json reads a `,string` float: in Go syntax, with a quoted null
// leaving the value unchanged.
func (f *Format) decodeFloat(ptr unsafe.Pointer, data []byte) error {
	if data[0] != '"' {
		if f.Quoted || !jsonlit.IsNumber(data) {
			return f.typeError(data, nil)
		}
		return f.parseFloat(ptr, data, data)
	}
	body, err := f.stringBody(data)
	if err != nil {
		return err
	}
	switch string(body) {
	case nanName:
		f.setFloat(ptr, math.NaN())
	case posInfName:
		f.setFloat(ptr, math.Inf(+1))
	case negInfName:
		f.setFloat(ptr, math.Inf(-1))
	case "null":
		if !f.Quoted {
			return f.typeError(data, nil)
		}
	default:
		if !f.Quoted {
			return f.typeError(data, nil)
		}
		return f.parseFloat(ptr, body, data)
	}
	return nil
}

// parseFloat parses num, the text of data, into the float at ptr.
func (f *Format) parseFloat(ptr unsafe.Pointer, num, data []byte) error {
	v, err := strconv.ParseFloat(borrowString(num), f.value.Bits())
	if err != nil {
		return f.typeError(data, errors.Unwrap(err))
	}
	f.setFloat(ptr, v)
	return nil
}

func (f *Format) setFloat(ptr unsafe.Pointer, v float64) {
	if f.value.Kind() == reflect.Float32 {
		*(*float32)(ptr) = float32(v)
	} else {
		*(*float64)(ptr) = v
	}
}

func (f *Format) typeError(data []byte, err error) error {
	return &jerr.UnmarshalTypeError{Value: kindName(data), Type: f.value, Err: err}
}

// kindName names the kind of the JSON value data starts with, in the words
// UnmarshalTypeError uses.
func kindName(data []byte) string {
	if len(data) == 0 {
		return "json"
	}
	switch data[0] {
	case 'n':
		return "null"
	case 't', 'f':
		return "bool"
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return "number"
}

// borrowString views b as a string without copying. The strconv parsers it
// feeds keep their input only in the error they return, which callers drop.
func borrowString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
