package gdec

import (
	"encoding/binary"
	"math/bits"
)

// ExtentScanner finds the end of the JSON value starting at Pos, resuming
// across buffer refills. It judges only extent: the parse that follows owns
// every grammar verdict, so malformed input merely ends somewhere.
type ExtentScanner struct {
	Pos     int
	depth   int
	started bool
	scalar  bool
	inStr   bool
	esc     bool // b[Pos] is the byte a backslash escapes
}

// Scan advances over b and reports the value's end offset once known. eof
// reports that b holds the rest of the input, which ends a scalar. Each
// call resumes over a b extending the previous one.
func (s *ExtentScanner) Scan(b []byte, eof bool) (int, bool) {
	i := s.Pos
	if !s.started {
		if i >= len(b) {
			return 0, false
		}
		s.started = true
		switch b[i] {
		case '"':
			s.inStr = true
		case '{', '[':
			s.depth = 1
		case '}', ']', ',':
			return i + 1, true
		default:
			s.scalar = true
		}
		i++
	}
	if s.scalar {
		for ; i < len(b); i++ {
			if !IsNonDelim(b[i]) {
				return i, true
			}
		}
		s.Pos = i
		if eof {
			return len(b), true
		}
		return 0, false
	}
	depth, inStr := s.depth, s.inStr
	if s.esc {
		i++
	}
	// One load flags every quote, backslash, and bracket of a word, and the
	// walk takes the flags in byte order, so the string state may flip
	// mid-word. Inside a string only quotes and backslashes count, outside
	// only quotes and brackets. A backslash drops the byte after it, the
	// next word's first when the backslash sits last.
	for i+8 <= len(b) {
		w := binary.LittleEndian.Uint64(b[i:])
		f := w | lsb*0x20 // bit 5 folds '[' onto '{' and ']' onto '}'
		q, e := eqMask(w, '"'), eqMask(w, '\\')
		o, c := eqMask(f, '{'), eqMask(f, '}')
		next := i + 8
		for m := q | e | o | c; m != 0; {
			bit := m & -m
			m ^= bit
			switch {
			case inStr && e&bit != 0:
				if bit == 1<<63 {
					next++
				}
				m &^= bit << 8
			case q&bit != 0:
				if inStr = !inStr; !inStr && depth == 0 {
					return i + bits.TrailingZeros64(bit)>>3 + 1, true
				}
			case inStr || e&bit != 0:
			case o&bit != 0:
				depth++
			default:
				if depth--; depth == 0 {
					return i + bits.TrailingZeros64(bit)>>3 + 1, true
				}
			}
		}
		i = next
	}
	for ; i < len(b); i++ {
		ch := b[i]
		if inStr {
			switch ch {
			case '\\':
				i++
			case '"':
				if inStr = false; depth == 0 {
					return i + 1, true
				}
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth--; depth == 0 {
				return i + 1, true
			}
		}
	}
	// A backslash ending b leaves i at len(b)+1: the escaped byte opens the
	// next refill.
	s.esc = i > len(b)
	s.Pos, s.depth, s.inStr = min(i, len(b)), depth, inStr
	return 0, false
}

// eqMask sets the high bit of exactly the bytes of w equal to c.
func eqMask(w uint64, c byte) uint64 {
	const low7 = ^uint64(msb)
	x := w ^ (lsb * uint64(c))
	return ^((x&low7 + low7) | x | low7)
}
