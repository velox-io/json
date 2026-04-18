// Field-name-to-index lookup table, read directly by C via fieldLookupABI.
//
// Data ownership: builtLookup holds the bitmap / lenMask / perfectTable
// slices. bindTypeInfo references builtLookup via LookupKeeper to prevent
// GC from collecting the slices while C holds raw pointers to them. The
// fieldLookupABI stores only unsafe.Pointer data heads.

package ndec

import "unsafe"

// builtLookup is the Go-side full lookup instance. The abi field is the
// C-visible view; other slices are owned by builtLookup for liveness.
type builtLookup struct {
	abi          fieldLookupABI
	bitmap       []uint8    // BITMAP8: maxKeyLen * 256 bytes
	lenMask      []uint8    // BITMAP8: maxKeyLen + 1 bytes
	perfectTable []uint8    // PERFECT: 1<<tableSizeLog2 bytes; 0xFF marks empty slot
	mapEntries   []mapEntry // MAP: sorted by hash ascending
}

// Three hash mixer kinds, shared with vdec.
const (
	mixerSimple uint8 = 0
	mixerFNV1a  uint8 = 1
	mixerMulacc uint8 = 2
)

func mixerSimpleHash(s string, seed uint64) uint64 {
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

func mixerFNV1aHash(s string, seed uint64) uint64 {
	h := seed ^ 0xcbf29ce484222325
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 0x100000001b3
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

func mixerMulaccHash(s string, seed uint64) uint64 {
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

const maxPerfectSeedAttempts = 1 << 16

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// buildLookupBitmap8 handles 1 to 8 fields.
// Lookup: AND each position's mask across all bytes, AND with lenMask;
// ctz of the remaining bits gives the field index.
func buildLookupBitmap8(names []string) *builtLookup {
	maxLen := 0
	for _, name := range names {
		if l := len(name); l > maxLen {
			maxLen = l
		}
	}

	bitmap := make([]uint8, maxLen*256)
	lenMask := make([]uint8, maxLen+1)
	for i, name := range names {
		bit := uint8(1) << uint(i)
		for j := 0; j < len(name); j++ {
			bitmap[j*256+int(name[j])] |= bit
		}
		lenMask[len(name)] |= bit
	}

	bl := &builtLookup{
		bitmap:  bitmap,
		lenMask: lenMask,
	}
	bl.abi = fieldLookupABI{
		Kind:         uint8(flkBitmap8),
		HasMixedCase: boolByte(hasAnyMixedCase(names)),
		MaxKeyLen:    uint8(maxLen),
		Bitmap:       unsafe.Pointer(unsafe.SliceData(bitmap)),
		LenMask:      unsafe.Pointer(unsafe.SliceData(lenMask)),
	}
	return bl
}

// tryBuildPerfectHash attempts to find a (seed, shift) pair using the given
// mixer such that hashes of n names have no collisions in tableSize slots.
func tryBuildPerfectHash(names []string, mixer func(string, uint64) uint64) (seed uint64, shift uint8, table []uint8, ok bool) {
	n := len(names)
	tableSize := nextPowerOf2(n * 2)
	mask := uint64(tableSize - 1)

	hashes := make([]uint64, n)
	seen := make([]uint8, tableSize)
	gen := uint8(1)

	for s := range uint64(maxPerfectSeedAttempts) {
		for i, name := range names {
			hashes[i] = mixer(name, s)
		}
		for sh := range uint8(64) {
			if gen == 255 {
				for i := range seen {
					seen[i] = 0
				}
				gen = 1
			} else {
				gen++
			}
			collision := false
			for _, h := range hashes {
				slot := (h >> sh) & mask
				if seen[slot] == gen {
					collision = true
					break
				}
				seen[slot] = gen
			}
			if !collision {
				tab := make([]uint8, tableSize)
				for i := range tab {
					tab[i] = 0xFF
				}
				for i, h := range hashes {
					slot := (h >> sh) & mask
					tab[slot] = uint8(i)
				}
				return s, sh, tab, true
			}
		}
	}
	return 0, 0, nil, false
}

// buildLookupPerfect handles 9 to 32 fields. Three mixers are tried in
// order; all failing returns nil (caller falls back to MAP).
func buildLookupPerfect(names []string) *builtLookup {
	mixers := []struct {
		id uint8
		fn func(string, uint64) uint64
	}{
		{mixerSimple, mixerSimpleHash},
		{mixerFNV1a, mixerFNV1aHash},
		{mixerMulacc, mixerMulaccHash},
	}
	for _, m := range mixers {
		seed, shift, table, ok := tryBuildPerfectHash(names, m.fn)
		if !ok {
			continue
		}
		bl := &builtLookup{perfectTable: table}
		bl.abi = fieldLookupABI{
			Kind:          uint8(flkPerfect),
			HasMixedCase:  boolByte(hasAnyMixedCase(names)),
			HashSeed:      seed,
			HashShift:     shift,
			HashMixer:     m.id,
			TableSizeLog2: uint16(log2u(uint(len(table)))),
			PerfectTable:  unsafe.Pointer(unsafe.SliceData(table)),
		}
		return bl
	}
	return nil
}

func log2u(n uint) uint {
	var k uint
	for (uint(1) << k) < n {
		k++
	}
	return k
}

type mapEntry struct {
	NamePtr unsafe.Pointer // off  0  *uint8
	NameLen uint32         // off  8
	Idx     uint32         // off 12
	Hash    uint64         // off 16
}

// buildLookupMap handles n > 32 fields. FNV1a hash has negligible 64-bit
// collision probability; a memcmp verification after hit ensures correctness.
func buildLookupMap(names []string) *builtLookup {
	entries := make([]mapEntry, len(names))
	for i, name := range names {
		entries[i] = mapEntry{
			NamePtr: unsafe.Pointer(unsafe.StringData(name)),
			NameLen: uint32(len(name)),
			Idx:     uint32(i),
			Hash:    mixerFNV1aHash(name, 0),
		}
	}
	// Sort by hash ascending. Simple insertion sort: n is typically < 1000,
	// O(n^2) build-time cost is acceptable.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].Hash > entries[j].Hash; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}

	bl := &builtLookup{}
	bl.mapEntries = entries
	bl.abi = fieldLookupABI{
		Kind:         uint8(flkMap),
		HasMixedCase: boolByte(hasAnyMixedCase(names)),
		EntryCount:   uint32(len(entries)),
		MapEntries:   unsafe.Pointer(unsafe.SliceData(entries)),
	}
	return bl
}

// buildLookup selects the lookup strategy by field count:
//
//	n == 0       : EMPTY
//	1 <= n <= 8  : BITMAP8
//	9 <= n <= 32 : PERFECT (3 mixers tried in order)
//	n > 32       : MAP (FNV1a + binary search)
func buildLookup(names []string) *builtLookup {
	n := len(names)
	switch {
	case n == 0:
		bl := &builtLookup{}
		bl.abi.Kind = uint8(flkEmpty)
		return bl
	case n <= 8:
		return buildLookupBitmap8(names)
	case n <= 32:
		bl := buildLookupPerfect(names)
		if bl != nil {
			return bl
		}
		// All three PERFECT mixers failed (extremely rare); fall back to MAP.
		return buildLookupMap(names)
	default:
		return buildLookupMap(names)
	}
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func hasAnyMixedCase(names []string) bool {
	for _, name := range names {
		for j := 0; j < len(name); j++ {
			if c := name[j]; c >= 'A' && c <= 'Z' {
				return true
			}
		}
	}
	return false
}
