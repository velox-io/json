package vdec

import (
	"math/bits"
	"strings"
	"unsafe"
)

type fieldLookup interface {
	lookup(si *DecStructInfo, key string) *DecFieldInfo
}

type emptyLookup struct{}

func (emptyLookup) lookup(_ *DecStructInfo, _ string) *DecFieldInfo { return nil }

// bitmapLookup8 uses a per-position character bitmap for ≤8 fields.
type bitmapLookup8 struct {
	maxKeyLen uint8
	bitmap    []uint8 // flattened [maxKeyLen][256], row-major
	lenMask   []uint8 // lenMask[len] = bitmask of fields with this length
}

func (b *bitmapLookup8) lookup(dec *DecStructInfo, key string) *DecFieldInfo {
	klen := len(key)
	if klen == 0 || klen > int(b.maxKeyLen) {
		return nil
	}
	bitmap := b.bitmap
	lenMask := b.lenMask
	cur := uint8(0xFF)
	for i := range klen {
		cur &= sliceAt(bitmap, i*256+int(key[i]))
		if cur == 0 {
			return nil
		}
	}
	cur &= sliceAt(lenMask, klen)
	if cur == 0 {
		return nil
	}
	idx := bits.TrailingZeros8(cur)
	return &dec.Fields[idx]
}

func buildBitmapLookup8(dec *DecStructInfo) *bitmapLookup8 {
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

	for i := range n {
		name := dec.Fields[i].JSONName
		bit := uint8(1) << i
		for j := 0; j < len(name); j++ {
			b.bitmap[j*256+int(name[j])] |= bit
		}
		b.lenMask[len(name)] |= bit
	}

	return b
}

type perfectHashBase struct {
	seed  uint64
	shift uint8
	table []uint8 // indices into Fields[]; 0xFF = empty
}

func (b *perfectHashBase) lookupByHash(dec *DecStructInfo, key string, h uint64) *DecFieldInfo {
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

type perfectSimpleLookup struct{ perfectHashBase }

func (p *perfectSimpleLookup) lookup(dec *DecStructInfo, key string) *DecFieldInfo {
	return p.lookupByHash(dec, key, simpleMixer(key, p.seed))
}

type perfectFNVLookup struct{ perfectHashBase }

func (p *perfectFNVLookup) lookup(dec *DecStructInfo, key string) *DecFieldInfo {
	return p.lookupByHash(dec, key, fnv1aMixer(key, p.seed))
}

type perfectMulaccLookup struct{ perfectHashBase }

func (p *perfectMulaccLookup) lookup(dec *DecStructInfo, key string) *DecFieldInfo {
	return p.lookupByHash(dec, key, mulaccMixer(key, p.seed))
}

type mapLookup struct {
	m map[string]*DecFieldInfo
}

func (l *mapLookup) lookup(_ *DecStructInfo, key string) *DecFieldInfo {
	return l.m[key]
}

func buildDecLookup(dec *DecStructInfo) {
	for i := range dec.Fields {
		if dec.Fields[i].JSONName != toLowerASCII(dec.Fields[i].JSONName) {
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

const maxSeedAttempts = 1 << 16

type hashMixer func(s string, seed uint64) uint64

func tryBuildPerfectHash(dec *DecStructInfo, mixer hashMixer) (perfectHashBase, bool) {
	n := len(dec.Fields)
	tableSize := nextPowerOf2(n * 2)
	mask := uint64(tableSize - 1)

	names := make([]string, n)
	for i := range dec.Fields {
		names[i] = dec.Fields[i].JSONName
	}

	hashes := make([]uint64, n)
	seen := make([]uint8, tableSize)
	gen := uint8(1)

	for seed := range uint64(maxSeedAttempts) {
		for i, name := range names {
			hashes[i] = mixer(name, seed)
		}

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

func buildMapFallback(dec *DecStructInfo) {
	m := make(map[string]*DecFieldInfo, len(dec.Fields))
	for i := range dec.Fields {
		m[dec.Fields[i].JSONName] = &dec.Fields[i]
	}
	dec.Lookup = &mapLookup{m: m}
}

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

func fnv1aMixer(s string, seed uint64) uint64 {
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

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca == cb {
			continue
		}
		if ca >= 0x80 || cb >= 0x80 {
			return strings.EqualFold(a, b)
		}
		if ca|0x20 != cb|0x20 {
			return false
		}
		if ca|0x20 < 'a' || ca|0x20 > 'z' {
			return false
		}
	}
	return true
}

func toLowerASCII(s string) string {
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

const (
	swarLo64 = uint64(0x0101010101010101)
	swarHi64 = uint64(0x8080808080808080)
)

func hasUpperASCII(key []byte) bool {
	const (
		addLo = (0x80 - 0x5B) * swarLo64
		addHi = (0x80 - 0x41) * swarLo64
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

func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
