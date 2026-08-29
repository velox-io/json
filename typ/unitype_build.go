package typ

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/value"
)

var uniTypeCache sync.Map // reflect.Type → *UniType or *uniTypePending

type uniTypePending struct {
	ut   *UniType
	done chan struct{}
}

// UniTypeOf returns the cached descriptor, waiting for in-flight builds.
func UniTypeOf(t reflect.Type) *UniType {
	if v, ok := uniTypeCache.Load(t); ok {
		switch e := v.(type) {
		case *UniType:
			return e
		case *uniTypePending:
			<-e.done
			return e.ut
		}
	}
	return buildUniType(t, nil)
}

// PartialUniTypeOf is the entry point for recursive type builds. The
// returned shell may have Ext == nil if t is in the current build chain.
func PartialUniTypeOf(t reflect.Type) *UniType {
	return partialUniTypeOf(t, nil)
}

// partialUniTypeOf checks the in-flight building map first to break
// cycles; otherwise it goes through the global cache and waits on any
// other goroutine's pending build.
func partialUniTypeOf(t reflect.Type, building map[reflect.Type]*UniType) *UniType {
	if ut, ok := building[t]; ok {
		return ut
	}
	if v, ok := uniTypeCache.Load(t); ok {
		switch e := v.(type) {
		case *UniType:
			return e
		case *uniTypePending:
			<-e.done
			return e.ut
		}
	}
	return buildUniType(t, building)
}

var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	numberType     = reflect.TypeFor[json.Number]()

	valueType = reflect.TypeFor[value.Value]()
)

// buildUniType assembles the descriptor for t. building holds shells the
// current goroutine has in flight, so recursive references reuse them
// without observing partial mutations through the global cache.
func buildUniType(t reflect.Type, building map[reflect.Type]*UniType) *UniType {
	p := &uniTypePending{
		ut: &UniType{
			Kind: KindForType(t),
			Type: t,
			Ptr:  gort.TypePtr(t),
			Size: t.Size(),
		},
		done: make(chan struct{}),
	}

	actual, loaded := uniTypeCache.LoadOrStore(t, p)
	if loaded {
		switch existing := actual.(type) {
		case *UniType:
			return existing
		case *uniTypePending:
			// Another goroutine owns this build; wait for it to publish.
			<-existing.done
			return existing.ut
		}
	}

	ut := p.ut
	if building == nil {
		building = map[reflect.Type]*UniType{}
	}
	building[t] = ut

	// Special aliases override the default reflect.Kind mapping.
	// value.Value is its own kind (KindValue): bind.h emits an opaque parsed
	// document descriptor. json.RawMessage stays KindRawMessage (byte-span
	// capture). stream.Stream[T] is mapped to
	// KindStream: storage layout is identical to KindSlice (slice header at
	// cur_dst, SlotClass-backed, grow via SLICE_GROW), but kind dispatch at
	// the few yield-policy points (empty-open / array close / drain) lets the
	// Go driver intercept yields to invoke the per-batch handler. The
	// mapping must happen here, before any reflect.Slice handling or struct
	// recursion, or these types would be treated as plain structs.
	var streamElemType reflect.Type
	switch {
	case t == valueType:
		ut.Kind = KindValue
	case t == rawMessageType:
		ut.Kind = KindRawMessage
	case t == numberType:
		ut.Kind = KindNumber
	case IsStreamType(t):
		ut.Kind = KindStream
		streamElemType = StreamElementType(t)
	}

	// Pointer kinds pick up hooks from the dereferenced element path.
	//
	// KindRawMessage is excluded from the hook scan: the engine captures its
	// byte span natively, and leaving Hooks populated would demote it to
	// KindUnmarshaler. value.Value is NOT excluded: it implements Marshaler
	// (so venc routes encode through MarshalJSON / tapeToJSON) but not
	// Unmarshaler (so no demotion; vbind still classifies it as KindValue).
	if t.Kind() != reflect.Pointer && ut.Kind != KindRawMessage {
		ut.Hooks = detectInterfaceHooks(t)
	}

	switch t.Kind() {
	case reflect.Struct:
		if streamElemType != nil {
			// stream.Stream[T] was remapped to KindStream above. Build the
			// slice ext from the element type obtained via the ElemType()
			// method (reflect has no public API for generic type params),
			// so vbind collects T and registers the slice SlotClass exactly
			// like a real []T field. The Stream struct's own payload (handler,
			// etc.) is never recursed into.
			//
			// partialUniTypeOf carries the building map so a recursive Stream
			// element (e.g. struct{ Children Stream[self] }) breaks the cycle
			// instead of infinite-recursing; vbind's streamRecurses check then
			// rejects the type at collect time.
			ut.Ext = &SliceTypeInfo{
				ElemType:       partialUniTypeOf(streamElemType, building),
				ElemHasPtr:     TypeContainsPointer(streamElemType),
				EmptySliceData: EmptySliceDataFor(streamElemType),
			}
		} else {
			ut.Ext = buildStructTypeInfo(t, building)
		}
	case reflect.Slice:
		ut.Ext = buildSliceTypeInfo(t, building)
	case reflect.Array:
		ut.Ext = buildArrayTypeInfo(t, building)
	case reflect.Map:
		ut.Ext = buildMapTypeInfo(t, building)
	case reflect.Pointer:
		ut.Ext = buildPointerTypeInfo(t, building)
	}

	// Publish the completed descriptor; close establishes happens-before
	// for waiters on done.
	uniTypeCache.Store(t, ut)
	close(p.done)
	return ut
}

var marshalerType = reflect.TypeFor[json.Marshaler]()
var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

func detectInterfaceHooks(t reflect.Type) *InterfaceHooks {
	ptrType := reflect.PointerTo(t)

	var hooks InterfaceHooks
	present := false

	if t.Implements(marshalerType) {
		hooks.MarshalFn = bindMarshalerValue(t)
		present = true
	} else if ptrType.Implements(marshalerType) {
		hooks.MarshalFn = bindMarshalerPtr(t)
		present = true
	}

	if t.Implements(unmarshalerType) {
		hooks.UnmarshalFn = bindUnmarshalerValue(t)
		present = true
	} else if ptrType.Implements(unmarshalerType) {
		hooks.UnmarshalFn = bindUnmarshalerPtr(t)
		present = true
	}

	if t.Implements(textMarshalerType) {
		hooks.TextMarshalFn = bindTextMarshalerValue(t)
		present = true
	} else if ptrType.Implements(textMarshalerType) {
		hooks.TextMarshalFn = bindTextMarshalerPtr(t)
		present = true
	}

	if t.Implements(textUnmarshalerType) {
		hooks.TextUnmarshalFn = bindTextUnmarshalerValue(t)
		present = true
	} else if ptrType.Implements(textUnmarshalerType) {
		hooks.TextUnmarshalFn = bindTextUnmarshalerPtr(t)
		present = true
	}

	if !present {
		return nil
	}
	return &hooks
}

func bindMarshalerValue(t reflect.Type) func(unsafe.Pointer) ([]byte, error) {
	sentinel := reflect.New(t)
	iface := sentinel.Elem().Interface().(json.Marshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer) ([]byte, error) {
		var m json.Marshaler
		*(*gort.GoIface)(unsafe.Pointer(&m)) = gort.GoIface{Tab: itab, Data: ptr}
		return m.MarshalJSON()
	}
}

func bindMarshalerPtr(t reflect.Type) func(unsafe.Pointer) ([]byte, error) {
	sentinel := reflect.New(t)
	iface := sentinel.Interface().(json.Marshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer) ([]byte, error) {
		var m json.Marshaler
		*(*gort.GoIface)(unsafe.Pointer(&m)) = gort.GoIface{Tab: itab, Data: ptr}
		return m.MarshalJSON()
	}
}

func bindUnmarshalerValue(t reflect.Type) func(unsafe.Pointer, []byte) error {
	sentinel := reflect.New(t)
	iface := sentinel.Elem().Interface().(json.Unmarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer, data []byte) error {
		var u json.Unmarshaler
		*(*gort.GoIface)(unsafe.Pointer(&u)) = gort.GoIface{Tab: itab, Data: ptr}
		return u.UnmarshalJSON(data)
	}
}

func bindUnmarshalerPtr(t reflect.Type) func(unsafe.Pointer, []byte) error {
	sentinel := reflect.New(t)
	iface := sentinel.Interface().(json.Unmarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer, data []byte) error {
		var u json.Unmarshaler
		*(*gort.GoIface)(unsafe.Pointer(&u)) = gort.GoIface{Tab: itab, Data: ptr}
		return u.UnmarshalJSON(data)
	}
}

func bindTextMarshalerValue(t reflect.Type) func(unsafe.Pointer) ([]byte, error) {
	sentinel := reflect.New(t)
	iface := sentinel.Elem().Interface().(encoding.TextMarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer) ([]byte, error) {
		var tm encoding.TextMarshaler
		*(*gort.GoIface)(unsafe.Pointer(&tm)) = gort.GoIface{Tab: itab, Data: ptr}
		return tm.MarshalText()
	}
}

func bindTextMarshalerPtr(t reflect.Type) func(unsafe.Pointer) ([]byte, error) {
	sentinel := reflect.New(t)
	iface := sentinel.Interface().(encoding.TextMarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer) ([]byte, error) {
		var tm encoding.TextMarshaler
		*(*gort.GoIface)(unsafe.Pointer(&tm)) = gort.GoIface{Tab: itab, Data: ptr}
		return tm.MarshalText()
	}
}

func bindTextUnmarshalerValue(t reflect.Type) func(unsafe.Pointer, []byte) error {
	sentinel := reflect.New(t)
	iface := sentinel.Elem().Interface().(encoding.TextUnmarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer, data []byte) error {
		var tu encoding.TextUnmarshaler
		*(*gort.GoIface)(unsafe.Pointer(&tu)) = gort.GoIface{Tab: itab, Data: ptr}
		return tu.UnmarshalText(data)
	}
}

func bindTextUnmarshalerPtr(t reflect.Type) func(unsafe.Pointer, []byte) error {
	sentinel := reflect.New(t)
	iface := sentinel.Interface().(encoding.TextUnmarshaler)
	itab := gort.ExtractItab(unsafe.Pointer(&iface))
	return func(ptr unsafe.Pointer, data []byte) error {
		var tu encoding.TextUnmarshaler
		*(*gort.GoIface)(unsafe.Pointer(&tu)) = gort.GoIface{Tab: itab, Data: ptr}
		return tu.UnmarshalText(data)
	}
}

func buildStructTypeInfo(t reflect.Type, building map[reflect.Type]*UniType) *StructTypeInfo {
	fields, rejects := collectStructFields(t, 0, building)
	return &StructTypeInfo{
		Fields:  fields,
		Rejects: rejects,
	}
}

func buildSliceTypeInfo(t reflect.Type, building map[reflect.Type]*UniType) *SliceTypeInfo {
	elemUT := partialUniTypeOf(t.Elem(), building)
	emptySlice := reflect.MakeSlice(t, 0, 0)
	return &SliceTypeInfo{
		ElemType:       elemUT,
		ElemHasPtr:     TypeContainsPointer(t.Elem()),
		EmptySliceData: unsafe.Pointer(emptySlice.Pointer()),
	}
}

func buildArrayTypeInfo(t reflect.Type, building map[reflect.Type]*UniType) *ArrayTypeInfo {
	elemUT := partialUniTypeOf(t.Elem(), building)
	return &ArrayTypeInfo{
		ElemType:   elemUT,
		ElemHasPtr: TypeContainsPointer(t.Elem()),
		ArrayLen:   t.Len(),
	}
}

func buildMapTypeInfo(t reflect.Type, building map[reflect.Type]*UniType) *MapTypeInfo {
	keyUT := partialUniTypeOf(t.Key(), building)
	valUT := partialUniTypeOf(t.Elem(), building)
	isStringKey := t.Key().Kind() == reflect.String
	mi := &MapTypeInfo{
		KeyType:     keyUT,
		ValType:     valUT,
		MapKind:     MapVariantGeneric,
		IsStringKey: isStringKey,
		ValHasPtr:   TypeContainsPointer(t.Elem()),
	}
	if isStringKey {
		switch valUT.Kind {
		case KindString:
			mi.MapKind = MapVariantStrStr
		case KindInt:
			mi.MapKind = MapVariantStrInt
		case KindInt64:
			mi.MapKind = MapVariantStrInt64
		}
	}
	return mi
}

func buildPointerTypeInfo(t reflect.Type, building map[reflect.Type]*UniType) *PointerTypeInfo {
	elemUT := partialUniTypeOf(t.Elem(), building)
	return &PointerTypeInfo{
		ElemType:   elemUT,
		ElemHasPtr: TypeContainsPointer(t.Elem()),
	}
}

// collectStructFields matches encoding/json field promotion rules.
// Direct fields win, shallower embedded fields win, and same-depth conflicts
// cancel. Unexported anonymous structs may still promote exported children.
//
// The second result lists shapes that cannot be represented; see
// StructTypeInfo.Rejects for why they travel as data rather than an error.
func collectStructFields(t reflect.Type, baseOffset uintptr, building map[reflect.Type]*UniType) ([]StructField, []string) {
	type nameInfo struct {
		depth int
		index int // index in fields[]; -1 = canceled
	}
	type bfsEntry struct {
		typ       reflect.Type
		offset    uintptr
		indexPath []int

		// ptrPath carries the embedded-pointer hops crossed to reach this level,
		// outermost first. It stays nil for the overwhelmingly common all-inlined
		// path. Once it is non-empty, offset is relative to the last hop's
		// pointee rather than to the host, so the two are read as a pair.
		ptrPath []PtrHop
	}
	type fieldWithOrder struct {
		sf        StructField
		indexPath []int
	}

	var fields []fieldWithOrder
	var rejects []string
	names := make(map[string]*nameInfo)

	addField := func(sf StructField, depth int, idxPath []int) {
		name := sf.JSONName
		if ni, ok := names[name]; ok {
			if ni.depth < depth {
				return
			}
			if ni.depth == depth {
				if ni.index >= 0 {
					// Two reserve-unknown fields reaching the same depth is a
					// mistake, not a name collision the author can have meant:
					// the name is a synthetic sentinel, so no JSON key selects
					// either one and both would silently stay empty. Ordinary
					// same-name fields still cancel without comment.
					if name == ReserveUnknownName {
						rejects = append(rejects, fmt.Sprintf(
							"struct %s has two `json:\",embed\"` value.Value fields at the same embedding depth; a struct reserves unknown keys into at most one field",
							t))
					}
					fields[ni.index].sf = StructField{}
					ni.index = -1
				}
				return
			}
			if ni.index >= 0 {
				fields[ni.index].sf = StructField{}
			}
			ni.depth = depth
			ni.index = len(fields)
			fields = append(fields, fieldWithOrder{sf, idxPath})
			return
		}
		names[name] = &nameInfo{depth: depth, index: len(fields)}
		fields = append(fields, fieldWithOrder{sf, idxPath})
	}

	collectDirect := func(st reflect.Type, base uintptr, depth int, parentPath []int, ptrPath []PtrHop, ambiguous bool) []bfsEntry {
		var nextLevel []bfsEntry
		for i := range st.NumField() {
			rf := st.Field(i)

			idxPath := make([]int, len(parentPath)+1)
			copy(idxPath, parentPath)
			idxPath[len(parentPath)] = i

			// Declared before the goto into namedField, which may not jump over
			// a declaration that is in scope at the label.
			promote := false

			if rf.Anonymous {
				ft := rf.Type
				isPtr := ft.Kind() == reflect.Pointer
				if isPtr {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					// if an anonymous field has a JSON tag with an explicit name (e.g. json:"addr"),
					// treat it as a named field instead of promoting its children.
					// Fields with json:",omitempty" (empty name) promote.
					// json:"-" excludes the entire embedded field (no promotion of its children either).
					if tag := rf.Tag.Get("json"); tag != "" {
						if tag == "-" {
							continue
						}
						name := tag
						if before, _, ok := strings.Cut(tag, ","); ok {
							name = before
						}
						if name != "" {
							goto namedField
						}
					}
					promote = true
				}
			} else if hasEmbedOption(rf.Tag) {
				// `json:",embed"` promotes a named field's content exactly as Go
				// embedding promotes a named field's content into the host.
				//
				// An explicit name contradicts embedding, which occupies no
				// name of its own; checked here because a struct promotes
				// during the BFS and never reaches namedField.
				if name, _, _ := strings.Cut(rf.Tag.Get("json"), ","); name != "" {
					rejects = append(rejects, fmt.Sprintf(
						"struct %s field %s: `json:%q` gives an embedded field an explicit JSON name; embedding occupies no name, so drop one of the two",
						st, rf.Name, rf.Tag.Get("json")))
					continue
				}
				// Only a struct promotes by offset arithmetic here. The shapes
				// whose promoted set is a run-time choice (value.Value, and an
				// interface with a variant) are classified at namedField, and
				// anything else is refused there.
				ft := rf.Type
				if ft.Kind() == reflect.Struct && ft != valueType && !IsStreamType(ft) {
					promote = true
				}
			}
			if promote {
				ft := rf.Type
				isPtr := ft.Kind() == reflect.Pointer
				if isPtr {
					ft = ft.Elem()
				}
				// Promotion is offset arithmetic: a child is addressed as
				// base+rf.Offset+childOffset. That identity holds only while
				// every hop is inlined storage, so an embedded pointer ends the
				// current run of arithmetic. Everything below it is addressed
				// from the pointee, which does not exist until something writes
				// through it.
				//
				// A hop therefore records the pointer's own location and resets
				// the running offset: subsequent offsets are relative to the new
				// pointee. Consumers walk the hops before applying Offset.
				childPtrPath, childBase := ptrPath, base+rf.Offset
				if isPtr {
					hop := PtrHop{
						SlotOffset:  base + rf.Offset,
						PointeeType: partialUniTypeOf(ft, building),
					}
					// Copied rather than appended in place: sibling embeds at
					// this level share the parent's slice header, and append
					// would let one sibling's hop overwrite another's.
					childPtrPath = append(append([]PtrHop(nil), ptrPath...), hop)
					childBase = 0
				}
				nextLevel = append(nextLevel, bfsEntry{ft, childBase, idxPath, childPtrPath})
				continue
			}
		namedField:

			// anonymous (embedded) fields bypass the IsExported gate: their type name need not be exported
			// because the JSON key is either the explicit tag name or a promoted exported child.
			// Only non-anonymous fields require an exported Go name to be serialized.
			if !rf.Anonymous && !rf.IsExported() {
				continue
			}

			jsonName := rf.Name
			omitEmpty := false
			quoted := false
			embed := hasEmbedOption(rf.Tag)
			if tag := rf.Tag.Get("json"); tag != "" {
				if tag == "-" {
					// `json:"-"` drops the field, so any velox-only option on it is
					// dead. Silence would be wrong here specifically: pairing the
					// dash with a vjson option was how reserve-unknown used to be
					// spelled, so a struct carrying the retired spelling would lose
					// its unknown keys with no diagnostic at all.
					if vj := ParseVJSONTag(rf.Tag); vj.Present {
						rejects = append(rejects, fmt.Sprintf(
							"struct %s field %s has `json:\"-\"` together with `vjson:%q`; the field is excluded, so the option cannot apply. Reserving unknown keys is now spelled `json:\",%s\"` on a value.Value field",
							st, rf.Name, rf.Tag.Get(VJSONTagKey), EmbedOption))
					}
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
					jsonName = rf.Name
				}
			}

			fieldUT := partialUniTypeOf(rf.Type, building)

			// `json:",embed"` promotes a field's content into its host, so the field
			// occupies no JSON member of its own. Which shapes can do that depends on
			// where the promoted set comes from.
			//
			// A struct is already gone by now: it was promoted by offset arithmetic
			// during the BFS above and never reaches this point. What remains is the
			// two shapes whose content is only known at run time, plus everything
			// that cannot embed at all.
			reserveUnknown := false
			embedIface := false
			if embed {
				// An anonymous field reaches namedField only when its tag named it,
				// which is the same contradiction the promotion path rejects.
				if jsonName != rf.Name {
					rejects = append(rejects, fmt.Sprintf(
						"struct %s field %s: `json:%q` gives an embedded field an explicit JSON name; embedding occupies no name, so drop one of the two",
						st, rf.Name, rf.Tag.Get("json")))
					continue
				}
				switch fieldUT.Kind {
				case KindValue:
					// The promoted set is every key the host did not declare, so it
					// is known only once the input has been read.
					reserveUnknown = true
					jsonName = ReserveUnknownName
				case KindAny, KindIface:
					// The promoted set is the variant case the discriminator picks,
					// so vbind resolves it. It needs `vjson:"variant=<disc>"` to name
					// the discriminator, which vbind reports if absent.
					//
					// The field keeps its Go name rather than taking
					// ReserveUnknownName. A struct may host both an embedded variant
					// and a reserve-unknown field (vbind separates their content at
					// struct close), so sharing the sentinel would make the two
					// cancel each other under the same-name rules. The name is
					// unreachable anyway: vbind marks the field TagInlineVariant, and
					// no input key is ever matched against it.
					embedIface = true
				default:
					rejects = append(rejects, fmt.Sprintf(
						"struct %s field %s of type %s cannot be embedded; `json:\",%s\"` needs a struct, a value.Value, or an interface with `vjson:\"variant=<disc>\"`",
						st, rf.Name, rf.Type, EmbedOption))
					continue
				}
			}

			if quoted {
				switch fieldUT.Kind {
				case KindPointer:
					if pi, ok := fieldUT.Ext.(*PointerTypeInfo); ok {
						if !IsQuotableKind(pi.ElemType.Kind) {
							quoted = false
						}
					}
				default:
					if !IsQuotableKind(fieldUT.Kind) {
						quoted = false
					}
				}
			}

			var tagFlags TagFlag
			if omitEmpty {
				tagFlags |= TagFlagOmitEmpty
			}
			if quoted {
				tagFlags |= TagFlagQuoted
			}
			if reserveUnknown {
				tagFlags |= TagFlagReserveUnknown
			}
			if embedIface {
				tagFlags |= TagFlagEmbed
			}

			sf := StructField{
				FieldType:      fieldUT,
				TagFlags:       tagFlags,
				Offset:         base + rf.Offset,
				JSONName:       jsonName,
				GoName:         rf.Name,
				PtrPath:        ptrPath,
				RawTag:         rf.Tag,
				DeclaringType:  st,
				KeyBytes:       encodeKeyBytes(jsonName),
				KeyBytesIndent: encodeKeyBytesIndent(jsonName),
				IsZeroFn:       makeIsZero(rf.Type),
			}
			// Value is a struct (tape-backed) but, like encoding/json's
			// treatment of struct types, omitempty must NOT elide it: a zero
			// Value marshals as null, not as absent. Override the struct zero
			// check so omitempty never fires on a Value field.
			if fieldUT.Kind == KindValue {
				sf.IsZeroFn = nil
			}

			addField(sf, depth, idxPath)

			// A type reached by more than one path at this depth promotes an
			// ambiguous selector, so the name must cancel rather than let the
			// first path win. The BFS visits such a type once, so the duplicate
			// that cancellation looks for never arrives on its own; feeding the
			// same name a second time supplies it. encoding/json does the same
			// (encode.go counts multiplicity and appends a second copy).
			if ambiguous {
				addField(sf, depth, idxPath)
			}
		}
		return nextLevel
	}

	// Breadth-first traversal preserves embedding depth precedence.
	current := []bfsEntry{{t, baseOffset, nil, nil}}
	visited := map[reflect.Type]bool{}
	for depth := 0; len(current) > 0; depth++ {
		// A type queued more than once at this depth was reached by that many
		// distinct paths, which makes every name it promotes an ambiguous
		// selector. visited collapses those paths into one traversal, so the
		// multiplicity has to be measured before the collapse.
		reachedBy := map[reflect.Type]int{}
		for i := range current {
			reachedBy[current[i].typ]++
		}
		var next []bfsEntry
		for _, e := range current {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			next = append(next, collectDirect(e.typ, e.offset, depth, e.indexPath, e.ptrPath, reachedBy[e.typ] > 1)...)
		}
		current = next
	}

	sort.Slice(fields, func(i, j int) bool {
		a, b := fields[i].indexPath, fields[j].indexPath
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})

	result := make([]StructField, 0, len(fields))
	for i := range fields {
		if fields[i].sf.JSONName != "" {
			result = append(result, fields[i].sf)
		}
	}
	return result, rejects
}

// encodeKeyBytes returns compact `"name":`.
// It assumes name needs no JSON escaping.
func encodeKeyBytes(name string) []byte {
	buf := make([]byte, 0, len(name)+4)
	buf = append(buf, '"')
	buf = append(buf, name...)
	buf = append(buf, '"', ':')
	return buf
}

// encodeKeyBytesIndent returns indented `"name": `.
func encodeKeyBytesIndent(name string) []byte {
	buf := make([]byte, 0, len(name)+5)
	buf = append(buf, '"')
	buf = append(buf, name...)
	buf = append(buf, '"', ':', ' ')
	return buf
}

// makeIsZero builds omitempty checks.
func makeIsZero(t reflect.Type) func(unsafe.Pointer) bool {
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
			sh := (*gort.SliceHeader)(ptr)
			return sh.Data == nil || sh.Len == 0
		}
	case reflect.Map:
		return func(ptr unsafe.Pointer) bool {
			if *(*unsafe.Pointer)(ptr) == nil {
				return true
			}
			return reflect.NewAt(t, ptr).Elem().Len() == 0
		}
	case reflect.Pointer, reflect.Interface:
		return func(ptr unsafe.Pointer) bool { return *(*unsafe.Pointer)(ptr) == nil }
	case reflect.Struct:
		return makeStructIsZero(t)
	default:
		return nil
	}
}

func makeStructIsZero(t reflect.Type) func(unsafe.Pointer) bool {
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
		fn := makeIsZero(sf.Type)
		if fn != nil {
			checks = append(checks, fieldCheck{sf.Offset, fn})
		}
	}
	if len(checks) == 0 {
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
