package vbind

import "unsafe"

// LookupFind reads a vlib.Build blob and returns its key index in [0, n), or -1
// on a miss. Struct blobs live in the process cache; each variant blob is rooted
// by its owning BindPolyTable in a TypeTree.
//
// The WINDOW tier probes a two-byte window that may sit past a short key's
// closing quote, which a string's backing need not hold, so short keys are
// copied into a stack buffer that supplies the quote. Longer keys can only
// match tiers that read the key alone.
func LookupFind(blob unsafe.Pointer, key string) int {
	if blob == nil || len(key) == 0 {
		return -1
	}
	if len(key) >= wKeyCap {
		// WINDOW rejects a key this long before its window read; the other
		// tiers read the key alone.
		return findAt(blob, unsafe.StringData(key), uintptr(len(key)), uintptr(len(key)))
	}
	var buf [wKeyCap]byte
	copy(buf[:], key)
	buf[len(key)] = '"'
	return findAt(blob, &buf[0], uintptr(len(key)), wKeyCap)
}

// LookupFindSpan is LookupFind over the key of n bytes at p, of which the
// first end bytes are readable and p[n] holds the closing quote. The WINDOW
// tier probes a two-byte window that may reach past the quote, so a byte at
// or past end reads as 0x20, the scan padding a native source supplies past
// its end; the probe verdict is unchanged either way, since a key short
// enough to leave the window past its quote matches no stored key.
func LookupFindSpan(blob unsafe.Pointer, p *byte, n, end int) int {
	if blob == nil || n == 0 {
		return -1
	}
	return findAt(blob, p, uintptr(n), uintptr(end))
}

// wKeyCap is the longest key the WINDOW tier stores, plus its quote.
const wKeyCap = 64

// findAt dispatches by tier. end is the readable byte count at p, which only
// windowFind consults: its probe may leave the key, while the other tiers
// read [0, klen) alone.
func findAt(blob unsafe.Pointer, p *byte, klen, end uintptr) int {
	switch *(*uint32)(blob) {
	case tierWindow:
		return windowFind(blob, p, klen, end)
	case tierGperf:
		return gperfFind(blob, p, klen)
	case tierHand:
		return handFind(blob, p, klen)
	case tierTable:
		return tableFind(blob, p, klen)
	default:
		return -1
	}
}

const (
	tierWindow uint32 = 1 << 0
	tierGperf  uint32 = 1 << 1
	tierHand   uint32 = 1 << 2
	tierTable  uint32 = 1 << 3
)

// gperfLastCh must match NDEC_LOOKUP_GPERF_LAST_CH in the native blob format.
const gperfLastCh = 0xFE

func readU8(p unsafe.Pointer, off uintptr) uint8 {
	return *(*uint8)(unsafe.Add(p, off))
}

func readU32(p unsafe.Pointer, off uintptr) uint32 {
	return *(*uint32)(unsafe.Add(p, off))
}

func readPtr(p unsafe.Pointer, off uintptr) uintptr {
	return *(*uintptr)(unsafe.Add(p, off))
}

func keyEquals(blob unsafe.Pointer, off, klen uintptr, p *byte) bool {
	stored := unsafe.String((*byte)(unsafe.Add(blob, off)), klen)
	return stored == unsafe.String(p, klen)
}

// WINDOW blob layout on the 64-bit native ABI:
//
//	kind@0(4) byte_offset@4(1) shift@5(1) cmp@8(4)
//	n@16(8) max_key_len@24(8) stride@32(8) key_bytes_off@40(8)
//	window_to_key[256]@48 key_len[n]@304
//	key_bytes[n*stride]@key_bytes_off
const (
	wOffByteOffset  = 4
	wOffShift       = 5
	wOffN           = 16
	wOffStride      = 32
	wOffKeyBytesOff = 40
	wOffWindowToKey = 48
	wSizeHeader     = 304 // sizeof(ndec_lookup_window)
)

func windowFind(blob unsafe.Pointer, p *byte, klen, end uintptr) int {
	boff := readU8(blob, wOffByteOffset)
	shift := readU8(blob, wOffShift)
	n := readPtr(blob, wOffN)
	stride := readPtr(blob, wOffStride)
	kboff := readPtr(blob, wOffKeyBytesOff)

	// WINDOW blobs store only keys of at most wKeyCap-1 bytes.
	if klen >= wKeyCap {
		return -1
	}

	// The probe window may sit past a short key's closing quote; the bytes
	// at or past end read as the scan padding byte, never memory the caller
	// did not prove readable.
	lo, hi := uintptr(' '), uintptr(' ')
	if uintptr(boff) < end {
		lo = uintptr(*(*byte)(unsafe.Add(unsafe.Pointer(p), uintptr(boff))))
	}
	if uintptr(boff)+1 < end {
		hi = uintptr(*(*byte)(unsafe.Add(unsafe.Pointer(p), uintptr(boff)+1)))
	}
	w := uint16(lo) | uint16(hi)<<8
	idx := int((w >> uint(shift)) & 0xFF)

	ki := readU8(blob, wOffWindowToKey+uintptr(idx))
	if uintptr(ki) >= n {
		return -1
	}

	storedKlen := readU8(blob, wSizeHeader+uintptr(ki))
	if uintptr(storedKlen) != klen {
		return -1
	}

	off := kboff + uintptr(ki)*stride
	if keyEquals(blob, off, klen, p) {
		return int(ki)
	}
	return -1
}

// GPERF blob layout on the 64-bit native ABI:
//
//	kind@0(4) cmp@4(4) num_positions@8(1) n@16(8) max_key_len@24(8)
//	table_size@32(8) stride@40(8) positions[8]@48 asso_off@56(8)
//	slots_off@64(8) key_len_off@72(8) key_bytes_off@80(8)
//	asso_values[num_positions*256]@asso_off slot_to_key[table_size]@slots_off
//	key_len[n]@key_len_off key_bytes[n*stride]@key_bytes_off
const (
	gOffN           = 16
	gOffTableSize   = 32
	gOffStride      = 40
	gOffPositions   = 48
	gOffAssoOff     = 56
	gOffSlotsOff    = 64
	gOffKeyLenOff   = 72
	gOffKeyBytesOff = 80
)

func gperfFind(blob unsafe.Pointer, p *byte, klen uintptr) int {
	np := readU8(blob, 8)
	n := readPtr(blob, gOffN)
	tableSize := readPtr(blob, gOffTableSize)
	stride := readPtr(blob, gOffStride)
	assoOff := readPtr(blob, gOffAssoOff)
	slotsOff := readPtr(blob, gOffSlotsOff)
	klenOff := readPtr(blob, gOffKeyLenOff)
	kboff := readPtr(blob, gOffKeyBytesOff)

	var h = klen
	positions := unsafe.Add(blob, gOffPositions)
	asso := unsafe.Add(blob, assoOff)
	for i := range np {
		pos := *(*uint8)(unsafe.Add(positions, uintptr(i)))
		var idx uintptr
		if pos == gperfLastCh {
			idx = klen - 1
		} else {
			idx = uintptr(pos)
		}
		if idx < klen {
			ch := *(*uint8)(unsafe.Add(unsafe.Pointer(p), idx))
			h += uintptr(*(*uint8)(unsafe.Add(asso, uintptr(i)*256+uintptr(ch))))
		}
	}

	slot := h & (tableSize - 1)
	ki := *(*uint8)(unsafe.Add(blob, slotsOff+slot))
	if uintptr(ki) >= n {
		return -1
	}

	storedKlen := readU8(blob, klenOff+uintptr(ki))
	if uintptr(storedKlen) != klen {
		return -1
	}

	off := kboff + uintptr(ki)*stride
	if keyEquals(blob, off, klen, p) {
		return int(ki)
	}
	return -1
}

// HAND blob layout on the 64-bit native ABI:
//
//	kind@0(4) cmp@4(4) variant@8(4) n@16(8) max_key_len@24(8)
//	table_size@32(8) stride@40(8) key_bytes_off@48(8) mask@56(8)
//	displacement[256]@64 slot_to_key[512]@320 key_len[n]@832
//	key_bytes[n*stride]@key_bytes_off
const (
	hOffVariant     = 8
	hOffN           = 16
	hOffMask        = 56
	hOffDispl       = 64
	hOffSlotToKey   = 320
	hOffKeyBytesOff = 48
	hSizeHeader     = 832 // sizeof(ndec_lookup_hand)
)

func handFind(blob unsafe.Pointer, p *byte, klen uintptr) int {
	variant := readU32(blob, hOffVariant)
	n := readPtr(blob, hOffN)
	mask := readPtr(blob, hOffMask) // uint64 but stored as uintptr
	kboff := readPtr(blob, hOffKeyBytesOff)

	var c0, c1 byte
	if klen > 0 {
		c0 = *(*byte)(unsafe.Pointer(p))
		c1 = *(*byte)(unsafe.Add(unsafe.Pointer(p), klen-1))
	}
	bucket := (uintptr(c0) + uintptr(c1)*3 + klen*17) & 0xFF

	var kh = klen
	if klen > 0 {
		kh = kh*31 + uintptr(c0)
	}
	kh = kh*31 + uintptr(safeChar(p, klen, 1))
	if variant == 1 {
		kh = kh*31 + uintptr(safeChar(p, klen, 2))
		kh = kh*31 + uintptr(safeChar(p, klen, 3))
	}

	displ := readU8(blob, hOffDispl+bucket)
	slot := (uintptr(displ) + kh) & mask
	ki := readU8(blob, hOffSlotToKey+slot)
	if uintptr(ki) >= n {
		return -1
	}

	storedKlen := readU8(blob, hSizeHeader+uintptr(ki))
	if uintptr(storedKlen) != klen {
		return -1
	}

	off := kboff + uintptr(ki)*readPtr(blob, 40)
	if keyEquals(blob, off, klen, p) {
		return int(ki)
	}
	return -1
}

func safeChar(p *byte, length, idx uintptr) byte {
	if idx < length {
		return *(*byte)(unsafe.Add(unsafe.Pointer(p), idx))
	}
	return 0
}

// TABLE blob layout on the 64-bit native ABI:
//
//	kind@0(4) n@8(8) cap@16(8) mask@24(8)
//	key_data_off@32(8) key_data_size@40(8) slots[cap]@48
//
// Each slot is 8 bytes: key_off(uint32)@0, key_len(uint16)@4, and
// value_p1(uint16)@6. key_off is relative to the blob base. Key bytes begin at
// key_data_off.
const (
	tOffMask  = 24
	tOffSlots = 48
)

func tableFind(blob unsafe.Pointer, p *byte, klen uintptr) int {
	mask := readPtr(blob, tOffMask)

	h := tableHash(p, klen)
	pos := uintptr(h & uint64(mask))
	for {
		slotBase := tOffSlots + pos*8
		valueP1 := *(*uint16)(unsafe.Add(blob, slotBase+6))
		if valueP1 == 0 {
			return -1
		}
		keyLen := *(*uint16)(unsafe.Add(blob, slotBase+4))
		if uintptr(keyLen) == klen {
			keyOff := *(*uint32)(unsafe.Add(blob, slotBase))
			if keyEquals(blob, uintptr(keyOff), klen, p) {
				return int(valueP1) - 1
			}
		}
		pos = (pos + 1) & mask
	}
}

func tableHash(p *byte, klen uintptr) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := uintptr(0); i < klen; i++ {
		h ^= uint64(*(*byte)(unsafe.Add(unsafe.Pointer(p), i)))
		h *= 0x100000001b3
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}
