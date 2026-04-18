package ndec

import (
	"reflect"
	"testing"
)

type ptrInt32Box struct {
	A *int32 `json:"a"`
	B *int32 `json:"b"`
}

type ptrStringBox struct {
	S *string `json:"s"`
}

type ptrBoolBox struct {
	B *bool `json:"b"`
}

type ptrFloatBox struct {
	F *float64 `json:"f"`
}

type ptrMixed struct {
	I *int64   `json:"i"`
	S *string  `json:"s"`
	B *bool    `json:"b"`
	F *float32 `json:"f"`
	U *uint8   `json:"u"`
}

type ptrInner struct {
	X int32  `json:"x"`
	Y string `json:"y"`
}

type ptrInnerBox struct {
	A int32     `json:"a"`
	I *ptrInner `json:"i"`
}

type ptrInnerEscBox struct {
	I *ptrInner `json:"i"`
}

func TestPtrInt32(t *testing.T) {
	cases := []string{
		`{"a":42,"b":-7}`,
		`{"a":0}`,
		`{"a":null,"b":1}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrInt32Box
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrString(t *testing.T) {
	cases := []string{
		`{"s":"hello"}`,
		`{"s":""}`,
		`{"s":null}`,
		`{}`,
		`{"s":"escape\nhere"}`,
		`{"s":"中文"}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrStringBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrBool(t *testing.T) {
	cases := []string{
		`{"b":true}`,
		`{"b":false}`,
		`{"b":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrBoolBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrFloat(t *testing.T) {
	cases := []string{
		`{"f":3.14}`,
		`{"f":1e308}`,
		`{"f":0}`,
		`{"f":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrFloatBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrMixed(t *testing.T) {
	cases := []string{
		`{"i":9223372036854775807,"s":"hi","b":true,"f":1.5,"u":255}`,
		`{"i":null,"s":null,"b":null,"f":null,"u":null}`,
		`{"u":0,"f":-0.0,"b":false,"s":"","i":0}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrMixed
			runParity(t, in, &got, &want)
		})
	}
}

func runPtrStructParity(t *testing.T, input string, got, want any) {
	t.Helper()
	runParity(t, input, got, want)
	_ = derefPtrInner
}

func derefPtrInner(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName("I")
		if f.IsValid() && !f.IsNil() {
			return f.Elem().Interface()
		}
	}
	return nil
}

func TestPtrStruct(t *testing.T) {
	cases := []string{
		`{"a":1,"i":{"x":42,"y":"hi"}}`,
		`{"a":-1,"i":{"x":0,"y":""}}`,
		`{"i":{"x":7,"y":"only"}}`,
		`{"a":99,"i":null}`,
		`{"a":99}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrInnerBox
			runPtrStructParity(t, in, &got, &want)
		})
	}
}

func TestPtrStructWithEscape(t *testing.T) {
	cases := []string{
		`{"i":{"x":1,"y":"a\nb"}}`,
		`{"i":{"x":2,"y":"中文"}}`,
		`{"i":{"y":"only-y","x":-3}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrInnerEscBox
			runPtrStructParity(t, in, &got, &want)
		})
	}
}

// encoding/json allocates each pointer level only when the JSON value is
// non-null. A null leaves the entire chain nil.

type ptrPtrInt struct {
	A **int `json:"a"`
}

type ptrPtrPtrInt struct {
	A ***int `json:"a"`
}

type ptrPtrString struct {
	S **string `json:"s"`
}

type ptrPtrBool struct {
	B **bool `json:"b"`
}

type ptrPtrFloat struct {
	F **float64 `json:"f"`
}

func TestPtrPtrInt(t *testing.T) {
	cases := []string{
		`{"a":42}`,
		`{"a":-7}`,
		`{"a":0}`,
		`{"a":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrPtrInt
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrPtrPtrInt(t *testing.T) {
	cases := []string{
		`{"a":42}`,
		`{"a":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrPtrPtrInt
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrPtrString(t *testing.T) {
	cases := []string{
		`{"s":"hello"}`,
		`{"s":""}`,
		`{"s":"a\nb"}`,
		`{"s":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrPtrString
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrPtrBool(t *testing.T) {
	cases := []string{
		`{"b":true}`,
		`{"b":false}`,
		`{"b":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrPtrBool
			runParity(t, in, &got, &want)
		})
	}
}

func TestPtrPtrFloat(t *testing.T) {
	cases := []string{
		`{"f":3.14}`,
		`{"f":1e10}`,
		`{"f":null}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want ptrPtrFloat
			runParity(t, in, &got, &want)
		})
	}
}
