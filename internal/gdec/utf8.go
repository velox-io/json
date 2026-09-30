package gdec

import (
	"encoding/binary"
	"math/bits"
	"unsafe"
)

// Strict body policy for string spans, shared by the binder walk. A body is
// valid when it holds no raw byte below 0x20 and its raw bytes are
// well-formed UTF-8, the verdicts utf8.Valid and the native scan reach.
// Escape bytes pass unchecked: escape validity is decode policy. Loads run
// unchecked off the base pointer: every offset the loops reach sits inside
// the span a guard proved.

// hasLess flags the bytes of the little-endian word w below n in their high
// bits. n stays below 0x80, so a byte at or above 0x80 never flags: the
// subtraction cannot wrap past it without its own high bit set, which &^ w
// clears. Borrow from a lower byte flags only words that already hold one.
func hasLess(w uint64, n byte) uint64 {
	return (w - lsb*uint64(n)) &^ w & msb
}

// utf8Lead packs what a lead byte starts: its sequence length, 0 for a
// continuation byte and for the leads no valid encoding starts (C0, C1,
// F5..FF), and the second byte's accepted range. E0 and F0 cut overlongs,
// ED cuts surrogates, F4 cuts codepoints past U+10FFFF.
var utf8Lead = func() (t [256]uint32) {
	lead := func(b byte, n uint8, lo, hi byte) {
		t[b] = uint32(n) | uint32(lo)<<8 | uint32(hi)<<16
	}
	for b := 0xC2; b <= 0xDF; b++ {
		lead(byte(b), 2, 0x80, 0xBF)
	}
	for b := 0xE1; b <= 0xEC; b++ {
		lead(byte(b), 3, 0x80, 0xBF)
	}
	for b := 0xEE; b <= 0xEF; b++ {
		lead(byte(b), 3, 0x80, 0xBF)
	}
	for b := 0xF1; b <= 0xF3; b++ {
		lead(byte(b), 4, 0x80, 0xBF)
	}
	lead(0xE0, 3, 0xA0, 0xBF)
	lead(0xED, 3, 0x80, 0x9F)
	lead(0xF0, 4, 0x90, 0xBF)
	lead(0xF4, 4, 0x80, 0x8F)
	return t
}()

// at reads src[i] without a bounds check. The caller proves i in range.
func at(p unsafe.Pointer, i int) byte { return *(*byte)(unsafe.Add(p, i)) }

// wordAt reads the little-endian word at src[i], i+8 <= len(src), without a
// bounds check.
func wordAt(p unsafe.Pointer, i int) uint64 {
	return binary.LittleEndian.Uint64((*[8]byte)(unsafe.Add(p, i))[:])
}

// validRun validates the multibyte sequences from src[i] while the bytes
// stay at or above 0x80 and returns the index of the first byte below it,
// which proves nothing about that byte. A malformed or truncated sequence
// returns -1. src ends where the run must: a span end always sits on an
// ASCII byte, so a sequence past it is malformed anyway. The loop advances
// the base pointer and folds each length's checks into one condition, the
// shape std's utf8.Valid takes; an indexed loop thrashes on runs that mix
// sequence lengths.
func validRun(src []byte, i int) int {
	p := src[i:]
	for len(p) > 0 {
		p0 := p[0]
		if p0 < 0x80 {
			return i
		}
		lead := utf8Lead[p0]
		sz, lo, hi := byte(lead), byte(lead>>8), byte(lead>>16)
		switch sz {
		case 2:
			if len(p) < 2 || p[1] < lo || hi < p[1] {
				return -1
			}
			p = p[2:]
		case 3:
			if len(p) < 3 || p[1] < lo || hi < p[1] || p[2] < 0x80 || 0xBF < p[2] {
				return -1
			}
			p = p[3:]
		case 4:
			if len(p) < 4 || p[1] < lo || hi < p[1] || p[2] < 0x80 || 0xBF < p[2] || p[3] < 0x80 || 0xBF < p[3] {
				return -1
			}
			p = p[4:]
		default:
			return -1
		}
		i += int(sz)
	}
	return i
}

// ValidateBody reports whether src[i:j] holds the strict body policy: no
// raw byte below 0x20 and well-formed UTF-8. Words of plain ASCII pass two
// gates; a high byte drops to a scalar run over the sequences around it.
func ValidateBody(src []byte, i, j int) bool {
	p, n := unsafe.Pointer(unsafe.SliceData(src)), j
	sub := src[:j]
	for k := i; k+8 <= n; {
		w := wordAt(p, k)
		if hasLess(w, 0x20) != 0 {
			return false
		}
		if w&msb == 0 {
			k += 8
			continue
		}
		if k = validRun(sub, k+bits.TrailingZeros64(w&msb)>>3); k < 0 {
			return false
		}
	}
	for k := i; k < n; {
		switch c := at(p, k); {
		case c < 0x20:
			return false
		case c < 0x80:
			k++
		default:
			if k = validRun(sub, k); k < 0 {
				return false
			}
		}
	}
	return true
}

// QuoteOrEscapeBody is QuoteOrEscape over a string body under the strict
// scan: the span it walks past before the stop holds the strict body
// policy, whose violation it reports as a negative stop. Stops and their
// spurious-mask company behave exactly as QuoteOrEscape returns them.
func QuoteOrEscapeBody(src []byte, i int) int {
	p, n := unsafe.Pointer(unsafe.SliceData(src)), len(src)
	for i+8 <= n {
		w := wordAt(p, i)
		if m := QuoteOrEscapeMask(w); m != 0 {
			k := i + bits.TrailingZeros64(m)>>3
			if !ValidateBody(src, i, k) {
				return -1
			}
			return k
		}
		if hasLess(w, 0x20) != 0 {
			return -1
		}
		if w&msb != 0 {
			if i = validRun(src, i+bits.TrailingZeros64(w&msb)>>3); i < 0 {
				return -1
			}
			continue
		}
		i += 8
	}
	for i < n {
		switch c := at(p, i); {
		case c == '"' || c == '\\':
			return i
		case c < 0x20:
			return -1
		case c < 0x80:
			i++
		default:
			if i = validRun(src, i); i < 0 {
				return -1
			}
		}
	}
	return n
}
