package tests

import (
	"bytes"
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
)

// Unmarshal: various JSON value types in a RawMessage field

func TestRawMessage_UnmarshalObject(t *testing.T) {
	type Msg struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	input := []byte(`{"type":"event","payload":{"id":1,"name":"test"}}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "event" {
		t.Fatalf("Type = %q, want %q", msg.Type, "event")
	}
	want := `{"id":1,"name":"test"}`
	if string(msg.Payload) != want {
		t.Fatalf("Payload = %s, want %s", msg.Payload, want)
	}
}

func TestRawMessage_UnmarshalArray(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":[1,2,"three",null,true]}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	want := `[1,2,"three",null,true]`
	if string(msg.Data) != want {
		t.Fatalf("Data = %s, want %s", msg.Data, want)
	}
}

func TestRawMessage_UnmarshalString(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":"hello world"}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	want := `"hello world"`
	if string(msg.Data) != want {
		t.Fatalf("Data = %s, want %s", msg.Data, want)
	}
}

func TestRawMessage_UnmarshalNumber(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":42.5}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	want := `42.5`
	if string(msg.Data) != want {
		t.Fatalf("Data = %s, want %s", msg.Data, want)
	}
}

func TestRawMessage_UnmarshalBoolNull(t *testing.T) {
	type Msg struct {
		A json.RawMessage `json:"a"`
		B json.RawMessage `json:"b"`
		C json.RawMessage `json:"c"`
	}
	input := []byte(`{"a":true,"b":false,"c":null}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if string(msg.A) != "true" {
		t.Fatalf("A = %s, want true", msg.A)
	}
	if string(msg.B) != "false" {
		t.Fatalf("B = %s, want false", msg.B)
	}
	if string(msg.C) != "null" {
		t.Fatalf("C = %s, want null", msg.C)
	}
}

func TestRawMessage_UnmarshalNested(t *testing.T) {
	type Msg struct {
		ID      int             `json:"id"`
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
		Score   float64         `json:"score"`
	}
	input := []byte(`{"id":42,"name":"test","payload":{"nested":true},"score":9.5}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.ID != 42 || msg.Name != "test" || msg.Score != 9.5 {
		t.Fatalf("fields mismatch: %+v", msg)
	}
	if string(msg.Payload) != `{"nested":true}` {
		t.Fatalf("Payload = %s", msg.Payload)
	}
}

// Marshal round-trip

func TestRawMessage_MarshalRoundTrip(t *testing.T) {
	type Msg struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	original := []byte(`{"type":"event","payload":{"id":1,"items":[1,2,3]}}`)
	var msg Msg
	if err := vjson.Unmarshal(original, &msg); err != nil {
		t.Fatal(err)
	}
	got, err := vjson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("round-trip:\ngot:  %s\nwant: %s", got, original)
	}
}

// Marshal nil / empty RawMessage

func TestRawMessage_MarshalNil(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	msg := Msg{Data: nil}
	got, err := vjson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data":null}`
	if string(got) != want {
		t.Fatalf("nil RawMessage: got %s, want %s", got, want)
	}
}

func TestRawMessage_MarshalEmpty(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	msg := Msg{Data: json.RawMessage{}}
	got, err := vjson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Empty slice (len==0) should produce "null", same as encoding/json
	want := `{"data":null}`
	if string(got) != want {
		t.Fatalf("empty RawMessage: got %s, want %s", got, want)
	}
}

// Top-level RawMessage

func TestRawMessage_TopLevelUnmarshal(t *testing.T) {
	input := []byte(`{"key":"value","n":123}`)
	var raw json.RawMessage
	if err := vjson.Unmarshal(input, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(input) {
		t.Fatalf("top-level: got %s, want %s", raw, input)
	}
}

func TestRawMessage_TopLevelMarshal(t *testing.T) {
	raw := json.RawMessage(`[1,2,3]`)
	got, err := vjson.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,2,3]` {
		t.Fatalf("top-level marshal: got %s", got)
	}
}

// RawMessage in slice

func TestRawMessage_InSlice(t *testing.T) {
	type Msg struct {
		Items []json.RawMessage `json:"items"`
	}
	input := []byte(`{"items":[{"a":1},{"b":2},"hello",42,null]}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Items) != 5 {
		t.Fatalf("len(Items) = %d, want 5", len(msg.Items))
	}
	expectations := []string{`{"a":1}`, `{"b":2}`, `"hello"`, `42`, `null`}
	for i, want := range expectations {
		if string(msg.Items[i]) != want {
			t.Errorf("Items[%d] = %s, want %s", i, msg.Items[i], want)
		}
	}

	// Marshal round-trip
	got, err := vjson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(input) {
		t.Fatalf("slice round-trip:\ngot:  %s\nwant: %s", got, input)
	}
}

// RawMessage pointer

func TestRawMessage_Pointer(t *testing.T) {
	type Msg struct {
		Data *json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"key":"val"}}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Data == nil {
		t.Fatal("Data is nil")
	}
	if string(*msg.Data) != `{"key":"val"}` {
		t.Fatalf("Data = %s", *msg.Data)
	}

	// Nil pointer
	input2 := []byte(`{"data":null}`)
	var msg2 Msg
	if err := vjson.Unmarshal(input2, &msg2); err != nil {
		t.Fatal(err)
	}
	if msg2.Data != nil {
		t.Fatalf("expected nil Data, got %s", *msg2.Data)
	}
}

// RawMessage with omitempty

func TestRawMessage_OmitEmpty(t *testing.T) {
	type Msg struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data,omitempty"`
	}
	msg := Msg{Name: "test", Data: nil}
	got, err := vjson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"test"}`
	if string(got) != want {
		t.Fatalf("omitempty: got %s, want %s", got, want)
	}
}

// Byte independence: source mutation must not affect result

func TestRawMessage_ByteIndependence(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"key":"value"}}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	saved := string(msg.Data) // snapshot

	// Mutate the source buffer
	for i := range input {
		input[i] = 'X'
	}

	// The RawMessage should be unaffected (copy semantics)
	if string(msg.Data) != saved {
		t.Fatalf("mutation affected RawMessage: got %s, want %s", msg.Data, saved)
	}
}

// encoding/json compatibility

func TestRawMessage_StdlibCompat(t *testing.T) {
	type Msg struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
		Count   int             `json:"count"`
	}

	inputs := []string{
		`{"type":"a","payload":{"nested":{"deep":true}},"count":1}`,
		`{"type":"b","payload":[1,"two",null,false],"count":2}`,
		`{"type":"c","payload":"just a string","count":3}`,
		`{"type":"d","payload":42,"count":4}`,
		`{"type":"e","payload":null,"count":5}`,
		`{"type":"f","payload":true,"count":6}`,
	}

	for _, input := range inputs {
		data := []byte(input)

		// Unmarshal with vjson
		var vMsg Msg
		if err := vjson.Unmarshal(data, &vMsg); err != nil {
			t.Fatalf("vjson unmarshal %q: %v", input, err)
		}

		// Unmarshal with encoding/json
		var sMsg Msg
		if err := json.Unmarshal(data, &sMsg); err != nil {
			t.Fatalf("stdlib unmarshal %q: %v", input, err)
		}

		// Compare Payload bytes
		if !bytes.Equal(vMsg.Payload, sMsg.Payload) {
			t.Errorf("payload mismatch for %q:\nvjson:  %s\nstdlib: %s", input, vMsg.Payload, sMsg.Payload)
		}

		// Marshal both and compare
		vOut, err := vjson.Marshal(vMsg)
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

// Deeply nested RawMessage

func TestRawMessage_DeeplyNested(t *testing.T) {
	type Inner struct {
		Extra json.RawMessage `json:"extra"`
	}
	type Outer struct {
		ID    int   `json:"id"`
		Inner Inner `json:"inner"`
	}
	input := []byte(`{"id":1,"inner":{"extra":{"a":{"b":{"c":true}}}}}`)
	var outer Outer
	if err := vjson.Unmarshal(input, &outer); err != nil {
		t.Fatal(err)
	}
	if outer.ID != 1 {
		t.Fatalf("ID = %d", outer.ID)
	}
	want := `{"a":{"b":{"c":true}}}`
	if string(outer.Inner.Extra) != want {
		t.Fatalf("Extra = %s, want %s", outer.Inner.Extra, want)
	}
}

// RawMessage with whitespace in original JSON

func TestRawMessage_PreservesCompactForm(t *testing.T) {
	// Note: skipValue does not preserve whitespace — it captures the
	// exact bytes from the source. If the source has whitespace, the
	// RawMessage will contain whitespace too.
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data": { "key" : "val" } }`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	// The raw bytes should be exactly the span from the source
	want := `{ "key" : "val" }`
	if string(msg.Data) != want {
		t.Fatalf("whitespace: got %q, want %q", msg.Data, want)
	}
}

// RawMessage with escaped strings inside

func TestRawMessage_EscapedStringsInside(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"msg":"hello \"world\"\nnewline"}}`)
	var msg Msg
	if err := vjson.Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	want := `{"msg":"hello \"world\"\nnewline"}`
	if string(msg.Data) != want {
		t.Fatalf("escaped: got %s, want %s", msg.Data, want)
	}
}
