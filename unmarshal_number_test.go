package vjson

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- Struct field: explicit json.Number ---

func TestNumber_StructField_Integer(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":12345}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "12345" {
		t.Fatalf("N = %q, want %q", msg.N, "12345")
	}
}

func TestNumber_StructField_Float(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":3.14159}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "3.14159" {
		t.Fatalf("N = %q, want %q", msg.N, "3.14159")
	}
}

func TestNumber_StructField_Exponent(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":1.5e10}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "1.5e10" {
		t.Fatalf("N = %q, want %q", msg.N, "1.5e10")
	}
}

func TestNumber_StructField_Negative(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":-42}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "-42" {
		t.Fatalf("N = %q, want %q", msg.N, "-42")
	}
}

func TestNumber_StructField_Quoted(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	// JSON string containing a number
	input := []byte(`{"n":"99.9"}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "99.9" {
		t.Fatalf("N = %q, want %q", msg.N, "99.9")
	}
}

func TestNumber_StructField_Null(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":null}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "" {
		t.Fatalf("N = %q, want empty string", msg.N)
	}
}

func TestNumber_StructField_Mixed(t *testing.T) {
	type Msg struct {
		Name  string      `json:"name"`
		Count json.Number `json:"count"`
		Score float64     `json:"score"`
	}
	input := []byte(`{"name":"test","count":9007199254740993,"score":1.5}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Name != "test" {
		t.Fatalf("Name = %q", msg.Name)
	}
	if string(msg.Count) != "9007199254740993" {
		t.Fatalf("Count = %q, want %q", msg.Count, "9007199254740993")
	}
	if msg.Score != 1.5 {
		t.Fatalf("Score = %f", msg.Score)
	}
}

// --- UseNumber + Unmarshal: interface{} gets json.Number ---

func TestNumber_UseNumber_Unmarshal(t *testing.T) {
	input := []byte(`{"n":42,"f":3.14,"big":9007199254740993}`)
	var result map[string]any
	if err := Unmarshal(input, &result, WithUseNumber()); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"n":   "42",
		"f":   "3.14",
		"big": "9007199254740993",
	} {
		v, ok := result[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
		n, ok := v.(json.Number)
		if !ok {
			t.Fatalf("result[%q] is %T, want json.Number", key, v)
		}
		if string(n) != want {
			t.Fatalf("result[%q] = %q, want %q", key, n, want)
		}
	}
}

func TestNumber_WithoutUseNumber_Unmarshal(t *testing.T) {
	// Without UseNumber, numbers should still be float64 (no regression)
	input := []byte(`{"n":42}`)
	var result map[string]any
	if err := Unmarshal(input, &result); err != nil {
		t.Fatal(err)
	}
	v := result["n"]
	if _, ok := v.(float64); !ok {
		t.Fatalf("result[\"n\"] is %T, want float64", v)
	}
}

func TestNumber_UseNumber_NestedInterface(t *testing.T) {
	input := []byte(`{"outer":{"inner":123456789012345678}}`)
	var result map[string]any
	if err := Unmarshal(input, &result, WithUseNumber()); err != nil {
		t.Fatal(err)
	}
	outer, ok := result["outer"].(map[string]any)
	if !ok {
		t.Fatalf("outer is %T", result["outer"])
	}
	inner, ok := outer["inner"].(json.Number)
	if !ok {
		t.Fatalf("inner is %T", outer["inner"])
	}
	if string(inner) != "123456789012345678" {
		t.Fatalf("inner = %q", inner)
	}
}

func TestNumber_UseNumber_ArrayInterface(t *testing.T) {
	input := []byte(`[1, 2.5, 9007199254740993]`)
	var result []any
	if err := Unmarshal(input, &result, WithUseNumber()); err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("len = %d", len(result))
	}
	expectations := []string{"1", "2.5", "9007199254740993"}
	for i, want := range expectations {
		n, ok := result[i].(json.Number)
		if !ok {
			t.Fatalf("result[%d] is %T, want json.Number", i, result[i])
		}
		if string(n) != want {
			t.Fatalf("result[%d] = %q, want %q", i, n, want)
		}
	}
}

// --- UseNumber + Decoder ---

func TestNumber_UseNumber_DecoderMethod_MultiValue(t *testing.T) {
	input := `{"val":9007199254740993}` + "\n" + `{"val":42}`
	dec := NewDecoder(strings.NewReader(input))
	dec.UseNumber()

	var msg1 map[string]any
	if err := dec.Decode(&msg1); err != nil {
		t.Fatal(err)
	}
	n, ok := msg1["val"].(json.Number)
	if !ok {
		t.Fatalf("val is %T, want json.Number", msg1["val"])
	}
	if string(n) != "9007199254740993" {
		t.Fatalf("val = %q", n)
	}

	var msg2 map[string]any
	if err := dec.Decode(&msg2); err != nil {
		t.Fatal(err)
	}
	n2, ok := msg2["val"].(json.Number)
	if !ok {
		t.Fatalf("val is %T, want json.Number", msg2["val"])
	}
	if string(n2) != "42" {
		t.Fatalf("val = %q", n2)
	}
}

func TestNumber_UseNumber_DecoderMethod(t *testing.T) {
	input := `{"val":9007199254740993}`
	dec := NewDecoder(strings.NewReader(input))
	dec.UseNumber()

	var msg map[string]any
	if err := dec.Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if _, ok := msg["val"].(json.Number); !ok {
		t.Fatalf("val is %T, want json.Number", msg["val"])
	}
}

func TestNumber_UseNumber_DecoderMethod_AfterFirstDecode(t *testing.T) {
	input := `{"val":1}` + "\n" + `{"val":9007199254740993}`
	dec := NewDecoder(strings.NewReader(input))

	var msg1 map[string]any
	if err := dec.Decode(&msg1); err != nil {
		t.Fatal(err)
	}
	if _, ok := msg1["val"].(float64); !ok {
		t.Fatalf("first val is %T, want float64", msg1["val"])
	}

	dec.UseNumber()

	var msg2 map[string]any
	if err := dec.Decode(&msg2); err != nil {
		t.Fatal(err)
	}
	n2, ok := msg2["val"].(json.Number)
	if !ok {
		t.Fatalf("second val is %T, want json.Number", msg2["val"])
	}
	if string(n2) != "9007199254740993" {
		t.Fatalf("second val = %q", n2)
	}
}

// --- Marshal ---

func TestNumber_Marshal_Basic(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	msg := Msg{N: json.Number("12345")}
	got, err := Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"n":12345}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestNumber_Marshal_Empty(t *testing.T) {
	type Msg struct {
		N json.Number `json:"n"`
	}
	msg := Msg{N: json.Number("")}
	got, err := Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"n":0}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestNumber_Marshal_OmitEmpty(t *testing.T) {
	type Msg struct {
		Name string      `json:"name"`
		N    json.Number `json:"n,omitempty"`
	}
	msg := Msg{Name: "test", N: ""}
	got, err := Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"test"}`
	if string(got) != want {
		t.Fatalf("omitempty: got %s, want %s", got, want)
	}
}

func TestNumber_Marshal_AnyField(t *testing.T) {
	// json.Number stored in an any field
	type Msg struct {
		Data any `json:"data"`
	}
	msg := Msg{Data: json.Number("99.99")}
	got, err := Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data":99.99}`
	if string(got) != want {
		t.Fatalf("any marshal: got %s, want %s", got, want)
	}
}

// --- Round-trip ---

func TestNumber_RoundTrip(t *testing.T) {
	type Msg struct {
		ID    json.Number `json:"id"`
		Score json.Number `json:"score"`
	}
	original := []byte(`{"id":9007199254740993,"score":3.14}`)
	var msg Msg
	if err := Unmarshal(original, &msg); err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("round-trip:\ngot:  %s\nwant: %s", got, original)
	}
}

func TestNumber_RoundTrip_UseNumber(t *testing.T) {
	// UseNumber unmarshal into interface{}, then marshal back
	original := []byte(`{"big":9007199254740993}`)
	var result map[string]any
	if err := Unmarshal(original, &result, WithUseNumber()); err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(&result)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("round-trip UseNumber:\ngot:  %s\nwant: %s", got, original)
	}
}

// --- Large integer precision ---

func TestNumber_LargeInteger_Preservation(t *testing.T) {
	// 2^53 + 1 = 9007199254740993, cannot be represented exactly as float64
	type Msg struct {
		N json.Number `json:"n"`
	}
	input := []byte(`{"n":9007199254740993}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.N) != "9007199254740993" {
		t.Fatalf("large int lost: N = %q", msg.N)
	}
	// Verify Int64() works
	v, err := msg.N.Int64()
	if err != nil {
		t.Fatal(err)
	}
	if v != 9007199254740993 {
		t.Fatalf("Int64() = %d, want 9007199254740993", v)
	}
}

// --- json.Number in slice ---

func TestNumber_InSlice(t *testing.T) {
	type Msg struct {
		Nums []json.Number `json:"nums"`
	}
	input := []byte(`{"nums":[1,2.5,9007199254740993]}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Nums) != 3 {
		t.Fatalf("len = %d", len(msg.Nums))
	}
	expectations := []string{"1", "2.5", "9007199254740993"}
	for i, want := range expectations {
		if string(msg.Nums[i]) != want {
			t.Errorf("Nums[%d] = %q, want %q", i, msg.Nums[i], want)
		}
	}
}

// --- json.Number pointer ---

func TestNumber_Pointer(t *testing.T) {
	type Msg struct {
		N *json.Number `json:"n"`
	}
	input := []byte(`{"n":42}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.N == nil {
		t.Fatal("N is nil")
	}
	if string(*msg.N) != "42" {
		t.Fatalf("N = %q", *msg.N)
	}

	// Null pointer
	input2 := []byte(`{"n":null}`)
	var msg2 Msg
	if err := Unmarshal(input2, &msg2); err != nil {
		t.Fatal(err)
	}
	if msg2.N != nil {
		t.Fatalf("expected nil N, got %q", *msg2.N)
	}
}

// --- Top-level json.Number ---

func TestNumber_TopLevel(t *testing.T) {
	input := []byte(`42`)
	var n json.Number
	if err := Unmarshal(input, &n); err != nil {
		t.Fatal(err)
	}
	if string(n) != "42" {
		t.Fatalf("top-level: n = %q", n)
	}
}

// --- encoding/json compatibility ---

func TestNumber_StdlibCompat(t *testing.T) {
	type Msg struct {
		A json.Number `json:"a"`
		B json.Number `json:"b"`
		C json.Number `json:"c"`
	}

	inputs := []string{
		`{"a":1,"b":2.5,"c":-3}`,
		`{"a":0,"b":1e5,"c":9007199254740993}`,
	}

	for _, input := range inputs {
		data := []byte(input)

		var vMsg Msg
		if err := Unmarshal(data, &vMsg); err != nil {
			t.Fatalf("vjson unmarshal %q: %v", input, err)
		}

		var sMsg Msg
		if err := json.Unmarshal(data, &sMsg); err != nil {
			t.Fatalf("stdlib unmarshal %q: %v", input, err)
		}

		if string(vMsg.A) != string(sMsg.A) || string(vMsg.B) != string(sMsg.B) || string(vMsg.C) != string(sMsg.C) {
			t.Errorf("unmarshal mismatch for %q:\nvjson:  %+v\nstdlib: %+v", input, vMsg, sMsg)
		}

		vOut, err := Marshal(&vMsg)
		if err != nil {
			t.Fatalf("vjson marshal: %v", err)
		}
		sOut, err := json.Marshal(&sMsg)
		if err != nil {
			t.Fatalf("stdlib marshal: %v", err)
		}
		if !bytes.Equal(vOut, sOut) {
			t.Errorf("marshal mismatch for %q:\nvjson:  %s\nstdlib: %s", input, vOut, sOut)
		}
	}
}

// --- UseNumber: non-number values still correct ---

func TestNumber_UseNumber_NonNumbers(t *testing.T) {
	input := []byte(`{"s":"hello","b":true,"n":null,"a":[1]}`)
	var result map[string]any
	if err := Unmarshal(input, &result, WithUseNumber()); err != nil {
		t.Fatal(err)
	}
	if s, ok := result["s"].(string); !ok || s != "hello" {
		t.Fatalf("s = %v (%T)", result["s"], result["s"])
	}
	if b, ok := result["b"].(bool); !ok || b != true {
		t.Fatalf("b = %v (%T)", result["b"], result["b"])
	}
	if result["n"] != nil {
		t.Fatalf("n = %v, want nil", result["n"])
	}
	arr, ok := result["a"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("a = %v (%T)", result["a"], result["a"])
	}
	// Array element should also be json.Number
	if _, ok := arr[0].(json.Number); !ok {
		t.Fatalf("a[0] is %T, want json.Number", arr[0])
	}
}
