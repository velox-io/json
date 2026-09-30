package gdec

import (
	"encoding/binary"
	"math"
	"math/bits"
	"unsafe"
)

// srcAt reads byte i of the n-byte source at b, or 0x20 past either end,
// without a bounds check.
func srcAt(b unsafe.Pointer, n, i int) byte {
	if uint(i) < uint(n) {
		return *(*byte)(unsafe.Add(b, i))
	}
	return 0x20
}

// ValidNumber checks the number grammar of the token at src[o] and its
// delimiter successor without parsing a value, as ndec_valid_number does:
// out-of-range tokens such as 1e999 stay valid. It returns the token end.
func ValidNumber(src []byte, o int) (int, bool) {
	b, n := unsafe.Pointer(unsafe.SliceData(src)), len(src)
	digitsFrom := func(p int) int {
		for p < n && IsDigit(*(*byte)(unsafe.Add(b, p))) {
			p++
		}
		return p
	}
	p := o
	if srcAt(b, n, p) == '-' {
		p++
	}
	d := p
	p = digitsFrom(p)
	if nd := p - d; nd == 0 || (srcAt(b, n, d) == '0' && nd > 1) {
		return p, false
	}
	if srcAt(b, n, p) == '.' {
		p++
		if !IsDigit(srcAt(b, n, p)) {
			return p, false
		}
		p = digitsFrom(p)
	}
	if c := srcAt(b, n, p); c == 'e' || c == 'E' {
		p++
		if c := srcAt(b, n, p); c == '+' || c == '-' {
			p++
		}
		if !IsDigit(srcAt(b, n, p)) {
			return p, false
		}
		p = digitsFrom(p)
	}
	return p, !IsNonDelim(srcAt(b, n, p))
}

// maxFastDigits is the longest digit run whose value always fits a uint64.
const maxFastDigits = 19

// IntToken lexes the digit run ndec_parse_int64_padded reads at src[o]: an
// optional '-' and digits, ending at end. bad reports a missing digit, a
// leading zero, or a fraction or exponent following the digits. mag is the
// run's magnitude unless long reports more than 19 digits, which the caller
// parses from the text.
func IntToken(src []byte, o int) (mag uint64, neg bool, end int, bad, long bool) {
	b, n := unsafe.Pointer(unsafe.SliceData(src)), len(src)
	p := o
	if neg = srcAt(b, n, p) == '-'; neg {
		p++
	}
	d := p
	for ; p < n; p++ {
		v := *(*byte)(unsafe.Add(b, p)) - '0'
		if v > 9 {
			break
		}
		mag = 10*mag + uint64(v)
	}
	nd := p - d
	if nd == 0 || (srcAt(b, n, d) == '0' && nd > 1) {
		return 0, neg, p, true, false
	}
	if c := srcAt(b, n, p); c == '.' || c|0x20 == 'e' {
		return 0, neg, p, true, false
	}
	return mag, neg, p, false, nd > maxFastDigits
}

// pow10 holds the powers of ten a float64 represents exactly.
var pow10 = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10,
	1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

// FloatToken lexes the longest JSON number prefix at src[o], the token the
// native JSON atof consumes, ending at end. ok is false for no integer
// digit, a leading zero followed by a digit, or a '.' without a fraction
// digit; an exponent without digits ends the prefix before its 'e'. A dirty
// successor is the caller's delimiter check.
//
// exact reports that f is the token's correctly rounded float64, which the
// native finalization computes the same way for a mantissa of at most 19
// digits and an exponent below 10^4: one IEEE operation within the exact
// range, Eisel-Lemire beyond it. Otherwise the caller converts the text.
func FloatToken(src []byte, o int) (f float64, end int, ok, exact bool) {
	b, n := unsafe.Pointer(unsafe.SliceData(src)), len(src)
	p := o
	neg := srcAt(b, n, p) == '-'
	if neg {
		p++
	}
	// The token's value is mant * 10^exp while digits stays within
	// maxFastDigits; leading zeros add no digit.
	var mant uint64
	digits, exp := 0, 0
	d := p
	if srcAt(b, n, p) == '0' {
		if p++; IsDigit(srcAt(b, n, p)) {
			return 0, p, false, false
		}
	} else {
		p, mant = digitRun(b, n, p, 0)
		digits = p - d
	}
	if p == d {
		return 0, p, false, false
	}
	if srcAt(b, n, p) == '.' {
		if !IsDigit(srcAt(b, n, p+1)) {
			return 0, p, false, false
		}
		p++
		frac := p
		if mant == 0 {
			for p < n && *(*byte)(unsafe.Add(b, p)) == '0' {
				p++
			}
		}
		z := p
		p, mant = digitRun(b, n, p, mant)
		digits += p - z
		exp = frac - p
	}
	// Leading fraction zeros shift exp without bound, so an exponent past
	// four significant digits stays with the text conversion: clipped, it
	// could land back in range.
	clipped := false
	if srcAt(b, n, p)|0x20 == 'e' {
		q := p + 1
		c := srcAt(b, n, q)
		negE := c == '-'
		if c == '+' || c == '-' {
			q++
		}
		if IsDigit(srcAt(b, n, q)) {
			e := 0
			for p = q; p < n; p++ {
				v := *(*byte)(unsafe.Add(b, p)) - '0'
				if v > 9 {
					break
				}
				if e >= 1000 {
					clipped = true
					continue
				}
				e = 10*e + int(v)
			}
			if negE {
				e = -e
			}
			exp += e
		}
	}
	if digits > maxFastDigits || clipped {
		return 0, p, true, false
	}
	switch {
	case mant == 0:
		f = 0
	case exp == 0:
		f = float64(mant)
	case mant <= 1<<53 && exp > 0 && exp < len(pow10):
		f = float64(mant) * pow10[exp]
	case mant <= 1<<53 && exp < 0 && -exp < len(pow10):
		f = float64(mant) / pow10[-exp]
	default:
		var fin bool
		if f, fin = eiselLemire(mant, exp); !fin {
			return 0, p, true, false
		}
	}
	if neg {
		f = -f
	}
	return f, p, true, true
}

// digitRun accumulates the decimal digits from p into mant, eight per step
// while a whole word of them remains, and returns the offset past the run.
// mant wraps past 19 digits, which the caller detects by count.
func digitRun(b unsafe.Pointer, n, p int, mant uint64) (int, uint64) {
	for p+8 <= n {
		w := binary.LittleEndian.Uint64((*[8]byte)(unsafe.Add(b, p))[:])
		if (w&0xF0F0F0F0F0F0F0F0)|(((w+0x0606060606060606)&0xF0F0F0F0F0F0F0F0)>>4) != 0x3333333333333333 {
			break
		}
		// Pairwise combine the digits: bytes into 2-digit, then 4-digit,
		// then 8-digit lanes.
		w = (w & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
		w = (w & 0x00FF00FF00FF00FF) * 6553601 >> 16
		w = (w & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
		mant = mant*100000000 + w
		p += 8
	}
	for ; p < n; p++ {
		v := *(*byte)(unsafe.Add(b, p)) - '0'
		if v > 9 {
			break
		}
		mant = 10*mant + uint64(v)
	}
	return p, mant
}

// eiselLemire is atof_i_eisel_lemire_f64: the correctly rounded float64 of
// d * 10^q for a nonzero d. fin is false for a q outside the table or a
// result that overflows, which the caller converts from the text.
func eiselLemire(d uint64, q int) (f float64, fin bool) {
	if q < pow5Smallest || q > pow5Largest {
		return 0, false
	}
	exponent := int64(((152170+65536)*q)>>16) + 1024 + 63
	lz := bits.LeadingZeros64(d)
	i := d << lz
	pw := &pow5Table[q-pow5Smallest]
	hi, lo := bits.Mul64(i, pw[0])
	// Low 9 bits all set: one multiply is not precise enough, so add the
	// high word of i * pw.lo.
	if hi&0x1ff == 0x1ff {
		h2, _ := bits.Mul64(i, pw[1])
		var carry uint64
		lo, carry = bits.Add64(lo, h2, 0)
		hi += carry
	}
	upper := hi >> 63
	m := hi >> (upper + 9)
	lz += int(1 ^ upper)
	re := exponent - int64(lz)
	if re <= 0 {
		// Subnormal: never a round-to-even tie.
		if -re+1 >= 64 {
			return 0, true
		}
		m >>= uint(-re + 1)
		m += m & 1
		m >>= 1
		re = 0
		if m >= 1<<52 {
			re = 1
		}
		return math.Float64frombits(uint64(re)<<52 | m&(1<<52-1)), true
	}
	// (2m+1) * 2^p with q in [-4, 23] can sit exactly between two floats;
	// clear the low bit to round to even.
	if lo <= 1 && q >= -4 && q <= 23 && m&3 == 1 && m<<(upper+64-53-2) == hi {
		m &^= 1
	}
	m += m & 1
	m >>= 1
	if m >= 1<<53 {
		m = 1 << 52
		re++
	}
	if re > 2046 {
		return 0, false
	}
	return math.Float64frombits(uint64(re)<<52 | m&(1<<52-1)), true
}
