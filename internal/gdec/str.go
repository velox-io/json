package gdec

import (
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// String bodies start one past the opening quote. An unclosed body fails
// at the end of src.

const (
	lsb = 0x0101010101010101
	msb = 0x8080808080808080
)

// hasByte flags the bytes of w equal to b in their high bits. Flags above
// the lowest true match may be spurious, so only the lowest is meaningful.
func hasByte(w uint64, b byte) uint64 {
	x := w ^ (lsb * uint64(b))
	return (x - lsb) &^ x & msb
}

// QuoteOrEscapeMask flags the bytes of the little-endian word w that are
// '"' or '\\', meaningful from the lowest flag up to the first match only.
func QuoteOrEscapeMask(w uint64) uint64 { return hasByte(w, '"') | hasByte(w, '\\') }

// QuoteOrEscape returns the index of the first '"' or '\\' at or past i,
// or len(src) when there is none.
func QuoteOrEscape(src []byte, i int) int {
	for ; i+8 <= len(src); i += 8 {
		w := binary.LittleEndian.Uint64(src[i:])
		if m := QuoteOrEscapeMask(w); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
	}
	for ; i < len(src); i++ {
		if c := src[i]; c == '"' || c == '\\' {
			return i
		}
	}
	return len(src)
}

// StringEnd returns the closing quote of the body at src[body] as the
// structural scan resolves it: a backslash escapes the next byte, whatever
// it is. ok is false for an unclosed body.
func StringEnd(src []byte, body int) (int, bool) {
	for i := body; ; i += 2 {
		if i = QuoteOrEscape(src, i); i >= len(src) {
			return len(src), false
		}
		if src[i] == '"' {
			return i, true
		}
	}
}

var simpleEscape = func() (t [256]byte) {
	t['"'] = '"'
	t['\\'] = '\\'
	t['/'] = '/'
	t['b'] = '\b'
	t['f'] = '\f'
	t['n'] = '\n'
	t['r'] = '\r'
	t['t'] = '\t'
	return t
}()

func hexVal(c byte) (uint32, bool) {
	switch {
	case c >= '0' && c <= '9':
		return uint32(c - '0'), true
	case c >= 'a' && c <= 'f':
		return uint32(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return uint32(c-'A') + 10, true
	}
	return 0, false
}

func hex4(src []byte, i int) (uint32, bool) {
	var r uint32
	for k := range 4 {
		v, ok := hexVal(At(src, i+k))
		if !ok {
			return 0, false
		}
		r = r<<4 | v
	}
	return r, true
}

// ScanString validates the escapes of one string body without decoding it
// and returns the index of its closing quote. Each \uXXXX stands alone:
// surrogate pairing is decode policy, not validity.
func ScanString(src []byte, body int) (int, bool) {
	for i := body; ; {
		if i = QuoteOrEscape(src, i); i >= len(src) {
			return len(src), false
		}
		if src[i] == '"' {
			return i, true
		}
		c := At(src, i+1)
		if c == 'u' {
			if _, ok := hex4(src, i+2); !ok {
				return i, false
			}
			i += 6
			continue
		}
		if simpleEscape[c] == 0 {
			return i, false
		}
		i += 2
	}
}

// DecodeEscape decodes the escape whose backslash sits at src[i] into dst,
// and returns the source index past it and the bytes written. A lone or
// unpaired surrogate decodes to U+FFFD.
func DecodeEscape(src []byte, i int, dst []byte) (next, n int, ok bool) {
	c := At(src, i+1)
	if c != 'u' {
		m := simpleEscape[c]
		if m == 0 {
			return i, 0, false
		}
		dst[0] = m
		return i + 2, 1, true
	}
	r, ok := hex4(src, i+2)
	if !ok {
		return i, 0, false
	}
	if r >= 0xD800 && r <= 0xDBFF {
		if At(src, i+6) == '\\' && At(src, i+7) == 'u' {
			if lo, ok := hex4(src, i+8); ok && lo >= 0xDC00 && lo <= 0xDFFF {
				cp := rune((r-0xD800)<<10 + (lo - 0xDC00) + 0x10000)
				return i + 12, utf8.EncodeRune(dst, cp), true
			}
		}
		return i + 6, utf8.EncodeRune(dst, utf8.RuneError), true
	}
	if r >= 0xDC00 && r <= 0xDFFF {
		return i + 6, utf8.EncodeRune(dst, utf8.RuneError), true
	}
	return i + 6, utf8.EncodeRune(dst, rune(r)), true
}

// AppendString appends the decoded body at src[body] to dst under the raw
// policy of DecodeString and returns its closing quote. ok is false for an
// invalid escape or an unclosed body.
func AppendString(dst, src []byte, body int) (out []byte, end int, ok bool) {
	// dst spans its capacity while n counts the bytes written, so a run
	// copies a word per store, the bytes past a stop included.
	n, i := len(dst), body
	dst = dst[:cap(dst)]
	for {
		stop := false
		for i+8 <= len(src) {
			if n+8 > len(dst) {
				dst = growTo(dst, n, n+8+(len(src)-i)/4)
			}
			w := binary.LittleEndian.Uint64(src[i:])
			binary.LittleEndian.PutUint64(dst[n:], w)
			if m := QuoteOrEscapeMask(w); m != 0 {
				k := bits.TrailingZeros64(m) >> 3
				n, i, stop = n+k, i+k, true
				break
			}
			n, i = n+8, i+8
		}
		if !stop {
			for ; i < len(src) && src[i] != '"' && src[i] != '\\'; i++ {
				if n == len(dst) {
					dst = growTo(dst, n, n+1)
				}
				dst[n] = src[i]
				n++
			}
			if i >= len(src) {
				return dst[:n], i, false
			}
		}
		if src[i] == '"' {
			return dst[:n], i, true
		}
		if n+utf8.UTFMax > len(dst) {
			dst = growTo(dst, n, n+utf8.UTFMax)
		}
		next, w, ok := DecodeEscape(src, i, dst[n:])
		if !ok {
			return dst[:n], i, false
		}
		n, i = n+w, next
	}
}

// growTo moves the n bytes of dst to a backing of at least need bytes and
// returns it at full capacity.
func growTo(dst []byte, n, need int) []byte {
	d := make([]byte, max(need, 2*len(dst), 64))
	copy(d, dst[:n])
	return d[:cap(d)]
}

// DecodeString decodes one string body into dst under the raw policy: bytes
// copy verbatim (high-bit and control bytes included) and only escapes
// decode. dst[n] receives the quote sentinel, so dst needs the body's
// source span plus one byte. esc reports whether any escape decoded.
func DecodeString(src []byte, body int, dst []byte) (n int, esc, ok bool) {
	for i := body; ; {
		j := QuoteOrEscape(src, i)
		n += copy(dst[n:], src[i:j])
		if j >= len(src) {
			return n, esc, false
		}
		if src[j] == '"' {
			dst[n] = '"'
			return n, esc, true
		}
		next, w, ok := DecodeEscape(src, j, dst[n:])
		if !ok {
			return n, esc, false
		}
		i = next
		n += w
		esc = true
	}
}
