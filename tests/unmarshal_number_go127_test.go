//go:build go1.27

package tests

import (
	"encoding/json"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// Long mantissas: the multiprecision refine path must stay bounded and
// correctly rounded for any digit count, at both float32 and float64.
//
// The float32 comparison needs Go 1.27: older stdlib drops mantissa digits
// beyond the 800 entry strconv decimal buffer without advancing the decimal
// point, so a huge number that overflows float32 can parse to a shrunken
// in-range value with no error where vjson reports the overflow.
func TestNumber_LongMantissa(t *testing.T) {
	// Exact decimal expansion of the midpoint between 1 and its successor
	// (1 + 2^-53), so the refine path decides purely on the digit tail.
	const mid64 = "1.00000000000000011102230246251565404236316680908203125"
	tests := []struct {
		name  string
		input string
	}{
		// fuzz: 1587 significant digits overflowed the mp limbs (SIGSEGV)
		{"fuzz_1587_digits", "1." + strings.Repeat("0", 15) + strings.Repeat("5", 1538) + strings.Repeat("0", 31) + "5"},
		{"mid64_exact", mid64},
		{"mid64_long_zero_tail", mid64 + strings.Repeat("0", 2000)},
		{"mid64_sticky_tail", mid64 + strings.Repeat("0", 2000) + "1"},
		{"mid64_sticky_tail_exp", mid64 + strings.Repeat("0", 2000) + "1e-300"},
		{"long_int", strings.Repeat("7", 3000) + "e-2800"},
		{"long_subnormal", "0." + strings.Repeat("0", 320) + strings.Repeat("4", 3000)},
		// float32 fast path used to accept a wrapped >19-digit mantissa
		{"f32_wrapped_mantissa", "9.4825950" + strings.Repeat("0", 62) + "1e+09"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.input)
			var vj64, std64 float64
			if err := vjson.Unmarshal(data, &vj64); err != nil {
				t.Fatalf("float64: vjson error: %v", err)
			}
			if err := json.Unmarshal(data, &std64); err != nil {
				t.Fatalf("float64: stdlib error: %v", err)
			}
			if vj64 != std64 {
				t.Errorf("float64: vjson=%.17g stdlib=%.17g", vj64, std64)
			}
			var vj32, std32 float32
			vjErr := vjson.Unmarshal(data, &vj32)
			stdErr := json.Unmarshal(data, &std32)
			if (vjErr != nil) != (stdErr != nil) || (vjErr == nil && vj32 != std32) {
				t.Errorf("float32: vjson=%g err=%v, stdlib=%g err=%v", vj32, vjErr, std32, stdErr)
			}
		})
	}
}
