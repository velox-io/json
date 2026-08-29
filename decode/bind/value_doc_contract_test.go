package bind

import (
	"testing"
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
)

func valueFromDescriptor(desc valueabi.Descriptor) value.Value {
	var v value.Value
	valueabi.Store(unsafe.Pointer(&v), desc)
	return v
}

func valueDescriptor(v *value.Value) *valueabi.Descriptor {
	desc := valueabi.Load(unsafe.Pointer(v))
	return &desc
}

func TestUnmarshalValueProgrammaticStringSentinel(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagString = uint64('"') << 56
	)
	packString := func(off, n uint32) uint64 {
		return tagString | uint64(off) | uint64(n)<<32
	}
	arena := make([]byte, 2+1+1+1, 2+1+1+1+64)
	copy(arena, "id\"x\"")
	doc := &valueabi.Doc{
		Tape: []uint64{
			tagObjBeg | 3 | 1<<32,
			packString(0, 2),
			packString(3, 1),
			tagObjEnd,
		},
		StrArena: arena,
	}
	var dst struct {
		ID string `json:"id"`
	}
	if err := UnmarshalValue(valueFromDescriptor(valueabi.Descriptor{Doc: doc, End: int32(len(doc.Tape))}), &dst); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if dst.ID != "x" {
		t.Fatalf("ID = %q, want x", dst.ID)
	}
}

func TestUnmarshalValueRepeatedStringReferenceAppendBound(t *testing.T) {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagString = uint64('"') << 56
		entries   = 128
	)
	packString := func(off, n uint32) uint64 {
		return tagString | uint64(off) | uint64(n)<<32
	}
	arena := make([]byte, 6, 70)
	copy(arena, "q\"\"x\"\"")
	tape := make([]uint64, 2+2*entries)
	tape[0] = tagObjBeg | uint64(len(tape)-1) | entries<<32
	for i := 0; i < entries; i++ {
		tape[1+2*i] = packString(0, 1)
		tape[2+2*i] = packString(2, 3)
	}
	tape[len(tape)-1] = tagObjEnd

	var dst struct {
		Q string `json:"q,string"`
	}
	doc := &valueabi.Doc{Tape: tape, StrArena: arena}
	if err := UnmarshalValue(valueFromDescriptor(valueabi.Descriptor{Doc: doc, End: int32(len(tape))}), &dst); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if dst.Q != "x" {
		t.Fatalf("Q = %q, want x", dst.Q)
	}
}
