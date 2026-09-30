package gbind

import (
	"encoding/binary"
	"math/bits"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/vbind"
)

// keyTable maps a struct's JSON keys to field indexes relative to its first
// field, through the vlib lookup blob the TypeTree attaches, the same bytes
// the native binder reads. A member tries its predicted field first: byIdx
// holds each field's key by index, so a predicted key of at most seven
// printable bytes matches its quoted pattern in one word, and a longer
// printable one compares in place before its closing quote. A mispredicted
// member then consults the binder's transition memo, the TypeTree's KeyMemo
// row, before the blob resolves it. byIdx's last entry is the one-past
// sentinel, the prediction after the last field, which matches no key.
type keyTable struct {
	blob  unsafe.Pointer
	byIdx []fieldKey
	// memoRow is the struct's row in the binder's key memo, the same
	// TypeMeta layout the native Parser's memo reads; 0 means none.
	memoRow uint16
	// fields holds, by field index, what the struct walk binds a member
	// with, so a hit dispatches without reaching back into the TypeTree.
	fields []fieldPlan
}

// fieldPlan is one field as the struct walk binds it.
type fieldPlan struct {
	f    *vbind.BindField
	off  uintptr
	ti   uint32
	kind vbind.Kind
	// plain marks a field bound straight at host+off: no `,string` and no
	// embedded pointer hop.
	plain bool
}

// fieldKey is one field's key for prediction: probe words for a source-span
// compare, and the quoted pattern, the key's bytes and closing quote under
// mask, which a member's first word matches directly. mask is zero for a
// key longer than seven bytes or one holding a byte the strict body policy
// judges. span marks a printable key of at least eight bytes, which a
// member matches by its words in place and the closing quote after them.
type fieldKey struct {
	w0, w1    uint64
	n         int
	key       string
	pat, mask uint64
	span      bool
}

// printable reports whether key holds only printable ASCII bytes other than
// a quote or backslash: a body of them is its own key and passes the strict
// body policy.
func printable(key string) bool {
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// quotedPattern returns the pattern of a printable key of at most seven
// bytes and its closing quote.
func quotedPattern(key string) (pat, mask uint64) {
	if len(key) > 7 || !printable(key) {
		return 0, 0
	}
	var b [8]byte
	copy(b[:], key)
	b[len(key)] = '"'
	return binary.LittleEndian.Uint64(b[:]), 1<<(8*(len(key)+1)) - 1
}

// keyWords returns the probe words of key: its first eight bytes, zero
// padded, and its last eight when it is longer.
func keyWords(key string) (w0, w1 uint64) {
	var b [8]byte
	copy(b[:], key)
	w0 = binary.LittleEndian.Uint64(b[:])
	if len(key) > 8 {
		w1 = binary.LittleEndian.Uint64([]byte(key[len(key)-8:]))
	}
	return w0, w1
}

// newKeyTable plans the lookup over names against blob, the lookup the
// TypeTree attached for the struct. Every name is unique, non-empty, and
// free of quote and backslash: the typ name rules cancel same-name fields
// and drop empty ones, and the blob's builder rejects the rest, so a struct
// whose names broke any never reaches a TypeTree. That is what lets byIdx
// hold every name unfiltered.
func newKeyTable(blob unsafe.Pointer, names []string) *keyTable {
	t := &keyTable{blob: blob, byIdx: make([]fieldKey, len(names)+1)}
	for i, name := range names {
		t.byIdx[i].w0, t.byIdx[i].w1 = keyWords(name)
		t.byIdx[i].n = len(name)
		t.byIdx[i].key = name
		t.byIdx[i].pat, t.byIdx[i].mask = quotedPattern(name)
		t.byIdx[i].span = len(name) >= 8 && printable(name)
	}
	// The one-past sentinel, the prediction after the last field, matches
	// no key: n is negative where every name's is not, so each compare that
	// begins with the length rejects it.
	t.byIdx[len(names)].n = -1
	return t
}

// keyAt resolves the key whose quote is at pos when it carries no escape,
// trying field next first, and returns its closing quote. A mispredicted
// member consults the transition memo through matchSpan before the blob.
// ok is false for a key with an escape, or one too near the end of the
// text to read by words, which the caller decodes first. Under the strict
// scan ok is false for a key body that violates the body policy.
func (c *binder) keyAt(kt *keyTable, s text, pos, next int) (idx, end int, ok bool) {
	body := pos + 1
	if body+8 > s.n {
		return 0, 0, false
	}
	w0 := s.word(body)
	h := &kt.byIdx[next]
	if h.mask != 0 && w0&h.mask == h.pat {
		return next, body + h.n, true
	}
	if h.span && h.spanAt(w0, s, body) {
		return next, body + h.n, true
	}
	if m := gdec.QuoteOrEscapeMask(w0); m != 0 {
		n := bits.TrailingZeros64(m) >> 3
		if s.at(body+n) != '"' {
			return 0, 0, false
		}
		if n == 0 {
			return -1, body, true
		}
		w0 &= 1<<(8*n) - 1
		if c.strict && !s.bodyOK(body, body+n) {
			return 0, 0, false
		}
		return c.matchSpan(kt, w0, 0, s, body, n, next), body + n, true
	}
	end = s.quoteOrEscape(body + 8)
	if end >= s.n || s.at(end) != '"' {
		return 0, 0, false
	}
	if c.strict && !s.bodyOK(body, end) {
		return 0, 0, false
	}
	n := end - body
	var w1 uint64
	if n > 8 {
		w1 = s.word(end - 8)
	}
	return c.matchSpan(kt, w0, w1, s, body, n, next), end, true
}

// matchString is matchSpan over a decoded key. The sentinel's n is negative,
// so no compare reaches it, and the empty key is no field's name: it falls to
// the blob's miss.
func (c *binder) matchString(kt *keyTable, key string, next int) int {
	if k := &kt.byIdx[next]; k.n == len(key) && k.key == key {
		return next
	}
	if m := c.memoRead(kt, next); m >= 0 && m != next && kt.byIdx[m].n == len(key) && kt.byIdx[m].key == key {
		return m
	}
	idx := vbind.LookupFind(kt.blob, key)
	if idx >= 0 {
		c.memoRecord(kt, next, idx)
	}
	return idx
}

// matchSpan returns the field index of the key of n bytes at body, trying
// field next first, then the field the memo holds against the prediction
// next, and the blob last; a resolution neither predicted, the memo
// records against next.
func (c *binder) matchSpan(kt *keyTable, w0, w1 uint64, s text, body, n, next int) int {
	if kt.hitAt(w0, w1, s, body, n, next) {
		return next
	}
	if m := c.memoRead(kt, next); m >= 0 && m != next && kt.hitAt(w0, w1, s, body, n, m) {
		return m
	}
	idx := vbind.LookupFindSpan(kt.blob, s.ptr(body), n, s.n-body)
	if idx >= 0 {
		c.memoRecord(kt, next, idx)
	}
	return idx
}

// memoRead returns the field the memo recorded against the failed
// prediction next, or -1 when the struct has no memo row or none is
// recorded.
func (c *binder) memoRead(kt *keyTable, next int) int {
	if row := int(kt.memoRow); row != 0 {
		if w := c.memo[row+next]; w != 0 {
			return int(w) - 1
		}
	}
	return -1
}

// memoRecord notes idx, a resolution, against the failed prediction next.
// The byte wraps past two hundred fifty four fields, where the memo merely
// stops helping, as a wrong field never matches.
func (c *binder) memoRecord(kt *keyTable, next, idx int) {
	if row := int(kt.memoRow); row != 0 {
		c.memo[row+next] = byte(idx + 1)
	}
}

// spanAt reports whether the member body at body, whose first word is w0,
// is the span key k: its words in place and the closing quote after them.
func (k *fieldKey) spanAt(w0 uint64, s text, body int) bool {
	end := body + k.n
	return w0 == k.w0 && end < s.n && s.at(end) == '"' && (k.n == 8 || s.word(end-8) == k.w1) &&
		(k.n <= 16 || k.key == unsafe.String(s.ptr(body), k.n))
}

// hitAt reports whether the key of n bytes at body is field m.
func (kt *keyTable) hitAt(w0, w1 uint64, s text, body, n, m int) bool {
	k := &kt.byIdx[m]
	return k.n == n && k.w0 == w0 && k.w1 == w1 && (n <= 16 || k.key == unsafe.String(s.ptr(body), n))
}
