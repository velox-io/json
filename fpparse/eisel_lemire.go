package fpparse

import "math/bits"

// Pow10f64 contains exact powers of 10 representable as float64 (10^0 through 10^22).
var Pow10f64 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19,
	1e20, 1e21, 1e22,
}

// EiselLemire converts mantissa * 10^power10 to a float64 bit pattern using the
// Eisel-Lemire algorithm. Returns (bits, true) on success, (0, false) when the
// result is ambiguous or out of range and the caller must fall back to
// strconv.ParseFloat.
//
// Preconditions: mantissa != 0, 1 <= digitCount <= 19.
//
//go:noinline
func EiselLemire(mantissa uint64, power10 int) (uint64, bool) {
	const (
		mantissaBits = 52
		exponentBias = -1023
	)

	if power10 < pow10Min || power10 > pow10Max {
		return 0, false
	}

	// Look up the 128-bit approximation of 10^power10.
	power := pow10Tab[power10-pow10Min]

	// Estimate the binary exponent: 10^e approx 2^(e * log2(10)).
	// 108853 / 2^15 approx 3.321928... approx log2(10).
	binaryExp := 1 + (power10*108853)>>15

	// Normalize: shift mantissa so its leading bit is at position 63.
	leadingZeros := bits.LeadingZeros64(mantissa)
	mantissa <<= uint(leadingZeros)
	resultExp := uint64(binaryExp+63-exponentBias) - uint64(leadingZeros)

	// 128-bit multiply: mantissa * power.hi (high 64 bits of 10^exp10)
	prodHi, prodLo := bits.Mul64(mantissa, power.hi)

	// If the product falls near a rounding boundary (low 9 bits all set)
	// and prodLo overflowed, refine with power.lo (low 64 bits).
	if prodHi&0x1FF == 0x1FF && prodLo+mantissa < mantissa {
		crossHi, crossLo := bits.Mul64(mantissa, power.lo)
		refinedLo := prodLo + crossHi
		if refinedLo < prodLo {
			prodHi++ // carry
		}
		if prodHi&0x1FF == 0x1FF && refinedLo+1 == 0 && crossLo+mantissa < mantissa {
			return 0, false // still ambiguous after refinement
		}
		prodLo = refinedLo
	}

	// Extract 54-bit mantissa from the 128-bit product.
	// Bit 63 determines the shift: if set, shift by 10; otherwise by 9.
	topBit := prodHi >> 63
	resultMantissa := prodHi >> (topBit + 9)
	resultExp -= 1 ^ topBit

	// Ambiguous: exactly halfway and rounding direction is uncertain.
	if prodLo == 0 && prodHi&0x1FF == 0 && resultMantissa&3 == 1 {
		return 0, false
	}

	// Round to nearest even.
	resultMantissa += resultMantissa & 1
	resultMantissa >>= 1
	if resultMantissa>>53 > 0 {
		resultMantissa >>= 1
		resultExp++
	}

	// Overflow or underflow.
	if resultExp-1 >= 0x7FF-1 {
		return 0, false
	}

	// Assemble IEEE 754 float64 bit pattern (without sign).
	return resultExp<<mantissaBits | resultMantissa&(1<<mantissaBits-1), true
}
