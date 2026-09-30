// Package gdec holds the pure-Go decode primitives that stand in for the
// native ndec blob when it is not mapped: the structural scan and its
// index-free verdict, the token, string, number, and atom lexers, the DOM
// tape walker, and the validation walk. Every primitive reproduces its
// native counterpart's accepted language and output bytes; the native
// sources under native/ndec/impl are the spec.
//
// Reads past the end of the source observe 0x20, the value the native
// scanners see in their padding, so callers need not pad.
package gdec

import (
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// ScanMode selects the structural scan's validation policy.
type ScanMode uint8

const (
	// ScanLax passes raw control bytes and malformed UTF-8 inside strings.
	ScanLax ScanMode = iota
	// ScanStrict rejects malformed UTF-8 anywhere and raw control bytes
	// inside strings.
	ScanStrict
	// ScanCtl rejects raw control bytes inside strings only, the Valid
	// policy that follows encoding/json.
	ScanCtl
)

// ScanSlack is the index slots the scan needs past one per source byte:
// three sentinels plus the native scanner's store overshoot, kept equal so
// capacities sized for the native entry fit.
const ScanSlack = 24

// nonDelim mirrors non_delim in core/delim.h: 1 for every byte that is not
// whitespace or one of { } [ ] ,. A ':' after a scalar is dirty: a scalar
// is never a key.
var nonDelim = func() (t [256]byte) {
	for i := range t {
		t[i] = 1
	}
	for _, c := range []byte(" \t\n\r{}[],") {
		t[c] = 0
	}
	return t
}()

// IsNonDelim reports whether c cannot terminate a scalar token.
func IsNonDelim(c byte) bool { return nonDelim[c] != 0 }

// IsDigit reports whether c is an ASCII decimal digit.
func IsDigit(c byte) bool { return c-'0' < 10 }

// At reads src[i], or 0x20 past either end: the padding byte the native
// scanners observe.
func At(src []byte, i int) byte {
	if uint(i) < uint(len(src)) {
		return src[i]
	}
	return 0x20
}

// TrimTrailingWS drops the whitespace a structural span carries between a
// value and the next structural byte.
func TrimTrailingWS(b []byte) []byte {
	for len(b) > 0 {
		switch b[len(b)-1] {
		case ' ', '\t', '\n', '\r':
			b = b[:len(b)-1]
		default:
			return b
		}
	}
	return b
}

// byteClass: 0 scalar body, 1 whitespace, 2 operator.
var byteClass = func() (t [256]byte) {
	for _, c := range []byte(" \t\n\r") {
		t[c] = 1
	}
	for _, c := range []byte("{}[],:") {
		t[c] = 2
	}
	return t
}()

// Scan writes the structural index of src into out: every operator outside
// a string, every opening quote, and the first byte of every scalar token,
// followed by three sentinels equal to len(src). It returns the index count
// (sentinels excluded) and the number of scalar starts. ok is false for an
// unclosed string, a policy violation, or len(out) < len(src)+ScanSlack.
//
// Escapes resolve by backslash-run parity over the whole input, inside and
// outside strings, exactly as the SIMD scanner's odd-bits carry does.
func Scan(src []byte, out []uint32, mode ScanMode) (n, scalars int, ok bool) {
	if len(out) < len(src)+ScanSlack {
		return 0, 0, false
	}
	inStr, esc, prevSW, ctlErr := false, false, true, false
	for i, c := range src {
		quote := c == '"' && !esc
		esc = c == '\\' && !esc
		if inStr {
			if quote {
				inStr = false
				prevSW = true
				continue
			}
			if c < 0x20 {
				ctlErr = true
			}
			prevSW = byteClass[c] == 1
			continue
		}
		if quote {
			out[n] = uint32(i)
			n++
			inStr = true
			prevSW = true
			continue
		}
		switch byteClass[c] {
		case 2:
			out[n] = uint32(i)
			n++
			prevSW = true
		case 1:
			prevSW = true
		default:
			if prevSW {
				out[n] = uint32(i)
				n++
				scalars++
			}
			prevSW = false
		}
	}
	if inStr {
		return 0, 0, false
	}
	if mode != ScanLax && ctlErr {
		return 0, 0, false
	}
	if mode == ScanStrict && !utf8.Valid(src) {
		return 0, 0, false
	}
	l := uint32(len(src))
	out[n], out[n+1], out[n+2] = l, l, l
	return n, scalars, true
}

// Check is the verdict Scan reaches under mode, without building the
// index: every string closes, and the policy holds.
func Check(src []byte, mode ScanMode) bool {
	ctl := mode != ScanLax
	for i := 0; ; {
		if i = QuoteOrEscape(src, i); i >= len(src) {
			break
		}
		if src[i] == '\\' {
			i += 2
			continue
		}
		for j := i + 1; ; {
			k := QuoteOrEscape(src, j)
			if ctl && hasCtl(src[j:k]) {
				return false
			}
			if k >= len(src) {
				return false
			}
			if src[k] == '"' {
				i = k + 1
				break
			}
			if j = k + 2; j > len(src) || (ctl && src[k+1] < 0x20) {
				return false
			}
		}
	}
	return mode != ScanStrict || utf8.Valid(src)
}

func hasCtl(b []byte) bool {
	for _, c := range b {
		if c < 0x20 {
			return true
		}
	}
	return false
}

// SkipWS returns the first index at or past i that is not whitespace. Runs
// of spaces, the indentation after a newline, skip a word at a time.
// It stays out of line so its callers' fast paths stay small.
//
//go:noinline
func SkipWS(src []byte, i int) int {
	for i < len(src) {
		if c := src[i]; c > ' ' || byteClass[c] != 1 {
			return i
		}
		if i++; i < len(src) && src[i] > ' ' {
			return i
		}
		for ; i+8 <= len(src); i += 8 {
			if x := binary.LittleEndian.Uint64(src[i:]) ^ (lsb * ' '); x != 0 {
				i += bits.TrailingZeros64(x) >> 3
				break
			}
		}
	}
	return i
}

// TokenEnd returns the end of the structural token Scan would index at
// src[o], a non-whitespace byte: one operator byte, a string through its
// closing quote, or a scalar run. ok is false for an unclosed string. At
// the end of src it returns o.
func TokenEnd(src []byte, o int) (int, bool) {
	if o >= len(src) {
		return o, true
	}
	switch c := src[o]; {
	case byteClass[c] == 2:
		return o + 1, true
	case c == '"':
		end, ok := StringEnd(src, o+1)
		return end + 1, ok
	}
	return scalarEnd(src, o), true
}

// scalarEnd ends a scalar run at whitespace, an operator, or an unescaped
// quote. A backslash escapes a following quote or backslash.
func scalarEnd(src []byte, o int) int {
	for i := o; i < len(src); i++ {
		switch c := src[i]; {
		case byteClass[c] != 0 || c == '"':
			return i
		case c == '\\' && i+1 < len(src) && (src[i+1] == '"' || src[i+1] == '\\'):
			i++
		}
	}
	return len(src)
}
