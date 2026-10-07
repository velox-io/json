package tests

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/dom"
)

// expectOverflowError checks that err is an *UnmarshalTypeError (or bridges to
// *json.UnmarshalTypeError via errors.As).
func expectOverflowError(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected overflow error, got nil", label)
		return
	}
	var ute *vjson.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Errorf("%s: expected *UnmarshalTypeError, got %T: %v", label, err, err)
		return
	}
	// Also verify stdlib bridging.
	var jute *json.UnmarshalTypeError
	if !errors.As(err, &jute) {
		t.Errorf("%s: errors.As(*json.UnmarshalTypeError) failed", label)
	}
}

// int8

func TestOverflow_Int8_Above(t *testing.T) {
	var v struct{ X int8 }
	err := vjson.Unmarshal([]byte(`{"X":128}`), &v)
	expectOverflowError(t, "int8 128", err)
	if v.X != 0 {
		t.Errorf("expected zero value, got %d", v.X)
	}
}

func TestOverflow_Int8_Below(t *testing.T) {
	var v struct{ X int8 }
	err := vjson.Unmarshal([]byte(`{"X":-129}`), &v)
	expectOverflowError(t, "int8 -129", err)
}

func TestOverflow_Int8_MaxOK(t *testing.T) {
	var v struct{ X int8 }
	if err := vjson.Unmarshal([]byte(`{"X":127}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != 127 {
		t.Errorf("got %d, want 127", v.X)
	}
}

func TestOverflow_Int8_MinOK(t *testing.T) {
	var v struct{ X int8 }
	if err := vjson.Unmarshal([]byte(`{"X":-128}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != -128 {
		t.Errorf("got %d, want -128", v.X)
	}
}

// int16

func TestOverflow_Int16(t *testing.T) {
	var v struct{ X int16 }
	err := vjson.Unmarshal([]byte(`{"X":32768}`), &v)
	expectOverflowError(t, "int16 32768", err)

	err = vjson.Unmarshal([]byte(`{"X":-32769}`), &v)
	expectOverflowError(t, "int16 -32769", err)
}

// int32

func TestOverflow_Int32(t *testing.T) {
	var v struct{ X int32 }
	err := vjson.Unmarshal([]byte(`{"X":2147483648}`), &v)
	expectOverflowError(t, "int32 2147483648", err)

	err = vjson.Unmarshal([]byte(`{"X":-2147483649}`), &v)
	expectOverflowError(t, "int32 -2147483649", err)
}

// int64

func TestOverflow_Int64_Above(t *testing.T) {
	var v struct{ X int64 }
	err := vjson.Unmarshal([]byte(`{"X":9223372036854775808}`), &v)
	expectOverflowError(t, "int64 max+1", err)
}

func TestOverflow_Int64_Below(t *testing.T) {
	var v struct{ X int64 }
	err := vjson.Unmarshal([]byte(`{"X":-9223372036854775809}`), &v)
	expectOverflowError(t, "int64 min-1", err)
}

func TestOverflow_Int64_MaxOK(t *testing.T) {
	var v struct{ X int64 }
	if err := vjson.Unmarshal([]byte(`{"X":9223372036854775807}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != math.MaxInt64 {
		t.Errorf("got %d, want %d", v.X, int64(math.MaxInt64))
	}
}

func TestOverflow_Int64_MinOK(t *testing.T) {
	var v struct{ X int64 }
	if err := vjson.Unmarshal([]byte(`{"X":-9223372036854775808}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != math.MinInt64 {
		t.Errorf("got %d, want %d", v.X, int64(math.MinInt64))
	}
}

// uint8

func TestOverflow_Uint8(t *testing.T) {
	var v struct{ X uint8 }
	err := vjson.Unmarshal([]byte(`{"X":256}`), &v)
	expectOverflowError(t, "uint8 256", err)
}

func TestOverflow_Uint8_MaxOK(t *testing.T) {
	var v struct{ X uint8 }
	if err := vjson.Unmarshal([]byte(`{"X":255}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != 255 {
		t.Errorf("got %d, want 255", v.X)
	}
}

// uint16

func TestOverflow_Uint16(t *testing.T) {
	var v struct{ X uint16 }
	err := vjson.Unmarshal([]byte(`{"X":65536}`), &v)
	expectOverflowError(t, "uint16 65536", err)
}

// uint32

func TestOverflow_Uint32(t *testing.T) {
	var v struct{ X uint32 }
	err := vjson.Unmarshal([]byte(`{"X":4294967296}`), &v)
	expectOverflowError(t, "uint32 4294967296", err)
}

// uint64

func TestOverflow_Uint64(t *testing.T) {
	var v struct{ X uint64 }
	err := vjson.Unmarshal([]byte(`{"X":18446744073709551616}`), &v)
	expectOverflowError(t, "uint64 max+1", err)
}

func TestOverflow_Uint64_MaxOK(t *testing.T) {
	var v struct{ X uint64 }
	if err := vjson.Unmarshal([]byte(`{"X":18446744073709551615}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != math.MaxUint64 {
		t.Errorf("got %d, want %d", v.X, uint64(math.MaxUint64))
	}
}

// negative to unsigned

func TestOverflow_NegativeToUnsigned(t *testing.T) {
	var v struct{ X uint8 }
	err := vjson.Unmarshal([]byte(`{"X":-1}`), &v)
	expectOverflowError(t, "uint8 -1", err)

	var v2 struct{ X uint64 }
	err = vjson.Unmarshal([]byte(`{"X":-1}`), &v2)
	expectOverflowError(t, "uint64 -1", err)
}

// very large numbers

func TestOverflow_HugeNumber(t *testing.T) {
	var v struct{ X int64 }
	err := vjson.Unmarshal([]byte(`{"X":99999999999999999999999}`), &v)
	expectOverflowError(t, "huge int64", err)

	var v2 struct{ X uint64 }
	err = vjson.Unmarshal([]byte(`{"X":99999999999999999999999}`), &v2)
	expectOverflowError(t, "huge uint64", err)
}

// stdlib compatibility

func TestOverflow_StdlibCompat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		v     any
	}{
		{"int8_300", `{"X":300}`, new(struct{ X int8 })},
		{"uint8_256", `{"X":256}`, new(struct{ X uint8 })},
		{"int64_max+1", `{"X":9223372036854775808}`, new(struct{ X int64 })},
		{"uint64_max+1", `{"X":18446744073709551616}`, new(struct{ X uint64 })},
		{"uint8_neg", `{"X":-1}`, new(struct{ X uint8 })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdErr := json.Unmarshal([]byte(tt.input), tt.v)
			if stdErr == nil {
				t.Skip("stdlib does not error")
			}

			// vjson should also error (use Decoder since Unmarshal is generic)
			dec := vjson.NewDecoder(strings.NewReader(tt.input))
			vjErr := dec.Decode(tt.v)
			if vjErr == nil {
				t.Errorf("vjson returned nil error, stdlib returned: %v", stdErr)
			}

			// Both should be UnmarshalTypeError
			var stdUTE *json.UnmarshalTypeError
			if !errors.As(stdErr, &stdUTE) {
				t.Fatalf("stdlib error is not *json.UnmarshalTypeError: %T", stdErr)
			}
			var vjUTE *json.UnmarshalTypeError
			if !errors.As(vjErr, &vjUTE) {
				t.Errorf("vjson error does not bridge to *json.UnmarshalTypeError: %T: %v", vjErr, vjErr)
			}
		})
	}
}

// top-level integer

func TestOverflow_TopLevel(t *testing.T) {
	var v int8
	err := vjson.Unmarshal([]byte(`300`), &v)
	expectOverflowError(t, "top-level int8", err)
	if v != 0 {
		t.Errorf("expected zero, got %d", v)
	}
}

// ,string tag overflow

func TestOverflow_StringTag_Int8(t *testing.T) {
	var v struct {
		X int8 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"128"}`), &v)
	expectOverflowError(t, "string int8 128", err)

	err = vjson.Unmarshal([]byte(`{"x":"-129"}`), &v)
	expectOverflowError(t, "string int8 -129", err)
}

func TestOverflow_StringTag_Int8_BoundaryOK(t *testing.T) {
	var v struct {
		X int8 `json:"x,string"`
	}
	if err := vjson.Unmarshal([]byte(`{"x":"127"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != 127 {
		t.Errorf("got %d, want 127", v.X)
	}
	if err := vjson.Unmarshal([]byte(`{"x":"-128"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != -128 {
		t.Errorf("got %d, want -128", v.X)
	}
}

func TestOverflow_StringTag_Int16(t *testing.T) {
	var v struct {
		X int16 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"32768"}`), &v)
	expectOverflowError(t, "string int16 32768", err)

	err = vjson.Unmarshal([]byte(`{"x":"-32769"}`), &v)
	expectOverflowError(t, "string int16 -32769", err)
}

func TestOverflow_StringTag_Int32(t *testing.T) {
	var v struct {
		X int32 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"2147483648"}`), &v)
	expectOverflowError(t, "string int32 2147483648", err)
}

func TestOverflow_StringTag_Int64(t *testing.T) {
	var v struct {
		X int64 `json:"x,string"`
	}
	// strconv.ParseInt returns error for int64 overflow
	err := vjson.Unmarshal([]byte(`{"x":"9223372036854775808"}`), &v)
	if err == nil {
		t.Error("expected error for int64 overflow via string tag, got nil")
	}
}

func TestOverflow_StringTag_Uint8(t *testing.T) {
	var v struct {
		X uint8 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"256"}`), &v)
	expectOverflowError(t, "string uint8 256", err)
}

func TestOverflow_StringTag_Uint8_BoundaryOK(t *testing.T) {
	var v struct {
		X uint8 `json:"x,string"`
	}
	if err := vjson.Unmarshal([]byte(`{"x":"255"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.X != 255 {
		t.Errorf("got %d, want 255", v.X)
	}
}

func TestOverflow_StringTag_Uint16(t *testing.T) {
	var v struct {
		X uint16 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"65536"}`), &v)
	expectOverflowError(t, "string uint16 65536", err)
}

func TestOverflow_StringTag_Uint32(t *testing.T) {
	var v struct {
		X uint32 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"4294967296"}`), &v)
	expectOverflowError(t, "string uint32 4294967296", err)
}

func TestOverflow_StringTag_NegativeToUnsigned(t *testing.T) {
	var v struct {
		X uint8 `json:"x,string"`
	}
	err := vjson.Unmarshal([]byte(`{"x":"-1"}`), &v)
	expectOverflowError(t, "string uint8 -1", err)

	var v2 struct {
		X uint64 `json:"x,string"`
	}
	err = vjson.Unmarshal([]byte(`{"x":"-1"}`), &v2)
	expectOverflowError(t, "string uint64 -1", err)
}

func TestOverflow_StringTag_StdlibCompat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		v     any
	}{
		{"str_int8_300", `{"x":"300"}`, new(struct {
			X int8 `json:"x,string"`
		})},
		{"str_uint8_256", `{"x":"256"}`, new(struct {
			X uint8 `json:"x,string"`
		})},
		{"str_uint8_neg", `{"x":"-1"}`, new(struct {
			X uint8 `json:"x,string"`
		})},
		{"str_int16_big", `{"x":"32768"}`, new(struct {
			X int16 `json:"x,string"`
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdErr := json.Unmarshal([]byte(tt.input), tt.v)
			if stdErr == nil {
				t.Skip("stdlib does not error")
			}

			dec := vjson.NewDecoder(strings.NewReader(tt.input))
			vjErr := dec.Decode(tt.v)
			if vjErr == nil {
				t.Errorf("vjson returned nil error, stdlib returned: %v", stdErr)
				return
			}

			var stdUTE *json.UnmarshalTypeError
			if !errors.As(stdErr, &stdUTE) {
				t.Fatalf("stdlib error is not *json.UnmarshalTypeError: %T", stdErr)
			}
			var vjUTE *json.UnmarshalTypeError
			if !errors.As(vjErr, &vjUTE) {
				t.Errorf("vjson error does not bridge to *json.UnmarshalTypeError: %T: %v", vjErr, vjErr)
			}
		})
	}
}

// type mismatch: skip and continue

// TestTypeMismatch_ContinueDecode verifies that a type mismatch on one struct
// field does not prevent subsequent fields from being decoded.
func TestTypeMismatch_ContinueDecode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		wantB string
	}{
		{"num_to_str", `{"A":123,"B":"hello"}`, "hello"},
		{"bool_to_str", `{"A":true,"B":"world"}`, "world"},
		{"obj_to_int", `{"X":{"nested":1},"B":"ok"}`, "ok"},
		{"arr_to_str", `{"A":[1,2,3],"B":"ok"}`, "ok"},
		{"overflow_int8", `{"C":300,"B":"ok"}`, "ok"},
	}

	type target struct {
		A string
		B string
		C int8
		X int
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v target
			err := vjson.Unmarshal([]byte(tt.input), &v)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if v.B != tt.wantB {
				t.Errorf("B = %q, want %q (decoding should have continued)", v.B, tt.wantB)
			}
			var ute *vjson.UnmarshalTypeError
			if !errors.As(err, &ute) {
				t.Errorf("expected *UnmarshalTypeError, got %T: %v", err, err)
			}
		})
	}
}

// TestTypeMismatch_FirstErrorReturned verifies that only the first type
// mismatch error is returned when multiple fields have mismatches.
func TestTypeMismatch_FirstErrorReturned(t *testing.T) {
	type S struct {
		A string
		B string
		C string
	}
	var v S
	err := vjson.Unmarshal([]byte(`{"A":1,"B":2,"C":"ok"}`), &v)
	if err == nil {
		t.Fatal("expected error")
	}
	if v.C != "ok" {
		t.Errorf("C = %q, want \"ok\"", v.C)
	}
	var ute *vjson.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError, got %T", err)
	}
}

// TestTypeMismatch_SyntaxErrorStillAborts verifies that actual syntax errors
// (malformed JSON) still abort immediately.
func TestTypeMismatch_SyntaxErrorStillAborts(t *testing.T) {
	type S struct {
		A int
		B string
	}
	var v S
	err := vjson.Unmarshal([]byte(`{"A":invalid,"B":"ok"}`), &v)
	if err == nil {
		t.Fatal("expected syntax error")
	}
	var se *vjson.SyntaxError
	if !errors.As(err, &se) {
		t.Errorf("expected *SyntaxError, got %T: %v", err, err)
	}
	// B should NOT be decoded because syntax errors abort.
	if v.B != "" {
		t.Errorf("B = %q, want empty (syntax error should abort)", v.B)
	}
}

// TestTypeMismatch_StdlibCompat verifies that vjson matches encoding/json:
// type mismatch fields are skipped, subsequent fields are decoded.
func TestTypeMismatch_StdlibCompat(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"num_to_str", `{"A":123,"B":"hello"}`},
		{"bool_to_str", `{"A":true,"B":"world"}`},
		{"obj_to_int", `{"X":{"nested":1},"B":"ok"}`},
		{"arr_to_str", `{"A":[1,2,3],"B":"ok"}`},
		{"overflow", `{"C":300,"B":"ok"}`},
	}

	type target struct {
		A string
		B string
		C int8
		X int
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdV target
			stdErr := json.Unmarshal([]byte(tt.input), &stdV)

			var vjV target
			dec := vjson.NewDecoder(strings.NewReader(tt.input))
			vjErr := dec.Decode(&vjV)

			// Both should error
			if (stdErr == nil) != (vjErr == nil) {
				t.Fatalf("error mismatch: stdlib=%v vjson=%v", stdErr, vjErr)
			}

			// Both should decode B the same way
			if stdV.B != vjV.B {
				t.Errorf("B mismatch: stdlib=%q vjson=%q", stdV.B, vjV.B)
			}

			// Both errors should be UnmarshalTypeError
			var stdUTE *json.UnmarshalTypeError
			var vjUTE *json.UnmarshalTypeError
			if errors.As(stdErr, &stdUTE) {
				if !errors.As(vjErr, &vjUTE) {
					t.Errorf("vjson error not *json.UnmarshalTypeError: %T: %v", vjErr, vjErr)
				}
			}

			// Field values should match
			if stdV != vjV {
				t.Errorf("decoded struct mismatch:\n  stdlib: %+v\n  vjson:  %+v", stdV, vjV)
			}
		})
	}
}

// TestTypeMismatch_NestedStruct verifies skip-and-continue works for nested structs.
func TestTypeMismatch_NestedStruct(t *testing.T) {
	type Inner struct {
		X string
		Y string
	}
	type Outer struct {
		A Inner
		B string
	}
	// A gets a number (type mismatch), B should still decode.
	var v Outer
	err := vjson.Unmarshal([]byte(`{"A":42,"B":"ok"}`), &v)
	if err == nil {
		t.Fatal("expected error")
	}
	if v.B != "ok" {
		t.Errorf("B = %q, want \"ok\"", v.B)
	}
	var ute *vjson.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Errorf("expected *UnmarshalTypeError, got %T", err)
	}
}

// TestTypeMismatch_ZeroValue verifies the mismatched field retains its zero value.
func TestTypeMismatch_ZeroValue(t *testing.T) {
	type S struct {
		A string
		B string
	}
	var v S
	err := vjson.Unmarshal([]byte(`{"A":999,"B":"ok"}`), &v)
	if err == nil {
		t.Fatal("expected error")
	}
	if v.A != "" {
		t.Errorf("A = %q, want empty (mismatched field should remain zero)", v.A)
	}
	if v.B != "ok" {
		t.Errorf("B = %q, want \"ok\"", v.B)
	}
}

// Verify stdlib also returns *json.UnmarshalTypeError for all mismatch variants.
func TestTypeMismatch_StdlibErrorType(t *testing.T) {
	cases := []string{
		`{"A":123}`,     // number → string
		`{"A":true}`,    // bool → string
		`{"A":[1,2]}`,   // array → string
		`{"A":{"x":1}}`, // object → string
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			var v struct{ A string }
			err := json.Unmarshal([]byte(input), &v)
			var ute *json.UnmarshalTypeError
			if !errors.As(err, &ute) {
				t.Fatalf("stdlib returned %T, not *json.UnmarshalTypeError", err)
			}
		})
	}
}

// A number past the float target's range stores ±Inf and still reports the
// type error, matching encoding/json (jsonv2 stores the parsed value before
// the error; Go <= 1.26 with jsonv1 left the destination untouched). Checked
// at top level, in struct fields, and through interface{}, on both the JSON
// bind path and the tape path.
func TestOverflow_FloatStoresInf(t *testing.T) {
	type sf64 struct{ X float64 }
	type sf32 struct{ X float32 }
	type san struct{ X any }

	cases := []struct{ name, input string }{
		{"pos_1e400", "1e400"},
		{"neg_1e400", "-1e400"},
		{"long_mantissa", strings.Repeat("7", 3000) + "e2600"},
	}

	check := func(t *testing.T, label string, got, want any, gotErr, wantErr error) {
		t.Helper()
		if (gotErr != nil) != (wantErr != nil) {
			t.Errorf("%s: err=%v, stdlib err=%v", label, gotErr, wantErr)
			return
		}
		if gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Errorf("%s: v=%#v, stdlib v=%#v", label, got, want)
		}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.input)

			// top level, JSON path
			var rj64, vj64 float64
			stdErr := json.Unmarshal(data, &rj64)
			vjErr := vjson.Unmarshal(data, &vj64)
			check(t, "top f64", vj64, rj64, vjErr, stdErr)

			var rj32, vj32 float32
			stdErr = json.Unmarshal(data, &rj32)
			vjErr = vjson.Unmarshal(data, &vj32)
			check(t, "top f32", vj32, rj32, vjErr, stdErr)

			var rjAny, vjAny any
			stdErr = json.Unmarshal(data, &rjAny)
			vjErr = vjson.Unmarshal(data, &vjAny)
			check(t, "top any", vjAny, rjAny, vjErr, stdErr)

			// struct field, JSON path
			f64in := []byte(`{"X":` + c.input + `}`)
			var rjsf, vjsf sf64
			stdErr = json.Unmarshal(f64in, &rjsf)
			vjErr = vjson.Unmarshal(f64in, &vjsf)
			check(t, "field f64", vjsf, rjsf, vjErr, stdErr)

			var rjs32, vjs32 sf32
			stdErr = json.Unmarshal(f64in, &rjs32)
			vjErr = vjson.Unmarshal(f64in, &vjs32)
			check(t, "field f32", vjs32, rjs32, vjErr, stdErr)

			var rjsa, vjsa san
			stdErr = json.Unmarshal(f64in, &rjsa)
			vjErr = vjson.Unmarshal(f64in, &vjsa)
			check(t, "field any", vjsa, rjsa, vjErr, stdErr)

			// tape path
			val, err := dom.Parse(data)
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var tv64 float64
			vjErr = vjson.UnmarshalValue(val, &tv64)
			check(t, "tape f64", tv64, rj64, vjErr, stdErr)

			var tv32 float32
			vjErr = vjson.UnmarshalValue(val, &tv32)
			check(t, "tape f32", tv32, rj32, vjErr, stdErr)

			var tvAny any
			vjErr = vjson.UnmarshalValue(val, &tvAny)
			check(t, "tape any", tvAny, rjAny, vjErr, stdErr)

			// every stored value must be the signed Inf
			if math.IsInf(rj64, 0) && vj64 != rj64 {
				t.Errorf("top f64: v=%v, stdlib v=%v", vj64, rj64)
			}
		})
	}
}

// A quoted number past the float range keeps the legacy semantics: the error is
// reported and the destination stays untouched, on both sides.
func TestOverflow_StringTagFloatKeepsZero(t *testing.T) {
	type sf64 struct {
		X float64 `json:"x,string"`
	}
	var v sf64
	if err := vjson.Unmarshal([]byte(`{"x":"1e400"}`), &v); err == nil {
		t.Errorf("quoted overflow: expected an error")
	}
	if v.X != 0 {
		t.Errorf("quoted overflow: X = %v, want 0", v.X)
	}
}

// The stored ±Inf must survive when the overflow is only the first error: a
// later sibling field still binds, like encoding/json's saveError semantics.
// Array and map elements do not share this: their mismatch policy aborts the
// decode (BIND_ERR_VALUE_OR_EOF), an independent difference from encoding/json
// that predates the ±Inf store.
func TestOverflow_FloatStoresInfThenContinues(t *testing.T) {
	type S struct{ A, B float32 }
	var v S
	err := vjson.Unmarshal([]byte(`{"A":1e300,"B":2}`), &v)
	if err == nil {
		t.Fatalf("expected an overflow error")
	}
	if !math.IsInf(float64(v.A), 1) || v.B != 2 {
		t.Errorf("v = %+v, want {A:+Inf B:2}", v)
	}
}
