package pjson

import (
	"strings"
	"testing"
	"unsafe"
)

// ============ Tokenizer Tests ============

func TestTokenizer_EmptyBuffer(t *testing.T) {
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(nil)
	tok := NewTokenizer(cm)

	// Without Complete(), exhaustion returns TokenDone
	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone for empty buffer, got %d", off)
	}
	if off := tok.Peek(); off != TokenDone {
		t.Fatalf("expected Peek()=TokenDone for empty buffer, got %d", off)
	}
}

func TestTokenizer_SimpleObject(t *testing.T) {
	// {"a":1}
	// 0123456
	// StructuralStart tokens: { " : 1 }
	// Positions:              0 1 4 5 6
	// Note: position 1 is opening quote (string scalar start),
	//       position 3 (closing quote) is NOT a token start.
	data := []byte(`{"a":1}`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	expected := []int{0, 1, 4, 5, 6}
	expectedChars := []byte{'{', '"', ':', '1', '}'}

	for i, want := range expected {
		off := tok.Next()
		if off != want {
			t.Fatalf("token[%d]: got offset %d, want %d", i, off, want)
		}
		if data[off] != expectedChars[i] {
			t.Errorf("token[%d]: data[%d]=%c, want %c", i, off, data[off], expectedChars[i])
		}
	}

	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone after last token, got %d (char=%c)", off, data[off])
	}
}

func TestTokenizer_Array(t *testing.T) {
	// [1,2,3]
	// 0123456
	// Tokens: [ 1 , 2 , 3 ]
	// Positions: 0 1 2 3 4 5 6
	data := []byte(`[1,2,3]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	expected := []int{0, 1, 2, 3, 4, 5, 6}
	expectedChars := []byte{'[', '1', ',', '2', ',', '3', ']'}

	for i, want := range expected {
		off := tok.Next()
		if off != want {
			t.Fatalf("token[%d]: got offset %d, want %d", i, off, want)
		}
		if data[off] != expectedChars[i] {
			t.Errorf("token[%d]: data[%d]=%c, want %c", i, off, data[off], expectedChars[i])
		}
	}

	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone after last token, got %d", off)
	}
}

func TestTokenizer_Keywords(t *testing.T) {
	// [true,false,null]
	data := []byte(`[true,false,null]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// Tokens: [ true , false , null ]
	expected := []struct {
		off  int
		char byte
	}{
		{0, '['},
		{1, 't'},   // true start
		{5, ','},
		{6, 'f'},   // false start
		{11, ','},
		{12, 'n'},  // null start
		{16, ']'},
	}

	for i, want := range expected {
		off := tok.Next()
		if off != want.off {
			t.Fatalf("token[%d]: got offset %d, want %d", i, off, want.off)
		}
		if data[off] != want.char {
			t.Errorf("token[%d]: data[%d]=%c, want %c", i, off, data[off], want.char)
		}
	}

	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone after last token, got %d", off)
	}
}

func TestTokenizer_PeekDoesNotAdvance(t *testing.T) {
	data := []byte(`[1,2]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// Peek multiple times — should always return the same offset
	for range 5 {
		off := tok.Peek()
		if off != 0 {
			t.Fatalf("Peek() = %d, want 0", off)
		}
	}

	// Now consume
	off := tok.Next()
	if off != 0 {
		t.Fatalf("Next() = %d, want 0", off)
	}

	// Peek should now return the next token
	off = tok.Peek()
	if off != 1 {
		t.Fatalf("Peek() after Next() = %d, want 1", off)
	}
}

func TestTokenizer_PeekEqualsNext(t *testing.T) {
	data := []byte(`{"key":"val"}`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	for {
		p := tok.Peek()
		n := tok.Next()
		if p != n {
			t.Fatalf("Peek()=%d != Next()=%d", p, n)
		}
		if n < 0 {
			break
		}
	}
}

func TestTokenizer_MultiChunk(t *testing.T) {
	// Build a JSON array large enough to span multiple 64-byte chunks.
	// [1,2,3,...,N]
	// Each element "N," is ~2-4 bytes. Use enough to exceed 128 bytes (2+ chunks).
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < 100; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte(byte('0' + i%10))
	}
	sb.WriteByte(']')
	data := []byte(sb.String())

	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)

	if len(cm.chunks) < 2 {
		t.Fatalf("expected multi-chunk, got %d chunks for %d bytes", len(cm.chunks), len(data))
	}

	tok := NewTokenizer(cm)

	// Collect all token offsets
	var offsets []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		if off < 0 || off >= len(data) {
			t.Fatalf("offset %d out of range [0, %d)", off, len(data))
		}
		offsets = append(offsets, off)
	}

	// We expect: [ + 100 digits + 99 commas + ] = 201 tokens
	wantTokens := 1 + 100 + 99 + 1
	if len(offsets) != wantTokens {
		t.Fatalf("expected %d tokens, got %d", wantTokens, len(offsets))
	}

	// Verify first token is '[' and last is ']'
	if data[offsets[0]] != '[' {
		t.Errorf("first token: data[%d]=%c, want '['", offsets[0], data[offsets[0]])
	}
	if data[offsets[len(offsets)-1]] != ']' {
		t.Errorf("last token: data[%d]=%c, want ']'", offsets[len(offsets)-1], data[offsets[len(offsets)-1]])
	}

	// Verify offsets are strictly ascending
	for i := 1; i < len(offsets); i++ {
		if offsets[i] <= offsets[i-1] {
			t.Fatalf("non-ascending: offsets[%d]=%d <= offsets[%d]=%d",
				i, offsets[i], i-1, offsets[i-1])
		}
	}
}

func TestTokenizer_HeadAlignment(t *testing.T) {
	// Create a misaligned buffer to force a head chunk.
	// Allocate aligned + 1 offset to guarantee misalignment.
	raw := make([]byte, 256)
	addr := uintptr(unsafe.Pointer(&raw[0]))
	offset := int(ChunkAlignSize - (addr % ChunkAlignSize))
	if offset == ChunkAlignSize {
		offset = 0
	}
	// Shift by 1 to guarantee misalignment
	offset = (offset + 1) % ChunkAlignSize
	if offset == 0 {
		offset = 1
	}

	buf := raw[offset:]
	data := []byte(`{"x":2}`)
	copy(buf, data)
	buf = buf[:len(data)]

	// Verify misalignment
	bufAddr := uintptr(unsafe.Pointer(&buf[0]))
	if bufAddr%ChunkAlignSize == 0 {
		t.Skip("could not create misaligned buffer")
	}

	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(buf)

	// Should have a head chunk
	if len(cm.chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	if !cm.chunks[0].IsHeadSpan {
		t.Log("warning: no head chunk produced (buffer may be short enough for single tail)")
	}

	tok := NewTokenizer(cm)

	// {"x":2}
	// 0123456
	// Expected tokens: { " : 2 }  at offsets 0 1 4 5 6
	expected := []struct {
		off  int
		char byte
	}{
		{0, '{'},
		{1, '"'},
		{4, ':'},
		{5, '2'},
		{6, '}'},
	}

	for i, want := range expected {
		off := tok.Next()
		if off != want.off {
			t.Fatalf("token[%d]: got offset %d, want %d", i, off, want.off)
		}
		if buf[off] != want.char {
			t.Errorf("token[%d]: buf[%d]=%c, want %c", i, off, buf[off], want.char)
		}
	}

	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone after last token, got %d", off)
	}
}

func TestTokenizer_LargeNestedJSON(t *testing.T) {
	// A more realistic nested structure spanning multiple chunks.
	data := []byte(`{"users":[{"name":"Alice","age":30},{"name":"Bob","age":25}],"total":2}`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// Collect all offsets and verify they're valid
	var offsets []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		if off < 0 || off >= len(data) {
			t.Fatalf("offset %d out of range", off)
		}
		offsets = append(offsets, off)
	}

	// Verify ascending
	for i := 1; i < len(offsets); i++ {
		if offsets[i] <= offsets[i-1] {
			t.Fatalf("non-ascending at %d: %d <= %d", i, offsets[i], offsets[i-1])
		}
	}

	// First should be '{', last should be '}'
	if data[offsets[0]] != '{' {
		t.Errorf("first token: %c, want '{'", data[offsets[0]])
	}
	if data[offsets[len(offsets)-1]] != '}' {
		t.Errorf("last token: %c, want '}'", data[offsets[len(offsets)-1]])
	}
}

// ============ EOF / Done / Reload Tests ============

func TestTokenizer_Done_WithoutComplete(t *testing.T) {
	data := []byte(`[1]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// Consume all tokens
	for tok.Next() >= 0 {
	}

	// Without Complete(), should return TokenDone
	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone, got %d", off)
	}
	if off := tok.Peek(); off != TokenDone {
		t.Fatalf("expected Peek()=TokenDone, got %d", off)
	}
}

func TestTokenizer_EOF_WithComplete(t *testing.T) {
	data := []byte(`[1]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	cm.Complete()
	tok := NewTokenizer(cm)

	// Consume all tokens
	for tok.Next() >= 0 {
	}

	// With Complete(), should return TokenEOF
	if off := tok.Next(); off != TokenEOF {
		t.Fatalf("expected TokenEOF, got %d", off)
	}
	if off := tok.Peek(); off != TokenEOF {
		t.Fatalf("expected Peek()=TokenEOF, got %d", off)
	}
}

func TestTokenizer_CompleteAfterExhaustion(t *testing.T) {
	// Complete() can be called after tokens are already exhausted.
	data := []byte(`{"a":1}`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// Exhaust tokens
	for tok.Next() >= 0 {
	}

	// Still TokenDone
	if off := tok.Peek(); off != TokenDone {
		t.Fatalf("expected TokenDone before Complete, got %d", off)
	}

	// Now complete
	cm.Complete()

	// Should switch to TokenEOF
	if off := tok.Peek(); off != TokenEOF {
		t.Fatalf("expected TokenEOF after Complete, got %d", off)
	}
	if off := tok.Next(); off != TokenEOF {
		t.Fatalf("expected TokenEOF after Complete, got %d", off)
	}
}

func TestTokenizer_Reload_Streaming(t *testing.T) {
	// Simulate streaming: two FeedBuffer calls, same Tokenizer.
	// Use structurals at buffer boundaries to avoid cross-buffer scalar issues.
	cm := NewChunkManager(DefaultScanner)

	// First buffer: {"a":
	buf1 := []byte(`{"a":`)
	cm.FeedBuffer(buf1)
	tok := NewTokenizer(cm)

	// Collect tokens from first buffer
	var offsets1 []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		offsets1 = append(offsets1, off)
	}
	if tok.Peek() != TokenDone {
		t.Fatalf("expected TokenDone after first buffer, got %d", tok.Peek())
	}

	// First buffer tokens: { " :
	if len(offsets1) < 2 {
		t.Fatalf("buf1: expected at least 2 tokens, got %d", len(offsets1))
	}
	if buf1[offsets1[0]] != '{' {
		t.Errorf("buf1 token[0]=%c, want '{'", buf1[offsets1[0]])
	}

	// Second buffer: 1}
	buf2 := []byte(`1}`)
	cm.FeedBuffer(buf2)
	tok.Reload()

	// Collect tokens from second buffer
	var offsets2 []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		offsets2 = append(offsets2, off)
	}

	// Verify second buffer has tokens ending with }
	if len(offsets2) == 0 {
		t.Fatal("buf2: expected tokens, got none")
	}
	lastOff := offsets2[len(offsets2)-1]
	if buf2[lastOff] != '}' {
		t.Errorf("buf2 last token=%c, want '}'", buf2[lastOff])
	}

	// Still TokenDone
	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone, got %d", off)
	}

	// Complete and verify EOF
	cm.Complete()
	if off := tok.Next(); off != TokenEOF {
		t.Fatalf("expected TokenEOF after Complete, got %d", off)
	}
}

func TestTokenizer_Reload_SameBuffer(t *testing.T) {
	// Reload without new FeedBuffer re-iterates the same tokens.
	data := []byte(`[1,2]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	tok := NewTokenizer(cm)

	// First pass
	var pass1 []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		pass1 = append(pass1, off)
	}

	// Reload and iterate again
	tok.Reload()
	var pass2 []int
	for off := tok.Next(); off >= 0; off = tok.Next() {
		pass2 = append(pass2, off)
	}

	if len(pass1) != len(pass2) {
		t.Fatalf("pass1 has %d tokens, pass2 has %d", len(pass1), len(pass2))
	}
	for i := range pass1 {
		if pass1[i] != pass2[i] {
			t.Errorf("token[%d]: pass1=%d, pass2=%d", i, pass1[i], pass2[i])
		}
	}
}

func TestTokenizer_Reset_ClearsEOF(t *testing.T) {
	data := []byte(`[1]`)
	cm := NewChunkManager(DefaultScanner)
	cm.FeedBuffer(data)
	cm.Complete()
	tok := NewTokenizer(cm)

	for tok.Next() >= 0 {
	}
	if off := tok.Next(); off != TokenEOF {
		t.Fatalf("expected TokenEOF, got %d", off)
	}

	// Reset clears eof
	cm.Reset()
	if off := tok.Next(); off != TokenDone {
		t.Fatalf("expected TokenDone after Reset, got %d", off)
	}
}

// ============ Tokenizer Benchmarks ============

func BenchmarkTokenizer_SmallJSON(b *testing.B) {
	data := []byte(`{"key":"value","num":42,"arr":[1,2,3]}`)
	cm := NewChunkManager(DefaultScanner)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.FeedBuffer(data)
		tok := NewTokenizer(cm)
		for tok.Next() >= 0 {
		}
	}
}

func BenchmarkTokenizer_LargeArray(b *testing.B) {
	// Build ~4KB JSON array
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < 1000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte(byte('0' + i%10))
	}
	sb.WriteByte(']')
	data := []byte(sb.String())

	cm := NewChunkManager(DefaultScanner)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.FeedBuffer(data)
		tok := NewTokenizer(cm)
		for tok.Next() >= 0 {
		}
	}
}
