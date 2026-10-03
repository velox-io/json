package venc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/vopt"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
)

// indirectMapValue exceeds abi.MapMaxElemBytes (128), so Go stores each
// element behind a pointer and the slot holds a *V. MAP_STR_ITER must
// dereference the slot once before running the value body.
type indirectMapValue struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Count   int      `json:"count"`
	Ratio   float64  `json:"ratio"`
	Extra   any      `json:"extra,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Enabled bool     `json:"enabled"`
	Pad     [64]byte `json:"-"`
}

type indirectMapHolder struct {
	ID  int                          `json:"id"`
	Map map[string]indirectMapValue  `json:"map"`
	Ptr map[string]*indirectMapValue `json:"ptr,omitempty"`
}

func indirectMapSample() indirectMapHolder {
	return indirectMapHolder{
		ID: 7,
		Map: map[string]indirectMapValue{
			"alpha": {Name: "a", Kind: "scalar", Count: 1, Ratio: 0.5, Tags: []string{"x"}, Enabled: true},
			"beta":  {Name: "b", Kind: "iface", Count: 2, Extra: map[string]any{"k": "v"}},
			"gamma": {Name: "c", Kind: "nested", Count: 3, Ratio: 1.25, Extra: []any{1, true, "s"}, Enabled: true},
		},
	}
}

func indirectMapLarge(n int) indirectMapHolder {
	h := indirectMapHolder{ID: 9, Map: make(map[string]indirectMapValue, n)}
	for i := range n {
		h.Map[fmt.Sprintf("key_%03d", i)] = indirectMapValue{
			Name:  fmt.Sprintf("name-%d", i),
			Kind:  "bulk",
			Count: i,
			Ratio: float64(i) / 64.0,
			Tags:  []string{fmt.Sprintf("t%d", i%7)},
		}
	}
	return h
}

// marshalCompareStdJSON marshals v both ways and compares the parsed JSON
// documents: the native walk emits map entries in slot order while
// encoding/json sorts keys, so byte equality is not the contract.
func marshalCompareStdJSON(t *testing.T, v any) {
	t.Helper()
	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(got, &gotDoc); err != nil {
		t.Fatalf("Marshal output is not valid JSON: %v\n  got: %s", err, got)
	}
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("json.Marshal output is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("documents differ:\n  got:  %s\n  want: %s", got, want)
	}
}

// mapIterInsts returns the lowered MAP_STR_ITER and MAP_STR_ITER_END
// instructions of the root blueprint for mapType, or ok=false if the map was
// not routed to the native iterator.
func mapIterInsts(mapType reflect.Type) (iter, end VjOpHdr, iterExt, endExt VjOpExt, ok bool) {
	ops := compileBlueprint(EncTypeInfoOf(mapType)).Ops
	var seen int
	for off := 0; off < len(ops); {
		hdr := *(*VjOpHdr)(unsafe.Pointer(&ops[off]))
		size := 8
		if isLongOp(hdr.OpType) {
			size = 16
		}
		switch hdr.OpType {
		case opMapStrIter:
			iter, iterExt = hdr, *(*VjOpExt)(unsafe.Pointer(&ops[off+8]))
			seen++
		case opMapStrIterEnd:
			end, endExt = hdr, *(*VjOpExt)(unsafe.Pointer(&ops[off+8]))
			seen++
		}
		off += size
	}
	return iter, end, iterExt, endExt, seen == 2
}

// checkMapIterRouting pins that mapType lowers to MAP_STR_ITER with the probed
// stride as a plain operand on both opcodes, and the indirect-element flag in
// both headers exactly when Go stores the element behind a pointer.
func checkMapIterRouting(t *testing.T, mapType reflect.Type) {
	t.Helper()
	mi := EncTypeInfoOf(mapType).ResolveMap()
	wantIndirect := gort.MapValueIsIndirect(mapType.Elem().Size())
	if mi.SlotSize == 0 {
		t.Errorf("%s: layout probe declined; MAP_STR_ITER requires a probed stride", mapType)
		return
	}
	if mi.Indirect != wantIndirect {
		t.Errorf("%s: probe Indirect=%v, want %v", mapType, mi.Indirect, wantIndirect)
	}
	iter, end, iterExt, endExt, ok := mapIterInsts(mapType)
	if !ok {
		t.Errorf("%s (stride %d): not routed to MAP_STR_ITER", mapType, mi.SlotSize)
		return
	}
	if iterExt.OperandA != int32(mi.SlotSize) || endExt.OperandB != int32(mi.SlotSize) {
		t.Errorf("%s: stride operands ITER=%d END=%d, want %d on both",
			mapType, iterExt.OperandA, endExt.OperandB, mi.SlotSize)
	}
	var wantFlags uint8
	if wantIndirect {
		wantFlags = opFlagIndirectElem
	}
	if iter.Flags != wantFlags || end.Flags != wantFlags {
		t.Errorf("%s: header flags ITER=%#x END=%#x, want %#x on both", mapType, iter.Flags, end.Flags, wantFlags)
	}
}

// TestMapIndirectElemNativeRouting pins that a map whose value type exceeds
// the inline limit probes as indirect and emits MAP_STR_ITER carrying the
// indirect-element flag.
func TestMapIndirectElemNativeRouting(t *testing.T) {
	if !SwissMapLayoutOK {
		t.Skip("swiss map layout unavailable")
	}
	mapType := reflect.TypeFor[map[string]indirectMapValue]()
	if sz := mapType.Elem().Size(); sz <= gort.MapMaxElemBytes {
		t.Fatalf("%s is %d bytes; it must exceed the %d-byte inline limit to force indirect element storage",
			mapType.Elem(), sz, gort.MapMaxElemBytes)
	}
	checkMapIterRouting(t, mapType)
	checkMapIterRouting(t, reflect.TypeFor[map[string]*indirectMapValue]())
}

// TestMapIterRoutingSmallElems pins that elements narrower than a pointer
// keep MAP_STR_ITER. Under the split group layout (GOEXPERIMENT=mapsplitgroup)
// the stride is the bare element size, so 1, 2, 4 and 12 are all valid strides.
func TestMapIterRoutingSmallElems(t *testing.T) {
	if !SwissMapLayoutOK {
		t.Skip("swiss map layout unavailable")
	}
	for _, mapType := range []reflect.Type{
		reflect.TypeFor[map[string]bool](),
		reflect.TypeFor[map[string]uint16](),
		reflect.TypeFor[map[string]int32](),
		reflect.TypeFor[map[string]float32](),
		reflect.TypeFor[map[string]struct{ A, B, C int32 }](),
		reflect.TypeFor[map[string]int](),
		reflect.TypeFor[map[string]string](),
	} {
		checkMapIterRouting(t, mapType)
	}
}

// TestMapIterSmallElemsMarshal encodes the narrow-element maps through the
// native walk, including buffer-full resumes, against encoding/json.
func TestMapIterSmallElemsMarshal(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	type narrow struct {
		B map[string]bool                    `json:"b"`
		U map[string]uint16                  `json:"u"`
		I map[string]int32                   `json:"i"`
		F map[string]float32                 `json:"f"`
		S map[string]struct{ A, B, C int32 } `json:"s"`
	}
	v := narrow{
		B: map[string]bool{}, U: map[string]uint16{}, I: map[string]int32{},
		F: map[string]float32{}, S: map[string]struct{ A, B, C int32 }{},
	}
	for i := range 20 {
		k := fmt.Sprintf("k%02d", i)
		v.B[k] = i%3 == 0
		v.U[k] = uint16(i * 1000)
		v.I[k] = int32(-i * 7)
		v.F[k] = float32(i) / 4
		v.S[k] = struct{ A, B, C int32 }{int32(i), int32(-i), int32(i * i)}
	}
	marshalCompareStdJSON(t, v)

	want, _ := json.Marshal(v)
	var wantDoc any
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("reference is not valid JSON: %v", err)
	}
	ti := EncTypeInfoOf(reflect.TypeOf(v))
	for n := 16; n <= 256; n += 8 {
		es := acquireEncodeState()
		es.applyOptions(vopt.BufSize(n))
		es.buf = make([]byte, 0, n)
		got, err := es.marshalWith(ti, unsafe.Pointer(&v))
		releaseEncodeState(es)
		if err != nil {
			t.Fatalf("buf size %d: %v", n, err)
		}
		var gotDoc any
		if err := json.Unmarshal(got, &gotDoc); err != nil {
			t.Fatalf("buf size %d: output is not valid JSON: %v\n  got: %s", n, err, got)
		}
		if !reflect.DeepEqual(gotDoc, wantDoc) {
			t.Fatalf("buf size %d documents differ:\n  got:  %s\n  want: %s", n, got, want)
		}
	}
}

// TestMapIndirectElemMarshal compares native MAP_STR_ITER output against
// encoding/json across map shapes: inline group, directory traversal, empty
// and nil maps, and a pointer-valued variant.
func TestMapIndirectElemMarshal(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	// Small map: one inline group, the resume-free path.
	marshalCompareStdJSON(t, indirectMapSample())

	// Large map: 50 entries force directory -> table -> group traversal.
	marshalCompareStdJSON(t, indirectMapLarge(50))

	// Empty map.
	marshalCompareStdJSON(t, indirectMapHolder{ID: 1, Map: map[string]indirectMapValue{}})

	// Nil map.
	marshalCompareStdJSON(t, indirectMapHolder{ID: 2})

	// Pointer values: the slot already holds the *V directly, so this map is
	// inline and must not be marked indirect; both flavors must agree.
	h := indirectMapLarge(12)
	h.Ptr = map[string]*indirectMapValue{"p": {Name: "ptr", Kind: "p", Count: 9}}
	marshalCompareStdJSON(t, h)
	ptrMapType := reflect.TypeOf(h.Ptr)
	pmi := EncTypeInfoOf(ptrMapType).ResolveMap()
	if pmi.Indirect {
		t.Errorf("map[string]*indirectMapValue reported indirect; a pointer value sits inline in the slot")
	}
}

// TestMapIndirectElemBufFullResume drives MAP_STR_ITER through buffer-full
// exits inside an indirect map: the resume state re-scans from the saved slot
// index and must dereference the slot pointer again.
func TestMapIndirectElemBufFullResume(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	if !SwissMapLayoutOK {
		t.Skip("swiss map layout unavailable")
	}

	h := indirectMapLarge(20)

	ti := EncTypeInfoOf(reflect.TypeOf(h))

	for _, cap := range []int{16, 24, 32, 48, 64, 96, 128, 192} {
		es := acquireEncodeState()
		es.applyOptions(vopt.BufSize(cap))
		es.buf = make([]byte, 0, cap)
		got, err := es.marshalWith(ti, unsafe.Pointer(&h))
		releaseEncodeState(es)
		if err != nil {
			t.Fatalf("buf cap %d: %v", cap, err)
		}

		want, _ := json.Marshal(h)
		var gotDoc, wantDoc any
		if err := json.Unmarshal(got, &gotDoc); err != nil {
			t.Fatalf("buf cap %d: Marshal output is not valid JSON: %v\n  got: %s", cap, err, got)
		}
		if err := json.Unmarshal(want, &wantDoc); err != nil {
			t.Fatalf("buf cap %d: reference is not valid JSON: %v", cap, err)
		}
		if !reflect.DeepEqual(gotDoc, wantDoc) {
			t.Errorf("buf cap %d documents differ:\n  got:  %s\n  want: %s", cap, got, want)
		}
	}
}

// TestMapIndirectElemNestedMap nests an indirect map inside a struct that is
// itself an indirect map value, so the value body runs another MAP_STR_ITER.
func TestMapIndirectElemNestedMap(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	type outer struct {
		Inner map[string]indirectMapValue `json:"inner"`
	}
	v := outer{Inner: indirectMapLarge(10).Map}
	marshalCompareStdJSON(t, v)
}

// TestMapIndirectElemLongStrings exercises escape dispatch on keys and values
// inside the indirect walk.
func TestMapIndirectElemLongStrings(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	v := indirectMapHolder{
		ID: 3,
		Map: map[string]indirectMapValue{
			strings.Repeat("k\"y\n", 8): {Name: strings.Repeat("v\\\t", 20), Kind: "esc", Count: 1},
			"plain":                     {Name: "p", Kind: "esc", Count: 2},
			strings.Repeat("x", 100):    {Name: strings.Repeat("y", 200), Kind: "esc", Count: 3},
		},
	}
	marshalCompareStdJSON(t, v)
}
