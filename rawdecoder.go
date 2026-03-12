package vjson

import (
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

type ElemTypeKind uint8

const (
	KindBool ElemTypeKind = iota
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
	KindString
	KindStruct  // nested struct - Decoder field holds *ReflectStructDecoder
	KindSlice   // slice - Decoder field holds *ReflectSliceDecoder
	KindPointer // pointer to T - Decoder field holds *ReflectPointerDecoder
	KindAny     // interface{} field
	KindMap     // map type - Decoder field holds *ReflectMapDecoder
)

// TypeInfo holds pre-computed metadata for a type.
// For struct fields it also carries offset and JSON name; for standalone
// type queries (via GetDecoder) only Kind/Size/Decoder are populated.
type TypeInfo struct {
	Kind          ElemTypeKind // primitive kind for type-switch dispatch
	Size          uintptr      // size of the field type (for int/uint variations)
	Offset        uintptr      // field offset in struct (for unsafe access)
	JSONName      string       // from `json:"name"` tag or field name
	JSONNameLower string       // pre-lowercased JSONName for case-insensitive lookup
	Decoder       any          // nested decoder (*ReflectStructDecoder, *ReflectSliceDecoder, etc.)
}

// decoderCache maps reflect.Type → *decoderEntry.
// Every type goes through GetDecoder exactly once; the returned *TypeInfo is
// stable and may be referenced by pointer from other decoders.
var decoderCache sync.Map // map[reflect.Type]*decoderEntry

// decoderEntry wraps a *TypeInfo with a channel that is closed once the
// decoder (TypeInfo.Decoder) is fully initialized. Concurrent readers
// wait on the channel before using the TypeInfo.
type decoderEntry struct {
	ti   *TypeInfo
	done chan struct{} // closed when ti.Decoder is set
}

// KindForType maps reflect.Kind to the internal ElemTypeKind.
// Panics on unsupported types (programming error, not runtime input error).
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
		panic("vjson: non-empty interface types not supported: " + t.String())
	case reflect.Map:
		return KindMap
	default:
		panic("vjson: unsupported type: " + t.String())
	}
}

// GetDecoder returns the cached *TypeInfo for the given type, building it on
// first access. The returned pointer is stable and safe to store by reference.
//
// Thread-safe: concurrent first-time access to the same type will block
// until the decoder is fully initialized. For recursive types (e.g.
// type Node struct { Children []*Node }), internal builder calls use
// getDecoderForCycle which returns the pointer without waiting.
func GetDecoder(t reflect.Type) *TypeInfo {
	if v, ok := decoderCache.Load(t); ok {
		e := v.(*decoderEntry)
		<-e.done
		return e.ti
	}
	return getDecoderSlow(t, true)
}

// getDecoderForCycle is used by composite decoder builders to resolve
// element types. It returns the *TypeInfo pointer immediately without
// waiting for the decoder to be built, which breaks recursive cycles.
// The pointer is stable and the Decoder field will be populated by the
// time any parsing occurs.
func getDecoderForCycle(t reflect.Type) *TypeInfo {
	if v, ok := decoderCache.Load(t); ok {
		e := v.(*decoderEntry)
		// Don't wait — may be called during initialization of this very type.
		return e.ti
	}
	return getDecoderSlow(t, false)
}

func getDecoderSlow(t reflect.Type, wait bool) *TypeInfo {
	e := &decoderEntry{
		ti:   &TypeInfo{Kind: KindForType(t), Size: t.Size()},
		done: make(chan struct{}),
	}

	actual, loaded := decoderCache.LoadOrStore(t, e)
	if loaded {
		existing := actual.(*decoderEntry)
		if wait {
			<-existing.done
		}
		return existing.ti
	}

	// We won the race — build the decoder.
	// e.ti is already in the cache, so recursive getDecoderForCycle calls
	// will return e.ti's pointer (without waiting).
	switch t.Kind() {
	case reflect.Struct:
		e.ti.Decoder = BuildStructDecoder(t)
	case reflect.Slice:
		e.ti.Decoder = BuildSliceDecoder(t)
	case reflect.Pointer:
		e.ti.Decoder = BuildPointerDecoder(t)
	case reflect.Map:
		e.ti.Decoder = BuildMapDecoder(t)
	}

	close(e.done)
	return e.ti
}

// CollectStructFields recursively collects fields from a struct type, promoting
// fields from anonymous (embedded) structs. baseOffset is added to each
// field's offset to handle nested embedding. Outer (earlier) fields with
// the same JSON name take precedence over inner (embedded) fields.
func CollectStructFields(t reflect.Type, baseOffset uintptr) []TypeInfo {
	var fields []TypeInfo
	seen := make(map[string]bool) // track JSON names to handle override

	// Two passes: first direct fields, then embedded structs.
	// This ensures outer direct fields always override embedded fields.
	type embeddedEntry struct {
		typ    reflect.Type
		offset uintptr
	}
	var embedded []embeddedEntry

	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

		// Collect anonymous embedded structs for second pass
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			embedded = append(embedded, embeddedEntry{sf.Type, baseOffset + sf.Offset})
			continue
		}

		// Parse json tag
		jsonName := sf.Name
		if tag := sf.Tag.Get("json"); tag != "" {
			if tag == "-" {
				continue
			}
			if before, _, ok := strings.Cut(tag, ","); ok {
				jsonName = before
			} else {
				jsonName = tag
			}
			if jsonName == "" {
				jsonName = sf.Name
			}
		}

		cached := getDecoderForCycle(sf.Type)
		fi := TypeInfo{
			Kind:          cached.Kind,
			Size:          cached.Size,
			Offset:        baseOffset + sf.Offset,
			JSONName:      jsonName,
			JSONNameLower: toLowerASCII(jsonName),
			Decoder:       cached.Decoder,
		}

		if !seen[jsonName] {
			seen[jsonName] = true
			fields = append(fields, fi)
		}
	}

	// Second pass: promote fields from embedded structs (lower priority)
	for _, e := range embedded {
		inner := CollectStructFields(e.typ, e.offset)
		for _, fi := range inner {
			if !seen[fi.JSONName] {
				seen[fi.JSONName] = true
				fields = append(fields, fi)
			}
		}
	}

	return fields
}

// --- Decoder Builders ---

// ReflectStructDecoder handles struct decoding using reflect.
type ReflectStructDecoder struct {
	Typ    reflect.Type
	Fields []TypeInfo

	// Tiered lookup — set by buildLookup at construction time.
	LookupFn  func(dec *ReflectStructDecoder, key string) *TypeInfo
	HashSeed  uint64
	HashShift uint8
	HashTable []uint8              // indices into Fields[], 0xFF = empty slot
	FieldMap  map[string]*TypeInfo // fallback for 33+ fields only
}

func BuildStructDecoder(t reflect.Type) *ReflectStructDecoder {
	dec := &ReflectStructDecoder{Typ: t}
	dec.Fields = CollectStructFields(t, 0)
	buildLookup(dec)
	return dec
}

// ReflectSliceDecoder handles slice decoding for any element type
type ReflectSliceDecoder struct {
	SliceType      reflect.Type   // the slice type itself, e.g., []int64
	ElemType       reflect.Type
	ElemSize       uintptr        // size of one element (for unsafe pointer arithmetic)
	ElemTI         *TypeInfo      // cached TypeInfo for element (pointer for cycle safety)
	EmptySliceData unsafe.Pointer // pre-created empty slice backing, avoids reflect.MakeSlice per empty []
	ElemHasPtr     bool           // true if element type contains pointer-like fields (string, slice, ptr, etc.)
	ElemRType      unsafe.Pointer // cached *abi.Type for element, used with runtime alloc via go:linkname
}

func BuildSliceDecoder(t reflect.Type) *ReflectSliceDecoder {
	elemTI := getDecoderForCycle(t.Elem())
	emptySlice := reflect.MakeSlice(t, 0, 0)
	return &ReflectSliceDecoder{
		SliceType:      t,
		ElemType:       t.Elem(),
		ElemSize:       t.Elem().Size(),
		ElemTI:         elemTI,
		EmptySliceData: unsafe.Pointer(emptySlice.Pointer()),
		ElemHasPtr:     typeContainsPointer(t.Elem()),
		ElemRType:      rtypePtr(t.Elem()),
	}
}

// typeContainsPointer reports whether a type contains any pointer-like fields
// that require GC scanning (pointers, strings, slices, maps, interfaces, etc.).
func typeContainsPointer(t reflect.Type) bool {
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
		return typeContainsPointer(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if typeContainsPointer(t.Field(i).Type) {
				return true
			}
		}
		return false
	default:
		// Ptr, Map, Chan, Func, Interface, Slice, String, UnsafePointer
		return true
	}
}

// ReflectMapDecoder handles map decoding for map[string]T types.
// JSON object keys are always strings; values are decoded according to ValTI.
type ReflectMapDecoder struct {
	MapType reflect.Type // the map type itself, e.g., map[string]int64
	KeyType reflect.Type // always string for JSON
	ValType reflect.Type // value element type
	ValSize uintptr      // size of one value element
	ValTI   *TypeInfo    // cached TypeInfo for value (pointer for cycle safety)

	// ValIsString is true for map[string]string, enabling zero-reflection fast path.
	ValIsString bool
}

func BuildMapDecoder(t reflect.Type) *ReflectMapDecoder {
	valTI := getDecoderForCycle(t.Elem())
	return &ReflectMapDecoder{
		MapType:     t,
		KeyType:     t.Key(),
		ValType:     t.Elem(),
		ValSize:     t.Elem().Size(),
		ValTI:       valTI,
		ValIsString: valTI.Kind == KindString,
	}
}

type ReflectPointerDecoder struct {
	PtrType    reflect.Type   // the pointer type itself, e.g., *Foo
	ElemType   reflect.Type
	ElemTI     *TypeInfo      // cached TypeInfo for the pointed-to element (pointer for cycle safety)
	ElemSize   uintptr        // size of the element type for allocation
	ElemHasPtr bool           // true if element type contains pointer-like fields
	ElemRType  unsafe.Pointer // cached *abi.Type for element, used with runtime alloc via go:linkname
}

func BuildPointerDecoder(t reflect.Type) *ReflectPointerDecoder {
	elemTI := getDecoderForCycle(t.Elem())
	return &ReflectPointerDecoder{
		PtrType:    t,
		ElemType:   t.Elem(),
		ElemTI:     elemTI,
		ElemSize:   t.Elem().Size(),
		ElemHasPtr: typeContainsPointer(t.Elem()),
		ElemRType:  rtypePtr(t.Elem()),
	}
}
