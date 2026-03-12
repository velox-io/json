package vjson

import (
	"fmt"
	"unsafe"
)

// scanMapStringString is a zero-reflection fast path for map[string]string.
func (sc *Parser) scanMapStringString(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	idx++
	idx = skipWSLong(src, idx)

	m := *(*map[string]string)(ptr)
	if m == nil {
		m = make(map[string]string)
		*(*map[string]string)(ptr) = m
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, nil
	}

	for {
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		var key string
		var err error
		idx, key, err = sc.scanString(src, idx)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		var val string
		idx, val, err = sc.scanString(src, idx)
		if err != nil {
			return idx, err
		}
		m[key] = val

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, nil
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, nil
			}
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in map, got %q", src[idx]), idx)
	}
}

// scanMapStringInt is a zero-reflection fast path for map[string]int.
func (sc *Parser) scanMapStringInt(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	idx++
	idx = skipWSLong(src, idx)

	m := *(*map[string]int)(ptr)
	if m == nil {
		m = make(map[string]int)
		*(*map[string]int)(ptr) = m
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, nil
	}

	for {
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		var key string
		var err error
		idx, key, err = sc.scanString(src, idx)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == 'n' {
			if idx+4 > len(src) {
				return idx, errUnexpectedEOF
			}
			if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
				return idx, invalidLiteralError(idx)
			}
			m[key] = 0
			idx += 4
		} else {
			end, v, isFloat, ok := scanInt64(src, idx)
			if isFloat {
				numEnd, _, numErr := scanNumberSpan(src, idx)
				if numErr != nil {
					return numEnd, numErr
				}
				return numEnd, newUnmarshalTypeError("number", intType, numEnd)
			}
			if !ok {
				if end == idx || (end == idx+1 && src[idx] == '-') {
					return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
				}
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), intType, end)
			}
			m[key] = int(v)
			idx = end
		}

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, nil
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, nil
			}
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in map, got %q", src[idx]), idx)
	}
}

// scanMapStringInt64 is a zero-reflection fast path for map[string]int64.
func (sc *Parser) scanMapStringInt64(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	idx++
	idx = skipWSLong(src, idx)

	m := *(*map[string]int64)(ptr)
	if m == nil {
		m = make(map[string]int64)
		*(*map[string]int64)(ptr) = m
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, nil
	}

	for {
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		var key string
		var err error
		idx, key, err = sc.scanString(src, idx)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == 'n' {
			if idx+4 > len(src) {
				return idx, errUnexpectedEOF
			}
			if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
				return idx, invalidLiteralError(idx)
			}
			m[key] = 0
			idx += 4
		} else {
			end, v, isFloat, ok := scanInt64(src, idx)
			if isFloat {
				numEnd, _, numErr := scanNumberSpan(src, idx)
				if numErr != nil {
					return numEnd, numErr
				}
				return numEnd, newUnmarshalTypeError("number", int64Type, numEnd)
			}
			if !ok {
				if end == idx || (end == idx+1 && sliceAt(src, idx) == '-') {
					return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
				}
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), int64Type, end)
			}
			m[key] = v
			idx = end
		}

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, nil
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, nil
			}
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in map, got %q", src[idx]), idx)
	}
}
