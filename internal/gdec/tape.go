package gdec

import (
	"math"
	"strconv"
	"unsafe"
)

// Tape tags, mirroring core/tape.h and internal/valueabi.
const (
	tagArrBeg  = uint64('[') << 56
	tagArrEnd  = uint64(']') << 56
	tagObjBeg  = uint64('{') << 56
	tagObjEnd  = uint64('}') << 56
	tagString  = uint64('"') << 56
	tagStrRaw  = uint64('R') << 56
	tagStrFree = uint64('S') << 56
	tagInt64   = uint64('l') << 56
	tagUint64  = uint64('u') << 56
	tagDouble  = uint64('d') << 56
	tagNumRaw  = uint64('D') << 56
	tagTrue    = uint64('t') << 56
	tagFalse   = uint64('f') << 56
	tagNull    = uint64('n') << 56

	payloadMask = 0x00FFFFFFFFFFFFFF
	maxSpan     = 0xFFFFFF // 24-bit length field
)

// maxDepth is the container nesting limit shared with the native walkers:
// an open at depth maxDepth fails, and an empty container never opens one.
const maxDepth = 256

// TapeNeed is the counted tape-word bound for a scan of n structurals, the
// same formula the native counted entry publishes.
func TapeNeed(n, scalars int) int { return n + scalars + 3 }

type openCtn struct {
	tidx  uint32
	count uint32 // bit 31: is_array
}

// walker replays dom_build_tape_impl / ndec_valid_walk_impl over a
// structural index. A nil tape selects the validation walk.
type walker struct {
	src  []byte
	idx  []uint32
	ip   int
	tape []uint64
	tp   int
	str  []byte
	sp   int
	zc   bool

	stack [maxDepth]openCtn
}

func (w *walker) at(i int) byte { return At(w.src, i) }

func (w *walker) off(k int) int {
	if k < len(w.idx) {
		return int(w.idx[k])
	}
	return len(w.src)
}

func (w *walker) peek() byte { return w.at(w.off(w.ip)) }

func (w *walker) adv() int {
	o := w.off(w.ip)
	w.ip++
	return o
}

func (w *walker) put(word uint64) {
	w.tape[w.tp] = word
	w.tp++
}

// BuildTape walks the first n entries of idx (plus the scanner's sentinels)
// over src and writes the DOM tape and string arena. zc aliases escape-free
// strings as 'R' words against src. It returns the tape words and arena
// bytes written; ok is false on any grammar or payload-width failure.
func BuildTape(src []byte, idx []uint32, n int, tape []uint64, str []byte, zc bool) (tapeLen, strUsed int, ok bool) {
	w := walker{src: src, idx: idx[:min(n+3, len(idx))], tape: tape, str: str, zc: zc}
	if !w.run(n) {
		return 0, 0, false
	}
	return w.tp, w.sp, true
}

// ValidWalk checks the complete grammar over a structural index without
// writing a tape.
func ValidWalk(src []byte, idx []uint32, n int) bool {
	w := walker{src: src, idx: idx[:min(n+3, len(idx))]}
	return w.run(n)
}

const (
	stObjBegin = iota
	stObjField
	stObjCont
	stArrBegin
	stArrValue
	stArrCont
	stScopeEnd
	stRootScalar
	stDocEnd
)

func (w *walker) emitEmpty(beg, end uint64) {
	if w.tape == nil {
		return
	}
	s := uint64(w.tp)
	w.put(beg | (s + 1))
	w.put(end | s)
}

func (w *walker) run(n int) bool {
	if n == 0 {
		return false
	}
	depth := -1
	var curCnt, curTidx uint32

	push := func(isArr bool) bool {
		if depth >= 0 {
			w.stack[depth] = openCtn{curTidx, curCnt}
		}
		nd := depth + 1
		if nd >= maxDepth {
			return false
		}
		depth = nd
		curTidx = uint32(w.tp)
		curCnt = 0
		if isArr {
			curCnt = 1 << 31
		}
		if w.tape != nil {
			w.tp++ // reserved, patched on close
		}
		return true
	}
	pop := func(beg, end uint64) {
		if w.tape != nil {
			s := curTidx
			c := curCnt & 0x7FFFFFFF
			if c > maxSpan {
				c = maxSpan
			}
			e := uint64(w.tp)
			w.tape[s] = beg | e | uint64(c)<<32
			w.put(end | uint64(s))
		}
		depth--
		if depth >= 0 {
			curTidx = w.stack[depth].tidx
			curCnt = w.stack[depth].count
		}
	}
	key := func() bool {
		k := w.adv()
		if w.at(k) != '"' {
			return false
		}
		if !w.visitString(k) {
			return false
		}
		return w.at(w.adv()) == ':'
	}
	// value handles one value in field or element position; it reports the
	// next state, or -1 on failure.
	value := func(cont int) int {
		switch w.peek() {
		case '{':
			w.ip++
			if w.peek() != '}' {
				return stObjBegin
			}
			w.ip++
			w.emitEmpty(tagObjBeg, tagObjEnd)
		case '[':
			w.ip++
			if w.peek() != ']' {
				return stArrBegin
			}
			w.ip++
			w.emitEmpty(tagArrBeg, tagArrEnd)
		default:
			if !w.visitPrimitive() {
				return -1
			}
		}
		return cont
	}

	var st int
	switch w.peek() {
	case '{':
		w.ip++
		if w.peek() == '}' {
			w.ip++
			w.emitEmpty(tagObjBeg, tagObjEnd)
			st = stDocEnd
		} else {
			st = stObjBegin
		}
	case '[':
		w.ip++
		if w.peek() == ']' {
			w.ip++
			w.emitEmpty(tagArrBeg, tagArrEnd)
			st = stDocEnd
		} else {
			st = stArrBegin
		}
	default:
		st = stRootScalar
	}

	for {
		switch st {
		case stObjBegin:
			if !push(false) {
				return false
			}
			curCnt++
			if !key() {
				return false
			}
			st = stObjField
		case stObjField:
			if st = value(stObjCont); st < 0 {
				return false
			}
		case stObjCont:
			switch w.at(w.adv()) {
			case ',':
				curCnt++
				if !key() {
					return false
				}
				st = stObjField
			case '}':
				pop(tagObjBeg, tagObjEnd)
				st = stScopeEnd
			case 0x20:
				st = stDocEnd
			default:
				return false
			}
		case stArrBegin:
			if !push(true) {
				return false
			}
			curCnt++
			st = stArrValue
		case stArrValue:
			if st = value(stArrCont); st < 0 {
				return false
			}
		case stArrCont:
			switch w.at(w.adv()) {
			case ',':
				curCnt++
				st = stArrValue
			case ']':
				pop(tagArrBeg, tagArrEnd)
				st = stScopeEnd
			case 0x20:
				st = stDocEnd
			default:
				return false
			}
		case stScopeEnd:
			if curCnt&(1<<31) != 0 {
				st = stArrCont
			} else {
				st = stObjCont
			}
		case stRootScalar:
			if !w.visitPrimitive() {
				return false
			}
			st = stDocEnd
		case stDocEnd:
			return depth == -1 && (w.ip == n || w.ip == n+1)
		}
	}
}

func (w *walker) visitPrimitive() bool {
	o := w.adv()
	c := w.at(o)
	switch {
	case c == '"':
		return w.visitString(o)
	case c == '-' || c-'0' < 10:
		if w.tape == nil {
			_, ok := ValidNumber(w.src, o)
			return ok
		}
		return w.visitNumber(o)
	case c == 't' || c == 'f' || c == 'n':
		if _, ok := ValidAtom(w.src, o); !ok {
			return false
		}
		if w.tape != nil {
			w.put(uint64(c) << 56)
		}
		return true
	}
	return false
}

// ValidAtom checks the literal starting at src[o] (whose first byte is t,
// f, or n) and its delimiter successor, as dom_validate_atom_ptr does, and
// returns the literal's end.
func ValidAtom(src []byte, o int) (int, bool) {
	var lit string
	switch At(src, o) {
	case 't':
		lit = "true"
	case 'f':
		lit = "false"
	case 'n':
		lit = "null"
	default:
		return o, false
	}
	end := o + len(lit)
	if end > len(src) || string(src[o:end]) != lit {
		return o, false
	}
	return end, !IsNonDelim(At(src, end))
}

func (w *walker) visitString(open int) bool {
	if w.tape == nil {
		_, ok := ScanString(w.src, open+1)
		return ok
	}
	if w.zc {
		body := open + 1
		if end := QuoteOrEscape(w.src, body); w.src[end] == '"' {
			n := end - body
			if n > maxSpan {
				return false
			}
			w.put(tagStrRaw | uint64(body) | uint64(n)<<32)
			return true
		}
		n, _, ok := DecodeString(w.src, body, w.str[w.sp:])
		if !ok || n > maxSpan {
			return false
		}
		w.put(tagString | uint64(w.sp) | uint64(n)<<32)
		w.sp += n + 1
		return true
	}
	n, esc, ok := DecodeString(w.src, open+1, w.str[w.sp:])
	if !ok || n > maxSpan {
		return false
	}
	tag := tagStrFree
	if esc {
		tag = tagString
	}
	w.put(tag | uint64(w.sp) | uint64(n)<<32)
	w.sp += n + 1
	return true
}

func (w *walker) storeText(tag uint64, from, to int) bool {
	n := to - from
	if n > maxSpan || w.sp > math.MaxUint32 {
		return false
	}
	copy(w.str[w.sp:], w.src[from:to])
	w.str[w.sp+n] = 0
	w.put(tag | uint64(w.sp) | uint64(n)<<32)
	w.sp += n + 1
	return true
}

// visitNumber replays dom_visit_number: exact integers as l/u, ordinary
// reals as d with their text retained, and text-only forms as D.
func (w *walker) visitNumber(o int) bool {
	p := o
	neg := w.at(p) == '-'
	if neg {
		p++
	}
	sd := p
	var i uint64
	for IsDigit(w.at(p)) {
		i = 10*i + uint64(w.at(p)-'0')
		p++
	}
	dc := p - sd
	if dc == 0 || (w.at(sd) == '0' && dc > 1) {
		return false
	}
	var exp int64
	real := false
	if w.at(p) == '.' {
		real = true
		p++
		first := p
		for IsDigit(w.at(p)) {
			i = 10*i + uint64(w.at(p)-'0')
			p++
		}
		if p == first {
			return false
		}
		exp = int64(first - p)
		dc = p - sd
	}
	if c := w.at(p); c == 'e' || c == 'E' {
		real = true
		p++
		var ok bool
		if p, ok = parseExponent(w.src, p, &exp); !ok {
			return false
		}
	}
	dirty := IsNonDelim(w.at(p))
	if real {
		if dc > 19 {
			s := sd
			for c := w.at(s); c == '0' || c == '.'; c = w.at(s) {
				s++
			}
			if dc-(s-sd) > 19 {
				return !dirty && w.storeText(tagNumRaw, o, p)
			}
		}
		if exp > 308 && i != 0 {
			return !dirty && w.storeText(tagNumRaw, o, p)
		}
		if dirty {
			return false
		}
		dv := realValue(w.src[o:p], i, exp, neg)
		if !w.storeText(tagDouble, o, p) {
			return false
		}
		w.put(math.Float64bits(dv))
		return true
	}

	longest := 20
	if neg {
		longest = 19
	}
	if dc > longest {
		return !dirty && w.storeText(tagNumRaw, o, p)
	}
	if dc == longest {
		if neg {
			if i > 1<<63 {
				return !dirty && w.storeText(tagNumRaw, o, p)
			}
		} else if w.at(o) != '1' || i <= math.MaxInt64 {
			return !dirty && w.storeText(tagNumRaw, o, p)
		}
	}
	if dirty {
		return false
	}
	if i > math.MaxInt64 && !neg {
		w.put(tagUint64)
		w.put(i)
	} else {
		v := i
		if neg {
			v = ^i + 1
		}
		w.put(tagInt64)
		w.put(v)
	}
	return true
}

// parseExponent reads the exponent after 'e', clamping more than 18
// significant digits the way dom_parse_exponent does.
func parseExponent(src []byte, p int, exp *int64) (int, bool) {
	negE := At(src, p) == '-'
	if negE || At(src, p) == '+' {
		p++
	}
	se := p
	var en uint64
	for IsDigit(At(src, p)) {
		en = 10*en + uint64(At(src, p)-'0')
		p++
	}
	if p == se {
		return p, false
	}
	if p > se+18 {
		for At(src, se) == '0' {
			se++
		}
		if p > se+18 {
			en = 999999999999999999
		}
	}
	if negE {
		*exp -= int64(en)
	} else {
		*exp += int64(en)
	}
	return p, true
}

// realValue is the binary64 a validated real token names: the exponent
// range gates of dom_visit_number, then the correctly rounded conversion.
func realValue(text []byte, i uint64, exp int64, neg bool) float64 {
	if i == 0 || exp < -342 || exp > 308 {
		if neg {
			return math.Copysign(0, -1)
		}
		return 0
	}
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(text), len(text)), 64)
	return f
}
