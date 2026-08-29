// Kindof resolves a process-wide registration or method witness into an
// immutable per-TypeTree table. The first JSON value token indexes the fixed
// five-kind dispatch array, and KindofCaseData owns the case arrays exposed
// through raw ABI pointers.

package vbind

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// JSONKindofCases is a type witness. Its sole parameter declares the descriptor,
// and its body is never called.
const kindofMethod = "JSONKindofCases"

// The process-wide registry accepts one kindof descriptor per host.
var kindofRegistry sync.Map

// KindofKindNames defines the ABI index shared with native dispatch:
// 0 is bool, 1 is number, 2 is string, 3 is array, and 4 is object.
// Descriptor field names must come from this set.
var KindofKindNames = [5]string{"bool", "number", "string", "array", "object"}

var kindofKindNames = KindofKindNames

func kindofKindIndex(name string) int8 {
	for i, k := range kindofKindNames {
		if k == name {
			return int8(i)
		}
	}
	return -1
}

// DefineKindofCases registers D as the process-wide JSON kind case set for T.
// D must be a descriptor struct mapping JSON kinds to concrete Go types.
// Registration must precede parsing T. Repeating the same registration is
// idempotent; a conflicting descriptor panics.
func DefineKindofCases[T any, D any]() {
	host := reflect.TypeFor[T]()
	desc := reflect.TypeFor[D]()
	registerKindof(host, desc)
}

func registerKindof(host, desc reflect.Type) {
	prev, loaded := kindofRegistry.LoadOrStore(host, desc)
	if !loaded {
		return
	}
	existing := prev.(reflect.Type)
	if existing == desc {
		return
	}
	panic(fmt.Errorf("vbind: conflicting kindof case definitions for host %s (DefineKindofCases called with %s then %s)", host, existing, desc))
}

// lookupKindofDescriptor gives a promoted field's declaring type precedence over
// its host. Registration and the method witness are equivalent declaration forms
// and therefore conflict when both resolve.
func lookupKindofDescriptor(host, decl reflect.Type) (reflect.Type, error) {
	var regDesc, methodDesc reflect.Type
	var regFrom reflect.Type
	for _, t := range [2]reflect.Type{decl, host} {
		if t == nil {
			continue
		}
		if v, ok := kindofRegistry.Load(t); ok {
			regDesc, regFrom = v.(reflect.Type), t
			break
		}
	}
	// MethodByName on host covers decl too: promotion carries methods along.
	if m, ok := host.MethodByName(kindofMethod); ok {
		// The method contract is one descriptor parameter, no results, and a value receiver.
		if m.Type.NumIn() == 2 && m.Type.NumOut() == 0 {
			methodDesc = m.Type.In(1)
		}
	}
	if regDesc != nil && methodDesc != nil {
		return nil, fmt.Errorf("vbind: host %s provides kindof descriptor via both DefineKindofCases on %s and %s method; pick one", host, regFrom, kindofMethod)
	}
	if regDesc != nil {
		return regDesc, nil
	}
	if methodDesc != nil {
		return methodDesc, nil
	}
	if decl != nil && decl != host {
		return nil, fmt.Errorf("vbind: kindof field declared on %s (promoted into %s) has no descriptor (define cases with DefineKindofCases on %s, or give it a %s method)", decl, host, decl, kindofMethod)
	}
	return nil, fmt.Errorf("vbind: host %s has a kindof field but no descriptor (define cases with DefineKindofCases or a %s method)", host, kindofMethod)
}

type kindofCase struct {
	KindIdx int8
	Target  reflect.Type
}

// A descriptor must be a nonempty struct whose field names are JSON kinds and
// whose field types are their case types. Blank fields, case tags, anonymous
// fields, unknown kinds, and duplicate kinds are rejected.
func parseKindofDescriptor(desc reflect.Type) ([]kindofCase, error) {
	if desc.Kind() != reflect.Struct {
		return nil, fmt.Errorf("vbind: kindof descriptor %s is not a struct", desc)
	}
	var cases []kindofCase
	seen := [5]bool{}
	for i := 0; i < desc.NumField(); i++ {
		f := desc.Field(i)
		if f.Anonymous {
			return nil, fmt.Errorf("vbind: kindof descriptor %s field %d (%s) is anonymous; kindof does not accept embedded fields (all JSON kinds are directly representable as Go identifiers)", desc, i, f.Name)
		}
		if f.Name == "_" {
			return nil, fmt.Errorf("vbind: kindof descriptor %s field %d is blank (_); kindof does not accept blank fields (use a named field matching the JSON kind: bool/number/string/array/object)", desc, i)
		}
		if _, hasCaseTag := f.Tag.Lookup("case"); hasCaseTag {
			return nil, fmt.Errorf("vbind: kindof descriptor %s field %d (%s) carries a `case:` tag; kindof does not accept case tags (the field name itself is the JSON kind)", desc, i, f.Name)
		}
		kindIdx := kindofKindIndex(f.Name)
		if kindIdx < 0 {
			return nil, fmt.Errorf("vbind: kindof descriptor %s field %d (%s) is not a legal JSON kind; want one of bool/number/string/array/object", desc, i, f.Name)
		}
		if seen[kindIdx] {
			return nil, fmt.Errorf("vbind: kindof descriptor %s has duplicate kind %q; kindof does not accept multiple candidates of the same kind", desc, f.Name)
		}
		seen[kindIdx] = true
		cases = append(cases, kindofCase{KindIdx: kindIdx, Target: f.Type})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("vbind: kindof descriptor %s has no case entries", desc)
	}
	return cases, nil
}

// A struct may contain multiple independent kindof fields. A field cannot be
// both variant and kindof because the high 16 flag bits hold an index interpreted
// by the selected feature's own table; that pairing, and any other malformed
// option set, is rejected by checkVJSONOptions during the variant pass.
func (b *builder) attachKindofsForStruct(hostUT *typ.UniType, si *typ.StructTypeInfo, fieldsBase uint32) error {
	for i, sf := range si.Fields {
		if !typ.ParseVJSONTag(sf.RawTag).Kindof {
			continue
		}
		// Same reason attachVariantsForStruct refuses this: kindof stores the
		// chosen case's eface at the field's offset from the host base, which a
		// field promoted across an embedded pointer does not have.
		if len(sf.PtrPath) > 0 {
			return fmt.Errorf("vbind: struct %s field %q is a kindof target promoted across an embedded pointer; dispatch needs the target in the host itself, so embed %s by value or give the pointer an explicit JSON name",
				hostUT.Type, sf.JSONName, sf.PtrPath[0].PointeeType.Type)
		}
		if err := b.buildOneKindofTable(hostUT, si, fieldsBase, i, sf.DeclaringType); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) buildOneKindofTable(hostUT *typ.UniType, si *typ.StructTypeInfo, fieldsBase uint32, kindofFieldIdx int, declType reflect.Type) error {
	host := hostUT.Type
	desc, err := lookupKindofDescriptor(host, declType)
	if err != nil {
		return err
	}
	cases, err := parseKindofDescriptor(desc)
	if err != nil {
		return err
	}
	// Empty and nonempty interfaces both occupy two words. In an eface, word zero
	// is the rtype. In an iface, word zero is the itab. For pointer and map cases,
	// the data word stores the direct pointer value. For value kinds, it points to
	// the value's storage.
	kindofFieldType := si.Fields[kindofFieldIdx].FieldType.Type
	isIface := false
	if kindofFieldType == reflect.TypeFor[any]() {
	} else if kindofFieldType.Kind() == reflect.Interface {
		isIface = true
	} else {
		return fmt.Errorf("vbind: kindof field %s.%s must be `any` or an interface (got %s)", host, si.Fields[kindofFieldIdx].JSONName, kindofFieldType)
	}
	caseTypeIdx := make([]uint16, len(cases))
	caseRType := make([]unsafe.Pointer, len(cases))
	caseSlotClass := make([]int32, len(cases))
	caseKinds := make([]string, len(cases))
	for i, c := range cases {
		targetUT := typ.UniTypeOf(c.Target)
		typeIdx, err := b.collect(targetUT)
		if err != nil {
			return err
		}
		caseTypeIdx[i] = uint16(typeIdx)
		if isIface {
			itab, err := computeItab(kindofFieldType, c.Target)
			if err != nil {
				return err
			}
			caseRType[i] = itab
		} else {
			caseRType[i] = gort.TypePtr(c.Target)
		}
		caseSlotClass[i] = int32(b.registerSlotClass(targetUT))
		caseKinds[i] = kindofKindNames[c.KindIdx]
	}
	// Native code reads this leading field as int8_t[5]. A negative entry means
	// that the descriptor does not accept the corresponding JSON kind.
	caseIdxByKind := [5]int8{-1, -1, -1, -1, -1}
	for i, c := range cases {
		caseIdxByKind[c.KindIdx] = int8(i)
	}
	kindofIdx := uint16(len(b.kindofs))
	b.kindofs = append(b.kindofs, BindKindofTable{
		CaseIdxByKind:     caseIdxByKind,
		CaseCount:         uint32(len(cases)),
		CaseTypeIdxData:   unsafe.Pointer(unsafe.SliceData(caseTypeIdx)),
		CaseRTypeData:     unsafe.Pointer(unsafe.SliceData(caseRType)),
		CaseSlotClassData: unsafe.Pointer(unsafe.SliceData(caseSlotClass)),
	})
	b.kindofCases = append(b.kindofCases, KindofCaseData{
		TypeIdx:   caseTypeIdx,
		RType:     caseRType,
		SlotClass: caseSlotClass,
		Kinds:     caseKinds,
	})
	kindofField := &b.fields[fieldsBase+uint32(kindofFieldIdx)]
	kindofField.Flags |= uint32(TagKindof) | (uint32(kindofIdx) << fieldFlagVariantIdxShift)
	return nil
}
