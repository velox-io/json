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
	KindIface           // non-empty interface (e.g. fmt.Stringer)
	KindUnmarshaler     // implements json.Unmarshaler
	KindTextUnmarshaler // implements encoding.TextUnmarshaler (only when UnmarshalJSON absent)
	KindValue           // value.Value: tape-backed navigation, native tape-emit descent in bind.h
	KindStream          // stream.Stream[T]: slice storage + yield policy (empty-open / close / drain)
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
	TagFlagQuoted         TagFlag = 1 << iota // `,string` tag
	TagFlagOmitEmpty                          // `omitempty` tag
	TagFlagReserveUnknown                     // `json:",embed"` on a value.Value: reserve all unmatched keys

	// TagFlagEmbed marks a field whose `json:",embed"` cannot be resolved by
	// offset arithmetic here, because which fields it promotes is a run-time
	// choice. Only an interface field reaches this: its promoted set is the
	// variant case the discriminator selects, so vbind resolves it.
	//
	// A struct field's `json:",embed"` never carries this flag. That promotion
	// is settled during collection: the field is replaced by its children and
	// does not survive into StructTypeInfo.Fields at all.
	TagFlagEmbed
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

// PtrHop is one embedded-pointer crossing on a promoted field's path.
//
// Promotion is normally offset arithmetic: a promoted field is addressed as
// hostBase+Offset. An embedded pointer breaks that identity, because the bytes
// holding the promoted field are not inside the host at all. A hop records how
// to get across: read the pointer at SlotOffset relative to the current base,
// allocate a PointeeType if it is nil, and continue from the pointee.
//
// Hops are ordered outermost first. StructField.Offset is then relative to the
// last hop's pointee rather than to the host, so the two are read together.
type PtrHop struct {
	// SlotOffset locates the pointer word, relative to the base established by
	// the previous hop (or the host base for the first hop).
	SlotOffset uintptr

	// PointeeType is what the pointer points at, needed to allocate a pointee
	// that is nil when a promoted field is being written.
	PointeeType *UniType
}

// StructTypeInfo describes a struct.
type StructTypeInfo struct {
	Fields []StructField

	// Rejects records shapes collectStructFields refused to represent. Like
	// VJSONTag.Unrecognized, the typ package cannot report them itself: field
	// collection has no error channel and runs inside a cached type build. The
	// consumers (vbind for decode, venc for encode) fail their build on a
	// non-empty list, which is what keeps a refused shape from silently
	// decoding into the wrong offsets.
	Rejects []string
}

// StructField describes one exported JSON-visible struct field.
type StructField struct {
	FieldType *UniType
	TagFlags  TagFlag

	// Offset locates the field relative to the base its PtrPath establishes:
	// the host base when PtrPath is empty (the common case), otherwise the
	// pointee of the last hop.
	Offset   uintptr
	JSONName string

	// GoName is the Go field name as declared. It differs from JSONName whenever
	// a tag renames the field, and it is the only name an embedded field has in
	// JSON terms at all, since such a field occupies no member.
	//
	// Metadata keyed per field must key on this: JSONName is not a single
	// namespace (it is the wire name for a named field but falls back to the Go
	// name for an embedded one), so an API that took it would silently accept two
	// different kinds of string.
	GoName string

	// PtrPath is non-empty only for a field promoted across one or more embedded
	// pointers. Reaching such a field means walking the hops first, which the
	// hot path cannot do with offset arithmetic alone, so consumers test for it
	// explicitly.
	PtrPath []PtrHop

	// RawTag is the field's full reflect.StructTag. The typ package parses the
	// json sub-tag into TagFlags/JSONName and the vjson sub-tag via
	// ParseVJSONTag; consumers that need the vjson options themselves (vbind
	// reads variant/kindof for polymorphic dispatch) re-parse them from here,
	// and descriptor structs carry their own `case` tag read by vbind.
	RawTag reflect.StructTag

	// DeclaringType is the struct type that literally declares this field, which
	// is the host type only when the field was not promoted. Metadata keyed by
	// type (vbind's variant/kindof descriptor registries) must key on this: the
	// tag lives on the declaring type, so its descriptor was registered there,
	// and promotion must not move that association. Contrast the discriminator,
	// which is resolved by JSON name in the flattened field set and therefore
	// follows shadowing rather than declaration.
	DeclaringType reflect.Type

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
