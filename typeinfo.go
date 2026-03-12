package vjson

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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

// tiFlag is a bitmask for hot-path checks in scanValue / encodeValue.
type tiFlag uint8

const (
	tiFlagHasUnmarshalFn     tiFlag = 1 << iota // Ext.UnmarshalFn != nil
	tiFlagHasTextUnmarshalFn                     // Ext.TextUnmarshalFn != nil
	tiFlagQuoted                                 // `,string` tag
	tiFlagHasMarshalFn                           // Ext.MarshalFn != nil
	tiFlagHasTextMarshalFn                       // Ext.TextMarshalFn != nil
	tiFlagOmitEmpty                              // omitempty
)

// TypeInfo holds pre-computed metadata for a type.
// Hot-path fields (accessed per-field during scan/encode) live here;
// cold fields (marshal key bytes, custom marshal/unmarshal funcs) live in Ext.
type TypeInfo struct {
	Kind          ElemTypeKind
	Flags         tiFlag
	Size          uintptr
	Offset        uintptr
	JSONName      string
	JSONNameLower string
	Decoder       any
	Ext           *TypeInfoExt // cold marshal/unmarshal metadata (nil when not needed)
}

// TypeInfoExt holds infrequently-accessed per-field metadata.
type TypeInfoExt struct {
	Type reflect.Type // Go type (used only in error paths)

	// Marshal metadata
	KeyBytes       []byte // pre-encoded `"name":` (compact)
	KeyBytesIndent []byte // pre-encoded `"name": ` (indented)
	IsZeroFn       func(ptr unsafe.Pointer) bool

	// Custom JSON marshaling/unmarshaling via json.Marshaler/json.Unmarshaler.
	MarshalFn   func(ptr unsafe.Pointer) ([]byte, error)
	UnmarshalFn func(ptr unsafe.Pointer, data []byte) error

	// Custom text marshaling/unmarshaling via encoding.TextMarshaler/TextUnmarshaler.
	TextMarshalFn   func(ptr unsafe.Pointer) ([]byte, error)
	TextUnmarshalFn func(ptr unsafe.Pointer, data []byte) error
}

// getOrAllocExt returns Ext, allocating it if nil.
func (ti *TypeInfo) getOrAllocExt() *TypeInfoExt {
	if ti.Ext == nil {
		ti.Ext = &TypeInfoExt{}
	}
	return ti.Ext
}

// decoderCache maps reflect.Type → *TypeInfo (steady-state) or
// *decoderEntry (transient, during construction).
// After construction completes the entry is promoted to *TypeInfo
// so the hot path is a single atomic load with no synchronization.
var decoderCache sync.Map

type decoderEntry struct {
	ti   *TypeInfo
	done chan struct{}
}

// KindForType maps reflect.Kind to ElemTypeKind.
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

// isQuotableKind reports whether the given kind supports the `,string` tag option.
func isQuotableKind(k ElemTypeKind) bool {
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

// GetDecoder returns the cached *TypeInfo for the given type.
// Thread-safe; blocks until the decoder is fully initialized.
func GetDecoder(t reflect.Type) *TypeInfo {
	if v, ok := decoderCache.Load(t); ok {
		switch e := v.(type) {
		case *TypeInfo:
			return e // hot path: no synchronization
		case *decoderEntry:
			<-e.done
			return e.ti
		}
	}
	return getDecoderSlow(t, true)
}

// getDecoderForCycle returns *TypeInfo without waiting, breaking recursive cycles.
func getDecoderForCycle(t reflect.Type) *TypeInfo {
	if v, ok := decoderCache.Load(t); ok {
		switch e := v.(type) {
		case *TypeInfo:
			return e
		case *decoderEntry:
			return e.ti
		}
	}
	return getDecoderSlow(t, false)
}

func getDecoderSlow(t reflect.Type, wait bool) *TypeInfo {
	e := &decoderEntry{
		ti:   &TypeInfo{Kind: KindForType(t), Size: t.Size(), Ext: &TypeInfoExt{Type: t}},
		done: make(chan struct{}),
	}

	actual, loaded := decoderCache.LoadOrStore(t, e)
	if loaded {
		switch existing := actual.(type) {
		case *TypeInfo:
			return existing
		case *decoderEntry:
			if wait {
				<-existing.done
			}
			return existing.ti
		}
	}

	// Won the race — build the decoder.
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

	// Detect json.Marshaler / json.Unmarshaler interfaces.
	// Skip for pointer types: pointer handling (scanPointer/encodePointer)
	// dereferences to the element type, which has its own MarshalFn/UnmarshalFn.
	if t.Kind() != reflect.Pointer {
		marshalerType := reflect.TypeFor[json.Marshaler]()
		unmarshalerType := reflect.TypeFor[json.Unmarshaler]()
		ptrType := reflect.PointerTo(t)

		ext := e.ti.getOrAllocExt()

		if t.Implements(marshalerType) {
			ext.MarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				return reflect.NewAt(t, ptr).Elem().Interface().(json.Marshaler).MarshalJSON()
			}
			e.ti.Flags |= tiFlagHasMarshalFn
		} else if ptrType.Implements(marshalerType) {
			ext.MarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				return reflect.NewAt(t, ptr).Interface().(json.Marshaler).MarshalJSON()
			}
			e.ti.Flags |= tiFlagHasMarshalFn
		}

		if t.Implements(unmarshalerType) {
			ext.UnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				return reflect.NewAt(t, ptr).Elem().Interface().(json.Unmarshaler).UnmarshalJSON(data)
			}
			e.ti.Flags |= tiFlagHasUnmarshalFn
		} else if ptrType.Implements(unmarshalerType) {
			ext.UnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				return reflect.NewAt(t, ptr).Interface().(json.Unmarshaler).UnmarshalJSON(data)
			}
			e.ti.Flags |= tiFlagHasUnmarshalFn
		}

		// Detect encoding.TextMarshaler / encoding.TextUnmarshaler.
		textMarshalerType := reflect.TypeFor[encoding.TextMarshaler]()
		textUnmarshalerType := reflect.TypeFor[encoding.TextUnmarshaler]()

		if t.Implements(textMarshalerType) {
			ext.TextMarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				return reflect.NewAt(t, ptr).Elem().Interface().(encoding.TextMarshaler).MarshalText()
			}
			e.ti.Flags |= tiFlagHasTextMarshalFn
		} else if ptrType.Implements(textMarshalerType) {
			ext.TextMarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				return reflect.NewAt(t, ptr).Interface().(encoding.TextMarshaler).MarshalText()
			}
			e.ti.Flags |= tiFlagHasTextMarshalFn
		}

		if t.Implements(textUnmarshalerType) {
			ext.TextUnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				return reflect.NewAt(t, ptr).Elem().Interface().(encoding.TextUnmarshaler).UnmarshalText(data)
			}
			e.ti.Flags |= tiFlagHasTextUnmarshalFn
		} else if ptrType.Implements(textUnmarshalerType) {
			ext.TextUnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				return reflect.NewAt(t, ptr).Interface().(encoding.TextUnmarshaler).UnmarshalText(data)
			}
			e.ti.Flags |= tiFlagHasTextUnmarshalFn
		}
	}

	close(e.done)
	// Promote: replace the transient *decoderEntry with the final *TypeInfo
	// so subsequent loads hit the fast path (no channel recv).
	decoderCache.Store(t, e.ti)
	return e.ti
}

// CollectStructFields collects fields from a struct type, promoting
// anonymous embedded struct fields. Direct (outer) fields take precedence
// over embedded fields with the same JSON name.
func CollectStructFields(t reflect.Type, baseOffset uintptr) []TypeInfo {
	var fields []TypeInfo
	seen := make(map[string]bool)

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
		omitEmpty := false
		quoted := false
		if tag := sf.Tag.Get("json"); tag != "" {
			if tag == "-" {
				continue
			}
			if before, opts, ok := strings.Cut(tag, ","); ok {
				jsonName = before
				omitEmpty = strings.Contains(opts, "omitempty")
				quoted = strings.Contains(opts, "string")
			} else {
				jsonName = tag
			}
			if jsonName == "" {
				jsonName = sf.Name
			}
		}

		cached := getDecoderForCycle(sf.Type)

		// `,string` is only meaningful for bool, int*, uint*, float*, string, and
		// pointer-to-those kinds. encoding/json silently ignores it for other types.
		if quoted {
			switch cached.Kind {
			case KindPointer:
				pDec := cached.Decoder.(*ReflectPointerDecoder)
				if !isQuotableKind(pDec.ElemTI.Kind) {
					quoted = false
				}
			default:
				if !isQuotableKind(cached.Kind) {
					quoted = false
				}
			}
		}

		fi := TypeInfo{
			Kind:          cached.Kind,
			Flags:         cached.Flags,
			Size:          cached.Size,
			Offset:        baseOffset + sf.Offset,
			JSONName:      jsonName,
			JSONNameLower: toLowerASCII(jsonName),
			Decoder:       cached.Decoder,
		}

		// Build Ext with marshal metadata.
		ext := fi.getOrAllocExt()
		ext.Type = sf.Type
		ext.KeyBytes = encodeKeyBytes(jsonName)
		ext.KeyBytesIndent = encodeKeyBytesIndent(jsonName)
		ext.IsZeroFn = makeIsZeroFn(sf.Type)
		if cachedExt := cached.Ext; cachedExt != nil {
			ext.MarshalFn = cachedExt.MarshalFn
			ext.UnmarshalFn = cachedExt.UnmarshalFn
			ext.TextMarshalFn = cachedExt.TextMarshalFn
			ext.TextUnmarshalFn = cachedExt.TextUnmarshalFn
		}

		if omitEmpty {
			fi.Flags |= tiFlagOmitEmpty
		}
		if quoted {
			fi.Flags |= tiFlagQuoted
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

// ReflectStructDecoder handles struct decoding.
type ReflectStructDecoder struct {
	Typ    reflect.Type
	Fields []TypeInfo

	// Tiered lookup — set by buildLookup at construction time.
	LookupFn     func(dec *ReflectStructDecoder, key string) *TypeInfo
	HashSeed     uint64
	HashShift    uint8
	HashTable    []uint8              // indices into Fields[]; 0xFF = empty
	FieldMap     map[string]*TypeInfo // fallback for 33+ fields
	HasMixedCase bool                 // true if any JSONName differs from JSONNameLower
}

func BuildStructDecoder(t reflect.Type) *ReflectStructDecoder {
	dec := &ReflectStructDecoder{Typ: t}
	dec.Fields = CollectStructFields(t, 0)
	buildLookup(dec)
	return dec
}

// ReflectSliceDecoder handles slice decoding.
type ReflectSliceDecoder struct {
	SliceType      reflect.Type
	ElemType       reflect.Type
	ElemSize       uintptr
	ElemTI         *TypeInfo
	EmptySliceData unsafe.Pointer
	ElemHasPtr     bool
	ElemRType      unsafe.Pointer
	capHint        atomic.Int32 // adaptive: EMA of observed array lengths
	emaAlpha       int32        // EMA smoothing denominator; 0 means default (2)
}

// SetEMAAlpha sets the EMA smoothing denominator for adaptive array capacity.
// The formula is: hint = (old*(alpha-1) + observed) / alpha.
// Default alpha is 2 (equal-weight average). Higher values make the EMA
// respond more slowly to length changes.
func (d *ReflectSliceDecoder) SetEMAAlpha(alpha int32) {
	if alpha < 2 {
		alpha = 2
	}
	d.emaAlpha = alpha
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
		emaAlpha:       2,
	}
}

// typeContainsPointer reports whether a type contains pointer-like fields.
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
		return true
	}
}

// ReflectMapDecoder handles map[K]V decoding.
type ReflectMapDecoder struct {
	MapType reflect.Type
	KeyType reflect.Type
	ValType reflect.Type
	ValSize uintptr
	ValTI   *TypeInfo
	KeyTI   *TypeInfo // key type info (for TextMarshaler on non-string keys)

	ValIsString bool // true for map[string]string fast path
}

func BuildMapDecoder(t reflect.Type) *ReflectMapDecoder {
	valTI := getDecoderForCycle(t.Elem())
	keyTI := getDecoderForCycle(t.Key())
	return &ReflectMapDecoder{
		MapType:     t,
		KeyType:     t.Key(),
		ValType:     t.Elem(),
		ValSize:     t.Elem().Size(),
		ValTI:       valTI,
		KeyTI:       keyTI,
		ValIsString: valTI.Kind == KindString && t.Key().Kind() == reflect.String,
	}
}

type ReflectPointerDecoder struct {
	PtrType    reflect.Type
	ElemType   reflect.Type
	ElemTI     *TypeInfo
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
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

// --- Marshal helpers ---

// encodeKeyBytes returns `"name":`
func encodeKeyBytes(name string) []byte {
	buf := make([]byte, 0, len(name)+4)
	buf = appendEscapedString(buf, name, 0)
	buf = append(buf, ':')
	return buf
}

// encodeKeyBytesIndent returns `"name": `
func encodeKeyBytesIndent(name string) []byte {
	buf := make([]byte, 0, len(name)+5)
	buf = appendEscapedString(buf, name, 0)
	buf = append(buf, ':', ' ')
	return buf
}

// --- Zero-value detection for omitempty ---

// makeIsZeroFn returns a zero-value check for the given type. Nil if not applicable.
func makeIsZeroFn(t reflect.Type) func(unsafe.Pointer) bool {
	switch t.Kind() {
	case reflect.Bool:
		return func(ptr unsafe.Pointer) bool { return !*(*bool)(ptr) }
	case reflect.Int:
		return func(ptr unsafe.Pointer) bool { return *(*int)(ptr) == 0 }
	case reflect.Int8:
		return func(ptr unsafe.Pointer) bool { return *(*int8)(ptr) == 0 }
	case reflect.Int16:
		return func(ptr unsafe.Pointer) bool { return *(*int16)(ptr) == 0 }
	case reflect.Int32:
		return func(ptr unsafe.Pointer) bool { return *(*int32)(ptr) == 0 }
	case reflect.Int64:
		return func(ptr unsafe.Pointer) bool { return *(*int64)(ptr) == 0 }
	case reflect.Uint:
		return func(ptr unsafe.Pointer) bool { return *(*uint)(ptr) == 0 }
	case reflect.Uint8:
		return func(ptr unsafe.Pointer) bool { return *(*uint8)(ptr) == 0 }
	case reflect.Uint16:
		return func(ptr unsafe.Pointer) bool { return *(*uint16)(ptr) == 0 }
	case reflect.Uint32:
		return func(ptr unsafe.Pointer) bool { return *(*uint32)(ptr) == 0 }
	case reflect.Uint64:
		return func(ptr unsafe.Pointer) bool { return *(*uint64)(ptr) == 0 }
	case reflect.Float32:
		return func(ptr unsafe.Pointer) bool { return *(*float32)(ptr) == 0 }
	case reflect.Float64:
		return func(ptr unsafe.Pointer) bool { return *(*float64)(ptr) == 0 }
	case reflect.String:
		return func(ptr unsafe.Pointer) bool { return len(*(*string)(ptr)) == 0 }
	case reflect.Slice, reflect.Map:
		return func(ptr unsafe.Pointer) bool { return *(*unsafe.Pointer)(ptr) == nil }
	case reflect.Pointer, reflect.Interface:
		return func(ptr unsafe.Pointer) bool { return *(*unsafe.Pointer)(ptr) == nil }
	case reflect.Struct:
		return makeIsZeroStruct(t)
	default:
		return nil
	}
}

// makeIsZeroStruct builds a zero-check for a struct type.
func makeIsZeroStruct(t reflect.Type) func(unsafe.Pointer) bool {
	type fieldCheck struct {
		offset uintptr
		fn     func(unsafe.Pointer) bool
	}
	var checks []fieldCheck
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fn := makeIsZeroFn(sf.Type)
		if fn != nil {
			checks = append(checks, fieldCheck{sf.Offset, fn})
		}
	}
	if len(checks) == 0 {
		return func(_ unsafe.Pointer) bool { return true }
	}
	return func(ptr unsafe.Pointer) bool {
		for _, c := range checks {
			if !c.fn(unsafe.Add(ptr, c.offset)) {
				return false
			}
		}
		return true
	}
}
