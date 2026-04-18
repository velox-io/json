package ndec

import (
	"strconv"
	"testing"
)

type scalarBool struct {
	A bool `json:"a"`
}

type scalarAllInt struct {
	I8  int8  `json:"i8"`
	I16 int16 `json:"i16"`
	I32 int32 `json:"i32"`
	I64 int64 `json:"i64"`
	I   int   `json:"i"`
}

type scalarAllUint struct {
	U8  uint8  `json:"u8"`
	U16 uint16 `json:"u16"`
	U32 uint32 `json:"u32"`
	U64 uint64 `json:"u64"`
	U   uint   `json:"u"`
}

type scalarAllFloat struct {
	F32 float32 `json:"f32"`
	F64 float64 `json:"f64"`
}

type scalarMixed struct {
	B bool    `json:"b"`
	I int32   `json:"i"`
	U uint64  `json:"u"`
	F float64 `json:"f"`
	S string  `json:"s"`
}

type scalarInt64Box struct {
	V int64 `json:"v"`
}

type scalarUint64Box struct {
	V uint64 `json:"v"`
}

func TestScalarBool(t *testing.T) {
	cases := []string{
		`{"a":true}`,
		`{"a":false}`,
		`{"a":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want scalarBool
			runParity(t, in, &got, &want)
		})
	}
}

func TestScalarAllInt(t *testing.T) {
	cases := []string{
		`{"i8":-128,"i16":-32768,"i32":-2147483648,"i64":-9223372036854775808,"i":-1}`,
		`{"i8":127,"i16":32767,"i32":2147483647,"i64":9223372036854775807,"i":1}`,
		`{"i8":0,"i16":0,"i32":0,"i64":0,"i":0}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want scalarAllInt
			runParity(t, in, &got, &want)
		})
	}
}

func TestScalarAllUint(t *testing.T) {
	cases := []string{
		`{"u8":255,"u16":65535,"u32":4294967295,"u64":18446744073709551615,"u":42}`,
		`{"u8":0,"u16":0,"u32":0,"u64":0,"u":0}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want scalarAllUint
			runParity(t, in, &got, &want)
		})
	}
}

func TestScalarAllFloat(t *testing.T) {
	cases := []string{
		`{"f32":3.14,"f64":2.718281828459045}`,
		`{"f32":-0.5,"f64":-1e308}`,
		`{"f32":1e-30,"f64":1e-300}`,
		`{"f32":0,"f64":0}`,
		`{"f32":42,"f64":-7}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want scalarAllFloat
			runParity(t, in, &got, &want)
		})
	}
}

func TestScalarMixedFields(t *testing.T) {
	cases := []string{
		`{"b":true,"i":-7,"u":12345,"f":3.14,"s":"hi"}`,
		`{"s":"first","i":2,"f":1.0,"u":0,"b":false}`,
		`{"b":null,"i":null,"u":null,"f":null,"s":null}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want scalarMixed
			runParity(t, in, &got, &want)
		})
	}
}

func TestScalarInt64Boundary(t *testing.T) {
	values := []int64{
		1<<63 - 1,      // INT64_MAX
		-(1 << 63),     // INT64_MIN
		1<<63 - 2,      // INT64_MAX - 1
		-(1 << 63) + 1, // INT64_MIN + 1
		1<<63 - 1023,
		-(1 << 63) + 512,
		1 << 53,
		1<<53 + 1,
		1<<53 - 1,
		-(1 << 53),
		-(1 << 53) - 1,
		0,
		1,
		-1,
		42,
		1234567890,
	}
	for _, v := range values {
		t.Run(strconv.FormatInt(v, 10), func(t *testing.T) {
			input := `{"v":` + strconv.FormatInt(v, 10) + `}`
			var got, want scalarInt64Box
			runParity(t, input, &got, &want)
			if got.V != v {
				t.Fatalf("ndec %q lost precision: got %d, want %d", input, got.V, v)
			}
		})
	}
}

// These values cover the uint64 range where lossy float conversions used to
// collapse distinct integers onto the same representation.
func TestScalarUint64Boundary(t *testing.T) {
	values := []uint64{
		^uint64(0),
		^uint64(0) - 1,
		^uint64(0) - 511,
		1 << 63,
		1<<63 + 1,
		1 << 53,
		1<<53 + 1,
		0,
		1,
		42,
	}
	for _, v := range values {
		t.Run(strconv.FormatUint(v, 10), func(t *testing.T) {
			input := `{"v":` + strconv.FormatUint(v, 10) + `}`
			var got, want scalarUint64Box
			runParity(t, input, &got, &want)
			if got.V != v {
				t.Fatalf("ndec %q lost precision: got %d, want %d", input, got.V, v)
			}
		})
	}
}

// encoding/json rejects any integer target fed by a token that still contains
// a decimal point or exponent marker, even when the numeric value is integral.
func TestScalarIntFloatLikeInput(t *testing.T) {
	type intBox struct {
		V int32 `json:"v"`
	}
	type uintBox struct {
		V uint32 `json:"v"`
	}

	errCases := []string{
		`{"v":1.0}`,
		`{"v":1e2}`,
		`{"v":1.5e1}`,
		`{"v":-2e3}`,
		`{"v":0.0}`,
		`{"v":1.5}`,
		`{"v":1e-1}`,
		`{"v":3.14}`,
	}
	for _, in := range errCases {
		t.Run("err:"+in, func(t *testing.T) {
			var got intBox
			err := Unmarshal([]byte(in), &got)
			if err == nil {
				t.Fatalf("ndec.Unmarshal(%q) should fail (float-literal to int), got V=%d", in, got.V)
			}
		})
	}

	// Unsigned targets must still reject a leading minus sign.
	negToUint := `{"v":-1}`
	t.Run("err:"+negToUint, func(t *testing.T) {
		var got uintBox
		err := Unmarshal([]byte(negToUint), &got)
		if err == nil {
			t.Fatalf("ndec.Unmarshal(%q) should fail (negative to uint), got V=%d", negToUint, got.V)
		}
	})
}
