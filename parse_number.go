package vjson

import (
	"math"
)

// scanInt64 validates and parses a JSON integer in one pass.
// Returns (end, value, isFloat, ok). isFloat=true means the number has
// a decimal point or exponent (caller should parse as float instead).
// ok=false means syntax error or overflow.
func scanInt64(src []byte, idx int) (end int, value int64, isFloat bool, ok bool) {
	length := len(src)
	pos := idx
	negative := false

	// Sign

	if pos < length && sliceAt(src, pos) == '-' {
		negative = true
		pos++
	}

	if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
		return pos, 0, false, false
	}

	// Leading zero

	if sliceAt(src, pos) == '0' {
		pos++
		if pos < length {
			c := sliceAt(src, pos)
			if c >= '0' && c <= '9' {
				return pos, 0, false, false // leading zeros
			}
			if c == '.' || c == 'e' || c == 'E' {
				return pos, 0, true, true
			}
		}
		return pos, 0, false, true
	}

	// Accumulate digits

	var absValue uint64
	absValue = uint64(sliceAt(src, pos) - '0')
	pos++

	// Up to 18 digits (cannot overflow uint64 with ≤18 digits starting from 1-9)
	fastLimit := min(pos+17, length)

	for pos < fastLimit {
		c := sliceAt(src, pos)
		if c < '0' || c > '9' {
			goto done
		}
		absValue = absValue*10 + uint64(c-'0')
		pos++
	}

	// 19th digit

	if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
		d := uint64(sliceAt(src, pos) - '0')
		absValue = absValue*10 + d
		pos++

		// 20+ digits: overflow
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			goto overflow
		}
	}

done:
	// Float detection

	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}

	// Convert to int64 with sign

	if negative {
		if absValue > uint64(math.MaxInt64)+1 {
			return pos, 0, false, false
		}
		if absValue == uint64(math.MaxInt64)+1 {
			return pos, math.MinInt64, false, true
		}
		return pos, -int64(absValue), false, true
	}
	if absValue > uint64(math.MaxInt64) {
		return pos, 0, false, false
	}
	return pos, int64(absValue), false, true

overflow:
	// Skip remaining digits, then check for float indicators.
	for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
		pos++
	}
	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}
	return pos, 0, false, false
}

// scanUint64 validates and parses a JSON unsigned integer in one pass.
func scanUint64(src []byte, idx int) (end int, value uint64, isFloat bool, ok bool) {
	length := len(src)
	pos := idx

	// Reject negative

	if pos < length && sliceAt(src, pos) == '-' {
		// Scan past the number to report correct end position.
		pos++
		for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			pos++
		}
		if pos < length {
			c := sliceAt(src, pos)
			if c == '.' || c == 'e' || c == 'E' {
				return pos, 0, true, true
			}
		}
		return pos, 0, false, false
	}

	if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
		return pos, 0, false, false
	}

	// Leading zero

	if sliceAt(src, pos) == '0' {
		pos++
		if pos < length {
			c := sliceAt(src, pos)
			if c >= '0' && c <= '9' {
				return pos, 0, false, false
			}
			if c == '.' || c == 'e' || c == 'E' {
				return pos, 0, true, true
			}
		}
		return pos, 0, false, true
	}

	// Accumulate digits

	var absValue uint64
	absValue = uint64(sliceAt(src, pos) - '0')
	pos++

	// Up to 19 total digits
	fastLimit := min(pos+18, length)
	for pos < fastLimit {
		c := sliceAt(src, pos)
		if c < '0' || c > '9' {
			goto done
		}
		absValue = absValue*10 + uint64(c-'0')
		pos++
	}

	// 20th digit: check overflow

	if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
		d := uint64(sliceAt(src, pos) - '0')
		const cutoff = math.MaxUint64 / 10
		const lastDigit = math.MaxUint64 % 10
		if absValue > cutoff || (absValue == cutoff && d > lastDigit) {
			pos++
			goto overflow
		}
		absValue = absValue*10 + d
		pos++

		// 21+ digits: overflow
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			goto overflow
		}
	}

done:
	// Float detection

	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}
	return pos, absValue, false, true

overflow:
	// Skip remaining digits, then check for float indicators.
	for pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
		pos++
	}
	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}
	return pos, 0, false, false
}
