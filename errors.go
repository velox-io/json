package vjson

import "github.com/velox-io/json/jerr"

type SyntaxError = jerr.SyntaxError
type UnmarshalTypeError = jerr.UnmarshalTypeError
type InvalidUnmarshalError = jerr.InvalidUnmarshalError
type UnsupportedTypeError = jerr.UnsupportedTypeError
type UnsupportedValueError = jerr.UnsupportedValueError
type MarshalerError = jerr.MarshalerError
