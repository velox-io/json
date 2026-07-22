package vjson

import "github.com/velox-io/json/vdec"

// UnmarshalOption configures Unmarshal behavior.
type UnmarshalOption = vdec.UnmarshalOption

// WithUseNumber causes numbers in interface{} fields to be decoded as json.Number instead of float64.
func WithUseNumber() UnmarshalOption { return vdec.WithUseNumber() }

// Unmarshal parses JSON data into v.
func Unmarshal[T any](data []byte, v T, opts ...UnmarshalOption) error {
	return vdec.Unmarshal(data, v, opts...)
}
