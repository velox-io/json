package fpparse

import (
	"math"
	"math/bits"
)

// bool2 converts b to an integer: 1 for true, 0 for false.
func bool2[T ~int | ~uint64](b bool) T {
	if b {
		return 1
	}
	return 0
}

// pack64 takes m, e and returns f = m * 2**e.
// It assumes the caller has provided a 53-bit mantissa m.
// Returns +Inf when the biased exponent overflows (≥ 2047).
func pack64(m uint64, e int) float64 {
	if m&(1<<52) == 0 {
		return math.Float64frombits(m)
	}
	biased := 1075 + e
	if biased >= 0x7FF {
		return math.Inf(1)
	}
	return math.Float64frombits(m&^(1<<52) | uint64(biased)<<52)
}

// An unrounded represents an unrounded value.
type unrounded uint64

func (u unrounded) round() uint64 { return uint64((u + 1 + (u>>2)&1) >> 2) }

// log2Pow10(x) returns ⌊log₂ 10**x⌋ = ⌊x * log₂ 10⌋.
func log2Pow10(x int) int {
	// log₂ 10 ≈ 3.32192809489 ≈ 108853 / 2^15
	return (x * 108853) >> 15
}

// A pmHiLo represents hi<<64 - lo.
type pmHiLo struct {
	hi uint64
	lo uint64
}

// A scaler holds derived scaling constants for a given e, p pair.
type scaler struct {
	pm pmHiLo
	s  int
}

// prescale returns the scaling constants for e, p.
// lp must be log2Pow10(p).
func prescale(e, p, lp int) scaler {
	return scaler{pm: pow10Tab[p-pow10Min], s: -(e + lp + 3)}
}

// uscale returns unround(x * 2**e * 10**p).
// The caller should pass c = prescale(e, p, log2Pow10(p))
// and should have left-justified x so its high bit is set.
func uscale(x uint64, c scaler) unrounded {
	hi, mid := bits.Mul64(x, c.pm.hi)
	sticky := uint64(1)
	if hi&(1<<(c.s&63)-1) == 0 {
		mid2, _ := bits.Mul64(x, c.pm.lo)
		sticky = bool2[uint64](mid-mid2 > 1)
		hi -= bool2[uint64](mid < mid2)
	}
	return unrounded(hi>>c.s | sticky)
}

// unmin returns the minimum unrounded that rounds to x.
func unmin(x uint64) unrounded {
	return unrounded(x<<2 - 2)
}

// Parse rounds d * 10**p to the nearest float64 f.
// d can have at most 19 digits (d <= 1e19).
// Returns 0 for underflow; returns +Inf for overflow.
func Parse(d uint64, p int) float64 {
	if d == 0 {
		return 0
	}

	// Check if p is within the precomputed table range.
	// pow10Tab covers [pow10Min, pow10Max] = [-348, 347].
	if p < pow10Min {
		return 0 // underflow
	}
	if p > pow10Max {
		return math.Inf(1) // overflow
	}

	b := bits.Len64(d)
	lp := log2Pow10(p)
	e := min(1074, 53-b-lp)
	u := uscale(d<<(64-b), prescale(e-(64-b), p, lp))

	// This block is branch-free code for:
	//	if u.round() >= 1<<53 {
	//		u = u.rsh(1)
	//		e = e - 1
	//	}
	s := bool2[int](u >= unmin(1<<53))
	u = (u >> s) | u&1
	e = e - s

	return pack64(u.round(), -e)
}
