package vjson

import (
	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/value"
)

// Value is the tape-backed view of a parsed JSON value. The native bind path
// (this library's Unmarshal) and Parse produce Values: bind.h descends into
// the value and emits its opaque parsed representation directly.
//
// A Value can appear as a struct field anywhere in a type:
//
//	var v json.Value
//	json.Unmarshal(data, &v)
//	id, ok := v.GetString("id")
//
//	type Response struct {
//		Code int        `json:"code"`
//		Data json.Value `json:"data"`
//	}
//
// Accessors (Get / Index / Str / Int / ForEachKey / ...) read the tape at
// the current cursor. Value does NOT implement json.Unmarshaler; it is
// populated by the native binder or Parse, not by a byte copy. For a
// byte-backed equivalent that works with encoding/json and on platforms
// without the native parser, use Raw.
type Value = value.Value

// Raw is the byte-backed counterpart of Value: it carries raw JSON bytes and
// accessors walk them on demand via the Go scanner (a buger/jsonparser style
// lazy parse). Raw exposes the same navigation API as Value.
//
// Use Raw for literal construction, encoding/json interop, or paths without a
// tape (e.g. the vdec fallback). Raw implements json.Marshaler and
// json.Unmarshaler.
type Raw = value.Raw

// WithZeroCopy aliases option.WithZeroCopy: escape-free strings alias the
// source buffer instead of being copied into strArena. Honored by
// ParsePadded only; Parse rejects it, and zero-copy Values are
// navigation-only (UnmarshalValue returns bind.ErrZeroCopyValue).
func WithZeroCopy() Option { return option.WithZeroCopy() }

// ParseOption aliases Option, retained for source compatibility with code
// written against the dom-specific type.
type ParseOption = Option

// Parse parses src into a tape-backed Value using the native DOM parser.
// opts select string handling and scan strictness; the defaults are copy
// mode and the lax scan.
// Returns ErrNoNative if the native parser is unavailable; use Raw for a
// byte-backed fallback in that case.
func Parse(src []byte, opts ...Option) (Value, error) {
	return dom.Parse(src, opts...)
}

// ParsePadded parses a caller-padded buffer into a tape-backed Value using
// the native DOM parser. Returns ErrNoNative if the native parser is
// unavailable.
//
// paddedSrc must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it. With WithZeroCopy, escape-free strings
// alias paddedSrc directly, so the caller must keep paddedSrc alive and
// unmodified as long as the Value (or any sub-value) is reachable.
// Zero-copy Values are navigation-only and rejected by UnmarshalValue.
func ParsePadded(paddedSrc []byte, opts ...Option) (Value, error) {
	return dom.ParsePadded(paddedSrc, opts...)
}

// ValueKind classifies the JSON held by a Value or Raw. Both types share the
// same Kind values.
type ValueKind = value.Kind

// Kinds reported by Value.Type / Raw.Type.
const (
	ValueInvalid = value.KindInvalid // empty or malformed
	ValueNull    = value.KindNull
	ValueBool    = value.KindBool
	ValueNumber  = value.KindNumber
	ValueString  = value.KindString
	ValueArray   = value.KindArray
	ValueObject  = value.KindObject
)
