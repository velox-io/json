//go:build vdec

package vjson

import (
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/vdec"
)

// Option is the unified functional-option type, aliasing option.Option so
// shared files (vj_value.go's Parse/ParsePadded/WithZeroCopy) compile under
// the vdec build. vdec.Unmarshal accepts UnmarshalOption (vdec-specific);
// options that don't apply to vdec are ignored.
type Option = option.Option

// UnmarshalOption is an alias for vdec.UnmarshalOption. The vdec build tag
// path does not support the unified option.Option model; options accepted
// here are vdec-specific.
type UnmarshalOption = vdec.UnmarshalOption

// WithUseNumber aliases vdec.WithUseNumber.
func WithUseNumber() UnmarshalOption { return vdec.WithUseNumber() }

// Unmarshal parses JSON data into v.
func Unmarshal[T any](data []byte, v T, opts ...UnmarshalOption) error {
	return vdec.Unmarshal(data, v, opts...)
}

// UnmarshalValue binds a pre-built value.Value into v. The native tape-bind
// path (decode/bind.UnmarshalValue) is absent in the vdec build; this
// fallback serializes the Value back to JSON and re-parses it with
// vdec.Unmarshal, copying every payload out of the Value's backings.
func UnmarshalValue[T any](v Value, out T, opts ...UnmarshalOption) error {
	data, err := v.MarshalJSON()
	if err != nil {
		return err
	}
	return vdec.Unmarshal(data, out, opts...)
}
