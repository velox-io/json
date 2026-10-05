// Package jsonlit holds the JSON literal grammar the Go side applies to raw
// spans: unquoting strings and validating numbers. The native scanner already
// validated any span it hands over; these helpers serve Go code that must read
// a literal itself and keep that reading identical across packages.
package jsonlit

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Bad is the index ScanNumber returns for input that is not a JSON number.
const Bad = -1

// Unquote decodes a quoted JSON string, including q's surrounding quotes.
// It resolves escapes and combines UTF-16 surrogate pairs. An escape-free
// string comes back as a subslice of q; anything else is a fresh buffer.
func Unquote(q []byte) ([]byte, bool) {
	s, esc, ok := splitQuoted(q)
	if !ok || esc < 0 {
		return s, ok
	}
	return appendUnescaped(make([]byte, 0, len(s)), s, esc)
}

// UnquoteString is Unquote for a caller that wants a string. Its working
// buffer does not outlive the call, so the string is the only allocation.
func UnquoteString(q []byte) (string, bool) {
	s, esc, ok := splitQuoted(q)
	if !ok || esc < 0 {
		return string(s), ok
	}
	b, ok := appendUnescaped(make([]byte, 0, len(s)), s, esc)
	return string(b), ok
}

// splitQuoted returns the body of the quoted string q and the index of its
// first backslash, or -1 when it has none.
func splitQuoted(q []byte) (body []byte, esc int, ok bool) {
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return nil, -1, false
	}
	body = q[1 : len(q)-1]
	for i := range len(body) {
		if body[i] == '\\' {
			return body, i, true
		}
	}
	return body, -1, true
}

// appendUnescaped appends s, whose first escape is at esc, to dst with its
// escapes resolved.
func appendUnescaped(dst, s []byte, esc int) ([]byte, bool) {
	dst = append(dst, s[:esc]...)
	for i := esc; i < len(s); {
		c := s[i]
		if c != '\\' {
			dst = append(dst, c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			return nil, false
		}
		switch s[i] {
		case '"':
			dst = append(dst, '"')
			i++
		case '\\':
			dst = append(dst, '\\')
			i++
		case '/':
			dst = append(dst, '/')
			i++
		case 'b':
			dst = append(dst, '\b')
			i++
		case 'f':
			dst = append(dst, '\f')
			i++
		case 'n':
			dst = append(dst, '\n')
			i++
		case 'r':
			dst = append(dst, '\r')
			i++
		case 't':
			dst = append(dst, '\t')
			i++
		case 'u':
			r, next, ok := decodeUnicodeEscape(s, i)
			if !ok {
				return nil, false
			}
			dst = utf8.AppendRune(dst, r)
			i = next
		default:
			return nil, false
		}
	}
	return dst, true
}

// decodeUnicodeEscape reads the \u escape whose 'u' sits at s[i] and returns
// the rune plus the index just past the escape. A high surrogate consumes a
// following low surrogate escape when one is present; an unpaired surrogate
// becomes U+FFFD, matching encoding/json.
func decodeUnicodeEscape(s []byte, i int) (rune, int, bool) {
	r1, ok := readHex4(s, i+1)
	if !ok {
		return 0, 0, false
	}
	i += 5
	if !utf16.IsSurrogate(r1) {
		return r1, i, true
	}
	// Look for the paired low surrogate.
	if i+5 < len(s) && s[i] == '\\' && s[i+1] == 'u' {
		if r2, ok2 := readHex4(s, i+2); ok2 {
			if r := utf16.DecodeRune(r1, r2); r != utf8.RuneError {
				return r, i + 6, true
			}
		}
	}
	return utf8.RuneError, i, true
}

// readHex4 decodes the four hex digits starting at s[i] into a rune.
func readHex4(s []byte, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	r := 0
	for k := range 4 {
		h := HexVal(s[i+k])
		if h < 0 {
			return 0, false
		}
		r = r<<4 | h
	}
	return rune(r), true
}

// HexVal returns the value of a hex digit, or a negative number if c is not one.
func HexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// ScanNumber validates JSON number grammar starting at b[i]: an optional
// minus, an integer part with no leading zeros, an optional fraction, and an
// optional exponent. It returns the index just past the number, or Bad.
func ScanNumber(b []byte, i int) int {
	start := i
	if i < len(b) && b[i] == '-' {
		i++
	}
	// Integer part.
	if i >= len(b) {
		return Bad
	}
	if b[i] == '0' {
		i++
	} else if b[i] >= '1' && b[i] <= '9' {
		for i < len(b) && isDigit(b[i]) {
			i++
		}
	} else {
		return Bad
	}
	// Fraction.
	if i < len(b) && b[i] == '.' {
		i++
		if i >= len(b) || !isDigit(b[i]) {
			return Bad
		}
		for i < len(b) && isDigit(b[i]) {
			i++
		}
	}
	// Exponent.
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if i >= len(b) || !isDigit(b[i]) {
			return Bad
		}
		for i < len(b) && isDigit(b[i]) {
			i++
		}
	}
	if i == start {
		return Bad
	}
	return i
}

// IsNumber reports whether b is exactly one JSON number.
func IsNumber(b []byte) bool {
	return ScanNumber(b, 0) == len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// wsLUT marks the four JSON whitespace bytes.
var wsLUT = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

// IsSpace reports whether c is JSON whitespace.
func IsSpace(c byte) bool { return wsLUT[c] }

// SkipWS returns the index of the first non-whitespace byte at or after i.
// The result may be len(b).
func SkipWS(b []byte, i int) int {
	for i < len(b) && wsLUT[b[i]] {
		i++
	}
	return i
}
