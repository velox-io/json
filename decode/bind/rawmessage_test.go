package bind

import (
	"bytes"
	"encoding/json"
	"testing"
)

// RawMessage diff tests. Compare bind.Unmarshal against encoding/json for
// json.RawMessage fields, slices, maps, pointers, and top-level values. The
// native state machine captures the raw JSON byte span (UnmarshalRecord with
// kind=RAW_MESSAGE), stages it in the unmarshal drain buffer, and the Go drain
// copies src bytes into a fresh []byte written to the target slice-header slot
// via typedmemmove.

func rawMsgEqual(t *testing.T, input []byte, got, want json.RawMessage) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("input %q:\ngot  %s\nwant %s", input, got, want)
	}
}

func TestBindRawMessage_ObjectValue(t *testing.T) {
	type Msg struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	input := []byte(`{"type":"event","payload":{"id":1,"name":"test"}}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got.Payload, want.Payload)
}

func TestBindRawMessage_ArrayValue(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":[1,2,"three",null,true]}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got.Data, want.Data)
}

func TestBindRawMessage_ScalarValues(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	inputs := []string{
		`{"data":"hello world"}`,
		`{"data":42.5}`,
		`{"data":true}`,
		`{"data":false}`,
		`{"data":null}`,
		`{"data":42}`,
	}
	for _, in := range inputs {
		input := []byte(in)
		var got Msg
		if err := Unmarshal(input, &got); err != nil {
			t.Fatalf("bind.Unmarshal %q: %v", in, err)
		}
		var want Msg
		_ = json.Unmarshal(input, &want)
		rawMsgEqual(t, input, got.Data, want.Data)
	}
}

func TestBindRawMessage_NestedStruct(t *testing.T) {
	type Msg struct {
		ID      int             `json:"id"`
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
		Score   float64         `json:"score"`
	}
	input := []byte(`{"id":42,"name":"test","payload":{"nested":true},"score":9.5}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	if got.ID != want.ID || got.Name != want.Name || got.Score != want.Score {
		t.Fatalf("scalar fields mismatch: got %+v want %+v", got, want)
	}
	rawMsgEqual(t, input, got.Payload, want.Payload)
}

func TestBindRawMessage_TopLevel(t *testing.T) {
	input := []byte(`{"key":"value","n":123}`)
	var got json.RawMessage
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want json.RawMessage
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got, want)
}

func TestBindRawMessage_TopLevelScalar(t *testing.T) {
	inputs := []string{`42`, `3.14`, `true`, `false`, `null`, `"hello"`, `[1,2,3]`}
	for _, in := range inputs {
		input := []byte(in)
		var got json.RawMessage
		if err := Unmarshal(input, &got); err != nil {
			t.Fatalf("bind.Unmarshal %q: %v", in, err)
		}
		var want json.RawMessage
		_ = json.Unmarshal(input, &want)
		rawMsgEqual(t, input, got, want)
	}
}

func TestBindRawMessage_InSlice(t *testing.T) {
	type Msg struct {
		Items []json.RawMessage `json:"items"`
	}
	input := []byte(`{"items":[{"a":1},{"b":2},"hello",42,null]}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	if len(got.Items) != len(want.Items) {
		t.Fatalf("len mismatch: got %d want %d", len(got.Items), len(want.Items))
	}
	for i := range got.Items {
		rawMsgEqual(t, input, got.Items[i], want.Items[i])
	}
}

func TestBindRawMessage_Pointer(t *testing.T) {
	type Msg struct {
		Data *json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"key":"val"}}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	if got.Data == nil {
		t.Fatal("Data is nil")
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, *got.Data, *want.Data)

	// Null pointer case.
	input2 := []byte(`{"data":null}`)
	var got2 Msg
	if err := Unmarshal(input2, &got2); err != nil {
		t.Fatal(err)
	}
	if got2.Data != nil {
		t.Fatalf("expected nil Data, got %s", *got2.Data)
	}
}

func TestBindRawMessage_InMap(t *testing.T) {
	input := []byte(`{"a":{"x":1},"b":[1,2],"c":"hi","d":42,"e":null}`)
	var got map[string]json.RawMessage
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want map[string]json.RawMessage
	_ = json.Unmarshal(input, &want)
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d", len(got), len(want))
	}
	for k := range want {
		rawMsgEqual(t, input, got[k], want[k])
	}
}

func TestBindRawMessage_ByteIndependence(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"key":"value"}}`)
	var msg Msg
	if err := Unmarshal(input, &msg); err != nil {
		t.Fatal(err)
	}
	saved := append([]byte(nil), msg.Data...)

	for i := range input {
		input[i] = 'X'
	}
	if !bytes.Equal(msg.Data, saved) {
		t.Fatalf("mutation affected RawMessage: got %s, want %s", msg.Data, saved)
	}
}

func TestBindRawMessage_WhitespacePreserved(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data": { "key" : "val" } }`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got.Data, want.Data)
}

func TestBindRawMessage_DeeplyNested(t *testing.T) {
	type Inner struct {
		Extra json.RawMessage `json:"extra"`
	}
	type Outer struct {
		ID    int   `json:"id"`
		Inner Inner `json:"inner"`
	}
	input := []byte(`{"id":1,"inner":{"extra":{"a":{"b":{"c":true}}}}}`)
	var got Outer
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Outer
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got.Inner.Extra, want.Inner.Extra)
}

func TestBindRawMessage_EscapedStringsInside(t *testing.T) {
	type Msg struct {
		Data json.RawMessage `json:"data"`
	}
	input := []byte(`{"data":{"msg":"hello \"world\"\nnewline"}}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	rawMsgEqual(t, input, got.Data, want.Data)
}

func TestBindRawMessage_FlushDrain(t *testing.T) {
	// Many RawMessage values to exercise the FLUSH_UNMARSHAL drain path
	// (deferred_drain fills up and is drained mid-parse).
	type Msg struct {
		Items []json.RawMessage `json:"items"`
	}
	// Build a JSON array of 200 objects so the unmarshal drain buffer
	// fills multiple times.
	var rawItems []byte
	rawItems = append(rawItems, '[')
	for i := 0; i < 200; i++ {
		if i > 0 {
			rawItems = append(rawItems, ',')
		}
		rawItems = append(rawItems, `{"i":`...)
		rawItems = append(rawItems, '0'+byte(i%10))
		rawItems = append(rawItems, '}')
	}
	rawItems = append(rawItems, ']')
	input := append([]byte(`{"items":`), rawItems...)
	input = append(input, '}')

	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	var want Msg
	_ = json.Unmarshal(input, &want)
	if len(got.Items) != len(want.Items) {
		t.Fatalf("len mismatch: got %d want %d", len(got.Items), len(want.Items))
	}
	for i := range got.Items {
		if !bytes.Equal(got.Items[i], want.Items[i]) {
			t.Errorf("Items[%d]: got %s, want %s", i, got.Items[i], want.Items[i])
			break
		}
	}
}

// RawMessage decoding reuses the destination's existing capacity, matching
// RawMessage.UnmarshalJSON. Decoding a shorter value over a longer one must
// leave the correct length behind and must not expose the old tail.
func TestBindRawMessage_ReusesCapacity(t *testing.T) {
	type Msg struct {
		D json.RawMessage `json:"d"`
	}
	input := []byte(`{"d":{"n":1}}`)

	got := Msg{D: json.RawMessage(`{"old":"aaaaaaaaaaaaaaaaaaaa"}`)}
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	want := Msg{D: json.RawMessage(`{"old":"aaaaaaaaaaaaaaaaaaaa"}`)}
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.D, want.D) {
		t.Fatalf("got %s, want %s", got.D, want.D)
	}
	if len(got.D) != len(`{"n":1}`) {
		t.Errorf("len = %d, want %d", len(got.D), len(`{"n":1}`))
	}
}

// A decoded RawMessage must own its bytes, so mutating the input afterwards
// must not change it.
func TestBindRawMessage_BytesAreCopied(t *testing.T) {
	type Msg struct {
		D json.RawMessage `json:"d"`
	}
	input := []byte(`{"d":{"a":1}}`)
	var got Msg
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	before := string(got.D)
	for i := range input {
		input[i] = 'X'
	}
	if string(got.D) != before {
		t.Errorf("RawMessage aliased the input: was %s, now %s", before, got.D)
	}
}
