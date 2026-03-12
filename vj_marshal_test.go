package vjson

import (
	"encoding/json"
	"testing"
)

// --- Test types ---

type MarshalSmall struct {
	Name  string `json:"name"`
	Age   int    `json:"age"`
	Score float64
}

type MarshalOmit struct {
	Name  string   `json:"name,omitempty"`
	Age   int      `json:"age,omitempty"`
	Score float64  `json:"score,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Ptr   *int     `json:"ptr,omitempty"`
}

type MarshalNested struct {
	ID    int           `json:"id"`
	Inner MarshalSmall  `json:"inner"`
	Ptr   *MarshalSmall `json:"ptr"`
	Items []int         `json:"items"`
}

type MarshalSkip struct {
	Name   string `json:"name"`
	Secret string `json:"-"`
	Value  int    `json:"value"`
}

// --- Basic struct ---

func TestMarshal_SmallStruct(t *testing.T) {
	v := MarshalSmall{Name: "Alice", Age: 30, Score: 9.5}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}

	// Verify round-trip
	var check MarshalSmall
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("encoding/json cannot parse output: %v\nJSON: %s", err, got)
	}
	if check.Name != v.Name || check.Age != v.Age || check.Score != v.Score {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", check, v)
	}
}

// --- omitempty ---

func TestMarshal_OmitEmpty(t *testing.T) {
	v := MarshalOmit{} // all zero
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := "{}"
	if string(got) != want {
		t.Fatalf("omitempty zero: got %s, want %s", got, want)
	}

	v2 := MarshalOmit{Name: "Bob", Age: 0, Score: 1.5}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	// Age=0 should be omitted
	var check map[string]any
	if err := json.Unmarshal(got2, &check); err != nil {
		t.Fatalf("parse error: %v\nJSON: %s", err, got2)
	}
	if _, ok := check["age"]; ok {
		t.Fatalf("omitempty: age=0 should be omitted, got %s", got2)
	}
	if check["name"] != "Bob" {
		t.Fatalf("omitempty: name mismatch, got %s", got2)
	}
}

func TestMarshal_OmitEmpty_EmptySlice(t *testing.T) {
	type S struct {
		NilSlice   []int `json:"nil_slice,omitempty"`
		EmptySlice []int `json:"empty_slice,omitempty"`
		HasData    []int `json:"has_data,omitempty"`
	}
	v := S{NilSlice: nil, EmptySlice: []int{}, HasData: []int{1}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, stdGot)
	}
	// Both nil and empty should be omitted, only has_data remains
	want := `{"has_data":[1]}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestMarshal_OmitEmpty_EmptyByteSlice(t *testing.T) {
	type S struct {
		Nil   []byte `json:"nil,omitempty"`
		Empty []byte `json:"empty,omitempty"`
		Data  []byte `json:"data,omitempty"`
	}
	v := S{Nil: nil, Empty: []byte{}, Data: []byte("hi")}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, stdGot)
	}
}

func TestMarshal_OmitEmpty_EmptyMap(t *testing.T) {
	type S struct {
		NilMap   map[string]int `json:"nil_map,omitempty"`
		EmptyMap map[string]int `json:"empty_map,omitempty"`
		HasData  map[string]int `json:"has_data,omitempty"`
	}
	v := S{NilMap: nil, EmptyMap: map[string]int{}, HasData: map[string]int{"k": 1}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, stdGot)
	}
	// Both nil and empty should be omitted
	want := `{"has_data":{"k":1}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestMarshal_OmitEmpty_StdlibCompat(t *testing.T) {
	type S struct {
		Str   string         `json:"str,omitempty"`
		Int   int            `json:"int,omitempty"`
		Bool  bool           `json:"bool,omitempty"`
		F64   float64        `json:"f64,omitempty"`
		Ptr   *int           `json:"ptr,omitempty"`
		Slice []int          `json:"slice,omitempty"`
		Map   map[string]int `json:"map,omitempty"`
		Bytes []byte         `json:"bytes,omitempty"`
	}
	// All zero/empty/nil
	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("all zero:\n  vjson:  %s\n  stdlib: %s", got, stdGot)
	}

	// Empty non-nil containers
	v2 := S{Slice: []int{}, Map: map[string]int{}, Bytes: []byte{}}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	stdGot2, _ := json.Marshal(v2)
	if string(got2) != string(stdGot2) {
		t.Errorf("empty containers:\n  vjson:  %s\n  stdlib: %s", got2, stdGot2)
	}
}

// --- json:"-" ---

func TestMarshal_SkipField(t *testing.T) {
	v := MarshalSkip{Name: "test", Secret: "hidden", Value: 42}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]any
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatal(err)
	}
	if _, ok := check["Secret"]; ok {
		t.Fatalf("json:\"-\" field should be skipped, got %s", got)
	}
	if _, ok := check["-"]; ok {
		t.Fatalf("json:\"-\" field should be skipped, got %s", got)
	}
}

// --- Nested struct + pointer ---

func TestMarshal_Nested(t *testing.T) {
	inner := MarshalSmall{Name: "inner", Age: 5, Score: 1.0}
	v := MarshalNested{
		ID:    1,
		Inner: inner,
		Ptr:   &inner,
		Items: []int{10, 20, 30},
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}

	var check MarshalNested
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if check.ID != 1 || check.Inner.Name != "inner" || check.Ptr == nil || check.Ptr.Age != 5 {
		t.Fatalf("nested mismatch: %+v", check)
	}
	if len(check.Items) != 3 || check.Items[2] != 30 {
		t.Fatalf("items mismatch: %+v", check.Items)
	}
}

// --- Nil pointer ---

func TestMarshal_NilPointer(t *testing.T) {
	v := MarshalNested{ID: 1, Ptr: nil, Items: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]any
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatal(err)
	}
	if check["ptr"] != nil {
		t.Fatalf("nil pointer should marshal to null, got %s", got)
	}
}

// --- Map ---

func TestMarshal_MapStringString(t *testing.T) {
	v := map[string]string{"key1": "value1", "key2": "value2"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]string
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if check["key1"] != "value1" || check["key2"] != "value2" {
		t.Fatalf("map mismatch: %+v", check)
	}
}

func TestMarshal_MapStringInt(t *testing.T) {
	v := map[string]int{"a": 1, "b": 2}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]int
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if check["a"] != 1 || check["b"] != 2 {
		t.Fatalf("map mismatch: %+v", check)
	}
}

// --- String escaping ---

func TestMarshal_StringEscape(t *testing.T) {
	v := struct {
		S string `json:"s"`
	}{S: "hello \"world\"\nnewline\ttab\\back"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check struct {
		S string `json:"s"`
	}
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if check.S != v.S {
		t.Fatalf("escape round-trip: got %q, want %q", check.S, v.S)
	}
}

// --- Bool ---

func TestMarshal_Bool(t *testing.T) {
	v := struct {
		A bool `json:"a"`
		B bool `json:"b"`
	}{A: true, B: false}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":true,"b":false}`
	if string(got) != want {
		t.Fatalf("bool: got %s, want %s", got, want)
	}
}

// --- Empty slice ---

func TestMarshal_EmptySlice(t *testing.T) {
	v := struct {
		Items []int `json:"items"`
	}{Items: []int{}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[]}`
	if string(got) != want {
		t.Fatalf("empty slice: got %s, want %s", got, want)
	}
}

// --- MarshalIndent ---

func TestMarshalIndent_Basic(t *testing.T) {
	v := MarshalSmall{Name: "Alice", Age: 30, Score: 9.5}
	got, err := MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var check MarshalSmall
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("indent parse failed: %v\nJSON:\n%s", err, got)
	}
	if check.Name != v.Name {
		t.Fatalf("indent round-trip mismatch")
	}
	found := false
	for _, b := range got {
		if b == '\n' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MarshalIndent should produce newlines, got: %s", got)
	}
}

// --- AppendMarshal ---

func TestAppendMarshal(t *testing.T) {
	v := MarshalSmall{Name: "test", Age: 1, Score: 0.5}
	prefix := []byte("PREFIX:")
	got, err := AppendMarshal(prefix, &v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:7]) != "PREFIX:" {
		t.Fatalf("prefix lost: got %s", got)
	}
	var check MarshalSmall
	if err := json.Unmarshal(got[7:], &check); err != nil {
		t.Fatalf("round-trip failed: %v", err)
	}
}

// --- interface{} ---

func TestMarshal_AnyInterface(t *testing.T) {
	v := struct {
		Data any `json:"data"`
	}{Data: map[string]any{
		"name": "test",
		"num":  float64(42),
		"ok":   true,
		"arr":  []any{float64(1), "two", nil},
	}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]any
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("parse failed: %v\nJSON: %s", err, got)
	}
	data := check["data"].(map[string]any)
	if data["name"] != "test" {
		t.Fatalf("any interface mismatch: %+v", data)
	}
}

// --- Byte slice (base64) ---

func TestMarshal_ByteSlice(t *testing.T) {
	v := struct {
		Data []byte `json:"data"`
	}{Data: []byte("hello world")}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check struct {
		Data []byte `json:"data"`
	}
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if string(check.Data) != "hello world" {
		t.Fatalf("byte slice mismatch: got %q", check.Data)
	}
}

func TestMarshal_ByteSlice_Nil(t *testing.T) {
	type S struct {
		Data []byte `json:"data"`
	}
	v := S{Data: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("nil []byte: vjson=%s stdlib=%s", got, stdGot)
	}
	// Should be null
	if string(got) != `{"data":null}` {
		t.Errorf("nil []byte: got %s, want {\"data\":null}", got)
	}
}

func TestMarshal_ByteSlice_Empty(t *testing.T) {
	type S struct {
		Data []byte `json:"data"`
	}
	v := S{Data: []byte{}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("empty []byte: vjson=%s stdlib=%s", got, stdGot)
	}
	// Should be ""
	if string(got) != `{"data":""}` {
		t.Errorf("empty []byte: got %s, want {\"data\":\"\"}", got)
	}
}

func TestMarshal_ByteSlice_NilVsEmpty_StdlibCompat(t *testing.T) {
	type S struct {
		Nil   []byte `json:"nil_field"`
		Empty []byte `json:"empty_field"`
		Data  []byte `json:"data_field"`
	}
	v := S{Nil: nil, Empty: []byte{}, Data: []byte("abc")}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("mismatch:\n  vjson:  %s\n  stdlib: %s", got, stdGot)
	}
}

func TestMarshal_NonByteSlice_Nil(t *testing.T) {
	// All nil slices → null (matches stdlib)
	type S struct {
		Ints []int    `json:"ints"`
		Strs []string `json:"strs"`
	}
	v := S{Ints: nil, Strs: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("nil slices: vjson=%s stdlib=%s", got, stdGot)
	}
}

func TestMarshal_NonByteSlice_Empty(t *testing.T) {
	// Non-nil empty slices → [] (matches stdlib)
	type S struct {
		Ints []int    `json:"ints"`
		Strs []string `json:"strs"`
	}
	v := S{Ints: []int{}, Strs: []string{}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	stdGot, _ := json.Marshal(v)
	if string(got) != string(stdGot) {
		t.Errorf("empty slices: vjson=%s stdlib=%s", got, stdGot)
	}
}

// --- Nil map ---

func TestMarshal_NilMap(t *testing.T) {
	v := struct {
		M map[string]string `json:"m"`
	}{M: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"m":null}`
	if string(got) != want {
		t.Fatalf("nil map: got %s, want %s", got, want)
	}
}

// --- Various integer types ---

func TestMarshal_IntTypes(t *testing.T) {
	type Ints struct {
		I   int    `json:"i"`
		I8  int8   `json:"i8"`
		I16 int16  `json:"i16"`
		I32 int32  `json:"i32"`
		I64 int64  `json:"i64"`
		U   uint   `json:"u"`
		U8  uint8  `json:"u8"`
		U16 uint16 `json:"u16"`
		U32 uint32 `json:"u32"`
		U64 uint64 `json:"u64"`
	}
	v := Ints{I: -1, I8: -8, I16: -16, I32: -32, I64: -64, U: 1, U8: 8, U16: 16, U32: 32, U64: 64}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	var check Ints
	if err := json.Unmarshal(got, &check); err != nil {
		t.Fatalf("round-trip failed: %v\nJSON: %s", err, got)
	}
	if check != v {
		t.Fatalf("int types mismatch: got %+v, want %+v", check, v)
	}
}

// --- Large integer (beyond small cache) ---

func TestMarshal_LargeInt(t *testing.T) {
	v := struct {
		N int64 `json:"n"`
	}{N: 1234567890}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"n":1234567890}`
	if string(got) != want {
		t.Fatalf("large int: got %s, want %s", got, want)
	}
}

// TestAppendMarshal_BufferRetainedInPool verifies that AppendMarshal does not
// let the pool retain (and later overwrite) the caller's buffer.
//
// Bug scenario:
//  1. Caller passes dst with enough capacity → AppendMarshal sets m.buf = dst.
//  2. putMarshaler keeps m.buf in the pool (cap ≤ marshalBufPoolLimit).
//  3. Next getMarshaler reuses that backing array → m.buf = m.buf[:0].
//  4. A subsequent Marshal/AppendMarshal writes into the same backing array,
//     corrupting the data the caller is still holding.
func TestAppendMarshal_BufferRetainedInPool(t *testing.T) {
	type small struct {
		X int `json:"x"`
	}

	// Pre-allocate dst with plenty of capacity so AppendMarshal won't
	// reallocate — the returned slice shares dst's backing array.
	prefix := []byte(`prefix:`)
	dst := make([]byte, len(prefix), 512)
	copy(dst, prefix)

	v1 := small{X: 1}
	result1, err := AppendMarshal(dst, &v1)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := string(result1) // pin the expected content
	want1 := `prefix:{"x":1}`
	if snapshot != want1 {
		t.Fatalf("first AppendMarshal: got %q, want %q", snapshot, want1)
	}

	// Now do a second marshal. If the pool still holds our backing array,
	// this call will scribble over result1's content.
	v2 := small{X: 999}
	_, err = Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}

	// Check that result1 is still intact.
	if got := string(result1); got != snapshot {
		t.Fatalf("AppendMarshal buffer was corrupted by subsequent Marshal!\n"+
			"  before: %q\n"+
			"  after:  %q\n"+
			"The pool retained the caller's buffer.", snapshot, got)
	}
}
