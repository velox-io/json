package jerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// ErrUnexpectedEOF is an internal sentinel used by the parser/decoder retry
// logic (compared with ==). When returned to callers it is wrapped as
// *SyntaxError in the Unmarshal/Decode entry points.
var ErrUnexpectedEOF = errors.New("vjson: unexpected end of input")

// SyntaxError indicates that the input is not valid JSON.
type SyntaxError struct {
	Msg    string
	Offset int64
	Err    error // optional wrapped error (e.g. strconv.NumError)
}

func (e *SyntaxError) Error() string { return e.Msg }

func (e *SyntaxError) Unwrap() error { return e.Err }

// As supports errors.As bridging to *json.SyntaxError.
func (e *SyntaxError) As(target any) bool {
	if t, ok := target.(**json.SyntaxError); ok {
		*t = &json.SyntaxError{Offset: e.Offset}
		return true
	}
	return false
}

func NewSyntaxError(msg string, offset int) *SyntaxError {
	return &SyntaxError{Msg: msg, Offset: int64(offset)}
}

func NewSyntaxErrorWrap(msg string, offset int, err error) *SyntaxError {
	return &SyntaxError{Msg: msg, Offset: int64(offset), Err: err}
}

// UnmarshalTypeError describes a JSON value that cannot be assigned to a Go type.
type UnmarshalTypeError struct {
	Value  string       // "string", "number", "bool", "array", "object"
	Type   reflect.Type // Go type that could not be assigned to
	Offset int64
	Struct string // struct being decoded (may be empty)
	Field  string // struct field (may be empty)
}

func (e *UnmarshalTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return fmt.Sprintf("vjson: cannot unmarshal %s into Go struct field %s.%s of type %s",
			e.Value, e.Struct, e.Field, e.Type)
	}
	return fmt.Sprintf("vjson: cannot unmarshal %s into Go value of type %s", e.Value, e.Type)
}

// As supports errors.As bridging to *json.UnmarshalTypeError.
func (e *UnmarshalTypeError) As(target any) bool {
	if t, ok := target.(**json.UnmarshalTypeError); ok {
		*t = &json.UnmarshalTypeError{
			Value:  e.Value,
			Type:   e.Type,
			Offset: e.Offset,
			Struct: e.Struct,
			Field:  e.Field,
		}
		return true
	}
	return false
}

func NewUnmarshalTypeError(value string, t reflect.Type, offset int) *UnmarshalTypeError {
	return &UnmarshalTypeError{Value: value, Type: t, Offset: int64(offset)}
}

// InvalidUnmarshalError describes an invalid argument passed to Unmarshal
// (must be a non-nil pointer).
type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "vjson: Unmarshal(nil)"
	}
	if e.Type.Kind() != reflect.Pointer {
		return "vjson: Unmarshal(non-pointer " + e.Type.String() + ")"
	}
	return "vjson: Unmarshal(nil " + e.Type.String() + ")"
}

// As supports errors.As bridging to *json.InvalidUnmarshalError.
func (e *InvalidUnmarshalError) As(target any) bool {
	if t, ok := target.(**json.InvalidUnmarshalError); ok {
		*t = &json.InvalidUnmarshalError{Type: e.Type}
		return true
	}
	return false
}

// UnsupportedTypeError indicates an attempt to marshal an unsupported type.
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "vjson: unsupported type: " + e.Type.String()
}

// As supports errors.As bridging to *json.UnsupportedTypeError.
func (e *UnsupportedTypeError) As(target any) bool {
	if t, ok := target.(**json.UnsupportedTypeError); ok {
		*t = &json.UnsupportedTypeError{Type: e.Type}
		return true
	}
	return false
}

// UnsupportedValueError indicates an attempt to marshal an unsupported value
// (e.g. NaN or Inf floats).
type UnsupportedValueError struct {
	Value reflect.Value
	Str   string
}

func (e *UnsupportedValueError) Error() string {
	return "vjson: unsupported value: " + e.Str
}

// As supports errors.As bridging to *json.UnsupportedValueError.
func (e *UnsupportedValueError) As(target any) bool {
	if t, ok := target.(**json.UnsupportedValueError); ok {
		*t = &json.UnsupportedValueError{Str: e.Str}
		return true
	}
	return false
}

// MarshalerError wraps an error returned by a custom MarshalJSON method.
type MarshalerError struct {
	Type reflect.Type
	Err  error
}

func (e *MarshalerError) Error() string {
	return "vjson: error calling MarshalJSON for type " + e.Type.String() + ": " + e.Err.Error()
}

func (e *MarshalerError) Unwrap() error { return e.Err }

// As supports errors.As bridging to *json.MarshalerError.
func (e *MarshalerError) As(target any) bool {
	if t, ok := target.(**json.MarshalerError); ok {
		*t = &json.MarshalerError{Type: e.Type, Err: e.Err}
		return true
	}
	return false
}
