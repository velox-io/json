package vjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseEightDigitsSWAR(t *testing.T) {
	tests := []struct {
		input string
		want  uint32
	}{
		{"12345678________", 12345678},
		{"00000000________", 0},
		{"99999999________", 99999999},
		{"00000001________", 1},
		{"10000000________", 10000000},
		{"01234567________", 1234567},
	}
	for _, tt := range tests {
		t.Run(tt.input[:8], func(t *testing.T) {
			got := parseEightDigitsSWAR([]byte(tt.input), 0)
			if got != tt.want {
				t.Errorf("parseEightDigitsSWAR(%q) = %d, want %d", tt.input[:8], got, tt.want)
			}
		})
	}
}

func TestParseUint64(t *testing.T) {
	tests := []struct {
		input string
		want  uint64
	}{
		// Short: pure Horner path
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"255", 255},
		{"65535", 65535},
		{"1234567", 1234567},
		// Exactly 8 digits: single SWAR
		{"12345678", 12345678},
		{"00000000", 0},
		{"99999999", 99999999},
		// 9-15 digits: SWAR + Horner tail
		{"123456789", 123456789},
		{"1234567890", 1234567890},
		{"4294967295", 4294967295},
		{"999999999999999", 999999999999999},
		// 16 digits: two SWAR calls
		{"1234567812345678", 1234567812345678},
		// 16+ digits: two SWAR + Horner tail
		{"12345678123456789", 12345678123456789},
		{"18446744073709551615", 18446744073709551615},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			b := []byte(tt.input)
			got := parseUint64(b, 0, len(b))
			if got != tt.want {
				t.Errorf("parseUint64(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"0", 0},
		{"1", 1},
		{"-1", -1},
		{"42", 42},
		{"-42", -42},
		{"-123", -123},
		{"2147483647", 2147483647},
		{"-2147483648", -2147483648},
		{"9223372036854775807", 9223372036854775807},
		// 8-digit unsigned portion
		{"12345678", 12345678},
		{"-12345678", -12345678},
		// 16-digit unsigned portion
		{"-1234567812345678", -1234567812345678},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			b := []byte(tt.input)
			got := parseInt64(b, 0, len(b))
			if got != tt.want {
				t.Errorf("parseInt64(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInt64_Empty(t *testing.T) {
	if got := parseInt64(nil, 0, 0); got != 0 {
		t.Errorf("parseInt64(nil) = %d, want 0", got)
	}
	if got := parseInt64([]byte{}, 0, 0); got != 0 {
		t.Errorf("parseInt64([]) = %d, want 0", got)
	}
}

// TestScanNumber_FloatToInt verifies that float-format numbers are rejected
// when the target field is an integer type, matching encoding/json behavior.
func TestScanNumber_FloatToInt(t *testing.T) {
	tests := []struct {
		json string
		desc string
	}{
		{`{"a":3.14}`, "fractional"},
		{`{"a":1e5}`, "exponent"},
		{`{"a":1.0}`, "dotZero"},
		{`{"a":1E10}`, "upperExponent"},
		{`{"a":-3.14}`, "negativeFractional"},
	}
	for _, tt := range tests {
		t.Run("int_"+tt.desc, func(t *testing.T) {
			var s struct {
				A int `json:"a"`
			}
			err := Unmarshal([]byte(tt.json), &s)
			if err == nil {
				t.Errorf("expected error for %s, got nil (value=%d)", tt.json, s.A)
			} else if !strings.Contains(err.Error(), "cannot unmarshal number") {
				t.Errorf("unexpected error: %v", err)
			}
		})
		t.Run("uint_"+tt.desc, func(t *testing.T) {
			json := strings.Replace(tt.json, "-3.14", "3.14", 1) // avoid negative for uint
			var s struct {
				A uint64 `json:"a"`
			}
			err := Unmarshal([]byte(json), &s)
			if err == nil {
				t.Errorf("expected error for %s, got nil (value=%d)", json, s.A)
			} else if !strings.Contains(err.Error(), "cannot unmarshal number") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	// Verify pure integers still work
	t.Run("int_ok", func(t *testing.T) {
		var s struct {
			A int `json:"a"`
		}
		if err := Unmarshal([]byte(`{"a":42}`), &s); err != nil {
			t.Fatal(err)
		}
		if s.A != 42 {
			t.Errorf("got %d, want 42", s.A)
		}
	})
	t.Run("uint_ok", func(t *testing.T) {
		var s struct {
			A uint64 `json:"a"`
		}
		if err := Unmarshal([]byte(`{"a":12345}`), &s); err != nil {
			t.Fatal(err)
		}
		if s.A != 12345 {
			t.Errorf("got %d, want 12345", s.A)
		}
	})
}

var sinkUint64 uint64

//nolint:unused
var sinkInt64 int64

func BenchmarkParseUint64_4digits(b *testing.B) {
	src := []byte("1234")
	for b.Loop() {
		sinkUint64 = parseUint64(src, 0, 4)
	}
}

func BenchmarkParseUint64_8digits(b *testing.B) {
	src := []byte("12345678")
	for b.Loop() {
		sinkUint64 = parseUint64(src, 0, 8)
	}
}

func BenchmarkParseUint64_10digits(b *testing.B) {
	src := []byte("1234567890")
	for b.Loop() {
		sinkUint64 = parseUint64(src, 0, 10)
	}
}

func BenchmarkParseUint64_16digits(b *testing.B) {
	src := []byte("1234567812345678")
	for b.Loop() {
		sinkUint64 = parseUint64(src, 0, 16)
	}
}

func BenchmarkParseUint64_19digits(b *testing.B) {
	src := []byte("9223372036854775807")
	for b.Loop() {
		sinkUint64 = parseUint64(src, 0, 19)
	}
}

// --- scanNumberSpan RFC 8259 validation tests ---

func TestScanNumberSpan_Valid(t *testing.T) {
	tests := []struct {
		input   string
		isFloat bool
	}{
		// Integers
		{"0", false},
		{"-0", false},
		{"1", false},
		{"-1", false},
		{"9", false},
		{"10", false},
		{"42", false},
		{"123", false},
		{"123456789", false},
		{"-123456789", false},

		// Fractions
		{"0.0", true},
		{"0.5", true},
		{"-0.5", true},
		{"1.0", true},
		{"1.5", true},
		{"123.456", true},
		{"-123.456", true},
		{"0.123456789", true},

		// Exponents
		{"1e10", true},
		{"1E10", true},
		{"1e+10", true},
		{"1e-10", true},
		{"1E+10", true},
		{"1E-10", true},
		{"-1e10", true},
		{"0e0", true},

		// Fraction + exponent
		{"1.5e2", true},
		{"-1.5e-2", true},
		{"0.1e+10", true},
		{"123.456e789", true},
		{"1.0E1", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			src := []byte(tt.input)
			end, isFloat, err := scanNumberSpan(src, 0)
			if err != nil {
				t.Fatalf("scanNumberSpan(%q) unexpected error: %v", tt.input, err)
			}
			if end != len(src) {
				t.Errorf("scanNumberSpan(%q) end=%d, want %d", tt.input, end, len(src))
			}
			if isFloat != tt.isFloat {
				t.Errorf("scanNumberSpan(%q) isFloat=%v, want %v", tt.input, isFloat, tt.isFloat)
			}
		})
	}
}

func TestScanNumberSpan_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// Leading zeros
		{"leading zero", "01"},
		{"leading zeros", "00"},
		{"negative leading zero", "-01"},
		{"negative leading zeros", "-00123"},

		// Missing digits
		{"plus sign", "+1"},
		{"bare minus", "-"},
		{"bare dot", "."},
		{"leading dot", ".5"},

		// Trailing dot (no digits after)
		{"trailing dot", "1."},
		{"negative trailing dot", "-1."},

		// Bad exponents
		{"bare exponent", "1e"},
		{"bare exponent upper", "1E"},
		{"exponent plus no digit", "1e+"},
		{"exponent minus no digit", "1e-"},
		{"bare frac exponent", "1.0e"},
		{"bare frac exponent+", "1.0e+"},

		// Double characters — scanNumberSpan stops at second occurrence;
		// the trailing garbage is caught by the caller.
		// These are tested at integration level in TestRFC8259_InvalidNumbers.
		// {"double dot", "1.2.3"},
		// {"double exponent", "1e2e3"},

		// Non-number literals
		{"double minus", "--1"},
		{"NaN", "NaN"},
		{"Infinity", "Infinity"},
		// 0x1F: scanNumberSpan sees "0" then stops at 'x' (valid "0" span).
		// Trailing "x1F" is rejected by the caller.
		// {"hex", "0x1F"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte(tt.input)
			_, _, err := scanNumberSpan(src, 0)
			if err == nil {
				t.Errorf("scanNumberSpan(%q) should return error, got nil", tt.input)
			}
		})
	}
}

// TestScanNumberSpan_TrailingChars verifies that the parser stops at the right
// position when a valid number is followed by non-number characters.
func TestScanNumberSpan_TrailingChars(t *testing.T) {
	tests := []struct {
		input   string
		wantEnd int
		isFloat bool
	}{
		{"123,", 3, false},
		{"0}", 1, false},
		{"-0]", 2, false},
		{"1.5,", 3, true},
		{"1e10}", 4, true},
		{"123 ", 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			src := []byte(tt.input)
			end, isFloat, err := scanNumberSpan(src, 0)
			if err != nil {
				t.Fatalf("scanNumberSpan(%q) unexpected error: %v", tt.input, err)
			}
			if end != tt.wantEnd {
				t.Errorf("scanNumberSpan(%q) end=%d, want %d", tt.input, end, tt.wantEnd)
			}
			if isFloat != tt.isFloat {
				t.Errorf("scanNumberSpan(%q) isFloat=%v, want %v", tt.input, isFloat, tt.isFloat)
			}
		})
	}
}

// TestRFC8259_InvalidNumbers tests that Unmarshal rejects invalid number formats,
// matching encoding/json behavior.
func TestRFC8259_InvalidNumbers(t *testing.T) {
	invalid := []string{
		`{"a":01}`,
		`{"a":00}`,
		`{"a":-01}`,
		`{"a":+1}`,
		`{"a":1.}`,
		`{"a":.5}`,
		`{"a":1e}`,
		`{"a":1e+}`,
		`{"a":1.2.3}`,
		`{"a":--1}`,
		`{"a":1ee2}`,
		`{"a":NaN}`,
		`{"a":Infinity}`,
		`{"a":0x1F}`,
	}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			var m map[string]any
			vjErr := Unmarshal([]byte(input), &m)
			if vjErr == nil {
				t.Errorf("vjson should reject %s, got value: %v", input, m)
			}

			// Verify encoding/json also rejects
			var stdM map[string]any
			stdErr := json.Unmarshal([]byte(input), &stdM)
			if stdErr == nil {
				t.Logf("NOTE: encoding/json accepts %s — divergence acceptable", input)
			}
		})
	}
}

// TestRFC8259_ValidNumbers tests that Unmarshal accepts valid number formats
// and produces the same results as encoding/json.
func TestRFC8259_ValidNumbers(t *testing.T) {
	valid := []struct {
		input string
		want  float64
	}{
		{`{"a":0}`, 0},
		{`{"a":-0}`, 0},
		{`{"a":1}`, 1},
		{`{"a":-1}`, -1},
		{`{"a":42}`, 42},
		{`{"a":123456789}`, 123456789},
		{`{"a":1.0}`, 1.0},
		{`{"a":1.5}`, 1.5},
		{`{"a":-0.5}`, -0.5},
		{`{"a":1e10}`, 1e10},
		{`{"a":1E10}`, 1e10},
		{`{"a":1e+10}`, 1e10},
		{`{"a":1e-10}`, 1e-10},
		{`{"a":1.5e2}`, 150},
		{`{"a":-1.5e-2}`, -0.015},
		{`{"a":0.0}`, 0},
		{`{"a":0e0}`, 0},
	}
	for _, tt := range valid {
		t.Run(tt.input, func(t *testing.T) {
			var m map[string]any
			if err := Unmarshal([]byte(tt.input), &m); err != nil {
				t.Fatalf("vjson should accept %s: %v", tt.input, err)
			}
			got, ok := m["a"].(float64)
			if !ok {
				t.Fatalf("expected float64, got %T", m["a"])
			}
			if got != tt.want {
				t.Errorf("%s: got %v, want %v", tt.input, got, tt.want)
			}

			// Cross-check with encoding/json
			var stdM map[string]any
			if err := json.Unmarshal([]byte(tt.input), &stdM); err != nil {
				t.Fatalf("encoding/json should accept %s: %v", tt.input, err)
			}
			stdGot := stdM["a"].(float64)
			if got != stdGot {
				t.Errorf("%s: vjson=%v, encoding/json=%v", tt.input, got, stdGot)
			}
		})
	}
}
