package vbind

import "unsafe"

// LookupFind reads a vlib.Init blob and returns its key index in [0, n), or -1
// on a miss. Struct blobs live in the process cache; each variant blob is rooted
// by its owning BindVariantTable in a TypeTree.
//
// The blob layout is the native lookup ABI. WINDOW normally validates the JSON
// closing quote at key[len]; this Go reader validates the stored length instead.
func LookupFind(blob unsafe.Pointer, key string) int {
	if blob == nil || len(key) == 0 {
		return -1
	}
	kind := *(*uint32)(blob)
	switch kind {
	case tierWindow:
		return windowFind(blob, key)
	case tierGperf:
		return gperfFind(blob, key)
	case tierHand:
		return handFind(blob, key)
	case tierTable:
		return tableFind(blob, key)
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

func keyEquals(blob unsafe.Pointer, off, klen uintptr, key string) bool {
	if klen != uintptr(len(key)) {
		return false
	}
	stored := unsafe.String((*byte)(unsafe.Add(blob, off)), klen)
	return key == stored
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

func windowFind(blob unsafe.Pointer, key string) int {
	boff := readU8(blob, wOffByteOffset)
	shift := readU8(blob, wOffShift)
	n := readPtr(blob, wOffN)
	stride := readPtr(blob, wOffStride)
	kboff := readPtr(blob, wOffKeyBytesOff)

	klen := uintptr(len(key))

	// WINDOW blobs store only keys of at most 63 bytes. Native input is padded
	// JSON source with a closing quote at key[len], so this fixed buffer provides
	// the quote and one readable byte for that tier format.
	var buf [65]byte
	if klen >= uintptr(len(buf)-1) {
		return -1
	}
	copy(buf[:], key)
	buf[len(key)] = '"'
	p := &buf[0]

	w := *(*uint16)(unsafe.Add(unsafe.Pointer(p), uintptr(boff)))
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
	if keyEquals(blob, off, uintptr(storedKlen), key) {
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

func gperfFind(blob unsafe.Pointer, key string) int {
	np := readU8(blob, 8)
	n := readPtr(blob, gOffN)
	tableSize := readPtr(blob, gOffTableSize)
	stride := readPtr(blob, gOffStride)
	assoOff := readPtr(blob, gOffAssoOff)
	slotsOff := readPtr(blob, gOffSlotsOff)
	klenOff := readPtr(blob, gOffKeyLenOff)
	kboff := readPtr(blob, gOffKeyBytesOff)

	klen := uintptr(len(key))

	var h = klen
	positions := unsafe.Add(blob, gOffPositions)
	asso := unsafe.Add(blob, assoOff)
	p := unsafe.StringData(key)
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
	if keyEquals(blob, off, uintptr(storedKlen), key) {
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

func handFind(blob unsafe.Pointer, key string) int {
	variant := readU32(blob, hOffVariant)
	n := readPtr(blob, hOffN)
	mask := readPtr(blob, hOffMask) // uint64 but stored as uintptr
	kboff := readPtr(blob, hOffKeyBytesOff)

	klen := uintptr(len(key))
	p := unsafe.StringData(key)

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
	if keyEquals(blob, off, uintptr(storedKlen), key) {
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

func tableFind(blob unsafe.Pointer, key string) int {
	mask := readPtr(blob, tOffMask)

	h := tableHash(key)
	pos := uintptr(h & uint64(mask))
	for {
		slotBase := tOffSlots + pos*8
		valueP1 := *(*uint16)(unsafe.Add(blob, slotBase+6))
		if valueP1 == 0 {
			return -1
		}
		keyLen := *(*uint16)(unsafe.Add(blob, slotBase+4))
		if uintptr(keyLen) == uintptr(len(key)) {
			keyOff := *(*uint32)(unsafe.Add(blob, slotBase))
			stored := unsafe.String((*byte)(unsafe.Add(blob, uintptr(keyOff))), uintptr(keyLen))
			if key == stored {
				return int(valueP1) - 1
			}
		}
		pos = (pos + 1) & mask
	}
}

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
