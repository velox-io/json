// Copyright 2025 The Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fpparse

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helper: convert text → (d, p) so we can feed Parse with readable cases.
// ---------------------------------------------------------------------------

// parseText extracts the significand d and exponent p from a decimal string
// such that the string represents d * 10^p. Only used in tests.
// Handles: "123", "1.23", "1.23e10", "-1.23e-5", etc.
// Returns d (always non-negative), p, neg, ok.
func parseText(s string) (d uint64, p int, neg bool, ok bool) {
	i := 0

	// Sign.
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	} else if i < len(s) && s[i] == '+' {
		i++
	}

	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }

	// Accumulate up to 19 significant digits, skip leading zeros.
	const maxSig = 19
	nd := 0 // significant digits stored
	dp := 0 // exponent adjustment
	start := i
	sawDot := false

	// Integer part.
	for ; i < len(s) && isDigit(s[i]); i++ {
		if s[i] == '0' && nd == 0 {
			continue // leading zero
		}
		if nd < maxSig {
			d = d*10 + uint64(s[i]-'0')
			nd++
		} else {
			dp++ // overflow integer digit
		}
	}

	// Fractional part.
	if i < len(s) && s[i] == '.' {
		sawDot = true
		i++
		for ; i < len(s) && isDigit(s[i]); i++ {
			if s[i] == '0' && nd == 0 {
				dp--
				continue
			}
			if nd < maxSig {
				d = d*10 + uint64(s[i]-'0')
				nd++
				dp--
			}
		}
	}
	_ = sawDot

	if i == start {
		return 0, 0, false, false
	}

	// Exponent.
	ep := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		esign := 1
		if i < len(s) && s[i] == '-' {
			esign = -1
			i++
		} else if i < len(s) && s[i] == '+' {
			i++
		}
		for ; i < len(s) && isDigit(s[i]); i++ {
			ep = ep*10 + int(s[i]-'0')
		}
		ep *= esign
	}

	if i != len(s) {
		return 0, 0, false, false
	}

	return d, dp + ep, neg, true
}

// mustParseText calls parseText and applies sign+Parse, for test convenience.
func mustParseText(t *testing.T, s string) float64 {
	t.Helper()
	d, p, neg, ok := parseText(s)
	if !ok {
		t.Fatalf("parseText(%q) failed", s)
	}
	f := Parse(d, p)
	if neg {
		f = -f
	}
	return f
}

// checkParse verifies Parse(d,p) == strconv.ParseFloat(fmt.Sprintf("%de%d", d, p)).
func checkParse(t *testing.T, d uint64, p int) {
	t.Helper()
	s := fmt.Sprintf("%de%d", d, p)
	want, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(want, 0) || math.IsNaN(want) {
		return // skip values strconv can't represent finitely
	}
	got := Parse(d, p)
	if got != want {
		t.Errorf("Parse(%d, %d) = %v (bits %016x), want %v (bits %016x)",
			d, p, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// checkText verifies parseText→Parse matches strconv.ParseFloat for a string.
func checkText(t *testing.T, s string) {
	t.Helper()
	want, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return
	}
	got := mustParseText(t, s)
	if got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
		t.Errorf("parseText+Parse(%q) = %v (bits %016x), want %v (bits %016x)",
			s, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// ---------------------------------------------------------------------------
// Basic Parse(d, p) tests
// ---------------------------------------------------------------------------

func TestParseBasic(t *testing.T) {
	tests := []struct {
		d    uint64
		p    int
		want float64
	}{
		{0, 0, 0},
		{1, 0, 1},
		{1, 1, 10},
		{1, -1, 0.1},
		{5, -1, 0.5},
		{15, -1, 1.5},
		{314159265358979, -14, 3.14159265358979},
		{123, -10, 1.23e-8},
		{123456789, 5, 1.23456789e13},
		{9007199254740992, 0, 9007199254740992}, // 2^53
		{1, -300, 1e-300},
		{1, 300, 1e300},
	}
	for _, tt := range tests {
		got := Parse(tt.d, tt.p)
		if got != tt.want {
			t.Errorf("Parse(%d, %d) = %v, want %v", tt.d, tt.p, got, tt.want)
		}
	}
}

func TestParseZero(t *testing.T) {
	for _, p := range []int{-400, -100, 0, 100, 400} {
		if f := Parse(0, p); f != 0 {
			t.Errorf("Parse(0, %d) = %v, want 0", p, f)
		}
	}
}

func TestParseOverflowUnderflow(t *testing.T) {
	// Underflow: exponent far below table range
	if got := Parse(1, -400); got != 0 {
		t.Errorf("Parse(1, -400) = %v, want 0", got)
	}
	if got := Parse(9999999999999999999, -400); got != 0 {
		t.Errorf("Parse(max19, -400) = %v, want 0", got)
	}
	// Overflow: exponent far above table range
	if got := Parse(1, 400); !math.IsInf(got, 1) {
		t.Errorf("Parse(1, 400) = %v, want +Inf", got)
	}
	if got := Parse(9999999999999999999, 400); !math.IsInf(got, 1) {
		t.Errorf("Parse(max19, 400) = %v, want +Inf", got)
	}
}

// ---------------------------------------------------------------------------
// Powers of 10: every power in [−325, 308]
// ---------------------------------------------------------------------------

func TestParseAllPowersOf10(t *testing.T) {
	for p := -325; p <= 308; p++ {
		checkParse(t, 1, p)
	}
}

// ---------------------------------------------------------------------------
// Powers of 2: every float64 that is an exact power of 2
// ---------------------------------------------------------------------------

func TestParsePowersOf2(t *testing.T) {
	for e := -1074; e <= 1023; e++ {
		f := math.Ldexp(1, e)
		if math.IsInf(f, 0) || f == 0 {
			continue
		}
		s := strconv.FormatFloat(f, 'e', -1, 64)
		checkText(t, s)
	}
}

// ---------------------------------------------------------------------------
// Subnormals (exponent field == 0, mantissa 1..2^52-1)
// ---------------------------------------------------------------------------

func TestParseSubnormals(t *testing.T) {
	// Specific subnormal mantissa bits
	subnormals := []uint64{
		1, // smallest subnormal: 5e-324
		2, 3, 7, 0xFF,
		0xFFF,
		0xFFFFF,
		0xFFFFFFFFF,
		0xFFFFFFFFFFFFF, // largest subnormal
		1 << 51,         // boundary subnormal
		(1 << 52) - 1,   // max subnormal
	}
	for _, m := range subnormals {
		f := math.Float64frombits(m)
		s := strconv.FormatFloat(f, 'e', 19, 64)
		checkText(t, s)
		// Also shortest representation
		s = strconv.FormatFloat(f, 'e', -1, 64)
		checkText(t, s)
	}
}

// Test subnormals near the normal/subnormal boundary
func TestParseSubnormalBoundary(t *testing.T) {
	// Smallest normal: 2^-1022 = 2.2250738585072014e-308
	minNormal := math.Float64frombits(0x0010000000000000)
	// Largest subnormal: just below min normal
	maxSubnormal := math.Nextafter(minNormal, 0)

	for _, f := range []float64{minNormal, maxSubnormal,
		math.Nextafter(minNormal, math.Inf(1)),
		math.Nextafter(maxSubnormal, 0)} {
		s := strconv.FormatFloat(f, 'e', 19, 64)
		checkText(t, s)
	}
}

// ---------------------------------------------------------------------------
// Max float64 and neighbors
// ---------------------------------------------------------------------------

func TestParseMaxFloat64(t *testing.T) {
	maxF := math.MaxFloat64
	belowMax := math.Nextafter(maxF, 0)

	for _, f := range []float64{maxF, belowMax} {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		checkText(t, s)
		s = strconv.FormatFloat(f, 'e', 19, 64)
		checkText(t, s)
	}

	// 1.7976931348623157e308 — exact max
	checkText(t, "1.7976931348623157e308")
	// 1.7976931348623158e308 — rounds to max or inf
	checkText(t, "1.7976931348623158e308")
}

// ---------------------------------------------------------------------------
// Halfway rounding cases: values exactly between two adjacent float64s.
// These are the hardest cases for any parser.
// ---------------------------------------------------------------------------

// manualHardCases are float64 bit patterns known to stress the rounding logic.
// Adapted from rsc.io/fpfmt test suite.
var manualHardCases = []uint64{
	0x0040000000000000, // near subnormal boundary
	0x000fffffffffffff, // max subnormal
	0x0010000000000000, // min normal
	0x0010000000000001, // min normal + 1 ulp
	0x7fefffffffffffff, // max float64
	0x7feffffffffffffe, // max float64 - 1 ulp

	// Cases where uscale sticky-bit computation is critical:
	// m * 10^p mod 1 ≈ 1/2, requiring precise sticky bit.
	uint64(math.Float64bits(math.Float64frombits(0x13de005bd620df00 >> 11))), // near 1/2
	uint64(math.Float64bits(math.Float64frombits(0x17c0747bd76fa100 >> 11))),
	uint64(math.Float64bits(math.Float64frombits(0x1491daad0ba28000 >> 11))),
}

func TestParseHardCases(t *testing.T) {
	fail := 0
	for _, bits := range manualHardCases {
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}
		// Test with the float and its two neighbors
		for _, ff := range []float64{
			math.Nextafter(f, math.Inf(-1)),
			f,
			math.Nextafter(f, math.Inf(1)),
		} {
			if math.IsInf(ff, 0) || ff == 0 {
				continue
			}
			// Test with 17 and 19 digits
			for _, prec := range []int{-1, 16, 18} {
				s := strconv.FormatFloat(ff, 'e', prec, 64)
				d, p, neg, ok := parseText(s)
				if !ok || neg {
					continue
				}
				want, _ := strconv.ParseFloat(s, 64)
				got := Parse(d, p)
				if got != want {
					t.Errorf("Parse(%d, %d) [from %q] = %v (bits %016x), want %v (bits %016x)",
						d, p, s, got, math.Float64bits(got), want, math.Float64bits(want))
					if fail++; fail >= 50 {
						t.Fatal("too many failures")
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Nudge tests: for each float, format as 17-digit d*10^p, then nudge d by
// ±1..±3 to create values near rounding boundaries.
// This is the same strategy used in rsc.io/fpfmt.
// ---------------------------------------------------------------------------

func TestParseNudge(t *testing.T) {
	var seed [32]byte
	r := rand.New(rand.NewChaCha8(seed))
	fail := 0

	for range 50000 {
		x := r.Uint64N(1<<63 - 1<<52)
		f := math.Float64frombits(x)
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}

		// Format with 17 significant digits → d * 10^p
		s := fmt.Sprintf("%.16e", f)
		d, p, _, ok := parseText(s)
		if !ok {
			continue
		}

		// Nudge d by small amounts around the rounding boundary
		for delta := int64(-3); delta <= 3; delta++ {
			d2 := d + uint64(delta)
			if d2 == 0 || d2 > uint64(1e19) {
				continue
			}
			s2 := fmt.Sprintf("%de%d", d2, p)
			want, err := strconv.ParseFloat(s2, 64)
			if err != nil || math.IsInf(want, 0) {
				continue
			}
			got := Parse(d2, p)
			if got != want {
				t.Errorf("Parse(%d, %d) = %v (bits %016x), want %v (bits %016x)",
					d2, p, got, math.Float64bits(got), want, math.Float64bits(want))
				if fail++; fail >= 30 {
					t.Fatal("too many failures")
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Exhaustive random d, p → compare with strconv (200K cases)
// ---------------------------------------------------------------------------

func TestParseRawExhaustive(t *testing.T) {
	var seed [32]byte
	r := rand.New(rand.NewChaCha8(seed))
	fail := 0
	for range 200000 {
		d := r.Uint64N(uint64(1e19))
		p := int(r.Int64N(600)) - 300
		if d == 0 {
			continue
		}
		s := fmt.Sprintf("%de%d", d, p)
		want, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(want, 0) {
			continue
		}
		got := Parse(d, p)
		if got != want {
			t.Errorf("Parse(%d, %d) = %v (bits %016x), strconv(%q) = %v (bits %016x)",
				d, p, got, math.Float64bits(got), s, want, math.Float64bits(want))
			if fail++; fail >= 20 {
				t.Fatal("too many failures")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Round-trip: random float64 → FormatFloat → parseText → Parse → compare
// ---------------------------------------------------------------------------

func TestParseRandomRoundTrip(t *testing.T) {
	var seed [32]byte
	r := rand.New(rand.NewChaCha8(seed))
	fail := 0
	for range 100000 {
		x := r.Uint64N(1<<63 - 1<<52)
		f := math.Float64frombits(x)
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}

		s := strconv.FormatFloat(f, 'e', -1, 64)
		want, _ := strconv.ParseFloat(s, 64)
		d, p, _, ok := parseText(s)
		if !ok {
			continue
		}
		got := Parse(d, p)
		if got != want {
			t.Errorf("Parse(%d, %d) from %q = %v (bits %016x), want %v (bits %016x)",
				d, p, s, got, math.Float64bits(got), want, math.Float64bits(want))
			if fail++; fail >= 20 {
				t.Fatal("too many failures")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test with 17, 18, 19 digit precision for random floats.
// 17 digits is the minimum to distinguish all float64s.
// 18-19 test that Parse handles "over-precision" correctly.
// ---------------------------------------------------------------------------

func TestParseMultiplePrecisions(t *testing.T) {
	var seed [32]byte
	r := rand.New(rand.NewChaCha8(seed))
	fail := 0

	for range 30000 {
		x := r.Uint64N(1<<63 - 1<<52)
		f := math.Float64frombits(x)
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}

		for _, prec := range []int{16, 17, 18} { // 17, 18, 19 significant digits
			s := fmt.Sprintf("%.*e", prec, f)
			want, _ := strconv.ParseFloat(s, 64)
			d, p, _, ok := parseText(s)
			if !ok || d > uint64(1e19) {
				continue
			}
			got := Parse(d, p)
			if got != want {
				t.Errorf("prec=%d: Parse(%d, %d) from %q = %v (bits %016x), want %v (bits %016x)",
					prec+1, d, p, s, got, math.Float64bits(got), want, math.Float64bits(want))
				if fail++; fail >= 30 {
					t.Fatal("too many failures")
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Specific well-known tricky decimal strings
// ---------------------------------------------------------------------------

func TestParseKnownTricky(t *testing.T) {
	// These are well-known difficult-to-parse decimal strings that exercise
	// exact-halfway and near-halfway rounding.
	cases := []string{
		// Exactly representable
		"1", "2", "0.5", "0.25", "0.125",
		// Classic 0.1 family — not exactly representable
		"0.1", "0.2", "0.3",
		// Financial rounding stress
		"1.005", "2.675", "4.015", "4.025",
		// Powers of 10
		"1e23", "1e-23",
		// Subnormals
		"5e-324", // smallest positive float64
		"1e-323", // 2 * smallest
		"1.5e-323",
		"2.4703282292062327e-324", // 5e-324 as full decimal
		// Normal boundary
		"2.2250738585072014e-308", // min normal
		"2.2250738585072011e-308", // max subnormal
		// Max float64
		"1.7976931348623157e308",
		"1.7976931348623158e308",
		// Near-halfway cases found by various fuzz testers
		"2.2250738585072012e-308",
		"2.2250738585072013e-308",
		"6.103515625e-05",  // exact 2^-14
		"9007199254740993", // 2^53 + 1 — needs rounding
		"9007199254740994", // 2^53 + 2
		"9007199254740995", // 2^53 + 3 — halfway
		"9007199254740996", // 2^53 + 4
		// Very long but exact
		"1",
		"10",
		"100",
		"1000000000000000",
		"10000000000000000",
		// Stress parsing with many digits
		"7.4109846876186981626485318930233205854758970392148714663837852375101326090531312779794975454245398856969484704316857659638998506553390969459816219401617585601223783614466868137673548102486194441137281543903320090021823769399871101184019073413047688958108448495844675097388567486839523807014099023548531503927215942944714057394224866343738975918183773734499618105635079700100959327544721296909466234046625678302773914918605035778245897743830046225883582029305022051398781292455694563264959711750427258116916992443833368449606432999483798590042376209551273167673156768596197362723619200368868987710218930836990145605230372389492985614793043225981606097785093378001282, " +
			"where 7.4109846876186981626485318930233205854758970392148714663837852375101326090531312779794975454245398856969484704316857659638998506553390969459816219401617585601223783614466868137673548102486194441137281543903320090021823769399871101184019073413047688958108448495844675097388567486839523807014099023548531503927215942944714057394224866343738975918183773734499618105635079700100959327544721296909466234046625678302773914918605035778245897743830046225883582029305022051398781292455694563264959711750427258116916992443833368449606432999483798590042376209551273167673156768596197362723619200368868987710218930836990145605230372389492985614793043225981606097785093378001282 is 2^-1074",
	}
	for _, s := range cases {
		// Some strings may contain extra text; use only the number part.
		numStr := s
		if before, _, ok := strings.Cut(s, ","); ok {
			numStr = strings.TrimSpace(before)
		}
		checkText(t, numStr)
	}
}

// ---------------------------------------------------------------------------
// Edge cases on Parse(d, p) boundaries
// ---------------------------------------------------------------------------

func TestParseEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		d    uint64
		p    int
	}{
		// Rounding boundaries
		{"5 * 10^-1", 5, -1},
		{"15 * 10^-1", 15, -1},
		{"25 * 10^-1", 25, -1},
		{"35 * 10^-1", 35, -1},
		{"45 * 10^-1", 45, -1},
		// Table boundary exponents
		{"d=1 p=-348 (pow10Min)", 1, -348},
		{"d=1 p=347 (pow10Max)", 1, 347},
		{"d=1 p=-347", 1, -347},
		{"d=1 p=346", 1, 346},
		// Large d near 10^19
		{"max 19-digit d", 9999999999999999999, 0},
		{"10^19", 10000000000000000000, 0},
		{"10^19 - 1", 9999999999999999999, -10},
		// Small d
		{"d=1 p=0", 1, 0},
		{"d=1 p=-1", 1, -1},
		{"d=1 p=1", 1, 1},
		{"d=2 p=0", 2, 0},
		// Results near 2^53 (mantissa overflow boundary)
		{"near 2^53", 9007199254740992, 0},
		{"near 2^53+1", 9007199254740993, 0},
		{"near 2^53+2", 9007199254740994, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkParse(t, tt.d, tt.p)
		})
	}
}

// ---------------------------------------------------------------------------
// strconv round-trip for all powers of 10 via text parsing
// ---------------------------------------------------------------------------

func TestParseTextPowersOf10(t *testing.T) {
	for p := -308; p <= 308; p++ {
		s := fmt.Sprintf("1e%d", p)
		checkText(t, s)
	}
}

// ---------------------------------------------------------------------------
// Negative numbers through parseText
// ---------------------------------------------------------------------------

func TestParseTextNegative(t *testing.T) {
	cases := []string{
		"-0", "-1", "-1.5", "-0.1", "-3.14159265358979",
		"-1e10", "-1e-10", "-1.7976931348623157e308",
		"-5e-324", "-2.2250738585072014e-308",
	}
	for _, s := range cases {
		want, _ := strconv.ParseFloat(s, 64)
		got := mustParseText(t, s)
		if got != want {
			t.Errorf("parseText+Parse(%q) = %v (bits %016x), want %v (bits %016x)",
				s, got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Parse(314159265358979, -14)
	}
}

func BenchmarkStrconvParseFloat(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		strconv.ParseFloat("3.14159265358979", 64)
	}
}
