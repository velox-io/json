package pjson

const (
	// TokenDone indicates the current buffer's tokens are exhausted.
	// More data may arrive via subsequent FeedBuffer calls.
	TokenDone = -1

	// TokenEOF indicates the document is finished. Returned by Next/Peek
	// only after ChunkManager.Complete() has been called and all tokens
	// in the final buffer have been consumed.
	TokenEOF = -2
)

// Tokenizer provides cursor-based iteration over all JSON structural tokens
// produced by ChunkManager's SIMD scan. All tokens are pre-flattened into
// a contiguous []uint32 of absolute byte offsets, so Next/Peek are simple
// array index operations with no per-chunk switching overhead.
type Tokenizer struct {
	cm  *ChunkManager
	idx int // current position in cm.tokenOffsets
}

// NewTokenizer creates a Tokenizer positioned at the first token.
func NewTokenizer(cm *ChunkManager) *Tokenizer {
	return &Tokenizer{cm: cm}
}

// Reload resets the cursor to the beginning.
func (t *Tokenizer) Reload() {
	t.idx = 0
}

// exhausted returns the appropriate sentinel when no more tokens remain.
func (t *Tokenizer) exhausted() int {
	if t.cm.eof {
		return TokenEOF
	}
	return TokenDone
}

// Next advances the cursor to the next token and returns its byte offset.
func (t *Tokenizer) Next() int {
	offsets := t.cm.tokenOffsets
	if t.idx >= len(offsets) {
		return t.exhausted()
	}
	off := int(offsets[t.idx])
	t.idx++
	return off
}

// NextString consumes two tokens (open and close quote) and returns both
// offsets plus whether there are escapes between them. Uses precomputed
// per-token chunk/bit info to check escape bitmaps without offset derivation.
func (t *Tokenizer) NextString() (openOff, closeOff int, hasEscape bool) {
	offsets := t.cm.tokenOffsets
	if t.idx >= len(offsets) {
		return t.exhausted(), 0, false
	}
	openIdx := t.idx
	openOff = int(offsets[openIdx])
	t.idx++

	if t.idx >= len(offsets) {
		return openOff, t.exhausted(), false
	}
	closeIdx := t.idx
	closeOff = int(offsets[closeIdx])
	t.idx++

	cm := t.cm
	openCI := int(cm.tokenChunkIdx[openIdx])
	openBit := uint32(cm.tokenBitPos[openIdx])
	closeCI := int(cm.tokenChunkIdx[closeIdx])
	closeBit := uint32(cm.tokenBitPos[closeIdx])

	hasEscape = hasEscapeBetween(cm.escapedResults, openCI, openBit, closeCI, closeBit)
	return
}

// hasEscapeBetween checks if any escaped character exists between two token
// positions (both exclusive). Uses escapedResults bitmaps directly.
func hasEscapeBetween(escaped []uint64, openCI int, openBit uint32, closeCI int, closeBit uint32) bool {
	if openCI == closeCI {
		lo := openBit + 1
		if lo >= closeBit {
			return false
		}
		rangeMask := ((uint64(1) << (closeBit - lo)) - 1) << lo
		return (escaped[openCI] & rangeMask) != 0
	}

	// Tail of open chunk
	lo := openBit + 1
	if lo < 64 {
		tailMask := ^((uint64(1) << lo) - 1)
		if escaped[openCI]&tailMask != 0 {
			return true
		}
	}

	// Full middle chunks
	for ci := openCI + 1; ci < closeCI; ci++ {
		if escaped[ci] != 0 {
			return true
		}
	}

	// Head of close chunk
	if closeBit > 0 {
		headMask := (uint64(1) << closeBit) - 1
		if escaped[closeCI]&headMask != 0 {
			return true
		}
	}

	return false
}

// Peek returns the byte offset of the next token without advancing the cursor.
func (t *Tokenizer) Peek() int {
	offsets := t.cm.tokenOffsets
	if t.idx >= len(offsets) {
		return t.exhausted()
	}
	return int(offsets[t.idx])
}
