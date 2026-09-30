package vbind

import (
	"errors"
	"strconv"
	"unsafe"
)

// EncodeIntKey converts a decoded JSON object key into the map's integer key
// representation, written to buf in the key kind's width. Every binding
// backend's map publication shares it, so key conversion failures read the
// same whichever engine bound the entries.
func (info *MapDrainInfo) EncodeIntKey(buf *[8]byte, key string) error {
	p := unsafe.Pointer(&buf[0])
	switch k := info.KeyKind; k {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		v, err := strconv.ParseInt(key, 10, intKeyBits(k))
		if err != nil {
			return errors.New("bind: map int key " + key + ": " + err.Error())
		}
		switch k {
		case KindInt8:
			*(*int8)(p) = int8(v)
		case KindInt16:
			*(*int16)(p) = int16(v)
		case KindInt32:
			*(*int32)(p) = int32(v)
		default:
			*(*int64)(p) = v
		}
		return nil
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		v, err := strconv.ParseUint(key, 10, intKeyBits(k))
		if err != nil {
			return errors.New("bind: map uint key " + key + ": " + err.Error())
		}
		switch k {
		case KindUint8:
			*(*uint8)(p) = uint8(v)
		case KindUint16:
			*(*uint16)(p) = uint16(v)
		case KindUint32:
			*(*uint32)(p) = uint32(v)
		default:
			*(*uint64)(p) = v
		}
		return nil
	}
	return errors.New("bind: unsupported map key kind")
}

func intKeyBits(k Kind) int {
	switch k {
	case KindInt8, KindUint8:
		return 8
	case KindInt16, KindUint16:
		return 16
	case KindInt32, KindUint32:
		return 32
	}
	return 64
}
