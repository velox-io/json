package tests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestNull_PrimitiveUnchanged verifies that null leaves primitive value types unchanged.
func TestNull_PrimitiveUnchanged(t *testing.T) {
	type S struct {
		Str   string
		Bool  bool
		Int   int
		Int8  int8
		Int64 int64
		Uint  uint
		F32   float32
		F64   float64
	}
	v := S{
		Str: "keep", Bool: true, Int: 42, Int8: 7, Int64: 99,
		Uint: 100, F32: 3.14, F64: 2.718,
	}
	input := `{"Str":null,"Bool":null,"Int":null,"Int8":null,"Int64":null,"Uint":null,"F32":null,"F64":null}`
	if err := vjson.Unmarshal([]byte(input), &v); err != nil {
		t.Fatal(err)
	}
	if v.Str != "keep" {
		t.Errorf("Str = %q, want \"keep\"", v.Str)
	}
	if v.Bool != true {
		t.Error("Bool = false, want true")
	}
	if v.Int != 42 {
		t.Errorf("Int = %d, want 42", v.Int)
	}
	if v.Int8 != 7 {
		t.Errorf("Int8 = %d, want 7", v.Int8)
	}
	if v.Int64 != 99 {
		t.Errorf("Int64 = %d, want 99", v.Int64)
	}
	if v.Uint != 100 {
		t.Errorf("Uint = %d, want 100", v.Uint)
	}
	if v.F32 != 3.14 {
		t.Errorf("F32 = %v, want 3.14", v.F32)
	}
	if v.F64 != 2.718 {
		t.Errorf("F64 = %v, want 2.718", v.F64)
	}
}

// TestNull_StructUnchanged verifies that null leaves a struct field unchanged.
func TestNull_StructUnchanged(t *testing.T) {
	type Inner struct{ X int }
	type Outer struct {
		A Inner
		B string
	}
	v := Outer{A: Inner{X: 42}, B: "before"}
	if err := vjson.Unmarshal([]byte(`{"A":null,"B":"after"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.X != 42 {
		t.Errorf("A.X = %d, want 42 (null should leave struct unchanged)", v.A.X)
	}
	if v.B != "after" {
		t.Errorf("B = %q, want \"after\"", v.B)
	}
}

// TestNull_PointerSetNil verifies that null sets pointer fields to nil.
func TestNull_PointerSetNil(t *testing.T) {
	n := 42
	s := "hello"
	type S struct {
		PI *int
		PS *string
	}
	v := S{PI: &n, PS: &s}
	if err := vjson.Unmarshal([]byte(`{"PI":null,"PS":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.PI != nil {
		t.Errorf("PI = %v, want nil", v.PI)
	}
	if v.PS != nil {
		t.Errorf("PS = %v, want nil", v.PS)
	}
}

// TestNull_SliceSetNil verifies that null sets slice fields to nil.
func TestNull_SliceSetNil(t *testing.T) {
	type S struct {
		A []int
		B []string
	}
	v := S{A: []int{1, 2, 3}, B: []string{"x"}}
	if err := vjson.Unmarshal([]byte(`{"A":null,"B":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != nil {
		t.Errorf("A = %v, want nil", v.A)
	}
	if v.B != nil {
		t.Errorf("B = %v, want nil", v.B)
	}
}

// TestNull_MapSetNil verifies that null sets map fields to nil.
func TestNull_MapSetNil(t *testing.T) {
	type S struct {
		M map[string]int
	}
	v := S{M: map[string]int{"k": 1}}
	if err := vjson.Unmarshal([]byte(`{"M":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.M != nil {
		t.Errorf("M = %v, want nil", v.M)
	}
}

// TestNull_InterfaceSetNil verifies that null sets interface{} fields to nil.
func TestNull_InterfaceSetNil(t *testing.T) {
	type S struct {
		V any
	}
	v := S{V: "something"}
	if err := vjson.Unmarshal([]byte(`{"V":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.V != nil {
		t.Errorf("V = %v, want nil", v.V)
	}
}

// TestNull_StdlibCompat verifies vjson matches encoding/json for all null cases.
func TestNull_StdlibCompat(t *testing.T) {
	type S struct {
		Str   string
		Int   int
		Bool  bool
		F64   float64
		PI    *int
		Slice []int
		Map   map[string]int
		Iface any
	}

	init := func() S {
		n := 42
		return S{
			Str: "keep", Int: 99, Bool: true, F64: 1.5,
			PI: &n, Slice: []int{1}, Map: map[string]int{"k": 1}, Iface: "x",
		}
	}

	input := `{"Str":null,"Int":null,"Bool":null,"F64":null,"PI":null,"Slice":null,"Map":null,"Iface":null}`

	stdV := init()
	stdErr := json.Unmarshal([]byte(input), &stdV)

	vjV := init()
	dec := vjson.NewDecoder(strings.NewReader(input))
	vjErr := dec.Decode(&vjV)

	if (stdErr == nil) != (vjErr == nil) {
		t.Fatalf("error mismatch: stdlib=%v vjson=%v", stdErr, vjErr)
	}

	// Primitives should be unchanged
	if stdV.Str != vjV.Str {
		t.Errorf("Str: stdlib=%q vjson=%q", stdV.Str, vjV.Str)
	}
	if stdV.Int != vjV.Int {
		t.Errorf("Int: stdlib=%d vjson=%d", stdV.Int, vjV.Int)
	}
	if stdV.Bool != vjV.Bool {
		t.Errorf("Bool: stdlib=%v vjson=%v", stdV.Bool, vjV.Bool)
	}
	if stdV.F64 != vjV.F64 {
		t.Errorf("F64: stdlib=%v vjson=%v", stdV.F64, vjV.F64)
	}

	// Pointer should be nil
	if (stdV.PI == nil) != (vjV.PI == nil) {
		t.Errorf("PI nil: stdlib=%v vjson=%v", stdV.PI == nil, vjV.PI == nil)
	}

	// Slice should be nil
	if (stdV.Slice == nil) != (vjV.Slice == nil) {
		t.Errorf("Slice nil: stdlib=%v vjson=%v", stdV.Slice == nil, vjV.Slice == nil)
	}

	// Map should be nil
	if (stdV.Map == nil) != (vjV.Map == nil) {
		t.Errorf("Map nil: stdlib=%v vjson=%v", stdV.Map == nil, vjV.Map == nil)
	}

	// Interface should be nil
	if (stdV.Iface == nil) != (vjV.Iface == nil) {
		t.Errorf("Iface nil: stdlib=%v vjson=%v", stdV.Iface == nil, vjV.Iface == nil)
	}
}

// TestNull_TopLevel verifies null handling for top-level values.
func TestNull_TopLevel(t *testing.T) {
	// Top-level pointer
	n := 42
	p := &n
	if err := vjson.Unmarshal([]byte(`null`), &p); err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Errorf("top-level *int = %v, want nil", p)
	}

	// Top-level value type: should be unchanged
	x := 99
	if err := vjson.Unmarshal([]byte(`null`), &x); err != nil {
		t.Fatal(err)
	}
	if x != 99 {
		t.Errorf("top-level int = %d, want 99 (null should leave unchanged)", x)
	}
}

// TestNull_TopLevel_StdlibCompat verifies top-level null matches stdlib.
func TestNull_TopLevel_StdlibCompat(t *testing.T) {
	// stdlib: null into *int → nil
	stdN := 42
	stdP := &stdN
	json.Unmarshal([]byte(`null`), &stdP)

	vjN := 42
	vjP := &vjN
	vjson.Unmarshal([]byte(`null`), &vjP)

	if (stdP == nil) != (vjP == nil) {
		t.Errorf("*int: stdlib nil=%v vjson nil=%v", stdP == nil, vjP == nil)
	}

	// stdlib: null into int → unchanged
	stdX := 99
	json.Unmarshal([]byte(`null`), &stdX)

	vjX := 99
	vjson.Unmarshal([]byte(`null`), &vjX)

	if stdX != vjX {
		t.Errorf("int: stdlib=%d vjson=%d", stdX, vjX)
	}
}

// TestNull_MixedWithValues verifies null fields don't affect non-null fields.
func TestNull_MixedWithValues(t *testing.T) {
	type S struct {
		A int
		B string
		C bool
		D float64
	}
	v := S{A: 1, B: "old", C: true, D: 1.0}
	input := `{"A":null,"B":"new","C":null,"D":3.14}`
	if err := vjson.Unmarshal([]byte(input), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 1 {
		t.Errorf("A = %d, want 1 (null → unchanged)", v.A)
	}
	if v.B != "new" {
		t.Errorf("B = %q, want \"new\"", v.B)
	}
	if v.C != true {
		t.Error("C = false, want true (null → unchanged)")
	}
	if v.D != 3.14 {
		t.Errorf("D = %v, want 3.14", v.D)
	}
}

// TestNull_ErrorBridging verifies no error is returned for null on any type.
func TestNull_NoError(t *testing.T) {
	type S struct {
		Str   string
		Int   int
		Bool  bool
		F64   float64
		PI    *int
		Slice []int
		Map   map[string]int
		Iface any
	}
	input := `{"Str":null,"Int":null,"Bool":null,"F64":null,"PI":null,"Slice":null,"Map":null,"Iface":null}`
	var v S
	err := vjson.Unmarshal([]byte(input), &v)
	if err != nil {
		var ute *vjson.UnmarshalTypeError
		if errors.As(err, &ute) {
			t.Fatalf("null should not produce UnmarshalTypeError, got: %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}
