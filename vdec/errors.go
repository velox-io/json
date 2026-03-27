package vdec

import (
	"reflect"

	"github.com/velox-io/json/jerr"
)

var errUnexpectedEOF = jerr.ErrUnexpectedEOF

type SyntaxError = jerr.SyntaxError
type UnmarshalTypeError = jerr.UnmarshalTypeError
type InvalidUnmarshalError = jerr.InvalidUnmarshalError

func newSyntaxError(msg string, offset int) *SyntaxError {
	return jerr.NewSyntaxError(msg, offset)
}

func newSyntaxErrorWrap(msg string, offset int, err error) *SyntaxError {
	return jerr.NewSyntaxErrorWrap(msg, offset, err)
}

func newUnmarshalTypeError(value string, t reflect.Type, offset int) *UnmarshalTypeError {
	return jerr.NewUnmarshalTypeError(value, t, offset)
}
