package vjson

import (
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
