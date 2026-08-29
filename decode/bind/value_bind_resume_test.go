package bind

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
)

// Rare-path coverage for the tape-bind sub-routine (t_* labels). The parity
// and roundTrip suites use small inputs, which never exhaust a slot class or
// fill the map buffer, so several resume and growth arms of the sub-routine
// run only under the forcings below. Each test states the mechanism that
// makes its path deterministic.

// ptrFieldHost borrows innerVal (map_test.go) so the pointee slot class is
// shared with no other shape in this file.
type ptrFieldHost struct {
	P *innerVal `json:"p"`
}

// TestUnmarshalValuePtrFieldResume forces the BLOCK_FULL resume at phase
// BIND_PHASE_TAPE_BIND_FIELD_VALUE_PTR_RESUME. The pointee class of *innerVal
// starts with a 64-slot bump block and its cursor persists across parses on
// one Parser, so past that point the mid-field PTR borrow yields BLOCK_FULL
// and the resume label must re-derive the field from cur_struct_field.
func TestUnmarshalValuePtrFieldResume(t *testing.T) {
	p, err := NewParser[ptrFieldHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := `{"p":{"x":7,"s":"seven"}}`
	// 200 rounds crosses the initial block regardless of its exact size.
	for i := range 200 {
		val, err := dom.Parse([]byte(src))
		if err != nil {
			t.Fatalf("round %d: dom.Parse: %v", i, err)
		}
		var h ptrFieldHost
		if err := p.UnmarshalValue(val, &h); err != nil {
			t.Fatalf("round %d: UnmarshalValue: %v", i, err)
		}
		if h.P == nil || h.P.X != 7 || h.P.S != "seven" {
			t.Fatalf("round %d: P = %+v", i, h.P)
		}
	}
}

// hopLeaf is the pointee of an embedded pointer; each fresh host
// allocates one through the hop's SlotClass.
type hopLeaf struct {
	X int    `json:"x"`
	S string `json:"s"`
}

type viaPtrHost struct {
	*hopLeaf
	Greet string `json:"greet"`
}

// TestUnmarshalValueViaPtrHopResume forces the BLOCK_FULL resume at phase
// BIND_PHASE_TAPE_BIND_OBJECT_FIELD_VALUE. The hopLeaf pointee class starts
// with a bump block and its cursor persists across parses on one Parser, so
// a later round's hop resolution yields mid-field; the resume restores the
// stashed field and replays the hops from hop zero.
func TestUnmarshalValueViaPtrHopResume(t *testing.T) {
	p, err := NewParser[viaPtrHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := `{"x":7,"s":"seven","greet":"hi"}`
	for i := range 200 {
		val, err := dom.Parse([]byte(src))
		if err != nil {
			t.Fatalf("round %d: dom.Parse: %v", i, err)
		}
		var h viaPtrHost
		if err := p.UnmarshalValue(val, &h); err != nil {
			t.Fatalf("round %d: UnmarshalValue: %v", i, err)
		}
		if h.hopLeaf == nil || h.X != 7 || h.S != "seven" || h.Greet != "hi" {
			t.Fatalf("round %d: host = %+v", i, h)
		}
	}
}

// TestUnmarshalValueVariantPtrCaseResume drives the same resume label through
// its VARIANT branch: a pointer variant case whose pointee borrow exhausts
// mid-bind re-derives the case from the stashed eface instead of
// cur_struct_field. variantEnvelopePtrMap already carries a *variantUser case.
func TestUnmarshalValueVariantPtrCaseResume(t *testing.T) {
	p, err := NewParser[variantEnvelopePtrMap]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := `{"type":"ptruser","data":{"name":"Grace","role":"admin"}}`
	for i := range 200 {
		val, err := dom.Parse([]byte(src))
		if err != nil {
			t.Fatalf("round %d: dom.Parse: %v", i, err)
		}
		var env variantEnvelopePtrMap
		if err := p.UnmarshalValue(val, &env); err != nil {
			t.Fatalf("round %d: UnmarshalValue: %v", i, err)
		}
		u, ok := env.Data.(*variantUser)
		if !ok || u.Name != "Grace" || u.Role != "admin" {
			t.Fatalf("round %d: Data = %#v", i, env.Data)
		}
	}
}

// TestUnmarshalValueAnySlotExhaustion forces the BLOCK_FULL resume inside
// t_any_value boxing: 300 elements cross the float64 and string classes'
// initial blocks mid-array, so the boxing of a later element resumes after
// ServeNewBlock.
func TestUnmarshalValueAnySlotExhaustion(t *testing.T) {
	type host struct {
		N []any `json:"n"`
	}
	var b strings.Builder
	b.WriteString(`{"n":[`)
	for i := range 300 {
		if i > 0 {
			b.WriteByte(',')
		}
		if i%7 == 0 {
			b.WriteString(`"s`)
			b.WriteString(strings.Repeat("x", i%5+1))
			b.WriteByte('"')
		} else {
			b.WriteString(strings.Repeat("0.", 1) + string(rune('0'+i%10)) + "5")
		}
	}
	b.WriteString(`]}`)
	parity3[host](t, "any-300", b.String())
}

// TestUnmarshalValueSiblingMapsFillMapBuf forces the t_map_open retry. Every
// map open carves a kv region (header plus 16 slots) out of map_buf, and
// closed regions stay resident until a drain, so 40 sibling maps with 6
// entries each overflow the 4KiB floor mid-array and a later open retries
// after the driver compacts. Two-element slice-of-map tests never cross the
// floor.
func TestUnmarshalValueSiblingMapsFillMapBuf(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := range 40 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		for k := range 6 {
			if k > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"k` + string(rune('a'+k)) + `":"v` + string(rune('0'+i%10)) + `"`)
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	parity3[[]map[string]string](t, "sibling-40", b.String())
}

// TestUnmarshalValueRecBatchRefillBypass drives RecBatch slice growth inside
// the tape-bind array path: 200 children on one recursive node grow through
// the matrix rows (REFILL) and past the 128-element cap (BYPASS to a Go-side
// backing). rec_slice_test.go covers the same mode on the JSON bind path
// only.
func TestUnmarshalValueRecBatchRefillBypass(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"v":0,"c":[`)
	for i := range 200 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"v":`)
		b.WriteString(string(rune('0' + i%10)))
		b.WriteString(`,"c":[]}`)
	}
	b.WriteString(`]}`)
	roundTrip[recSliceNode](t, b.String())
}

// TestUnmarshalValueNarrowOverflow pins the range check of
// tape_bind_write_number at container element positions. diff_test.go covers
// in-range narrow kinds at struct fields; the reject direction (out-of-range
// int8/uint/int16/uint16) is pinned here across field, slice element, fixed
// array, and map value positions. All three legs must reject.
func TestUnmarshalValueNarrowOverflow(t *testing.T) {
	parity3[diffFlat](t, "i8-field-overflow", `{"i8":128}`)
	parity3[diffFlat](t, "u-field-negative", `{"u":-1}`)
	parity3[[]int8](t, "i8-elem-overflow", `[1,127,128]`)
	parity3[[2]int16](t, "i16-elem-overflow", `[100,40000]`)
	parity3[map[string]uint16](t, "u16-map-overflow", `{"k":70000}`)
	// Full-width unsigned kinds rejected a negative payload silently before
	// the range check covered them; positive control pins acceptance of the
	// TAPE_UINT64 tag above 2^63.
	roundTripExpectErr[uint](t, `-2`)
	roundTripExpectErr[uint64](t, `-1`)
	roundTrip[uint64](t, `18446744073709551615`)
}

func TestUnmarshalValueFloat32Overflow(t *testing.T) {
	type field struct {
		V float32 `json:"v"`
	}

	for _, token := range []string{"1e39", "-1e39"} {
		parity3[float32](t, "root-"+token, token)
		parity3[field](t, "field-"+token, `{"v":`+token+`}`)
		parity3[[]float32](t, "slice-"+token, `[`+token+`]`)
		parity3[[1]float32](t, "array-"+token, `[`+token+`]`)
		parity3[map[string]float32](t, "map-"+token, `{"v":`+token+`}`)
	}

	parity3[float32](t, "finite-positive", `3.4e38`)
	parity3[float32](t, "finite-negative", `-3.4e38`)
	parity3[float32](t, "single-rounding-midpoint", `1.0000000596046448`)
	parity3[float32](t, "max-finite-rounding", `3.4028235677973366e38`)
	parity3[float32](t, "raw-single-rounding", `1.00000005960464480000000000000000001`)
	parity3[float32](t, "underflow-to-zero", `1e-50`)
}

func TestUnmarshalValueCorruptDoubleSpanRejected(t *testing.T) {
	// Every parser-produced TagDouble carries the decimal span in str_arena.
	// A zero-span word can only come from a corrupt tape; the float32 binder
	// must reject it instead of narrowing the binary64 payload.
	word := uint64(valueabi.TagDouble) << 56
	for _, tc := range []struct {
		name string
		desc valueabi.Descriptor
	}{
		{"root", valueabi.Descriptor{Doc: &valueabi.Doc{Tape: []uint64{word, math.Float64bits(1.5)}}, End: 2}},
		{"nonzero-base", valueabi.Descriptor{Doc: &valueabi.Doc{Tape: []uint64{0, word, math.Float64bits(1.5)}}, Base: 1, End: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got float32
			if err := UnmarshalValue(valueFromDescriptor(tc.desc), &got); err == nil {
				t.Fatalf("UnmarshalValue accepted zero-span TagDouble, value = %g", got)
			}
		})
	}
}

func TestUnmarshalValueFloat32UsesStableNumberText(t *testing.T) {
	const token = `1.0000000596046448`
	want64, err := strconv.ParseFloat(token, 32)
	if err != nil {
		t.Fatalf("ParseFloat: %v", err)
	}
	want := float32(want64)

	check := func(t *testing.T, v value.Value) {
		t.Helper()
		desc := valueDescriptor(&v)
		root, _ := desc.Extent()
		word := desc.Doc.Tape[int(desc.Base)+root]
		if off := uint32(word); off == 0 {
			t.Fatal("double text offset = 0; want a nonzero StrArena offset")
		}
		src := desc.Doc.Src
		for i := range src {
			src[i] = '0'
		}
		var got float32
		if err := UnmarshalValue(v, &got); err != nil {
			t.Fatalf("UnmarshalValue: %v", err)
		}
		if got != want {
			t.Errorf("value = %.9g, want %.9g", got, want)
		}
	}

	t.Run("dom", func(t *testing.T) {
		root, err := dom.Parse([]byte(`{"padding":"x","v":` + token + `}`))
		if err != nil {
			t.Fatalf("dom.Parse: %v", err)
		}
		check(t, root.Get("v"))
	})

	t.Run("bind", func(t *testing.T) {
		var host struct {
			Padding string      `json:"padding"`
			V       value.Value `json:"v"`
		}
		if err := Unmarshal([]byte(`{"padding":"x","v":`+token+`}`), &host); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		check(t, host.V)
	})
}

func TestUnmarshalValueTypeMismatchHasNoSourceOffset(t *testing.T) {
	val, err := dom.Parse([]byte(`"not an int"`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var dst int
	err = UnmarshalValue(val, &dst)
	var typeErr *UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("error = %v, want *UnmarshalTypeError", err)
	}
	if typeErr.Offset != 0 {
		t.Errorf("Offset = %d, want 0", typeErr.Offset)
	}
	if typeErr.Value != "json" {
		t.Errorf("Value = %q, want %q", typeErr.Value, "json")
	}

	err = Unmarshal([]byte(`"not an int"`), &dst)
	if !errors.As(err, &typeErr) {
		t.Fatalf("JSON error = %v, want *UnmarshalTypeError", err)
	}
	if typeErr.Offset != 0 || typeErr.Value != "string" {
		t.Errorf("JSON error = {Offset:%d Value:%q}, want {Offset:0 Value:string}", typeErr.Offset, typeErr.Value)
	}
}

// TestUnmarshalValueMapValueTypeMismatch pins the map value mismatch arm
// (TAPE_BIND_TYPE_MISMATCH_SKIP): the first mismatch is recorded and the
// value skipped, the error surfaces at completion. Root and array element
// mismatches are pinned in value_bind_test.go; the map value arm is not.
func TestUnmarshalValueMapValueTypeMismatch(t *testing.T) {
	parity3[mapStrInt](t, "string-into-int", `{"m":{"a":"s","b":2}}`)
	parity3[mapStrInt](t, "bool-into-int", `{"m":{"ok":true}}`)
	parity3[mapStrStruct](t, "scalar-into-struct", `{"m":{"a":1}}`)
}

// TestUnmarshalValueValueInContainers covers value.Value at slice element
// and map value positions, which bind by aliasing the input tape subtree.
// value_test.go covers these positions on the JSON bind path; the tape-bind
// arms (array and map VALUE cases) were only reached at struct fields.
func TestUnmarshalValueValueInContainers(t *testing.T) {
	type host struct {
		Vs []value.Value          `json:"vs"`
		M  map[string]value.Value `json:"m"`
	}
	src := `{"vs":[1,"two",[3],{"k":4}],"m":{"a":true,"b":{"x":1}}}`

	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var got host
	if err := UnmarshalValue(val, &got); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	var ref host
	if err := Unmarshal([]byte(src), &ref); err != nil {
		t.Fatalf("Unmarshal ref: %v", err)
	}

	if len(got.Vs) != len(ref.Vs) || len(got.M) != len(ref.M) {
		t.Fatalf("container sizes: Vs=%d/%d M=%d/%d",
			len(got.Vs), len(ref.Vs), len(got.M), len(ref.M))
	}
	for i := range got.Vs {
		if got.Vs[i].String() != ref.Vs[i].String() {
			t.Errorf("Vs[%d] = %s, want %s", i, got.Vs[i].String(), ref.Vs[i].String())
		}
	}
	for k, v := range got.M {
		if v.String() != ref.M[k].String() {
			t.Errorf("M[%q] = %s, want %s", k, v.String(), ref.M[k].String())
		}
	}
	// The aliased subtrees must be navigable, not just printable.
	v3 := got.Vs[2].Index(0)
	if n, ok := v3.Int(); !ok || n != 3 {
		t.Errorf("Vs[2][0] = %d (ok=%v), want 3", n, ok)
	}
	mb := got.M["b"]
	mx := mb.Get("x")
	if n, ok := mx.Int(); !ok || n != 1 {
		t.Errorf("M[b].x = %d (ok=%v), want 1", n, ok)
	}
}
