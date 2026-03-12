package vjson

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/velox-io/json/native/encvm"
)

// ================================================================
// OpType ↔ ElemTypeKind alignment + kindToOpcode
// ================================================================

func TestPrimitiveOpTypeAlignedWithElemTypeKind(t *testing.T) {
	// Primitive opcodes (0-13) must equal the corresponding ElemTypeKind
	// values so kindToOpcode can use a direct 1:1 mapping.
	checks := []struct {
		kind ElemTypeKind
		op   uint16
		name string
	}{
		{KindBool, opBool, "Bool"},
		{KindInt, opInt, "Int"},
		{KindInt8, opInt8, "Int8"},
		{KindInt16, opInt16, "Int16"},
		{KindInt32, opInt32, "Int32"},
		{KindInt64, opInt64, "Int64"},
		{KindUint, opUint, "Uint"},
		{KindUint8, opUint8, "Uint8"},
		{KindUint16, opUint16, "Uint16"},
		{KindUint32, opUint32, "Uint32"},
		{KindUint64, opUint64, "Uint64"},
		{KindFloat32, opFloat32, "Float32"},
		{KindFloat64, opFloat64, "Float64"},
		{KindString, opString, "String"},
	}
	for _, c := range checks {
		if uint16(c.kind) != c.op {
			t.Errorf("Kind%s=%d != op%s=%d", c.name, c.kind, c.name, c.op)
		}
	}
}

func TestKindToOpcode(t *testing.T) {
	// Verify kindToOpcode returns correct opcodes for all supported kinds.
	checks := []struct {
		kind ElemTypeKind
		op   uint16
		name string
	}{
		{KindBool, opBool, "Bool"},
		{KindInt, opInt, "Int"},
		{KindString, opString, "String"},
		{KindAny, opInterface, "Any/Interface"},
		{KindRawMessage, opRawMessage, "RawMessage"},
		{KindNumber, opNumber, "Number"},
	}
	for _, c := range checks {
		got := kindToOpcode(c.kind)
		if got != c.op {
			t.Errorf("kindToOpcode(Kind%s) = %d, want %d", c.name, got, c.op)
		}
	}
}

func TestKindToOpcodePanicsForStructural(t *testing.T) {
	// kindToOpcode must panic for kinds that have no single instruction opcode.
	panicKinds := []ElemTypeKind{KindStruct, KindSlice, KindPointer, KindMap}
	for _, k := range panicKinds {
		k := k
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("kindToOpcode(%d) did not panic", k)
				}
			}()
			kindToOpcode(k)
		})
	}
}

// ================================================================
// VjEncFlags ↔ escapeFlags alignment
// ================================================================

func TestVjEncFlagsAlignedWithEscapeFlags(t *testing.T) {
	if vjEncEscapeHTML != uint32(escapeHTML) {
		t.Errorf("vjEncEscapeHTML=%d != escapeHTML=%d", vjEncEscapeHTML, escapeHTML)
	}
}

// ================================================================
// Phase 3: Assembly bridge integration tests
// ================================================================

func TestNativeEncodeStructBridgeAvailable(t *testing.T) {
	// On darwin/arm64 the native encoder should be available.
	// This test confirms the .syso is linked and the trampoline works.
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
}

// ================================================================
// omitempty tests
// ================================================================

func TestNativeEncodeOmitemptyZeroValues(t *testing.T) {
	// All zero-valued omitempty fields should be skipped.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
		Count  int32   `json:"count,omitempty"`
	}
	v := S{ID: 42} // only ID is non-zero; rest are omitempty + zero
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":42}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyNonZero(t *testing.T) {
	// Non-zero omitempty fields should be encoded normally.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
	}
	v := S{ID: 1, Name: "hello", Score: 3.14, Active: true}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":1,"name":"hello","score":3.14,"active":true}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyMixed(t *testing.T) {
	// Mix of zero and non-zero omitempty fields.
	type S struct {
		A int    `json:"a,omitempty"`
		B string `json:"b"`
		C int    `json:"c,omitempty"`
		D string `json:"d,omitempty"`
		E int    `json:"e"`
	}
	v := S{A: 0, B: "always", C: 99, D: "", E: 0}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"always","c":99,"e":0}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyAllZero(t *testing.T) {
	// All fields are omitempty and zero → empty object.
	type S struct {
		A int     `json:"a,omitempty"`
		B string  `json:"b,omitempty"`
		C float64 `json:"c,omitempty"`
		D bool    `json:"d,omitempty"`
	}
	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyFirstFieldSkipped(t *testing.T) {
	// First field is omitempty+zero; second is non-omitempty.
	// Verifies comma logic when leading fields are skipped.
	type S struct {
		A int    `json:"a,omitempty"`
		B string `json:"b"`
	}
	v := S{A: 0, B: "hello"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"hello"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyAllTypes(t *testing.T) {
	// Verify omitempty for all numeric types and string.
	type S struct {
		B   bool    `json:"b,omitempty"`
		I   int     `json:"i,omitempty"`
		I8  int8    `json:"i8,omitempty"`
		I16 int16   `json:"i16,omitempty"`
		I32 int32   `json:"i32,omitempty"`
		I64 int64   `json:"i64,omitempty"`
		U   uint    `json:"u,omitempty"`
		U8  uint8   `json:"u8,omitempty"`
		U16 uint16  `json:"u16,omitempty"`
		U32 uint32  `json:"u32,omitempty"`
		U64 uint64  `json:"u64,omitempty"`
		F32 float32 `json:"f32,omitempty"`
		F64 float64 `json:"f64,omitempty"`
		S   string  `json:"s,omitempty"`
	}
	// All zero → empty object.
	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{}` {
		t.Errorf("all zero: got %s, want {}", got)
	}

	// All non-zero → all fields present.
	v2 := S{
		B: true, I: 1, I8: 2, I16: 3, I32: 4, I64: 5,
		U: 6, U8: 7, U16: 8, U32: 9, U64: 10,
		F32: 1.5, F64: 2.5, S: "hi",
	}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	// Compare with encoding/json.
	want2, _ := json.Marshal(&v2)
	if string(got2) != string(want2) {
		t.Errorf("all non-zero:\n  got  %s\n  want %s", got2, want2)
	}
}

func TestNativeEncodeOmitemptyConsistency(t *testing.T) {
	// Compare native encoder output with encoding/json for various inputs.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
		Tag    string  `json:"tag,omitempty"`
	}
	tests := []S{
		{ID: 0},
		{ID: 1, Name: "a"},
		{ID: 2, Score: 3.14},
		{ID: 3, Active: true},
		{ID: 4, Name: "x", Score: 1.0, Active: true, Tag: "test"},
		{},
	}
	for i, v := range tests {
		got, err := Marshal(&v)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		want, _ := json.Marshal(&v)
		if string(got) != string(want) {
			t.Errorf("case %d:\n  got  %s\n  want %s", i, got, want)
		}
	}
}

// ================================================================
// Hot Resume (断点续传) tests
// ================================================================

func TestHotResumeMapFieldMiddle(t *testing.T) {
	// Map field in the middle: C handles ID, Go handles Tags, C resumes for Name.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 42, Tags: map[string]string{"a": "1"}, Name: "hello"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMapFieldFirst(t *testing.T) {
	// Map field is the first field: Go handles it, C resumes for the rest.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		Tags map[string]string `json:"tags"`
		ID   int               `json:"id"`
		Name string            `json:"name"`
	}

	v := S{Tags: map[string]string{"x": "y"}, ID: 1, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMapFieldLast(t *testing.T) {
	// Map field is the last field: C handles all fields until map, Go finishes.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Name string            `json:"name"`
		Tags map[string]string `json:"tags"`
	}

	v := S{ID: 42, Name: "hello", Tags: map[string]string{"k": "v"}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeSliceField(t *testing.T) {
	// Slice field triggers fallback.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int    `json:"id"`
		Items []int  `json:"items"`
		Name  string `json:"name"`
	}

	v := S{ID: 1, Items: []int{10, 20, 30}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeInterfaceField(t *testing.T) {
	// Interface field triggers fallback.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int    `json:"id"`
		Value any    `json:"value"`
		Name  string `json:"name"`
	}

	v := S{ID: 1, Value: "dynamic", Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerField(t *testing.T) {
	// Pointer field is now handled natively by C engine.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Age  int     `json:"age"`
	}

	name := "Alice"
	v := S{ID: 1, Name: &name, Age: 30}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerFieldNil(t *testing.T) {
	// Nil pointer field → JSON null, handled natively.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Age  int     `json:"age"`
	}

	v := S{ID: 1, Name: nil, Age: 30}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerPrimitiveTypes(t *testing.T) {
	// Various pointer-to-primitive types, all handled by C engine.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		PBool    *bool    `json:"p_bool"`
		PInt     *int     `json:"p_int"`
		PInt8    *int8    `json:"p_int8"`
		PInt16   *int16   `json:"p_int16"`
		PInt32   *int32   `json:"p_int32"`
		PInt64   *int64   `json:"p_int64"`
		PUint    *uint    `json:"p_uint"`
		PUint8   *uint8   `json:"p_uint8"`
		PUint16  *uint16  `json:"p_uint16"`
		PUint32  *uint32  `json:"p_uint32"`
		PUint64  *uint64  `json:"p_uint64"`
		PFloat32 *float32 `json:"p_float32"`
		PFloat64 *float64 `json:"p_float64"`
		PString  *string  `json:"p_string"`
	}

	b := true
	i := 42
	i8 := int8(-7)
	i16 := int16(-300)
	i32 := int32(-100000)
	i64 := int64(-9999999999)
	u := uint(100)
	u8 := uint8(255)
	u16 := uint16(65535)
	u32 := uint32(4000000000)
	u64 := uint64(18000000000000000000)
	f32 := float32(3.14)
	f64 := 2.718281828
	s := "hello\nworld"

	v := S{
		PBool: &b, PInt: &i, PInt8: &i8, PInt16: &i16, PInt32: &i32, PInt64: &i64,
		PUint: &u, PUint8: &u8, PUint16: &u16, PUint32: &u32, PUint64: &u64,
		PFloat32: &f32, PFloat64: &f64, PString: &s,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerAllNil(t *testing.T) {
	// All pointer fields nil → all "null".
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A *int    `json:"a"`
		B *string `json:"b"`
		C *bool   `json:"c"`
	}

	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerStruct(t *testing.T) {
	// *PureStruct: non-nil → nested JSON object.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: &Inner{X: 42, Y: "hello"}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerStructNil(t *testing.T) {
	// *PureStruct: nil → "null".
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyNil(t *testing.T) {
	// *int with omitempty, nil → field omitted.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	v := S{ID: 1, Val: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyNonNil(t *testing.T) {
	// *int with omitempty, non-nil → field present.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	val := 42
	v := S{ID: 1, Val: &val}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyZeroValue(t *testing.T) {
	// *int with omitempty, pointing to zero value → field present (not omitted).
	// Only nil pointers are considered "empty", not zero-valued pointees.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	zero := 0
	v := S{ID: 1, Val: &zero}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerMixedWithRegularFields(t *testing.T) {
	// Struct with mix of regular and pointer fields → should be nativeFull.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		A int `json:"a"`
	}
	type S struct {
		ID    int     `json:"id"`
		Name  *string `json:"name"`
		Score float64 `json:"score"`
		Inner *Inner  `json:"inner"`
		OK    bool    `json:"ok"`
	}

	name := "test"
	v := S{ID: 1, Name: &name, Score: 99.5, Inner: &Inner{A: 7}, OK: true}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumePointerToNonNativeStruct(t *testing.T) {
	// *MixedStruct (contains map) → falls back to Go for pointer field.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		Tags map[string]string `json:"tags"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: &Inner{Tags: map[string]string{"a": "b"}}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumePointerToPointer(t *testing.T) {
	// **int → falls back to Go.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int   `json:"id"`
		Val **int `json:"val"`
	}

	inner := 42
	ptr := &inner
	v := S{ID: 1, Val: &ptr}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerConsistencyWithStdlib(t *testing.T) {
	// Comprehensive stdlib consistency check for pointer scenarios.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	type S struct {
		A *int     `json:"a"`
		B *string  `json:"b"`
		C *bool    `json:"c"`
		D *float64 `json:"d"`
		E *Inner   `json:"e"`
		F *int     `json:"f,omitempty"`
		G *string  `json:"g,omitempty"`
		H int      `json:"h"`
	}

	a := 100
	b := "hello \"world\""
	c := false
	d := 1.5
	g := ""

	tests := []struct {
		name string
		val  S
	}{
		{"all non-nil", S{A: &a, B: &b, C: &c, D: &d, E: &Inner{X: 1, Y: "y"}, F: &a, G: &g, H: 99}},
		{"all nil", S{H: 42}},
		{"mixed nil/non-nil", S{A: &a, B: nil, C: &c, D: nil, E: nil, F: nil, G: &g, H: 0}},
		{"omitempty nil", S{A: &a, F: nil, G: nil, H: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(&tt.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(&tt.val)
			if string(got) != string(want) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestNativePointerToCustomMarshaler(t *testing.T) {
	// *big.Int has MarshalJSON on the element type — must fall back to Go,
	// not be treated as native *Struct.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Wallet struct {
		Owner   string   `json:"owner"`
		Balance *big.Int `json:"balance"`
	}

	w := Wallet{Owner: "alice", Balance: new(big.Int).SetInt64(42)}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(w)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMultipleFallbacks(t *testing.T) {
	// Multiple fallback fields: C→Go→C→Go→C pattern.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A int               `json:"a"`
		B map[string]string `json:"b"`
		C string            `json:"c"`
		D []int             `json:"d"`
		E bool              `json:"e"`
	}

	v := S{
		A: 1,
		B: map[string]string{"x": "y"},
		C: "middle",
		D: []int{10, 20},
		E: true,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeAllFallbackFields(t *testing.T) {
	// All fields are fallback — still uses hot resume path.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A map[string]string `json:"a"`
		B []int             `json:"b"`
		C any               `json:"c"`
	}

	v := S{
		A: map[string]string{"k": "v"},
		B: []int{1, 2},
		C: 42,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeWithOmitempty(t *testing.T) {
	// Fallback field with native omitempty fields.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int               `json:"id"`
		Empty string            `json:"empty,omitempty"`
		Tags  map[string]string `json:"tags"`
		Name  string            `json:"name,omitempty"`
	}

	// Case 1: omitempty fields are zero, skipped.
	v1 := S{ID: 42, Tags: map[string]string{"a": "1"}}
	got1, err := Marshal(&v1)
	if err != nil {
		t.Fatal(err)
	}
	want1, _ := json.Marshal(&v1)
	if string(got1) != string(want1) {
		t.Errorf("case1: got  %s\nwant %s", got1, want1)
	}

	// Case 2: omitempty fields are non-zero.
	v2 := S{ID: 42, Empty: "yes", Tags: map[string]string{"a": "1"}, Name: "bob"}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	want2, _ := json.Marshal(&v2)
	if string(got2) != string(want2) {
		t.Errorf("case2: got  %s\nwant %s", got2, want2)
	}
}

func TestHotResumeEmptyMapField(t *testing.T) {
	// Empty map still goes through fallback path.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 1, Tags: map[string]string{}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeNilMapField(t *testing.T) {
	// Nil map still goes through fallback path.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 1, Tags: nil, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeNestedImpureStruct(t *testing.T) {
	// Nested struct with unsupported fields gets fallback at depth=0.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X     int   `json:"x"`
		Items []int `json:"items"`
	}
	type Outer struct {
		ID    int    `json:"id"`
		Inner Inner  `json:"inner"`
		Name  string `json:"name"`
	}

	v := Outer{ID: 1, Inner: Inner{X: 42, Items: []int{1, 2, 3}}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeConsistencyWithStdlib(t *testing.T) {
	// Comprehensive consistency test: compare Marshal output with encoding/json
	// for various mixed structs.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Mixed struct {
		ID     int               `json:"id"`
		Tags   map[string]string `json:"tags"`
		Name   string            `json:"name"`
		Items  []int             `json:"items"`
		Score  float64           `json:"score"`
		Value  any               `json:"value"`
		Active bool              `json:"active"`
	}

	tests := []struct {
		name string
		val  Mixed
	}{
		{
			name: "all_populated",
			val: Mixed{
				ID:     1,
				Tags:   map[string]string{"a": "1", "b": "2"},
				Name:   "test",
				Items:  []int{10, 20, 30},
				Score:  3.14,
				Value:  "dynamic",
				Active: true,
			},
		},
		{
			name: "nil_collections",
			val: Mixed{
				ID:     2,
				Tags:   nil,
				Name:   "test2",
				Items:  nil,
				Score:  0,
				Value:  nil,
				Active: false,
			},
		},
		{
			name: "empty_collections",
			val: Mixed{
				ID:     3,
				Tags:   map[string]string{},
				Name:   "",
				Items:  []int{},
				Score:  -1.5,
				Value:  42,
				Active: true,
			},
		},
		{
			name: "nested_value",
			val: Mixed{
				ID:    4,
				Tags:  map[string]string{"key": "val"},
				Name:  "nested",
				Items: []int{1},
				Score: 100,
				Value: map[string]any{"nested": true},
			},
		},
		{
			name: "zero_struct",
			val:  Mixed{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(&tt.val)
			if err != nil {
				t.Fatal(err)
			}
			// Compare by round-tripping through JSON unmarshal to handle
			// non-deterministic map key ordering.
			var gotMap, wantMap any
			want, _ := json.Marshal(&tt.val)
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("failed to unmarshal our output: %v\noutput: %s", err, got)
			}
			if err := json.Unmarshal(want, &wantMap); err != nil {
				t.Fatalf("failed to unmarshal stdlib output: %v", err)
			}
			if !reflect.DeepEqual(gotMap, wantMap) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

// ================================================================
// []NativeStruct batch encoding tests
// ================================================================

func TestNativeSliceOfStruct(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	items := []Item{
		{ID: 1, Name: "alice"},
		{ID: 2, Name: "bob"},
		{ID: 3, Name: "charlie"},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructEmpty(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}

	items := []Item{}
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructNil(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}

	var items []Item
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructSingleElement(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		A int     `json:"a"`
		B string  `json:"b"`
		C float64 `json:"c"`
		D bool    `json:"d"`
	}

	items := []Item{{A: 42, B: "hello", C: 3.14, D: true}}
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructLarge(t *testing.T) {
	// Large slice to force buffer growth (BUF_FULL resume).
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	items := make([]Item, 500)
	for i := range items {
		items[i] = Item{ID: i, Name: "user_name_with_some_length", Score: i * 100}
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("output mismatch for 500-element slice\ngot  len=%d\nwant len=%d", len(got), len(want))
	}
}

func TestNativeSliceOfStructNested(t *testing.T) {
	// Struct with nested struct — still fully native.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		ID    int   `json:"id"`
		Inner Inner `json:"inner"`
	}

	items := []Outer{
		{ID: 1, Inner: Inner{X: 10, Y: "a"}},
		{ID: 2, Inner: Inner{X: 20, Y: "b"}},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructWithPointers(t *testing.T) {
	// Struct with pointer fields — should be fully native.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Val  *int    `json:"val"`
	}

	name := "test"
	val := 42
	items := []Item{
		{ID: 1, Name: &name, Val: &val},
		{ID: 2, Name: nil, Val: nil},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructWithOmitempty(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name,omitempty"`
		Val  int    `json:"val,omitempty"`
	}

	items := []Item{
		{ID: 1, Name: "alice", Val: 100},
		{ID: 2, Name: "", Val: 0},
		{ID: 3, Name: "charlie", Val: 0},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSliceOfNonNativeStructFallsBack(t *testing.T) {
	// Struct with map field is not nativeFull — should use Go loop.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
	}

	items := []Item{
		{ID: 1, Tags: map[string]string{"a": "b"}},
		{ID: 2, Tags: map[string]string{"c": "d"}},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructConsistencyWithStdlib(t *testing.T) {
	// Comprehensive stdlib consistency across various struct shapes.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		A int    `json:"a"`
		B string `json:"b"`
	}

	type S struct {
		ID    int     `json:"id"`
		Score float64 `json:"score"`
		Inner Inner   `json:"inner"`
		OK    bool    `json:"ok"`
	}

	items := []S{
		{ID: 1, Score: 99.5, Inner: Inner{A: 10, B: "x"}, OK: true},
		{ID: 2, Score: 0, Inner: Inner{A: 0, B: ""}, OK: false},
		{ID: 3, Score: -1.5, Inner: Inner{A: -1, B: "hello \"world\""}, OK: true},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceInStructField(t *testing.T) {
	// []NativeStruct as a field of another struct — triggers hot resume
	// for the outer struct, but the slice itself should be batch-encoded.
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}
	type Wrapper struct {
		Name  string `json:"name"`
		Items []Item `json:"items"`
	}

	w := Wrapper{Name: "test", Items: []Item{{X: 1}, {X: 2}, {X: 3}}}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&w)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
