package gbind

import (
	"encoding/binary"
	"math/bits"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
)

// text is the source as the walk's spine passes it: base and length, two
// registers per call. Its loads carry no bounds check; at and word require
// an offset the caller has proven in range, peek observes 0x20 past the
// end as the native scanners do in their padding.
type text struct {
	b unsafe.Pointer
	n int
}

func mkText(src []byte) text { return text{unsafe.Pointer(unsafe.SliceData(src)), len(src)} }

// at reads byte i < n.
func (s text) at(i int) byte { return *(*byte)(unsafe.Add(s.b, i)) }

// word reads the little-endian word at i, i+8 <= n.
func (s text) word(i int) uint64 {
	return binary.LittleEndian.Uint64((*[8]byte)(unsafe.Add(s.b, i))[:])
}

func (s text) ptr(i int) *byte { return (*byte)(unsafe.Add(s.b, i)) }

func (s text) peek(i int) byte {
	if uint(i) < uint(s.n) {
		return s.at(i)
	}
	return ' '
}

// skip returns the first token start at or past o: o itself when a token
// follows directly, as it mostly does.
func (s text) skip(o int) int {
	if o < s.n && s.at(o) > ' ' {
		return o
	}
	return s.skipWS(o)
}

// skipWS passes the whitespace at i. Gaps are short (`": "`, a newline and
// an indent), so a byte loop beats a word scan, whose first word mostly
// ends the run and whose exit is a serial load, ctz, and recheck.
func (s text) skipWS(i int) int {
	for i < s.n && isWS[s.at(i)] {
		i++
	}
	return i
}

// atomEnd checks the literal at token start p, whose first byte ch is t,
// f, or n, and its delimiter successor, and returns the literal's end.
func (s text) atomEnd(p int, ch byte) (int, bool) {
	var end int
	switch ch {
	case 't':
		end = p + 4
		if end > s.n || s.word4(p) != 't'|'r'<<8|'u'<<16|'e'<<24 {
			return p, false
		}
	case 'n':
		end = p + 4
		if end > s.n || s.word4(p) != 'n'|'u'<<8|'l'<<16|'l'<<24 {
			return p, false
		}
	case 'f':
		end = p + 5
		if end > s.n || s.word4(p+1) != 'a'|'l'<<8|'s'<<16|'e'<<24 {
			return p, false
		}
	default:
		return p, false
	}
	return end, !gdec.IsNonDelim(s.peek(end))
}

// word4 reads the little-endian 32-bit word at i, i+4 <= n.
func (s text) word4(i int) uint32 {
	return binary.LittleEndian.Uint32((*[4]byte)(unsafe.Add(s.b, i))[:])
}

// isWS classifies the JSON whitespace bytes. A byte index needs no bounds
// check.
var isWS = [256]bool{' ': true, '\n': true, '\t': true, '\r': true}

// quoteOrEscape returns the first '"' or '\\' at or past i, or n.
func (s text) quoteOrEscape(i int) int {
	for ; i+8 <= s.n; i += 8 {
		if m := gdec.QuoteOrEscapeMask(s.word(i)); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
	}
	for ; i < s.n; i++ {
		if c := s.at(i); c == '"' || c == '\\' {
			return i
		}
	}
	return s.n
}

// stringEnd returns the closing quote of the string body at i, a backslash
// escaping the byte after it, or n for an unclosed body.
func (s text) stringEnd(i int) int {
	for {
		if i = s.quoteOrEscape(i); i >= s.n || s.at(i) == '"' {
			return min(i, s.n)
		}
		i += 2
	}
}

// view returns the text as a byte slice over its own bytes, for the gdec
// scanners that take a span of the source.
func (s text) view() []byte { return unsafe.Slice((*byte)(s.b), s.n) }

// quoteOrEscapeBody is quoteOrEscape over a string body under the strict
// scan, as gdec.QuoteOrEscapeBody walks it: a span that violates the body
// policy reports a negative stop.
func (s text) quoteOrEscapeBody(i int) int { return gdec.QuoteOrEscapeBody(s.view(), i) }

// bodyOK reports whether the body span [i, j) holds the strict body
// policy.
func (s text) bodyOK(i, j int) bool { return gdec.ValidateBody(s.view(), i, j) }

// skipNested passes the rest of a container entered at depth one, as a
// token walk does, and returns the offset past its close. A scalar's
// backslash escapes a following quote or backslash. Under the strict scan a
// string body that violates the policy stops with stopBody. With commas, a
// comma followed by a close or another comma stops with stopComma at that
// token.
func (s text) skipNested(i int, strict, commas bool) (int, skipStop) {
	depth := 1
	for i < s.n {
		switch skipClass[s.at(i)] {
		case skScalar:
			i++
		case skWS:
			i = s.skipWS(i)
		case skQuote:
			open := i
			if i = s.stringEnd(i + 1); i >= s.n {
				return s.n, stopUnclosed
			}
			if strict && !s.bodyOK(open+1, i) {
				return open, stopBody
			}
			i++
		case skOpen:
			depth++
			i++
		case skClose:
			if depth--; depth == 0 {
				return i + 1, stopClosed
			}
			i++
		case skComma:
			if i = s.skip(i + 1); commas && i < s.n {
				if c := s.at(i); c == ']' || c == '}' || c == ',' {
					return i, stopComma
				}
			}
		case skEscape:
			if i+1 < s.n && (s.at(i+1) == '"' || s.at(i+1) == '\\') {
				i++
			}
			i++
		}
	}
	return s.n, stopEOF
}

// skipClass classifies the bytes skipNested treats apart.
var skipClass = [256]uint8{
	' ': skWS, '\n': skWS, '\t': skWS, '\r': skWS,
	'"': skQuote, '{': skOpen, '[': skOpen, '}': skClose, ']': skClose,
	',': skComma, '\\': skEscape,
}

const (
	skScalar = iota
	skWS
	skQuote
	skOpen
	skClose
	skComma
	skEscape
)

// skipStop is how skipNested ended.
type skipStop uint8

const (
	stopClosed   skipStop = iota
	stopEOF               // the text ended inside the container
	stopUnclosed          // a string runs to the end
	stopBody              // a string body violates the strict policy
	stopComma             // a comma precedes a close or another comma
)
