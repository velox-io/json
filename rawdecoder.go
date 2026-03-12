package pjson

import (
	"reflect"
	"strings"
	"sync"
)

// =============================================================================
// Field Metadata Types
// =============================================================================

type elemTypeKind uint8

const (
	kindBool elemTypeKind = iota
	kindInt
	kindInt8
	kindInt16
	kindInt32
	kindInt64
	kindUint
	kindUint8
	kindUint16
	kindUint32
	kindUint64
	kindFloat32
	kindFloat64
	kindString
	kindStruct  // nested struct - decoder field holds *reflectStructDecoder
	kindSlice   // slice - decoder field holds *reflectSliceDecoder
	kindPointer // pointer to T - decoder field holds *reflectPointerDecoder
	kindAny     // interface{} field
	kindMap     // map type - decoder field holds *reflectMapDecoder
)

// typeInfo holds pre-computed metadata for a type.
// For struct fields it also carries offset and JSON name; for standalone
// type queries (via getDecoder) only kind/size/decoder are populated.
type typeInfo struct {
	kind          elemTypeKind // primitive kind for type-switch dispatch
	size          uintptr      // size of the field type (for int/uint variations)
	offset        uintptr      // field offset in struct (for unsafe access)
	jsonName      string       // from `json:"name"` tag or field name
	jsonNameLower string       // pre-lowercased jsonName for case-insensitive lookup
	decoder       any          // nested decoder (*reflectStructDecoder, *reflectSliceDecoder, etc.)
}

// decoderCache maps reflect.Type → *typeInfo.
// Every type goes through getDecoder exactly once; the returned *typeInfo is
// stable and may be referenced by pointer from other decoders.
var decoderCache sync.Map // map[reflect.Type]*typeInfo

// =============================================================================
// Unified Entry Point
// =============================================================================

// kindForType maps reflect.Kind to the internal elemTypeKind.
// Panics on unsupported types (programming error, not runtime input error).
func kindForType(t reflect.Type) elemTypeKind {
	switch t.Kind() {
	case reflect.Bool:
		return kindBool
	case reflect.Int:
		return kindInt
	case reflect.Int8:
		return kindInt8
	case reflect.Int16:
		return kindInt16
	case reflect.Int32:
		return kindInt32
	case reflect.Int64:
		return kindInt64
	case reflect.Uint:
		return kindUint
	case reflect.Uint8:
		return kindUint8
	case reflect.Uint16:
		return kindUint16
	case reflect.Uint32:
		return kindUint32
	case reflect.Uint64:
		return kindUint64
	case reflect.Float32:
		return kindFloat32
	case reflect.Float64:
		return kindFloat64
	case reflect.String:
		return kindString
	case reflect.Struct:
		return kindStruct
	case reflect.Slice:
		return kindSlice
	case reflect.Pointer:
		return kindPointer
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return kindAny
		}
		panic("pjson: non-empty interface types not supported: " + t.String())
	case reflect.Map:
		return kindMap
	default:
		panic("pjson: unsupported type: " + t.String())
	}
}

// getDecoder returns the cached *typeInfo for the given type, building it on
// first access. The returned pointer is stable and safe to store by reference.
//
// For recursive types (e.g. type Node struct { Children []*Node }), a
// partially-initialized *typeInfo is stored in the cache before construction
// begins. Composite decoders that reference it via pointer will see the
// fully-populated value once construction completes.
func getDecoder(t reflect.Type) *typeInfo {
	if cached, ok := decoderCache.Load(t); ok {
		return cached.(*typeInfo)
	}

	ti := &typeInfo{
		kind: kindForType(t),
		size: t.Size(),
	}

	// Occupy the cache slot before building the decoder to break cycles.
	actual, loaded := decoderCache.LoadOrStore(t, ti)
	if loaded {
		return actual.(*typeInfo)
	}

	// Build decoder for composite types. ti is already in the cache,
	// so recursive getDecoder calls will hit the cache and return ti's pointer.
	switch t.Kind() {
	case reflect.Struct:
		ti.decoder = buildStructDecoder(t)
	case reflect.Slice:
		ti.decoder = buildSliceDecoder(t)
	case reflect.Pointer:
		ti.decoder = buildPointerDecoder(t)
	case reflect.Map:
		ti.decoder = buildMapDecoder(t)
	}

	return ti
}

// =============================================================================
// Struct Field Collection
// =============================================================================

// collectStructFields recursively collects fields from a struct type, promoting
// fields from anonymous (embedded) structs. baseOffset is added to each
// field's offset to handle nested embedding. Outer (earlier) fields with
// the same JSON name take precedence over inner (embedded) fields.
func collectStructFields(t reflect.Type, baseOffset uintptr) []typeInfo {
	var fields []typeInfo
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

		cached := getDecoder(sf.Type)
		fi := typeInfo{
			kind:          cached.kind,
			size:          cached.size,
			offset:        baseOffset + sf.Offset,
			jsonName:      jsonName,
			jsonNameLower: toLowerASCII(jsonName),
			decoder:       cached.decoder,
		}

		if !seen[jsonName] {
			seen[jsonName] = true
			fields = append(fields, fi)
		}
	}

	// Second pass: promote fields from embedded structs (lower priority)
	for _, e := range embedded {
		inner := collectStructFields(e.typ, e.offset)
		for _, fi := range inner {
			if !seen[fi.jsonName] {
				seen[fi.jsonName] = true
				fields = append(fields, fi)
			}
		}
	}

	return fields
}

// =============================================================================
// Decoder Builders (no caching — that's getDecoder's job)
// =============================================================================

// reflectStructDecoder handles struct decoding using reflect.
type reflectStructDecoder struct {
	typ    reflect.Type
	fields []typeInfo

	// Tiered lookup — set by buildLookup at construction time.
	lookupFn  func(dec *reflectStructDecoder, key string) *typeInfo
	hashSeed  uint64
	hashShift uint8
	hashTable []uint8              // indices into fields[], 0xFF = empty slot
	fieldMap  map[string]*typeInfo // fallback for 33+ fields only
}

func buildStructDecoder(t reflect.Type) *reflectStructDecoder {
	dec := &reflectStructDecoder{typ: t}
	dec.fields = collectStructFields(t, 0)
	buildLookup(dec)
	return dec
}

// reflectSliceDecoder handles slice decoding for any element type
type reflectSliceDecoder struct {
	sliceType   reflect.Type // the slice type itself, e.g., []int64
	elemType    reflect.Type
	elemSize    uintptr   // size of one element (for unsafe pointer arithmetic)
	elemTI      *typeInfo // cached typeInfo for element (pointer for cycle safety)
}

func buildSliceDecoder(t reflect.Type) *reflectSliceDecoder {
	elemTI := getDecoder(t.Elem())
	return &reflectSliceDecoder{
		sliceType: t,
		elemType:  t.Elem(),
		elemSize:  t.Elem().Size(),
		elemTI:    elemTI,
	}
}

// reflectMapDecoder handles map decoding for map[string]T types.
// JSON object keys are always strings; values are decoded according to valTI.
type reflectMapDecoder struct {
	mapType reflect.Type // the map type itself, e.g., map[string]int64
	keyType reflect.Type // always string for JSON
	valType reflect.Type // value element type
	valSize uintptr      // size of one value element
	valTI   *typeInfo    // cached typeInfo for value (pointer for cycle safety)
}

func buildMapDecoder(t reflect.Type) *reflectMapDecoder {
	valTI := getDecoder(t.Elem())
	return &reflectMapDecoder{
		mapType: t,
		keyType: t.Key(),
		valType: t.Elem(),
		valSize: t.Elem().Size(),
		valTI:   valTI,
	}
}

type reflectPointerDecoder struct {
	ptrType  reflect.Type // the pointer type itself, e.g., *Foo
	elemType reflect.Type
	elemTI   *typeInfo // cached typeInfo for the pointed-to element (pointer for cycle safety)
}

func buildPointerDecoder(t reflect.Type) *reflectPointerDecoder {
	elemTI := getDecoder(t.Elem())
	return &reflectPointerDecoder{
		ptrType:  t,
		elemType: t.Elem(),
		elemTI:   elemTI,
	}
}
