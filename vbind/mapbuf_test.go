package vbind

import (
	"reflect"
	"testing"
)

func TestMapMetaStride(t *testing.T) {
	type X struct {
		M map[string]int64 `json:"m"`
	}
	tt, err := TypeTreeOf(reflect.TypeFor[X]())
	if err != nil {
		t.Fatal(err)
	}
	for i := range tt.TypeMeta {
		bt := &tt.Types[i]
		if bt.Kind != KindMap {
			continue
		}
		mm := tt.TypeMeta[i].MapMeta()
		wantStride := uint32(16 + ((uint32(reflect.TypeFor[int64]().Size()) + 7) &^ 7))
		if mm.Stride != wantStride {
			t.Errorf("Stride = %d, want %d", mm.Stride, wantStride)
		}
		return
	}
	t.Fatal("no KindMap type found in TypeTree")
}

func TestMapBufMinBytesRecursive(t *testing.T) {
	type Node struct {
		M map[string]*Node `json:"m"`
	}
	type Flat struct {
		M map[string]int `json:"m"`
	}
	ttRec, err := TypeTreeOf(reflect.TypeFor[Node]())
	if err != nil {
		t.Fatal(err)
	}
	ttFlat, err := TypeTreeOf(reflect.TypeFor[Flat]())
	if err != nil {
		t.Fatal(err)
	}
	// Recursive descent keeps multiple map regions live at once.
	if ttRec.MapBufMinBytes <= ttFlat.MapBufMinBytes {
		t.Errorf("recursive MapBufMinBytes (%d) should exceed flat MapBufMinBytes (%d)",
			ttRec.MapBufMinBytes, ttFlat.MapBufMinBytes)
	}
}

func TestMapBufMinBytesNonZeroForMapType(t *testing.T) {
	type X struct {
		M map[string]int `json:"m"`
	}
	tt, err := TypeTreeOf(reflect.TypeFor[X]())
	if err != nil {
		t.Fatal(err)
	}
	if tt.MapBufMinBytes == 0 {
		t.Error("MapBufMinBytes should be non-zero for a type with a map field")
	}
	// The capacity floor must hold one complete region.
	if tt.MapBufMinBytes < 224 {
		t.Errorf("MapBufMinBytes = %d, want >= 224 (one minimal region)", tt.MapBufMinBytes)
	}
}
