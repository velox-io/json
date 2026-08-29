package bind

import "testing"

// Types covering the `,string` (quoted) tag across quotable scalar kinds,
// including pointer-to-scalar and the string double-quoting case. Each is run
// through the parity3 harness (encoding/json + Unmarshal + UnmarshalValue) so
// both the JSON bind path and the tape-bind sub-routine are covered.

type stInt struct {
	V int `json:"v,string"`
}
type stInt64 struct {
	V int64 `json:"v,string"`
}
type stUint struct {
	V uint `json:"v,string"`
}
type stUint8 struct {
	V uint8 `json:"v,string"`
}
type stFloat64 struct {
	V float64 `json:"v,string"`
}
type stFloat32 struct {
	V float32 `json:"v,string"`
}
type stBool struct {
	V bool `json:"v,string"`
}
type stString struct {
	V string `json:"v,string"`
}
type stPtrInt struct {
	V *int `json:"v,string"`
}

// stIgnored: ,string on non-quotable kinds is ignored by encoding/json, so the
// value is parsed as an ordinary field.
type stIgnored struct {
	S []int          `json:"s,string"`
	M map[string]int `json:"m,string"`
}

func TestStringTagQuotedScalars(t *testing.T) {
	parity3[stInt](t, "int", `{"v":"123"}`)
	parity3[stInt](t, "int-neg", `{"v":"-42"}`)
	parity3[stInt64](t, "int64", `{"v":"-9223372036854775808"}`)
	parity3[stUint](t, "uint", `{"v":"4294967295"}`)
	parity3[stUint8](t, "uint8", `{"v":"255"}`)
	parity3[stUint8](t, "uint8-overflow", `{"v":"256"}`)
	parity3[stFloat64](t, "float64", `{"v":"3.14"}`)
	parity3[stFloat64](t, "float64-positive-overflow", `{"v":"1e400"}`)
	parity3[stFloat64](t, "float64-negative-overflow", `{"v":"-1e400"}`)
	parity3[stFloat64](t, "float64-finite-positive", `{"v":"1.7976931348623157e308"}`)
	parity3[stFloat64](t, "float64-finite-negative", `{"v":"-1.7976931348623157e308"}`)
	parity3[stFloat32](t, "float32", `{"v":"-0.5"}`)
	parity3[stFloat32](t, "float32-positive-overflow", `{"v":"1e39"}`)
	parity3[stFloat32](t, "float32-negative-overflow", `{"v":"-1e39"}`)
	parity3[stFloat32](t, "float32-finite-positive", `{"v":"3.4e38"}`)
	parity3[stFloat32](t, "float32-finite-negative", `{"v":"-3.4e38"}`)
	parity3[stFloat32](t, "float32-single-rounding-midpoint", `{"v":"1.0000000596046448"}`)
	parity3[stFloat32](t, "float32-max-finite-rounding", `{"v":"3.4028235677973366e38"}`)
	parity3[stFloat32](t, "float32-underflow-to-zero", `{"v":"1e-50"}`)
	parity3[stBool](t, "bool-true", `{"v":"true"}`)
	parity3[stBool](t, "bool-false", `{"v":"false"}`)
	parity3[stBool](t, "bool-bad", `{"v":"yes"}`)
	// string field: the JSON value is a doubly-quoted string.
	parity3[stString](t, "string", `{"v":"\"hello\""}`)
	parity3[stString](t, "string-escapes", `{"v":"\"a\\tb\""}`)
	parity3[stString](t, "string-long", `{"v":"\"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz\""}`)
	// pointer to scalar.
	parity3[stPtrInt](t, "ptr-int", `{"v":"77"}`)
	// null on a quoted field.
	parity3[stInt](t, "null", `{"v":null}`)
	parity3[stPtrInt](t, "ptr-null", `{"v":null}`)
	// wrong shape: quoted scalar must be a JSON string.
	parity3[stInt](t, "not-string", `{"v":123}`)
	// ,string ignored on non-quotable kinds.
	parity3[stIgnored](t, "ignored", `{"s":[1,2,3],"m":{"a":1}}`)
}
