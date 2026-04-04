package venc

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"
)

// Benchmark structs: all fields are native-encoder eligible
// (no floats, slices, maps, pointers, interfaces, omitempty).

// benchFlat5 — small flat struct, 5 fields.
type benchFlat5 struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Age    int32  `json:"age"`
	Active bool   `json:"active"`
	Score  uint64 `json:"score"`
}

// benchFlat10 — medium flat struct, 10 fields.
type benchFlat10 struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Age      int32  `json:"age"`
	Active   bool   `json:"active"`
	Score    uint64 `json:"score"`
	Level    int16  `json:"level"`
	Country  string `json:"country"`
	Role     string `json:"role"`
	Rank     uint32 `json:"rank"`
	Verified bool   `json:"verified"`
}

// benchNested — two-level nesting.
type benchInner struct {
	X int64  `json:"x"`
	Y string `json:"y"`
	Z bool   `json:"z"`
}

type benchNested struct {
	ID    int64      `json:"id"`
	Name  string     `json:"name"`
	Inner benchInner `json:"inner"`
	Code  int32      `json:"code"`
}

// benchDeep — three-level nesting.
type benchL3 struct {
	Val  int64  `json:"val"`
	Tag  string `json:"tag"`
	Flag bool   `json:"flag"`
}

type benchL2 struct {
	Name string  `json:"name"`
	L3   benchL3 `json:"l3"`
	Seq  uint32  `json:"seq"`
}

type benchL1 struct {
	ID int64   `json:"id"`
	L2 benchL2 `json:"l2"`
}

// benchDeep5 — five-level nesting, stresses stack frame overhead.
type benchD5 struct {
	V int64 `json:"v"`
}

type benchD4 struct {
	D5 benchD5 `json:"d5"`
	X  int64   `json:"x"`
}

type benchD3 struct {
	D4 benchD4 `json:"d4"`
	Y  string  `json:"y"`
}

type benchD2 struct {
	D3 benchD3 `json:"d3"`
	Z  bool    `json:"z"`
}

type benchD1 struct {
	ID int64   `json:"id"`
	D2 benchD2 `json:"d2"`
}

// benchMultiNest — struct with multiple nested struct siblings.
type benchMNInner struct {
	A int64  `json:"a"`
	B string `json:"b"`
}

type benchMultiNest struct {
	ID int64        `json:"id"`
	N1 benchMNInner `json:"n1"`
	N2 benchMNInner `json:"n2"`
	N3 benchMNInner `json:"n3"`
	OK bool         `json:"ok"`
}

// benchWide — 15 fields, all basic types.
type benchWide struct {
	F1  string `json:"f1"`
	F2  int64  `json:"f2"`
	F3  string `json:"f3"`
	F4  bool   `json:"f4"`
	F5  uint64 `json:"f5"`
	F6  string `json:"f6"`
	F7  int32  `json:"f7"`
	F8  string `json:"f8"`
	F9  bool   `json:"f9"`
	F10 uint32 `json:"f10"`
	F11 string `json:"f11"`
	F12 int16  `json:"f12"`
	F13 string `json:"f13"`
	F14 bool   `json:"f14"`
	F15 int64  `json:"f15"`
}

// Test data

var (
	flat5Val = benchFlat5{
		ID: 12345, Name: "Alice Johnson", Age: 30, Active: true, Score: 98765,
	}
	flat10Val = benchFlat10{
		ID: 12345, Name: "Alice Johnson", Age: 30, Active: true, Score: 98765,
		Level: 42, Country: "United States", Role: "administrator", Rank: 7, Verified: true,
	}
	nestedVal = benchNested{
		ID: 100, Name: "test-object",
		Inner: benchInner{X: 42, Y: "hello world", Z: true},
		Code:  200,
	}
	deepVal = benchL1{
		ID: 1,
		L2: benchL2{
			Name: "level-two",
			L3:   benchL3{Val: 999, Tag: "deep-value", Flag: false},
			Seq:  77,
		},
	}
	deep5Val = benchD1{
		ID: 1,
		D2: benchD2{
			D3: benchD3{
				D4: benchD4{
					D5: benchD5{V: 42},
					X:  100,
				},
				Y: "deep-five",
			},
			Z: true,
		},
	}
	multiNestVal = benchMultiNest{
		ID: 1,
		N1: benchMNInner{A: 10, B: "alpha"},
		N2: benchMNInner{A: 20, B: "bravo"},
		N3: benchMNInner{A: 30, B: "charlie"},
		OK: true,
	}
	wideVal = benchWide{
		F1: "alpha", F2: 123456789, F3: "bravo charlie", F4: true, F5: 9876543210,
		F6: "delta echo", F7: -42, F8: "foxtrot", F9: false, F10: 314159,
		F11: "golf hotel india", F12: 256, F13: "juliet", F14: true, F15: -999999,
	}
)

// Helper: force Go-only encoding path through marshaler.

func marshalGoOnly[T any](v *T) ([]byte, error) {
	m := getMarshaler()
	ti := EncTypeInfoOf(reflect.TypeFor[T]())

	hint := ti.HintBytes
	if hint > cap(m.buf) {
		m.buf = make([]byte, 0, hint)
	}

	// Force Go VM path.
	si := ti.ResolveStruct()
	bp := si.getBlueprint()
	if err := m.goVM(bp, unsafe.Pointer(v)); err != nil {
		putMarshaler(m)
		return nil, err
	}

	return m.finalize(), nil
}

// Flat5 benchmarks

func BenchmarkMarshal_Flat5_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(flat5Val); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Flat5_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&flat5Val); err != nil {
			b.Fatal(err)
		}
	}
}

// Flat10 benchmarks

func BenchmarkMarshal_Flat10_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(flat10Val); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Flat10_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&flat10Val); err != nil {
			b.Fatal(err)
		}
	}
}

// Nested (2-level) benchmarks

func BenchmarkMarshal_Nested_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(nestedVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Nested_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&nestedVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Deep (3-level) benchmarks

func BenchmarkMarshal_Deep_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(deepVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Deep_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&deepVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Deep5 (5-level nesting) benchmarks

func BenchmarkMarshal_Deep5_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(deep5Val); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Deep5_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&deep5Val); err != nil {
			b.Fatal(err)
		}
	}
}

// MultiNest (3 nested struct siblings) benchmarks

func BenchmarkMarshal_MultiNest_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(multiNestVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MultiNest_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&multiNestVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Wide (15 fields) benchmarks

func BenchmarkMarshal_Wide_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(wideVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Wide_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&wideVal); err != nil {
			b.Fatal(err)
		}
	}
}

// String-heavy benchmark (stress string escaping)

type benchStringHeavy struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Author  string `json:"author"`
	Summary string `json:"summary"`
	ID      int64  `json:"id"`
}

var stringHeavyVal = benchStringHeavy{
	Title:   `Breaking: "Major Event" Unfolds — <details> & more`,
	Body:    "Line 1\nLine 2\nLine 3\tTabbed\nLine 4 with \"quotes\" and \\backslash\\ and <html>&amp;</html>",
	Author:  "Jane O'Connor-Smith",
	Summary: "A quick summary with special chars: \x00\x01\x1f and unicode 世界 🌍",
	ID:      42,
}

func BenchmarkMarshal_StringHeavy_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(stringHeavyVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_StringHeavy_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&stringHeavyVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Float-heavy benchmark (stress float formatting via Ryu)

type benchWithFloats struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Rate  float32 `json:"rate"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

var floatVal = benchWithFloats{
	ID: 12345, Name: "test", Score: 3.141592653589793,
	Rate: 2.718, X: 100000.5, Y: -0.001,
}

func BenchmarkMarshal_WithFloats_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(floatVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_WithFloats_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&floatVal); err != nil {
			b.Fatal(err)
		}
	}
}

// omitempty benchmark (mix of zero and non-zero fields)

type benchOmitempty struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name,omitempty"`
	Score  float64 `json:"score,omitempty"`
	Active bool    `json:"active,omitempty"`
	Tag    string  `json:"tag,omitempty"`
	Count  int32   `json:"count,omitempty"`
}

// Half the fields are zero → skipped by omitempty.
var omitVal = benchOmitempty{
	ID: 12345, Name: "test", Score: 3.14,
	// Active=false, Tag="", Count=0 → omitted
}

func BenchmarkMarshal_Omitempty_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(omitVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Omitempty_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&omitVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Slice of struct benchmarks

// benchSliceItem — small struct for slice benchmarks.
type benchSliceItem struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

var sliceVal100 = func() []benchSliceItem {
	items := make([]benchSliceItem, 100)
	for i := range items {
		items[i] = benchSliceItem{ID: i, Name: "user", Score: i * 10}
	}
	return items
}()

func BenchmarkMarshal_Slice100_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(sliceVal100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Slice100_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&sliceVal100); err != nil {
			b.Fatal(err)
		}
	}
}

// marshalSliceGoOnly encodes a []T using velox's Go-path element loop,
// bypassing the C batch array optimisation.  Each element is still encoded
// via encodeStructGo (velox Go struct encoder).
func marshalSliceGoOnly[T any](sl *[]T) ([]byte, error) {
	m := getMarshaler()
	ti := EncTypeInfoOf(reflect.TypeFor[T]())
	si := ti.ResolveStruct()
	bp := si.getBlueprint()

	hint := ti.HintBytes * len(*sl)
	if hint > cap(m.buf) {
		m.buf = make([]byte, 0, hint)
	}

	m.buf = append(m.buf, '[')
	for i := range *sl {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if err := m.goVM(bp, unsafe.Pointer(&(*sl)[i])); err != nil {
			putMarshaler(m)
			return nil, err
		}
	}
	m.buf = append(m.buf, ']')
	return m.finalize(), nil
}

func BenchmarkMarshal_Slice100_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalSliceGoOnly(&sliceVal100); err != nil {
			b.Fatal(err)
		}
	}
}
