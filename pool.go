package pjson

import (
	"sync"

	"github.com/penglei/pjson/jsonmarker"
)

// Predefined chunk capacity tiers.
// Each tier pre-allocates slices for the given number of 64-byte chunks.
const (
	// CapSmall suits JSON documents up to ~4 KB (64 chunks × 64 bytes).
	CapSmall = 64

	// CapMedium suits JSON documents up to ~8 KB (128 chunks × 64 bytes).
	CapMedium = 128

	// CapLarge suits JSON documents up to ~64 KB (1024 chunks × 64 bytes).
	CapLarge = 1024
)

const numTiers = 3

// ParserPool manages a set of sync.Pool instances, one per capacity tier.
// Get selects the smallest tier that can handle the input size, avoiding
// unnecessary allocation while keeping pooled parsers appropriately sized.
type ParserPool struct {
	scanner *jsonmarker.StdScanner
	pools   [numTiers]sync.Pool
	caps    [numTiers]int
}

// NewParserPool creates a pool backed by the given SIMD scanner.
func NewParserPool(scanner *jsonmarker.StdScanner) *ParserPool {
	pp := &ParserPool{
		scanner: scanner,
		caps:    [numTiers]int{CapSmall, CapMedium, CapLarge},
	}
	pp.warm()
	return pp
}

// warm pre-populates each tier with one ready-to-use Parser so that the
// first real Unmarshal call avoids a cold allocation on the hot path.
func (pp *ParserPool) warm() {
	for i, c := range pp.caps {
		cm := NewChunkManager(pp.scanner, WithChunkCap(c))
		pp.pools[i].Put(&Parser{cm: cm, tier: i})
	}
}

// tierFor returns the index of the smallest capacity tier that can hold
// the estimated number of chunks for a buffer of length dataLen.
func (pp *ParserPool) tierFor(dataLen int) int {
	need := dataLen/ChunkSize + 2
	for i, c := range pp.caps {
		if need <= c {
			return i
		}
	}
	return numTiers - 1
}

// Get returns a Parser from the appropriate pool tier, or creates a new one.
func (pp *ParserPool) Get(dataLen int) *Parser {
	tier := pp.tierFor(dataLen)
	if v := pp.pools[tier].Get(); v != nil {
		return v.(*Parser)
	}
	cm := NewChunkManager(pp.scanner, WithChunkCap(pp.caps[tier]))
	return &Parser{cm: cm, tier: tier}
}

// Put returns a Parser to its pool tier for reuse.
// The caller must not use the Parser after Put.
func (pp *ParserPool) Put(p *Parser) {
	p.data = nil // release reference to input buffer for GC
	pp.pools[p.tier].Put(p)
}
