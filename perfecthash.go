package vjson

import (
	"strings"
	"unsafe"
)

const (
	lookupModeEmpty uint8 = iota
	lookupModeLinear
	lookupModePerfectSimple
	lookupModePerfectFNV
	lookupModePerfectMulacc
	lookupModeMap
)

// LookupFieldBytes looks up a struct field by JSON key.
// It tries an exact match first (fast path), then falls back to
// case-insensitive matching per encoding/json semantics.
func (dec *StructCodec) LookupFieldBytes(key []byte) *TypeInfo {
	k := unsafe.String(unsafe.SliceData(key), len(key))

	// Fast path: exact match against original tag names.
	var fi *TypeInfo
	switch dec.LookupMode {
	case lookupModeEmpty:
		fi = nil
	case lookupModeLinear:
		fi = lookupLinear(dec, k)
	case lookupModePerfectSimple:
		fi = lookupPerfectByHash(dec, k, simpleMixer(k, dec.HashSeed))
	case lookupModePerfectFNV:
		fi = lookupPerfectByHash(dec, k, fnv1aMixer(k, dec.HashSeed))
	case lookupModePerfectMulacc:
		fi = lookupPerfectByHash(dec, k, mulaccMixer(k, dec.HashSeed))
	case lookupModeMap:
		fi = lookupMap(dec, k)
	default:
		fi = dec.LookupFn(dec, k)
	}
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
	for i := range dec.Fields {
		if equalFoldASCII(dec.Fields[i].JSONName, k) {
			return &dec.Fields[i]
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
		dec.LookupFn = lookupEmpty
		dec.LookupMode = lookupModeEmpty
	case n <= 4:
		dec.LookupFn = lookupLinear
		dec.LookupMode = lookupModeLinear
	case n <= 32:
		if tryBuildPerfectHash(dec, simpleMixer) {
			dec.LookupFn = makePerfectHashLookup(simpleMixer)
			dec.LookupMode = lookupModePerfectSimple
		} else if tryBuildPerfectHash(dec, fnv1aMixer) {
			dec.LookupFn = makePerfectHashLookup(fnv1aMixer)
			dec.LookupMode = lookupModePerfectFNV
		} else {
			buildMapFallback(dec)
		}
	default:
		if tryBuildPerfectHash(dec, mulaccMixer) {
			dec.LookupFn = makePerfectHashLookup(mulaccMixer)
			dec.LookupMode = lookupModePerfectMulacc
		} else {
			buildMapFallback(dec)
		}
	}
}

const maxSeedAttempts = 1 << 16 // 64K seeds, each tested against all shifts

// tryBuildPerfectHash attempts to find (seed, shift) such that mixer(name, seed) >> shift
// maps each field's JSON tag name to a unique slot in a power-of-2 table.
//
// Strategy: for each seed, compute all hashes once, then sweep shifts to find a
// zero-collision mapping. The search is bounded by maxSeedAttempts; callers
// fall back to a map when no perfect hash is found.
func tryBuildPerfectHash(dec *StructCodec, mixer hashMixer) bool {
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
				dec.HashSeed = seed
				dec.HashShift = shift
				dec.HashTable = make([]uint8, tableSize)
				for i := range dec.HashTable {
					dec.HashTable[i] = 0xFF
				}
				for i, h := range hashes {
					slot := (h >> shift) & mask
					dec.HashTable[slot] = uint8(i)
				}
				return true
			}
		}
	}
	return false
}

// buildMapFallback initializes the traditional map[string]*TypeInfo for large structs.
func buildMapFallback(dec *StructCodec) {
	dec.FieldMap = make(map[string]*TypeInfo, len(dec.Fields))
	for i := range dec.Fields {
		dec.FieldMap[dec.Fields[i].JSONName] = &dec.Fields[i]
	}
	dec.LookupFn = lookupMap
	dec.LookupMode = lookupModeMap
}

// Lookup strategies (one is selected per struct by buildLookup).

// lookupEmpty always returns nil (zero-field struct).
func lookupEmpty(_ *StructCodec, _ string) *TypeInfo {
	return nil
}

// lookupLinear performs a linear scan over 1-4 fields.
func lookupLinear(dec *StructCodec, key string) *TypeInfo {
	fields := dec.Fields
	for i := range fields {
		if fields[i].JSONName == key {
			return &fields[i]
		}
	}
	return nil
}

func lookupPerfectByHash(dec *StructCodec, key string, h uint64) *TypeInfo {
	slot := int(h>>dec.HashShift) & (len(dec.HashTable) - 1)

	idx := dec.HashTable[slot]
	if idx == 0xFF {
		return nil
	}

	fi := &dec.Fields[idx]
	if fi.JSONName == key {
		return fi
	}
	return nil
}

// makePerfectHashLookup returns a lookup function bound to a specific mixer.
func makePerfectHashLookup(mixer hashMixer) func(*StructCodec, string) *TypeInfo {
	return func(dec *StructCodec, key string) *TypeInfo {
		return lookupPerfectByHash(dec, key, mixer(key, dec.HashSeed))
	}
}

// lookupMap uses the fallback map for large structs.
func lookupMap(dec *StructCodec, key string) *TypeInfo {
	return dec.FieldMap[key]
}

// Hash mixers.

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
