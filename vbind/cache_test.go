package vbind

import (
	"reflect"
	"sync"
	"testing"
)

func TestTypeTreeOfIdempotent(t *testing.T) {
	type X struct {
		V int
	}
	a, err := TypeTreeOf(reflect.TypeFor[X]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := TypeTreeOf(reflect.TypeFor[X]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if a != b {
		t.Errorf("TypeTreeOf returned different pointers: %p vs %p", a, b)
	}
}

func TestTypeTreeOfRecursiveStable(t *testing.T) {
	type Tree struct {
		Val      int
		Children []*Tree
	}
	tt0, err := TypeTreeOf(reflect.TypeFor[Tree]())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rootTreePtr := &tt0.Types[tt0.Root]
	if rootTreePtr.Kind != KindStruct {
		t.Fatalf("root not struct")
	}
	// Use index accessors throughout. Reconstructing child pointers from stored
	// relative offsets is rejected by -d=checkptr under -race.
	firstIdx := rootTreePtr.StructFirstFieldIndex(&tt0.Fields[0])
	secondField := &tt0.Fields[firstIdx+1]
	sliceFieldType := &tt0.Types[secondField.FieldTypeIndex(&tt0.Types[0])]
	if sliceFieldType.Kind != KindSlice {
		t.Fatalf("second field not slice, kind=%d", sliceFieldType.Kind)
	}
	ptrType := &tt0.Types[sliceFieldType.ChildIndex(&tt0.Types[0])]
	if ptrType.Kind != KindPointer {
		t.Fatalf("slice elem not pointer, kind=%d", ptrType.Kind)
	}
	if ptrType.ChildIndex(&tt0.Types[0]) != tt0.Root {
		t.Errorf("recursive *Tree does not point back to root Tree type")
	}
}

func TestTypeTreeOfConcurrent(t *testing.T) {
	type Node struct {
		Val  int
		Next *Node
	}
	rt := reflect.TypeFor[Node]()
	const N = 64

	// Release all goroutines together so they race on the cache miss before one
	// result is published.
	var (
		wg      sync.WaitGroup
		results = make([]*TypeTree, N)
		errs    = make([]error, N)
		start   = make(chan struct{})
	)
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = TypeTreeOf(rt)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range N {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
	}
	first := results[0]
	for i := 1; i < N; i++ {
		if results[i] != first {
			t.Errorf("goroutine %d returned different *TypeTree (%p) than goroutine 0 (%p)", i, results[i], first)
		}
	}
}
