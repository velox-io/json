package bind

import (
	"reflect"
	"testing"

	"github.com/velox-io/json/vbind"
)

// TestBuildTypeTreeFlatStruct verifies the TypeTree builder produces a
// correct flat type table for a flat struct with scalar fields.
func TestBuildTypeTreeFlatStruct(t *testing.T) {
	type flat struct {
		Name string  `json:"name"`
		N    int     `json:"n"`
		F    float64 `json:"f"`
		B    bool    `json:"b"`
	}
	bt, err := vbind.TypeTreeOf(reflect.TypeFor[flat]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if int(bt.Root) >= len(bt.Types) {
		t.Fatalf("Root %d out of range (len=%d)", bt.Root, len(bt.Types))
	}
	root := &bt.Types[bt.Root]
	if root.Kind != vbind.KindStruct {
		t.Errorf("root kind = %d, want %d (struct)", root.Kind, vbind.KindStruct)
	}
	rs := root.Struct()
	if rs.FieldCount != 4 {
		t.Errorf("root field_count = %d, want 4", rs.FieldCount)
	}

	// Field names no longer live in the tree (lookup goes through the
	// perfect-hash blob), so verify the field types by declaration order:
	// {name string, n int, f float64, b bool}.
	wantKinds := []vbind.Kind{
		vbind.KindString,
		vbind.KindInt,
		vbind.KindFloat64,
		vbind.KindBool,
	}
	firstIdx := root.StructFirstFieldIndex(&bt.Fields[0])
	for i := uint32(0); i < rs.FieldCount; i++ {
		f := &bt.Fields[firstIdx+i]
		gotKind := bt.Types[f.FieldTypeIndex(&bt.Types[0])].Kind
		if gotKind != wantKinds[i] {
			t.Errorf("field %d kind = %d, want %d", i, gotKind, wantKinds[i])
		}
	}
}

// TestBuildTypeTreePointer verifies pointer types register a slot class.
func TestBuildTypeTreePointer(t *testing.T) {
	type box struct {
		P *int `json:"p"`
	}
	bt, err := vbind.TypeTreeOf(reflect.TypeFor[box]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	root := &bt.Types[bt.Root]
	if root.Kind != vbind.KindStruct {
		t.Fatalf("root kind = %d, want struct", root.Kind)
	}
	firstIdx := root.StructFirstFieldIndex(&bt.Fields[0])
	ft := &bt.Types[bt.Fields[firstIdx].FieldTypeIndex(&bt.Types[0])]
	if ft.Kind != vbind.KindPointer {
		t.Errorf("field type kind = %d, want pointer", ft.Kind)
	}
	if ft.Pointer().AllocClass < 0 {
		t.Errorf("ptr alloc_class = %d, want >= 0", ft.Pointer().AllocClass)
	}
	if len(bt.Slots) == 0 {
		t.Errorf("no slot classes registered")
	}
}

// TestBuildTypeTreeSlice verifies slice types register elem type metadata.
func TestBuildTypeTreeSlice(t *testing.T) {
	type withSlice struct {
		List []int `json:"list"`
	}
	bt, err := vbind.TypeTreeOf(reflect.TypeFor[withSlice]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	root := &bt.Types[bt.Root]
	firstIdx := root.StructFirstFieldIndex(&bt.Fields[0])
	sliceType := &bt.Types[bt.Fields[firstIdx].FieldTypeIndex(&bt.Types[0])]
	if sliceType.Kind != vbind.KindSlice {
		t.Fatalf("field kind = %d, want slice", sliceType.Kind)
	}
	sl := sliceType.Slice()
	if sl.ChildSize == 0 {
		t.Errorf("slice elem_size = 0, want nonzero")
	}
	elemType := &bt.Types[sliceType.ChildIndex(&bt.Types[0])]
	if elemType.Kind != vbind.KindInt {
		t.Errorf("elem kind = %d, want int", elemType.Kind)
	}
}
