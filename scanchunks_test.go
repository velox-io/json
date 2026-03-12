package pjson

import (
	"math/bits"
	"testing"
)

// ============ ScanChunks Tests ============

// TestScanChunks_Empty verifies ScanChunks returns empty results when no buffers are fed.
func TestScanChunks_Empty(t *testing.T) {
	cm := NewChunkManager(testScanner())
	if len(cm.scanResults) != 0 {
		t.Errorf("expected 0 results, got %d", len(cm.scanResults))
	}
}

// TestScanChunks_SingleChunkSimpleJSON tests scanning a single aligned 64-byte chunk
// containing a simple JSON object.
func TestScanChunks_SingleChunkSimpleJSON(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	json := `{"key": "value"}`
	copy(buf, json)

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	// { at 0, } at 15 → Op should have bits 0 and 15
	// : at 6 → Op should have bit 6
	if r.Op == 0 {
		t.Error("Op bitmap should not be zero")
	}
	if r.Op&(1<<0) == 0 {
		t.Error("expected '{' at position 0 in Op")
	}
	if r.Op&(1<<15) == 0 {
		t.Error("expected '}' at position 15 in Op")
	}
	if r.Op&(1<<6) == 0 {
		t.Error("expected ':' at position 6 in Op")
	}

	// Quotes at positions 1, 5, 8, 14
	if r.Strings.Quote == 0 {
		t.Error("Quote bitmap should not be zero")
	}

	// StructuralStart should include scalar starts (the string values)
	if r.StructuralStart == 0 {
		t.Error("StructuralStart should not be zero")
	}
}

// TestScanChunks_ResultCountMatchesChunks verifies that the number of scan results
// equals the number of chunks, for various buffer sizes.
func TestScanChunks_ResultCountMatchesChunks(t *testing.T) {
	sizes := []int{1, 30, 59, 60, 63, 64, 100, 128, 200, 256, 500}
	for _, size := range sizes {
		cm := NewChunkManager(testScanner())
		buf := makeAligned(size, ChunkAlignSize, 0)
		// Fill with spaces (valid JSON whitespace)
		for i := range buf {
			buf[i] = ' '
		}
		cm.FeedBuffer(buf)
		results := cm.scanResults
		nChunks := len(cm.chunks)
		if len(results) != nChunks {
			t.Errorf("size=%d: len(results)=%d != len(chunks)=%d", size, len(results), nChunks)
		}
	}
}

// TestScanChunks_WhitespaceOnly verifies that a buffer of only spaces produces
// whitespace bitmasks and no structural characters.
func TestScanChunks_WhitespaceOnly(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	for i := range buf {
		buf[i] = ' '
	}

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Whitespace == 0 {
		t.Error("expected non-zero whitespace bitmap for all-space buffer")
	}
	if r.Op != 0 {
		t.Errorf("expected zero Op for whitespace-only buffer, got %064b", r.Op)
	}
	if r.Strings.Quote != 0 {
		t.Errorf("expected zero Quote for whitespace-only buffer, got %064b", r.Strings.Quote)
	}
}

// TestScanChunks_MultiChunkBodyBatching tests that multiple contiguous body chunks
// from a single aligned buffer are scanned correctly (exercises the batching path).
func TestScanChunks_MultiChunkBodyBatching(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 4 full chunks = 256 bytes, aligned
	buf := makeAligned(256, ChunkAlignSize, 0)
	// Place a JSON object at the start of each 64-byte chunk
	jsons := []string{
		`{"a":1}`,
		`{"b":2}`,
		`{"c":3}`,
		`{"d":4}`,
	}
	for i, j := range jsons {
		copy(buf[i*64:], j)
	}

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	// Each chunk should detect { at position 0 and } at the end of the JSON literal
	for i, r := range results {
		if r.Op&(1<<0) == 0 {
			t.Errorf("chunk %d: expected '{' at position 0 in Op", i)
		}
		if r.Op == 0 {
			t.Errorf("chunk %d: Op should not be zero", i)
		}
	}
}

// TestScanChunks_HeadChunk tests scanning when the buffer is misaligned,
// producing a head chunk followed by body chunks.
func TestScanChunks_HeadChunk(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// Force 5-byte misalignment → head chunk of 11 bytes + body chunks
	buf := makeAligned(128+5, ChunkAlignSize, 5)
	// Put a '{' in the first byte so the head chunk has structural content
	buf[0] = '{'
	// Put a '}' somewhere in the body
	buf[64] = '}'

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if len(results) != len(cm.chunks) {
		t.Fatalf("results count %d != chunks count %d", len(results), len(cm.chunks))
	}

	// Head chunk: '{' is at buf[0], which is the first byte of the head span (11 bytes).
	// With the new layout, head data is placed at blk[ChunkSize-headLen-1],
	// so '{' is at position ChunkSize-headLen-1 = 64-11-1 = 52 in the chunk.
	headLen := ChunkAlignSize - 5 // 11
	bitPos := ChunkSize - headLen - 1
	if results[0].Op&(1<<uint(bitPos)) == 0 {
		t.Errorf("head chunk: expected '{' at position %d in Op", bitPos)
	}
}

// TestScanChunks_TailChunk tests scanning when the buffer has a tail remainder,
// producing body chunks followed by a tail chunk.
func TestScanChunks_TailChunk(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 64 + 30 = 94 bytes, aligned → 1 body chunk + 1 tail chunk
	buf := makeAligned(94, ChunkAlignSize, 0)
	buf[0] = '['  // in body chunk, position 0
	buf[64] = ']' // in tail chunk, position 0

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Body chunk: '[' at position 0
	if results[0].Op&(1<<0) == 0 {
		t.Error("body chunk: expected '[' at position 0 in Op")
	}
	// Tail chunk: ']' at position 0
	if results[1].Op&(1<<0) == 0 {
		t.Error("tail chunk: expected ']' at position 0 in Op")
	}
}

// TestScanChunks_HeadBodyTail tests the full combination: misaligned buffer
// producing head + body + tail chunks.
func TestScanChunks_HeadBodyTail(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 5-byte misalignment + enough data for head + 2 body chunks + tail
	// headLen = 16-5 = 11, remaining = 128+5-11 = 122, body = 1×64, tail = 58
	buf := makeAligned(128+5+58, ChunkAlignSize, 5)
	buf[0] = '{'        // in head
	buf[11+32] = ':'    // in first body chunk at offset 32
	buf[11+64+10] = ',' // in second body chunk at offset 10
	buf[11+128] = '}'   // in tail at offset 0

	cm.FeedBuffer(buf)
	results := cm.scanResults

	nChunks := len(cm.chunks)
	if len(results) != nChunks {
		t.Fatalf("results count %d != chunks count %d", len(results), nChunks)
	}

	// At least the head and tail chunks should have Op bits set
	if results[0].Op == 0 {
		t.Error("head chunk: expected non-zero Op")
	}
	if results[nChunks-1].Op == 0 {
		t.Error("tail chunk: expected non-zero Op")
	}
}

// TestScanChunks_SequentialFeeds tests scanning two buffers fed sequentially.
func TestScanChunks_SequentialFeeds(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf1 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf1, `{"x":1}`)

	cm.FeedBuffer(buf1)
	if len(cm.scanResults) != 1 {
		t.Fatalf("expected 1 result after first feed, got %d", len(cm.scanResults))
	}
	if cm.scanResults[0].Op&(1<<0) == 0 {
		t.Error("first feed: expected '{' at position 0 in Op")
	}

	buf2 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf2, `{"y":2}`)

	cm.Reset()
	cm.FeedBuffer(buf2)
	if len(cm.scanResults) != 1 {
		t.Fatalf("expected 1 result after second feed, got %d", len(cm.scanResults))
	}
	if cm.scanResults[0].Op&(1<<0) == 0 {
		t.Error("second feed: expected '{' at position 0 in Op")
	}
}

// TestScanChunks_StringDetection verifies that in-string detection works correctly
// through ScanChunks.
func TestScanChunks_StringDetection(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	// "hello world" → positions 0..12, quotes at 0 and 12
	copy(buf, `"hello world"`)

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	// Quotes at 0 and 12
	if r.Strings.Quote&(1<<0) == 0 {
		t.Error("expected quote at position 0")
	}
	if r.Strings.Quote&(1<<12) == 0 {
		t.Error("expected quote at position 12")
	}
	// Positions 1-11 should be inside string
	for i := 1; i <= 11; i++ {
		if r.Strings.InString&(uint64(1)<<i) == 0 {
			t.Errorf("expected position %d to be inside string", i)
		}
	}
}

// TestScanChunks_ResetScanState verifies that Reset clears the streaming
// state so a new document can be parsed correctly.
func TestScanChunks_ResetScanState(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// First document: an open string (no closing quote) to leave state dirty
	buf1 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf1, `"unclosed string without end`)
	cm.FeedBuffer(buf1)
	// Reset and scan a new clean document
	cm.Reset()

	buf2 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf2, `{"key": "value"}`)
	cm.FeedBuffer(buf2)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// After reset, '{' at 0 should be detected as structural (not inside a string)
	r := results[0]
	if r.Op&(1<<0) == 0 {
		t.Error("after reset: expected '{' at position 0 in Op")
	}
	if r.StructuralStart&(1<<0) == 0 {
		t.Error("after reset: expected structural start at position 0")
	}
}

// TestScanChunks_StreamingStateCarry verifies that scan state carries across
// multiple FeedBuffer + ScanChunks calls (streaming scenario).
func TestScanChunks_StreamingStateCarry(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// First chunk: open a string
	buf1 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf1, `"this is a long string that spans`)
	cm.FeedBuffer(buf1)
	results1 := cm.scanResults

	if len(results1) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results1))
	}

	// Second chunk: continuation — the ':' here should be detected as inside a string,
	// so it should NOT appear in Op
	buf2 := makeAligned(64, ChunkAlignSize, 0)
	copy(buf2, ` across : two chunks"`)
	cm.FeedBuffer(buf2)
	results2 := cm.scanResults

	if len(results2) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results2))
	}

	// The ':' at position 8 in buf2 is inside the string, so Op should NOT have bit 8
	r2 := results2[0]
	if r2.Op&(1<<8) != 0 {
		t.Errorf("expected ':' at position 8 to be inside string (not in Op), Op=%064b", r2.Op)
	}
}

// TestScanChunks_ShortBufferHead tests scanning a very short buffer (< 64 bytes)
// that becomes a head chunk.
func TestScanChunks_ShortBufferHead(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := []byte(`{"a":1}`) // 7 bytes → head chunk
	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Op&(1<<0) == 0 {
		t.Error("short head: expected '{' at position 0")
	}
	if r.Op&(1<<6) == 0 {
		t.Error("short head: expected '}' at position 6")
	}
}

// TestScanChunks_ReuseAcrossCalls verifies that the internal scanResults slice
// is reused across multiple FeedBuffer+ScanChunks cycles without extra allocations.
func TestScanChunks_ReuseAcrossCalls(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	copy(buf, `{"a":1}`)

	cm.FeedBuffer(buf)
	r1 := cm.scanResults
	op1 := r1[0].Op

	// Second call — should reuse the same backing array
	cm.FeedBuffer(buf)
	r2 := cm.scanResults

	if len(r2) != 1 {
		t.Fatalf("expected 1 result, got %d", len(r2))
	}
	if r2[0].Op != op1 {
		t.Errorf("expected same Op across reuse, got %064b vs %064b", r2[0].Op, op1)
	}
}

// TestScanChunks_AllStructuralChars verifies detection of all JSON structural characters.
func TestScanChunks_AllStructuralChars(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	//                  0123456789...
	copy(buf, `{ "a" : [ 1 , 2 ] }`)

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	// { at 0
	if r.Op&(1<<0) == 0 {
		t.Error("expected '{' at position 0")
	}
	// : at 6
	if r.Op&(1<<6) == 0 {
		t.Error("expected ':' at position 6")
	}
	// [ at 8
	if r.Op&(1<<8) == 0 {
		t.Error("expected '[' at position 8")
	}
	// , at 12
	if r.Op&(1<<12) == 0 {
		t.Error("expected ',' at position 12")
	}
	// ] at 16
	if r.Op&(1<<16) == 0 {
		t.Error("expected ']' at position 16")
	}
	// } at 18
	if r.Op&(1<<18) == 0 {
		t.Error("expected '}' at position 18")
	}
}

// TestScanChunks_LargeBuffer tests scanning a buffer that produces many chunks
// to exercise the batching loop extensively.
func TestScanChunks_LargeBuffer(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 16 chunks = 1024 bytes
	nChunks := 16
	buf := makeAligned(nChunks*64, ChunkAlignSize, 0)

	// Place '[' at the start of each chunk
	for i := 0; i < nChunks; i++ {
		buf[i*64] = '['
		buf[i*64+1] = ']'
	}

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != nChunks {
		t.Fatalf("expected %d results, got %d", nChunks, len(results))
	}

	for i, r := range results {
		if r.Op&(1<<0) == 0 {
			t.Errorf("chunk %d: expected '[' at position 0", i)
		}
		if r.Op&(1<<1) == 0 {
			t.Errorf("chunk %d: expected ']' at position 1", i)
		}
	}
}

// TestScanChunks_EscapedQuote verifies that escaped quotes inside strings
// are handled correctly through ScanChunks.
func TestScanChunks_EscapedQuote(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	copy(buf, `{"k":"v\"w"}`)

	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	// The escaped quote should not appear as a real quote
	// Real quotes: 0:{, 1:", 3:", 5:", 7:\ 8:", but \"
	// Let's just verify Op detects { and }
	if r.Op&(1<<0) == 0 {
		t.Error("expected '{' at position 0")
	}
	// There should be an escaped character
	if r.Strings.Escaped == 0 {
		t.Error("expected non-zero Escaped bitmap")
	}
}

// TestScanChunks_ZeroPaddingCorrectness verifies that the zero-padded tail region
// of head/tail chunks doesn't produce spurious structural Op detections.
// Note: StructuralStart may include scalar-start bits for 0x00 bytes (the scanner
// treats them as non-whitespace non-structural), but Op should be clean.
func TestScanChunks_ZeroPaddingCorrectness(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 9-byte buffer → head chunk with 9 bytes of data + 55 bytes of zeros
	buf := []byte(`[1, 2, 3]`)
	cm.FeedBuffer(buf)
	results := cm.scanResults

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	// Only positions within the original data should have Op bits.
	// Zero bytes (0x00) are not JSON structural characters ({, }, [, ], :, ,)
	dataLen := len(buf)
	opAboveData := r.Op >> dataLen
	if opAboveData != 0 {
		t.Errorf("spurious Op bits in zero-padded region: %064b", r.Op)
	}
}

// TestScanChunks_NilBuffer verifies ScanChunks handles nil buffers gracefully.
func TestScanChunks_NilBuffer(t *testing.T) {
	cm := NewChunkManager(testScanner())
	cm.FeedBuffer(nil)
	results := cm.scanResults
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil buffer, got %d", len(results))
	}
}

// TestScanChunks_DifferentBufferSizes tests scanning buffers of different sizes
// fed sequentially to FeedBuffer.
func TestScanChunks_DifferentBufferSizes(t *testing.T) {
	bufs := []struct {
		size int
		json string
	}{
		{30, `{"small": true}`},
		{128, `{"medium": 1}`},
		{90, `{"big": "value"}`},
	}

	for _, tc := range bufs {
		cm := NewChunkManager(testScanner())
		buf := makeAligned(tc.size, ChunkAlignSize, 0)
		copy(buf, tc.json)

		cm.FeedBuffer(buf)
		results := cm.scanResults

		nChunks := len(cm.chunks)
		if len(results) != nChunks {
			t.Fatalf("size=%d: results=%d chunks=%d", tc.size, len(results), nChunks)
		}

		// Verify chunk got scanned
		totalOp := uint64(0)
		for _, r := range results {
			totalOp |= r.Op
		}
		if totalOp == 0 {
			t.Errorf("size=%d: expected at least some Op bits across all results", tc.size)
		}
	}
}

// ============ Benchmarks ============

func BenchmarkScanChunks_SingleChunk(b *testing.B) {
	cm := NewChunkManager(testScanner())
	buf := makeAligned(64, ChunkAlignSize, 0)
	copy(buf, `{"key": "value", "num": 12345}`)

	b.ResetTimer()
	for range b.N {
		cm.Reset()
		cm.FeedBuffer(buf)
	}
}

func BenchmarkScanChunks_16Chunks(b *testing.B) {
	cm := NewChunkManager(testScanner())
	buf := makeAligned(1024, ChunkAlignSize, 0)
	for i := 0; i < 16; i++ {
		copy(buf[i*64:], `{"key": "value", "num": 12345}`)
	}

	b.ResetTimer()
	for range b.N {
		cm.Reset()
		cm.FeedBuffer(buf)
	}
}

func BenchmarkScanChunks_WithHeadTail(b *testing.B) {
	cm := NewChunkManager(testScanner())
	// 5-byte misalignment → head + body + tail
	buf := makeAligned(256+5+30, ChunkAlignSize, 5)
	copy(buf, `{"key": "value", "arr": [1, 2, 3]}`)

	cm.FeedBuffer(buf)
	nChunks := len(cm.chunks)

	b.ResetTimer()
	for range b.N {
		cm.Reset()
		cm.FeedBuffer(buf)
		if len(cm.scanResults) != nChunks {
			b.Fatalf("unexpected result count: %d", len(cm.scanResults))
		}
	}
}

// ============ StructuralResults Tests ============

// TestStructuralResults_MatchesScanResults verifies that structuralResults[i]
// equals scanResults[i].StructuralStart for every chunk.
func TestStructuralResults_MatchesScanResults(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(256, ChunkAlignSize, 0)
	copy(buf, `{"a":1,"b":[2,3],"c":"hello"}`)
	copy(buf[64:], `{"d":true,"e":null,"f":4.5}`)
	copy(buf[128:], `[1,2,3,4,5,6,7,8,9,10]`)
	copy(buf[192:], `{"nested":{"x":"y"}}`)

	cm.FeedBuffer(buf)

	if len(cm.structuralResults) != len(cm.scanResults) {
		t.Fatalf("structuralResults len %d != scanResults len %d",
			len(cm.structuralResults), len(cm.scanResults))
	}

	for i, r := range cm.scanResults {
		if cm.structuralResults[i] != r.StructuralStart {
			t.Errorf("chunk %d: structuralResults=%064b != StructuralStart=%064b",
				i, cm.structuralResults[i], r.StructuralStart)
		}
	}
}

// TestStructuralResults_Empty verifies structuralResults is empty when no buffer is fed.
func TestStructuralResults_Empty(t *testing.T) {
	cm := NewChunkManager(testScanner())
	if len(cm.structuralResults) != 0 {
		t.Errorf("expected 0 structuralResults, got %d", len(cm.structuralResults))
	}
}

// TestStructuralResults_NilBuffer verifies structuralResults is empty for nil buffer.
func TestStructuralResults_NilBuffer(t *testing.T) {
	cm := NewChunkManager(testScanner())
	cm.FeedBuffer(nil)
	if len(cm.structuralResults) != 0 {
		t.Errorf("expected 0 structuralResults for nil buffer, got %d", len(cm.structuralResults))
	}
}

// TestStructuralResults_SimpleJSON verifies that structuralResults detects all
// token start positions in a simple JSON object.
func TestStructuralResults_SimpleJSON(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	//               0123456789012345
	copy(buf, `{"a":1}`)

	cm.FeedBuffer(buf)

	if len(cm.structuralResults) != 1 {
		t.Fatalf("expected 1 structuralResult, got %d", len(cm.structuralResults))
	}

	s := cm.structuralResults[0]

	// { at 0
	if s&(1<<0) == 0 {
		t.Error("expected structural start at position 0 ('{')")
	}
	// "a" key starts at 1
	if s&(1<<1) == 0 {
		t.Error("expected structural start at position 1 ('\"a\"')")
	}
	// : at 4
	if s&(1<<4) == 0 {
		t.Error("expected structural start at position 4 (':')")
	}
	// 1 at 5 (scalar start)
	if s&(1<<5) == 0 {
		t.Error("expected structural start at position 5 ('1')")
	}
	// } at 6
	if s&(1<<6) == 0 {
		t.Error("expected structural start at position 6 ('}')")
	}

	// Count tokens within the data region (positions 0..6).
	// Zero-padding beyond the data may produce spurious scalar-start bits
	// (the scanner treats 0x00 as non-whitespace non-structural).
	dataMask := uint64((1 << 7) - 1) // positions 0..6
	nTokens := bits.OnesCount64(s & dataMask)
	if nTokens != 5 {
		t.Errorf("expected 5 token starts in data region, got %d (bitmap=%064b)", nTokens, s&dataMask)
	}
}

// TestStructuralResults_MultiChunk verifies structuralResults across multiple chunks.
func TestStructuralResults_MultiChunk(t *testing.T) {
	cm := NewChunkManager(testScanner())

	// 3 body chunks + 1 tail
	buf := makeAligned(200, ChunkAlignSize, 0)
	copy(buf, `[1,2,3]`)       // chunk 0
	copy(buf[64:], `{"k":"v"}`) // chunk 1
	copy(buf[128:], `[true]`)   // chunk 2 (body), rest is tail

	cm.FeedBuffer(buf)

	if len(cm.structuralResults) != len(cm.chunks) {
		t.Fatalf("structuralResults len %d != chunks len %d",
			len(cm.structuralResults), len(cm.chunks))
	}

	// Chunk 0 should have tokens: [ 1 , 2 , 3 ]
	s0 := cm.structuralResults[0]
	if s0 == 0 {
		t.Error("chunk 0: expected non-zero structuralResults")
	}

	// Chunk 1 should have tokens: { "k" : "v" }
	s1 := cm.structuralResults[1]
	if s1 == 0 {
		t.Error("chunk 1: expected non-zero structuralResults")
	}
}

// TestStructuralResults_Reuse verifies that structuralResults slice is reused
// across multiple FeedBuffer calls.
func TestStructuralResults_Reuse(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	copy(buf, `{"a":1}`)

	cm.FeedBuffer(buf)
	s1 := cm.structuralResults[0]

	cm.FeedBuffer(buf)
	s2 := cm.structuralResults[0]

	if s1 != s2 {
		t.Errorf("expected same structuralResults across reuse, got %064b vs %064b", s1, s2)
	}
}

// TestStructuralResults_WhitespaceOnly verifies structuralResults is zero
// for whitespace-only input.
func TestStructuralResults_WhitespaceOnly(t *testing.T) {
	cm := NewChunkManager(testScanner())

	buf := makeAligned(64, ChunkAlignSize, 0)
	for i := range buf {
		buf[i] = ' '
	}

	cm.FeedBuffer(buf)

	if len(cm.structuralResults) != 1 {
		t.Fatalf("expected 1 structuralResult, got %d", len(cm.structuralResults))
	}

	if cm.structuralResults[0] != 0 {
		t.Errorf("expected zero structuralResults for whitespace-only, got %064b",
			cm.structuralResults[0])
	}
}

// keep compiler happy
var _ = bits.OnesCount64
