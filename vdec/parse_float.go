package vdec

import (
	"fmt"
	"math"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/fpparse"
)

func parseEightDigits(src []byte, i int) uint32 {
	val := *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), i))
	val = (val & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
	val = (val & 0x00FF00FF00FF00FF) * 6553601 >> 16
	val = (val & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
	return uint32(val)
}

func scanFloat64(src []byte, idx int) (end int, value float64, err error) {
	length := len(src)
	pos := idx
	negative := false

	if pos < length && sliceAt(src, pos) == '-' {
		negative = true
		pos++
	}
	if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
		return pos, 0, fmt.Errorf("invalid number at position %d", idx)
	}

	var mantissa uint64
	digitCount := 0
	fracStart := -1

	if sliceAt(src, pos) == '0' {
		pos++
		if pos < length && sliceAt(src, pos) >= '0' && sliceAt(src, pos) <= '9' {
			return pos, 0, fmt.Errorf("leading zeros in number at position %d", idx)
		}
	} else {
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

	if pos < length && sliceAt(src, pos) == '.' {
		pos++
		if pos >= length || sliceAt(src, pos) < '0' || sliceAt(src, pos) > '9' {
			return pos, 0, fmt.Errorf("invalid fraction at position %d", idx)
		}
		fracStart = digitCount
		if digitCount == 0 {
			for pos < length && sliceAt(src, pos) == '0' {
				digitCount++
				pos++
			}
		}
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
				goto fallback
			}
			pos++
		}
		if expNegative {
			exponent = -exponent
		}
	}

	end = pos
	power10 = exponent
	if fracStart >= 0 {
		power10 -= (digitCount - fracStart)
	}

	if digitCount <= 15 && power10 >= -22 && power10 <= 22 {
		f := float64(mantissa)
		if power10 >= 0 {
			f *= fpparse.Pow10f64[power10]
		} else {
			f /= fpparse.Pow10f64[-power10]
		}
		if negative {
			f = -f
		}
		return end, f, nil
	}

	if digitCount > 19 {
		goto fallback
	}
	if mantissa == 0 {
		if negative {
			return end, math.Float64frombits(1 << 63), nil
		}
		return end, 0, nil
	}
	if resultBits, ok := fpparse.EiselLemire(mantissa, power10); ok {
		if negative {
			resultBits |= 1 << 63
		}
		f := math.Float64frombits(resultBits)
		return end, f, nil
	}

fallback:
	f, err := strconv.ParseFloat(string(src[idx:pos]), 64)
	return pos, f, err
}
