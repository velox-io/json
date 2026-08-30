// Variant descriptors resolve process-wide registrations or method witnesses
// into immutable per-TypeTree dispatch tables. Each BindPolyTable roots its
// generated lookup blob, while PolyCaseData owns the arrays exposed through raw
// ABI pointers. Sibling variants carry per-field state; an inline variant uses
// the host's single merged-tape dispatch slot.

package vbind

import (
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/vlib"
	"github.com/velox-io/json/typ"
)

// JSONVariantCases is a type witness. Its sole parameter declares the descriptor,
// and its body is never called.
const variantMethod = "JSONVariantCases"

// The process-wide registry is keyed by host and field because fields sharing a
// discriminator may map its value through different case sets. The empty field
// name stores the host fallback. The lock protects registration and lookup.
var (
	variantRegistryMu sync.RWMutex
	variantRegistry   = map[reflect.Type]map[string]reflect.Type{}
)

// DefineVariantCases registers D as T's fallback case set. D must be a descriptor
// struct mapping discriminator values to concrete Go types. Registration is
// process-wide and must precede parsing T. Repeating the same registration is
// idempotent; a conflicting descriptor panics.
func DefineVariantCases[T any, D any]() {
	host := reflect.TypeFor[T]()
	desc := reflect.TypeFor[D]()
	registerVariant(host, desc, "")
}

// DefineVariantCasesAt registers D for one variant field on T. fieldName is the
// Go field name so it also identifies embedded fields. Registration is
// process-wide and must precede parsing T. Repeating the same registration is
// idempotent; a conflicting descriptor panics. Build requires the name to select
// a variant field before applying this field-specific descriptor.
func DefineVariantCasesAt[T any, D any](fieldName string) {
	if fieldName == "" {
		panic("vbind: DefineVariantCasesAt called with empty fieldName; use DefineVariantCases for the host-wide fallback")
	}
	host := reflect.TypeFor[T]()
	desc := reflect.TypeFor[D]()
	registerVariant(host, desc, fieldName)
}

func registerVariant(host, desc reflect.Type, fieldName string) {
	variantRegistryMu.Lock()
	defer variantRegistryMu.Unlock()
	existing, ok := variantRegistry[host]
	if !ok {
		variantRegistry[host] = map[string]reflect.Type{fieldName: desc}
		return
	}
	if prev, dup := existing[fieldName]; dup {
		if prev == desc {
			return
		}
		panic(fmt.Errorf("vbind: conflicting variant case definitions for host %s field %q (DefineVariantCases/DefineVariantCasesAt called with %s then %s)", host, fieldName, prev, desc))
	}
	merged := make(map[string]reflect.Type, len(existing)+1)
	maps.Copy(merged, existing)
	merged[fieldName] = desc
	variantRegistry[host] = merged
}

// lookupVariantDescriptor prefers a field-specific registration, then the host
// fallback. A promoted field's declaring type precedes its host, while Go method
// promotion makes the method witness visible through host. Registry and method
// results conflict when both resolve.
func lookupVariantDescriptor(host, decl reflect.Type, fieldName string) (reflect.Type, error) {
	var regDesc, methodDesc reflect.Type
	var regFrom reflect.Type
	variantRegistryMu.RLock()
	for _, t := range [2]reflect.Type{decl, host} {
		if t == nil {
			continue
		}
		table, ok := variantRegistry[t]
		if !ok {
			continue
		}
		if d, ok := table[fieldName]; ok {
			regDesc, regFrom = d, t
		} else if d, ok := table[""]; ok {
			regDesc, regFrom = d, t
		}
		if regDesc != nil {
			break
		}
	}
	variantRegistryMu.RUnlock()
	// MethodByName on host covers decl too: an embedded field's methods are
	// promoted alongside it.
	if m, ok := host.MethodByName(variantMethod); ok {
		// The method contract is one descriptor parameter, no results, and a value receiver.
		if m.Type.NumIn() == 2 && m.Type.NumOut() == 0 {
			methodDesc = m.Type.In(1)
		}
	}
	if regDesc != nil && methodDesc != nil {
		return nil, fmt.Errorf("vbind: host %s provides a variant descriptor for field %q via both DefineVariantCases/DefineVariantCasesAt on %s and the %s method; pick one", host, fieldName, regFrom, variantMethod)
	}
	if regDesc != nil {
		return regDesc, nil
	}
	if methodDesc != nil {
		return methodDesc, nil
	}
	if decl != nil && decl != host {
		return nil, fmt.Errorf("vbind: variant field %q declared on %s (promoted into %s) has no descriptor (define cases with DefineVariantCases/DefineVariantCasesAt on %s, or give it a %s method)", fieldName, decl, host, decl, variantMethod)
	}
	return nil, fmt.Errorf("vbind: host %s variant field %q has no descriptor (define cases with DefineVariantCases/DefineVariantCasesAt or a %s method)", host, fieldName, variantMethod)
}

type variantCase struct {
	Value  string
	Target reflect.Type
}

// parsedDescriptor is a descriptor's case set plus its optional default case.
// The default is kept out of cases because it carries no discriminator value:
// it is selected by the absence of a match, not by a string, so it has nothing
// to put in the lookup blob.
type parsedDescriptor struct {
	Cases []variantCase
	// Default is the target for a discriminator value no case matched, or nil
	// when the descriptor declares none (unmatched values then report).
	Default reflect.Type
}

// A descriptor must be a nonempty struct with unique case values. A named field
// uses its Go name and forbids a case tag. A blank field with a case tag uses the
// tag value; a blank field without one is the default case, and at most one may
// appear. Anonymous fields do not participate.
func parseVariantDescriptor(desc reflect.Type) (parsedDescriptor, error) {
	var out parsedDescriptor
	if desc.Kind() != reflect.Struct {
		return out, fmt.Errorf("vbind: variant descriptor %s is not a struct", desc)
	}
	seen := map[string]struct{}{}
	defaultAt := -1
	for i := 0; i < desc.NumField(); i++ {
		f := desc.Field(i)
		if f.Anonymous {
			continue
		}
		_, hasCaseTag := f.Tag.Lookup("case")
		if f.Name == "_" {
			if !hasCaseTag {
				// Blank and untagged: the default case. It names no discriminator
				// value, so it is the one entry that stays out of the lookup blob.
				if defaultAt >= 0 {
					return out, fmt.Errorf("vbind: variant descriptor %s declares two default cases (blank `_` fields %d and %d with no `case:` tag); only one is supported", desc, defaultAt, i)
				}
				defaultAt = i
				out.Default = f.Type
				continue
			}
			caseVal, _ := f.Tag.Lookup("case")
			if err := addVariantCase(&out.Cases, seen, caseVal, f.Type, desc, i); err != nil {
				return out, err
			}
			continue
		}
		if hasCaseTag {
			return out, fmt.Errorf("vbind: variant descriptor %s field %d (%s) is a named field; named fields use the field name as case value and must not carry a `case:` tag", desc, i, f.Name)
		}
		if err := addVariantCase(&out.Cases, seen, f.Name, f.Type, desc, i); err != nil {
			return out, err
		}
	}
	if len(out.Cases) == 0 && out.Default == nil {
		return out, fmt.Errorf("vbind: variant descriptor %s has no case entries", desc)
	}
	return out, nil
}

func addVariantCase(cases *[]variantCase, seen map[string]struct{}, value string, target reflect.Type, desc reflect.Type, fieldIdx int) error {
	if value == "" {
		// vlib cannot index an empty key, so the lookup table cannot represent it.
		return fmt.Errorf("vbind: variant descriptor %s field %d has an empty case value; empty discriminator values are not yet supported (use a non-empty case tag)", desc, fieldIdx)
	}
	if _, dup := seen[value]; dup {
		return fmt.Errorf("vbind: variant descriptor %s has duplicate case value %q", desc, value)
	}
	seen[value] = struct{}{}
	*cases = append(*cases, variantCase{Value: value, Target: target})
	return nil
}

// The returned blob maps each case string to its position in cases. Its caller
// must keep the backing array alive while native code retains its base pointer.
func buildVariantCaseLookup(cases []variantCase) ([]byte, error) {
	if len(cases) == 0 {
		return nil, nil
	}
	if !vlib.Available {
		return nil, nil
	}
	keys := make([]vlib.Key, len(cases))
	for i, c := range cases {
		// Init borrows each string pointer until it returns.
		keys[i] = vlib.Key{
			Str: unsafe.StringData(c.Value),
			Len: uintptr(len(c.Value)),
		}
	}
	scratch := make([]byte, vlib.ScratchSize())
	cfg := vlib.Config{
		Keys:        &keys[0],
		N:           uintptr(len(cases)),
		Tiers:       vlib.TiersAll,
		Scratch:     unsafe.Pointer(&scratch[0]),
		ScratchSize: uintptr(len(scratch)),
	}
	sz := vlib.SizeFor(&cfg)
	if sz == 0 {
		return nil, fmt.Errorf("vbind: cannot size variant case lookup (init failed)")
	}
	blob := make([]byte, sz)
	rc := vlib.Init(unsafe.Pointer(&blob[0]), sz, &cfg)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(scratch)
	if rc <= 0 {
		return nil, fmt.Errorf("vbind: cannot build variant case lookup (init rc=%d)", rc)
	}
	return blob, nil
}

// computeItab uses reflection to make the runtime materialize the immutable itab
// for a concrete type and nonempty interface pair. The temporary concrete value
// is not retained.
func computeItab(ifaceType, concreteType reflect.Type) (unsafe.Pointer, error) {
	if !concreteType.Implements(ifaceType) {
		return nil, fmt.Errorf("vbind: case type %s does not implement variant interface %s", concreteType, ifaceType)
	}
	var cv reflect.Value
	if concreteType.Kind() == reflect.Pointer {
		cv = reflect.New(concreteType.Elem())
	} else {
		cv = reflect.New(concreteType).Elem()
	}
	slot := reflect.New(ifaceType)
	slot.Elem().Set(cv.Convert(ifaceType))
	return gort.ExtractItab(slot.UnsafePointer()), nil
}

// findStreamField reports whether t directly or indirectly contains a
// stream.Stream[T] field. It recurses through struct fields, slice/array
// elements, map values, and pointer pointees. visited breaks cycles in
// self-referential types.
//
// The check is one-directional: it only forbids Stream[T] inside a variant
// case target. The element type T of a Stream[T] may itself contain variant
// fields; that direction is not inspected here, because stream element
// production is independent of any enclosing variant dispatch.
func findStreamField(t reflect.Type, visited map[reflect.Type]bool) (string, bool) {
	if t == nil {
		return "", false
	}
	if visited[t] {
		return "", false
	}
	// Report t itself if it is a Stream[T]: callers use this predicate for
	// both nested-stream detection (struct fields, slice/array elements, map
	// values) and direct-stream detection (map[K]Stream[T], []Stream[T]).
	if typ.IsStreamType(t) {
		return t.String(), true
	}
	if visited == nil {
		visited = map[reflect.Type]bool{}
	}
	visited[t] = true
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if path, ok := findStreamField(f.Type, visited); ok {
				return t.String() + "." + f.Name + "/" + path, true
			}
		}
	case reflect.Slice, reflect.Array:
		return findStreamField(t.Elem(), visited)
	case reflect.Map:
		return findStreamField(t.Elem(), visited)
	case reflect.Pointer:
		return findStreamField(t.Elem(), visited)
	}
	return "", false
}

// streamRecurses reports whether elemType contains (transitively, through any
// struct field, slice/array, map, or pointer path) a Stream whose element type
// is elemType itself. Such a cycle through a Stream edge makes the stream type
// tree non-terminating: building Stream[T] requires T, which contains Stream[T],
// which must yield per-element and re-enter the same scope. Regular recursion
// (a struct containing []itself) is unaffected; only cycles that cross a Stream
// edge are rejected.
//
// The walk follows Stream element edges (via typ.StreamElementType) in addition
// to the structural edges findStreamField walks, so indirect cycles like
// Stream[A] whose A contains Stream[B] whose B contains Stream[A] are caught.
// visited breaks cycles in the structural graph.
func streamRecurses(elemType reflect.Type) bool {
	visited := map[reflect.Type]bool{}
	var walk func(t reflect.Type) bool
	walk = func(t reflect.Type) bool {
		if t == nil || visited[t] {
			return false
		}
		if typ.IsStreamType(t) {
			se := typ.StreamElementType(t)
			if se == elemType {
				return true
			}
			return walk(se)
		}
		visited[t] = true
		switch t.Kind() {
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				if walk(t.Field(i).Type) {
					return true
				}
			}
		case reflect.Slice, reflect.Array:
			return walk(t.Elem())
		case reflect.Map:
			return walk(t.Elem())
		case reflect.Pointer:
			return walk(t.Elem())
		}
		return false
	}
	return walk(elemType)
}

// checkVJSONOptions validates every field so unknown or incompatible vjson
// options fail during TypeTree construction.
func checkVJSONOptions(host reflect.Type, sf *typ.StructField, vj typ.VJSONTag) error {
	// `json:",embed"` on an interface promotes the fields of whichever case the
	// discriminator selects, so it needs a discriminator to select with. Checked
	// before the Present gate: the offending tag may be the json one alone, with
	// no vjson tag present at all.
	if sf.TagFlags&typ.TagFlagEmbed != 0 && !vj.HasVariant {
		return fmt.Errorf("vbind: struct %s field %q has `json:\",embed\"` on an interface without `vjson:\"variant=<disc>\"`; an embedded interface promotes the fields of the case its discriminator selects, so the discriminator must be named", host, sf.JSONName)
	}
	if !vj.Present {
		return nil
	}
	if len(vj.Unrecognized) > 0 {
		return fmt.Errorf("vbind: struct %s field %q has unrecognized vjson option(s) %q; supported options are variant=<disc> and kindof (field layout is spelled `json:\",embed\"`)",
			host, sf.JSONName, vj.Unrecognized)
	}
	// One field cannot be both: the high 16 flag bits hold a table index that is
	// interpreted by the selected feature's own table, so the feature must be
	// unambiguous.
	if vj.HasVariant && vj.Kindof {
		return fmt.Errorf("vbind: struct %s field %q sets both variant and kindof; pick one", host, sf.JSONName)
	}
	if sf.TagFlags&typ.TagFlagReserveUnknown != 0 && (vj.HasVariant || vj.Kindof) {
		return fmt.Errorf("vbind: struct %s field %q pairs an embedded value.Value with variant/kindof; the reserve-unknown field collects keys and cannot also be a polymorphic target", host, sf.JSONName)
	}
	return nil
}

// Sibling variants carry independent table and destination state in each field
// record. An inline variant uses the host's single InlineVariantIdx and merged
// tape split state, so each host may contain at most one inline variant.
func (b *builder) attachVariantsForStruct(hostUT *typ.UniType, hostIdx uint32, si *typ.StructTypeInfo, fieldsBase uint32) error {
	inlineCount := 0
	var variantFieldNames []string
	for i, sf := range si.Fields {
		vj := typ.ParseVJSONTag(sf.RawTag)
		if err := checkVJSONOptions(hostUT.Type, &sf, vj); err != nil {
			return err
		}
		if !vj.HasVariant {
			continue
		}
		// Polymorphic dispatch reads the discriminator relative to the host base
		// (poly_case_by_disc takes cur_dst) and stores the chosen case's eface at
		// the field's offset from that same base. A field promoted across an
		// embedded pointer has neither property: its bytes live in a pointee, and
		// the discriminator promoted alongside it does too. Supporting this needs
		// dispatch to carry a per-field base, which is a different change.
		if len(sf.PtrPath) > 0 {
			return fmt.Errorf("vbind: struct %s field %q is a variant target promoted across an embedded pointer; polymorphic dispatch needs the discriminator and the target in the host itself, so embed %s by value or give the pointer an explicit JSON name",
				hostUT.Type, sf.JSONName, sf.PtrPath[0].PointeeType.Type)
		}
		discName := vj.Variant
		isInline := sf.TagFlags&typ.TagFlagEmbed != 0
		if isInline {
			inlineCount++
			if inlineCount > 1 {
				return fmt.Errorf("vbind: struct %s has multiple embedded variant fields (`json:\",embed\"`); only one is supported", hostUT.Type)
			}
		}
		if discName == "" {
			return fmt.Errorf("vbind: struct %s variant field %q has empty discriminator name; vjson:\"variant=<discName>\" must name the discriminator's JSON field", hostUT.Type, sf.JSONName)
		}
		vdiscFieldIdx := -1
		for j, sf2 := range si.Fields {
			if sf2.JSONName == discName {
				vdiscFieldIdx = j
				break
			}
		}
		if vdiscFieldIdx < 0 {
			return fmt.Errorf("vbind: struct %s variant tag names discriminator %q but no field has json:\"%s\"", hostUT.Type, discName, discName)
		}
		// Native dispatch reads this field with the Go string header ABI.
		if vdiscFieldType := si.Fields[vdiscFieldIdx].FieldType.Type; vdiscFieldType.Kind() != reflect.String {
			return fmt.Errorf("vbind: struct %s discriminator field %q must be a string, got %s", hostUT.Type, discName, vdiscFieldType)
		}
		if err := b.buildOneVariantTable(hostUT, hostIdx, si, fieldsBase, vdiscFieldIdx, i, isInline, sf.DeclaringType, sf.GoName); err != nil {
			return err
		}
		variantFieldNames = append(variantFieldNames, sf.GoName)
	}
	// Validation waits for TypeTree construction, when the host's complete set of
	// variant field names is available.
	return checkVariantFieldNames(hostUT.Type, variantFieldNames)
}

// checkVariantFieldNames validates field-specific registrations against the
// variant fields declared by host. Registrations on embedded declaring types are
// validated when those types are built.
func checkVariantFieldNames(host reflect.Type, built []string) error {
	variantRegistryMu.RLock()
	table := variantRegistry[host]
	names := make([]string, 0, len(table))
	for name := range table {
		if name != "" {
			names = append(names, name)
		}
	}
	variantRegistryMu.RUnlock()
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names) // map order would make the error message nondeterministic
	for _, name := range names {
		if slices.Contains(built, name) {
			continue
		}
		// Name the alternatives: the mistake is nearly always a JSON name written
		// where the Go name belongs, or a plain misspelling.
		if len(built) == 0 {
			return fmt.Errorf("vbind: DefineVariantCasesAt names field %q on %s, which declares no variant field at all (a variant field needs `vjson:\"variant=<disc>\"`)", name, host)
		}
		return fmt.Errorf("vbind: DefineVariantCasesAt names field %q on %s, which has no such variant field; its variant fields are %v (use the Go field name, not the JSON name)", name, host, built)
	}
	return nil
}

// Inline cases must be structs and must not themselves host an inline variant.
// This prevents one case field set from being unfolded into two hosts.
func (b *builder) buildOneVariantTable(hostUT *typ.UniType, hostIdx uint32, si *typ.StructTypeInfo, fieldsBase uint32, vdiscFieldIdx, variantFieldIdx int, isInline bool, declType reflect.Type, fieldName string) error {
	host := hostUT.Type
	desc, err := lookupVariantDescriptor(host, declType, fieldName)
	if err != nil {
		return err
	}
	pd, err := parseVariantDescriptor(desc)
	if err != nil {
		return err
	}
	// The default case is built as an ordinary entry so it gets a TypeIdx, an
	// rtype/itab and a slot class from the same loop as the rest (native needs all
	// three to box and allocate it, not just a type). It is appended last and
	// excluded from the lookup blob, since it answers to no discriminator string.
	cases := pd.Cases
	defaultCaseIdx := -1
	if pd.Default != nil {
		defaultCaseIdx = len(cases)
		cases = append(cases, variantCase{Value: "", Target: pd.Default})
	}
	// Empty and nonempty interfaces both occupy two words. In an eface, word zero
	// is the rtype. In an iface, word zero is the itab. For pointer and map cases,
	// the data word stores the direct pointer value. For value kinds, it points to
	// the value's storage.
	variantFieldType := si.Fields[variantFieldIdx].FieldType.Type
	isIface := false
	if variantFieldType == reflect.TypeFor[any]() {
	} else if variantFieldType.Kind() == reflect.Interface {
		isIface = true
	} else {
		return fmt.Errorf("vbind: variant field %s.%s must be `any` or an interface (got %s)", host, si.Fields[variantFieldIdx].JSONName, variantFieldType)
	}
	caseTypeIdx := make([]uint16, len(cases))
	caseRType := make([]unsafe.Pointer, len(cases))
	caseSlotClass := make([]int32, len(cases))
	for i, c := range cases {
		// The default case has no discriminator value, so name it by role in the
		// diagnostics below rather than printing an empty %q.
		caseLabel := fmt.Sprintf("%q", c.Value)
		if i == defaultCaseIdx {
			caseLabel = "default"
		}
		if isInline {
			if c.Target.Kind() != reflect.Struct {
				return fmt.Errorf("vbind: embedded variant %s case %d (%s) must be a struct (got %s); an embedded variant unfolds case fields into the host, so drop `json:\",embed\"` for non-struct cases", host, i, caseLabel, c.Target.Kind())
			}
		}
		if streamHost, ok := findStreamField(c.Target, nil); ok {
			return fmt.Errorf("vbind: variant %s case %d (%s) target type %s contains stream.Stream[T] field at %s; stream fields cannot appear in a variant case target (per-element yield conflicts with discriminator-driven dispatch). Stream[T]'s element type may itself contain variant fields; only the case-target direction is restricted", host, i, caseLabel, c.Target, streamHost)
		}
		targetUT := typ.UniTypeOf(c.Target)
		var typeIdx uint32
		typeIdx, err = b.collect(targetUT)
		if err != nil {
			return err
		}
		if isInline {
			// The close-time split classifies keys against one selected case table.
			// A nested inline case would require a second discriminator-dependent
			// classification over the same object, including keys seen before that
			// discriminator.
			if ivIdx := b.typeMeta[typeIdx].StructMeta().InlineVariantIdx; ivIdx != 0xFFFF {
				return fmt.Errorf("vbind: embedded variant %s case %d (%s) is type %s which itself hosts an embedded variant; two levels of `json:\",embed\"` unfold into one JSON object and the struct-close split classifies keys one level at a time, so the inner level's keys would be dropped. Make the inner variant a sibling (give it a JSON name instead of `json:\",embed\"`)",
					host, i, caseLabel, c.Target)
			}
		}
		caseTypeIdx[i] = uint16(typeIdx)
		if isIface {
			var itab unsafe.Pointer
			itab, err = computeItab(variantFieldType, c.Target)
			if err != nil {
				return err
			}
			caseRType[i] = itab
		} else {
			caseRType[i] = gort.TypePtr(c.Target)
		}
		caseSlotClass[i] = int32(b.registerSlotClass(targetUT))
	}
	// Only the string-keyed cases go in the blob; a lookup miss is what selects the
	// default, so indexing it would defeat its purpose. pd.Cases is exactly that
	// prefix, and the case index a hit returns still lines up with the arrays above
	// because the default was appended last.
	blob, err := buildVariantCaseLookup(pd.Cases)
	if err != nil {
		return err
	}
	var lookupBase unsafe.Pointer
	if len(blob) > 0 {
		lookupBase = unsafe.Pointer(unsafe.SliceData(blob))
	}
	polyIdx := uint16(len(b.polys))
	table := BindPolyTable{
		DiscFieldOff:      uint32(si.Fields[vdiscFieldIdx].Offset),
		DefaultCaseIdx:    variantNoDefaultCase,
		CaseCount:         uint16(len(cases)),
		caseTypeIdxData:   unsafe.Pointer(unsafe.SliceData(caseTypeIdx)),
		caseRTypeData:     unsafe.Pointer(unsafe.SliceData(caseRType)),
		caseSlotClassData: unsafe.Pointer(unsafe.SliceData(caseSlotClass)),
		Lookup:            lookupBase,
	}
	if defaultCaseIdx >= 0 {
		table.DefaultCaseIdx = uint16(defaultCaseIdx)
	}
	b.polys = append(b.polys, table)
	b.polyCases = append(b.polyCases, PolyCaseData{
		TypeIdx:   caseTypeIdx,
		RType:     caseRType,
		SlotClass: caseSlotClass,
	})
	// The high 16 flag bits index the poly table, and the tag bit selects the
	// variant or kindof interpretation. Runtime entries at one depth must
	// therefore be matched by depth, index, and feature kind.
	variantField := &b.fields[fieldsBase+uint32(variantFieldIdx)]
	if isInline {
		variantField.Flags |= uint32(TagInlineVariant) | (uint32(polyIdx) << fieldFlagPolyIdxShift)
		// Zero is a valid table index; 0xFFFF means no inline variant. The
		// native struct-open path reads this to intercept before IDX_CONSUME
		// and route the whole struct through vd_dispatch.
		b.typeMeta[hostIdx].StructMeta().InlineVariantIdx = polyIdx
	} else {
		variantField.Flags |= uint32(TagVariant) | (uint32(polyIdx) << fieldFlagPolyIdxShift)
	}
	// The vdisc mark routes the discriminator's own value. For an embedded variant
	// that is the whole point: the value goes onto the merged tape rather than
	// straight to Go, because the selected case may be a value.Value that must see
	// the discriminator among its fields. TagInlineVDisc records which of the two
	// kinds this is so native need not compare the field's idx against the host's
	// InlineVariantIdx at parse time.
	//
	// A sibling's discriminator binds like any other string field, and dispatch
	// reads it through the table's own DiscFieldOff rather than through this field
	// record, so the idx stamped here is unused on that path. That is what lets two
	// siblings name the same discriminator: the second call finds TagVDisc already
	// set and leaves the first idx in place, and nothing reads it.
	//
	// One field can be both, when an embedded and a sibling variant share a
	// discriminator. Inline wins the high 16 bits: native's struct-open path routes
	// through the merged tape using InlineVariantIdx, so the scan that binds the
	// discriminator out of the tape matches on that same index. The low-16 mask
	// preserves TagInlineVDisc if an earlier call already set it.
	vdiscField := &b.fields[fieldsBase+uint32(vdiscFieldIdx)]
	if isInline {
		vdiscField.Flags = (vdiscField.Flags & 0x0000FFFF) | uint32(TagVDisc) | uint32(TagInlineVDisc) |
			(uint32(polyIdx) << fieldFlagPolyIdxShift)
	} else if vdiscField.Flags&uint32(TagVDisc) == 0 {
		vdiscField.Flags |= uint32(TagVDisc) | (uint32(polyIdx) << fieldFlagPolyIdxShift)
	}
	return nil
}
