package pjson

import "unsafe"

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
// produced by ChunkManager's SIMD scan. It walks the structuralResults bitmaps
// chunk by chunk, extracting bit positions via ExtractIndices and converting
// them to raw buffer offsets.
//
// Single-buffer usage:
//
//	cm.FeedBuffer(data)
//	cm.Complete()
//	tok := NewTokenizer(cm)
//	for off := tok.Next(); off >= 0; off = tok.Next() {
//	    fmt.Printf("token at offset %d: %c\n", off, data[off])
//	}
//	// off == TokenEOF
//
// Streaming usage:
//
//	tok := NewTokenizer(cm)
//	for chunk := range chunks {
//	    cm.FeedBuffer(chunk)
//	    tok.Reload()
//	    for off := tok.Next(); off >= 0; off = tok.Next() { ... }
//	    // off == TokenDone — wait for more data
//	}
//	cm.Complete()
//	tok.Reload()
//	// tok.Next() == TokenEOF
type Tokenizer struct {
	cm       *ChunkManager
	chunkIdx int      // current chunk in cm.structuralResults
	bitIdx   int      // position within indices
	indices  []uint32 // extracted bit positions for current chunk (reused)
}

// NewTokenizer creates a Tokenizer positioned at the first token.
// The ChunkManager must have been fed a buffer via FeedBuffer before calling this.
func NewTokenizer(cm *ChunkManager) *Tokenizer {
	t := &Tokenizer{
		cm:      cm,
		indices: make([]uint32, 0, 64),
	}
	t.loadChunk()
	return t
}

// Reload resets the cursor to the beginning of the current structuralResults.
// Call this after each FeedBuffer to iterate the new buffer's tokens.
// The Tokenizer retains its indices buffer for reuse.
func (t *Tokenizer) Reload() {
	t.chunkIdx = 0
	t.bitIdx = 0
	t.indices = t.indices[:0]
	t.loadChunk()
}

// loadChunk extracts token bit positions for the current chunkIdx.
// It masks structuralResults by chunk Length to exclude zero-padded regions
// (head/tail chunks may have Length < 64; zero padding can produce spurious bits).
// Skips chunks with no tokens, advancing chunkIdx until a non-empty chunk is found
// or all chunks are exhausted.
func (t *Tokenizer) loadChunk() {
	for t.chunkIdx < len(t.cm.structuralResults) {
		bits := t.cm.structuralResults[t.chunkIdx]
		length := t.cm.chunks[t.chunkIdx].Length

		if length < ChunkSize {
			if t.cm.chunks[t.chunkIdx].IsHeadSpan {
				// Head chunk: data is right-aligned at [ChunkSize-length-1 .. ChunkSize-2].
				startBit := ChunkSize - int(length) - 1
				mask := ((uint64(1) << length) - 1) << startBit
				bits &= mask
			} else {
				// Tail chunk: data occupies [0 .. length-1].
				bits &= (uint64(1) << length) - 1
			}
		}

		t.indices = ExtractIndices(bits, t.indices[:0])
		t.bitIdx = 0
		if len(t.indices) > 0 {
			return
		}
		t.chunkIdx++
	}
}

// exhausted returns the appropriate sentinel when no more tokens remain.
func (t *Tokenizer) exhausted() int {
	if t.cm.eof {
		return TokenEOF
	}
	return TokenDone
}

// resolve converts a chunk-local bit position to an absolute offset in the
// original buffer passed to FeedBuffer.
//
// Calculation: rawBase is a sub-slice of inputBuffer; pointer arithmetic gives
// the base offset of this chunk's raw data in the original buffer.
// Adding the bit position and RawOffset gives the final offset.
func (t *Tokenizer) resolve(chunkIdx int, pos uint32) int {
	chunk := &t.cm.chunks[chunkIdx]
	bufBase := uintptr(unsafe.Pointer(&t.cm.inputBuffer[0]))
	rawBase := uintptr(unsafe.Pointer(&chunk.rawBase[0]))
	return int(rawBase-bufBase) + int(pos) + int(chunk.RawOffset)
}

// Next advances the cursor to the next token and returns its byte offset
// in the original buffer. Returns TokenDone (-1) when the current buffer's
// tokens are exhausted, or TokenEOF (-2) when Complete() has been called
// and no tokens remain.
func (t *Tokenizer) Next() int {
	if t.chunkIdx >= len(t.cm.structuralResults) {
		return t.exhausted()
	}
	pos := t.indices[t.bitIdx]
	offset := t.resolve(t.chunkIdx, pos)

	t.bitIdx++
	if t.bitIdx >= len(t.indices) {
		t.chunkIdx++
		t.loadChunk()
	}

	return offset
}

// Peek returns the byte offset of the next token without advancing the cursor.
// Returns TokenDone (-1) or TokenEOF (-2) when no more tokens are available.
func (t *Tokenizer) Peek() int {
	if t.chunkIdx >= len(t.cm.structuralResults) {
		return t.exhausted()
	}
	return t.resolve(t.chunkIdx, t.indices[t.bitIdx])
}
