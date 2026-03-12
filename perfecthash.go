package vjson

import (
	"math/bits"
	"strings"
	"unsafe"
)

// fieldLookup is the interface for struct field lookup strategies.
type fieldLookup interface {
	lookup(dec *StructCodec, key string) *TypeInfo
}

// emptyLookup always returns nil (zero-field struct).
type emptyLookup struct{}

func (emptyLookup) lookup(_ *StructCodec, _ string) *TypeInfo { return nil }

// bitmapLookup8 uses a per-position character bitmap for ≤8 fields.
// Each field is assigned 1 bit. For each (charIndex, byte) pair, the bitmap
// records which fields have that byte at that position. Lookup ANDs the bits
// across all positions + length mask, yielding the unique matching field.
type bitmapLookup8 struct {
	maxKeyLen uint8
	bitmap    []uint8 // flattened [maxKeyLen][256], row-major
	lenMask   []uint8 // lenMask[len] = bitmask of fields with this length; size = maxKeyLen+1
}

// sliceGet performs an unchecked index into a slice.
// The caller must guarantee i is in bounds.
//
//go:nosplit
func sliceGet[T any](s []T, i int) T {
	return *(*T)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(s)), uintptr(i)*unsafe.Sizeof(s[0])))
}

func (b *bitmapLookup8) lookup(dec *StructCodec, key string) *TypeInfo {
	klen := len(key)
	if klen == 0 || klen > int(b.maxKeyLen) {
		return nil
	}
	bitmap := b.bitmap
	lenMask := b.lenMask
	cur := uint8(0xFF)
	for i := 0; i < klen; i++ {
		cur &= sliceGet(bitmap, i*256+int(key[i]))
		if cur == 0 {
			return nil
		}
	}
	cur &= sliceGet(lenMask, klen)
	if cur == 0 {
		return nil
	}
	idx := bits.TrailingZeros8(cur)
	return &dec.Fields[idx]
}

// buildBitmapLookup8 constructs a bitmapLookup8 for a StructCodec with ≤8 fields.
func buildBitmapLookup8(dec *StructCodec) *bitmapLookup8 {
	n := len(dec.Fields)
	maxLen := 0
	for i := range dec.Fields {
		if l := len(dec.Fields[i].JSONName); l > maxLen {
			maxLen = l
		}
	}

	b := &bitmapLookup8{
		maxKeyLen: uint8(maxLen),
		bitmap:    make([]uint8, maxLen*256),
		lenMask:   make([]uint8, maxLen+1),
	}

	for i := 0; i < n; i++ {
		name := dec.Fields[i].JSONName
		bit := uint8(1) << i
		for j := 0; j < len(name); j++ {
			b.bitmap[j*256+int(name[j])] |= bit
		}
		b.lenMask[len(name)] |= bit
	}

	return b
}

// perfectHashBase holds the common state for all perfect-hash strategies.
type perfectHashBase struct {
	seed  uint64
	shift uint8
	table []uint8 // indices into Fields[]; 0xFF = empty
}

func (b *perfectHashBase) lookupByHash(dec *StructCodec, key string, h uint64) *TypeInfo {
	slot := int(h>>b.shift) & (len(b.table) - 1)
	idx := b.table[slot]
	if idx == 0xFF {
		return nil
	}
	fi := &dec.Fields[idx]
	if fi.JSONName == key {
		return fi
	}
	return nil
}

// perfectSimpleLookup uses simpleMixer for hashing.
type perfectSimpleLookup struct{ perfectHashBase }

func (p *perfectSimpleLookup) lookup(dec *StructCodec, key string) *TypeInfo {
	return p.lookupByHash(dec, key, simpleMixer(key, p.seed))
}

// perfectFNVLookup uses fnv1aMixer for hashing.
type perfectFNVLookup struct{ perfectHashBase }

func (p *perfectFNVLookup) lookup(dec *StructCodec, key string) *TypeInfo {
	return p.lookupByHash(dec, key, fnv1aMixer(key, p.seed))
}

// perfectMulaccLookup uses mulaccMixer for hashing.
type perfectMulaccLookup struct{ perfectHashBase }

func (p *perfectMulaccLookup) lookup(dec *StructCodec, key string) *TypeInfo {
	return p.lookupByHash(dec, key, mulaccMixer(key, p.seed))
}

// mapLookup uses a fallback map for large structs.
type mapLookup struct {
	m map[string]*TypeInfo
}

func (l *mapLookup) lookup(_ *StructCodec, key string) *TypeInfo {
	return l.m[key]
}

// LookupFieldBytes looks up a struct field by JSON key.
// It tries an exact match first (fast path), then falls back to
// case-insensitive matching per encoding/json semantics.
func (dec *StructCodec) LookupFieldBytes(key []byte) *TypeInfo {
	k := unsafe.String(unsafe.SliceData(key), len(key))

	// Fast path: exact match via polymorphic lookup.
	fi := dec.Lookup.lookup(dec, k)
	if fi != nil {
		return fi
	}

	// If neither the key nor any tag contains uppercase, exact miss is final.
	if !dec.HasMixedCase && !hasUpperASCII(key) {
		return nil
	}

	// Slow path: case-insensitive linear scan.
	// Use fast ASCII fold first; fall back to strings.EqualFold only
	// when a non-ASCII byte is detected.
	fields := dec.Fields
	for i := range fields {
		if equalFoldASCII(fields[i].JSONName, k) {
			return &fields[i]
		}
	}
	return nil
}

// Build phase (called once per struct type at initialization).

// buildLookup selects and constructs the optimal lookup strategy for a
// StructCodec based on its field count. Called once at construction.
func buildLookup(dec *StructCodec) {
	for i := range dec.Fields {
		if dec.Fields[i].JSONName != dec.Fields[i].JSONNameLower {
			dec.HasMixedCase = true
			break
		}
	}

	n := len(dec.Fields)
	switch {
	case n == 0:
		dec.Lookup = emptyLookup{}
	case n <= 8:
		dec.Lookup = buildBitmapLookup8(dec)
	case n <= 32:
		if base, ok := tryBuildPerfectHash(dec, simpleMixer); ok {
			dec.Lookup = &perfectSimpleLookup{base}
		} else if base, ok := tryBuildPerfectHash(dec, fnv1aMixer); ok {
			dec.Lookup = &perfectFNVLookup{base}
		} else {
			buildMapFallback(dec)
		}
	default:
		if base, ok := tryBuildPerfectHash(dec, mulaccMixer); ok {
			dec.Lookup = &perfectMulaccLookup{base}
		} else {
			buildMapFallback(dec)
		}
	}
}

const maxSeedAttempts = 1 << 16 // 64K seeds, each tested against all shifts

// hashMixer is a hash function type used for perfect hash construction.
type hashMixer func(s string, seed uint64) uint64

// tryBuildPerfectHash attempts to find (seed, shift) such that mixer(name, seed) >> shift
// maps each field's JSON tag name to a unique slot in a power-of-2 table.
//
// Strategy: for each seed, compute all hashes once, then sweep shifts to find a
// zero-collision mapping. The search is bounded by maxSeedAttempts; callers
// fall back to a map when no perfect hash is found.
func tryBuildPerfectHash(dec *StructCodec, mixer hashMixer) (perfectHashBase, bool) {
	n := len(dec.Fields)
	tableSize := nextPowerOf2(n * 2) // load factor ~50%
	mask := uint64(tableSize - 1)

	names := make([]string, n)
	for i := range dec.Fields {
		names[i] = dec.Fields[i].JSONName
	}

	// Reusable buffers
	hashes := make([]uint64, n)
	seen := make([]uint8, tableSize) // generation counter to avoid clearing
	gen := uint8(1)

	for seed := range uint64(maxSeedAttempts) {
		// Compute all hashes for this seed
		for i, name := range names {
			hashes[i] = mixer(name, seed)
		}

		// Try all shifts 0..63 (only those that produce at least tableSizeBits
		// bits below the mask matter, but just sweep — it's cheap with n<=32)
		for shift := range uint8(64) {
			if gen == 255 {
				clear(seen)
				gen = 1
			} else {
				gen++
			}

			collision := false
			for _, h := range hashes {
				slot := (h >> shift) & mask
				if seen[slot] == gen {
					collision = true
					break
				}
				seen[slot] = gen
			}

			if !collision {
				// Found a perfect hash — build the table
				table := make([]uint8, tableSize)
				for i := range table {
					table[i] = 0xFF
				}
				for i, h := range hashes {
					slot := (h >> shift) & mask
					table[slot] = uint8(i)
				}
				return perfectHashBase{seed: seed, shift: shift, table: table}, true
			}
		}
	}
	return perfectHashBase{}, false
}

// buildMapFallback initializes the traditional map[string]*TypeInfo for large structs.
func buildMapFallback(dec *StructCodec) {
	m := make(map[string]*TypeInfo, len(dec.Fields))
	for i := range dec.Fields {
		m[dec.Fields[i].JSONName] = &dec.Fields[i]
	}
	dec.Lookup = &mapLookup{m: m}
}

// Hash mixers.

// simpleMixer hashes a string using 4 features: length, first byte, last byte,
// and middle byte. No loop — constant time regardless of string length.
// Works well for small sets (5-16 fields) with diverse field names.
func simpleMixer(s string, seed uint64) uint64 {
	n := uint64(len(s))
	if n == 0 {
		return seed * 0x9e3779b97f4a7c15
	}
	first := uint64(s[0])
	last := uint64(s[len(s)-1])
	mid := uint64(s[len(s)/2])

	h := seed
	h ^= n * 0x9e3779b97f4a7c15
	h ^= first * 0xbf58476d1ce4e5b9
	h ^= last * 0x94d049bb133111eb
	h ^= mid * 0xff51afd7ed558ccd
	return h
}

// mulaccMixer uses a multiply-accumulate chain over 5 byte positions
// (first, last, mid, second, penultimate), seeded with length.
// O(1) constant time, works well for 30-100+ fields.
func mulaccMixer(s string, seed uint64) uint64 {
	n := uint64(len(s))
	if n == 0 {
		return seed * 0x9e3779b97f4a7c15
	}
	h := seed + n*0x9e3779b97f4a7c15
	h = h*0xbf58476d1ce4e5b9 + uint64(s[0])
	h = h*0x94d049bb133111eb + uint64(s[len(s)-1])
	h = h*0xff51afd7ed558ccd + uint64(s[len(s)/2])
	if n > 1 {
		h = h*0xc4ceb9fe1a85ec53 + uint64(s[1])
	}
	if n > 2 {
		h = h*0x62a9d9ed799705f5 + uint64(s[len(s)-2])
	}
	return h
}

// fnv1aMixer hashes all bytes of the string using the FNV-1a algorithm.
// Stronger distribution than simpleMixer, used as fallback for larger sets
// or when simpleMixer fails to produce a perfect hash.
func fnv1aMixer(s string, seed uint64) uint64 {
	h := seed ^ 0xcbf29ce484222325 // FNV offset basis
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 0x100000001b3 // FNV prime
	}
	// Final avalanche to improve bit distribution
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

// equalFoldASCII is a fast ASCII-only case-insensitive string comparison.
// For pure-ASCII strings (the common case for JSON field names), this is
// significantly faster than strings.EqualFold which handles full Unicode.
// Falls back to strings.EqualFold when any byte is >= 0x80.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca == cb {
			continue
		}
		// Non-ASCII byte → delegate to full Unicode fold.
		if ca >= 0x80 || cb >= 0x80 {
			return strings.EqualFold(a, b)
		}
		// ASCII case fold: OR 0x20 maps A-Z to a-z.
		if ca|0x20 != cb|0x20 {
			return false
		}
		// Verify both are actually letters (not e.g. '@' vs '`').
		if ca|0x20 < 'a' || ca|0x20 > 'z' {
			return false
		}
	}
	return true
}

// ASCII case conversion.

// toLowerASCII returns a lowercased version of s for ASCII letters.
// If s contains no uppercase ASCII, returns s directly (common case, zero alloc).
// Non-ASCII bytes are left unchanged.
func toLowerASCII(s string) string {
	// Fast check: any uppercase?
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}

	buf := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 0x20
		}
		buf[i] = c
	}
	return unsafe.String(&buf[0], len(buf))
}

// SWAR broadcast constants for hasUpperASCII.
const (
	swarLo64 = uint64(0x0101010101010101)
	swarHi64 = uint64(0x8080808080808080)
)

// hasUpperASCII reports whether any byte in key is an uppercase ASCII letter.
// Uses SWAR range test: bytes in [0x41,0x5A] (A-Z) set the high bit marker.
func hasUpperASCII(key []byte) bool {
	const (
		addLo = (0x80 - 0x5B) * swarLo64 // 0x2525252525252525
		addHi = (0x80 - 0x41) * swarLo64 // 0x3F3F3F3F3F3F3F3F
	)
	i := 0
	for i+8 <= len(key) {
		w := *(*uint64)(unsafe.Pointer(&key[i]))
		if ((w+addHi)^(w+addLo))&swarHi64 != 0 {
			return true
		}
		i += 8
	}
	for ; i < len(key); i++ {
		if key[i] >= 'A' && key[i] <= 'Z' {
			return true
		}
	}
	return false
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
