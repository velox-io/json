package vjson

import (
	"encoding"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var rawMessageType = reflect.TypeFor[json.RawMessage]()
var numberType = reflect.TypeFor[json.Number]()
var intType = reflect.TypeFor[int]()
var int64Type = reflect.TypeFor[int64]()
var timeType = reflect.TypeFor[time.Time]()

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
	KindString     // 1–14: primitives (= VM opcode = ZeroCheckTag)
	KindStruct     // Codec: *StructCodec
	KindSlice      // Codec: *SliceCodec
	KindPointer    // Codec: *PointerCodec
	KindAny        // interface{}
	KindMap        // Codec: *MapCodec
	KindRawMessage // json.RawMessage
	KindNumber     // json.Number
	KindArray      // Codec: *ArrayCodec
	KindIface      // non-empty interface (e.g. fmt.Stringer)
)

// MapVariant represents specialized map types for fast path optimization.
type MapVariant uint8

const (
	MapVariantGeneric  MapVariant = iota // generic map (default)
	MapVariantStrStr                     // map[string]string
	MapVariantStrInt                     // map[string]int
	MapVariantStrInt64                   // map[string]int64
)

// tiFlag is a bitmask for hot-path checks in scanValue / encodeValue.
type tiFlag uint16

const (
	tiFlagHasUnmarshalFn     tiFlag = 1 << iota // Ext.UnmarshalFn != nil
	tiFlagHasTextUnmarshalFn                    // Ext.TextUnmarshalFn != nil
	tiFlagQuoted                                // `,string` tag
	tiFlagHasMarshalFn                          // Ext.MarshalFn != nil
	tiFlagHasTextMarshalFn                      // Ext.TextMarshalFn != nil
	tiFlagOmitEmpty                             // omitempty
	tiFlagRawMessage                            // json.RawMessage native handling
	tiFlagNumber                                // json.Number native handling
	tiFlagCopyString                            // `,copy` tag

	// tiFlagUnmarshalMask groups flags that affect the unmarshal code path.
	// Marshal-only flags (MarshalFn, TextMarshalFn, OmitEmpty) do not
	// require the scanValueSpecial slow path during decoding.
	tiFlagUnmarshalMask = tiFlagHasUnmarshalFn | tiFlagHasTextUnmarshalFn |
		tiFlagQuoted | tiFlagRawMessage | tiFlagNumber | tiFlagCopyString
)

// TypeInfo holds pre-computed metadata for a type.
// Hot-path fields (accessed per-field during scan/encode) live here;
// cold fields (marshal key bytes, custom marshal/unmarshal funcs) live in Ext.
type TypeInfo struct {
	Kind          ElemTypeKind
	Flags         tiFlag // all flags (used by marshal)
	UFlags        tiFlag // unmarshal-only flags subset (used by parser fast-path check)
	Size          uintptr
	Offset        uintptr
	JSONName      string
	JSONNameLower string
	EncodeFn      func(m *marshaler, ptr unsafe.Pointer) error // pre-bound encode dispatch
	HintBytes     int                                          // static size hint for pre-allocating marshal buffer
	Codec         any
	Ext           *TypeInfoExt        // cold marshal/unmarshal metadata (nil when not needed)
	ScanArrayFn   ArraySpecialScanner // specialized array element scanner (nil = generic path)
}

// ArraySpecialScanner is a specialized array scanner that bypasses
// scanValue/scanNumber dispatch for known element types (int*, uint*, float64).
type ArraySpecialScanner func(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error)

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

// resolveCodec returns ti.Codec, lazily resolving it from the codec cache
// if it was nil due to a construction-time cycle (getCodecForCycle returns
// a partial TypeInfo whose Codec is value-copied as nil into StructCodec.Fields).
//
// MUST only be called after all codec construction is complete, i.e. at
// Marshal/Unmarshal time, not during type registration.
func (ti *TypeInfo) resolveCodec() any {
	if c := ti.Codec; c != nil {
		return c
	}
	if ti.Ext != nil && ti.Ext.Type != nil {
		full := getCodec(ti.Ext.Type)
		c := full.Codec
		ti.Codec = c // cache for next call; benign race: idempotent write
		return c
	}
	return nil
}

// codecCache maps reflect.Type → *TypeInfo (steady-state) or
// *codecEntry (transient, during construction).
// After construction completes the entry is promoted to *TypeInfo
// so the hot path is a single atomic load with no synchronization.
var codecCache sync.Map

type codecEntry struct {
	ti   *TypeInfo
	done chan struct{}
}

// kindForType maps reflect.Kind to ElemTypeKind.
// Returns 0 (invalid) for unsupported types (chan, func, etc.).
// The caller should handle 0 as an unsupported type — encodeValueSlow's default
// branch returns UnsupportedTypeError in that case.
func kindForType(t reflect.Type) ElemTypeKind {
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
		return KindIface // non-empty interface: handled like any
	case reflect.Map:
		return KindMap
	case reflect.Array:
		return KindArray
	default:
		return 0 // chan, func, complex, uintptr, etc.: unsupported
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

// codecFastCacheTable is a fixed-size direct-mapped cache that sits in front
// of the sync.Map (codecCache).  Each slot stores a (rtype-pointer, *TypeInfo)
// pair atomically.  On hit the cost is one atomic load + one uintptr compare;
// on miss we fall through to the sync.Map and then populate the slot.
//
// 32 slots is enough for most programs (few hot types) while keeping the
// table small enough to stay in L1.  Collisions simply evict the old entry.
//
// Marshal and unmarshal use separate tables so that interleaved encode/decode
// workloads do not thrash each other's cache entries.
const codecFastCacheSize = 32 // must be power of two

type codecCachePair struct {
	key uintptr   // rtype pointer
	val *TypeInfo // cached result (never nil once stored)
}

type codecFastCacheTable [codecFastCacheSize]atomic.Pointer[codecCachePair]

var (
	codecCacheMarshal   codecFastCacheTable
	codecCacheUnmarshal codecFastCacheTable
)

// codecFastCacheIndex hashes an rtype pointer to a slot index.
// rtype pointers are at least 8-byte aligned so the low 3 bits are zero;
// a multiply-shift distributes entropy across all index bits.
func codecFastCacheIndex(rtp uintptr) uintptr {
	// Fibonacci hashing: multiply by a large odd constant, then shift.
	const magic = 0x9e3779b97f4a7c15 // golden-ratio × 2^64
	return (rtp * magic) >> (64 - 5) // 5 = log2(32)
}

func (c *codecFastCacheTable) getCodec(t reflect.Type) *TypeInfo {
	rtp := uintptr(rtypePtr(t))
	idx := codecFastCacheIndex(rtp)
	if p := c[idx].Load(); p != nil && p.key == rtp {
		return p.val
	}
	ti := getCodecViaMap(t)
	c[idx].Store(&codecCachePair{key: rtp, val: ti})
	return ti
}

// getCodec returns the cached *TypeInfo for the given type (shared/uncategorized callers).
// Thread-safe; blocks until the codec is fully initialized.
func getCodec(t reflect.Type) *TypeInfo {
	return getCodecViaMap(t)
}

func getCodecViaMap(t reflect.Type) *TypeInfo {
	if v, ok := codecCache.Load(t); ok {
		switch e := v.(type) {
		case *TypeInfo:
			return e
		case *codecEntry:
			<-e.done
			return e.ti
		}
	}
	return getCodecSlow(t, true)
}

// getCodecForCycle returns *TypeInfo without waiting, breaking recursive cycles.
func getCodecForCycle(t reflect.Type) *TypeInfo {
	if v, ok := codecCache.Load(t); ok {
		switch e := v.(type) {
		case *TypeInfo:
			return e
		case *codecEntry:
			return e.ti
		}
	}
	return getCodecSlow(t, false)
}

func getCodecSlow(t reflect.Type, wait bool) *TypeInfo {
	e := &codecEntry{
		ti:   &TypeInfo{Kind: kindForType(t), Size: t.Size(), Ext: &TypeInfoExt{Type: t}},
		done: make(chan struct{}),
	}

	actual, loaded := codecCache.LoadOrStore(t, e)
	if loaded {
		switch existing := actual.(type) {
		case *TypeInfo:
			return existing
		case *codecEntry:
			if wait {
				<-existing.done
			}
			return existing.ti
		}
	}

	// Won the race — build the codec.
	switch t.Kind() {
	case reflect.Struct:
		e.ti.Codec = BuildStructCodec(t)
	case reflect.Slice:
		e.ti.Codec = BuildSliceCodec(t)
	case reflect.Pointer:
		e.ti.Codec = BuildPointerCodec(t)
	case reflect.Map:
		e.ti.Codec = BuildMapCodec(t)
	case reflect.Array:
		e.ti.Codec = BuildArrayCodec(t)
	}

	// json.RawMessage: override Kind to KindRawMessage and set the flag
	// so that scanValueSpecial uses the native skip+copy path instead of
	// going through the json.Unmarshaler interface.
	if t == rawMessageType {
		e.ti.Kind = KindRawMessage
		e.ti.Flags |= tiFlagRawMessage
		e.ti.UFlags = e.ti.Flags & tiFlagUnmarshalMask
		bindEncodeFn(e.ti)
		e.ti.HintBytes = 64
		close(e.done)
		codecCache.Store(t, e.ti)
		return e.ti
	}

	// json.Number: override Kind to KindNumber so the parser stores the
	// raw number text as a string instead of converting to float64.
	if t == numberType {
		e.ti.Kind = KindNumber
		e.ti.Flags |= tiFlagNumber
		e.ti.UFlags = e.ti.Flags & tiFlagUnmarshalMask
		bindEncodeFn(e.ti)
		e.ti.HintBytes = 12
		close(e.done)
		codecCache.Store(t, e.ti)
		return e.ti
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
			// Value receiver: extract itab once, then construct iface directly at runtime.
			// This avoids the mallocgc boxing that reflect.Value.Interface() triggers
			// for value types larger than a pointer (e.g. time.Time = 24 bytes).
			sentinel := reflect.New(t)
			iface := sentinel.Elem().Interface().(json.Marshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.MarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				var m json.Marshaler
				*(*goIface)(unsafe.Pointer(&m)) = goIface{tab: itab, data: ptr}
				return m.MarshalJSON()
			}
			e.ti.Flags |= tiFlagHasMarshalFn
		} else if ptrType.Implements(marshalerType) {
			// Pointer receiver: the itab is for *T, and ptr is already *T.
			sentinel := reflect.New(t)
			iface := sentinel.Interface().(json.Marshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.MarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				var m json.Marshaler
				*(*goIface)(unsafe.Pointer(&m)) = goIface{tab: itab, data: ptr}
				return m.MarshalJSON()
			}
			e.ti.Flags |= tiFlagHasMarshalFn
		}

		if t.Implements(unmarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Elem().Interface().(json.Unmarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.UnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				var u json.Unmarshaler
				*(*goIface)(unsafe.Pointer(&u)) = goIface{tab: itab, data: ptr}
				return u.UnmarshalJSON(data)
			}
			e.ti.Flags |= tiFlagHasUnmarshalFn
		} else if ptrType.Implements(unmarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Interface().(json.Unmarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.UnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				var u json.Unmarshaler
				*(*goIface)(unsafe.Pointer(&u)) = goIface{tab: itab, data: ptr}
				return u.UnmarshalJSON(data)
			}
			e.ti.Flags |= tiFlagHasUnmarshalFn
		}

		// Detect encoding.TextMarshaler / encoding.TextUnmarshaler.
		textMarshalerType := reflect.TypeFor[encoding.TextMarshaler]()
		textUnmarshalerType := reflect.TypeFor[encoding.TextUnmarshaler]()

		if t.Implements(textMarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Elem().Interface().(encoding.TextMarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.TextMarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				var tm encoding.TextMarshaler
				*(*goIface)(unsafe.Pointer(&tm)) = goIface{tab: itab, data: ptr}
				return tm.MarshalText()
			}
			e.ti.Flags |= tiFlagHasTextMarshalFn
		} else if ptrType.Implements(textMarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Interface().(encoding.TextMarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.TextMarshalFn = func(ptr unsafe.Pointer) ([]byte, error) {
				var tm encoding.TextMarshaler
				*(*goIface)(unsafe.Pointer(&tm)) = goIface{tab: itab, data: ptr}
				return tm.MarshalText()
			}
			e.ti.Flags |= tiFlagHasTextMarshalFn
		}

		if t.Implements(textUnmarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Elem().Interface().(encoding.TextUnmarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.TextUnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				var tu encoding.TextUnmarshaler
				*(*goIface)(unsafe.Pointer(&tu)) = goIface{tab: itab, data: ptr}
				return tu.UnmarshalText(data)
			}
			e.ti.Flags |= tiFlagHasTextUnmarshalFn
		} else if ptrType.Implements(textUnmarshalerType) {
			sentinel := reflect.New(t)
			iface := sentinel.Interface().(encoding.TextUnmarshaler)
			itab := extractItab(unsafe.Pointer(&iface))
			ext.TextUnmarshalFn = func(ptr unsafe.Pointer, data []byte) error {
				var tu encoding.TextUnmarshaler
				*(*goIface)(unsafe.Pointer(&tu)) = goIface{tab: itab, data: ptr}
				return tu.UnmarshalText(data)
			}
			e.ti.Flags |= tiFlagHasTextUnmarshalFn
		}
	}

	bindScanArrayFn(e.ti)

	bindEncodeFn(e.ti)
	bindStructFieldEncodeFns(e.ti)
	e.ti.HintBytes = computeHintBytes(e.ti, 0)
	e.ti.UFlags = e.ti.Flags & tiFlagUnmarshalMask

	close(e.done)
	// Promote: replace the transient *codecEntry with the final *TypeInfo
	// so subsequent loads hit the fast path (no channel recv).
	codecCache.Store(t, e.ti)
	return e.ti
}

// CollectStructFields collects fields from a struct type, promoting
// anonymous embedded struct fields using breadth-first search.
//
// Priority rules (matching encoding/json):
//   - Direct (depth-0) fields always win over embedded fields.
//   - Among embedded fields, shallower depth wins.
//   - At the same depth, conflicting names cancel each other (neither appears).
//   - Unexported anonymous struct fields still promote their exported children.
//   - Field output order matches struct declaration order (by index path).
func CollectStructFields(t reflect.Type, baseOffset uintptr) []TypeInfo {
	// nameInfo tracks the winning field for each JSON name.
	type nameInfo struct {
		depth int // depth at which this name was first seen
		index int // index in fields[]; -1 = canceled
	}

	// BFS queue entry.
	type bfsEntry struct {
		typ       reflect.Type
		offset    uintptr
		indexPath []int // field index path from root
	}

	// fieldWithOrder pairs a TypeInfo with its index path for sorting.
	type fieldWithOrder struct {
		ti        TypeInfo
		indexPath []int
	}

	var fields []fieldWithOrder
	names := make(map[string]*nameInfo) // JSON name → winner info

	// addField attempts to insert a field.
	addField := func(fi TypeInfo, depth int, idxPath []int) {
		name := fi.JSONName
		if ni, ok := names[name]; ok {
			if ni.depth < depth {
				return // shallower depth already owns this name
			}
			if ni.depth == depth {
				// Same depth conflict — cancel the earlier entry.
				if ni.index >= 0 {
					fields[ni.index].ti = TypeInfo{} // zero out — compacted later
					ni.index = -1
				}
				return
			}
			// depth < ni.depth: current field is shallower — replace.
			if ni.index >= 0 {
				fields[ni.index].ti = TypeInfo{}
			}
			ni.depth = depth
			ni.index = len(fields)
			fields = append(fields, fieldWithOrder{fi, idxPath})
			return
		}
		names[name] = &nameInfo{depth: depth, index: len(fields)}
		fields = append(fields, fieldWithOrder{fi, idxPath})
	}

	// collectDirect scans one struct level: adds non-anonymous exported
	// fields, and returns embedded structs for the next BFS level.
	collectDirect := func(st reflect.Type, base uintptr, depth int, parentPath []int) []bfsEntry {
		var nextLevel []bfsEntry
		for i := range st.NumField() {
			sf := st.Field(i)

			// Build index path for this field.
			idxPath := make([]int, len(parentPath)+1)
			copy(idxPath, parentPath)
			idxPath[len(parentPath)] = i

			// Anonymous struct embedding — queue for next depth level.
			// Allow unexported anonymous structs (their exported fields
			// are still promoted per encoding/json rules).
			if sf.Anonymous {
				ft := sf.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					nextLevel = append(nextLevel, bfsEntry{ft, base + sf.Offset, idxPath})
					continue
				}
			}

			if !sf.IsExported() {
				continue
			}

			// Parse json tag.
			jsonName := sf.Name
			omitEmpty := false
			quoted := false
			copyStr := false
			if tag := sf.Tag.Get("json"); tag != "" {
				if tag == "-" {
					continue
				}
				if before, opts, ok := strings.Cut(tag, ","); ok {
					jsonName = before
					omitEmpty = strings.Contains(opts, "omitempty")
					quoted = strings.Contains(opts, "string")
					copyStr = strings.Contains(opts, "copy")
				} else {
					jsonName = tag
				}
				if jsonName == "" {
					jsonName = sf.Name
				}
			}

			cached := getCodecForCycle(sf.Type)

			if quoted {
				switch cached.Kind {
				case KindPointer:
					if cached.Codec != nil {
						pDec := cached.Codec.(*PointerCodec)
						if !isQuotableKind(pDec.ElemTI.Kind) {
							quoted = false
						}
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
				Offset:        base + sf.Offset,
				JSONName:      jsonName,
				JSONNameLower: toLowerASCII(jsonName),
				Codec:         cached.Codec,
			}

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
			if copyStr && fi.Kind == KindString {
				fi.Flags |= tiFlagCopyString
			}
			fi.UFlags = fi.Flags & tiFlagUnmarshalMask

			addField(fi, depth, idxPath)
		}
		return nextLevel
	}

	// BFS: start with the root struct at depth 0.
	current := []bfsEntry{{t, baseOffset, nil}}
	visited := map[reflect.Type]bool{} // prevent infinite recursion
	for depth := 0; len(current) > 0; depth++ {
		var next []bfsEntry
		for _, e := range current {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			next = append(next, collectDirect(e.typ, e.offset, depth, e.indexPath)...)
		}
		current = next
	}

	// Sort by index path to match struct declaration order.
	sort.Slice(fields, func(i, j int) bool {
		a, b := fields[i].indexPath, fields[j].indexPath
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})

	// Compact: remove canceled (zero-value) entries.
	result := make([]TypeInfo, 0, len(fields))
	for i := range fields {
		if fields[i].ti.JSONName != "" {
			result = append(result, fields[i].ti)
		}
	}
	return result
}

// Codec Builders

// StructCodec holds pre-computed metadata for struct encoding/decoding.
type StructCodec struct {
	Typ    reflect.Type
	Fields []TypeInfo

	// Pre-built encode steps for compact (non-indent) marshaling.
	// Each step encodes one field with all branching resolved at build time.
	EncodeSteps []structEncodeStep

	// Tiered lookup — set by buildLookup at construction time.
	Lookup       fieldLookup // polymorphic field lookup strategy
	HasMixedCase bool        // true if any JSONName differs from JSONNameLower

	// Native C encoder VM cache — lazily initialized by getBlueprint().
	vm atomic.Pointer[encvmCache]
}

// vmCache returns the (lazily allocated) encoder VM cache.
func (dec *StructCodec) vmCache() *encvmCache {
	if p := dec.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if dec.vm.CompareAndSwap(nil, p) {
		return p
	}
	return dec.vm.Load()
}

func BuildStructCodec(t reflect.Type) *StructCodec {
	dec := &StructCodec{Typ: t}
	dec.Fields = CollectStructFields(t, 0)
	buildLookup(dec)
	return dec
}

// SliceCodec holds pre-computed metadata for slice encoding/decoding.
type SliceCodec struct {
	SliceType      reflect.Type
	ElemType       reflect.Type
	ElemSize       uintptr
	ElemTI         *TypeInfo
	EmptySliceData unsafe.Pointer
	ElemHasPtr     bool
	ElemRType      unsafe.Pointer
	capHint        atomic.Int32 // adaptive: EMA of observed array lengths
	emaAlpha       int32        // EMA smoothing denominator; 0 means default (2)

	vm atomic.Pointer[encvmCache]
}

func (d *SliceCodec) vmCache() *encvmCache {
	if p := d.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if d.vm.CompareAndSwap(nil, p) {
		return p
	}
	return d.vm.Load()
}

// SetEMAAlpha sets the EMA smoothing denominator for adaptive array capacity.
// Default 2 (equal-weight). Higher values respond more slowly to length changes.
func (d *SliceCodec) SetEMAAlpha(alpha int32) {
	if alpha < 2 {
		alpha = 2
	}
	d.emaAlpha = alpha
}

func BuildSliceCodec(t reflect.Type) *SliceCodec {
	elemTI := getCodecForCycle(t.Elem())
	emptySlice := reflect.MakeSlice(t, 0, 0)
	return &SliceCodec{
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

// ArrayCodec holds pre-computed metadata for encoding/decoding [N]T arrays.
type ArrayCodec struct {
	ArrayType  reflect.Type
	ElemType   reflect.Type
	ElemSize   uintptr
	ArrayLen   int // compile-time fixed N
	ElemTI     *TypeInfo
	ElemHasPtr bool
	ElemRType  unsafe.Pointer

	vm atomic.Pointer[encvmCache]
}

func (d *ArrayCodec) vmCache() *encvmCache {
	if p := d.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if d.vm.CompareAndSwap(nil, p) {
		return p
	}
	return d.vm.Load()
}

func BuildArrayCodec(t reflect.Type) *ArrayCodec {
	elemTI := getCodecForCycle(t.Elem())
	return &ArrayCodec{
		ArrayType:  t,
		ElemType:   t.Elem(),
		ElemSize:   t.Elem().Size(),
		ArrayLen:   t.Len(),
		ElemTI:     elemTI,
		ElemHasPtr: typeContainsPointer(t.Elem()),
		ElemRType:  rtypePtr(t.Elem()),
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

// MapCodec holds pre-computed metadata for map[K]V encoding/decoding.
type MapCodec struct {
	MapType reflect.Type
	KeyType reflect.Type
	ValType reflect.Type
	ValSize uintptr
	ValTI   *TypeInfo
	KeyTI   *TypeInfo // key type info (for TextMarshaler on non-string keys)

	MapKind     MapVariant     // specialized map type for fast path
	mapRType    unsafe.Pointer // cached *abi.MapType for mapsIterInit
	keyRType    unsafe.Pointer // cached *abi.Type for key (used by unsafe_New)
	valRType    unsafe.Pointer // cached *abi.Type for value (used by unsafe_New/typedmemmove)
	isStringKey bool           // KeyType.Kind() == reflect.String
	valHasPtr   bool           // value type contains GC-traced pointers
	KeySize     uintptr
	SlotSize    uintptr // Swiss Map slot size (0 = not probed or probe failed)
	ScanMapFn   func(sc *Parser, src []byte, idx int, ptr unsafe.Pointer) (int, error)
	vm          atomic.Pointer[encvmCache]
}

func (d *MapCodec) vmCache() *encvmCache {
	if p := d.vm.Load(); p != nil {
		return p
	}
	p := &encvmCache{}
	if d.vm.CompareAndSwap(nil, p) {
		return p
	}
	return d.vm.Load()
}

func BuildMapCodec(t reflect.Type) *MapCodec {
	valTI := getCodecForCycle(t.Elem())
	keyTI := getCodecForCycle(t.Key())
	isStringKey := t.Key().Kind() == reflect.String
	mc := &MapCodec{
		MapType:     t,
		KeyType:     t.Key(),
		ValType:     t.Elem(),
		ValSize:     t.Elem().Size(),
		ValTI:       valTI,
		KeyTI:       keyTI,
		MapKind:     MapVariantGeneric,
		mapRType:    rtypePtr(t),
		keyRType:    rtypePtr(t.Key()),
		valRType:    rtypePtr(t.Elem()),
		isStringKey: isStringKey,
		valHasPtr:   typeContainsPointer(t.Elem()),
		KeySize:     t.Key().Size(),
	}
	if isStringKey {
		switch valTI.Kind {
		case KindString:
			mc.ScanMapFn = (*Parser).scanMapStringString
			mc.MapKind = MapVariantStrStr
		case KindInt:
			mc.ScanMapFn = (*Parser).scanMapStringInt
			mc.MapKind = MapVariantStrInt
		case KindInt64:
			mc.ScanMapFn = (*Parser).scanMapStringInt64
			mc.MapKind = MapVariantStrInt64
		}
		// Probe Swiss Map slot size for generic string-key iterator.
		// This enables MAP_STR_ITER for map[string]<any_value_type>.
		if slotSize, ok := probeSwissMapSlotSize(t, t.Elem().Size()); ok {
			mc.SlotSize = slotSize
		}
	}
	return mc
}

type PointerCodec struct {
	PtrType    reflect.Type
	ElemType   reflect.Type
	ElemTI     *TypeInfo
	ElemSize   uintptr
	ElemHasPtr bool
	ElemRType  unsafe.Pointer
}

func BuildPointerCodec(t reflect.Type) *PointerCodec {
	elemTI := getCodecForCycle(t.Elem())
	return &PointerCodec{
		PtrType:    t,
		ElemType:   t.Elem(),
		ElemTI:     elemTI,
		ElemSize:   t.Elem().Size(),
		ElemHasPtr: typeContainsPointer(t.Elem()),
		ElemRType:  rtypePtr(t.Elem()),
	}
}

// Marshal helpers

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

// Zero-value detection for omitempty.

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
	case reflect.Slice:
		return func(ptr unsafe.Pointer) bool {
			sh := (*SliceHeader)(ptr)
			return sh.Data == nil || sh.Len == 0
		}
	case reflect.Map:
		return func(ptr unsafe.Pointer) bool {
			// A map variable is a pointer to the internal hmap.
			// nil pointer → nil map; otherwise use reflect for len check.
			if *(*unsafe.Pointer)(ptr) == nil {
				return true
			}
			return reflect.NewAt(t, ptr).Elem().Len() == 0
		}
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
		// No exported fields to check (e.g. time.Time has only unexported fields).
		// Return nil so the field is never omitted — matching encoding/json behavior
		// which does not apply omitempty to struct values.
		return nil
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

// typeFromRTypePtr reconstructs a reflect.Type from a raw *abi.Type pointer
// (reverse of rtypePtr).
func typeFromRTypePtr(p unsafe.Pointer) reflect.Type {
	// Construct reflect.Type by copying the itab from a donor and
	// setting the data word to p.
	var dummy reflect.Type
	eface := (*[2]unsafe.Pointer)(unsafe.Pointer(&dummy))
	donor := reflect.TypeFor[int]()
	donorEface := (*[2]unsafe.Pointer)(unsafe.Pointer(&donor))
	eface[0] = donorEface[0] // copy itab
	eface[1] = p             // set *rtype data word
	return dummy
}
