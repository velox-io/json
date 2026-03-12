package pjson

import (
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
)

// =============================================================================
// Basic Type Tests
// =============================================================================

func TestParse_SimpleBool(t *testing.T) {
	type S struct {
		A bool `json:"a"`
	}
	var s S
	if err := Unmarshal([]byte(`{"a": true}`), &s); err != nil {
		t.Fatal(err)
	}
	if !s.A {
		t.Errorf("expected true, got %v", s.A)
	}
}

func TestParse_BoolFalse(t *testing.T) {
	type S struct {
		A bool `json:"a"`
	}
	var s S
	if err := Unmarshal([]byte(`{"a": false}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.A {
		t.Errorf("expected false, got %v", s.A)
	}
}

func TestParse_SimpleString(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	if err := Unmarshal([]byte(`{"name": "hello"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "hello" {
		t.Errorf("expected %q, got %q", "hello", s.Name)
	}
}

func TestParse_SimpleInt(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`{"x": 42}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != 42 {
		t.Errorf("expected 42, got %d", s.X)
	}
}

func TestParse_NegativeInt(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`{"x": -123}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != -123 {
		t.Errorf("expected -123, got %d", s.X)
	}
}

func TestParse_Float64(t *testing.T) {
	type S struct {
		F float64 `json:"f"`
	}
	var s S
	if err := Unmarshal([]byte(`{"f": 3.14}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.F < 3.13 || s.F > 3.15 {
		t.Errorf("expected ~3.14, got %f", s.F)
	}
}

func TestParse_NullString(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	s := S{Name: "original"}
	if err := Unmarshal([]byte(`{"name": null}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "" {
		t.Errorf("expected empty string after null, got %q", s.Name)
	}
}

func TestParse_MultipleFields(t *testing.T) {
	type S struct {
		Name string  `json:"name"`
		Age  int     `json:"age"`
		OK   bool    `json:"ok"`
		Rate float64 `json:"rate"`
	}
	var s S
	if err := Unmarshal([]byte(`{"name": "alice", "age": 30, "ok": true, "rate": 0.95}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "alice" {
		t.Errorf("Name: expected %q, got %q", "alice", s.Name)
	}
	if s.Age != 30 {
		t.Errorf("Age: expected 30, got %d", s.Age)
	}
	if !s.OK {
		t.Errorf("OK: expected true, got false")
	}
	if s.Rate < 0.94 || s.Rate > 0.96 {
		t.Errorf("Rate: expected ~0.95, got %f", s.Rate)
	}
}

func TestParse_EmptyObject(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`{}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != 0 {
		t.Errorf("expected 0, got %d", s.X)
	}
}

func TestParse_UnknownField(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`{"x": 1, "unknown": "skip_me", "y": 2}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != 1 {
		t.Errorf("expected 1, got %d", s.X)
	}
}

func TestParse_UnknownFieldWithNestedObject(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`{"unknown": {"a": 1, "b": [1,2,3]}, "x": 42}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != 42 {
		t.Errorf("expected 42, got %d", s.X)
	}
}

// =============================================================================
// Integer Variants
// =============================================================================

func TestParse_IntVariants(t *testing.T) {
	type S struct {
		I8  int8   `json:"i8"`
		I16 int16  `json:"i16"`
		I32 int32  `json:"i32"`
		I64 int64  `json:"i64"`
		U8  uint8  `json:"u8"`
		U16 uint16 `json:"u16"`
		U32 uint32 `json:"u32"`
		U64 uint64 `json:"u64"`
	}
	var s S
	data := []byte(`{"i8": 127, "i16": 32000, "i32": 100000, "i64": 9999999999, "u8": 255, "u16": 65000, "u32": 4000000000, "u64": 18446744073709551615}`)
	if err := Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.I8 != 127 {
		t.Errorf("I8: expected 127, got %d", s.I8)
	}
	if s.I16 != 32000 {
		t.Errorf("I16: expected 32000, got %d", s.I16)
	}
	if s.I32 != 100000 {
		t.Errorf("I32: expected 100000, got %d", s.I32)
	}
	if s.I64 != 9999999999 {
		t.Errorf("I64: expected 9999999999, got %d", s.I64)
	}
	if s.U8 != 255 {
		t.Errorf("U8: expected 255, got %d", s.U8)
	}
	if s.U16 != 65000 {
		t.Errorf("U16: expected 65000, got %d", s.U16)
	}
	if s.U32 != 4000000000 {
		t.Errorf("U32: expected 4000000000, got %d", s.U32)
	}
	if s.U64 != 18446744073709551615 {
		t.Errorf("U64: expected max uint64, got %d", s.U64)
	}
}

// =============================================================================
// Nested Struct
// =============================================================================

func TestParse_NestedStruct(t *testing.T) {
	type Inner struct {
		V int `json:"v"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
	}
	var s Outer
	if err := Unmarshal([]byte(`{"name": "test", "inner": {"v": 7}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "test" {
		t.Errorf("Name: expected %q, got %q", "test", s.Name)
	}
	if s.Inner.V != 7 {
		t.Errorf("Inner.V: expected 7, got %d", s.Inner.V)
	}
}

func TestParse_DeeplyNested(t *testing.T) {
	type C struct {
		Z bool `json:"z"`
	}
	type B struct {
		C C `json:"c"`
	}
	type A struct {
		B B `json:"b"`
	}
	var a A
	if err := Unmarshal([]byte(`{"b": {"c": {"z": true}}}`), &a); err != nil {
		t.Fatal(err)
	}
	if !a.B.C.Z {
		t.Errorf("expected true, got false")
	}
}

// =============================================================================
// Slice (Array)
// =============================================================================

func TestParse_IntSlice(t *testing.T) {
	type S struct {
		Items []int `json:"items"`
	}
	var s S
	if err := Unmarshal([]byte(`{"items": [1, 2, 3]}`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.Items))
	}
	expected := []int{1, 2, 3}
	for i, v := range expected {
		if s.Items[i] != v {
			t.Errorf("items[%d]: expected %d, got %d", i, v, s.Items[i])
		}
	}
}

func TestParse_StringSlice(t *testing.T) {
	type S struct {
		Tags []string `json:"tags"`
	}
	var s S
	if err := Unmarshal([]byte(`{"tags": ["a", "b", "c"]}`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(s.Tags))
	}
	expected := []string{"a", "b", "c"}
	for i, v := range expected {
		if s.Tags[i] != v {
			t.Errorf("tags[%d]: expected %q, got %q", i, v, s.Tags[i])
		}
	}
}

func TestParse_EmptyArray(t *testing.T) {
	type S struct {
		Items []int `json:"items"`
	}
	var s S
	if err := Unmarshal([]byte(`{"items": []}`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(s.Items))
	}
}

// =============================================================================
// Pointer Fields
// =============================================================================

func TestParse_PointerField_NonNull(t *testing.T) {
	type Inner struct {
		Name string `json:"name"`
	}
	type S struct {
		Inner *Inner `json:"inner"`
	}
	var s S
	if err := Unmarshal([]byte(`{"inner": {"name": "x"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Inner == nil {
		t.Fatal("expected non-nil pointer")
	}
	if s.Inner.Name != "x" {
		t.Errorf("expected %q, got %q", "x", s.Inner.Name)
	}
}

func TestParse_PointerField_Null(t *testing.T) {
	type Inner struct {
		Name string `json:"name"`
	}
	type S struct {
		Inner *Inner `json:"inner"`
	}
	s := S{Inner: &Inner{Name: "old"}}
	if err := Unmarshal([]byte(`{"inner": null}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Inner != nil {
		t.Errorf("expected nil pointer, got %+v", s.Inner)
	}
}

// =============================================================================
// Map Fields
// =============================================================================

func TestParse_MapStringString(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	if err := Unmarshal([]byte(`{"m": {"a": "1", "b": "2"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.M) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(s.M))
	}
	if s.M["a"] != "1" || s.M["b"] != "2" {
		t.Errorf("unexpected map: %v", s.M)
	}
}

func TestParse_MapStringInt(t *testing.T) {
	type S struct {
		M map[string]int `json:"m"`
	}
	var s S
	if err := Unmarshal([]byte(`{"m": {"x": 1, "y": 2}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.M["x"] != 1 || s.M["y"] != 2 {
		t.Errorf("unexpected map: %v", s.M)
	}
}

// =============================================================================
// any (interface{}) Fields
// =============================================================================

func TestParse_AnyField_String(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	if err := Unmarshal([]byte(`{"v": "hello"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.V != "hello" {
		t.Errorf("expected %q, got %v", "hello", s.V)
	}
}

func TestParse_AnyField_Number(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	if err := Unmarshal([]byte(`{"v": 42}`), &s); err != nil {
		t.Fatal(err)
	}
	f, ok := s.V.(float64)
	if !ok || f != 42 {
		t.Errorf("expected float64(42), got %T(%v)", s.V, s.V)
	}
}

func TestParse_AnyField_Bool(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	if err := Unmarshal([]byte(`{"v": true}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.V != true {
		t.Errorf("expected true, got %v", s.V)
	}
}

func TestParse_AnyField_Null(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	s := S{V: "something"}
	if err := Unmarshal([]byte(`{"v": null}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.V != nil {
		t.Errorf("expected nil, got %v", s.V)
	}
}

func TestParse_AnyField_Object(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	if err := Unmarshal([]byte(`{"v": {"a": 1, "b": "two"}}`), &s); err != nil {
		t.Fatal(err)
	}
	m, ok := s.V.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", s.V)
	}
	if m["a"] != float64(1) {
		t.Errorf("m[a]: expected 1, got %v", m["a"])
	}
	if m["b"] != "two" {
		t.Errorf("m[b]: expected %q, got %v", "two", m["b"])
	}
}

func TestParse_AnyField_Array(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	if err := Unmarshal([]byte(`{"v": [1, "two", true, null]}`), &s); err != nil {
		t.Fatal(err)
	}
	arr, ok := s.V.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", s.V)
	}
	if len(arr) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(arr))
	}
	if arr[0] != float64(1) {
		t.Errorf("[0]: expected 1, got %v", arr[0])
	}
	if arr[1] != "two" {
		t.Errorf("[1]: expected %q, got %v", "two", arr[1])
	}
	if arr[2] != true {
		t.Errorf("[2]: expected true, got %v", arr[2])
	}
	if arr[3] != nil {
		t.Errorf("[3]: expected nil, got %v", arr[3])
	}
}

func TestParse_AnyField_NestedComplex(t *testing.T) {
	type S struct {
		V any `json:"v"`
	}
	var s S
	data := []byte(`{"v": {"users": [{"name": "Alice", "age": 30}, {"name": "Bob", "age": 25}], "total": 2}}`)
	if err := Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}

	m, ok := s.V.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", s.V)
	}
	users, ok := m["users"].([]any)
	if !ok {
		t.Fatalf("users: expected []any, got %T", m["users"])
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	u0, ok := users[0].(map[string]any)
	if !ok {
		t.Fatalf("users[0]: expected map, got %T", users[0])
	}
	if u0["name"] != "Alice" {
		t.Errorf("users[0].name: expected Alice, got %v", u0["name"])
	}
	if m["total"] != float64(2) {
		t.Errorf("total: expected 2, got %v", m["total"])
	}
}

func TestParse_MapStringAny(t *testing.T) {
	type S struct {
		M map[string]any `json:"m"`
	}
	var s S
	if err := Unmarshal([]byte(`{"m": {"a": 1, "b": "two", "c": true, "d": null}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.M["a"] != float64(1) {
		t.Errorf("m[a]: expected 1, got %v", s.M["a"])
	}
	if s.M["b"] != "two" {
		t.Errorf("m[b]: expected %q, got %v", "two", s.M["b"])
	}
	if s.M["c"] != true {
		t.Errorf("m[c]: expected true, got %v", s.M["c"])
	}
	if s.M["d"] != nil {
		t.Errorf("m[d]: expected nil, got %v", s.M["d"])
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestParse_NotPointer(t *testing.T) {
	type S struct{}
	var s S
	err := Unmarshal([]byte(`{}`), &s)
	// Unmarshal[T] always takes *T, so this won't trigger the non-pointer error.
	// Test via Parse directly.
	p := NewParser(DefaultScanner)
	err = p.Parse([]byte(`{}`), s)
	if err == nil {
		t.Fatal("expected error for non-pointer")
	}
}

func TestParse_NilPointer(t *testing.T) {
	p := NewParser(DefaultScanner)
	err := p.Parse([]byte(`{}`), (*struct{})(nil))
	if err == nil {
		t.Fatal("expected error for nil pointer")
	}
}

// =============================================================================
// Whitespace Variations
// =============================================================================

func TestParse_ExtraWhitespace(t *testing.T) {
	type S struct {
		X int `json:"x"`
	}
	var s S
	if err := Unmarshal([]byte(`  {  "x"  :  42  }  `), &s); err != nil {
		t.Fatal(err)
	}
	if s.X != 42 {
		t.Errorf("expected 42, got %d", s.X)
	}
}

// =============================================================================
// Case-Insensitive Field Matching
// =============================================================================

func TestParse_CaseInsensitiveField(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	if err := Unmarshal([]byte(`{"Name": "hello"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "hello" {
		t.Errorf("expected %q, got %q", "hello", s.Name)
	}
}

// =============================================================================
// Escaped Strings
// =============================================================================

func TestParse_EscapedString(t *testing.T) {
	type S struct {
		Msg string `json:"msg"`
	}
	var s S
	if err := Unmarshal([]byte(`{"msg": "hello\nworld"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Msg != "hello\nworld" {
		t.Errorf("expected %q, got %q", "hello\nworld", s.Msg)
	}
}

func TestParse_EscapedQuote(t *testing.T) {
	type S struct {
		Msg string `json:"msg"`
	}
	var s S
	if err := Unmarshal([]byte(`{"msg": "say \"hi\""}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Msg != `say "hi"` {
		t.Errorf("expected %q, got %q", `say "hi"`, s.Msg)
	}
}

// =============================================================================
// GC Stress
// =============================================================================

func TestGCStress_PointersAndSlices(t *testing.T) {
	type Inner struct {
		Name string `json:"name"`
	}
	type Item struct {
		Value string `json:"value"`
		Inner *Inner `json:"inner"`
	}
	type Root struct {
		Items []Item `json:"items"`
	}

	data := []byte(`{"items":[{"value":"a","inner":{"name":"x"}},{"value":"b","inner":{"name":"y"}},{"value":"c","inner":null},{"value":"d","inner":{"name":"z"}}]}`)

	for i := 0; i < 10000; i++ {
		var r Root
		if err := Unmarshal(data, &r); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if len(r.Items) != 4 {
			t.Fatalf("iteration %d: expected 4 items, got %d", i, len(r.Items))
		}
		if r.Items[0].Inner == nil || r.Items[0].Inner.Name != "x" {
			t.Fatalf("iteration %d: items[0].inner wrong", i)
		}
		if r.Items[2].Inner != nil {
			t.Fatalf("iteration %d: items[2].inner should be nil", i)
		}
		if i%1000 == 0 {
			runtime.GC()
		}
	}
}

// =============================================================================
// Comparison with encoding/json
// =============================================================================

func TestParse_MatchesStdlib(t *testing.T) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type S struct {
		Name   string         `json:"name"`
		Age    int            `json:"age"`
		Score  float64        `json:"score"`
		Active bool           `json:"active"`
		Inner  Inner          `json:"inner"`
		Tags   []string       `json:"tags"`
		Extra  map[string]int `json:"extra"`
		Ptr    *Inner         `json:"ptr"`
	}

	data := []byte(`{
		"name": "test",
		"age": 25,
		"score": 99.5,
		"active": true,
		"inner": {"x": 1, "y": "hello"},
		"tags": ["a", "b"],
		"extra": {"k1": 10, "k2": 20},
		"ptr": {"x": 3, "y": "world"}
	}`)

	var got S
	if err := Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	var want S
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkParse_SmallObject(b *testing.B) {
	type S struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
		OK   bool   `json:"ok"`
	}
	data := []byte(`{"name": "alice", "age": 30, "ok": true}`)
	p := NewParser(DefaultScanner)
	var s S

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s = S{}
		if err := p.Parse(data, &s); err != nil {
			b.Fatal(err)
		}
	}
	_ = s
}

func BenchmarkParse_MediumObject(b *testing.B) {
	type S struct {
		A string  `json:"a"`
		B string  `json:"b"`
		C int     `json:"c"`
		D int     `json:"d"`
		E float64 `json:"e"`
		F bool    `json:"f"`
		G string  `json:"g"`
		H int64   `json:"h"`
	}
	data := []byte(`{"a":"hello","b":"world","c":42,"d":99,"e":3.14,"f":true,"g":"test","h":1234567890}`)
	p := NewParser(DefaultScanner)
	var s S

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s = S{}
		if err := p.Parse(data, &s); err != nil {
			b.Fatal(err)
		}
	}
	_ = s
}

func BenchmarkStdlib_SmallObject(b *testing.B) {
	type S struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
		OK   bool   `json:"ok"`
	}
	data := []byte(`{"name": "alice", "age": 30, "ok": true}`)
	var s S

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s = S{}
		if err := json.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
	_ = s
}

func BenchmarkStdlib_MediumObject(b *testing.B) {
	type S struct {
		A string  `json:"a"`
		B string  `json:"b"`
		C int     `json:"c"`
		D int     `json:"d"`
		E float64 `json:"e"`
		F bool    `json:"f"`
		G string  `json:"g"`
		H int64   `json:"h"`
	}
	data := []byte(`{"a":"hello","b":"world","c":42,"d":99,"e":3.14,"f":true,"g":"test","h":1234567890}`)
	var s S

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s = S{}
		if err := json.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
	_ = s
}
