package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// bindEncodeFn binds ti.Encode after Ext is wired.
// Pointer closures read ElemType.Encode at call time to handle cycles.
func bindEncodeFn(ti *EncTypeInfo) {
	// Already bound (e.g. from encTypeCache hit).
	if ti.Encode != nil {
		return
	}

	// Custom marshal hooks take priority.
	if ti.TypeFlags&EncTypeFlagHasMarshalFn != 0 {
		hooks := ti.Hooks
		ti.Encode = func(es *encodeState, ptr unsafe.Pointer) error {
			data, err := hooks.MarshalFn(ptr)
			if err != nil {
				return err
			}
			if len(data) == 0 {
				es.buf = append(es.buf, litNull...)
			} else {
				es.buf = append(es.buf, data...)
			}
			return nil
		}
		return
	}
	if ti.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		hooks := ti.Hooks
		ti.Encode = func(es *encodeState, ptr unsafe.Pointer) error {
			data, err := hooks.TextMarshalFn(ptr)
			if err != nil {
				return err
			}
			es.encodeString(unsafeString(data))
			return nil
		}
		return
	}

	switch ti.Kind {
	case typ.KindBool:
		ti.Encode = fnEncodeBool
	case typ.KindInt:
		ti.Encode = fnEncodeInt
	case typ.KindInt8:
		ti.Encode = fnEncodeInt8
	case typ.KindInt16:
		ti.Encode = fnEncodeInt16
	case typ.KindInt32:
		ti.Encode = fnEncodeInt32
	case typ.KindInt64:
		ti.Encode = fnEncodeInt64
	case typ.KindUint:
		ti.Encode = fnEncodeUint
	case typ.KindUint8:
		ti.Encode = fnEncodeUint8
	case typ.KindUint16:
		ti.Encode = fnEncodeUint16
	case typ.KindUint32:
		ti.Encode = fnEncodeUint32
	case typ.KindUint64:
		ti.Encode = fnEncodeUint64
	case typ.KindFloat32:
		ti.Encode = fnEncodeFloat32
	case typ.KindFloat64:
		ti.Encode = fnEncodeFloat64
	case typ.KindString:
		ti.Encode = fnEncodeString
	case typ.KindRawMessage:
		ti.Encode = fnEncodeRawMessage
	case typ.KindNumber:
		ti.Encode = fnEncodeNumber

	case typ.KindStruct:
		ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
			return m.exec(ti.getBlueprint(), ptr)
		}

	case typ.KindSlice:
		si := ti.ResolveSlice()
		if si.ElemType.Kind == typ.KindUint8 && si.ElemSize == 1 {
			ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
				sh := (*SliceHeader)(ptr)
				if sh.Data == nil {
					m.buf = append(m.buf, litNull...)
					return nil
				}
				return m.encodeByteSlice(sh)
			}
		} else {
			ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
				sh := (*SliceHeader)(ptr)
				if sh.Data == nil {
					m.buf = append(m.buf, litNull...)
					return nil
				}
				return m.exec(ti.getBlueprint(), ptr)
			}
		}

	case typ.KindArray:
		ai := ti.ResolveArray()
		if ai.ElemType.Kind == typ.KindUint8 && ai.ElemSize == 1 {
			ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
				return m.encodeByteArray(ai, ptr)
			}
		} else {
			ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
				return m.exec(ti.getBlueprint(), ptr)
			}
		}

	case typ.KindMap:
		mi := ti.ResolveMap()
		ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
			if mi.IsStringKey && !m.inVM && m.nativeCompat {
				return m.exec(ti.getBlueprint(), ptr)
			}
			if mi.MapKind == typ.MapVariantStrStr {
				return m.encodeMapStringString(ptr)
			}
			return m.encodeMapGeneric(mi, ptr)
		}

	case typ.KindPointer:
		// Read ElemType.Encode at call time (not bind time) to handle cycles.
		pi := ti.ResolvePointer()
		ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
			elemPtr := *(*unsafe.Pointer)(ptr)
			if elemPtr == nil {
				m.buf = append(m.buf, litNull...)
				return nil
			}
			return pi.ElemType.Encode(m, elemPtr)
		}

	case typ.KindAny:
		ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
			return m.encodeAny(*(*any)(ptr))
		}

	case typ.KindIface:
		rtype := ti.Type
		ti.Encode = func(m *encodeState, ptr unsafe.Pointer) error {
			rv := reflect.NewAt(rtype, ptr).Elem()
			if rv.IsNil() {
				m.buf = append(m.buf, litNull...)
				return nil
			}
			return m.encodeAnyReflect(rv.Elem().Interface())
		}

	default:
		rtype := ti.Type
		ti.Encode = func(_ *encodeState, _ unsafe.Pointer) error {
			return &UnsupportedTypeError{Type: rtype}
		}
	}
}

// ── Primitive encode functions (stateless, no closure needed) ───────

func fnEncodeBool(es *encodeState, ptr unsafe.Pointer) error {
	if *(*bool)(ptr) {
		es.buf = append(es.buf, litTrue...)
	} else {
		es.buf = append(es.buf, litFalse...)
	}
	return nil
}

func fnEncodeInt(es *encodeState, ptr unsafe.Pointer) error {
	es.appendInt64(int64(*(*int)(ptr)))
	return nil
}

func fnEncodeInt8(es *encodeState, ptr unsafe.Pointer) error {
	es.appendInt64(int64(*(*int8)(ptr)))
	return nil
}

func fnEncodeInt16(es *encodeState, ptr unsafe.Pointer) error {
	es.appendInt64(int64(*(*int16)(ptr)))
	return nil
}

func fnEncodeInt32(es *encodeState, ptr unsafe.Pointer) error {
	es.appendInt64(int64(*(*int32)(ptr)))
	return nil
}

func fnEncodeInt64(es *encodeState, ptr unsafe.Pointer) error {
	es.appendInt64(*(*int64)(ptr))
	return nil
}

func fnEncodeUint(es *encodeState, ptr unsafe.Pointer) error {
	es.appendUint64(uint64(*(*uint)(ptr)))
	return nil
}

func fnEncodeUint8(es *encodeState, ptr unsafe.Pointer) error {
	es.appendUint64(uint64(*(*uint8)(ptr)))
	return nil
}

func fnEncodeUint16(es *encodeState, ptr unsafe.Pointer) error {
	es.appendUint64(uint64(*(*uint16)(ptr)))
	return nil
}

func fnEncodeUint32(es *encodeState, ptr unsafe.Pointer) error {
	es.appendUint64(uint64(*(*uint32)(ptr)))
	return nil
}

func fnEncodeUint64(es *encodeState, ptr unsafe.Pointer) error {
	es.appendUint64(*(*uint64)(ptr))
	return nil
}

func fnEncodeFloat32(es *encodeState, ptr unsafe.Pointer) error {
	return es.encodeFloat32(ptr)
}

func fnEncodeFloat64(es *encodeState, ptr unsafe.Pointer) error {
	return es.encodeFloat64(ptr)
}

func fnEncodeString(es *encodeState, ptr unsafe.Pointer) error {
	es.encodeString(*(*string)(ptr))
	return nil
}

func fnEncodeRawMessage(es *encodeState, ptr unsafe.Pointer) error {
	raw := *(*[]byte)(ptr)
	if len(raw) == 0 {
		es.buf = append(es.buf, litNull...)
	} else {
		es.buf = append(es.buf, raw...)
	}
	return nil
}

func fnEncodeNumber(es *encodeState, ptr unsafe.Pointer) error {
	s := *(*string)(ptr)
	if s == "" {
		es.buf = append(es.buf, '0')
	} else {
		es.buf = append(es.buf, s...)
	}
	return nil
}
