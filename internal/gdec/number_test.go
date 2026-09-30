package gdec

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// TestFloatTokenRounding checks every exact FloatToken result against
// strconv, the correctly rounded reference, across the mantissa and
// exponent ranges each finalization path serves.
func TestFloatTokenRounding(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	check := func(s string) {
		t.Helper()
		f, end, ok, exact := FloatToken([]byte(s), 0)
		if !ok || end != len(s) {
			t.Fatalf("%q: ok=%v end=%d", s, ok, end)
		}
		if !exact {
			return
		}
		want, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%q: exact token strconv rejects: %v", s, err)
		}
		if math.Float64bits(f) != math.Float64bits(want) {
			t.Fatalf("%q: got %v (%#x), want %v (%#x)", s, f, math.Float64bits(f), want, math.Float64bits(want))
		}
	}
	for _, s := range []string{
		"0", "-0", "0.0", "1", "-65.613616999999977", "9007199254740993", "18446744073709551615",
		"1e22", "1e23", "2.2250738585072011e-308", "2.2250738585072014e-308", "4.9406564584124654e-324",
		"1.7976931348623157e308", "1.7976931348623158e308", "0.000000000000000000001234", "123456789012345678e-30",
		"7.2057594037927933e16", "9223372036854775807", "9223372036854775808e-5", "1e-342", "1e308",
	} {
		check(s)
	}
	for range 2_000_000 {
		nd := 1 + r.IntN(19)
		var b []byte
		if r.IntN(2) == 0 {
			b = append(b, '-')
		}
		b = append(b, byte('1'+r.IntN(9)))
		for range nd - 1 {
			b = append(b, byte('0'+r.IntN(10)))
		}
		if r.IntN(2) == 0 && len(b) > 2 {
			dot := 1 + r.IntN(len(b)-1)
			if b[0] == '-' && dot == 1 {
				dot = 2
			}
			b = append(b[:dot], append([]byte{'.'}, b[dot:]...)...)
			if b[len(b)-1] == '.' {
				b = append(b, '0')
			}
		}
		if r.IntN(3) != 0 {
			b = append(b, 'e')
			b = strconv.AppendInt(b, int64(r.IntN(700)-350), 10)
		}
		check(string(b))
	}
}
