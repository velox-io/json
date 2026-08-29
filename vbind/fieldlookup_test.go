package vbind

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/vlib"
	"github.com/velox-io/json/typ"
)

func structInfoOf(t *testing.T, rt reflect.Type) *typ.StructTypeInfo {
	t.Helper()
	ut := typ.UniTypeOf(rt)
	if ut == nil {
		t.Fatalf("UniTypeOf(%s) = nil", rt)
	}
	si, ok := ut.Ext.(*typ.StructTypeInfo)
	if !ok {
		t.Fatalf("UniTypeOf(%s).Ext is %T, want *StructTypeInfo", rt, ut.Ext)
	}
	return si
}

func TestFieldLookupSharedPerType(t *testing.T) {
	type Inner struct {
		A int    `json:"a"`
		B string `json:"bb"`
		C bool   `json:"ccc"`
	}
	si := structInfoOf(t, reflect.TypeFor[Inner]())

	blob1, err := getStructLookup(si)
	if err != nil {
		t.Fatalf("getStructLookup: %v", err)
	}
	if len(blob1) == 0 {
		t.Fatal("expected non-empty blob for a struct with fields")
	}

	blob2, err := getStructLookup(si)
	if err != nil {
		t.Fatalf("getStructLookup (2nd): %v", err)
	}
	if &blob1[0] != &blob2[0] {
		t.Fatal("second getStructLookup returned a different blob (not shared)")
	}

	if tier := vlib.GetTier(unsafe.Pointer(&blob1[0])); tier == vlib.TierNone {
		t.Fatalf("blob tier = none")
	}
}

func TestFieldLookupCrossTreeShared(t *testing.T) {
	if !vlib.Available {
		t.Skip("native lookup not linked on this platform")
	}
	type Meta struct {
		Name string `json:"name"`
		Ver  int    `json:"version"`
	}
	type RootA struct {
		M Meta `json:"m"`
		X int  `json:"x"`
	}
	type RootB struct {
		Meta Meta   `json:"meta"`
		Y    string `json:"y"`
	}

	ttA, err := Build(typ.UniTypeOf(reflect.TypeFor[RootA]()))
	if err != nil {
		t.Fatalf("Build RootA: %v", err)
	}
	ttB, err := Build(typ.UniTypeOf(reflect.TypeFor[RootB]()))
	if err != nil {
		t.Fatalf("Build RootB: %v", err)
	}

	pA := metaBlobPtr(ttA)
	pB := metaBlobPtr(ttB)
	if pA == nil || pB == nil {
		t.Fatalf("Meta blob not attached (A=%p B=%p)", pA, pB)
	}
	if pA != pB {
		t.Errorf("Meta blob differs across trees: %p vs %p (want shared)", pA, pB)
	}
}

// Field names are unavailable here, so identify Meta by the unique string and
// int field shape. The roots cannot match because their first field is Meta.
func metaBlobPtr(tt *TypeTree) unsafe.Pointer {
	for i := range tt.Types {
		if tt.Types[i].Kind != KindStruct || tt.Types[i].Struct().FieldCount != 2 {
			continue
		}
		firstIdx := tt.Types[i].StructFirstFieldIndex(&tt.Fields[0])
		f0Kind := tt.Types[tt.Fields[firstIdx].FieldTypeIndex(&tt.Types[0])].Kind
		f1Kind := tt.Types[tt.Fields[firstIdx+1].FieldTypeIndex(&tt.Types[0])].Kind
		if f0Kind == KindString && f1Kind == KindInt {
			return tt.TypeMeta[i].StructMeta().Lookup
		}
	}
	return nil
}

// Fieldless structs must use the NONE sentinel so native field lookup needs no
// field count guard.
func TestFieldLookupEmptyStruct(t *testing.T) {
	type Empty struct{}
	tt, err := Build(typ.UniTypeOf(reflect.TypeFor[Empty]()))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	addr := tt.TypeMeta[tt.Root].StructMeta().Lookup
	if addr == nil {
		t.Errorf("empty struct root should have the TierNone sentinel lookup, got NULL")
	}
}
