package vbind

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestBindTypeUnionAccessor(t *testing.T) {
	var ti BindType

	ti.Kind = KindStruct
	ti.Struct().FieldCount = 0x11223344
	if got := ti.InnerRaw(); got != 0x11223344 {
		t.Errorf("Struct.FieldCount inner mismatch: got %#x", got)
	}

	ti = BindType{Kind: KindSlice}
	ti.Slice().ChildSize = 24
	if got := ti.InnerRaw(); got != 24 {
		t.Errorf("Slice.ChildSize inner mismatch: got %d", got)
	}

	ti = BindType{Kind: KindArray}
	ti.Array().ChildSize = 8
	if got := ti.InnerRaw(); got != 8 {
		t.Errorf("Array.ChildSize inner mismatch: got %d", got)
	}

	// AllocClass is int32; the negative sentinel must round trip through the
	// shared inner slot without corrupting the sign.
	ti = BindType{Kind: KindPointer}
	ti.Pointer().AllocClass = -1
	if got := int32(ti.InnerRaw()); got != -1 {
		t.Errorf("Pointer.AllocClass sign lost: got %d", got)
	}

	ti = BindType{Kind: KindMap}
	ti.Map().AllocClass = 7
	if got := ti.Map().AllocClass; got != 7 {
		t.Errorf("Map.AllocClass mismatch: got %d", got)
	}
}

func TestBindTypeKindIndependentOfPayload(t *testing.T) {
	ti := BindType{Kind: KindStruct}
	ti.Struct().FieldCount = 0xFFFFFFFF
	// A real heap pointer exercises the same child-slot write without the
	// integer-to-pointer conversion that go vet's unsafeptr check flags.
	var sink BindType
	ti.setChild(unsafe.Pointer(&sink))
	if ti.Kind != KindStruct {
		t.Errorf("payload write smashed Kind: %d", ti.Kind)
	}
}

func TestBuildResolvesChildPointers(t *testing.T) {
	type inner struct {
		X int `json:"x"`
	}
	type outer struct {
		A []inner  `json:"a"`
		B *inner   `json:"b"`
		C [4]inner `json:"c"`
	}
	tt, err := TypeTreeOf(reflect.TypeFor[outer]())
	if err != nil {
		t.Fatalf("TypeTreeOf err: %v", err)
	}
	typesBase := uintptr(unsafe.Pointer(&tt.Types[0]))
	typesEnd := typesBase + uintptr(len(tt.Types))*unsafe.Sizeof(BindType{})
	fieldsBase := uintptr(unsafe.Pointer(&tt.Fields[0]))
	fieldsEnd := fieldsBase + uintptr(len(tt.Fields))*unsafe.Sizeof(BindField{})

	for i := range tt.Types {
		bt := &tt.Types[i]
		switch bt.Kind {
		case KindPointer, KindSlice, KindArray, KindMap:
			p := bt.ChildRaw()
			if p < typesBase || p >= typesEnd {
				t.Errorf("Types[%d] Kind=%d child ptr %#x outside Types range [%#x,%#x)", i, bt.Kind, p, typesBase, typesEnd)
			}
		case KindStruct:
			p := bt.ChildRaw()
			if p < fieldsBase || p > fieldsEnd { // p == fieldsEnd valid for zero-field struct at tail
				t.Errorf("Types[%d] KindStruct child ptr %#x outside Fields range [%#x,%#x]", i, p, fieldsBase, fieldsEnd)
			}
		}
	}
	for i := range tt.Fields {
		p := tt.Fields[i].Type
		if p < typesBase || p >= typesEnd {
			t.Errorf("Fields[%d].Type ptr %#x outside Types range [%#x,%#x)", i, p, typesBase, typesEnd)
		}
	}
}
