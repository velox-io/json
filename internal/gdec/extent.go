package gdec

// ExtentScanner finds the end of the JSON value starting at Pos, resuming
// across buffer refills. It judges only extent: the parse that follows owns
// every grammar verdict, so malformed input merely ends somewhere.
type ExtentScanner struct {
	Pos     int
	depth   int
	started bool
	scalar  bool
	inStr   bool
	esc     bool
}

// Scan advances over b and reports the value's end offset once known. eof
// reports that b holds the rest of the input, which ends a scalar.
func (s *ExtentScanner) Scan(b []byte, eof bool) (int, bool) {
	for ; s.Pos < len(b); s.Pos++ {
		c := b[s.Pos]
		if s.inStr {
			switch {
			case s.esc:
				s.esc = false
			case c == '\\':
				s.esc = true
			case c == '"':
				s.inStr = false
				if s.depth == 0 {
					return s.Pos + 1, true
				}
			}
			continue
		}
		if !s.started {
			s.started = true
			switch c {
			case '"':
				s.inStr = true
			case '{', '[':
				s.depth = 1
			case '}', ']', ',':
				return s.Pos + 1, true
			default:
				s.scalar = true
			}
			continue
		}
		if s.scalar {
			if !IsNonDelim(c) {
				return s.Pos, true
			}
			continue
		}
		switch c {
		case '"':
			s.inStr = true
		case '{', '[':
			s.depth++
		case '}', ']':
			if s.depth--; s.depth == 0 {
				return s.Pos + 1, true
			}
		}
	}
	if eof && s.scalar {
		return len(b), true
	}
	return 0, false
}
