package pjson

import "testing"

// ============ ExtractIndices Tests ============

func TestExtractIndices_Zero(t *testing.T) {
	indices := ExtractIndices(0, nil)
	if len(indices) != 0 {
		t.Fatalf("expected 0 indices for bitmask=0, got %d", len(indices))
	}
}

func TestExtractIndices_SingleBit(t *testing.T) {
	for bit := 0; bit < 64; bit++ {
		bitmask := uint64(1) << bit
		indices := ExtractIndices(bitmask, nil)
		if len(indices) != 1 {
			t.Fatalf("bit=%d: expected 1 index, got %d", bit, len(indices))
		}
		if indices[0] != uint32(bit) {
			t.Errorf("bit=%d: expected index %d, got %d", bit, bit, indices[0])
		}
	}
}

func TestExtractIndices_AllBitsSet(t *testing.T) {
	bitmask := ^uint64(0) // all 64 bits set
	indices := ExtractIndices(bitmask, nil)
	if len(indices) != 64 {
		t.Fatalf("expected 64 indices, got %d", len(indices))
	}
	for i, idx := range indices {
		if idx != uint32(i) {
			t.Errorf("index[%d] = %d, want %d", i, idx, i)
		}
	}
}

func TestExtractIndices_SparsePattern(t *testing.T) {
	// bits at positions 0, 7, 15, 31, 32, 48, 63
	bitmask := uint64(1)<<0 | uint64(1)<<7 | uint64(1)<<15 |
		uint64(1)<<31 | uint64(1)<<32 | uint64(1)<<48 | uint64(1)<<63
	expected := []uint32{0, 7, 15, 31, 32, 48, 63}

	indices := ExtractIndices(bitmask, nil)
	if len(indices) != len(expected) {
		t.Fatalf("expected %d indices, got %d", len(expected), len(indices))
	}
	for i, idx := range indices {
		if idx != expected[i] {
			t.Errorf("index[%d] = %d, want %d", i, idx, expected[i])
		}
	}
}

func TestExtractIndices_LowByte(t *testing.T) {
	// All 8 bits in the lowest byte
	bitmask := uint64(0xFF)
	indices := ExtractIndices(bitmask, nil)
	if len(indices) != 8 {
		t.Fatalf("expected 8 indices, got %d", len(indices))
	}
	for i, idx := range indices {
		if idx != uint32(i) {
			t.Errorf("index[%d] = %d, want %d", i, idx, i)
		}
	}
}

func TestExtractIndices_HighByte(t *testing.T) {
	// All 8 bits in the highest byte
	bitmask := uint64(0xFF) << 56
	indices := ExtractIndices(bitmask, nil)
	if len(indices) != 8 {
		t.Fatalf("expected 8 indices, got %d", len(indices))
	}
	for i, idx := range indices {
		if idx != uint32(56+i) {
			t.Errorf("index[%d] = %d, want %d", i, idx, 56+i)
		}
	}
}

func TestExtractIndices_Exactly8Bits(t *testing.T) {
	// Exactly 8 bits — exercises the unrolled path once with no tail
	bitmask := uint64(1)<<0 | uint64(1)<<8 | uint64(1)<<16 | uint64(1)<<24 |
		uint64(1)<<32 | uint64(1)<<40 | uint64(1)<<48 | uint64(1)<<56
	expected := []uint32{0, 8, 16, 24, 32, 40, 48, 56}

	indices := ExtractIndices(bitmask, nil)
	if len(indices) != 8 {
		t.Fatalf("expected 8 indices, got %d", len(indices))
	}
	for i, idx := range indices {
		if idx != expected[i] {
			t.Errorf("index[%d] = %d, want %d", i, idx, expected[i])
		}
	}
}

func TestExtractIndices_9Bits(t *testing.T) {
	// 9 bits — exercises unrolled path (8) + tail (1)
	bitmask := uint64(1)<<0 | uint64(1)<<8 | uint64(1)<<16 | uint64(1)<<24 |
		uint64(1)<<32 | uint64(1)<<40 | uint64(1)<<48 | uint64(1)<<56 |
		uint64(1)<<63
	expected := []uint32{0, 8, 16, 24, 32, 40, 48, 56, 63}

	indices := ExtractIndices(bitmask, nil)
	if len(indices) != 9 {
		t.Fatalf("expected 9 indices, got %d", len(indices))
	}
	for i, idx := range indices {
		if idx != expected[i] {
			t.Errorf("index[%d] = %d, want %d", i, idx, expected[i])
		}
	}
}

func TestExtractIndices_AppendToExisting(t *testing.T) {
	// Verify that ExtractIndices appends to a pre-existing slice
	existing := []uint32{100, 200}
	bitmask := uint64(1)<<3 | uint64(1)<<7
	indices := ExtractIndices(bitmask, existing)
	if len(indices) != 4 {
		t.Fatalf("expected 4 total indices, got %d", len(indices))
	}
	if indices[0] != 100 || indices[1] != 200 {
		t.Error("pre-existing values were corrupted")
	}
	if indices[2] != 3 || indices[3] != 7 {
		t.Errorf("appended values = [%d, %d], want [3, 7]", indices[2], indices[3])
	}
}

func TestExtractIndices_Ascending(t *testing.T) {
	// For any bitmask, extracted indices must be in strictly ascending order
	patterns := []uint64{
		0x0123456789ABCDEF,
		0xAAAAAAAAAAAAAAAA,
		0x5555555555555555,
		0xF0F0F0F0F0F0F0F0,
		0xFFFFFFFFFFFFFFFF,
		0x8000000000000001,
	}
	for _, bitmask := range patterns {
		indices := ExtractIndices(bitmask, nil)
		for i := 1; i < len(indices); i++ {
			if indices[i] <= indices[i-1] {
				t.Errorf("bitmask=%#x: non-ascending at [%d]=%d, [%d]=%d",
					bitmask, i-1, indices[i-1], i, indices[i])
				break
			}
		}
	}
}

// ============ ExtractIndices Benchmarks ============

func BenchmarkExtractIndices_Sparse(b *testing.B) {
	// 6 bits set — all go through tail path
	bitmask := uint64(1)<<0 | uint64(1)<<10 | uint64(1)<<20 |
		uint64(1)<<30 | uint64(1)<<50 | uint64(1)<<63
	indices := make([]uint32, 0, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indices = ExtractIndices(bitmask, indices[:0])
	}
}

func BenchmarkExtractIndices_Dense(b *testing.B) {
	// 32 bits set — exercises 4 unrolled iterations
	bitmask := uint64(0xAAAAAAAAAAAAAAAA)
	indices := make([]uint32, 0, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indices = ExtractIndices(bitmask, indices[:0])
	}
}

func BenchmarkExtractIndices_Full(b *testing.B) {
	// All 64 bits set — 8 unrolled iterations
	bitmask := ^uint64(0)
	indices := make([]uint32, 0, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indices = ExtractIndices(bitmask, indices[:0])
	}
}
