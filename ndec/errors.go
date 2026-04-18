package ndec

import (
	"errors"
	"fmt"
	"reflect"
)

var (
	errUnexpectedEOF = errors.New("ndec: unexpected end of JSON input")
)

type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "ndec.Unmarshal(nil)"
	}
	if e.Type.Kind() != reflect.Pointer {
		return fmt.Sprintf("ndec.Unmarshal(non-pointer %s)", e.Type)
	}
	return fmt.Sprintf("ndec.Unmarshal(nil %s)", e.Type)
}

// SyntaxError fields match stdlib encoding/json.SyntaxError layout.
type SyntaxError struct {
	msg    string
	Offset int64
	Code   uint32 // ndec-specific: NDEC_ERR_SYNTAX / EOF / DEPTH / KEYWORD / TRAILING
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("ndec: %s at offset %d", e.msg, e.Offset)
}

type UnmarshalTypeError struct {
	Value  string
	Type   reflect.Type
	Offset int64
	Struct string
	Field  string
}

func (e *UnmarshalTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return fmt.Sprintf("ndec: cannot unmarshal %s into Go struct field %s.%s of type %s",
			e.Value, e.Struct, e.Field, e.Type)
	}
	return fmt.Sprintf("ndec: cannot unmarshal %s into Go value of type %s", e.Value, e.Type)
}

type UnknownFieldError struct {
	Field  string
	Struct string
	Offset int64
}

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("ndec: unknown field %s in %s at offset %d", e.Field, e.Struct, e.Offset)
}

func translateNdecError(code uint32, pos uint32) error {
	switch code {
	case exitErrSyntax:
		return &SyntaxError{Code: code, Offset: int64(pos), msg: "syntax error"}
	case exitErrEOF:
		return errUnexpectedEOF
	case exitErrDepth:
		return &SyntaxError{Code: code, Offset: int64(pos), msg: "max depth exceeded"}
	case exitErrKeyword:
		return &SyntaxError{Code: code, Offset: int64(pos), msg: "invalid keyword"}
	case exitErrTrailing:
		return &SyntaxError{Code: code, Offset: int64(pos), msg: "trailing data after value"}
	default:
		return &SyntaxError{Code: code, Offset: int64(pos), msg: "native error"}
	}
}
