package pjson

import "unsafe"

// =============================================================================
// Hash Functions
// =============================================================================

// hashMixer is a hash function type used for perfect hash construction.
type hashMixer func(s string, seed uint64) uint64

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

// =============================================================================
// ASCII Case Conversion
// =============================================================================

// toLowerASCII returns a lowercased version of s for ASCII letters.
// If s contains no uppercase ASCII, returns s directly (zero allocation).
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

// toLowerASCIIBytes lowercases a byte slice in-place for ASCII letters and
// returns it as a string via unsafe (zero-copy). The caller must not modify
// buf after calling this function.
func toLowerASCIIBytes(buf []byte) string {
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if c >= 'A' && c <= 'Z' {
			buf[i] = c + 0x20
		}
	}
	if len(buf) == 0 {
		return ""
	}
	return unsafe.String(&buf[0], len(buf))
}

// =============================================================================
// Build Phase
// =============================================================================

// buildLookup selects and constructs the optimal lookup strategy for a
// reflectStructDecoder based on its field count. Called once at construction.
func buildLookup(dec *reflectStructDecoder) {
	n := len(dec.fields)
	switch {
	case n == 0:
		dec.lookupFn = lookupEmpty
	case n <= 4:
		dec.lookupFn = lookupLinear
	case n <= 32:
		if tryBuildPerfectHash(dec, simpleMixer) {
			dec.lookupFn = makePerfectHashLookup(simpleMixer)
		} else if tryBuildPerfectHash(dec, fnv1aMixer) {
			dec.lookupFn = makePerfectHashLookup(fnv1aMixer)
		} else {
			buildMapFallback(dec)
		}
	default:
		buildMapFallback(dec)
	}
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

const maxSeedAttempts = 1 << 16 // 64K seeds, each tested against all shifts

// tryBuildPerfectHash attempts to find (seed, shift) such that mixer(name, seed) >> shift
// maps each field's lowercased name to a unique slot in a power-of-2 table.
//
// Strategy: for each seed, compute all hashes once, then sweep all useful shift
// values to find a zero-collision mapping. This is much faster than calling
// findBestShift as a separate function with its own allocations.
func tryBuildPerfectHash(dec *reflectStructDecoder, mixer hashMixer) bool {
	n := len(dec.fields)
	tableSize := nextPowerOf2(n * 2) // load factor ~50%
	mask := uint64(tableSize - 1)

	// Pre-compute lowercased names (used once)
	names := make([]string, n)
	for i := range dec.fields {
		names[i] = dec.fields[i].jsonNameLower
	}

	// Reusable buffers
	hashes := make([]uint64, n)
	seen := make([]uint8, tableSize) // generation counter to avoid clearing
	gen := uint8(1)

	for seed := uint64(0); seed < maxSeedAttempts; seed++ {
		// Compute all hashes for this seed
		for i, name := range names {
			hashes[i] = mixer(name, seed)
		}

		// Try all shifts 0..63 (only those that produce at least tableSizeBits
		// bits below the mask matter, but just sweep — it's cheap with n<=32)
		for shift := uint8(0); shift < 64; shift++ {
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
				dec.hashSeed = seed
				dec.hashShift = shift
				dec.hashTable = make([]uint8, tableSize)
				for i := range dec.hashTable {
					dec.hashTable[i] = 0xFF
				}
				for i, h := range hashes {
					slot := (h >> shift) & mask
					dec.hashTable[slot] = uint8(i)
				}
				return true
			}
		}
	}
	return false
}

// buildMapFallback initializes the traditional map[string]*fieldInfo for large structs.
func buildMapFallback(dec *reflectStructDecoder) {
	dec.fieldMap = make(map[string]*typeInfo, len(dec.fields))
	for i := range dec.fields {
		dec.fieldMap[dec.fields[i].jsonNameLower] = &dec.fields[i]
	}
	dec.lookupFn = lookupMap
}

// =============================================================================
// Lookup Functions
// =============================================================================

// lookupEmpty always returns nil (zero-field struct).
func lookupEmpty(_ *reflectStructDecoder, _ string) *typeInfo {
	return nil
}

// lookupLinear performs a linear scan over 1-4 fields.
// The key must already be lowercased.
func lookupLinear(dec *reflectStructDecoder, key string) *typeInfo {
	fields := dec.fields
	for i := range fields {
		if fields[i].jsonNameLower == key {
			return &fields[i]
		}
	}
	return nil
}

// makePerfectHashLookup returns a lookup function bound to a specific mixer.
func makePerfectHashLookup(mixer hashMixer) func(*reflectStructDecoder, string) *typeInfo {
	return func(dec *reflectStructDecoder, key string) *typeInfo {
		h := mixer(key, dec.hashSeed)
		slot := int(h>>dec.hashShift) & (len(dec.hashTable) - 1)

		idx := dec.hashTable[slot]
		if idx == 0xFF {
			return nil
		}

		fi := &dec.fields[idx]
		if fi.jsonNameLower == key {
			return fi
		}
		return nil
	}
}

// lookupMap uses the fallback map for large structs.
// The key must already be lowercased.
func lookupMap(dec *reflectStructDecoder, key string) *typeInfo {
	return dec.fieldMap[key]
}

// lookupField is the public entry point for field lookup.
// It lowercases the key (ASCII fast path) and dispatches to the tiered strategy.
func (dec *reflectStructDecoder) lookupField(key string) *typeInfo {
	return dec.lookupFn(dec, toLowerASCII(key))
}

// lookupFieldBytes is the zero-allocation entry point for field lookup from []byte.
// It lowercases the key in a scratch buffer and dispatches to the tiered strategy.
// The scratch buffer is provided by the caller to avoid allocation.
func (dec *reflectStructDecoder) lookupFieldBytes(key []byte, scratch []byte) *typeInfo {
	// Fast path: if key is already all-lowercase ASCII, skip copy+toLower entirely.
	allLower := true
	for _, c := range key {
		if c >= 'A' && c <= 'Z' {
			allLower = false
			break
		}
	}
	if allLower {
		k := unsafe.String(unsafe.SliceData(key), len(key))
		return dec.lookupFn(dec, k)
	}

	// Slow path: copy into scratch and lowercase.
	if len(key) > len(scratch) {
		scratch = make([]byte, len(key))
	}
	buf := scratch[:len(key)]
	copy(buf, key)
	lowered := toLowerASCIIBytes(buf)
	return dec.lookupFn(dec, lowered)
}
