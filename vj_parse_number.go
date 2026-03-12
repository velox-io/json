package vjson

import (
	"fmt"
	"math"
	"math/bits"
	"unsafe"
)

// parseInt64 parses an integer from src[start:end] without allocation.
func parseInt64(src []byte, start, end int) int64 {
	if start >= end {
		return 0
	}
	neg := false
	i := start
	if src[i] == '-' {
		neg = true
		i++
	}
	n := int64(parseUint64(src, i, end))
	if neg {
		return -n
	}
	return n
}

// parseEightDigitsSWAR parses exactly 8 ASCII digits into a uint32.
func parseEightDigitsSWAR(src []byte, i int) uint32 {
	val := *(*uint64)(unsafe.Pointer(&src[i]))
	val = (val & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
	val = (val & 0x00FF00FF00FF00FF) * 6553601 >> 16
	val = (val & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
	return uint32(val)
}

// parseUint64 parses an unsigned integer from src[start:end] using SWAR.
func parseUint64(src []byte, start, end int) uint64 {
	i := start
	nDigits := end - start

	var n uint64
	if nDigits >= 8 {
		n = uint64(parseEightDigitsSWAR(src, i))
		i += 8
		nDigits -= 8
		if nDigits >= 8 {
			n = n*100_000_000 + uint64(parseEightDigitsSWAR(src, i))
			i += 8
		}
	}
	for ; i < end; i++ {
		n = n*10 + uint64(src[i]-'0')
	}
	return n
}

// intFitsKind checks whether v fits in the target signed integer kind.
func intFitsKind(v int64, kind ElemTypeKind) bool {
	switch kind {
	case KindInt8:
		return v >= math.MinInt8 && v <= math.MaxInt8
	case KindInt16:
		return v >= math.MinInt16 && v <= math.MaxInt16
	case KindInt32:
		return v >= math.MinInt32 && v <= math.MaxInt32
	default: // KindInt, KindInt64
		return true
	}
}

// uintFitsKind checks whether v fits in the target unsigned integer kind.
func uintFitsKind(v uint64, kind ElemTypeKind) bool {
	switch kind {
	case KindUint8:
		return v <= math.MaxUint8
	case KindUint16:
		return v <= math.MaxUint16
	case KindUint32:
		return v <= math.MaxUint32
	default: // KindUint, KindUint64
		return true
	}
}

var internedFloats = func() [256]any {
	var arr [256]any
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}()

// scanInt64SinglePass validates and parses a JSON integer in one pass.
func scanInt64SinglePass(src []byte, idx int) (end int, value int64, isFloat bool, ok bool) {
	n := len(src)
	i := idx
	neg := false

	// Optional leading '-'
	if i < n && src[i] == '-' {
		neg = true
		i++
	}

	if i >= n || src[i] < '0' || src[i] > '9' {
		return i, 0, false, false
	}

	// Leading zero: must not be followed by another digit
	if src[i] == '0' {
		i++
		if i < n {
			c := src[i]
			if c >= '0' && c <= '9' {
				return i, 0, false, false // leading zeros
			}
			if c == '.' || c == 'e' || c == 'E' {
				return i, 0, true, true
			}
		}
		return i, 0, false, true
	}

	var val uint64
	val = uint64(src[i] - '0')
	i++

	// Accumulate up to 18 digits (cannot overflow uint64 with ≤18 digits starting from 1-9)
	fastLimit := min(i+17, n)

	for i < fastLimit {
		c := src[i]
		if c < '0' || c > '9' {
			goto done
		}
		val = val*10 + uint64(c-'0')
		i++
	}

	// 19th digit
	if i < n && src[i] >= '0' && src[i] <= '9' {
		d := uint64(src[i] - '0')
		val = val*10 + d
		i++

		// 20+ digits: overflow
		if i < n && src[i] >= '0' && src[i] <= '9' {
			for i < n && src[i] >= '0' && src[i] <= '9' {
				i++
			}
			if i < n && (src[i] == '.' || src[i] == 'e' || src[i] == 'E') {
				return i, 0, true, true
			}
			return i, 0, false, false
		}
	}

done:
	if i < n && (src[i] == '.' || src[i] == 'e' || src[i] == 'E') {
		return i, 0, true, true
	}

	// Convert to int64 with sign
	if neg {
		if val > uint64(math.MaxInt64)+1 {
			return i, 0, false, false
		}
		if val == uint64(math.MaxInt64)+1 {
			return i, math.MinInt64, false, true
		}
		return i, -int64(val), false, true
	}
	if val > uint64(math.MaxInt64) {
		return i, 0, false, false
	}
	return i, int64(val), false, true
}

// scanUint64SinglePass validates and parses a JSON unsigned integer in one pass.
func scanUint64SinglePass(src []byte, idx int) (end int, value uint64, isFloat bool, ok bool) {
	n := len(src)
	i := idx

	if i < n && src[i] == '-' {
		// Scan past the number to report correct end position
		i++
		for i < n && src[i] >= '0' && src[i] <= '9' {
			i++
		}
		if i < n && src[i] == '.' {
			return i, 0, true, true
		}
		if i < n && (src[i] == 'e' || src[i] == 'E') {
			return i, 0, true, true
		}
		return i, 0, false, false
	}

	if i >= n || src[i] < '0' || src[i] > '9' {
		return i, 0, false, false
	}

	if src[i] == '0' {
		i++
		if i < n {
			c := src[i]
			if c >= '0' && c <= '9' {
				return i, 0, false, false
			}
			if c == '.' || c == 'e' || c == 'E' {
				return i, 0, true, true
			}
		}
		return i, 0, false, true
	}

	var val uint64
	val = uint64(src[i] - '0')
	i++

	// Accumulate up to 19 total digits
	fastLimit := i + 18
	if fastLimit > n {
		fastLimit = n
	}
	for i < fastLimit {
		c := src[i]
		if c < '0' || c > '9' {
			goto done
		}
		val = val*10 + uint64(c-'0')
		i++
	}

	// 20th digit: check overflow
	if i < n && src[i] >= '0' && src[i] <= '9' {
		d := uint64(src[i] - '0')
		const cutoff = math.MaxUint64 / 10
		const lastDigit = math.MaxUint64 % 10
		if val > cutoff || (val == cutoff && d > lastDigit) {
			i++
			for i < n && src[i] >= '0' && src[i] <= '9' {
				i++
			}
			if i < n && (src[i] == '.' || src[i] == 'e' || src[i] == 'E') {
				return i, 0, true, true
			}
			return i, 0, false, false
		}
		val = val*10 + d
		i++

		// 21+ digits: overflow
		if i < n && src[i] >= '0' && src[i] <= '9' {
			for i < n && src[i] >= '0' && src[i] <= '9' {
				i++
			}
			if i < n && (src[i] == '.' || src[i] == 'e' || src[i] == 'E') {
				return i, 0, true, true
			}
			return i, 0, false, false
		}
	}

done:
	if i < n && (src[i] == '.' || src[i] == 'e' || src[i] == 'E') {
		return i, 0, true, true
	}
	return i, val, false, true
}

// pow10f64 contains exact powers of 10 representable as float64 (10^0 through 10^22).
var pow10f64 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19,
	1e20, 1e21, 1e22,
}

// scanFloat64Fast parses a JSON number into float64 in a single pass.
// Uses three tiers: exact pow10 (≤15 digits), Eisel-Lemire (16-19 digits),
// then falls back to strconv.ParseFloat via the caller.
func scanFloat64Fast(src []byte, idx int) (end int, value float64, usedFast bool, err error) {
	n := len(src)
	i := idx
	neg := false

	// Optional leading '-'
	if i < n && src[i] == '-' {
		neg = true
		i++
	}

	if i >= n || src[i] < '0' || src[i] > '9' {
		return i, 0, false, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
	}

	var mantissa uint64
	nDigits := 0
	decimalPos := -1

	// Integer part
	if src[i] == '0' {
		i++
		if i < n && src[i] >= '0' && src[i] <= '9' {
			return i, 0, false, newSyntaxError(fmt.Sprintf("vjson: leading zeros in number at offset %d", idx), idx)
		}
	} else {
		// SWAR: process 8 digits at a time
		for i+8 <= n {
			w := *(*uint64)(unsafe.Pointer(&src[i]))
			a := w + 0x4646464646464646
			b := w - 0x3030303030303030
			if (a|b)&hi64 != 0 {
				break
			}
			if nDigits+8 <= 19 {
				mantissa = mantissa*100_000_000 + uint64(parseEightDigitsSWAR(src, i))
				nDigits += 8
			} else {
				nDigits += 8
			}
			i += 8
		}
		for i < n && src[i] >= '0' && src[i] <= '9' {
			if nDigits < 19 {
				mantissa = mantissa*10 + uint64(src[i]-'0')
			}
			nDigits++
			i++
		}
	}

	// Fraction
	if i < n && src[i] == '.' {
		i++
		if i >= n || src[i] < '0' || src[i] > '9' {
			return i, 0, false, newSyntaxError(fmt.Sprintf("vjson: invalid fraction in number at offset %d", idx), idx)
		}
		decimalPos = nDigits

		if nDigits == 0 {
			// "0.000123" — skip leading fraction zeros
			for i < n && src[i] == '0' {
				decimalPos++
				i++
			}
		}

		// SWAR for fraction digits
		for i+8 <= n {
			w := *(*uint64)(unsafe.Pointer(&src[i]))
			a := w + 0x4646464646464646
			b := w - 0x3030303030303030
			if (a|b)&hi64 != 0 {
				break
			}
			if nDigits+8 <= 19 {
				mantissa = mantissa*100_000_000 + uint64(parseEightDigitsSWAR(src, i))
				nDigits += 8
			} else {
				nDigits += 8
			}
			i += 8
		}
		for i < n && src[i] >= '0' && src[i] <= '9' {
			if nDigits < 19 {
				mantissa = mantissa*10 + uint64(src[i]-'0')
			}
			nDigits++
			i++
		}
	}

	// Exponent
	explicitExp := 0
	if i < n && (src[i] == 'e' || src[i] == 'E') {
		i++
		expNeg := false
		if i < n && (src[i] == '+' || src[i] == '-') {
			if src[i] == '-' {
				expNeg = true
			}
			i++
		}
		if i >= n || src[i] < '0' || src[i] > '9' {
			return i, 0, false, newSyntaxError(fmt.Sprintf("vjson: invalid exponent in number at offset %d", idx), idx)
		}
		for i < n && src[i] >= '0' && src[i] <= '9' {
			explicitExp = explicitExp*10 + int(src[i]-'0')
			if explicitExp > 400 {
				i++
				for i < n && src[i] >= '0' && src[i] <= '9' {
					i++
				}
				return i, 0, false, nil
			}
			i++
		}
		if expNeg {
			explicitExp = -explicitExp
		}
	}

	end = i

	adjExp := explicitExp
	if decimalPos >= 0 {
		adjExp -= (nDigits - decimalPos)
	}

	// Tier 1: exact pow10 (≤15 significant digits, exponent in [-22, 22])
	if nDigits <= 15 && adjExp >= -22 && adjExp <= 22 {
		f := float64(mantissa)
		if adjExp >= 0 {
			f *= pow10f64[adjExp]
		} else {
			f /= pow10f64[-adjExp]
		}
		if neg {
			f = -f
		}
		return end, f, true, nil
	}

	// Tier 2: Eisel-Lemire (16-19 digits)
	if nDigits <= 19 {
		f, ok := eiselLemire64(mantissa, adjExp, neg)
		if ok {
			return end, f, true, nil
		}
	}

	// Tier 3: caller falls back to strconv.ParseFloat
	return end, 0, false, nil
}

// eiselLemire64 converts mantissa × 10^exp10 to float64.
// Returns (0, false) on ambiguous half-way/subnormal cases.
// Ref: https://nigeltao.github.io/blog/2020/eisel-lemire.html
func eiselLemire64(man uint64, exp10 int, neg bool) (float64, bool) {
	const (
		float64MantBits = 52
		float64Bias     = -1023
	)

	if man == 0 {
		if neg {
			return math.Float64frombits(0x8000000000000000), true
		}
		return 0, true
	}

	// Check exponent range against the table.
	if exp10 < elPow10Min || exp10 > elPow10Max {
		return 0, false
	}
	pow := elPow10Tab[exp10-elPow10Min]
	exp2 := 1 + (exp10*108853)>>15

	// Normalization.
	clz := bits.LeadingZeros64(man)
	man <<= uint(clz)
	retExp2 := uint64(exp2+63-float64Bias) - uint64(clz)

	xHi, xLo := bits.Mul64(man, pow[0])

	// Wider approximation.
	if xHi&0x1FF == 0x1FF && xLo+man < man {
		yHi, yLo := bits.Mul64(man, pow[1])
		mergedHi, mergedLo := xHi, xLo+yHi
		if mergedLo < xLo {
			mergedHi++
		}
		if mergedHi&0x1FF == 0x1FF && mergedLo+1 == 0 && yLo+man < man {
			return 0, false
		}
		xHi, xLo = mergedHi, mergedLo
	}

	// Shifting to 54 bits.
	msb := xHi >> 63
	retMantissa := xHi >> (msb + 9)
	retExp2 -= 1 ^ msb

	// Half-way ambiguity.
	if xLo == 0 && xHi&0x1FF == 0 && retMantissa&3 == 1 {
		return 0, false
	}

	// 54 → 53 bits.
	retMantissa += retMantissa & 1
	retMantissa >>= 1
	if retMantissa>>53 > 0 {
		retMantissa >>= 1
		retExp2++
	}
	if retExp2-1 >= 0x7FF-1 {
		return 0, false
	}
	retBits := retExp2<<float64MantBits | retMantissa&(1<<float64MantBits-1)
	if neg {
		retBits |= 0x8000000000000000
	}
	return math.Float64frombits(retBits), true
}
func writeIntValue(ptr unsafe.Pointer, kind ElemTypeKind, v int64) {
	switch kind {
	case KindInt:
		*(*int)(ptr) = int(v)
	case KindInt8:
		*(*int8)(ptr) = int8(v)
	case KindInt16:
		*(*int16)(ptr) = int16(v)
	case KindInt32:
		*(*int32)(ptr) = int32(v)
	case KindInt64:
		*(*int64)(ptr) = v
	}
}

func writeUintValue(ptr unsafe.Pointer, kind ElemTypeKind, v uint64) {
	switch kind {
	case KindUint:
		*(*uint)(ptr) = uint(v)
	case KindUint8:
		*(*uint8)(ptr) = uint8(v)
	case KindUint16:
		*(*uint16)(ptr) = uint16(v)
	case KindUint32:
		*(*uint32)(ptr) = uint32(v)
	case KindUint64:
		*(*uint64)(ptr) = v
	}
}
