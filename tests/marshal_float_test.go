package tests

import (
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"testing"

	vjson "github.com/velox-io/json"
)

type wrapF64 struct {
	V float64 `json:"v"`
}
type wrapF32 struct {
	V float32 `json:"v"`
}

// marshalFloat64 marshals val through a struct field to hit the native VM.
// Returns the raw number string (without the JSON wrapper).
func marshalFloat64(t *testing.T, val float64) string {
	t.Helper()
	w := wrapF64{V: val}
	got, err := vjson.Marshal(w, vjson.WithFloatExpAuto())
	if err != nil {
		t.Fatalf("vjson.Marshal(%v) error: %v", val, err)
	}
	return extractNumber(t, string(got))
}

// marshalFloat32 marshals val through a struct field to hit the native VM.
func marshalFloat32(t *testing.T, val float32) string {
	t.Helper()
	w := wrapF32{V: val}
	got, err := vjson.Marshal(w, vjson.WithFloatExpAuto())
	if err != nil {
		t.Fatalf("vjson.Marshal(%v) error: %v", val, err)
	}
	return extractNumber(t, string(got))
}

// stdlibFloat64 returns encoding/json's output for a float64 via a struct field.
func stdlibFloat64(t *testing.T, val float64) string {
	t.Helper()
	w := wrapF64{V: val}
	got, _ := json.Marshal(w)
	return extractNumber(t, string(got))
}

// stdlibFloat32 returns encoding/json's output for a float32 via a struct field.
func stdlibFloat32(t *testing.T, val float32) string {
	t.Helper()
	w := wrapF32{V: val}
	got, _ := json.Marshal(w)
	return extractNumber(t, string(got))
}

// extractNumber extracts the number from `{"v":<number>}`.
func extractNumber(t *testing.T, s string) string {
	t.Helper()
	const prefix = `{"v":`
	const suffix = `}`
	if len(s) < len(prefix)+len(suffix) || s[:len(prefix)] != prefix || s[len(s)-1] != '}' {
		t.Fatalf("unexpected output format: %s", s)
	}
	return s[len(prefix) : len(s)-len(suffix)]
}

// TestNativeFloat64 — table-driven edge cases for float64 via native VM

func TestNativeFloat64(t *testing.T) {
	cases := []struct {
		name string
		val  float64
	}{
		// zeros
		{"zero", 0.0},
		{"neg_zero", math.Copysign(0, -1)},

		// small integers
		{"one", 1.0},
		{"neg_one", -1.0},
		{"42", 42.0},
		{"100", 100.0},
		{"minus_100", -100.0},
		{"large_int", 999999999999999.0},
		{"neg_large_int", -999999999999999.0},

		// typical decimals
		{"pi_approx", 3.14},
		{"0.1", 0.1},
		{"0.2", 0.2},
		{"0.3", 0.3},
		{"neg_0.1", -0.1},
		{"1.5", 1.5},
		{"long_decimal", 1.23456789012345678},

		// powers of two
		{"0.5", 0.5},
		{"0.25", 0.25},
		{"0.125", 0.125},
		{"1024", 1024.0},
		{"65536", 65536.0},
		{"2^53", 9007199254740992.0},   // max safe integer
		{"2^53-1", 9007199254740991.0}, // max safe integer - 1
		{"2^-1074", 5e-324},            // = SmallestNonzeroFloat64

		// powers of ten (positive)
		{"1e1", 1e1},
		{"1e2", 1e2},
		{"1e5", 1e5},
		{"1e10", 1e10},
		{"1e15", 1e15},
		{"1e20", 1e20},
		{"1e50", 1e50},
		{"1e100", 1e100},
		{"1e200", 1e200},
		{"1e308", 1e308},

		// powers of ten (negative)
		{"1e-1", 1e-1},
		{"1e-2", 1e-2},
		{"1e-5", 1e-5},
		{"1e-10", 1e-10},
		{"1e-15", 1e-15},
		{"1e-20", 1e-20},
		{"1e-50", 1e-50},
		{"1e-100", 1e-100},
		{"1e-200", 1e-200},
		{"1e-300", 1e-300},

		// extremes
		{"max_float64", math.MaxFloat64},
		{"smallest_nonzero", math.SmallestNonzeroFloat64},
		{"smallest_normal", 2.2250738585072014e-308},
		{"just_below_normal", 2.225073858507201e-308},
		{"neg_max_float64", -math.MaxFloat64},
		{"neg_smallest_nonzero", -math.SmallestNonzeroFloat64},

		// precision boundaries
		{"9.999999999999998", 9.999999999999998},
		{"10.000000000000002", 10.000000000000002},
		{"1+ulp", 1.0000000000000002},
		{"1-ulp", 0.9999999999999998},
		{"nextafter_zero", math.Nextafter(0, 1)},
		{"nextafter_one_up", math.Nextafter(1, 2)},
		{"nextafter_one_down", math.Nextafter(1, 0)},

		// fractions
		{"1/3", 1.0 / 3},
		{"2/3", 2.0 / 3},
		{"1/7", 1.0 / 7},
		{"1/11", 1.0 / 11},
		{"1/13", 1.0 / 13},
		{"1/17", 1.0 / 17},

		// values that exercise carry/rounding
		{"9.9", 9.9},
		{"99.99", 99.99},
		{"999.999", 999.999},
		{"9999.9999", 9999.9999},
		{"99999.99999", 99999.99999},

		// subnormals near boundary
		{"subnormal_mid", 1e-310},
		{"subnormal_small", 1e-320},
		{"subnormal_tiny", 1e-323},

		// format boundary: 1e-6 and 1e21 thresholds
		{"just_below_1e-6", math.Nextafter(1e-6, 0)},
		{"exactly_1e-6", 1e-6},
		{"just_above_1e-6", math.Nextafter(1e-6, 1)},
		{"just_below_1e21", math.Nextafter(1e21, 0)},
		{"exactly_1e21", 1e21},
		{"just_above_1e21", math.Nextafter(1e21, math.MaxFloat64)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			numStr := marshalFloat64(t, tc.val)
			want := stdlibFloat64(t, tc.val)
			if numStr != want {
				t.Errorf("mismatch for %v (%016x):\n  got:  %s\n  want: %s",
					tc.val, math.Float64bits(tc.val), numStr, want)
			}
		})
	}
}

// TestNativeFloat32 — table-driven edge cases for float32 via native VM

func TestNativeFloat32(t *testing.T) {

	cases := []struct {
		name string
		val  float32
	}{
		// zeros
		{"zero", 0.0},
		{"neg_zero", float32(math.Copysign(0, -1))},

		// small integers
		{"one", 1.0},
		{"neg_one", -1.0},
		{"42", 42.0},
		{"100", 100.0},
		{"1000", 1000.0},

		// typical decimals
		{"3.14", 3.14},
		{"0.1", 0.1},
		{"0.2", 0.2},
		{"0.3", 0.3},
		{"1.5", 1.5},
		{"neg_1.5", -1.5},

		// powers of two
		{"0.5", 0.5},
		{"0.25", 0.25},
		{"0.125", 0.125},
		{"1024", 1024.0},
		{"65536", 65536.0},
		{"2^23", 8388608.0},  // exact int boundary for float32
		{"2^24", 16777216.0}, // max exact int for float32

		// powers of ten
		{"1e1", 1e1},
		{"1e5", 1e5},
		{"1e10", 1e10},
		{"1e20", 1e20},
		{"1e30", 1e30},
		{"1e38", 1e38},
		{"1e-1", 1e-1},
		{"1e-5", 1e-5},
		{"1e-10", 1e-10},
		{"1e-20", 1e-20},
		{"1e-30", 1e-30},
		{"1e-38", 1e-38},

		// extremes
		{"max_float32", math.MaxFloat32},
		{"smallest_nonzero", math.SmallestNonzeroFloat32},
		{"smallest_normal", float32(1.1754944e-38)},
		{"neg_max_float32", -math.MaxFloat32},
		{"neg_smallest_nonzero", -math.SmallestNonzeroFloat32},

		// float32 subnormals
		{"subnormal_1.4e-45", 1.4e-45},
		{"subnormal_1e-40", 1e-40},
		{"subnormal_1e-43", 1e-43},

		// precision boundary (~7 significant digits)
		{"9.999999", 9.999999},
		{"10.000001", 10.000001},
		{"nextafter_one_up", math.Nextafter32(1, 2)},
		{"nextafter_one_down", math.Nextafter32(1, 0)},
		{"nextafter_zero", math.Nextafter32(0, 1)},

		// fractions
		{"1/3", 1.0 / 3},
		{"2/3", 2.0 / 3},
		{"1/7", 1.0 / 7},

		// format boundary for float32
		{"f32_just_below_1e-6", math.Nextafter32(1e-6, 0)},
		{"f32_exactly_1e-6", 1e-6},
		{"f32_just_above_1e-6", math.Nextafter32(1e-6, 1)},
		{"f32_just_below_1e21", math.Nextafter32(1e21, 0)},
		{"f32_exactly_1e21", 1e21},
		{"f32_just_above_1e21", math.Nextafter32(1e21, math.MaxFloat32)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			numStr := marshalFloat32(t, tc.val)
			want := stdlibFloat32(t, tc.val)
			if numStr != want {
				t.Errorf("mismatch for %v (%08x):\n  got:  %s\n  want: %s",
					tc.val, math.Float32bits(tc.val), numStr, want)
			}
		})
	}
}

// TestNativeFloat_SpecialValues — NaN / Inf must return error via native VM

func TestNativeFloat_SpecialValues(t *testing.T) {

	t.Run("float64_NaN", func(t *testing.T) {
		w := wrapF64{V: math.NaN()}
		_, err := vjson.Marshal(w)
		if err == nil {
			t.Fatal("expected error for NaN, got nil")
		}
		if _, ok := err.(*vjson.UnsupportedValueError); !ok {
			t.Fatalf("expected *vjson.UnsupportedValueError, got %T: %v", err, err)
		}
	})
	t.Run("float64_PosInf", func(t *testing.T) {
		w := wrapF64{V: math.Inf(1)}
		if _, err := vjson.Marshal(w); err == nil {
			t.Fatal("expected error for +Inf, got nil")
		}
	})
	t.Run("float64_NegInf", func(t *testing.T) {
		w := wrapF64{V: math.Inf(-1)}
		if _, err := vjson.Marshal(w); err == nil {
			t.Fatal("expected error for -Inf, got nil")
		}
	})
	t.Run("float32_NaN", func(t *testing.T) {
		w := wrapF32{V: float32(math.NaN())}
		if _, err := vjson.Marshal(w); err == nil {
			t.Fatal("expected error for float32 NaN, got nil")
		}
	})
	t.Run("float32_PosInf", func(t *testing.T) {
		w := wrapF32{V: float32(math.Inf(1))}
		if _, err := vjson.Marshal(w); err == nil {
			t.Fatal("expected error for float32 +Inf, got nil")
		}
	})
	t.Run("float32_NegInf", func(t *testing.T) {
		w := wrapF32{V: float32(math.Inf(-1))}
		if _, err := vjson.Marshal(w); err == nil {
			t.Fatal("expected error for float32 -Inf, got nil")
		}
	})
}

// TestNativeFloat64_Roundtrip — lossless roundtrip for key float64 values

func TestNativeFloat64_Roundtrip(t *testing.T) {

	values := []float64{
		0, 1, -1, 0.1, 0.2, 0.3,
		math.Pi, math.E, math.Phi, math.Ln2, math.Log2E,
		math.MaxFloat64, math.SmallestNonzeroFloat64,
		-math.MaxFloat64, -math.SmallestNonzeroFloat64,
		2.2250738585072014e-308, 5e-324,
		1e-100, 1e-200, 1e-300,
		1e100, 1e200, 1e308,
		1.0 / 3, 2.0 / 3, 1.0 / 7,
		9007199254740992.0,
		1.7976931348623155e+308,
		math.Nextafter(1, 2),
		math.Nextafter(0, 1),
	}

	for _, val := range values {
		numStr := marshalFloat64(t, val)
		parsed, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q) for val=%v error: %v", numStr, val, err)
		}
		if math.Float64bits(parsed) != math.Float64bits(val) {
			t.Errorf("roundtrip failed for %v:\n  output: %s\n  parsed: %v\n  bits: %016x vs %016x",
				val, numStr, parsed, math.Float64bits(val), math.Float64bits(parsed))
		}
	}
}

// TestNativeFloat32_Roundtrip — lossless roundtrip for key float32 values

func TestNativeFloat32_Roundtrip(t *testing.T) {

	values := []float32{
		0, 1, -1, 0.1, 0.2, 0.3,
		float32(math.Pi), float32(math.E),
		math.MaxFloat32, math.SmallestNonzeroFloat32,
		-math.MaxFloat32, -math.SmallestNonzeroFloat32,
		float32(1.1754944e-38), 1.4e-45,
		1e-10, 1e-20, 1e-30, 1e-38,
		1e10, 1e20, 1e30, 1e38,
		1.0 / 3, 2.0 / 3, 1.0 / 7,
		16777216.0, 16777215.0,
		math.Nextafter32(1, 2),
		math.Nextafter32(0, 1),
	}

	for _, val := range values {
		numStr := marshalFloat32(t, val)
		parsed, err := strconv.ParseFloat(numStr, 32)
		if err != nil {
			t.Fatalf("ParseFloat(%q) for val=%v error: %v", numStr, val, err)
		}
		if math.Float32bits(float32(parsed)) != math.Float32bits(val) {
			t.Errorf("roundtrip failed for %v:\n  output: %s\n  parsed: %v\n  bits: %08x vs %08x",
				val, numStr, float32(parsed), math.Float32bits(val), math.Float32bits(float32(parsed)))
		}
	}
}

// TestNativeFloat64_Random — stress test with random float64 values

func TestNativeFloat64_Random(t *testing.T) {

	rng := rand.New(rand.NewSource(20260227))
	const N = 10000

	var mismatches int
	for i := 0; i < N; i++ {
		bits := rng.Uint64()
		val := math.Float64frombits(bits)
		if math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}

		numStr := marshalFloat64(t, val)
		want := stdlibFloat64(t, val)
		if numStr != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("random #%d mismatch for %v (%016x):\n  got:  %s\n  want: %s",
					i, val, bits, numStr, want)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("... and %d more mismatches (total %d/%d)", mismatches-20, mismatches, N)
	}
}

// TestNativeFloat32_Random — stress test with random float32 values

func TestNativeFloat32_Random(t *testing.T) {

	rng := rand.New(rand.NewSource(20260227))
	const N = 10000

	var mismatches int
	for i := 0; i < N; i++ {
		bits := rng.Uint32()
		val := math.Float32frombits(bits)
		if float64(val) != float64(val) || math.IsInf(float64(val), 0) {
			continue
		}

		numStr := marshalFloat32(t, val)
		want := stdlibFloat32(t, val)
		if numStr != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("random #%d mismatch for %v (%08x):\n  got:  %s\n  want: %s",
					i, val, bits, numStr, want)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("... and %d more mismatches (total %d/%d)", mismatches-20, mismatches, N)
	}
}

// TestNativeFloat_InStruct — floats in struct with pointers and omitempty

func TestNativeFloat_InStruct(t *testing.T) {

	type FloatStruct struct {
		F64     float64  `json:"f64"`
		F32     float32  `json:"f32"`
		PF64    *float64 `json:"pf64"`
		PF32    *float32 `json:"pf32"`
		OmitF64 float64  `json:"omit_f64,omitempty"`
		OmitF32 float32  `json:"omit_f32,omitempty"`
	}

	f64val := 3.141592653589793
	f32val := float32(2.718281828)

	cases := []struct {
		name string
		val  FloatStruct
	}{
		{
			name: "all_fields",
			val: FloatStruct{
				F64: 1.23456789, F32: 9.87654,
				PF64: &f64val, PF32: &f32val,
				OmitF64: 42.0, OmitF32: 0.5,
			},
		},
		{"zero_values", FloatStruct{}},
		{"nil_pointers_omit_zero", FloatStruct{F64: 1.0, F32: 2.0}},
		{
			name: "negative_values",
			val: FloatStruct{
				F64: -99.99, F32: -0.001,
				OmitF64: -0.001, OmitF32: -0.001,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val, vjson.WithFloatExpAuto())
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestNativeFloat64_AllPowersOfTwo — all 2^n for n in [-1074, 1023]

func TestNativeFloat64_AllPowersOfTwo(t *testing.T) {

	for exp := -1074; exp <= 1023; exp++ {
		val := math.Ldexp(1.0, exp)
		if math.IsInf(val, 0) || val == 0 {
			continue
		}
		numStr := marshalFloat64(t, val)
		want := stdlibFloat64(t, val)
		if numStr != want {
			t.Errorf("2^%d = %v:\n  got:  %s\n  want: %s", exp, val, numStr, want)
		}
	}
}

// TestNativeFloat64_AllPowersOfTen — all 10^n for n in [-323, 308]

func TestNativeFloat64_AllPowersOfTen(t *testing.T) {

	for exp := -323; exp <= 308; exp++ {
		val := math.Pow(10, float64(exp))
		if math.IsInf(val, 0) || val == 0 || math.IsNaN(val) {
			continue
		}
		numStr := marshalFloat64(t, val)
		want := stdlibFloat64(t, val)
		if numStr != want {
			t.Errorf("1e%d = %v:\n  got:  %s\n  want: %s", exp, val, numStr, want)
		}
	}
}

// TestNativeFloat64_BoundaryBits — normal/subnormal boundary ±10 ULP

func TestNativeFloat64_BoundaryBits(t *testing.T) {

	const smallestNormalBits uint64 = 0x0010000000000000
	for delta := uint64(0); delta <= 10; delta++ {
		for _, bits := range []uint64{
			smallestNormalBits - delta,
			smallestNormalBits + delta,
		} {
			val := math.Float64frombits(bits)
			numStr := marshalFloat64(t, val)
			want := stdlibFloat64(t, val)
			if numStr != want {
				t.Errorf("boundary bits=%016x:\n  got:  %s\n  want: %s", bits, numStr, want)
			}
		}
	}
}

// TestNativeFloat64_Sequential — sweep interesting bit regions (1000 each)

func TestNativeFloat64_Sequential(t *testing.T) {

	regions := []struct {
		name  string
		start uint64
		count int
	}{
		{"near_zero", 0x0000000000000001, 1000},
		{"near_smallest_normal", 0x0010000000000000 - 500, 1000},
		{"near_one", 0x3FF0000000000000 - 500, 1000},
		{"near_max", 0x7FEFFFFFFFFFFFFF - 999, 1000},
		{"mid_range", 0x4000000000000000, 1000},
		{"small_integers", 0x4340000000000000, 100},
	}

	for _, r := range regions {
		t.Run(r.name, func(t *testing.T) {
			for i := 0; i < r.count; i++ {
				bits := r.start + uint64(i)
				val := math.Float64frombits(bits)
				if math.IsNaN(val) || math.IsInf(val, 0) {
					continue
				}

				numStr := marshalFloat64(t, val)
				want := stdlibFloat64(t, val)
				if numStr != want {
					t.Errorf("bits=%016x val=%v:\n  got:  %s\n  want: %s", bits, val, numStr, want)
				}

				// Also test negative
				negBits := bits | (1 << 63)
				negVal := math.Float64frombits(negBits)
				negStr := marshalFloat64(t, negVal)
				negWant := stdlibFloat64(t, negVal)
				if negStr != negWant {
					t.Errorf("neg bits=%016x:\n  got:  %s\n  want: %s", negBits, negStr, negWant)
				}
			}
		})
	}
}

// TestNativeFloat32_BoundaryBits — float32 normal/subnormal boundary ±10 ULP

func TestNativeFloat32_BoundaryBits(t *testing.T) {

	const smallestNormalBits uint32 = 0x00800000
	for delta := uint32(0); delta <= 10; delta++ {
		for _, bits := range []uint32{
			smallestNormalBits - delta,
			smallestNormalBits + delta,
		} {
			val := math.Float32frombits(bits)
			numStr := marshalFloat32(t, val)
			want := stdlibFloat32(t, val)
			if numStr != want {
				t.Errorf("f32 boundary bits=%08x:\n  got:  %s\n  want: %s", bits, numStr, want)
			}
		}
	}
}

// TestNativeFloat64_Integers — integer values stored as float64

func TestNativeFloat64_Integers(t *testing.T) {

	values := []float64{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
		-1, -2, -10, -100,
		255, 256, 1000, 10000, 100000, 1000000,
		1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15,
		9007199254740992,  // 2^53
		9007199254740991,  // 2^53 - 1
		-9007199254740992, // -2^53
		4503599627370496,  // 2^52
	}

	for _, val := range values {
		numStr := marshalFloat64(t, val)
		want := stdlibFloat64(t, val)
		if numStr != want {
			t.Errorf("integer %v:\n  got:  %s\n  want: %s", val, numStr, want)
		}
	}
}

// TestNativeFloat_ValidJSON — verify native output is valid JSON number

func TestNativeFloat_ValidJSON(t *testing.T) {

	values := []float64{
		0, -0, 1, -1, 0.5, -0.5,
		math.MaxFloat64, -math.MaxFloat64,
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		1e100, 1e-100, 1e200, 1e-200, 1e308, 1e-323,
		math.Pi, math.E,
	}

	for _, val := range values {
		w := wrapF64{V: val}
		got, err := vjson.Marshal(w)
		if err != nil {
			t.Fatalf("vjson.Marshal(%v) error: %v", val, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(got, &m); err != nil {
			t.Errorf("output for %v is not valid JSON: %s\n  error: %v", val, got, err)
		}
	}
}
