package gdec

import "unicode/utf8"

// Locate finds where src stops being one well-formed JSON document under
// mode, for a failure whose producer names no position: the structural
// scan's verdict. off is the offset of the first offending byte, or
// len(src) with eof set when the document ends before it completes. ok is
// false when src is well formed under mode.
//
// It runs on the error path alone, so it favors plainness over speed.
func Locate(src []byte, mode ScanMode) (off int, eof, ok bool) {
	const (
		value = iota
		valueOrClose
		key
		keyOrClose
		colon
		after
	)
	var closers []byte
	st := value
	for i := SkipWS(src, 0); ; i = SkipWS(src, i) {
		if i >= len(src) {
			if st == after && len(closers) == 0 {
				return 0, false, false
			}
			return len(src), true, true
		}
		c := src[i]
		if (st == valueOrClose && c == ']') || (st == keyOrClose && c == '}') {
			closers = closers[:len(closers)-1]
			i++
			st = after
			continue
		}
		switch st {
		case keyOrClose:
			st = key
		case valueOrClose:
			st = value
		}
		switch st {
		case value:
			switch {
			case c == '{' || c == '[':
				closers = append(closers, c+2) // '}' and ']' follow their openers by two
				i++
				st = keyOrClose
				if c == '[' {
					st = valueOrClose
				}
				continue
			case c == '"':
				end, e, bad := locateString(src, i, mode)
				if bad {
					return end, e, true
				}
				i = end
			case c == 't' || c == 'f' || c == 'n':
				lit := "null"
				switch c {
				case 't':
					lit = "true"
				case 'f':
					lit = "false"
				}
				k := 0
				for k < len(lit) && i+k < len(src) && src[i+k] == lit[k] {
					k++
				}
				if k < len(lit) {
					return i + k, i+k >= len(src), true
				}
				i += k
			case c == '-' || IsDigit(c):
				end, bad := locateNumber(src, i)
				if bad {
					return end, end >= len(src), true
				}
				i = end
			default:
				return i, false, true
			}
			st = after
		case key:
			if c != '"' {
				return i, false, true
			}
			end, e, bad := locateString(src, i, mode)
			if bad {
				return end, e, true
			}
			i = end
			st = colon
		case colon:
			if c != ':' {
				return i, false, true
			}
			i++
			st = value
		default: // after
			if len(closers) == 0 {
				return i, false, true
			}
			top := closers[len(closers)-1]
			switch c {
			case ',':
				st = key
				if top == ']' {
					st = value
				}
			case top:
				closers = closers[:len(closers)-1]
				st = after
			default:
				return i, false, true
			}
			i++
		}
	}
}

// locateString checks the string token at src[i] under mode. It returns
// the offset past the closing quote, or with bad set the offset of the
// first offending byte, eof marking a string still open at the end.
func locateString(src []byte, i int, mode ScanMode) (end int, eof, bad bool) {
	body := i + 1
	close, ok := ScanString(src, body)
	if !ok {
		if close >= len(src) {
			return len(src), true, true
		}
		// A bad escape: name the first byte past the backslash that breaks it.
		k := close + 1
		if At(src, k) == 'u' {
			for k++; k < close+6 && k < len(src); k++ {
				if _, hex := hexVal(src[k]); !hex {
					break
				}
			}
		}
		return k, k >= len(src), true
	}
	for k := body; k < close; {
		c := src[k]
		switch {
		case c < 0x20 && mode != ScanLax:
			return k, false, true
		case c >= 0x80 && mode == ScanStrict:
			r, size := utf8.DecodeRune(src[k:close])
			if r == utf8.RuneError && size <= 1 {
				return k, false, true
			}
			k += size
		default:
			k++
		}
	}
	return close + 1, false, false
}

// locateNumber checks the number token at src[i]. It returns the offset
// past the token, or with bad set the first byte the grammar rejects.
func locateNumber(src []byte, i int) (end int, bad bool) {
	digits := func(p int) int {
		for p < len(src) && IsDigit(src[p]) {
			p++
		}
		return p
	}
	p := i
	if At(src, p) == '-' {
		p++
	}
	switch c := At(src, p); {
	case c == '0':
		p++
	case IsDigit(c):
		p = digits(p)
	default:
		return min(p, len(src)), true
	}
	if At(src, p) == '.' {
		if p++; !IsDigit(At(src, p)) {
			return min(p, len(src)), true
		}
		p = digits(p)
	}
	if c := At(src, p); c == 'e' || c == 'E' {
		if p++; At(src, p) == '+' || At(src, p) == '-' {
			p++
		}
		if !IsDigit(At(src, p)) {
			return min(p, len(src)), true
		}
		p = digits(p)
	}
	return p, false
}

// CheckAt is Check with the position of its failure, for a window whose
// start sits outside any string: the first raw control byte or malformed
// UTF-8 sequence mode rejects, or len(src) with eof set for a string still
// open at the end. ok is false when Check passes.
func CheckAt(src []byte, mode ScanMode) (off int, eof, ok bool) {
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\':
			if inStr && mode != ScanLax && i+1 < len(src) && src[i+1] < 0x20 {
				return i + 1, false, true
			}
			i++
			continue
		case c == '"':
			inStr = !inStr
		case c < 0x20 && inStr && mode != ScanLax:
			return i, false, true
		case c >= 0x80 && mode == ScanStrict:
			r, size := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && size <= 1 {
				return i, false, true
			}
			i += size - 1
		}
	}
	if inStr {
		return len(src), true, true
	}
	return 0, false, false
}
