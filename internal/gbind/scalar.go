package gbind

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// Strings.

// alloc carves n bytes from the string view. Decoded strings and number
// text never outgrow the source span they come from, so the carves of a
// contiguous bind fit the srcLen its arena reserves; a span bound twice, as
// a poly entry may be, spills to its own backing. A stream scope's view is
// a chunk, replaced by a fresh one once full.
func (c *binder) alloc(n int) []byte {
	l := c.strUsed
	if n > len(c.strs)-l {
		if c.streams == 0 {
			return make([]byte, n)
		}
		c.newStrView(max(n, scopeStrView))
		l = 0
	}
	c.strUsed = l + n
	return c.strs[l : l+n : l+n]
}

// scopeStrView sizes a stream scope's string chunk. Strings never move, so
// an item a handler keeps pins the chunk it was carved from, as it pins its
// slot blocks.
const scopeStrView = 4 << 10

// newStrView installs a fresh n-byte string view on the allocator.
func (c *binder) newStrView(n int) {
	c.a.InstallScopedStrView(n)
	c.strs, c.strUsed = c.a.StrArena, 0
}

// quoteOrEscape returns the first quote or backslash in the body of the
// string at src[pos], and whether it closes the string. Under the strict
// scan it reports -1 when the span it walked violates the body policy.
func (c *binder) quoteOrEscape(pos int) (int, bool) {
	if c.strict {
		i := gdec.QuoteOrEscapeBody(c.src, pos+1)
		if i < 0 {
			return -1, false
		}
		return i, i < len(c.src) && c.src[i] == '"'
	}
	i := gdec.QuoteOrEscape(c.src, pos+1)
	return i, i < len(c.src) && c.src[i] == '"'
}

// str decodes the string at the cursor and moves past it. A persistent
// result owns its bytes or aliases strBase; a transient one may view src
// and must not outlive the Bind call. borrowed reports an alias of
// caller-owned input under the zero-copy opt.
func (c *binder) str(persist bool) (b []byte, borrowed bool, err error) {
	pos := c.p
	end, plain := c.quoteOrEscape(pos)
	if end < 0 {
		return nil, false, c.failNoPos(ndec.BindErrSyntax)
	}
	if !plain {
		if !persist {
			b, err = c.unescape(pos, end, (*c.scratch)[:0])
			*c.scratch = b
			return b, false, c.strictErr(err, pos)
		}
		b, err = c.unescapeOwned(pos, end)
		return b, false, c.strictErr(err, pos)
	}
	c.to(end + 1)
	body := pos + 1
	switch {
	case c.strBase != nil:
		return c.strBase[body:end:end], true, nil
	case !persist:
		return c.src[body:end], false, nil
	}
	b = c.alloc(end - body)
	copy(b, c.src[body:end])
	return b, false, nil
}

// strictErr passes the escaped walk's error through, first re-checking the
// whole body it passed when it succeeded under the strict scan, as only the
// span ahead of the first escape went through the fused scanner.
func (c *binder) strictErr(err error, pos int) error {
	if err != nil || !c.strict {
		return err
	}
	if !gdec.ValidateBody(c.src, pos+1, c.p-1) {
		return c.failNoPos(ndec.BindErrSyntax)
	}
	return nil
}

// unescapeOwned decodes the string at src[pos], whose first backslash is at
// esc, into storage it owns and moves past it. It decodes in place past the
// arena's used bytes: a result that stays within the arena is committed
// there, one that outgrew it already owns a fresh backing.
func (c *binder) unescapeOwned(pos, esc int) ([]byte, error) {
	l := c.strUsed
	b, err := c.unescape(pos, esc, c.strs[l:l:len(c.strs)])
	if err != nil {
		return nil, err
	}
	if l < len(c.strs) && unsafe.SliceData(b) == &c.strs[l] {
		c.strUsed = l + len(b)
	}
	return b[:len(b):len(b)], nil
}

// unescape appends the decoding of the string at src[pos], whose first
// backslash is at esc, to dst in one pass and moves past it.
func (c *binder) unescape(pos, esc int, dst []byte) ([]byte, error) {
	out, end, ok := gdec.AppendString(append(dst, c.src[pos+1:esc]...), c.src, esc)
	if !ok {
		return out, c.fail(ndec.BindErrSyntax, uint64(pos))
	}
	c.to(end + 1)
	return out, nil
}

// string is str as a string.
func (c *binder) string(persist bool) (string, error) {
	b, _, err := c.str(persist)
	if err != nil || len(b) == 0 {
		return "", err
	}
	return unsafe.String(&b[0], len(b)), nil
}

// strAt decodes the string at p, a quote, as a persistent string and
// returns the token start past it. A string without an escape aliases the
// zero-copy base or copies into the arena in place.
func (c *binder) strAt(s text, p int) (string, int, error) {
	body := p + 1
	var end int
	if c.strict {
		if end = s.quoteOrEscapeBody(body); end < 0 {
			return "", p, c.failNoPos(ndec.BindErrSyntax)
		}
	} else {
		end = s.quoteOrEscape(body)
	}
	if end < s.n && s.at(end) == '"' {
		n := end - body
		switch {
		case n == 0:
			return "", s.skip(end + 1), nil
		case c.zc != nil:
			return unsafe.String((*byte)(unsafe.Add(c.zc, body)), n), s.skip(end + 1), nil
		}
		b := c.alloc(n)
		copy(b, unsafe.Slice(s.ptr(body), n))
		return unsafe.String(&b[0], n), s.skip(end + 1), nil
	}
	return c.strEscaped(s, p, end)
}

// strEscaped is strAt's path for a string whose first quote or backslash at
// esc does not close it. It finds the close first, so a decode into the
// arena, never longer than its source span, runs without capacity checks.
func (c *binder) strEscaped(s text, p, esc int) (string, int, error) {
	body := p + 1
	end := s.stringEnd(esc)
	if c.strict && !s.bodyOK(body, end) {
		return "", p, c.failNoPos(ndec.BindErrSyntax)
	}
	l := c.strUsed
	if end >= s.n || end-body+8 > len(c.strs)-l {
		c.p = p
		b, err := c.unescapeOwned(p, esc)
		if err != nil {
			return "", c.p, err
		}
		return unsafe.String(&b[0], len(b)), c.p, nil
	}
	d := unsafe.Pointer(&c.strs[l])
	n, i := 0, body
	for {
		// A run copies a word per store, the bytes past its stop included:
		// the arena holds the span plus a word.
		for i+8 <= s.n {
			w := s.word(i)
			binary.LittleEndian.PutUint64((*[8]byte)(unsafe.Add(d, n))[:], w)
			if m := gdec.QuoteOrEscapeMask(w); m != 0 {
				k := bits.TrailingZeros64(m) >> 3
				n, i = n+k, i+k
				goto stop
			}
			n, i = n+8, i+8
		}
		for ; i < end && s.at(i) != '\\'; i++ {
			*(*byte)(unsafe.Add(d, n)) = s.at(i)
			n++
		}
	stop:
		if i >= end {
			break
		}
		next, w, ok := gdec.DecodeEscape(c.src, i, c.strs[l+n:])
		if !ok {
			return "", p, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		n, i = n+w, next
	}
	c.strUsed = l + n
	return unsafe.String((*byte)(d), n), s.skip(end + 1), nil
}

// storeString decodes the string at p into a string header.
func (c *binder) storeString(s text, p int, dst unsafe.Pointer) (int, error) {
	str, p, err := c.strAt(s, p)
	if err == nil {
		*(*string)(dst) = str
	}
	return p, err
}

// Numbers.

// numberAt parses the number at token start p into a numeric kind and
// returns the token start past it. A value the kind cannot hold reports
// mismatch and leaves p; a token outside the number grammar is a syntax
// error, as is a token that runs into a non-delimiter.
func (c *binder) numberAt(s text, p int, dst unsafe.Pointer, k vbind.Kind) (next int, mismatch bool, err error) {
	src := unsafe.Slice((*byte)(s.b), s.n)
	var end int
	switch k {
	case vbind.KindFloat32, vbind.KindFloat64:
		f, e, ok, exact := gdec.FloatToken(src, p)
		if end = e; !ok {
			return p, false, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		if exact && k == vbind.KindFloat64 {
			*(*float64)(dst) = f
		} else if !storeFloatNum(dst, k, unsafe.String(s.ptr(p), end-p)) {
			return p, true, nil
		}
	default:
		mag, neg, e, bad, long := gdec.IntToken(src, p)
		if end = e; bad {
			// A fraction or exponent is a well-formed number the integer
			// kind rejects; every other bad shape is outside the grammar.
			if _, valid := gdec.ValidNumber(src, p); !valid {
				return p, false, c.fail(ndec.BindErrSyntax, uint64(p))
			}
			return p, true, nil
		}
		var fits bool
		if long {
			fits = storeInt(dst, k, unsafe.String(s.ptr(p), end-p))
		} else {
			fits = storeMag(dst, k, mag, neg)
		}
		if !fits {
			return p, true, nil
		}
	}
	if gdec.IsNonDelim(s.peek(end)) {
		return p, false, c.fail(ndec.BindErrSyntax, uint64(p))
	}
	return s.skip(end), false, nil
}

// storeMag stores a magnitude of at most 19 digits into an integer kind,
// reporting false when it does not fit, as storeInt would.
func storeMag(dst unsafe.Pointer, k vbind.Kind, mag uint64, neg bool) bool {
	bits := intBits(k)
	if isSignedKind(k) {
		lim := uint64(1) << (bits - 1)
		if mag > lim || (mag == lim && !neg) {
			return false
		}
		v := int64(mag)
		if neg {
			v = -v
		}
		switch k {
		case vbind.KindInt8:
			*(*int8)(dst) = int8(v)
		case vbind.KindInt16:
			*(*int16)(dst) = int16(v)
		case vbind.KindInt32:
			*(*int32)(dst) = int32(v)
		default:
			*(*int64)(dst) = v
		}
		return true
	}
	if (neg && mag != 0) || (bits < 64 && mag>>bits != 0) {
		return false
	}
	switch k {
	case vbind.KindUint8:
		*(*uint8)(dst) = uint8(mag)
	case vbind.KindUint16:
		*(*uint16)(dst) = uint16(mag)
	case vbind.KindUint32:
		*(*uint32)(dst) = uint32(mag)
	default:
		*(*uint64)(dst) = mag
	}
	return true
}

// storeInt parses a decimal integer into an integer kind, reporting false
// when it does not fit. "-0" fits an unsigned kind.
func storeInt(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	if isSignedKind(k) {
		return storeSigned(dst, k, s)
	}
	if s[0] == '-' {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v != 0 {
			return false
		}
		s = "0"
	}
	return storeUnsigned(dst, k, s)
}

func isSignedKind(k vbind.Kind) bool { return k >= vbind.KindInt && k <= vbind.KindInt64 }

func storeSigned(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	v, err := strconv.ParseInt(s, 10, intBits(k))
	if err != nil {
		return false
	}
	switch k {
	case vbind.KindInt8:
		*(*int8)(dst) = int8(v)
	case vbind.KindInt16:
		*(*int16)(dst) = int16(v)
	case vbind.KindInt32:
		*(*int32)(dst) = int32(v)
	default:
		*(*int64)(dst) = v
	}
	return true
}

func storeUnsigned(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	v, err := strconv.ParseUint(s, 10, intBits(k))
	if err != nil {
		return false
	}
	switch k {
	case vbind.KindUint8:
		*(*uint8)(dst) = uint8(v)
	case vbind.KindUint16:
		*(*uint16)(dst) = uint16(v)
	case vbind.KindUint32:
		*(*uint32)(dst) = uint32(v)
	default:
		*(*uint64)(dst) = v
	}
	return true
}

func storeFloat(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	f, err := strconv.ParseFloat(s, floatBits(k))
	if err != nil {
		return false
	}
	setFloat(dst, k, f)
	return true
}

// storeFloatNum is the direct JSON number path's store. A range overflow
// stores the ±Inf while still reporting the mismatch, like the native
// bind_write_number and encoding/json. The quoted number path keeps the
// destination untouched instead, so it stays on storeFloat.
func storeFloatNum(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	f, err := strconv.ParseFloat(s, floatBits(k))
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			setFloat(dst, k, f)
		}
		return false
	}
	setFloat(dst, k, f)
	return true
}

func floatBits(k vbind.Kind) int {
	if k == vbind.KindFloat32 {
		return 32
	}
	return 64
}

func setFloat(dst unsafe.Pointer, k vbind.Kind, f float64) {
	if k == vbind.KindFloat32 {
		*(*float32)(dst) = float32(f)
	} else {
		*(*float64)(dst) = f
	}
}

func intBits(k vbind.Kind) int {
	switch k {
	case vbind.KindInt8, vbind.KindUint8:
		return 8
	case vbind.KindInt16, vbind.KindUint16:
		return 16
	case vbind.KindInt32, vbind.KindUint32:
		return 32
	}
	return 64
}

// storeNumberText keeps the text of the number token at the cursor in a
// json.Number. A token outside the grammar is a syntax error.
func (c *binder) storeNumberText(dst unsafe.Pointer) error {
	str, q, err := c.numberTextAt(c.txt, c.p)
	if err != nil {
		return err
	}
	c.p = q
	*(*string)(dst) = str
	return nil
}

// numberTextAt returns the text of the number token at p, aliasing the
// zero-copy base or copied into the arena, and the token start past it.
func (c *binder) numberTextAt(s text, p int) (string, int, error) {
	_, end, ok, _ := gdec.FloatToken(unsafe.Slice((*byte)(s.b), s.n), p)
	if !ok {
		return "", p, c.fail(ndec.BindErrSyntax, uint64(p))
	}
	if gdec.IsNonDelim(s.peek(end)) {
		return "", p, c.fail(ndec.BindErrSyntax, uint64(p))
	}
	n := end - p
	if c.zc != nil {
		return unsafe.String((*byte)(unsafe.Add(c.zc, p)), n), s.skip(end), nil
	}
	b := c.alloc(n)
	copy(b, unsafe.Slice(s.ptr(p), n))
	return unsafe.String(&b[0], n), s.skip(end), nil
}

// writeQuotedScalar binds a `,string` body, following encoding/json's
// quoted grammar: strconv for numbers, an embedded JSON string literal for
// strings. Strconv's literal extensions (hex floats, digit separators)
// come along for free, mirroring the native atof core.
func (c *binder) writeQuotedScalar(dst unsafe.Pointer, k vbind.Kind, s string) bool {
	switch k {
	case vbind.KindBool:
		switch s {
		case "true":
			*(*bool)(dst) = true
		case "false":
			*(*bool)(dst) = false
		default:
			return false
		}
		return true
	case vbind.KindString:
		// The native walk takes the body's last byte as the closing quote
		// whatever it holds, so `"a+` binds as "a".
		if len(s) < 2 || s[0] != '"' {
			return false
		}
		lit := []byte(s)
		lit[len(lit)-1] = '"'
		if closingQuote(lit) != len(lit)-1 {
			return false
		}
		b, _, ok := gdec.AppendString(lit[:0:0], lit, 1)
		if !ok {
			return false
		}
		*(*string)(dst) = string(b)
		return true
	case vbind.KindInt, vbind.KindInt8, vbind.KindInt16, vbind.KindInt32, vbind.KindInt64:
		return storeSigned(dst, k, s)
	case vbind.KindUint, vbind.KindUint8, vbind.KindUint16, vbind.KindUint32, vbind.KindUint64:
		return storeUnsigned(dst, k, s)
	case vbind.KindFloat32, vbind.KindFloat64:
		return storeFloat(dst, k, s)
	}
	return false
}

// closingQuote returns the index of the first unescaped quote past s[0],
// or -1. The walk is bounded by s, which carries no scan sentinel.
func closingQuote(s []byte) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '"':
			return i
		case '\\':
			i++
		}
	}
	return -1
}
