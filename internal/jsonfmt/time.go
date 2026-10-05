// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The numeric and ISO 8601 codecs below are ported from encoding/json/v2
// (arshal_time.go) so velox reads and writes exactly the text it does.

package jsonfmt

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"time"
)

// appendTimeRFC3339 formats t with an RFC 3339 layout, rejecting timestamps
// that layout writes but RFC 3339 cannot express.
// See https://go.dev/issue/4556 and https://go.dev/issue/54580.
func appendTimeRFC3339(b []byte, t time.Time, layout string) ([]byte, error) {
	n0 := len(b)
	b = t.AppendFormat(b, layout)
	switch s := b[n0:]; {
	case s[len("9999")] != '-': // year must be exactly 4 digits wide
		return b, errors.New("year outside of range [0,9999]")
	case s[len(s)-1] != 'Z':
		c := s[len(s)-len("Z07:00")]
		if ('0' <= c && c <= '9') || parseDec2(s[len(s)-len("07:00"):]) >= 24 {
			return b, errors.New("timezone hour outside of range [0,23]")
		}
	}
	return b, nil
}

// parseTimeRFC3339 parses an RFC 3339 timestamp, with or without fractional
// seconds. It adds the checks time.Time.UnmarshalText omits, since the time
// package accepts some timestamps RFC 3339 forbids.
// TODO(https://go.dev/issue/57912): drop the manual checks once time does them.
func parseTimeRFC3339(b []byte) (time.Time, error) {
	var t time.Time
	if err := t.UnmarshalText(b); err != nil {
		return time.Time{}, err
	}
	newParseError := func(layoutElem, valueElem, message string) error {
		return &time.ParseError{Layout: time.RFC3339, Value: string(b), LayoutElem: layoutElem, ValueElem: valueElem, Message: message}
	}
	switch {
	case b[len("2006-01-02T")+1] == ':': // hour must be two digits
		return time.Time{}, newParseError("15", string(b[len("2006-01-02T"):][:1]), "")
	case b[len("2006-01-02T15:04:05")] == ',': // sub-second separator must be a period
		return time.Time{}, newParseError(".", ",", "")
	case b[len(b)-1] != 'Z':
		switch {
		case parseDec2(b[len(b)-len("07:00"):]) >= 24: // timezone hour must be in range
			return time.Time{}, newParseError("Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone hour out of range")
		case parseDec2(b[len(b)-len("00"):]) >= 60: // timezone minute must be in range
			return time.Time{}, newParseError("Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone minute out of range")
		}
	}
	return t, nil
}

// appendDurationBase10 appends d formatted as a decimal fractional number,
// where pow10 is a power-of-10 used to scale down the number.
func appendDurationBase10(b []byte, d time.Duration, pow10 uint64) []byte {
	b, n := mayAppendDurationSign(b, d)     // append sign
	whole, frac := bits.Div64(0, n, pow10)  // compute whole and frac fields
	b = strconv.AppendUint(b, whole, 10)    // append whole field
	return appendFracBase10(b, frac, pow10) // append frac field
}

// parseDurationBase10 parses d from a decimal fractional number,
// where pow10 is a power-of-10 used to scale up the number.
func parseDurationBase10(b []byte, pow10 uint64) (time.Duration, error) {
	suffix, neg := consumeSign(b, false)                     // consume sign
	wholeBytes, fracBytes := bytesCutByte(suffix, '.', true) // consume whole and frac fields
	whole, okWhole := parseUint(wholeBytes)                  // parse whole field; may overflow
	frac, okFrac := parseFracBase10(fracBytes, pow10)        // parse frac field
	hi, lo := bits.Mul64(whole, pow10)                       // overflow if hi > 0
	sum, co := bits.Add64(lo, frac, 0)                       // overflow if co > 0
	switch d := mayApplyDurationSign(sum, neg); {            // overflow if neg != (d < 0)
	case (!okWhole && whole != math.MaxUint64) || !okFrac:
		return 0, fmt.Errorf("invalid duration %q: %w", b, strconv.ErrSyntax)
	case !okWhole || hi > 0 || co > 0 || neg != (d < 0):
		return 0, fmt.Errorf("invalid duration %q: %w", b, strconv.ErrRange)
	default:
		return d, nil
	}
}

// appendDurationISO8601 appends an ISO 8601 duration with a restricted grammar,
// where leading and trailing zeroes and zero-value designators are omitted.
// It only uses hour, minute, and second designators since ISO 8601 defines
// those as being "accurate", while year, month, week, and day are "nominal".
func appendDurationISO8601(b []byte, d time.Duration) []byte {
	if d == 0 {
		return append(b, "PT0S"...)
	}
	b, n := mayAppendDurationSign(b, d)
	b = append(b, "PT"...)
	n, nsec := bits.Div64(0, n, 1e9)  // compute nsec field
	n, sec := bits.Div64(0, n, 60)    // compute sec field
	hour, min := bits.Div64(0, n, 60) // compute hour and min fields
	if hour > 0 {
		b = append(strconv.AppendUint(b, hour, 10), 'H')
	}
	if min > 0 {
		b = append(strconv.AppendUint(b, min, 10), 'M')
	}
	if sec > 0 || nsec > 0 {
		b = append(appendFracBase10(strconv.AppendUint(b, sec, 10), nsec, 1e9), 'S')
	}
	return b
}

// daysPerYear is the exact average number of days in a year according to
// the Gregorian calendar, which has an extra day each year that is
// a multiple of 4, unless it is evenly divisible by 100 but not by 400.
// This does not take into account leap seconds, which are not deterministic.
const daysPerYear = 365.2425

var errInaccurateDateUnits = errors.New("inaccurate year, month, week, or day units")

// parseDurationISO8601 parses a duration according to ISO 8601-1:2019,
// section 5.5.2.2 and 5.5.2.3 with the following restrictions or extensions:
//
//   - A leading minus sign is permitted for negative duration according
//     to ISO 8601-2:2019, section 4.4.1.9. We do not permit negative values
//     for each "time scale component", which is permitted by section 4.4.1.1,
//     but rarely supported by parsers.
//
//   - A leading plus sign is permitted (and ignored).
//     This is not required by ISO 8601, but not forbidden either.
//     There is some precedent for this as it is supported by the principle of
//     duration arithmetic as specified in ISO 8601-2-2019, section 14.1.
//     Of note, the JavaScript grammar for ISO 8601 permits a leading plus sign.
//
//   - A fractional value is only permitted for accurate units
//     (i.e., hour, minute, and seconds) in the last time component,
//     which is permissible by ISO 8601-1:2019, section 5.5.2.3.
//
//   - Both periods ('.') and commas (',') are supported as the separator
//     between the integer part and fraction part of a number,
//     as specified in ISO 8601-1:2019, section 3.2.6.
//     While ISO 8601 recommends comma as the default separator,
//     most formatters use a period.
//
//   - Leading zeros are ignored. This is not required by ISO 8601,
//     but also not forbidden by the standard. Many parsers support this.
//
//   - Lowercase designators are supported. This is not required by ISO 8601,
//     but also not forbidden by the standard. Many parsers support this.
//
// If the nominal units of year, month, week, or day are present,
// this produces a best-effort value and also reports [errInaccurateDateUnits].
//
// The accepted grammar is identical to JavaScript's Duration:
//
//	https://tc39.es/proposal-temporal/#prod-Duration
func parseDurationISO8601(b []byte) (time.Duration, error) {
	var invalid, overflow, inaccurate, sawFrac bool
	var sumNanos, n, co uint64

	// cutBytes is like [bytes.Cut], but uses either c0 or c1 as the separator.
	cutBytes := func(b []byte, c0, c1 byte) (prefix, suffix []byte, ok bool) {
		for i, c := range b {
			if c == c0 || c == c1 {
				return b[:i], b[i+1:], true
			}
		}
		return b, nil, false
	}

	// mayParseUnit attempts to parse another date or time number
	// identified by the desHi and desLo unit characters.
	// If the part is absent for current unit, it returns b as is.
	mayParseUnit := func(b []byte, desHi, desLo byte, unit time.Duration) []byte {
		number, suffix, ok := cutBytes(b, desHi, desLo)
		if !ok || sawFrac {
			return b // designator is not present or already saw fraction, which can only be in the last component
		}

		// Parse the number.
		// A fraction is allowed for the accurate units in the last part.
		whole, frac, ok := cutBytes(number, '.', ',')
		if ok {
			sawFrac = true
			invalid = invalid || len(frac) == len("") || unit > time.Hour
			if unit == time.Second {
				n, ok = parsePaddedBase10(frac, uint64(time.Second))
				invalid = invalid || !ok
			} else {
				f, err := strconv.ParseFloat("0."+string(frac), 64)
				invalid = invalid || err != nil || len(bytes.Trim(frac[len("."):], "0123456789")) > 0
				n = uint64(math.Round(f * float64(unit))) // never overflows since f is within [0..1]
			}
			sumNanos, co = bits.Add64(sumNanos, n, 0) // overflow if co > 0
			overflow = overflow || co > 0
		}
		for len(whole) > 1 && whole[0] == '0' {
			whole = whole[len("0"):] // trim leading zeros
		}
		n, ok := parseUint(whole)                  // overflow if !ok && MaxUint64
		hi, lo := bits.Mul64(n, uint64(unit))      // overflow if hi > 0
		sumNanos, co = bits.Add64(sumNanos, lo, 0) // overflow if co > 0
		invalid = invalid || (!ok && n != math.MaxUint64)
		overflow = overflow || (!ok && n == math.MaxUint64) || hi > 0 || co > 0
		inaccurate = inaccurate || unit > time.Hour
		return suffix
	}

	suffix, neg := consumeSign(b, true)
	prefix, suffix, okP := cutBytes(suffix, 'P', 'p')
	durDate, durTime, okT := cutBytes(suffix, 'T', 't')
	invalid = invalid || len(prefix) > 0 || !okP || (okT && len(durTime) == 0) || len(durDate)+len(durTime) == 0
	if len(durDate) > 0 { // nominal portion of the duration
		durDate = mayParseUnit(durDate, 'Y', 'y', time.Duration(daysPerYear*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'M', 'm', time.Duration(daysPerYear/12*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'W', 'w', time.Duration(7*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'D', 'd', time.Duration(24*60*60*1e9))
		invalid = invalid || len(durDate) > 0 // unknown elements
	}
	if len(durTime) > 0 { // accurate portion of the duration
		durTime = mayParseUnit(durTime, 'H', 'h', time.Duration(60*60*1e9))
		durTime = mayParseUnit(durTime, 'M', 'm', time.Duration(60*1e9))
		durTime = mayParseUnit(durTime, 'S', 's', time.Duration(1e9))
		invalid = invalid || len(durTime) > 0 // unknown elements
	}
	d := mayApplyDurationSign(sumNanos, neg)
	overflow = overflow || (neg != (d < 0) && d != 0) // overflows signed duration

	switch {
	case invalid:
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, strconv.ErrSyntax)
	case overflow:
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, strconv.ErrRange)
	case inaccurate:
		return d, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, errInaccurateDateUnits)
	default:
		return d, nil
	}
}

// mayAppendDurationSign appends a negative sign if n is negative.
func mayAppendDurationSign(b []byte, d time.Duration) ([]byte, uint64) {
	if d < 0 {
		b = append(b, '-')
		d *= -1
	}
	return b, uint64(d)
}

// mayApplyDurationSign inverts n if neg is specified.
func mayApplyDurationSign(n uint64, neg bool) time.Duration {
	if neg {
		return -1 * time.Duration(n)
	}
	return +1 * time.Duration(n)
}

// appendTimeUnix appends t formatted as a decimal fractional number,
// where pow10 is a power-of-10 used to scale up the number.
func appendTimeUnix(b []byte, t time.Time, pow10 uint64) []byte {
	sec, nsec := t.Unix(), int64(t.Nanosecond())
	if sec < 0 {
		b = append(b, '-')
		sec, nsec = negateSecNano(sec, nsec)
	}
	switch {
	case pow10 == 1e0: // fast case where units is in seconds
		b = strconv.AppendUint(b, uint64(sec), 10)
		return appendFracBase10(b, uint64(nsec), 1e9)
	case uint64(sec) < 1e9: // intermediate case where units is not seconds, but no overflow
		b = strconv.AppendUint(b, uint64(sec)*pow10+uint64(nsec)/(1e9/pow10), 10)
		return appendFracBase10(b, (uint64(nsec)*pow10)%1e9, 1e9)
	default: // slow case where units is not seconds and overflow would occur
		b = strconv.AppendUint(b, uint64(sec), 10)
		b = appendPaddedBase10(b, uint64(nsec)/(1e9/pow10), pow10)
		return appendFracBase10(b, (uint64(nsec)*pow10)%1e9, 1e9)
	}
}

// parseTimeUnix parses t formatted as a decimal fractional number,
// where pow10 is a power-of-10 used to scale down the number.
func parseTimeUnix(b []byte, pow10 uint64) (time.Time, error) {
	suffix, neg := consumeSign(b, false)                     // consume sign
	wholeBytes, fracBytes := bytesCutByte(suffix, '.', true) // consume whole and frac fields
	whole, okWhole := parseUint(wholeBytes)                  // parse whole field; may overflow
	frac, okFrac := parseFracBase10(fracBytes, 1e9/pow10)    // parse frac field
	var sec, nsec int64
	switch {
	case pow10 == 1e0: // fast case where units is in seconds
		sec = int64(whole) // check overflow later after negation
		nsec = int64(frac) // cannot overflow
	case okWhole: // intermediate case where units is not seconds, but no overflow
		sec = int64(whole / pow10)                     // check overflow later after negation
		nsec = int64((whole%pow10)*(1e9/pow10) + frac) // cannot overflow
	case !okWhole && whole == math.MaxUint64: // slow case where units is not seconds and overflow occurred
		width := int(math.Log10(float64(pow10)))                               // compute len(strconv.Itoa(pow10-1))
		whole, okWhole = parseUint(wholeBytes[:len(wholeBytes)-width])         // parse the upper whole field
		mid, _ := parsePaddedBase10(wholeBytes[len(wholeBytes)-width:], pow10) // parse the lower whole field
		sec = int64(whole)                                                     // check overflow later after negation
		nsec = int64(mid*(1e9/pow10) + frac)                                   // cannot overflow
	}
	if neg {
		sec, nsec = negateSecNano(sec, nsec)
	}
	switch t := time.Unix(sec, nsec).UTC(); {
	case (!okWhole && whole != math.MaxUint64) || !okFrac:
		return time.Time{}, fmt.Errorf("invalid time %q: %w", b, strconv.ErrSyntax)
	case !okWhole || neg != (t.Unix() < 0):
		return time.Time{}, fmt.Errorf("invalid time %q: %w", b, strconv.ErrRange)
	default:
		return t, nil
	}
}

// negateSecNano negates a Unix timestamp, where nsec must be within [0, 1e9).
func negateSecNano(sec, nsec int64) (int64, int64) {
	sec = ^sec               // twos-complement negation (i.e., -1*sec + 1)
	nsec = -nsec + 1e9       // negate nsec and add 1e9 (which is the extra +1 from sec negation)
	sec += int64(nsec / 1e9) // handle possible overflow of nsec if it started as zero
	nsec %= 1e9              // ensure nsec stays within [0, 1e9)
	return sec, nsec
}

// appendFracBase10 appends the fraction of n/max10,
// where max10 is a power-of-10 that is larger than n.
func appendFracBase10(b []byte, n, max10 uint64) []byte {
	if n == 0 {
		return b
	}
	return bytes.TrimRight(appendPaddedBase10(append(b, '.'), n, max10), "0")
}

// parseFracBase10 parses the fraction of n/max10,
// where max10 is a power-of-10 that is larger than n.
func parseFracBase10(b []byte, max10 uint64) (n uint64, ok bool) {
	switch {
	case len(b) == 0:
		return 0, true
	case len(b) < len(".0") || b[0] != '.':
		return 0, false
	}
	return parsePaddedBase10(b[len("."):], max10)
}

// appendPaddedBase10 appends a zero-padded encoding of n,
// where max10 is a power-of-10 that is larger than n.
func appendPaddedBase10(b []byte, n, max10 uint64) []byte {
	if n < max10/10 {
		// Formatting of n is shorter than log10(max10),
		// so add max10/10 to ensure the length is equal to log10(max10).
		i := len(b)
		b = strconv.AppendUint(b, n+max10/10, 10)
		b[i]-- // subtract the addition of max10/10
		return b
	}
	return strconv.AppendUint(b, n, 10)
}

// parsePaddedBase10 parses b as the zero-padded encoding of n,
// where max10 is a power-of-10 that is larger than n.
// Truncated suffix is treated as implicit zeros.
// Extended suffix is ignored, but verified to contain only digits.
func parsePaddedBase10(b []byte, max10 uint64) (n uint64, ok bool) {
	pow10 := uint64(1)
	for pow10 < max10 {
		n *= 10
		if len(b) > 0 {
			if b[0] < '0' || '9' < b[0] {
				return n, false
			}
			n += uint64(b[0] - '0')
			b = b[1:]
		}
		pow10 *= 10
	}
	if len(b) > 0 && len(bytes.TrimRight(b, "0123456789")) > 0 {
		return n, false // trailing characters are not digits
	}
	return n, true
}

// consumeSign consumes an optional leading negative or positive sign.
func consumeSign(b []byte, allowPlus bool) ([]byte, bool) {
	if len(b) > 0 {
		if b[0] == '-' {
			return b[len("-"):], true
		} else if b[0] == '+' && allowPlus {
			return b[len("+"):], false
		}
	}
	return b, false
}

// bytesCutByte is similar to bytes.Cut(b, []byte{c}),
// except c may optionally be included as part of the suffix.
func bytesCutByte(b []byte, c byte, include bool) ([]byte, []byte) {
	if i := bytes.IndexByte(b, c); i >= 0 {
		if include {
			return b[:i], b[i:]
		}
		return b[:i], b[i+1:]
	}
	return b, nil
}

// parseDec2 parses b as an unsigned, base-10, 2-digit number.
// The result is undefined if digits are not base-10.
func parseDec2(b []byte) byte {
	if len(b) < 2 {
		return 0
	}
	return 10*(b[0]-'0') + (b[1] - '0')
}

// parseUint parses b as a decimal unsigned integer per the JSON integer
// grammar: no sign, no leading zeros. On overflow it reports
// (math.MaxUint64, false); any other failure reports (0, false).
func parseUint(b []byte) (v uint64, ok bool) {
	const unsafeWidth = 20 // len(fmt.Sprint(uint64(math.MaxUint64)))
	var n int
	for ; len(b) > n && ('0' <= b[n] && b[n] <= '9'); n++ {
		v = 10*v + uint64(b[n]-'0')
	}
	switch {
	case n == 0 || len(b) != n || (b[0] == '0' && string(b) != "0"):
		return 0, false
	case n >= unsafeWidth && (b[0] != '1' || v < 1e19 || n > unsafeWidth):
		return math.MaxUint64, false
	}
	return v, true
}
