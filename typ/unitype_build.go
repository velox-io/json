package typ

import (
	"encoding"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
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
	return buildUniType(t, true)
}

// PartialUniTypeOf returns the shell immediately for recursive type builds.
func PartialUniTypeOf(t reflect.Type) *UniType {
	if v, ok := uniTypeCache.Load(t); ok {
		switch e := v.(type) {
		case *UniType:
			return e
		case *uniTypePending:
			return e.ut
		}
	}
	return buildUniType(t, false)
}

var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	numberType     = reflect.TypeFor[json.Number]()
)

func buildUniType(t reflect.Type, wait bool) *UniType {
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
			if wait {
				<-existing.done
			}
			return existing.ut
		}
	}

	ut := p.ut

	// Special aliases override the default reflect.Kind mapping.
	switch t {
	case rawMessageType:
		ut.Kind = KindRawMessage
	case numberType:
		ut.Kind = KindNumber
	}

	// Pointer kinds pick up hooks from the dereferenced element path.
	if t.Kind() != reflect.Pointer {
		ut.Hooks = detectInterfaceHooks(t)
	}

	switch t.Kind() {
	case reflect.Struct:
		ut.Ext = buildStructTypeInfo(t)
	case reflect.Slice:
		ut.Ext = buildSliceTypeInfo(t)
	case reflect.Array:
		ut.Ext = buildArrayTypeInfo(t)
	case reflect.Map:
		ut.Ext = buildMapTypeInfo(t)
	case reflect.Pointer:
		ut.Ext = buildPointerTypeInfo(t)
	}

	// Publish the completed descriptor in place of the pending shell.
	uniTypeCache.Store(t, ut)
	close(p.done)
	return ut
}

func detectInterfaceHooks(t reflect.Type) *InterfaceHooks {
	marshalerType := reflect.TypeFor[json.Marshaler]()
	unmarshalerType := reflect.TypeFor[json.Unmarshaler]()
	textMarshalerType := reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerType := reflect.TypeFor[encoding.TextUnmarshaler]()
	ptrType := reflect.PointerTo(t)

	var hooks InterfaceHooks
	any := false

	if t.Implements(marshalerType) {
		hooks.MarshalFn = bindMarshalerValue(t)
		any = true
	} else if ptrType.Implements(marshalerType) {
		hooks.MarshalFn = bindMarshalerPtr(t)
		any = true
	}

	if t.Implements(unmarshalerType) {
		hooks.UnmarshalFn = bindUnmarshalerValue(t)
		any = true
	} else if ptrType.Implements(unmarshalerType) {
		hooks.UnmarshalFn = bindUnmarshalerPtr(t)
		any = true
	}

	if t.Implements(textMarshalerType) {
		hooks.TextMarshalFn = bindTextMarshalerValue(t)
		any = true
	} else if ptrType.Implements(textMarshalerType) {
		hooks.TextMarshalFn = bindTextMarshalerPtr(t)
		any = true
	}

	if t.Implements(textUnmarshalerType) {
		hooks.TextUnmarshalFn = bindTextUnmarshalerValue(t)
		any = true
	} else if ptrType.Implements(textUnmarshalerType) {
		hooks.TextUnmarshalFn = bindTextUnmarshalerPtr(t)
		any = true
	}

	if !any {
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

func buildStructTypeInfo(t reflect.Type) *StructTypeInfo {
	return &StructTypeInfo{
		Fields: collectStructFields(t, 0),
	}
}

func buildSliceTypeInfo(t reflect.Type) *SliceTypeInfo {
	elemUT := PartialUniTypeOf(t.Elem())
	emptySlice := reflect.MakeSlice(t, 0, 0)
	return &SliceTypeInfo{
		ElemType:       elemUT,
		ElemHasPtr:     TypeContainsPointer(t.Elem()),
		EmptySliceData: unsafe.Pointer(emptySlice.Pointer()),
	}
}

func buildArrayTypeInfo(t reflect.Type) *ArrayTypeInfo {
	elemUT := PartialUniTypeOf(t.Elem())
	return &ArrayTypeInfo{
		ElemType:   elemUT,
		ElemHasPtr: TypeContainsPointer(t.Elem()),
		ArrayLen:   t.Len(),
	}
}

func buildMapTypeInfo(t reflect.Type) *MapTypeInfo {
	keyUT := PartialUniTypeOf(t.Key())
	valUT := PartialUniTypeOf(t.Elem())
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

func buildPointerTypeInfo(t reflect.Type) *PointerTypeInfo {
	elemUT := PartialUniTypeOf(t.Elem())
	return &PointerTypeInfo{
		ElemType:   elemUT,
		ElemHasPtr: TypeContainsPointer(t.Elem()),
	}
}

// collectStructFields matches encoding/json field promotion rules.
// Direct fields win, shallower embedded fields win, and same-depth conflicts
// cancel. Unexported anonymous structs may still promote exported children.
func collectStructFields(t reflect.Type, baseOffset uintptr) []StructField {
	type nameInfo struct {
		depth int
		index int // index in fields[]; -1 = canceled
	}
	type bfsEntry struct {
		typ       reflect.Type
		offset    uintptr
		indexPath []int
	}
	type fieldWithOrder struct {
		sf        StructField
		indexPath []int
	}

	var fields []fieldWithOrder
	names := make(map[string]*nameInfo)

	addField := func(sf StructField, depth int, idxPath []int) {
		name := sf.JSONName
		if ni, ok := names[name]; ok {
			if ni.depth < depth {
				return
			}
			if ni.depth == depth {
				if ni.index >= 0 {
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

	collectDirect := func(st reflect.Type, base uintptr, depth int, parentPath []int) []bfsEntry {
		var nextLevel []bfsEntry
		for i := range st.NumField() {
			rf := st.Field(i)

			idxPath := make([]int, len(parentPath)+1)
			copy(idxPath, parentPath)
			idxPath[len(parentPath)] = i

			if rf.Anonymous {
				ft := rf.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					nextLevel = append(nextLevel, bfsEntry{ft, base + rf.Offset, idxPath})
					continue
				}
			}

			if !rf.IsExported() {
				continue
			}

			jsonName := rf.Name
			omitEmpty := false
			quoted := false
			copyStr := false
			if tag := rf.Tag.Get("json"); tag != "" {
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
					jsonName = rf.Name
				}
			}

			fieldUT := PartialUniTypeOf(rf.Type)

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
			if copyStr && fieldUT.Kind == KindString {
				tagFlags |= TagFlagCopyString
			}

			sf := StructField{
				FieldType:      fieldUT,
				TagFlags:       tagFlags,
				Offset:         base + rf.Offset,
				JSONName:       jsonName,
				KeyBytes:       encodeKeyBytes(jsonName),
				KeyBytesIndent: encodeKeyBytesIndent(jsonName),
				IsZeroFn:       makeIsZero(rf.Type),
			}

			addField(sf, depth, idxPath)
		}
		return nextLevel
	}

	// Breadth-first traversal preserves embedding depth precedence.
	current := []bfsEntry{{t, baseOffset, nil}}
	visited := map[reflect.Type]bool{}
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
	return result
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
