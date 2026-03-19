package vjson

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"unsafe"
)

// parseEightDigits parses exactly 8 ASCII digits into a uint32 using SWAR.
func parseEightDigits(src []byte, i int) uint32 {
	val := *(*uint64)(unsafe.Pointer(&src[i]))
	val = (val & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
	val = (val & 0x00FF00FF00FF00FF) * 6553601 >> 16
	val = (val & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
	return uint32(val)
}

// scanFloat64 parses a JSON number starting at src[idx] into float64.
//
// The algorithm scans digits in a single pass using SWAR (8 bytes at a time),
// then converts the accumulated mantissa × 10^power10 to float64 through
// three tiers of increasing generality:
//
//   - Tier 1 — Exact pow10: when ≤15 significant digits and |power10| ≤ 22,
//     float64 multiplication/division with exact powers of 10 is correctly rounded.
//
//   - Tier 2 — Eisel-Lemire: for ≤19 significant digits, computes a 128-bit
//     product of the mantissa with a precomputed 128-bit approximation of 10^exp.
//     The high bits of the product directly yield the float64 mantissa and exponent.
//     When the product falls exactly on a rounding boundary (ambiguous halfway case)
//     or the result is subnormal/overflow, the algorithm cannot guarantee correctness
//     and falls through to Tier 3.
//
//   - Tier 3 — strconv.ParseFloat: handles all remaining cases (>19 digits,
//     ambiguous rounding, extreme exponents) via Go's standard library.
func scanFloat64(src []byte, idx int) (end int, value float64, err error) {
	const (
		mantissaBits = 52    // float64 explicit mantissa width
		exponentBias = -1023 // float64 exponent bias
	)

	length := len(src)
	pos := idx
	negative := false

	// ── Sign ──

	if pos < length && sliceAt(src, pos) == '-' {
		negative = true
		pos++
	}

	if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
		return pos, 0, fmt.Errorf("invalid number at position %d", idx)
	}

	var mantissa uint64
	digitCount := 0
	fracStart := -1 // digit index where fractional part begins; -1 means no fraction

	// ── Integer digits ──

	if sliceAt(src, pos) == '0' {
		pos++
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			return pos, 0, fmt.Errorf("leading zeros in number at position %d", idx)
		}
	} else {
		// SWAR: accumulate 8 ASCII digits at a time.
		//   above9 overflows the high bit if any byte > '9'
		//   below0 sets the high bit if any byte < '0'
		for pos+8 <= length {
			w := *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), pos))
			above9 := w + 0x4646464646464646
			below0 := w - 0x3030303030303030
			if (above9|below0)&hi64 != 0 {
				break
			}
			if digitCount+8 <= 19 {
				mantissa = mantissa*100_000_000 + uint64(parseEightDigits(src, pos))
				digitCount += 8
			} else {
				digitCount += 8
			}
			pos += 8
		}
		for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			if digitCount < 19 {
				mantissa = mantissa*10 + uint64(sliceAt(src, pos)-'0')
			}
			digitCount++
			pos++
		}
	}

	// ── Fractional digits ──

	if pos < length && sliceAt(src, pos) == '.' {
		pos++
		if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
			return pos, 0, fmt.Errorf("invalid fraction at position %d", idx)
		}
		fracStart = digitCount

		// Skip leading zeros after decimal point (e.g. "0.000123").
		// These don't contribute to the mantissa but must be counted in
		// digitCount so that power10 = exponent - (digitCount - fracStart)
		// correctly reflects the fractional shift.
		if digitCount == 0 {
			for pos < length && sliceAt(src, pos) == '0' {
				digitCount++
				pos++
			}
		}

		// SWAR: same 8-digit technique as the integer part
		for pos+8 <= length {
			w := *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), pos))
			above9 := w + 0x4646464646464646
			below0 := w - 0x3030303030303030
			if (above9|below0)&hi64 != 0 {
				break
			}
			if digitCount+8 <= 19 {
				mantissa = mantissa*100_000_000 + uint64(parseEightDigits(src, pos))
				digitCount += 8
			} else {
				digitCount += 8
			}
			pos += 8
		}
		for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			if digitCount < 19 {
				mantissa = mantissa*10 + uint64(sliceAt(src, pos)-'0')
			}
			digitCount++
			pos++
		}
	}

	// ── Exponent (e.g. "e+10", "E-5") ──

	exponent := 0
	power10 := 0
	if pos < length && (sliceAt(src, pos) == 'e' || sliceAt(src, pos) == 'E') {
		pos++
		expNegative := false
		if pos < length && (sliceAt(src, pos) == '+' || sliceAt(src, pos) == '-') {
			expNegative = sliceAt(src, pos) == '-'
			pos++
		}
		if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
			return pos, 0, fmt.Errorf("invalid exponent at position %d", idx)
		}
		for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			exponent = exponent*10 + int(sliceAt(src, pos)-'0')
			if exponent > 400 {
				pos++
				for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
					pos++
				}
				goto fallback // exponent too large for fast paths
			}
			pos++
		}
		if expNegative {
			exponent = -exponent
		}
	}

	end = pos

	// Effective power of 10: value = mantissa × 10^power10.
	// Fractional digits shift the decimal left.
	power10 = exponent
	if fracStart >= 0 {
		power10 -= (digitCount - fracStart)
	}

	// ── Tier 1: Exact pow10 (≤15 significant digits, |power10| ≤ 22) ──
	// float64 has ~15.95 decimal digits of precision. With ≤15 digits and exact
	// powers of 10, the multiplication/division is correctly rounded.
	if digitCount <= 15 && power10 >= -22 && power10 <= 22 {
		f := float64(mantissa)
		if power10 >= 0 {
			f *= pow10f64[power10]
		} else {
			f /= pow10f64[-power10]
		}
		if negative {
			f = -f
		}
		return end, f, nil
	}

	// ── Tier 2: Eisel-Lemire (≤19 significant digits) ──
	if digitCount > 19 {
		goto fallback
	}

	// Zero is always exact.
	if mantissa == 0 {
		if negative {
			return end, math.Float64frombits(1 << 63), nil // -0.0
		}
		return end, 0, nil
	}

	if resultBits, ok := eiselLemire(mantissa, power10); ok {
		if negative {
			resultBits |= 1 << 63
		}
		return end, math.Float64frombits(resultBits), nil
	}

fallback:
	// ── Tier 3: strconv.ParseFloat ──
	end = pos
	s := (*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), 1*uintptr(idx))) // 1 == unsafe.Sizeof(*new(byte))
	f, err := strconv.ParseFloat(unsafe.String(s, pos-idx), 64)
	return end, f, err
}

// eiselLemire converts mantissa × 10^power10 to a float64 bit pattern using the
// Eisel-Lemire algorithm. Returns (bits, true) on success, (0, false) when the
// result is ambiguous or out of range and the caller must fall back to
// strconv.ParseFloat.
//
// Preconditions: mantissa != 0, 1 ≤ digitCount ≤ 19.
//
// Kept as a separate function (not inlined into scanFloat64) so the compiler
// can allocate registers freely for this algorithm without interference from
// the many live variables in the surrounding digit-scanning loops.
//
//go:noinline
func eiselLemire(mantissa uint64, power10 int) (uint64, bool) {
	const (
		mantissaBits = 52
		exponentBias = -1023
	)

	if power10 < elPow10Min || power10 > elPow10Max {
		return 0, false
	}

	// Look up the 128-bit approximation of 10^power10.
	power := elPow10Tab[power10-elPow10Min]

	// Estimate the binary exponent: 10^e ≈ 2^(e × log₂10).
	// 108853 / 2^15 ≈ 3.321928... ≈ log₂(10).
	binaryExp := 1 + (power10*108853)>>15

	// Normalize: shift mantissa so its leading bit is at position 63.
	leadingZeros := bits.LeadingZeros64(mantissa)
	mantissa <<= uint(leadingZeros)
	resultExp := uint64(binaryExp+63-exponentBias) - uint64(leadingZeros)

	// 128-bit multiply: mantissa × power[0] (high 64 bits of 10^exp10)
	prodHi, prodLo := bits.Mul64(mantissa, power[0])

	// If the product falls near a rounding boundary (low 9 bits all set)
	// and prodLo overflowed, refine with power[1] (low 64 bits).
	if prodHi&0x1FF == 0x1FF && prodLo+mantissa < mantissa {
		crossHi, crossLo := bits.Mul64(mantissa, power[1])
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
