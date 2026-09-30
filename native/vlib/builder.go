package vlib

import "encoding/binary"

// Build constructs a lookup blob for keys, producing the same bytes
// ndec_lookup_init would produce, so native and Go readers share one ABI.
// tiers selects the candidate tier set; zero means TiersAll. On success it
// returns the blob and the selected Tier* flag. On failure it returns nil
// and a negative code from the Err* set.
//
// The blob layout constants below mirror native/vlib/impl/vlib/lookup.h on
// the 64-bit ABI. All multi-byte fields are little-endian.
func Build(keys []string, tiers uint32) ([]byte, int32) {
	if tiers == 0 {
		tiers = TiersAll
	}
	maxLen, code := validateKeys(keys, tiers)
	if code != 0 {
		return nil, code
	}
	n := len(keys)
	totalKeyBytes := 0
	for _, k := range keys {
		totalKeyBytes += len(k)
	}
	maxNeeded := 0
	for _, tier := range tierOrder {
		if tiers&tier == 0 {
			continue
		}
		if s := sizeForTier(tier, n, maxLen, totalKeyBytes); s > maxNeeded {
			maxNeeded = s
		}
	}
	blob := make([]byte, maxNeeded)
	for _, tier := range tierOrder {
		if tiers&tier == 0 {
			continue
		}
		if sizeForTier(tier, n, maxLen, totalKeyBytes) == 0 {
			continue
		}
		putU32(blob, 0, tier)
		if buildTier(tier, blob, keys) {
			return blob, int32(tier)
		}
	}
	return nil, ErrNoTierMatches
}

var tierOrder = [4]uint32{TierWindow, TierGperf, TierHand, TierTable}

const (
	maxKeys      = 255
	keyStrideMax = 64
	gperfMaxPos  = 8
	gperfMaxTab  = 512
	gperfLastCh  = 0xFE
	handMaxTable = 512
)

// WINDOW header: kind@0(4) byte_offset@4(1) shift@5(1) cmp@8(4)
// n@16 max_key_len@24 stride@32 key_bytes_off@40 window_to_key[256]@48
const (
	wOffCmp         = 8
	wOffN           = 16
	wOffMaxKeyLen   = 24
	wOffStride      = 32
	wOffKeyBytesOff = 40
	wOffWindowToKey = 48
	wHdrSize        = 304
)

// GPERF header: kind@0(4) cmp@4(4) num_positions@8(1)
// n@16 max_key_len@24 table_size@32 stride@40 positions[8]@48
// asso_off@56 slots_off@64 key_len_off@72 key_bytes_off@80
const (
	gOffCmp          = 4
	gOffNumPositions = 8
	gOffN            = 16
	gOffMaxKeyLen    = 24
	gOffTableSize    = 32
	gOffStride       = 40
	gOffPositions    = 48
	gOffAssoOff      = 56
	gOffSlotsOff     = 64
	gOffKeyLenOff    = 72
	gOffKeyBytesOff  = 80
	gHdrSize         = 88
)

// HAND header: kind@0(4) cmp@4(4) variant@8(4)
// n@16 max_key_len@24 table_size@32 stride@40 key_bytes_off@48 mask@56
// displacement[256]@64 slot_to_key[512]@320
const (
	hOffCmp          = 4
	hOffVariant      = 8
	hOffN            = 16
	hOffMaxKeyLen    = 24
	hOffTableSize    = 32
	hOffStride       = 40
	hOffKeyBytesOff  = 48
	hOffMask         = 56
	hOffDisplacement = 64
	hOffSlotToKey    = 320
	hHdrSize         = 832
)

// TABLE header: kind@0(4) n@8 cap@16 mask@24 key_data_off@32
// key_data_size@40 slots[cap]@48; each slot is key_off(4) key_len(2) value_p1(2)
const (
	tOffN           = 8
	tOffCap         = 16
	tOffMask        = 24
	tOffKeyDataOff  = 32
	tOffKeyDataSize = 40
	tOffSlots       = 48
	tHdrSize        = 48
)

func putU32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
func putU64(b []byte, off int, v uint64) { binary.LittleEndian.PutUint64(b[off:], v) }
func putU16(b []byte, off int, v uint16) { binary.LittleEndian.PutUint16(b[off:], v) }
func putU8(b []byte, off int, v uint8)   { b[off] = v }

func cmpKindFor(maxKeyLen int) uint32 {
	switch {
	case maxKeyLen <= 16:
		return 0
	case maxKeyLen <= 32:
		return 1
	case maxKeyLen <= 48:
		return 2
	default:
		return 3
	}
}

func strideFor(maxKeyLen int) int {
	switch {
	case maxKeyLen <= 16:
		return 16
	case maxKeyLen <= 32:
		return 32
	case maxKeyLen <= 48:
		return 48
	default:
		return 64
	}
}

func roundUp16(v int) int { return (v + 15) &^ 15 }

func nextPow2(v int) int {
	p := 1
	for p < v {
		p <<= 1
	}
	return p
}

func tableCap(n int) int {
	c := 16
	for c < n*2 {
		c <<= 1
	}
	return c
}

func validateKeys(keys []string, tiers uint32) (maxLen int, code int32) {
	if len(keys) == 0 {
		return 0, ErrKeysEmpty
	}
	if len(keys) > maxKeys {
		return 0, ErrKeysTooMany
	}
	allowOver63 := tiers&TierTable != 0
	for _, k := range keys {
		if len(k) == 0 {
			return 0, ErrKeyEmpty
		}
		if len(k) >= keyStrideMax && !allowOver63 {
			return 0, ErrKeyTooLong
		}
		for i := 0; i < len(k); i++ {
			if c := k[i]; c == 0x00 || c == 0x22 || c == 0x5C {
				return 0, ErrKeyInvalidByte
			}
		}
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] == keys[j] {
				return 0, ErrKeyDuplicate
			}
		}
	}
	return maxLen, 0
}

func sizeForTier(tier uint32, n, maxLen, totalKeyBytes int) int {
	switch tier {
	case TierWindow:
		return roundUp16(wHdrSize+n) + n*strideFor(maxLen)
	case TierGperf:
		off := roundUp16(gHdrSize + gperfMaxPos*256)
		off += gperfMaxTab
		off = roundUp16(off + n)
		return off + n*strideFor(maxLen)
	case TierHand:
		return roundUp16(hHdrSize+n) + n*strideFor(maxLen)
	case TierTable:
		cap := tableCap(n)
		if cap > 65536 {
			return 0
		}
		return tHdrSize + cap*8 + totalKeyBytes
	}
	return 0
}

func buildTier(tier uint32, blob []byte, keys []string) bool {
	switch tier {
	case TierWindow:
		return windowBuild(blob, keys)
	case TierGperf:
		return gperfBuild(blob, keys)
	case TierHand:
		return handBuild(blob, keys)
	case TierTable:
		return tableBuild(blob, keys)
	}
	return false
}

// ---- Tier 1: window ----

// windowByte reads the byte of key i at idx, substituting the JSON quote
// for positions past the end so a window may sit at the key's tail.
func windowByte(keys []string, i, idx int) byte {
	if idx < len(keys[i]) {
		return keys[i][idx]
	}
	return '"'
}

func windowInitForward(blob []byte, keys []string, minLen int) bool {
	n := len(keys)
	for off := 0; off <= minLen; off++ {
		for shift := 0; shift < 8; shift++ {
			if shift != 0 && off+1 > minLen {
				continue
			}
			distinct := true
			for i := 0; i < n && distinct; i++ {
				for j := i + 1; j < n; j++ {
					vi := ((uint32(windowByte(keys, i, off)) | uint32(windowByte(keys, i, off+1))<<8) >> uint(shift)) & 0xFF
					vj := ((uint32(windowByte(keys, j, off)) | uint32(windowByte(keys, j, off+1))<<8) >> uint(shift)) & 0xFF
					if vi == vj {
						distinct = false
						break
					}
				}
			}
			if !distinct {
				continue
			}
			blob[4] = uint8(off)
			blob[5] = uint8(shift)
			for b := 0; b < 256; b++ {
				blob[wOffWindowToKey+b] = uint8(n)
			}
			for i := 0; i < n; i++ {
				val := ((uint32(windowByte(keys, i, off)) | uint32(windowByte(keys, i, off+1))<<8) >> uint(shift)) & 0xFF
				blob[wOffWindowToKey+int(val)] = uint8(i)
			}
			return true
		}
	}
	return false
}

func windowBuild(blob []byte, keys []string) bool {
	n := len(keys)
	if n == 0 || n > maxKeys {
		return false
	}
	minLen, maxLen := len(keys[0]), len(keys[0])
	for _, k := range keys[1:] {
		if len(k) < minLen {
			minLen = len(k)
		}
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	if maxLen >= keyStrideMax {
		return false
	}
	putU64(blob, wOffN, uint64(n))
	putU64(blob, wOffMaxKeyLen, uint64(maxLen))
	if !windowInitForward(blob, keys, minLen) {
		return false
	}
	stride := strideFor(maxLen)
	putU32(blob, wOffCmp, cmpKindFor(maxLen))
	putU64(blob, wOffStride, uint64(stride))
	kbo := roundUp16(wHdrSize + n)
	putU64(blob, wOffKeyBytesOff, uint64(kbo))
	writeKeyBlock(blob, keys, wHdrSize, kbo, stride)
	return true
}

// writeKeyBlock stores each key's length at klenOff and zero-pads the key
// bytes into per-key stride slots at kbo, the layout shared by WINDOW,
// GPERF, and HAND.
func writeKeyBlock(blob []byte, keys []string, klenOff, kbo, stride int) {
	for i, k := range keys {
		blob[klenOff+i] = uint8(len(k))
		copy(blob[kbo+i*stride:], k)
	}
}

// ---- Tier 2: gperf ----

type gperfScratch struct {
	numPositions uint8
	positions    [gperfMaxPos]uint8
	asso         [gperfMaxPos][256]uint8
	slotToKey    [gperfMaxTab]uint8
}

// gperfSym is a (position, byte) pair the greedy asso assignment prices by
// its frequency across keys.
type gperfSym struct {
	pos  uint8
	ch   int
	freq int
}

type gperfWorkspace struct {
	out     gperfScratch
	kchars  [maxKeys][gperfMaxPos]int
	syms    [gperfMaxPos * 256]gperfSym
	salt    [gperfMaxPos][256]uint64
	sig     [maxKeys]uint64
	phash   [maxKeys]int
	order   [maxKeys]int
	slotGen [gperfMaxTab]int
	freq    [257]int
}

// gperfCharAt reads the key byte a gperf position names. The LAST_CH
// position addresses the final byte; a position past the end reads 256,
// which no symbol's byte range contains, so it never matches.
func gperfCharAt(key string, pos uint8) int {
	if pos == gperfLastCh {
		return int(key[len(key)-1])
	}
	if int(pos) >= len(key) {
		return 256
	}
	return int(key[pos])
}

// gperfUndistinguishedPairs counts key pairs sharing len%modulus that the
// selected positions fail to separate.
func gperfUndistinguishedPairs(keys []string, positions []uint8, modulus int) int {
	n := len(keys)
	count := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if len(keys[i])%modulus != len(keys[j])%modulus {
				continue
			}
			distinguished := false
			for p := range positions {
				if gperfCharAt(keys[i], positions[p]) != gperfCharAt(keys[j], positions[p]) {
					distinguished = true
					break
				}
			}
			if !distinguished {
				count++
			}
		}
	}
	return count
}

// gperfSelectPositions greedily grows the position set until every
// len%modulus key pair is separated, up to gperfMaxPos positions. It
// returns the set size, or -1 when the positions cannot separate the keys.
// The final blob copies the full positions array, so tentative writes the
// greedy leaves past the set size become blob bytes and are reproduced
// rather than cleaned up.
func gperfSelectPositions(keys []string, maxKeyLen, modulus int, out *[gperfMaxPos]uint8) int {
	candidates := make([]uint8, 0, maxKeyLen+1)
	for p := 0; p < maxKeyLen && len(candidates) < 256; p++ {
		candidates = append(candidates, uint8(p))
	}
	candidates = append(candidates, gperfLastCh)

	numPos := 0
	if gperfUndistinguishedPairs(keys, out[:0], modulus) == 0 {
		return 0
	}
	for numPos < gperfMaxPos {
		bestReduction := 0
		bestVal := uint8(0)
		haveBest := false
		before := gperfUndistinguishedPairs(keys, out[:numPos], modulus)
		for _, cand := range candidates {
			already := false
			for p := 0; p < numPos; p++ {
				if out[p] == cand {
					already = true
					break
				}
			}
			if already {
				continue
			}
			out[numPos] = cand
			after := gperfUndistinguishedPairs(keys, out[:numPos+1], modulus)
			if reduction := before - after; reduction > bestReduction {
				bestReduction = reduction
				bestVal = cand
				haveBest = true
			}
		}
		if !haveBest {
			break
		}
		out[numPos] = bestVal
		numPos++
		if gperfUndistinguishedPairs(keys, out[:numPos], modulus) == 0 {
			return numPos
		}
	}
	if gperfUndistinguishedPairs(keys, out[:numPos], modulus) != 0 {
		return -1
	}
	return numPos
}

func gperfTry(ws *gperfWorkspace, maxKeyLen int, keys []string, modulus int) bool {
	n := len(keys)
	gs := &ws.out
	np := gperfSelectPositions(keys, maxKeyLen, modulus, &gs.positions)
	if np < 0 {
		return false
	}
	gs.numPositions = uint8(np)
	gs.asso = [gperfMaxPos][256]uint8{}

	if np == 0 {
		for s := 0; s < modulus; s++ {
			gs.slotToKey[s] = uint8(n)
		}
		for i, k := range keys {
			slot := len(k) % modulus
			if gs.slotToKey[slot] != uint8(n) {
				return false
			}
			gs.slotToKey[slot] = uint8(i)
		}
		return true
	}

	for k := 0; k < n; k++ {
		for p := 0; p < np; p++ {
			ws.kchars[k][p] = gperfCharAt(keys[k], gs.positions[p])
		}
	}

	nsyms := 0
	for p := 0; p < np; p++ {
		clear(ws.freq[:])
		for k := 0; k < n; k++ {
			if c := ws.kchars[k][p]; c < 256 {
				ws.freq[c]++
			}
		}
		for c := 0; c < 256; c++ {
			if ws.freq[c] > 0 {
				ws.syms[nsyms] = gperfSym{pos: uint8(p), ch: c, freq: ws.freq[c]}
				nsyms++
			}
		}
	}
	for i := 1; i < nsyms; i++ {
		x := ws.syms[i]
		j := i
		for j > 0 && ws.syms[j-1].freq < x.freq {
			ws.syms[j] = ws.syms[j-1]
			j--
		}
		ws.syms[j] = x
	}

	s := uint64(0x9e3779b97f4a7c15)
	for p := 0; p < np; p++ {
		for c := 0; c < 256; c++ {
			s = s*6364136223846793005 + 1442695040888963407
			ws.salt[p][c] = s
		}
	}
	for k := 0; k < n; k++ {
		var sg uint64
		for p := 0; p < np; p++ {
			if c := ws.kchars[k][p]; c < 256 {
				sg ^= ws.salt[p][c]
			}
		}
		ws.sig[k] = sg
	}

	for k := 0; k < n; k++ {
		ws.phash[k] = len(keys[k])
		ws.order[k] = k
	}
	clear(ws.slotGen[:modulus])
	gen := 0

	searchLimit := modulus
	if searchLimit < 32 {
		searchLimit = 32
	}

	for si := 0; si < nsyms; si++ {
		sp := ws.syms[si].pos
		sc := ws.syms[si].ch
		spSalt := ws.salt[sp][sc]

		for k := 0; k < n; k++ {
			if ws.kchars[k][sp] == sc {
				ws.sig[k] ^= spSalt
			}
		}
		for i := 1; i < n; i++ {
			x := ws.order[i]
			xs := ws.sig[x]
			j := i
			for j > 0 && ws.sig[ws.order[j-1]] > xs {
				ws.order[j] = ws.order[j-1]
				j--
			}
			ws.order[j] = x
		}

		found := false
		for v := 0; v < searchLimit && !found; v++ {
			collision := false
			ci := 0
			for ci < n && !collision {
				classSig := ws.sig[ws.order[ci]]
				cj := ci
				for cj < n && ws.sig[ws.order[cj]] == classSig {
					cj++
				}
				if cj-ci > 1 {
					gen++
					for x := ci; x < cj; x++ {
						k := ws.order[x]
						h := ws.phash[k]
						if ws.kchars[k][sp] == sc {
							h += v
						}
						h &= modulus - 1
						if ws.slotGen[h] == gen {
							collision = true
							break
						}
						ws.slotGen[h] = gen
					}
				}
				ci = cj
			}
			if !collision {
				gs.asso[sp][sc] = uint8(v)
				for k := 0; k < n; k++ {
					if ws.kchars[k][sp] == sc {
						ws.phash[k] += v
					}
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}

	for s := 0; s < modulus; s++ {
		gs.slotToKey[s] = uint8(n)
	}
	for i := 0; i < n; i++ {
		slot := ws.phash[i] & (modulus - 1)
		if gs.slotToKey[slot] != uint8(n) {
			return false
		}
		gs.slotToKey[slot] = uint8(i)
	}
	return true
}

func gperfBuild(blob []byte, keys []string) bool {
	n := len(keys)
	if n == 0 || n > maxKeys {
		return false
	}
	maxLen := 0
	for _, k := range keys {
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	if maxLen >= keyStrideMax {
		return false
	}
	putU64(blob, gOffN, uint64(n))
	putU64(blob, gOffMaxKeyLen, uint64(maxLen))

	ws := new(gperfWorkspace)
	for m := nextPow2(n); m <= gperfMaxTab; m <<= 1 {
		if !gperfTry(ws, maxLen, keys, m) {
			continue
		}
		gs := &ws.out
		np := int(gs.numPositions)
		stride := strideFor(maxLen)
		putU8(blob, gOffNumPositions, gs.numPositions)
		putU64(blob, gOffTableSize, uint64(m))
		putU32(blob, gOffCmp, cmpKindFor(maxLen))
		putU64(blob, gOffStride, uint64(stride))
		copy(blob[gOffPositions:], gs.positions[:])

		assoOff := gHdrSize
		putU64(blob, gOffAssoOff, uint64(assoOff))
		off := roundUp16(assoOff + np*256)
		putU64(blob, gOffSlotsOff, uint64(off))
		copy(blob[off:off+m], gs.slotToKey[:m])
		off = roundUp16(off + m)
		keyLenOff := off
		putU64(blob, gOffKeyLenOff, uint64(keyLenOff))
		off = roundUp16(off + n)
		kbo := off
		putU64(blob, gOffKeyBytesOff, uint64(kbo))

		for p := 0; p < np; p++ {
			copy(blob[assoOff+p*256:], gs.asso[p][:])
		}
		writeKeyBlock(blob, keys, keyLenOff, kbo, stride)
		return true
	}
	return false
}

// ---- Tier 3: hash-and-displace ----

type handWorkspace struct {
	bucketOf    [maxKeys]int
	buckets     [256]int
	counts      [256]int
	bucketKeys  [maxKeys]int
	bucketSlots [maxKeys]int
}

func handBucketHash(key string) int {
	var c0, c1 byte
	if len(key) > 0 {
		c0 = key[0]
		c1 = key[len(key)-1]
	}
	return (int(c0) + int(c1)*3 + len(key)*17) & 0xFF
}

func handSafeChar(key string, idx int) int {
	if idx < len(key) {
		return int(key[idx])
	}
	return 0
}

func handKeyHash2(key string) int {
	kc := int64(len(key))
	kc = kc*31 + int64(handSafeChar(key, 0))
	kc = kc*31 + int64(handSafeChar(key, 1))
	return int(kc)
}

func handKeyHash4(key string) int {
	kc := int64(len(key))
	kc = kc*31 + int64(handSafeChar(key, 0))
	kc = kc*31 + int64(handSafeChar(key, 1))
	kc = kc*31 + int64(handSafeChar(key, 2))
	kc = kc*31 + int64(handSafeChar(key, 3))
	return int(kc)
}

func handTryPlacement(blob []byte, keys []string, bucketOf, bucketsOrdered []int, M int, keyHash func(string) int, ws *handWorkspace) bool {
	n := len(keys)
	mask := M - 1
	clear(blob[hOffDisplacement : hOffDisplacement+256])
	for s := 0; s < M; s++ {
		blob[hOffSlotToKey+s] = uint8(n)
	}

	for b := range bucketsOrdered {
		ch := bucketsOrdered[b]
		bkCount := 0
		for i := 0; i < n; i++ {
			if bucketOf[i] == ch {
				ws.bucketKeys[bkCount] = i
				bkCount++
			}
		}
		placed := false
		maxD := M
		if maxD > 255 {
			maxD = 255
		}
		for d := 0; d < maxD && !placed; d++ {
			ok := true
			for k := 0; k < bkCount && ok; k++ {
				s := (d + keyHash(keys[ws.bucketKeys[k]])) & mask
				if blob[hOffSlotToKey+s] != uint8(n) {
					ok = false
					break
				}
				for k2 := 0; k2 < k; k2++ {
					if ws.bucketSlots[k2] == s {
						ok = false
						break
					}
				}
				ws.bucketSlots[k] = s
			}
			if ok {
				blob[hOffDisplacement+ch] = uint8(d)
				for k := 0; k < bkCount; k++ {
					blob[hOffSlotToKey+ws.bucketSlots[k]] = uint8(ws.bucketKeys[k])
				}
				placed = true
			}
		}
		if !placed {
			return false
		}
	}
	return true
}

func handTrySize(blob []byte, keys []string, M int, ws *handWorkspace) bool {
	n := len(keys)
	if M > handMaxTable {
		return false
	}
	putU64(blob, hOffTableSize, uint64(M))
	putU64(blob, hOffMask, uint64(M-1))
	putU64(blob, hOffN, uint64(n))

	for i, k := range keys {
		ws.bucketOf[i] = handBucketHash(k)
	}
	numBuckets := 0
	for i := 0; i < n; i++ {
		bk := ws.bucketOf[i]
		found := false
		for b := 0; b < numBuckets; b++ {
			if ws.buckets[b] == bk {
				ws.counts[b]++
				found = true
				break
			}
		}
		if !found {
			ws.buckets[numBuckets] = bk
			ws.counts[numBuckets] = 1
			numBuckets++
		}
	}
	for i := 1; i < numBuckets; i++ {
		for j := i; j > 0 && ws.counts[j] > ws.counts[j-1]; j-- {
			ws.buckets[j], ws.buckets[j-1] = ws.buckets[j-1], ws.buckets[j]
			ws.counts[j], ws.counts[j-1] = ws.counts[j-1], ws.counts[j]
		}
	}

	if handTryPlacement(blob, keys, ws.bucketOf[:n], ws.buckets[:numBuckets], M, handKeyHash2, ws) {
		putU32(blob, hOffVariant, 0)
		return true
	}
	if handTryPlacement(blob, keys, ws.bucketOf[:n], ws.buckets[:numBuckets], M, handKeyHash4, ws) {
		putU32(blob, hOffVariant, 1)
		return true
	}
	return false
}

func handBuild(blob []byte, keys []string) bool {
	n := len(keys)
	if n == 0 || n > maxKeys {
		return false
	}
	maxLen := 0
	for _, k := range keys {
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	if maxLen >= keyStrideMax {
		return false
	}
	putU64(blob, hOffMaxKeyLen, uint64(maxLen))

	ws := new(handWorkspace)
	for M := nextPow2(n); M <= handMaxTable; M <<= 1 {
		if !handTrySize(blob, keys, M, ws) {
			continue
		}
		stride := strideFor(maxLen)
		putU32(blob, hOffCmp, cmpKindFor(maxLen))
		putU64(blob, hOffStride, uint64(stride))
		kbo := roundUp16(hHdrSize + n)
		putU64(blob, hOffKeyBytesOff, uint64(kbo))
		writeKeyBlock(blob, keys, hHdrSize, kbo, stride)
		return true
	}
	return false
}

// ---- Tier 4: table ----

func tableHash(key string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 0x100000001b3
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

func tableBuild(blob []byte, keys []string) bool {
	n := len(keys)
	if n == 0 || n > maxKeys {
		return false
	}
	totalKeyBytes := 0
	for _, k := range keys {
		totalKeyBytes += len(k)
	}
	cap := tableCap(n)
	keyDataOff := tHdrSize + cap*8
	if keyDataOff+totalKeyBytes > len(blob) {
		return false
	}
	putU64(blob, tOffN, uint64(n))
	putU64(blob, tOffCap, uint64(cap))
	putU64(blob, tOffMask, uint64(cap-1))
	putU64(blob, tOffKeyDataOff, uint64(keyDataOff))
	putU64(blob, tOffKeyDataSize, uint64(totalKeyBytes))

	// A failed HAND attempt may have written into the slots region, so every
	// slot starts empty here rather than relying on the zeroed allocation.
	clear(blob[tOffSlots : tOffSlots+cap*8])

	writeOffset := 0
	for i, k := range keys {
		copy(blob[keyDataOff+writeOffset:], k)
		pos := int(tableHash(k) & uint64(cap-1))
		for binary.LittleEndian.Uint16(blob[tOffSlots+pos*8+6:]) != 0 {
			pos = (pos + 1) & (cap - 1)
		}
		putU32(blob, tOffSlots+pos*8, uint32(keyDataOff+writeOffset))
		putU16(blob, tOffSlots+pos*8+4, uint16(len(k)))
		putU16(blob, tOffSlots+pos*8+6, uint16(i+1))
		writeOffset += len(k)
	}
	return true
}
