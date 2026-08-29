package value

import (
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// bad is the sentinel index returned by the scanner on a syntax error.
const bad = -1

// Raw is the byte-backed view of a parsed JSON value: it carries the raw JSON
// bytes and accessors walk them on demand via the Go scanner.
type Raw []byte

// Bytes returns the underlying raw JSON bytes without copying.
func (r Raw) Bytes() []byte { return r }

// Exists reports whether r holds any bytes. A missing key or an out of range
// index yields a Raw for which Exists is false, so lookups can be chained and
// checked once at the end.
func (r Raw) Exists() bool { return len(skipWSTrim(r)) > 0 }

// Type reports the kind of JSON value held by r. It only inspects the first
// significant byte, so it is cheap but does not validate the whole span. Use
// Valid for a full check.
func (r Raw) Type() Kind {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) {
		return KindInvalid
	}
	switch b[i] {
	case '{':
		return KindObject
	case '[':
		return KindArray
	case '"':
		return KindString
	case 't', 'f':
		return KindBool
	case 'n':
		return KindNull
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return KindNumber
	}
	return KindInvalid
}

// Valid reports whether r holds exactly one well formed JSON value, optionally
// surrounded by whitespace.
func (r Raw) Valid() bool {
	b := r
	i := skipWS(b, 0)
	end := scanValue(b, i)
	if end == bad {
		return false
	}
	return skipWS(b, end) == len(b)
}

// Len returns the number of elements in an array or the number of keys in an
// object. It returns 0 for every other kind, including invalid JSON.
func (r Raw) Len() int {
	n := 0
	switch r.Type() {
	case KindArray:
		r.ForEachElem(func(int, Raw) bool { n++; return true })
	case KindObject:
		r.ForEachKey(func(string, Raw) bool { n++; return true })
	}
	return n
}

// Str returns the decoded string held by r, with escapes resolved. The second
// result is false if r does not hold a well formed JSON string.
func (r Raw) Str() (string, bool) {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '"' {
		return "", false
	}
	end := scanString(b, i)
	if end == bad || skipWS(b, end) != len(b) {
		return "", false
	}
	return unquote(b[i:end])
}

// Int returns the integer held by r. The second result is false if r does not
// hold a JSON number expressible as an int64.
func (r Raw) Int() (int64, bool) {
	s, ok := r.numberSpan()
	if !ok {
		return 0, false
	}
	// string(s) is passed straight to strconv, which does not retain it; the
	// compiler drops the byte-to-string conversion, so this allocates nothing.
	n, err := strconv.ParseInt(string(s), 10, 64)
	if err == nil {
		return n, true
	}
	// Accept integral floats such as 1e3 and 2.0 the way callers expect.
	f, ferr := strconv.ParseFloat(string(s), 64)
	if ferr != nil || f != float64(int64(f)) {
		return 0, false
	}
	return int64(f), true
}

// Float returns the number held by r. The second result is false if r does not
// hold a JSON number.
func (r Raw) Float() (float64, bool) {
	s, ok := r.numberSpan()
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(s), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// Bool returns the boolean held by r. The second result is false if r does not
// hold true or false.
func (r Raw) Bool() (bool, bool) {
	switch string(skipWSTrim(r)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// IsNull reports whether r holds JSON null.
func (r Raw) IsNull() bool { return string(skipWSTrim(r)) == "null" }

// Get walks into nested objects, one key per argument, and returns the value at
// the end of the path. It returns a Raw for which Exists is false if any key
// is missing or a level is not an object.
func (r Raw) Get(keys ...string) Raw {
	cur := r
	for _, k := range keys {
		child := cur.field(k)
		if !child.Exists() && child == nil {
			return nil
		}
		cur = child
	}
	return cur
}

// field returns the value of key k in the object r, or nil if absent. It scans
// the object directly and compares each key against k as raw bytes, so a key
// with no escapes never materializes a string and the lookup allocates nothing.
func (r Raw) field(k string) Raw {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return nil
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		return nil
	}
	for {
		i = skipWS(b, i)
		if i >= len(b) || b[i] != '"' {
			return nil
		}
		keyEnd := scanString(b, i)
		if keyEnd == bad {
			return nil
		}
		if keyEqual(b[i:keyEnd], k) {
			i = skipWS(b, keyEnd)
			if i >= len(b) || b[i] != ':' {
				return nil
			}
			i = skipWS(b, i+1)
			valEnd := scanValue(b, i)
			if valEnd == bad {
				return nil
			}
			return Raw(b[i:valEnd])
		}
		// Not this key: skip its value and advance to the next member.
		i = skipWS(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return nil
		}
		i = skipWS(b, i+1)
		valEnd := scanValue(b, i)
		if valEnd == bad {
			return nil
		}
		i = skipWS(b, valEnd)
		if i >= len(b) {
			return nil
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return nil
		default:
			return nil
		}
	}
}

// keyEqual reports whether the quoted JSON key q (with its surrounding quotes)
// equals the Go string k. The fast path scans the bytes between the quotes and
// compares them directly when no escape is present; escaped keys fall back to a
// full unquote. The fast path allocates nothing.
func keyEqual(q []byte, k string) bool {
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return false
	}
	s := q[1 : len(q)-1]
	if slices.Contains(s, '\\') {
		dec, ok := unquote(q)
		return ok && dec == k
	}
	if len(s) != len(k) {
		return false
	}
	for i := range s {
		if s[i] != k[i] {
			return false
		}
	}
	return true
}

// Index returns the i'th element of the array r, or a Raw for which Exists is
// false if r is not an array or i is out of range. Negative i counts from the
// end.
func (r Raw) Index(i int) Raw {
	if i < 0 {
		n := r.Len()
		if i += n; i < 0 {
			return nil
		}
	}
	var out Raw
	found := false
	r.ForEachElem(func(idx int, elem Raw) bool {
		if idx == i {
			out, found = elem, true
			return false
		}
		return true
	})
	if !found {
		return nil
	}
	return out
}

// ForEachKey calls fn for each member of the object r, in document order.
// Iteration stops early if fn returns false, and stops silently at the first
// syntax error. fn is never called if r is not an object. The child Raw
// passed to fn aliases r's raw bytes; read it eagerly or call String before
// mutating r if you need to retain it.
func (r Raw) ForEachKey(fn func(key string, val Raw) bool) {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		return
	}
	for {
		i = skipWS(b, i)
		if i >= len(b) || b[i] != '"' {
			return
		}
		keyEnd := scanString(b, i)
		if keyEnd == bad {
			return
		}
		key, ok := unquote(b[i:keyEnd])
		if !ok {
			return
		}
		i = skipWS(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return
		}
		i = skipWS(b, i+1)
		valEnd := scanValue(b, i)
		if valEnd == bad {
			return
		}
		if !fn(key, Raw(b[i:valEnd])) {
			return
		}
		i = skipWS(b, valEnd)
		if i >= len(b) {
			return
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return
		default:
			return
		}
	}
}

// ForEachElem calls fn for each element of the array r, in order. Iteration
// stops early if fn returns false, and stops silently at the first syntax
// error. fn is never called if r is not an array.
func (r Raw) ForEachElem(fn func(i int, val Raw) bool) {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '[' {
		return
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == ']' {
		return
	}
	for idx := 0; ; idx++ {
		i = skipWS(b, i)
		end := scanValue(b, i)
		if end == bad {
			return
		}
		if !fn(idx, Raw(b[i:end])) {
			return
		}
		i = skipWS(b, end)
		if i >= len(b) {
			return
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return
		default:
			return
		}
	}
}

// GetString walks the key path and returns the string at the end.
func (r Raw) GetString(keys ...string) (string, bool) { return r.Get(keys...).Str() }

// GetInt walks the key path and returns the integer at the end.
func (r Raw) GetInt(keys ...string) (int64, bool) { return r.Get(keys...).Int() }

// GetFloat walks the key path and returns the number at the end.
func (r Raw) GetFloat(keys ...string) (float64, bool) { return r.Get(keys...).Float() }

// GetBool walks the key path and returns the boolean at the end.
func (r Raw) GetBool(keys ...string) (bool, bool) { return r.Get(keys...).Bool() }

// String returns the raw JSON text of r. It is not the decoded string value;
// use Str for that.
func (r Raw) String() string { return string(r) }

// numberSpan returns the byte slice of the single JSON number held by r (with
// surrounding whitespace trimmed), or false if r is not exactly one number. It
// does not allocate; callers feed the bytes to strconv via a string conversion
// that the compiler optimizes away.
func (r Raw) numberSpan() ([]byte, bool) {
	b := r
	i := skipWS(b, 0)
	if i >= len(b) {
		return nil, false
	}
	if c := b[i]; c != '-' && !isDigit(c) {
		return nil, false
	}
	end := scanNumber(b, i)
	if end == bad || skipWS(b, end) != len(b) {
		return nil, false
	}
	return b[i:end], true
}

// --- scanner primitives ---
//
// The only scanning primitive for values is scanValue; Type, Get, Index, Len,
// ForEachKey, ForEachElem and Valid are all expressed in terms of it, so syntax
// handling lives in exactly one place.

// wsLUT marks the four JSON whitespace bytes.
var wsLUT = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

// skipWS returns the index of the first non whitespace byte at or after i.
// The result may be len(b).
func skipWS(b []byte, i int) int {
	for i < len(b) && wsLUT[b[i]] {
		i++
	}
	return i
}

// skipWSTrim returns b with leading and trailing whitespace removed.
func skipWSTrim(b []byte) []byte {
	i := skipWS(b, 0)
	j := len(b)
	for j > i && wsLUT[b[j-1]] {
		j--
	}
	return b[i:j]
}

// scanValue validates the single JSON value starting at b[i] and returns the
// index just past it. It returns bad on any syntax error.
//
// i must already point at a non whitespace byte.
func scanValue(b []byte, i int) int {
	if i >= len(b) {
		return bad
	}
	switch b[i] {
	case '{':
		return scanObject(b, i)
	case '[':
		return scanArray(b, i)
	case '"':
		return scanString(b, i)
	case 't':
		return scanLit(b, i, "true")
	case 'f':
		return scanLit(b, i, "false")
	case 'n':
		return scanLit(b, i, "null")
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return scanNumber(b, i)
	}
	return bad
}

// scanLit matches a bare literal such as true, false or null.
func scanLit(b []byte, i int, lit string) int {
	if i+len(lit) > len(b) || string(b[i:i+len(lit)]) != lit {
		return bad
	}
	return i + len(lit)
}

// scanObject scans from the opening brace to just past the matching close.
func scanObject(b []byte, i int) int {
	i++ // consume '{'
	i = skipWS(b, i)
	if i < len(b) && b[i] == '}' {
		return i + 1
	}
	for {
		i = skipWS(b, i)
		if i >= len(b) || b[i] != '"' {
			return bad
		}
		if i = scanString(b, i); i == bad {
			return bad
		}
		i = skipWS(b, i)
		if i >= len(b) || b[i] != ':' {
			return bad
		}
		i = skipWS(b, i+1)
		if i = scanValue(b, i); i == bad {
			return bad
		}
		i = skipWS(b, i)
		if i >= len(b) {
			return bad
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return i + 1
		default:
			return bad
		}
	}
}

// scanArray scans from the opening bracket to just past the matching close.
func scanArray(b []byte, i int) int {
	i++ // consume '['
	i = skipWS(b, i)
	if i < len(b) && b[i] == ']' {
		return i + 1
	}
	for {
		i = skipWS(b, i)
		if i = scanValue(b, i); i == bad {
			return bad
		}
		i = skipWS(b, i)
		if i >= len(b) {
			return bad
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return i + 1
		default:
			return bad
		}
	}
}

// scanString scans a quoted string, validating escapes. b[i] must be '"'.
// The returned index is just past the closing quote.
func scanString(b []byte, i int) int {
	i++ // consume opening quote
	for i < len(b) {
		c := b[i]
		switch {
		case c == '"':
			return i + 1
		case c == '\\':
			i++
			if i >= len(b) {
				return bad
			}
			switch b[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				if i+4 >= len(b) {
					return bad
				}
				for k := 1; k <= 4; k++ {
					if hexVal(b[i+k]) < 0 {
						return bad
					}
				}
				i += 5
			default:
				return bad
			}
		case c < 0x20:
			// Raw control bytes are not legal inside a JSON string.
			return bad
		default:
			i++
		}
	}
	return bad
}

// scanNumber validates JSON number grammar: an optional minus, an integer part
// with no leading zeros, an optional fraction, and an optional exponent.
func scanNumber(b []byte, i int) int {
	start := i
	if i < len(b) && b[i] == '-' {
		i++
	}
	// Integer part.
	if i >= len(b) {
		return bad
	}
	if b[i] == '0' {
		i++
	} else if b[i] >= '1' && b[i] <= '9' {
		for i < len(b) && isDigit(b[i]) {
			i++
		}
	} else {
		return bad
	}
	// Fraction.
	if i < len(b) && b[i] == '.' {
		i++
		if i >= len(b) || !isDigit(b[i]) {
			return bad
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
			return bad
		}
		for i < len(b) && isDigit(b[i]) {
			i++
		}
	}
	if i == start {
		return bad
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// hexVal returns the value of a hex digit, or a negative number if c is not one.
func hexVal(c byte) int {
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

// unquote decodes a quoted JSON string, including q's surrounding quotes.
// It resolves escapes and combines UTF-16 surrogate pairs.
func unquote(q []byte) (string, bool) {
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return "", false
	}
	s := q[1 : len(q)-1]

	// Fast path: no escapes means the bytes are already the value.
	esc := -1
	for i := range len(s) {
		if s[i] == '\\' {
			esc = i
			break
		}
	}
	if esc < 0 {
		return string(s), true
	}

	buf := make([]byte, 0, len(s))
	buf = append(buf, s[:esc]...)
	for i := esc; i < len(s); {
		c := s[i]
		if c != '\\' {
			buf = append(buf, c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		switch s[i] {
		case '"':
			buf = append(buf, '"')
			i++
		case '\\':
			buf = append(buf, '\\')
			i++
		case '/':
			buf = append(buf, '/')
			i++
		case 'b':
			buf = append(buf, '\b')
			i++
		case 'f':
			buf = append(buf, '\f')
			i++
		case 'n':
			buf = append(buf, '\n')
			i++
		case 'r':
			buf = append(buf, '\r')
			i++
		case 't':
			buf = append(buf, '\t')
			i++
		case 'u':
			r, next, ok := decodeUnicodeEscape(s, i)
			if !ok {
				return "", false
			}
			buf = utf8.AppendRune(buf, r)
			i = next
		default:
			return "", false
		}
	}
	return string(buf), true
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
		h := hexVal(s[i+k])
		if h < 0 {
			return 0, false
		}
		r = r<<4 | h
	}
	return rune(r), true
}
