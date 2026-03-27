package typ

import (
	"reflect"
	"unsafe"
)

// ElemTypeKind drives JSON encode/decode dispatch.
type ElemTypeKind uint8

const (
	_ ElemTypeKind = iota // 0 reserved (invalid/unset)
	KindBool
	KindInt
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindUint
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	KindFloat32
	KindFloat64
	KindString // 1–14: primitives (= VM opcode = ZeroCheckTag)
	KindStruct
	KindSlice
	KindPointer
	KindAny
	KindMap
	KindRawMessage // json.RawMessage
	KindNumber     // json.Number
	KindArray
	KindIface // non-empty interface (e.g. fmt.Stringer)
)

// MapVariant selects map fast paths.
type MapVariant uint8

const (
	MapVariantGeneric  MapVariant = iota // generic map (default)
	MapVariantStrStr                     // map[string]string
	MapVariantStrInt                     // map[string]int
	MapVariantStrInt64                   // map[string]int64
)

// TypeFlag stores type-level behavior flags.
type TypeFlag uint8

const (
	TypeFlagHasUnmarshalFn     TypeFlag = 1 << iota // json.Unmarshaler
	TypeFlagHasTextUnmarshalFn                      // encoding.TextUnmarshaler
	TypeFlagHasMarshalFn                            // json.Marshaler
	TypeFlagHasTextMarshalFn                        // encoding.TextMarshaler
	TypeFlagRawMessage                              // json.RawMessage
	TypeFlagNumber                                  // json.Number
)

// TagFlag stores field tag options.
type TagFlag uint8

const (
	TagFlagQuoted     TagFlag = 1 << iota // `,string` tag
	TagFlagOmitEmpty                      // `omitempty` tag
	TagFlagCopyString                     // `,copy` tag
)

// UniType is the shared type descriptor for encode and decode.
// Ext is nil for primitives and points to the container-specific descriptor
// for struct, slice, array, map, and pointer kinds.
type UniType struct {
	Kind ElemTypeKind
	Type reflect.Type
	Ptr  unsafe.Pointer // rtype pointer (8-byte type identity)
	Size uintptr

	Hooks *InterfaceHooks // nil if the type has no marshal hooks

	Ext any // *StructTypeInfo / *SliceTypeInfo / ... for container kinds
}

// InterfaceHooks stores pre-bound marshal hooks to avoid reflect boxing.
type InterfaceHooks struct {
	// json.Marshaler / json.Unmarshaler.
	MarshalFn   func(ptr unsafe.Pointer) ([]byte, error)
	UnmarshalFn func(ptr unsafe.Pointer, data []byte) error

	// encoding.TextMarshaler / encoding.TextUnmarshaler.
	TextMarshalFn   func(ptr unsafe.Pointer) ([]byte, error)
	TextUnmarshalFn func(ptr unsafe.Pointer, data []byte) error
}

// StructTypeInfo describes a struct.
type StructTypeInfo struct {
	Fields []StructField
}

// StructField describes one exported JSON-visible struct field.
type StructField struct {
	FieldType *UniType
	TagFlags  TagFlag

	Offset   uintptr
	JSONName string

	KeyBytes       []byte                        // compact `"name":`
	KeyBytesIndent []byte                        // indented `"name": `
	IsZeroFn       func(ptr unsafe.Pointer) bool // omitempty check
}

// SliceTypeInfo describes a slice.
type SliceTypeInfo struct {
	ElemType       *UniType
	ElemHasPtr     bool
	EmptySliceData unsafe.Pointer // pointer to a zero-length slice's backing
}

// ArrayTypeInfo describes a fixed-size array.
type ArrayTypeInfo struct {
	ElemType   *UniType
	ElemHasPtr bool
	ArrayLen   int
}

// MapTypeInfo describes a map.
type MapTypeInfo struct {
	KeyType *UniType
	ValType *UniType

	MapKind     MapVariant
	IsStringKey bool
	ValHasPtr   bool
	SlotSize    uintptr // Swiss Map slot size; 0 if unknown
}

// PointerTypeInfo describes a pointer.
type PointerTypeInfo struct {
	ElemType   *UniType
	ElemHasPtr bool
}

// KindForType maps reflect.Kind to ElemTypeKind.
// Unsupported kinds return 0.
func KindForType(t reflect.Type) ElemTypeKind {
	switch t.Kind() {
	case reflect.Bool:
		return KindBool
	case reflect.Int:
		return KindInt
	case reflect.Int8:
		return KindInt8
	case reflect.Int16:
		return KindInt16
	case reflect.Int32:
		return KindInt32
	case reflect.Int64:
		return KindInt64
	case reflect.Uint:
		return KindUint
	case reflect.Uint8:
		return KindUint8
	case reflect.Uint16:
		return KindUint16
	case reflect.Uint32:
		return KindUint32
	case reflect.Uint64:
		return KindUint64
	case reflect.Float32:
		return KindFloat32
	case reflect.Float64:
		return KindFloat64
	case reflect.String:
		return KindString
	case reflect.Struct:
		return KindStruct
	case reflect.Slice:
		return KindSlice
	case reflect.Pointer:
		return KindPointer
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return KindAny
		}
		return KindIface
	case reflect.Map:
		return KindMap
	case reflect.Array:
		return KindArray
	default:
		return 0
	}
}

// IsQuotableKind reports whether a kind supports the `,string` tag.
func IsQuotableKind(k ElemTypeKind) bool {
	switch k {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString:
		return true
	}
	return false
}

// TypeContainsPointer reports whether a type needs GC pointer scanning.
func TypeContainsPointer(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128:
		return false
	case reflect.Array:
		if t.Len() == 0 {
			return false
		}
		return TypeContainsPointer(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if TypeContainsPointer(t.Field(i).Type) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
