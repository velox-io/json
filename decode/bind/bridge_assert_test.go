package bind

import (
	"testing"
	"unsafe"

	nativendec "github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// TestBindBridgeSizes verifies that Go bridge structs match the sizes pinned
// by the native binder. Size drift would make the native state machine misread
// tables.
func TestBindBridgeSizes(t *testing.T) {
	if sz := unsafe.Sizeof(nativendec.BindType{}); sz != 16 {
		t.Errorf("sizeof BindType = %d, want 16", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindField{}); sz != 16 {
		t.Errorf("sizeof BindField = %d, want 16", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindSlotClass{}); sz != 48 {
		t.Errorf("sizeof BindSlotClass = %d, want 48", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindContext{}); sz != 64 {
		t.Errorf("sizeof BindContext = %d, want 64", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindAllocator{}); sz != 120 {
		t.Errorf("sizeof BindAllocator = %d, want 120", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindYield{}); sz != 24 {
		t.Errorf("sizeof BindYield = %d, want 24", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindMachine{}); sz != 288 {
		t.Errorf("sizeof BindMachine = %d, want 288", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindCoreHeader{}); sz != 80 {
		t.Errorf("sizeof BindCoreHeader = %d, want 80", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindMapRegionHeader{}); sz != 32 {
		t.Errorf("sizeof BindMapRegionHeader = %d, want 32", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindPolyTable{}); sz != 40 {
		t.Errorf("sizeof BindPolyTable = %d, want 40", sz)
	}
	if sz := unsafe.Sizeof(nativendec.BindTypeMeta{}); sz != 32 {
		t.Errorf("sizeof BindTypeMeta = %d, want 32", sz)
	}
}

// TestBindMachineOffsets verifies the offsets consumed by the native binder.
// The Go BindAllocator fields are flattened; the first 8 bytes mirror the C
// BindAllocView prefix.
func TestBindMachineOffsets(t *testing.T) {
	var a nativendec.BindMachine
	check := func(name string, got, want uintptr) {
		if got != want {
			t.Errorf("%s offset = %d, want %d", name, got, want)
		}
	}

	check("BindMachine.Ctx", unsafe.Offsetof(a.Ctx), 0)
	check("BindMachine.Alloc", unsafe.Offsetof(a.Alloc), 64)
	check("BindMachine.Yield", unsafe.Offsetof(a.Yield), 184)
	check("BindMachine.Core", unsafe.Offsetof(a.Core), 208)

	check("Ctx.Types", unsafe.Offsetof(a.Ctx.Types), 0)
	check("Ctx.TypeMeta", unsafe.Offsetof(a.Ctx.TypeMeta), 8)
	check("Ctx.Src", unsafe.Offsetof(a.Ctx.Src), 16)
	check("Ctx.SrcLen", unsafe.Offsetof(a.Ctx.SrcLen), 24)
	check("Ctx.RootType", unsafe.Offsetof(a.Ctx.RootType), 32)
	check("Ctx.RootDst", unsafe.Offsetof(a.Ctx.RootDst), 40)
	check("Ctx.OptFlags", unsafe.Offsetof(a.Ctx.OptFlags), 48)
	check("Ctx.AnyTypeIdx", unsafe.Offsetof(a.Ctx.AnyTypeIdx), 52)
	check("Ctx.Polys", unsafe.Offsetof(a.Ctx.Polys), 56)

	check("Alloc.SlotClasses", unsafe.Offsetof(a.Alloc.SlotClasses), 0)
	check("Alloc.StrArena", unsafe.Offsetof(a.Alloc.StrArena), 8)
	check("Alloc.StrArenaCap", unsafe.Offsetof(a.Alloc.StrArenaCap), 16)
	check("Alloc.StrGenStart", unsafe.Offsetof(a.Alloc.StrGenStart), 24)
	check("Alloc.Structural", unsafe.Offsetof(a.Alloc.Structural), 32)
	check("Alloc.StructuralCap", unsafe.Offsetof(a.Alloc.StructuralCap), 40)
	check("Alloc.DeferredDrain", unsafe.Offsetof(a.Alloc.DeferredDrain), 48)
	check("Alloc.DeferredDrainCap", unsafe.Offsetof(a.Alloc.DeferredDrainCap), 56)
	check("Alloc.DeferredDrainUsed", unsafe.Offsetof(a.Alloc.DeferredDrainUsed), 60)
	check("Alloc.MapBuf", unsafe.Offsetof(a.Alloc.MapBuf), 64)
	check("Alloc.MapBufUsed", unsafe.Offsetof(a.Alloc.MapBufUsed), 76)
	check("Alloc.MapBufCap", unsafe.Offsetof(a.Alloc.MapBufCap), 72)
	check("Alloc.ValueTape", unsafe.Offsetof(a.Alloc.ValueTape), 80)
	check("Alloc.ValueDoc", unsafe.Offsetof(a.Alloc.ValueDoc), 88)
	check("Alloc.TapeArena", unsafe.Offsetof(a.Alloc.TapeArena), 96)
	check("Alloc.TapeArenaCap", unsafe.Offsetof(a.Alloc.TapeArenaCap), 104)
	check("Alloc.TapeUsed", unsafe.Offsetof(a.Alloc.TapeUsed), 112)

	check("Yield.PendingAction", unsafe.Offsetof(a.Yield.PendingAction), 0)
	check("Yield.Arg0", unsafe.Offsetof(a.Yield.Arg0), 4)
	check("Yield.Arg1", unsafe.Offsetof(a.Yield.Arg1), 8)
	check("Yield.FirstErrorPos", unsafe.Offsetof(a.Yield.FirstErrorPos), 12)
	check("Yield.Target", unsafe.Offsetof(a.Yield.Target), 16)

	check("Core.Phase", unsafe.Offsetof(a.Core.Phase), 0)
	check("Core.StrUsed", unsafe.Offsetof(a.Core.StrUsed), 40)
	check("Core.Atof", unsafe.Offsetof(a.Core.Atof), 48)
	check("Core.CurAux", unsafe.Offsetof(a.Core.CurAux), 72)

	var r nativendec.BindMapRegionHeader
	check("BindMapRegionHeader.Stride", unsafe.Offsetof(r.Stride), 0)
	check("BindMapRegionHeader.NextEntryOff", unsafe.Offsetof(r.NextEntryOff), 4)
	check("BindMapRegionHeader.Hmap", unsafe.Offsetof(r.Hmap), 16)

	var v nativendec.BindPolyTable
	check("BindPolyTable.DiscFieldOff", unsafe.Offsetof(v.DiscFieldOff), 0)
	check("BindPolyTable.DefaultCaseIdx", unsafe.Offsetof(v.DefaultCaseIdx), 4)
	check("BindPolyTable.CaseCount", unsafe.Offsetof(v.CaseCount), 6)
	check("BindPolyTable.Lookup", unsafe.Offsetof(v.Lookup), 32)

	var sm vbind.StructMetaPayload
	check("StructMetaPayload.InlineVariantIdx", unsafe.Offsetof(sm.InlineVariantIdx), 8)
}

// TestBindKindValues verifies that vbind.Kind values match native BindKind
// values. The native state machine branches on these numbers directly.
func TestBindKindValues(t *testing.T) {
	cases := []struct {
		name string
		got  vbind.Kind
		want vbind.Kind
	}{
		{"Bool", vbind.KindBool, 1},
		{"String", vbind.KindString, 14},
		{"Struct", vbind.KindStruct, 15},
		{"Slice", vbind.KindSlice, 16},
		{"Pointer", vbind.KindPointer, 17},
		{"Array", vbind.KindArray, 22},
		{"Unmarshaler", vbind.KindUnmarshaler, 24},
		{"TextUnmarshaler", vbind.KindTextUnmarshaler, 25},
		{"Value", vbind.KindValue, 26},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("Kind%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
