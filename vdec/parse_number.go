package vdec

import "math"

func scanInt64(src []byte, idx int) (end int, value int64, isFloat bool, ok bool) {
	length := len(src)
	pos := idx
	negative := false

	if pos < length && sliceAt(src, pos) == '-' {
		negative = true
		pos++
	}
	if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
		return pos, 0, false, false
	}
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

	var absValue uint64
	absValue = uint64(sliceAt(src, pos) - '0')
	pos++

	fastLimit := min(pos+17, length)
	for pos < fastLimit {
		c := sliceAt(src, pos)
		if c < '0' || c > '9' {
			goto done
		}
		absValue = absValue*10 + uint64(c-'0')
		pos++
	}

	if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
		d := uint64(sliceAt(src, pos) - '0')
		absValue = absValue*10 + d
		pos++
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			goto overflow
		}
	}

done:
	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}
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

func scanUint64(src []byte, idx int) (end int, value uint64, isFloat bool, ok bool) {
	length := len(src)
	pos := idx

	if pos < length && sliceAt(src, pos) == '-' {
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

	var absValue uint64
	absValue = uint64(sliceAt(src, pos) - '0')
	pos++

	fastLimit := min(pos+18, length)
	for pos < fastLimit {
		c := sliceAt(src, pos)
		if c < '0' || c > '9' {
			goto done
		}
		absValue = absValue*10 + uint64(c-'0')
		pos++
	}

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
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			goto overflow
		}
	}

done:
	if pos < length {
		c := sliceAt(src, pos)
		if c == '.' || c == 'e' || c == 'E' {
			return pos, 0, true, true
		}
	}
	return pos, absValue, false, true

overflow:
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
