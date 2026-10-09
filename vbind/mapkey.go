package vbind

import (
	"strconv"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/typ"
)

// mapKeyKind classifies how a decoded JSON object key converts to the key
// type, by the encoding/json rule: a TextUnmarshaler decodes the key through
// UnmarshalText whatever its kind, a string kind (json.Number included) takes
// the key as is, and an integer kind parses it in base 10. Every other kind
// keeps its own Kind, which no key converts to. A pointer key belongs there
// too: the typ layer detects no hooks on pointer types, and encoding/json
// does not decode a key through a pointee.
func mapKeyKind(ut *typ.UniType) Kind {
	if ut.Hooks != nil && ut.Hooks.TextUnmarshalFn != nil {
		return KindTextUnmarshaler
	}
	if k := Kind(ut.Kind); k != KindNumber {
		return k
	}
	return KindString
}

// KeyScratch is the storage AssignKey converts a key into, since mapassign
// takes its key by address. A TextUnmarshaler key decodes into a typed
// instance of its type, so the pointers its hook writes stay visible to the
// GC until the map copies the key; the instance is kept per key type. A drive
// owns one KeyScratch and reuses it for every entry.
type KeyScratch struct {
	num  [8]byte
	text []keyTemp
}

type keyTemp struct{ rtype, p unsafe.Pointer }

func (ks *KeyScratch) textKey(rtype unsafe.Pointer) unsafe.Pointer {
	for _, t := range ks.text {
		if t.rtype == rtype {
			return t.p
		}
	}
	p := gort.UnsafeNew(rtype)
	ks.text = append(ks.text, keyTemp{rtype: rtype, p: p})
	return p
}

// AssignKey converts a decoded JSON object key into the key type of a map
// whose KeyKind is not KindString, assigns it into m, and returns the value
// slot. Every binding backend's map publication shares it, so conversion
// reads the same whichever engine bound the entries.
//
// A TextUnmarshaler key decodes into a zero key, and a failing hook's error
// is returned as the hook returned it. A key an integer kind cannot hold, and
// any key of a kind no key converts to, is an UnmarshalTypeError naming the
// key type, as with encoding/json; the drain runs after the parse, so it
// carries no document offset. A failed key leaves m unchanged.
func (info *MapDrainInfo) AssignKey(m unsafe.Pointer, key string, ks *KeyScratch) (unsafe.Pointer, error) {
	p := unsafe.Pointer(&ks.num[0])
	switch k := info.KeyKind; k {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		v, err := strconv.ParseInt(key, 10, intKeyBits(k))
		if err != nil {
			return nil, info.keyTypeError("number "+key, err)
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
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		v, err := strconv.ParseUint(key, 10, intKeyBits(k))
		if err != nil {
			return nil, info.keyTypeError("number "+key, err)
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
	case KindTextUnmarshaler:
		return info.assignTextKey(m, key, ks)
	default:
		return nil, info.keyTypeError("string", nil)
	}
	return gort.MapAssign(info.MapRType, m, p), nil
}

// assignTextKey decodes key through the key type's UnmarshalText. The hook
// sees a zero key, as encoding/json's fresh one per key; the clear after the
// assign keeps the instance from retaining what the hook wrote.
func (info *MapDrainInfo) assignTextKey(m unsafe.Pointer, key string, ks *KeyScratch) (unsafe.Pointer, error) {
	k := ks.textKey(info.KeyRType)
	size := uintptr(info.KeySize)
	gort.MemclrHasPointers(k, size)
	err := info.KeyText(k, unsafe.Slice(unsafe.StringData(key), len(key)))
	var slot unsafe.Pointer
	if err == nil {
		slot = gort.MapAssign(info.MapRType, m, k)
	}
	gort.MemclrHasPointers(k, size)
	return slot, err
}

func (info *MapDrainInfo) keyTypeError(value string, err error) error {
	return &jerr.UnmarshalTypeError{
		Value: value,
		Type:  gort.TypeFromRType(info.KeyRType),
		Err:   err,
	}
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
